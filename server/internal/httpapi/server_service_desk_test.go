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

// ---- T3: 券码定位 by-code -----------------------------------------------------------

// seedIssuedBenefit: 用真实 confirm 落一条 issued 权益,返回 (benefitID, code)。
func (f *serviceDeskFixture) seedIssuedBenefit(t *testing.T, pid, slot, evidence, eventID string) (string, string) {
	t.Helper()
	status, _, out := f.do(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-confirm", "sess-owner-a", f.tenA,
		confirmBody(pid, slot, evidence, f.storeA, eventID))
	mustEqual(t, status, http.StatusOK)
	ben, _ := out["benefit"].(map[string]any)
	if ben == nil {
		t.Fatalf("confirm produced no benefit: %v", out)
	}
	id, _ := ben["id"].(string)
	code, _ := ben["code"].(string)
	return id, code
}

func TestRewardBenefitsByCodeScan(t *testing.T) {
	f := newServiceDeskFixture(t)
	pid, _ := f.startParticipation(t, "idem-code-1")
	benID, code := f.seedIssuedBenefit(t, pid, "first_publish", "staff_observed_submission", "evt-code-1")
	if code == "" {
		t.Fatal("store_coupon benefit must carry a code")
	}

	// 本店经理扫券码:200 核销卡载荷。
	status, _, card := f.do(t, "GET", "/api/v1/reward-benefits/by-code?code="+code, "sess-mgr-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	if card["id"] != benID || card["status"] != "issued" {
		t.Fatalf("by-code card = %v, want id %s issued", card, benID)
	}

	// 邻店经理:404 掩蔽(跨店记录不可见)。
	status, _, _ = f.do(t, "GET", "/api/v1/reward-benefits/by-code?code="+code, "sess-mgr-b", f.tenA, "")
	mustEqual(t, status, http.StatusNotFound)

	// 未知识码:404;空码:400。
	status, _, _ = f.do(t, "GET", "/api/v1/reward-benefits/by-code?code=NOPE", "sess-mgr-a", f.tenA, "")
	mustEqual(t, status, http.StatusNotFound)
	status, _, _ = f.do(t, "GET", "/api/v1/reward-benefits/by-code?code=", "sess-mgr-a", f.tenA, "")
	mustEqual(t, status, http.StatusBadRequest)

	// 错租户:404(租户内查无此码)。
	status, _, _ = f.do(t, "GET", "/api/v1/reward-benefits/by-code?code="+code, "sess-owner-b", f.tenB, "")
	mustEqual(t, status, http.StatusNotFound)
}

// ---- T4: 全链验收矩阵 ---------------------------------------------------------------

// benefitFor: 从权益列表里找该参与的权益(无则 nil)。
func benefitFor(t *testing.T, f *serviceDeskFixture, session, tenant, campaignID, participationID string) map[string]any {
	t.Helper()
	status, _, out := f.do(t, "GET", "/api/v1/campaigns/"+campaignID+"/reward-benefits", session, tenant, "")
	mustEqual(t, status, http.StatusOK)
	items, _ := out["items"].([]any)
	for _, it := range items {
		ben, _ := it.(map[string]any)
		if ben["participation_id"] == participationID {
			return ben
		}
	}
	return nil
}

