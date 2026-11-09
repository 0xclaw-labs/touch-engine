// store_agency.go: HUI-1675 FEAT-0176 代理与子账号的存储层。
//
// 纪律:
//   - 代管关系只追加历史:解除 = 墓碑(revoked_at/revoked_by),绝不物理删除;
//     同 (tenant, agent) 至多一条激活(0008 部分唯一索引兜底);
//   - 代理权威 = 激活关系:HasActiveAgencyRelation 是 authz fail-closed 的
//     数据源(任何错误一律按「无激活关系」处理,绝不放宽);
//   - 解除代管同时停用 agent 成员行 —— 可见性立即收窄的存储半边(另一半在
//     authz 的 AgencyActive 裁决);停用只触 agent 行,别角色零波及;
//   - 代开留痕 append-only:子账号成员行与留痕行同事务落库,失败零残留;
//   - 子账号角色白名单 = staff / store_manager(代理永不铸造 org_owner/agent),
//     store 侧为最后一道闸,处理层先行校验。
//
// HUI-4593 additive 增量(0064 列):allowed_actions/store_scope_set/valid_until/
// grant_revision/payer_grant_ref。旧行为逐字节不变(空 scope = 旧只读+开子账号);
// scope 只经 SetAgencyRelationScope 显式变更(乐观并发 + 审计同事务);到期 =
// 关系整体失效(HasValidAgencyRelation 单点,与撤销同待遇)。
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Sentinel errors (handler 层映射为独立 HTTP 原因码).
var (
	// ErrAgencyAlreadyRevoked: 解除已解除的关系(显式拒绝,绝不静默二次处理)。
	ErrAgencyAlreadyRevoked = errors.New("store: agency relation already revoked")
	// ErrAgentMemberConflict: 目标主体已有非 agent 成员行,拒绝改角色。
	ErrAgentMemberConflict = errors.New("store: principal already a non-agent member of this tenant")
	// ErrMemberExists: 目标主体已在本租户(开子账号唯一性冲突)。
	ErrMemberExists = errors.New("store: member already exists in this tenant")
	// ErrSubAccountRole: 子账号角色不在白名单(staff/store_manager)。
	ErrSubAccountRole = errors.New("store: sub-account role must be staff or store_manager")
	// ErrSubAccountScope: 子账号角色与门店作用域配对非法。
	ErrSubAccountScope = errors.New("store: sub-account store scope does not match its role")
	// ErrStaleGrantRevision (HUI-4593): scope 变更携带的期望版本 ≠ 当前版本。
	ErrStaleGrantRevision = errors.New("store: stale agency grant revision")
	// ErrAgencyGrantInvalid (HUI-4593): allowed_actions/门店集/期限非法。
	ErrAgencyGrantInvalid = errors.New("store: agency grant scope invalid")
)

// agencyAllowedActions is the frozen whitelist for allowed_actions values
// (authz.AgencyAction* 常量的存储层镜像;白名单外写入即拒)。
var agencyAllowedActions = map[string]bool{
	"storefront":     true,
	"action":         true,
	"channel_target": true,
	"renewal":        true,
}

// ---- agency relations(代管关系行)---------------------------------------------

type AgencyRelation struct {
	ID             string `json:"id"`
	TenantID       string `json:"tenant_id"`
	AgentPrincipal string `json:"agent_principal"`
	Note           string `json:"note,omitempty"`
	EstablishedBy  string `json:"established_by"`
	EstablishedAt  string `json:"established_at"`
	RevokedBy      string `json:"revoked_by,omitempty"`
	RevokedAt      string `json:"revoked_at,omitempty"`
	// HUI-4593 additive scope (0064 列;空 = 旧语义:只读+开子账号)。
	AllowedActions []string `json:"allowed_actions,omitempty"`
	StoreScopeSet  []string `json:"store_scope_set,omitempty"` // 空 = 全门店
	ValidUntil     string   `json:"valid_until,omitempty"`     // 空 = 不过期(RFC3339)
	GrantRevision  int64    `json:"grant_revision"`
	PayerGrantRef  string   `json:"payer_grant_ref,omitempty"` // 空 = 不可代付
}

