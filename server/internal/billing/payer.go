// payer.go: 订单付款授权解析(HUI-4591 F15 × HUI-4593 接线)。
//
// 纪律(4593 裁定书/接线指令):
//   - payer_grant_ref 是独立公共权威引用:本包把 4593 的占位字段接成真值——
//     agent 代发起订阅订单时,授权来自**服务端解析的激活代管关系**(含 renewal
//     动作 + payer_grant_ref 非空),解析快照落单(order.payer_grant_ref/
//     agency_relation_ref/payer_principal);客户端声明一律忽略。
//   - 最终扣费仍由公共 payer 权威裁决;本解析只是订单发起资格(不扣费)。
//   - org_owner = 本人付款主体(资格=billing:manage_payer,4593 冻结矩阵)。

package billing

// Role names mirrored from the authz domain (string compare; no import cycle).
const (
	RoleOrgOwner = "org_owner"
	RoleAgent    = "agent"
)

// PayerReasonRequired is the only refusal reason: an order without a resolved
// payer authorization must not be created (fail-closed).
const PayerReasonRequired = "payment_authorization_required"

// AgencyGrantView is the caller-resolved LIVE agency relation (from the authz
// domain rows; revoked/expired relations never reach this package).
type AgencyGrantView struct {
	RelationRef    string
	AllowedActions []string
	PayerGrantRef  string
	GrantRevision  int64
}

// PayerDecision is the order-initiation verdict plus the snapshot to store.
type PayerDecision struct {
	Authorized     bool
	Reason         string
	PayerGrantRef  string // agent 代付授权引用快照(owner 直购为空)
	RelationRef    string
	GrantRevision  int64
	PayerPrincipal string
}

// DecidePayerForOrder resolves who pays for a new order. agent needs a live
// grant with the renewal action AND a non-empty payer_grant_ref; owner pays
// as the tenant itself; every other role is refused.
func DecidePayerForOrder(role, principal string, grant *AgencyGrantView) PayerDecision {
	switch role {
	case RoleOrgOwner:
		return PayerDecision{Authorized: true, PayerPrincipal: principal}
	case RoleAgent:
		if grant == nil {
			return PayerDecision{Reason: PayerReasonRequired}
		}
		hasRenewal := false
		for _, a := range grant.AllowedActions {
			if a == "renewal" {
				hasRenewal = true
				break
			}
		}
		if !hasRenewal || grant.PayerGrantRef == "" {
			return PayerDecision{Reason: PayerReasonRequired}
		}
		return PayerDecision{
			Authorized:     true,
			PayerGrantRef:  grant.PayerGrantRef,
			RelationRef:    grant.RelationRef,
			GrantRevision:  grant.GrantRevision,
			PayerPrincipal: principal,
		}
	default:
		return PayerDecision{Reason: PayerReasonRequired}
	}
}
