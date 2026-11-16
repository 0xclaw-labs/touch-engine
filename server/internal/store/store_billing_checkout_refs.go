package store

import (
	"database/sql"
	"errors"
	"strings"
)

// HUI-4664 / touch-order-checkout v1.1：Touch↔公共收银台映射投影的存储面。
// 合同纪律（contract.md §1 两侧职责 / §0 统一契约）：
//   - UNIQUE(tenant_id, order_id) 一单一行、ref 恒定：同原单重试/查回/续付
//     只读回同一映射，绝不换键。
//   - checkout_ref = Touch 本地续付引用 ck_*（双向事件主路由键）；
//     remote_checkout_ref = producer mint 签发的 tco_*（向公共侧查单/回发键）。
//   - paid 只由 payment_paid gating 事件（金额裁决通过）推进；activated 只由
//     activation_active 回发成功推进；浏览器返回/success query 一律不写。

// ErrCheckoutRefMapState 是映射行状态推进非法（终态回退/重复推进）的错误。
var ErrCheckoutRefMapState = errors.New("store: billing_checkout_refs state transition rejected")

// BillingCheckoutRefRow 是一单的收银映射投影。
type BillingCheckoutRefRow struct {
	TenantID           string
	OrderID            string
	CheckoutRef        string // ck_*（主路由键）
	RemoteCheckoutRef  string // tco_*（producer 支付引用）
	Status             string // pending | paid | activated | closed
	AmountMinor        int64
	Currency           string
	ReturnContext      string
	CreatedAt          string
	UpdatedAt          string
}

const billingCheckoutRefCols = `tenant_id,order_id,checkout_ref,remote_checkout_ref,status,
amount_minor,currency,return_context,created_at,updated_at`

func scanBillingCheckoutRef(row *sql.Row) (BillingCheckoutRefRow, error) {
	var r BillingCheckoutRefRow
	err := row.Scan(&r.TenantID, &r.OrderID, &r.CheckoutRef, &r.RemoteCheckoutRef, &r.Status,
		&r.AmountMinor, &r.Currency, &r.ReturnContext, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

// UpsertBillingCheckoutRef 绑定/刷新一单映射。一单一行：同 (tenant,order)
// 的 ck_* 与既有不同 = 拒绝（ref 恒定合同，绝不换键）。
func (s *Store) UpsertBillingCheckoutRef(r BillingCheckoutRefRow, at string) (BillingCheckoutRefRow, error) {
	if strings.TrimSpace(r.CheckoutRef) == "" || strings.TrimSpace(r.OrderID) == "" ||
		strings.TrimSpace(r.Status) == "" {
		return BillingCheckoutRefRow{}, ErrCheckoutRefMapState
	}
	// ref 恒定合同显式预检：同单已有不同 ck_* → 拒绝（SQLite 在 ON CONFLICT
	// 前先验行 CHECK，靠隐式报错会把「换键」伪装成约束错）。
	existing, err := s.GetBillingCheckoutRef(r.TenantID, r.OrderID)
	if err != nil {
		return BillingCheckoutRefRow{}, err
	}
	if existing.CheckoutRef != "" && existing.CheckoutRef != r.CheckoutRef {
		return BillingCheckoutRefRow{}, ErrCheckoutRefMapState
	}
	_, err = s.DB.Exec(`INSERT INTO billing_checkout_refs(
		tenant_id, order_id, checkout_ref, remote_checkout_ref, status,
		amount_minor, currency, return_context, created_at, updated_at)
	VALUES(?,?,?,?,?,?,?,?,?,?)
	ON CONFLICT(tenant_id, order_id) DO UPDATE SET
		remote_checkout_ref = CASE WHEN excluded.remote_checkout_ref != '' THEN excluded.remote_checkout_ref ELSE billing_checkout_refs.remote_checkout_ref END,
		status = CASE WHEN billing_checkout_refs.status IN ('pending') AND excluded.status != 'pending' THEN excluded.status ELSE billing_checkout_refs.status END,
		updated_at = excluded.updated_at`,
		r.TenantID, r.OrderID, r.CheckoutRef, r.RemoteCheckoutRef, r.Status,
		r.AmountMinor, r.Currency, r.ReturnContext, at, at)
	if err != nil {
		return BillingCheckoutRefRow{}, err
	}
	return s.GetBillingCheckoutRef(r.TenantID, r.OrderID)
}

// GetBillingCheckoutRef 按原单读映射。无映射返回零值行 + nil（未接线单）。
func (s *Store) GetBillingCheckoutRef(tenantID, orderID string) (BillingCheckoutRefRow, error) {
	row := s.DB.QueryRow(`SELECT `+billingCheckoutRefCols+` FROM billing_checkout_refs
		WHERE tenant_id=? AND order_id=?`, tenantID, orderID)
	r, err := scanBillingCheckoutRef(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return BillingCheckoutRefRow{}, nil
		}
		return BillingCheckoutRefRow{}, err
	}
	return r, nil
}

// GetBillingCheckoutRefByRef 以 ck_*（或 tco_*）读映射——事件路由面：
// payment_paid 顶层 checkout_ref=ck_*；refund 不带 checkout_ref 按
// order_id 路由；两个 UNIQUE 都可查。
func (s *Store) GetBillingCheckoutRefByRef(tenantID, ref string) (BillingCheckoutRefRow, error) {
	row := s.DB.QueryRow(`SELECT `+billingCheckoutRefCols+` FROM billing_checkout_refs
		WHERE tenant_id=? AND (checkout_ref=? OR remote_checkout_ref=?)`, tenantID, ref, ref)
	r, err := scanBillingCheckoutRef(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return BillingCheckoutRefRow{}, nil
		}
		return BillingCheckoutRefRow{}, err
	}
	return r, nil
}

// AdvanceBillingCheckoutRefPaid 推进 paid（payment_paid gating 裁决通过的
// 唯一写入口）。终态（activated/closed）不得回退 paid。
func (s *Store) AdvanceBillingCheckoutRefPaid(tenantID, orderID, at string) error {
	res, err := s.DB.Exec(`UPDATE billing_checkout_refs SET status='paid', updated_at=?
		WHERE tenant_id=? AND order_id=? AND status='pending'`, at, tenantID, orderID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrCheckoutRefMapState
	}
	return nil
}

// AdvanceBillingCheckoutRefActivated 推进 activated（activation_active 回发
// 成功后；要求已 paid——未收钱先"激活"是资金异常，拒绝）。
func (s *Store) AdvanceBillingCheckoutRefActivated(tenantID, orderID, at string) error {
	res, err := s.DB.Exec(`UPDATE billing_checkout_refs SET status='activated', updated_at=?
		WHERE tenant_id=? AND order_id=? AND status='paid'`, at, tenantID, orderID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrCheckoutRefMapState
	}
	return nil
}

// NewTouchCheckoutRef 铸造本地续付引用 ck_*：合同 §0 主路由键，32 hex
// 熵（一次性凭据纪律同 identity handoff binding）。走 store 既有 newID
// 随机器（前缀改造），熵不可得时显式降级（调用方按重复冲突拒绝）。
func NewTouchCheckoutRef() string {
	return newID("ck_")
}
