package httpapi

// hui4665_4662_regression_test.go: end-to-end adversarial regression suite for
// HUI-4665 (empty-DB & fact-separated analytics) and HUI-4662 (active inquiry
// minimal fields, honest receive states, revocation waterline, zero PII leak)
// exercised through the real HTTP handlers and SQLite store.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/dashboard"
	"github.com/bianjiefilm/touch-engine/server/internal/leads"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

func mustJSONString(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return string(b)
}

// ---- HUI-4665: Empty-DB, Store-Only, Fact Separation, Scope & Permission Guards ----

func TestHUI4665EmptyOrgAndStoreOnlyContracts(t *testing.T) {
	f := newDashboardFixture(t)
	start, end := opsDayWindow()
	q := "window_start=" + start + "&window_end=" + end

	t.Run("brand-new empty org (0 stores, 0 campaigns, 0 events) never panics or fakes unknown as zero", func(t *testing.T) {
		// 1. /api/v1/dashboard/operations
		status, _, out := f.do(t, "GET", "/api/v1/dashboard/operations?"+q, "sess-owner-b", f.tenB, "")
		raw := mustJSONString(t, out)
		if status != http.StatusOK {
			t.Fatalf("operations empty org = %d body=%s", status, raw)
		}
		if echo, ok := out["filter_echo"].(map[string]any); !ok || len(echo) != 0 {
			t.Fatalf("empty filter_echo must be {}, got %#v", out["filter_echo"])
		}
		if inv, ok := out["inventory"].([]any); !ok || len(inv) != 0 {
			t.Fatalf("empty inventory must be [], got %#v", out["inventory"])
		}
		// Never contains a misleading combined "total_success" or fabricated conversion_rate
		for _, forbiddenKey := range []string{`"total_success"`, `"conversion_rate"`, `"roi"`} {
			if strings.Contains(raw, forbiddenKey) {
				t.Fatalf("operations payload must not fabricate %s: %s", forbiddenKey, raw)
			}
		}
		// Seven funnel stages are confirmed local zeros (available=true, value=0)
		funnel, _ := out["funnel"].([]any)
		if len(funnel) != 7 {
			t.Fatalf("funnel len = %d, want 7", len(funnel))
		}
		for _, item := range funnel {
			st := item.(map[string]any)
			if st["available"] != true || st["value"] != float64(0) {
				t.Fatalf("empty local funnel stage must be confirmed zero: %v", st)
			}
		}
		// Six evidence layers are present and separated
		layersBlock, _ := out["evidence_layers"].(map[string]any)
		layers, _ := layersBlock["layers"].([]any)
		if len(layers) != 6 {
			t.Fatalf("evidence_layers len = %d, want 6", len(layers))
		}
		// Unknown / unrecorded costs must stay unavailable (not fake 0)
		cost := out["cost"].(map[string]any)
		if cost["content_generation_available"] != false {
			t.Fatalf("empty content generation cost must be unavailable, got %v", cost)
		}
		if _, hasAmt := cost["content_generation_amount_minor"]; hasAmt {
			t.Fatalf("unavailable content generation cost must omit amount, got %v", cost)
		}
		recon := out["cost_reconciliation"].(map[string]any)
		if recon["subscription_available"] != false || recon["hardware_available"] != false {
			t.Fatalf("missing payment facts must remain unavailable, got %v", recon)
		}
		if _, ok := recon["subscription_paid_minor"]; ok {
			t.Fatalf("missing subscription payment must omit paid_minor, got %v", recon)
		}
		if _, ok := recon["hardware_paid_minor"]; ok {
			t.Fatalf("missing hardware payment must omit paid_minor, got %v", recon)
		}

		// 2. /api/v1/dashboard catalog: unknown external metrics must be available=false, value=null
		status, rawCat, cat := f.do(t, "GET", "/api/v1/dashboard?"+q, "sess-owner-b", f.tenB, "")
		if status != http.StatusOK {
			t.Fatalf("dashboard catalog empty org = %d body=%s", status, rawCat)
		}
		unknownKeys := map[string]bool{
			dashboard.KeyPublishedVideos:   false,
			dashboard.KeyUGCPlays:          false,
			dashboard.KeyUGCLikes:          false,
			dashboard.KeyPOIExposureDelta:  false,
			dashboard.KeyCouponRedemptions: false,
		}
		for _, item := range cat["metrics"].([]any) {
			m := item.(map[string]any)
			k := m["key"].(string)
			if _, isUnknown := unknownKeys[k]; isUnknown {
				unknownKeys[k] = true
				if m["available"] != false || m["value"] != nil {
					t.Fatalf("external metric %s must be available=false value=null, got %v", k, m)
				}
			}
		}
		for k, seen := range unknownKeys {
			if !seen {
				t.Fatalf("expected external metric %s in catalog", k)
			}
		}

		// 3. /api/v1/dashboard/operations-summary
		status, rawSum, sum := f.do(t, "GET", "/api/v1/dashboard/operations-summary", "sess-owner-b", f.tenB, "")
		if status != http.StatusOK {
			t.Fatalf("operations-summary empty org = %d body=%s", status, rawSum)
		}
		for _, k := range []string{"pending_confirm", "rewards_issued", "rewards_redeemed", "anomalies"} {
			if sum[k] != float64(0) {
				t.Fatalf("empty summary %s = %v, want 0", k, sum[k])
			}
		}
	})

	t.Run("store-only org (1 store, 0 campaigns, 0 events) exposes store dimension cleanly with zero cross-tenant bleed", func(t *testing.T) {
		// Seed tenant A activity first so we also prove zero cross-tenant bleed into tenant B
		f.beacon(t, f.code1, "nfc")
		storeB, err := f.s.St.CreateStore(f.tenB, "商家B首店", "地址B", "test")
		if err != nil {
			t.Fatalf("create storeB: %v", err)
		}

		status, raw, out := f.do(t, "GET", "/api/v1/dashboard/operations?"+q+"&store_id="+storeB.ID, "sess-owner-b", f.tenB, "")
		if status != http.StatusOK {
			t.Fatalf("store-only operations = %d body=%s", status, raw)
		}
		dims := out["dimensions"].(map[string]any)
		stores, _ := dims["stores"].([]any)
		if len(stores) != 1 || stores[0].(map[string]any)["id"] != storeB.ID {
			t.Fatalf("store-only dimensions.stores = %v, want [%s]", stores, storeB.ID)
		}
		if camps, ok := dims["campaigns"]; ok && len(camps.([]any)) != 0 {
			t.Fatalf("store-only org must have no campaigns, got %v", camps)
		}
		funnel := out["funnel"].([]any)
		if funnel[0].(map[string]any)["value"] != float64(0) {
			t.Fatalf("tenant A visit leaked into store-only tenant B: %v", funnel[0])
		}
	})
}

