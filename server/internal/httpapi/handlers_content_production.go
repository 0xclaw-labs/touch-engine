// handlers_content_production.go: HUI-4595 真实内容制作接线 HTTP 面。
//
// 纪律(票面/裁定书):
//   - 公共发起面(商家会话): POST/GET /api/v1/stores/{id}/content-production-handoffs
//     与 capabilities。管理动作走 authz.manage_content_packs,查询走 read_list
//     (同 3553 门店内容包授权)。首次制作:空内容包(零候选/零采用)完全合法,
//     绝不为通过校验伪建 campaign 或虚构采用版本(T05);
//   - 公共权威 seam:报价经 production.QuoteAuthority(统一账单;配置齐全才
//     核验,未配置=申报值落库且如实 quote_verified:false,绝不谎称已核实);
//     任务经 production.TaskSubmitter(派诺生态 Task;本仓不自建制作队列;
//     未配置=诚实关闭,status=recorded,绝不伪造 task_ref);
//   - 受信生产方开关未配诚实关闭:TOUCH_SERVICE_TOKENS 无 content_return_
//     producer 角色服务时,发起面答 503 production_unavailable(行照常落库
//     去重,同 3553「记录 brief + 诚实降级」户型);
//   - 幂等(T15):发起面同租户同 idempotency_key 重放返回同一行,不重新核价
//     不重新提交任务;回流面 source_app+event_id 去重 + 负载摘要,重复回流
//     与轮询不重复扣费;
//   - 受信回流(POST /api/v1/internal/content-production-handoffs/{id}/results)
//     仅 content_return_producer 角色服务可调;无服务身份不落商家会话路径
//     (ops-grant 户型,统一 401);tenant/store/pack 只由已存 handoff 行恢复,
//     请求不可覆盖;task_ref/result_revision/两端资产哈希/费用引用逐项校验;
//     result_revision 水位拒旧稿迟到;旧回流绝不覆盖新采用(候选只落
//     3553 AddStoreReturnCandidate,采用指针永不在此移动);
//   - /api/v1/internal/* 不向公共 BFF 暴露:web BFF 对 internal 段拒透传
//     (见 web/src/lib/bff.ts + tests/bff-internal.test.ts);
//   - 浏览/预览零计费:候选(含回流候选)预览复用 3553 media-access 短 TTL
//     受控 URL,本文件零生成/计费调用;顾客可触达面没有制作通道(结构保证)。
package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
	"github.com/bianjiefilm/touch-engine/server/internal/production"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

// contentReturnProducerRole gates the results-return seam. A registered
// service WITHOUT this role (default queue_producer) can never return
// results — least privilege per seam.
const contentReturnProducerRole = "content_return_producer"

