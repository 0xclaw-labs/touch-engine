package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// lead_record_test.go: HUI-4594 trusted leads-receiver seam. The contact leaves
// this process ONLY through GET /api/v1/internal/lead-records/{submissionId},
// authenticated by TOUCH_SERVICE_TOKENS + the lead_receiver role, with the
// ingesting tenant declared and verified. Revoked records are tombstones.
// The persisted ACK (five-tuple) is the only thing that ends the sync.

const (
	leadsReceiverID    = "leads-ingest"
	leadsReceiverToken = "test-leads-receiver-secret"
)

// fetchRecordV2 calls the trusted receiver fetch.
func (f *leadsFixture) fetchRecordV2(t *testing.T, ref, serviceID, token, tenant string) (int, string) {
	t.Helper()
	return f.serviceCall(t, http.MethodGet, "/api/v1/internal/lead-records/"+ref, serviceID, token, tenant, "")
}

// ackRecordV2 calls the persisted-ACK endpoint.
func (f *leadsFixture) ackRecordV2(t *testing.T, ref, serviceID, token, tenant, body string) (int, map[string]any) {
	t.Helper()
	code, raw := f.serviceCall(t, http.MethodPost, "/api/v1/internal/lead-records/"+ref+"/acknowledgements", serviceID, token, tenant, body)
	var out map[string]any
	_ = json.Unmarshal([]byte(raw), &out)
	return code, out
}

func (f *leadsFixture) serviceCall(t *testing.T, method, path, serviceID, token, tenant, body string) (int, string) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, f.ts.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if serviceID != "" {
		req.Header.Set("X-Service-ID", serviceID)
	}
	if token != "" {
		req.Header.Set("X-Service-Token", token)
	}
	if tenant != "" {
		req.Header.Set("X-Leads-Tenant", tenant)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(raw)
}

func ackBody(submissionID string) string {
	return fmt.Sprintf(`{"receipt_id":"rcp_v2_1","source_event_id":%q,"submission_id":%q,"source_revision":1,"receiver_state":"stored"}`, submissionID, submissionID)
}

