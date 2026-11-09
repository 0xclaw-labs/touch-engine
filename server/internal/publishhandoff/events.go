// events.go: touch-handoff/v2 事件六层 —— 事实分层分记与 source+event_id 去重。
//
// 六层(裁定书/SHARED-BRIEF 约束 7,冻结命名,4585-4588 消费):
//
//	1 customer_click       点击(弱证据,永不升级状态)
//	2 wake_attempt         唤起尝试(弱证据,永不升级状态)
//	3 media_handoff        媒体交接(素材交出≠发布;下载开始不算平台成功)
//	4 customer_self_report 顾客自报(仅核验请求:不改状态、不触发奖励)
//	5 staff_confirm        店员确认(人工事实;永不转官方)
//	6 platform_confirm     平台证实(仅验签通过的官方回执到达)
//
// 去重语义(SHARED-BRIEF 事件通用语义):
//   - 每事件带 source(写入方标识)+event_id(写入方侧幂等 ID);
//   - (tenant, handoff, source, event_id) 唯一(0073 部分唯一索引);
//   - 同键同 kind = 静默去重(幂等重放);
//   - 同键不同 kind = ErrEventConflict(应用层映射 IDEMPOTENCY_CONFLICT)。
//
// 弱证据层(1-4)由公共 facts 面写入,对状态机结构性无害:requiredEvidence
// 不含任何由这些事件触发的迁移,状态只随强证据(verifier_proof/official_receipt/
// staff_observation/customer_action/system_*)迁移。
package publishhandoff

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Event kinds (v2 six fact layers — frozen names).
const (
	EventCustomerClick      = "customer_click"
	EventWakeAttempt        = "wake_attempt"
	EventMediaHandoff       = "media_handoff"
	EventCustomerSelfReport = "customer_self_report"
	EventStaffConfirm       = "staff_confirm"
	EventPlatformConfirm    = "platform_confirm"
)

// EventPlatformQuery records one adapter Query result (照录不确认).
const EventPlatformQuery = "platform_query"

// WeakFactKinds lists the four customer-side fact layers the public facts
// endpoint may record. They never move the state machine.
var WeakFactKinds = map[string]bool{
	EventCustomerClick: true, EventWakeAttempt: true,
	EventMediaHandoff: true, EventCustomerSelfReport: true,
}

// ErrEventConflict: same source+event_id recorded with a different kind
// (同幂等键不同负载拒绝,映射 HTTP 409 IDEMPOTENCY_CONFLICT).
var ErrEventConflict = errors.New("publishhandoff: event id already recorded with a different kind")

// AppendFact appends one deduped fact event. source+event_id are mandatory.
// Returns deduped=true when the exact (source, event_id, kind) triple was
// already recorded (idempotent replay, no second row).
func (s *Service) AppendFact(tenantID, handoffID, kind, source, eventID, actor string, ev EvidenceSource, payload map[string]any) (deduped bool, err error) {
	if tenantID == "" || handoffID == "" || kind == "" || source == "" || eventID == "" {
		return false, ErrBadInput
	}
	// 重放判定:同键存在即去重或冲突(不同 kind 绝不静默覆盖)。
	var seenKind string
	err = s.db.QueryRow(`SELECT kind FROM publish_handoff_events
		WHERE tenant_id=? AND handoff_id=? AND source=? AND event_id=?`,
		tenantID, handoffID, source, eventID).Scan(&seenKind)
	if err == nil {
		if seenKind != kind {
			return false, fmt.Errorf("%w: %s/%s is %s, not %s", ErrEventConflict, source, eventID, seenKind, kind)
		}
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("publishhandoff: fact lookup: %w", err)
	}
	if payload == nil {
		payload = map[string]any{}
	}
	raw, merr := json.Marshal(payload)
	if merr != nil {
		return false, fmt.Errorf("publishhandoff: fact payload: %w", merr)
	}
	if _, err := s.db.Exec(`INSERT INTO publish_handoff_events
		(id, tenant_id, handoff_id, kind, actor, evidence_source, receipt_id, source, event_id, payload, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		newID("phe_"), tenantID, handoffID, kind, actor, string(ev), "", source, eventID, string(raw), rfc3339(s.now())); err != nil {
		return false, fmt.Errorf("publishhandoff: append fact: %w", err)
	}
	return false, nil
}
