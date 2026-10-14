// handlers_dashboard_ops.go: HUI-1677 经营看板增量(经营数据增量)的 HTTP 读面。
//
// 纪律(与 handlers_dashboard.go 同一纪律,零改动):
//   - 只读:GET /api/v1/dashboard/operations 与 /operations-summary,无写路径;
//   - 开关:FEATURE_DASHBOARD(默认 off)= 路由不注册 + dashGate 兜底双 404
//     (同一开关名,不新建);
//   - 口径:窗口参数必填且 RFC3339,非法一律 400;筛选值白名单校验(未知
//     touchpoint/handoff_method 400,绝不静默匹配空集冒充真实零);
//   - 作用域:服务端单点裁决——org_owner/staff 租户级;store_manager 一律
//     收窄到 members.store_scope,空作用域的畸形经理 fail-closed 403;店经理
//     传越店 store_id 筛选一律 403(不静默收窄)。客户端输入永不参与授权。
package httpapi

import (
	"net/http"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/dashboard"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// opsScope resolves the server-side scope for the operations read surface,
// mirroring handleDashboard: store managers are narrowed to their store
// scope; a deformed manager (empty scope) fails closed.
func (s *Server) opsScope(w http.ResponseWriter, r *http.Request) (tenantID, scope string, ok bool) {
	c := callerFrom(r)
	if !s.allowScopedList(c, w) {
		return "", "", false
	}
	if callerIsStoreManager(c) {
		scope = callerStoreScope(c)
		if scope == "" {
			fail(w, http.StatusForbidden, authz.ReasonOutOfScope, "store manager has no store scope")
			return "", "", false
		}
	}
	return c.Member.TenantID, scope, true
}

// GET /api/v1/dashboard/operations?
//
//	window_start=...&window_end=...
//	&campaign_id=&store_id=&content_pack_version=&touchpoint=&platform=&handoff_method=
func (s *Server) handleDashboardOperations(w http.ResponseWriter, r *http.Request) {
	if !s.dashGate(w) {
		return
	}
	tenantID, scope, ok := s.opsScope(w, r)
	if !ok {
		return
	}
	win, err := dashboard.ParseWindow(r.URL.Query().Get("window_start"), r.URL.Query().Get("window_end"))
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid_window", err.Error())
		return
	}
	q := r.URL.Query()
	filters, err := dashboard.ParseFilterSet(
		q.Get("campaign_id"), q.Get("store_id"), q.Get("content_pack_version"),
		q.Get("touchpoint"), q.Get("platform"), q.Get("handoff_method"))
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid_filter", err.Error())
		return
	}
	// 店经理的越店筛选拒绝(不静默收窄):请求里点名要看别的店,就该大声失败。
	if scope != "" && filters.StoreID != "" && filters.StoreID != scope {
		fail(w, http.StatusForbidden, authz.ReasonOutOfScope, "store filter escapes the manager scope")
		return
	}
	facts, dims, err := s.St.DashboardOps(tenantID, scope, win.Start, win.End, opsFilterFrom(filters))
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "operations aggregation failed")
		return
	}
	body := opsFactsFrom(facts)
	body.Dims = dims
	writeJSON(w, http.StatusOK, dashboard.BuildOperations(win, dashboard.Scope{TenantID: tenantID, StoreScope: scope}, body, filters))
}

// GET /api/v1/dashboard/operations-summary — the fixed read-only port for the
// home page (HUI-2628 consumes; this ticket never touches TodayDesk). Today =
// UTC calendar day [00:00, now], disclosed in the contract.
func (s *Server) handleDashboardOperationsSummary(w http.ResponseWriter, r *http.Request) {
	if !s.dashGate(w) {
		return
	}
	tenantID, scope, ok := s.opsScope(w, r)
	if !ok {
		return
	}
	now := time.Now().UTC()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	facts, err := s.St.DashboardOpsSummary(tenantID, scope, dayStart, now)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "operations summary failed")
		return
	}
	writeJSON(w, http.StatusOK, dashboard.BuildOperationsSummary(now, dashboard.SummaryFacts{
		PendingConfirm:  facts.PendingConfirm,
		RewardsIssued:   facts.RewardsIssued,
		RewardsRedeemed: facts.RewardsRedeemed,
		Anomalies:       facts.Anomalies,
	}))
}

// ---- store→dashboard fact mapping (mechanical, field by field) --------------------

func opsFilterFrom(f dashboard.FilterSet) store.OpsFilter {
	return store.OpsFilter{
		CampaignID:         f.CampaignID,
		StoreID:            f.StoreID,
		ContentPackVersion: f.ContentPackVersion,
		Touchpoint:         f.Touchpoint,
		Platform:           f.Platform,
		HandoffMethod:      f.HandoffMethod,
	}
}

func opsFactsFrom(in store.DashboardOpsFacts) dashboard.OpsFacts {
	out := dashboard.OpsFacts{
		VisitTotal:                     in.VisitTotal,
		StartedTotal:                   in.StartedTotal,
		ContentSelectedTotal:           in.ContentSelectedTotal,
		HandoffInitiatedTotal:          in.HandoffInitiatedTotal,
		EvidenceByClass:                in.EvidenceByClass,
		EvidenceDistinctParticipations: in.EvidenceDistinctParticipations,
		OfficialConfirmedAttempts:      in.OfficialConfirmedAttempts,
		IssuedTotal:                    in.IssuedTotal,
		IssuedFaceMinor:                in.IssuedFaceMinor,
		IssuedCostMinor:                in.IssuedCostMinor,
		RedeemedTotal:                  in.RedeemedTotal,
		RedeemedCostMinor:              in.RedeemedCostMinor,
	}
	if out.EvidenceByClass == nil {
		out.EvidenceByClass = map[string]int64{}
	}
	for _, l := range in.Inventory {
		out.Inventory = append(out.Inventory, dashboard.InventoryFactLine{
			InventoryID: l.InventoryID, CampaignID: l.CampaignID, StoreID: l.StoreID,
			Name: l.Name, Kind: l.Kind, BenefitSlot: l.BenefitSlot,
			RuleVersion: l.RuleVersion, Currency: l.Currency,
			TotalQty: l.TotalQty, Reserved: l.Reserved, Issued: l.Issued,
			Redeemed: l.Redeemed, Reversed: l.Reversed, Expired: l.Expired,
			FaceValueMinor: l.FaceValueMinor, CostMinor: l.CostMinor,
		})
	}
	out.Anomalies = dashboard.AnomalyFacts{
		PendingReview:     in.Anomalies.PendingReview,
		ReservationFailed: in.Anomalies.ReservationFailed,
		ReversedInWindow:  in.Anomalies.ReversedInWindow,
		ExpiredInWindow:   in.Anomalies.ExpiredInWindow,
	}
	out.Cost = dashboard.CostFacts{
		ContentGenerationAmountMinor: in.Cost.ContentGenerationAmountMinor,
		ContentGenerationBilledRuns:  in.Cost.ContentGenerationBilledRuns,
		RewardCostMinor:              in.Cost.RewardCostMinor,
		RewardRedeemedCostMinor:      in.Cost.RewardRedeemedCostMinor,
		RewardFaceMinor:              in.Cost.RewardFaceMinor,
		CostUnrecordedBatches:        in.Cost.CostUnrecordedBatches,
	}
	return out
}
