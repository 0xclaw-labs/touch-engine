// handlers_channel_admission.go: HUI-3535 渠道准入与承接清单的 HTTP 读面。
//
// 纪律:
//   - 开关:FEATURE_CHANNEL_ADMISSION(默认 off)= 路由不注册(见 server.go
//     条件注册)且本 gate 再答统一 404 —— 与 FEATURE_DASHBOARD / FEATURE_AGENCY
//     等的 off 双保险同一写法;
//   - 纯只读:两个 GET 端点,零写路径;裁决全部来自 channeladmission.Gate
//     纯函数(服务端单点),HTTP 层零业务判断,前端隐藏按钮不算数;
//   - 商家面(/api/v1/channel-admissions):HUI-1676 消费 —— 按工作区租户返回
//     可选渠道与缺项原因(被禁用原因及所需材料对商家可见);
//   - 顾客/交接面(/api/v1/public/links/{code}/channel-admission):HUI-1670/
//     1896 消费 —— 公共面 GET-only 白名单字段,租户取自短码链路本身(查询
//     参数不参与租户选择,绝不跨租户探测);未登记路线按 unknown 规则诚实
//     拒绝,不默认允许;
//   - v1 种子无 admitted 路线:两面都如实输出缺项,不制造"可发布"假象;
//     渠道关闭(channel_closed)后本面只报告,停止新交接的执行归 HUI-1670,
//     既有参与保留状态与履约处理入口(语义文档化于 channeladmission 包)。
package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// admissionGate answers uniform 404 when FEATURE_CHANNEL_ADMISSION is off: an
// invisible surface (agencyGate/dashboardGate 同一纪律).
func (s *Server) admissionGate(w http.ResponseWriter) bool {
	if s.Cfg.FeatureChannelAdmission {
		return true
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found", "message": "resource not found"})
	return false
}

// admissionPayload is the whitelisted projection of one route + its Gate
// verdict. Both surfaces share it; the public surface simply sends the same
// fields (nothing here is tenant-internal — official sources are public docs).
type admissionPayload struct {
	Platform        string                        `json:"platform"`
	EntryMode       string                        `json:"entry_mode"`
	Scenario        string                        `json:"scenario"`
	AppBinding      string                        `json:"app_binding"`
	Status          string                        `json:"status"`
	RewardAdmitted  bool                          `json:"reward_admitted"`
	Allowed         bool                          `json:"allowed"`
	Reason          string                        `json:"reason"`
	ReasonText      string                        `json:"reason_text"`
	Missing         []string                      `json:"missing"`
	Capabilities    channeladmission.Capabilities `json:"capabilities"`
	OfficialSources []string                      `json:"official_sources"`
	LastCheckedAt   string                        `json:"last_checked_at"`
	ValidUntil      string                        `json:"valid_until"`
	CloseReason     string                        `json:"close_reason"`
}

func admissionPayloadOf(in channeladmission.GateInput, r channeladmission.Record) admissionPayload {
	d := channeladmission.Gate(in, r)
	return admissionPayload{
		Platform: r.Platform, EntryMode: r.EntryMode, Scenario: r.Scenario,
		AppBinding: r.AppBinding, Status: r.Status, RewardAdmitted: r.RewardAdmitted,
		Allowed: d.Allowed, Reason: d.Reason, ReasonText: d.ReasonText, Missing: d.Missing,
		Capabilities: r.Capabilities, OfficialSources: r.OfficialSources,
		LastCheckedAt: r.LastCheckedAt, ValidUntil: r.ValidUntil, CloseReason: r.CloseReason,
	}
}

// handleChannelAdmissionList is the merchant surface: every route visible to
// the workspace tenant with its honest verdict and missing items.
func (s *Server) handleChannelAdmissionList(w http.ResponseWriter, r *http.Request) {
	if !s.admissionGate(w) {
		return
	}
	c := callerFrom(r)
	if c == nil || c.Member == nil {
		fail(w, http.StatusUnauthorized, "unauthenticated", "no session")
		return
	}
	rows, err := s.St.ListChannelAdmissions(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "channel admission lookup failed")
		return
	}
	in := channeladmission.GateInput{Now: time.Now().UTC(), TenantID: c.Member.TenantID}
	out := make([]admissionPayload, 0, len(rows))
	for _, row := range rows {
		out = append(out, admissionPayloadOf(in, row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"records": out})
}

// handlePublicChannelAdmission is the customer/handoff surface: the gate
// verdict for one platform + entry_mode + scenario on the link's own tenant.
// Unregistered routes answer as unknown rules (fail-closed), never as allowed.
func (s *Server) handlePublicChannelAdmission(w http.ResponseWriter, r *http.Request) {
	if !s.admissionGate(w) {
		return
	}
	res, ok := s.availablePublishLink(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	platform := strings.TrimSpace(q.Get("platform"))
	entryMode := strings.TrimSpace(q.Get("entry_mode"))
	scenario := strings.TrimSpace(q.Get("scenario"))
	if platform == "" || entryMode == "" || scenario == "" {
		fail(w, http.StatusBadRequest, "param_required",
			"platform, entry_mode and scenario query parameters are required")
		return
	}
	tenantID := res.Campaign.TenantID
	in := channeladmission.GateInput{Now: time.Now().UTC(), TenantID: tenantID}
	row, err := s.St.GetChannelAdmission(platform, entryMode, scenario, tenantID)
	if errors.Is(err, store.ErrNotFound) {
		// 未登记路线 = 未知规则:诚实拒绝,不默认允许,不泄露库内存在性。
		writeJSON(w, http.StatusOK, admissionPayload{
			Platform: platform, EntryMode: entryMode, Scenario: scenario,
			Allowed: false, Reason: channeladmission.ReasonAdmissionUnknown,
			ReasonText: "该渠道路线暂无准入记录，按未知规则处理，不默认允许",
			Missing:    []string{"该平台/入口/场景的官方准入核验记录"},
		})
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "channel admission lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, admissionPayloadOf(in, row))
}
