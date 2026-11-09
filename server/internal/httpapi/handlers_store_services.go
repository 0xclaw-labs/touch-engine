package httpapi

// HUI-4589 B07 门店连接域 HTTP 面。
//
//   - 商家面（GET/PUT /api/v1/stores/{id}/services）：4584 actions 同风格 CRUD——
//     鉴权复用 authz（GET=ReadRecord、PUT=Update，store scope），expected_revision
//     乐观锁（409 stale_version 零写入），负载内重复/未知 id 明确拒绝，内容一致
//     重放幂等。config 按 kind 形状校验（企微必须 work.weixin.qq.com 官方链接、
//     外链公网 https）。文档回显企微回调资质诚实态（待资质/已配置）。
//   - 公共面（GET /api/v1/public/storefronts/{code}/services）：FEATURE_STOREFRONT
//     双保险 gate。白名单投影：点评/关注/QQ群动作投受控解析的 target_url + 3535
//     direct_open 六环境能力判定（无能力记录=admission unknown，绝不 promised）；
//     Wi-Fi 投 SSID/密码/加密方式（仅展示+主动复制，无任何留资/会话前置）；微信投
//     二维码/微信号/企微官方链接；社群投群号/邀请链接+审批备注原样。点评投影
//     零内容字段（零模板零AIGC后端侧拦）。
//   - 事件面（POST .../services/events）：只收 open/jump_attempt/copy 三种行为
//     证据；响应明示「不代表已关注/已连接/已添加/已点评」。成功语义在整个存储
//     结构上不存在。

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// storeServicesSchemaVersion freezes the public services payload shape.
const storeServicesSchemaVersion = "store-services.v1"

// eventsHonestyNote is appended to every event acknowledgement: the evidence
// tier is click/jump_attempt and never a platform success.
const eventsHonestyNote = "已记录为顾客点击/尝试证据；不代表已关注、不代表已连接、不代表已添加、不代表已点评——无平台官方回执本系统不记录任何成功。"

// ---- 商家面 -------------------------------------------------------------------

type storeServiceIn struct {
	ServiceID   string         `json:"service_id"`
	CatalogCode string         `json:"catalog_code"`
	Kind        string         `json:"kind"`
	Variant     string         `json:"variant"`
	Label       string         `json:"label"`
	Config      map[string]any `json:"config"`
	Order       int            `json:"order"`
	Enabled     bool           `json:"enabled"`
}

func (s *Server) handleStoreServicesGet(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	rec, ok := s.storeForActions(w, r, c)
	if !ok {
		return
	}
	if !s.requireScopedAction(c, authz.ActionReadRecord, authz.RecordScope{TenantID: c.Member.TenantID, StoreID: rec.ID}, true, w) {
		return
	}
	sf, items, rev, ok := s.loadStoreServices(w, c.Member.TenantID, rec.ID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, storeServicesDocument(rec, sf, items, rev, s.wecomCallbackState()))
}

