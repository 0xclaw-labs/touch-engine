// handlers_content_packs.go: HUI-3553 活动内容包的 HTTP 面。
//
// 纪律:
//   - 两面分离:管理面端点不加 FEATURE 开关(仓内惯例);顾客可触达的快照读
//     面走 FEATURE_CONTENT_PACKS(默认 off;off = 路由不注册 + gate 统一 404
//     —— 与 FEATURE_ASSET_LIB 同一双保险写法);
//   - 授权:管理动作(建包/建版本/明确采用/阻止新分发/回流候选/制作交接/
//     受控媒体交接铸造)走 authz.ActionManageContentPacks(仅 org_owner);
//     查询与交接兑换按既有业务角色(ActionReadList/ActionReadRecord);
//   - 商家面不出现技术字段:建版本只收素材 ID(asset_id)与语义声明,引用快照
//     由存储层从 lib_assets 落定(media_missing = 422);
//   - 「帮我制作」诚实降级:引擎未接 = 503 handoff_unavailable;brief 仍落库
//     去重(同幂等键重放同一行),绝不伪造可用能力;
//   - 媒体交接 = 受控短时效 URL(默认 10 分钟、上限 1 小时),兑换只回引用
//     元数据(asset_ref/sha256/content-type/disposition),文件字节零过本服务;
//   - 公共快照只回公开字段白名单;blocked/授权过期 = 409 拒新分发;采用版
//     两次读取逐字段一致(不静默变化)。
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
	"github.com/bianjiefilm/touch-engine/server/internal/mxopsview"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// contentPackGate answers uniform 404 when FEATURE_CONTENT_PACKS is off (顾客
// 可触达面登记制开关;管理面不经过本 gate).
func (s *Server) contentPackGate(w http.ResponseWriter) bool {
	if s.Cfg.FeatureContentPacks {
		return true
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found", "message": "resource not found"})
	return false
}

// packTenantAction authorizes a tenant-level content-pack action (packs and
// versions carry no store binding of their own — same as the asset lib).
func (s *Server) packTenantAction(c *caller, action authz.Action, w http.ResponseWriter) bool {
	return s.requireAction(c, action, authz.RecordScope{TenantID: c.Member.TenantID}, w)
}

// invalidPack422 maps a contentpack.ValidationError to its HTTP code:
// 422 for declaration failures, 410 for expired handoffs, 400 for invalid
// tokens. Returns false when err is not a domain validation error.
func invalidPack422(w http.ResponseWriter, err error) bool {
	var ve *contentpack.ValidationError
	if !errors.As(err, &ve) {
		return false
	}
	status := http.StatusUnprocessableEntity
	switch ve.Reason {
	case contentpack.ReasonHandoffExpired:
		status = http.StatusGone
	case contentpack.ReasonHandoffInvalid:
		status = http.StatusBadRequest
	}
	fail(w, status, ve.Reason, "content pack rejected: "+ve.Reason+" ("+ve.Field+")")
	return true
}

// decodePackBody decodes a JSON body with unknown fields refused; an empty
// body decodes to the zero value.
func decodePackBody(r *http.Request, in any) (bool, error) {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(in)
	if errors.Is(err, io.EOF) {
		return true, nil
	}
	return err == nil, err
}

func packStoreFail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrDuplicateContentPack):
		fail(w, http.StatusConflict, "duplicate_content_pack", "this campaign already has a pack with this title")
	case errors.Is(err, store.ErrDuplicatePackVersion):
		fail(w, http.StatusConflict, "duplicate_idempotency_key", "this idempotency key already produced a version; retry returns the same row")
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "not_found", "resource not found")
	default:
		if !invalidPack422(w, err) {
			fail(w, http.StatusInternalServerError, "internal", "content pack write failed")
		}
	}
}

// ---- version spec body (商家语义字段;引用快照由存储层落定) ---------------------------

type platformVersionBody struct {
	Platform      string `json:"platform"`
	Note          string `json:"note"`
	SourceVersion string `json:"source_version"`
}

