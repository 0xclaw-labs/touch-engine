package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
	"github.com/bianjiefilm/touch-engine/server/internal/participation"
	"github.com/bianjiefilm/touch-engine/server/internal/publishflow"
	"github.com/bianjiefilm/touch-engine/server/internal/publishhandoff"
	"github.com/bianjiefilm/touch-engine/server/internal/publishhandoff/adapters"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

// hui4660UnknownSubmitAdapter simulates a platform adapter whose Submit call
// encounters a network timeout (ResultUnknownError -> query_original) and whose
// Query endpoint checks the existing attempt without creating a duplicate.
type hui4660UnknownSubmitAdapter struct {
	platform    string
	submitCalls int
	queryCalls  int
}

func (a *hui4660UnknownSubmitAdapter) Platform() string { return a.platform }
func (a *hui4660UnknownSubmitAdapter) Prepare(_ context.Context, req publishhandoff.PrepareRequest) (publishhandoff.PrepareResult, error) {
	return publishhandoff.PrepareResult{
		ClientAction: publishhandoff.ClientAction{
			Type: publishhandoff.ClientActionOAuthConsent,
			Params: map[string]string{
				"phase":       "confirm_submit",
				"account_ref": "verified_ks_openid_4660",
				"client_key":  "ck_secret_should_not_leak",
				"scope":       "user_video_publish",
			},
			Note: "OAuth 授权就绪，client_key=ck_secret_should_not_leak，scope=user_video_publish，请确认提交。",
		},
		UnsupportedFields: req.Plan.UnsupportedFields,
	}, nil
}
func (a *hui4660UnknownSubmitAdapter) Submit(_ context.Context, _ publishhandoff.Handoff, _ publishhandoff.PrepareResult) error {
	a.submitCalls++
	return &adapters.ResultUnknownError{Reason: "upstream network timeout"}
}
func (a *hui4660UnknownSubmitAdapter) Query(_ context.Context, _ publishhandoff.Handoff) (publishhandoff.QueryOutcome, error) {
	a.queryCalls++
	return publishhandoff.QueryOutcome{State: "processing", Detail: map[string]string{"checked": "yes"}}, nil
}
func (a *hui4660UnknownSubmitAdapter) VerifyCallback(_ context.Context, _ publishhandoff.CallbackPayload) (publishhandoff.Receipt, error) {
	return publishhandoff.Receipt{}, publishhandoff.ErrUnsupported
}