func TestHUI4665FactSeparationMultiEvidenceAndScopeIsolation(t *testing.T) {
	f := newDashboardFixture(t)
	// Create a second store under tenant A for cross-store isolation checks
	store2, err := f.s.St.CreateStore(f.tenA, "二号店", "地址2", "test")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ts := now.Format(time.RFC3339)

	// 1. Seed heterogeneous facts for store1:
	//    - store service open + jump_attempt
	//    - same participation with TWO different reward evidence classes (must NOT double-count distinct_participations)
	//    - authorized leads: 1 pending_sync, 1 receiver-confirmed stored, 1 revoked
	_, err = f.s.St.DB.Exec(
		`INSERT INTO store_service_events(id,tenant_id,store_id,service_id,event_kind,environment,evidence_tier,occurred_at)
		 VALUES('sse_1',?,?,'sv_wifi','open','ios_safari','click',?),
		       ('sse_2',?,?,'sv_wifi','jump_attempt','ios_safari','jump_attempt',?)`,
		f.tenA, f.store1ID, ts, f.tenA, f.store1ID, ts)
	if err != nil {
		t.Fatalf("seed service events: %v", err)
	}

	_, err = f.s.St.DB.Exec(
		`INSERT INTO participations(id,tenant_id,store_id,campaign_id,link_code,touchpoint_source,rule_version,content_pack_version,visitor_id,status,idempotency_key,benefit_slot,benefit_reservation_state,starts_at,expires_at,created_at,updated_at)
		 VALUES('part_multi',?,?,?,'LNK-1','nfc',1,'v1','vis_1','in_progress','idem_1','slot_a','confirmed',?,?,?,?)`,
		f.tenA, f.store1ID, f.camp1ID, ts, ts, ts, ts)
	if err != nil {
		t.Fatalf("seed participation: %v", err)
	}
	_, err = f.s.St.DB.Exec(
		`INSERT INTO reward_inventory(id,tenant_id,campaign_id,store_id,kind,name,benefit_slot,rule_version,allowed_evidence,total_qty,face_value_minor,cost_minor,cost_recorded,currency,status,created_at)
		 VALUES('inv_1',?,?,?,'store_coupon','券A','slot_a',1,'[]',10,1000,300,1,'CNY','active',?),
		       ('inv_2',?,?,?,'store_coupon','券B','slot_b',1,'[]',10,500,150,1,'CNY','active',?)`,
		f.tenA, f.camp1ID, f.store1ID, ts,
		f.tenA, f.camp1ID, f.store1ID, ts)
	if err != nil {
		t.Fatalf("seed inventory: %v", err)
	}
	// Same participation 'part_multi' gets two different evidence classes across two benefit slots
	_, err = f.s.St.DB.Exec(
		`INSERT INTO reward_benefits(id,tenant_id,campaign_id,store_id,inventory_id,participation_id,benefit_slot,rule_version,evidence,code,status,face_value_minor,cost_minor,cost_recorded,currency,reserved_at,issued_at,redeemed_at,reversed_at,created_at,updated_at)
		 VALUES('rb_1',?,?,?,'inv_1','part_multi','slot_a',1,'staff_observed_submission','CODE-1','issued',1000,300,1,'CNY',?,?,'','',?,?),
		       ('rb_2',?,?,?,'inv_2','part_multi','slot_b',1,'official_publish_confirmed','CODE-2','redeemed',500,150,1,'CNY',?,?,?,'',?,?)`,
		f.tenA, f.camp1ID, f.store1ID, ts, ts, ts, ts,
		f.tenA, f.camp1ID, f.store1ID, ts, ts, ts, ts, ts)
	if err != nil {
		t.Fatalf("seed benefits: %v", err)
	}

	// Seed leads across store1 and store2 (including a revoked row with waterline > 0)
	seedLeadRow := func(id, ref, storeID, phone, syncState, recvState string, revokeRev int) {
		t.Helper()
		_, e := f.s.St.DB.Exec(
			`INSERT INTO lead_submissions(id,tenant_id,campaign_id,store_id,link_id,form_id,session_id,capture_source,submission_ref,dedup_key,name,phone,wechat,marketing_optin,consent_notice_version,consent_at,consent_ip_fp,sync_state,sync_error,attempts,source_version,revoked_at,created_at,updated_at,receiver_state,receipt_id,revoke_source_revision)
			 VALUES(?,?,?,?,?,'frm_1','','store',?,?,?,'', '',0,'v1',?,'ipfp',?,'',0,1,'',?,?,?, '',?)`,
			id, f.tenA, "", storeID, "", ref, "dk_"+ref, phone, ts, syncState, ts, ts, recvState, revokeRev)
		if e != nil {
			t.Fatalf("seed lead %s: %v", id, e)
		}
	}
	seedLeadRow("l_s1_pending", "sub_s1_p", f.store1ID, "13800010001", "pending_sync", "", 0)
	seedLeadRow("l_s1_stored", "sub_s1_s", f.store1ID, "13800010002", "crm_received", "stored", 0)
	seedLeadRow("l_s1_revoked", "sub_s1_r", f.store1ID, "13800010003", "revoked", "", 2)
	// Edge case: a row with waterline > 0 must NEVER count as receiver_confirmed or pending_sync even if sync_state drifted
	seedLeadRow("l_s1_waterline", "sub_s1_w", f.store1ID, "13800010004", "crm_received", "stored", 2)
	// Store 2 lead (must not leak to store1 manager)
	seedLeadRow("l_s2_stored", "sub_s2_s", store2.ID, "13800020001", "crm_received", "stored", 0)

	start, end := opsDayWindow()
	q := "window_start=" + start + "&window_end=" + end

	// Owner reads store1 filtered operations
	status, raw, out := f.admin(t, "GET", "/api/v1/dashboard/operations?"+q+"&store_id="+f.store1ID, "")
	if status != http.StatusOK {
		t.Fatalf("owner ops store1 = %d body=%s", status, raw)
	}
	// Multi-evidence on same participation: by_class sums to 2, but distinct_participations MUST stay 1
	ev := out["evidence"].(map[string]any)
	byClass := ev["by_class"].(map[string]any)
	if byClass[dashboard.EvidenceStaffObserved] != float64(1) || byClass[dashboard.EvidenceOfficial] != float64(1) {
		t.Fatalf("evidence by_class = %v, want observed=1 official=1", byClass)
	}
	if ev["distinct_participations"] != float64(1) {
		t.Fatalf("same-session multi-evidence inflated distinct_participations: %v", ev)
	}
	// Leads facts for store1: pending=1, confirmed=1 (waterline row excluded), revoked=2, store2 excluded
	lp := out["leads"].(map[string]any)
	if lp["pending_sync"] != float64(1) || lp["receiver_confirmed"] != float64(1) || lp["revoked"] != float64(2) {
		t.Fatalf("store1 leads panel wrong: %v", lp)
	}
	if lp["store_submitted"] != float64(4) {
		t.Fatalf("store1 store_submitted = %v, want 4 (store2 must not bleed)", lp)
	}
	// Store service opens/jumps counted separately in evidence layers
	layers := out["evidence_layers"].(map[string]any)["layers"].([]any)
	findLayerCount := func(layerKey, countKey string) float64 {
		for _, l := range layers {
			lm := l.(map[string]any)
			if lm["key"] != layerKey {
				continue
			}
			for _, c := range lm["counts"].([]any) {
				cm := c.(map[string]any)
				if cm["key"] == countKey {
					return cm["value"].(float64)
				}
			}
		}
		t.Fatalf("layer %s/%s not found", layerKey, countKey)
		return -1
	}
	if got := findLayerCount(dashboard.LayerActionClicked, "service_opens"); got != 1 {
		t.Fatalf("service_opens = %v, want 1", got)
	}
	if got := findLayerCount(dashboard.LayerInvokeAttempt, "service_jump_attempts"); got != 1 {
		t.Fatalf("service_jump_attempts = %v, want 1", got)
	}

	// Store1 manager querying /api/v1/work/leads: BOTH items AND stats must exclude store2!
	status, rawWL, wl := f.do(t, "GET", "/api/v1/work/leads", "sess-sm-a", f.tenA, "")
	if status != http.StatusOK {
		t.Fatalf("work/leads manager = %d body=%s", status, rawWL)
	}
	items := wl["items"].([]any)
	if len(items) != 4 {
		t.Fatalf("store1 manager items = %d, want 4 (store2 leaked?): %s", len(items), rawWL)
	}
	wlStats := wl["stats"].(map[string]any)
	if wlStats["authorized_total"] != float64(4) ||
		wlStats["pending_sync"] != float64(1) ||
		wlStats["receiver_received"] != float64(1) ||
		wlStats["revoked"] != float64(2) {
		t.Fatalf("store1 manager work/leads stats leaked other store or miscounted waterline: %v", wlStats)
	}

	// Store1 manager lacks billing:view -> cost_reconciliation is forbidden, not fake 0
	status, _, smOps := f.do(t, "GET", "/api/v1/dashboard/operations?"+q, "sess-sm-a", f.tenA, "")
	if status != http.StatusOK {
		t.Fatalf("sm ops = %d", status)
	}
	smRecon := smOps["cost_reconciliation"].(map[string]any)
	if smRecon["availability"] != "forbidden" || len(smRecon) != 2 {
		t.Fatalf("sm without billing:view must get forbidden recon only: %v", smRecon)
	}
	// Store1 manager escaping to store2 -> 403
	if stEsc, _, _ := f.do(t, "GET", "/api/v1/dashboard/operations?"+q+"&store_id="+store2.ID, "sess-sm-a", f.tenA, ""); stEsc != http.StatusForbidden {
		t.Fatalf("manager store escape = %d, want 403", stEsc)
	}
	// Deformed store_manager (empty store_scope) -> fail-closed 403 on both operations and work/leads
	if _, err := f.s.St.CreateMemberScoped(f.tenA, "usr_sm_bad", "store_manager", "bad", "test", true, ""); err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"/api/v1/dashboard/operations?" + q, "/api/v1/work/leads"} {
		if stBad, _, _ := f.do(t, "GET", route, "sess-sm-bad", f.tenA, ""); stBad != http.StatusForbidden {
			t.Fatalf("deformed manager on %s = %d, want 403", route, stBad)
		}
	}
}

