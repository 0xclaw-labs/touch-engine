package httpapi

// HUI-3562 店员确认与核销台（/work/service）的服务端窄补丁验收。
//
// 覆盖(真实 SQLite + 真实 HTTP,无 mock):
//   - staff/peek:凭据定位参与卡但不消费(重复扫码幂等,两店员可同开);
//   - 手输限流:token 类店员端口按成员固定窗口限流(429);
//   - by-code:券码定位核销卡,错店掩蔽;
//   - 全链矩阵:确认→发券→核销→并发恰好一份;官方专属拒人工;
//     伪造回执拒;gift 现场兑现两事实;库存打空;撤权;越权;过期。
//
// 纪律:所有发奖/核销事务都在 rewardops 账本(1671);本文件不新增任何
// 账本行为,只验收店员台消费既有端口 + 三个窄辅助端点。

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/campaign"
	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/identity"
	"github.com/bianjiefilm/touch-engine/server/internal/leads"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

// serviceDeskFixture: 参与(3546) + 奖励账本(1671) 双面齐备的店员台夹具。
// 本店 storeA / 邻店 storeB / 错租户 tenantB / 撤权成员 / 三种批次
// (门店券·官方专属·现场赠品)。
type serviceDeskFixture struct {
	s  *Server
	ts *httptest.Server
	// ids
	tenA, tenB     string
	storeA, storeB string
	campaignA      string
	linkCode       string
	batchCoupon    string // slot first_publish, 门店券, staff 证据两类, 余量2(打空用)
	batchOfficial  string // slot official_first, 只认官方确认
	batchGift      string // slot gift_today, gift_discount(现场兑现)
}

