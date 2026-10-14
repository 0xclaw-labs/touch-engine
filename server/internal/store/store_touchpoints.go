// store_touchpoints.go: HUI-1665 增量(2026-10-14) —— 稳定触点别名域持久化。
//
// 纪律(票面增量实施节 + 设计裁决):
//   - 别名是既有标签/短码域之上的独立入口层,零第二套链接模型:campaign_links
//     与五态解析(ResolveLink)零改动;别名 code 与短码同一熵源(campaign.NewShortcode)。
//   - 实体卡写固定 HTTPS 入口 /t/{code}:换绑只改 campaign_id,URL 永不变化;
//     卡上载荷只有不透明码 URL(本表没有 URL/凭据/租户外部标识字段)。
//   - SetTouchpointStatus 只动别名自身,绝不级联 campaign_links —— 单卡停用
//     不误停共享 link 的其他卡(旧 SetTagStatus 传播语义逐字节不动,/admin 兼容)。
//   - RebindTouchpoint 乐观并发控制:expected 不符拒改(并发换绑显式 409),
//     同事务落 rebind 审计;目标必须本租户且至少一条短码。
//   - 部署五态只许单步显式推进;CSV/二维码/检查路径绝不调用本方法。
//   - 全部查询租户作用域、全参数化;写路径 bumpEpoch —— 别名建/停/换绑对
//     公共解析缓存是 0 陈旧请求的结构失效(readcache 既有策略)。
//   - CountStartedParticipations 是对 participations 表(3546 域所有)的只读
//     COUNT,仅用于换绑影响面统计;participation 包本体零改动(同 store_dashboard
//     读 leads 表的跨域只读先例)。
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/campaign"
)

// Deployment stages (票面:已登记/待写入/已写入待回读/已回读/真机打开验证).
const (
	StageRegistered              = "registered"
	StageAwaitingWrite           = "awaiting_write"
	StageWrittenAwaitingReadback = "written_awaiting_readback"
	StageReadbackDone            = "readback_done"
	StageDeviceVerified          = "device_verified"
)

// stageOrder is the closed, forward-only pipeline.
var stageOrder = []string{StageRegistered, StageAwaitingWrite, StageWrittenAwaitingReadback, StageReadbackDone, StageDeviceVerified}

// Sentinel errors (stable for tests/handlers).
var (
	// ErrTagAlreadyProvisioned: the tag already owns a stable entry (1 card = 1 alias).
	ErrTagAlreadyProvisioned = errors.New("store: tag already has a touchpoint alias")
	// ErrRebindConflict: optimistic concurrency — the current target moved.
	ErrRebindConflict = errors.New("store: touchpoint rebind conflict")
	// ErrStageTransition: stage move is not exactly one step forward.
	ErrStageTransition = errors.New("store: illegal deployment stage transition")
	// ErrNoEntryLink: the target campaign has no short code to land guests on.
	ErrNoEntryLink = errors.New("store: target campaign has no entry link")
)