type markingBody struct {
	AdRequired         bool `json:"ad_required"`
	IncentiveDisclosed bool `json:"incentive_disclosed"`
	AIGenerated        bool `json:"ai_generated"`
}

type versionSpecBody struct {
	ContentKind       string                `json:"content_kind"`
	VideoAssetID      string                `json:"video_asset_id"`
	CoverAssetID      string                `json:"cover_asset_id"`
	ImageTextAssetIDs []string              `json:"image_text_asset_ids"`
	Title             string                `json:"title"`
	Body              string                `json:"body"`
	Topics            []string              `json:"topics"`
	StoreName         string                `json:"store_name"`
	PriceNote         string                `json:"price_note"`
	ValidNote         string                `json:"valid_note"`
	PlatformVersions  []platformVersionBody `json:"platform_versions"`
	GrantRefs         []string              `json:"grant_refs"`
	GrantUntil        string                `json:"grant_until"`
	Marking           markingBody           `json:"marking"`
	Source            string                `json:"source"`
	IdempotencyKey    string                `json:"idempotency_key"`
	Status            string                `json:"status"`
}

func (b versionSpecBody) spec() contentpack.VersionSpec {
	spec := contentpack.VersionSpec{
		ContentKind:    contentpack.ContentKind(b.ContentKind),
		Title:          b.Title,
		Body:           b.Body,
		Topics:         b.Topics,
		StoreName:      b.StoreName,
		PriceNote:      b.PriceNote,
		ValidNote:      b.ValidNote,
		GrantRefs:      b.GrantRefs,
		GrantUntil:     b.GrantUntil,
		Marking:        contentpack.Marking(b.Marking),
		Source:         contentpack.Source(b.Source),
		IdempotencyKey: b.IdempotencyKey,
	}
	if b.VideoAssetID != "" {
		spec.Video = contentpack.AssetSnapshot{AssetID: b.VideoAssetID}
	}
	if b.CoverAssetID != "" {
		spec.Cover = contentpack.AssetSnapshot{AssetID: b.CoverAssetID}
	}
	for _, id := range b.ImageTextAssetIDs {
		spec.ImageText = append(spec.ImageText, contentpack.AssetSnapshot{AssetID: id})
	}
	for _, pv := range b.PlatformVersions {
		spec.PlatformVersions = append(spec.PlatformVersions, contentpack.PlatformVersion{
			Platform: pv.Platform, Note: pv.Note, SourceVersion: pv.SourceVersion,
		})
	}
	return spec
}

func versionView(v store.ContentPackVersion) map[string]any {
	return map[string]any{
		"id": v.ID, "pack_id": v.PackID, "version_no": v.VersionNo,
		"content_kind": v.ContentKind, "status": v.Status, "source": v.Source,
		"video": videoRefView(v.Video), "cover": coverRefView(v.Cover),
		"image_text": v.ImageText,
		"title":      v.Title, "body": v.Body, "topics": v.Topics,
		"store_name": v.StoreName, "price_note": v.PriceNote, "valid_note": v.ValidNote,
		"platform_versions": v.PlatformVersions, "grant_refs": v.GrantRefs,
		"grant_until": v.GrantUntil, "marking": v.Marking,
		"blocked_reason": v.BlockedReason, "idempotency_key": v.IdempotencyKey,
		"created_by": v.CreatedBy, "created_at": v.CreatedAt,
	}
}

func videoRefView(a contentpack.AssetSnapshot) any {
	if a.IsEmpty() {
		return nil
	}
	return a
}

func coverRefView(a contentpack.AssetSnapshot) any {
	if a.IsEmpty() {
		return nil
	}
	return a
}

// ---- packs -------------------------------------------------------------------

// POST /api/v1/campaigns/{id}/content-packs
func (s *Server) handleContentPackCreate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.packTenantAction(c, authz.ActionManageContentPacks, w) {
		return
	}
	var in struct {
		Title   string `json:"title"`
		StoreID string `json:"store_id"`
	}
	ok, err := decodePackBody(r, &in)
	if err != nil || !ok {
		fail(w, http.StatusBadRequest, "bad_request", "invalid or disallowed field in body")
		return
	}
	p, err := s.St.CreateContentPack(c.Member.TenantID, r.PathValue("id"), strings.TrimSpace(in.StoreID), in.Title, c.Member.PrincipalRef)
	if err != nil {
		packStoreFail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, packView(p))
}