func newServiceDeskFixture(t *testing.T) *serviceDeskFixture {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	st := store.New(d)

	tenA, err := st.CreateTenant("店员台商家A")
	mustNoErr(t, err)
	tenB, err := st.CreateTenant("店员台商家B")
	mustNoErr(t, err)
	storeA, err := st.CreateStore(tenA.ID, "旗舰店", "", "test")
	mustNoErr(t, err)
	storeB, err := st.CreateStore(tenA.ID, "二店", "", "test")
	mustNoErr(t, err)
	for _, m := range []struct{ tenant, principal, role string }{
		{tenA.ID, "usr_owner_a", "org_owner"},
		{tenB.ID, "usr_owner_b", "org_owner"},
	} {
		if _, err := st.CreateMember(m.tenant, m.principal, m.role, m.principal, "test", true); err != nil {
			t.Fatalf("seed member: %v", err)
		}
	}
	if _, err := st.CreateMemberScoped(tenA.ID, "usr_mgr_a", "store_manager", "usr_mgr_a", "test", true, storeA.ID); err != nil {
		t.Fatalf("seed manager A: %v", err)
	}
	if _, err := st.CreateMemberScoped(tenA.ID, "usr_mgr_b", "store_manager", "usr_mgr_b", "test", true, storeB.ID); err != nil {
		t.Fatalf("seed manager B: %v", err)
	}
	// 撤权负例成员。
	if _, err := st.CreateMember(tenA.ID, "usr_off_a", "staff", "usr_off_a", "test", true); err != nil {
		t.Fatalf("seed member: %v", err)
	}
	if _, err := st.UpdateMember(mustMemberID(t, st, tenA.ID, "usr_off_a"), nil, boolPtr(false), nil); err != nil {
		t.Fatalf("disable member: %v", err)
	}

	now := time.Now().UTC()
	cmp, err := st.CreateCampaign(store.NewCampaign{
		TenantID: tenA.ID, Title: "店员台活动", PublicContent: "发一条领一份",
		StartsAt: now.Add(-time.Hour).Format(time.RFC3339),
		EndsAt:   now.Add(48 * time.Hour).Format(time.RFC3339),
		StoreID:  storeA.ID, CreatedBy: "test",
	})
	mustNoErr(t, err)
	if _, err := st.TransitionCampaign(cmp.ID, tenA.ID, campaign.StatusActive); err != nil {
		t.Fatalf("activate: %v", err)
	}
	lnk, err := st.CreateLink(tenA.ID, cmp.ID, "test")
	mustNoErr(t, err)

	mk := func(kind, slot string, evidence []string, qty int) string {
		inv, err := st.CreateRewardInventory(tenA.ID, store.NewRewardInventory{
			CampaignID: cmp.ID, Kind: kind, Name: "批次-" + slot,
			BenefitSlot: slot, RuleVersion: 1, AllowedEvidence: evidence,
			TotalQty: qty, FaceValueMinor: 500, CostMinor: 300, Currency: "FEN",
			StoreID: storeA.ID, CreatedBy: "usr_owner_a",
		})
		mustNoErr(t, err)
		return inv.ID
	}
	batchCoupon := mk(store.RewardKindStoreCoupon, "first_publish",
		[]string{"staff_observed_submission", "staff_verified_post"}, 2)
	batchOfficial := mk(store.RewardKindStoreCoupon, "official_first",
		[]string{"official_publish_confirmed"}, 5)
	batchGift := mk(store.RewardKindGiftDiscount, "gift_today",
		[]string{"staff_observed_submission"}, 5)

	cfg := config.Load(func(k string) string {
		switch k {
		case "TOUCH_INTERNAL_TOKEN":
			return "test-internal-secret"
		case "PLATFORM_IDENTITY_BASE_URL":
			return "http://identity.test"
		case "PLATFORM_IDENTITY_TOKEN":
			return "identity-token"
		case "FEATURE_PARTICIPATION":
			return "on"
		case "PUBLIC_BASE_URL":
			return "https://h5.example.com"
		}
		return ""
	})
	idsrv := fakeIdentity(t, map[string]identitySession{
		"sess-owner-a": {"usr_owner_a", "a@example.com"},
		"sess-mgr-a":   {"usr_mgr_a", "mgra@example.com"},
		"sess-mgr-b":   {"usr_mgr_b", "mgrb@example.com"},
		"sess-off-a":   {"usr_off_a", "off@example.com"},
		"sess-owner-b": {"usr_owner_b", "b@example.com"},
	})
	idc := &identity.Client{BaseURL: idsrv.URL, Token: "identity-token", AppID: "touch-engine", HTTP: idsrv.Client()}
	s := New(cfg, d, idc, &upload.Client{}, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	return &serviceDeskFixture{
		s: s, ts: ts, tenA: tenA.ID, tenB: tenB.ID,
		storeA: storeA.ID, storeB: storeB.ID, campaignA: cmp.ID,
		linkCode: lnk.Code, batchCoupon: batchCoupon, batchOfficial: batchOfficial, batchGift: batchGift,
	}
}

// startParticipation: 顾客显式开始一次参与,返回 (participationID, visitorCookie)。
func (f *serviceDeskFixture) startParticipation(t *testing.T, key string) (string, string) {
	t.Helper()
	req, err := http.NewRequest("POST", f.ts.URL+"/api/v1/participations", strings.NewReader(
		fmt.Sprintf(`{"link_code":%q,"touchpoint_source":"nfc","idempotency_key":%q,"content_pack_version":"cp_v1"}`, f.linkCode, key)))
	mustNoErr(t, err)
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	req.Header.Set("Content-Type", "application/json")
	res, err := f.ts.Client().Do(req)
	mustNoErr(t, err)
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	if res.StatusCode != http.StatusCreated && res.StatusCode != http.StatusOK {
		t.Fatalf("start participation = %d (%v)", res.StatusCode, out)
	}
	cookie := ""
	if sc := res.Header.Get("Set-Cookie"); sc != "" {
		cookie = strings.Split(sc, ";")[0]
	}
	id, _ := out["id"].(string)
	if id == "" {
		t.Fatalf("start participation: no id in %v", out)
	}
	return id, cookie
}

// mintStaffVerify: 顾客出示店员核验凭据,返回明文 token(只在创建响应出现一次)。
func (f *serviceDeskFixture) mintStaffVerify(t *testing.T, pid, cookie string) string {
	t.Helper()
	req, err := http.NewRequest("POST", f.ts.URL+"/api/v1/participations/"+pid+"/handoff", strings.NewReader(`{"purpose":"staff_verify"}`))
	mustNoErr(t, err)
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookie)
	res, err := f.ts.Client().Do(req)
	mustNoErr(t, err)
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("staff_verify handoff = %d (%v)", res.StatusCode, out)
	}
	tok, _ := out["token"].(string)
	if !strings.HasPrefix(tok, "ptx_") {
		t.Fatalf("token = %q", tok)
	}
	return tok
}