// TestHUI4660_NextStepProjectionDiagnosticSanitizationAndCampaignlessNoParticipation
// exercises real HTTP /handoffs/plan and /handoffs endpoints across official_handoff,
// pending_external, and refused routes, asserting:
//   - Single clear primary_action + sanitized customer copy (no client_key/scope/OAuth leaks)
//   - manual_only / official_handoff never claim "发布完成"
//   - Single-asset session sets omit_asset_picker=true
//   - Restricted routes never allow manual reward fallback
//   - Campaign-less store handoff creates ZERO participation rows and reserves ZERO inventory
func TestHUI4660_NextStepProjectionDiagnosticSanitizationAndCampaignlessNoParticipation(t *testing.T) {
	f := newHandoffV2Fixture(t, true)

	// 1) official_handoff plan preview (A07 media, single asset).
	status, _, planBody := f.do(t, "GET",
		"/api/v1/public/actions/"+f.actA07+"/handoffs/plan?method=js_media_handoff&environment=ios_safari&payload_kind=media&action_session_id="+f.sidB,
		f.cookie, "")
	if status != http.StatusOK {
		t.Fatalf("plan A07 = %d (%v)", status, planBody)
	}
	ns, ok := planBody["next_step"].(map[string]any)
	if !ok {
		t.Fatalf("missing next_step on plan response: %v", planBody)
	}
	if ns["omit_asset_picker"] != true {
		t.Fatalf("single-asset session must set omit_asset_picker=true, got %v", ns["omit_asset_picker"])
	}
	if ns["show_free_text_account_input"] != false || ns["free_text_account_trusted"] != false {
		t.Fatalf("free-text account must never be shown/trusted: %v", ns)
	}
	primary := ns["primary_action"].(map[string]any)
	if primary["kind"] != string(publishflow.ActionConfirmAndOpenPlatform) || primary["primary"] != true {
		t.Fatalf("unexpected primary action: %v", primary)
	}
	rawNS, _ := json.Marshal(ns)
	for _, forbidden := range []string{"发布完成", "client_key", "scope", "OAuth", "adapter"} {
		if strings.Contains(string(rawNS), forbidden) {
			t.Fatalf("next_step leaked %q: %s", forbidden, string(rawNS))
		}
	}

	// 2) pending_external (unregistered method) must refuse manual reward fallback.
	status, _, pendBody := f.do(t, "GET",
		"/api/v1/public/actions/"+f.actA07+"/handoffs/plan?method=h5_share&environment=ios_safari&action_session_id="+f.sidB,
		f.cookie, "")
	if status != http.StatusOK {
		t.Fatalf("pending plan = %d (%v)", status, pendBody)
	}
	pendNS := pendBody["next_step"].(map[string]any)
	if pendNS["plan_kind"] != string(publishflow.PlanPendingExternal) || pendNS["manual_reward_fallback_allowed"] != false {
		t.Fatalf("pending_external next_step wrong: %v", pendNS)
	}
	if pendNS["primary_action"].(map[string]any)["kind"] != string(publishflow.ActionReturnToStore) {
		t.Fatalf("pending_external primary must be return_to_store, got %v", pendNS["primary_action"])
	}

	// 3) Start campaign-less store handoff (A27 link_share): ZERO participations created, facts enforce no inventory reservation.
	status, _, linkPlan := f.do(t, "GET",
		"/api/v1/public/actions/"+f.actA27+"/handoffs/plan?method=link_share&environment=ios_safari&payload_kind=link_share&action_session_id="+f.sidA,
		f.cookie, "")
	if status != http.StatusOK {
		t.Fatalf("link plan = %d (%v)", status, linkPlan)
	}
	conf, _ := linkPlan["confirmation_required"].(string)
	startBody := `{"action_session_id":"` + f.sidA + `","action_revision":1,"payload_kind":"link_share","method":"link_share","environment":"ios_safari","confirmation":"` + conf + `","idempotency_key":"idem-4660-nocamp"}`
	status, _, started := f.do(t, "POST", "/api/v1/public/actions/"+f.actA27+"/handoffs", f.cookie, startBody)
	if status != http.StatusCreated {
		t.Fatalf("start A27 = %d (%v)", status, started)
	}
	facts := started["facts"].(map[string]any)
	if facts["must_not_create_participation"] != true || facts["must_not_reserve_inventory"] != true {
		t.Fatalf("campaign-less store handoff must forbid participation/inventory: %v", facts)
	}
	rewardFact := facts["reward_fact"].(map[string]any)
	if rewardFact["status"] != string(publishflow.RewardFactNone) || rewardFact["campaign_attached"] != false {
		t.Fatalf("campaign-less store handoff reward_fact must be none: %v", rewardFact)
	}
	var partCount int
	if err := f.d.QueryRow(`SELECT COUNT(1) FROM participations`).Scan(&partCount); err != nil {
		t.Fatal(err)
	}
	if partCount != 0 {
		t.Fatalf("campaign-less store handoff created %d participations, want 0", partCount)
	}
}

