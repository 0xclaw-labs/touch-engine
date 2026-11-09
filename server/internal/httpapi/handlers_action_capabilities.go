// handlers_action_capabilities.go: HUI-3535 (2026-11-09 增量) 动作级能力
// 投影与平台端受控读写的 HTTP 面。
//
// 纪律:
//   - 开关:沿用 FEATURE_CHANNEL_ADMISSION(默认 off)= 路由不注册且本 gate 再
//     答统一 404 —— 双保险同款;
//   - 公开面(GET /api/v1/public/actions/{id}/capabilities)只答平台级真值
//     (tenant_scope='*'),不接受客户端租户参数(拒绝探测面);{id} 是动作目录码
//     (A01–A30,大小写不敏感);公开投影白名单字段,seed_version/payload_hash/
//     tenant_scope 等内部字段绝不出现;
//   - 四分能力(navigation/media_handoff/platform_receipt/reward_eligibility)
//     原样返回存储值,绝不互相推断;routes 按 method×environment 展开(B04
//     routes 口径),环境过滤参数非法=400;
//   - unknown 路由=PENDING_EXTERNAL 语义(pending_external=true,promised=
//     false):顾客侧不承诺、不显示可完成态,永不自动降级为 manual 许可;
//   - 管理面(/api/v1/channel-actions):读=internal token 全量(4592 O4 消费面)
//     或会话租户视图(1676 消费面,只看 '*'+本租户);写(受控 upsert/紧急停用/
//     审计查询)=internal token 独占 —— 商家会话永不写准入真值,平台权威在
//     平台运营;每次写必留审计(操作者/原因/动作/载荷指纹)。
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// publicCapabilityRoute is one whitelisted route entry (method×environment).
type publicCapabilityRoute struct {
	Platform          string            `json:"platform"`
	Method            string            `json:"method"`
	Environment       string            `json:"environment"`
	Scenario          string            `json:"scenario"`
	AppBinding        string            `json:"app_binding"`
	Status            string            `json:"status"`
	Allowed           bool              `json:"allowed"`
	Promised          bool              `json:"promised"`
	PendingExternal   bool              `json:"pending_external"`
	Reason            string            `json:"reason"`
	ReasonText        string            `json:"reason_text"`
	Navigation        string            `json:"navigation"`
	MediaHandoff      string            `json:"media_handoff"`
	PlatformReceipt   string            `json:"platform_receipt"`
	RewardEligibility string            `json:"reward_eligibility"`
	FieldSupport      map[string]string `json:"field_support"`
	RequiredInputs    []string          `json:"required_inputs"`
	Missing           []string          `json:"missing"`
	OfficialSources   []string          `json:"official_sources"`
	EvidenceGrade     string            `json:"evidence_grade"`
	CheckedAt         string            `json:"checked_at"`
	ValidUntil        string            `json:"valid_until"`
	EmergencyDisabled bool              `json:"emergency_disabled"`
	CloseReason       string            `json:"close_reason"`
}

type publicActionCapabilitiesPayload struct {
	ActionID      string                  `json:"action_id"`
	CatalogCode   string                  `json:"catalog_code"`
	ActionKind    string                  `json:"action_kind"`
	Label         string                  `json:"label"`
	SuggestedOnly bool                    `json:"suggested_only"`
	Environment   string                  `json:"environment,omitempty"`
	Routes        []publicCapabilityRoute `json:"routes"`
}

