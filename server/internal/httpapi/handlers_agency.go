// handlers_agency.go: HUI-1675 FEAT-0176 代理与子账号的 HTTP 面。
//
// 纪律:
//   - 开关:FEATURE_AGENCY(默认 off)= 路由不注册(见 server.go 条件注册)
//     且本 gate 再答统一 404 —— 与 FEATURE_DASHBOARD / FEATURE_CAMPAIGN_RULES /
//     FEATURE_ASSET_LIB / FEATURE_VIDEO_TEMPLATES 的 off 双保险同一写法;
//   - 授权:建立/解除代管走 authz.ActionManageAgency(平台/owner 类,
//     org_owner 专属);开子账号与留痕查询走 authz.ActionManageAgencySubaccounts
//     (org_owner 恒可;agent 仅当激活代管关系,由 authz.Authorize 按服务端
//     回填的 AgencyActive 裁决);
//   - 单级代理 v1:层级固定 商家租户⇄代理;「代理下再挂代理」不做(deferred);
//   - 子账号 = 商家租户内的既有成员角色:白名单 staff / store_manager
//     (代理永不铸造 org_owner/agent —— 400 bad_role),store_manager 必须带
//     本租户真实门店作用域(复用既有 validateMemberScope);
//   - 可见性(fail-closed):代理的全部数据可见性由既有租户成员行门槛 +
//     激活代管关系双门槛限定;解除代管 = 墓碑 + 停用 agent 成员行,立即收窄;
//     路径/体里的任何租户声明都不参与授权(工作区租户只取 X-Tenant-ID)。
//
// 白名单动作清单(agent 能力集,全量披露;白名单外一律 4xx):
//   - read_list / read_record:名下商家数据只读(走既有业务路由);
//   - manage_agency_subaccounts:开子账号 + 代开留痕查询。
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// agencyGate answers uniform 404 when FEATURE_AGENCY is off: an invisible
// surface (videoTemplatesGate/assetLibGate/rulesGate 同一纪律).
func (s *Server) agencyGate(w http.ResponseWriter) bool {
	if s.Cfg.FeatureAgency {
		return true
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found", "message": "resource not found"})
	return false
}

// ---- bodies -----------------------------------------------------------------

type agencyEstablishBody struct {
	AgentPrincipal string `json:"agent_principal"`
	DisplayName    string `json:"display_name"`
	Note           string `json:"note"`
}

type agencySubAccountBody struct {
	PrincipalRef string `json:"principal_ref"`
	Role         string `json:"role"`
	DisplayName  string `json:"display_name"`
	StoreScope   string `json:"store_scope"`
}

// ---- establish(建立代管,org_owner 专属)---------------------------------------

func (s *Server) handleAgencyRelationCreate(w http.ResponseWriter, r *http.Request) {
	if !s.agencyGate(w) {
		return
	}
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageAgency, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	var in agencyEstablishBody
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid json body")
		return
	}
	ref := strings.TrimSpace(in.AgentPrincipal)
	if !strings.HasPrefix(ref, "usr_") || strings.ContainsAny(ref, "@ :/+ ") {
		fail(w, http.StatusBadRequest, "bad_principal_ref", "agent_principal must be an identity principal id (usr_*); emails/phones never become agents")
		return
	}

	// 幂等:已存在激活关系 → 200 replayed(同一关系行,零新增,留痕不动)
	if cur, err := s.St.GetActiveAgencyRelation(c.Member.TenantID, ref); err == nil {
		writeJSON(w, http.StatusOK, map[string]any{"replayed": true, "id": cur.ID, "relation": cur})
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusInternalServerError, "internal", "agency relation lookup failed")
		return
	}

	// 既有非 agent 成员行绝不静默改角色(409,由 owner 走既有成员面自行处置)
	if _, err := s.St.EnsureAgentMember(c.Member.TenantID, ref, strings.TrimSpace(in.DisplayName), c.Member.PrincipalRef); err != nil {
		if errors.Is(err, store.ErrAgentMemberConflict) {
			fail(w, http.StatusConflict, "agent_member_conflict", "principal already holds a non-agent membership in this tenant")
			return
		}
		fail(w, http.StatusInternalServerError, "internal", "agent member ensure failed")
		return
	}
	rel, err := s.St.CreateAgencyRelation(c.Member.TenantID, ref, c.Member.PrincipalRef, strings.TrimSpace(in.Note))
	if err != nil {
		// 唯一索引兜底:并发/重放下唯一冲突按幂等重放回答(同一激活行)
		if cur, gerr := s.St.GetActiveAgencyRelation(c.Member.TenantID, ref); gerr == nil {
			writeJSON(w, http.StatusOK, map[string]any{"replayed": true, "id": cur.ID, "relation": cur})
			return
		}
		fail(w, http.StatusInternalServerError, "internal", "agency relation create failed")
		return
	}
	writeJSON(w, http.StatusCreated, rel)
}

