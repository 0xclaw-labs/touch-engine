package production

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const SourceReservationV1 = "source_reservation_v1"

// QuoteRequest contains only server-resolved ownership/configuration.
// usage records ingested/quoted facts; it never holds or charges funds.
type QuoteRequest struct {
	TenantID       string `json:"tenant_id"`
	StoreID        string `json:"store_id"`
	PackID         string `json:"pack_id"`
	PayerAccountID string `json:"payer_account_id"`
	PayerUserID    string `json:"payer_user_id"`
	PricingVersion string `json:"pricing_version"`
	Deliverables   string `json:"deliverables"`
	IdempotencyKey string `json:"idempotency_key"`
	Brief          string `json:"brief"`
}

type QuoteRequester interface {
	Ready() bool
	RequestQuote(context.Context, QuoteRequest) (Quote, error)
	ConfirmOffer(context.Context, QuoteRequest, Quote) (Hold, error)
}

type Hold struct {
	ID          string `json:"hold_id"`
	UsageID     string `json:"usage_id"`
	AmountMinor int64  `json:"amount_minor"`
	Status      string `json:"status"`
}

type sourceQuote struct {
	QuoteID        string `json:"quote_id"`
	UsageID        string `json:"usage_id"`
	AmountMinor    *int64 `json:"amount_minor"`
	ExpiresAtUnix  int64  `json:"expires_at_unix"`
	PricingVersion string `json:"pricing_version"`
}

func (c *BillingQuoteAuthority) scope(in QuoteRequest) map[string]any {
	return map[string]any{"funding_version": SourceReservationV1, "app_id": c.AppID, "payer_account_id": in.PayerAccountID, "payer_user_id": in.PayerUserID}
}
func (c *BillingQuoteAuthority) RequestQuote(ctx context.Context, in QuoteRequest) (Quote, error) {
	if !c.Ready() || in.PricingVersion == "" || in.PayerAccountID == "" || in.PayerUserID == "" || in.Deliverables == "" {
		return Quote{}, ErrUnavailable
	}
	var usage struct {
		Fact struct {
			UsageID string `json:"usage_id"`
		} `json:"fact"`
	}
	body := c.scope(in)
	body["capability"] = "content_production"
	body["quantity"] = 1
	body["pricing_version"] = in.PricingVersion
	body["business_ref"] = "pack:" + in.PackID + ":" + in.Brief
	body["idempotency_key"] = in.IdempotencyKey
	if err := c.post(ctx, c.BaseURL+"/internal/v1/billing/unified/usage", body, &usage); err != nil {
		return Quote{}, err
	}
	if strings.TrimSpace(usage.Fact.UsageID) == "" {
		return Quote{}, fmt.Errorf("%w: no usage reference", ErrUnavailable)
	}
	body = c.scope(in)
	body["usage_id"] = usage.Fact.UsageID
	var out struct {
		Quote sourceQuote `json:"quote"`
	}
	if err := c.post(ctx, c.BaseURL+"/internal/v1/billing/unified/quote", body, &out); err != nil {
		return Quote{}, err
	}
	if out.Quote.AmountMinor == nil || out.Quote.UsageID != usage.Fact.UsageID || out.Quote.PricingVersion != in.PricingVersion {
		return Quote{}, ErrUnavailable
	}
	q := Quote{Ref: out.Quote.QuoteID, UsageID: out.Quote.UsageID, AmountMinor: *out.Quote.AmountMinor, Currency: "CNY", ExpiresAt: time.Unix(out.Quote.ExpiresAtUnix, 0).UTC().Format(time.RFC3339), Deliverables: in.Deliverables, PricingVersion: out.Quote.PricingVersion}
	if err := ValidateOffer(q, time.Now()); err != nil {
		if out.Quote.ExpiresAtUnix > 0 && out.Quote.ExpiresAtUnix <= time.Now().Unix() {
			return Quote{}, ErrOfferExpired
		}
		return Quote{}, err
	}
	return q, nil
}