// ---- HUI-4662: Active Inquiry Minimal Fields, Honest States, Waterline & Zero PII Leak ----

func TestHUI4662MinimalFieldsNoticeBindingAndHonestStates(t *testing.T) {
	f := newLeadsFixture(t)
	storeCode, storeID := storeLane(t, f)
	// Pin a custom notice version "v2" on the store form to verify GET->POST version binding
	if _, err := f.s.St.UpsertLeadFormForStore(f.tenA, storeID, "v2", false, "test"); err != nil {
		t.Fatalf("upsert store form v2: %v", err)
	}

	// 1. GET lead-form discloses optional name, required phone, purpose, recipient, notice v2
	stForm, rawForm, form := f.guest(t, "GET", "/api/v1/public/storefronts/"+storeCode+"/lead-form", "")
	if stForm != http.StatusOK || form["enabled"] != true {
		t.Fatalf("store lead-form = %d %s", stForm, rawForm)
	}
	notice := form["notice"].(map[string]any)
	if notice["version"] != "v2" {
		t.Fatalf("form notice version = %v, want v2", notice)
	}
	if form["marketing_optin_enabled"] != false || form["marketing_blocks_browse"] != false {
		t.Fatalf("marketing flags wrong: %v", form)
	}

	// 2. Active inquiry with EMPTY name (optional addressing), phone only, marketing_optin=false, consent_version=v2
	//    Also verify wrong version ("v1") is rejected while pinned "v2" succeeds.
	phone := "13912345678"
	wechat := "wx_secret_handle_99"
	stBadVer, _, badVer := f.guest(t, "POST", "/api/v1/public/storefronts/"+storeCode+"/lead-submissions",
		fmt.Sprintf(`{"name":"","phone":%q,"wechat":%q,"consent":true,"consent_version":"v1","marketing_optin":false}`, phone, wechat))
	if stBadVer != http.StatusBadRequest || badVer["error"] != "notice_version_mismatch" {
		t.Fatalf("wrong version against v2 form = %d %v, want 400 notice_version_mismatch", stBadVer, badVer)
	}

	// Oversized wechat is refused cleanly (400)
	longWechat := strings.Repeat("w", 101)
	stLongWx, _, _ := f.guest(t, "POST", "/api/v1/public/storefronts/"+storeCode+"/lead-submissions",
		fmt.Sprintf(`{"name":"","phone":%q,"wechat":%q,"consent":true,"consent_version":"v2","marketing_optin":false}`, phone, longWechat))
	if stLongWx != http.StatusBadRequest {
		t.Fatalf("oversized wechat = %d, want 400", stLongWx)
	}

	// Valid minimal submit: empty name, phone + optional wechat, service consent only
	stSub, hdrSub, sub := f.guest(t, "POST", "/api/v1/public/storefronts/"+storeCode+"/lead-submissions",
		fmt.Sprintf(`{"name":"","phone":"+86 139-1234-5678","wechat":%q,"consent":true,"consent_version":"v2","marketing_optin":true}`, wechat))
	rawSub := mustJSONString(t, sub) + mustJSONString(t, hdrSub)
	if stSub != http.StatusCreated {
		t.Fatalf("minimal store submit = %d %s", stSub, rawSub)
	}
	ref := sub["submission_ref"].(string)
	// Honest local acceptance: NOT crm_received yet, sales/owner unknown, ZERO PII in public response
	if sub["state"] != leads.StateAccepted || sub["crm_received"] != false ||
		sub["sales_received"] != "unknown" || sub["owner_followed_up"] != "unknown" {
		t.Fatalf("honest initial state broken: %v", sub)
	}
	if strings.Contains(rawSub, phone) || strings.Contains(rawSub, wechat) {
		t.Fatalf("public submit response leaked PII: %s", rawSub)
	}
	// Marketing opt-in disabled on form -> stored marketing_optin must be false
	storedLead, err := f.s.St.GetLeadSubmissionByRef(ref)
	if err != nil || storedLead.Name != "" || storedLead.Phone != phone || storedLead.Wechat != wechat || storedLead.MarketingOptin {
		t.Fatalf("stored minimal lead mismatch: %+v err=%v", storedLead, err)
	}

	// 3. Forward to notify AND mark notify delivered -> MUST STAY pending_sync (NEVER crm_received!)
	f.forwarder.Tick(context.Background())
	f.stub.DeliverAll()
	f.forwarder.Tick(context.Background())

	stStatus, hdrStatus, statusOut := f.guest(t, "POST", "/api/v1/public/storefronts/"+storeCode+"/lead-status",
		fmt.Sprintf(`{"submission_ref":%q,"phone":%q}`, ref, phone))
	rawStatus := mustJSONString(t, statusOut) + mustJSONString(t, hdrStatus)
	if stStatus != http.StatusOK {
		t.Fatalf("lead-status = %d %s", stStatus, rawStatus)
	}
	if statusOut["state"] != leads.StatePendingSync || statusOut["crm_received"] != false || statusOut["receiver_state"] != "" {
		t.Fatalf("pending_sync/notify-delivered must NEVER claim crm_received: %v", statusOut)
	}
	if strings.Contains(rawStatus, phone) || strings.Contains(rawStatus, wechat) {
		t.Fatalf("public lead-status leaked PII: %s", rawStatus)
	}

	// GET on lead-status must be refused (405) so phone is never placed in URL query strings
	stGetStatus, _, _ := f.guest(t, "GET", "/api/v1/public/storefronts/"+storeCode+"/lead-status?phone="+phone, "")
	if stGetStatus != http.StatusMethodNotAllowed {
		t.Fatalf("GET lead-status = %d, want 405", stGetStatus)
	}

	// Outbox payload & audit rows must contain zero phone/wechat PII
	outboxPayload, err := f.s.St.LeadOutboxPayload(ref, "submit")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(outboxPayload, phone) || strings.Contains(outboxPayload, wechat) {
		t.Fatalf("outbox payload leaked PII: %s", outboxPayload)
	}
	auditRows, err := f.s.St.ListLeadAudit(f.tenA, ref)
	if err != nil {
		t.Fatal(err)
	}
	auditJSON, _ := json.Marshal(auditRows)
	if strings.Contains(string(auditJSON), phone) || strings.Contains(string(auditJSON), wechat) {
		t.Fatalf("audit rows leaked PII: %s", string(auditJSON))
	}

	// 4. Trusted receiver persisted ACK ends sync honestly
	ackCode, ackOut := f.ackRecordV2(t, ref, leadsReceiverID, leadsReceiverToken, f.tenA, ackBody(ref))
	if ackCode != http.StatusCreated || ackOut["receiver_state"] != store.ReceiverStateStored {
		t.Fatalf("stored ack = %d %v", ackCode, ackOut)
	}
	stAfterAck, _, statusAfterAck := f.guest(t, "POST", "/api/v1/public/storefronts/"+storeCode+"/lead-status",
		fmt.Sprintf(`{"submission_ref":%q,"phone":%q}`, ref, phone))
	if stAfterAck != http.StatusOK || statusAfterAck["state"] != leads.StateCRMReceived || statusAfterAck["crm_received"] != true {
		t.Fatalf("post-ACK status wrong: %d %v", stAfterAck, statusAfterAck)
	}
}

