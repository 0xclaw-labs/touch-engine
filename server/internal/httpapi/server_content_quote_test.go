package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestContentQuoteConfirmUsesTrustedBillingAndReplaysOneTask(t *testing.T) {
	holds, tasks := 0, 0
	taskKeys := map[string]bool{}
	expiry := time.Now().Add(time.Hour).Unix()
	held := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if r.Method == "POST" {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		switch r.URL.Path {
		case "/internal/v1/billing/unified/usage":
			if body["funding_version"] != "source_reservation_v1" || body["payer_user_id"] != "usr_owner_a" || body["payer_account_id"] != "trusted-account" {
				t.Errorf("untrusted payer: %+v", body)
			}
			_, _ = w.Write([]byte(`{"fact":{"usage_id":"usage-one","status":"ingested"}}`))
		case "/internal/v1/billing/unified/quote":
			_ = json.NewEncoder(w).Encode(map[string]any{"quote": map[string]any{"quote_id": "quote-one", "usage_id": "usage-one", "amount_minor": 8900, "expires_at_unix": expiry, "pricing_version": "production-v1"}})
		case "/internal/v1/billing/unified/usage/usage-one":
			h := any(nil)
			if held {
				h = map[string]any{"hold_id": "hold-one", "status": "open", "amount_minor": 8900}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"facts": map[string]any{"quote": map[string]any{"quote_id": "quote-one", "amount_minor": 8900, "expires_at_unix": expiry}, "hold": h}})
		case "/internal/v1/billing/unified/hold":
			holds++
			held = true
			if body["expected_amount_minor"] != float64(8900) {
				t.Error("hold is not price pinned")
			}
			_, _ = w.Write([]byte(`{"hold":{"hold_id":"hold-one","usage_id":"usage-one","status":"open","amount_minor":8900}}`))
		case "/internal/v1/tasks":
			tasks++
			key, _ := body["idempotency_key"].(string)
			taskKeys[key] = true
			params, _ := body["params"].(map[string]any)
			if params["usage_id"] != "usage-one" || params["hold_id"] != "hold-one" || params["payer_account_id"] != "trusted-account" {
				t.Errorf("lost source reservation: %+v", params)
			}
			if tasks == 1 {
				w.WriteHeader(503)
				_, _ = w.Write([]byte(`{"error":"response_lost_after_acceptance"}`))
				return
			}
			_, _ = w.Write([]byte(`{"task_id":"task-one","status":"submitted"}`))
		default:
			t.Errorf("unexpected upstream path: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	f := newProductionFixture(t, productionConfigOpts{serviceTokens: producerTokens, serviceRoles: producerRoles, billingURL: upstream.URL, billingToken: "bill-token", taskURL: upstream.URL, taskToken: "task-token", featureTask: true})
	f.s.Cfg.ProductionBillingAccounts = fmt.Sprintf(`{%q:"trusted-account"}`, f.tenA)
	f.s.Cfg.ProductionPricingVersion = "production-v1"
	f.s.Cfg.ProductionDeliverables = "视频一条，商家预览后采用"
	endpoint := "/api/v1/stores/" + f.storeA + "/content-production-handoffs"
	quoteBody := fmt.Sprintf(`{"content_pack_ref":"pack:%s","idempotency_key":"quote-key-one","note":"门店介绍"}`, f.packA)
	status, out := f.do(t, "POST", endpoint+"/quote", "sess-owner-a", f.tenA, quoteBody)
	if status != 200 || holds != 0 || tasks != 0 {
		t.Fatalf("quote incurred work: %d %+v holds=%d tasks=%d", status, out, holds, tasks)
	}
	q := out["quote"].(map[string]any)
	if q["payer"] != "trusted-account" || q["quote_ref"] != "quote-one" {
		t.Fatalf("quote not authority-backed: %+v", q)
	}
	body := map[string]any{"content_pack_ref": "pack:" + f.packA, "quote_ref": q["quote_ref"], "offer_token": q["offer_token"], "accepted_amount_minor": q["amount_minor"], "accepted_currency": q["currency"], "accepted_expires_at": q["expires_at"], "idempotency_key": "quote-key-one", "note": "门店介绍"}
	raw, _ := json.Marshal(body)
	status, first := f.do(t, "POST", endpoint, "sess-owner-a", f.tenA, string(raw))
	if status != 201 || holds != 1 || tasks != 1 {
		t.Fatalf("confirm: %d %+v holds=%d tasks=%d", status, first, holds, tasks)
	}
	status, replay := f.do(t, "POST", endpoint, "sess-owner-a", f.tenA, string(raw))
	if status != 200 || replay["replayed"] != true || holds != 1 || tasks != 1 {
		t.Fatalf("duplicate: %d %+v holds=%d tasks=%d", status, replay, holds, tasks)
	}
	handoff := first["handoff"].(map[string]any)
	status, recovered := f.do(t, "POST", endpoint+"/"+handoff["id"].(string)+"/retry", "sess-owner-a", f.tenA, `{}`)
	if status != 200 || holds != 1 || tasks != 2 || len(taskKeys) != 1 || recovered["handoff"].(map[string]any)["task_ref"] != "task-one" {
		t.Fatalf("lost response recovery duplicated task/hold: %d %+v holds=%d tasks=%d keys=%d", status, recovered, holds, tasks, len(taskKeys))
	}
	body["idempotency_key"] = "tamper-attempt"
	body["accepted_amount_minor"] = 1
	raw, _ = json.Marshal(body)
	status, _ = f.do(t, "POST", endpoint, "sess-owner-a", f.tenA, string(raw))
	if status != 409 || holds != 1 {
		t.Fatalf("price tamper accepted: %d holds=%d", status, holds)
	}
	body["offer_token"] = q["offer_token"].(string) + "tamper"
	raw, _ = json.Marshal(body)
	status, _ = f.do(t, "POST", endpoint, "sess-owner-a", f.tenA, string(raw))
	if status != 422 || holds != 1 {
		t.Fatalf("signature tamper accepted: %d holds=%d", status, holds)
	}
	status, _ = f.do(t, "POST", endpoint+"/quote", "sess-staff-a", f.tenA, quoteBody)
	if status != 403 {
		t.Fatalf("staff quote: %d", status)
	}
	if strings.Contains(fmt.Sprint(out), "bill-token") {
		t.Fatal("token leaked")
	}
}
