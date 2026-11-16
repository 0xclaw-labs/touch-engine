package store

import (
	"errors"
	"time"
)

// HUI-4664 原单 checkout 引用的存储面(迁移 0139)。
// 写入纪律:CheckoutRef/Status/ExpiresAt/RequestedBy/ReturnContext 五列
// 只由受信 checkout producer 合同的 adapter 路径写入(handlers_billing 的
// originalOrderCheckout);浏览器返回、success query、本地推算一律不得写。
// 同 (tenant_id, checkout_ref) 查回原单 = 响应丢失后的同 key 恢复读面。

// ErrCheckoutRefConflict 是同租户下 checkout_ref 已绑定其他订单时的错误:
// 一个支付引用只归属一个 Touch 原单,绑歪即拒绝(不覆盖、不抢注)。
var ErrCheckoutRefConflict = errors.New("store: checkout_ref already bound to another order")

// SetBillingOrderCheckout 把一次受信签发(或查回)的 checkout 引用钉到原单。
// 幂等:同 ref 重放同单无害;ref 已属他单返回 ErrCheckoutRefConflict。
func (s *Store) SetBillingOrderCheckout(tenantID, orderID string, in BillingOrderRow, at string) error {
	res, err := s.DB.Exec(`UPDATE billing_orders
		SET checkout_ref=?, checkout_status=?, checkout_expires_at=?, checkout_requested_by=?, return_context=?, updated_at=?
		WHERE tenant_id=? AND id=?
		  AND (checkout_ref='' OR checkout_ref=?)
		  AND NOT EXISTS (
		    SELECT 1 FROM billing_orders other
		    WHERE other.tenant_id=? AND other.checkout_ref=? AND other.id<>?
		  )`,
		in.CheckoutRef, in.CheckoutStatus, in.CheckoutExpiresAt, in.CheckoutRequestedBy, in.ReturnContext, at,
		tenantID, orderID, in.CheckoutRef,
		tenantID, in.CheckoutRef, orderID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrCheckoutRefConflict
	}
	return nil
}

// GetBillingOrderByCheckoutRef 以公共支付引用查回原单(同 key 恢复读面)。
// 查不到返回零值行与 nil——调用方按未知处理,不臆造订单。
func (s *Store) GetBillingOrderByCheckoutRef(tenantID, checkoutRef string) (BillingOrderRow, error) {
	if checkoutRef == "" {
		return BillingOrderRow{}, nil
	}
	return scanBillingOrder(s.DB.QueryRow(
		`SELECT `+billingOrderCols+` FROM billing_orders WHERE tenant_id=? AND checkout_ref=?`,
		tenantID, checkoutRef))
}

// BillingOrderCheckoutLive 报告订单当前 checkout 引用是否仍在有效期内。
// 空引用=从未签发(false);无期限声明(合同未给)=按未过期处理,由权威查单
// 裁决真到期——本地时间只做展示,不做资金裁决。
func BillingOrderCheckoutLive(order BillingOrderRow, now time.Time) bool {
	if order.CheckoutRef == "" {
		return false
	}
	if order.CheckoutExpiresAt == "" {
		return true
	}
	exp, err := time.Parse(time.RFC3339, order.CheckoutExpiresAt)
	if err != nil {
		return true
	}
	return now.Before(exp)
}