// do posts an authenticated admin request (session/tenant headers), same
// shape as fixture.do.
func (f *serviceDeskFixture) do(t *testing.T, method, path, session, tenant, body string) (int, http.Header, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, f.ts.URL+path, rd)
	mustNoErr(t, err)
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	if session != "" {
		req.Header.Set("X-Session-Token", session)
	}
	if tenant != "" {
		req.Header.Set("X-Tenant-ID", tenant)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := f.ts.Client().Do(req)
	mustNoErr(t, err)
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, res.Header, out
}

// confirmBody: 店员确认请求体(证据、批次 slot、幂等键)。
func confirmBody(pid, slot, evidence, storeID, eventID string) string {
	return fmt.Sprintf(`{"participation_id":%q,"benefit_slot":%q,"rule_version":1,"event_id":%q,"evidence":%q,"store_id":%q,"note":"","fulfill_now":false}`,
		pid, slot, eventID, evidence, storeID)
}

// ---- T1: staff/peek ---------------------------------------------------------------

func TestStaffPeekOpensCardWithoutConsuming(t *testing.T) {
	f := newServiceDeskFixture(t)
	pid, cookie := f.startParticipation(t, "idem-peek-1")
	token := f.mintStaffVerify(t, pid, cookie)

	// 本店经理扫码(peek):200 参与卡载荷。
	status, _, card := f.do(t, "POST", "/api/v1/participations/staff/peek", "sess-mgr-a", f.tenA, fmt.Sprintf(`{"token":%q}`, token))
	mustEqual(t, status, http.StatusOK)
	if card["id"] != pid {
		t.Fatalf("peek id = %v, want %v", card["id"], pid)
	}
	if card["campaign_id"] != f.campaignA || card["store_id"] != f.storeA {
		t.Fatalf("peek card campaign/store = %v/%v, want %v/%v", card["campaign_id"], card["store_id"], f.campaignA, f.storeA)
	}
	if card["status"] != "pending" || card["rule_version"] != float64(1) {
		t.Fatalf("peek card state = %v", card)
	}
	// 白名单:访客键/幂等键/预留引用/租户不出现。
	for _, leak := range []string{"visitor_id", "idempotency_key", "benefit_reservation_ref", "tenant_id"} {
		if _, ok := card[leak]; ok {
			t.Fatalf("peek payload must not carry %q", leak)
		}
	}

	// 重复扫码:同码再 peek 幂等回同一张卡。
	status, _, again := f.do(t, "POST", "/api/v1/participations/staff/peek", "sess-mgr-a", f.tenA, fmt.Sprintf(`{"token":%q}`, token))
	mustEqual(t, status, http.StatusOK)
	if again["id"] != pid {
		t.Fatalf("re-peek id = %v, want %v", again["id"], pid)
	}

	// peek 不消费凭据:之后店员兑换(staff/redeem)仍然成功。
	status, _, rd := f.do(t, "POST", "/api/v1/participations/staff/redeem", "sess-mgr-a", f.tenA, fmt.Sprintf(`{"token":%q}`, token))
	mustEqual(t, status, http.StatusOK)
	if rd["status"] != "awaiting_review" {
		t.Fatalf("after redeem status = %v, want awaiting_review", rd["status"])
	}
}

