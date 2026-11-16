// store_campaign_publish_config.go: HUI-1676 增量(商家配置与真实启用)持久层。
//
// 纪律:
//   - 配置挂在既有 campaigns 维度上(UNIQUE campaign_id,行级租户隔离);
//     跨租户一律 ErrNotFound(绝不泄露存在性);
//   - 变更不静默改写:save/enable/pause/resume 每次都在
//     campaign_publish_config_revisions 落一条全量快照修订(同事务),
//     revision 按活动内 COUNT(*)+1 单调递增(SQLite 单写者串行化);
//   - 形状校验复用 campaignenable.ValidateSpec(HTTP 层已校验一次,双重把关);
//     拒绝时不留任何半条记录(修订与配置行同事务回滚);
//   - 保存只更新待发布版本，既有 enabled_version 与暂停状态不变；
//     正式读面只解析原 enable/resume 修订，显式启用才替换线上承诺；
//   - 显式启用校验编辑稿 expected version，再原子更新生效钉版与修订；
//     恢复只使用原已发布修订，不顺带启用暂停期间的新草稿。
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/campaignenable"
)

// PublishConfig stores the editable spec plus the independently published pin.
// GetPublishedConfig resolves the live spec from immutable enable/resume history.
type PublishConfig struct {
	ID             string              `json:"id"`
	TenantID       string              `json:"tenant_id"`
	CampaignID     string              `json:"campaign_id"`
	Version        int                 `json:"version"`
	Status         string              `json:"status"`
	Spec           campaignenable.Spec `json:"spec"`
	EnabledVersion int                 `json:"enabled_version"`
	EnabledAt      string              `json:"enabled_at"`
	PausedAt       string              `json:"paused_at"`
	Disclosure     string              `json:"disclosure"`
	CreatedBy      string              `json:"created_by"`
	CreatedAt      string              `json:"created_at"`
	UpdatedAt      string              `json:"updated_at"`
}

// PublishConfigRevision is one immutable snapshot in the config change trail.
// Spec carries the full configuration at that moment; action tells why the
// revision exists (create|update|enable|pause|resume).
type PublishConfigRevision struct {
	ID         string              `json:"id"`
	TenantID   string              `json:"tenant_id"`
	CampaignID string              `json:"campaign_id"`
	Revision   int                 `json:"revision"`
	Action     string              `json:"action"`
	Version    int                 `json:"version"`
	Spec       campaignenable.Spec `json:"spec"`
	Status     string              `json:"status"`
	ChangedBy  string              `json:"changed_by"`
	CreatedAt  string              `json:"created_at"`
}

type configRow struct {
	cfg      PublishConfig
	channels string
	storeIDs string
}

const publishConfigCols = `id,tenant_id,campaign_id,version,status,verification_mode,customer_copy,` +
	`benefit_kind,benefit_slot,budget_mode,total_quantity,cost_budget_minor,currency,store_ids,` +
	`claim_from,claim_until,redeem_from,redeem_until,onsite_redemption,staff_scope,` +
	`out_of_stock_rule,dedup_basis,per_order_limit,channels,content_version_id,` +
	`enabled_version,enabled_at,paused_at,disclosure,created_by,created_at,updated_at`