const agencyRelCols = `id,tenant_id,agent_principal,note,established_by,established_at,revoked_by,revoked_at,
	allowed_actions,store_scope_set,valid_until,grant_revision,payer_grant_ref`

func scanAgencyRel(sc interface{ Scan(...any) error }) (AgencyRelation, error) {
	var r AgencyRelation
	var revokedAt sql.NullString
	var allowedActions, scopeSet string
	if err := sc.Scan(&r.ID, &r.TenantID, &r.AgentPrincipal, &r.Note, &r.EstablishedBy, &r.EstablishedAt, &r.RevokedBy, &revokedAt,
		&allowedActions, &scopeSet, &r.ValidUntil, &r.GrantRevision, &r.PayerGrantRef); err != nil {
		return AgencyRelation{}, err
	}
	r.RevokedAt = revokedAt.String
	r.AllowedActions = parseStringList(allowedActions)
	r.StoreScopeSet = parseStringList(scopeSet)
	return r, nil
}

// stringListJSON marshals a string list into the JSON text column form
// (nil/empty -> "" = 旧语义/全门店)。
func stringListJSON(list []string) string {
	if len(list) == 0 {
		return ""
	}
	b, err := json.Marshal(list)
	if err != nil {
		return ""
	}
	return string(b)
}

// parseStringList reads the JSON text column; empty/invalid -> nil
// (fail-closed:坏数据按空 scope 处理,绝不放宽)。
func parseStringList(v string) []string {
	if v == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(v), &out); err != nil {
		return nil
	}
	return out
}

// SetAgencyScopeInput carries one explicit scope change. ExpectRevision is the
// optimistic-concurrency guard; Reason is required by the handler (audit).
type SetAgencyScopeInput struct {
	AllowedActions []string
	StoreScopeSet  []string
	ValidUntil     string
	PayerGrantRef  string
	ExpectRevision int64
	ActorPrincipal string
	Reason         string
}

// validateAgencyScope is the store-side last gate for scope values
// (handler 先行校验给出更友好的错误码)。store_scope_set 的每家店必须真实
// 属于本租户(门店目标永不跨店)。
func (s *Store) validateAgencyScope(tenantID string, in SetAgencyScopeInput) error {
	for _, a := range in.AllowedActions {
		if !agencyAllowedActions[a] {
			return ErrAgencyGrantInvalid
		}
	}
	for _, sto := range in.StoreScopeSet {
		if _, err := s.GetStore(sto, tenantID); err != nil {
			return ErrAgencyGrantInvalid
		}
	}
	if in.ValidUntil != "" {
		if _, err := time.Parse(time.RFC3339, in.ValidUntil); err != nil {
			return ErrAgencyGrantInvalid
		}
	}
	return nil
}