func packView(p store.ContentPack) map[string]any {
	return map[string]any{
		"id": p.ID, "tenant_id": p.TenantID, "campaign_id": p.CampaignID,
		"store_id": p.StoreID, "title": p.Title,
		"created_by": p.CreatedBy, "created_at": p.CreatedAt, "updated_at": p.UpdatedAt,
	}
}

// GET /api/v1/campaigns/{id}/content-packs — the content panel data face:
// packs with their versions and current adoption pointers in one read.
func (s *Server) handleContentPackList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.packTenantAction(c, authz.ActionReadList, w) {
		return
	}
	tenantID := c.Member.TenantID
	campaignID := r.PathValue("id")
	if _, err := s.St.GetCampaign(campaignID, tenantID); err != nil {
		packStoreFail(w, err)
		return
	}
	packs, err := s.St.ListContentPacks(tenantID, campaignID)
	if err != nil {
		packStoreFail(w, err)
		return
	}
	adoptions, err := s.St.ListAdoptions(tenantID, campaignID)
	if err != nil {
		packStoreFail(w, err)
		return
	}
	items := make([]map[string]any, 0, len(packs))
	for _, p := range packs {
		versions, err := s.St.ListContentPackVersions(tenantID, p.ID)
		if err != nil {
			packStoreFail(w, err)
			return
		}
		versionViews := make([]map[string]any, 0, len(versions))
		for _, v := range versions {
			versionViews = append(versionViews, versionView(v))
		}
		packAdoptions := make([]map[string]any, 0)
		for _, a := range adoptions {
			// adoptions are campaign-level; only show the ones rooted in this pack
			v, err := s.St.GetContentPackVersion(a.VersionID, tenantID)
			if err != nil {
				continue
			}
			if v.PackID != p.ID {
				continue
			}
			packAdoptions = append(packAdoptions, map[string]any{
				"platform": a.Platform, "version_id": a.VersionID,
				"adopted_by": a.AdoptedBy, "updated_at": a.UpdatedAt,
			})
		}
		item := packView(p)
		item["versions"] = versionViews
		item["adoptions"] = packAdoptions
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// GET /api/v1/content-packs/{packId}
func (s *Server) handleContentPackGet(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.packTenantAction(c, authz.ActionReadRecord, w) {
		return
	}
	p, err := s.St.GetContentPack(r.PathValue("packId"), c.Member.TenantID)
	if err != nil {
		packStoreFail(w, err)
		return
	}
	versions, err := s.St.ListContentPackVersions(c.Member.TenantID, p.ID)
	if err != nil {
		packStoreFail(w, err)
		return
	}
	versionViews := make([]map[string]any, 0, len(versions))
	for _, v := range versions {
		versionViews = append(versionViews, versionView(v))
	}
	out := packView(p)
	out["versions"] = versionViews
	writeJSON(w, http.StatusOK, out)
}

// POST /api/v1/content-packs/{packId}/versions — merchant edits copy = a NEW
// immutable version; nothing here can overwrite an existing one.
func (s *Server) handleContentPackVersionCreate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.packTenantAction(c, authz.ActionManageContentPacks, w) {
		return
	}
	var in versionSpecBody
	ok, err := decodePackBody(r, &in)
	if err != nil || !ok {
		fail(w, http.StatusBadRequest, "bad_request", "invalid or disallowed field in body")
		return
	}
	status := contentpack.StatusCandidate
	if strings.TrimSpace(in.Status) == string(contentpack.StatusDraft) {
		status = contentpack.StatusDraft
	}
	v, err := s.St.CreateContentPackVersion(c.Member.TenantID, r.PathValue("packId"), in.spec(), status, c.Member.PrincipalRef)
	if err != nil {
		packStoreFail(w, err)
		return
	}
	out := versionView(v)
	out["replayed"] = false
	writeJSON(w, http.StatusCreated, out)
}

