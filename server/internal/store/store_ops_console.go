// store_ops_console.go: HUI-4592 平台运营控制台的 SQL 层。
//
// 纪律(裁定书):
//   - 本文件是 ops 控制台自己的读写面(0124-0128 五表 + 跨租户只读投影);
//     跨租户读只做 SELECT,各域写面(4591 目录/订单权威缝、3535 能力行、
//     3553 内容下架、4593 分发状态)一律调用既有域函数,不在本文件另写
//     UPDATE——平台运营没有第二写面;
//   - 审计(0124)append-only:只有 INSERT 与 SELECT;
//   - 商户 lifecycle/charge_hold 复用 2053 语义,但带单语句 revision 守卫
//     (WHERE ops_revision=?),防止过期页面覆盖新状态;
//   - 操作信封(0126)idempotency_key UNIQUE:同键重试读回同一行,不重复执行;
//     同键异负载由调用方按 canonical JSON 判等后拒绝。

package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/billing"
	"github.com/bianjiefilm/touch-engine/server/internal/ops"
)

// ErrOpsRevisionConflict: expected_revision 不匹配(409 stale_version)。
var ErrOpsRevisionConflict = errors.New("store: ops_revision_conflict")

// ---- 审计(0124) -----------------------------------------------------------------

// OpsAuditEntry is one append-only ops console audit row.
type OpsAuditEntry struct {
	ID              string `json:"id"`
	OccurredAt      string `json:"occurred_at"`
	ActorRef        string `json:"actor_ref"`
	ActorRole       string `json:"actor_role"`
	AuthoritySource string `json:"authority_source"`
	Command         string `json:"command"`
	Action          string `json:"action"`
	TargetType      string `json:"target_type"`
	TargetID        string `json:"target_id"`
	TenantID        string `json:"tenant_id"`
	BeforeJSON      string `json:"before_json"`
	AfterJSON       string `json:"after_json"`
	Reason          string `json:"reason"`
	OperationID     string `json:"operation_id"`
}

