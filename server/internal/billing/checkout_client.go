// checkout_client.go: 公共结算侧 Touch 原单收银客户端(touch-order-checkout/v1.1)。
//
// 纪律:
//   - 真实调用 public-ai /internal/v1/billing/touch-orders/checkout (POST Mint /
//     GET Inspect) 与 /internal/v1/billing/touch-orders/activation-active (POST
//     NotifyActivated), 使用 /internal/v1/billing/ 的 app token 闸族:
//     X-App-ID: touch-engine + X-PilotSeaview-Internal-Token: <PLATFORM_BILLING_TOKEN>。
//   - 请求体严格对齐 producer 的 DisallowUnknownFields 冻结字段集合;响应必须携带
//     权威 remote checkout_ref(pending 时必带 code_url),缺字段 fail-closed 拒绝,
//     绝不在本地伪造假收银地址或假 paid 状态。
package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	TouchCheckoutAppID         = "touch-engine"
	TouchCheckoutReturnTarget  = "ti-touch-engine-billing"
	TouchCheckoutPath          = "/internal/v1/billing/touch-orders/checkout"
	TouchActivationActivePath  = "/internal/v1/billing/touch-orders/activation-active"
	TouchCheckoutAppHeader     = "X-App-ID"
	TouchCheckoutTokenHeader   = "X-PilotSeaview-Internal-Token"
	TouchProviderWechat        = "wechat"
	TouchProviderAlipay        = "alipay"
	defaultTouchCheckoutTimout = 5 * time.Second
)

// Remote producer error sentinels mapped from public-ai's frozen error dictionary.
var (
	ErrRemoteCheckoutUnavailable = errors.New("billing: remote_checkout_unavailable")
	ErrRemoteInvalidRequest      = errors.New("billing: remote_checkout_invalid_request")
	ErrRemoteTargetNotAllowed    = errors.New("billing: remote_checkout_target_not_allowed")
	ErrRemotePayerUnknown        = errors.New("billing: remote_checkout_payer_unknown")
	ErrRemoteCheckoutNotFound    = errors.New("billing: remote_checkout_not_found")
	ErrRemoteIdempotencyConflict = errors.New("billing: remote_checkout_idempotency_conflict")
	ErrRemotePriceChanged        = errors.New("billing: remote_checkout_price_changed")
	ErrRemoteCheckoutViolation   = errors.New("billing: remote_checkout_violation")
)

// TouchCheckoutPayer is the frozen payer binding (touch-order-checkout/v1 §1).
type TouchCheckoutPayer struct {
	PrincipalRef  string `json:"principal_ref"`
	GrantRef      string `json:"grant_ref"`
	GrantRevision int64  `json:"grant_revision"`
}

// TouchCheckoutLine is the frozen price admission line (touch-order-checkout/v1 §1/§4).
type TouchCheckoutLine struct {
	Kind               string `json:"kind"`
	PlanCode           string `json:"plan_code"`
	PriceRef           string `json:"price_ref"`
	PricingVersion     string `json:"pricing_version"`
	AmountMinor        int64  `json:"amount_minor"`
	Currency           string `json:"currency"`
	ServiceDays        int    `json:"service_days"`
	IncludedStoreCount int    `json:"included_store_count"`
	AdditionalStores   int    `json:"additional_stores"`
}

// TouchCheckoutMintRequest matches public-ai's touchCheckoutHTTPRequest byte-for-byte
// (producer uses DisallowUnknownFields; exactly these 8 fields).
type TouchCheckoutMintRequest struct {
	SourceApp      string             `json:"source_app"`
	SourceOrderRef string             `json:"source_order_ref"`
	IntentKey      string             `json:"intent_key"`
	TenantRef      string             `json:"tenant_ref"`
	Provider       string             `json:"provider"`
	Payer          TouchCheckoutPayer `json:"payer"`
	Line           TouchCheckoutLine  `json:"line"`
	ReturnTarget   string             `json:"return_target"`
}

