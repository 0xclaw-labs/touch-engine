// handlers_ops_console.go: HUI-4592 平台运营控制台(gate + 六视图读面 + 受控
// 支持访问)。操作信封分派在 handlers_ops_operations.go。
//
// 纪律(票面/裁定书):
//   - 权限唯一来源 = 4593 受信 ops_grants(ops_grant_manager 缝签发,绑定
//     principal+tenant);org_owner ≠ 平台管理员;商家会话无 grant 一律 403;
//     FEATURE_AUTHZ off = 路由不注册且 gate 再答统一 404(双保险同款);
//   - 六视图读真实状态:unavailable/unknown 与零记录区分;深层数据(门店/
//     成员/订单明细)需要活跃 support access——限时+原因+审计,到期/撤销
//     每请求实时失效,不泄露租户数据;
//   - 本文件只读 + 支持访问管理;所有运营写经 POST /api/v1/ops/operations
//     统一信封(审计三件套:前后值/操作者/时间/原因)。
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/billing"
	"github.com/bianjiefilm/touch-engine/server/internal/ops"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// registerOpsConsoleRoutes wires the platform ops console. FEATURE_AUTHZ 段
// 内注册(server.go 窄补丁一行)——ops 命令只存在于 4593 authz 模型。
func (s *Server) registerOpsConsoleRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/ops/tenants", s.requireOpsSession(ops.CmdTenantsRead, s.handleOpsTenantsList))
	mux.Handle("GET /api/v1/ops/tenants/{id}", s.requireOpsSession(ops.CmdTenantsRead, s.handleOpsTenantDetail))
	mux.Handle("GET /api/v1/ops/plans", s.requireOpsSession(ops.CmdPlansRead, s.handleOpsPlansList))
	mux.Handle("GET /api/v1/ops/orders", s.requireOpsSession(ops.CmdOrdersRead, s.handleOpsOrdersList))
	mux.Handle("GET /api/v1/ops/channels", s.requireOpsSession(ops.CmdChannelsRead, s.handleOpsChannelsList))
	mux.Handle("GET /api/v1/ops/templates", s.requireOpsSession(ops.CmdTemplatesRead, s.handleOpsTemplatesList))
	mux.Handle("GET /api/v1/ops/operations", s.requireOpsSession(ops.CmdOpsRead, s.handleOpsOperationsView))
	mux.Handle("POST /api/v1/ops/operations", s.requireOpsSession("", s.handleOpsOperationCreate))
	mux.Handle("GET /api/v1/ops/operations/{id}", s.requireOpsSession(ops.CmdOpsRead, s.handleOpsOperationGet))
	mux.Handle("GET /api/v1/ops/audit", s.requireOpsSession(ops.CmdOpsRead, s.handleOpsAuditList))
	mux.Handle("POST /api/v1/ops/support-access", s.requireOpsSession(ops.CmdSupportManage, s.handleOpsSupportAccessCreate))
	mux.Handle("GET /api/v1/ops/support-access", s.requireOpsSession(ops.CmdOpsRead, s.handleOpsSupportAccessList))
	mux.Handle("POST /api/v1/ops/support-access/{id}/close", s.requireOpsSession(ops.CmdSupportManage, s.handleOpsSupportAccessClose))
}

// opsGate answers uniform 404 when FEATURE_AUTHZ is off (authzGate 同纪律).
func (s *Server) opsGate(w http.ResponseWriter) bool {
	return s.authzGate(w)
}

// requireOpsSession gates one ops route: authz gate + session + the ops
// command (empty command = envelope dispatcher, which re-checks per action).
// 权限只来自受信 ops grant(resolveAccess → authz.Can);请求头/体永不授权。
func (s *Server) requireOpsSession(command string, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.opsGate(w) {
			return
		}
		s.requireSession(func(w http.ResponseWriter, r *http.Request) {
			if command == "" {
				next(w, r)
				return
			}
			c := callerFrom(r)
			a := s.resolveAccess(c)
			if !authz.Can(a, command, "").Allowed {
				fail(w, http.StatusForbidden, authz.ReasonForbidden, "this platform operation requires the trusted ops grant "+command)
				return
			}
			next(w, r)
		}).ServeHTTP(w, r)
	})
}

