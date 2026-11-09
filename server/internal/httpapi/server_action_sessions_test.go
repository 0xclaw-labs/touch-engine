package httpapi

// HUI-3546 增量轮: 匿名动作会话的 HTTP 面。
//
// 纪律:
//   - FEATURE_PARTICIPATION off = 路由不注册 + handler gate 统一 404(双保险);
//   - 目录端口未接线 = 503 action_directory_unavailable(fail-closed,不伪报);
//   - 载荷白名单:visitor 键/幂等键/规则快照原文/预留引用一律不出响应;
//   - 恢复凭据明文只在签发响应出现一次;凭据不进 URL 查询参数;
//   - 店员码(staff_verify)进不了会话兑换面;取消即全废散落凭据。

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/participation"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// stubASDirectory: 目录夹具只进测试;生产由 HUI-4584 动作目录一行接线。
type stubASDirectory struct {
	ref participation.ActionRef
	ok  bool
}

func (d *stubASDirectory) ResolveAction(_ context.Context, id string) (participation.ActionRef, bool, error) {
	if d.ok && d.ref.ActionID != "" && d.ref.ActionID != id {
		return participation.ActionRef{}, false, nil
	}
	return d.ref, d.ok, nil
}

// newASFixture: 参与夹具(REAL SQLite/identity/门店作用域) + 目录接线。
// 返回夹具、目录桩、活动短码(供参与链路负例复用真实 campaign)。
func newASFixture(t *testing.T) (*fixture, *stubASDirectory, string) {
	t.Helper()
	f, linkCode := newPartFixture(t)
	res := f.s.St.ResolveLink(linkCode, time.Now())
	if res.Outcome != store.OutcomeAvailable {
		t.Fatalf("resolve link: %v", res.Outcome)
	}
	dir := &stubASDirectory{ok: true, ref: participation.ActionRef{
		ActionID: "act_001", CatalogCode: "A04", Platform: "wechat", Kind: "share",
		TenantID: f.tenA, StoreID: res.Campaign.StoreID,
		StorefrontRevision: 5, ActionRevision: 12, RuleVersion: 3, RuleSnapshot: `{"v":3}`,
		ContentPackVersion: "cp_v7",
		CampaignID:         res.Campaign.ID,
		Window:             participation.Window{StartsAt: res.Campaign.StartsAt, ExpiresAt: res.Campaign.EndsAt},
		Enabled:            true,
	}}
	f.s.Participation.SetActionDirectory(dir)
	return f, dir, linkCode
}

func sessionCreateBody(idem string) string {
	if idem == "" {
		return `{"touchpoint_source":"nfc"}`
	}
	return fmt.Sprintf(`{"touchpoint_source":"nfc","idempotency_key":%q}`, idem)
}

// off = 路由不注册,统一 404,面不可见(与参与面同一开关双保险)。
func TestActionSessionRoutesInvisibleWhenOff(t *testing.T) {
	f := newFixture(t, false) // 无 FEATURE_PARTICIPATION
	for _, tc := range []struct{ m, p string }{
		{"POST", "/api/v1/public/actions/act_001/sessions"},
		{"GET", "/api/v1/public/action-sessions/as_x/status"},
		{"POST", "/api/v1/public/action-sessions/as_x/resume-credentials"},
		{"POST", "/api/v1/public/resume/exchange"},
		{"POST", "/api/v1/public/action-sessions/as_x/cancel"},
	} {
		if status, _, _ := f.do(t, tc.m, tc.p, "", f.tenA, `{}`); status != http.StatusNotFound {
			t.Fatalf("off %s %s = %d, want 404", tc.m, tc.p, status)
		}
	}
}

// 目录未接线:fail-closed 503,不建行、不伪报。
func TestActionSessionDirectoryNotWiredFailsClosed(t *testing.T) {
	f, _, _ := newASFixture(t)
	f.s.Participation.SetActionDirectory(nil) // 模拟未接线
	status, _, body := f.doCookie(t, "POST", "/api/v1/public/actions/act_001/sessions", "", sessionCreateBody(""))
	if status != http.StatusServiceUnavailable {
		t.Fatalf("unwired create = %d, want 503", status)
	}
	if body["error"] != "action_directory_unavailable" {
		t.Fatalf("error = %v, want action_directory_unavailable", body["error"])
	}
}

