// store_action_capabilities.go: HUI-3535 动作级渠道能力持久层(additive)。
//
// 纪律:
//   - 库内表 channel_action_capabilities 是迁移 0060–0063 落库的种子真值读
//     模型;平台端更新只走受控 upsert(同事务追加 channel_admission_control_log
//     审计:操作者/原因/动作/载荷指纹),无静默改写;
//   - 载荷指纹对齐迁移生成器:同样对 13 个规范字段(canonical JSON,键序一致)
//     做 sha256,使「种子行的首次受控重写内容不变 = no-op,不追加审计」;
//   - 读取永远租户作用域化:tenant_scope='*' 平台真值对每租户可见,定向行只在
//     自己租户内可见;ListActionCapabilitiesAll 是平台运维读(4592 O4 消费面),
//     两者都不泄露他人租户定向行给普通租户;
//   - JSON 列扫描即验证(六环境键/三态/字段白名单),脏行 fail-closed 报错;
//   - 业务判定在 channeladmission 包(JudgeActionRoute),存储层零业务判断。
package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
)

// actionCapInsertCols is the column list WITHOUT id (insert & update value
// order); actionCapCols prefixes id for scans. Order mirrors migration 0060.
const actionCapInsertCols = `catalog_code,platform,method,scenario,action_kind,label,` +
	`suggested_only,app_binding,tenant_scope,status,environments,field_support,` +
	`required_inputs,evidence_grade,official_sources,checked_at,valid_until,` +
	`emergency_disabled,close_reason,missing,reason_code,reason_text,seed_version,payload_hash`

const actionCapCols = `id,` + actionCapInsertCols + `,updated_at`

func scanActionCapability(sc interface{ Scan(...any) error }) (channeladmission.ActionCapability, error) {
	var r channeladmission.ActionCapability
	var id string
	var suggested, emergency int
	var envs, fieldSupport, requiredInputs, sources, missing string
	var seedVersion int
	var payloadHash, updatedAt string
	if err := sc.Scan(&id, &r.CatalogCode, &r.Platform, &r.Method, &r.Scenario, &r.ActionKind,
		&r.Label, &suggested, &r.AppBinding, &r.TenantScope, &r.Status,
		&envs, &fieldSupport, &requiredInputs, &r.EvidenceGrade, &sources,
		&r.CheckedAt, &r.ValidUntil, &emergency, &r.CloseReason,
		&missing, &r.ReasonCode, &r.ReasonText, &seedVersion, &payloadHash, &updatedAt); err != nil {
		return channeladmission.ActionCapability{}, err
	}
	r.ID = id
	r.SuggestedOnly = suggested == 1
	r.EmergencyDisabled = emergency == 1
	r.SeedVersion = seedVersion
	// JSON 列扫描即验证:脏行在这里失败,绝不带病进入判定层。
	if err := json.Unmarshal([]byte(envs), &r.Environments); err != nil {
		return channeladmission.ActionCapability{}, fmt.Errorf("action capability %s environments: %w", r.IdentityKey(), err)
	}
	if err := json.Unmarshal([]byte(fieldSupport), &r.FieldSupport); err != nil {
		return channeladmission.ActionCapability{}, fmt.Errorf("action capability %s field_support: %w", r.IdentityKey(), err)
	}
	if err := json.Unmarshal([]byte(requiredInputs), &r.RequiredInputs); err != nil {
		return channeladmission.ActionCapability{}, fmt.Errorf("action capability %s required_inputs: %w", r.IdentityKey(), err)
	}
	if err := json.Unmarshal([]byte(missing), &r.Missing); err != nil {
		return channeladmission.ActionCapability{}, fmt.Errorf("action capability %s missing: %w", r.IdentityKey(), err)
	}
	if err := json.Unmarshal([]byte(sources), &r.OfficialSources); err != nil {
		return channeladmission.ActionCapability{}, fmt.Errorf("action capability %s sources: %w", r.IdentityKey(), err)
	}
	if err := r.Validate(); err != nil {
		return channeladmission.ActionCapability{}, err
	}
	return r, nil
}