// POST /api/v1/content-packs/{packId}/versions/{versionId}/adopt — EXPLICIT
// adoption: the (campaign, platform) pointer only moves here.
func (s *Server) handleContentPackVersionAdopt(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.packTenantAction(c, authz.ActionManageContentPacks, w) {
		return
	}
	var in struct {
		Platform string `json:"platform"`
	}
	ok, err := decodePackBody(r, &in)
	if err != nil || !ok {
		fail(w, http.StatusBadRequest, "bad_request", "invalid or disallowed field in body")
		return
	}
	a, err := s.St.AdoptContentPackVersion(c.Member.TenantID, r.PathValue("packId"), r.PathValue("versionId"),
		in.Platform, c.Member.PrincipalRef, time.Now())
	if err != nil {
		packStoreFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"platform": a.Platform, "version_id": a.VersionID,
		"adopted_by": a.AdoptedBy, "updated_at": a.UpdatedAt,
	})
}

// POST /api/v1/content-packs/{packId}/versions/{versionId}/block — 撤权/重大
// 内容问题:指针保留(旧参与保留原版本),新分发被拒,处理记录落事件表。
func (s *Server) handleContentPackVersionBlock(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.packTenantAction(c, authz.ActionManageContentPacks, w) {
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	ok, err := decodePackBody(r, &in)
	if err != nil || !ok {
		fail(w, http.StatusBadRequest, "bad_request", "invalid or disallowed field in body")
		return
	}
	v, err := s.St.BlockContentPackVersion(c.Member.TenantID, r.PathValue("packId"), r.PathValue("versionId"),
		in.Reason, c.Member.PrincipalRef, time.Now())
	if err != nil {
		packStoreFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, versionView(v))
}

// POST /api/v1/campaigns/{id}/content-packs/{packId}/return-candidates — the
// HUI-1745 ecosystem return landing point: ALWAYS a candidate version, never
// overwriting the current adoption; same idempotency key replays.
func (s *Server) handleReturnCandidate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.packTenantAction(c, authz.ActionManageContentPacks, w) {
		return
	}
	var in versionSpecBody
	ok, err := decodePackBody(r, &in)
	if err != nil || !ok {
		fail(w, http.StatusBadRequest, "bad_request", "invalid or disallowed field in body")
		return
	}
	v, replayed, err := s.St.AddReturnCandidate(c.Member.TenantID, r.PathValue("packId"), in.spec(), c.Member.PrincipalRef)
	if err != nil {
		packStoreFail(w, err)
		return
	}
	out := versionView(v)
	out["replayed"] = replayed
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	writeJSON(w, status, out)
}

// ---- production handoff (帮我制作:诚实降级 + 幂等落库) ---------------------------------

// GET /api/v1/content-handoffs/capabilities — 如实申报:本票未接真实制作引擎
// (产品图/GoBoost/AiCut/数字人真实交接归 HUI-1745 与后续联调)。
func (s *Server) handleContentHandoffCapabilities(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.packTenantAction(c, authz.ActionReadList, w) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"production_available": false,
		"reason":               "制作引擎尚未接入:产品图/GoBoost/AiCut/数字人的真实交接还没开通。上传已有内容和从素材库选择不受影响。",
	})
}

