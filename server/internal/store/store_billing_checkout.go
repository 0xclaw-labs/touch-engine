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
//     比对,不匹配不推进)。同 payment_ref 重放零新增零改写。唯一例外:
//     MarkBillingCheckoutActivated(统一契约 v1.1 D3)——touch 制作工程本地
//     事实(handoff 已提交任务)把 paid 推进 activated,幂等且只此一跃。
//   - 租户全封闭:一切读取都带 tenant_id;他租户/不存在同答 ErrNotFound。

package store

import (
	"database/sql"
	"errors"

	"github.com/bianjiefilm/touch-engine/server/internal/billing"
)

// BillingCheckoutRefRow mirrors one billing_checkout_refs row (0139 + 0141).
type BillingCheckoutRefRow struct {
	ID                 string `json:"id"`
	TenantID           string `json:"tenant_id"`
	HandoffID          string `json:"handoff_id,omitempty"`
	QuoteRef           string `json:"quote_ref,omitempty"`
	CheckoutRef        string `json:"checkout_ref"`
	OrderRef           string `json:"order_ref"`
	OrderKind          string `json:"order_kind"`
	State              string `json:"state"`
	AmountMinor        int64  `json:"amount_minor"`
	Currency           string `json:"currency"`
	PayerRef           string `json:"payer_ref,omitempty"`
	PayerGrantRef      string `json:"payer_grant_ref,omitempty"`
	PayerGrantRevision int64  `json:"payer_grant_revision,omitempty"`
	PricingVersion     string `json:"pricing_version,omitempty"`
	IntentKey          string `json:"intent_key,omitempty"`
	RemoteCheckoutRef  string `json:"remote_checkout_ref,omitempty"`
	OutTradeNo         string `json:"out_trade_no,omitempty"`
	Provider           string `json:"provider,omitempty"`
	RemoteStatus       string `json:"remote_status,omitempty"`
	ReturnTarget       string `json:"return_target,omitempty"`
	PriceDigest        string `json:"price_digest,omitempty"`
	ExpiresAt          string `json:"expires_at,omitempty"`
	PaymentRef         string `json:"payment_ref,omitempty"`
	FailReason         string `json:"fail_reason,omitempty"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
}

// BillingCheckoutRemoteMeta carries authoritative non-URL metadata returned by
// public-ai Mint/Inspect (code_url / launch_url / return_url NEVER land in DB).
type BillingCheckoutRemoteMeta struct {
	RemoteCheckoutRef string
	OutTradeNo        string
	Provider          string
	RemoteStatus      string
	ReturnTarget      string
	PriceDigest       string
	ExpiresAt         string
}

// NewBillingCheckoutRef is one merchant-initiated continuation registration.
// Everything is server-assembled (tenant/handoff/quote/amount/payer); the
// browser never supplies money facts.
type NewBillingCheckoutRef struct {
	TenantID           string
	HandoffID          string
	QuoteRef           string
	OrderRef           string
	OrderKind          string // content_production (default) | subscription | hardware
	AmountMinor        int64
	Currency           string
	PayerRef           string
	PayerGrantRef      string
	PayerGrantRevision int64
	PricingVersion     string
	IntentKey          string
	ReturnTarget       string
	// CheckoutRef 留空 = 本地铸造稳定的 ck_* 路由键;非空 = 显式登记(恢复/测试路径)。
	CheckoutRef string
}

const billingCheckoutRefCols = `id,tenant_id,handoff_id,quote_ref,checkout_ref,order_ref,order_kind,state,
amount_minor,currency,payer_ref,payer_grant_ref,payer_grant_revision,pricing_version,intent_key,
remote_checkout_ref,out_trade_no,provider,remote_status,return_target,price_digest,expires_at,
payment_ref,fail_reason,created_at,updated_at`

func scanBillingCheckoutRef(row interface{ Scan(...any) error }) (BillingCheckoutRefRow, error) {
	var r BillingCheckoutRefRow
	err := row.Scan(&r.ID, &r.TenantID, &r.HandoffID, &r.QuoteRef, &r.CheckoutRef, &r.OrderRef,
		&r.OrderKind, &r.State, &r.AmountMinor, &r.Currency, &r.PayerRef, &r.PayerGrantRef,
		&r.PayerGrantRevision, &r.PricingVersion, &r.IntentKey, &r.RemoteCheckoutRef, &r.OutTradeNo,
		&r.Provider, &r.RemoteStatus, &r.ReturnTarget, &r.PriceDigest, &r.ExpiresAt,
		&r.PaymentRef, &r.FailReason, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

func normalizedCheckoutOrderKind(kind string) string {
	if kind == "" {
		return "content_production"
	}
	return kind
}

// sameCheckoutRefIntent compares the immutable server-assembled facts of a
// continuation registration against the stored row. checkout_ref is compared
// only for explicit registrations (mint requests reuse the standing ref).
func sameCheckoutRefIntent(row BillingCheckoutRefRow, in NewBillingCheckoutRef, explicitRef bool) bool {
	same := row.AmountMinor == in.AmountMinor && row.Currency == in.Currency &&
		row.HandoffID == in.HandoffID && row.QuoteRef == in.QuoteRef &&
		normalizedCheckoutOrderKind(row.OrderKind) == normalizedCheckoutOrderKind(in.OrderKind)
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
	orderKind := normalizedCheckoutOrderKind(in.OrderKind)
	// Insert first obtains the write lock before reading (same order as
	// createBillingOrderTx): competing requests read the committed winner.
	result, err := s.DB.Exec(`INSERT INTO billing_checkout_refs
		(id,tenant_id,handoff_id,quote_ref,checkout_ref,order_ref,order_kind,state,
		 amount_minor,currency,payer_ref,payer_grant_ref,payer_grant_revision,pricing_version,intent_key,
		 remote_checkout_ref,out_trade_no,provider,remote_status,return_target,price_digest,expires_at,
		 payment_ref,fail_reason,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(tenant_id,order_ref) DO NOTHING`,
		newID("bcr_"), in.TenantID, in.HandoffID, in.QuoteRef, ref, in.OrderRef, orderKind,
		billing.CheckoutStatePending, in.AmountMinor, in.Currency, in.PayerRef, in.PayerGrantRef,
		in.PayerGrantRevision, in.PricingVersion, in.IntentKey, "", "", "", "", in.ReturnTarget, "", "",
		"", "", at, at)
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

// MarkBillingCheckoutActivated advances one paid continuation reference to
// activated on the TOUCH-LOCAL production fact (统一契约 v1.1 D3:制作 handoff
// 已提交任务=制作工程确认开动;回发 activation_active 由 HTTP 层负责,本方法
// 只落行)。幂等:已 activated = replay(read-back);paid 之外的状态拒绝
// (checkout_pending 未有支付事实,failed 的生效只能走权威 activation_active
// 补偿路径);并发推进以先落行为准,败者重读现状回答。
func (s *Store) MarkBillingCheckoutActivated(tenantID, checkoutRef, at string) (BillingCheckoutRefRow, bool, error) {
	row, err := s.GetBillingCheckoutRefByRef(tenantID, checkoutRef)
	if err != nil {
		return BillingCheckoutRefRow{}, false, err
	}
	if row.State == billing.CheckoutStateActivated {
		return row, true, nil
	}
	if row.State != billing.CheckoutStatePaid {
		return row, false, billing.ErrInvalidTransition
	}
	result, err := s.DB.Exec(
		`UPDATE billing_checkout_refs SET state=?,fail_reason=?,updated_at=?
		 WHERE tenant_id=? AND checkout_ref=? AND state=?`,
		billing.CheckoutStateActivated, "", at, tenantID, checkoutRef, billing.CheckoutStatePaid)
	if err != nil {
		return row, false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return row, false, err
	}
	if n == 0 {
		// 并发败者:另一请求已推进本行;重读以现状回答(不伪造 replay)。
		row2, gerr := s.GetBillingCheckoutRefByRef(tenantID, checkoutRef)
		if gerr != nil {
			return row, false, gerr
		}
		if row2.State == billing.CheckoutStateActivated {
			return row2, true, nil
		}
		return row, false, billing.ErrInvalidTransition
	}
	row.State = billing.CheckoutStateActivated
	row.FailReason = ""
	row.UpdatedAt = at
	return row, false, nil
}

// UpdateBillingCheckoutRemoteMetadata records non-URL authoritative metadata
// returned by public-ai Mint/Inspect (remote_checkout_ref/out_trade_no/provider/
// remote_status/return_target/price_digest/expires_at). It NEVER advances local
// payment/activation state and refuses if remote_checkout_ref would drift.
func (s *Store) UpdateBillingCheckoutRemoteMetadata(tenantID, checkoutRef string, meta BillingCheckoutRemoteMeta, at string) (BillingCheckoutRefRow, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return BillingCheckoutRefRow{}, err
	}
	defer tx.Rollback()
	row, err := scanBillingCheckoutRef(tx.QueryRow(
		`SELECT `+billingCheckoutRefCols+` FROM billing_checkout_refs WHERE tenant_id=? AND checkout_ref=?`,
		tenantID, checkoutRef))
	if errors.Is(err, sql.ErrNoRows) {
		return BillingCheckoutRefRow{}, ErrNotFound
	}
	if err != nil {
		return BillingCheckoutRefRow{}, err
	}
	if row.RemoteCheckoutRef != "" && meta.RemoteCheckoutRef != "" && row.RemoteCheckoutRef != meta.RemoteCheckoutRef {
		return row, ErrBillingIdempotencyConflict
	}
	next := row
	if meta.RemoteCheckoutRef != "" {
		next.RemoteCheckoutRef = meta.RemoteCheckoutRef
	}
	if meta.OutTradeNo != "" {
		next.OutTradeNo = meta.OutTradeNo
	}
	if meta.Provider != "" {
		next.Provider = meta.Provider
	}
	if meta.RemoteStatus != "" {
		next.RemoteStatus = meta.RemoteStatus
	}
	if meta.ReturnTarget != "" {
		next.ReturnTarget = meta.ReturnTarget
	}
	if meta.PriceDigest != "" {
		next.PriceDigest = meta.PriceDigest
	}
	if meta.ExpiresAt != "" {
		next.ExpiresAt = meta.ExpiresAt
	}
	// 无实质变化时零写入,避免覆盖激活落行时刻(updated_at 确定性纪律)。
	if next.RemoteCheckoutRef == row.RemoteCheckoutRef &&
		next.OutTradeNo == row.OutTradeNo &&
		next.Provider == row.Provider &&
		next.RemoteStatus == row.RemoteStatus &&
		next.ReturnTarget == row.ReturnTarget &&
		next.PriceDigest == row.PriceDigest &&
		next.ExpiresAt == row.ExpiresAt {
		return row, nil
	}
	// 已激活行保持原激活落行时间戳(§2b occurred_at 重发字节恒等)。
	updatedAt := at
	if row.State == billing.CheckoutStateActivated {
		updatedAt = row.UpdatedAt
	}
	if _, err := tx.Exec(`UPDATE billing_checkout_refs
		SET remote_checkout_ref=?,out_trade_no=?,provider=?,remote_status=?,
		    return_target=?,price_digest=?,expires_at=?,updated_at=?
		WHERE tenant_id=? AND checkout_ref=?`,
		next.RemoteCheckoutRef, next.OutTradeNo, next.Provider, next.RemoteStatus,
		next.ReturnTarget, next.PriceDigest, next.ExpiresAt, updatedAt, tenantID, checkoutRef); err != nil {
		return row, err
	}
	if err := tx.Commit(); err != nil {
		return BillingCheckoutRefRow{}, err
	}
	next.UpdatedAt = updatedAt
	return next, nil
}

// ListBillingCheckoutRefs lists tenant-scoped checkout continuation rows,
// optionally filtered by order_kind.
func (s *Store) ListBillingCheckoutRefs(tenantID, orderKind string) ([]BillingCheckoutRefRow, error) {
	q := `SELECT ` + billingCheckoutRefCols + ` FROM billing_checkout_refs WHERE tenant_id=?`
	args := []any{tenantID}
	if orderKind != "" {
		q += ` AND order_kind=?`
		args = append(args, orderKind)
	}
	q += ` ORDER BY created_at DESC, id LIMIT 200`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BillingCheckoutRefRow{}
	for rows.Next() {
		r, err := scanBillingCheckoutRef(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