// opsAuthoritySource builds the audited authority reference for one call:
// the live ops grant refs (+ the active support access ref when present).
// 审计不可由调用方伪造 actor 或来源——两列全部服务端解析。
func (s *Server) opsAuthoritySource(c *caller, tenantID string) string {
	a := s.resolveAccess(c)
	parts := make([]string, 0, 2)
	if len(a.OpsGrantRefs) > 0 {
		parts = append(parts, "ops_grant:"+strings.Join(a.OpsGrantRefs, ","))
	}
	if tenantID != "" {
		if sup, err := s.St.ActiveSupportAccess(c.Member.PrincipalRef, tenantID, time.Now()); err == nil {
			parts = append(parts, "support_access:"+sup.ID)
		}
	}
	return strings.Join(parts, " ")
}

// ---- O1 商户与开通 ---------------------------------------------------------------

func (s *Server) handleOpsTenantsList(w http.ResponseWriter, r *http.Request) {
	if !s.opsGate(w) {
		return
	}
	rows, err := s.St.ListTenantsOps(strings.TrimSpace(r.URL.Query().Get("q")), strings.TrimSpace(r.URL.Query().Get("lifecycle")))
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "tenant listing failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tenants": rows, "count": len(rows)})
}

func (s *Server) handleOpsTenantDetail(w http.ResponseWriter, r *http.Request) {
	if !s.opsGate(w) {
		return
	}
	id := r.PathValue("id")
	ten, err := s.St.GetTenant(id)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "tenant not found")
		return
	} else if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "tenant lookup failed")
		return
	}
	rev, _ := s.St.TenantRevision(id)
	sub, subErr := s.St.GetBillingSubscription(id)
	stores, storesErr := s.St.ListStores(id)
	members, membersErr := s.St.ListMembers(id)
	supportRef := ""
	// 深层数据(门店清单/成员清单)只在活跃 support access 内可见;
	// 平台层事实(lifecycle/订阅投影/计数)任何 ops:tenants.read 持有者可见。
	hasSupport := false
	c := callerFrom(r)
	if sup, err := s.St.ActiveSupportAccess(c.Member.PrincipalRef, id, time.Now()); err == nil {
		hasSupport = true
		supportRef = sup.ID
	}
	deep := map[string]any{"available": hasSupport}
	if !hasSupport {
		deep["state"] = "locked"
		deep["message"] = "merchant business data requires an active support access (time-boxed, audited)"
		// 计数是聚合事实,不是明细——保持可见。
		deep["store_count"] = len(orNilStores(stores, storesErr))
		deep["member_count"] = len(orNilMembers(members, membersErr))
	} else {
		deep["state"] = "open"
		deep["support_access_ref"] = supportRef
		deep["stores"] = orNilStores(stores, storesErr)
		deep["members"] = orNilMembers(members, membersErr)
	}
	subView := map[string]any{"available": subErr == nil}
	if subErr == nil {
		subView["status"] = sub.Status
		subView["paid_until"] = sub.PaidUntil
		subView["retain_until"] = sub.RetainUntil
		subView["effective_status"] = sub.EffectiveStatus(time.Now().UTC().Format(time.RFC3339))
	} else {
		subView["state"] = "no_projection_row"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tenant":        ten,
		"ops_revision":  rev,
		"subscription":  subView,
		"business_data": deep,
	})
}

func orNilStores(v []store.StoreRecord, err error) []store.StoreRecord {
	if err != nil {
		return []store.StoreRecord{}
	}
	return v
}

func orNilMembers(v []store.Member, err error) []store.Member {
	if err != nil {
		return []store.Member{}
	}
	return v
}

// ---- 受控支持访问(限时+原因+审计) -------------------------------------------------

type opsSupportAccessRequest struct {
	TenantID   string `json:"tenant_id"`
	Reason     string `json:"reason"`
	TTLMinutes int    `json:"ttl_minutes"`
}