// POST /api/v1/campaigns/{id}/content-handoffs — records the brief (dedup by
// idempotency key: 同一交接重试不新建制作工程) and answers honestly that no
// production engine is wired yet (503 handoff_unavailable).
func (s *Server) handleContentHandoffCreate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.packTenantAction(c, authz.ActionManageContentPacks, w) {
		return
	}
	var in struct {
		Purpose        string   `json:"purpose"`
		StoreBrief     string   `json:"store_brief"`
		DesiredForm    string   `json:"desired_form"`
		AssetIDs       []string `json:"asset_ids"`
		IdempotencyKey string   `json:"idempotency_key"`
		// MX-OPS R2 (HUI-3553): preference only narrows within allowed templates;
		// there is deliberately NO priority field (strict decode rejects it).
		ExecutionPreference string `json:"execution_preference"`
		PolicyRef           string `json:"policy_ref"`
		PolicyVersion       int    `json:"policy_version"`
	}
	ok, err := decodePackBody(r, &in)
	if err != nil || !ok {
		fail(w, http.StatusBadRequest, "bad_request", "invalid or disallowed field in body")
		return
	}
	if in.ExecutionPreference != "" && !validExecutionPreference(in.ExecutionPreference) {
		fail(w, http.StatusBadRequest, "bad_request", "invalid execution_preference")
		return
	}
	h, replayed, err := s.St.CreateContentHandoff(c.Member.TenantID, r.PathValue("id"), store.HandoffInput{
		Purpose: in.Purpose, StoreBrief: in.StoreBrief, DesiredForm: in.DesiredForm,
		AssetIDs: in.AssetIDs, IdempotencyKey: in.IdempotencyKey,
		ExecutionPreference: in.ExecutionPreference, PolicyRef: in.PolicyRef, PolicyVersion: in.PolicyVersion,
	}, c.Member.PrincipalRef)
	if err != nil {
		packStoreFail(w, err)
		return
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]any{
		"error":   "handoff_unavailable",
		"message": "制作引擎尚未接入,这次交接已记下但没有送去做。上传已有内容和从素材库选择不受影响。",
		"handoff": map[string]any{
			"id": h.ID, "campaign_id": h.CampaignID, "purpose": h.Purpose,
			"desired_form": h.DesiredForm, "idempotency_key": h.IdempotencyKey,
			"execution_preference": h.ExecutionPreference, "policy_ref": h.PolicyRef,
			"policy_version": h.PolicyVersion, "job_ref": h.JobRef,
			"job_queue_view": h.JobQueueView,
		},
		"replayed": replayed,
	})
}

// validExecutionPreference accepts ONLY the contract template names; the client
// cannot invent a priority (no numeric priority field exists on this wire).
func validExecutionPreference(preference string) bool {
	switch preference {
	case "immediate", "night_batch", "free_idle", "paid_elastic":
		return true
	default:
		return false
	}
}

// GET /api/v1/campaigns/{id}/content-handoffs — the merchant production list
// (restore entry: same handoff_id/job_ref, never a new production project).
func (s *Server) handleContentHandoffList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.packTenantAction(c, authz.ActionReadList, w) {
		return
	}
	campaignID := r.PathValue("id")
	if _, err := s.St.GetCampaign(campaignID, c.Member.TenantID); err != nil {
		packStoreFail(w, err)
		return
	}
	handoffs, err := s.St.ListContentHandoffs(c.Member.TenantID, campaignID)
	if err != nil {
		packStoreFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"handoffs": handoffs})
}

// POST /api/v1/content-handoffs/{handoffId}/queue-view — the adapter seam: a
// trusted producer syncs the authoritative queue projection after strict
// contract validation; the PUBLIC projection (internal_debug stripped) is all
// that is stored/exposed. Clients cannot write internal fields directly.
func (s *Server) handleContentHandoffQueueView(w http.ResponseWriter, r *http.Request) {
	// HUI-3553 W5 dual auth: the service producer path (verified in
	// requireServiceProducer) resolves the tenant from the handoff ROW — never
	// from X-Tenant-ID — and skips the session/manage gate. The legacy session
	// path keeps its exact authorisation and error behaviour.
	producer := serviceProducerFrom(r.Context())
	handoffID := r.PathValue("handoffId")
	var tenantID string
	if producer != nil {
		h, err := s.St.GetContentHandoffByID(handoffID)
		if err != nil {
			// Unknown id is a uniform 404; no cross-tenant existence leak.
			s.auditQueueViewProducer(producer, handoffID, "", "", "rejected", "handoff_not_found")
			packStoreFail(w, err)
			return
		}
		tenantID = h.TenantID
	} else {
		c := callerFrom(r)
		if !s.packTenantAction(c, authz.ActionManageContentPacks, w) {
			return
		}
		tenantID = c.Member.TenantID
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "unreadable queue view")
		return
	}
	view, err := mxopsview.Parse(raw)
	if err != nil {
		// Strict contract rejection: never partially default a queue view.
		if producer != nil {
			s.auditQueueViewProducer(producer, handoffID, tenantID, "", "rejected", "invalid_queue_view")
		}
		fail(w, http.StatusUnprocessableEntity, "mx_ops_c0_invalid_queue_view", "queue view does not match mx-ops-c0/v1")
		return
	}
	public, err := view.PublicJSON()
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "queue view projection failed")
		return
	}
	h, err := s.St.UpdateContentHandoffQueueView(tenantID, handoffID, view.JobRef, public)
	if err != nil {
		if producer != nil {
			s.auditQueueViewProducer(producer, handoffID, tenantID, view.JobRef, "rejected", "store_write_failed")
		}
		packStoreFail(w, err)
		return
	}
	if producer != nil {
		s.auditQueueViewProducer(producer, handoffID, tenantID, h.JobRef, "ok", "")
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"handoff_id":     h.ID,
		"job_ref":        h.JobRef,
		"job_queue_view": h.JobQueueView,
	})
}