func scanPublishConfig(sc interface{ Scan(...any) error }) (configRow, error) {
	var r configRow
	var totalQty, costBudget, enabledVersion sql.NullInt64
	var onsite, perOrder int64
	err := sc.Scan(&r.cfg.ID, &r.cfg.TenantID, &r.cfg.CampaignID, &r.cfg.Version, &r.cfg.Status,
		&r.cfg.Spec.VerificationMode, &r.cfg.Spec.CustomerCopy,
		&r.cfg.Spec.Reward.Kind, &r.cfg.Spec.Reward.Slot, &r.cfg.Spec.Reward.BudgetMode,
		&totalQty, &costBudget, &r.cfg.Spec.Reward.Currency, &r.storeIDs,
		&r.cfg.Spec.Reward.ClaimFrom, &r.cfg.Spec.Reward.ClaimUntil,
		&r.cfg.Spec.Reward.RedeemFrom, &r.cfg.Spec.Reward.RedeemUntil,
		&onsite, &r.cfg.Spec.Reward.StaffScope, &r.cfg.Spec.Reward.OutOfStockRule,
		&r.cfg.Spec.DedupBasis, &perOrder, &r.channels, &r.cfg.Spec.ContentVerID,
		&enabledVersion, &r.cfg.EnabledAt, &r.cfg.PausedAt, &r.cfg.Disclosure,
		&r.cfg.CreatedBy, &r.cfg.CreatedAt, &r.cfg.UpdatedAt)
	if err != nil {
		return configRow{}, err
	}
	if totalQty.Valid {
		n := int(totalQty.Int64)
		r.cfg.Spec.Reward.TotalQuantity = &n
	}
	if costBudget.Valid {
		n := int(costBudget.Int64)
		r.cfg.Spec.Reward.CostBudgetMinor = &n
	}
	if enabledVersion.Valid {
		r.cfg.EnabledVersion = int(enabledVersion.Int64)
	}
	r.cfg.Spec.Reward.OnsiteRedempt = onsite == 1
	r.cfg.Spec.PerOrderLimit = perOrder == 1
	_ = json.Unmarshal([]byte(r.channels), &r.cfg.Spec.Channels)
	_ = json.Unmarshal([]byte(r.storeIDs), &r.cfg.Spec.Reward.StoreIDs)
	return r, nil
}

// configVals flattens the spec into SQL values (insert/update column order).
func configVals(spec campaignenable.Spec) (qty, budget any, onsite, perOrder int, channels, storeIDs string) {
	nullInt := func(p *int) any {
		if p == nil {
			return nil
		}
		return *p
	}
	boolInt := func(b bool) int {
		if b {
			return 1
		}
		return 0
	}
	channelsB, _ := json.Marshal(spec.Channels)
	storeIDsB, _ := json.Marshal(spec.Reward.StoreIDs)
	return nullInt(spec.Reward.TotalQuantity), nullInt(spec.Reward.CostBudgetMinor),
		boolInt(spec.Reward.OnsiteRedempt), boolInt(spec.PerOrderLimit),
		string(channelsB), string(storeIDsB)
}

// specPayload is the revision snapshot body: full spec plus the row state it
// belonged to, so enable/pause revisions are replayable audits.
type specPayload struct {
	Spec           campaignenable.Spec `json:"spec"`
	Status         string              `json:"status"`
	EnabledVersion int                 `json:"enabled_version"`
	Disclosure     string              `json:"disclosure"`
}

func insertConfigRevision(tx *sql.Tx, cfg PublishConfig, revision int, action, changedBy string) error {
	payload, err := json.Marshal(specPayload{Spec: cfg.Spec, Status: cfg.Status, EnabledVersion: cfg.EnabledVersion, Disclosure: cfg.Disclosure})
	if err != nil {
		return err
	}
	_, err = tx.Exec(
		`INSERT INTO campaign_publish_config_revisions(id,tenant_id,campaign_id,revision,action,version,payload,changed_by,created_at)
		 VALUES(?,?,?,?,?,?,?,?,?)`,
		newID("cpcr_"), cfg.TenantID, cfg.CampaignID, revision, action, cfg.Version, string(payload), changedBy, now())
	return err
}

// nextConfigRevision is the revision monotonicity guard inside an open tx.
func nextConfigRevision(tx *sql.Tx, tenantID, campaignID string) (int, error) {
	var n int
	if err := tx.QueryRow(
		`SELECT COUNT(*) FROM campaign_publish_config_revisions WHERE tenant_id=? AND campaign_id=?`,
		tenantID, campaignID).Scan(&n); err != nil {
		return 0, err
	}
	return n + 1, nil
}

