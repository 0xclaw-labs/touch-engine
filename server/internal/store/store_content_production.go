// store_content_production.go: HUI-4595 门店真实内容制作交接持久层(additive)。
//
// 纪律(裁定书/票面):
//   - 制作走公共任务权威(派诺生态 Task),本仓不自建制作队列:本层只持久化
//     商家明确提交的制作请求与真实引用投影(task_ref/quote_ref/charge_ref);
//     引用一律由受信路径落定,绝不伪造任务号或金额;
//   - 幂等:发起面 (tenant, idempotency_key) 唯一,同键重放返回同一行——
//     反复恢复同一制作请求不创建新任务或重复扣费(T15);
//   - 回流面 (source_app, event_id) 唯一 + 负载摘要比对:同键同摘要重放返回
//     同一行,同键异负载拒绝(SHARED-BRIEF 幂等语义);result_revision 单调
//     水位,旧稿迟到拒绝,旧回流绝不覆盖新采用(采用指针只在 3553 采用事务
//     内移动);
//   - 租户真值:handoff 行是 tenant/store/pack 的唯一权威;受信接缝
//     (GetProductionHandoffByID) 无租户作用域,调用方必须以行值为准;
//   - 状态独立可辨:recorded/submitted/produced/failed/canceled;failed 后
//     更高 revision 的 produced 是合法重试;canceled 是终态。
//   - SQLite 单连接纪律:先收尾迭代器再开新查询(同仓内惯例)。

package store

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
)

// 交接与回流的身份/状态词表。
const (
	ProductionStatusRecorded  = "recorded"  // 已登记;任务权威未接通的诚实态
	ProductionStatusSubmitted = "submitted" // 已提交平台任务(处理中)
	ProductionStatusProduced  = "produced"  // 已产出(至少一次 produced 回流)
	ProductionStatusFailed    = "failed"    // 生产方受信失败回执
	ProductionStatusCanceled  = "canceled"  // 生产方受信取消回执(终态)

	ProductionOutcomeProduced = "produced"
	ProductionOutcomeFailed   = "failed"
	ProductionOutcomeCanceled = "canceled"
)

// 水位/终态/task 引用拒绝(与 SHARED-BRIEF 标准负例同形)。同键异负载的
// IDEMPOTENCY_CONFLICT 复用仓内既有 ErrIdempotencyConflict(copydrafts)。
var (
	ErrStaleResult     = errors.New("store: stale production result revision")
	ErrHandoffCanceled = errors.New("store: production handoff is canceled")
	ErrTaskRefMismatch = errors.New("store: task_ref does not match the recorded task")
)

