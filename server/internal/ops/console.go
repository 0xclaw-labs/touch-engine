// Package ops: HUI-4592 平台运营控制台的领域核(纯函数,不触库不看 HTTP)。
//
// 纪律(裁定书/票面):
//   - 本包冻结 ops 命令与操作白名单——命令集只增不改,改名即破坏 4593
//     已签发的 grant 与消费方;权限裁决本身归 authz(4593),本包只声明
//     "哪个 action 需要哪个命令",裁决由 HTTP 层用 authz.Can 执行;
//   - 操作信封 envelope 校验在进任何分派前完成:白名单外 action、缺
//     原因、缺 idempotency_key 在这里就拒绝;
//   - 支持访问限时是硬规则:无到期时间或超上限的申请在这里拒绝;
//   - 工单状态机 open→confirmed→resolved→closed,终态不可再改,关闭
//     必带理由;迁移规则唯一出处在本包,HTTP/store 都不自行判断。
package ops

import "errors"

// Command is one frozen ops permission string ("<dimension>:<command>").
// 权限串经由 4593 ops_grants 签发;改名即破坏已签发 grant——只增不改。
const (
	CmdTenantsRead     = "ops:tenants.read"
	CmdTenantsManage   = "ops:tenants.manage"
	CmdPlansRead       = "ops:plans.read"
	CmdPlansManage     = "ops:plans.manage"
	CmdOrdersRead      = "ops:orders.read"
	CmdOrdersManage    = "ops:orders.manage"
	CmdChannelsRead    = "ops:channels.read"
	CmdChannelsManage  = "ops:channels.manage"
	CmdTemplatesRead   = "ops:templates.read"
	CmdTemplatesManage = "ops:templates.manage"
	CmdOpsRead         = "ops:operations.read"
	CmdOpsManage       = "ops:operations.manage"
	CmdSupportManage   = "ops:support.manage"
)

// Action is one whitelisted operation dispatched by POST /api/v1/ops/operations.
// 每个动作分派到各域已有的唯一写面(本票不新造第二写面)。
const (
	ActionTenantSuspend    = "tenant.suspend"
	ActionTenantResume     = "tenant.resume"
	ActionChargeHoldSet    = "tenant.charge_hold.set"
	ActionChargeHoldClear  = "tenant.charge_hold.clear"
	ActionPlanPublish      = "plan.publish"
	ActionPlanRetire       = "plan.retire"
	ActionOrderRecheck     = "order.recheck"
	ActionChannelDisable   = "channel.disable"
	ActionChannelRestore   = "channel.restore"
	ActionContentRetract   = "content.retract"
	ActionAssignmentCancel = "template.assignment.cancel"
	ActionTicketOpen       = "ticket.open"
	ActionTicketConfirm    = "ticket.confirm"
	ActionTicketResolve    = "ticket.resolve"
	ActionTicketClose      = "ticket.close"
	ActionDrillRecord      = "drill.record"
)

// ActionSpec binds one action to the ops command it requires.
type ActionSpec struct {
	Command string
	// Revisioned marks resources guarded by expected_revision (tenant rows).
	// 未标位的资源(3535 能力行/目录行)暂无 revision 列,expected_revision
	// 可空——结构上不假装有守卫。
	Revisioned bool
}

var actionRegistry = map[string]ActionSpec{
	ActionTenantSuspend:    {Command: CmdTenantsManage, Revisioned: true},
	ActionTenantResume:     {Command: CmdTenantsManage, Revisioned: true},
	ActionChargeHoldSet:    {Command: CmdTenantsManage, Revisioned: true},
	ActionChargeHoldClear:  {Command: CmdTenantsManage, Revisioned: true},
	ActionPlanPublish:      {Command: CmdPlansManage},
	ActionPlanRetire:       {Command: CmdPlansManage},
	ActionOrderRecheck:     {Command: CmdOrdersManage},
	ActionChannelDisable:   {Command: CmdChannelsManage},
	ActionChannelRestore:   {Command: CmdChannelsManage},
	ActionContentRetract:   {Command: CmdTemplatesManage},
	ActionAssignmentCancel: {Command: CmdTemplatesManage},
	ActionTicketOpen:       {Command: CmdOpsManage},
	ActionTicketConfirm:    {Command: CmdOpsManage},
	ActionTicketResolve:    {Command: CmdOpsManage},
	ActionTicketClose:      {Command: CmdOpsManage},
	ActionDrillRecord:      {Command: CmdOpsManage},
}

