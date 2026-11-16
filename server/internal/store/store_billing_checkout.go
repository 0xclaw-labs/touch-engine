// store_billing_checkout.go: billing_checkout_refs 持久层(HUI-4664 原单
// checkout 接续映射;R3 §2.1 的任务适配版,additive)。
//
// 纪律(裁定书/R3 实现方案):
//   - 本表是可重建投影,不是支付权威:写入只来自(a)租户内授权的续付登记
//     (UpsertBillingCheckoutRef,merchant 会话经服务端装配)与(b)受信权威缝
//     的事实同步(ApplyBillingCheckoutEvent,billing_authority 服务身份)。
//   - UNIQUE(tenant_id, order_ref) = 同单恢复复用同一引用:同单同参重放只
//     读回既有行;同单 checkout_ref 变化 = 合同违约(ref 恒定,换发只刷
//     URL),拒绝并保原行;同单金额/币种漂移 = 幂等冲突。
//   - 状态推进唯一入口 ApplyBillingCheckoutEvent → billing.ApplyCheckoutEvent
//     纯函数;payment_paid 先过 AdjudicatePaymentFact(金额/币种/引用逐字节
//     比对,不匹配不推进)。同 payment_ref 重放零新增零改写。
//   - 租户全封闭:一切读取都带 tenant_id;他租户/不存在同答 ErrNotFound。

package store

import (
	"database/sql"
	"errors"

	"github.com/bianjiefilm/touch-engine/server/internal/billing"
)