func TestLeadRecordFetchAuthMatrix(t *testing.T) {
	f := newLeadsFixture(t)
	status, out := f.submitLead(t, f.code, validLeadBody)
	if status != 201 {
		t.Fatalf("submit = %d %v", status, out)
	}
	ref := out["submission_ref"].(string)

	t.Run("no service identity is a uniform 401", func(t *testing.T) {
		code, body := f.fetchRecordV2(t, ref, "", "", f.tenA)
		if code != http.StatusUnauthorized {
			t.Fatalf("status=%d body=%s", code, body)
		}
		if strings.Contains(body, "13800138000") {
			t.Fatalf("leak: %s", body)
		}
	})

	t.Run("wrong token is a uniform 401 without existence disclosure", func(t *testing.T) {
		code, _ := f.fetchRecordV2(t, ref, leadsReceiverID, "wrong-token", f.tenA)
		if code != http.StatusUnauthorized {
			t.Fatalf("status=%d", code)
		}
	})

	t.Run("registered service without the lead_receiver role is 403", func(t *testing.T) {
		code, _ := f.fetchRecordV2(t, ref, "other-svc", "other-svc-token", f.tenA)
		if code != http.StatusForbidden {
			t.Fatalf("status=%d", code)
		}
	})

	t.Run("foreign tenant is a uniform 404 that leaks nothing", func(t *testing.T) {
		code, body := f.fetchRecordV2(t, ref, leadsReceiverID, leadsReceiverToken, "tnt_other")
		if code != http.StatusNotFound {
			t.Fatalf("status=%d body=%s", code, body)
		}
		if strings.Contains(body, "13800138000") || strings.Contains(body, "张三") {
			t.Fatalf("mismatch leaked contact: %s", body)
		}
	})

	t.Run("old internal-token fetch route is retired", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, f.ts.URL+"/internal/v1/lead-records/"+ref, nil)
		req.Header.Set("X-Internal-Token", "test-internal-secret")
		req.Header.Set("X-Leads-Tenant", f.tenA)
		res, err := f.ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("legacy route must be gone, got %d", res.StatusCode)
		}
	})

	t.Run("authorized record: minimal fields, source/consent revision, purpose", func(t *testing.T) {
		code, body := f.fetchRecordV2(t, ref, leadsReceiverID, leadsReceiverToken, f.tenA)
		if code != http.StatusOK {
			t.Fatalf("status=%d body=%s", code, body)
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(body), &rec); err != nil {
			t.Fatal(err)
		}
		if rec["submission_ref"] != ref || rec["tenant_id"] != f.tenA {
			t.Fatalf("record=%v", rec)
		}
		if rec["name"] != "张三" || rec["phone"] != "13800138000" {
			t.Fatalf("record=%v", rec)
		}
		if rec["authorization"] != "granted" || rec["purpose"] != "lead_submission" {
			t.Fatalf("record=%v", rec)
		}
		if rec["source_revision"] != float64(1) || rec["consent_revision"] != "v1" {
			t.Fatalf("revisions missing: %v", rec)
		}
		consents, _ := rec["consents"].(map[string]any)
		if consents["form_authorized"] != true || consents["marketing_phone"] != true || consents["notice_version"] != "v1" {
			t.Fatalf("consents=%v", consents)
		}
		// internal-only fields never travel
		for _, forbidden := range []string{"sync_error", "consent_ip", "dedup_key", "attempts", "receipt_id"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("record exposed internal field %q: %s", forbidden, body)
			}
		}
	})

	t.Run("campaign provenance is a reference, never contact data", func(t *testing.T) {
		_, body := f.fetchRecordV2(t, ref, leadsReceiverID, leadsReceiverToken, f.tenA)
		if !strings.Contains(body, `"campaign_ref"`) {
			t.Fatalf("campaign reference missing: %s", body)
		}
	})
}

func TestLeadRecordRevokedIsTombstone(t *testing.T) {
	f := newLeadsFixture(t)
	_, out := f.submitLead(t, f.code, validLeadBody)
	ref := out["submission_ref"].(string)
	if status, _, _ := f.guest(t, "POST", "/api/v1/public/links/"+f.code+"/lead-revocations",
		fmt.Sprintf(`{"submission_ref":%q,"phone":"13800138000"}`, ref)); status != 200 {
		t.Fatal("revoke failed")
	}

	code, body := f.fetchRecordV2(t, ref, leadsReceiverID, leadsReceiverToken, f.tenA)
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, body)
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(body), &rec); err != nil {
		t.Fatal(err)
	}
	if rec["authorization"] != "revoked" || rec["tombstone"] != true {
		t.Fatalf("record=%v", rec)
	}
	for _, contact := range []string{"13800138000", "张三", `"name"`, `"phone"`, `"wechat"`} {
		if strings.Contains(body, contact) {
			t.Fatalf("tombstone carries contact %q: %s", contact, body)
		}
	}
	// the waterline revision is served so the receiver can converge monotonically
	lead, err := f.s.St.GetLeadSubmissionByRef(ref)
	if err != nil {
		t.Fatal(err)
	}
	if rec["source_revision"] != float64(lead.RevokeSourceRevision) {
		t.Fatalf("tombstone revision %v want %d", rec["source_revision"], lead.RevokeSourceRevision)
	}
	consents, _ := rec["consents"].(map[string]any)
	if consents["marketing_phone"] != false || consents["form_authorized"] != false {
		t.Fatalf("tombstone consents=%v", consents)
	}
}

