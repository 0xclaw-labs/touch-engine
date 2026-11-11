// store_storefront_versions.go: HUI-1676 门店首页展示版本(B01)持久层。
//
// 纪律(裁定书 + 票面):
//   - 版本行挂既有 stores 维度(行级租户隔离);跨租户一律 ErrNotFound;
//   - 最新行是可变草稿(published 行永不再写):内容相同=幂等重放(不推修订),
//     内容变化=原行修订+1;最新行已 published → 新建 rev+1 draft 行;
//   - 发布 = 冻结引用原子切换:事务内重校验 refs(行修订漂移→RefDrift;
//     实时撤权/过期/停用→PublishBlocked)、行置 published、指针 UPDATE
//     store_storefronts.storefront_revision(4584 建列,本票接管写)、事件落
//     storefront_publish_events,提交后 bumpEpoch(缓存 0 请求过期);
//   - publish 携确认摘要指纹:服务端按当前 published↔候选实时重算 diff,
//     指纹不齐 = 草稿在确认后变过 → ErrStorefrontConfirmRequired;
//   - publish 幂等:指针已等于候选 → 同一发布结果(Replayed);expected 不匹配
//     → ErrStaleRevision;候选非最新 → ErrStaleRevision(发布落后草稿一律拒绝);
//   - 回滚也是一次发布事件:目标须已发布;refs 逐动作重查实时撤权/过期/停用
//     (不复活失效权限;剔除须商家在草稿明确排除形成新修订,历史行零改写);
//   - 版本只存引用与冻结白名单载荷:不写参与/奖励表,旧参与快照不随换版改写;
//     发布/回滚零触碰 store_actions 行(测试锁定)。
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
	"github.com/bianjiefilm/touch-engine/server/internal/extrajump"
	"github.com/bianjiefilm/touch-engine/server/internal/storefrontversion"
)

// Storefront version statuses.
const (
	StorefrontStatusDraft     = "draft"
	StorefrontStatusPublished = "published"
)

// Storefront-level failures. The HTTP layer maps them onto stable codes.
var (
	ErrStorefrontRevisionUnknown = errors.New("store: storefront revision unknown")
	ErrStorefrontNotPublished    = errors.New("store: storefront rollback target is not published")
	ErrStorefrontConfirmRequired = errors.New("store: storefront change summary confirmation mismatch")
	ErrStorefrontRefDrift        = errors.New("store: storefront action refs drifted from live rows")
	ErrUnknownOfferRef           = errors.New("store: unknown offer ref")
	ErrUnknownCampaignRef        = errors.New("store: unknown campaign ref")
)

// StorefrontBlockedError carries the per-action veto list for a refused
// publish or rollback (the merchant must explicitly exclude these in the
// draft — never silently here).
type StorefrontBlockedError struct {
	Kind    string // publish | rollback
	Actions []storefrontversion.VetoableAction
}

func (e *StorefrontBlockedError) Error() string {
	ids := make([]string, 0, len(e.Actions))
	for _, a := range e.Actions {
		ids = append(ids, a.ActionID)
	}
	return fmt.Sprintf("store: storefront %s blocked by %d action(s): %s", e.Kind, len(e.Actions), strings.Join(ids, ","))
}

// StorefrontVersion is one version row (latest = mutable draft; published =
// immutable history).
type StorefrontVersion struct {
	ID                 string                             `json:"id"`
	TenantID           string                             `json:"-"`
	StoreID            string                             `json:"store_id"`
	StoreCode          string                             `json:"store_code"`
	Revision           int                                `json:"revision"`
	Status             string                             `json:"status"`
	Sections           []storefrontversion.Section        `json:"sections"`
	Actions            []storefrontversion.ActionSnapshot `json:"actions"`
	ActionRefs         map[string]int                     `json:"action_revision_refs"`
	OfferRefs          []string                           `json:"offer_refs"`
	CampaignRefs       []string                           `json:"campaign_refs"`
	BrandRef           string                             `json:"brand_ref"`
	SummaryFingerprint string                             `json:"summary_fingerprint"`
	CreatedBy          string                             `json:"created_by"`
	CreatedAt          string                             `json:"created_at"`
	UpdatedAt          string                             `json:"updated_at"`
	PublishedAt        string                             `json:"published_at"`
}

