// handlers_store_content.go: HUI-3553 门店根内容包的 HTTP 管理面(additive)。
//
// 纪律(裁定书):
//   - 门店根建包:POST /api/v1/stores/{id}/content-packs,campaign 可空;
//     旧 campaign 端点保留不动(handlers_content_packs.go);
//   - 真实上传:multipart 字节经 upload.MediaStore 实测验证(MIME/长度/哈希),
//     上传完成 = 字节已落盘且通过验证;商家不填任何技术字段;
//   - 建版本只收 media_id 与语义声明,引用快照由存储层从实测行落定
//     (media_missing = 422,存在性不泄露);
//   - 管理动作走 authz.ActionManageContentPacks(仅 org_owner);查询按既有
//     业务角色(ActionReadList);
//   - 列表是查询适配:本店旧 campaign 活动包一并返回并带 legacy 标记;
//   - 明确采用记录操作者与时间;换版 = 新候选再采用,旧版内容零改动。

package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

// maxMediaUploadBytes is the hard upload cap (大文件弱网在受控读面靠
// Range/续签解决;上传侧直接限流).
const maxMediaUploadBytes = 200 << 20

// ---- media upload (真实字节;服务器以实测定类型) ---------------------------------------

// storeMediaView is the verified metadata the upload/registry returns.
func storeMediaView(m store.ContentMedia, replayed bool) map[string]any {
	return map[string]any{
		"id": m.ID, "store_id": m.StoreID, "kind": m.Kind,
		"mime_type": m.MimeType, "byte_size": m.ByteSize, "sha256": m.SHA256,
		"duration_ms": m.DurationMS, "width": m.Width, "height": m.Height,
		"filename": m.Filename, "replayed": replayed,
		"created_at": m.CreatedAt,
	}
}

// POST /api/v1/stores/{id}/content-media — multipart field "file".
func (s *Server) handleStoreContentMediaUpload(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.packTenantAction(c, authz.ActionManageContentPacks, w) {
		return
	}
	storeID := r.PathValue("id")
	if _, err := s.St.GetStore(storeID, c.Member.TenantID); err != nil {
		packStoreFail(w, err)
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "multipart form required (field: file)")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "multipart field \"file\" is required")
		return
	}
	defer file.Close()
	// 客户端声明的类型/文件名一律不采信:类型来自魔数,大小/哈希来自落盘实测。
	meta, err := s.Media.Save(file)
	if err != nil {
		switch {
		case errors.Is(err, upload.ErrUnsupportedMedia):
			fail(w, http.StatusUnprocessableEntity, "unsupported_media", "这不是受支持的视频/图片文件(服务器按真实内容判定)")
		case errors.Is(err, upload.ErrMediaTooLarge):
			fail(w, http.StatusRequestEntityTooLarge, "media_too_large", "文件超过大小上限")
		default:
			fail(w, http.StatusInternalServerError, "internal", "media upload failed")
		}
		return
	}
	m, replayed, err := s.St.RegisterContentMedia(c.Member.TenantID, storeID, meta, header.Filename, c.Member.PrincipalRef)
	if err != nil {
		packStoreFail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, storeMediaView(m, replayed))
}

// GET /api/v1/stores/{id}/content-media — the store's verified media list.
func (s *Server) handleStoreContentMediaList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.packTenantAction(c, authz.ActionReadList, w) {
		return
	}
	media, err := s.St.ListContentMedia(c.Member.TenantID, r.PathValue("id"))
	if err != nil {
		packStoreFail(w, err)
		return
	}
	items := make([]map[string]any, 0, len(media))
	for _, m := range media {
		items = append(items, storeMediaView(m, false))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// ---- store content packs ------------------------------------------------------------

// POST /api/v1/stores/{id}/content-packs {title, campaign_id?} — 门店根建包,
// campaign 可空(不为无活动门店强建活动)。
func (s *Server) handleStoreContentPackCreate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.packTenantAction(c, authz.ActionManageContentPacks, w) {
		return
	}
	var in struct {
		Title      string `json:"title"`
		CampaignID string `json:"campaign_id"`
	}
	ok, err := decodePackBody(r, &in)
	if err != nil || !ok {
		fail(w, http.StatusBadRequest, "bad_request", "invalid or disallowed field in body")
		return
	}
	p, err := s.St.CreateStoreContentPack(c.Member.TenantID, r.PathValue("id"), strings.TrimSpace(in.CampaignID), in.Title, c.Member.PrincipalRef)
	if err != nil {
		packStoreFail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, storePackView(p))
}