func TestLeadAckEndsSyncAndReplaysIdempotently(t *testing.T) {
	f := newLeadsFixture(t)
	_, out := f.submitLead(t, f.code, validLeadBody)
	ref := out["submission_ref"].(string)
	f.forwarder.Tick(t.Context()) // publish -> pending_sync

	t.Run("happy path ends the sync", func(t *testing.T) {
		code, resp := f.ackRecordV2(t, ref, leadsReceiverID, leadsReceiverToken, f.tenA, ackBody(ref))
		if code != http.StatusCreated {
			t.Fatalf("ack = %d %v", code, resp)
		}
		lead, err := f.s.St.GetLeadSubmissionByRef(ref)
		if err != nil || lead.SyncState != "crm_received" || lead.ReceiverState != "stored" {
			t.Fatalf("lead=%+v err=%v", lead, err)
		}
	})

	t.Run("identical replay is idempotent", func(t *testing.T) {
		code, resp := f.ackRecordV2(t, ref, leadsReceiverID, leadsReceiverToken, f.tenA, ackBody(ref))
		if code != http.StatusOK || resp["duplicate"] != true {
			t.Fatalf("replay = %d %v", code, resp)
		}
	})

	t.Run("same receipt id different payload conflicts", func(t *testing.T) {
		body := strings.Replace(ackBody(ref), `"source_revision":1`, `"source_revision":9`, 1)
		code, resp := f.ackRecordV2(t, ref, leadsReceiverID, leadsReceiverToken, f.tenA, body)
		if code != http.StatusConflict || resp["error"] != "idempotency_conflict" {
			t.Fatalf("conflict = %d %v", code, resp)
		}
	})
}

func TestLeadAckNegativeMatrix(t *testing.T) {
	f := newLeadsFixture(t)
	_, out := f.submitLead(t, f.code, validLeadBody)
	ref := out["submission_ref"].(string)
	f.forwarder.Tick(t.Context())

	assertStillPending := func(t *testing.T) {
		t.Helper()
		lead, err := f.s.St.GetLeadSubmissionByRef(ref)
		if err != nil || lead.SyncState != "pending_sync" || lead.ReceiverState != "" {
			t.Fatalf("a refused ack mutated the lead: %+v err=%v", lead, err)
		}
	}

	t.Run("incomplete five-tuple is 400 and never ends sync", func(t *testing.T) {
		for name, body := range map[string]string{
			"missing receipt_id":      `{"source_event_id":"x","submission_id":"y","source_revision":1,"receiver_state":"stored"}`,
			"missing source_event_id": `{"receipt_id":"r","submission_id":"y","source_revision":1,"receiver_state":"stored"}`,
			"missing submission_id":   `{"receipt_id":"r","source_event_id":"x","source_revision":1,"receiver_state":"stored"}`,
			"missing revision":        `{"receipt_id":"r","source_event_id":"x","submission_id":"y","receiver_state":"stored"}`,
			"missing receiver_state":  `{"receipt_id":"r","source_event_id":"x","submission_id":"y","source_revision":1}`,
			"unknown receiver_state":  `{"receipt_id":"r","source_event_id":"x","submission_id":"y","source_revision":1,"receiver_state":"maybe"}`,
			"submission mismatch":     `{"receipt_id":"r","source_event_id":"x","submission_id":"other","source_revision":1,"receiver_state":"stored"}`,
			"unknown field":           `{"receipt_id":"r","source_event_id":"x","submission_id":"y","source_revision":1,"receiver_state":"stored","tenant_id":"tnt_evil"}`,
		} {
			code, resp := f.ackRecordV2(t, ref, leadsReceiverID, leadsReceiverToken, f.tenA, body)
			if code != http.StatusBadRequest {
				t.Fatalf("%s = %d %v, want 400", name, code, resp)
			}
		}
		assertStillPending(t)
	})

	t.Run("fabricated source event is 409", func(t *testing.T) {
		body := strings.Replace(ackBody(ref), `"source_event_id":"`+ref+`"`, `"source_event_id":"ev-fabricated"`, 1)
		code, resp := f.ackRecordV2(t, ref, leadsReceiverID, leadsReceiverToken, f.tenA, body)
		if code != http.StatusConflict || resp["error"] != "unknown_source_event" {
			t.Fatalf("fabricated = %d %v", code, resp)
		}
		assertStillPending(t)
	})

	t.Run("foreign tenant is 404", func(t *testing.T) {
		code, _ := f.ackRecordV2(t, ref, leadsReceiverID, leadsReceiverToken, "tnt_other", ackBody(ref))
		if code != http.StatusNotFound {
			t.Fatalf("status=%d", code)
		}
	})

	t.Run("unauthenticated ack never acts", func(t *testing.T) {
		code, _ := f.ackRecordV2(t, ref, leadsReceiverID, "wrong", f.tenA, ackBody(ref))
		if code != http.StatusUnauthorized {
			t.Fatalf("status=%d", code)
		}
		assertStillPending(t)
	})
}