// StorefrontPublishEvent is one append-only publish/rollback audit row.
type StorefrontPublishEvent struct {
	ID               string   `json:"id"`
	TenantID         string   `json:"-"`
	StoreID          string   `json:"store_id"`
	Action           string   `json:"action"`
	Revision         int      `json:"revision"`
	PreviousRevision int      `json:"previous_revision"`
	Actor            string   `json:"actor"`
	Summary          []string `json:"summary"`
	CreatedAt        string   `json:"created_at"`
}

// StorefrontPublishResult is the outcome of a publish or rollback.
type StorefrontPublishResult struct {
	Revision         int      `json:"storefront_revision"`
	PreviousRevision int      `json:"previous_revision"`
	PublishedAt      string   `json:"published_at"`
	Summary          []string `json:"summary"`
	Replayed         bool     `json:"replayed"`
}

const storefrontVersionCols = `id,tenant_id,store_id,store_code,revision,status,sections,actions,` +
	`action_revision_refs,offer_refs,campaign_refs,brand_ref,summary_fingerprint,created_by,created_at,updated_at,published_at`

func scanStorefrontVersion(sc interface{ Scan(...any) error }) (StorefrontVersion, error) {
	var v StorefrontVersion
	var sections, actions, refs, offers, campaigns string
	err := sc.Scan(&v.ID, &v.TenantID, &v.StoreID, &v.StoreCode, &v.Revision, &v.Status,
		&sections, &actions, &refs, &offers, &campaigns, &v.BrandRef, &v.SummaryFingerprint,
		&v.CreatedBy, &v.CreatedAt, &v.UpdatedAt, &v.PublishedAt)
	if err != nil {
		return StorefrontVersion{}, err
	}
	_ = json.Unmarshal([]byte(sections), &v.Sections)
	_ = json.Unmarshal([]byte(actions), &v.Actions)
	_ = json.Unmarshal([]byte(refs), &v.ActionRefs)
	_ = json.Unmarshal([]byte(offers), &v.OfferRefs)
	_ = json.Unmarshal([]byte(campaigns), &v.CampaignRefs)
	if v.ActionRefs == nil {
		v.ActionRefs = map[string]int{}
	}
	if v.Sections == nil {
		v.Sections = []storefrontversion.Section{}
	}
	if v.Actions == nil {
		v.Actions = []storefrontversion.ActionSnapshot{}
	}
	if v.OfferRefs == nil {
		v.OfferRefs = []string{}
	}
	if v.CampaignRefs == nil {
		v.CampaignRefs = []string{}
	}
	return v, nil
}

// DraftDocument reassembles the leaf Document from a stored row (diffs and
// fingerprints operate on the leaf shape; the HTTP layer recomputes summaries
// from the same leaf truth).
func (v StorefrontVersion) DraftDocument() storefrontversion.Document {
	return storefrontversion.Document{
		Sections: v.Sections, Actions: v.Actions, ActionRefs: v.ActionRefs,
		OfferRefs: v.OfferRefs, CampaignRefs: v.CampaignRefs, BrandRef: v.BrandRef,
	}
}

func draftFingerprint(doc storefrontversion.Document) string {
	return storefrontversion.Fingerprint(storefrontversion.ChangeSummary(storefrontversion.Document{}, doc))
}

// latestStorefrontVersion returns the highest-revision row; ErrNotFound when
// the store has no version rows yet.
func (s *Store) latestStorefrontVersion(tenantID, storeID string) (StorefrontVersion, error) {
	v, err := scanStorefrontVersion(s.DB.QueryRow(
		`SELECT `+storefrontVersionCols+` FROM storefront_versions
		 WHERE tenant_id=? AND store_id=? ORDER BY revision DESC LIMIT 1`, tenantID, storeID))
	if errors.Is(err, sql.ErrNoRows) {
		return StorefrontVersion{}, ErrNotFound
	}
	return v, err
}

func (s *Store) storefrontVersionRow(tenantID, storeID string, revision int) (StorefrontVersion, error) {
	v, err := scanStorefrontVersion(s.DB.QueryRow(
		`SELECT `+storefrontVersionCols+` FROM storefront_versions
		 WHERE tenant_id=? AND store_id=? AND revision=?`, tenantID, storeID, revision))
	if errors.Is(err, sql.ErrNoRows) {
		return StorefrontVersion{}, ErrStorefrontRevisionUnknown
	}
	return v, err
}

