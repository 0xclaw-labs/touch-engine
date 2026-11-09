// service2.go: touch-handoff/v2 服务面 —— 动作实例交接的创建、事实分记、
// 自动提交/回查与平台回调。publish_handoffs / publish_handoff_events 两表的
// 唯一写入方仍是本包;v2 行(payload_kind 非空)与 v1 /c 行共用状态机与
// 证据门禁,计划与 payload 面按 v2 冻结契约落列。
//
// 纪律(裁定书):
//   - gate-first:refused / pending_external 零落行(只回诚实原因);
//   - 请求权威 = 3546 动作会话(httpapi 校验会话归属与 action_revision 后
//     传入;participation/attempt 可选,给了就必须已冻结);
//   - payload_kind 分流:media 必须指纹确认(3553 已采用版本);
//     link_share 无内容包、无媒体授权、share_url 服务端生成;
//   - 幂等:同键同负载回原行,同键不同负载 = ErrIdempotencyConflict
//     (IDEMPOTENCY_CONFLICT);
//   - 本文件零平台外呼:adapter 调用点(Prepare/Submit/Query/VerifyCallback)
//     是唯一平台边界,未注册=全部 ErrUnsupported(诚实缺位)。
package publishhandoff

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
)

// ActionStartInput is one confirmed v2 handoff request. Record/Query are the
// server-resolved admission facts (the httpapi layer resolves the record and
// derives RewardPlay/MerchantAppRef — 客户端标记永不参与); PlanPayload carries
// the resolved snapshot/share target for plan + confirmation.
type ActionStartInput struct {
	TenantID    string
	StoreID     string
	ActionID    string // 4584 动作实例 id (sa_*)
	CatalogCode string
	Platform    string
	SessionID   string // 3546 动作会话
	VisitorID   string

	ActionRevision int
	PayloadKind    string // media | link_share
	Method         string
	Environment    string
	Scenario       string

	ParticipationID string // 可空
	AttemptID       string // 可空(给了就必须 handed_off)

	IdempotencyKey string
	Confirmation   string // media=快照指纹;link_share=share_url 本身

	// Record + Query are the resolved 3535 record and server-derived query.
	Record channeladmission.ActionCapability
	Query  channeladmission.ActionQuery
	// PlanPayload carries the resolved snapshot/share target.
	PlanPayload ActionPlanInput
	// ContentPackVersionID is the adopted version reference for media payloads
	// (resolved by the httpapi layer from the session binding; frozen on the row).
	ContentPackVersionID string
}

// ActionHandoff is the safe-to-expose v2 view of one handoff row.
type ActionHandoff struct {
	Handoff
	ActionID          string
	CatalogCode       string
	SessionID         string
	ActionRevision    int
	PayloadKind       string
	Method            string
	Environment       string
	PlanKind          string
	EvidenceLevel     string
	ClientAction      *ClientAction
	UnsupportedFields []string
	ShareURL          string
	RequestHash       string
}

// ActionResult reports the created-or-replayed v2 handoff with its frozen plan.
type ActionResult struct {
	Handoff ActionHandoff
	Plan    ActionPlan
	Created bool
}

// ErrIdempotencyConflict: same idempotency key, different payload
// (SHARED-BRIEF: 同幂等键不同负载拒绝)。
var ErrIdempotencyConflict = errors.New("publishhandoff: idempotency key already used with a different payload")

// ErrPayloadMismatch: payload_kind contradicts the route's required_inputs.
var ErrPayloadMismatch = errors.New("publishhandoff: payload_kind does not match the route's required inputs")

// actionHandoffCols is the v2 read projection (participation/attempt NULL-safe:
// link_share 行合法无参与).
const actionHandoffCols = `id, tenant_id, store_id, COALESCE(participation_id,''), COALESCE(attempt_id,''), platform, entry_mode,
	scenario, status, evidence_source, content_version, content_pack_version_id,
	query_count, timed_out, cancel_reason, created_at, updated_at,
	action_id, catalog_code, action_session_id, action_revision, payload_kind, method,
	environment, plan_kind, evidence_level, client_action_json, unsupported_fields_json,
	share_url, request_hash`