// TouchCheckoutResult matches public-ai's TouchOrderCheckoutResult wire shape.
type TouchCheckoutResult struct {
	CheckoutRef     string             `json:"checkout_ref"`
	OutTradeNo      string             `json:"out_trade_no"`
	SourceApp       string             `json:"source_app"`
	SourceOrderRef  string             `json:"source_order_ref"`
	Status          string             `json:"status"`
	CodeURL         string             `json:"code_url"`
	Replayed        bool               `json:"replayed"`
	ExpiresAt       string             `json:"expires_at"`
	ReturnTarget    string             `json:"return_target"`
	ReturnURL       string             `json:"return_url"`
	PriceDigest     string             `json:"price_digest"`
	Payer           TouchCheckoutPayer `json:"payer"`
	Line            TouchCheckoutLine  `json:"line"`
	CreatedAtUnix   int64              `json:"created_at_unix"`
	SettledAtUnix   int64              `json:"settled_at_unix"`
	ActivatedAtUnix int64              `json:"activated_at_unix"`
}

// TouchActivationActiveNotice matches public-ai's v1.1 §2b frozen 5-field wire body.
type TouchActivationActiveNotice struct {
	CheckoutRef     string `json:"checkout_ref"`
	SourceOrderRef  string `json:"source_order_ref"`
	OccurredAt      string `json:"occurred_at"`
	SubscriptionRef string `json:"subscription_ref"`
	PaidUntil       string `json:"paid_until"`
}

// TouchActivationActiveReceipt matches public-ai's RecordTouchActivationActive response.
type TouchActivationActiveReceipt struct {
	CheckoutRef     string `json:"checkout_ref"`
	SourceOrderRef  string `json:"source_order_ref"`
	Status          string `json:"status"`
	Activated       bool   `json:"activated"`
	Replayed        bool   `json:"replayed"`
	ActivatedAtUnix int64  `json:"activated_at_unix"`
}

// TouchCheckoutClient is the authoritative public-billing Mint/Inspect/NotifyActivated consumer.
type TouchCheckoutClient interface {
	Mint(ctx context.Context, req TouchCheckoutMintRequest) (TouchCheckoutResult, error)
	Inspect(ctx context.Context, sourceOrderRef string) (TouchCheckoutResult, error)
	NotifyActivated(ctx context.Context, notice TouchActivationActiveNotice) (TouchActivationActiveReceipt, error)
}

// HTTPTouchCheckoutClient calls public-ai's /internal/v1/billing/touch-orders/* endpoints.
type HTTPTouchCheckoutClient struct {
	BaseURL string
	Token   string
	Client  *http.Client
}

// NewHTTPTouchCheckoutClient constructs a fail-closed HTTP client for public-ai touch-orders.
func NewHTTPTouchCheckoutClient(baseURL, token string, client *http.Client) *HTTPTouchCheckoutClient {
	if client == nil {
		client = &http.Client{Timeout: defaultTouchCheckoutTimout}
	}
	return &HTTPTouchCheckoutClient{
		BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		Token:   strings.TrimSpace(token),
		Client:  client,
	}
}

func (c *HTTPTouchCheckoutClient) configured() bool {
	return c != nil && c.BaseURL != "" && c.Token != ""
}