func (s *Server) handleOpsSupportAccessCreate(w http.ResponseWriter, r *http.Request) {
	if !s.opsGate(w) {
		return
	}
	var in opsSupportAccessRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_json", "request body must be JSON")
		return
	}
	c := callerFrom(r)
	if strings.TrimSpace(in.Reason) == "" {
		fail(w, http.StatusBadRequest, "reason_required", "a support access must state its business reason")
		return
	}
	if strings.TrimSpace(in.TenantID) == "" {
		fail(w, http.StatusBadRequest, "tenant_required", "support access must name its target tenant")
		return
	}
	if _, err := s.St.GetTenant(in.TenantID); err != nil {
		fail(w, http.StatusNotFound, "not_found", "target tenant not found")
		return
	}
	if in.TTLMinutes <= 0 || in.TTLMinutes > ops.MaxSupportAccessMinutes {
		fail(w, http.StatusBadRequest, "ttl_invalid",
			"ttl_minutes must be between 1 and "+strconv.Itoa(ops.MaxSupportAccessMinutes))
		return
	}
	// 打开前先撤既有活跃授权:一个主体对一个目标同时至多一个活跃窗口。
	if prev, err := s.St.ActiveSupportAccess(c.Member.PrincipalRef, in.TenantID, time.Now()); err == nil {
		if _, err := s.St.CloseSupportAccess(prev.ID, c.Member.PrincipalRef, "superseded by a new support access", time.Now()); err != nil {
			fail(w, http.StatusInternalServerError, "internal", "previous access close failed")
			return
		}
	}
	a, err := s.St.CreateSupportAccess(in.TenantID, c.Member.PrincipalRef, strings.TrimSpace(in.Reason),
		time.Duration(in.TTLMinutes)*time.Minute, time.Now())
	if err != nil {
		fail(w, http.StatusBadRequest, "support_access_rejected", err.Error())
		return
	}
	authority := s.opsAuthoritySource(c, in.TenantID)
	_, _ = s.St.AppendOpsAudit(store.OpsAuditEntry{
		ActorRef: c.Member.PrincipalRef, ActorRole: c.Member.Role, AuthoritySource: authority,
		Command: ops.CmdSupportManage, Action: "support.open", TargetType: "support_access", TargetID: a.ID,
		TenantID: in.TenantID, BeforeJSON: "null",
		AfterJSON: jsonSnap(map[string]any{"expires_at": a.ExpiresAt, "reason": a.Reason}),
		Reason:    in.Reason,
	})
	writeJSON(w, http.StatusCreated, map[string]any{"support_access": a})
}

func (s *Server) handleOpsSupportAccessList(w http.ResponseWriter, r *http.Request) {
	if !s.opsGate(w) {
		return
	}
	rows, err := s.St.ListSupportAccess(100)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "support access listing failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"support_access": rows})
}

func (s *Server) handleOpsSupportAccessClose(w http.ResponseWriter, r *http.Request) {
	if !s.opsGate(w) {
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_json", "request body must be JSON")
		return
	}
	if strings.TrimSpace(in.Reason) == "" {
		fail(w, http.StatusBadRequest, "reason_required", "closing a support access must state why it ended")
		return
	}
	id := r.PathValue("id")
	c := callerFrom(r)
	a, err := s.St.CloseSupportAccess(id, c.Member.PrincipalRef, strings.TrimSpace(in.Reason), time.Now())
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "support access not found")
		return
	} else if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "close failed")
		return
	}
	_, _ = s.St.AppendOpsAudit(store.OpsAuditEntry{
		ActorRef: c.Member.PrincipalRef, ActorRole: c.Member.Role,
		AuthoritySource: s.opsAuthoritySource(c, a.TenantID),
		Command:         ops.CmdSupportManage, Action: "support.close",
		TargetType: "support_access", TargetID: a.ID, TenantID: a.TenantID,
		BeforeJSON: jsonSnap(map[string]any{"closed_at": "", "close_reason": ""}),
		AfterJSON:  jsonSnap(map[string]any{"closed_at": a.ClosedAt, "close_reason": a.CloseReason}),
		Reason:     in.Reason,
	})
	writeJSON(w, http.StatusOK, map[string]any{"support_access": a})
}

// ---- O2 套餐与价格 ---------------------------------------------------------------

func (s *Server) handleOpsPlansList(w http.ResponseWriter, r *http.Request) {
	if !s.opsGate(w) {
		return
	}
	entries, err := s.St.ListCatalogAll()
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "catalog lookup failed")
		return
	}
	nowT := time.Now()
	type planView struct {
		billing.CatalogEntry
		Effective   bool   `json:"effective"`
		Retired     bool   `json:"retired"`
		WindowState string `json:"window_state"`
		OrderPinned bool   `json:"order_price_snapshot_immutable"`
	}
	views := make([]planView, 0, len(entries))
	for _, e := range entries {
		v := planView{CatalogEntry: e, OrderPinned: true}
		switch {
		case opsCatalogEffective(e, nowT):
			v.Effective = true
			v.WindowState = "effective"
		case e.EffectiveTo != "" && windowPassed(e.EffectiveTo, nowT):
			v.Retired = true
			v.WindowState = "retired"
		default:
			v.WindowState = "not_yet_effective"
		}
		views = append(views, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"plans": views,
		"note":  "orders pin their own price snapshot; publishing or retiring here never rewrites historical orders",
	})
}