// scanActionHandoff reads the full v2 projection.
func (s *Service) scanActionHandoff(row rowScanner) (ActionHandoff, error) {
	var h ActionHandoff
	var status, ev, clientJSON, unsupportedJSON string
	var timedOut int
	err := row.Scan(&h.ID, &h.TenantID, &h.StoreID, &h.ParticipationID, &h.AttemptID, &h.Platform,
		&h.EntryMode, &h.Scenario, &status, &ev, &h.ContentVersion, &h.ContentPackVersionID,
		&h.QueryCount, &timedOut, &h.CancelReason, &h.CreatedAt, &h.UpdatedAt,
		&h.ActionID, &h.CatalogCode, &h.SessionID, &h.ActionRevision, &h.PayloadKind, &h.Method,
		&h.Environment, &h.PlanKind, &h.EvidenceLevel, &clientJSON, &unsupportedJSON,
		&h.ShareURL, &h.RequestHash)
	if err != nil {
		return ActionHandoff{}, err
	}
	h.Status, h.EvidenceSource, h.TimedOut = State(status), EvidenceSource(ev), timedOut == 1
	if clientJSON != "" && clientJSON != "{}" {
		var ca ClientAction
		if err := json.Unmarshal([]byte(clientJSON), &ca); err == nil && ca.Type != "" {
			h.ClientAction = &ca
		}
	}
	if unsupportedJSON != "" && unsupportedJSON != "[]" {
		_ = json.Unmarshal([]byte(unsupportedJSON), &h.UnsupportedFields)
	}
	return h, nil
}