// Mint creates or idempotently replays an authoritative checkout session at public-ai.
func (c *HTTPTouchCheckoutClient) Mint(ctx context.Context, req TouchCheckoutMintRequest) (TouchCheckoutResult, error) {
	if !c.configured() {
		return TouchCheckoutResult{}, ErrRemoteCheckoutUnavailable
	}
	if req.SourceApp == "" {
		req.SourceApp = TouchCheckoutAppID
	}
	if req.ReturnTarget == "" {
		req.ReturnTarget = TouchCheckoutReturnTarget
	}
	body, err := json.Marshal(req)
	if err != nil {
		return TouchCheckoutResult{}, fmt.Errorf("%w: marshal: %v", ErrRemoteInvalidRequest, err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+TouchCheckoutPath, bytes.NewReader(body))
	if err != nil {
		return TouchCheckoutResult{}, fmt.Errorf("%w: request: %v", ErrRemoteInvalidRequest, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(TouchCheckoutAppHeader, TouchCheckoutAppID)
	httpReq.Header.Set(TouchCheckoutTokenHeader, c.Token)

	resp, err := c.Client.Do(httpReq)
	if err != nil {
		return TouchCheckoutResult{}, fmt.Errorf("%w: %v", ErrRemoteCheckoutUnavailable, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return TouchCheckoutResult{}, fmt.Errorf("%w: read: %v", ErrRemoteCheckoutUnavailable, err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return TouchCheckoutResult{}, mapRemoteTouchCheckoutError(resp.StatusCode, raw)
	}
	var out TouchCheckoutResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return TouchCheckoutResult{}, fmt.Errorf("%w: decode: %v", ErrRemoteCheckoutUnavailable, err)
	}
	// Fail-closed contract check: authoritative mint must carry remote checkout_ref;
	// a pending checkout must carry an authoritative code_url.
	if strings.TrimSpace(out.CheckoutRef) == "" {
		return TouchCheckoutResult{}, fmt.Errorf("%w: missing remote checkout_ref", ErrRemoteCheckoutUnavailable)
	}
	if (out.Status == "" || out.Status == "pending") && strings.TrimSpace(out.CodeURL) == "" {
		return TouchCheckoutResult{}, fmt.Errorf("%w: pending mint missing code_url", ErrRemoteCheckoutUnavailable)
	}
	return out, nil
}

// Inspect reads the authoritative checkout status for one Touch source_order_ref.
// Strictly read-only: callers never treat Inspect as a local payment-state mutation.
func (c *HTTPTouchCheckoutClient) Inspect(ctx context.Context, sourceOrderRef string) (TouchCheckoutResult, error) {
	if !c.configured() {
		return TouchCheckoutResult{}, ErrRemoteCheckoutUnavailable
	}
	sourceOrderRef = strings.TrimSpace(sourceOrderRef)
	if sourceOrderRef == "" {
		return TouchCheckoutResult{}, ErrRemoteInvalidRequest
	}
	q := url.Values{}
	q.Set("source_app", TouchCheckoutAppID)
	q.Set("source_order_ref", sourceOrderRef)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+TouchCheckoutPath+"?"+q.Encode(), nil)
	if err != nil {
		return TouchCheckoutResult{}, fmt.Errorf("%w: request: %v", ErrRemoteInvalidRequest, err)
	}
	httpReq.Header.Set(TouchCheckoutAppHeader, TouchCheckoutAppID)
	httpReq.Header.Set(TouchCheckoutTokenHeader, c.Token)

	resp, err := c.Client.Do(httpReq)
	if err != nil {
		return TouchCheckoutResult{}, fmt.Errorf("%w: %v", ErrRemoteCheckoutUnavailable, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return TouchCheckoutResult{}, fmt.Errorf("%w: read: %v", ErrRemoteCheckoutUnavailable, err)
	}
	if resp.StatusCode != http.StatusOK {
		return TouchCheckoutResult{}, mapRemoteTouchCheckoutError(resp.StatusCode, raw)
	}
	var out TouchCheckoutResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return TouchCheckoutResult{}, fmt.Errorf("%w: decode: %v", ErrRemoteCheckoutUnavailable, err)
	}
	if strings.TrimSpace(out.CheckoutRef) == "" {
		return TouchCheckoutResult{}, fmt.Errorf("%w: inspect missing remote checkout_ref", ErrRemoteCheckoutUnavailable)
	}
	return out, nil
}

// NotifyActivated posts Touch's asynchronous activation_active receipt back to public-ai (§2b).
func (c *HTTPTouchCheckoutClient) NotifyActivated(ctx context.Context, notice TouchActivationActiveNotice) (TouchActivationActiveReceipt, error) {
	if !c.configured() {
		return TouchActivationActiveReceipt{}, ErrRemoteCheckoutUnavailable
	}
	body, err := json.Marshal(notice)
	if err != nil {
		return TouchActivationActiveReceipt{}, fmt.Errorf("%w: marshal: %v", ErrRemoteCheckoutViolation, err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+TouchActivationActivePath, bytes.NewReader(body))
	if err != nil {
		return TouchActivationActiveReceipt{}, fmt.Errorf("%w: request: %v", ErrRemoteCheckoutViolation, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(TouchCheckoutAppHeader, TouchCheckoutAppID)
	httpReq.Header.Set(TouchCheckoutTokenHeader, c.Token)

	resp, err := c.Client.Do(httpReq)
	if err != nil {
		return TouchActivationActiveReceipt{}, fmt.Errorf("%w: %v", ErrRemoteCheckoutUnavailable, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return TouchActivationActiveReceipt{}, fmt.Errorf("%w: read: %v", ErrRemoteCheckoutUnavailable, err)
	}
	switch {
	case resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated:
		var out TouchActivationActiveReceipt
		_ = json.Unmarshal(raw, &out)
		return out, nil
	case resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusRequestTimeout:
		return TouchActivationActiveReceipt{}, fmt.Errorf("%w: status %d", ErrRemoteCheckoutUnavailable, resp.StatusCode)
	case resp.StatusCode == http.StatusNotFound:
		return TouchActivationActiveReceipt{}, fmt.Errorf("%w: producer_unknown_checkout_ref status 404", ErrRemoteCheckoutNotFound)
	default:
		return TouchActivationActiveReceipt{}, fmt.Errorf("%w: status %d", ErrRemoteCheckoutViolation, resp.StatusCode)
	}
}

func mapRemoteTouchCheckoutError(status int, raw []byte) error {
	var body struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &body)
	code := strings.TrimSpace(body.Error)
	switch code {
	case "touch_checkout_price_changed", "touch_checkout_price_not_registered", "touch_checkout_price_mismatch":
		return fmt.Errorf("%w: %s", ErrRemotePriceChanged, code)
	case "touch_checkout_idempotency_conflict":
		return fmt.Errorf("%w: %s", ErrRemoteIdempotencyConflict, code)
	case "touch_checkout_target_not_allowed":
		return fmt.Errorf("%w: %s", ErrRemoteTargetNotAllowed, code)
	case "touch_checkout_payer_unknown":
		return fmt.Errorf("%w: %s", ErrRemotePayerUnknown, code)
	case "touch_checkout_not_found":
		return fmt.Errorf("%w: %s", ErrRemoteCheckoutNotFound, code)
	case "touch_checkout_invalid_request":
		return fmt.Errorf("%w: %s", ErrRemoteInvalidRequest, code)
	case "touch_checkout_unavailable", "touch_checkout_provider_unavailable":
		return fmt.Errorf("%w: %s", ErrRemoteCheckoutUnavailable, code)
	}
	switch {
	case status == http.StatusNotFound:
		return fmt.Errorf("%w: status 404", ErrRemoteCheckoutNotFound)
	case status == http.StatusConflict:
		return fmt.Errorf("%w: status 409 %s", ErrRemoteIdempotencyConflict, code)
	case status == http.StatusForbidden:
		return fmt.Errorf("%w: status 403 %s", ErrRemoteTargetNotAllowed, code)
	case status == http.StatusBadRequest:
		return fmt.Errorf("%w: status 400 %s", ErrRemoteInvalidRequest, code)
	case status >= 500 || status == http.StatusTooManyRequests || status == http.StatusRequestTimeout:
		return fmt.Errorf("%w: status %d %s", ErrRemoteCheckoutUnavailable, status, code)
	default:
		return fmt.Errorf("%w: status %d %s", ErrRemoteCheckoutViolation, status, code)
	}
}

// ValidTouchProvider checks whether provider is on public-ai's frozen provider whitelist.
func ValidTouchProvider(provider string) bool {
	return provider == TouchProviderWechat || provider == TouchProviderAlipay
}
