// handlers_billing_checkout_callback.go: HUI-4664 原单收银接续的 activation
// 回发通道(ADV-9 统一契约 v1.1 D3)。
//
// 语义(v1.1 注记):Touch 内容制作履约是异步工程事实——paid=付款完成可开工
//(制作任务在发起时已提交);activated=制作工程确认开动(本地事实)。当
// payment_paid 把接续引用落到 paid、且制作 handoff 已处于 submitted(或其后
// 的 produced)态,Touch 自主把行推进 activated,并把 activation_active 回发
// 给 producer,供其结算行收口。
//
// 回发通道(v1.1 裁定):producer 侧若无专用接收端点,则经 authority events
// 缝的形状回发——POST {PLATFORM_BILLING_BASE_URL}/api/v1/billing/authority/
// events,source=touch-engine,X-Service-ID/X-Service-Token 与 producer 外发
// 缝(PR4 §外发缝)同形;producer 侧按合同消费。凭据=PLATFORM_BILLING_TOKEN
//(Touch 在公共结算侧的登记令牌);未配置=诚实跳过回发,本地事实照落。
//
// 确定性纪律(桥式自愈的前提):event_id=touch-act-<checkout_ref>,occurred_at
// =行的 updated_at(激活落行时刻)——重发(渠道重试触发的 payment_paid 重放)
// 字节恒等,producer 按 (source,event_id) 去重=幂等读回,绝不产生「同键异
// 负载」409。回发可重试失败(网络/5xx/429/408)时本请求答 503,让 producer
// 按既有 webhook FAIL→重试→replay 姿态回来补投;4xx/409=合同违例,大声留痕
// 后放行本请求(重试不可修复,不得把已消费的支付事实拖入无限 FAIL)。
package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/billing"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// Send-back failure classes (mirrors the producer's touchauthority posture):
// retryable = transport/5xx/429/408 (channel retry heals); violation = 4xx/409
// (loud, not fixable by retry).
var (
	errRetryableDelivery           = errors.New("checkout activation send-back retryably failed")
	errActivationDeliveryViolation = errors.New("checkout activation send-back rejected as contract violation")
)

// checkoutActivationSource is the service identity Touch presents on the
// send-back (v1.1: source=touch-engine); the token is Touch's registered
// credential at the public settlement side (PLATFORM_BILLING_TOKEN).
const checkoutActivationSource = "touch-engine"

// checkoutActivationEventIDPrefix + path mirror the producer's outbound seam
// discipline (PR4 §外发缝): stable fact id, seam-shaped endpoint.
const (
	checkoutActivationEventIDPrefix = "touch-act-"
	checkoutActivationEventsPath    = "/api/v1/billing/authority/events"
)

// checkoutActivationCallTimeout bounds one send-back attempt; a wedged
// producer must turn into a retryable answer (503), not a hung seam request.
const checkoutActivationCallTimeout = 5 * time.Second

var checkoutActivationClient = &http.Client{Timeout: checkoutActivationCallTimeout}

// checkoutActivationWire is the send-back body: the authority-events seam
// shape (same field names as the producer's eventWire / this seam's own
// authorityEventBody), with checkout_ref as the routing key (v1.1).
type checkoutActivationWire struct {
	TenantID    string               `json:"tenant_id"`
	OrderID     string               `json:"order_id"`
	Source      string               `json:"source"`
	EventID     string               `json:"event_id"`
	Type        string               `json:"type"`
	OccurredAt  string               `json:"occurred_at"`
	Payload     billing.EventPayload `json:"payload"`
	CheckoutRef string               `json:"checkout_ref,omitempty"`
}

// checkoutCallbackConfigured answers whether the send-back channel can
// deliver: the checkout capability is on AND the producer base URL AND
// Touch's registered token are configured.
func (s *Server) checkoutCallbackConfigured() bool {
	return s.Cfg.FeatureBillingCheckout &&
		strings.TrimSpace(s.Cfg.BillingBaseURL) != "" &&
		strings.TrimSpace(s.Cfg.BillingToken) != ""
}