// TouchpointAlias is one physical card's stable entry: /t/{Code} resolves to
// CampaignID's current public page.
type TouchpointAlias struct {
	ID              string `json:"id"`
	TenantID        string `json:"tenant_id"`
	Code            string `json:"code"`
	TagID           string `json:"tag_id,omitempty"`
	CampaignID      string `json:"campaign_id"`
	StoreID         string `json:"store_id,omitempty"`
	Label           string `json:"label"`
	Position        string `json:"position,omitempty"`
	Status          string `json:"status"`
	DeploymentStage string `json:"deployment_stage"`
	StageNote       string `json:"stage_note,omitempty"`
	StageBy         string `json:"stage_by,omitempty"`
	StageAt         string `json:"stage_at,omitempty"`
	CreatedBy       string `json:"created_by"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

// TouchpointEvent is one append-only audit row (启用/换绑/停用/五态推进).
type TouchpointEvent struct {
	ID        string `json:"id"`
	TenantID  string `json:"tenant_id"`
	AliasID   string `json:"alias_id"`
	Kind      string `json:"kind"`
	Detail    string `json:"detail,omitempty"`
	CreatedBy string `json:"created_by,omitempty"`
	CreatedAt string `json:"created_at"`
}

const aliasCols = `id,tenant_id,code,tag_id,campaign_id,store_id,label,position,status,deployment_stage,stage_note,stage_by,stage_at,created_by,created_at,updated_at`

func scanAlias(sc interface{ Scan(...any) error }) (TouchpointAlias, error) {
	var a TouchpointAlias
	var tagID, storeID, position, stageNote, stageBy, stageAt sql.NullString
	err := sc.Scan(&a.ID, &a.TenantID, &a.Code, &tagID, &a.CampaignID, &storeID, &a.Label, &position,
		&a.Status, &a.DeploymentStage, &stageNote, &stageBy, &stageAt, &a.CreatedBy, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return TouchpointAlias{}, err
	}
	a.TagID, a.StoreID, a.Position = tagID.String, storeID.String, position.String
	a.StageNote, a.StageBy, a.StageAt = stageNote.String, stageBy.String, stageAt.String
	return a, nil
}

// TouchpointView is the management-surface projection: the alias plus the
// joined display fields plus the effective entry (what guests see right now).
type TouchpointView struct {
	TouchpointAlias
	TagLabel       string `json:"tag_label,omitempty"`
	TagStatus      string `json:"tag_status,omitempty"`
	GroupName      string `json:"group_name,omitempty"`
	StoreName      string `json:"store_name,omitempty"`
	UIDHint        string `json:"uid_hint,omitempty"` // 管理面回填提示,公共面零泄漏
	CampaignTitle  string `json:"campaign_title"`
	CampaignStatus string `json:"campaign_status"`
	// EntryCode / EntryState: the canonical short code guests are redirected
	// to and its five-state at check time (检查所见 = 访客所见). Empty when the
	// entry cannot be computed (fail closed in ResolveTouchpoint).
	EntryCode  string `json:"entry_code,omitempty"`
	EntryState string `json:"entry_state,omitempty"`
}

const touchpointViewSelect = `SELECT ` + aliasColsA + `,
	COALESCE(t.label,''),COALESCE(t.status,''),COALESCE(g.name,''),COALESCE(st.name,''),COALESCE(t.uid_hint,''),
	c.title,c.status
FROM touchpoint_aliases a
LEFT JOIN nfc_tags t ON t.id=a.tag_id
LEFT JOIN nfc_tag_groups g ON g.id=t.group_id
LEFT JOIN stores st ON st.id=a.store_id
JOIN campaigns c ON c.id=a.campaign_id`

// aliasColsA renders the alias column list with the `a` table prefix.
const aliasColsA = `a.id,a.tenant_id,a.code,a.tag_id,a.campaign_id,a.store_id,a.label,a.position,` +
	`a.status,a.deployment_stage,a.stage_note,a.stage_by,a.stage_at,a.created_by,a.created_at,a.updated_at`

func scanTouchpointView(sc interface{ Scan(...any) error }) (TouchpointView, error) {
	var v TouchpointView
	var tagID, storeID, position, stageNote, stageBy, stageAt, tagLabel, tagStatus, groupName, storeName, uidHint sql.NullString
	err := sc.Scan(&v.ID, &v.TenantID, &v.Code, &tagID, &v.CampaignID, &storeID, &v.Label, &position,
		&v.Status, &v.DeploymentStage, &stageNote, &stageBy, &stageAt, &v.CreatedBy, &v.CreatedAt, &v.UpdatedAt,
		&tagLabel, &tagStatus, &groupName, &storeName, &uidHint, &v.CampaignTitle, &v.CampaignStatus)
	if err != nil {
		return TouchpointView{}, err
	}
	v.TagID, v.StoreID, v.Position = tagID.String, storeID.String, position.String
	v.StageNote, v.StageBy, v.StageAt = stageNote.String, stageBy.String, stageAt.String
	v.TagLabel, v.TagStatus, v.GroupName, v.StoreName, v.UIDHint =
		tagLabel.String, tagStatus.String, groupName.String, storeName.String, uidHint.String
	return v, nil
}

// attachEntry computes the effective entry for a view (检查 = 公共解析同源).
func (s *Store) attachEntry(v *TouchpointView) {
	// 检查所见=访客所见:redirect 带规范码与五态;别名停用/解析失败也如实带出
	// touchpoint_disabled / not_found,管理面检查不显示「状态未知」。
	res := s.ResolveTouchpoint(v.Code, time.Now())
	switch res.Outcome {
	case TpRedirect:
		v.EntryCode, v.EntryState = res.Code, string(res.State)
	case TpDisabled:
		v.EntryState = string(TpDisabled)
	default:
		v.EntryState = string(TpNotFound)
	}
}

func (s *Store) GetTouchpointView(id, tenantID string) (TouchpointView, error) {
	v, err := scanTouchpointView(s.DB.QueryRow(touchpointViewSelect+` WHERE a.id=? AND a.tenant_id=?`, id, tenantID))
	if errors.Is(err, sql.ErrNoRows) {
		return TouchpointView{}, ErrNotFound
	}
	if err != nil {
		return TouchpointView{}, err
	}
	s.attachEntry(&v)
	return v, nil
}

// TouchpointFilter narrows ListTouchpoints; empty fields mean "no filter".
type TouchpointFilter struct {
	CampaignID string
	StoreID    string
	GroupID    string // via the backing tag's group
	Status     string
}

func (f TouchpointFilter) valid() bool {
	switch f.Status {
	case "", TagStatusActive, TagStatusDisabled:
	default:
		return false
	}
	return true
}

func (s *Store) ListTouchpoints(tenantID string, f TouchpointFilter) ([]TouchpointView, error) {
	if !f.valid() {
		return nil, fmt.Errorf("store: bad touchpoint status filter %q", f.Status)
	}
	conds := []string{"a.tenant_id=?"}
	args := []any{tenantID}
	if f.CampaignID != "" {
		conds = append(conds, "a.campaign_id=?")
		args = append(args, f.CampaignID)
	}
	if f.StoreID != "" {
		conds = append(conds, "a.store_id=?")
		args = append(args, f.StoreID)
	}
	if f.GroupID != "" {
		conds = append(conds, "t.group_id=?")
		args = append(args, f.GroupID)
	}
	if f.Status != "" {
		conds = append(conds, "a.status=?")
		args = append(args, f.Status)
	}
	rows, err := s.DB.Query(touchpointViewSelect+` WHERE `+strings.Join(conds, " AND ")+` ORDER BY a.created_at,a.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]TouchpointView, 0)
	for rows.Next() {
		v, err := scanTouchpointView(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// 检查态是每行的同源解析(列表规模 = 门店触点数,量级小)。
	for i := range out {
		s.attachEntry(&out[i])
	}
	return out, nil
}

// ProvisionTouchpoint mints a stable alias for an existing tag. The initial
// campaign/store derive from the tag's binding (server-derived, never client
// input); the code is fresh server-side entropy. One tag owns at most one alias.
func (s *Store) ProvisionTouchpoint(tenantID, tagID, label, position, createdBy string) (TouchpointAlias, error) {
	v, err := s.GetTagView(tagID, tenantID)
	if err != nil {
		return TouchpointAlias{}, err // ErrNotFound masks missing AND cross-tenant
	}
	l, err := s.GetLink(v.LinkID, tenantID)
	if err != nil {
		return TouchpointAlias{}, err
	}
	code, err := campaign.NewShortcode()
	if err != nil {
		return TouchpointAlias{}, err
	}
	a := TouchpointAlias{
		ID: newID("tp_"), TenantID: tenantID, Code: code, TagID: tagID,
		CampaignID: l.CampaignID, StoreID: v.StoreID,
		Label: strings.TrimSpace(label), Position: strings.TrimSpace(position),
		Status: TagStatusActive, DeploymentStage: StageRegistered,
		CreatedBy: createdBy, CreatedAt: now(), UpdatedAt: now(),
	}
	err = s.withAudit(func(tx *sql.Tx) error {
		if _, err := tx.Exec(
			`INSERT INTO touchpoint_aliases(`+aliasCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			a.ID, a.TenantID, a.Code, nullable(a.TagID), a.CampaignID, nullable(a.StoreID),
			a.Label, a.Position, a.Status, a.DeploymentStage, "", "", "", a.CreatedBy, a.CreatedAt, a.UpdatedAt); err != nil {
			if isUniqueViolation(err, "tag_id") {
				return ErrTagAlreadyProvisioned
			}
			return err
		}
		return insertEvent(tx, a.TenantID, a.ID, "created", "code="+a.Code, createdBy)
	})
	if err != nil {
		return TouchpointAlias{}, err
	}
	s.bumpEpoch()
	return a, nil
}

// SetTouchpointStatus flips ONLY the alias. The bound campaign_link (shared by
// legacy cards) is never touched — 停用单卡不误停共享的其他卡.
func (s *Store) SetTouchpointStatus(id, tenantID string, status, by string) (TouchpointAlias, error) {
	if status != TagStatusActive && status != TagStatusDisabled {
		return TouchpointAlias{}, fmt.Errorf("store: bad touchpoint status %q", status)
	}
	cur, err := s.GetTouchpointAlias(id, tenantID)
	if err != nil {
		return TouchpointAlias{}, err
	}
	err = s.withAudit(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE touchpoint_aliases SET status=?,updated_at=? WHERE id=? AND tenant_id=?`,
			status, now(), id, tenantID); err != nil {
			return err
		}
		return insertEvent(tx, tenantID, id, "status_change", "status="+status, by)
	})
	if err != nil {
		return TouchpointAlias{}, err
	}
	s.bumpEpoch()
	cur.Status = status
	return cur, nil
}

// RebindTouchpoint moves the alias to a new campaign under optimistic
// concurrency: the caller must have previewed against expectedCampaignID; a
// concurrent rebind makes the write refuse instead of silently winning. The
// alias code (the printed URL) never changes.
func (s *Store) RebindTouchpoint(id, tenantID, newCampaignID, expectedCampaignID, createdBy string) (TouchpointAlias, error) {
	if newCampaignID == expectedCampaignID {
		return TouchpointAlias{}, ErrRebindConflict // no-op is a stale call
	}
	if _, err := s.GetCampaign(newCampaignID, tenantID); err != nil {
		return TouchpointAlias{}, err // ErrNotFound masks cross-tenant
	}
	if _, ok, err := s.pickCanonicalLink(tenantID, newCampaignID); err != nil || !ok {
		if err != nil {
			return TouchpointAlias{}, err
		}
		return TouchpointAlias{}, ErrNoEntryLink
	}
	err := s.withAudit(func(tx *sql.Tx) error {
		var current string
		switch err := tx.QueryRow(`SELECT campaign_id FROM touchpoint_aliases WHERE id=? AND tenant_id=?`, id, tenantID).Scan(&current); {
		case errors.Is(err, sql.ErrNoRows):
			return ErrNotFound
		case err != nil:
			return err
		}
		if current != expectedCampaignID {
			return ErrRebindConflict
		}
		if _, err := tx.Exec(`UPDATE touchpoint_aliases SET campaign_id=?,updated_at=? WHERE id=? AND tenant_id=? AND campaign_id=?`,
			newCampaignID, now(), id, tenantID, expectedCampaignID); err != nil {
			return err
		}
		return insertEvent(tx, tenantID, id, "rebind", "campaign "+current+" -> "+newCampaignID, createdBy)
	})
	if err != nil {
		return TouchpointAlias{}, err
	}
	s.bumpEpoch()
	return s.GetTouchpointAlias(id, tenantID)
}

// AdvanceTouchpointStage moves the deployment pipeline exactly one step
// forward. Nothing else may advance it: CSV/QR/check paths never call this.
func (s *Store) AdvanceTouchpointStage(id, tenantID, stage, note, by string) (TouchpointAlias, error) {
	cur, err := s.GetTouchpointAlias(id, tenantID)
	if err != nil {
		return TouchpointAlias{}, err
	}
	next, ok := nextStage(cur.DeploymentStage)
	if !ok || stage != next {
		return TouchpointAlias{}, fmt.Errorf("%w: %s -> %s", ErrStageTransition, cur.DeploymentStage, stage)
	}
	ts := now()
	err = s.withAudit(func(tx *sql.Tx) error {
		if _, err := tx.Exec(
			`UPDATE touchpoint_aliases SET deployment_stage=?,stage_note=?,stage_by=?,stage_at=?,updated_at=? WHERE id=? AND tenant_id=?`,
			stage, strings.TrimSpace(note), by, ts, ts, id, tenantID); err != nil {
			return err
		}
		return insertEvent(tx, tenantID, id, "stage_change", cur.DeploymentStage+" -> "+stage, by)
	})
	if err != nil {
		return TouchpointAlias{}, err
	}
	s.bumpEpoch()
	return s.GetTouchpointAlias(id, tenantID)
}

func (s *Store) GetTouchpointAlias(id, tenantID string) (TouchpointAlias, error) {
	a, err := scanAlias(s.DB.QueryRow(`SELECT `+aliasCols+` FROM touchpoint_aliases WHERE id=? AND tenant_id=?`, id, tenantID))
	if errors.Is(err, sql.ErrNoRows) {
		return TouchpointAlias{}, ErrNotFound
	}
	return a, err
}

// ListTouchpointEvents returns the audit trail, oldest first.
func (s *Store) ListTouchpointEvents(tenantID, aliasID string) ([]TouchpointEvent, error) {
	rows, err := s.DB.Query(
		`SELECT id,tenant_id,alias_id,kind,detail,COALESCE(created_by,''),created_at
		 FROM touchpoint_events WHERE tenant_id=? AND alias_id=? ORDER BY created_at,id`, tenantID, aliasID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]TouchpointEvent, 0)
	for rows.Next() {
		var e TouchpointEvent
		if err := rows.Scan(&e.ID, &e.TenantID, &e.AliasID, &e.Kind, &e.Detail, &e.CreatedBy, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CountStartedParticipations is the read-only impact stat for the rebind
// preview: participations (3546 domain) stay bound to their original campaign
// no matter where the touchpoint points afterwards.
func (s *Store) CountStartedParticipations(tenantID, campaignID string) (int64, error) {
	var n int64
	err := s.DB.QueryRow(`SELECT COUNT(1) FROM participations WHERE tenant_id=? AND campaign_id=?`,
		tenantID, campaignID).Scan(&n)
	return n, err
}

// withAudit runs fn in one transaction; a nil error commits.
func (s *Store) withAudit(fn func(tx *sql.Tx) error) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func insertEvent(tx *sql.Tx, tenantID, aliasID, kind, detail, createdBy string) error {
	_, err := tx.Exec(
		`INSERT INTO touchpoint_events(id,tenant_id,alias_id,kind,detail,created_by,created_at) VALUES(?,?,?,?,?,?,?)`,
		newID("tpe_"), tenantID, aliasID, kind, detail, createdBy, now())
	return err
}

func nextStage(cur string) (string, bool) {
	for i, st := range stageOrder {
		if st == cur && i+1 < len(stageOrder) {
			return stageOrder[i+1], true
		}
	}
	return "", false
}

// ---- stable-entry resolution (public /t/{code}) --------------------------------

// TouchpointOutcome is the machine-readable verdict of a stable-entry lookup.
//
//	tp_not_found          -> shape-invalid OR unknown code (indistinguishable)
//	touchpoint_disabled   -> the card's own switch is off (alias-level only)
//	redirect              -> hand the guest to /c/{Code}; the five-state is
//	                        re-derived there (State mirrors it for 检查 parity)
type TouchpointOutcome string

const (
	TpNotFound TouchpointOutcome = "not_found"
	TpDisabled TouchpointOutcome = "touchpoint_disabled"
	TpRedirect TouchpointOutcome = "redirect"
)

// TouchpointResolution mirrors ResolvedLink for the alias layer.
type TouchpointResolution struct {
	Outcome TouchpointOutcome
	// Code is the canonical short code (oldest enabled link, else oldest) the
	// guest is handed to; State is that code's five-state at `at`.
	Code  string
	State ResolveOutcome
}

// TouchpointRows is the cacheable form of everything the resolution consumes
// (same epoch-validation discipline as ResolvedRows).
type TouchpointRows struct {
	Found bool
	Alias TouchpointAlias

	HasCampaign bool
	Campaign    Campaign

	HasLink bool
	Link    CampaignLink
}

// CampaignHasEntryLink reports whether the campaign owns at least one short
// code (a rebind target without an entry cannot land guests anywhere).
func (s *Store) CampaignHasEntryLink(tenantID, campaignID string) (bool, error) {
	_, ok, err := s.pickCanonicalLink(tenantID, campaignID)
	return ok, err
}

// pickCanonicalLink chooses the entry code of a campaign: oldest ENABLED link
// first, else oldest link (guests then see the existing five-state 停用 text).
func (s *Store) pickCanonicalLink(tenantID, campaignID string) (CampaignLink, bool, error) {
	l, err := scanLink(s.DB.QueryRow(
		`SELECT `+linkCols+` FROM campaign_links WHERE tenant_id=? AND campaign_id=? ORDER BY enabled DESC, created_at ASC, id ASC LIMIT 1`,
		tenantID, campaignID))
	if errors.Is(err, sql.ErrNoRows) {
		return CampaignLink{}, false, nil
	}
	if err != nil {
		return CampaignLink{}, false, err
	}
	return l, true, nil
}

// LoadTouchpointRows is the cache-miss backfill. Any unreadable row stays
// absent and the decision fails closed to not_found — a lookup error must
// never look available.
func (s *Store) LoadTouchpointRows(code string) TouchpointRows {
	var rows TouchpointRows
	if !campaign.ValidShortcode(code) {
		return rows // malformed is indistinguishable from unknown
	}
	a, err := scanAlias(s.DB.QueryRow(`SELECT `+aliasCols+` FROM touchpoint_aliases WHERE code=?`, code))
	if err != nil { // ErrNoRows OR real error: fail closed
		return rows
	}
	rows.Found, rows.Alias = true, a
	if c, err := s.GetCampaign(a.CampaignID, a.TenantID); err == nil {
		rows.HasCampaign, rows.Campaign = true, c
	}
	if l, ok, err := s.pickCanonicalLink(a.TenantID, a.CampaignID); err == nil && ok {
		rows.HasLink, rows.Link = true, l
	}
	return rows
}

// DecideTouchpoint is the pure decision over fetched rows, judged at `at`:
// windows are never frozen into a cached entry, and admin edits (alias
// create/disable/rebind all bumpEpoch) invalidate structurally.
func DecideTouchpoint(rows TouchpointRows, at time.Time) TouchpointResolution {
	if !rows.Found || !rows.HasCampaign || !rows.HasLink {
		return TouchpointResolution{Outcome: TpNotFound}
	}
	if rows.Alias.Status != TagStatusActive {
		return TouchpointResolution{Outcome: TpDisabled}
	}
	// Five-state parity with the /c page: same pure decision over the same rows.
	state := DecideLink(ResolvedRows{Found: true, Link: rows.Link, HasCampaign: true, Campaign: rows.Campaign}, at)
	if state.Outcome == OutcomeNotFound {
		return TouchpointResolution{Outcome: TpNotFound}
	}
	return TouchpointResolution{Outcome: TpRedirect, Code: rows.Link.Code, State: state.Outcome}
}

// ResolveTouchpoint is the direct (uncached) stable-entry lookup.
func (s *Store) ResolveTouchpoint(code string, at time.Time) TouchpointResolution {
	return DecideTouchpoint(s.LoadTouchpointRows(code), at)
}