// TestHUI4660_ContentChangeInvalidatesPriorConfirmation verifies AC4 over real
// HTTP + SQLite: switching session content invalidates the prior confirmation
// token (422 confirmation_mismatch, zero row created) until fresh confirmation.
func TestHUI4660_ContentChangeInvalidatesPriorConfirmation(t *testing.T) {
	f := newHandoffV2Fixture(t, true)

	// Create and adopt a second content version for A07, and wire both into session candidates.
	var packID, videoID, coverID string
	if err := f.d.QueryRow(`SELECT id FROM store_content_packs WHERE tenant_id=? LIMIT 1`, f.tenA).Scan(&packID); err != nil {
		t.Fatal(err)
	}
	v2Media, _, err := f.s.St.RegisterContentMedia(f.tenA, f.stoA, upload.MediaMeta{
		SHA256: strings.Repeat("c", 64), MIME: "video/mp4", Kind: upload.KindVideo,
		ByteSize: 2048, DurationMS: 2000, Width: 64, Height: 64, MediaRef: strings.Repeat("c", 64),
	}, "v2_alt.mp4", "usr_owner_a")
	if err != nil {
		t.Fatal(err)
	}
	videoID = v2Media.ID
	cover2, _, err := f.s.St.RegisterContentMedia(f.tenA, f.stoA, upload.MediaMeta{
		SHA256: strings.Repeat("d", 64), MIME: "image/jpeg", Kind: upload.KindImage,
		ByteSize: 1024, Width: 64, Height: 64, MediaRef: strings.Repeat("d", 64),
	}, "v2_alt.jpg", "usr_owner_a")
	if err != nil {
		t.Fatal(err)
	}
	coverID = cover2.ID
	ver2, err := f.s.St.CreateStoreContentPackVersion(f.tenA, packID, store.StoreVersionSpec{
		ContentKind: contentpack.ContentKindVideo, VideoMediaID: videoID, CoverMediaID: coverID,
		Title: "新品拿铁", Body: "第二杯半价", Topics: []string{"咖啡"}, StoreName: "旗舰店",
		PriceNote: "半价", ValidNote: "2026-12-31", GrantRefs: []string{"grant-2"}, Source: contentpack.SourceMerchantUpload,
	}, contentpack.StatusCandidate, "usr_owner_a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.St.AdoptStoreContentPackVersion(f.tenA, packID, ver2.ID, "xiaohongshu", "usr_owner_a", time.Now()); err != nil {
		t.Fatal(err)
	}

	// Capture confirmation token for version 1 first.
	status, _, planV1 := f.do(t, "GET",
		"/api/v1/public/actions/"+f.actA07+"/handoffs/plan?method=js_media_handoff&environment=ios_safari&payload_kind=media&action_session_id="+f.sidB,
		f.cookie, "")
	if status != http.StatusOK {
		t.Fatalf("plan v1 = %d (%v)", status, planV1)
	}
	confV1, _ := planV1["confirmation_required"].(string)
	if confV1 == "" {
		t.Fatalf("missing v1 confirmation: %v", planV1)
	}

	// Add ver2 to session's frozen candidate pool and switch content to ver2.
	var curVer string
	if err := f.d.QueryRow(`SELECT content_pack_version FROM action_sessions WHERE id=?`, f.sidB).Scan(&curVer); err != nil {
		t.Fatal(err)
	}
	candJSON, _ := json.Marshal([]string{curVer, ver2.ID})
	if _, err := f.d.Exec(`UPDATE action_sessions SET content_candidates=? WHERE id=?`, string(candJSON), f.sidB); err != nil {
		t.Fatal(err)
	}
	status, _, switched := f.do(t, "POST", "/api/v1/public/action-sessions/"+f.sidB+"/content", f.cookie,
		`{"content_pack_version":"`+ver2.ID+`"}`)
	if status != http.StatusOK {
		t.Fatalf("switch content = %d (%v)", status, switched)
	}

	// Stale v1 confirmation MUST be rejected (422 confirmation_mismatch).
	staleReq := `{"action_session_id":"` + f.sidB + `","action_revision":1,"payload_kind":"media","method":"js_media_handoff","environment":"ios_safari","confirmation":"` + confV1 + `","idempotency_key":"idem-4660-stale"}`
	status, _, staleResp := f.do(t, "POST", "/api/v1/public/actions/"+f.actA07+"/handoffs", f.cookie, staleReq)
	if status != http.StatusUnprocessableEntity || staleResp["error"] != "confirmation_mismatch" {
		t.Fatalf("stale confirmation must fail 422 confirmation_mismatch, got %d (%v)", status, staleResp)
	}

	// Multi-asset plan now keeps switch_asset reachable (omit_asset_picker=false) and issues fresh confirmation.
	status, _, planV2 := f.do(t, "GET",
		"/api/v1/public/actions/"+f.actA07+"/handoffs/plan?method=js_media_handoff&environment=ios_safari&payload_kind=media&action_session_id="+f.sidB,
		f.cookie, "")
	if status != http.StatusOK {
		t.Fatalf("plan v2 = %d (%v)", status, planV2)
	}
	if planV2["next_step"].(map[string]any)["omit_asset_picker"] != false {
		t.Fatalf("multi-asset session must keep asset picker reachable, got %v", planV2["next_step"])
	}
	confV2, _ := planV2["confirmation_required"].(string)
	if confV2 == "" || confV2 == confV1 {
		t.Fatalf("expected fresh confirmation different from v1, got %q vs %q", confV2, confV1)
	}

	// Fresh confirmation succeeds.
	freshReq := `{"action_session_id":"` + f.sidB + `","action_revision":1,"payload_kind":"media","method":"js_media_handoff","environment":"ios_safari","confirmation":"` + confV2 + `","idempotency_key":"idem-4660-fresh"}`
	status, _, freshResp := f.do(t, "POST", "/api/v1/public/actions/"+f.actA07+"/handoffs", f.cookie, freshReq)
	if status != http.StatusCreated {
		t.Fatalf("fresh confirmation start = %d (%v)", status, freshResp)
	}
}

