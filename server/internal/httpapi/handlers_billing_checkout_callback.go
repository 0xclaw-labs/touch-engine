// handlers_billing_checkout_callback.go: HUI-4664 原单收银接续的 activation
// 回发通道(ADV-9 统一契约 v1.1 D3)。
//
// 语义(v1.1 注记):Touch 内容制作履约是异步工程事实——paid=付款完成可开工
//(制作任务在发起时已提交);activated=制作工程确认开动(本地事实)。当
// payment_paid 把接续引用落到 paid、且制作 handoff 已处于 submitted(或其后
// 的 produced)态,Touch 自主把行推进 activated,并把 activation_active 回发
// 给 producer,供其结算行收口。
//
// 回发通道(v1.1 §2b 定谳,ADV-9 FIX-C/D5 收敛):producer 冻结了专用接收端
// 点——POST {PLATFORM_BILLING_BASE_URL}/internal/v1/billing/touch-orders/
// activation-active(touch-order-checkout/v1 合同 §2b),挂 /internal/v1/
// billing/ 既有 app token 闸:头 X-App-ID: touch-engine + 其绑定 token
// (X-PilotSeaview-Internal-Token)。凭据=PLATFORM_BILLING_TOKEN——与
// copyjob 平台缝同源:该 env 在本仓的既有语义就是 touch-engine 在公共结算
// 侧 /internal/v1/billing/ 前缀的 app token(POST unified/usage、unified/
// quote 同一闸同一头族);producer 侧 BILLING_APP_TOKENS=touch-engine:<同一
// 值>。请求体=§2b 冻结形状(checkout_ref 主路由键 + source_order_ref/
// occurred_at 等回声;producer DisallowUnknownFields,多一个字段即 400)。
// 未配置=诚实跳过回发,本地事实照落。
//
// 确定性纪律(幂等重放的前提,§2b 适配):event_id=touch-act-<checkout_ref>
// 恒定(留痕与身份空间),occurred_at=行的 updated_at(激活落行时刻)——重发
// (渠道重试触发的 payment_paid 重放)字节恒等;producer 按 checkout_ref 幂
// 等落章(activated_at_unix=0 守卫单次落章,重放答 replayed=true)。回发可
// 重试失败(网络/5xx/429/408)时本请求答 503,让 producer 按既有 webhook
// FAIL→重试→replay 姿态回来补投;4xx/409=合同违例(含 404=producer 未知
// checkout_ref,照实留痕),大声留痕后放行本请求(重试不可修复,不得把已
// 消费的支付事实拖入无限 FAIL)。
package httpapi

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

// checkoutActivationSource is the app identity Touch presents on the send-back
// (v1.1 §2b: X-App-ID=touch-engine); the token is the touch-engine app token
// at the public settlement side (PLATFORM_BILLING_TOKEN — the same credential
// this repo already presents as the app token on the /internal/v1/billing/
// prefix, copyjob platform seam precedent).
const checkoutActivationSource = "touch-engine"

// checkoutActivationEventIDPrefix + path: stable fact id (log/identity space)
// and the frozen §2b producer endpoint (touch-order-checkout/v1 contract §2b).
const (
	checkoutActivationEventIDPrefix = "touch-act-"
	checkoutActivationActivePath    = "/internal/v1/billing/touch-orders/activation-active"
)

// Send-back credential headers = the /internal/v1/billing/ app family (same
// headers the copyjob platform seam already uses for this prefix).
const (
	checkoutActivationAppHeader   = "X-App-ID"
	checkoutActivationTokenHeader = "X-PilotSeaview-Internal-Token"
)

// checkoutActivationCallTimeout bounds one send-back attempt; a wedged
// producer must turn into a retryable answer (503), not a hung seam request.
const checkoutActivationCallTimeout = 5 * time.Second

var checkoutActivationClient = &http.Client{Timeout: checkoutActivationCallTimeout}