func TestActionSessionCreateRestoreStatusPositive(t *testing.T) {
	f, _, _ := newASFixture(t)

	// 显式动作创建:201 + cookie + 冻结快照 + 白名单载荷。
	status, hdr, body := f.doCookie(t, "POST", "/api/v1/public/actions/act_001/sessions", "", sessionCreateBody("idem-1"))
	if status != http.StatusCreated {
		t.Fatalf("create = %d (%v)", status, body)
	}
	cookie := cookiePair(hdr)
	if cookie == "" {
		t.Fatal("create must issue the visitor cookie")
	}
	sid, _ := body["action_session_id"].(string)
	if sid == "" {
		t.Fatal("action_session_id required")
	}
	if body["status"] != "active" || body["storefront_revision"] != float64(5) || body["action_revision"] != float64(12) {
		t.Fatalf("frozen snapshot wrong: %v", body)
	}
	if body["resume_available"] != true {
		t.Fatalf("resume_available = %v, want true", body["resume_available"])
	}
	for _, leak := range []string{"visitor_id", "idempotency_key", "rule_snapshot", "tenant_id", "store_id", "participation_id"} {
		if _, ok := body[leak]; ok {
			t.Fatalf("whitelist payload must not carry %q", leak)
		}
	}

	// 同浏览器重复碰触:200 恢复同一会话。
	status, _, body2 := f.doCookie(t, "POST", "/api/v1/public/actions/act_001/sessions", cookie, sessionCreateBody("idem-1"))
	if status != http.StatusOK || body2["action_session_id"] != sid {
		t.Fatalf("restore = %d %v, want 200 same %v", status, body2["action_session_id"], sid)
	}

	// 状态只读:实时位。
	status, _, body3 := f.doCookie(t, "GET", "/api/v1/public/action-sessions/"+sid+"/status", cookie, "")
	if status != http.StatusOK || body3["action_available"] != true || body3["resume_available"] != true {
		t.Fatalf("status = %d %v", status, body3)
	}
}

func TestActionSessionNegatives(t *testing.T) {
	f, dir, _ := newASFixture(t)

	// 未知动作:统一 404。
	status, _, _ := f.doCookie(t, "POST", "/api/v1/public/actions/act_ghost/sessions", "", sessionCreateBody(""))
	if status != http.StatusNotFound {
		t.Fatalf("unknown action = %d, want 404", status)
	}
	// 停用动作:409 action_unavailable。
	dir.ref.Enabled = false
	status, _, body := f.doCookie(t, "POST", "/api/v1/public/actions/act_001/sessions", "", sessionCreateBody(""))
	if status != http.StatusConflict || body["error"] != "action_unavailable" {
		t.Fatalf("disabled = %d %v, want 409 action_unavailable", status, body["error"])
	}
	dir.ref.Enabled = true

	// 期望版本只是期望:stale 拒绝。
	status, _, body = f.doCookie(t, "POST", "/api/v1/public/actions/act_001/sessions", "", `{"touchpoint_source":"nfc","expected_action_revision":11}`)
	if status != http.StatusConflict || body["error"] != "stale_version" {
		t.Fatalf("stale = %d %v, want 409 stale_version", status, body["error"])
	}

	// 创建一个会话,再验证顺序 id 探测面:无 cookie/陌生 cookie 一律 404。
	_, _, created := f.doCookie(t, "POST", "/api/v1/public/actions/act_001/sessions", "", sessionCreateBody(""))
	sid := created["action_session_id"].(string)
	if status, _, _ := f.doCookie(t, "GET", "/api/v1/public/action-sessions/"+sid+"/status", "", ""); status != http.StatusNotFound {
		t.Fatalf("no-cookie status = %d, want 404", status)
	}
	if status, _, _ := f.doCookie(t, "GET", "/api/v1/public/action-sessions/"+sid+"/status", "touch_participation_visitor=ffffffffffffffffffffffffffffffff", ""); status != http.StatusNotFound {
		t.Fatalf("foreign-cookie status = %d, want 404", status)
	}
}

