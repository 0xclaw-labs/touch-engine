package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// HUI-1896: the public page must name the store before asking for contact
// details, and a visit must not become a lead or a CRM row.

func TestPublicPageNamesMerchantWithoutInternalFields(t *testing.T) {
	f := newFixture(t, true)
	status, _, sto := f.do(t, "POST", "/api/v1/stores", "sess-owner-a", f.tenA, `{"name":"湖滨店","address":"内部地址不公开"}`)
	mustEqual(t, status, http.StatusCreated)
	storeID := sto["id"].(string)

	status, _, cmp := f.do(t, "POST", "/api/v1/campaigns", "sess-owner-a", f.tenA,
		`{"title":"到店咖啡","public_content":"到店送一杯","store_id":"`+storeID+`","starts_at":"2026-09-01T00:00:00Z","ends_at":"2026-10-31T00:00:00Z"}`)
	mustEqual(t, status, http.StatusCreated)
	id := cmp["id"].(string)
	status, _, _ = f.do(t, "POST", "/api/v1/campaigns/"+id+"/status", "sess-owner-a", f.tenA, `{"status":"active"}`)
	mustEqual(t, status, http.StatusOK)
	status, _, link := f.do(t, "POST", "/api/v1/campaigns/"+id+"/links", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusCreated)
	code := link["code"].(string)

	status, _, view := f.do(t, "GET", "/api/v1/public/links/"+code+"?entry=nfc&tenant_id="+f.tenB, "", "", "")
	mustEqual(t, status, http.StatusOK)
	if view["merchant_name"] != "商家A" || view["store_name"] != "湖滨店" || view["title"] != "到店咖啡" || view["public_content"] != "到店送一杯" {
		t.Fatalf("public identity = %v", view)
	}
	for _, forbidden := range []string{
		"tenant_id", "store_id", "id", "order_ref", "created_by", "principal", "agency",
		"asset_id", "crm", "address", "code", "status",
	} {
		if _, ok := view[forbidden]; ok {
			t.Fatalf("public payload leaks %s: %v", forbidden, view)
		}
	}
	raw, _ := json.Marshal(view)
	if strings.Contains(string(raw), "内部地址") || strings.Contains(string(raw), f.tenB) {
		t.Fatalf("query or address leaked: %v", view)
	}
}

func TestLeadFormStatesPurposeSeparatelyFromMarketing(t *testing.T) {
	f := newLeadsFixture(t)
	status, _, form := f.guest(t, "GET", "/api/v1/public/links/"+f.code+"/lead-form", "")
	if status != 200 {
		t.Fatalf("lead form = %d %v", status, form)
	}
	recipient, _ := form["recipient"].(map[string]any)
	if recipient["name"] != "商家A" {
		t.Fatalf("recipient = %v", form["recipient"])
	}
	if form["purpose"] == "" || form["marketing_blocks_browse"] != false {
		t.Fatalf("purpose/browse = %v", form)
	}
	notice, _ := form["notice"].(map[string]any)
	if notice["version"] != "v1" {
		t.Fatalf("notice = %v", form["notice"])
	}
	required, _ := form["required_fields"].([]any)
	if len(required) != 2 || required[0] != "name" || required[1] != "phone" {
		t.Fatalf("required = %v", form["required_fields"])
	}
}

func TestSubmitSaysAcceptedNotSalesReceived(t *testing.T) {
	f := newLeadsFixture(t)
	beforeMembers := countRows(t, f, "members")
	body := `{"name":"李四","phone":"13800138000","consent_version":"v1","consent":true,"marketing_optin":false,"channel":"nfc"}`
	status, out := f.submitLead(t, f.code, body)
	if status != 201 {
		t.Fatalf("submit = %d %v", status, out)
	}
	if out["state"] != "accepted" || out["duplicate"] != false || out["sales_received"] != "unknown" || out["crm_received"] != false || out["owner_followed_up"] != "unknown" || out["creates_platform_account"] != false {
		t.Fatalf("submit honesty = %v", out)
	}
	submitted, _ := out["submitted_to"].(map[string]any)
	if submitted["name"] != "商家A" {
		t.Fatalf("submitted_to = %v", out["submitted_to"])
	}
	if countRows(t, f, "members") != beforeMembers {
		t.Fatal("visitor submit created a platform member")
	}
	rows, _ := f.s.St.ListLeadSubmissions(f.tenA, f.campaignID)
	if len(rows) != 1 || rows[0].MarketingOptin {
		t.Fatalf("stored lead = %+v", rows)
	}

	againStatus, again := f.submitLead(t, f.code, body)
	if againStatus != 200 || again["duplicate"] != true || again["submission_ref"] != out["submission_ref"] || again["sales_received"] != "unknown" {
		t.Fatalf("duplicate = %d %v", againStatus, again)
	}
	rows, _ = f.s.St.ListLeadSubmissions(f.tenA, f.campaignID)
	if len(rows) != 1 {
		t.Fatalf("duplicate created another row: %d", len(rows))
	}
}