// UpsertPublishConfigDraft creates (version 1) or replaces (version+1) the
// configuration draft. Saving never publishes, pauses, or withdraws the live
// version. The original enabled pin and explicit pause state stay authoritative.
func (s *Store) UpsertPublishConfigDraft(tenantID, campaignID string, spec campaignenable.Spec, changedBy string) (PublishConfig, error) {
	if err := campaignenable.ValidateSpec(spec); err != nil {
		return PublishConfig{}, err
	}
	if _, err := s.GetCampaign(campaignID, tenantID); err != nil {
		return PublishConfig{}, err
	}
	qty, budget, onsite, perOrder, channels, storeIDs := configVals(spec)

	tx, err := s.DB.Begin()
	if err != nil {
		return PublishConfig{}, err
	}
	defer func() { _ = tx.Rollback() }()

	row, err := scanPublishConfig(tx.QueryRow(
		`SELECT `+publishConfigCols+` FROM campaign_publish_configs WHERE campaign_id=? AND tenant_id=?`,
		campaignID, tenantID))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		cfg := PublishConfig{
			ID: newID("cpcfg_"), TenantID: tenantID, CampaignID: campaignID,
			Version: 1, Status: "draft", Spec: spec,
			CreatedBy: changedBy, CreatedAt: now(), UpdatedAt: now(),
		}
		if _, err := tx.Exec(
			`INSERT INTO campaign_publish_configs(`+publishConfigCols+`)
			 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			cfg.ID, cfg.TenantID, cfg.CampaignID, cfg.Version, cfg.Status,
			spec.VerificationMode, spec.CustomerCopy,
			spec.Reward.Kind, spec.Reward.Slot, spec.Reward.BudgetMode,
			qty, budget, spec.Reward.Currency, storeIDs,
			spec.Reward.ClaimFrom, spec.Reward.ClaimUntil, spec.Reward.RedeemFrom, spec.Reward.RedeemUntil,
			onsite, spec.Reward.StaffScope, spec.Reward.OutOfStockRule,
			spec.DedupBasis, perOrder, channels, spec.ContentVerID,
			nil, "", "", "", cfg.CreatedBy, cfg.CreatedAt, cfg.UpdatedAt); err != nil {
			return PublishConfig{}, err
		}
		if err := insertConfigRevision(tx, cfg, 1, "create", changedBy); err != nil {
			return PublishConfig{}, err
		}
		if err := tx.Commit(); err != nil {
			return PublishConfig{}, err
		}
		return cfg, nil
	case err != nil:
		return PublishConfig{}, err
	}

	// 相同业务承诺重试不生成新版本，也不使已准备库存失联。
	if samePublishPromise(row.cfg.Spec, spec) {
		return row.cfg, nil
	}
	// Only the editable version changes. The live lifecycle is independent.
	next := row.cfg.Version + 1
	revision, err := nextConfigRevision(tx, tenantID, campaignID)
	if err != nil {
		return PublishConfig{}, err
	}
	ts := now()
	if _, err := tx.Exec(
		`UPDATE campaign_publish_configs SET verification_mode=?,customer_copy=?,benefit_kind=?,benefit_slot=?,
		 budget_mode=?,total_quantity=?,cost_budget_minor=?,currency=?,store_ids=?,
		 claim_from=?,claim_until=?,redeem_from=?,redeem_until=?,onsite_redemption=?,
		 staff_scope=?,out_of_stock_rule=?,dedup_basis=?,per_order_limit=?,channels=?,
		 content_version_id=?,version=?,status=CASE WHEN enabled_version>0 THEN CASE WHEN paused_at<>'' OR status='paused' THEN 'paused' ELSE 'enabled' END ELSE 'draft' END,updated_at=?
		 WHERE id=? AND tenant_id=?`,
		spec.VerificationMode, spec.CustomerCopy, spec.Reward.Kind, spec.Reward.Slot,
		spec.Reward.BudgetMode, qty, budget, spec.Reward.Currency, storeIDs,
		spec.Reward.ClaimFrom, spec.Reward.ClaimUntil, spec.Reward.RedeemFrom, spec.Reward.RedeemUntil,
		onsite, spec.Reward.StaffScope, spec.Reward.OutOfStockRule,
		spec.DedupBasis, perOrder, channels, spec.ContentVerID,
		next, ts, row.cfg.ID, tenantID); err != nil {
		return PublishConfig{}, err
	}
	updated, err := scanPublishConfig(tx.QueryRow(
		`SELECT `+publishConfigCols+` FROM campaign_publish_configs WHERE id=? AND tenant_id=?`,
		row.cfg.ID, tenantID))
	if err != nil {
		return PublishConfig{}, err
	}
	if err := insertConfigRevision(tx, updated.cfg, revision, "update", changedBy); err != nil {
		return PublishConfig{}, err
	}
	if err := tx.Commit(); err != nil {
		return PublishConfig{}, err
	}
	return updated.cfg, nil
}

// EnablePublishConfig is the conditional activation (draft -> enabled, paused
// -> enabled/resume): it pins the enabled version and freezes the disclosure
// snapshot, recording an enable/resume revision. When another writer enabled
// first, the row stays as-is and no duplicate revision is written (the caller
// answers replays 200).
func (s *Store) EnablePublishConfig(tenantID, campaignID string, version int, disclosure, changedBy string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	row, err := scanPublishConfig(tx.QueryRow(
		`SELECT `+publishConfigCols+` FROM campaign_publish_configs WHERE campaign_id=? AND tenant_id=?`,
		campaignID, tenantID))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if row.cfg.Version != version {
		return ErrPublishConfigVersionConflict
	}
	if row.cfg.EnabledVersion == version && row.cfg.Status != "paused" && row.cfg.PausedAt == "" {
		// 已启用:幂等重放,不重复写修订。版本对齐属于调用方 Check 的职责,
		// 这里只守护状态机。
		return tx.Commit()
	}
	action := "enable"
	if row.cfg.EnabledVersion == version && (row.cfg.Status == "paused" || row.cfg.PausedAt != "") {
		action = "resume"
	}
	if !validPublishedDisclosure(row.cfg.Spec, version, disclosure) {
		return ErrPublishConfigSnapshotInvalid
	}
	revision, err := nextConfigRevision(tx, tenantID, campaignID)
	if err != nil {
		return err
	}
	ts := now()
	res, err := tx.Exec(
		`UPDATE campaign_publish_configs SET status='enabled',enabled_version=?,enabled_at=?,paused_at='',disclosure=?,updated_at=?
		 WHERE campaign_id=? AND tenant_id=? AND version=? AND status IN ('draft','paused','enabled')`,
		version, ts, disclosure, ts, campaignID, tenantID, version)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrPublishConfigVersionConflict
	}
	row.cfg.Status = "enabled"
	row.cfg.EnabledVersion = version
	row.cfg.EnabledAt = ts
	row.cfg.PausedAt = ""
	row.cfg.Disclosure = disclosure
	if err := insertConfigRevision(tx, row.cfg, revision, action, changedBy); err != nil {
		return err
	}
	return tx.Commit()
}

// PausePublishConfig is the emergency stop: enabled -> paused. Participations
// and rewards are untouched (staff fulfillment keeps its own read paths); only
// new publish-reward activity is stopped.
func (s *Store) PausePublishConfig(tenantID, campaignID, changedBy string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	row, err := scanPublishConfig(tx.QueryRow(
		`SELECT `+publishConfigCols+` FROM campaign_publish_configs WHERE campaign_id=? AND tenant_id=?`,
		campaignID, tenantID))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if row.cfg.EnabledVersion <= 0 {
		return ErrPublishConfigNotEnabled
	}
	if row.cfg.Status == "paused" || row.cfg.PausedAt != "" {
		return tx.Commit()
	}
	revision, err := nextConfigRevision(tx, tenantID, campaignID)
	if err != nil {
		return err
	}
	ts := now()
	res, err := tx.Exec(
		`UPDATE campaign_publish_configs SET status='paused',paused_at=?,updated_at=?
		 WHERE campaign_id=? AND tenant_id=? AND enabled_version>0 AND paused_at='' AND status IN ('enabled','draft')`,
		ts, ts, campaignID, tenantID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return tx.Commit()
	}
	row.cfg.Status = "paused"
	row.cfg.PausedAt = ts
	if err := insertConfigRevision(tx, row.cfg, revision, "pause", changedBy); err != nil {
		return err
	}
	return tx.Commit()
}

// GetPublishConfig is the tenant-scoped admin lookup (ErrNotFound = never configured).
func (s *Store) GetPublishConfig(tenantID, campaignID string) (PublishConfig, error) {
	row, err := scanPublishConfig(s.DB.QueryRow(
		`SELECT `+publishConfigCols+` FROM campaign_publish_configs WHERE campaign_id=? AND tenant_id=?`,
		campaignID, tenantID))
	if errors.Is(err, sql.ErrNoRows) {
		return PublishConfig{}, ErrNotFound
	}
	if err != nil {
		return PublishConfig{}, err
	}
	return row.cfg, nil
}

// ListPublishConfigRevisions returns the immutable change trail oldest-first.
func (s *Store) ListPublishConfigRevisions(tenantID, campaignID string) ([]PublishConfigRevision, error) {
	rows, err := s.DB.Query(
		`SELECT id,tenant_id,campaign_id,revision,action,version,payload,changed_by,created_at
		 FROM campaign_publish_config_revisions WHERE tenant_id=? AND campaign_id=? ORDER BY revision`,
		tenantID, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PublishConfigRevision, 0)
	for rows.Next() {
		var r PublishConfigRevision
		var payload string
		if err := rows.Scan(&r.ID, &r.TenantID, &r.CampaignID, &r.Revision, &r.Action, &r.Version, &payload, &r.ChangedBy, &r.CreatedAt); err != nil {
			return nil, err
		}
		var p specPayload
		if err := json.Unmarshal([]byte(payload), &p); err != nil {
			return nil, err
		}
		r.Spec = p.Spec
		r.Status = p.Status
		out = append(out, r)
	}
	return out, rows.Err()
}

func samePublishPromise(a, b campaignenable.Spec) bool {
	normalize := func(s campaignenable.Spec) campaignenable.Spec {
		for _, p := range []*string{&s.Reward.ClaimFrom, &s.Reward.ClaimUntil, &s.Reward.RedeemFrom, &s.Reward.RedeemUntil} {
			if t, err := time.Parse(time.RFC3339, *p); err == nil {
				*p = t.UTC().Format(time.RFC3339Nano)
			}
		}
		s.Channels = append([]campaignenable.ChannelRef(nil), s.Channels...)
		sort.Slice(s.Channels, func(i, j int) bool {
			a, b := s.Channels[i], s.Channels[j]
			return a.Platform+"|"+a.EntryMode+"|"+a.Scenario < b.Platform+"|"+b.EntryMode+"|"+b.Scenario
		})
		s.Reward.StoreIDs = append([]string(nil), s.Reward.StoreIDs...)
		sort.Strings(s.Reward.StoreIDs)
		if len(s.Channels) == 0 {
			s.Channels = nil
		}
		if len(s.Reward.StoreIDs) == 0 {
			s.Reward.StoreIDs = nil
		}
		return s
	}
	return reflect.DeepEqual(normalize(a), normalize(b))
}

var (
	ErrPublishConfigNotEnabled      = errors.New("store: publish config not enabled")
	ErrPublishConfigSnapshotInvalid = errors.New("store: published config snapshot invalid")
	ErrPublishConfigVersionConflict = errors.New("store: publish config version changed")
)

type publishConfigQuerier interface{ QueryRow(string, ...any) *sql.Row }

// publishedConfigVersion reads only an enable/resume revision. Missing or
// malformed historical evidence never falls back to today's editable spec.
func publishedConfigVersion(q publishConfigQuerier, current PublishConfig, version int) (PublishConfig, error) {
	if version <= 0 || current.EnabledVersion <= 0 {
		return PublishConfig{}, ErrPublishConfigNotEnabled
	}
	var raw, at string
	err := q.QueryRow(`SELECT payload,created_at FROM campaign_publish_config_revisions WHERE tenant_id=? AND campaign_id=? AND version=? AND action IN ('enable','resume') ORDER BY revision DESC LIMIT 1`, current.TenantID, current.CampaignID, version).Scan(&raw, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return PublishConfig{}, ErrPublishConfigSnapshotInvalid
	}
	if err != nil {
		return PublishConfig{}, err
	}
	var p specPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return PublishConfig{}, fmt.Errorf("%w: payload", ErrPublishConfigSnapshotInvalid)
	}
	if p.Status != "enabled" || p.EnabledVersion != version || campaignenable.ValidateSpec(p.Spec) != nil {
		return PublishConfig{}, ErrPublishConfigSnapshotInvalid
	}
	if !validPublishedDisclosure(p.Spec, version, p.Disclosure) {
		return PublishConfig{}, ErrPublishConfigSnapshotInvalid
	}
	current.Version = version
	current.Spec = p.Spec
	current.Disclosure = p.Disclosure
	current.EnabledAt = at
	paused := current.PausedAt != "" || current.Status == "paused"
	current.Status = "enabled"
	if paused {
		current.Status = "paused"
	}
	return current, nil
}

func validPublishedDisclosure(spec campaignenable.Spec, version int, raw string) bool {
	disc, err := campaignenable.ParseDisclosure(raw)
	if err != nil {
		return false
	}
	generated, err := time.Parse(time.RFC3339Nano, disc.GeneratedAt)
	return err == nil && reflect.DeepEqual(disc, campaignenable.BuildDisclosure(spec, version, generated))
}

// GetPublishedConfig separates the live promise from GetPublishConfig's
// editable draft. Paused snapshots are returned with status=paused so every
// reader can display the same frozen terms without authorizing new activity.
func (s *Store) GetPublishedConfig(tenantID, campaignID string) (PublishConfig, error) {
	current, err := s.GetPublishConfig(tenantID, campaignID)
	if err != nil {
		return PublishConfig{}, err
	}
	return publishedConfigVersion(s.DB, current, current.EnabledVersion)
}

// ResumePublishConfig resumes exactly the previously published revision. It
// leaves any newer editable spec untouched and cannot publish it incidentally.
func (s *Store) ResumePublishConfig(tenantID, campaignID string, expectedVersion int, changedBy string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	row, err := scanPublishConfig(tx.QueryRow(`SELECT `+publishConfigCols+` FROM campaign_publish_configs WHERE tenant_id=? AND campaign_id=?`, tenantID, campaignID))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if row.cfg.EnabledVersion != expectedVersion {
		return ErrPublishConfigVersionConflict
	}
	live, err := publishedConfigVersion(tx, row.cfg, expectedVersion)
	if err != nil {
		return err
	}
	if row.cfg.PausedAt == "" && row.cfg.Status != "paused" {
		return tx.Commit()
	}
	revision, err := nextConfigRevision(tx, tenantID, campaignID)
	if err != nil {
		return err
	}
	ts := now()
	if _, err := tx.Exec(`UPDATE campaign_publish_configs SET status='enabled',paused_at='',updated_at=? WHERE tenant_id=? AND campaign_id=? AND enabled_version=?`, ts, tenantID, campaignID, expectedVersion); err != nil {
		return err
	}
	live.Status = "enabled"
	live.PausedAt = ""
	if err := insertConfigRevision(tx, live, revision, "resume", changedBy); err != nil {
		return err
	}
	return tx.Commit()
}

// publishedRewardActiveInTx closes the read-gate/write gap for a configured
// campaign. It reads the same publication authority on the ledger connection;
// no nested s.DB access is allowed while SQLite's single connection is held.
func publishedRewardActiveInTx(tx *sql.Tx, tenantID, campaignID string) error {
	row, err := scanPublishConfig(tx.QueryRow(`SELECT `+publishConfigCols+` FROM campaign_publish_configs WHERE tenant_id=? AND campaign_id=?`, tenantID, campaignID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	} // unchanged legacy unconfigured ledger
	if err != nil {
		return err
	}
	live, err := publishedConfigVersion(tx, row.cfg, row.cfg.EnabledVersion)
	if errors.Is(err, ErrPublishConfigNotEnabled) {
		return fmt.Errorf("%w: promise_not_enabled", ErrPromiseBlocked)
	}
	if err != nil {
		return fmt.Errorf("%w: publication_unknown", ErrPromiseBlocked)
	}
	if live.Status != "enabled" {
		return fmt.Errorf("%w: promise_not_enabled", ErrPromiseBlocked)
	}
	var status string
	if err := tx.QueryRow(`SELECT status FROM campaigns WHERE tenant_id=? AND id=?`, tenantID, campaignID).Scan(&status); err != nil {
		return err
	}
	if status != "active" {
		return fmt.Errorf("%w: campaign_not_active", ErrPromiseBlocked)
	}
	return nil
}

// GetPublishedConfigVersion resolves an originally enabled rule version for
// an existing participation. Current pause state still applies; draft spec is
// never a fallback and no new publication is created here.
func (s *Store) GetPublishedConfigVersion(tenantID, campaignID string, version int) (PublishConfig, error) {
	current, err := s.GetPublishConfig(tenantID, campaignID)
	if err != nil {
		return PublishConfig{}, err
	}
	result, err := publishedConfigVersion(s.DB, current, version)
	result.EnabledVersion = version
	return result, err
}

func (s *Store) FirstPublishedConfigAt(tenantID, campaignID string, version int) (string, error) {
	var at string
	err := s.DB.QueryRow(`SELECT created_at FROM campaign_publish_config_revisions WHERE tenant_id=? AND campaign_id=? AND version=? AND action IN ('enable','resume') ORDER BY revision LIMIT 1`, tenantID, campaignID, version).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrPublishConfigSnapshotInvalid
	}
	return at, err
}
