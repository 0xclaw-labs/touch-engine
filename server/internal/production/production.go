// Package production is the HUI-4595 public-authority seam: quotes come from
// the platform unified billing authority and production tasks from the
// platform task authority (派诺生态 Task / Billing). Touch never mints a
// price or a task reference: missing configuration fails closed and the
// caller answers honestly (生成前价格可见;未接通=诚实关闭,不伪造成功).
//
// Contract shape follows the in-repo copyjob platform client (billing
// unified usage/quote, task create with idempotency key), so HUI-4591 can
// adopt this seam without a second billing dialect. Tokens never appear in
// logs; URLs come from process config only.
package production

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	taskTokenHeader = "X-PilotSeaview-Internal-Token"
	appIDHeader     = "X-App-ID"
)

// ErrNotConfirmed means the authority answered but the quote is not in a
// confirmed state. Nothing was submitted.
var ErrNotConfirmed = errors.New("production: quote is not confirmed")

// ErrUnavailable means the authority cannot be asked at all (not configured
// or transport failure). Callers must not treat this as confirmation.
var ErrUnavailable = errors.New("production: authority unavailable")

// Quote is the authoritative answer about one declared quote reference.
type Quote struct {
	Ref            string `json:"quote_ref"`
	AmountMinor    int64  `json:"amount_minor"`
	Currency       string `json:"currency"`
	Status         string `json:"status"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// QuoteAuthority verifies that a merchant-declared quote_ref is actually
// confirmed by the billing authority (服务器核实付款授权,票面).
//
// HUI-4591 合流切换点:4591 已把裁决 exported 到库层
// (billing.AdjudicateQuoteRef 五拒绝码 + Store.QuoteProjectionFor/
// ConsumeQuoteProjection;quote.go 头注释即契约)。账单投影已入本仓时,
// httpapi 调用侧改走库层裁决;本接口保留为账单权威仍在生态侧的远程形态。
// 调用侧不自建价格裁决逻辑(形状+归属+已确认语义之外零裁决)。
type QuoteAuthority interface {
	Ready() bool
	ConfirmedQuote(ctx context.Context, quoteRef string) (Quote, error)
}

// BillingQuoteAuthority asks the unified billing quote endpoint.
type BillingQuoteAuthority struct {
	BaseURL string
	Token   string
	AppID   string
	HTTP    *http.Client
}

// NewQuoteAuthority wires the client; empty base URL or token yields a client
// that is NOT ready (fail-closed at use).
func NewQuoteAuthority(baseURL, token, appID string) *BillingQuoteAuthority {
	return &BillingQuoteAuthority{
		BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		Token:   strings.TrimSpace(token),
		AppID:   strings.TrimSpace(appID),
	}
}

// Ready reports whether a real call is possible.
func (c *BillingQuoteAuthority) Ready() bool {
	if c == nil {
		return false
	}
	return c.BaseURL != "" && c.Token != ""
}

type billingQuoteResponse struct {
	Quote Quote `json:"quote"`
}

// ConfirmedQuote returns the quote only when the authority confirms it
// (status=confirmed with a positive amount); everything else is
// ErrNotConfirmed or ErrUnavailable — never a silent pass.
func (c *BillingQuoteAuthority) ConfirmedQuote(ctx context.Context, quoteRef string) (Quote, error) {
	if !c.Ready() {
		return Quote{}, ErrUnavailable
	}
	var out billingQuoteResponse
	if err := c.post(ctx, c.BaseURL+"/internal/v1/billing/unified/quote",
		map[string]any{"app_id": c.AppID, "quote_ref": strings.TrimSpace(quoteRef)}, &out); err != nil {
		return Quote{}, err
	}
	q := out.Quote
	if q.Status != "confirmed" || q.AmountMinor <= 0 {
		return q, ErrNotConfirmed
	}
	return q, nil
}

func (c *BillingQuoteAuthority) post(ctx context.Context, url string, payload any, dest any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrUnavailable, err.Error())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("%w: %s", ErrUnavailable, err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(taskTokenHeader, c.Token)
	req.Header.Set(appIDHeader, c.AppID)
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrUnavailable, err.Error())
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%w: %s", ErrUnavailable, err.Error())
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("%w: http %d", ErrUnavailable, res.StatusCode)
	}
	if err := json.Unmarshal(body, dest); err != nil {
		return fmt.Errorf("%w: bad json", ErrUnavailable)
	}
	return nil
}

// TaskSubmission is one idempotent platform task create request.
type TaskSubmission struct {
	IdempotencyKey string
	AccountID      string
	Kind           string
	ParamsJSON     string
}

// TaskSubmitter posts production tasks to the platform task authority and
// returns the REAL task reference (task_ref). Touch keeps no queue of its
// own; an unconfigured submitter is the honest-closure path.
type TaskSubmitter struct {
	BaseURL string
	Token   string
	AppID   string
	HTTP    *http.Client
}

// NewTaskSubmitter wires the client; empty config yields not-ready.
func NewTaskSubmitter(baseURL, token, appID string) *TaskSubmitter {
	return &TaskSubmitter{
		BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		Token:   strings.TrimSpace(token),
		AppID:   strings.TrimSpace(appID),
	}
}

// Ready reports whether a real submission is possible.
func (s *TaskSubmitter) Ready() bool {
	if s == nil {
		return false
	}
	return s.BaseURL != "" && s.Token != ""
}

// Submit posts one task and returns its authoritative task reference. The
// platform deduplicates on (app_id, idempotency_key), so retries converge on
// the same task (响应丢失重试不重复扣费, T15).
func (s *TaskSubmitter) Submit(ctx context.Context, in TaskSubmission) (string, error) {
	if !s.Ready() {
		return "", ErrUnavailable
	}
	body := map[string]any{
		"app_id":          s.AppID,
		"account_id":      in.AccountID,
		"idempotency_key": in.IdempotencyKey,
		"kind":            in.Kind,
		"params":          json.RawMessage(defaultParams(in.ParamsJSON)),
	}
	var out struct {
		TaskID string `json:"task_id"`
		Status string `json:"status"`
	}
	if err := s.post(ctx, s.BaseURL+"/internal/v1/tasks", body, &out); err != nil {
		return "", err
	}
	if strings.TrimSpace(out.TaskID) == "" {
		return "", fmt.Errorf("%w: no task_id in acceptance", ErrUnavailable)
	}
	return out.TaskID, nil
}

func defaultParams(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "{}"
	}
	return raw
}

func (s *TaskSubmitter) post(ctx context.Context, url string, payload any, dest any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrUnavailable, err.Error())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("%w: %s", ErrUnavailable, err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(taskTokenHeader, s.Token)
	req.Header.Set(appIDHeader, s.AppID)
	client := s.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrUnavailable, err.Error())
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%w: %s", ErrUnavailable, err.Error())
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("%w: http %d", ErrUnavailable, res.StatusCode)
	}
	if err := json.Unmarshal(body, dest); err != nil {
		return fmt.Errorf("%w: bad json", ErrUnavailable)
	}
	return nil
}