func opsCatalogEffective(e billing.CatalogEntry, nowT time.Time) bool {
	if e.EffectiveFrom != "" {
		if from, err := time.Parse(time.RFC3339, e.EffectiveFrom); err != nil || nowT.Before(from) {
			return false
		}
	}
	if e.EffectiveTo != "" {
		if to, err := time.Parse(time.RFC3339, e.EffectiveTo); err != nil || nowT.After(to) {
			return false
		}
	}
	return true
}

func windowPassed(to string, nowT time.Time) bool {
	t, err := time.Parse(time.RFC3339, to)
	return err == nil && nowT.After(t)
}

// ---- O3 订单与对账 ---------------------------------------------------------------

func (s *Server) handleOpsOrdersList(w http.ResponseWriter, r *http.Request) {
	if !s.opsGate(w) {
		return
	}
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	orders, err := s.St.ListOrdersAll(kind, 200)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "order listing failed")
		return
	}
	refunds, err := s.St.ListRefundsAll(200)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "refund listing failed")
		return
	}
	ai, err := s.St.ListAIUsageAll(200)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "ai usage listing failed")
		return
	}
	// 异常队列:failed 订单与退款金额不匹配订单余额的疑点(只标记,不改账)。
	queue := []map[string]any{}
	for _, o := range orders {
		if o.Status == "failed" {
			queue = append(queue, map[string]any{
				"order_id": o.ID, "tenant_id": o.TenantID, "kind": o.Kind,
				"status": o.Status, "fail_reason": o.FailReason, "discrepancy": "order_failed",
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"orders": orders, "refunds": refunds, "ai_usage": ai, "discrepancy_queue": queue,
		"note": "billing facts are projections of the public order authority; this view never edits amounts or entitlements",
	})
}

// ---- O4 渠道与健康 ---------------------------------------------------------------

func (s *Server) handleOpsChannelsList(w http.ResponseWriter, r *http.Request) {
	if !s.admissionGate(w) {
		return
	}
	rows, err := s.St.ListActionCapabilitiesAll()
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "capability lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"records": actionAdminPayloads(rows), "scope": "platform",
		"note": "emergency disable/restore only suspends the matching method row; unrelated navigation is never touched",
	})
}

// ---- O5 模板与内容 ---------------------------------------------------------------

func (s *Server) handleOpsTemplatesList(w http.ResponseWriter, r *http.Request) {
	if !s.opsGate(w) {
		return
	}
	templates, err := s.St.ListTemplatesAll(100)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "template listing failed")
		return
	}
	versions, err := s.St.ListContentVersionsAll(100)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "content version listing failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"templates": templates, "content_versions": versions,
		"note": "this view manages directory references, distribution and retraction commands; assets stay in their owning domains",
	})
}

// ---- O6 运行与交付(聚合,不是纯指标大屏:每项都带定位/去向) -----------------------

func (s *Server) handleOpsOperationsView(w http.ResponseWriter, r *http.Request) {
	if !s.opsGate(w) {
		return
	}
	overall, targets := s.domainHealthSnapshot(r.Context())
	hardware, hwErr := s.St.ListHardwareAll(50)
	hwState := "ok"
	if hwErr != nil {
		hwState = "unavailable"
		hardware = []store.OpsHardwareRow{}
	}
	inventory, invErr := s.St.TouchpointInventory()
	if invErr != nil {
		inventory = map[string]int{}
	}
	tickets, _ := s.St.ListOpsTickets("", 50)
	drills, _ := s.St.ListOpsDrills(20)
	operations, _ := s.St.ListOpsOperations(50)
	writeJSON(w, http.StatusOK, map[string]any{
		"domain_health": map[string]any{"status": overall, "targets": targets},
		"hardware_fulfillment": map[string]any{
			"state":  hwState,
			"source": "billing_projection_4591",
			"note":   "read-only projection of the 4591 five-step hardware fulfillment; 1663 binding/acceptance facts land here when that wave merges",
			"orders": hardware,
		},
		"touchpoint_inventory": inventory,
		"tickets":              tickets,
		"drills":               drills,
		"recent_operations":    operations,
	})
}

// ---- 审计回查 ---------------------------------------------------------------------

func (s *Server) handleOpsAuditList(w http.ResponseWriter, r *http.Request) {
	if !s.opsGate(w) {
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	rows, err := s.St.ListOpsAudit(store.OpsAuditFilter{
		TenantID:  strings.TrimSpace(q.Get("tenant")),
		TargetID:  strings.TrimSpace(q.Get("target")),
		Operation: strings.TrimSpace(q.Get("operation")),
		Limit:     limit,
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "audit listing failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"audit": rows})
}

// jsonSnap marshals v or the literal string "null" on failure (audit 快照
// 永不因序列化失败而落空串)。
func jsonSnap(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(raw)
}