func ValidateOffer(q Quote, now time.Time) error {
	expires, err := time.Parse(time.RFC3339, q.ExpiresAt)
	if q.Ref == "" || q.UsageID == "" || q.AmountMinor < 0 || q.Currency != "CNY" || q.Deliverables == "" || err != nil || !expires.After(now) {
		return fmt.Errorf("%w: invalid or expired authoritative source quote", ErrUnavailable)
	}
	return nil
}

// ConfirmOffer first recovers any existing hold. An expired offer may recover
// its existing reservation, but can never open a new one. Hold is pinned to
// quote_id + expected_amount_minor; no charge call exists in this adapter.
func (c *BillingQuoteAuthority) ConfirmOffer(ctx context.Context, in QuoteRequest, q Quote) (Hold, error) {
	facts, err := c.lookup(ctx, in, q.UsageID)
	if err != nil {
		return Hold{}, err
	}
	if facts.Quote.AmountMinor == nil || facts.Quote.QuoteID != q.Ref || *facts.Quote.AmountMinor != q.AmountMinor || facts.Quote.ExpiresAtUnix != mustUnix(q.ExpiresAt) {
		return Hold{}, ErrOfferChanged
	}
	if facts.Hold.ID != "" {
		if facts.Hold.Status == "open" && facts.Hold.AmountMinor != nil && *facts.Hold.AmountMinor == q.AmountMinor {
			return facts.Hold.hold(), nil
		}
		return Hold{}, ErrOfferChanged
	}
	if err := ValidateOffer(q, time.Now()); err != nil {
		return Hold{}, ErrOfferExpired
	}
	body := c.scope(in)
	body["usage_id"] = q.UsageID
	body["quote_id"] = q.Ref
	body["expected_amount_minor"] = q.AmountMinor
	body["idempotency_key"] = in.IdempotencyKey + ":hold"
	body["reason"] = "商家明确确认内容制作报价"
	var out struct {
		Hold sourceHold `json:"hold"`
	}
	if err := c.post(ctx, c.BaseURL+"/internal/v1/billing/unified/hold", body, &out); err != nil {
		return Hold{}, err
	}
	if out.Hold.ID == "" || out.Hold.Status != "open" || out.Hold.AmountMinor == nil || *out.Hold.AmountMinor != q.AmountMinor {
		return Hold{}, ErrOfferChanged
	}
	return out.Hold.hold(), nil
}
func mustUnix(s string) int64 { t, _ := time.Parse(time.RFC3339, s); return t.Unix() }

type sourceFacts struct {
	Quote sourceQuote `json:"quote"`
	Hold  sourceHold  `json:"hold"`
}

type sourceHold struct {
	ID          string `json:"hold_id"`
	UsageID     string `json:"usage_id"`
	AmountMinor *int64 `json:"amount_minor"`
	Status      string `json:"status"`
}

func (h sourceHold) hold() Hold {
	return Hold{ID: h.ID, UsageID: h.UsageID, AmountMinor: *h.AmountMinor, Status: h.Status}
}

func (c *BillingQuoteAuthority) lookup(ctx context.Context, in QuoteRequest, usageID string) (sourceFacts, error) {
	query := url.Values{"app_id": {c.AppID}, "payer_account_id": {in.PayerAccountID}, "payer_user_id": {in.PayerUserID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/internal/v1/billing/unified/usage/"+url.PathEscape(usageID)+"?"+query.Encode(), nil)
	if err != nil {
		return sourceFacts{}, ErrUnavailable
	}
	req.Header.Set(taskTokenHeader, c.Token)
	req.Header.Set(appIDHeader, c.AppID)
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return sourceFacts{}, ErrUnavailable
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return sourceFacts{}, ErrUnavailable
	}
	if res.StatusCode != 200 {
		return sourceFacts{}, &AuthorityError{Status: res.StatusCode}
	}
	var out struct {
		Facts sourceFacts `json:"facts"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return sourceFacts{}, ErrUnavailable
	}
	return out.Facts, nil
}