// AppendOpsAudit appends one audit row (append-only; no update path exists).
func (s *Store) AppendOpsAudit(e OpsAuditEntry) (OpsAuditEntry, error) {
	if e.ID == "" {
		e.ID = newID("oa_")
	}
	if e.OccurredAt == "" {
		e.OccurredAt = now()
	}
	_, err := s.DB.Exec(
		`INSERT INTO ops_audit_log(id,occurred_at,actor_ref,actor_role,authority_source,command,action,
		 target_type,target_id,tenant_id,before_json,after_json,reason,operation_id)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.ID, e.OccurredAt, e.ActorRef, e.ActorRole, e.AuthoritySource, e.Command, e.Action,
		e.TargetType, e.TargetID, e.TenantID, e.BeforeJSON, e.AfterJSON, e.Reason, e.OperationID)
	if err == nil {
		s.bumpEpoch()
	}
	return e, err
}

// OpsAuditFilter narrows one audit listing (all fields optional).
type OpsAuditFilter struct {
	TenantID  string
	TargetID  string
	Operation string
	Limit     int
}

func scanOpsAudit(sc interface{ Scan(...any) error }) (OpsAuditEntry, error) {
	var e OpsAuditEntry
	err := sc.Scan(&e.ID, &e.OccurredAt, &e.ActorRef, &e.ActorRole, &e.AuthoritySource, &e.Command,
		&e.Action, &e.TargetType, &e.TargetID, &e.TenantID, &e.BeforeJSON, &e.AfterJSON,
		&e.Reason, &e.OperationID)
	return e, err
}

const opsAuditCols = `id,occurred_at,actor_ref,actor_role,authority_source,command,action,target_type,target_id,tenant_id,before_json,after_json,reason,operation_id`

// ListOpsAudit reads audit rows, newest first. Limit<=0 → 50.
func (s *Store) ListOpsAudit(f OpsAuditFilter) ([]OpsAuditEntry, error) {
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT ` + opsAuditCols + ` FROM ops_audit_log WHERE 1=1`
	args := []any{}
	if f.TenantID != "" {
		q += ` AND tenant_id=?`
		args = append(args, f.TenantID)
	}
	if f.TargetID != "" {
		q += ` AND target_id=?`
		args = append(args, f.TargetID)
	}
	if f.Operation != "" {
		q += ` AND operation_id=?`
		args = append(args, f.Operation)
	}
	q += ` ORDER BY occurred_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OpsAuditEntry{}
	for rows.Next() {
		e, err := scanOpsAudit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- 商户 revision 读写(2053 语义 + 单语句守卫) ---------------------------------

// TenantRevision returns the tenant's current ops_revision (0 = tenant missing).
func (s *Store) TenantRevision(id string) (int64, error) {
	var rev int64
	err := s.DB.QueryRow(`SELECT ops_revision FROM tenants WHERE id=?`, id).Scan(&rev)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return rev, err
}

// SetTenantLifecycleOps applies the 2053 lifecycle vocabulary with a
// single-statement revision guard. Stale expectedRevision → ErrOpsRevisionConflict.
func (s *Store) SetTenantLifecycleOps(id, lifecycle string, expectedRevision int64) (int64, error) {
	switch lifecycle {
	case "", "active", "suspended", "security_freeze", "offboarding", "retired":
	default:
		return 0, ErrBillingBadInput
	}
	res, err := s.DB.Exec(
		`UPDATE tenants SET lifecycle=?, ops_revision=ops_revision+1 WHERE id=? AND ops_revision=?`,
		lifecycle, id, expectedRevision)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return 0, ErrOpsRevisionConflict
	}
	s.bumpEpoch()
	return s.TenantRevision(id)
}

// SetChargeHoldOps applies the 2053 charge-hold flag with the same guard.
func (s *Store) SetChargeHoldOps(id string, hold bool, expectedRevision int64) (int64, error) {
	res, err := s.DB.Exec(
		`UPDATE tenants SET charge_hold=?, ops_revision=ops_revision+1 WHERE id=? AND ops_revision=?`,
		btoi(hold), id, expectedRevision)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return 0, ErrOpsRevisionConflict
	}
	s.bumpEpoch()
	return s.TenantRevision(id)
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ---- 跨租户商户读面(O1) ---------------------------------------------------------

// TenantOpsRow is one platform-view merchant row: lifecycle/charge hold plus
// aggregate counts. Business payloads (store lists, member lists) stay out —
// deep data needs an active support access (HTTP 层裁决).
type TenantOpsRow struct {
	Tenant
	StoreCount  int    `json:"store_count"`
	MemberCount int    `json:"member_count"`
	AgencyCount int    `json:"agency_count"`
	SubStatus   string `json:"subscription_status"`
	PaidUntil   string `json:"paid_until"`
	RetainUntil string `json:"retain_until"`
	OpsRevision int64  `json:"ops_revision"`
}

// ListTenantsOps searches merchants by name/id substring and lifecycle filter.
func (s *Store) ListTenantsOps(query, lifecycle string) ([]TenantOpsRow, error) {
	q := `SELECT t.id,t.name,t.created_at,t.brand_id,t.lifecycle,t.charge_hold,t.ops_revision,
		(SELECT COUNT(*) FROM stores st WHERE st.tenant_id=t.id),
		(SELECT COUNT(*) FROM members m WHERE m.tenant_id=t.id AND m.enabled=1),
		(SELECT COUNT(*) FROM agency_relations ar WHERE ar.tenant_id=t.id),
		COALESCE(bs.status,''), COALESCE(bs.paid_until,''), COALESCE(bs.retain_until,'')
		FROM tenants t
		LEFT JOIN billing_subscriptions bs ON bs.tenant_id=t.id
		WHERE 1=1`
	args := []any{}
	if query != "" {
		q += ` AND (t.name LIKE ? OR t.id LIKE ?)`
		like := "%" + query + "%"
		args = append(args, like, like)
	}
	if lifecycle != "" {
		q += ` AND t.lifecycle=?`
		args = append(args, lifecycle)
	}
	q += ` ORDER BY t.created_at DESC LIMIT 200`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TenantOpsRow{}
	for rows.Next() {
		var r TenantOpsRow
		var hold int
		if err := rows.Scan(&r.ID, &r.Name, &r.CreatedAt, &r.BrandID, &r.Lifecycle, &hold, &r.OpsRevision,
			&r.StoreCount, &r.MemberCount, &r.AgencyCount, &r.SubStatus, &r.PaidUntil, &r.RetainUntil); err != nil {
			return nil, err
		}
		r.ChargeHold = hold == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---- 支持访问(0125) -------------------------------------------------------------

// SupportAccess is one time-boxed support access grant (0125).
type SupportAccess struct {
	ID           string `json:"id"`
	TenantID     string `json:"tenant_id"`
	PrincipalRef string `json:"principal_ref"`
	Reason       string `json:"reason"`
	OpenedAt     string `json:"opened_at"`
	ExpiresAt    string `json:"expires_at"`
	ClosedBy     string `json:"closed_by"`
	ClosedAt     string `json:"closed_at"`
	CloseReason  string `json:"close_reason"`
}

const supportAccessCols = `id,tenant_id,principal_ref,reason,opened_at,expires_at,closed_by,closed_at,close_reason`

func scanSupportAccess(sc interface{ Scan(...any) error }) (SupportAccess, error) {
	var a SupportAccess
	err := sc.Scan(&a.ID, &a.TenantID, &a.PrincipalRef, &a.Reason, &a.OpenedAt, &a.ExpiresAt,
		&a.ClosedBy, &a.ClosedAt, &a.CloseReason)
	return a, err
}

// CreateSupportAccess inserts one time-boxed grant. TTL was validated by the
// ops domain package; this layer enforces the structural not-null expiry.
func (s *Store) CreateSupportAccess(tenantID, principalRef, reason string, ttl time.Duration, at time.Time) (SupportAccess, error) {
	if reason == "" {
		return SupportAccess{}, ErrBillingBadInput
	}
	if ttl <= 0 || ttl > ops.MaxSupportAccessMinutes*time.Minute {
		return SupportAccess{}, ErrBillingBadInput
	}
	a := SupportAccess{
		ID: newID("sup_"), TenantID: tenantID, PrincipalRef: principalRef,
		Reason: reason, OpenedAt: at.UTC().Format(time.RFC3339),
		ExpiresAt: at.Add(ttl).UTC().Format(time.RFC3339),
	}
	_, err := s.DB.Exec(
		`INSERT INTO ops_support_access(id,tenant_id,principal_ref,reason,opened_at,expires_at) VALUES(?,?,?,?,?,?)`,
		a.ID, a.TenantID, a.PrincipalRef, a.Reason, a.OpenedAt, a.ExpiresAt)
	if err != nil {
		return SupportAccess{}, err
	}
	s.bumpEpoch()
	return a, nil
}

// GetSupportAccess reads one row by id.
func (s *Store) GetSupportAccess(id string) (SupportAccess, error) {
	row := s.DB.QueryRow(`SELECT `+supportAccessCols+` FROM ops_support_access WHERE id=?`, id)
	a, err := scanSupportAccess(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SupportAccess{}, ErrNotFound
	}
	return a, err
}

// ActiveSupportAccess returns the caller's live grant for one tenant
// (unclosed and unexpired), or ErrNotFound. 每请求实时调用 = 到期立即停止。
func (s *Store) ActiveSupportAccess(principalRef, tenantID string, at time.Time) (SupportAccess, error) {
	row := s.DB.QueryRow(
		`SELECT `+supportAccessCols+` FROM ops_support_access
		 WHERE principal_ref=? AND tenant_id=? AND closed_at='' AND expires_at>?
		 ORDER BY opened_at DESC LIMIT 1`, principalRef, tenantID, at.UTC().Format(time.RFC3339))
	a, err := scanSupportAccess(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SupportAccess{}, ErrNotFound
	}
	return a, err
}

// ListSupportAccess lists recent grants (all operators; platform view).
func (s *Store) ListSupportAccess(limit int) ([]SupportAccess, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.DB.Query(
		`SELECT `+supportAccessCols+` FROM ops_support_access ORDER BY opened_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SupportAccess{}
	for rows.Next() {
		a, err := scanSupportAccess(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CloseSupportAccess tombstones one grant (idempotent: closing twice is a no-op read-back).
func (s *Store) CloseSupportAccess(id, closedBy, closeReason string, at time.Time) (SupportAccess, error) {
	cur, err := s.GetSupportAccess(id)
	if err != nil {
		return SupportAccess{}, err
	}
	if cur.ClosedAt != "" {
		return cur, nil
	}
	ts := at.UTC().Format(time.RFC3339)
	if _, err := s.DB.Exec(
		`UPDATE ops_support_access SET closed_by=?,closed_at=?,close_reason=? WHERE id=? AND closed_at=''`,
		closedBy, ts, closeReason, id); err != nil {
		return SupportAccess{}, err
	}
	s.bumpEpoch()
	return s.GetSupportAccess(id)
}

// ---- 操作信封(0126) -------------------------------------------------------------

// OpsOperation is one unified operation envelope row.
type OpsOperation struct {
	ID               string `json:"id"`
	IdempotencyKey   string `json:"idempotency_key"`
	Action           string `json:"action"`
	ResourceRef      string `json:"resource_ref"`
	TenantID         string `json:"tenant_id"`
	ExpectedRevision string `json:"expected_revision"`
	Reason           string `json:"reason"`
	ActorRef         string `json:"actor_ref"`
	ActorRole        string `json:"actor_role"`
	AuthoritySource  string `json:"authority_source"`
	State            string `json:"state"`
	ErrorCode        string `json:"error_code"`
	ResultJSON       string `json:"result_json"`
	CreatedAt        string `json:"created_at"`
	FinishedAt       string `json:"finished_at"`
}

const opsOperationCols = `id,idempotency_key,action,resource_ref,tenant_id,expected_revision,reason,actor_ref,actor_role,authority_source,state,error_code,result_json,created_at,finished_at`

func scanOpsOperation(sc interface{ Scan(...any) error }) (OpsOperation, error) {
	var o OpsOperation
	err := sc.Scan(&o.ID, &o.IdempotencyKey, &o.Action, &o.ResourceRef, &o.TenantID, &o.ExpectedRevision,
		&o.Reason, &o.ActorRef, &o.ActorRole, &o.AuthoritySource, &o.State, &o.ErrorCode,
		&o.ResultJSON, &o.CreatedAt, &o.FinishedAt)
	return o, err
}

// RecordOpsOperation inserts the running row for one fresh operation.
// ErrBillingIdempotencyConflict when the key is taken (caller re-reads and
// compares payloads).
func (s *Store) RecordOpsOperation(o OpsOperation) (OpsOperation, error) {
	if o.ID == "" {
		o.ID = newID("opn_")
	}
	o.State = "running"
	o.CreatedAt = now()
	_, err := s.DB.Exec(
		`INSERT INTO ops_operations(id,idempotency_key,action,resource_ref,tenant_id,expected_revision,
		 reason,actor_ref,actor_role,authority_source,state,error_code,result_json,created_at,finished_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,'','',?,'')`,
		o.ID, o.IdempotencyKey, o.Action, o.ResourceRef, o.TenantID, o.ExpectedRevision,
		o.Reason, o.ActorRef, o.ActorRole, o.AuthoritySource, o.ResultJSON, o.CreatedAt)
	if err != nil {
		return OpsOperation{}, err
	}
	s.bumpEpoch()
	return o, nil
}

// GetOpsOperation reads one operation by id.
func (s *Store) GetOpsOperation(id string) (OpsOperation, error) {
	row := s.DB.QueryRow(`SELECT `+opsOperationCols+` FROM ops_operations WHERE id=?`, id)
	o, err := scanOpsOperation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return OpsOperation{}, ErrNotFound
	}
	return o, err
}

// GetOpsOperationByIdempotencyKey reads one operation by its retry key.
func (s *Store) GetOpsOperationByIdempotencyKey(key string) (OpsOperation, error) {
	row := s.DB.QueryRow(`SELECT `+opsOperationCols+` FROM ops_operations WHERE idempotency_key=?`, key)
	o, err := scanOpsOperation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return OpsOperation{}, ErrNotFound
	}
	return o, err
}

// FinishOpsOperation records the terminal state and result of one operation.
func (s *Store) FinishOpsOperation(id, state, errorCode, resultJSON string) (OpsOperation, error) {
	if _, err := s.DB.Exec(
		`UPDATE ops_operations SET state=?,error_code=?,result_json=?,finished_at=? WHERE id=?`,
		state, errorCode, resultJSON, now(), id); err != nil {
		return OpsOperation{}, err
	}
	s.bumpEpoch()
	return s.GetOpsOperation(id)
}

// ListOpsOperations lists recent operations, newest first.
func (s *Store) ListOpsOperations(limit int) ([]OpsOperation, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.DB.Query(
		`SELECT `+opsOperationCols+` FROM ops_operations ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OpsOperation{}
	for rows.Next() {
		o, err := scanOpsOperation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ---- 跨租户投影读面(O2/O3/O5/O6,只读) -------------------------------------------

// ListCatalogAll reads every price-catalog row (global + tenant-scoped).
func (s *Store) ListCatalogAll() ([]billing.CatalogEntry, error) {
	rows, err := s.DB.Query(
		`SELECT id,scope_tenant,plan_code,pricing_version,price_ref,currency,price_minor,service_days,
		 included_store_count,extra_store_price_minor,tier_json,grace_days,effective_from,effective_to
		 FROM billing_price_catalog ORDER BY scope_tenant,plan_code,pricing_version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []billing.CatalogEntry{}
	for rows.Next() {
		var e billing.CatalogEntry
		if err := rows.Scan(&e.ID, &e.ScopeTenant, &e.PlanCode, &e.PricingVersion, &e.PriceRef,
			&e.Currency, &e.PriceMinor, &e.ServiceDays, &e.IncludedStoreCount, &e.ExtraStorePriceMinor,
			&e.TierJSON, &e.GraceDays, &e.EffectiveFrom, &e.EffectiveTo); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetCatalogEntryByID reads one catalog row by primary key.
func (s *Store) GetCatalogEntryByID(id string) (billing.CatalogEntry, error) {
	row := s.DB.QueryRow(
		`SELECT id,scope_tenant,plan_code,pricing_version,price_ref,currency,price_minor,service_days,
		 included_store_count,extra_store_price_minor,tier_json,grace_days,effective_from,effective_to
		 FROM billing_price_catalog WHERE id=?`, id)
	var e billing.CatalogEntry
	err := row.Scan(&e.ID, &e.ScopeTenant, &e.PlanCode, &e.PricingVersion, &e.PriceRef,
		&e.Currency, &e.PriceMinor, &e.ServiceDays, &e.IncludedStoreCount, &e.ExtraStorePriceMinor,
		&e.TierJSON, &e.GraceDays, &e.EffectiveFrom, &e.EffectiveTo)
	if errors.Is(err, sql.ErrNoRows) {
		return billing.CatalogEntry{}, ErrNotFound
	}
	return e, err
}

// OpsOrderRow is one cross-tenant order projection row.
type OpsOrderRow struct {
	BillingOrderRow
	RefundTotalMinor int64 `json:"refund_total_minor"`
}

// ListOrdersAll reads orders across tenants (optional kind filter), with the
// per-order refunded total for the discrepancy queue.
func (s *Store) ListOrdersAll(kind string, limit int) ([]OpsOrderRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	q := `SELECT o.id,o.tenant_id,o.kind,o.action,o.plan_code,o.price_ref,o.price_snapshot,
		o.amount_minor,o.currency,o.status,o.order_ref,o.payment_ref,o.idempotency_key,
		o.payer_grant_ref,o.agency_relation_ref,o.payer_principal,o.actor_principal,o.actor_role,
		o.fail_reason,o.created_at,o.updated_at,
		COALESCE((SELECT SUM(r.amount_minor) FROM billing_refunds r WHERE r.tenant_id=o.tenant_id AND r.order_id=o.id),0)
		FROM billing_orders o`
	args := []any{}
	if kind != "" {
		q += ` WHERE o.kind=?`
		args = append(args, kind)
	}
	q += ` ORDER BY o.created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OpsOrderRow{}
	for rows.Next() {
		var r OpsOrderRow
		if err := rows.Scan(&r.ID, &r.TenantID, &r.Kind, &r.Action, &r.PlanCode, &r.PriceRef,
			&r.PriceSnapshot, &r.AmountMinor, &r.Currency, &r.Status, &r.OrderRef, &r.PaymentRef,
			&r.IdempotencyKey, &r.PayerGrantRef, &r.AgencyRelationRef, &r.PayerPrincipal,
			&r.ActorPrincipal, &r.ActorRole, &r.FailReason, &r.CreatedAt, &r.UpdatedAt,
			&r.RefundTotalMinor); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListRefundsAll reads refunds across tenants.
func (s *Store) ListRefundsAll(limit int) ([]BillingRefundRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.DB.Query(
		`SELECT id,tenant_id,order_id,refund_ref,amount_minor,currency,reason,invoice_title,
		 invoice_status,invoice_ref,auditor,source,event_id,occurred_at,created_at
		 FROM billing_refunds ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BillingRefundRow{}
	for rows.Next() {
		var r BillingRefundRow
		if err := rows.Scan(&r.ID, &r.TenantID, &r.OrderID, &r.RefundRef, &r.AmountMinor, &r.Currency,
			&r.Reason, &r.InvoiceTitle, &r.InvoiceStatus, &r.InvoiceRef, &r.Auditor, &r.Source,
			&r.EventID, &r.OccurredAt, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListAIUsageAll reads AI usage projections across tenants.
func (s *Store) ListAIUsageAll(limit int) ([]BillingAIUsageRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.DB.Query(
		`SELECT id,tenant_id,quote_ref,capability,quantity,amount_minor,currency,pricing_version,
		 state,business_ref,consumed_by_ref,occurred_at,created_at
		 FROM billing_ai_usage ORDER BY created_at DESC LIMIT ?`, limit)
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

// OpsHardwareRow is one cross-tenant hardware order with its fulfillment
// projection (4591 §硬件五状态;1663 未先行的只读投影 seam)。
type OpsHardwareRow struct {
	OpsOrderRow
	Fulfillment billing.HardwareFulfillment `json:"fulfillment"`
}

// ListHardwareAll reads hardware orders with fulfillment across tenants.
func (s *Store) ListHardwareAll(limit int) ([]OpsHardwareRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.DB.Query(
		`SELECT o.id,o.tenant_id FROM billing_orders o WHERE o.kind='hardware' ORDER BY o.created_at DESC LIMIT ?`,
		limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := [][2]string{}
	for rows.Next() {
		var id, ten string
		if err := rows.Scan(&id, &ten); err != nil {
			return nil, err
		}
		ids = append(ids, [2]string{id, ten})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []OpsHardwareRow{}
	for _, pair := range ids {
		order, err := s.GetBillingOrder(pair[1], pair[0])
		if err != nil {
			continue // 行级缺失不拖垮列表(诚实逐行)
		}
		row := OpsHardwareRow{OpsOrderRow: OpsOrderRow{BillingOrderRow: order}}
		if ful, err := s.GetHardwareFulfillment(pair[1], pair[0]); err == nil {
			row.Fulfillment = ful
		}
		out = append(out, row)
	}
	return out, nil
}

// TouchpointInventory counts touchpoint aliases platform-wide by status.
func (s *Store) TouchpointInventory() (map[string]int, error) {
	rows, err := s.DB.Query(`SELECT COALESCE(status,''),COUNT(*) FROM touchpoint_aliases GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	return out, rows.Err()
}

// ---- 工单(0127) ------------------------------------------------------------------

// OpsTicket is one operations exception ticket.
type OpsTicket struct {
	ID           string `json:"id"`
	TenantID     string `json:"tenant_id"`
	Kind         string `json:"kind"`
	Severity     string `json:"severity"`
	Title        string `json:"title"`
	Detail       string `json:"detail"`
	Status       string `json:"status"`
	OwnerRef     string `json:"owner_ref"`
	Source       string `json:"source"`
	OpenedAt     string `json:"opened_at"`
	OpenedBy     string `json:"opened_by"`
	ConfirmedBy  string `json:"confirmed_by"`
	ConfirmedAt  string `json:"confirmed_at"`
	ResolvedBy   string `json:"resolved_by"`
	ResolvedAt   string `json:"resolved_at"`
	Resolution   string `json:"resolution"`
	ClosedBy     string `json:"closed_by"`
	ClosedAt     string `json:"closed_at"`
	ClosedReason string `json:"closed_reason"`
}

const opsTicketCols = `id,tenant_id,kind,severity,title,detail,status,owner_ref,source,opened_at,opened_by,confirmed_by,confirmed_at,resolved_by,resolved_at,resolution,closed_by,closed_at,closed_reason`

func scanOpsTicket(sc interface{ Scan(...any) error }) (OpsTicket, error) {
	var t OpsTicket
	err := sc.Scan(&t.ID, &t.TenantID, &t.Kind, &t.Severity, &t.Title, &t.Detail, &t.Status, &t.OwnerRef,
		&t.Source, &t.OpenedAt, &t.OpenedBy, &t.ConfirmedBy, &t.ConfirmedAt, &t.ResolvedBy,
		&t.ResolvedAt, &t.Resolution, &t.ClosedBy, &t.ClosedAt, &t.ClosedReason)
	return t, err
}

// CreateOpsTicket opens one ticket (title required; state machine starts at open).
func (s *Store) CreateOpsTicket(tenantID, kind, severity, title, detail, ownerRef, source, openedBy string, at time.Time) (OpsTicket, error) {
	if strings.TrimSpace(title) == "" {
		return OpsTicket{}, ErrBillingBadInput
	}
	if severity == "" {
		severity = "normal"
	}
	if source == "" {
		source = "manual"
	}
	t := OpsTicket{
		ID: newID("tkt_"), TenantID: tenantID, Kind: kind, Severity: severity,
		Title: title, Detail: detail, Status: ops.TicketOpen, OwnerRef: ownerRef,
		Source: source, OpenedAt: at.UTC().Format(time.RFC3339), OpenedBy: openedBy,
	}
	_, err := s.DB.Exec(
		`INSERT INTO ops_tickets(id,tenant_id,kind,severity,title,detail,status,owner_ref,source,opened_at,opened_by)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.TenantID, t.Kind, t.Severity, t.Title, t.Detail, t.Status, t.OwnerRef, t.Source, t.OpenedAt, t.OpenedBy)
	if err != nil {
		return OpsTicket{}, err
	}
	s.bumpEpoch()
	return t, nil
}

// GetOpsTicket reads one ticket by id.
func (s *Store) GetOpsTicket(id string) (OpsTicket, error) {
	row := s.DB.QueryRow(`SELECT `+opsTicketCols+` FROM ops_tickets WHERE id=?`, id)
	t, err := scanOpsTicket(row)
	if errors.Is(err, sql.ErrNoRows) {
		return OpsTicket{}, ErrNotFound
	}
	return t, err
}

// TransitionOpsTicket applies one state-machine step (rules live in the ops
// domain package; this layer only persists the verdict).
func (s *Store) TransitionOpsTicket(id, to, resolution, closeReason, actor string, at time.Time) (OpsTicket, error) {
	cur, err := s.GetOpsTicket(id)
	if err != nil {
		return OpsTicket{}, err
	}
	next, err := ops.TicketTransition(cur.Status, to, resolution, closeReason)
	if err != nil {
		return OpsTicket{}, err
	}
	ts := at.UTC().Format(time.RFC3339)
	var q string
	switch next {
	case ops.TicketConfirmed:
		q = `UPDATE ops_tickets SET status=?,confirmed_by=?,confirmed_at=? WHERE id=?`
		_, err = s.DB.Exec(q, next, actor, ts, id)
	case ops.TicketResolved:
		q = `UPDATE ops_tickets SET status=?,resolved_by=?,resolved_at=?,resolution=? WHERE id=?`
		_, err = s.DB.Exec(q, next, actor, ts, resolution, id)
	case ops.TicketClosed:
		q = `UPDATE ops_tickets SET status=?,closed_by=?,closed_at=?,closed_reason=? WHERE id=?`
		_, err = s.DB.Exec(q, next, actor, ts, closeReason, id)
	}
	if err != nil {
		return OpsTicket{}, err
	}
	s.bumpEpoch()
	return s.GetOpsTicket(id)
}

// ListOpsTickets lists tickets, newest first (optional status filter).
func (s *Store) ListOpsTickets(status string, limit int) ([]OpsTicket, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT ` + opsTicketCols + ` FROM ops_tickets`
	args := []any{}
	if status != "" {
		q += ` WHERE status=?`
		args = append(args, status)
	}
	q += ` ORDER BY opened_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OpsTicket{}
	for rows.Next() {
		t, err := scanOpsTicket(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ---- 演练(0128) ------------------------------------------------------------------

// OpsDrill is one backup/restore drill record.
type OpsDrill struct {
	ID                 string `json:"id"`
	Kind               string `json:"kind"`
	Target             string `json:"target"`
	Result             string `json:"result"`
	VerificationReport string `json:"verification_report"`
	ExecutedBy         string `json:"executed_by"`
	ExecutedAt         string `json:"executed_at"`
	Notes              string `json:"notes"`
}

// CreateOpsDrill inserts one drill (validation in the ops domain package).
func (s *Store) CreateOpsDrill(kind, target, result, report, executedBy, notes string, at time.Time) (OpsDrill, error) {
	if err := ops.ValidateDrill(kind, result, report); err != nil {
		return OpsDrill{}, err
	}
	d := OpsDrill{
		ID: newID("drl_"), Kind: kind, Target: target, Result: result,
		VerificationReport: report, ExecutedBy: executedBy,
		ExecutedAt: at.UTC().Format(time.RFC3339), Notes: notes,
	}
	_, err := s.DB.Exec(
		`INSERT INTO ops_drills(id,kind,target,result,verification_report,executed_by,executed_at,notes)
		 VALUES(?,?,?,?,?,?,?,?)`,
		d.ID, d.Kind, d.Target, d.Result, d.VerificationReport, d.ExecutedBy, d.ExecutedAt, d.Notes)
	if err != nil {
		return OpsDrill{}, err
	}
	s.bumpEpoch()
	return d, nil
}

// ListOpsDrills lists drills, newest first.
func (s *Store) ListOpsDrills(limit int) ([]OpsDrill, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.DB.Query(
		`SELECT id,kind,target,result,verification_report,executed_by,executed_at,notes
		 FROM ops_drills ORDER BY executed_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OpsDrill{}
	for rows.Next() {
		var d OpsDrill
		if err := rows.Scan(&d.ID, &d.Kind, &d.Target, &d.Result, &d.VerificationReport,
			&d.ExecutedBy, &d.ExecutedAt, &d.Notes); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ---- O5 跨租户模板读面(只读) ------------------------------------------------------

// OpsTemplateRow is one cross-tenant video-template directory row with its
// published version pointer (引用,不复制资产)。
type OpsTemplateRow struct {
	TenantID        string `json:"tenant_id"`
	ID              string `json:"id"`
	Name            string `json:"name"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
	VersionCount    int    `json:"version_count"`
	PublishedCount  int    `json:"published_count"`
	LatestVersion   int    `json:"latest_version"`
	AssignmentCount int    `json:"assignment_count"`
}

// ListTemplatesAll reads the platform-wide template directory with per-tenant
// version/assignment aggregates.
func (s *Store) ListTemplatesAll(limit int) ([]OpsTemplateRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.DB.Query(
		`SELECT t.tenant_id,t.id,t.name,t.created_at,t.updated_at,
		 (SELECT COUNT(*) FROM video_template_versions v WHERE v.template_id=t.id),
		 (SELECT COUNT(*) FROM video_template_versions v WHERE v.template_id=t.id AND v.status='published'),
		 COALESCE((SELECT MAX(v.version) FROM video_template_versions v WHERE v.template_id=t.id),0),
		 (SELECT COUNT(*) FROM video_template_assignments va WHERE va.template_id=t.id)
		 FROM video_templates t ORDER BY t.updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OpsTemplateRow{}
	for rows.Next() {
		var r OpsTemplateRow
		if err := rows.Scan(&r.TenantID, &r.ID, &r.Name, &r.CreatedAt, &r.UpdatedAt,
			&r.VersionCount, &r.PublishedCount, &r.LatestVersion, &r.AssignmentCount); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// OpsContentVersionRow is one store content-pack version with its risk state.
type OpsContentVersionRow struct {
	TenantID      string `json:"tenant_id"`
	PackID        string `json:"pack_id"`
	VersionID     string `json:"version_id"`
	Version       int    `json:"version"`
	Status        string `json:"status"`
	BlockedReason string `json:"blocked_reason"`
	UpdatedAt     string `json:"updated_at"`
}

// ListContentVersionsAll reads store content-pack versions across tenants
// (risk-takedown visibility: blocked rows carry their reason).
func (s *Store) ListContentVersionsAll(limit int) ([]OpsContentVersionRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.DB.Query(
		`SELECT tenant_id,pack_id,id,version,status,COALESCE(blocked_reason,''),updated_at
		 FROM store_content_pack_versions ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OpsContentVersionRow{}
	for rows.Next() {
		var r OpsContentVersionRow
		if err := rows.Scan(&r.TenantID, &r.PackID, &r.VersionID, &r.Version, &r.Status,
			&r.BlockedReason, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
