package httpapi

// HUI-1671 T16/边界 HTTP 面:
//   - 活动结束+订阅到期: 新承诺(confirm/reserve)按原因拒绝;已发未过期券
//     顾客可查、可重签展示码、受权店员可核销;
//   - 点评动作(review)确认被服务端拒绝,库存/账不变;
//   - 外部平台券本地核销拒绝;
//   - 店员码定位不带顾客身份。

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

func (f *rewardFixture) doVisitor(t *testing.T, method, path, visitor, body string) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, f.ts.URL+path, rd)
	mustNoErr(t, err)
	if visitor != "" {
		req.AddCookie(&http.Cookie{Name: "touch_participation_visitor", Value: visitor})
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := f.ts.Client().Do(req)
	mustNoErr(t, err)
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

// seedVisitorParticipation inserts a participation row for the visitor path
// (participations 表只读使用——测试种子走 SQL,生产写入仍归参与域)。
func (f *rewardFixture) seedVisitorParticipation(t *testing.T, participationID, visitorID string) {
	t.Helper()
	now := time.Now().UTC()
	_, err := f.s.St.DB.Exec(
		`INSERT INTO participations(id,tenant_id,store_id,campaign_id,link_code,touchpoint_source,rule_version,visitor_id,status,idempotency_key,benefit_slot,benefit_reservation_ref,benefit_reservation_state,fulfillment_ref,starts_at,expires_at,created_at,updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		participationID, f.tenA, f.storeA, f.campaignA, f.linkCode, "manual", 1, visitorID, "in_progress",
		"idem-"+participationID, "first_publish", "", "not_requested", "",
		now.Add(-24*time.Hour).Format(time.RFC3339), now.Add(48*time.Hour).Format(time.RFC3339),
		now.Format(time.RFC3339), now.Format(time.RFC3339))
	mustNoErr(t, err)
}

// expireCampaignAndSubscription simulates 活动结束+订阅到期.
func (f *rewardFixture) expireCampaignAndSubscription(t *testing.T) {
	t.Helper()
	if _, err := f.s.St.DB.Exec(`UPDATE campaigns SET ends_at='2026-11-01T00:00:00Z' WHERE id=?`, f.campaignA); err != nil {
		t.Fatal(err)
	}
	f.s.Cfg.SubscriptionStatus = "expired"
}

func TestRewardPromiseBlockedButOldCouponAliveT16(t *testing.T) {
	f := rewardHTTPFixture(t, true)
	f.seedVisitorParticipation(t, "p-old", "visitor-old")

	// 发一份真实权益(在活动还活着的时候)。
	code, out := f.doR(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-confirm", "sess-staff-a",
		f.confirmBody("p-old", "staff_observed_submission", false))
	if code != 200 {
		t.Fatalf("confirm while alive: %d %v", code, out)
	}

	f.expireCampaignAndSubscription(t)

	// 新承诺被拒,原因属于六种到期原因。
	code, out = f.doR(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-confirm", "sess-staff-a",
		f.confirmBody("p-new", "staff_observed_submission", false))
	if code != 422 {
		t.Fatalf("new promise after expiry must 422: %d %v", code, out)
	}
	reason, _ := out["error"].(string)
	switch reason {
	case "subscription_expired", "campaign_ended":
	default:
		t.Fatalf("reason must name the exact cause: %v", out)
	}

	// 受信预留同样按到期拒绝。
	code, out = f.doR(t, "POST", "/internal/v1/reward-reservations", "", `{"tenant_id":"`+f.tenA+`","campaign_id":"`+f.campaignA+`","participation_id":"p-new","benefit_slot":"first_publish","rule_version":1,"event_id":"res-new","actor":"participation-service","ttl_seconds":3600}`)
	if code != 422 {
		t.Fatalf("reserve after expiry must 422: %d %v", code, out)
	}

	// T16: 旧券仍可查(不经 resolveAvailableLink)。
	code, out = f.doVisitor(t, "GET", "/api/v1/visitor/reward-benefits", "visitor-old", "")
	if code != 200 {
		t.Fatalf("visitor query after expiry must 200: %d %v", code, out)
	}
	items, _ := out["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("visitor must see their issued benefit: %v", out)
	}
	ben := items[0].(map[string]any)["benefit"].(map[string]any)
	if ben["code"] == "" || ben["status"] != "issued" {
		t.Fatalf("own code and status must ride along: %v", ben)
	}

	// T16: 受权店员按原条件核销旧券。
	benefitID := ben["id"].(string)
	code, out = f.doR(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-benefits/"+benefitID+"/redeem",
		"sess-staff-a", `{"event_id":"rdm-old","store_id":"`+f.storeA+`"}`)
	if code != 200 || out["outcome"] != "redeemed" {
		t.Fatalf("staff redeem of old coupon must succeed: %d %v", code, out)
	}
}

func TestRewardConfirmRefusesReviewAction(t *testing.T) {
	f := rewardHTTPFixture(t, false)
	body := strings.Replace(f.confirmBody("p-review", "staff_observed_submission", false),
		`"note":"店员现场确认"`, `"note":"点评后领券","action_kind":"review"`, 1)
	code, out := f.doR(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-confirm", "sess-staff-a", body)
	if code != 422 || out["error"] != "reward_not_eligible" {
		t.Fatalf("review-triggered confirm must refuse reward_not_eligible: %d %v", code, out)
	}
	// 库存/账不变。
	var benefits int
	mustNoErr(t, f.s.St.DB.QueryRow(`SELECT COUNT(*) FROM reward_benefits`).Scan(&benefits))
	if benefits != 0 {
		t.Fatalf("refused confirm must not create a benefit: %d", benefits)
	}
	inv, err := f.s.St.GetRewardInventory(f.invID, f.tenA)
	mustNoErr(t, err)
	if inv.AvailableQty != inv.TotalQty {
		t.Fatalf("stock must be untouched: %+v", inv)
	}
}

func TestVisitorDisplayCodeAndStaffLookup(t *testing.T) {
	f := rewardHTTPFixture(t, true)
	f.seedVisitorParticipation(t, "p-code", "visitor-code")
	code, out := f.doR(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-confirm", "sess-staff-a",
		f.confirmBody("p-code", "staff_observed_submission", false))
	if code != 200 {
		t.Fatalf("confirm: %d %v", code, out)
	}

	// 无访客会话 → 诚实 401。
	if code, _ = f.doVisitor(t, "GET", "/api/v1/visitor/reward-benefits", "", ""); code != 401 {
		t.Fatalf("no cookie must 401: %d", code)
	}

	// 持有效访客会话重签展示码。
	code, out = f.doVisitor(t, "POST", "/api/v1/visitor/reward-benefits/rben-unknown/display-code", "visitor-code", "")
	if code != 404 {
		t.Fatalf("unknown benefit must 404: %d %v", code, out)
	}
	var benefitID string
	mustNoErr(t, f.s.St.DB.QueryRow(`SELECT id FROM reward_benefits LIMIT 1`).Scan(&benefitID))
	code, out = f.doVisitor(t, "POST", "/api/v1/visitor/reward-benefits/"+benefitID+"/display-code", "visitor-code", "")
	if code != 201 {
		t.Fatalf("re-sign display code: %d %v", code, out)
	}
	token, _ := out["token"].(string)
	if token == "" {
		t.Fatalf("token must ride the mint response: %v", out)
	}

	// 店员码定位: 定位卡,无顾客身份。
	code, out = f.doR(t, "POST", "/api/v1/reward-display-codes/lookup", "sess-staff-a",
		fmt.Sprintf(`{"code":%q}`, token))
	if code != 200 {
		t.Fatalf("staff lookup: %d %v", code, out)
	}
	if out["customer_identity_returned"] != false {
		t.Fatalf("lookup must never return customer identity: %v", out)
	}
	if b, ok := out["benefit"].(map[string]any); !ok || b["id"] != benefitID {
		t.Fatalf("lookup must locate the benefit: %v", out)
	}
	if _, has := out["visitor"]; has {
		t.Fatalf("lookup payload must not carry a visitor field: %v", out)
	}

	// 过期旧码本身拒绝(掩蔽为 not_found)。
	if _, err := f.s.St.DB.Exec(`UPDATE reward_display_codes SET expires_at='2026-01-01T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	code, _ = f.doR(t, "POST", "/api/v1/reward-display-codes/lookup", "sess-staff-a",
		fmt.Sprintf(`{"code":%q}`, token))
	if code != 404 {
		t.Fatalf("expired old code must be refused: %d", code)
	}

	// 跨访客/伪造请求 → 掩蔽 404。
	code, _ = f.doVisitor(t, "POST", "/api/v1/visitor/reward-benefits/"+benefitID+"/display-code", "visitor-evil", "")
	if code != 404 {
		t.Fatalf("foreign visitor must be masked: %d", code)
	}

	// FEATURE off → 路由不注册,统一 404。
	off := rewardHTTPFixture(t, false)
	if code, _ = off.doVisitor(t, "GET", "/api/v1/visitor/reward-benefits", "v", ""); code != 404 {
		t.Fatalf("feature-off visitor surface must 404: %d", code)
	}
}

func TestExternalBenefitRedeemForbiddenHTTP(t *testing.T) {
	f := rewardHTTPFixture(t, false)
	offer, err := f.s.St.CreateDealOffer(f.tenA, store.NewDealOffer{
		CampaignID: f.campaignA, Kind: store.DealKindExternalProduct, Issuer: "meituan",
		Provider: "meituan", ProviderOfferID: "mt-1", AuthorizationRef: "auth-1",
		Title: "美团套餐", FaceValueMinor: 8800, CreatedBy: "usr_owner_a",
	})
	mustNoErr(t, err)
	if _, err := f.s.St.DB.Exec(`UPDATE reward_inventory SET offer_id=? WHERE id=?`, offer.ID, f.invID); err != nil {
		t.Fatal(err)
	}
	code, out := f.doR(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-confirm", "sess-staff-a",
		f.confirmBody("p-ext", "staff_observed_submission", false))
	if code != 200 {
		t.Fatalf("issue against external offer (display/await semantics): %d %v", code, out)
	}
	var benefitID string
	mustNoErr(t, f.s.St.DB.QueryRow(`SELECT id FROM reward_benefits LIMIT 1`).Scan(&benefitID))

	// 本地账无权核销外部券(明确拒绝码)。
	code, out = f.doR(t, "POST", "/api/v1/campaigns/"+f.campaignA+"/reward-benefits/"+benefitID+"/redeem",
		"sess-staff-a", `{"event_id":"rdm-ext","store_id":"`+f.storeA+`"}`)
	if code != 422 || out["error"] != "external_redeem_local_forbidden" {
		t.Fatalf("local redeem of external coupon must refuse: %d %v", code, out)
	}
	// 账不变。
	cur, err := f.s.St.GetRewardBenefit(benefitID, f.tenA, f.campaignA)
	mustNoErr(t, err)
	if cur.Status != store.RewardStatusIssued {
		t.Fatalf("refused redeem must not mutate: %+v", cur)
	}
}
