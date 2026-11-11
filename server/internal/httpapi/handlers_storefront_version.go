package httpapi

// HUI-1676 门店首页版本域 HTTP 面（B01 四端点 + 预览/审计读面）。
//
// 纪律：
//   - 开关：FEATURE_STOREFRONT（4584 建的同一开关，本票接管整个门店版本域）
//     —— off = 路由不注册 + 本 gate 再答统一 404（双保险同款）；
//   - 授权：复用既有 authz 门店作用域（GET=ReadRecord、写=Update）——
//     org_owner 全门店、store_manager 本店（HUI-4593 维度天然生效），无新权限体系；
//   - 服务端单点：逐动作预检与发布否决在服务端完成（storefrontversion 纯函数 +
//     store 层事务内重校验），前端隐藏按钮/篡改预检结果不算数；
//   - 发布确认：publish 必须回带 preflight 给出的 summary_fingerprint（服务端
//     按当前 published↔候选实时重算 diff），勾选框只是 UX，指纹才是闸；
//   - 拒绝不静默：受限/缺配置动作逐项带 state+reason_codes+修复入口返回，
//     商家在草稿里明确排除后形成新修订才能发布 —— 绝不静默删项或把受限项标可用；
//   - 旧参与保护：本域零写参与/奖励/券表（存储面只有 storefront_versions 与
//     storefront_publish_events），换版/回滚不重写任何参与快照。

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/storefrontversion"
)