// SetAgencyRelationScope performs ONE explicit scope change on an ACTIVE
// relation: optimistic revision check, whitelist/tenancy validation, revision
// bump and the audit row land in ONE transaction (失败零残留)。
func (s *Store) SetAgencyRelationScope(id, tenantID string, in SetAgencyScopeInput) (AgencyRelation, error) {
	cur, err := s.GetAgencyRelation(id)
	if err != nil {
		return AgencyRelation{}, err // ErrNotFound = 不存在或非本租户(掩码语义)
	}
	if cur.TenantID != tenantID {
		return AgencyRelation{}, ErrNotFound
	}
	if cur.RevokedAt != "" {
		return AgencyRelation{}, ErrAgencyAlreadyRevoked
	}
	if cur.GrantRevision != in.ExpectRevision {
		return AgencyRelation{}, ErrStaleGrantRevision
	}
	if err := s.validateAgencyScope(tenantID, in); err != nil {
		return AgencyRelation{}, err
	}
	validUntil := in.ValidUntil
	if validUntil != "" {
		if tt, perr := time.Parse(time.RFC3339, validUntil); perr == nil {
			validUntil = tt.UTC().Format(time.RFC3339Nano)
		}
	}
	next := cur.GrantRevision + 1

	tx, err := s.DB.Begin()
	if err != nil {
		return AgencyRelation{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		`UPDATE agency_relations SET allowed_actions=?,store_scope_set=?,valid_until=?,payer_grant_ref=?,grant_revision=?
		 WHERE id=? AND tenant_id=? AND revoked_at IS NULL AND grant_revision=?`,
		stringListJSON(in.AllowedActions), stringListJSON(in.StoreScopeSet), validUntil, in.PayerGrantRef, next,
		cur.ID, tenantID, cur.GrantRevision); err != nil {
		return AgencyRelation{}, err
	}
	if _, err := appendAudit(tx, AuthzAuditEntry{
		TenantID: tenantID, ActorPrincipal: in.ActorPrincipal, ActorRole: "org_owner",
		Action: "agency_scope_set", TargetType: "agency_relation", TargetID: cur.ID,
		BeforeJSON: agencySnapshot(cur), AfterJSON: agencySnapshot(AgencyRelation{
			AllowedActions: in.AllowedActions, StoreScopeSet: in.StoreScopeSet,
			ValidUntil: validUntil, GrantRevision: next, PayerGrantRef: in.PayerGrantRef,
		}),
		Reason: in.Reason,
	}); err != nil {
		return AgencyRelation{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgencyRelation{}, err
	}
	s.bumpEpoch()
	cur.AllowedActions, cur.StoreScopeSet, cur.ValidUntil = in.AllowedActions, in.StoreScopeSet, validUntil
	cur.PayerGrantRef, cur.GrantRevision = in.PayerGrantRef, next
	return cur, nil
}

// agencySnapshot is the audit before/after JSON for a relation scope state.
func agencySnapshot(r AgencyRelation) string {
	b, _ := json.Marshal(map[string]any{
		"allowed_actions": r.AllowedActions,
		"store_scope_set": r.StoreScopeSet,
		"valid_until":     r.ValidUntil,
		"grant_revision":  r.GrantRevision,
		"payer_grant_ref": r.PayerGrantRef,
	})
	return string(b)
}

// CreateAgencyRelation mints a NEW relation row. A still-active relation for
// the same (tenant, principal) is refused here (the HTTP layer idempotizes
// before calling); a revoked history never blocks re-establishment.
func (s *Store) CreateAgencyRelation(tenantID, agentPrincipal, establishedBy, note string) (AgencyRelation, error) {
	r := AgencyRelation{
		ID: newID("agr_"), TenantID: tenantID, AgentPrincipal: agentPrincipal,
		Note: note, EstablishedBy: establishedBy, EstablishedAt: now(),
	}
	_, err := s.DB.Exec(
		`INSERT INTO agency_relations(`+agencyRelCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.TenantID, r.AgentPrincipal, r.Note, r.EstablishedBy, r.EstablishedAt, "", nil,
		"", "", "", 0, "")
	if err != nil {
		return AgencyRelation{}, err
	}
	s.bumpEpoch()
	return r, nil
}

func (s *Store) GetAgencyRelation(id string) (AgencyRelation, error) {
	row := s.DB.QueryRow(`SELECT `+agencyRelCols+` FROM agency_relations WHERE id=?`, id)
	r, err := scanAgencyRel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return AgencyRelation{}, ErrNotFound
	}
	return r, err
}

// GetActiveAgencyRelation resolves the ONE active relation for the pair.
func (s *Store) GetActiveAgencyRelation(tenantID, agentPrincipal string) (AgencyRelation, error) {
	row := s.DB.QueryRow(
		`SELECT `+agencyRelCols+` FROM agency_relations WHERE tenant_id=? AND agent_principal=? AND revoked_at IS NULL`,
		tenantID, agentPrincipal)
	r, err := scanAgencyRel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return AgencyRelation{}, ErrNotFound
	}
	return r, err
}