// handlePublicActionCapabilities resolves one catalog action into structured,
// per-environment capability routes. 客户端以 reward=false 绕过的口子在这里
// 不存在:公开投影恒 RewardPlay=false,奖励判定由消费方服务端按动作的奖励
// 性质(server-derived)走 JudgeActionRoute。
func (s *Server) handlePublicActionCapabilities(w http.ResponseWriter, r *http.Request) {
	if !s.admissionGate(w) {
		return
	}
	code := strings.ToUpper(strings.TrimSpace(r.PathValue("id")))
	if code == "" {
		fail(w, http.StatusBadRequest, "param_required", "action id path parameter is required")
		return
	}
	q := r.URL.Query()
	envFilter := strings.TrimSpace(q.Get("environment"))
	if envFilter != "" && !isAdmissionEnvironment(envFilter) {
		fail(w, http.StatusBadRequest, "param_invalid",
			"environment must be one of desktop_browser|ios_safari|android_browser|ios_wechat|android_wechat|target_app")
		return
	}
	methodFilter := strings.TrimSpace(q.Get("method"))
	scenarioFilter := strings.TrimSpace(q.Get("scenario"))

	rows, err := s.St.GetActionCapabilitiesByCode(code, "")
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "action capability lookup failed")
		return
	}
	if len(rows) == 0 {
		// 未登记动作:诚实 404,不泄露库内存在性,也绝不合成可完成路线。
		fail(w, http.StatusNotFound, "not_found", "unknown action")
		return
	}
	routes := make([]publicCapabilityRoute, 0, len(rows)*len(channeladmission.Environments))
	for _, row := range rows {
		if methodFilter != "" && row.Method != methodFilter {
			continue
		}
		if scenarioFilter != "" && row.Scenario != scenarioFilter {
			continue
		}
		for _, env := range channeladmission.Environments {
			if envFilter != "" && env != envFilter {
				continue
			}
			d := channeladmission.JudgeActionRoute(channeladmission.ActionQuery{
				Now: time.Now().UTC(), TenantID: "", Environment: env,
				// RewardPlay/MerchantAppRef 不进公开面:奖励判定在消费方服务端
				// 依据动作奖励性质与真实 App 绑定派生,客户端标记不可信。
			}, row)
			caps := d.Caps
			routes = append(routes, publicCapabilityRoute{
				Platform: row.Platform, Method: row.Method, Environment: env,
				Scenario: row.Scenario, AppBinding: row.AppBinding, Status: row.Status,
				Allowed: d.Allowed, Promised: d.Promised, PendingExternal: d.PendingExternal,
				Reason: d.Reason, ReasonText: d.ReasonText,
				Navigation: caps.Navigation, MediaHandoff: caps.MediaHandoff,
				PlatformReceipt: caps.PlatformReceipt, RewardEligibility: caps.RewardEligibility,
				FieldSupport: orEmptyMap(row.FieldSupport), RequiredInputs: orEmptySlice(row.RequiredInputs),
				Missing: orEmptySlice(d.Missing), OfficialSources: orEmptySlice(row.OfficialSources),
				EvidenceGrade: row.EvidenceGrade, CheckedAt: row.CheckedAt, ValidUntil: row.ValidUntil,
				EmergencyDisabled: row.EmergencyDisabled, CloseReason: row.CloseReason,
			})
		}
	}
	first := rows[0]
	writeJSON(w, http.StatusOK, publicActionCapabilitiesPayload{
		ActionID: first.CatalogCode, CatalogCode: first.CatalogCode,
		ActionKind: first.ActionKind, Label: first.Label, SuggestedOnly: first.SuggestedOnly,
		Environment: envFilter, Routes: routes,
	})
}

func isAdmissionEnvironment(s string) bool {
	for _, e := range channeladmission.Environments {
		if s == e {
			return true
		}
	}
	return false
}

func orEmptySlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func orEmptyMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// ---- 管理面:受控读写(internal token 平台权威 + 会话租户视图) -------------------