// TestHUI4660_WeakFactsTerminalImmutabilityAndUnknownQueryBeforeRetry verifies AC5:
//   - Weak customer facts never escalate publish status to official_confirmed
//   - Network unknown submit moves to unknown, next_step forces query_status (must_query_before_retry=true),
//     blind re-submit is rejected (409 invalid_state), and query checks the same attempt without re-submitting
//   - Canceling reaches terminal state that cannot be overwritten by later operations
func TestHUI4660_WeakFactsTerminalImmutabilityAndUnknownQueryBeforeRetry(t *testing.T) {
	f := newHandoffV2Fixture(t, true)
	adapter := &hui4660UnknownSubmitAdapter{platform: "xiaohongshu"}
	f.s.PublishHandoff.SetAdapter(adapter)

	status, _, planBody := f.do(t, "GET",
		"/api/v1/public/actions/"+f.actA07+"/handoffs/plan?method=js_media_handoff&environment=ios_safari&payload_kind=media&action_session_id="+f.sidB,
		f.cookie, "")
	if status != http.StatusOK {
		t.Fatalf("plan = %d (%v)", status, planBody)
	}
	conf, _ := planBody["confirmation_required"].(string)
	startBody := `{"action_session_id":"` + f.sidB + `","action_revision":1,"payload_kind":"media","method":"js_media_handoff","environment":"ios_safari","confirmation":"` + conf + `","idempotency_key":"idem-4660-unknown"}`
	status, _, started := f.do(t, "POST", "/api/v1/public/actions/"+f.actA07+"/handoffs", f.cookie, startBody)
	if status != http.StatusCreated {
		t.Fatalf("start = %d (%v)", status, started)
	}
	hid := started["handoff"].(map[string]any)["handoff_id"].(string)

	// Verify customer next_step stripped adapter's client_key/scope/OAuth diagnostics.
	rawNS, _ := json.Marshal(started["next_step"])
	for _, leak := range []string{"ck_secret_should_not_leak", "client_key", "scope", "OAuth"} {
		if strings.Contains(string(rawNS), leak) {
			t.Fatalf("next_step leaked adapter diagnostic %q: %s", leak, string(rawNS))
		}
	}

	// Weak facts (click, wake, media_handoff, self_report) NEVER escalate to platform_confirmed.
	for i, kind := range []string{"customer_click", "wake_attempt", "media_handoff", "customer_self_report"} {
		body := `{"kind":"` + kind + `","source":"h5","event_id":"ev_4660_` + string(rune('a'+i)) + `"}`
		status, _, factResp := f.do(t, "POST", "/api/v1/public/handoffs/"+hid+"/facts", f.cookie, body)
		if status != http.StatusOK {
			t.Fatalf("fact %s = %d (%v)", kind, status, factResp)
		}
		if factResp["handoff"].(map[string]any)["status"] != "handoff_ready" {
			t.Fatalf("weak fact %s escalated status to %v", kind, factResp["handoff"])
		}
	}

	// Submit triggers ResultUnknownError -> status transitions to unknown; adapter Submit ran once.
	status, _, submitResp := f.do(t, "POST", "/api/v1/public/handoffs/"+hid+"/submit", f.cookie, "")
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("unknown submit = %d (%v)", status, submitResp)
	}
	if adapter.submitCalls != 1 {
		t.Fatalf("submitCalls = %d, want 1", adapter.submitCalls)
	}

	// GET handoff projects state=unknown, must_query_before_retry=true, primary=query_status.
	status, _, got := f.do(t, "GET", "/api/v1/public/handoffs/"+hid, f.cookie, "")
	if status != http.StatusOK {
		t.Fatalf("get unknown handoff = %d (%v)", status, got)
	}
	ns := got["next_step"].(map[string]any)
	if ns["state"] != string(publishflow.StateUnknown) || ns["must_query_before_retry"] != true ||
		ns["primary_action"].(map[string]any)["kind"] != string(publishflow.ActionQueryStatus) {
		t.Fatalf("unknown next_step must force query_status: %v", ns)
	}

	// Blind re-submit is rejected (409 invalid_state); Submit is NOT called a second time.
	status, _, retryResp := f.do(t, "POST", "/api/v1/public/handoffs/"+hid+"/submit", f.cookie, "")
	if status != http.StatusConflict || retryResp["error"] != "invalid_state" {
		t.Fatalf("blind retry on unknown must be refused 409 invalid_state, got %d (%v)", status, retryResp)
	}
	if adapter.submitCalls != 1 {
		t.Fatalf("blind retry duplicated submit: submitCalls=%d", adapter.submitCalls)
	}

	// Query checks the existing attempt (query_count=1).
	status, _, qResp := f.do(t, "POST", "/api/v1/public/handoffs/"+hid+"/query", f.cookie, "")
	if status != http.StatusOK || adapter.queryCalls != 1 || qResp["handoff"].(map[string]any)["query_count"] != float64(1) {
		t.Fatalf("query original attempt failed: %d (%v) queryCalls=%d", status, qResp, adapter.queryCalls)
	}

	// Cancel -> terminal state; subsequent query/submit refused; next_step reflects cancelled.
	status, _, cancelResp := f.do(t, "POST", "/api/v1/public/handoffs/"+hid+"/cancel", f.cookie, `{"reason":"customer cancelled"}`)
	if status != http.StatusOK || cancelResp["handoff"].(map[string]any)["status"] != "canceled" {
		t.Fatalf("cancel = %d (%v)", status, cancelResp)
	}
	status, _, postCancelQuery := f.do(t, "POST", "/api/v1/public/handoffs/"+hid+"/query", f.cookie, "")
	if status != http.StatusConflict {
		t.Fatalf("query after terminal cancel must be refused 409, got %d (%v)", status, postCancelQuery)
	}
	status, _, finalView := f.do(t, "GET", "/api/v1/public/handoffs/"+hid, f.cookie, "")
	if status != http.StatusOK || finalView["next_step"].(map[string]any)["state"] != string(publishflow.StateCancelled) ||
		finalView["facts"].(map[string]any)["publish_fact"].(map[string]any)["terminal"] != true {
		t.Fatalf("terminal cancelled view wrong: %d (%v)", status, finalView)
	}
}