// auditQueueViewProducer records the trusted-service write (HUI-3553 W5).
// It logs only non-secret facts: service id, handoff id, resolved tenant, job
// ref and result. The service token and request body never enter the log.
func (s *Server) auditQueueViewProducer(producer *serviceProducer, handoffID, tenantID, jobRef, result, reason string) {
	if s == nil || s.Log == nil || producer == nil {
		return
	}
	line := "mx_ops_queue_view_producer service=" + producer.ServiceID +
		" handoff=" + handoffID + " tenant=" + tenantID + " job_ref=" + jobRef + " result=" + result
	if reason != "" {
		line += " reason=" + reason
	}
	s.Log.Print(line)
}

// ---- controlled media handoff (受控下载 URL,供 HUI-1670 消费) ---------------------------

// POST /api/v1/content-pack-versions/{versionId}/media-handoff — mints a
// short-lived signed URL bound to one frozen asset snapshot.
func (s *Server) handleMediaHandoffMint(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.packTenantAction(c, authz.ActionManageContentPacks, w) {
		return
	}
	var in struct {
		Asset       string `json:"asset"` // video | cover | imagetext
		ImageIndex  int    `json:"image_index"`
		ContentType string `json:"content_type"`
		Disposition string `json:"disposition"`
		Filename    string `json:"filename"`
		TTLSeconds  int    `json:"ttl_seconds"`
	}
	ok, err := decodePackBody(r, &in)
	if err != nil || !ok {
		fail(w, http.StatusBadRequest, "bad_request", "invalid or disallowed field in body")
		return
	}
	v, err := s.St.GetContentPackVersion(r.PathValue("versionId"), c.Member.TenantID)
	if err != nil {
		packStoreFail(w, err)
		return
	}
	var snap contentpack.AssetSnapshot
	switch in.Asset {
	case "video":
		snap = v.Video
	case "cover":
		snap = v.Cover
	case "imagetext":
		if in.ImageIndex < 0 || in.ImageIndex >= len(v.ImageText) {
			fail(w, http.StatusUnprocessableEntity, contentpack.ReasonMediaMissing, "image index out of range")
			return
		}
		snap = v.ImageText[in.ImageIndex]
	default:
		fail(w, http.StatusUnprocessableEntity, contentpack.ReasonMediaMissing, "unknown asset selector: "+in.Asset)
		return
	}
	if snap.IsEmpty() {
		fail(w, http.StatusUnprocessableEntity, contentpack.ReasonMediaMissing, "this version has no "+in.Asset+" reference")
		return
	}
	ttl := time.Duration(in.TTLSeconds) * time.Second
	claim, err := contentpack.NewHandoffClaim(v.ID, snap.AssetID, snap.SHA256, in.ContentType, in.Disposition, in.Filename, time.Now(), ttl)
	if err != nil {
		if !invalidPack422(w, err) {
			fail(w, http.StatusInternalServerError, "internal", "handoff claim failed")
		}
		return
	}
	token, err := contentpack.MintHandoff(s.Cfg.InternalToken, claim)
	if err != nil {
		if !invalidPack422(w, err) {
			fail(w, http.StatusInternalServerError, "internal", "handoff mint failed")
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"url":                 "/api/v1/media-handoffs/" + token,
		"token":               token,
		"expires_at":          claim.ExpiresAt.UTC().Format(time.RFC3339),
		"content_type":        claim.ContentType,
		"content_disposition": claim.ContentDisposition(),
	})
}

