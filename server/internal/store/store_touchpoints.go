// store_touchpoints.go: HUI-1665 增量(2026-10-14) —— 稳定触点别名域持久化。
// HUI-1665 Wave2(2026-11-10) —— B02 门店稳定触点:target_type=store 根(campaign 可空)。
//
// 纪律(票面增量实施节 + 设计裁决):
//   - 别名是既有标签/短码域之上的独立入口层,零第二套链接模型:campaign_links
//     与五态解析(ResolveLink)零改动;别名 code 与短码同一熵源(campaign.NewShortcode)。
//   - 实体卡写固定 HTTPS 入口 /t/{code}:换绑只改目标(target_type+store/campaign),
//     URL 永不变化;卡上载荷只有不透明码 URL(本表没有 URL/凭据/租户外部标识字段)。
//   - B02 目标模型:target_type=store 以 store_id 为根(guest→/s/{store_code},
//     campaign_id 只作可选主推注记,解析零活动门控);target_type=legacy_campaign 是
//     旧行语义(guest→/c/{规范短码},五态照旧)。绑定了门店的新 provision 落 store 根;
//     无店标签保持 legacy(旧卡兼容,不虚构门店)。迁移(0104)只补 store_id 归属,
//     不静默改写任何行的 target_type。
//   - SetTouchpointStatus 只动别名自身,绝不级联 campaign_links —— 单卡停用
//     不误停共享 link 的其他卡(旧 SetTagStatus 传播语义逐字节不动,/admin 兼容)。
//   - 换绑乐观并发控制:expected_binding_revision 不符拒改(并发换绑显式 409);
//     旧签名(expected campaign)保留为兼容委托。目标必须本租户;store 目标
//     EnsureStorefront 保证门店公共码在绑定时已存在(公共解析保持纯读 fail-closed)。
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

// B02 target types (票面契约草案,target_type 白名单见 0103 迁移 CHECK).
const (
	// TargetStore: 门店根 —— guest 解析到 /s/{store_code};campaign_id 仅作可选注记。
	TargetStore = "store"
	// TargetLegacyCampaign: 旧行语义 —— guest 解析到 /c/{规范短码},五态照旧。
	TargetLegacyCampaign = "legacy_campaign"
)

