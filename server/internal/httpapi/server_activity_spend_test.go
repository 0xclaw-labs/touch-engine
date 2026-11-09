package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPublicClaimDoesNotCreateUserOrCash(t *testing.T) {
	f := newFixture(t, false)
	code := activePublicCode(t, f)
	before := countTable(t, f, "members")

	status, _, view := f.do(t, "POST", "/api/v1/public/links/"+code+"/benefit-claims", "", "",
		`{"kind":"coupon","face_minor":2000,"op":"claim","as_platform_cash":true}`)
	if status != 422 {
		t.Fatalf("cash disguise = %d %v", status, view)
	}
	if countTable(t, f, "members") != before || countTable(t, f, "activity_benefit_facts") != 0 {
		t.Fatal("disguise created a user or a cash row")
	}

	status, _, view = f.do(t, "POST", "/api/v1/public/links/"+code+"/benefit-claims", "", "",
		`{"kind":"coupon","face_minor":2000,"op":"claim"}`)
	if status != 201 {
		t.Fatalf("claim = %d %v", status, view)
	}
	if view["ledger"] != "activity" || view["platform_user_created"] != false || view["wallet_debited_minor"] != float64(0) || view["org_balance_visible"] != false {
		t.Fatalf("claim honesty = %v", view)
	}
	for _, key := range []string{"balance", "wallet", "principal", "platform_user", "payer_account_id", "cash_expense_minor", "org_balance"} {
		if _, ok := view[key]; ok {
			t.Fatalf("claim payload leaked %s: %v", key, view)
		}
	}
	if countTable(t, f, "members") != before {
		t.Fatal("claim created a platform member")
	}
	var cash int
	var ledger string
	if err := f.s.St.DB.QueryRow(`SELECT ledger, cash_expense_minor FROM activity_benefit_facts`).Scan(&ledger, &cash); err != nil {
		t.Fatal(err)
	}
	if ledger != "activity" || cash != 0 {
		t.Fatalf("stored ledger=%s cash=%d", ledger, cash)
	}
}

func TestPublicPageHidesEnterpriseBalance(t *testing.T) {
	f := newFixture(t, false)
	code := activePublicCode(t, f)
	status, _, view := f.do(t, "GET", "/api/v1/public/links/"+code, "", "", "")
	if status != 200 {
		t.Fatalf("public = %d %v", status, view)
	}
	raw, _ := json.Marshal(view)
	for _, forbidden := range []string{"balance", "wallet", "org_balance", "billing", "payer"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("public page leaked %s: %s", forbidden, raw)
		}
	}
}

func TestAgentChargeCannotUsePersonalWallet(t *testing.T) {
	f := newAgencyFixture(t, true)
	f.mustEstablish(t, "usr_ag1", "代运营")
	f.s.Cfg.SubscriptionStatus = "active"

	status, out := f.do(t, "POST", "/api/v1/account-separation/charges", "sess-agent1", f.tenA,
		`{"capability":"digital_human","invoked":true,"payer_authorized":true,"payer_account_id":"wal_agent","personal_wallet_id":"wal_agent"}`)
	if status != 200 {
		t.Fatalf("charge = %d %v", status, out)
	}
	if out["quote_created"] != false || out["usage_created"] != false || out["personal_wallet_debited"] != false || out["balance_mutated"] != false || out["cash_debited_minor"] != float64(0) {
		t.Fatalf("agent charge = %v", out)
	}
	if out["reason"] != "payer_authorization_required" && out["reason"] != "personal_wallet_not_payer" {
		t.Fatalf("reason = %v", out["reason"])
	}
	if out["debited_account"] != "" && out["debited_account"] != nil {
		t.Fatalf("debited = %v", out["debited_account"])
	}
}

func TestEditCopyDoesNotCreateQuote(t *testing.T) {
	f := newAgencyFixture(t, true)
	f.s.Cfg.SubscriptionStatus = "active"
	status, out := f.do(t, "POST", "/api/v1/account-separation/charges", "sess-owner-a", f.tenA,
		`{"capability":"edit_copy","invoked":true,"payer_authorized":true,"payer_account_id":"org_bill_1"}`)
	if status != 200 || out["quote_created"] != false || out["usage_created"] != false || out["balance_mutated"] != false || out["reason"] != "no_chargeable_call" {
		t.Fatalf("edit copy = %d %v", status, out)
	}
}