// sourceAppPattern is the same shape as service ids: ids appear in audit.
var sourceAppPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,62}$`)

// requireContentReturnProducer is the results-return twin of
// requireOpsGrantProducer: same per-service token seam (constant-time
// compare, fail-closed registry) but the content_return_producer role.
// Requests without a service identity skip the session path on purpose and
// fall through to the handler's uniform 401 — 商家会话永不伪造回流。
func (s *Server) requireContentReturnProducer(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serviceID := strings.TrimSpace(r.Header.Get(serviceIDHeader))
		if serviceID == "" {
			next(w, r)
			return
		}
		if len(s.Cfg.ServiceTokens) == 0 {
			s.auditServiceProducer("unavailable", serviceID, "registry_not_configured")
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error":   "service_producer_unavailable",
				"message": "trusted service producer registry is not configured; refusing to act (fail-closed)",
			})
			return
		}
		want, registered := s.Cfg.ServiceTokens[serviceID]
		got := strings.TrimSpace(r.Header.Get(serviceTokenHeader))
		if !registered || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			// Uniform refusal: never disclose whether the service id exists.
			s.auditServiceProducer("rejected", serviceID, "bad_service_identity")
			fail(w, http.StatusUnauthorized, "unauthorized", "missing or wrong service identity")
			return
		}
		if s.Cfg.ServiceProducerRole(serviceID) != contentReturnProducerRole {
			s.auditServiceProducer("rejected", serviceID, "role_not_content_return_producer")
			fail(w, http.StatusForbidden, "forbidden", "service identity lacks the content_return_producer role")
			return
		}
		s.auditServiceProducer("accepted", serviceID, "")
		ctx := context.WithValue(r.Context(), serviceProducerKey, &serviceProducer{ServiceID: serviceID})
		next(w, r.WithContext(ctx))
	})
}

// contentProducerFrom re-checks the verified service identity inside the
// handler (the middleware context is the only source; absent => uniform 401).
func (s *Server) contentProducerFrom(w http.ResponseWriter, r *http.Request) (string, bool) {
	p := serviceProducerFrom(r.Context())
	if p == nil || p.ServiceID == "" {
		fail(w, http.StatusUnauthorized, "unauthorized", "production results are accepted through the trusted service seam only")
		return "", false
	}
	if s.Cfg.ServiceProducerRole(p.ServiceID) != contentReturnProducerRole {
		fail(w, http.StatusForbidden, "forbidden", "service identity lacks the content_return_producer role")
		return "", false
	}
	return p.ServiceID, true
}

// contentProducerConfigured answers honestly whether any registered trusted
// service carries the content_return_producer role (the production switch).
func (s *Server) contentProducerConfigured() bool {
	for id := range s.Cfg.ServiceTokens {
		if s.Cfg.ServiceProducerRole(id) == contentReturnProducerRole {
			return true
		}
	}
	return false
}

// productionHandoffView is the merchant-facing projection (refs are opaque;
// no internal digests, no service tokens).
func productionHandoffView(h store.ProductionHandoff) map[string]any {
	return map[string]any{
		"id":                   h.ID,
		"store_id":             h.StoreID,
		"pack_id":              h.PackID,
		"content_pack_ref":     actionContentBindingRef(h.PackID),
		"content_binding_ref":  h.ContentBindingRef,
		"quote_ref":            h.QuoteRef,
		"quote_verified":       h.QuoteVerified,
		"status":               h.Status,
		"task_ref":             h.TaskRef,
		"last_charge_ref":      h.LastChargeRef,
		"last_result_revision": h.LastResultRevision,
		"note":                 h.Note,
		"created_by":           h.CreatedBy,
		"created_at":           h.CreatedAt,
		"updated_at":           h.UpdatedAt,
	}
}

func productionResultView(res store.ProductionResult) map[string]any {
	return map[string]any{
		"id": res.ID, "source_app": res.SourceApp, "event_id": res.EventID,
		"outcome": res.Outcome, "task_ref": res.TaskRef,
		"result_revision": res.ResultRevision, "charge_ref": res.ChargeRef,
		"version_id":      res.VersionID,
		"declared_sha256": res.DeclaredSHA, "measured_sha256": res.MeasuredSHA,
		"reason": res.Reason, "created_at": res.CreatedAt,
	}
}

// resolveContentPackRef accepts "pack:<id>" (the拟契约 ref shape used by the
// action content bindings) or a bare pack id; anything else is a bad ref.
func resolveContentPackRef(ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", false
	}
	if strings.HasPrefix(ref, "pack:") {
		id := strings.TrimSpace(strings.TrimPrefix(ref, "pack:"))
		return id, id != ""
	}
	return ref, true
}

// ---- merchant initiate face ---------------------------------------------------

// GET /api/v1/stores/{id}/content-production-handoffs/capabilities
func (s *Server) handleStoreContentProductionCapabilities(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.packTenantAction(c, authz.ActionReadList, w) {
		return
	}
	if _, err := s.St.GetStore(r.PathValue("id"), c.Member.TenantID); err != nil {
		packStoreFail(w, err)
		return
	}
	producerOK := s.contentProducerConfigured()
	quoteOK := s.ProductionQuotes.Ready()
	taskOK := s.Cfg.FeatureTask && s.ProductionTasks.Ready()
	reason := "已接通受信制作生产方。提交后由其按真实任务与账单权威执行。"
	if !producerOK {
		reason = "制作生产方尚未接入:还没有注册受信制作服务。这里先如实关闭发起;上传已有内容和从素材库选择不受影响,已记录的发起会保留。"
	} else if !quoteOK {
		reason = "受信制作生产方已注册;账单权威尚未接入,报价引用先按申报记录,费用状态以账单为准。"
	} else if !taskOK {
		reason = "受信制作生产方与账单权威已接;公共任务权威未接通,提交先记录、任务暂不开始。"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"production_available":       producerOK,
		"producer_configured":        producerOK,
		"quote_authority_configured": quoteOK,
		"task_authority_configured":  taskOK,
		"reason":                     reason,
	})
}

type contentProductionHandoffBody struct {
	ContentPackRef    string `json:"content_pack_ref"`
	ContentBindingRef string `json:"content_binding_ref"`
	QuoteRef          string `json:"quote_ref"`
	IdempotencyKey    string `json:"idempotency_key"`
	Note              string `json:"note"`
}

// POST /api/v1/stores/{id}/content-production-handoffs
func (s *Server) handleStoreContentProductionHandoffCreate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.packTenantAction(c, authz.ActionManageContentPacks, w) {
		return
	}
	tenantID := c.Member.TenantID
	storeID := r.PathValue("id")
	if _, err := s.St.GetStore(storeID, tenantID); err != nil {
		packStoreFail(w, err)
		return
	}
	var in contentProductionHandoffBody
	ok, err := decodePackBody(r, &in)
	if err != nil || !ok {
		fail(w, http.StatusBadRequest, "bad_request", "invalid or disallowed field in body")
		return
	}
	packID, okRef := resolveContentPackRef(in.ContentPackRef)
	if !okRef {
		fail(w, http.StatusBadRequest, "bad_content_pack_ref", "content_pack_ref must be pack:<id> or a pack id")
		return
	}
	// content_binding_ref 可空 = 首次制作合法路径;非空必须是本包的绑定引用
	// (pack:<id> 同包锚点),别名/跨包一律拒绝。
	bindingRef := strings.TrimSpace(in.ContentBindingRef)
	if bindingRef != "" {
		boundPack, okBind := resolveContentPackRef(bindingRef)
		if !okBind || boundPack != packID {
			fail(w, http.StatusBadRequest, "bad_content_binding_ref", "content_binding_ref must reference the same pack as content_pack_ref")
			return
		}
		bindingRef = actionContentBindingRef(packID)
	}
	key := strings.TrimSpace(in.IdempotencyKey)
	if err := contentpack.ValidateIdempotencyKey(key); err != nil {
		if !invalidPack422(w, err) {
			fail(w, http.StatusBadRequest, "bad_request", "invalid idempotency_key")
		}
		return
	}
	quoteRef := strings.TrimSpace(in.QuoteRef)
	if quoteRef == "" || len(quoteRef) > contentpack.MaxIdempotencyKey {
		fail(w, http.StatusBadRequest, "bad_request", "quote_ref is required (已确认报价引用,来自账单权威)")
		return
	}

	// 幂等重放先行:同一发起重试返回同一行,不重新核价、不重新提交任务(T15)。
	if existing, err := s.St.GetProductionHandoffByIdempotencyKey(tenantID, key); err == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"handoff":  productionHandoffView(existing),
			"replayed": true,
			"notes":    []string{"同一幂等键重放:返回既有发起,不新建制作工程、不重复报价。"},
		})
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		packStoreFail(w, err)
		return
	}

	// quote seam:配置齐全才核验;核验不过不落行(fail-closed);未配置=申报值
	// 落库并如实标注(费用状态以账单为准,4591 接线后收敛)。
	quoteVerified := false
	notes := make([]string, 0, 3)
	if s.ProductionQuotes.Ready() {
		// 金额不落本仓(账单权威=生态统一账单,本仓只存引用与核验事实)。
		_, qerr := s.ProductionQuotes.ConfirmedQuote(r.Context(), quoteRef)
		if qerr != nil {
			if errors.Is(qerr, production.ErrNotConfirmed) {
				fail(w, http.StatusUnprocessableEntity, "quote_not_confirmed", "账单权威核验:该报价引用不是已确认状态,不能发起制作")
				return
			}
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error":   "quote_authority_unavailable",
				"message": "账单权威暂时无法核验报价;这次发起没有落库,请稍后重试(绝不把无法核实当作已核实)",
			})
			return
		}
		quoteVerified = true
		notes = append(notes, "报价引用经账单权威核验:已确认。")
	} else {
		notes = append(notes, "账单权威未接入:报价引用按申报记录,quote_verified=false,费用状态以账单为准。")
	}

	h, _, err := s.St.CreateProductionHandoff(tenantID, store.ProductionHandoffInput{
		StoreID:           storeID,
		PackID:            packID,
		ContentBindingRef: bindingRef,
		QuoteRef:          quoteRef,
		QuoteVerified:     quoteVerified,
		Note:              strings.TrimSpace(in.Note),
		IdempotencyKey:    key,
	}, c.Member.PrincipalRef)
	if err != nil {
		packStoreFail(w, err)
		return
	}

	// task seam:公共任务权威配置齐全才提交真实任务;未配置=诚实关闭
	// (status=recorded,绝不伪造 task_ref;本仓不自建制作队列)。
	if s.Cfg.FeatureTask && s.ProductionTasks.Ready() {
		params, _ := json.Marshal(map[string]string{
			"handoff_id": h.ID, "store_id": h.StoreID, "pack_id": h.PackID, "quote_ref": h.QuoteRef,
		})
		taskRef, terr := s.ProductionTasks.Submit(r.Context(), production.TaskSubmission{
			IdempotencyKey: h.IdempotencyKey, AccountID: c.Member.PrincipalRef,
			Kind: "touch_content_production", ParamsJSON: string(params),
		})
		if terr == nil {
			if marked, merr := s.St.MarkProductionHandoffSubmitted(tenantID, h.ID, taskRef); merr == nil {
				h = marked
				notes = append(notes, "已提交公共任务权威(任务 "+taskRef+"),处理中。")
			} else {
				notes = append(notes, "任务已受理但引用落库失败,请刷新查看(不重复提交)。")
			}
		} else {
			notes = append(notes, "公共任务权威提交失败:这次请求已记录,任务没有开始;可稍后重试同一幂等键。")
		}
	} else {
		notes = append(notes, "公共任务权威未接通:请求已记录(未提交),制作暂不开始;这是如实的未接通状态。")
	}

	if !s.contentProducerConfigured() {
		// 受信生产方开关未配:诚实关闭(行照常落库去重,同 3553 户型)。
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "production_unavailable",
			"message": "受信制作生产方尚未接入:这次发起已记下但没有送去做。上传已有内容和从素材库选择不受影响。",
			"handoff": productionHandoffView(h),
			"notes":   notes,
		})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"handoff": productionHandoffView(h), "replayed": false, "notes": notes,
	})
}

// GET /api/v1/stores/{id}/content-production-handoffs — 恢复入口:重登/断网后
// 查询同一任务(同一 handoff_id/task_ref),状态 处理中/已产出/失败/取消/未提交
// 独立可辨。
func (s *Server) handleStoreContentProductionHandoffList(w http.ResponseWriter, r *http.Request) {
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
	handoffs, err := s.St.ListProductionHandoffs(tenantID, storeID)
	if err != nil {
		packStoreFail(w, err)
		return
	}
	items := make([]map[string]any, 0, len(handoffs))
	for _, h := range handoffs {
		item := productionHandoffView(h)
		results, err := s.St.ListProductionResults(tenantID, h.ID)
		if err == nil {
			receipts := make([]map[string]any, 0, len(results))
			for _, res := range results {
				receipts = append(receipts, productionResultView(res))
			}
			item["results"] = receipts
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"handoffs": items})
}

// ---- trusted results-return face -----------------------------------------------

// productionResultDeclaration is the validated multipart declaration.
type productionResultDeclaration struct {
	TaskRef        string
	ResultRevision int
	ChargeRef      string
	SourceApp      string
	EventID        string
	Outcome        string
	Reason         string
	VideoSHA       string
	CoverSHA       string
	Title          string
	Body           string
	Topics         []string
	StoreName      string
	PriceNote      string
	ValidNote      string
	AIGenerated    bool
	AdRequired     bool
}

func multipartText(form *multipart.Form, key string) string {
	vals := form.Value[key]
	if len(vals) == 0 {
		return ""
	}
	return strings.TrimSpace(vals[0])
}

func multipartBool(form *multipart.Form, key string) bool {
	switch strings.ToLower(multipartText(form, key)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// normalizeDeclaredSHA enforces the declared-hash shape: lowercase hex, 64.
func normalizeDeclaredSHA(raw string) (string, bool) {
	sha := strings.ToLower(strings.TrimSpace(raw))
	if len(sha) != 64 {
		return "", false
	}
	for _, ch := range sha {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f')) {
			return "", false
		}
	}
	return sha, true
}

// productionPayloadDigest is the canonical integrity digest of the whole
// declaration (same key + same digest = same payload = replay; any field
// difference is a conflict). The digest never leaves the server.
func productionPayloadDigest(handoffID string, d productionResultDeclaration) string {
	h := sha256.New()
	fields := []string{
		"hui-4595/result/v1", handoffID, d.SourceApp, d.EventID, d.Outcome,
		d.TaskRef, strconv.Itoa(d.ResultRevision), d.ChargeRef,
		d.VideoSHA, d.CoverSHA, d.Title, d.Body, d.StoreName, d.PriceNote, d.ValidNote,
		strings.Join(d.Topics, "\x1f"),
		strconv.FormatBool(d.AIGenerated), strconv.FormatBool(d.AdRequired),
	}
	for _, f := range fields {
		_, _ = h.Write([]byte(f))
		_, _ = h.Write([]byte{0x1f})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// parseProductionResultForm validates the multipart declaration (structural
// checks only; authority checks live in the admission chain).
func parseProductionResultForm(w http.ResponseWriter, r *http.Request) (*multipart.Form, productionResultDeclaration, bool) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "multipart form required")
		return nil, productionResultDeclaration{}, false
	}
	form := r.MultipartForm
	if form == nil {
		fail(w, http.StatusBadRequest, "bad_request", "multipart form required")
		return nil, productionResultDeclaration{}, false
	}
	d := productionResultDeclaration{
		TaskRef:     multipartText(form, "task_ref"),
		ChargeRef:   multipartText(form, "charge_ref"),
		SourceApp:   multipartText(form, "source_app"),
		EventID:     multipartText(form, "event_id"),
		Outcome:     strings.ToLower(multipartText(form, "outcome")),
		Reason:      multipartText(form, "reason"),
		VideoSHA:    multipartText(form, "video_sha256"),
		CoverSHA:    multipartText(form, "cover_sha256"),
		Title:       multipartText(form, "title"),
		Body:        multipartText(form, "body"),
		StoreName:   multipartText(form, "store_name"),
		PriceNote:   multipartText(form, "price_note"),
		ValidNote:   multipartText(form, "valid_note"),
		AIGenerated: multipartBool(form, "ai_generated"),
		AdRequired:  multipartBool(form, "ad_required"),
	}
	if topics := multipartText(form, "topics"); topics != "" {
		for _, t := range strings.FieldsFunc(topics, func(rn rune) bool { return rn == ',' || rn == '，' }) {
			if t = strings.TrimSpace(t); t != "" {
				d.Topics = append(d.Topics, t)
			}
		}
	}
	rev, err := strconv.Atoi(multipartText(form, "result_revision"))
	if err != nil || rev < 1 {
		fail(w, http.StatusUnprocessableEntity, "bad_result_revision", "result_revision must be an integer >= 1")
		return nil, productionResultDeclaration{}, false
	}
	d.ResultRevision = rev
	if !sourceAppPattern.MatchString(d.SourceApp) {
		fail(w, http.StatusUnprocessableEntity, "bad_source_app", "source_app must be a stable service identifier")
		return nil, productionResultDeclaration{}, false
	}
	if d.EventID == "" || len(d.EventID) > 64 {
		fail(w, http.StatusUnprocessableEntity, "bad_event_id", "event_id is required (<=64 chars) and pairs with source_app for idempotency")
		return nil, productionResultDeclaration{}, false
	}
	if d.TaskRef == "" || len(d.TaskRef) > 128 {
		fail(w, http.StatusUnprocessableEntity, "bad_task_ref", "task_ref is required (the real source task this result belongs to)")
		return nil, productionResultDeclaration{}, false
	}
	if d.ChargeRef == "" || len(d.ChargeRef) > 128 {
		fail(w, http.StatusUnprocessableEntity, "bad_charge_ref", "charge_ref is required (实际费用引用,账务对账依据)")
		return nil, productionResultDeclaration{}, false
	}
	switch d.Outcome {
	case "", store.ProductionOutcomeProduced, store.ProductionOutcomeFailed, store.ProductionOutcomeCanceled:
	default:
		fail(w, http.StatusUnprocessableEntity, "bad_outcome", "outcome must be produced, failed or canceled")
		return nil, productionResultDeclaration{}, false
	}
	if d.Outcome == "" {
		// 缺省 produced:media/版本分支与准入口径必须一致(同一默认)。
		d.Outcome = store.ProductionOutcomeProduced
	}
	if (d.Outcome == store.ProductionOutcomeFailed || d.Outcome == store.ProductionOutcomeCanceled) && d.Reason == "" {
		fail(w, http.StatusUnprocessableEntity, "bad_reason", "failed/canceled receipts must carry a merchant-readable reason")
		return nil, productionResultDeclaration{}, false
	}
	return form, d, true
}

// saveProducerMedia verifies one declared asset: real bytes through the
// controlled media store, measured SHA must equal the declared SHA (两端SHA
// 核对), kind must match the role. Any mismatch fails before any registration
// (不落半批事实).
func (s *Server) saveProducerMedia(w http.ResponseWriter, form *multipart.Form, field, declaredRaw string, wantKind string, tenantID, storeID, actor string) (store.ContentMedia, bool) {
	declared, okSHA := normalizeDeclaredSHA(declaredRaw)
	if !okSHA {
		fail(w, http.StatusUnprocessableEntity, "bad_asset_hash", field+"_sha256 must be a lowercase hex sha256")
		return store.ContentMedia{}, false
	}
	files := form.File[field]
	if len(files) == 0 {
		fail(w, http.StatusUnprocessableEntity, contentpack.ReasonMediaMissing, "multipart file field \""+field+"\" is required")
		return store.ContentMedia{}, false
	}
	fh := files[0]
	src, err := fh.Open()
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "unreadable "+field+" file")
		return store.ContentMedia{}, false
	}
	defer src.Close()
	meta, err := s.Media.Save(src)
	if err != nil {
		switch {
		case errors.Is(err, upload.ErrUnsupportedMedia):
			fail(w, http.StatusUnprocessableEntity, "unsupported_media", field+" 不是受支持的视频/图片文件(服务器按真实内容判定)")
		case errors.Is(err, upload.ErrMediaTooLarge):
			fail(w, http.StatusRequestEntityTooLarge, "media_too_large", field+" 超过大小上限")
		default:
			fail(w, http.StatusInternalServerError, "internal", field+" media save failed")
		}
		return store.ContentMedia{}, false
	}
	kind := string(meta.Kind)
	if kind != wantKind {
		fail(w, http.StatusUnprocessableEntity, "media_kind_mismatch", field+" 的真实类型是 "+kind+",与该角色的要求不符")
		return store.ContentMedia{}, false
	}
	if meta.SHA256 != declared {
		// 两端 SHA 不一致:如实拒绝并展示两端指纹(票面必须记录两端 SHA)。
		fail(w, http.StatusUnprocessableEntity, "hash_mismatch",
			field+" 声明哈希与实测哈希不一致(declared "+declared[:12]+"…, measured "+meta.SHA256[:12]+"…)")
		return store.ContentMedia{}, false
	}
	m, _, err := s.St.RegisterContentMedia(tenantID, storeID, meta, fh.Filename, actor)
	if err != nil {
		packStoreFail(w, err)
		return store.ContentMedia{}, false
	}
	return m, true
}

// POST /api/v1/internal/content-production-handoffs/{handoffId}/results
//
// The trusted producer seam. tenant/store/pack come ONLY from the stored
// handoff row (GetProductionHandoffByID — no tenant scope; the row is the
// single tenant truth and request input cannot override it). The produced
// candidate is registered through the 3553 UNIQUE candidate transaction
// (AddStoreReturnCandidate) inside the admitted window; the current adoption
// pointer is never touched here.
func (s *Server) handleContentProductionResultReturn(w http.ResponseWriter, r *http.Request) {
	serviceID, ok := s.contentProducerFrom(w, r)
	if !ok {
		return
	}
	form, d, okForm := parseProductionResultForm(w, r)
	if !okForm {
		return
	}
	handoffID := r.PathValue("handoffId")
	digest := productionPayloadDigest(handoffID, d)

	in := store.ProductionResultInput{
		HandoffID:      handoffID,
		SourceApp:      d.SourceApp,
		EventID:        d.EventID,
		Outcome:        d.Outcome,
		TaskRef:        d.TaskRef,
		ResultRevision: d.ResultRevision,
		ChargeRef:      d.ChargeRef,
		Digest:         digest,
		Reason:         d.Reason,
	}
	// 准入(不写):重放/冲突/终态/task 引用/水位逐项裁决;handoff 行恢复
	// tenant/store/pack 权威映射。
	h, prev, replayed, admitErr := s.St.AdmitProductionResult(in)
	if admitErr != nil {
		s.auditResultReturn(serviceID, handoffID, d.SourceApp, d.EventID, "rejected", admitErr)
		switch {
		case errors.Is(admitErr, store.ErrNotFound):
			fail(w, http.StatusNotFound, "not_found", "resource not found")
		case errors.Is(admitErr, store.ErrIdempotencyConflict):
			fail(w, http.StatusConflict, "idempotency_conflict", "same source_app+event_id with a different payload")
		case errors.Is(admitErr, store.ErrHandoffCanceled):
			fail(w, http.StatusConflict, "handoff_canceled", "this production handoff is canceled; new results are refused")
		case errors.Is(admitErr, store.ErrTaskRefMismatch):
			fail(w, http.StatusConflict, "task_ref_mismatch", "task_ref does not match the task recorded on this handoff")
		case errors.Is(admitErr, store.ErrStaleResult):
			fail(w, http.StatusConflict, "result_revision_stale", "a newer result revision was already recorded; late results never overwrite it")
		default:
			if !invalidPack422(w, admitErr) {
				fail(w, http.StatusInternalServerError, "internal", "result admission failed")
			}
		}
		return
	}
	if replayed {
		// 同键同负载:重放同一回执,不重复登记候选、不重复扣费(T15)。
		s.auditResultReturn(serviceID, handoffID, d.SourceApp, d.EventID, "replayed", nil)
		writeJSON(w, http.StatusOK, map[string]any{
			"result": productionResultView(prev), "replayed": true,
		})
		return
	}

	actor := "service:" + serviceID
	var version store.StoreContentPackVersion
	if d.Outcome == store.ProductionOutcomeProduced {
		// 两端哈希核对 + 真实字节落受控媒体库(先于一切登记,不落半批事实)。
		video, okV := s.saveProducerMedia(w, form, "video", d.VideoSHA, "video", h.TenantID, h.StoreID, actor)
		if !okV {
			return
		}
		cover, okC := s.saveProducerMedia(w, form, "cover", d.CoverSHA, "image", h.TenantID, h.StoreID, actor)
		if !okC {
			return
		}
		spec := store.StoreVersionSpec{
			ContentKind:    contentpack.ContentKindVideo,
			VideoMediaID:   video.ID,
			CoverMediaID:   cover.ID,
			Title:          d.Title,
			Body:           d.Body,
			Topics:         d.Topics,
			StoreName:      d.StoreName,
			PriceNote:      d.PriceNote,
			ValidNote:      d.ValidNote,
			Marking:        contentpack.Marking{AdRequired: d.AdRequired, IncentiveDisclosed: d.AdRequired, AIGenerated: d.AIGenerated},
			Source:         contentpack.SourceProductPhotoReturn,
			IdempotencyKey: d.SourceApp + ":" + d.EventID,
		}
		v, candidateReplayed, err := s.St.AddStoreReturnCandidate(h.TenantID, h.PackID, spec, actor)
		if err != nil {
			s.auditResultReturn(serviceID, handoffID, d.SourceApp, d.EventID, "rejected", err)
			packStoreFail(w, err)
			return
		}
		version = v
		if candidateReplayed {
			// 候选登记事务自身命中同一幂等键(例如上次回执行落库前的中断):
			// 引用同一候选行,不是新增(唯一候选登记事务的语义)。
			s.auditResultReturn(serviceID, handoffID, d.SourceApp, d.EventID, "candidate_replayed", nil)
		}
		in.VersionID = v.ID
		in.DeclaredSHA = d.VideoSHA
		in.MeasuredSHA = video.SHA256
	} else {
		in.VersionID = ""
	}

	r2, err := s.St.AppendProductionResult(in)
	if err != nil {
		s.auditResultReturn(serviceID, handoffID, d.SourceApp, d.EventID, "rejected", err)
		packStoreFail(w, err)
		return
	}
	s.auditResultReturn(serviceID, handoffID, d.SourceApp, d.EventID, "ok", nil)
	// 回流后的交接状态以行为准(Append 已推进 produced/failed/canceled)。
	cur, herr := s.St.GetProductionHandoff(h.TenantID, h.ID)
	out := map[string]any{
		"result": productionResultView(r2), "replayed": false,
	}
	if herr == nil {
		out["handoff_status"] = cur.Status
	}
	if d.Outcome == store.ProductionOutcomeProduced {
		out["version"] = storeVersionView(version)
		// 候选立即「可播放真实媒体」:商家走 media-access preview(短 TTL 受控
		// URL,纯预览零计费);顾客侧仍只在明确采用后经 B05 取得。
		out["preview_hint"] = "候选已登记:商家可在素材页预览(media-access)并明确采用;顾客侧在采用前不可见。"
	}
	writeJSON(w, http.StatusCreated, out)
}

// auditResultReturn logs the trusted-service write (same discipline as
// auditQueueViewProducer): only non-secret facts; the service token, file
// bytes and digests never enter the log.
func (s *Server) auditResultReturn(serviceID, handoffID, sourceApp, eventID, result string, err error) {
	if s == nil || s.Log == nil {
		return
	}
	line := "content_production_return service=" + serviceID +
		" handoff=" + handoffID + " source=" + sourceApp + " event=" + eventID + " result=" + result
	if err != nil {
		line += " reason=" + err.Error()
	}
	s.Log.Print(line)
}