func storePackView(p store.StoreContentPack) map[string]any {
	return map[string]any{
		"id": p.ID, "tenant_id": p.TenantID, "store_id": p.StoreID,
		"campaign_id": p.CampaignID, "title": p.Title, "legacy": p.Legacy,
		"created_by": p.CreatedBy, "created_at": p.CreatedAt, "updated_at": p.UpdatedAt,
	}
}

func storeVersionView(v store.StoreContentPackVersion) map[string]any {
	return map[string]any{
		"id": v.ID, "pack_id": v.PackID, "version_no": v.VersionNo,
		"content_kind": v.ContentKind, "status": v.Status, "source": v.Source,
		"video": storeMediaSnapshotView(v.Video), "cover": storeMediaSnapshotView(v.Cover),
		"image_text": storeMediaSnapshotsView(v.ImageText),
		"title":      v.Title, "body": v.Body, "topics": v.Topics,
		"store_name": v.StoreName, "price_note": v.PriceNote, "valid_note": v.ValidNote,
		"platform_versions": v.PlatformVersions, "grant_refs": v.GrantRefs,
		"grant_until": v.GrantUntil, "marking": v.Marking,
		"blocked_reason": v.BlockedReason, "idempotency_key": v.IdempotencyKey,
		"created_by": v.CreatedBy, "created_at": v.CreatedAt,
	}
}

func storeMediaSnapshotView(a store.StoreMediaSnapshot) any {
	if a.IsEmpty() {
		return nil
	}
	return map[string]any{
		"media_id": a.MediaID, "sha256": a.SHA256, "mime_type": a.MimeType,
		"byte_size": a.ByteSize, "duration_ms": a.DurationMS,
		"width": a.Width, "height": a.Height, "order": a.Order,
	}
}

func storeMediaSnapshotsView(list []store.StoreMediaSnapshot) []map[string]any {
	out := make([]map[string]any, 0, len(list))
	for _, a := range list {
		if m := storeMediaSnapshotView(a); m != nil {
			out = append(out, m.(map[string]any))
		}
	}
	return out
}