// ProductionHandoff is one merchant-initiated real-production request.
type ProductionHandoff struct {
	ID                 string `json:"id"`
	TenantID           string `json:"tenant_id"`
	StoreID            string `json:"store_id"`
	PackID             string `json:"pack_id"`
	ContentBindingRef  string `json:"content_binding_ref,omitempty"`
	QuoteRef           string `json:"quote_ref"`
	QuoteVerified      bool   `json:"quote_verified"`
	Status             string `json:"status"`
	TaskRef            string `json:"task_ref,omitempty"`
	LastChargeRef      string `json:"last_charge_ref,omitempty"`
	LastResultRevision int    `json:"last_result_revision"`
	Note               string `json:"note,omitempty"`
	IdempotencyKey     string `json:"idempotency_key,omitempty"`
	CreatedBy          string `json:"created_by"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
}

// ProductionHandoffInput is the merchant-language creation payload; refs are
// opaque non-empty strings (semantic declarations, never credentials).
type ProductionHandoffInput struct {
	StoreID           string
	PackID            string
	ContentBindingRef string
	QuoteRef          string
	QuoteVerified     bool
	Note              string
	IdempotencyKey    string
}

// ProductionResult is one append-only trusted-producer receipt (produced /
// failed / canceled are independent, ticket-mandated states). digest is the
// internal payload-integrity field and never travels on the JSON wire.
type ProductionResult struct {
	ID             string `json:"id"`
	TenantID       string `json:"tenant_id"`
	HandoffID      string `json:"handoff_id"`
	SourceApp      string `json:"source_app"`
	EventID        string `json:"event_id"`
	Outcome        string `json:"outcome"`
	TaskRef        string `json:"task_ref,omitempty"`
	ResultRevision int    `json:"result_revision"`
	ChargeRef      string `json:"charge_ref,omitempty"`
	VersionID      string `json:"version_id,omitempty"`
	DeclaredSHA    string `json:"declared_sha256,omitempty"`
	MeasuredSHA    string `json:"measured_sha256,omitempty"`
	Reason         string `json:"reason,omitempty"`
	CreatedAt      string `json:"created_at"`

	digest string
}

// ProductionResultInput carries the handler-verified declaration; Digest is
// the canonical payload digest computed by the HTTP layer.
type ProductionResultInput struct {
	HandoffID      string
	SourceApp      string
	EventID        string
	Outcome        string
	TaskRef        string
	ResultRevision int
	ChargeRef      string
	VersionID      string
	Digest         string
	DeclaredSHA    string
	MeasuredSHA    string
	Reason         string
}

const productionHandoffCols = `id,tenant_id,store_id,pack_id,content_binding_ref,quote_ref,quote_verified,status,` +
	`task_ref,last_charge_ref,last_result_revision,note,idempotency_key,created_by,created_at,updated_at`

func scanProductionHandoff(sc interface{ Scan(...any) error }) (ProductionHandoff, error) {
	var h ProductionHandoff
	var idemKey sql.NullString
	var verified int
	err := sc.Scan(&h.ID, &h.TenantID, &h.StoreID, &h.PackID, &h.ContentBindingRef, &h.QuoteRef, &verified, &h.Status,
		&h.TaskRef, &h.LastChargeRef, &h.LastResultRevision, &h.Note, &idemKey, &h.CreatedBy, &h.CreatedAt, &h.UpdatedAt)
	if err != nil {
		return ProductionHandoff{}, err
	}
	h.QuoteVerified = verified == 1
	h.IdempotencyKey = idemKey.String
	return h, nil
}

func (s *Store) productionHandoffByIdempotencyKey(tenantID, key string) (ProductionHandoff, error) {
	h, err := scanProductionHandoff(s.DB.QueryRow(
		`SELECT `+productionHandoffCols+` FROM store_content_production_handoffs WHERE tenant_id=? AND idempotency_key=?`,
		tenantID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return ProductionHandoff{}, ErrNotFound
	}
	return h, err
}

// CreateProductionHandoff records one production request. The same tenant +
// idempotency key replays the existing row (replayed=true) — retry never
// creates a second production project or a second charge. 空包合法: pack 有零
// 版本/零采用也放行(首次制作路径);跨店/跨租户引用一律 ErrNotFound。
func (s *Store) CreateProductionHandoff(tenantID string, in ProductionHandoffInput, createdBy string) (ProductionHandoff, bool, error) {
	key := contentpack.NormalizeText(in.IdempotencyKey)
	if err := contentpack.ValidateIdempotencyKey(key); err != nil {
		return ProductionHandoff{}, false, err
	}
	quoteRef := contentpack.NormalizeText(in.QuoteRef)
	if quoteRef == "" {
		return ProductionHandoff{}, false, contentpack.NewValidationError(contentpack.ReasonMissingPriceNote, "quote_ref")
	}
	if len(quoteRef) > contentpack.MaxIdempotencyKey {
		return ProductionHandoff{}, false, contentpack.NewValidationError(contentpack.ReasonBadIdempotencyKey, "quote_ref")
	}
	if _, err := s.GetStore(contentpack.NormalizeText(in.StoreID), tenantID); err != nil {
		return ProductionHandoff{}, false, err
	}
	// 空包合法的关键:只验证包存在且属于本店,不要求任何候选/采用。
	pack, err := s.GetStoreContentPack(contentpack.NormalizeText(in.PackID), tenantID)
	if err != nil {
		return ProductionHandoff{}, false, err
	}
	if pack.StoreID != contentpack.NormalizeText(in.StoreID) {
		return ProductionHandoff{}, false, ErrNotFound
	}
	bindingRef := contentpack.NormalizeText(in.ContentBindingRef)
	if cur, err := s.productionHandoffByIdempotencyKey(tenantID, key); err == nil {
		return cur, true, nil
	} else if !errors.Is(err, ErrNotFound) {
		return ProductionHandoff{}, false, err
	}
	h := ProductionHandoff{
		ID: newID("cph_"), TenantID: tenantID, StoreID: pack.StoreID, PackID: pack.ID,
		ContentBindingRef: bindingRef, QuoteRef: quoteRef, QuoteVerified: in.QuoteVerified,
		Status: ProductionStatusRecorded, Note: contentpack.NormalizeText(in.Note),
		IdempotencyKey: key, CreatedBy: createdBy, CreatedAt: now(), UpdatedAt: now(),
	}
	verified := 0
	if h.QuoteVerified {
		verified = 1
	}
	if _, err := s.DB.Exec(
		`INSERT INTO store_content_production_handoffs(`+productionHandoffCols+`)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		h.ID, h.TenantID, h.StoreID, h.PackID, h.ContentBindingRef, h.QuoteRef, verified, h.Status,
		h.TaskRef, h.LastChargeRef, h.LastResultRevision, h.Note, h.IdempotencyKey, h.CreatedBy, h.CreatedAt, h.UpdatedAt); err != nil {
		return ProductionHandoff{}, false, err
	}
	return h, false, nil
}