// ---- relations listing(双视角)------------------------------------------------

func (s *Server) handleAgencyRelationList(w http.ResponseWriter, r *http.Request) {
	if !s.agencyGate(w) {
		return
	}
	c := callerFrom(r)
	// 查询门按既有业务角色裁决:org_owner/staff 通过;agent 需激活关系;
	// 其余(store_manager 等)拒绝。行集再按身份单点收窄(见下),客户端参数
	// 永不参与过滤。
	if !s.requireAction(c, authz.ActionReadList, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	am := s.authzMember(c)
	if am != nil && am.Role == authz.RoleAgent {
		// 代理视角:名下商家组合(按主体过滤,仅激活行;已解除的立即不可见)
		rels, err := s.St.ListActiveAgencyRelationsByAgent(c.Member.PrincipalRef)
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "agency relation list failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": rels})
		return
	}
	// owner 视角:本租户全量(含已解除墓碑,留痕可回查)
	rels, err := s.St.ListAgencyRelationsByTenant(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "agency relation list failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rels})
}

// ---- revoke(解除代管,org_owner 专属)------------------------------------------

func (s *Server) handleAgencyRelationRevoke(w http.ResponseWriter, r *http.Request) {
	if !s.agencyGate(w) {
		return
	}
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageAgency, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	rel, err := s.St.GetAgencyRelation(r.PathValue("id"))
	if err != nil || rel.TenantID != c.Member.TenantID {
		// 他租户关系不可见(掩码语义:不存在与不可见同答)
		fail(w, http.StatusNotFound, "not_found", "agency relation not found in this tenant")
		return
	}
	// HUI-4593: 撤销原因可选入体(M13 撤回操作带原因留审计;legacy 空原因
	// 调用保持兼容,审计行仍然必落)。
	reason := ""
	if r.Body != nil {
		var in struct {
			Reason string `json:"reason"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err == nil {
			reason = strings.TrimSpace(in.Reason)
		}
	}
	rev, err := s.St.RevokeAgencyRelationReasoned(rel.ID, c.Member.TenantID, c.Member.PrincipalRef, reason)
	if err != nil {
		if errors.Is(err, store.ErrAgencyAlreadyRevoked) {
			fail(w, http.StatusConflict, "agency_already_revoked", "this agency relation is already revoked")
			return
		}
		fail(w, http.StatusInternalServerError, "internal", "agency revoke failed")
		return
	}
	// 立即收窄:墓碑之外同时停用 agent 成员行(双重门槛一起落下)
	if err := s.St.DisableAgentMember(rel.TenantID, rel.AgentPrincipal); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "agent member disable failed")
		return
	}
	writeJSON(w, http.StatusOK, rev)
}

// ---- scope(代管范围显式授权/变更,HUI-4593,org_owner 专属)-----------------------
//
// 契约:allowed_actions ⊆ {storefront, action, channel_target, renewal};
// store_scope_set 必须全是本租户真实门店(空 = 全门店);valid_until RFC3339
// (空 = 不过期,到期与撤销同待遇);payer_grant_ref 缺省空(不可代付,资金
// 裁决归公共 payer 权威);grant_revision 乐观并发(STALE_VERSION 拒绝);
// reason 必填(审计:操作者/时间/前后值/原因)。旧关系(空 scope)不会被本
// 接口静默升级 —— 每次扩权都是 owner 的显式确认并留痕。
func (s *Server) handleAgencyRelationScopeSet(w http.ResponseWriter, r *http.Request) {
	if !s.agencyGate(w) {
		return
	}
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageAgency, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	var in struct {
		AllowedActions []string `json:"allowed_actions"`
		StoreScopeSet  []string `json:"store_scope_set"`
		ValidUntil     string   `json:"valid_until"`
		PayerGrantRef  string   `json:"payer_grant_ref"`
		GrantRevision  *int64   `json:"grant_revision"`
		Reason         string   `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid json body")
		return
	}
	if strings.TrimSpace(in.Reason) == "" {
		fail(w, http.StatusBadRequest, "reason_required", "a reason is required for agency scope changes")
		return
	}
	if in.GrantRevision == nil {
		fail(w, http.StatusBadRequest, "bad_request", "grant_revision is required (optimistic concurrency)")
		return
	}
	for _, a := range in.AllowedActions {
		switch a {
		case authz.AgencyActionStorefront, authz.AgencyActionAction, authz.AgencyActionChannelTarget, authz.AgencyActionRenewal:
		default:
			fail(w, http.StatusBadRequest, "bad_scope", "allowed_actions must be storefront/action/channel_target/renewal")
			return
		}
	}
	for _, sto := range in.StoreScopeSet {
		if _, err := s.St.GetStore(sto, c.Member.TenantID); err != nil {
			fail(w, http.StatusBadRequest, "bad_scope", "store_scope_set must reference existing stores of this tenant")
			return
		}
	}
	if in.ValidUntil != "" {
		if _, err := time.Parse(time.RFC3339, in.ValidUntil); err != nil {
			fail(w, http.StatusBadRequest, "bad_scope", "valid_until must be RFC3339 (or empty = never expires)")
			return
		}
	}

	rel, err := s.St.SetAgencyRelationScope(r.PathValue("id"), c.Member.TenantID, store.SetAgencyScopeInput{
		AllowedActions: in.AllowedActions, StoreScopeSet: in.StoreScopeSet,
		ValidUntil: in.ValidUntil, PayerGrantRef: strings.TrimSpace(in.PayerGrantRef),
		ExpectRevision: *in.GrantRevision,
		ActorPrincipal: c.Member.PrincipalRef, Reason: strings.TrimSpace(in.Reason),
	})
	if err != nil {
		switch {
		case errors.Is(err, store.ErrStaleGrantRevision):
			fail(w, http.StatusConflict, "stale_version", "grant_revision is stale; re-read and retry")
		case errors.Is(err, store.ErrAgencyAlreadyRevoked):
			fail(w, http.StatusConflict, "agency_already_revoked", "this agency relation is already revoked")
		case errors.Is(err, store.ErrAgencyGrantInvalid):
			fail(w, http.StatusBadRequest, "bad_scope", "agency scope is invalid")
		default:
			fail(w, http.StatusNotFound, "not_found", "agency relation not found in this tenant")
		}
		return
	}
	writeJSON(w, http.StatusOK, rel)
}

// ---- sub-accounts(开子账号 + 留痕回查)----------------------------------------

func (s *Server) handleAgencySubAccountCreate(w http.ResponseWriter, r *http.Request) {
	if !s.agencyGate(w) {
		return
	}
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageAgencySubaccounts, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	var in agencySubAccountBody
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid json body")
		return
	}
	ref := strings.TrimSpace(in.PrincipalRef)
	if !strings.HasPrefix(ref, "usr_") || strings.ContainsAny(ref, "@ :/+ ") {
		fail(w, http.StatusBadRequest, "bad_principal_ref", "principal_ref must be an identity principal id (usr_*); emails/phones never create members")
		return
	}
	// 子账号角色白名单:代理永不铸造 org_owner/agent(400,先于既有 scope 校验)
	role := strings.TrimSpace(in.Role)
	if role != "staff" && role != "store_manager" {
		fail(w, http.StatusBadRequest, "bad_role", "sub-account role must be staff or store_manager; org_owner/agent are never issued through the agency surface")
		return
	}
	// 门店作用域不变量复用既有校验(store_manager 必须带本租户真实门店)
	scope, ok := s.validateMemberScope(c, role, in.StoreScope, w)
	if !ok {
		return
	}

	// 代开留痕:agent 路径记录 (激活关系行, 代理主体);owner 直开两者留空
	// (留痕行保留,但绝不冒充代理身份)。
	relID, issuerPrincipal := "", ""
	am := s.authzMember(c)
	if am != nil && am.Role == authz.RoleAgent {
		rel, err := s.St.GetActiveAgencyRelation(c.Member.TenantID, c.Member.PrincipalRef)
		if err != nil {
			fail(w, http.StatusForbidden, authz.ReasonAgencyInactive, "no active agency relation for this agent")
			return
		}
		relID, issuerPrincipal = rel.ID, c.Member.PrincipalRef
	}

	m, tr, err := s.St.IssueSubAccount(store.IssueSubAccountInput{
		TenantID: c.Member.TenantID, PrincipalRef: ref, Role: role,
		DisplayName: in.DisplayName, StoreScope: scope,
		CreatedBy: c.Member.PrincipalRef, Enabled: true,
		AgencyRelationID: relID, IssuedByPrincipal: issuerPrincipal,
	})
	if err != nil {
		switch {
		case errors.Is(err, store.ErrMemberExists):
			fail(w, http.StatusConflict, "member_exists", "principal already a member of this tenant")
		case errors.Is(err, store.ErrSubAccountRole):
			fail(w, http.StatusBadRequest, "bad_role", "sub-account role must be staff or store_manager")
		case errors.Is(err, store.ErrSubAccountScope):
			fail(w, http.StatusBadRequest, "bad_scope", "store_scope must match the sub-account role")
		default:
			fail(w, http.StatusInternalServerError, "internal", "sub-account issue failed")
		}
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"member": m, "trail": tr})
}