func TestViewAndScanDoNotCreateLeads(t *testing.T) {
	f := newLeadsFixture(t)
	for _, channel := range []string{"nfc", "qr", "web"} {
		status, _, _ := f.guest(t, "POST", "/api/v1/public/links/"+f.code+"/view-events", `{"channel":"`+channel+`"}`)
		if status != 204 {
			t.Fatalf("view %s = %d", channel, status)
		}
	}
	if status, _, _ := f.guest(t, "POST", "/api/v1/public/links/"+f.code+"/view-events", `{"channel":"nfc","tenant_id":"tnt_evil"}`); status != 400 {
		t.Fatalf("forged tenant on beacon = %d, want 400", status)
	}
	rows, _ := f.s.St.ListLeadSubmissions(f.tenA, f.campaignID)
	if len(rows) != 0 {
		t.Fatalf("views created leads: %d", len(rows))
	}
	for _, channel := range []string{"nfc", "qr", "web"} {
		n, err := f.s.St.GetViewStat(f.code, todayUTC(), channel)
		if err != nil || n != 1 {
			t.Fatalf("stat %s = %d %v", channel, n, err)
		}
	}
}

func TestLeadStatusProjectsCRMPausedWithoutClaimingSales(t *testing.T) {
	f := newLeadsFixture(t)
	status, out := f.submitLead(t, f.code, validLeadBody)
	if status != 201 {
		t.Fatalf("submit = %d %v", status, out)
	}
	ref := out["submission_ref"].(string)
	if err := f.s.St.MarkLeadRejected(ref, "subscription_disabled: crm paused"); err != nil {
		t.Fatal(err)
	}
	status, _, body := f.guest(t, "POST", "/api/v1/public/links/"+f.code+"/lead-status",
		`{"submission_ref":"`+ref+`","phone":"13800138000"}`)
	if status != 200 || body["state"] != "crm_paused" || body["sales_received"] != "unknown" || body["crm_received"] != false || body["owner_followed_up"] != "unknown" {
		t.Fatalf("crm paused status = %d %v", status, body)
	}
	status, _, hidden := f.guest(t, "POST", "/api/v1/public/links/"+f.code+"/lead-status",
		`{"submission_ref":"`+ref+`","phone":"13900139000"}`)
	if status != 404 {
		t.Fatalf("wrong phone status = %d %v", status, hidden)
	}
}