// GET /api/v1/stores/{id}/content-packs — one read: packs (store-domain +
// legacy campaign adapter), their versions and current adoption pointers.
func (s *Server) handleStoreContentPackList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.packTenantAction(c, authz.ActionReadList, w) {
		return
	}
	tenantID := c.Member.TenantID
	storeID := r.PathValue("id")
	if _, err := s.St.GetStore(storeID, tenantID); err != nil {
		packStoreFail(w, err)
		return
	}
	packs, err := s.St.ListStoreContentPacks(tenantID, storeID)
	if err != nil {
		packStoreFail(w, err)
		return
	}
	adoptions, err := s.St.ListStoreAdoptions(tenantID, storeID)
	if err != nil {
		packStoreFail(w, err)
		return
	}
	items := make([]map[string]any, 0, len(packs))
	for _, p := range packs {
		item := storePackView(p)
		if p.Legacy {
			// 查询适配投影:旧 campaign 域包按旧表原样只读呈现。
			versions, err := s.St.ListContentPackVersions(tenantID, p.ID)
			if err != nil {
				packStoreFail(w, err)
				return
			}
			versionViews := make([]map[string]any, 0, len(versions))
			for _, v := range versions {
				versionViews = append(versionViews, versionView(v))
			}
			item["versions"] = versionViews
			item["adoptions"] = []map[string]any{}
		} else {
			versions, err := s.St.ListStoreContentPackVersions(tenantID, p.ID)
			if err != nil {
				packStoreFail(w, err)
				return
			}
			versionViews := make([]map[string]any, 0, len(versions))
			for _, v := range versions {
				versionViews = append(versionViews, storeVersionView(v))
			}
			item["versions"] = versionViews
			packAdoptions := make([]map[string]any, 0)
			for _, a := range adoptions {
				if a.PackID == p.ID {
					packAdoptions = append(packAdoptions, map[string]any{
						"platform": a.Platform, "pack_id": a.PackID, "version_id": a.VersionID,
						"adopted_by": a.AdoptedBy, "updated_at": a.UpdatedAt,
					})
				}
			}
			item["adoptions"] = packAdoptions
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// ---- versions / adopt / block ---------------------------------------------------------

type storeVersionSpecBody struct {
	ContentKind       string                `json:"content_kind"`
	VideoMediaID      string                `json:"video_media_id"`
	CoverMediaID      string                `json:"cover_media_id"`
	ImageTextMediaIDs []string              `json:"image_text_media_ids"`
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

func (b storeVersionSpecBody) spec() store.StoreVersionSpec {
	spec := store.StoreVersionSpec{
		ContentKind:    contentpack.ContentKind(b.ContentKind),
		VideoMediaID:   b.VideoMediaID,
		CoverMediaID:   b.CoverMediaID,
		ImageTextMediaIDs: b.ImageTextMediaIDs,
		Title:          b.Title, Body: b.Body, Topics: b.Topics,
		StoreName: b.StoreName, PriceNote: b.PriceNote, ValidNote: b.ValidNote,
		GrantRefs: b.GrantRefs, GrantUntil: b.GrantUntil,
		Marking:        contentpack.Marking(b.Marking),
		Source:         contentpack.Source(b.Source),
		IdempotencyKey: b.IdempotencyKey,
	}
	for _, pv := range b.PlatformVersions {
		spec.PlatformVersions = append(spec.PlatformVersions, contentpack.PlatformVersion{
			Platform: pv.Platform, Note: pv.Note, SourceVersion: pv.SourceVersion,
		})
	}
	return spec
}

// POST /api/v1/stores/{id}/content-packs/{packId}/versions — merchant edits
// copy / swaps media = a NEW immutable version; nothing overwrites one.
func (s *Server) handleStoreContentPackVersionCreate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.packTenantAction(c, authz.ActionManageContentPacks, w) {
		return
	}
	var in storeVersionSpecBody
	ok, err := decodePackBody(r, &in)
	if err != nil || !ok {
		fail(w, http.StatusBadRequest, "bad_request", "invalid or disallowed field in body")
		return
	}
	status := contentpack.StatusCandidate
	if strings.TrimSpace(in.Status) == string(contentpack.StatusDraft) {
		status = contentpack.StatusDraft
	}
	v, err := s.St.CreateStoreContentPackVersion(c.Member.TenantID, r.PathValue("packId"), in.spec(), status, c.Member.PrincipalRef)
	if err != nil {
		packStoreFail(w, err)
		return
	}
	out := storeVersionView(v)
	out["replayed"] = false
	writeJSON(w, http.StatusCreated, out)
}

// POST /api/v1/stores/{id}/content-packs/{packId}/versions/{versionId}/adopt —
// EXPLICIT adoption: the (store, platform) pointer only moves here; the
// operator and time are recorded.
func (s *Server) handleStoreContentPackVersionAdopt(w http.ResponseWriter, r *http.Request) {
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
	a, err := s.St.AdoptStoreContentPackVersion(c.Member.TenantID, r.PathValue("packId"), r.PathValue("versionId"),
		in.Platform, c.Member.PrincipalRef, time.Now())
	if err != nil {
		packStoreFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"platform": a.Platform, "pack_id": a.PackID, "version_id": a.VersionID,
		"adopted_by": a.AdoptedBy, "created_at": a.CreatedAt, "updated_at": a.UpdatedAt,
	})
}

// POST /api/v1/stores/{id}/content-packs/{packId}/versions/{versionId}/block —
// 撤权/重大内容问题:指针保留(旧引用不动),新分发被拒,处理记录落事件表。
func (s *Server) handleStoreContentPackVersionBlock(w http.ResponseWriter, r *http.Request) {
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
	v, err := s.St.BlockStoreContentPackVersion(c.Member.TenantID, r.PathValue("packId"), r.PathValue("versionId"),
		in.Reason, c.Member.PrincipalRef, time.Now())
	if err != nil {
		packStoreFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, storeVersionView(v))
}