func (s *Server) handleStoreServicesPut(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	rec, ok := s.storeForActions(w, r, c)
	if !ok {
		return
	}
	if !s.requireScopedAction(c, authz.ActionUpdate, authz.RecordScope{TenantID: c.Member.TenantID, StoreID: rec.ID}, false, w) {
		return
	}
	var body struct {
		ExpectedRevision *int              `json:"expected_revision"`
		Items            *[]storeServiceIn `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ExpectedRevision == nil || body.Items == nil {
		fail(w, http.StatusBadRequest, "bad_request", "expected_revision and items are required")
		return
	}
	inputs := make([]store.StoreServiceInput, 0, len(*body.Items))
	for _, raw := range *body.Items {
		label := strings.TrimSpace(raw.Label)
		if len(label) > 200 {
			fail(w, http.StatusBadRequest, "bad_label", "label is too long")
			return
		}
		inputs = append(inputs, store.StoreServiceInput{
			ID: strings.TrimSpace(raw.ServiceID), CatalogCode: strings.TrimSpace(raw.CatalogCode),
			Kind: strings.TrimSpace(raw.Kind), Variant: strings.TrimSpace(raw.Variant),
			Label: label, Config: raw.Config, Order: raw.Order, Enabled: raw.Enabled,
		})
	}
	rev, items, err := s.St.ReplaceStoreServices(c.Member.TenantID, rec.ID, *body.ExpectedRevision, inputs)
	switch {
	case errors.Is(err, store.ErrStaleRevision):
		fail(w, http.StatusConflict, "stale_version", "config was changed by someone else; reload and retry")
		return
	case errors.Is(err, store.ErrDuplicateServiceID):
		fail(w, http.StatusBadRequest, "duplicate_service_id", "the same service_id appears twice in items")
		return
	case errors.Is(err, store.ErrUnknownServiceID):
		fail(w, http.StatusBadRequest, "unknown_service_id", "a service_id does not exist in this store")
		return
	case errors.Is(err, store.ErrBadServiceConfig):
		fail(w, http.StatusBadRequest, "bad_service_config", "service config rejected: "+err.Error())
		return
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "not_found", "store not found")
		return
	case err != nil:
		fail(w, http.StatusInternalServerError, "internal", "store services could not be saved")
		return
	}
	sf := storefrontOf(w, s, c.Member.TenantID, rec.ID)
	writeJSON(w, http.StatusOK, storeServicesDocument(rec, sf, items, rev, s.wecomCallbackState()))
}

func (s *Server) loadStoreServices(w http.ResponseWriter, tenantID, storeID string) (store.Storefront, []store.StoreService, int, bool) {
	sf, err := s.St.StorefrontForStore(tenantID, storeID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "storefront could not be provisioned")
		return store.Storefront{}, nil, 0, false
	}
	items, rev, err := s.St.ListStoreServices(tenantID, storeID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "store services lookup failed")
		return store.Storefront{}, nil, 0, false
	}
	return sf, items, rev, true
}

// wecomCallbackState is the honest credential state surfaced to the merchant:
// 缺失凭据绝不显示为已配置（待资质）。
func (s *Server) wecomCallbackState() map[string]any {
	if s.Cfg.WecomCallbackToken == "" || s.Cfg.WecomCallbackAESKey == "" || s.Cfg.WecomCallbackCorpID == "" {
		return map[string]any{
			"state": "pending_qualification",
			"note":  "企微客户事件回调凭据未配置（待资质）：官方权限未核实，URL 验证无法完成；资质核实前不记录任何「客户已添加」。",
		}
	}
	return map[string]any{
		"state": "configured",
		"note":  "回调凭据已配置；回调仅作为证据留存（digest-only），添加结果仍以企业微信官方侧为准。",
	}
}

func storeServicesDocument(rec store.StoreRecord, sf store.Storefront, items []store.StoreService, rev int, callback map[string]any) map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"service_id":       item.ID,
			"service_revision": item.Revision,
			"catalog_code":     item.CatalogCode,
			"kind":             item.Kind,
			"variant":          item.Variant,
			"label":            item.Label,
			"config":           item.ConfigJSON,
			"order":            item.SortOrder,
			"enabled":          item.Enabled,
			"revision":         item.Revision,
		})
	}
	return map[string]any{
		"store_id":        rec.ID,
		"store_name":      rec.Name,
		"store_code":      sf.StoreCode,
		"config_revision": rev,
		"wecom_callback":  callback,
		"items":           out,
	}
}

// ---- 公共面 -------------------------------------------------------------------

// storefrontForServices resolves the store code with the same fail-closed
// gates as the storefront read (tenant lifecycle, store status).
func (s *Server) storefrontForServices(w http.ResponseWriter, r *http.Request) (store.StorefrontPublic, []store.StoreAction, []store.StoreService, bool) {
	if !s.Cfg.FeatureStorefront {
		fail(w, http.StatusNotFound, "not_found", "storefront is not available")
		return store.StorefrontPublic{}, nil, nil, false
	}
	pub, err := s.St.GetStorefrontPublicByCode(r.PathValue("storeCode"))
	if err != nil {
		fail(w, http.StatusNotFound, "not_found", "storefront is not available")
		return store.StorefrontPublic{}, nil, nil, false
	}
	if pub.TenantLifecycle != "active" {
		fail(w, http.StatusNotFound, "not_found", "storefront is not available")
		return store.StorefrontPublic{}, nil, nil, false
	}
	now := time.Now().UTC()
	actions, err := s.St.ListPublicStoreActions(pub.StoreID, now)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "storefront lookup failed")
		return store.StorefrontPublic{}, nil, nil, false
	}
	services, err := s.St.ListPublicStoreServices(pub.StoreID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "store services lookup failed")
		return store.StorefrontPublic{}, nil, nil, false
	}
	if pub.StoreStatus != "active" {
		actions, services = nil, nil
	}
	return pub, actions, services, true
}

func (s *Server) handlePublicStoreServices(w http.ResponseWriter, r *http.Request) {
	pub, actions, services, ok := s.storefrontForServices(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	envFilter := strings.TrimSpace(q.Get("environment"))
	if envFilter != "" && !isAdmissionEnvironment(envFilter) {
		fail(w, http.StatusBadRequest, "param_invalid",
			"environment must be one of desktop_browser|ios_safari|android_browser|ios_wechat|android_wechat|target_app")
		return
	}
	idFilter := strings.TrimSpace(q.Get("action_id"))

	items := make([]map[string]any, 0, len(actions)+len(services))
	for _, action := range actions {
		doc, ok := s.publicActionServiceItem(action, pub.TenantID, envFilter)
		if !ok {
			continue
		}
		items = append(items, doc)
	}
	for _, svc := range services {
		items = append(items, publicStoreServiceItem(svc))
	}
	if idFilter != "" {
		filtered := make([]map[string]any, 0, 1)
		for _, item := range items {
			if item["service_id"] == idFilter {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}

	state := "active"
	if pub.StoreStatus != "active" {
		state = "disabled"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version": storeServicesSchemaVersion,
		"store_code":     pub.StoreCode,
		"store": map[string]any{
			"name": pub.StoreName, "address": pub.StoreAddress, "state": state,
		},
		"services": items,
	})
}

// actionPanelKind maps catalog action kinds onto the C3 panel kinds. publish
// actions belong to the content/handoff domain and never appear here.
func actionPanelKind(kind string) (string, bool) {
	switch kind {
	case "review":
		return "review", true
	case "follow":
		return "follow", true
	case "join_group":
		return "group", true
	}
	return "", false
}

// actionScenario maps the panel kind onto the 3535 capability scenario.
func actionScenario(kind string) string {
	if kind == "review" {
		return channeladmission.ActionScenarioReview
	}
	return channeladmission.ActionScenarioFollow // follow / join_group（社群）
}

// publicActionServiceItem projects one visible store action into its panel
// item: controlled target resolution + 3535 direct_open capability judgment.
func (s *Server) publicActionServiceItem(action store.StoreAction, tenantID, envFilter string) (map[string]any, bool) {
	kind, ok := actionPanelKind(action.Kind)
	if !ok {
		return nil, false
	}
	doc := map[string]any{
		"service_id":   action.ID,
		"kind":         kind,
		"catalog_code": action.CatalogCode,
		"platform":     action.Platform,
		"variant":      action.Variant,
		"label":        action.Label,
		"target_url":   action.TargetRef,
		"guidance":     panelGuidance(kind),
	}
	// 3535 能力消费（不自建白名单）：direct_open × 本动作场景 × 六环境。
	routes := []map[string]any{}
	admission := "unknown"
	rows, err := s.St.GetActionCapabilitiesByCode(action.CatalogCode, tenantID)
	if err == nil {
		anyPromised, anyPending := false, false
		for _, row := range rows {
			if row.Method != channeladmission.ActionMethodDirectOpen || row.Scenario != actionScenario(kind) {
				continue
			}
			if action.Platform != "" && row.Platform != action.Platform {
				continue
			}
			for _, env := range channeladmission.Environments {
				if envFilter != "" && env != envFilter {
					continue
				}
				d := channeladmission.JudgeActionRoute(channeladmission.ActionQuery{
					Now: time.Now().UTC(), TenantID: tenantID, Environment: env,
					// RewardPlay 恒 false：点评/关注场景的奖励由 3535 真值约束
					// （点评种子 reward=unsupported），绝不由客户端标记开启。
				}, row)
				caps := d.Caps
				routes = append(routes, map[string]any{
					"method":             row.Method,
					"environment":        env,
					"allowed":            d.Allowed,
					"promised":           d.Promised,
					"pending_external":   d.PendingExternal,
					"reason":             d.Reason,
					"navigation":         caps.Navigation,
					"platform_receipt":   caps.PlatformReceipt,
					"reward_eligibility": caps.RewardEligibility,
				})
				if d.Promised {
					anyPromised = true
				}
				if d.PendingExternal {
					anyPending = true
				}
			}
		}
		switch {
		case len(routes) == 0:
			admission = "unknown"
		case anyPromised:
			admission = "promised"
		case anyPending:
			admission = "pending_external"
		default:
			admission = "blocked"
		}
	}
	doc["routes"] = routes
	doc["admission"] = admission
	return doc, true
}

// publicStoreServiceItem projects one store service row. Wi-Fi 投影带密码
// （商家主动公示的连接凭据；仅展示+主动复制）；点评类内容字段在此结构中不存在。
// 公共投影统一用 C3 面板 kind（社群存 storage kind=community，投影为 group）。
func publicStoreServiceItem(svc store.StoreService) map[string]any {
	cfg := svc.ConfigJSON
	str := func(key string) string {
		v, _ := cfg[key].(string)
		return strings.TrimSpace(v)
	}
	kind := svc.Kind
	if kind == store.ServiceKindCommunity {
		kind = "group"
	}
	doc := map[string]any{
		"service_id":   svc.ID,
		"kind":         kind,
		"catalog_code": svc.CatalogCode,
		"variant":      svc.Variant,
		"label":        svc.Label,
	}
	switch svc.Kind {
	case store.ServiceKindWifi:
		doc["wifi"] = map[string]any{
			"ssid": str("ssid"), "password": str("password"), "encryption": str("encryption"),
		}
		doc["guidance"] = wifiGuidance()
	case store.ServiceKindContact:
		if qr := str("qr_ref"); qr != "" {
			doc["qr_url"] = qr
		}
		if id := str("wechat_id"); id != "" {
			doc["wechat_id"] = id
		}
		if url := str("url"); url != "" {
			doc["wecom_url"] = url
		}
		if note := str("source_note"); note != "" {
			doc["source_note"] = note
		}
		doc["guidance"] = contactGuidance(svc.Variant)
	case store.ServiceKindCommunity:
		if g := str("group_no"); g != "" {
			doc["group_no"] = g
		}
		if u := str("invite_url"); u != "" {
			doc["invite_url"] = u
		}
		if qr := str("qr_ref"); qr != "" {
			doc["qr_url"] = qr
		}
		if note := str("approval_note"); note != "" {
			doc["approval_note"] = note
		}
		doc["guidance"] = panelGuidance("group")
	}
	return doc
}

// panelGuidance is the fixed honest guidance per panel kind. 点评指引零模板
// 零AIGC零截图核验零奖励暗示；关注/加群明示点击不是结果。
func panelGuidance(kind string) map[string]any {
	switch kind {
	case "review":
		return map[string]any{
			"title": "到对应平台自主评价",
			"steps": []string{
				"打开该平台内本店的门店主页",
				"按平台自己的流程查找门店、撰写并发表你的评价",
			},
			"notices": []string{
				"本入口不提供评价内容，也没有任何模板或自动生成；评价文字完全由你自己决定。",
				"不需要向本店提交评价截图；评价是否发布以平台为准，且不与任何奖励挂钩。",
			},
		}
	case "follow":
		return map[string]any{
			"title": "前往账号主页关注",
			"steps": []string{
				"打开账号主页链接查看并确认账号信息",
				"在平台内自行点击关注；未安装该平台时按平台提示搜索同名账号",
			},
			"notices": []string{"点击只记录为点击；本系统不记录、也不显示「已关注」结果。"},
		}
	case "group":
		return map[string]any{
			"title":   "加入社群",
			"steps":   []string{"通过群号或邀请链接在对应平台内申请加群", "按平台规则等待群主/管理员通过"},
			"notices": []string{"点击不代表已加群；群若在审批中会如实标注，以平台群状态为准。"},
		}
	}
	return map[string]any{"steps": []string{}}
}

// wifiGuidance carries the manual fallback (T12: 连接失败→手动指引回退).
func wifiGuidance() map[string]any {
	return map[string]any{
		"title": "连接门店 Wi-Fi",
		"steps": []string{
			"复制密码后，到系统 Wi-Fi 设置中选择对应的 Wi-Fi 名称并粘贴密码",
			"连接失败时回到本页核对名称密码，或直接询问店员获取当前密码",
		},
		"notices": []string{"本页只展示连接信息，不能替你完成连接；是否连上以你的系统 Wi-Fi 设置为准。"},
	}
}

// contactGuidance gives environment-aware recognition steps (T13 微信).
func contactGuidance(variant string) map[string]any {
	if variant == "wecom" {
		return map[string]any{
			"title": "添加企业微信",
			"steps": []string{
				"打开企业微信「联系我」链接，在企业微信中确认并添加",
				"未安装企业微信时，可在微信内按链接提示继续",
			},
			"notices": []string{"打开链接或点击不代表已添加；添加结果以企业微信官方为准。"},
		}
	}
	return map[string]any{
		"title": "添加个人微信",
		"steps": []string{
			"微信内长按识别二维码，或截图后使用微信扫一扫",
			"也可以复制微信号后在微信搜索添加",
		},
		"notices": []string{"扫码或复制微信号不代表已添加；对方通过好友申请后才算添加成功，以微信官方为准。"},
	}
}

// ---- 事件面 -------------------------------------------------------------------

func (s *Server) handlePublicStoreServiceEvents(w http.ResponseWriter, r *http.Request) {
	// 无会话、无留资——事件只证明「有人点了」。独立小预算限流防探测。
	ip := clientIP(r)
	if s.ServiceEventsLimiter != nil && !s.ServiceEventsLimiter.Allow(ip) {
		fail(w, http.StatusTooManyRequests, "rate_limited", "too many events from this client, retry later")
		return
	}
	pub, actions, services, ok := s.storefrontForServices(w, r)
	if !ok {
		return
	}
	var body struct {
		ServiceID   string `json:"service_id"`
		Event       string `json:"event"`
		Environment string `json:"environment"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "service_id and event are required")
		return
	}
	serviceID := strings.TrimSpace(body.ServiceID)
	event := strings.TrimSpace(body.Event)
	if serviceID == "" || event == "" {
		fail(w, http.StatusBadRequest, "bad_request", "service_id and event are required")
		return
	}
	environment := strings.TrimSpace(body.Environment)
	if environment != "" && !isAdmissionEnvironment(environment) {
		// 事件是证据：environment 与 3535 六环境同口径，脏值不入证据表。
		fail(w, http.StatusBadRequest, "param_invalid",
			"environment must be one of desktop_browser|ios_safari|android_browser|ios_wechat|android_wechat|target_app")
		return
	}

	// service_id 必须当帧在公共面可见（fail-closed：看不到的项不给记事件）。
	var tenantID, storeID string
	var isAction, found bool
	for _, action := range actions {
		if action.ID == serviceID {
			if _, ok := actionPanelKind(action.Kind); ok {
				tenantID, storeID, isAction, found = pub.TenantID, pub.StoreID, true, true
			}
			break
		}
	}
	if !found {
		for _, svc := range services {
			if svc.ID == serviceID {
				tenantID, storeID, found = pub.TenantID, pub.StoreID, true
				break
			}
		}
	}
	if !found {
		fail(w, http.StatusNotFound, "not_found", "service is not available")
		return
	}
	var recordErr error
	if isAction {
		recordErr = s.St.RecordActionEvent(tenantID, storeID, serviceID, event, environment)
	} else {
		recordErr = s.St.RecordServiceEvent(tenantID, storeID, serviceID, event, environment)
	}
	if recordErr != nil {
		// 成功语义枚举（followed 等）与未知服务都在这里被拒绝。
		fail(w, http.StatusBadRequest, "bad_event", "event must be one of open|jump_attempt|copy")
		return
	}
	tier := "click"
	if event == store.ServiceEventJumpAttempt {
		tier = "jump_attempt"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"recorded":      true,
		"event":         event,
		"evidence_tier": tier,
		"note":          eventsHonestyNote,
	})
}