func actionCapVals(r channeladmission.ActionCapability, payloadHash string) ([]any, error) {
	envs, err := json.Marshal(r.Environments)
	if err != nil {
		return nil, err
	}
	fs, err := json.Marshal(r.FieldSupport)
	if err != nil {
		return nil, err
	}
	ri, err := json.Marshal(r.RequiredInputs)
	if err != nil {
		return nil, err
	}
	missing, err := json.Marshal(r.Missing)
	if err != nil {
		return nil, err
	}
	sources, err := json.Marshal(r.OfficialSources)
	if err != nil {
		return nil, err
	}
	sugg, emer := 0, 0
	if r.SuggestedOnly {
		sugg = 1
	}
	if r.EmergencyDisabled {
		emer = 1
	}
	return []any{r.CatalogCode, r.Platform, r.Method, r.Scenario, r.ActionKind, r.Label,
		sugg, r.AppBinding, r.TenantScope, r.Status, string(envs), string(fs), string(ri),
		r.EvidenceGrade, string(sources), r.CheckedAt, r.ValidUntil, emer, r.CloseReason,
		string(missing), r.ReasonCode, r.ReasonText, r.SeedVersion, payloadHash}, nil
}

// marshalCanonical encodes v the way the migration seed generator did:
// compact JSON, sorted map keys, raw UTF-8, no HTML escaping.
func marshalCanonical(v any) ([]byte, error) {
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	// Encoder appends a trailing newline; the generator wrote none.
	return []byte(strings.TrimSuffix(buf.String(), "\n")), nil
}

// ActionCapabilityPayloadHash fingerprints the 13 canonical capability fields
// with the same canonical-JSON rule as the migration seed generator, so a
// controlled rewrite of an unchanged seed row is a no-op (no audit row).
func ActionCapabilityPayloadHash(r channeladmission.ActionCapability) string {
	// environments 的内层用纯 map 编码(键按字母序),与生成器的 sort_keys=True
	// 完全一致;结构体字段序与字母序不同,不能直接 marshal。
	envs := make(map[string]map[string]string, len(r.Environments))
	for e, c := range r.Environments {
		envs[e] = map[string]string{
			"navigation": c.Navigation, "media_handoff": c.MediaHandoff,
			"platform_receipt": c.PlatformReceipt, "reward_eligibility": c.RewardEligibility,
		}
	}
	payload := map[string]any{
		"catalog_code": r.CatalogCode, "platform": r.Platform, "method": r.Method,
		"scenario": r.Scenario, "app_binding": r.AppBinding, "tenant_scope": r.TenantScope,
		"status": r.Status, "environments": envs, "field_support": r.FieldSupport,
		"required_inputs": r.RequiredInputs, "evidence_grade": r.EvidenceGrade,
		"checked_at": r.CheckedAt, "valid_until": r.ValidUntil,
	}
	b, err := marshalCanonical(payload)
	if err != nil {
		return fmt.Sprintf("unmarshalable:%s", r.IdentityKey())
	}
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum)
}

// ControlAudit is the mandatory trail for every controlled write (票面 F20:
// 来源/操作者/原因). Action ∈ create|update|emergency_disable|emergency_enable.
type ControlAudit struct {
	Actor  string
	Reason string
	Action string
}

func validAuditAction(a string) bool {
	switch a {
	case "create", "update", "emergency_disable", "emergency_enable":
		return true
	}
	return false
}

// ActionCapabilityAuditRow is one immutable controlled-change trail entry.
type ActionCapabilityAuditRow struct {
	ID           string
	CapabilityID string
	CatalogCode  string
	Platform     string
	Method       string
	Scenario     string
	TenantScope  string
	Action       string
	Actor        string
	Reason       string
	PayloadHash  string
	CreatedAt    string
}

const auditCols = `id,capability_id,catalog_code,platform,method,scenario,tenant_scope,` +
	`action,actor,reason,payload_hash,created_at`