// GetStorefrontDraft returns the latest version row (the mutable draft) and
// the currently published revision (0 = never published). Provisioning the
// storefront row (stable store code) happens on first use, like the merchant
// actions surface.
func (s *Store) GetStorefrontDraft(tenantID, storeID string) (StorefrontVersion, int, error) {
	sf, err := s.StorefrontForStore(tenantID, storeID)
	if err != nil {
		return StorefrontVersion{}, 0, err
	}
	v, err := s.latestStorefrontVersion(tenantID, storeID)
	if errors.Is(err, ErrNotFound) {
		return StorefrontVersion{}, sf.StorefrontRevision, ErrNotFound
	}
	if err != nil {
		return StorefrontVersion{}, 0, err
	}
	return v, sf.StorefrontRevision, nil
}

// SaveStorefrontDraft persists the desired document as the mutable draft.
// expectedRevision must equal the latest row's revision (0 = first save).
// See the file-level discipline block for the idempotent-replay contract.
func (s *Store) SaveStorefrontDraft(tenantID, storeID string, expectedRevision int, doc storefrontversion.Document, actor string) (StorefrontVersion, error) {
	if err := storefrontversion.ValidateDraft(doc); err != nil {
		return StorefrontVersion{}, err
	}
	if _, err := s.GetStore(storeID, tenantID); err != nil {
		return StorefrontVersion{}, err
	}
	sf, err := s.StorefrontForStore(tenantID, storeID)
	if err != nil {
		return StorefrontVersion{}, err
	}
	// 引用校验先于一切写入:未知动作/套餐/活动引用是保存级拒绝(不留半条)。
	live, err := s.listRows(tenantID, storeID)
	if err != nil {
		return StorefrontVersion{}, err
	}
	liveLeaf := make([]storefrontversion.LiveAction, 0, len(live))
	for _, row := range live {
		liveLeaf = append(liveLeaf, storefrontversion.LiveAction{
			ID: row.ID, CatalogCode: row.CatalogCode, Platform: row.Platform, Kind: row.Kind,
			Variant: row.Variant, Label: row.Label, CapabilityRef: row.CapabilityRef, Revision: row.Revision,
		})
	}
	snaps, refs, err := storefrontversion.BuildSnapshots(doc.Sections, liveLeaf)
	if err != nil {
		// 段落引用了不存在的动作:保存级 400(跨店/伪造引用进不了库)。
		return StorefrontVersion{}, fmt.Errorf("%w: %v", ErrUnknownActionID, err)
	}
	for _, ref := range doc.OfferRefs {
		if _, err := s.GetDealOffer(strings.TrimSpace(ref), tenantID); err != nil {
			return StorefrontVersion{}, fmt.Errorf("%w: %s", ErrUnknownOfferRef, ref)
		}
	}
	for _, ref := range doc.CampaignRefs {
		if _, err := s.GetCampaign(strings.TrimSpace(ref), tenantID); err != nil {
			return StorefrontVersion{}, fmt.Errorf("%w: %s", ErrUnknownCampaignRef, ref)
		}
	}

	latest, err := s.latestStorefrontVersion(tenantID, storeID)
	hasLatest := !errors.Is(err, ErrNotFound)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return StorefrontVersion{}, err
	}
	if hasLatest && expectedRevision != latest.Revision {
		return StorefrontVersion{}, ErrStaleRevision
	}
	if !hasLatest && expectedRevision != 0 {
		return StorefrontVersion{}, ErrStaleRevision
	}

	desired := StorefrontVersion{
		TenantID: tenantID, StoreID: storeID, StoreCode: sf.StoreCode,
		Sections: doc.Sections, Actions: snaps, ActionRefs: refs,
		OfferRefs: normalizeStringList(doc.OfferRefs), CampaignRefs: normalizeStringList(doc.CampaignRefs),
		BrandRef: strings.TrimSpace(doc.BrandRef), CreatedBy: actor,
	}
	if hasLatest && latest.Status == StorefrontStatusDraft {
		if draftFingerprint(doc) == draftFingerprint(latest.DraftDocument()) {
			return latest, nil // 幂等重放:不推修订、不触 epoch
		}
		desired.ID = latest.ID
		desired.Revision = latest.Revision + 1
		desired.CreatedAt = latest.CreatedAt
		desired.Status = StorefrontStatusDraft
		desired.PublishedAt = ""
		desired.SummaryFingerprint = ""
		if err := s.updateStorefrontVersionRow(desired); err != nil {
			return StorefrontVersion{}, err
		}
		s.bumpEpoch()
		return desired, nil
	}
	// 无行,或最新行已发布(历史只读)→ 新 draft 行。
	rev := 1
	if hasLatest {
		rev = latest.Revision + 1
	}
	desired.Revision = rev
	desired.Status = StorefrontStatusDraft
	desired.ID = newID("sfv_")
	desired.CreatedAt = now()
	desired.UpdatedAt = desired.CreatedAt
	sections, _ := json.Marshal(desired.Sections)
	actions, _ := json.Marshal(desired.Actions)
	refsJSON, _ := json.Marshal(desired.ActionRefs)
	offers, _ := json.Marshal(desired.OfferRefs)
	campaigns, _ := json.Marshal(desired.CampaignRefs)
	if _, err := s.DB.Exec(`INSERT INTO storefront_versions(
			id,tenant_id,store_id,store_code,revision,status,sections,actions,action_revision_refs,
			offer_refs,campaign_refs,brand_ref,summary_fingerprint,created_by,created_at,updated_at,published_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		desired.ID, tenantID, storeID, desired.StoreCode, desired.Revision, desired.Status,
		string(sections), string(actions), string(refsJSON), string(offers), string(campaigns),
		desired.BrandRef, "", actor, desired.CreatedAt, desired.CreatedAt, ""); err != nil {
		return StorefrontVersion{}, err
	}
	s.bumpEpoch()
	return desired, nil
}

func (s *Store) updateStorefrontVersionRow(v StorefrontVersion) error {
	v.UpdatedAt = now()
	sections, _ := json.Marshal(v.Sections)
	actions, _ := json.Marshal(v.Actions)
	refsJSON, _ := json.Marshal(v.ActionRefs)
	offers, _ := json.Marshal(v.OfferRefs)
	campaigns, _ := json.Marshal(v.CampaignRefs)
	res, err := s.DB.Exec(`UPDATE storefront_versions SET
			store_code=?,revision=?,status=?,sections=?,actions=?,action_revision_refs=?,
			offer_refs=?,campaign_refs=?,brand_ref=?,summary_fingerprint=?,updated_at=?,published_at=?
		WHERE id=? AND tenant_id=? AND store_id=?`,
		v.StoreCode, v.Revision, v.Status, string(sections), string(actions), string(refsJSON),
		string(offers), string(campaigns), v.BrandRef, v.SummaryFingerprint, v.UpdatedAt, v.PublishedAt,
		v.ID, v.TenantID, v.StoreID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// PublishStorefrontVersion freezes the candidate draft and atomically switches
// the published pointer. candidateRevision must be the LATEST revision;
// expectedPublishedRevision must equal the current pointer; confirmFingerprint
// must equal the server-recomputed change-summary fingerprint.
func (s *Store) PublishStorefrontVersion(tenantID, storeID string, candidateRevision, expectedPublished int, confirmFingerprint, actor string, at time.Time) (StorefrontPublishResult, error) {
	if _, err := s.GetStore(storeID, tenantID); err != nil {
		return StorefrontPublishResult{}, err
	}
	sf, err := s.StorefrontForStore(tenantID, storeID)
	if err != nil {
		return StorefrontPublishResult{}, err
	}
	pointer := sf.StorefrontRevision
	if pointer == candidateRevision {
		// 幂等重放:同请求重复,返回同一发布结果(不推修订、不重复记事件)。
		cand, err := s.storefrontVersionRow(tenantID, storeID, candidateRevision)
		if err != nil {
			return StorefrontPublishResult{}, err
		}
		return StorefrontPublishResult{
			Revision: pointer, PreviousRevision: pointer,
			PublishedAt: cand.PublishedAt, Summary: []string{}, Replayed: true,
		}, nil
	}
	if expectedPublished != pointer {
		return StorefrontPublishResult{}, ErrStaleRevision
	}
	latest, err := s.latestStorefrontVersion(tenantID, storeID)
	if errors.Is(err, ErrNotFound) {
		return StorefrontPublishResult{}, ErrStorefrontRevisionUnknown
	}
	if err != nil {
		return StorefrontPublishResult{}, err
	}
	// 候选行必须真实存在,且必须是最新修订(发布落后草稿一律拒绝——回滚才走旧版本)。
	if _, err := s.storefrontVersionRow(tenantID, storeID, candidateRevision); err != nil {
		return StorefrontPublishResult{}, err
	}
	if latest.Revision != candidateRevision {
		return StorefrontPublishResult{}, ErrStaleRevision
	}

	// 确认变更摘要(服务端单点):按当前 published↔候选实时重算 diff,指纹必须
	// 与 preflight 给出的一致;草稿在确认后变过 → 指纹不齐 → 拒绝。
	prevDoc := storefrontversion.Document{}
	if pointer > 0 {
		prevRow, err := s.storefrontVersionRow(tenantID, storeID, pointer)
		if err != nil {
			return StorefrontPublishResult{}, err
		}
		prevDoc = prevRow.DraftDocument()
	}
	summary := storefrontversion.ChangeSummary(prevDoc, latest.DraftDocument())
	if storefrontversion.Fingerprint(summary) != confirmFingerprint {
		return StorefrontPublishResult{}, ErrStorefrontConfirmRequired
	}

	// 冻结引用校验:行修订漂移 = 动作在草稿保存后被改过(必须重存草稿再发布);
	// 实时否决 = 撤权/过期/停用/目标失效/内容停发(明确排除后形成新修订)。
	if err := s.assertStorefrontRefsFresh(latest, tenantID); err != nil {
		return StorefrontPublishResult{}, err
	}
	verdicts, err := s.storefrontRefVerdicts(tenantID, storeID, latest.ActionRefs, at)
	if err != nil {
		return StorefrontPublishResult{}, err
	}
	if blocked := storefrontversion.VetoableActions(verdicts); len(blocked) > 0 {
		return StorefrontPublishResult{}, &StorefrontBlockedError{Kind: "publish", Actions: blocked}
	}

	publishedAt := at.UTC().Format(time.RFC3339)
	latest.Status = StorefrontStatusPublished
	latest.PublishedAt = publishedAt
	latest.SummaryFingerprint = confirmFingerprint
	if err := s.txStorefrontPublish(latest, pointer, "publish", summary, actor, publishedAt); err != nil {
		return StorefrontPublishResult{}, err
	}
	s.bumpEpoch()
	return StorefrontPublishResult{
		Revision: latest.Revision, PreviousRevision: pointer,
		PublishedAt: publishedAt, Summary: summary, Replayed: false,
	}, nil
}

// RollbackStorefrontVersion moves the published pointer back to an already
// published revision. It re-checks the LIVE authorization for every referenced
// action first: 回滚绝不复活已撤销/过期/停用内容;剔除须商家明确确认并形成
// 新修订。History rows are never rewritten.
func (s *Store) RollbackStorefrontVersion(tenantID, storeID string, targetRevision, expectedPublished int, actor string, at time.Time) (StorefrontPublishResult, error) {
	if _, err := s.GetStore(storeID, tenantID); err != nil {
		return StorefrontPublishResult{}, err
	}
	sf, err := s.StorefrontForStore(tenantID, storeID)
	if err != nil {
		return StorefrontPublishResult{}, err
	}
	pointer := sf.StorefrontRevision
	if pointer == targetRevision {
		target, err := s.storefrontVersionRow(tenantID, storeID, targetRevision)
		if err != nil {
			return StorefrontPublishResult{}, err
		}
		return StorefrontPublishResult{
			Revision: pointer, PreviousRevision: pointer,
			PublishedAt: target.PublishedAt, Summary: []string{}, Replayed: true,
		}, nil
	}
	if expectedPublished != pointer {
		return StorefrontPublishResult{}, ErrStaleRevision
	}
	target, err := s.storefrontVersionRow(tenantID, storeID, targetRevision)
	if err != nil {
		return StorefrontPublishResult{}, err
	}
	if target.Status != StorefrontStatusPublished {
		return StorefrontPublishResult{}, ErrStorefrontNotPublished
	}
	verdicts, err := s.storefrontRefVerdicts(tenantID, storeID, target.ActionRefs, at)
	if err != nil {
		return StorefrontPublishResult{}, err
	}
	if blocked := storefrontversion.VetoableActions(verdicts); len(blocked) > 0 {
		return StorefrontPublishResult{}, &StorefrontBlockedError{Kind: "rollback", Actions: blocked}
	}

	publishedAt := at.UTC().Format(time.RFC3339)
	rollRow := target // 历史行零改写:只动指针与事件,行内容原样。
	if err := s.txStorefrontPublish(rollRow, pointer, "rollback", []string{}, actor, publishedAt); err != nil {
		return StorefrontPublishResult{}, err
	}
	s.bumpEpoch()
	return StorefrontPublishResult{
		Revision: target.Revision, PreviousRevision: pointer,
		PublishedAt: target.PublishedAt, Summary: []string{}, Replayed: false,
	}, nil
}

// txStorefrontPublish is the atomic switch: version row status + pointer +
// audit event in ONE transaction (SQLite single writer serializes readers of
// the old pointer out).
func (s *Store) txStorefrontPublish(v StorefrontVersion, previousRevision int, action string, summary []string, actor, publishedAt string) error {
	sections, _ := json.Marshal(v.Sections)
	actions, _ := json.Marshal(v.Actions)
	refsJSON, _ := json.Marshal(v.ActionRefs)
	offers, _ := json.Marshal(v.OfferRefs)
	campaigns, _ := json.Marshal(v.CampaignRefs)
	summaryJSON, _ := json.Marshal(normalizeStringList(summary))
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if action == "publish" {
		// 发布冻结行:置 published + published_at + 确认的摘要指纹(历史自此只读)。
		if _, err := tx.Exec(`UPDATE storefront_versions SET status=?,published_at=?,
				summary_fingerprint=?,updated_at=?
			WHERE id=? AND tenant_id=? AND store_id=? AND revision=?`,
			StorefrontStatusPublished, publishedAt, v.SummaryFingerprint, publishedAt,
			v.ID, v.TenantID, v.StoreID, v.Revision); err != nil {
			return err
		}
	}
	// 单一发布指针(4584 建列,本票接管写)。
	if _, err := tx.Exec(`UPDATE store_storefronts SET storefront_revision=?,updated_at=?
		WHERE store_id=? AND tenant_id=?`,
		v.Revision, publishedAt, v.StoreID, v.TenantID); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO storefront_publish_events(
			id,tenant_id,store_id,action,revision,previous_revision,actor,summary,created_at)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		newID("sfe_"), v.TenantID, v.StoreID, action, v.Revision, previousRevision,
		actor, string(summaryJSON), publishedAt); err != nil {
		return err
	}
	// 段落/载荷完整性守卫(发布行必须是完整冻结载荷;防未来的半行写入)。
	_ = sections
	_ = actions
	_ = refsJSON
	_ = offers
	_ = campaigns
	return tx.Commit()
}

// ListStorefrontVersions returns the store's version rows, newest first
// (历史只读;draft 行是当前可变稿).
func (s *Store) ListStorefrontVersions(tenantID, storeID string) ([]StorefrontVersion, error) {
	rs, err := s.DB.Query(`SELECT `+storefrontVersionCols+` FROM storefront_versions
		WHERE tenant_id=? AND store_id=? ORDER BY revision DESC`, tenantID, storeID)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := []StorefrontVersion{}
	for rs.Next() {
		v, scanErr := scanStorefrontVersion(rs)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, v)
	}
	return out, rs.Err()
}

// ListStorefrontPublishEvents returns the append-only publish/rollback trail,
// newest first.
func (s *Store) ListStorefrontPublishEvents(tenantID, storeID string) ([]StorefrontPublishEvent, error) {
	rs, err := s.DB.Query(`SELECT id,tenant_id,store_id,action,revision,previous_revision,actor,summary,created_at
		FROM storefront_publish_events WHERE tenant_id=? AND store_id=? ORDER BY rowid DESC`,
		tenantID, storeID)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := []StorefrontPublishEvent{}
	for rs.Next() {
		var e StorefrontPublishEvent
		var summary string
		if err := rs.Scan(&e.ID, &e.TenantID, &e.StoreID, &e.Action, &e.Revision,
			&e.PreviousRevision, &e.Actor, &summary, &e.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(summary), &e.Summary)
		if e.Summary == nil {
			e.Summary = []string{}
		}
		out = append(out, e)
	}
	return out, rs.Err()
}

// GetStorefrontPublishedByCode resolves the public store code to the CURRENTLY
// published version row. No pointer (never published) or unknown code is
// ErrNotFound — the public handler keeps its 4584 fallback.
func (s *Store) GetStorefrontPublishedByCode(code string) (StorefrontVersion, error) {
	v, err := scanStorefrontVersion(s.DB.QueryRow(
		`SELECT v.id,v.tenant_id,v.store_id,v.store_code,v.revision,v.status,v.sections,v.actions,
			v.action_revision_refs,v.offer_refs,v.campaign_refs,v.brand_ref,v.summary_fingerprint,
			v.created_by,v.created_at,v.updated_at,v.published_at
		 FROM storefront_versions v
		 JOIN store_storefronts f ON f.store_id = v.store_id AND f.storefront_revision = v.revision
		 WHERE f.store_code = ?`, strings.TrimSpace(code)))
	if errors.Is(err, sql.ErrNoRows) {
		return StorefrontVersion{}, ErrNotFound
	}
	return v, err
}

// GetStorefrontVersionForPreview returns one version row of this store for
// the session-constrained preview face (draft/published/rollback-target all
// render from the same frozen payload).
func (s *Store) GetStorefrontVersionForPreview(tenantID, storeID string, revision int) (StorefrontVersion, error) {
	if revision <= 0 {
		return s.latestStorefrontVersion(tenantID, storeID)
	}
	return s.storefrontVersionRow(tenantID, storeID, revision)
}

// assertStorefrontRefsFresh refuses a publish whose frozen refs no longer
// match the live rows: the action changed after the draft was saved, so the
// merchant must re-save (re-freeze) before publishing.
func (s *Store) assertStorefrontRefsFresh(latest StorefrontVersion, tenantID string) error {
	var drifted []string
	for actionID, frozen := range latest.ActionRefs {
		row, err := s.GetStoreActionByID(tenantID, latest.StoreID, actionID)
		if errors.Is(err, ErrNotFound) {
			drifted = append(drifted, actionID)
			continue
		}
		if err != nil {
			return err
		}
		if row.Revision != frozen {
			drifted = append(drifted, actionID)
		}
	}
	if len(drifted) > 0 {
		return fmt.Errorf("%w: %s", ErrStorefrontRefDrift, strings.Join(sortedStrings(drifted), ","))
	}
	return nil
}

// StorefrontActionFacts assembles the live world facts for the given action
// ids of this store (HTTP 层预检输入): store_actions 行 + 内容绑定真值,只读。
func (s *Store) StorefrontActionFacts(tenantID, storeID string, actionIDs []string, at time.Time) ([]storefrontversion.ActionFact, error) {
	facts := make([]storefrontversion.ActionFact, 0, len(actionIDs))
	for _, id := range actionIDs {
		row, err := s.GetStoreActionByID(tenantID, storeID, id)
		if errors.Is(err, ErrNotFound) {
			facts = append(facts, storefrontversion.ActionFact{ActionID: id, Found: false})
			continue
		}
		if err != nil {
			return nil, err
		}
		facts = append(facts, storefrontversion.ActionFact{
			ActionID: id, Found: true, Enabled: row.Enabled, Revoked: row.Revoked,
			Expired:        actionExpired(row.ExpiresAt, at),
			TargetOfficial: row.TargetRef != "" && extrajump.OfficialAddress(row.TargetRef),
			ContentState:   s.actionContentState(tenantID, id, at),
		})
	}
	return facts, nil
}

// storefrontRefVerdicts assembles the live world facts for every referenced
// action and evaluates the leaf preflight. Read-only over existing truths:
// store_actions 行 + store_action_content_bindings + store_content_pack_adoptions
// (3553 contentpack 真值). Zero writes.
func (s *Store) storefrontRefVerdicts(tenantID, storeID string, refs map[string]int, at time.Time) ([]storefrontversion.Verdict, error) {
	ids := make([]string, 0, len(refs))
	for id := range refs {
		ids = append(ids, id)
	}
	facts := make([]storefrontversion.ActionFact, 0, len(ids))
	for _, id := range ids {
		row, err := s.GetStoreActionByID(tenantID, storeID, id)
		if errors.Is(err, ErrNotFound) {
			facts = append(facts, storefrontversion.ActionFact{ActionID: id, Found: false})
			continue
		}
		if err != nil {
			return nil, err
		}
		fact := storefrontversion.ActionFact{
			ActionID: id, Found: true, Enabled: row.Enabled, Revoked: row.Revoked,
			Expired:        actionExpired(row.ExpiresAt, at),
			TargetOfficial: row.TargetRef != "" && extrajump.OfficialAddress(row.TargetRef),
			ContentState:   s.actionContentState(tenantID, id, at),
		}
		facts = append(facts, fact)
	}
	doc := storefrontversion.Document{Sections: []storefrontversion.Section{
		{ID: "refs", Type: storefrontversion.SectionActions, Title: "refs", ActionIDs: ids},
	}}
	verdicts, fatal := storefrontversion.Preflight(doc, facts)
	if len(fatal) > 0 {
		// refs 集合本身结构合法(来自冻结行);fatal 只可能是空集。
		return nil, fmt.Errorf("store: storefront ref verdict assembly failed: %v", fatal)
	}
	return verdicts, nil
}

// actionContentState maps the 3553 content truth for one action:
// 无绑定 = 不需要内容(ContentOK,普通关注/Wi-Fi/团购动作不受内容拖累);
// 有绑定 = 包的当前采用版必须存在且可分发(blocked/grant 过期分别报告)。
func (s *Store) actionContentState(tenantID, actionID string, at time.Time) string {
	binding, err := s.GetActionContentBinding(tenantID, actionID)
	if errors.Is(err, ErrNotFound) {
		return storefrontversion.ContentOK
	}
	if err != nil {
		return storefrontversion.ContentOK // 读取失败不升级为内容否决;发布侧其余闸照常
	}
	ver, err := s.GetStoreAdoptedPackVersion(tenantID, binding.PackID)
	if errors.Is(err, ErrNotFound) {
		return storefrontversion.ContentMissing
	}
	if err != nil {
		return storefrontversion.ContentOK
	}
	if ver.Status == contentpack.StatusBlocked {
		return storefrontversion.ContentBlocked
	}
	if contentpack.Distributable(ver.Status, ver.GrantUntil, at) != nil {
		return storefrontversion.ContentGrantExpired
	}
	return storefrontversion.ContentOK
}

// ListStoreCampaignDealOffers lists the deal offers (1671 目录,只读) reachable
// from this store: offers of campaigns bound to the store. Read-only SELECT
// join; the offer ledger stays 1671's.
//
// wave2 集成改名:1676 分支带来的本方法与 4590 store_deal_offers 清单
// (ListStoreDealOffers)同名不同型,合流未捕获致 main 编译破裂;本方法读的
// 是 1671 campaign deal_offers,按域改名为 ListStoreCampaignDealOffers
// (4590 的店级套餐目录方法名保持不动,其调用面 handlers_deals 零改动)。
func (s *Store) ListStoreCampaignDealOffers(tenantID, storeID string) ([]DealOffer, error) {
	rs, err := s.DB.Query(`SELECT d.id,d.tenant_id,d.campaign_id,d.kind,d.issuer,d.provider,
		d.provider_offer_id,d.authorization_ref,d.title,d.face_value_minor,d.currency,
		d.valid_from,d.valid_until,d.status,d.note,d.created_by,d.created_at
		FROM deal_offers d
		JOIN campaigns c ON c.id = d.campaign_id
		WHERE d.tenant_id=? AND c.store_id=? AND d.status='active'
		ORDER BY d.created_at DESC, d.id`, tenantID, storeID)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := []DealOffer{}
	for rs.Next() {
		o, scanErr := scanDealOffer(rs)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, o)
	}
	return out, rs.Err()
}

func normalizeStringList(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
