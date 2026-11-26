package billing

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPTouchCheckoutClientMintInspectAndNotify(t *testing.T) {
	var (
		gotMintBody   map[string]any
		gotInspectRef string
		gotNotifyBody map[string]any
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(TouchCheckoutAppHeader) != TouchCheckoutAppID ||
			r.Header.Get(TouchCheckoutTokenHeader) != "tok-secret" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"billing_forbidden"}`))
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == TouchCheckoutPath:
			if err := json.NewDecoder(r.Body).Decode(&gotMintBody); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(TouchCheckoutResult{
				CheckoutRef:    "tco_remote_1",
				OutTradeNo:     "OTN_20261117_001",
				SourceApp:      TouchCheckoutAppID,
				SourceOrderRef: "ck_local_1",
				Status:         "pending",
				CodeURL:        "weixin://wxpay/bizpayurl?pr=mock001",
				ExpiresAt:      "2026-11-17T14:00:00Z",
				ReturnTarget:   TouchCheckoutReturnTarget,
				ReturnURL:      "https://touch.test/work/billing",
				PriceDigest:    "sha256:digest1",
			})
		case r.Method == http.MethodGet && r.URL.Path == TouchCheckoutPath:
			gotInspectRef = r.URL.Query().Get("source_order_ref")
			_ = json.NewEncoder(w).Encode(TouchCheckoutResult{
				CheckoutRef:    "tco_remote_1",
				OutTradeNo:     "OTN_20261117_001",
				SourceApp:      TouchCheckoutAppID,
				SourceOrderRef: gotInspectRef,
				Status:         "paid",
				CodeURL:        "weixin://wxpay/bizpayurl?pr=mock001",
				SettledAtUnix:  1763370000,
			})
		case r.Method == http.MethodPost && r.URL.Path == TouchActivationActivePath:
			if err := json.NewDecoder(r.Body).Decode(&gotNotifyBody); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(TouchActivationActiveReceipt{
				CheckoutRef:     "ck_local_1",
				SourceOrderRef:  "bo_1",
				Status:          "paid",
				Activated:       true,
				ActivatedAtUnix: 1763370010,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	client := NewHTTPTouchCheckoutClient(ts.URL, "tok-secret", nil)
	minted, err := client.Mint(context.Background(), TouchCheckoutMintRequest{
		SourceOrderRef: "ck_local_1",
		IntentKey:      "idem_1",
		TenantRef:      "tnt_1",
		Provider:       TouchProviderWechat,
		Payer:          TouchCheckoutPayer{PrincipalRef: "usr_owner", GrantRef: "", GrantRevision: 0},
		Line: TouchCheckoutLine{
			Kind: KindSubscription, PlanCode: PlanMonthly, PriceRef: "price_m_v1",
			PricingVersion: "v2026", AmountMinor: 9900, Currency: "CNY", ServiceDays: 30, IncludedStoreCount: 1,
		},
	})
	if err != nil {
		t.Fatalf("Mint failed: %v", err)
	}
	if minted.CheckoutRef != "tco_remote_1" || minted.CodeURL == "" || minted.OutTradeNo != "OTN_20261117_001" {
		t.Fatalf("unexpected mint result: %+v", minted)
	}
	// Frozen 8-field wire contract (producer DisallowUnknownFields).
	if len(gotMintBody) != 8 {
		t.Fatalf("mint request must carry exactly 8 fields, got %d: %v", len(gotMintBody), gotMintBody)
	}
	if gotMintBody["source_app"] != TouchCheckoutAppID || gotMintBody["return_target"] != TouchCheckoutReturnTarget {
		t.Fatalf("mint defaults not applied: %v", gotMintBody)
	}

	inspected, err := client.Inspect(context.Background(), "ck_local_1")
	if err != nil {
		t.Fatalf("Inspect failed: %v", err)
	}
	if gotInspectRef != "ck_local_1" || inspected.Status != "paid" || inspected.SettledAtUnix == 0 {
		t.Fatalf("unexpected inspect result: ref=%q res=%+v", gotInspectRef, inspected)
	}

	receipt, err := client.NotifyActivated(context.Background(), TouchActivationActiveNotice{
		CheckoutRef:     "ck_local_1",
		SourceOrderRef:  "bo_1",
		OccurredAt:      "2026-11-17T10:00:00Z",
		SubscriptionRef: "sub_1",
		PaidUntil:       "2026-12-17T10:00:00Z",
	})
	if err != nil {
		t.Fatalf("NotifyActivated failed: %v", err)
	}
	if !receipt.Activated || len(gotNotifyBody) != 5 {
		t.Fatalf("unexpected notify receipt=%+v body=%v", receipt, gotNotifyBody)
	}
}

func TestHTTPTouchCheckoutClientErrorsAndFailClosed(t *testing.T) {
	// Unconfigured client fails closed.
	unwired := NewHTTPTouchCheckoutClient("", "", nil)
	if _, err := unwired.Mint(context.Background(), TouchCheckoutMintRequest{}); !errors.Is(err, ErrRemoteCheckoutUnavailable) {
		t.Fatalf("unwired mint err = %v", err)
	}
	if _, err := unwired.Inspect(context.Background(), "ck_1"); !errors.Is(err, ErrRemoteCheckoutUnavailable) {
		t.Fatalf("unwired inspect err = %v", err)
	}

	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"price_changed", 409, `{"error":"touch_checkout_price_changed"}`, ErrRemotePriceChanged},
		{"price_not_registered", 409, `{"error":"touch_checkout_price_not_registered"}`, ErrRemotePriceChanged},
		{"price_mismatch", 409, `{"error":"touch_checkout_price_mismatch"}`, ErrRemotePriceChanged},
		{"idempotency_conflict", 409, `{"error":"touch_checkout_idempotency_conflict"}`, ErrRemoteIdempotencyConflict},
		{"target_not_allowed", 403, `{"error":"touch_checkout_target_not_allowed"}`, ErrRemoteTargetNotAllowed},
		{"payer_unknown", 404, `{"error":"touch_checkout_payer_unknown"}`, ErrRemotePayerUnknown},
		{"not_found", 404, `{"error":"touch_checkout_not_found"}`, ErrRemoteCheckoutNotFound},
		{"provider_unavailable", 503, `{"error":"touch_checkout_provider_unavailable"}`, ErrRemoteCheckoutUnavailable},
		{"missing_code_url_fail_closed", 200, `{"checkout_ref":"tco_1","status":"pending","code_url":""}`, ErrRemoteCheckoutUnavailable},
		{"missing_checkout_ref_fail_closed", 200, `{"checkout_ref":"","status":"pending","code_url":"weixin://x"}`, ErrRemoteCheckoutUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer ts.Close()
			c := NewHTTPTouchCheckoutClient(ts.URL, "tok", nil)
			_, err := c.Mint(context.Background(), TouchCheckoutMintRequest{SourceOrderRef: "ck_1"})
			if !errors.Is(err, tc.want) {
				t.Fatalf("got err %v, want %v", err, tc.want)
			}
		})
	}
}
