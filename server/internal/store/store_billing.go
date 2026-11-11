// store_billing.go: HUI-4591 订购收费账单的 SQL 层(可重建投影,租户全封闭)。
//
// 纪律(裁定书):
//   - 一切写入由受信权威缝或租户内授权动作触发;幂等键 (tenant, idempotency_key)
//     与事件去重 (tenant, source, event_id) 是唯一防重复机制——重放只读回,
//     同键不同负载拒绝 ErrBillingIdempotencyConflict。
//   - 任何函数都不维护余额;金额列只是订单/投影的快照事实。
//   - 状态推进唯一入口 ApplyBillingOrderEvent(billing.ApplyOrderEvent 纯函数)。

package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/billing"
)

// Billing-domain errors (HTTP 层按码分诊)。
var (
	ErrBillingIdempotencyConflict = errors.New("store: billing_idempotency_conflict")
	ErrBillingQuoteNotConsumable  = errors.New("store: billing_quote_not_consumable")
	ErrBillingBadInput            = errors.New("store: billing_bad_input")
)

// BillingOrderRow mirrors one billing_orders row.
type BillingOrderRow struct {
	ID                string `json:"id"`
	TenantID          string `json:"tenant_id"`
	Kind              string `json:"kind"`
	Action            string `json:"action"`
	PlanCode          string `json:"plan_code"`
	PriceRef          string `json:"price_ref"`
	PriceSnapshot     string `json:"price_snapshot"`
	AmountMinor       int64  `json:"amount_minor"`
	Currency          string `json:"currency"`
	Status            string `json:"status"`
	OrderRef          string `json:"order_ref"`
	PaymentRef        string `json:"payment_ref"`
	IdempotencyKey    string `json:"idempotency_key"`
	PayerGrantRef     string `json:"payer_grant_ref,omitempty"`
	AgencyRelationRef string `json:"agency_relation_ref,omitempty"`
	PayerPrincipal    string `json:"payer_principal,omitempty"`
	ActorPrincipal    string `json:"actor_principal"`
	ActorRole         string `json:"actor_role"`
	FailReason        string `json:"fail_reason,omitempty"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
}

// NewBillingOrder is a merchant-initiated order (price snapshot attached).
type NewBillingOrder struct {
	TenantID          string
	Kind              string
	Action            string
	PlanCode          string
	PriceRef          string
	PriceSnapshot     string
	AmountMinor       int64
	Currency          string
	IdempotencyKey    string
	PayerGrantRef     string
	AgencyRelationRef string
	PayerPrincipal    string
	ActorPrincipal    string
	ActorRole         string
}

func scanBillingOrder(row interface{ Scan(...any) error }) (BillingOrderRow, error) {
	var r BillingOrderRow
	err := row.Scan(&r.ID, &r.TenantID, &r.Kind, &r.Action, &r.PlanCode, &r.PriceRef,
		&r.PriceSnapshot, &r.AmountMinor, &r.Currency, &r.Status, &r.OrderRef, &r.PaymentRef,
		&r.IdempotencyKey, &r.PayerGrantRef, &r.AgencyRelationRef, &r.PayerPrincipal,
		&r.ActorPrincipal, &r.ActorRole, &r.FailReason, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

const billingOrderCols = `id,tenant_id,kind,action,plan_code,price_ref,price_snapshot,
amount_minor,currency,status,order_ref,payment_ref,idempotency_key,
payer_grant_ref,agency_relation_ref,payer_principal,actor_principal,actor_role,
fail_reason,created_at,updated_at`

// CreateBillingOrder inserts one processing order, or replays the row that the
// idempotency key already owns (replayed=true). Never creates a second order
// for the same key.
func (s *Store) CreateBillingOrder(in NewBillingOrder, at string) (BillingOrderRow, bool, error) {
	if strings.TrimSpace(in.IdempotencyKey) == "" || strings.TrimSpace(in.TenantID) == "" {
		return BillingOrderRow{}, false, ErrBillingBadInput
	}
	var existing BillingOrderRow
	existing, err := scanBillingOrder(s.DB.QueryRow(
		`SELECT `+billingOrderCols+` FROM billing_orders WHERE tenant_id=? AND idempotency_key=?`,
		in.TenantID, in.IdempotencyKey))
	if err == nil {
		return existing, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return BillingOrderRow{}, false, err
	}
	id := newID("bo_")
	_, err = s.DB.Exec(`INSERT INTO billing_orders(`+billingOrderCols+`)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, in.TenantID, in.Kind, in.Action, in.PlanCode, in.PriceRef, in.PriceSnapshot,
		in.AmountMinor, in.Currency, billing.OrderProcessing, "", "", in.IdempotencyKey,
		in.PayerGrantRef, in.AgencyRelationRef, in.PayerPrincipal,
		in.ActorPrincipal, in.ActorRole, "", at, at)
	if err != nil {
		return BillingOrderRow{}, false, err
	}
	row, err := s.GetBillingOrder(in.TenantID, id)
	return row, false, err
}