// checkoutActivationWire is the send-back body: the frozen §2b shape
// (touchActivationActiveRequest on the producer side — DisallowUnknownFields,
// so exactly these five fields; an extra field is a 400). checkout_ref is the
// primary routing key; source_order_ref/occurred_at are echoes of Touch's own
// mapping (logged verbatim by the producer, never re-adjudicated).
type checkoutActivationWire struct {
	CheckoutRef     string `json:"checkout_ref"`
	SourceOrderRef  string `json:"source_order_ref"`
	OccurredAt      string `json:"occurred_at"`
	SubscriptionRef string `json:"subscription_ref"`
	PaidUntil       string `json:"paid_until"`
}

// checkoutCallbackConfigured answers whether the send-back channel can
// deliver: either an injected TouchCheckout client is present, or the checkout
// capability is on AND the producer base URL AND Touch's registered token are configured.
func (s *Server) checkoutCallbackConfigured() bool {
	if s.TouchCheckout != nil {
		return true
	}
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
// the producer's frozen §2b endpoint. nil = delivered (200/201);
// errRetryableDelivery = transport/5xx/429/408 (channel retry heals); any
// other error = contract violation (loud, not fixable by retry) — including
// 404, the producer honestly reporting an unknown checkout_ref.
func (s *Server) postCheckoutActivationActive(row store.BillingCheckoutRefRow) error {
	return s.postCheckoutActivationActiveWithSubscription(row, "", "")
}

func (s *Server) postCheckoutActivationActiveWithSubscription(row store.BillingCheckoutRefRow, subscriptionRef, paidUntil string) error {
	eventID := checkoutActivationEventIDPrefix + row.CheckoutRef
	if s.TouchCheckout != nil {
		_, err := s.TouchCheckout.NotifyActivated(context.Background(), billing.TouchActivationActiveNotice{
			CheckoutRef:     row.CheckoutRef,
			SourceOrderRef:  row.OrderRef,
			OccurredAt:      row.UpdatedAt,
			SubscriptionRef: subscriptionRef,
			PaidUntil:       paidUntil,
		})
		if err == nil {
			s.Log.Printf("billing_checkout_callback_delivered checkout_ref=%s event_id=%s", row.CheckoutRef, eventID)
			return nil
		}
		if errors.Is(err, billing.ErrRemoteCheckoutUnavailable) {
			return fmt.Errorf("%w: %v", errRetryableDelivery, err)
		}
		return fmt.Errorf("%w: %v", errActivationDeliveryViolation, err)
	}
	body, err := json.Marshal(checkoutActivationWire{
		CheckoutRef:     row.CheckoutRef,
		SourceOrderRef:  row.OrderRef,  // 原单引用回声(制作接续=cph_* handoff id; 订阅/硬件=bo_*)
		OccurredAt:      row.UpdatedAt, // 激活落行时刻:重发字节恒等(确定性纪律)
		SubscriptionRef: subscriptionRef,
		PaidUntil:       paidUntil,
	})
	if err != nil {
		return fmt.Errorf("%w: marshal: %v", errActivationDeliveryViolation, err)
	}
	req, err := http.NewRequest(http.MethodPost,
		strings.TrimRight(s.Cfg.BillingBaseURL, "/")+checkoutActivationActivePath, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: build request: %v", errActivationDeliveryViolation, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(checkoutActivationAppHeader, checkoutActivationSource)
	req.Header.Set(checkoutActivationTokenHeader, s.Cfg.BillingToken)
	resp, err := checkoutActivationClient.Do(req)
	if err != nil {
		return errRetryableDelivery
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
	switch {
	case resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated:
		s.Log.Printf("billing_checkout_callback_delivered checkout_ref=%s event_id=%s", row.CheckoutRef, eventID)
		return nil
	case resp.StatusCode == http.StatusNotFound:
		// producer 照实报告未知 checkout_ref:照实留痕(重试不可修复,归违例类)。
		return fmt.Errorf("%w: producer_unknown_checkout_ref status 404 event_id=%s", errActivationDeliveryViolation, eventID)
	case resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusRequestTimeout:
		return fmt.Errorf("%w: status %d", errRetryableDelivery, resp.StatusCode)
	default:
		return fmt.Errorf("%w: status %d", errActivationDeliveryViolation, resp.StatusCode)
	}
}