func TestVisitorCloseKeepsUnknownAndOneLead(t *testing.T) {
	f := newLeadsFixture(t)
	beforeLeads := countRows(t, f, "lead_submissions")
	beforeMembers := countRows(t, f, "members")
	var tenB string
	if err := f.s.St.DB.QueryRow(`SELECT id FROM tenants WHERE name=?`, "商家B").Scan(&tenB); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		status string
		want   string
	}{
		{"暂停", "paused", "paused"},
		{"结束", "ended", "ended"},
	} {
		_, _, cmp := f.admin(t, "POST", "/api/v1/campaigns",
			`{"title":"`+tc.name+`活动","public_content":"不能留资","starts_at":"2026-01-01T00:00:00Z","ends_at":"2030-01-01T00:00:00Z"}`)
		id := cmp["id"].(string)
		if status, _, _ := f.admin(t, "POST", "/api/v1/campaigns/"+id+"/status", `{"status":"active"}`); status != 200 {
			t.Fatalf("%s activate = %d", tc.name, status)
		}
		if tc.status == "paused" {
			if status, _, _ := f.admin(t, "POST", "/api/v1/campaigns/"+id+"/status", `{"status":"paused"}`); status != 200 {
				t.Fatalf("pause = %d", status)
			}
		} else if status, _, _ := f.admin(t, "POST", "/api/v1/campaigns/"+id+"/status", `{"status":"ended"}`); status != 200 {
			t.Fatalf("end = %d", status)
		}
		_, _, link := f.admin(t, "POST", "/api/v1/campaigns/"+id+"/links", ``)
		code := link["code"].(string)
		status, _, body := f.guest(t, "POST", "/api/v1/public/links/"+code+"/lead-submissions", validLeadBody)
		if status == http.StatusCreated || body["state"] != tc.want {
			t.Fatalf("%s lead = %d %v", tc.name, status, body)
		}
	}
	status, _, missing := f.guest(t, "POST", "/api/v1/public/links/NO_SUCH_CODE/lead-submissions", validLeadBody)
	if status == http.StatusCreated || missing["state"] != "not_found" {
		t.Fatalf("missing lead = %d %v", status, missing)
	}
	if countRows(t, f, "lead_submissions") != beforeLeads {
		t.Fatalf("closed campaigns stored a lead")
	}

	body := `{"name":"李四","phone":"13800138000","consent_version":"v1","consent":true,"marketing_optin":false,"channel":"nfc"}`
	status, out := f.submitLead(t, f.code, body)
	if status != http.StatusCreated {
		t.Fatalf("submit = %d %v", status, out)
	}
	if out["sales_received"] != "unknown" || out["owner_followed_up"] != "unknown" || out["crm_received"] != false || out["creates_platform_account"] != false {
		t.Fatalf("progress = %v", out)
	}
	if _, ok := out["order"]; ok {
		t.Fatalf("visitor submit named an order: %v", out)
	}
	if _, ok := out["order_ref"]; ok {
		t.Fatalf("visitor submit named an order ref: %v", out)
	}
	rows, _ := f.s.St.ListLeadSubmissions(f.tenA, f.campaignID)
	if len(rows) != 1 || rows[0].MarketingOptin {
		t.Fatalf("stored = %+v", rows)
	}
	if countRows(t, f, "leads_outbox") != 1 {
		t.Fatalf("outbox = %d", countRows(t, f, "leads_outbox"))
	}

	again := `{"name":"李四","phone":"13800138000","consent_version":"v1","consent":true,"marketing_optin":true,"channel":"nfc","tenant_id":"` + tenB + `"}`
	forgedStatus, _, forged := f.guest(t, "POST", "/api/v1/public/links/"+f.code+"/lead-submissions", again)
	if forgedStatus == http.StatusCreated {
		t.Fatalf("forged tenant created a lead: %v", forged)
	}
	replay := `{"name":"李四","phone":"13800138000","consent_version":"v1","consent":true,"marketing_optin":true,"channel":"nfc"}`
	replayStatus, replayBody := f.submitLead(t, f.code, replay)
	if replayStatus != http.StatusOK || replayBody["duplicate"] != true || replayBody["submission_ref"] != out["submission_ref"] || replayBody["sales_received"] != "unknown" {
		t.Fatalf("replay = %d %v", replayStatus, replayBody)
	}
	rows, _ = f.s.St.ListLeadSubmissions(f.tenA, f.campaignID)
	if len(rows) != 1 || rows[0].MarketingOptin {
		t.Fatalf("replay stored = %+v", rows)
	}
	if countRows(t, f, "leads_outbox") != 1 || countRows(t, f, "members") != beforeMembers {
		t.Fatalf("replay wrote another fact or member")
	}
	var other int
	if err := f.s.St.DB.QueryRow(`SELECT COUNT(1) FROM lead_submissions WHERE tenant_id=?`, tenB).Scan(&other); err != nil || other != 0 {
		t.Fatalf("other merchant leads = %d %v", other, err)
	}
}