func scanAuditRow(sc interface{ Scan(...any) error }) (ActionCapabilityAuditRow, error) {
	var r ActionCapabilityAuditRow
	err := sc.Scan(&r.ID, &r.CapabilityID, &r.CatalogCode, &r.Platform, &r.Method,
		&r.Scenario, &r.TenantScope, &r.Action, &r.Actor, &r.Reason, &r.PayloadHash, &r.CreatedAt)
	return r, err
}

func (s *Store) insertAudit(tx *sql.Tx, r channeladmission.ActionCapability, audit ControlAudit, hash string) error {
	if audit.Actor == "" {
		return errors.New("control audit requires an actor")
	}
	if audit.Reason == "" {
		return errors.New("control audit requires a reason")
	}
	if !validAuditAction(audit.Action) {
		return fmt.Errorf("control audit action %q unknown", audit.Action)
	}
	_, err := tx.Exec(
		`INSERT INTO channel_admission_control_log(id,capability_id,catalog_code,platform,
		method,scenario,tenant_scope,action,actor,reason,payload_hash,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		newID("cac_"), r.ID, r.CatalogCode, r.Platform, r.Method, r.Scenario, r.TenantScope,
		audit.Action, audit.Actor, audit.Reason, hash, now())
	return err
}

// UpsertActionCapability is the controlled write path: validate → hash-diff →
// insert/update + audit row in ONE transaction. An unchanged re-write is a
// no-op (no row change, no audit). The caller declares intent via
// audit.Action (create|update); storage refuses to dress up a conflict.
func (s *Store) UpsertActionCapability(r channeladmission.ActionCapability, audit ControlAudit) (created bool, err error) {
	if err := r.Validate(); err != nil {
		return false, err
	}
	if !validAuditAction(audit.Action) {
		return false, fmt.Errorf("control audit action %q unknown", audit.Action)
	}
	hash := ActionCapabilityPayloadHash(r)
	vals, err := actionCapVals(r, hash)
	if err != nil {
		return false, err
	}
	placeholders := "?" + strings.Repeat(",?", len(vals)+1)

	tx, err := s.DB.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	var curID string
	var curHash string
	err = tx.QueryRow(`SELECT id,payload_hash FROM channel_action_capabilities
		WHERE catalog_code=? AND platform=? AND method=? AND scenario=? AND tenant_scope=?`,
		r.CatalogCode, r.Platform, r.Method, r.Scenario, r.TenantScope).Scan(&curID, &curHash)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if r.ID == "" {
			r.ID = newID("cap_")
		}
		_, err := tx.Exec(
			`INSERT INTO channel_action_capabilities(id,`+actionCapInsertCols+`,updated_at)
			VALUES(`+placeholders+`)`,
			append(append([]any{r.ID}, vals...), now())...)
		if err != nil {
			return false, err
		}
		if err := s.insertAudit(tx, r, audit, hash); err != nil {
			return false, err
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return true, nil
	case err != nil:
		return false, err
	}
	if curHash == hash {
		return false, nil // unchanged controlled re-write: no write, no audit
	}
	r.ID = curID
	_, err = tx.Exec(`UPDATE channel_action_capabilities SET catalog_code=?,platform=?,
		method=?,scenario=?,action_kind=?,label=?,suggested_only=?,app_binding=?,
		tenant_scope=?,status=?,environments=?,field_support=?,required_inputs=?,
		evidence_grade=?,official_sources=?,checked_at=?,valid_until=?,emergency_disabled=?,
		close_reason=?,missing=?,reason_code=?,reason_text=?,seed_version=?,payload_hash=?,
		updated_at=? WHERE id=?`,
		vals[0], vals[1], vals[2], vals[3], vals[4], vals[5], vals[6], vals[7],
		vals[8], vals[9], vals[10], vals[11], vals[12], vals[13], vals[14], vals[15],
		vals[16], vals[17], vals[18], vals[19], vals[20], vals[21], vals[22], vals[23],
		now(), curID)
	if err != nil {
		return false, err
	}
	if err := s.insertAudit(tx, r, audit, hash); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return false, nil
}

// SetActionCapabilityEmergency flips the independent emergency veto on one
// record (只停对应方法/动作,不影响其他行) and appends the mandatory audit row in
// the same transaction. Unknown ids answer ErrNotFound and write nothing.
func (s *Store) SetActionCapabilityEmergency(id string, disabled bool, audit ControlAudit) (bool, error) {
	if !validAuditAction(audit.Action) {
		return false, fmt.Errorf("control audit action %q unknown", audit.Action)
	}
	if audit.Actor == "" || audit.Reason == "" {
		return false, errors.New("control audit requires actor and reason")
	}
	action := "emergency_enable"
	if disabled {
		action = "emergency_disable"
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRow(`SELECT `+actionCapCols+` FROM channel_action_capabilities WHERE id=?`, id)
	r, err := scanActionCapability(row)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	emer := 0
	if disabled {
		emer = 1
	}
	res, err := tx.Exec(`UPDATE channel_action_capabilities SET emergency_disabled=?,updated_at=? WHERE id=?`,
		emer, now(), id)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, ErrNotFound
	}
	r.EmergencyDisabled = disabled
	if err := s.insertAudit(tx, r, ControlAudit{Actor: audit.Actor, Reason: audit.Reason, Action: action},
		ActionCapabilityPayloadHash(r)); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// GetActionCapabilitiesByCode returns every visible record for one catalog
// code: platform-level truth ('*') plus the tenant's own scoped rows.
// tenantID="" selects platform truth only (公共面语义:不接受客户端租户参数).
func (s *Store) GetActionCapabilitiesByCode(catalogCode, tenantID string) ([]channeladmission.ActionCapability, error) {
	rows, err := s.DB.Query(
		`SELECT `+actionCapCols+` FROM channel_action_capabilities
		WHERE catalog_code=? AND (tenant_scope='*' OR tenant_scope=?)
		ORDER BY method, scenario, platform`, catalogCode, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectActionCapabilities(rows)
}

// ListActionCapabilities returns every record visible to a tenant ('*' plus
// its own scoped rows) — merchant surface (HUI-1676 消费).
func (s *Store) ListActionCapabilities(tenantID string) ([]channeladmission.ActionCapability, error) {
	rows, err := s.DB.Query(
		`SELECT `+actionCapCols+` FROM channel_action_capabilities
		WHERE tenant_scope='*' OR tenant_scope=?
		ORDER BY catalog_code, method, scenario, platform`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectActionCapabilities(rows)
}

// ListActionCapabilitiesAll is the platform-operator read (internal token;
// HUI-4592 O4 渠道健康消费面): every record including other tenants' scoped
// rows. Never exposed to merchant sessions.
func (s *Store) ListActionCapabilitiesAll() ([]channeladmission.ActionCapability, error) {
	rows, err := s.DB.Query(
		`SELECT ` + actionCapCols + ` FROM channel_action_capabilities
		ORDER BY tenant_scope, catalog_code, method, scenario, platform`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectActionCapabilities(rows)
}

func collectActionCapabilities(rows *sql.Rows) ([]channeladmission.ActionCapability, error) {
	out := make([]channeladmission.ActionCapability, 0)
	for rows.Next() {
		r, err := scanActionCapability(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListActionCapabilityAudit returns the controlled-change trail for one
// catalog code (internal read; limit caps the window, 0 = default 50).
func (s *Store) ListActionCapabilityAudit(catalogCode string, limit int) ([]ActionCapabilityAuditRow, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.DB.Query(
		`SELECT `+auditCols+` FROM channel_admission_control_log
		WHERE catalog_code=? ORDER BY created_at, id LIMIT ?`, catalogCode, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ActionCapabilityAuditRow, 0)
	for rows.Next() {
		r, err := scanAuditRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountActionCapabilityAudit returns the trail size for one catalog code
// (test seam + cheap integrity probe).
func (s *Store) CountActionCapabilityAudit(catalogCode string) (int, error) {
	var n int
	err := s.DB.QueryRow(
		`SELECT COUNT(1) FROM channel_admission_control_log WHERE catalog_code=?`,
		catalogCode).Scan(&n)
	return n, err
}
