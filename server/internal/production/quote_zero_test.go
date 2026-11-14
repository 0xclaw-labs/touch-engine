package production

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAuthoritativeZeroQuoteIsDistinctFromMissingAmount(t *testing.T) {
	now := time.Now()
	q := Quote{Ref: "quote-free", UsageID: "usage-free", AmountMinor: 0, Currency: "CNY", ExpiresAt: now.Add(time.Hour).Format(time.RFC3339), Deliverables: "已报价的门店介绍"}
	if err := ValidateOffer(q, now); err != nil {
		t.Fatalf("source_reservation_v1 permits an authoritative zero amount: %v", err)
	}
	q.AmountMinor = -1
	if err := ValidateOffer(q, now); err == nil {
		t.Fatal("negative price accepted")
	}
}

func TestSourceQuoteRequiresExplicitAmountEvenForZeroPrice(t *testing.T) {
	for _, includeAmount := range []bool{false, true} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/internal/v1/billing/unified/usage" {
				_, _ = w.Write([]byte(`{"fact":{"usage_id":"usage-zero"}}`))
				return
			}
			quote := map[string]any{"quote_id": "quote-zero", "usage_id": "usage-zero", "expires_at_unix": time.Now().Add(time.Hour).Unix(), "pricing_version": "zero-v1"}
			if includeAmount {
				quote["amount_minor"] = 0
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"quote": quote})
		}))
		q, err := NewQuoteAuthority(srv.URL, "t", "touch-engine").RequestQuote(context.Background(), QuoteRequest{PayerAccountID: "payer", PayerUserID: "user", PricingVersion: "zero-v1", Deliverables: "免费报价的制作", IdempotencyKey: "request"})
		srv.Close()
		if includeAmount && (err != nil || q.AmountMinor != 0) {
			t.Fatalf("explicit zero rejected: %+v %v", q, err)
		}
		if !includeAmount && err == nil {
			t.Fatal("missing amount fabricated as free")
		}
	}
}

func TestZeroQuoteRecoveryRequiresExplicitHoldAmount(t *testing.T) {
	now := time.Now().Add(time.Hour).Truncate(time.Second)
	for _, includeAmount := range []bool{false, true} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Error("an existing reservation must not start another hold")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			hold := map[string]any{"hold_id": "hold-zero", "usage_id": "usage-zero", "status": "open"}
			if includeAmount {
				hold["amount_minor"] = 0
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"facts": map[string]any{
				"quote": map[string]any{"quote_id": "quote-zero", "usage_id": "usage-zero", "amount_minor": 0, "expires_at_unix": now.Unix()},
				"hold":  hold,
			}})
		}))
		q := Quote{Ref: "quote-zero", UsageID: "usage-zero", AmountMinor: 0, Currency: "CNY", ExpiresAt: now.Format(time.RFC3339), Deliverables: "免费报价的制作"}
		hold, err := NewQuoteAuthority(srv.URL, "t", "touch-engine").ConfirmOffer(context.Background(), QuoteRequest{PayerAccountID: "payer", PayerUserID: "user", IdempotencyKey: "request"}, q)
		srv.Close()
		if includeAmount && (err != nil || hold.ID != "hold-zero" || hold.AmountMinor != 0) {
			t.Fatalf("explicit zero hold rejected: %+v %v", hold, err)
		}
		if !includeAmount && err == nil {
			t.Fatal("missing hold amount fabricated as free")
		}
	}
}
