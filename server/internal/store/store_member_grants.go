// store_member_grants.go: HUI-4593 修复轮 2(F2) 店员动作级授权存储。
//
// 纪律:
//   - 只授店员(role=staff,最后一道闸;owner/manager/agent 走各自既有模型);
//   - 白名单 = storefront:*/action:* 冻结权限串;billing/ops 永不经此面;
//   - 现行态 = revoked_at IS NULL(0065 部分唯一索引兜底);撤销 = 墓碑,
//     再授权 = 新行;每次变更(全量替换)同事务落审计一行(前值/后值/原因);
//   - 撤回即时生效:access/effective 每请求实时读取;门店范围沿用成员行
//     store_scope 语义(带 scope 店员 = 授权仅本店,authz 侧收口)。
package store

import (
	"encoding/json"
	"errors"
	"sort"
)

// ErrMemberGrantInvalid: 授权目标非店员 / 权限串不在白名单 / 权限串为空。
var ErrMemberGrantInvalid = errors.New("store: member permission grant invalid")

// memberGrantWhitelist 冻结:店员可被授予的权限串(只开 storefront/action 两个
// 维度的配置命令;billing/ops 永不经此面)。
var memberGrantWhitelist = map[string]bool{
	"storefront:configure":  true,
	"action:configure":      true,
	"action:channel_target": true,
	"action:manage_deal":    true,
	"action:manage_voucher": true,
}

type MemberGrant struct {
	ID         string `json:"id"`
	TenantID   string `json:"tenant_id"`
	MemberID   string `json:"member_id"`
	Permission string `json:"permission"`
	GrantedBy  string `json:"granted_by"`
	GrantedAt  string `json:"granted_at"`
	RevokedBy  string `json:"revoked_by,omitempty"`
	RevokedAt  string `json:"revoked_at,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// ListMemberPermissionGrants returns the ACTIVE granted permission strings of
// one member (tenant-masked; any error answers empty = fail-closed).
func (s *Store) ListMemberPermissionGrants(tenantID, memberID string) []string {
	rows, err := s.DB.Query(
		`SELECT permission FROM member_permission_grant
		 WHERE tenant_id=? AND member_id=? AND revoked_at IS NULL ORDER BY permission`,
		tenantID, memberID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil
		}
		out = append(out, p)
	}
	return out
}

// ListTenantPermissionGrants maps member_id → active granted permissions for a
// whole tenant (成员列表一次性回填用;错误按空表处理)。
func (s *Store) ListTenantPermissionGrants(tenantID string) map[string][]string {
	out := map[string][]string{}
	rows, err := s.DB.Query(
		`SELECT member_id, permission FROM member_permission_grant
		 WHERE tenant_id=? AND revoked_at IS NULL ORDER BY member_id, permission`, tenantID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var memberID, p string
		if err := rows.Scan(&memberID, &p); err != nil {
			return out
		}
		out[memberID] = append(out[memberID], p)
	}
	return out
}

// SetMemberPermissionGrants atomically replaces one STAFF member's active
// grant set: additions get new rows, removals get tombstones, and the whole
// change lands with ONE audit row (before/after full sets + reason) in the
// same transaction. Returns the new active set.
func (s *Store) SetMemberPermissionGrants(tenantID, memberID string, permissions []string, actor, reason string) ([]string, error) {
	m, err := s.GetMember(memberID)
	if err != nil {
		return nil, err // ErrNotFound = 不存在或非本租户(掩码)
	}
	if m.TenantID != tenantID {
		return nil, ErrNotFound
	}
	if m.Role != "staff" {
		return nil, ErrMemberGrantInvalid // 只授店员;别角色走各自既有模型
	}
	seen := map[string]bool{}
	for _, p := range permissions {
		if !memberGrantWhitelist[p] || seen[p] {
			return nil, ErrMemberGrantInvalid
		}
		seen[p] = true
	}

	current := s.ListMemberPermissionGrants(tenantID, memberID)
	currentSet := map[string]bool{}
	for _, p := range current {
		currentSet[p] = true
	}
	var toAdd, toRemove []string
	for _, p := range permissions {
		if !currentSet[p] {
			toAdd = append(toAdd, p)
		}
	}
	for _, p := range current {
		if !seen[p] {
			toRemove = append(toRemove, p)
		}
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	stamped := now()
	for _, p := range toAdd {
		if _, err := tx.Exec(
			`INSERT INTO member_permission_grant(id,tenant_id,member_id,permission,granted_by,granted_at,revoked_by,revoked_at,reason)
			 VALUES(?,?,?,?,?,?, '', NULL, ?)`,
			newID("mpg_"), tenantID, memberID, p, actor, stamped, reason); err != nil {
			return nil, err
		}
	}
	for _, p := range toRemove {
		if _, err := tx.Exec(
			`UPDATE member_permission_grant SET revoked_by=?, revoked_at=?
			 WHERE tenant_id=? AND member_id=? AND permission=? AND revoked_at IS NULL`,
			actor, stamped, tenantID, memberID, p); err != nil {
			return nil, err
		}
	}
	if _, err := appendAudit(tx, AuthzAuditEntry{
		TenantID: tenantID, ActorPrincipal: actor, ActorMemberID: m.ID, ActorRole: "org_owner",
		Action: "member_permissions_set", TargetType: "member", TargetID: memberID,
		BeforeJSON: marshalStringSet(current),
		AfterJSON:  marshalStringSet(permissions),
		Reason:     reason,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.bumpEpoch()
	return permissions, nil
}

// marshalStringSet renders a permission set as a sorted JSON array; an empty
// (or nil) set is the explicit "[]" — never "" or "null"(前值语义:空集≠无前值).
func marshalStringSet(list []string) string {
	sorted := append([]string{}, list...)
	sort.Strings(sorted)
	b, err := json.Marshal(sorted)
	if err != nil || string(b) == "null" {
		return "[]"
	}
	return string(b)
}