// BillingCheckoutRefRow mirrors one billing_checkout_refs row.
type BillingCheckoutRefRow struct {
	ID          string `json:"id"`
	TenantID    string `json:"tenant_id"`
	HandoffID   string `json:"handoff_id,omitempty"`
	QuoteRef    string `json:"quote_ref,omitempty"`
	CheckoutRef string `json:"checkout_ref"`
	OrderRef    string `json:"order_ref"`
	State       string `json:"state"`
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	PayerRef    string `json:"payer_ref,omitempty"`
	PaymentRef  string `json:"payment_ref,omitempty"`
	FailReason  string `json:"fail_reason,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// NewBillingCheckoutRef is one merchant-initiated continuation registration.
// Everything is server-assembled (tenant/handoff/quote/amount/payer); the
// browser never supplies money facts.
type NewBillingCheckoutRef struct {
	TenantID    string
	HandoffID   string
	QuoteRef    string
	OrderRef    string
	AmountMinor int64
	Currency    string
	PayerRef    string
	// CheckoutRef 留空 = 本地铸造(上游 PR-4 到位后改为透传公共侧返回引用,
	// 表结构与调用方零改动);非空 = 显式登记(恢复/测试路径)。
	CheckoutRef string
}

const billingCheckoutRefCols = `id,tenant_id,handoff_id,quote_ref,checkout_ref,order_ref,state,
amount_minor,currency,payer_ref,payment_ref,fail_reason,created_at,updated_at`

func scanBillingCheckoutRef(row interface{ Scan(...any) error }) (BillingCheckoutRefRow, error) {
	var r BillingCheckoutRefRow
	err := row.Scan(&r.ID, &r.TenantID, &r.HandoffID, &r.QuoteRef, &r.CheckoutRef, &r.OrderRef,
		&r.State, &r.AmountMinor, &r.Currency, &r.PayerRef, &r.PaymentRef, &r.FailReason,
		&r.CreatedAt, &r.UpdatedAt)
	return r, err
}

// sameCheckoutRefIntent compares the immutable server-assembled facts of a
// continuation registration against the stored row. checkout_ref is compared
// only for explicit registrations (mint requests reuse the standing ref).
func sameCheckoutRefIntent(row BillingCheckoutRefRow, in NewBillingCheckoutRef, explicitRef bool) bool {
	same := row.AmountMinor == in.AmountMinor && row.Currency == in.Currency &&
		row.HandoffID == in.HandoffID && row.QuoteRef == in.QuoteRef
	if explicitRef {
		same = same && row.CheckoutRef == in.CheckoutRef
	}
	return same
}

// UpsertBillingCheckoutRef registers (or reads back) the tenant's single
// continuation reference for one original order. Same order + same immutable
// facts = replay of the existing row (replayed=true; mint requests reuse the
// standing ref — 同单恢复复用同一引用); same order with different facts or a
// different explicit checkout_ref is a contract violation and refuses while
// keeping the original row.
func (s *Store) UpsertBillingCheckoutRef(in NewBillingCheckoutRef, at string) (BillingCheckoutRefRow, bool, error) {
	if in.TenantID == "" || in.OrderRef == "" || in.AmountMinor < 0 {
		return BillingCheckoutRefRow{}, false, ErrBillingBadInput
	}
	explicitRef := in.CheckoutRef != ""
	ref := in.CheckoutRef
	if ref == "" {
		ref = newID("ck_")
	}
	// Insert first obtains the write lock before reading (same order as
	// createBillingOrderTx): competing requests read the committed winner.
	result, err := s.DB.Exec(`INSERT INTO billing_checkout_refs
		(id,tenant_id,handoff_id,quote_ref,checkout_ref,order_ref,state,amount_minor,currency,payer_ref,payment_ref,fail_reason,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(tenant_id,order_ref) DO NOTHING`,
		newID("bcr_"), in.TenantID, in.HandoffID, in.QuoteRef, ref, in.OrderRef,
		billing.CheckoutStatePending, in.AmountMinor, in.Currency, in.PayerRef, "", "", at, at)
	if err != nil {
		return BillingCheckoutRefRow{}, false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return BillingCheckoutRefRow{}, false, err
	}
	row, err := s.GetBillingCheckoutRefByOrder(in.TenantID, in.OrderRef)
	if err != nil {
		return BillingCheckoutRefRow{}, false, err
	}
	if n == 0 {
		// 同单已有行:重放必须逐字段一致(金额/币种/报价/挂接不可漂移);
		// 显式引用不一致 = 合同违约(引用恒定),拒绝并保原行;铸造请求
		// 复用既有引用(并发下先到者为准)。
		if !sameCheckoutRefIntent(row, in, explicitRef) {
			return BillingCheckoutRefRow{}, false, ErrBillingIdempotencyConflict
		}
		return row, true, nil
	}
	return row, false, nil
}

// GetBillingCheckoutRefByOrder fetches the tenant's continuation ref for one
// original order (同单恢复读回); foreign tenants / unknown orders are
// ErrNotFound (同形掩蔽).
func (s *Store) GetBillingCheckoutRefByOrder(tenantID, orderRef string) (BillingCheckoutRefRow, error) {
	row, err := scanBillingCheckoutRef(s.DB.QueryRow(
		`SELECT `+billingCheckoutRefCols+` FROM billing_checkout_refs WHERE tenant_id=? AND order_ref=?`,
		tenantID, orderRef))
	if errors.Is(err, sql.ErrNoRows) {
		return BillingCheckoutRefRow{}, ErrNotFound
	}
	return row, err
}

// GetBillingCheckoutRefByHandoff fetches the continuation ref recorded for a
// content-production handoff (tenant-scoped).
func (s *Store) GetBillingCheckoutRefByHandoff(tenantID, handoffID string) (BillingCheckoutRefRow, error) {
	row, err := scanBillingCheckoutRef(s.DB.QueryRow(
		`SELECT `+billingCheckoutRefCols+` FROM billing_checkout_refs WHERE tenant_id=? AND handoff_id=?`,
		tenantID, handoffID))
	if errors.Is(err, sql.ErrNoRows) {
		return BillingCheckoutRefRow{}, ErrNotFound
	}
	return row, err
}

// GetBillingCheckoutRefByRef fetches one row by its checkout ref inside the
// tenant scope (the trusted authority seam's lookup key).
func (s *Store) GetBillingCheckoutRefByRef(tenantID, checkoutRef string) (BillingCheckoutRefRow, error) {
	row, err := scanBillingCheckoutRef(s.DB.QueryRow(
		`SELECT `+billingCheckoutRefCols+` FROM billing_checkout_refs WHERE tenant_id=? AND checkout_ref=?`,
		tenantID, checkoutRef))
	if errors.Is(err, sql.ErrNoRows) {
		return BillingCheckoutRefRow{}, ErrNotFound
	}
	return row, err
}

// ApplyBillingCheckoutEvent is the checkout-ref state advance entry for the
// trusted authority seam: dedup by payment_ref on the row (同 payment_ref
// 重放零新增零改写) → payment_paid adjudication → pure machine → row update,
// all in one transaction. Returns replayed=true when the event changed nothing.
func (s *Store) ApplyBillingCheckoutEvent(tenantID, checkoutRef string, in billing.AuthorityEventInput, at string) (BillingCheckoutRefRow, bool, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return BillingCheckoutRefRow{}, false, err
	}
	defer tx.Rollback()
	row, err := scanBillingCheckoutRef(tx.QueryRow(
		`SELECT `+billingCheckoutRefCols+` FROM billing_checkout_refs WHERE tenant_id=? AND checkout_ref=?`,
		tenantID, checkoutRef))
	if errors.Is(err, sql.ErrNoRows) {
		return BillingCheckoutRefRow{}, false, ErrNotFound
	}
	if err != nil {
		return BillingCheckoutRefRow{}, false, err
	}
	if in.Type == billing.EventPaymentPaid {
		verdict := billing.AdjudicatePaymentFact(billing.CheckoutRefState{
			ID: row.ID, TenantID: row.TenantID, CheckoutRef: row.CheckoutRef, OrderRef: row.OrderRef,
			State: row.State, AmountMinor: row.AmountMinor, Currency: row.Currency,
			PaymentRef: row.PaymentRef, FailReason: row.FailReason,
		}, billing.PaymentFact{
			PaymentRef: in.Payload.PaymentRef, CheckoutRef: checkoutRef,
			AmountMinor: in.AmountMinor, Currency: in.Currency,
		})
		if !verdict.Allowed {
			// 金额/币种/引用不匹配:不推进、不改写(宁可挂起不可错开);
			// 生产方按 409 event_mismatch 修正后重发。
			return row, false, billing.ErrEventMismatch
		}
	}
	state := billing.CheckoutRefState{
		ID: row.ID, TenantID: row.TenantID, CheckoutRef: row.CheckoutRef, OrderRef: row.OrderRef,
		State: row.State, AmountMinor: row.AmountMinor, Currency: row.Currency,
		PaymentRef: row.PaymentRef, FailReason: row.FailReason,
	}
	next, err := billing.ApplyCheckoutEvent(state, in)
	if err != nil {
		return row, false, err
	}
	if next.State == row.State && next.PaymentRef == row.PaymentRef && next.FailReason == row.FailReason {
		return row, true, nil // 幂等重放:零改写
	}
	if _, err := tx.Exec(`UPDATE billing_checkout_refs SET state=?,payment_ref=?,fail_reason=?,updated_at=?
		WHERE tenant_id=? AND checkout_ref=?`,
		next.State, next.PaymentRef, next.FailReason, at, tenantID, checkoutRef); err != nil {
		return row, false, err
	}
	if err := tx.Commit(); err != nil {
		return BillingCheckoutRefRow{}, false, err
	}
	row.State = next.State
	row.PaymentRef = next.PaymentRef
	row.FailReason = next.FailReason
	row.UpdatedAt = at
	return row, false, nil
}