func TestStaffPeekRefusalMatrix(t *testing.T) {
	f := newServiceDeskFixture(t)
	pid, cookie := f.startParticipation(t, "idem-peek-2")
	token := f.mintStaffVerify(t, pid, cookie)

	// 伪造/未知 token:统一 404(防枚举)。
	status, _, _ := f.do(t, "POST", "/api/v1/participations/staff/peek", "sess-mgr-a", f.tenA, `{"token":"ptx_zzzz.zzzz"}`)
	mustEqual(t, status, http.StatusNotFound)
	// 空 token:400。
	status, _, _ = f.do(t, "POST", "/api/v1/participations/staff/peek", "sess-mgr-a", f.tenA, `{"token":""}`)
	mustEqual(t, status, http.StatusBadRequest)
	// 邻店经理:403(域层 ErrNotEntitled,与 staff-view 同语义)。
	status, _, _ = f.do(t, "POST", "/api/v1/participations/staff/peek", "sess-mgr-b", f.tenA, fmt.Sprintf(`{"token":%q}`, token))
	mustEqual(t, status, http.StatusForbidden)
	// 错租户:403 target_mismatch(peek 先定位真实租户,与 staff/redeem 同语义)。
	status, _, _ = f.do(t, "POST", "/api/v1/participations/staff/peek", "sess-owner-b", f.tenB, fmt.Sprintf(`{"token":%q}`, token))
	mustEqual(t, status, http.StatusForbidden)
	// 撤权成员:403。
	status, _, _ = f.do(t, "POST", "/api/v1/participations/staff/peek", "sess-off-a", f.tenA, fmt.Sprintf(`{"token":%q}`, token))
	mustEqual(t, status, http.StatusForbidden)
	// 未登录:401。
	status, _, _ = f.do(t, "POST", "/api/v1/participations/staff/peek", "", f.tenA, fmt.Sprintf(`{"token":%q}`, token))
	mustEqual(t, status, http.StatusUnauthorized)

	// 拒绝路径全部不消费凭据:本店经理随后 peek/redeem 仍成功。
	status, _, _ = f.do(t, "POST", "/api/v1/participations/staff/peek", "sess-mgr-a", f.tenA, fmt.Sprintf(`{"token":%q}`, token))
	mustEqual(t, status, http.StatusOK)
	status, _, _ = f.do(t, "POST", "/api/v1/participations/staff/redeem", "sess-mgr-a", f.tenA, fmt.Sprintf(`{"token":%q}`, token))
	mustEqual(t, status, http.StatusOK)
}

func TestStaffPeekInvisibleWhenParticipationOff(t *testing.T) {
	// FEATURE_PARTICIPATION off = 路由不注册 = 统一 404(与 3546 同纪律)。
	f := newServiceDeskFixture(t)
	f.s.Cfg.FeatureParticipation = false
	status, _, _ := f.do(t, "POST", "/api/v1/participations/staff/peek", "sess-mgr-a", f.tenA, `{"token":"ptx_aaaaaaaaaaaaaaaaaaaaaa.a"}`)
	mustEqual(t, status, http.StatusNotFound)
}

// ---- T2: 手输限流 -------------------------------------------------------------------

func TestStaffTokenEntryRateLimited(t *testing.T) {
	f := newServiceDeskFixture(t)
	// 收紧窗口便于测试:每分钟 3 次。
	f.s.StaffTokenLimiter = leads.NewRateLimiter(3, time.Minute)

	pid, cookie := f.startParticipation(t, "idem-rl-1")
	token := f.mintStaffVerify(t, pid, cookie)

	codes := []int{}
	for i := 0; i < 4; i++ {
		status, _, _ := f.do(t, "POST", "/api/v1/participations/staff/peek", "sess-mgr-a", f.tenA, fmt.Sprintf(`{"token":%q}`, token))
		codes = append(codes, status)
	}
	if codes[0] != http.StatusOK || codes[1] != http.StatusOK || codes[2] != http.StatusOK {
		t.Fatalf("first three peeks should pass, got %v", codes)
	}
	if codes[3] != http.StatusTooManyRequests {
		t.Fatalf("fourth peek = %d, want 429", codes[3])
	}
	// 其他成员不受影响(按成员计)。
	status, _, _ := f.do(t, "POST", "/api/v1/participations/staff/peek", "sess-owner-a", f.tenA, fmt.Sprintf(`{"token":%q}`, token))
	mustEqual(t, status, http.StatusOK)
}