func TestExpiryKeepsCampaignAndRewardWithoutRecharge(t *testing.T) {
	f := newFixture(t, false)
	code, campaignID := activePublicCampaign(t, f)
	status, _, claim := f.do(t, "POST", "/api/v1/public/links/"+code+"/benefit-claims", "", "",
		`{"kind":"group_buy_voucher","face_minor":1500,"op":"claim"}`)
	if status != 201 {
		t.Fatalf("claim = %d %v", status, claim)
	}
	f.s.Cfg.SubscriptionStatus = "expired"

	status, _, campaigns := f.do(t, "GET", "/api/v1/campaigns", "sess-owner-a", f.tenA, "")
	if status != 200 || !jsonContains(campaigns, campaignID) {
		t.Fatalf("campaigns after expiry = %d %v", status, campaigns)
	}

	status, _, sep := f.do(t, "GET", "/api/v1/account-separation", "sess-owner-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("separation = %d %v", status, sep)
	}
	pkg, _ := sep["package"].(map[string]any)
	rewards, _ := sep["rewards"].(map[string]any)
	fees, _ := sep["ai_fees"].(map[string]any)
	if pkg["locks_existing"] != false || pkg["restricts_new_premium"] != true || pkg["kind"] != "touch_subscription" {
		t.Fatalf("package = %v", pkg)
	}
	if rewards["ledger"] != "activity" || rewards["appears_in_platform_wallet"] != false || rewards["face_as_cash_expense"] != false {
		t.Fatalf("rewards = %v", rewards)
	}
	if fees["includes_marketing_face"] != false || fees["kind"] != "ai_tool_fee" {
		t.Fatalf("fees = %v", fees)
	}
	raw, _ := json.Marshal(fees)
	if strings.Contains(string(raw), "1500") {
		t.Fatalf("voucher face folded into fees: %s", raw)
	}

	status, _, recharge := f.do(t, "POST", "/api/v1/account-separation/recharge", "sess-owner-a", f.tenA, `{"amount_minor":100}`)
	if status != 403 || recharge["error"] != "production_not_authorized" {
		t.Fatalf("recharge = %d %v", status, recharge)
	}
	status, _, campaigns = f.do(t, "GET", "/api/v1/campaigns", "sess-owner-a", f.tenA, "")
	if status != 200 || !jsonContains(campaigns, campaignID) || countTable(t, f, "activity_benefit_facts") != 1 {
		t.Fatalf("recharge lock or delete: %d %v", status, campaigns)
	}

	status, _, charge := f.do(t, "POST", "/api/v1/account-separation/charges", "sess-owner-a", f.tenA,
		`{"capability":"product_image","invoked":true,"payer_authorized":true,"payer_account_id":"org_bill_1"}`)
	if status != 200 || charge["quote_created"] != false || charge["reason"] != "entitlement_expired" || charge["balance_mutated"] != false {
		t.Fatalf("expired charge = %d %v", status, charge)
	}
}

func TestWorkbenchShowsThreeAccountPanes(t *testing.T) {
	f := newFixture(t, false)
	code, _ := activePublicCampaign(t, f)
	status, _, _ := f.do(t, "POST", "/api/v1/public/links/"+code+"/benefit-claims", "", "",
		`{"kind":"points","face_minor":300,"op":"claim"}`)
	if status != 201 {
		t.Fatalf("claim = %d", status)
	}
	status, _, view := f.do(t, "GET", "/api/v1/workbench", "sess-owner-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("workbench = %d %v", status, view)
	}
	billing, _ := json.Marshal(view["billing"])
	if strings.Contains(string(billing), "points") || strings.Contains(string(billing), "300") || strings.Contains(string(billing), "coupon") {
		t.Fatalf("billing absorbed marketing: %s", billing)
	}
	sep, _ := view["account_separation"].(map[string]any)
	if sep["package"] == nil || sep["ai_fees"] == nil || sep["rewards"] == nil {
		t.Fatalf("separation = %v", sep)
	}
	rewards, _ := sep["rewards"].(map[string]any)
	if rewards["appears_in_platform_wallet"] != false || rewards["ledger"] != "activity" {
		t.Fatalf("rewards = %v", rewards)
	}
	rawRewards, _ := json.Marshal(rewards)
	if !strings.Contains(string(rawRewards), "points") || !strings.Contains(string(rawRewards), "300") {
		t.Fatalf("reward missing the points fact: %s", rawRewards)
	}
}

func activePublicCode(t *testing.T, f *fixture) string {
	t.Helper()
	code, _ := activePublicCampaign(t, f)
	return code
}

func activePublicCampaign(t *testing.T, f *fixture) (string, string) {
	t.Helper()
	// HUI-1671 根因修复: 旧夹具硬编码 2026-10-31 已成过去时,ResolveLink 按
	// 活动窗口诚实判 expired(实现正确,数据过期)。窗口改为相对 now,测试
	// 不再随日历漂移;到期语义由 TestExpiryKeepsCampaignAndRewardWithoutRecharge
	// 用订阅状态翻转单独覆盖。
	now := time.Now().UTC()
	status, _, cmp := f.do(t, "POST", "/api/v1/campaigns", "sess-owner-a", f.tenA,
		`{"title":"到店礼","public_content":"到店可领","starts_at":"`+now.Add(-24*time.Hour).Format(time.RFC3339)+`","ends_at":"`+now.Add(48*time.Hour).Format(time.RFC3339)+`"}`)
	if status != 201 {
		t.Fatalf("campaign = %d %v", status, cmp)
	}
	id := cmp["id"].(string)
	status, _, _ = f.do(t, "POST", "/api/v1/campaigns/"+id+"/status", "sess-owner-a", f.tenA, `{"status":"active"}`)
	if status != 200 {
		t.Fatalf("activate = %d", status)
	}
	status, _, link := f.do(t, "POST", "/api/v1/campaigns/"+id+"/links", "sess-owner-a", f.tenA, "")
	if status != 201 {
		t.Fatalf("link = %d %v", status, link)
	}
	return link["code"].(string), id
}

func countTable(t *testing.T, f *fixture, table string) int {
	t.Helper()
	var n int
	if err := f.s.St.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func jsonContains(v any, needle string) bool {
	raw, _ := json.Marshal(v)
	return strings.Contains(string(raw), needle)
}