func TestResumeCredentialIssueExchangeReplay(t *testing.T) {
	f, _, _ := newASFixture(t)

	// 创建会话(浏览器 A)。
	_, hdr, body := f.doCookie(t, "POST", "/api/v1/public/actions/act_001/sessions", "", sessionCreateBody(""))
	cookieA := cookiePair(hdr)
	sid := body["action_session_id"].(string)

	// A 签发恢复凭据:明文一次。
	status, _, cred := f.doCookie(t, "POST", "/api/v1/public/action-sessions/"+sid+"/resume-credentials", cookieA, "")
	if status != http.StatusCreated {
		t.Fatalf("issue = %d (%v)", status, cred)
	}
	token, _ := cred["token"].(string)
	if token == "" || cred["credential_id"] == "" || cred["expires_at"] == "" {
		t.Fatalf("credential payload incomplete: %v", cred)
	}
	if cred["purpose"] != "customer_resume" {
		t.Fatalf("purpose = %v, want customer_resume", cred["purpose"])
	}

	// 陌生 visitor 不能替 A 签发。
	if status, _, _ := f.doCookie(t, "POST", "/api/v1/public/action-sessions/"+sid+"/resume-credentials", "touch_participation_visitor=ffffffffffffffffffffffffffffffff", ""); status != http.StatusNotFound {
		t.Fatalf("foreign issue = %d, want 404", status)
	}

	// 浏览器 B 兑换:无 cookie 进来,换绑同一会话;响应带新 cookie。
	status, hdrB, ex := f.do(t, "POST", "/api/v1/public/resume/exchange", "", f.tenA, fmt.Sprintf(`{"token":%q}`, token))
	if status != http.StatusOK {
		t.Fatalf("exchange = %d (%v)", status, ex)
	}
	if ex["action_session_id"] != sid {
		t.Fatalf("exchange opened %v, want same %v", ex["action_session_id"], sid)
	}
	cookieB := cookiePair(hdrB)
	if cookieB == "" {
		t.Fatal("exchange must bind the new browser with a visitor cookie")
	}

	// B 读取同一会话:状态可见。
	status, _, _ = f.doCookie(t, "GET", "/api/v1/public/action-sessions/"+sid+"/status", cookieB, "")
	if status != http.StatusOK {
		t.Fatalf("status after exchange = %d", status)
	}

	// 重放:409 resume_used。
	status, _, replay := f.do(t, "POST", "/api/v1/public/resume/exchange", "", f.tenA, fmt.Sprintf(`{"token":%q}`, token))
	if status != http.StatusConflict || replay["error"] != "resume_used" {
		t.Fatalf("replay = %d %v, want 409 resume_used", status, replay["error"])
	}

	// A 的旧 cookie 已被换绑:不再能读(旧绑定失效;恢复只认凭据与当前绑定)。
	status, _, _ = f.doCookie(t, "GET", "/api/v1/public/action-sessions/"+sid+"/status", cookieA, "")
	if status != http.StatusNotFound {
		t.Fatalf("old visitor status after rebind = %d, want 404", status)
	}
}

// 店员码(staff_verify)与参与交接凭据进不了会话兑换面。
func TestResumeExchangeRefusesStaffToken(t *testing.T) {
	f, _, code := newASFixture(t)

	// 先走真实参与链路造一条 staff_verify 凭据。
	status, hdr, body := f.doCookie(t, "POST", "/api/v1/participations", "", startBody(code, "idem-staff", "cp_v1"))
	if status != http.StatusCreated {
		t.Fatalf("start participation = %d (%v)", status, body)
	}
	cookie := cookiePair(hdr)
	pid := body["id"].(string)
	status, _, att := f.doCookie(t, "POST", "/api/v1/participations/"+pid+"/attempts", cookie, `{"platform":"wechat","handoff_method":"h5"}`)
	if status != http.StatusCreated {
		t.Fatalf("attempt = %d (%v)", status, att)
	}
	status, _, handoff := f.doCookie(t, "POST", "/api/v1/participations/"+pid+"/handoff", cookie, `{"attempt_id":"`+att["id"].(string)+`","purpose":"staff_verify"}`)
	if status != http.StatusCreated {
		t.Fatalf("staff handoff = %d (%v)", status, handoff)
	}
	staffToken := handoff["token"].(string)
	// 店员码喂给会话兑换:404(查无此凭据——表结构性隔离)。
	status, _, _ = f.do(t, "POST", "/api/v1/public/resume/exchange", "", f.tenA, fmt.Sprintf(`{"token":%q}`, staffToken))
	if status != http.StatusNotFound {
		t.Fatalf("staff token exchange = %d, want 404", status)
	}
}