// storefrontGate answers uniform 404 when FEATURE_STOREFRONT is off.
func (s *Server) storefrontGate(w http.ResponseWriter) bool {
	if s.Cfg.FeatureStorefront {
		return true
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found", "message": "resource not found"})
	return false
}

type storefrontSectionIn struct {
	ID        string   `json:"id"`
	Type      string   `json:"type"`
	Title     string   `json:"title"`
	ActionIDs []string `json:"action_ids"`
	OfferRefs []string `json:"offer_refs"`
}

type storefrontDraftPut struct {
	ExpectedRevision *int                  `json:"expected_revision"`
	Sections         []storefrontSectionIn `json:"sections"`
	OfferRefs        []string              `json:"offer_refs"`
	CampaignRefs     []string              `json:"campaign_refs"`
	BrandRef         string                `json:"brand_ref"`
}

// GET /api/v1/stores/{id}/storefront-draft — the mutable draft + the published
// pointer (revision 0 = never published; the draft starts empty).
func (s *Server) handleStorefrontDraftGet(w http.ResponseWriter, r *http.Request) {
	if !s.storefrontGate(w) {
		return
	}
	c := callerFrom(r)
	rec, ok := s.storeForActions(w, r, c)
	if !ok {
		return
	}
	if !s.requireScopedAction(c, authz.ActionReadRecord, authz.RecordScope{TenantID: c.Member.TenantID, StoreID: rec.ID}, true, w) {
		return
	}
	draft, published, err := s.St.GetStorefrontDraft(c.Member.TenantID, rec.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusOK, s.storefrontDraftDocument(store.StorefrontVersion{}, published, rec))
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "storefront draft lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, s.storefrontDraftDocument(draft, published, rec))
}

// PUT /api/v1/stores/{id}/storefront-draft — idempotent save with optimistic
// concurrency (expected_revision → 409 stale_version on drift).
func (s *Server) handleStorefrontDraftPut(w http.ResponseWriter, r *http.Request) {
	if !s.storefrontGate(w) {
		return
	}
	c := callerFrom(r)
	rec, ok := s.storeForActions(w, r, c)
	if !ok {
		return
	}
	if !s.requireScopedAction(c, authz.ActionUpdate, authz.RecordScope{TenantID: c.Member.TenantID, StoreID: rec.ID}, false, w) {
		return
	}
	var in storefrontDraftPut
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil || in.ExpectedRevision == nil {
		fail(w, http.StatusBadRequest, "bad_request", "expected_revision and sections are required")
		return
	}
	doc := storefrontversion.Document{
		OfferRefs:    in.OfferRefs,
		CampaignRefs: in.CampaignRefs,
		BrandRef:     in.BrandRef,
	}
	for _, sec := range in.Sections {
		doc.Sections = append(doc.Sections, storefrontversion.Section{
			ID: sec.ID, Type: sec.Type, Title: sec.Title,
			ActionIDs: sec.ActionIDs, OfferRefs: sec.OfferRefs,
		})
	}
	draft, err := s.St.SaveStorefrontDraft(c.Member.TenantID, rec.ID, *in.ExpectedRevision, doc, c.Member.PrincipalRef)
	switch {
	case errors.Is(err, store.ErrStaleRevision):
		fail(w, http.StatusConflict, "stale_version", "draft was changed by someone else; reload and retry")
		return
	case errors.Is(err, store.ErrUnknownActionID):
		fail(w, http.StatusBadRequest, "unknown_action_id", "a section references an action that does not exist in this store")
		return
	case errors.Is(err, store.ErrUnknownOfferRef):
		fail(w, http.StatusBadRequest, "unknown_offer_ref", "an offer reference does not exist in this tenant")
		return
	case errors.Is(err, store.ErrUnknownCampaignRef):
		fail(w, http.StatusBadRequest, "unknown_campaign_ref", "a campaign reference does not exist in this tenant")
		return
	case err != nil:
		fail(w, http.StatusBadRequest, "invalid_sections", err.Error())
		return
	}
	_, published, gerr := s.St.GetStorefrontDraft(c.Member.TenantID, rec.ID)
	if gerr != nil && !errors.Is(gerr, store.ErrNotFound) {
		fail(w, http.StatusInternalServerError, "internal", "storefront draft lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, s.storefrontDraftDocument(draft, published, rec))
}

// POST /api/v1/stores/{id}/storefront/preflight — per-action verdicts on the
// current draft plus the page-level fatal list and the change summary the
// merchant must confirm before publishing. Read-only.
func (s *Server) handleStorefrontPreflight(w http.ResponseWriter, r *http.Request) {
	if !s.storefrontGate(w) {
		return
	}
	c := callerFrom(r)
	rec, ok := s.storeForActions(w, r, c)
	if !ok {
		return
	}
	if !s.requireScopedAction(c, authz.ActionReadRecord, authz.RecordScope{TenantID: c.Member.TenantID, StoreID: rec.ID}, true, w) {
		return
	}
	draft, published, err := s.St.GetStorefrontDraft(c.Member.TenantID, rec.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusOK, map[string]any{
			"draft_revision": 0, "published_revision": published,
			"fatal": []string{}, "actions": []any{},
			"change_summary": []string{}, "summary_fingerprint": "", "publishable": false,
		})
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "storefront draft lookup failed")
		return
	}
	verdicts, fatal := s.storefrontPreflight(c.Member.TenantID, rec.ID, draft, time.Now().UTC())
	if fatal == nil {
		fatal = []string{}
	}
	if verdicts == nil {
		verdicts = []storefrontversion.Verdict{}
	}
	summary, fingerprint := s.storefrontChangeSummary(c.Member.TenantID, rec.ID, draft, published)
	publishable := len(fatal) == 0 && len(storefrontversion.VetoableActions(verdicts)) == 0
	writeJSON(w, http.StatusOK, map[string]any{
		"draft_revision": draft.Revision, "published_revision": published,
		"fatal": fatal, "actions": verdicts,
		"change_summary": summary, "summary_fingerprint": fingerprint,
		"publishable": publishable,
	})
}

// POST /api/v1/stores/{id}/storefront/publish — freeze the candidate draft and
// atomically switch the published pointer. The confirmed change summary
// fingerprint is mandatory (确认变更摘要).
func (s *Server) handleStorefrontPublish(w http.ResponseWriter, r *http.Request) {
	if !s.storefrontGate(w) {
		return
	}
	s.handleStorefrontPublishRollback(w, r, "publish")
}

// POST /api/v1/stores/{id}/storefront/rollback — move the published pointer
// back to an already published revision, re-checking live revocation first.
func (s *Server) handleStorefrontRollback(w http.ResponseWriter, r *http.Request) {
	if !s.storefrontGate(w) {
		return
	}
	s.handleStorefrontPublishRollback(w, r, "rollback")
}

func (s *Server) handleStorefrontPublishRollback(w http.ResponseWriter, r *http.Request, kind string) {
	c := callerFrom(r)
	rec, ok := s.storeForActions(w, r, c)
	if !ok {
		return
	}
	if !s.requireScopedAction(c, authz.ActionUpdate, authz.RecordScope{TenantID: c.Member.TenantID, StoreID: rec.ID}, false, w) {
		return
	}
	var in struct {
		CandidateRevision         *int   `json:"candidate_revision"`
		TargetRevision            *int   `json:"target_revision"`
		ExpectedPublishedRevision *int   `json:"expected_published_revision"`
		SummaryFingerprint        string `json:"summary_fingerprint"`
		ConfirmChanges            bool   `json:"confirm_changes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.ExpectedPublishedRevision == nil {
		fail(w, http.StatusBadRequest, "bad_request", "expected_published_revision is required")
		return
	}
	rev, want := 0, ""
	switch kind {
	case "publish":
		if in.CandidateRevision == nil || !in.ConfirmChanges {
			fail(w, http.StatusBadRequest, "bad_request", "candidate_revision and confirm_changes are required")
			return
		}
		rev, want = *in.CandidateRevision, strings.TrimSpace(in.SummaryFingerprint)
	case "rollback":
		if in.TargetRevision == nil {
			fail(w, http.StatusBadRequest, "bad_request", "target_revision is required")
			return
		}
		rev = *in.TargetRevision
	}
	var (
		res store.StorefrontPublishResult
		err error
	)
	if kind == "publish" {
		res, err = s.St.PublishStorefrontVersion(c.Member.TenantID, rec.ID, rev, *in.ExpectedPublishedRevision, want, c.Member.PrincipalRef, time.Now().UTC())
	} else {
		res, err = s.St.RollbackStorefrontVersion(c.Member.TenantID, rec.ID, rev, *in.ExpectedPublishedRevision, c.Member.PrincipalRef, time.Now().UTC())
	}
	switch {
	case errors.Is(err, store.ErrStaleRevision):
		fail(w, http.StatusConflict, "stale_version", "the published pointer moved; reload and retry")
		return
	case errors.Is(err, store.ErrStorefrontConfirmRequired):
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   "confirm_required",
			"message": "the change summary changed after confirmation; run preflight again and confirm the new diff",
		})
		return
	case errors.Is(err, store.ErrStorefrontRefDrift):
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   "action_changed",
			"message": "an action changed after the draft was saved; re-save the draft and confirm the diff again",
		})
		return
	case errors.Is(err, store.ErrStorefrontRevisionUnknown):
		fail(w, http.StatusNotFound, "not_found", "that storefront revision does not exist")
		return
	case errors.Is(err, store.ErrStorefrontNotPublished):
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   "not_published",
			"message": "rollback targets must be published versions",
		})
		return
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "not_found", "store not found")
		return
	}
	var blocked *store.StorefrontBlockedError
	if errors.As(err, &blocked) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":   "actions_not_ready",
			"kind":    blocked.Kind,
			"actions": blocked.Actions,
			"message": "exclude the listed actions in the draft (forming a new revision) before publishing",
		})
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "storefront publish failed")
		return
	}
	draft, published, gerr := s.St.GetStorefrontDraft(c.Member.TenantID, rec.ID)
	if gerr != nil && !errors.Is(gerr, store.ErrNotFound) {
		fail(w, http.StatusInternalServerError, "internal", "storefront draft lookup failed")
		return
	}
	doc := s.storefrontDraftDocument(draft, published, rec)
	if res.Replayed {
		doc["replayed"] = true
	}
	doc["result"] = res
	writeJSON(w, http.StatusOK, doc)
}

// GET /api/v1/stores/{id}/storefront/revisions — version rows + the append-only
// publish/rollback trail (回滚 UI 数据源 + 可审计发布版本).
func (s *Server) handleStorefrontRevisions(w http.ResponseWriter, r *http.Request) {
	if !s.storefrontGate(w) {
		return
	}
	c := callerFrom(r)
	rec, ok := s.storeForActions(w, r, c)
	if !ok {
		return
	}
	if !s.requireScopedAction(c, authz.ActionReadRecord, authz.RecordScope{TenantID: c.Member.TenantID, StoreID: rec.ID}, true, w) {
		return
	}
	versions, err := s.St.ListStorefrontVersions(c.Member.TenantID, rec.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "storefront versions lookup failed")
		return
	}
	events, err := s.St.ListStorefrontPublishEvents(c.Member.TenantID, rec.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "storefront events lookup failed")
		return
	}
	_, published, derr := s.St.GetStorefrontDraft(c.Member.TenantID, rec.ID)
	if derr != nil && !errors.Is(derr, store.ErrNotFound) {
		fail(w, http.StatusInternalServerError, "internal", "storefront draft lookup failed")
		return
	}
	if versions == nil {
		versions = []store.StorefrontVersion{}
	}
	if events == nil {
		events = []store.StorefrontPublishEvent{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"published_revision": published,
		"items":              versions,
		"events":             events,
	})
}

// GET /api/v1/stores/{id}/storefront-preview?revision=N — the session-
// constrained preview data face: the SAME whitelist projection the public
// endpoint serves, for any revision (default = current draft). 手机预览渲染
// 本端点的真实数据面并叠加明显预览标识；预览零写路径、零奖励消耗。
func (s *Server) handleStorefrontPreview(w http.ResponseWriter, r *http.Request) {
	if !s.storefrontGate(w) {
		return
	}
	c := callerFrom(r)
	rec, ok := s.storeForActions(w, r, c)
	if !ok {
		return
	}
	if !s.requireScopedAction(c, authz.ActionReadRecord, authz.RecordScope{TenantID: c.Member.TenantID, StoreID: rec.ID}, true, w) {
		return
	}
	revision := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("revision")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			fail(w, http.StatusBadRequest, "bad_request", "revision must be a non-negative integer")
			return
		}
		revision = n
	}
	ver, err := s.St.GetStorefrontVersionForPreview(c.Member.TenantID, rec.ID, revision)
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrStorefrontRevisionUnknown) {
		fail(w, http.StatusNotFound, "not_found", "that storefront revision does not exist")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "storefront preview lookup failed")
		return
	}
	sf, err := s.St.StorefrontForStore(c.Member.TenantID, rec.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "storefront could not be provisioned")
		return
	}
	payload := s.storefrontPublicPayload(rec, ver, sf.OpeningHours, time.Now().UTC())
	payload["preview"] = true
	writeJSON(w, http.StatusOK, payload)
}

// ---- shared assembly ---------------------------------------------------------

// handleStoreDealOffersList serves GET /api/v1/stores/{id}/deal-offers — the
// M3 offers-section picker source (套餐区数据面): active deal offers reachable
// from THIS store (campaigns bound to it), read-only. Whitelist projection:
// the merchant form needs id/title/kind/issuer/face value; internal fields
// (authorization_ref / provider_offer_id / campaign wiring / tenant) never
// leave the server — the offer ledger itself stays HUI-1671's.
func (s *Server) handleStoreDealOffersList(w http.ResponseWriter, r *http.Request) {
	if !s.storefrontGate(w) {
		return
	}
	c := callerFrom(r)
	rec, ok := s.storeForActions(w, r, c)
	if !ok {
		return
	}
	if !s.requireScopedAction(c, authz.ActionReadRecord, authz.RecordScope{TenantID: c.Member.TenantID, StoreID: rec.ID}, true, w) {
		return
	}
	offers, err := s.St.ListStoreCampaignDealOffers(c.Member.TenantID, rec.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "deal offers lookup failed")
		return
	}
	items := make([]map[string]any, 0, len(offers))
	for _, o := range offers {
		items = append(items, map[string]any{
			"id": o.ID, "title": o.Title, "kind": o.Kind,
			"issuer": o.Issuer, "face_value_minor": o.FaceValueMinor,
			"currency": o.Currency, "valid_until": o.ValidUntil, "status": o.Status,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// storefrontPreflight runs the leaf preflight over the draft with live facts.
func (s *Server) storefrontPreflight(tenantID, storeID string, draft store.StorefrontVersion, at time.Time) ([]storefrontversion.Verdict, []string) {
	var ids []string
	for _, sec := range draft.Sections {
		ids = append(ids, sec.ActionIDs...)
	}
	if len(ids) == 0 {
		return []storefrontversion.Verdict{}, storefrontversion.FatalProblems(draft.DraftDocument())
	}
	facts, err := s.St.StorefrontActionFacts(tenantID, storeID, ids, at)
	if err != nil {
		return nil, []string{"facts_assembly_failed"}
	}
	return storefrontversion.Preflight(draft.DraftDocument(), facts)
}

// storefrontChangeSummary recomputes the diff between the currently published
// version (empty when never published) and the draft, pinning the fingerprint
// the publish call must echo.
func (s *Server) storefrontChangeSummary(tenantID, storeID string, draft store.StorefrontVersion, published int) ([]string, string) {
	prev := storefrontversion.Document{}
	if published > 0 {
		row, err := s.St.GetStorefrontVersionForPreview(tenantID, storeID, published)
		if err == nil {
			prev = row.DraftDocument()
		}
	}
	summary := storefrontversion.ChangeSummary(prev, draft.DraftDocument())
	return summary, storefrontversion.Fingerprint(summary)
}

func (s *Server) storefrontDraftDocument(draft store.StorefrontVersion, published int, rec store.StoreRecord) map[string]any {
	if draft.ID == "" {
		return map[string]any{
			"store_id": rec.ID, "store_name": rec.Name, "store_code": "",
			"revision": 0, "status": "draft",
			"sections": []any{}, "actions": []any{}, "action_revision_refs": map[string]int{},
			"offer_refs": []any{}, "campaign_refs": []any{}, "brand_ref": "",
			"published_revision": published,
		}
	}
	return map[string]any{
		"store_id": rec.ID, "store_name": rec.Name, "store_code": draft.StoreCode,
		"revision": draft.Revision, "status": draft.Status,
		"sections": draft.Sections, "actions": draft.Actions, "action_revision_refs": draft.ActionRefs,
		"offer_refs": draft.OfferRefs, "campaign_refs": draft.CampaignRefs, "brand_ref": draft.BrandRef,
		"published_revision": published,
	}
}