func TestHUI4662RevocationWaterlineOutOfOrderAndReplayGuards(t *testing.T) {
	f := newLeadsFixture(t)
	phone := "13788889999"
	wechat := "wx_revoke_test"

	// ---- Case A: Revoke BEFORE delivery (suppressed, zero network, replay never resurrects) ----
	st1, sub1 := f.submitLead(t, f.code, fmt.Sprintf(`{"name":"","phone":%q,"wechat":%q,"consent_version":"v1","consent":true,"marketing_optin":true}`, phone, wechat))
	if st1 != http.StatusCreated {
		t.Fatalf("submit1 = %d %v", st1, sub1)
	}
	ref1 := sub1["submission_ref"].(string)

	stRev1, hdrRev1, rev1 := f.guest(t, "POST", "/api/v1/public/links/"+f.code+"/lead-revocations",
		fmt.Sprintf(`{"submission_ref":%q,"phone":%q}`, ref1, phone))
	rawRev1 := mustJSONString(t, rev1) + mustJSONString(t, hdrRev1)
	if stRev1 != http.StatusOK || rev1["state"] != leads.StateRevoked {
		t.Fatalf("revoke before send = %d %s", stRev1, rawRev1)
	}
	if strings.Contains(rawRev1, phone) || strings.Contains(rawRev1, wechat) {
		t.Fatalf("revoke response leaked PII: %s", rawRev1)
	}

	// Forwarder tick must send ZERO events for ref1
	f.forwarder.Tick(context.Background())
	if len(f.stub.events) != 0 {
		t.Fatalf("revoked-before-send lead must produce zero network events, got %d", len(f.stub.events))
	}
	// Same-day resubmission of same phone returns existing revoked submission (no resurrection)
	stReplay, replayOut := f.submitLead(t, f.code, fmt.Sprintf(`{"name":"新称呼","phone":%q,"consent_version":"v1","consent":true,"marketing_optin":true}`, phone))
	if stReplay != http.StatusOK || replayOut["duplicate"] != true || replayOut["submission_ref"] != ref1 || replayOut["state"] != leads.StateRevoked {
		t.Fatalf("replay resurrected revoked lead: %d %v", stReplay, replayOut)
	}
	// Receiver fetch returns tombstone with ZERO contact fields
	tombCode, tombBody := f.fetchRecordV2(t, ref1, leadsReceiverID, leadsReceiverToken, f.tenA)
	if tombCode != http.StatusOK || strings.Contains(tombBody, phone) || strings.Contains(tombBody, wechat) {
		t.Fatalf("tombstone leaked contact or failed: code=%d body=%s", tombCode, tombBody)
	}

	// ---- Case B: In-flight revocation + late stored ACK + stale-revision revoked ACK + valid waterline ACK ----
	phone2 := "13677776666"
	st2, sub2 := f.submitLead(t, f.code, fmt.Sprintf(`{"name":"","phone":%q,"consent_version":"v1","consent":true,"marketing_optin":true}`, phone2))
	if st2 != http.StatusCreated {
		t.Fatalf("submit2 = %d %v", st2, sub2)
	}
	ref2 := sub2["submission_ref"].(string)
	f.forwarder.Tick(context.Background()) // submit forwarded (rev 1)

	// Consumer revokes while in flight -> waterline = 2
	stRev2, _, rev2 := f.guest(t, "POST", "/api/v1/public/links/"+f.code+"/lead-revocations",
		fmt.Sprintf(`{"submission_ref":%q,"phone":%q}`, ref2, phone2))
	if stRev2 != http.StatusOK || rev2["state"] != leads.StateRevoked || rev2["revocation_pending"] != true {
		t.Fatalf("in-flight revoke = %d %v", stRev2, rev2)
	}
	f.forwarder.Tick(context.Background()) // revoke event forwarded (rev 2)

	// 1) Late stored ACK with quotes in receipt_id -> 409 revocation_waterline + valid audit JSON
	trickyReceipt := `rcp_late_"quote"\1`
	lateStored, _ := json.Marshal(map[string]any{
		"receipt_id":      trickyReceipt,
		"source_event_id": ref2,
		"submission_id":   ref2,
		"source_revision": 1,
		"receiver_state":  store.ReceiverStateStored,
	})
	codeLate, outLate := f.ackRecordV2(t, ref2, leadsReceiverID, leadsReceiverToken, f.tenA, string(lateStored))
	if codeLate != http.StatusConflict || outLate["error"] != "revocation_waterline" {
		t.Fatalf("late stored ACK = %d %v, want 409 revocation_waterline", codeLate, outLate)
	}
	// Even a stored ACK claiming source_revision=2 cannot resurrect a revoked lead
	rev2Stored, _ := json.Marshal(map[string]any{
		"receipt_id":      "rcp_stored_rev2",
		"source_event_id": ref2,
		"submission_id":   ref2,
		"source_revision": 2,
		"receiver_state":  store.ReceiverStateStored,
	})
	codeStored2, outStored2 := f.ackRecordV2(t, ref2, leadsReceiverID, leadsReceiverToken, f.tenA, string(rev2Stored))
	if codeStored2 != http.StatusConflict || outStored2["error"] != "revocation_waterline" {
		t.Fatalf("stored ACK at rev 2 = %d %v, want 409 revocation_waterline", codeStored2, outStored2)
	}

	// 2) Stale revoked ACK below waterline (source_revision=1 < 2) MUST be refused (revocation stays pending)
	staleRevoked, _ := json.Marshal(map[string]any{
		"receipt_id":      "rcp_revoked_stale_rev1",
		"source_event_id": ref2,
		"submission_id":   ref2,
		"source_revision": 1,
		"receiver_state":  store.ReceiverStateRevoked,
	})
	codeStaleRev, outStaleRev := f.ackRecordV2(t, ref2, leadsReceiverID, leadsReceiverToken, f.tenA, string(staleRevoked))
	if codeStaleRev != http.StatusConflict || outStaleRev["error"] != "revocation_waterline" {
		t.Fatalf("below-waterline revoked ACK = %d %v, want 409 revocation_waterline", codeStaleRev, outStaleRev)
	}
	_, _, stillPending := f.guest(t, "POST", "/api/v1/public/links/"+f.code+"/lead-status",
		fmt.Sprintf(`{"submission_ref":%q,"phone":%q}`, ref2, phone2))
	if stillPending["revocation_pending"] != true {
		t.Fatalf("stale revoked ACK falsely cleared revocation_pending: %v", stillPending)
	}

	// 3) Valid waterline revoked ACK (source_revision=2) converges receiver_state=revoked and clears pending
	validRevoked, _ := json.Marshal(map[string]any{
		"receipt_id":      `rcp_revoked_"valid"\2`,
		"source_event_id": ref2 + "-revoke",
		"submission_id":   ref2,
		"source_revision": 2,
		"receiver_state":  store.ReceiverStateRevoked,
	})
	codeConv, outConv := f.ackRecordV2(t, ref2, leadsReceiverID, leadsReceiverToken, f.tenA, string(validRevoked))
	if codeConv != http.StatusCreated || outConv["receiver_state"] != store.ReceiverStateRevoked {
		t.Fatalf("waterline revoked ACK = %d %v, want 201", codeConv, outConv)
	}
	_, _, converged := f.guest(t, "POST", "/api/v1/public/links/"+f.code+"/lead-status",
		fmt.Sprintf(`{"submission_ref":%q,"phone":%q}`, ref2, phone2))
	if converged["state"] != leads.StateRevoked || converged["receiver_state"] != store.ReceiverStateRevoked || converged["revocation_pending"] != false {
		t.Fatalf("post-convergence status wrong: %v", converged)
	}

	// 4) All audit rows for ref2 must carry valid JSON details (no broken escaping) and zero PII
	audits, err := f.s.St.ListLeadAudit(f.tenA, ref2)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range audits {
		if !json.Valid([]byte(row["detail"])) {
			t.Fatalf("invalid audit JSON for action %s: %s", row["action"], row["detail"])
		}
		if strings.Contains(row["detail"], phone2) {
			t.Fatalf("audit detail leaked phone: %s", row["detail"])
		}
	}
}

func TestHUI4662ConcurrentPublicLeadWritesAreRaceFree(t *testing.T) {
	f := newLeadsFixture(t)
	f.s.LeadsLimiter = leads.NewRateLimiter(100, time.Minute)

	const workers = 12
	var wg sync.WaitGroup
	errs := make(chan string, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			phone := fmt.Sprintf("1350000%04d", idx)
			body := fmt.Sprintf(`{"name":"","phone":%q,"consent_version":"v1","consent":true,"marketing_optin":false}`, phone)
			st, out := f.submitLead(t, f.code, body)
			if st != http.StatusCreated && st != http.StatusOK {
				errs <- fmt.Sprintf("worker %d submit=%d %v", idx, st, out)
				return
			}
			ref, _ := out["submission_ref"].(string)
			revBody := fmt.Sprintf(`{"submission_ref":%q,"phone":%q}`, ref, phone)
			stRev, _, revOut := f.guest(t, "POST", "/api/v1/public/links/"+f.code+"/lead-revocations", revBody)
			if stRev != http.StatusOK || revOut["state"] != leads.StateRevoked {
				errs <- fmt.Sprintf("worker %d revoke=%d %v", idx, stRev, revOut)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Fatal(msg)
	}
}