// TestHUI4660_CrossBrowserResumeStaffVerifyBlockedAndTouchpointRebind verifies AC3, AC4, AC6:
//   - Cross-browser customer_resume restores exact original session, adopted content, and handoff
//   - Consumed customer_resume cannot be replayed (409 resume_used)
//   - staff_verify credential is strictly rejected on /api/v1/public/resume/exchange
//   - Credential-less caller cannot recover session/handoff (404 fail-closed)
//   - Action/touchpoint rebind or disablement preserves original session/handoff attribution
func TestHUI4660_CrossBrowserResumeStaffVerifyBlockedAndTouchpointRebind(t *testing.T) {
	f := newHandoffV2Fixture(t, true)

	// Start a handoff in browser A.
	status, _, planBody := f.do(t, "GET",
		"/api/v1/public/actions/"+f.actA07+"/handoffs/plan?method=js_media_handoff&environment=ios_safari&payload_kind=media&action_session_id="+f.sidB,
		f.cookie, "")
	if status != http.StatusOK {
		t.Fatalf("plan = %d (%v)", status, planBody)
	}
	conf, _ := planBody["confirmation_required"].(string)
	startBody := `{"action_session_id":"` + f.sidB + `","action_revision":1,"payload_kind":"media","method":"js_media_handoff","environment":"ios_safari","confirmation":"` + conf + `","idempotency_key":"idem-4660-resume"}`
	status, _, started := f.do(t, "POST", "/api/v1/public/actions/"+f.actA07+"/handoffs", f.cookie, startBody)
	if status != http.StatusCreated {
		t.Fatalf("start = %d (%v)", status, started)
	}
	hid := started["handoff"].(map[string]any)["handoff_id"].(string)

	// Issue a single-use customer_resume credential from browser A.
	status, _, cred := f.do(t, "POST", "/api/v1/public/action-sessions/"+f.sidB+"/resume-credentials", f.cookie, `{}`)
	if status != http.StatusCreated || cred["purpose"] != "customer_resume" {
		t.Fatalf("issue resume cred = %d (%v)", status, cred)
	}
	resumeToken := cred["token"].(string)

	// Browser B (no prior cookie) exchanges customer_resume via POST body.
	status, hdrB, exchanged := f.do(t, "POST", "/api/v1/public/resume/exchange", "", `{"token":"`+resumeToken+`"}`)
	if status != http.StatusOK || exchanged["action_session_id"] != f.sidB {
		t.Fatalf("exchange in browser B = %d (%v)", status, exchanged)
	}
	cookieB := cookiePair(hdrB)

	// Replaying the consumed customer_resume token fails 409 resume_used.
	status, _, replay := f.do(t, "POST", "/api/v1/public/resume/exchange", "", `{"token":"`+resumeToken+`"}`)
	if status != http.StatusConflict || replay["error"] != "resume_used" {
		t.Fatalf("replay resume token must fail 409 resume_used, got %d (%v)", status, replay)
	}

	// Browser B recovers the exact same original handoff via session lookup.
	status, _, resumedHandoff := f.do(t, "GET", "/api/v1/public/action-sessions/"+f.sidB+"/handoff", cookieB, "")
	if status != http.StatusOK || resumedHandoff["handoff"].(map[string]any)["handoff_id"] != hid {
		t.Fatalf("browser B did not restore original handoff %s: %d (%v)", hid, status, resumedHandoff)
	}

	// Credential-less caller cannot recover session or handoff (fail-closed 404).
	if status, _, _ := f.do(t, "GET", "/api/v1/public/handoffs/"+hid, "", ""); status != http.StatusNotFound {
		t.Fatalf("credential-less handoff read must 404, got %d", status)
	}
	if status, _, _ := f.do(t, "GET", "/api/v1/public/action-sessions/"+f.sidB+"/status", "", ""); status != http.StatusNotFound {
		t.Fatalf("credential-less session status must 404, got %d", status)
	}

	// staff_verify credential (minted via real participation BeginHandoff) MUST be rejected by /api/v1/public/resume/exchange.
	camp, err := f.s.St.CreateCampaign(store.NewCampaign{
		TenantID: f.tenA, StoreID: f.stoA, Title: "测试活动", CreatedBy: "usr_owner_a",
	})
	if err != nil {
		t.Fatal(err)
	}
	startedP, err := f.s.Participation.Start(context.Background(), participation.StartInput{
		TenantID: f.tenA, StoreID: f.stoA, CampaignID: camp.ID, LinkCode: "lnk4660",
		VisitorID: "vis_staff_owner", IdempotencyKey: "idem-pt-4660", ContentPackVersion: "cp_1", RuleVersion: 1,
		Source: participation.SourceNFC,
	})
	if err != nil {
		t.Fatal(err)
	}
	staffCred, err := f.s.Participation.BeginHandoff(context.Background(), participation.HandoffInput{
		ParticipationID: startedP.Participation.ID, VisitorID: "vis_staff_owner",
		Purpose: participation.PurposeStaffVerify,
	})
	if err != nil {
		t.Fatal(err)
	}
	status, _, staffReject := f.do(t, "POST", "/api/v1/public/resume/exchange", "", `{"token":"`+staffCred.Token+`"}`)
	if status != http.StatusNotFound {
		t.Fatalf("staff_verify token must be rejected on customer resume exchange (404), got %d (%v)", status, staffReject)
	}

	// Simulate touchpoint/action rebind to a different store/action in the live directory:
	// original session status and handoff history remain readable with original frozen attribution!
	f.s.Participation.SetActionDirectory(&stubV2Directory{refs: map[string]participation.ActionRef{
		f.actA07: {
			ActionID: f.actA07, CatalogCode: "A07", Platform: "xiaohongshu", Kind: "publish",
			TenantID: f.tenA, StoreID: "sto_rebound_other", StorefrontRevision: 9, ActionRevision: 9, Enabled: false,
		},
	}})
	status, _, postRebindStatus := f.do(t, "GET", "/api/v1/public/action-sessions/"+f.sidB+"/status", cookieB, "")
	if status != http.StatusOK || postRebindStatus["action_session_id"] != f.sidB || postRebindStatus["action_revision"] != float64(1) {
		t.Fatalf("original session frozen revision/attribution lost after rebind: %d (%v)", status, postRebindStatus)
	}
	status, _, postRebindHandoff := f.do(t, "GET", "/api/v1/public/handoffs/"+hid, cookieB, "")
	if status != http.StatusOK || postRebindHandoff["handoff"].(map[string]any)["handoff_id"] != hid {
		t.Fatalf("original handoff record must remain readable after rebind: %d (%v)", status, postRebindHandoff)
	}
}