func TestActionSessionAttachAndCancel(t *testing.T) {
	f, _, _ := newASFixture(t)

	// 无活动动作:升级拒绝 campaign_required(绝不伪造活动)。
	f2, dir2, _ := newASFixture(t)
	dir2.ref.CampaignID = ""
	_, hdr0, b0 := f2.doCookie(t, "POST", "/api/v1/public/actions/act_001/sessions", "", sessionCreateBody(""))
	sid0 := b0["action_session_id"].(string)
	cookie0 := cookiePair(hdr0)
	status, _, body := f2.doCookie(t, "POST", "/api/v1/public/action-sessions/"+sid0+"/participations", cookie0, `{"touchpoint_source":"nfc"}`)
	if status != http.StatusUnprocessableEntity || body["error"] != "campaign_required" {
		t.Fatalf("no-campaign attach = %d %v, want 422 campaign_required", status, body["error"])
	}

	// 有活动动作:显式升级。
	_, hdr, b := f.doCookie(t, "POST", "/api/v1/public/actions/act_001/sessions", "", sessionCreateBody(""))
	sid := b["action_session_id"].(string)
	cookie := cookiePair(hdr)
	status, _, att := f.doCookie(t, "POST", "/api/v1/public/action-sessions/"+sid+"/participations", cookie, `{"touchpoint_source":"nfc"}`)
	if status != http.StatusCreated {
		t.Fatalf("attach = %d (%v)", status, att)
	}
	pid := att["participation_id"].(string)
	pv := att["participation"].(map[string]any)
	if pv["status"] != "pending" || pv["benefit_reservation_state"] != "not_requested" {
		t.Fatalf("attach participation = %v (nil reserver must stay not_requested)", pv)
	}
	// 幂等重复升级:同一参与(200)。
	status, _, att2 := f.doCookie(t, "POST", "/api/v1/public/action-sessions/"+sid+"/participations", cookie, `{"touchpoint_source":"nfc"}`)
	if status != http.StatusOK || att2["participation_id"] != pid {
		t.Fatalf("re-attach = %d %v, want 200 same %v", status, att2["participation_id"], pid)
	}
	// 状态视图携带权益引用。
	status, _, st := f.doCookie(t, "GET", "/api/v1/public/action-sessions/"+sid+"/status", cookie, "")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	pr, _ := st["participation"].(map[string]any)
	if pr == nil || pr["id"] != pid || pr["benefit_reservation_state"] != "not_requested" {
		t.Fatalf("status participation = %v", st["participation"])
	}

	// 取消:会话终态,散落凭据全废。
	status, _, issued := f.doCookie(t, "POST", "/api/v1/public/action-sessions/"+sid+"/resume-credentials", cookie, "")
	if status != http.StatusCreated {
		t.Fatalf("issue before cancel = %d", status)
	}
	status, _, _ = f.doCookie(t, "POST", "/api/v1/public/action-sessions/"+sid+"/cancel", cookie, "")
	if status != http.StatusOK {
		t.Fatalf("cancel = %d", status)
	}
	status, _, _ = f.do(t, "POST", "/api/v1/public/resume/exchange", "", f.tenA, fmt.Sprintf(`{"token":%q}`, issued["token"]))
	if status != http.StatusConflict {
		t.Fatalf("exchange after cancel = %d, want 409", status)
	}
}
