// store_channel_admission.go: HUI-3535 渠道准入持久层(additive)。
//
// 纪律:
//   - 库内表是 docs/contracts/channel-admission/v1/seed.json(vendored 副本
//     go:embed)的读模型;启动装载,迁移保持纯 DDL;
//   - 变更不静默改写:载荷指纹(sha256 of canonical record JSON)未变的重复
//     装载不追加修订行;变了才在同一事务内落 upsert + revision(create|update);
//   - 读取永远租户作用域化:tenant_scope='*' 是平台级真值,对每租户可见;
//     定向行(tenant_scope=<tenant>)在自己租户内优先于 '*'(best-match),
//     多租户/白标绝不会读到别家租户的定向行;
//   - Gate 裁决在 channeladmission 包,存储层零业务判断。
package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
)

// admissionInsertCols is the column list WITHOUT the id (insert & update
// value order); admissionCols prefixes id for scans.
const admissionInsertCols = `platform,entry_mode,scenario,app_binding,tenant_scope,status,` +
	`reward_admitted,reason_code,reason_text,cap_media,cap_title,cap_body,cap_topic,` +
	`cap_poi,cap_receipt,cap_callback,missing,official_sources,last_checked_at,` +
	`valid_until,close_reason,seed_version,payload_hash`

const admissionCols = `id,` + admissionInsertCols + `,updated_at`

func scanAdmission(sc interface{ Scan(...any) error }) (channeladmission.Record, error) {
	var r channeladmission.Record
	var id string
	var reward int
	var missing, sources string
	var seedVersion int
	var payloadHash, updatedAt string
	if err := sc.Scan(&id, &r.Platform, &r.EntryMode, &r.Scenario, &r.AppBinding, &r.TenantScope,
		&r.Status, &reward, &r.ReasonCode, &r.ReasonText,
		&r.Capabilities.Media, &r.Capabilities.Title, &r.Capabilities.Body, &r.Capabilities.Topic,
		&r.Capabilities.POI, &r.Capabilities.Receipt, &r.Capabilities.Callback,
		&missing, &sources, &r.LastCheckedAt, &r.ValidUntil, &r.CloseReason,
		&seedVersion, &payloadHash, &updatedAt); err != nil {
		return channeladmission.Record{}, err
	}
	r.RewardAdmitted = reward == 1
	if err := json.Unmarshal([]byte(missing), &r.Missing); err != nil {
		return channeladmission.Record{}, fmt.Errorf("channel admission %s missing list: %w", r.IdentityKey(), err)
	}
	if err := json.Unmarshal([]byte(sources), &r.OfficialSources); err != nil {
		return channeladmission.Record{}, fmt.Errorf("channel admission %s sources: %w", r.IdentityKey(), err)
	}
	return r, nil
}

// admissionVals flattens a record into SQL values (admissionInsertCols order).
func admissionVals(r channeladmission.Record, payloadHash string, seedVersion int) ([]any, error) {
	missing, err := json.Marshal(r.Missing)
	if err != nil {
		return nil, err
	}
	sources, err := json.Marshal(r.OfficialSources)
	if err != nil {
		return nil, err
	}
	reward := 0
	if r.RewardAdmitted {
		reward = 1
	}
	return []any{r.Platform, r.EntryMode, r.Scenario, r.AppBinding, r.TenantScope, r.Status,
		reward, r.ReasonCode, r.ReasonText,
		r.Capabilities.Media, r.Capabilities.Title, r.Capabilities.Body, r.Capabilities.Topic,
		r.Capabilities.POI, r.Capabilities.Receipt, r.Capabilities.Callback,
		string(missing), string(sources), r.LastCheckedAt, r.ValidUntil, r.CloseReason,
		seedVersion, payloadHash}, nil
}