// GetBillingOrder fetches one order inside the tenant scope only.
func (s *Store) GetBillingOrder(tenantID, id string) (BillingOrderRow, error) {
	return scanBillingOrder(s.DB.QueryRow(
		`SELECT `+billingOrderCols+` FROM billing_orders WHERE tenant_id=? AND id=?`, tenantID, id))
}

// ListBillingOrders lists tenant orders, optionally filtered by kind.
func (s *Store) ListBillingOrders(tenantID, kind string) ([]BillingOrderRow, error) {
	q := `SELECT ` + billingOrderCols + ` FROM billing_orders WHERE tenant_id=?`
	args := []any{tenantID}
	if kind != "" {
		q += ` AND kind=?`
		args = append(args, kind)
	}
	q += ` ORDER BY created_at DESC, id LIMIT 200`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BillingOrderRow{}
	for rows.Next() {
		r, err := scanBillingOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// orderEventPayloadJSON canonicalizes an event for dedup payload equality.
func orderEventPayloadJSON(in billing.AuthorityEventInput) (string, error) {
	b, err := json.Marshal(map[string]any{
		"type": in.Type, "occurred_at": in.OccurredAt, "payload": in.Payload,
	})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ApplyBillingOrderEvent is the ONLY state-advance entry: dedup (source,event_id)
// → pure machine → order update (+ subscription projection / hardware payment
// fact) → event row, all in one transaction. Same key + same payload = replay;
// same key + different payload = ErrBillingIdempotencyConflict.
func (s *Store) ApplyBillingOrderEvent(tenantID, orderID string, in billing.AuthorityEventInput, at string) (BillingOrderRow, bool, error) {
	payloadJSON, err := orderEventPayloadJSON(in)
	if err != nil {
		return BillingOrderRow{}, false, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return BillingOrderRow{}, false, err
	}
	defer tx.Rollback()

	var prevPayload string
	err = tx.QueryRow(`SELECT payload FROM billing_order_events WHERE tenant_id=? AND source=? AND event_id=?`,
		tenantID, in.Source, in.EventID).Scan(&prevPayload)
	if err == nil {
		if prevPayload != payloadJSON {
			return BillingOrderRow{}, false, ErrBillingIdempotencyConflict
		}
		row, gerr := scanBillingOrder(tx.QueryRow(
			`SELECT `+billingOrderCols+` FROM billing_orders WHERE tenant_id=? AND id=?`, tenantID, orderID))
		if gerr != nil {
			return BillingOrderRow{}, false, gerr
		}
		return row, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return BillingOrderRow{}, false, err
	}

	row, err := scanBillingOrder(tx.QueryRow(
		`SELECT `+billingOrderCols+` FROM billing_orders WHERE tenant_id=? AND id=?`, tenantID, orderID))
	if err != nil {
		return BillingOrderRow{}, false, err
	}
	state := billing.OrderState{
		ID: row.ID, TenantID: row.TenantID, Kind: row.Kind, Action: row.Action,
		PlanCode: row.PlanCode, PriceRef: row.PriceRef, PriceSnapshot: row.PriceSnapshot,
		AmountMinor: row.AmountMinor, Currency: row.Currency, Status: row.Status,
		OrderRef: row.OrderRef, PaymentRef: row.PaymentRef, FailReason: row.FailReason,
	}
	next, result, err := billing.ApplyOrderEvent(state, in)
	if err != nil {
		return row, false, err
	}
	if _, err := tx.Exec(`UPDATE billing_orders SET status=?,order_ref=?,payment_ref=?,fail_reason=?,updated_at=?
		WHERE tenant_id=? AND id=?`,
		next.Status, next.OrderRef, next.PaymentRef, next.FailReason, at, tenantID, orderID); err != nil {
		return row, false, err
	}
	// activation_active:重建订阅投影(整行刷新;权威在生态,本行可随时重建)。
	if result.Subscription != nil {
		if err := upsertSubscriptionTx(tx, tenantID, orderID, *result.Subscription, at); err != nil {
			return row, false, err
		}
	}
	// 硬件订单:支付事实同时落履约首步(操作者=权威服务标识,可回查)。
	if next.Kind == billing.KindHardware && in.Type == billing.EventPaymentPaid {
		if err := upsertHardwareStepTx(tx, tenantID, orderID, billing.HardwareStepInput{
			Step: billing.StepPayment, Operator: "authority:" + in.Source, OccurredAt: in.OccurredAt,
		}, at); err != nil {
			return row, false, err
		}
	}
	if _, err := tx.Exec(`INSERT INTO billing_order_events(id,tenant_id,order_id,source,event_id,event_type,payload,occurred_at,created_at)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		newID("boe_"), tenantID, orderID, in.Source, in.EventID, in.Type, payloadJSON, in.OccurredAt, at); err != nil {
		return row, false, err
	}
	if err := tx.Commit(); err != nil {
		return BillingOrderRow{}, false, err
	}
	row.Status = next.Status
	row.OrderRef = next.OrderRef
	row.PaymentRef = next.PaymentRef
	row.FailReason = next.FailReason
	return row, false, nil
}

func upsertSubscriptionTx(tx *sql.Tx, tenantID, orderID string, u billing.SubscriptionProjectionUpdate, at string) error {
	if u.EntitlementProjection == "" {
		u.EntitlementProjection = "{}"
	}
	_, err := tx.Exec(`INSERT INTO billing_subscriptions
		(tenant_id,subscription_ref,plan_code,pricing_version,status,paid_until,grace_days,store_limit,entitlement_projection,last_order_id,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(tenant_id) DO UPDATE SET
		subscription_ref=excluded.subscription_ref,plan_code=excluded.plan_code,
		pricing_version=excluded.pricing_version,status=excluded.status,
		paid_until=excluded.paid_until,grace_days=excluded.grace_days,
		store_limit=excluded.store_limit,entitlement_projection=excluded.entitlement_projection,
		last_order_id=excluded.last_order_id,updated_at=excluded.updated_at`,
		tenantID, u.SubscriptionRef, u.PlanCode, u.PricingVersion, billing.SubStatusActive,
		u.PaidUntil, u.GraceDays, u.StoreLimit, u.EntitlementProjection, orderID, at, at)
	return err
}

// ---- subscription projection -------------------------------------------------

// GetBillingSubscription returns the tenant's subscription projection;
// store.ErrNotFound when the tenant has no row (caller falls back honestly).
func (s *Store) GetBillingSubscription(tenantID string) (billing.SubscriptionFact, error) {
	var f billing.SubscriptionFact
	err := s.DB.QueryRow(`SELECT tenant_id,subscription_ref,plan_code,pricing_version,status,paid_until,
		grace_days,store_limit,entitlement_projection,last_order_id
		FROM billing_subscriptions WHERE tenant_id=?`, tenantID).
		Scan(&f.TenantID, &f.SubscriptionRef, &f.PlanCode, &f.PricingVersion, &f.Status,
			&f.PaidUntil, &f.GraceDays, &f.StoreLimit, &f.EntitlementProjection, &f.LastOrderID)
	if errors.Is(err, sql.ErrNoRows) {
		return billing.SubscriptionFact{}, ErrNotFound
	}
	return f, err
}

// BillingStoreUsage reports the quota view: the plan's store limit and the
// tenant's live store count (disabled stores do not consume quota).
func (s *Store) BillingStoreUsage(tenantID string) (limit int, live int, err error) {
	var ignoreNull sql.NullInt64
	if err := s.DB.QueryRow(`SELECT store_limit FROM billing_subscriptions WHERE tenant_id=?`, tenantID).Scan(&ignoreNull); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, 0, err
	}
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM stores WHERE tenant_id=? AND status=?`,
		tenantID, StoreStatusActive).Scan(&live); err != nil {
		return 0, 0, err
	}
	return int(ignoreNull.Int64), live, nil
}

// ---- price catalog ------------------------------------------------------------

// UpsertBillingCatalogEntry writes one catalog projection row (trusted authority
// seam only). Unique key (scope_tenant, plan_code, pricing_version).
func (s *Store) UpsertBillingCatalogEntry(e billing.CatalogEntry, at string) (billing.CatalogEntry, error) {
	if e.PlanCode == "" || e.PricingVersion == "" || e.PriceRef == "" || e.Currency == "" {
		return billing.CatalogEntry{}, ErrBillingBadInput
	}
	if e.TierJSON == "" {
		e.TierJSON = "[]"
	}
	if _, err := s.DB.Exec(`INSERT INTO billing_price_catalog
		(id,scope_tenant,plan_code,pricing_version,price_ref,currency,price_minor,service_days,
		 included_store_count,extra_store_price_minor,tier_json,grace_days,effective_from,effective_to,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(scope_tenant,plan_code,pricing_version) DO UPDATE SET
		price_ref=excluded.price_ref,currency=excluded.currency,price_minor=excluded.price_minor,
		service_days=excluded.service_days,included_store_count=excluded.included_store_count,
		extra_store_price_minor=excluded.extra_store_price_minor,tier_json=excluded.tier_json,
		grace_days=excluded.grace_days,effective_from=excluded.effective_from,
		effective_to=excluded.effective_to,updated_at=excluded.updated_at`,
		firstNonEmptyStr(e.ID, newID("bp_")), e.ScopeTenant, e.PlanCode, e.PricingVersion, e.PriceRef,
		e.Currency, e.PriceMinor, e.ServiceDays, e.IncludedStoreCount, e.ExtraStorePriceMinor,
		e.TierJSON, e.GraceDays, e.EffectiveFrom, e.EffectiveTo, at, at); err != nil {
		return billing.CatalogEntry{}, err
	}
	e.ID = ""
	err := s.DB.QueryRow(`SELECT id FROM billing_price_catalog WHERE scope_tenant=? AND plan_code=? AND pricing_version=?`,
		e.ScopeTenant, e.PlanCode, e.PricingVersion).Scan(&e.ID)
	return e, err
}

// ListBillingCatalog returns the rows visible to a tenant (own scoped + global).
func (s *Store) ListBillingCatalog(tenantID string) ([]billing.CatalogEntry, error) {
	rows, err := s.DB.Query(`SELECT id,scope_tenant,plan_code,pricing_version,price_ref,currency,price_minor,
		service_days,included_store_count,extra_store_price_minor,tier_json,grace_days,effective_from,effective_to
		FROM billing_price_catalog WHERE scope_tenant=? OR scope_tenant='' ORDER BY plan_code, effective_from`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []billing.CatalogEntry{}
	for rows.Next() {
		var e billing.CatalogEntry
		if err := rows.Scan(&e.ID, &e.ScopeTenant, &e.PlanCode, &e.PricingVersion, &e.PriceRef,
			&e.Currency, &e.PriceMinor, &e.ServiceDays, &e.IncludedStoreCount,
			&e.ExtraStorePriceMinor, &e.TierJSON, &e.GraceDays, &e.EffectiveFrom, &e.EffectiveTo); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- AI usage projection (F13 / 4595 seam) ------------------------------------

// BillingAIUsageRow mirrors one billing_ai_usage row.
type BillingAIUsageRow struct {
	ID             string `json:"id"`
	TenantID       string `json:"tenant_id"`
	QuoteRef       string `json:"quote_ref"`
	Capability     string `json:"capability"`
	Quantity       int64  `json:"quantity"`
	AmountMinor    int64  `json:"amount_minor"`
	Currency       string `json:"currency"`
	PricingVersion string `json:"pricing_version"`
	State          string `json:"state"`
	BusinessRef    string `json:"business_ref,omitempty"`
	ConsumedByRef  string `json:"consumed_by_ref,omitempty"`
	OccurredAt     string `json:"occurred_at"`
	CreatedAt      string `json:"created_at"`
}

// NewBillingAIUsage is one trusted quote/charge reference to project.
type NewBillingAIUsage struct {
	TenantID       string
	QuoteRef       string
	Capability     string
	Quantity       int64
	AmountMinor    int64
	Currency       string
	PricingVersion string
	State          string
	BusinessRef    string
	OccurredAt     string
}

// RecordBillingAIUsage inserts the projection row; same (tenant, quote_ref)
// replays the existing row (retry idempotency, F13).
func (s *Store) RecordBillingAIUsage(in NewBillingAIUsage, at string) (BillingAIUsageRow, bool, error) {
	if strings.TrimSpace(in.QuoteRef) == "" || strings.TrimSpace(in.TenantID) == "" {
		return BillingAIUsageRow{}, false, ErrBillingBadInput
	}
	if in.State == "" {
		in.State = billing.QuoteStateConfirmed
	}
	var existing BillingAIUsageRow
	err := s.DB.QueryRow(`SELECT id,tenant_id,quote_ref,capability,quantity,amount_minor,currency,
		pricing_version,state,business_ref,consumed_by_ref,occurred_at,created_at
		FROM billing_ai_usage WHERE tenant_id=? AND quote_ref=?`, in.TenantID, in.QuoteRef).
		Scan(&existing.ID, &existing.TenantID, &existing.QuoteRef, &existing.Capability, &existing.Quantity,
			&existing.AmountMinor, &existing.Currency, &existing.PricingVersion, &existing.State,
			&existing.BusinessRef, &existing.ConsumedByRef, &existing.OccurredAt, &existing.CreatedAt)
	if err == nil {
		return existing, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return BillingAIUsageRow{}, false, err
	}
	id := newID("bau_")
	_, err = s.DB.Exec(`INSERT INTO billing_ai_usage
		(id,tenant_id,quote_ref,capability,quantity,amount_minor,currency,pricing_version,state,business_ref,occurred_at,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, in.TenantID, in.QuoteRef, in.Capability, in.Quantity, in.AmountMinor, in.Currency,
		in.PricingVersion, in.State, in.BusinessRef, in.OccurredAt, at)
	if err != nil {
		return BillingAIUsageRow{}, false, err
	}
	row, err := s.GetBillingAIUsage(in.TenantID, in.QuoteRef)
	return row, false, err
}

// GetBillingAIUsage fetches one projection row.
func (s *Store) GetBillingAIUsage(tenantID, quoteRef string) (BillingAIUsageRow, error) {
	var r BillingAIUsageRow
	err := s.DB.QueryRow(`SELECT id,tenant_id,quote_ref,capability,quantity,amount_minor,currency,
		pricing_version,state,business_ref,consumed_by_ref,occurred_at,created_at
		FROM billing_ai_usage WHERE tenant_id=? AND quote_ref=?`, tenantID, quoteRef).
		Scan(&r.ID, &r.TenantID, &r.QuoteRef, &r.Capability, &r.Quantity, &r.AmountMinor, &r.Currency,
			&r.PricingVersion, &r.State, &r.BusinessRef, &r.ConsumedByRef, &r.OccurredAt, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return BillingAIUsageRow{}, ErrNotFound
	}
	return r, err
}

// QuoteProjectionFor adapts the row onto the exported 4595 seam type.
func (s *Store) QuoteProjectionFor(tenantID, quoteRef string) (*billing.QuoteProjection, error) {
	r, err := s.GetBillingAIUsage(tenantID, quoteRef)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &billing.QuoteProjection{
		QuoteRef: r.QuoteRef, TenantID: r.TenantID, Capability: r.Capability,
		Quantity: r.Quantity, AmountMinor: r.AmountMinor, Currency: r.Currency,
		PricingVersion: r.PricingVersion, State: r.State, ConsumedByRef: r.ConsumedByRef,
	}, nil
}

// ConsumeQuoteProjection marks a confirmed quote as consumed by one handoff.
// Only confirmed → consumed is legal (双确认防重复消费)。
func (s *Store) ConsumeQuoteProjection(tenantID, quoteRef, consumedByRef, at string) error {
	res, err := s.DB.Exec(`UPDATE billing_ai_usage SET state=?,consumed_by_ref=?,occurred_at=?
		WHERE tenant_id=? AND quote_ref=? AND state=?`,
		billing.QuoteStateConsumed, consumedByRef, at, tenantID, quoteRef, billing.QuoteStateConfirmed)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrBillingQuoteNotConsumable
	}
	return nil
}

// ListBillingAIUsage lists the tenant's AI usage projection (F13 可查面).
func (s *Store) ListBillingAIUsage(tenantID string) ([]BillingAIUsageRow, error) {
	rows, err := s.DB.Query(`SELECT id,tenant_id,quote_ref,capability,quantity,amount_minor,currency,
		pricing_version,state,business_ref,consumed_by_ref,occurred_at,created_at
		FROM billing_ai_usage WHERE tenant_id=? ORDER BY occurred_at DESC, id LIMIT 200`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BillingAIUsageRow{}
	for rows.Next() {
		var r BillingAIUsageRow
		if err := rows.Scan(&r.ID, &r.TenantID, &r.QuoteRef, &r.Capability, &r.Quantity, &r.AmountMinor,
			&r.Currency, &r.PricingVersion, &r.State, &r.BusinessRef, &r.ConsumedByRef,
			&r.OccurredAt, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---- hardware (F14) -----------------------------------------------------------

// BillingHardwareItem is one one-off goods/service line of a hardware order.
type BillingHardwareItem struct {
	ID              string `json:"id"`
	OrderID         string `json:"order_id"`
	SKU             string `json:"sku"`
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	Qty             int    `json:"qty"`
	UnitAmountMinor int64  `json:"unit_amount_minor"`
}

// CreateHardwareOrder inserts the order and its item lines atomically.
func (s *Store) CreateHardwareOrder(in NewBillingOrder, items []BillingHardwareItem, at string) (BillingOrderRow, bool, error) {
	if in.Kind != billing.KindHardware || len(items) == 0 {
		return BillingOrderRow{}, false, ErrBillingBadInput
	}
	for _, it := range items {
		if it.Name == "" || it.Qty <= 0 || it.UnitAmountMinor < 0 || (it.Kind != "goods" && it.Kind != "service") {
			return BillingOrderRow{}, false, ErrBillingBadInput
		}
	}
	row, replayed, err := s.CreateBillingOrder(in, at)
	if err != nil || replayed {
		return row, replayed, err
	}
	for _, it := range items {
		if _, err := s.DB.Exec(`INSERT INTO billing_hardware_items(id,tenant_id,order_id,sku,name,kind,qty,unit_amount_minor,created_at)
			VALUES(?,?,?,?,?,?,?,?,?)`,
			firstNonEmptyStr(it.ID, newID("bhi_")), in.TenantID, row.ID, it.SKU, it.Name, it.Kind, it.Qty, it.UnitAmountMinor, at); err != nil {
			return row, false, err
		}
	}
	return row, false, nil
}

// ListHardwareItems lists one order's lines.
func (s *Store) ListHardwareItems(tenantID, orderID string) ([]BillingHardwareItem, error) {
	rows, err := s.DB.Query(`SELECT id,order_id,sku,name,kind,qty,unit_amount_minor
		FROM billing_hardware_items WHERE tenant_id=? AND order_id=? ORDER BY id`, tenantID, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BillingHardwareItem{}
	for rows.Next() {
		var it BillingHardwareItem
		if err := rows.Scan(&it.ID, &it.OrderID, &it.SKU, &it.Name, &it.Kind, &it.Qty, &it.UnitAmountMinor); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func scanFulfillment(row interface{ Scan(...any) error }) (billing.HardwareFulfillment, error) {
	var f billing.HardwareFulfillment
	var p, s1, w, r, a [4]string
	err := row.Scan(&p[0], &p[1], &p[2], &p[3], &s1[0], &s1[1], &s1[2], &s1[3],
		&w[0], &w[1], &w[2], &w[3], &r[0], &r[1], &r[2], &r[3], &a[0], &a[1], &a[2], &a[3])
	if err != nil {
		return f, err
	}
	f.Payment = HardwareStepFromRow(p)
	f.Shipment = HardwareStepFromRow(s1)
	f.WriteCard = HardwareStepFromRow(w)
	f.Readback = HardwareStepFromRow(r)
	f.Acceptance = HardwareStepFromRow(a)
	return f, nil
}

// HardwareStepFromRow adapts (state, operator, at, device) columns.
func HardwareStepFromRow(v [4]string) billing.HardwareStep {
	return billing.HardwareStep{State: v[0], Operator: v[1], OccurredAt: v[2], DeviceRef: v[3]}
}

const fulfillmentCols = `payment_state,payment_operator,payment_at,payment_device_ref,
shipment_state,shipment_operator,shipment_at,shipment_device_ref,
write_card_state,write_card_operator,write_card_at,write_card_device_ref,
readback_state,readback_operator,readback_at,readback_device_ref,
acceptance_state,acceptance_operator,acceptance_at,acceptance_device_ref`

// GetHardwareFulfillment reads the five-step projection (empty = all pending).
func (s *Store) GetHardwareFulfillment(tenantID, orderID string) (billing.HardwareFulfillment, error) {
	return scanFulfillment(s.DB.QueryRow(
		`SELECT `+fulfillmentCols+` FROM billing_hardware_fulfillment WHERE tenant_id=? AND order_id=?`,
		tenantID, orderID))
}

func upsertHardwareStepTx(tx *sql.Tx, tenantID, orderID string, in billing.HardwareStepInput, at string) error {
	return upsertHardwareStep(tx, tenantID, orderID, in, at)
}

func upsertHardwareStep(tx *sql.Tx, tenantID, orderID string, in billing.HardwareStepInput, at string) error {
	// 读取当前投影(行不存在=全 pending)。
	current, err := scanFulfillment(tx.QueryRow(
		`SELECT `+fulfillmentCols+` FROM billing_hardware_fulfillment WHERE tenant_id=? AND order_id=?`,
		tenantID, orderID))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	next, err := billing.ApplyHardwareStep(current, in)
	if err != nil {
		return err
	}
	// 未发生的步骤落库为 pending(零值语义归一,库层 CHECK 同值域)。
	normalize := func(s billing.HardwareStep) billing.HardwareStep {
		if s.State == "" {
			s.State = billing.StepPending
		}
		return s
	}
	next.Payment = normalize(next.Payment)
	next.Shipment = normalize(next.Shipment)
	next.WriteCard = normalize(next.WriteCard)
	next.Readback = normalize(next.Readback)
	next.Acceptance = normalize(next.Acceptance)
	_, err = tx.Exec(`INSERT INTO billing_hardware_fulfillment
		(order_id,tenant_id,`+fulfillmentCols+`,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(order_id) DO UPDATE SET
		payment_state=excluded.payment_state,payment_operator=excluded.payment_operator,
		payment_at=excluded.payment_at,payment_device_ref=excluded.payment_device_ref,
		shipment_state=excluded.shipment_state,shipment_operator=excluded.shipment_operator,
		shipment_at=excluded.shipment_at,shipment_device_ref=excluded.shipment_device_ref,
		write_card_state=excluded.write_card_state,write_card_operator=excluded.write_card_operator,
		write_card_at=excluded.write_card_at,write_card_device_ref=excluded.write_card_device_ref,
		readback_state=excluded.readback_state,readback_operator=excluded.readback_operator,
		readback_at=excluded.readback_at,readback_device_ref=excluded.readback_device_ref,
		acceptance_state=excluded.acceptance_state,acceptance_operator=excluded.acceptance_operator,
		acceptance_at=excluded.acceptance_at,acceptance_device_ref=excluded.acceptance_device_ref,
		updated_at=excluded.updated_at`,
		orderID, tenantID,
		next.Payment.State, next.Payment.Operator, next.Payment.OccurredAt, next.Payment.DeviceRef,
		next.Shipment.State, next.Shipment.Operator, next.Shipment.OccurredAt, next.Shipment.DeviceRef,
		next.WriteCard.State, next.WriteCard.Operator, next.WriteCard.OccurredAt, next.WriteCard.DeviceRef,
		next.Readback.State, next.Readback.Operator, next.Readback.OccurredAt, next.Readback.DeviceRef,
		next.Acceptance.State, next.Acceptance.Operator, next.Acceptance.OccurredAt, next.Acceptance.DeviceRef,
		at)
	return err
}

// ApplyHardwareStepInTx records one trusted delivery fact inside the caller's
// transaction (HUI-1663 接线,4591 报告 §7「1663 落地时把交付状态机接线到本投影
// 即可」):交付域在自身事件事务内同步推进订单投影,判定核仍是 upsertHardwareStep
// → billing.ApplyHardwareStep 纯函数,幂等/不可跳跃纪律逐字节一致。
func ApplyHardwareStepInTx(tx *sql.Tx, tenantID, orderID string, in billing.HardwareStepInput, at string) error {
	return upsertHardwareStep(tx, tenantID, orderID, in, at)
}

// ApplyHardwareStep records one trusted delivery fact (五状态推进)。
func (s *Store) ApplyHardwareStep(tenantID, orderID string, in billing.HardwareStepInput, at string) (billing.HardwareFulfillment, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return billing.HardwareFulfillment{}, err
	}
	defer tx.Rollback()
	if err := upsertHardwareStep(tx, tenantID, orderID, in, at); err != nil {
		return billing.HardwareFulfillment{}, err
	}
	f, err := scanFulfillment(tx.QueryRow(
		`SELECT `+fulfillmentCols+` FROM billing_hardware_fulfillment WHERE tenant_id=? AND order_id=?`,
		tenantID, orderID))
	if err != nil {
		return billing.HardwareFulfillment{}, err
	}
	if err := tx.Commit(); err != nil {
		return billing.HardwareFulfillment{}, err
	}
	return f, nil
}

// ---- refunds (F19) -------------------------------------------------------------

// BillingRefundRow mirrors one billing_refunds row.
type BillingRefundRow struct {
	ID            string `json:"id"`
	TenantID      string `json:"tenant_id"`
	OrderID       string `json:"order_id"`
	RefundRef     string `json:"refund_ref"`
	AmountMinor   int64  `json:"amount_minor"`
	Currency      string `json:"currency"`
	Reason        string `json:"reason"`
	InvoiceTitle  string `json:"invoice_title"`
	InvoiceStatus string `json:"invoice_status"`
	InvoiceRef    string `json:"invoice_ref,omitempty"`
	Auditor       string `json:"auditor"`
	Source        string `json:"source"`
	EventID       string `json:"event_id"`
	OccurredAt    string `json:"occurred_at"`
	CreatedAt     string `json:"created_at"`
}

// ApplyBillingRefund records one refund fact: dedup by (tenant, source, event_id)
// with payload equality (同键异负载拒绝), amount guarded against the order's
// remaining un-refunded balance. 重复回调不重复退款。
func (s *Store) ApplyBillingRefund(tenantID, orderID string, in billing.RefundInput, at string) (BillingRefundRow, bool, error) {
	payloadJSON, err := refundPayloadJSON(in)
	if err != nil {
		return BillingRefundRow{}, false, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return BillingRefundRow{}, false, err
	}
	defer tx.Rollback()
	var prevPayload string
	err = tx.QueryRow(`SELECT payload FROM billing_refunds WHERE tenant_id=? AND source=? AND event_id=?`,
		tenantID, in.Source, in.EventID).Scan(&prevPayload)
	if err == nil {
		if prevPayload != payloadJSON {
			return BillingRefundRow{}, false, ErrBillingIdempotencyConflict
		}
		row, gerr := scanRefund(tx.QueryRow(`SELECT id,tenant_id,order_id,refund_ref,amount_minor,currency,reason,
			invoice_title,invoice_status,invoice_ref,auditor,source,event_id,occurred_at,created_at
			FROM billing_refunds WHERE tenant_id=? AND source=? AND event_id=?`, tenantID, in.Source, in.EventID))
		return row, true, gerr
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return BillingRefundRow{}, false, err
	}
	var orderAmount int64
	if err := tx.QueryRow(`SELECT amount_minor FROM billing_orders WHERE tenant_id=? AND id=?`,
		tenantID, orderID).Scan(&orderAmount); err != nil {
		return BillingRefundRow{}, false, err
	}
	var refunded int64
	if err := tx.QueryRow(`SELECT COALESCE(SUM(amount_minor),0) FROM billing_refunds WHERE tenant_id=? AND order_id=?`,
		tenantID, orderID).Scan(&refunded); err != nil {
		return BillingRefundRow{}, false, err
	}
	if err := billing.ValidateRefundAgainstOrder(in, orderAmount, refunded); err != nil {
		return BillingRefundRow{}, false, err
	}
	id := newID("brf_")
	if _, err := tx.Exec(`INSERT INTO billing_refunds
		(id,tenant_id,order_id,refund_ref,amount_minor,currency,reason,invoice_title,invoice_status,invoice_ref,auditor,source,event_id,payload,occurred_at,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, tenantID, orderID, in.RefundRef, in.AmountMinor, in.Currency, in.Reason,
		in.InvoiceTitle, in.InvoiceStatus, in.InvoiceRef, in.Auditor, in.Source, in.EventID,
		payloadJSON, in.OccurredAt, at); err != nil {
		return BillingRefundRow{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return BillingRefundRow{}, false, err
	}
	row, err := s.GetBillingRefundByEvent(tenantID, in.Source, in.EventID)
	return row, false, err
}

// refundPayloadJSON canonicalizes a refund fact for dedup payload equality.
func refundPayloadJSON(in billing.RefundInput) (string, error) {
	b, err := json.Marshal(map[string]any{
		"refund_ref": in.RefundRef, "amount_minor": in.AmountMinor, "reason": in.Reason,
		"invoice_title": in.InvoiceTitle, "invoice_status": in.InvoiceStatus, "auditor": in.Auditor,
	})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// GetBillingRefundByEvent fetches one refund by its dedup key.
func (s *Store) GetBillingRefundByEvent(tenantID, source, eventID string) (BillingRefundRow, error) {
	return scanRefund(s.DB.QueryRow(`SELECT id,tenant_id,order_id,refund_ref,amount_minor,currency,reason,
		invoice_title,invoice_status,invoice_ref,auditor,source,event_id,occurred_at,created_at
		FROM billing_refunds WHERE tenant_id=? AND source=? AND event_id=?`, tenantID, source, eventID))
}

func scanRefund(row interface{ Scan(...any) error }) (BillingRefundRow, error) {
	var r BillingRefundRow
	err := row.Scan(&r.ID, &r.TenantID, &r.OrderID, &r.RefundRef, &r.AmountMinor, &r.Currency,
		&r.Reason, &r.InvoiceTitle, &r.InvoiceStatus, &r.InvoiceRef, &r.Auditor, &r.Source,
		&r.EventID, &r.OccurredAt, &r.CreatedAt)
	return r, err
}

// ListBillingRefunds lists the tenant's refund/invoice reconciliation rows.
func (s *Store) ListBillingRefunds(tenantID string) ([]BillingRefundRow, error) {
	rows, err := s.DB.Query(`SELECT id,tenant_id,order_id,refund_ref,amount_minor,currency,reason,
		invoice_title,invoice_status,invoice_ref,auditor,source,event_id,occurred_at,created_at
		FROM billing_refunds WHERE tenant_id=? ORDER BY occurred_at DESC, id LIMIT 200`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BillingRefundRow{}
	for rows.Next() {
		r, err := scanRefund(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
