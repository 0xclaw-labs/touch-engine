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
//   - enabled 下保存 → 自动回 draft(改版必须重新过启用检查,不出现
//     「未检查的新版本继续在线」);启用时点的 enabled_version/disclosure
//     保留不覆盖(在途参与按原告知处理);
//   - 启用走条件 UPDATE(status IN draft,paused 才可置 enabled):
//     并发启用串行化为一次生效,0 行 = 他人已启用 → 幂等提交且不重复写
//     enable 修订;paused → enabled 的恢复修订记 resume。
package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/bianjiefilm/touch-engine/server/internal/campaignenable"
)

// PublishConfig is the active per-campaign publish-reward configuration row.
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
// configuration draft. Saving over an enabled config demotes it to draft: the
// new version must pass the enable check again before it is live. The previous
// enable pin (enabled_version/disclosure) survives for in-flight participations.
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

	// enabled/paused 下保存 → 回 draft;旧启用钉版保留。
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
		 content_version_id=?,version=?,status='draft',updated_at=?
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
	if row.cfg.Status == "enabled" {
		// 已启用:幂等重放,不重复写修订。版本对齐属于调用方 Check 的职责,
		// 这里只守护状态机。
		return tx.Commit()
	}
	action := "enable"
	if row.cfg.Status == "paused" {
		action = "resume"
	}
	revision, err := nextConfigRevision(tx, tenantID, campaignID)
	if err != nil {
		return err
	}
	ts := now()
	res, err := tx.Exec(
		`UPDATE campaign_publish_configs SET status='enabled',enabled_version=?,enabled_at=?,paused_at='',disclosure=?,updated_at=?
		 WHERE campaign_id=? AND tenant_id=? AND status IN ('draft','paused')`,
		version, ts, disclosure, ts, campaignID, tenantID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// 并发下他人先启用:读回返回,不双写。
		return tx.Commit()
	}
	row.cfg.Status = "enabled"
	row.cfg.EnabledVersion = version
	row.cfg.EnabledAt = ts
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
	if row.cfg.Status != "enabled" {
		return ErrNotFound
	}
	revision, err := nextConfigRevision(tx, tenantID, campaignID)
	if err != nil {
		return err
	}
	ts := now()
	res, err := tx.Exec(
		`UPDATE campaign_publish_configs SET status='paused',paused_at=?,updated_at=?
		 WHERE campaign_id=? AND tenant_id=? AND status='enabled'`,
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