// admissionPayloadHash fingerprints the effective record so identical seed
// re-loads are no-ops. Records come from the validated seed, so marshalling
// cannot fail in practice; the fallback keeps a pathological record visible
// in the audit trail instead of silently matching a previous hash.
func admissionPayloadHash(r channeladmission.Record) string {
	b, err := json.Marshal(r)
	if err != nil {
		return fmt.Sprintf("unmarshalable:%s", r.IdentityKey())
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// LoadAdmissionSeed upserts every seed record. Returns the number of records
// that actually changed (created or updated). Unchanged re-loads write
// nothing — idempotent startup.
func (s *Store) LoadAdmissionSeed(seed *channeladmission.Seed) (changed int, err error) {
	if seed == nil {
		return 0, errors.New("nil seed")
	}
	for _, r := range seed.Records {
		c, err := s.upsertAdmission(r, seed.Version)
		if err != nil {
			return changed, err
		}
		changed += c
	}
	if changed > 0 {
		s.bumpEpoch()
	}
	return changed, nil
}

func (s *Store) upsertAdmission(r channeladmission.Record, seedVersion int) (int, error) {
	hash := admissionPayloadHash(r)
	vals, err := admissionVals(r, hash, seedVersion)
	if err != nil {
		return 0, err
	}
	// id + admissionInsertCols(vals) + updated_at = len(vals)+2 slots.
	placeholders := "?" + strings.Repeat(",?", len(vals)+1)

	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var curID string
	var curHash string
	err = tx.QueryRow(`SELECT id,payload_hash FROM channel_admissions
		WHERE platform=? AND entry_mode=? AND scenario=? AND tenant_scope=?`,
		r.Platform, r.EntryMode, r.Scenario, r.TenantScope).Scan(&curID, &curHash)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err := tx.Exec(
			`INSERT INTO channel_admissions(id,`+admissionInsertCols+`,updated_at)
			VALUES(`+placeholders+`)`,
			append(append([]any{newID("cha_")}, vals...), now())...)
		if err != nil {
			return 0, err
		}
		if err := insertAdmissionRevision(tx, r, seedVersion, "create", hash); err != nil {
			return 0, err
		}
		if err := tx.Commit(); err != nil {
			return 0, err
		}
		return 1, nil
	case err != nil:
		return 0, err
	}
	if curHash == hash {
		return 0, nil // unchanged re-load: no write, no revision
	}
	_, err = tx.Exec(`UPDATE channel_admissions SET app_binding=?,tenant_scope=?,status=?,
		reward_admitted=?,reason_code=?,reason_text=?,cap_media=?,cap_title=?,cap_body=?,
		cap_topic=?,cap_poi=?,cap_receipt=?,cap_callback=?,missing=?,official_sources=?,
		last_checked_at=?,valid_until=?,close_reason=?,seed_version=?,payload_hash=?,updated_at=?
		WHERE id=?`,
		vals[3], vals[4], vals[5], vals[6], vals[7], vals[8], vals[9], vals[10], vals[11],
		vals[12], vals[13], vals[14], vals[15], vals[16], vals[17], vals[18], vals[19],
		vals[20], vals[21], vals[22], now(), curID)
	if err != nil {
		return 0, err
	}
	if err := insertAdmissionRevision(tx, r, seedVersion, "update", hash); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return 1, nil
}

func insertAdmissionRevision(tx *sql.Tx, r channeladmission.Record, seedVersion int, action, hash string) error {
	_, err := tx.Exec(
		`INSERT INTO channel_admission_revisions(id,platform,entry_mode,scenario,tenant_scope,
		seed_version,action,status,payload_hash,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		newID("char_"), r.Platform, r.EntryMode, r.Scenario, r.TenantScope,
		seedVersion, action, r.Status, hash, now())
	return err
}

// GetChannelAdmission resolves one route for a tenant with best-match scope:
// a tenant-specific row wins over the platform "*" row. Cross-tenant scoped
// rows are invisible (never leak another merchant's admission).
func (s *Store) GetChannelAdmission(platform, entryMode, scenario, tenantID string) (channeladmission.Record, error) {
	r, err := scanAdmission(s.DB.QueryRow(
		`SELECT `+admissionCols+` FROM channel_admissions
		WHERE platform=? AND entry_mode=? AND scenario=? AND (tenant_scope=? OR tenant_scope='*')
		ORDER BY CASE WHEN tenant_scope=? THEN 0 ELSE 1 END LIMIT 1`,
		platform, entryMode, scenario, tenantID, tenantID))
	if errors.Is(err, sql.ErrNoRows) {
		return channeladmission.Record{}, ErrNotFound
	}
	return r, err
}

// ListChannelAdmissions returns every route visible to a tenant: the
// platform-level truth plus the tenant's own scoped rows. Merchant surface
// (HUI-1676 消费) renders 可选渠道与缺项原因 from this list.
func (s *Store) ListChannelAdmissions(tenantID string) ([]channeladmission.Record, error) {
	rows, err := s.DB.Query(
		`SELECT `+admissionCols+` FROM channel_admissions
		WHERE tenant_scope='*' OR tenant_scope=? ORDER BY platform,entry_mode,scenario`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]channeladmission.Record, 0)
	for rows.Next() {
		r, err := scanAdmission(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountChannelAdmissionRevisions returns the audit trail size for one route
// (test seam; a revisions read route is deliberately out of v1 scope).
func (s *Store) CountChannelAdmissionRevisions(platform, entryMode, scenario, tenantScope string) (int, error) {
	var n int
	err := s.DB.QueryRow(
		`SELECT COUNT(1) FROM channel_admission_revisions
		WHERE platform=? AND entry_mode=? AND scenario=? AND tenant_scope=?`,
		platform, entryMode, scenario, tenantScope).Scan(&n)
	return n, err
}