func TestServiceDeskFullChainConfirmIssueRedeem(t *testing.T) {
	f := newServiceDeskFixture(t)
	pid, cookie := f.startParticipation(t, "idem-chain-1")
	token := f.mintStaffVerify(t, pid, cookie)

	// 扫码开卡(peek,不消费)。
	status, _, peeked := f.do(t, "POST", "/api/v1/participations/staff/peek", "sess-mgr-a", f.tenA, fmt.Sprintf(`{"token":%q}`, token))
	mustEqual(t, status, http.StatusOK)
	if peeked["id"] != pid {
		t.Fatalf("peek id = %v, want %v", peeked["id"], pid)
	}

	// 一次明确确认:按批次允许证据选「店员见到提交」→ 发放。
	status, _, out := f.do(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-confirm", "sess-mgr-a", f.tenA,
		confirmBody(pid, "first_publish", "staff_observed_submission", f.storeA, "evt-chain-1"))
	mustEqual(t, status, http.StatusOK)
	if out["outcome"] != "issued" || out["replay"] != false {
		t.Fatalf("confirm outcome = %v", out)
	}
	ben := benefitFor(t, f, "sess-mgr-a", f.tenA, f.campaignA, pid)
	if ben == nil || ben["status"] != "issued" {
		t.Fatalf("benefit after confirm = %v", ben)
	}
	code, _ := ben["code"].(string)
	if code == "" {
		t.Fatal("store_coupon must carry a code")
	}

	// 券码扫码 → 核销卡 → 独立核销入口。
	status, _, byCode := f.do(t, "GET", "/api/v1/reward-benefits/by-code?code="+code, "sess-mgr-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	benID, _ := byCode["id"].(string)
	status, _, rd := f.do(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-benefits/"+benID+"/redeem", "sess-mgr-a", f.tenA,
		fmt.Sprintf(`{"event_id":"evt-chain-redeem","store_id":%q}`, f.storeA))
	mustEqual(t, status, http.StatusOK)
	if rd["outcome"] != "redeemed" {
		t.Fatalf("redeem outcome = %v", rd)
	}
	ben = benefitFor(t, f, "sess-mgr-a", f.tenA, f.campaignA, pid)
	if ben["status"] != "redeemed" {
		t.Fatalf("benefit after redeem = %v", ben)
	}
	// 处理人/时间可读(另一端「已处理」显示的数据源)。
	if ben["redeemed_by"] != "usr_mgr_a" || ben["redeemed_at"] == "" {
		t.Fatalf("redeem actor/time = %v/%v", ben["redeemed_by"], ben["redeemed_at"])
	}
}

func TestServiceDeskTwoStaffRedeemExactlyOne(t *testing.T) {
	f := newServiceDeskFixture(t)
	pid, _ := f.startParticipation(t, "idem-race-1")
	benID, _ := f.seedIssuedBenefit(t, pid, "first_publish", "staff_observed_submission", "evt-race-issue")

	// 两位店员先后核销(1671 单事务原子):第一位 applied,第二位读回已处理。
	status, _, first := f.do(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-benefits/"+benID+"/redeem", "sess-mgr-a", f.tenA,
		fmt.Sprintf(`{"event_id":"evt-race-a","store_id":%q}`, f.storeA))
	mustEqual(t, status, http.StatusOK)
	if first["outcome"] != "redeemed" || first["applied"] != true {
		t.Fatalf("first redeem = %v", first)
	}
	_ = first
	status, _, second := f.do(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-benefits/"+benID+"/redeem", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"event_id":"evt-race-b","store_id":%q}`, f.storeA))
	mustEqual(t, status, http.StatusOK)
	if second["outcome"] != "already_redeemed" {
		t.Fatalf("second redeem = %v, want already_redeemed", second)
	}
	// 账本恰好一份:权益仍只有一条,核销人是第一位。
	ben := benefitFor(t, f, "sess-owner-a", f.tenA, f.campaignA, pid)
	if ben["redeemed_by"] != "usr_mgr_a" {
		t.Fatalf("redeemed_by = %v, want the first staff only", ben["redeemed_by"])
	}
}

func TestServiceDeskConfirmReplaySameEvent(t *testing.T) {
	f := newServiceDeskFixture(t)
	pid, _ := f.startParticipation(t, "idem-replay-1")

	// 断网重试:同 event_id 重放由账本去重——不重复发放、不提示重领。
	status, _, first := f.do(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-confirm", "sess-mgr-a", f.tenA,
		confirmBody(pid, "first_publish", "staff_observed_submission", f.storeA, "evt-replay-1"))
	mustEqual(t, status, http.StatusOK)
	if first["replay"] != false {
		t.Fatalf("first confirm = %v, want applied (not a replay)", first)
	}
	status, _, again := f.do(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-confirm", "sess-mgr-a", f.tenA,
		confirmBody(pid, "first_publish", "staff_observed_submission", f.storeA, "evt-replay-1"))
	mustEqual(t, status, http.StatusOK)
	if again["replay"] != true || again["outcome"] != "issued" {
		t.Fatalf("replay confirm = %v", again)
	}
	// 恰好一条权益。
	status, _, out := f.do(t, "GET", "/api/v1/campaigns/"+f.campaignA+"/reward-benefits", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	items, _ := out["items"].([]any)
	n := 0
	for _, it := range items {
		if b, _ := it.(map[string]any); b["participation_id"] == pid {
			n++
		}
	}
	mustEqual(t, n, 1)
}

func TestServiceDeskOfficialOnlyRuleRefusesStaffAndFabricated(t *testing.T) {
	f := newServiceDeskFixture(t)
	pid, _ := f.startParticipation(t, "idem-official-1")

	// 规则只认官方确认:人工证据被拒(official_evidence_required)。
	status, _, out := f.do(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-confirm", "sess-mgr-a", f.tenA,
		confirmBody(pid, "official_first", "staff_observed_submission", f.storeA, "evt-off-1"))
	mustEqual(t, status, http.StatusUnprocessableEntity)
	if out["error"] != "official_evidence_required" {
		t.Fatalf("staff evidence on official-only = %v", out)
	}

	// 人工不能冒充官方回执:编造 fact/post 被 1671 拒(fabricated_official_receipt)。
	status, _, out = f.do(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-confirm", "sess-mgr-a", f.tenA,
		fmt.Sprintf(`{"participation_id":%q,"benefit_slot":"official_first","rule_version":1,"event_id":"evt-off-2","evidence":"official_publish_confirmed","official":{"fact_id":"fake-fact","platform_post_id":"fake-post","receipt_source":"official_query"},"store_id":%q,"note":"","fulfill_now":false}`, pid, f.storeA))
	mustEqual(t, status, http.StatusUnprocessableEntity)
	if out["error"] != "fabricated_official_receipt" {
		t.Fatalf("fabricated receipt = %v", out)
	}

	// 被拒的确认不留账本行(「暂不符合/证据不符」零写入)。
	if ben := benefitFor(t, f, "sess-owner-a", f.tenA, f.campaignA, pid); ben != nil {
		t.Fatalf("refused confirm must leave no benefit row, got %v", ben)
	}
}

func TestServiceDeskGiftFulfillNowTwoFacts(t *testing.T) {
	f := newServiceDeskFixture(t)
	pid, _ := f.startParticipation(t, "idem-gift-1")

	// 现场赠品/折扣:「确认并现场兑现」= 同事务发放+核销两条事实。
	body := fmt.Sprintf(`{"participation_id":%q,"benefit_slot":"gift_today","rule_version":1,"event_id":"evt-gift-1","evidence":"staff_observed_submission","store_id":%q,"note":"现场给过","fulfill_now":true,"redeem_event_id":"evt-gift-1-redeem"}`, pid, f.storeA)
	status, _, out := f.do(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-confirm", "sess-mgr-a", f.tenA, body)
	mustEqual(t, status, http.StatusOK)
	if out["outcome"] != "issued_and_redeemed" {
		t.Fatalf("gift fulfill-now = %v", out)
	}
	ben := benefitFor(t, f, "sess-mgr-a", f.tenA, f.campaignA, pid)
	if ben["status"] != "redeemed" || ben["issued_at"] == "" || ben["redeemed_at"] == "" {
		t.Fatalf("gift benefit = %v", ben)
	}
	// 现场赠品无券码。
	if code, _ := ben["code"].(string); code != "" {
		t.Fatalf("gift benefit must be code-less, got %q", code)
	}
}

func TestServiceDeskStockExhausted(t *testing.T) {
	f := newServiceDeskFixture(t)
	// first_publish 批次余量 2:两单成功,第三单 422 out_of_stock。
	for i, key := range []string{"idem-stock-1", "idem-stock-2", "idem-stock-3"} {
		pid, _ := f.startParticipation(t, key)
		status, _, out := f.do(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-confirm", "sess-mgr-a", f.tenA,
			confirmBody(pid, "first_publish", "staff_observed_submission", f.storeA, "evt-stock-"+fmt.Sprint(i)))
		if i < 2 {
			mustEqual(t, status, http.StatusOK)
		} else {
			mustEqual(t, status, http.StatusUnprocessableEntity)
			if out["error"] != "out_of_stock" {
				t.Fatalf("third confirm = %v, want out_of_stock", out)
			}
		}
	}
}

func TestServiceDeskWrongStoreRefusals(t *testing.T) {
	f := newServiceDeskFixture(t)
	pid, _ := f.startParticipation(t, "idem-wrongstore-1")

	// 邻店经理对本店活动:campaignScoped 404 掩蔽。
	status, _, _ := f.do(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-confirm", "sess-mgr-b", f.tenA,
		confirmBody(pid, "first_publish", "staff_observed_submission", f.storeB, "evt-ws-1"))
	mustEqual(t, status, http.StatusNotFound)

	// 参与属于本店、却声明在别店确认:确认门店必须与参与门店一致(403)。
	status, _, out := f.do(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-confirm", "sess-owner-a", f.tenA,
		confirmBody(pid, "first_publish", "staff_observed_submission", f.storeB, "evt-ws-2"))
	mustEqual(t, status, http.StatusForbidden)
	if out["error"] != "target_mismatch" {
		t.Fatalf("wrong store confirm = %v", out)
	}
	// 被拒确认不留账本行。
	if ben := benefitFor(t, f, "sess-owner-a", f.tenA, f.campaignA, pid); ben != nil {
		t.Fatalf("refused confirm must leave no benefit row, got %v", ben)
	}
}

func TestServiceDeskConfirmBindsRealParticipation(t *testing.T) {
	f := newServiceDeskFixture(t)

	// 编造参与引用不能过账:确认必须绑定真实参与(防伪造确认绕过准入)。
	status, _, _ := f.do(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-confirm", "sess-mgr-a", f.tenA,
		confirmBody("pt_forged000000000000000000000000000", "first_publish", "staff_observed_submission", f.storeA, "evt-forged-1"))
	mustEqual(t, status, http.StatusNotFound)
	if ben := benefitFor(t, f, "sess-owner-a", f.tenA, f.campaignA, "pt_forged000000000000000000000000000"); ben != nil {
		t.Fatalf("forged participation must leave no benefit row, got %v", ben)
	}
}

func TestServiceDeskRevokedMemberCannotAct(t *testing.T) {
	f := newServiceDeskFixture(t)
	pid, _ := f.startParticipation(t, "idem-revoked-1")

	// 撤权成员确认/核销:403 member_disabled。
	status, _, _ := f.do(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-confirm", "sess-off-a", f.tenA,
		confirmBody(pid, "first_publish", "staff_observed_submission", f.storeA, "evt-rv-1"))
	mustEqual(t, status, http.StatusForbidden)
	status, _, _ = f.do(t, "GET", "/api/v1/campaigns/"+f.campaignA+"/reward-benefits", "sess-off-a", f.tenA, "")
	mustEqual(t, status, http.StatusForbidden)
}

func TestServiceDeskExpiredWindowParticipation(t *testing.T) {
	f := newServiceDeskFixture(t)
	pid, _ := f.startParticipation(t, "idem-expired-1")

	// 参与窗口已关闭(时间推移后过期):确认被拒(409 frozen),不发奖。
	_, err := f.s.St.DB.Exec(`UPDATE participations SET expires_at=? WHERE id=?`,
		time.Now().UTC().Add(-time.Hour).Format(time.RFC3339), pid)
	mustNoErr(t, err)
	status, _, out := f.do(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-confirm", "sess-mgr-a", f.tenA,
		confirmBody(pid, "first_publish", "staff_observed_submission", f.storeA, "evt-exp-1"))
	mustEqual(t, status, http.StatusConflict)
	if out["error"] != "frozen" {
		t.Fatalf("expired participation confirm = %v, want frozen", out)
	}
	if ben := benefitFor(t, f, "sess-owner-a", f.tenA, f.campaignA, pid); ben != nil {
		t.Fatalf("expired participation must leave no benefit row, got %v", ben)
	}
}

func TestServiceDeskReverseOwnerOnly(t *testing.T) {
	f := newServiceDeskFixture(t)
	pid, _ := f.startParticipation(t, "idem-reverse-1")
	benID, _ := f.seedIssuedBenefit(t, pid, "first_publish", "staff_observed_submission", "evt-rev-issue")

	// 店员/店经理没有更正撤销权(预算权归总部)。
	status, _, _ := f.do(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-benefits/"+benID+"/reverse", "sess-mgr-a", f.tenA,
		`{"reason":"测试","event_id":"evt-rev-mgr"}`)
	mustEqual(t, status, http.StatusForbidden)

	// org_owner + 原因:撤销保留原记录(追加,不删账)。
	status, _, out := f.do(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-benefits/"+benID+"/reverse", "sess-owner-a", f.tenA,
		`{"reason":"误扫顾客甲","event_id":"evt-rev-owner"}`)
	mustEqual(t, status, http.StatusOK)
	if out["outcome"] != "reversed" {
		t.Fatalf("reverse = %v", out)
	}
	ben := benefitFor(t, f, "sess-owner-a", f.tenA, f.campaignA, pid)
	if ben["status"] != "reversed" || ben["reverse_reason"] != "误扫顾客甲" || ben["issued_at"] == "" {
		t.Fatalf("reversed benefit must keep the original issue facts: %v", ben)
	}

	// 无原因不撤销。
	status, _, _ = f.do(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-benefits/"+benID+"/reverse", "sess-owner-a", f.tenA,
		`{"reason":"","event_id":"evt-rev-noreason"}`)
	mustEqual(t, status, http.StatusBadRequest)
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