// handleChannelActionList: internal token → 全量(含他租户定向行,O4/4592 消费);
// 会话 → 租户可见视图('*'+本租户,1676 消费);两者皆无 → 401。
func (s *Server) handleChannelActionList(w http.ResponseWriter, r *http.Request) {
	if !s.admissionGate(w) {
		return
	}
	if s.Cfg.InternalToken != "" && r.Header.Get(internalTokenHeader) == s.Cfg.InternalToken {
		rows, err := s.St.ListActionCapabilitiesAll()
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "action capability lookup failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"records": actionAdminPayloads(rows), "scope": "platform"})
		return
	}
	c := callerFrom(r)
	if c == nil || c.Member == nil {
		fail(w, http.StatusUnauthorized, "unauthenticated", "internal token or session required")
		return
	}
	rows, err := s.St.ListActionCapabilities(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "action capability lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"records": actionAdminPayloads(rows), "scope": "tenant"})
}

type actionAdminRecord struct {
	publicCapabilityRoute
	TenantScope string `json:"tenant_scope"`
}

func actionAdminPayloads(rows []channeladmission.ActionCapability) []actionAdminRecord {
	now := time.Now().UTC()
	out := make([]actionAdminRecord, 0, len(rows)*len(channeladmission.Environments))
	for _, row := range rows {
		for _, env := range channeladmission.Environments {
			d := channeladmission.JudgeActionRoute(channeladmission.ActionQuery{
				Now: now, TenantID: row.TenantScope, Environment: env,
			}, row)
			caps := d.Caps
			out = append(out, actionAdminRecord{
				publicCapabilityRoute: publicCapabilityRoute{
					Platform: row.Platform, Method: row.Method, Environment: env,
					Scenario: row.Scenario, AppBinding: row.AppBinding, Status: row.Status,
					Allowed: d.Allowed, Promised: d.Promised, PendingExternal: d.PendingExternal,
					Reason: d.Reason, ReasonText: d.ReasonText,
					Navigation: caps.Navigation, MediaHandoff: caps.MediaHandoff,
					PlatformReceipt: caps.PlatformReceipt, RewardEligibility: caps.RewardEligibility,
					FieldSupport: orEmptyMap(row.FieldSupport), RequiredInputs: orEmptySlice(row.RequiredInputs),
					Missing: orEmptySlice(d.Missing), OfficialSources: orEmptySlice(row.OfficialSources),
					EvidenceGrade: row.EvidenceGrade, CheckedAt: row.CheckedAt, ValidUntil: row.ValidUntil,
					EmergencyDisabled: row.EmergencyDisabled, CloseReason: row.CloseReason,
				},
				TenantScope: row.TenantScope,
			})
		}
	}
	return out
}

// handleChannelActionUpsert is the controlled record endpoint (管理员权限 =
// internal token): one validated capability record write with a mandatory
// actor+reason audit trail. 商家会话无此路径。
func (s *Server) handleChannelActionUpsert(w http.ResponseWriter, r *http.Request) {
	if !s.admissionGate(w) {
		return
	}
	var body struct {
		channeladmission.ActionCapability
		Actor  string `json:"actor"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "bad_json", "request body must be a capability record")
		return
	}
	if strings.TrimSpace(body.Actor) == "" {
		fail(w, http.StatusBadRequest, "actor_required", "actor (操作者) is required for controlled writes")
		return
	}
	if strings.TrimSpace(body.Reason) == "" {
		fail(w, http.StatusBadRequest, "reason_required", "reason (变更原因) is required for controlled writes")
		return
	}
	if err := body.Validate(); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": "invalid_record", "message": err.Error(),
		})
		return
	}
	created, err := s.St.UpsertActionCapability(body.ActionCapability, store.ControlAudit{
		Actor: strings.TrimSpace(body.Actor), Reason: strings.TrimSpace(body.Reason),
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "controlled write failed")
		return
	}
	if created {
		writeJSON(w, http.StatusCreated, map[string]any{
			"record": body.ActionCapability, "result": "created",
			"notice": "受控记录已写入并留审计；资格未知(unknown)不构成允许。",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"record": body.ActionCapability, "result": "unchanged",
		"notice": "载荷指纹未变化,无写入、无审计追加。",
	})
}

// handleChannelActionEmergency flips the independent emergency veto (只停对应
// 方法/动作,不影响其他行;O4/4592 的暂停/恢复走这里)。
func (s *Server) handleChannelActionEmergency(w http.ResponseWriter, r *http.Request) {
	if !s.admissionGate(w) {
		return
	}
	var body struct {
		Disabled bool   `json:"disabled"`
		Reason   string `json:"reason"`
		Actor    string `json:"actor"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "bad_json", "request body must be {disabled, reason}")
		return
	}
	if strings.TrimSpace(body.Reason) == "" {
		fail(w, http.StatusBadRequest, "reason_required", "reason (变更原因) is required for emergency toggles")
		return
	}
	actor := strings.TrimSpace(body.Actor)
	if actor == "" {
		actor = "platform-ops"
	}
	// found 恒为 true when err==nil（store 已把未知 id 映射为 ErrNotFound）。
	if _, err := s.St.SetActionCapabilityEmergency(r.PathValue("id"), body.Disabled, store.ControlAudit{
		Actor: actor, Reason: strings.TrimSpace(body.Reason),
		Action: "emergency_disable",
	}); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(w, http.StatusNotFound, "not_found", "unknown capability record")
			return
		}
		fail(w, http.StatusInternalServerError, "internal", "emergency toggle failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": r.PathValue("id"), "emergency_disabled": body.Disabled,
		"notice": "紧急停用为独立否决：只停对应方法/动作，无关导航与其他动作不受影响。",
	})
}

// handleChannelActionAudit returns the controlled-change trail (internal read).
func (s *Server) handleChannelActionAudit(w http.ResponseWriter, r *http.Request) {
	if !s.admissionGate(w) {
		return
	}
	rows, err := s.St.ListActionCapabilityAudit(strings.ToUpper(strings.TrimSpace(r.PathValue("code"))), 200)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "audit lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"audit": rows})
}