// RequestFingerprint is the payload hash guarding the idempotency key: same
// key + same resolved payload = replay; same key + different payload =
// conflict. It covers the semantically load-bearing request fields only.
func RequestFingerprint(in ActionStartInput, confirmation string) string {
	parts := []string{
		in.TenantID, in.StoreID, in.ActionID, in.Platform, in.SessionID,
		in.PayloadKind, in.Method, in.Environment, in.Scenario,
		in.ParticipationID, in.AttemptID, confirmation,
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(sum[:])
}

// StartAction opens (or replays) one v2 handoff:
//
//	gate(JudgeActionRoute) → payload/required_inputs 分流 → confirmation 核验
//	→ 幂等(同键同负载回放/异负载冲突) → 参与/尝试可选冻结核验 → 落行
//	(previewed→preparing→ready,计划冻结) → adapter Prepare(可缺)。
//
// Gate 拒绝与确认不符零落行;Prepare 失败诚实 failed(零付费生成,零平台重试)。
func (s *Service) StartAction(_ context.Context, in ActionStartInput) (ActionResult, error) {
	switch {
	case strings.TrimSpace(in.TenantID) == "", strings.TrimSpace(in.StoreID) == "",
		strings.TrimSpace(in.ActionID) == "", strings.TrimSpace(in.Platform) == "",
		strings.TrimSpace(in.SessionID) == "", strings.TrimSpace(in.VisitorID) == "",
		strings.TrimSpace(in.IdempotencyKey) == "", strings.TrimSpace(in.Method) == "",
		strings.TrimSpace(in.Environment) == "", strings.TrimSpace(in.PayloadKind) == "":
		return ActionResult{}, ErrBadInput
	}
	if in.PayloadKind != PayloadKindMedia && in.PayloadKind != PayloadKindLinkShare {
		return ActionResult{}, fmt.Errorf("%w: payload_kind must be media|link_share", ErrBadInput)
	}

	// 1) 闸(JudgeActionRoute;RewardPlay/MerchantAppRef 已由服务端派生冻结)。
	plan := JudgeActionPlan(in.Query, in.Record, in.PlanPayload)
	if !plan.Allowed {
		return ActionResult{}, &HandoffRefusedError{
			Reason: plan.Reason, ReasonText: plan.ReasonText, Notice: plan.Notice,
			Missing: plan.Missing, PendingExternal: plan.PendingExternal,
		}
	}

	// 2) payload 分流:required_inputs 说了算(3535 合同 §4 语义)。
	requiresContent := false
	for _, r := range in.Record.RequiredInputs {
		if strings.TrimSpace(r) == "content_pack_version" {
			requiresContent = true
		}
	}
	if requiresContent && in.PayloadKind != PayloadKindMedia {
		return ActionResult{}, fmt.Errorf("%w: this route requires a real adopted content pack version", ErrPayloadMismatch)
	}
	if !requiresContent && in.PayloadKind != PayloadKindLinkShare {
		return ActionResult{}, fmt.Errorf("%w: this route is a pure link/open target and takes no content pack", ErrPayloadMismatch)
	}

	// 3) 幂等先于确认核验:同键同负载回放;同键不同负载冲突
	// (负载指纹含请求原样 confirmation——客户端确认了不同内容=不同负载)。
	requestHash := RequestFingerprint(in, in.Confirmation)
	if existing, ok, err := s.byKey(in.TenantID, in.IdempotencyKey); err != nil {
		return ActionResult{}, err
	} else if ok {
		full, ferr := s.actionByID(in.TenantID, existing.ID)
		if ferr != nil {
			return ActionResult{}, ferr
		}
		if full.RequestHash != requestHash {
			return ActionResult{}, ErrIdempotencyConflict
		}
		return ActionResult{Handoff: full, Plan: s.frozenPlan(full), Created: false}, nil
	}

	// 4) 确认:media=快照指纹;link_share=share_url 本身。换内容=换确认。
	wantConfirmation, err := ConfirmationFor(in.PayloadKind, in.PlanPayload)
	if err != nil {
		return ActionResult{}, ErrBadInput
	}
	if in.Confirmation != wantConfirmation {
		return ActionResult{}, ErrConfirmationMismatch
	}

	// 5) 参与/尝试可选:给了就必须已按 3546 语义冻结。
	if strings.TrimSpace(in.ParticipationID) != "" {
		if strings.TrimSpace(in.AttemptID) == "" {
			return ActionResult{}, ErrAttemptNotFrozen
		}
		if err := s.requireFrozenAttempt2(in.TenantID, in.ParticipationID, in.AttemptID); err != nil {
			return ActionResult{}, err
		}
	}

	// 6) 落行 + 状态链(previewed→preparing→ready;计划与 Gate 上下文冻结在行)。
	clientJSON, err := json.Marshal(plan.ClientAction)
	if err != nil {
		return ActionResult{}, err
	}
	unsupportedJSON, err := json.Marshal(plan.UnsupportedFields)
	if err != nil {
		return ActionResult{}, err
	}
	gateJSON, err := json.Marshal(map[string]any{
		"reward_play": in.Query.RewardPlay, "merchant_app_ref": in.Query.MerchantAppRef,
		"environment": in.Query.Environment, "record_id": in.Record.ID,
		"scenario": in.Scenario, "status": in.Record.Status,
	})
	if err != nil {
		return ActionResult{}, err
	}
	mediaJSON, err := json.Marshal(in.PlanPayload.Media)
	if err != nil {
		return ActionResult{}, err
	}
	textJSON, err := json.Marshal(in.PlanPayload.Text)
	if err != nil {
		return ActionResult{}, err
	}
	now := rfc3339(s.now())
	h := ActionHandoff{Handoff: Handoff{
		ID: newID("pha_"), TenantID: in.TenantID, StoreID: in.StoreID,
		Platform: in.Platform, Scenario: in.Scenario, Status: StatePreviewed,
		EvidenceSource: EvidenceCustomerAction, ContentVersion: wantConfirmation,
		CreatedAt: now, UpdatedAt: now,
	},
		ActionID: in.ActionID, CatalogCode: in.CatalogCode, SessionID: in.SessionID,
		ActionRevision: in.ActionRevision, PayloadKind: in.PayloadKind, Method: in.Method,
		Environment: in.Environment, PlanKind: plan.Kind, EvidenceLevel: plan.EvidenceLevel,
		ClientAction: plan.ClientAction, UnsupportedFields: plan.UnsupportedFields,
		ShareURL: in.PlanPayload.ShareURL, RequestHash: requestHash,
	}
	contentPackVersionID := ""
	if in.PayloadKind == PayloadKindMedia {
		// media 路线的版本引用由 httpapi 随输入解析冻结(3553 已采用版本)。
		contentPackVersionID = in.ContentPackVersionID
	}
	if _, err := s.db.Exec(`INSERT INTO publish_handoffs
		(id, tenant_id, store_id, participation_id, attempt_id, platform, scenario,
		 idempotency_key, status, evidence_source, content_version, content_pack_version_id,
		 media_json, text_json, visitor_id, created_at, updated_at,
		 action_id, catalog_code, action_session_id, action_revision, payload_kind,
		 method, environment, plan_kind, evidence_level, client_action_json,
		 unsupported_fields_json, share_url, request_hash, gate_json)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		h.ID, in.TenantID, in.StoreID, nullIfEmpty(in.ParticipationID), nullIfEmpty(in.AttemptID),
		in.Platform, in.Scenario, in.IdempotencyKey, string(StatePreviewed),
		string(EvidenceCustomerAction), wantConfirmation, contentPackVersionID,
		string(mediaJSON), string(textJSON), in.VisitorID, now, now,
		in.ActionID, in.CatalogCode, in.SessionID, in.ActionRevision, in.PayloadKind,
		in.Method, in.Environment, plan.Kind, plan.EvidenceLevel, string(clientJSON),
		string(unsupportedJSON), h.ShareURL, requestHash, string(gateJSON)); err != nil {
		return ActionResult{}, fmt.Errorf("publishhandoff: insert v2: %w", err)
	}
	preparing, err := s.setState(h.Handoff, StateHandoffPreparing, EvidenceCustomerAction)
	if err != nil {
		return ActionResult{}, err
	}
	ready, err := s.setState(preparing, StateHandoffReady, EvidenceSystemAdapter)
	if err != nil {
		return ActionResult{}, err
	}
	h.Handoff = ready

	// 7) adapter Prepare(可缺):平台 adapter 细化 client_action。失败=诚实
	// failed(不伪造入口;零付费生成)。
	if adapter := s.AdapterFor(in.Platform); adapter != nil {
		prepared, perr := adapter.Prepare(context.Background(), PrepareRequest{
			Handoff: h, Plan: plan, Text: in.PlanPayload.Text, Media: in.PlanPayload.Media,
			MediaRefs: in.PlanPayload.MediaRefs, ShareURL: in.PlanPayload.ShareURL,
			SessionID: in.SessionID, Environment: in.Environment,
		})
		if perr != nil {
			failed, ferr := s.setState(h.Handoff, StateFailed, EvidenceSystemAdapter)
			if ferr != nil {
				return ActionResult{}, errors.Join(perr, ferr)
			}
			h.Handoff = failed
			return ActionResult{}, fmt.Errorf("%w: adapter prepare: %v", ErrMediaPreparation, perr)
		}
		if prepared.ClientAction.Type != "" {
			merged := prepared.ClientAction
			if len(prepared.Payload) > 0 {
				if merged.Params == nil {
					merged.Params = map[string]string{}
				}
				for k, v := range prepared.Payload {
					merged.Params[k] = v
				}
			}
			if raw, merr := json.Marshal(merged); merr == nil {
				if _, uerr := s.db.Exec(`UPDATE publish_handoffs SET client_action_json=?,
					unsupported_fields_json=?, updated_at=? WHERE id=? AND tenant_id=?`,
					string(raw), mustJSON(prepared.UnsupportedFields), rfc3339(s.now()), h.ID, in.TenantID); uerr == nil {
					h.ClientAction = &merged
					h.UnsupportedFields = prepared.UnsupportedFields
				}
			}
		}
	}

	// 冻结计划回读(行内 client_action 为准)。
	full, err := s.actionByID(in.TenantID, h.ID)
	if err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Handoff: full, Plan: s.frozenPlan(full), Created: true}, nil
}

// frozenPlan rebuilds the row-frozen plan view (GET/replay never re-judge:
// 既有交接保留其入口,准入变化只影响新交接).
func (s *Service) frozenPlan(h ActionHandoff) ActionPlan {
	plan := ActionPlan{
		Kind: h.PlanKind, Allowed: IsActive(h.Status) && h.PlanKind != PlanPendingExternal && h.PlanKind != PlanRefused,
		Notice: "", EvidenceLevel: h.EvidenceLevel,
		ClientAction: h.ClientAction, UnsupportedFields: h.UnsupportedFields,
	}
	return plan
}

// GetActionForVisitor reads the caller's OWN v2 handoff (visitor binding is
// the access secret; mismatch answers ErrNotFound, 防枚举).
func (s *Service) GetActionForVisitor(_ context.Context, id, visitorID string) (ActionHandoff, error) {
	if visitorID == "" {
		return ActionHandoff{}, ErrNotFound
	}
	h, err := s.scanActionHandoff(s.db.QueryRow(`SELECT `+actionHandoffCols+` FROM publish_handoffs
		WHERE id=? AND visitor_id=?`, id, visitorID))
	if errors.Is(err, sql.ErrNoRows) {
		return ActionHandoff{}, ErrNotFound
	}
	if err != nil {
		return ActionHandoff{}, err
	}
	// v1 行(payload_kind='')不走 v2 读面。
	if h.PayloadKind == "" {
		return ActionHandoff{}, ErrNotFound
	}
	return h, nil
}

// actionByID reads the full v2 row inside a tenant.
func (s *Service) actionByID(tenantID, id string) (ActionHandoff, error) {
	h, err := s.scanActionHandoff(s.db.QueryRow(`SELECT `+actionHandoffCols+` FROM publish_handoffs
		WHERE id=? AND tenant_id=?`, id, tenantID))
	if errors.Is(err, sql.ErrNoRows) {
		return ActionHandoff{}, ErrNotFound
	}
	if err != nil {
		return ActionHandoff{}, err
	}
	return h, nil
}

// RecordActionFact records one customer-side fact layer (click/wake/media/
// self_report) on the caller's own handoff. Weak facts never move the state.
func (s *Service) RecordActionFact(_ context.Context, id, visitorID, kind, source, eventID string, payload map[string]any) (ActionHandoff, bool, error) {
	if !WeakFactKinds[kind] {
		return ActionHandoff{}, false, ErrBadInput
	}
	h, err := s.GetActionForVisitor(_context(), id, visitorID)
	if err != nil {
		return ActionHandoff{}, false, err
	}
	deduped, err := s.AppendFact(h.TenantID, h.ID, kind, source, eventID, "customer", EvidenceCustomerAction, payload)
	if err != nil {
		return h, false, err
	}
	return h, deduped, nil
}

// CancelAction ends the caller's own v2 handoff (terminal; never resurrects).
func (s *Service) CancelAction(_ context.Context, id, visitorID, reason string) (ActionHandoff, error) {
	h, err := s.GetActionForVisitor(_context(), id, visitorID)
	if err != nil {
		return ActionHandoff{}, err
	}
	next, err := s.setState(h.Handoff, StateCanceled, EvidenceCustomerAction)
	if err != nil {
		return ActionHandoff{}, err
	}
	if _, err := s.db.Exec(`UPDATE publish_handoffs SET cancel_reason=?, updated_at=? WHERE id=? AND tenant_id=?`,
		strings.TrimSpace(reason), rfc3339(s.now()), h.ID, h.TenantID); err != nil {
		return ActionHandoff{}, fmt.Errorf("publishhandoff: cancel reason: %w", err)
	}
	if err := s.appendEvent(h.TenantID, h.ID, "canceled", "customer", EvidenceCustomerAction, "", map[string]any{
		"reason": strings.TrimSpace(reason),
	}); err != nil {
		return ActionHandoff{}, err
	}
	h.Handoff = next
	h.CancelReason = strings.TrimSpace(reason)
	return h, nil
}

// SubmitAction drives the platform adapter's Submit for the caller's own
// handoff. Unsupported/missing adapter = ErrUnsupported (状态不推进). Success
// moves handoff_ready/editor_opened → submitting (证据=system_adapter).
func (s *Service) SubmitAction(ctx context.Context, id, visitorID string) (ActionHandoff, error) {
	h, err := s.GetActionForVisitor(_context(), id, visitorID)
	if err != nil {
		return ActionHandoff{}, err
	}
	adapter := s.AdapterFor(h.Platform)
	if adapter == nil {
		return ActionHandoff{}, ErrUnsupported
	}
	if h.Status != StateHandoffReady && h.Status != StateEditorOpened {
		return h, fmt.Errorf("%w: %s is not submittable", ErrInvalidState, h.Status)
	}
	prepared, err := adapter.Prepare(ctx, s.prepareRequestFor(h))
	if err != nil {
		return h, fmt.Errorf("%w: adapter prepare: %v", ErrUnsupported, err)
	}
	if err := adapter.Submit(ctx, h.Handoff, prepared); err != nil {
		return h, err
	}
	next, err := s.setState(h.Handoff, StateSubmitting, EvidenceSystemAdapter)
	if err != nil {
		return h, err
	}
	h.Handoff = next
	return h, nil
}

// QueryAction re-checks the SAME handoff through the adapter (先回查,不重投):
// counts the attempt, records platform_query (照录不确认). A query-produced
// verified Receipt flows through the full callback pipeline.
func (s *Service) QueryAction(ctx context.Context, id, visitorID string) (ActionHandoff, error) {
	h, err := s.GetActionForVisitor(_context(), id, visitorID)
	if err != nil {
		return ActionHandoff{}, err
	}
	adapter := s.AdapterFor(h.Platform)
	if adapter == nil {
		return h, ErrUnsupported
	}
	if h.Status != StateSubmitting && h.Status != StateUnknown {
		return h, fmt.Errorf("%w: %s has nothing in flight", ErrInvalidState, h.Status)
	}
	outcome, err := adapter.Query(ctx, h.Handoff)
	if err != nil {
		return h, err
	}
	if _, err := s.db.Exec(`UPDATE publish_handoffs SET query_count=query_count+1, updated_at=? WHERE id=? AND tenant_id=?`,
		rfc3339(s.now()), h.ID, h.TenantID); err != nil {
		return h, fmt.Errorf("publishhandoff: query: %w", err)
	}
	h.QueryCount++
	detail := map[string]any{"state": outcome.State}
	for k, v := range outcome.Detail {
		detail[k] = v
	}
	if _, err := s.AppendFact(h.TenantID, h.ID, EventPlatformQuery, "adapter:"+h.Platform, newID("q_"), "", EvidenceSystemAdapter, detail); err != nil {
		return h, err
	}
	if outcome.Receipt != nil {
		// 平台侧受信凭据:与回调完全相同的绑定→去重→确认管线(查询本身
		// 永不确认——确认只随五绑定通过的回执语义到达)。
		confirmed, rerr := s.confirmPipeline(h.Handoff, h.TenantID, h.ID, *outcome.Receipt)
		if rerr != nil {
			return h, rerr
		}
		h.Handoff = confirmed
		return h, nil
	}
	return h, nil
}

// ApplyPlatformCallback verifies one raw platform callback through the
// registered adapter, then runs the same bind→dedupe→confirm pipeline as the
// HMAC path (v2 rows emit platform_confirm). No adapter / unsupported = refuse
// everything (NOT_VERIFIED 残差). 租户从行取真值,回执声称与行不符即拒。
func (s *Service) ApplyPlatformCallback(ctx context.Context, platform string, raw CallbackPayload) (ActionHandoff, error) {
	adapter := s.AdapterFor(platform)
	if adapter == nil {
		return ActionHandoff{}, ErrReceiptUnverified
	}
	receipt, err := adapter.VerifyCallback(ctx, raw)
	if err != nil {
		return ActionHandoff{}, fmt.Errorf("%w: %v", ErrReceiptUnverified, err)
	}
	if strings.TrimSpace(receipt.Platform) == "" {
		receipt.Platform = platform
	}
	if strings.TrimSpace(receipt.ReceiptID) == "" || strings.TrimSpace(receipt.HandoffID) == "" {
		return ActionHandoff{}, fmt.Errorf("%w: receipt id/handoff reference required", ErrReceiptUnverified)
	}
	h, err := s.LoadForCallback(ctx, receipt.HandoffID)
	if err != nil {
		return ActionHandoff{}, err
	}
	next, err := s.confirmPipeline(h, h.TenantID, h.ID, receipt)
	if err != nil {
		return ActionHandoff{Handoff: h}, err
	}
	full, err := s.actionByID(h.TenantID, h.ID)
	if err != nil {
		return ActionHandoff{}, err
	}
	full.Handoff = next
	return full, nil
}

// prepareRequestFor builds the adapter Prepare request from a frozen row.
func (s *Service) prepareRequestFor(h ActionHandoff) PrepareRequest {
	text, media, _ := s.SnapshotOf(_context(), h.ID, h.TenantID)
	return PrepareRequest{
		Handoff: h, Text: text, Media: media, ShareURL: h.ShareURL,
		SessionID: h.SessionID, Environment: h.Environment,
	}
}

func (s *Service) requireFrozenAttempt2(tenantID, participationID, attemptID string) error {
	var status string
	err := s.db.QueryRow(`SELECT status FROM participation_attempts
		WHERE id=? AND tenant_id=? AND participation_id=?`, attemptID, tenantID, participationID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAttemptNotFrozen
	}
	if err != nil {
		return fmt.Errorf("publishhandoff: attempt lookup: %w", err)
	}
	if status != "handed_off" {
		return ErrAttemptNotFrozen
	}
	return nil
}

func nullIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func mustJSON(v []string) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(raw)
}