func TestPublicVisitorR3KeepsSeparateCounts(t *testing.T) {
	f := newLeadsFixture(t)
	beforeMembers := countRows(t, f, "members")
	var tenB string
	if err := f.s.St.DB.QueryRow(`SELECT id FROM tenants WHERE name=?`, "商家B").Scan(&tenB); err != nil {
		t.Fatal(err)
	}

	for _, query := range []string{"", "?entry=nfc", "?entry=qr", "?entry=nfc&tenant_id=" + tenB} {
		status, _, view := f.guest(t, "GET", "/api/v1/public/links/"+f.code+query, "")
		if status != http.StatusOK || view["title"] != "周末市集" || view["merchant_name"] != "商家A" {
			t.Fatalf("same activity %s = %d %v", query, status, view)
		}
		raw, _ := json.Marshal(view)
		for _, forbidden := range []string{"conversion", "tenant_id", "org", "agent", "asset_grant", "channel_balance", "platform_account", "crm_status", "sales_received"} {
			if _, ok := view[forbidden]; ok || strings.Contains(string(raw), "销售已收到") {
				t.Fatalf("public activity leaked %s: %s", forbidden, raw)
			}
		}
		if strings.Contains(string(raw), tenB) {
			t.Fatalf("query tenant leaked into %s", query)
		}
	}

	status, _, _ := f.guest(t, "POST", "/api/v1/public/links/"+f.code+"/view-events", `{"channel":"nfc"}`)
	if status != http.StatusNoContent {
		t.Fatalf("exposure = %d", status)
	}
	body := `{"actions":[{"kind":"wifi","enabled":true,"href":"https://shop.example.com/wifi"}],"return":{"enabled":true,"href":"/c/back"}}`
	status, _, saved := f.admin(t, "PUT", "/api/v1/campaigns/"+f.campaignID+"/extra-jumps", body)
	if status != http.StatusOK {
		t.Fatalf("jumps = %d %v", status, saved)
	}
	status, _, click := f.guest(t, "POST", "/api/v1/public/links/"+f.code+"/extra-jumps/wifi/clicks", `{}`)
	if status != http.StatusOK || click["success"] != false || click["platform_result"] != "unknown" || click["lead_created"] != false {
		t.Fatalf("click = %d %v", status, click)
	}
	status, _, back := f.guest(t, "POST", "/api/v1/public/links/"+f.code+"/returns/clicks", `{}`)
	if status != http.StatusOK || back["success"] != false || back["platform_result"] != "unknown" || back["href"] != "/c/back" {
		t.Fatalf("return = %d %v", status, back)
	}

	submitBody := `{"name":"李四","phone":"13800138000","consent_version":"v1","consent":true,"marketing_optin":false,"channel":"nfc"}`
	status, out := f.submitLead(t, f.code, submitBody)
	if status != http.StatusCreated {
		t.Fatalf("submit = %d %v", status, out)
	}
	raw, _ := json.Marshal(out)
	if out["state"] != "accepted" || out["duplicate"] != false || out["crm_received"] != false || out["sales_received"] != "unknown" || out["creates_platform_account"] != false {
		t.Fatalf("submit honesty = %v", out)
	}
	if _, ok := out["conversion"]; ok || strings.Contains(string(raw), "销售已收到") {
		t.Fatalf("submit collapsed or claimed sales: %s", raw)
	}
	submitted, _ := out["submitted_to"].(map[string]any)
	if submitted["name"] != "商家A" {
		t.Fatalf("submitted_to = %v", out["submitted_to"])
	}

	exposure, err := f.s.St.GetViewStat(f.code, todayUTC(), "nfc")
	if err != nil {
		t.Fatal(err)
	}
	var crm int
	if err := f.s.St.DB.QueryRow(`SELECT COUNT(1) FROM lead_submissions WHERE sync_state=?`, "crm_received").Scan(&crm); err != nil {
		t.Fatal(err)
	}
	clicks := countRows(t, f, "extra_jump_clicks")
	returns := countRows(t, f, "authorized_return_clicks")
	leads := countRows(t, f, "lead_submissions")
	if exposure != 1 || clicks != 1 || returns != 1 || leads != 1 || crm != 0 {
		t.Fatalf("counts exposure=%d click=%d return=%d lead=%d crm=%d", exposure, clicks, returns, leads, crm)
	}
	if countRows(t, f, "members") != beforeMembers {
		t.Fatal("visitor submit created a platform member")
	}
	rows, _ := f.s.St.ListLeadSubmissions(f.tenA, f.campaignID)
	if len(rows) != 1 || rows[0].MarketingOptin || rows[0].TenantID != f.tenA {
		t.Fatalf("stored lead = %+v", rows)
	}
}