// GET /api/v1/media-handoffs/{token} — redemption: reference metadata only
// (the publisher pulls bytes from platform upload); expiry/signature enforced.
func (s *Server) handleMediaHandoffRedeem(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.packTenantAction(c, authz.ActionReadRecord, w) {
		return
	}
	claim, err := contentpack.VerifyHandoff(s.Cfg.InternalToken, r.PathValue("token"), time.Now())
	if err != nil {
		if !invalidPack422(w, err) {
			fail(w, http.StatusBadRequest, "handoff_invalid", "bad handoff token")
		}
		return
	}
	v, err := s.St.GetContentPackVersion(claim.VersionID, c.Member.TenantID)
	if err != nil {
		packStoreFail(w, err)
		return
	}
	var snap contentpack.AssetSnapshot
	switch {
	case v.Video.AssetID == claim.AssetID:
		snap = v.Video
	case v.Cover.AssetID == claim.AssetID:
		snap = v.Cover
	default:
		for _, img := range v.ImageText {
			if img.AssetID == claim.AssetID {
				snap = img
				break
			}
		}
	}
	if snap.AssetID != claim.AssetID || snap.SHA256 != claim.SHA256 {
		fail(w, http.StatusGone, contentpack.ReasonMediaMissing, "the referenced media snapshot is no longer part of this version")
		return
	}
	if _, err := s.St.GetLibAsset(claim.AssetID, c.Member.TenantID); err != nil {
		// 源素材行被移除:素材缺失,明确拒绝(不伪造可用)
		fail(w, http.StatusGone, contentpack.ReasonMediaMissing, "the referenced lib asset no longer exists")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version_id":          claim.VersionID,
		"asset_ref":           snap.AssetRef,
		"sha256":              snap.SHA256,
		"content_type":        claim.ContentType,
		"content_disposition": claim.ContentDisposition(),
		"expires_at":          claim.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

// ---- public guest surface (FEATURE_CONTENT_PACKS;公开字段白名单) -----------------------

// publicMediaRef is the whitelist media anchor (fetch reference only; no
// internal asset id, no grant material).
type publicMediaRef struct {
	AssetRef string `json:"asset_ref"`
	SHA256   string `json:"sha256"`
}

type publicSnapshotView struct {
	PackVersionID string              `json:"pack_version_id"`
	ContentKind   string              `json:"content_kind"`
	Title         string              `json:"title"`
	Body          string              `json:"body"`
	Topics        []string            `json:"topics"`
	StoreName     string              `json:"store_name"`
	PriceNote     string              `json:"price_note"`
	ValidNote     string              `json:"valid_note"`
	Video         *publicMediaRef     `json:"video,omitempty"`
	Cover         *publicMediaRef     `json:"cover,omitempty"`
	ImageText     []publicMediaRef    `json:"image_text,omitempty"`
	Platform      string              `json:"platform"`
	PlatformNote  string              `json:"platform_note,omitempty"`
	Marking       contentpack.Marking `json:"marking"`
}

func mediaRefOf(a contentpack.AssetSnapshot) *publicMediaRef {
	if a.IsEmpty() {
		return nil
	}
	return &publicMediaRef{AssetRef: a.AssetRef, SHA256: a.SHA256}
}

func snapshotViewOf(v store.ContentPackVersion, pv *contentpack.PlatformVersion) publicSnapshotView {
	view := publicSnapshotView{
		PackVersionID: v.ID, ContentKind: string(v.ContentKind),
		Title: v.Title, Body: v.Body, Topics: v.Topics,
		StoreName: v.StoreName, PriceNote: v.PriceNote, ValidNote: v.ValidNote,
		Video: mediaRefOf(v.Video), Cover: mediaRefOf(v.Cover),
		Marking: v.Marking,
	}
	for _, img := range v.ImageText {
		if ref := mediaRefOf(img); ref != nil {
			view.ImageText = append(view.ImageText, *ref)
		}
	}
	if pv != nil {
		view.Platform = pv.Platform
		view.PlatformNote = pv.Note
	} else {
		view.Platform = ""
	}
	return view
}

// platformAdaptation picks the requested platform's adaptation note.
func platformAdaptation(v store.ContentPackVersion, platform string) *contentpack.PlatformVersion {
	for i := range v.PlatformVersions {
		if v.PlatformVersions[i].Platform == platform {
			return &v.PlatformVersions[i]
		}
	}
	return nil
}

// GET /api/v1/public/links/{code}/content-choices — packs of this campaign
// that HAVE a current adoption (少量真实不同主题;未采用的包不出现).
func (s *Server) handlePublicContentChoices(w http.ResponseWriter, r *http.Request) {
	if !s.contentPackGate(w) {
		return
	}
	res := s.St.ResolveLink(r.PathValue("code"), time.Now())
	if res.Outcome != store.OutcomeAvailable {
		fail(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	adoptions, err := s.St.ListAdoptions(res.Campaign.TenantID, res.Campaign.ID)
	if err != nil {
		fail(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	items := make([]map[string]any, 0, len(adoptions))
	for _, a := range adoptions {
		v, err := s.St.GetContentPackVersion(a.VersionID, res.Campaign.TenantID)
		if err != nil {
			continue
		}
		now := time.Now()
		if err := contentpack.Distributable(v.Status, v.GrantUntil, now); err != nil {
			continue // blocked/expired versions are not offered
		}
		p, err := s.St.GetContentPack(v.PackID, res.Campaign.TenantID)
		if err != nil {
			continue
		}
		cover := v.Cover
		if cover.IsEmpty() && len(v.ImageText) > 0 {
			cover = v.ImageText[0]
		}
		item := map[string]any{
			"pack_id": p.ID, "title": p.Title, "version_id": v.ID,
			"platform": a.Platform, "content_kind": v.ContentKind,
		}
		if ref := mediaRefOf(cover); ref != nil {
			item["cover"] = ref
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// GET /api/v1/public/links/{code}/content-snapshot?pack_id=&platform= — the
// immutable adopted snapshot (HUI-1670/HUI-1896 consumption). Same read twice
// returns identical fields; blocked/expired answers 409 (拒新分发).
func (s *Server) handlePublicContentSnapshot(w http.ResponseWriter, r *http.Request) {
	if !s.contentPackGate(w) {
		return
	}
	res := s.St.ResolveLink(r.PathValue("code"), time.Now())
	if res.Outcome != store.OutcomeAvailable {
		fail(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	packID := strings.TrimSpace(r.URL.Query().Get("pack_id"))
	platform := strings.TrimSpace(r.URL.Query().Get("platform"))
	if packID == "" || platform == "" {
		fail(w, http.StatusBadRequest, "bad_request", "pack_id and platform are required")
		return
	}
	p, err := s.St.GetContentPack(packID, res.Campaign.TenantID)
	if err != nil || p.CampaignID != res.Campaign.ID {
		fail(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	v, err := s.St.GetAdoptedVersion(res.Campaign.TenantID, res.Campaign.ID, platform)
	if err != nil {
		fail(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	if v.PackID != p.ID {
		fail(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	if err := contentpack.Distributable(v.Status, v.GrantUntil, time.Now()); err != nil {
		if !invalidPack409(w, err) {
			fail(w, http.StatusInternalServerError, "internal", "distribution check failed")
		}
		return
	}
	writeJSON(w, http.StatusOK, snapshotViewOf(v, platformAdaptation(v, platform)))
}

// invalidPack409 maps distribution-refusal reasons to 409.
func invalidPack409(w http.ResponseWriter, err error) bool {
	var ve *contentpack.ValidationError
	if !errors.As(err, &ve) {
		return false
	}
	fail(w, http.StatusConflict, ve.Reason, "this content is not available for publishing: "+ve.Reason)
	return true
}