// checkoutSelfActivation is the v1.1 D3 follow-up after a payment_paid fact
// landed on the checkout-ref projection (row state paid or activated):
//   - paid + handoff task committed (submitted/produced) → advance the row to
//     activated locally (制作工程确认开动);
//   - activated → send activation_active back to the producer (deterministic
//     bytes; replay-safe).
//
// It returns the (possibly advanced) row and ok=false when the send-back is
// retryably pending and the response has already been written (503) so the
// producer's channel retry heals the delivery.
func (s *Server) checkoutSelfActivation(w http.ResponseWriter, tenantID string, row store.BillingCheckoutRefRow) (store.BillingCheckoutRefRow, bool) {
	// (a) 本地事实:handoff 已提交任务 → paid 自主转 activated。produced 是
	// 提交后的产出态,任务当然已提交;recorded/failed/canceled 不构成开动。
	if row.State == billing.CheckoutStatePaid && strings.TrimSpace(row.HandoffID) != "" {
		if h, err := s.St.GetProductionHandoff(tenantID, row.HandoffID); err == nil &&
			(h.Status == store.ProductionStatusSubmitted || h.Status == store.ProductionStatusProduced) {
			advanced, _, aerr := s.St.MarkBillingCheckoutActivated(tenantID, row.CheckoutRef, nowRFC3339())
			if aerr == nil {
				row = advanced
				s.Log.Printf("billing_checkout_self_activated checkout_ref=%s handoff=%s", row.CheckoutRef, row.HandoffID)
			} else {
				// 推进失败保持 paid 如实(不伪造本地事实);回发跳过。
				s.Log.Printf("billing_checkout_self_activation_refused checkout_ref=%s err=%v", row.CheckoutRef, aerr)
			}
		}
	}
	if row.State != billing.CheckoutStateActivated {
		return row, true // 未达开动:保持 paid,响应照常
	}
	if !s.checkoutCallbackConfigured() {
		// 回发缝未配置:本地事实照落,回发诚实留痕跳过(producer 侧自身仍有
		// activation_active 权威事实流,链路不断)。
		s.Log.Printf("billing_checkout_callback_skipped checkout_ref=%s reason=unconfigured", row.CheckoutRef)
		return row, true
	}
	if err := s.postCheckoutActivationActive(row); err != nil {
		if errors.Is(err, errRetryableDelivery) {
			// 可重试失败:让 producer 重试→重放→本回发再触发(字节恒等)。
			s.Log.Printf("billing_checkout_callback_pending checkout_ref=%s err=%v", row.CheckoutRef, err)
			fail(w, http.StatusServiceUnavailable, "activation_callback_pending",
				"payment fact consumed; the activation_active send-back to the producer is retryably pending")
			return row, false
		}
		// 合同违例(4xx/409):重试不可修复;大声留痕后放行本请求,不把已
		// 消费的支付事实拖入无限 FAIL 循环。
		s.Log.Printf("billing_checkout_callback_violation checkout_ref=%s err=%v", row.CheckoutRef, err)
	}
	return row, true
}

// postCheckoutActivationActive delivers the deterministic activation fact to
// the producer's authority-events seam. nil = delivered (200/201);
// errRetryableDelivery = transport/5xx/429/408 (channel retry heals); any
// other error = contract violation (loud, not fixable by retry).
func (s *Server) postCheckoutActivationActive(row store.BillingCheckoutRefRow) error {
	body, err := json.Marshal(checkoutActivationWire{
		TenantID:   row.TenantID,
		OrderID:    row.OrderRef,
		Source:     checkoutActivationSource,
		EventID:    checkoutActivationEventIDPrefix + row.CheckoutRef,
		Type:       billing.EventActivationActive,
		OccurredAt: row.UpdatedAt, // 激活落行时刻:重发字节恒等(确定性纪律)
		Payload: billing.EventPayload{
			OrderRef:   row.OrderRef,
			PaymentRef: row.PaymentRef,
		},
		CheckoutRef: row.CheckoutRef,
	})
	if err != nil {
		return fmt.Errorf("%w: marshal: %v", errActivationDeliveryViolation, err)
	}
	req, err := http.NewRequest(http.MethodPost,
		strings.TrimRight(s.Cfg.BillingBaseURL, "/")+checkoutActivationEventsPath, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: build request: %v", errActivationDeliveryViolation, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Service-ID", checkoutActivationSource)
	req.Header.Set("X-Service-Token", s.Cfg.BillingToken)
	resp, err := checkoutActivationClient.Do(req)
	if err != nil {
		return errRetryableDelivery
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
	switch {
	case resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated:
		s.Log.Printf("billing_checkout_callback_delivered checkout_ref=%s event_id=%s",
			row.CheckoutRef, checkoutActivationEventIDPrefix+row.CheckoutRef)
		return nil
	case resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusRequestTimeout:
		return fmt.Errorf("%w: status %d", errRetryableDelivery, resp.StatusCode)
	default:
		return fmt.Errorf("%w: status %d", errActivationDeliveryViolation, resp.StatusCode)
	}
}