// GetProductionHandoff is tenant-scoped; foreign ids are ErrNotFound.
func (s *Store) GetProductionHandoff(tenantID, id string) (ProductionHandoff, error) {
	h, err := scanProductionHandoff(s.DB.QueryRow(
		`SELECT `+productionHandoffCols+` FROM store_content_production_handoffs WHERE tenant_id=? AND id=?`,
		tenantID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ProductionHandoff{}, ErrNotFound
	}
	return h, err
}

// GetProductionHandoffByID returns one handoff WITHOUT tenant scoping (the
// trusted producer seam). Callers MUST treat the returned row's TenantID /
// StoreID / PackID as the only authority — request input can never override
// them (票面: 请求中的 tenant/store 不能覆盖权威映射). Unknown -> ErrNotFound.
func (s *Store) GetProductionHandoffByID(id string) (ProductionHandoff, error) {
	h, err := scanProductionHandoff(s.DB.QueryRow(
		`SELECT `+productionHandoffCols+` FROM store_content_production_handoffs WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ProductionHandoff{}, ErrNotFound
	}
	return h, err
}

// ListProductionHandoffs returns the store's requests oldest-first.
func (s *Store) ListProductionHandoffs(tenantID, storeID string) ([]ProductionHandoff, error) {
	rows, err := s.DB.Query(
		`SELECT `+productionHandoffCols+` FROM store_content_production_handoffs WHERE tenant_id=? AND store_id=? ORDER BY created_at, id`,
		tenantID, storeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ProductionHandoff, 0)
	for rows.Next() {
		h, err := scanProductionHandoff(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// MarkProductionHandoffSubmitted records the REAL platform task reference
// after a successful authoritative submission. The task reference is
// immutable once set: a later submission naming a different task is a
// mismatch (retry with the same key must reuse the same task — T15).
func (s *Store) MarkProductionHandoffSubmitted(tenantID, id, taskRef string) (ProductionHandoff, error) {
	taskRef = contentpack.NormalizeText(taskRef)
	if taskRef == "" {
		return ProductionHandoff{}, contentpack.NewValidationError(contentpack.ReasonMissingTitle, "task_ref")
	}
	h, err := s.GetProductionHandoff(tenantID, id)
	if err != nil {
		return ProductionHandoff{}, err
	}
	if h.TaskRef != "" && h.TaskRef != taskRef {
		return ProductionHandoff{}, ErrTaskRefMismatch
	}
	if h.Status == ProductionStatusSubmitted && h.TaskRef == taskRef {
		return h, nil // idempotent re-confirm
	}
	if _, err := s.DB.Exec(
		`UPDATE store_content_production_handoffs SET status=?, task_ref=?, updated_at=? WHERE tenant_id=? AND id=?`,
		ProductionStatusSubmitted, taskRef, now(), tenantID, id); err != nil {
		return ProductionHandoff{}, err
	}
	return s.GetProductionHandoff(tenantID, id)
}

const productionResultCols = `id,tenant_id,handoff_id,source_app,event_id,outcome,task_ref,result_revision,` +
	`charge_ref,version_id,payload_digest,declared_sha256,measured_sha256,reason,created_at`

func scanProductionResult(sc interface{ Scan(...any) error }) (ProductionResult, error) {
	var r ProductionResult
	err := sc.Scan(&r.ID, &r.TenantID, &r.HandoffID, &r.SourceApp, &r.EventID, &r.Outcome, &r.TaskRef, &r.ResultRevision,
		&r.ChargeRef, &r.VersionID, &r.digest, &r.DeclaredSHA, &r.MeasuredSHA, &r.Reason, &r.CreatedAt)
	return r, err
}

func (s *Store) productionResultByEvent(sourceApp, eventID string) (ProductionResult, error) {
	r, err := scanProductionResult(s.DB.QueryRow(
		`SELECT `+productionResultCols+` FROM store_content_production_results WHERE source_app=? AND event_id=?`,
		sourceApp, eventID))
	if errors.Is(err, sql.ErrNoRows) {
		return ProductionResult{}, ErrNotFound
	}
	return r, err
}

// RegisterProductionResult is the trusted-producer append transaction:
//   - replay: same (source_app, event_id) + same digest returns the SAME row
//     (never a second registration, never a second charge);
//   - conflict: same key + different digest is refused;
//   - watermark: a NEW event whose revision does not advance past the stored
//     watermark is stale (旧稿迟到); the current adoption is never touched;
//   - canceled is terminal: new results are refused, replays still resolve;
//   - task_ref: once the handoff records a task, receipts must name it.
//
// On accepted produced/failed/canceled receipts the handoff row follows
// (status, last_charge_ref, last_result_revision) so the merchant list shows
// independent 处理中/已产出/失败/取消 states.
func (s *Store) RegisterProductionResult(in ProductionResultInput) (ProductionResult, bool, error) {
	sourceApp := contentpack.NormalizeText(in.SourceApp)
	eventID := contentpack.NormalizeText(in.EventID)
	if sourceApp == "" || eventID == "" {
		return ProductionResult{}, false, contentpack.NewValidationError(contentpack.ReasonMissingTitle, "source_app/event_id")
	}
	if in.ResultRevision < 1 {
		return ProductionResult{}, false, contentpack.NewValidationError(contentpack.ReasonBadIdempotencyKey, "result_revision")
	}
	outcome := contentpack.NormalizeText(in.Outcome)
	if outcome == "" {
		outcome = ProductionOutcomeProduced
	}
	switch outcome {
	case ProductionOutcomeProduced, ProductionOutcomeFailed, ProductionOutcomeCanceled:
	default:
		return ProductionResult{}, false, contentpack.NewValidationError(contentpack.ReasonBadContentKind, "outcome")
	}
	h, err := s.GetProductionHandoffByID(contentpack.NormalizeText(in.HandoffID))
	if err != nil {
		return ProductionResult{}, false, err
	}
	// 幂等优先于一切终态判断:丢失响应的重放必须总能解析回原行。
	if prev, err := s.productionResultByEvent(sourceApp, eventID); err == nil {
		if prev.HandoffID != h.ID || prev.PayloadDigest() != contentpack.NormalizeText(in.Digest) {
			return ProductionResult{}, false, ErrIdempotencyConflict
		}
		return prev, true, nil
	} else if !errors.Is(err, ErrNotFound) {
		return ProductionResult{}, false, err
	}
	if h.Status == ProductionStatusCanceled {
		return ProductionResult{}, false, ErrHandoffCanceled
	}
	if h.TaskRef != "" && contentpack.NormalizeText(in.TaskRef) != h.TaskRef {
		return ProductionResult{}, false, ErrTaskRefMismatch
	}
	if in.ResultRevision <= h.LastResultRevision {
		return ProductionResult{}, false, ErrStaleResult
	}
	r := ProductionResult{
		ID: newID("cpr_"), TenantID: h.TenantID, HandoffID: h.ID,
		SourceApp: sourceApp, EventID: eventID, Outcome: outcome,
		TaskRef: contentpack.NormalizeText(in.TaskRef), ResultRevision: in.ResultRevision,
		ChargeRef: contentpack.NormalizeText(in.ChargeRef), VersionID: contentpack.NormalizeText(in.VersionID),
		DeclaredSHA: strings.ToLower(contentpack.NormalizeText(in.DeclaredSHA)),
		MeasuredSHA: strings.ToLower(contentpack.NormalizeText(in.MeasuredSHA)),
		Reason:      contentpack.NormalizeText(in.Reason), CreatedAt: now(),
	}
	if _, err := s.DB.Exec(
		`INSERT INTO store_content_production_results(`+productionResultCols+`)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.TenantID, r.HandoffID, r.SourceApp, r.EventID, r.Outcome, r.TaskRef, r.ResultRevision,
		r.ChargeRef, r.VersionID, contentpack.NormalizeText(in.Digest), r.DeclaredSHA, r.MeasuredSHA, r.Reason, r.CreatedAt); err != nil {
		return ProductionResult{}, false, err
	}
	// 交接行跟随(produced/failed/canceled 独立可辨;水位只进不退)。
	newStatus := h.Status
	switch outcome {
	case ProductionOutcomeProduced:
		newStatus = ProductionStatusProduced
	case ProductionOutcomeFailed:
		newStatus = ProductionStatusFailed
	case ProductionOutcomeCanceled:
		newStatus = ProductionStatusCanceled
	}
	charge := h.LastChargeRef
	if r.ChargeRef != "" {
		charge = r.ChargeRef
	}
	if _, err := s.DB.Exec(
		`UPDATE store_content_production_handoffs SET status=?, last_charge_ref=?, last_result_revision=?, updated_at=? WHERE id=?`,
		newStatus, charge, r.ResultRevision, now(), h.ID); err != nil {
		return ProductionResult{}, false, err
	}
	return r, false, nil
}

// PayloadDigest exposes the stored digest for comparisons (kept off the JSON
// wire — the digest is an internal integrity field).
func (r ProductionResult) PayloadDigest() string { return r.digest }

// ListProductionResults returns the handoff's receipts oldest-first.
func (s *Store) ListProductionResults(tenantID, handoffID string) ([]ProductionResult, error) {
	rows, err := s.DB.Query(
		`SELECT `+productionResultCols+` FROM store_content_production_results WHERE tenant_id=? AND handoff_id=? ORDER BY created_at, id`,
		tenantID, handoffID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ProductionResult, 0)
	for rows.Next() {
		r, err := scanProductionResult(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