func TestLeadAckWaterlineBlocksLateReplayResurrection(t *testing.T) {
	f := newLeadsFixture(t)
	_, out := f.submitLead(t, f.code, validLeadBody)
	ref := out["submission_ref"].(string)
	f.forwarder.Tick(t.Context())
	// the consumer revokes while the fact is already in flight
	if status, _, _ := f.guest(t, "POST", "/api/v1/public/links/"+f.code+"/lead-revocations",
		fmt.Sprintf(`{"submission_ref":%q,"phone":"13800138000"}`, ref)); status != 200 {
		t.Fatal("revoke failed")
	}
	f.forwarder.Tick(t.Context()) // the revoke fact leaves

	t.Run("late stored ack of the submit fact is waterline-refused", func(t *testing.T) {
		code, resp := f.ackRecordV2(t, ref, leadsReceiverID, leadsReceiverToken, f.tenA, ackBody(ref))
		if code != http.StatusConflict || resp["error"] != "revocation_waterline" {
			t.Fatalf("waterline = %d %v", code, resp)
		}
		lead, _ := f.s.St.GetLeadSubmissionByRef(ref)
		if lead.SyncState != "revoked" || lead.ReceiverState == "stored" {
			t.Fatalf("resurrection: %+v", lead)
		}
		// the refusal is auditable
		rows, _ := f.s.St.ListLeadAudit(f.tenA, ref)
		found := false
		for _, r := range rows {
			if r["action"] == "ack_refused" && strings.Contains(r["detail"], "revocation_waterline") {
				found = true
			}
		}
		if !found {
			t.Fatalf("ack_refused audit missing: %v", rows)
		}
	})

	t.Run("the receiver converging the revoke is recorded", func(t *testing.T) {
		lead, _ := f.s.St.GetLeadSubmissionByRef(ref)
		body := fmt.Sprintf(`{"receipt_id":"rcp_revoke_v2","source_event_id":%q,"submission_id":%q,"source_revision":%d,"receiver_state":"revoked"}`,
			ref+"-revoke", ref, lead.RevokeSourceRevision)
		code, resp := f.ackRecordV2(t, ref, leadsReceiverID, leadsReceiverToken, f.tenA, body)
		if code != http.StatusCreated {
			t.Fatalf("revoke ack = %d %v", code, resp)
		}
		final, _ := f.s.St.GetLeadSubmissionByRef(ref)
		if final.ReceiverState != "revoked" {
			t.Fatalf("receiver projection = %+v", final)
		}
	})
}

func TestLeadRecordSurfaceInvisibleWhenCaptureOff(t *testing.T) {
	cfg := leadsTestConfig("http://notify.test")
	cfg.FeatureLeadsCapture = false
	f := newLeadsFixtureWithConfig(t, cfg)
	if code, _ := f.fetchRecordV2(t, "sub_x", leadsReceiverID, leadsReceiverToken, "tnt_x"); code != http.StatusNotFound {
		t.Fatalf("feature off must be invisible, got %d", code)
	}
}

var _ = store.ReceiverStateStored // keep the import anchored for future asserts
