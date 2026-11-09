// store_authz_audit.go: HUI-4593 授权域审计存储(append-only)。
//
// 纪律:
//   - 只有 INSERT 面;UPDATE/DELETE 零暴露;
//   - 每行必备:操作者(principal/成员行/角色)/时间/动作/目标;前后值 JSON 快照;
//   - 同事务写:scope 变更/撤销等特权动作与审计行一起落库(失败零残留);
//   - 列表按租户收口,目标过滤可选 —— 他租户审计永不外泄。
package store

import (
	"database/sql"
	"errors"
)

// AuditExecer covers *sql.DB and *sql.Tx so privileged writers can append the
// audit row inside their own transaction.
type AuditExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

type AuthzAuditEntry struct {
	ID             string `json:"id"`
	TenantID       string `json:"tenant_id"`
	ActorPrincipal string `json:"actor_principal"`
	ActorMemberID  string `json:"actor_member_id,omitempty"`
	ActorRole      string `json:"actor_role,omitempty"`
	Action         string `json:"action"`
	TargetType     string `json:"target_type"`
	TargetID       string `json:"target_id"`
	BeforeJSON     string `json:"before_json,omitempty"`
	AfterJSON      string `json:"after_json,omitempty"`
	Reason         string `json:"reason,omitempty"`
	CreatedAt      string `json:"created_at"`
}

const authzAuditCols = `id,tenant_id,actor_principal,actor_member_id,actor_role,action,target_type,target_id,before_json,after_json,reason,created_at`

func scanAuthzAudit(sc interface{ Scan(...any) error }) (AuthzAuditEntry, error) {
	var e AuthzAuditEntry
	err := sc.Scan(&e.ID, &e.TenantID, &e.ActorPrincipal, &e.ActorMemberID, &e.ActorRole,
		&e.Action, &e.TargetType, &e.TargetID, &e.BeforeJSON, &e.AfterJSON, &e.Reason, &e.CreatedAt)
	return e, err
}

// appendAudit stamps and inserts one audit row on the given execer (tx-safe).
// Caller supplies tenant/actor/action/target; ID and CreatedAt are minted here.
func appendAudit(ex AuditExecer, e AuthzAuditEntry) (AuthzAuditEntry, error) {
	if e.TenantID == "" || e.ActorPrincipal == "" || e.Action == "" || e.TargetType == "" || e.TargetID == "" {
		return AuthzAuditEntry{}, errors.New("store: audit entry missing mandatory fields")
	}
	e.ID = newID("aud_")
	e.CreatedAt = now()
	_, err := ex.Exec(
		`INSERT INTO authz_audit_log(`+authzAuditCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.ID, e.TenantID, e.ActorPrincipal, e.ActorMemberID, e.ActorRole,
		e.Action, e.TargetType, e.TargetID, e.BeforeJSON, e.AfterJSON, e.Reason, e.CreatedAt)
	if err != nil {
		return AuthzAuditEntry{}, err
	}
	return e, nil
}

// AppendAuthzAudit is the standalone append (writes not wrapped in a caller tx).
func (s *Store) AppendAuthzAudit(e AuthzAuditEntry) (AuthzAuditEntry, error) {
	out, err := appendAudit(s.DB, e)
	if err == nil {
		s.bumpEpoch()
	}
	return out, err
}

// ListAuthzAudit lists a tenant's audit rows, newest first. targetType/targetID
// "" = unfiltered (never crosses tenants: tenantID is a bound parameter).
func (s *Store) ListAuthzAudit(tenantID, targetType, targetID string, limit int) ([]AuthzAuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT ` + authzAuditCols + ` FROM authz_audit_log WHERE tenant_id=?`
	args := []any{tenantID}
	if targetType != "" {
		q += ` AND target_type=?`
		args = append(args, targetType)
		if targetID != "" {
			q += ` AND target_id=?`
			args = append(args, targetID)
		}
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AuthzAuditEntry, 0)
	for rows.Next() {
		e, err := scanAuthzAudit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