// TouchpointAlias is one physical card's stable entry: /t/{Code} resolves to
// the current target (store root or legacy campaign) of the card.
type TouchpointAlias struct {
	ID              string `json:"id"`
	TenantID        string `json:"tenant_id"`
	Code            string `json:"code"`
	TagID           string `json:"tag_id,omitempty"`
	CampaignID      string `json:"campaign_id,omitempty"`
	StoreID         string `json:"store_id,omitempty"`
	TargetType      string `json:"target_type"`
	BindingRevision int64  `json:"binding_revision"`
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

const aliasCols = `id,tenant_id,code,tag_id,campaign_id,store_id,target_type,binding_revision,label,position,status,deployment_stage,stage_note,stage_by,stage_at,created_by,created_at,updated_at`

func scanAlias(sc interface{ Scan(...any) error }) (TouchpointAlias, error) {
	var a TouchpointAlias
	var tagID, campaignID, storeID, position, stageNote, stageBy, stageAt sql.NullString
	err := sc.Scan(&a.ID, &a.TenantID, &a.Code, &tagID, &campaignID, &storeID, &a.TargetType, &a.BindingRevision,
		&a.Label, &position, &a.Status, &a.DeploymentStage, &stageNote, &stageBy, &stageAt,
		&a.CreatedBy, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return TouchpointAlias{}, err
	}
	a.TagID, a.CampaignID, a.StoreID, a.Position = tagID.String, campaignID.String, storeID.String, position.String
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
	UIDHint        string `json:"uid_hint,omitempty"`       // 管理面回填提示,公共面零泄漏
	CampaignTitle  string `json:"campaign_title,omitempty"` // store 根行可为空注记
	CampaignStatus string `json:"campaign_status,omitempty"`
	// StoreCode: B02 门店公共码(store_storefronts,store 根行的 /s 目标);
	// legacy 行通常也为绑定门店带出(归属事实),空=归属未定(迁移异常并列展示)。
	StoreCode string `json:"store_code,omitempty"`
	// EntryCode / EntryState: the code guests are redirected to (legacy=规范
	// 短码,store=门店公共码) and its state at check time (检查所见 = 访客所见).
	// Empty when the entry cannot be computed (fail closed in ResolveTouchpoint).
	EntryCode  string `json:"entry_code,omitempty"`
	EntryState string `json:"entry_state,omitempty"`
}

const touchpointViewSelect = `SELECT ` + aliasColsA + `,
	COALESCE(t.label,''),COALESCE(t.status,''),COALESCE(g.name,''),COALESCE(st.name,''),COALESCE(t.uid_hint,''),
	COALESCE(c.title,''),COALESCE(c.status,''),COALESCE(f.store_code,'')
FROM touchpoint_aliases a
LEFT JOIN nfc_tags t ON t.id=a.tag_id
LEFT JOIN nfc_tag_groups g ON g.id=t.group_id
LEFT JOIN stores st ON st.id=a.store_id
LEFT JOIN campaigns c ON c.id=a.campaign_id
LEFT JOIN store_storefronts f ON f.store_id=a.store_id AND f.tenant_id=a.tenant_id`

// aliasColsA renders the alias column list with the `a` table prefix.
const aliasColsA = `a.id,a.tenant_id,a.code,a.tag_id,a.campaign_id,a.store_id,a.target_type,a.binding_revision,a.label,a.position,` +
	`a.status,a.deployment_stage,a.stage_note,a.stage_by,a.stage_at,a.created_by,a.created_at,a.updated_at`

func scanTouchpointView(sc interface{ Scan(...any) error }) (TouchpointView, error) {
	var v TouchpointView
	var tagID, campaignID, storeID, position, stageNote, stageBy, stageAt, tagLabel, tagStatus, groupName, storeName, uidHint sql.NullString
	err := sc.Scan(&v.ID, &v.TenantID, &v.Code, &tagID, &campaignID, &storeID, &v.TargetType, &v.BindingRevision,
		&v.Label, &position, &v.Status, &v.DeploymentStage, &stageNote, &stageBy, &stageAt,
		&v.CreatedBy, &v.CreatedAt, &v.UpdatedAt,
		&tagLabel, &tagStatus, &groupName, &storeName, &uidHint, &v.CampaignTitle, &v.CampaignStatus, &v.StoreCode)
	if err != nil {
		return TouchpointView{}, err
	}
	v.TagID, v.CampaignID, v.StoreID, v.Position = tagID.String, campaignID.String, storeID.String, position.String
	v.StageNote, v.StageBy, v.StageAt = stageNote.String, stageBy.String, stageAt.String
	v.TagLabel, v.TagStatus, v.GroupName, v.StoreName, v.UIDHint =
		tagLabel.String, tagStatus.String, groupName.String, storeName.String, uidHint.String
	return v, nil
}

// attachEntry computes the effective entry for a view (检查 = 公共解析同源).
func (s *Store) attachEntry(v *TouchpointView) {
	// 检查所见=访客所见:redirect 带规范码(legacy)或门店公共码(store)与状态;
	// 别名停用/解析失败也如实带出 touchpoint_disabled / not_found,管理面检查
	// 不显示「状态未知」。
	res := s.ResolveTouchpoint(v.Code, time.Now())
	switch res.Outcome {
	case TpRedirect:
		if res.TargetType == TargetStore {
			v.EntryCode = res.StoreCode
		} else {
			v.EntryCode = res.Code
		}
		v.EntryState = string(res.State)
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

// ProvisionTouchpoint mints a stable alias for an existing tag. Everything
// derives from the tag's binding (server-derived, never client input); the
// code is fresh server-side entropy. One tag owns at most one alias.
//
// B02 目标派生:标签绑定在门店上 → store 根(store_id 权威必填;campaign 只作
// 可选注记,解析零活动门控);标签无店 → 保持 legacy_campaign(旧卡兼容,不虚构
// 门店)。store 根绑定前 EnsureStorefront,门店公共码在绑定时已存在 —— 公共解析
// 保持纯读,失败 fail-closed。
func (s *Store) ProvisionTouchpoint(tenantID, tagID, label, position, createdBy string) (TouchpointAlias, error) {
	v, err := s.GetTagView(tagID, tenantID)
	if err != nil {
		return TouchpointAlias{}, err // ErrNotFound masks missing AND cross-tenant
	}
	l, err := s.GetLink(v.LinkID, tenantID)
	if err != nil {
		return TouchpointAlias{}, err
	}
	targetType := TargetLegacyCampaign
	storeID := v.StoreID
	if storeID != "" {
		targetType = TargetStore
		if _, err := s.EnsureStorefront(tenantID, storeID); err != nil {
			return TouchpointAlias{}, err
		}
	}
	code, err := campaign.NewShortcode()
	if err != nil {
		return TouchpointAlias{}, err
	}
	a := TouchpointAlias{
		ID: newID("tp_"), TenantID: tenantID, Code: code, TagID: tagID,
		CampaignID: l.CampaignID, StoreID: storeID,
		TargetType: targetType, BindingRevision: 1,
		Label: strings.TrimSpace(label), Position: strings.TrimSpace(position),
		Status: TagStatusActive, DeploymentStage: StageRegistered,
		CreatedBy: createdBy, CreatedAt: now(), UpdatedAt: now(),
	}
	err = s.withAudit(func(tx *sql.Tx) error {
		if _, err := tx.Exec(
			`INSERT INTO touchpoint_aliases(`+aliasCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			a.ID, a.TenantID, a.Code, nullable(a.TagID), nullable(a.CampaignID), nullable(a.StoreID),
			a.TargetType, a.BindingRevision,
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

// TouchpointTarget is a rebind destination (B02): a store root or a legacy
// campaign entry. Exactly one of the ids is meaningful per type.
type TouchpointTarget struct {
	Type       string
	StoreID    string
	CampaignID string
}

// TouchpointRebindExpectation carries the optimistic-concurrency guard seen at
// preview time. Revision > 0 is the canonical guard (binding_revision);
// CampaignID is the legacy guard kept for existing callers.
type TouchpointRebindExpectation struct {
	Revision   int64
	CampaignID string
}

// RebindTouchpointTarget moves the alias to a new target under optimistic
// concurrency: the caller must have previewed against expected; a concurrent
// rebind makes the write refuse (ErrRebindConflict) instead of silently
// winning. The alias code (the printed URL) never changes, and existing
// participations stay bound to their original campaign (快照不挪动).
//
//	store 目标:本租户门店,EnsureStorefront 保证公共码存在;campaign 注记清空。
//	legacy 目标:本租户活动且至少一条短码(guest 必须有处可落)。
//	同一事务落 rebind 审计并 +1 binding_revision。
func (s *Store) RebindTouchpointTarget(id, tenantID string, target TouchpointTarget, expected TouchpointRebindExpectation, createdBy string) (TouchpointAlias, error) {
	switch target.Type {
	case TargetStore:
		if strings.TrimSpace(target.StoreID) == "" {
			return TouchpointAlias{}, fmt.Errorf("store: store target requires store_id")
		}
		if _, err := s.GetStore(target.StoreID, tenantID); err != nil {
			return TouchpointAlias{}, err // ErrNotFound masks cross-tenant
		}
		// 门店公共码必须在换绑时就存在(公共解析纯读,fail-closed 不现场补行)。
		if _, err := s.EnsureStorefront(tenantID, target.StoreID); err != nil {
			return TouchpointAlias{}, err
		}
	case TargetLegacyCampaign:
		if strings.TrimSpace(target.CampaignID) == "" {
			return TouchpointAlias{}, fmt.Errorf("store: campaign target requires campaign_id")
		}
		if _, err := s.GetCampaign(target.CampaignID, tenantID); err != nil {
			return TouchpointAlias{}, err // ErrNotFound masks cross-tenant
		}
		if _, ok, err := s.pickCanonicalLink(tenantID, target.CampaignID); err != nil || !ok {
			if err != nil {
				return TouchpointAlias{}, err
			}
			return TouchpointAlias{}, ErrNoEntryLink
		}
	default:
		return TouchpointAlias{}, fmt.Errorf("store: bad touchpoint target type %q", target.Type)
	}
	err := s.withAudit(func(tx *sql.Tx) error {
		var cur TouchpointAlias
		switch a, scanErr := scanAlias(tx.QueryRow(`SELECT `+aliasCols+` FROM touchpoint_aliases WHERE id=? AND tenant_id=?`, id, tenantID)); {
		case scanErr == nil:
			cur = a
		case errors.Is(scanErr, sql.ErrNoRows):
			return ErrNotFound
		default:
			return scanErr
		}
		// 乐观并发闸:revision 为准;旧调用方退化比较 campaign。
		if expected.Revision > 0 {
			if cur.BindingRevision != expected.Revision {
				return ErrRebindConflict
			}
		} else if cur.CampaignID != expected.CampaignID {
			return ErrRebindConflict
		}
		// 同目标同闸值 = 无操作的陈旧调用,显式拒绝避免审计噪音。
		sameTarget := target.Type == cur.TargetType &&
			((target.Type == TargetStore && target.StoreID == cur.StoreID) ||
				(target.Type == TargetLegacyCampaign && target.CampaignID == cur.CampaignID))
		if sameTarget {
			return ErrRebindConflict
		}
		newStoreID, newCampaignID, detail := cur.StoreID, cur.CampaignID, ""
		switch target.Type {
		case TargetStore:
			newStoreID, newCampaignID = target.StoreID, ""
			detail = "target " + cur.TargetType + " -> store " + target.StoreID
		default:
			newCampaignID = target.CampaignID
			detail = "target " + cur.TargetType + " -> campaign " + target.CampaignID
		}
		if _, err := tx.Exec(
			`UPDATE touchpoint_aliases SET campaign_id=?,store_id=?,target_type=?,binding_revision=binding_revision+1,updated_at=?
			 WHERE id=? AND tenant_id=? AND binding_revision=?`,
			nullable(newCampaignID), nullable(newStoreID), target.Type, now(), id, tenantID, cur.BindingRevision); err != nil {
			return err
		}
		return insertEvent(tx, tenantID, id, "rebind", detail, createdBy)
	})
	if err != nil {
		return TouchpointAlias{}, err
	}
	s.bumpEpoch()
	return s.GetTouchpointAlias(id, tenantID)
}

// RebindTouchpoint is the legacy-campaign rebind kept for existing callers
// (旧 /t 卡换活动):campaign guard + campaign target. New surfaces should use
// RebindTouchpointTarget with expected_binding_revision.
func (s *Store) RebindTouchpoint(id, tenantID, newCampaignID, expectedCampaignID, createdBy string) (TouchpointAlias, error) {
	if newCampaignID == expectedCampaignID {
		return TouchpointAlias{}, ErrRebindConflict // no-op is a stale call
	}
	return s.RebindTouchpointTarget(id, tenantID,
		TouchpointTarget{Type: TargetLegacyCampaign, CampaignID: newCampaignID},
		TouchpointRebindExpectation{CampaignID: expectedCampaignID}, createdBy)
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
//	tp_not_found          -> shape-invalid OR unknown code OR unresolvable
//	                         target (indistinguishable, fail-closed)
//	touchpoint_disabled   -> the card's own switch is off (alias-level only)
//	redirect              -> hand the guest to the target: store root goes to
//	                         /s/{StoreCode} (no campaign gating — 无活动/暂停/
//	                         结束不关闭门店基础服务); legacy goes to /c/{Code}
//	                         with its five-state re-derived there
type TouchpointOutcome string

const (
	TpNotFound TouchpointOutcome = "not_found"
	TpDisabled TouchpointOutcome = "touchpoint_disabled"
	TpRedirect TouchpointOutcome = "redirect"
)

// TouchpointResolution mirrors ResolvedLink for the alias layer, plus the B02
// target facts (target_type + store code + binding_revision) the public
// payload needs. Code carries the legacy canonical short code; StoreCode
// carries the store-root public code; exactly one is set on a redirect.
type TouchpointResolution struct {
	Outcome    TouchpointOutcome
	TargetType string
	// Code is the canonical short code (oldest enabled link, else oldest) the
	// guest is handed to on a legacy row; State is that code's five-state at
	// `at`. Empty on store-root rows (their page governs its own state).
	Code      string
	StoreCode string
	State     ResolveOutcome
	// BindingRevision mirrors the alias revision for the public payload.
	BindingRevision int64
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

	// B02 store-root facts: the storefront row (store_code) must exist for a
	// store-root alias to resolve; its absence fails closed to not_found.
	HasStore  bool
	StoreCode string
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
	if a.TargetType == TargetStore {
		// store 根只产出门店定位:store_code 存在即可解析(门店面自身状态由
		// GET /api/v1/public/storefronts/{storeCode} 呈现),活动注记零参与。
		var storeCode string
		if err := s.DB.QueryRow(
			`SELECT f.store_code FROM store_storefronts f
			 JOIN stores st ON st.id=f.store_id AND st.tenant_id=f.tenant_id
			 JOIN tenants t ON t.id=f.tenant_id
			 WHERE f.store_id=? AND f.tenant_id=?`, a.StoreID, a.TenantID).Scan(&storeCode); err == nil {
			rows.HasStore, rows.StoreCode = true, storeCode
		}
		return rows
	}
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
	if !rows.Found {
		return TouchpointResolution{Outcome: TpNotFound}
	}
	rev := rows.Alias.BindingRevision
	if rows.Alias.TargetType == TargetStore {
		if !rows.HasStore { // 门店面行缺失=目标不可解析,先于停用判定(同旧行为形状,fail-closed)
			return TouchpointResolution{Outcome: TpNotFound}
		}
		if rows.Alias.Status != TagStatusActive {
			return TouchpointResolution{Outcome: TpDisabled, TargetType: TargetStore, BindingRevision: rev}
		}
		return TouchpointResolution{Outcome: TpRedirect, TargetType: TargetStore,
			StoreCode: rows.StoreCode, State: OutcomeAvailable, BindingRevision: rev}
	}
	// legacy 行为逐字节保留:目标行缺失 → not_found(先于停用判定);停用只看别名自身。
	if !rows.HasCampaign || !rows.HasLink {
		return TouchpointResolution{Outcome: TpNotFound, TargetType: TargetLegacyCampaign, BindingRevision: rev}
	}
	if rows.Alias.Status != TagStatusActive {
		return TouchpointResolution{Outcome: TpDisabled, TargetType: TargetLegacyCampaign, BindingRevision: rev}
	}
	// Five-state parity with the /c page: same pure decision over the same rows.
	state := DecideLink(ResolvedRows{Found: true, Link: rows.Link, HasCampaign: true, Campaign: rows.Campaign}, at)
	if state.Outcome == OutcomeNotFound {
		return TouchpointResolution{Outcome: TpNotFound, TargetType: TargetLegacyCampaign, BindingRevision: rev}
	}
	return TouchpointResolution{Outcome: TpRedirect, TargetType: TargetLegacyCampaign,
		Code: rows.Link.Code, State: state.Outcome, BindingRevision: rev}
}

// ResolveTouchpoint is the direct (uncached) stable-entry lookup.
func (s *Store) ResolveTouchpoint(code string, at time.Time) TouchpointResolution {
	return DecideTouchpoint(s.LoadTouchpointRows(code), at)
}