// LookupAction returns the spec for a whitelisted action (ok=false otherwise).
func LookupAction(action string) (ActionSpec, bool) {
	spec, ok := actionRegistry[action]
	return spec, ok
}

// Ticket statuses (0127 状态机唯一出处)。
const (
	TicketOpen      = "open"
	TicketConfirmed = "confirmed"
	TicketResolved  = "resolved"
	TicketClosed    = "closed"
)

var (
	// ErrActionUnknown: 白名单外动作(结构上不可分派)。
	ErrActionUnknown = errors.New("ops: action_not_allowed")
	// ErrReasonRequired: 审计三件套之一,ops 写面强制。
	ErrReasonRequired = errors.New("ops: reason_required")
	// ErrIdempotencyKeyRequired: 重试语义的定位键。
	ErrIdempotencyKeyRequired = errors.New("ops: idempotency_key_required")
	// ErrRevisionRequired: revision 守卫资源缺 expected_revision。
	ErrRevisionRequired = errors.New("ops: revision_required")
	// ErrTicketTransition: 工单状态机非法迁移(终态不可再改)。
	ErrTicketTransition = errors.New("ops: ticket_transition_not_allowed")
	// ErrTicketCloseReason: 关闭必带理由。
	ErrTicketCloseReason = errors.New("ops: ticket_close_reason_required")
	// ErrSupportTTL: 支持访问必须限时且不超上限。
	ErrSupportTTL = errors.New("ops: support_ttl_invalid")
)

// MaxSupportAccessMinutes bounds one support-access grant (硬上限 4 小时;
// 到期/撤销由每请求实时校验兜底,这里是申请侧的第一道闸)。
const MaxSupportAccessMinutes = 240

// ValidateEnvelope checks the operation envelope before any dispatch.
// reason 与 idempotency_key 是硬要求;revisioned 资源必须声明 expected_revision。
func ValidateEnvelope(action, reason, idemKey, expectedRevision string) error {
	if _, ok := actionRegistry[action]; !ok {
		return ErrActionUnknown
	}
	if len(reason) == 0 || len(reason) > 500 {
		return ErrReasonRequired
	}
	if idemKey == "" || len(idemKey) > 120 {
		return ErrIdempotencyKeyRequired
	}
	if actionRegistry[action].Revisioned && expectedRevision == "" {
		return ErrRevisionRequired
	}
	return nil
}

// TicketTransition decides whether status → to is allowed and what the
// transition requires. 终态(closed)不可再改;resolve 必带处理说明;close
// 必带关闭理由。返回 (nextStatus, error)。
func TicketTransition(status, to, resolution, closeReason string) (string, error) {
	switch {
	case status == TicketOpen && to == TicketConfirmed:
		return TicketConfirmed, nil
	case status == TicketConfirmed && to == TicketResolved:
		if resolution == "" {
			return "", ErrTicketTransition
		}
		return TicketResolved, nil
	case status == TicketResolved && to == TicketClosed:
		if closeReason == "" {
			return "", ErrTicketCloseReason
		}
		return TicketClosed, nil
	case status == TicketOpen && to == TicketClosed:
		// 误报直接关闭也是合法路径,但必须留关闭理由。
		if closeReason == "" {
			return "", ErrTicketCloseReason
		}
		return TicketClosed, nil
	default:
		return "", ErrTicketTransition
	}
}

// DrillKind / DrillResult 枚举(0128)。
const (
	DrillBackup  = "backup"
	DrillRestore = "restore"
)

// ValidateDrill checks one drill record. restore 演练必带恢复校验报告
// ——只写"已备份"不算演练。
func ValidateDrill(kind, result, verificationReport string) error {
	if kind != DrillBackup && kind != DrillRestore {
		return errors.New("ops: drill_kind_invalid")
	}
	if result != "ok" && result != "failed" {
		return errors.New("ops: drill_result_invalid")
	}
	if kind == DrillRestore && verificationReport == "" {
		return errors.New("ops: verification_report_required")
	}
	return nil
}