func (s *Server) handleAgencySubAccountList(w http.ResponseWriter, r *http.Request) {
	if !s.agencyGate(w) {
		return
	}
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageAgencySubaccounts, authz.RecordScope{TenantID: c.Member.TenantID}, w) {
		return
	}
	// 代开留痕可回查:owner 见本租户全量;agent 严格只见自己代开的行
	// (过滤器是服务端解析出的主体,不是客户端自由参数)。
	am := s.authzMember(c)
	issuer := ""
	if am != nil && am.Role == authz.RoleAgent {
		issuer = c.Member.PrincipalRef
	}
	rows, err := s.St.ListSubAccountTrail(c.Member.TenantID, issuer)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "trail list failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

// ---- granted(主体视角跨租户代管关系全集,修复轮 2/F1)-----------------------------
//
// GET /api/v1/agency/relations/granted:返回授予【调用主体】的全部激活代管关系,
// 与当前工作区租户上下文无关 —— 代理同时是 A 租户 owner + B 租户代理时,
// 在 A 上下文里也必须看到 B 的卡片(走查 Walk B / F1)。
// 安全性:行集恒以 agent_principal = 调用者主体 收口,自己被授予的关系对自己
// 可见(与 agent 视图同一纪律),工作区租户头不放大可见性;已撤销关系不可见。
// 附 grantor 租户名与代付状态(卡片直显;租户名本就对其可见 —— 代理是该租户成员行)。
func (s *Server) handleAgencyGrantedList(w http.ResponseWriter, r *http.Request) {
	if !s.agencyGate(w) {
		return
	}
	c := callerFrom(r)
	if c == nil || c.Member == nil {
		fail(w, http.StatusForbidden, authz.ReasonNotMember, "principal is not a member of this tenant")
		return
	}
	rels, err := s.St.ListActiveAgencyRelationsByAgent(c.Member.PrincipalRef)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "agency granted list failed")
		return
	}
	items := make([]map[string]any, 0, len(rels))
	for _, rel := range rels {
		item := map[string]any{
			"id": rel.ID, "tenant_id": rel.TenantID, "agent_principal": rel.AgentPrincipal,
			"note": rel.Note, "established_by": rel.EstablishedBy, "established_at": rel.EstablishedAt,
			"allowed_actions": rel.AllowedActions, "store_scope_set": rel.StoreScopeSet,
			"valid_until": rel.ValidUntil, "grant_revision": rel.GrantRevision,
			"payer_granted": rel.PayerGrantRef != "",
		}
		if t, terr := s.St.GetTenant(rel.TenantID); terr == nil && t.Name != "" {
			item["tenant_name"] = t.Name
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
