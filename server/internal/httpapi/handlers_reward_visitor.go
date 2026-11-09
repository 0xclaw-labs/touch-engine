package httpapi

// HUI-1671: 旧券保护 HTTP 面——顾客凭本人访客会话查询自己的有效券、
// 重签短时展示码;店员凭码定位查验(不换取顾客身份)。
//
// 纪律:
//   - 查询路径不经过 resolveAvailableLink/新参与 Gate:活动结束、订阅到期
//     后旧券仍可查(T16);
//   - 两个访客端口走 FEATURE_REWARD_OPS(默认 off = 路由不注册);
//   - 店员 lookup 需要 session + 门店 read 权限;只返回权益定位卡,永不
//     返回顾客身份(没有 visitor 字段可给);
//   - 核销永远走既有 redeem 端点(同一事务/账本),展示码只是定位。

import (
	"net/http"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
)

// visitorIdentity reads the first-party visitor key or fails with an honest
// 401: no cookie means no identity, and identity-less requests cannot read
// (or enumerate) anyone's benefits.
func (s *Server) visitorIdentity(w http.ResponseWriter, r *http.Request) (string, bool) {
	v := visitorKey(r)
	if strings.TrimSpace(v) == "" {
		fail(w, http.StatusUnauthorized, "visitor_required", "需要访客会话才能查看自己的权益。")
		return "", false
	}
	return v, true
}

// handleVisitorRewardBenefits answers the visitor's own valid benefits with
// per-deadline expiry reasons and their own redeem code.
func (s *Server) handleVisitorRewardBenefits(w http.ResponseWriter, r *http.Request) {
	if !s.rewardOpsGate(w) {
		return
	}
	visitor, ok := s.visitorIdentity(w, r)
	if !ok {
		return
	}
	items, err := s.St.ListRewardBenefitsForVisitor(visitor, nowRFC3339())
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "visitor benefits lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "platform_user_created": false})
}

// handleVisitorDisplayCode re-signs a short-lived staff_verify display code
// for one of the visitor's own issued benefits. Old codes are not extended:
// an expired code stays refused; a valid visitor session can mint a new one.
func (s *Server) handleVisitorDisplayCode(w http.ResponseWriter, r *http.Request) {
	if !s.rewardOpsGate(w) {
		return
	}
	visitor, ok := s.visitorIdentity(w, r)
	if !ok {
		return
	}
	benefitID := strings.TrimSpace(r.PathValue("id"))
	code, err := s.St.MintRewardDisplayCode(benefitID, visitor, nowRFC3339())
	if err != nil {
		rewardError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"token": code.Token, "expires_at": code.ExpiresAt,
		"benefit_id": code.BenefitID, "purpose": "staff_verify_display",
	})
}

// handleRewardDisplayCodeLookup locates a benefit from a customer-shown
// display code (3562 店员台消费). The benefit's own store scopes the read;
// the answer is a locate card without any customer identity.
func (s *Server) handleRewardDisplayCodeLookup(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if c == nil || c.Member == nil {
		fail(w, http.StatusUnauthorized, "unauthenticated", "sign in required")
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if !readRewardBody(w, r, &body) {
		return
	}
	ben, err := s.St.ResolveRewardDisplayCode(c.Member.TenantID, body.Code, nowRFC3339())
	if err != nil {
		rewardError(w, err)
		return
	}
	if !s.requireScopedAction(c, authz.ActionReadRecord,
		authz.RecordScope{TenantID: c.Member.TenantID, StoreID: ben.StoreID}, true, w) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"benefit": ben,
		// 明示:这是定位卡,不是顾客身份;核销仍走 redeem。
		"customer_identity_returned": false,
	})
}