func countRows(t *testing.T, f *leadsFixture, table string) int {
	t.Helper()
	var n int
	if err := f.s.St.DB.QueryRow(`SELECT COUNT(1) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func todayUTC() string {
	return time.Now().UTC().Format("2006-01-02")
}

// HUI-2628: the public page must only offer actions the server can actually
// serve. Capabilities are configuration facts, not client guesses.
func TestPublicLinkPublishesCapabilityFlags(t *testing.T) {
	f := newFixtureEnv(t, false, map[string]string{
		"FEATURE_PARTICIPATION": "on",
		"FEATURE_CONTENT_PACKS": "on",
		"FEATURE_REWARD_OPS":    "on",
	})
	status, _, sto := f.do(t, "POST", "/api/v1/stores", "sess-owner-a", f.tenA, `{"name":"能力店","address":"x"}`)
	mustEqual(t, status, http.StatusCreated)
	storeID := sto["id"].(string)
	status, _, cmp := f.do(t, "POST", "/api/v1/campaigns", "sess-owner-a", f.tenA,
		`{"title":"能力活动","public_content":"看活动","store_id":"`+storeID+`","starts_at":"2026-09-01T00:00:00Z","ends_at":"2026-10-31T00:00:00Z"}`)
	mustEqual(t, status, http.StatusCreated)
	id := cmp["id"].(string)
	status, _, _ = f.do(t, "POST", "/api/v1/campaigns/"+id+"/status", "sess-owner-a", f.tenA, `{"status":"active"}`)
	mustEqual(t, status, http.StatusOK)
	status, _, link := f.do(t, "POST", "/api/v1/campaigns/"+id+"/links", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusCreated)
	code := link["code"].(string)

	status, _, view := f.do(t, "GET", "/api/v1/public/links/"+code, "", "", "")
	mustEqual(t, status, http.StatusOK)
	caps, ok := view["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities missing: %v", view)
	}
	for _, key := range []string{"participation", "content_packs", "reward_ops"} {
		if caps[key] != true {
			t.Fatalf("capability %s = %v, want true", key, caps[key])
		}
	}
	if caps["leads_capture"] != false {
		t.Fatalf("leads capability must stay false without its own config: %v", caps)
	}

	off := newFixture(t, false)
	status, _, sto = off.do(t, "POST", "/api/v1/stores", "sess-owner-a", off.tenA, `{"name":"关闭店","address":"x"}`)
	mustEqual(t, status, http.StatusCreated)
	status, _, cmp = off.do(t, "POST", "/api/v1/campaigns", "sess-owner-a", off.tenA,
		`{"title":"关闭活动","public_content":"看活动","store_id":"`+sto["id"].(string)+`","starts_at":"2026-09-01T00:00:00Z","ends_at":"2026-10-31T00:00:00Z"}`)
	mustEqual(t, status, http.StatusCreated)
	id = cmp["id"].(string)
	status, _, _ = off.do(t, "POST", "/api/v1/campaigns/"+id+"/status", "sess-owner-a", off.tenA, `{"status":"active"}`)
	mustEqual(t, status, http.StatusOK)
	status, _, link = off.do(t, "POST", "/api/v1/campaigns/"+id+"/links", "sess-owner-a", off.tenA, "")
	mustEqual(t, status, http.StatusCreated)
	status, _, view = off.do(t, "GET", "/api/v1/public/links/"+link["code"].(string), "", "", "")
	mustEqual(t, status, http.StatusOK)
	caps, ok = view["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities missing on off fixture: %v", view)
	}
	for _, key := range []string{"participation", "content_packs", "reward_ops", "leads_capture"} {
		if caps[key] != false {
			t.Fatalf("capability %s = %v, want false", key, caps[key])
		}
	}
}