// HasActiveAgencyRelation is the fail-closed data source for the authz
// AgencyActive backfill: any error answers false.
func (s *Store) HasActiveAgencyRelation(tenantID, agentPrincipal string) bool {
	var n int
	if err := s.DB.QueryRow(
		`SELECT COUNT(1) FROM agency_relations WHERE tenant_id=? AND agent_principal=? AND revoked_at IS NULL`,
		tenantID, agentPrincipal).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

// HasValidAgencyRelation (HUI-4593) is the fail-closed data source for the
// authz AgencyActive backfill after the additive scope columns: 撤销与到期
// 同待遇 —— revoked_at 非空或 valid_until 已过 = false(旧行 valid_until 空
// = 不过期,行为与 HasActiveAgencyRelation 完全一致)。任何错误一律 false。
func (s *Store) HasValidAgencyRelation(tenantID, agentPrincipal string, nowT time.Time) bool {
	var validUntil string
	err := s.DB.QueryRow(
		`SELECT valid_until FROM agency_relations
		 WHERE tenant_id=? AND agent_principal=? AND revoked_at IS NULL`,
		tenantID, agentPrincipal).Scan(&validUntil)
	if err != nil {
		return false
	}
	if validUntil == "" {
		return true
	}
	exp, perr := time.Parse(time.RFC3339, validUntil)
	if perr != nil {
		return false // 坏数据 fail-closed
	}
	return nowT.Before(exp)
}

func (s *Store) ListAgencyRelationsByTenant(tenantID string) ([]AgencyRelation, error) {
	return s.listAgencyRels(`SELECT `+agencyRelCols+` FROM agency_relations WHERE tenant_id=? ORDER BY established_at`, tenantID)
}

// ListActiveAgencyRelationsByAgent is the agent's portfolio (its managed
// merchants; active rows only — revoked relations are invisible to the agent).
func (s *Store) ListActiveAgencyRelationsByAgent(agentPrincipal string) ([]AgencyRelation, error) {
	return s.listAgencyRels(
		`SELECT `+agencyRelCols+` FROM agency_relations WHERE agent_principal=? AND revoked_at IS NULL ORDER BY established_at`,
		agentPrincipal)
}

func (s *Store) listAgencyRels(q string, arg string) ([]AgencyRelation, error) {
	rows, err := s.DB.Query(q, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AgencyRelation, 0)
	for rows.Next() {
		r, err := scanAgencyRel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RevokeAgencyRelation tombstones the relation (append-only history). The
// tenant is mandatory: a foreign relation is indistinguishable from a missing
// one (ErrNotFound, 掩码语义). Double revoke is an explicit conflict.
// HUI-4593: 撤销动作同事务落审计行(操作者/时间/前值/原因;legacy 调用方
// reason 为空 —— 审计行仍然必落)。
func (s *Store) RevokeAgencyRelation(id, tenantID, revokedBy string) (AgencyRelation, error) {
	return s.RevokeAgencyRelationReasoned(id, tenantID, revokedBy, "")
}

// RevokeAgencyRelationReasoned is RevokeAgencyRelation with a mandatory-trail
// reason (M13 页面走这个;reason 由 handler 强制非空)。
func (s *Store) RevokeAgencyRelationReasoned(id, tenantID, revokedBy, reason string) (AgencyRelation, error) {
	cur, err := s.GetAgencyRelation(id)
	if err != nil {
		return AgencyRelation{}, err
	}
	if cur.TenantID != tenantID {
		return AgencyRelation{}, ErrNotFound
	}
	if cur.RevokedAt != "" {
		return AgencyRelation{}, ErrAgencyAlreadyRevoked
	}
	cur.RevokedBy, cur.RevokedAt = revokedBy, now()

	tx, err := s.DB.Begin()
	if err != nil {
		return AgencyRelation{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE agency_relations SET revoked_by=?,revoked_at=? WHERE id=? AND tenant_id=? AND revoked_at IS NULL`,
		cur.RevokedBy, cur.RevokedAt, cur.ID, cur.TenantID); err != nil {
		return AgencyRelation{}, err
	}
	if _, err := appendAudit(tx, AuthzAuditEntry{
		TenantID: tenantID, ActorPrincipal: revokedBy, ActorRole: "org_owner",
		Action: "agency_revoke", TargetType: "agency_relation", TargetID: cur.ID,
		BeforeJSON: agencySnapshot(cur),
		AfterJSON:  `{"revoked":true}`,
		Reason:     reason,
	}); err != nil {
		return AgencyRelation{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgencyRelation{}, err
	}
	s.bumpEpoch()
	return cur, nil
}

// ---- agent member rows(既有 members 体系内的代理角色行)-----------------------

// EnsureAgentMember creates (or re-enables) the agent member row for the pair.
// It is the ONLY sanctioned creation path for role=agent rows; an existing
// non-agent membership is never re-roled (ErrAgentMemberConflict).
func (s *Store) EnsureAgentMember(tenantID, agentPrincipal, displayName, createdBy string) (Member, error) {
	cur, err := s.GetMemberByPrincipal(tenantID, agentPrincipal)
	if err == nil {
		if cur.Role != "agent" {
			return Member{}, ErrAgentMemberConflict
		}
		if !cur.Enabled {
			return s.UpdateMember(cur.ID, nil, boolPtr(true), nil)
		}
		return cur, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Member{}, err
	}
	return s.CreateMemberScoped(tenantID, agentPrincipal, "agent", displayName, createdBy, true, "")
}

// DisableAgentMember flips enabled=0 on the AGENT row of the pair only
// (non-agent rows are untouched; absent rows are a no-op). Used by revoke so
// visibility narrows immediately at the membership gate too.
func (s *Store) DisableAgentMember(tenantID, agentPrincipal string) error {
	_, err := s.DB.Exec(`UPDATE members SET enabled=0,updated_at=? WHERE tenant_id=? AND principal_ref=? AND role='agent'`,
		now(), tenantID, agentPrincipal)
	return err
}

// ---- sub-accounts(代开的成员)+ trail(代开留痕)--------------------------------

type SubAccountTrail struct {
	ID                string `json:"id"`
	TenantID          string `json:"tenant_id"`
	MemberID          string `json:"member_id"`
	AgencyRelationID  string `json:"agency_relation_id,omitempty"`
	IssuedByPrincipal string `json:"issued_by_principal"`
	IssuedByMember    string `json:"issued_by_member"`
	CreatedAt         string `json:"created_at"`
	// joined member snapshot(留痕回查直接可读,无需二次查询)
	MemberPrincipalRef string `json:"member_principal_ref"`
	MemberRole         string `json:"member_role"`
	MemberDisplayName  string `json:"member_display_name"`
	MemberEnabled      bool   `json:"member_enabled"`
}

type IssueSubAccountInput struct {
	TenantID     string
	PrincipalRef string
	Role         string // 白名单:staff / store_manager
	DisplayName  string
	StoreScope   string // 仅 store_manager 允许非空(存在性由处理层校验)
	CreatedBy    string // 操作者的 members 行主体(留痕用)
	Enabled      bool
	// 代开留痕:AgencyRelationID/IssuedByPrincipal 为空 = owner 直开
	// (留痕行保留,但绝不冒充代理身份)。
	AgencyRelationID  string
	IssuedByPrincipal string
}

// IssueSubAccount creates the sub-account member row and its trail row in ONE
// transaction (失败零残留). The role whitelist and scope pairing are enforced
// here as the last gate; the handler validates them first for better codes.
func (s *Store) IssueSubAccount(in IssueSubAccountInput) (Member, SubAccountTrail, error) {
	// role whitelist: 代理永不铸造 org_owner/agent(legacy "owner" 也不收)
	if in.Role != "staff" && in.Role != "store_manager" {
		return Member{}, SubAccountTrail{}, ErrSubAccountRole
	}
	// scope pairing(与 provision/handleMemberCreate 同一不变量)
	if in.Role == "store_manager" && in.StoreScope == "" {
		return Member{}, SubAccountTrail{}, ErrSubAccountScope
	}
	if in.Role != "store_manager" && in.StoreScope != "" {
		return Member{}, SubAccountTrail{}, ErrSubAccountScope
	}
	// uniqueness pre-check for a clean sentinel (the UNIQUE constraint stays as
	// the concurrency backstop)
	if _, err := s.GetMemberByPrincipal(in.TenantID, in.PrincipalRef); err == nil {
		return Member{}, SubAccountTrail{}, ErrMemberExists
	} else if !errors.Is(err, ErrNotFound) {
		return Member{}, SubAccountTrail{}, err
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return Member{}, SubAccountTrail{}, err
	}
	defer tx.Rollback()

	m := Member{
		ID: newID("mem_"), TenantID: in.TenantID, PrincipalRef: in.PrincipalRef,
		Role: in.Role, Enabled: in.Enabled, DisplayName: in.DisplayName,
		StoreScope: in.StoreScope,
		CreatedBy:  in.CreatedBy, CreatedAt: now(), UpdatedAt: now(),
	}
	if _, err := tx.Exec(
		`INSERT INTO members(id,tenant_id,principal_ref,role,store_scope,enabled,display_name,created_by,created_at,updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.TenantID, m.PrincipalRef, m.Role, nullable(m.StoreScope), boolInt(m.Enabled), m.DisplayName, m.CreatedBy, m.CreatedAt, m.UpdatedAt); err != nil {
		return Member{}, SubAccountTrail{}, err
	}

	tr := SubAccountTrail{
		ID: newID("sat_"), TenantID: in.TenantID, MemberID: m.ID,
		AgencyRelationID: in.AgencyRelationID, IssuedByPrincipal: in.IssuedByPrincipal,
		IssuedByMember: in.CreatedBy, CreatedAt: now(),
	}
	if _, err := tx.Exec(
		`INSERT INTO agency_subaccount_trail(id,tenant_id,member_id,agency_rel_id,issued_by_principal,issued_by_member,created_at)
		 VALUES(?,?,?,?,?,?,?)`,
		tr.ID, tr.TenantID, tr.MemberID, nullable(tr.AgencyRelationID), tr.IssuedByPrincipal, tr.IssuedByMember, tr.CreatedAt); err != nil {
		return Member{}, SubAccountTrail{}, err
	}
	if err := tx.Commit(); err != nil {
		return Member{}, SubAccountTrail{}, err
	}
	return m, tr, nil
}

// ListSubAccountTrail lists the issuance trail of a tenant, newest first.
// issuedByPrincipal "" = owner view (all rows); non-empty = agent view
// (strictly its own issues — the filter is a bound parameter, never client
// free-text beyond the resolved principal).
func (s *Store) ListSubAccountTrail(tenantID, issuedByPrincipal string) ([]SubAccountTrail, error) {
	q := `SELECT t.id,t.tenant_id,t.member_id,t.agency_rel_id,t.issued_by_principal,t.issued_by_member,t.created_at,
	             m.principal_ref,m.role,m.display_name,m.enabled
	      FROM agency_subaccount_trail t JOIN members m ON m.id = t.member_id
	      WHERE t.tenant_id=?`
	args := []any{tenantID}
	if issuedByPrincipal != "" {
		q += ` AND t.issued_by_principal=?`
		args = append(args, issuedByPrincipal)
	}
	q += ` ORDER BY t.created_at DESC, t.id DESC`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SubAccountTrail, 0)
	for rows.Next() {
		var t SubAccountTrail
		var relID sql.NullString
		var enabled int
		if err := rows.Scan(&t.ID, &t.TenantID, &t.MemberID, &relID, &t.IssuedByPrincipal, &t.IssuedByMember, &t.CreatedAt,
			&t.MemberPrincipalRef, &t.MemberRole, &t.MemberDisplayName, &enabled); err != nil {
			return nil, err
		}
		t.AgencyRelationID = relID.String
		t.MemberEnabled = enabled == 1
		out = append(out, t)
	}
	return out, rows.Err()
}

func boolPtr(b bool) *bool { return &b }
