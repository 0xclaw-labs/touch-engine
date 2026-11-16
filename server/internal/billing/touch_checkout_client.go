package billing

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

// touchCheckoutClient 是 mint 合同端点的 HTTP 客户端（v1.1）。
// 认证：BILLING_APP_TOKENS 的 touch-engine 绑定（Authorization: Bearer +
// X-App-ID，platformauth.ResolveApp 同款——与 producer 侧 appClaim 对表）。
// 配置复用既有权威 env（PLATFORM_BILLING_BASE_URL / PLATFORM_BILLING_TOKEN），
// 同一权威部署零新增 env；分域部署改 BASE_URL 即可。
type touchCheckoutClient struct {
	baseURL string
	token   string
	appID   string
	http    *http.Client
}

func newTouchCheckoutClient(baseURL, token, appID string) *touchCheckoutClient {
	return &touchCheckoutClient{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		token:   strings.TrimSpace(token),
		appID:   strings.TrimSpace(appID),
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *touchCheckoutClient) Ready() bool {
	return c != nil && c.baseURL != "" && c.token != "" && c.appID != ""
}

func (c *touchCheckoutClient) post(ctx context.Context, path string, in, out any) error {
	if !c.Ready() {
		return ErrCheckoutProducerUnavailable
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCheckoutProducerUnavailable, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCheckoutProducerUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-App-ID", c.appID)
	return c.do(req, out)
}

func (c *touchCheckoutClient) get(ctx context.Context, path string, out any) error {
	if !c.Ready() {
		return ErrCheckoutProducerUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCheckoutProducerUnavailable, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-App-ID", c.appID)
	return c.do(req, out)
}

func (c *touchCheckoutClient) do(req *http.Request, out any) error {
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCheckoutProducerUnavailable, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCheckoutProducerUnavailable, err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		// producer 的 4xx 类（价格/付款人/幂等冲突）按拒绝透传 reason；
		// 5xx/未知形状按不可用处理（调用方诚实 unavailable）。
		var e struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		return fmt.Errorf("%w: %s", ErrCheckoutProducerRejected, producerReason(res.StatusCode, e.Error.Code, e.Error.Message))
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("%w: %v", ErrCheckoutProducerUnavailable, err)
		}
	}
	return nil
}

func producerReason(status int, code, message string) string {
	if code != "" {
		return fmt.Sprintf("%d %s", status, code)
	}
	if message != "" {
		return fmt.Sprintf("%d %s", status, message)
	}
	return fmt.Sprintf("%d", status)
}

// Mint 实现 CheckoutProducer。
func (c *touchCheckoutClient) Mint(ctx context.Context, in MintRequest) (MintResult, error) {
	var out MintResult
	if err := c.post(ctx, "/internal/v1/billing/touch-orders/checkout", in, &out); err != nil {
		return MintResult{}, err
	}
	return out, nil
}

// Inspect 实现 GET 查单（404 折拒绝态——未知引用按不可续付诚实处理）。
func (c *touchCheckoutClient) Inspect(ctx context.Context, ckRef string) (MintResult, error) {
	var out MintResult
	if err := c.get(ctx, "/internal/v1/billing/touch-orders/checkout?source_app=touch-engine&source_order_ref="+ckRef, &out); err != nil {
		if errors.Is(err, ErrCheckoutProducerUnavailable) {
			return MintResult{}, err
		}
		return MintResult{}, fmt.Errorf("%w: %v", ErrCheckoutProducerRejected, err)
	}
	return out, nil
}

// NotifyActivated 实现 activation-active 回发（§2b 非阻塞：失败仅返回
// error 由调用方记日志；producer 侧幂等戳，重发自愈）。
func (c *touchCheckoutClient) NotifyActivated(ctx context.Context, ckRef string, occurredAt time.Time, detail map[string]string) error {
	body := map[string]any{
		"checkout_ref":  ckRef,
		"source_order_ref": detail["source_order_ref"],
		"occurred_at":   occurredAt.UTC().Format(time.RFC3339),
	}
	if v := detail["subscription_ref"]; v != "" {
		body["subscription_ref"] = v
	}
	if v := detail["paid_until"]; v != "" {
		body["paid_until"] = v
	}
	var out map[string]any
	return c.post(ctx, "/internal/v1/billing/touch-orders/activation-active", body, &out)
}
