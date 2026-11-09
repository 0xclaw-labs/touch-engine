// store_ops_grants.go: HUI-4593 受信服务端 ops grant 存储。
//
// 纪律:
//   - ops grant 是平台运营维度(ops:*)的唯一权威来源;落库只经受信服务生产者
//     缝(HTTP 层强制),商家面/代理接口/用户输入永不自授;
//   - 不创建第二套账号:principal_ref 是既有 identity 主体,行只是 grant;
//   - commands 白名单前缀 ops:*(命令语义归 HUI-4592;本层只校验形状);
//   - 撤销 = 墓碑(revoked_at/revoked_by),绝不物理删除;双撤显式冲突;
//   - ListValidOpsGrants 只返回未撤销且未过期的行(过期判定在本函数,时间由
//     调用方注入;任何错误一律不返回该行,fail-closed)。
package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ErrOpsGrantAlreadyRevoked: 撤销已撤销的 ops grant(显式拒绝)。
var ErrOpsGrantAlreadyRevoked = errors.New("store: ops grant already revoked")

type OpsGrant struct {
	ID              string   `json:"id"`
	TenantID        string   `json:"tenant_id"`
	PrincipalRef    string   `json:"principal_ref"`
	StoreScopeSet   []string `json:"store_scope_set"` // 空 = 全门店
	Commands        []string `json:"commands"`
	IssuedByService string   `json:"issued_by_service"`
	IssuedAt        string   `json:"issued_at"`
	ExpiresAt       string   `json:"expires_at,omitempty"` // 空 = 不过期
	RevokedBy       string   `json:"revoked_by,omitempty"`
	RevokedAt       string   `json:"revoked_at,omitempty"`
}

const opsGrantCols = `id,tenant_id,principal_ref,store_scope_set,commands,issued_by_service,issued_at,expires_at,revoked_by,revoked_at`

func scanOpsGrant(sc interface{ Scan(...any) error }) (OpsGrant, error) {
	var g OpsGrant
	var scopeSet, commands string
	var revokedAt sql.NullString
	if err := sc.Scan(&g.ID, &g.TenantID, &g.PrincipalRef, &scopeSet, &commands,
		&g.IssuedByService, &g.IssuedAt, &g.ExpiresAt, &g.RevokedBy, &revokedAt); err != nil {
		return OpsGrant{}, err
	}
	g.StoreScopeSet = parseStringList(scopeSet)
	g.Commands = parseStringList(commands)
	g.RevokedAt = revokedAt.String
	return g, nil
}

type NewOpsGrant struct {
	TenantID        string
	PrincipalRef    string
	StoreScopeSet   []string
	Commands        []string
	IssuedByService string
	ExpiresAt       string
}

// CreateOpsGrant validates shape (ops:* commands, usr_* principal, RFC3339
// expiry) and inserts one grant row.
func (s *Store) CreateOpsGrant(in NewOpsGrant) (OpsGrant, error) {
	if !strings.HasPrefix(in.PrincipalRef, "usr_") {
		return OpsGrant{}, errors.New("store: ops grant principal must be an identity principal (usr_*)")
	}
	if len(in.Commands) == 0 {
		return OpsGrant{}, errors.New("store: ops grant needs at least one command")
	}
	for _, c := range in.Commands {
		if !strings.HasPrefix(c, "ops:") || len(c) <= 4 {
			return OpsGrant{}, errors.New("store: ops grant commands must be ops:*")
		}
	}
	if in.IssuedByService == "" {
		return OpsGrant{}, errors.New("store: ops grant issuer (service id) is required")
	}
	if in.ExpiresAt != "" {
		if _, err := time.Parse(time.RFC3339, in.ExpiresAt); err != nil {
			return OpsGrant{}, errors.New("store: ops grant expires_at must be RFC3339")
		}
	}
	g := OpsGrant{
		ID: newID("ops_"), TenantID: in.TenantID, PrincipalRef: in.PrincipalRef,
		StoreScopeSet: in.StoreScopeSet, Commands: in.Commands,
		IssuedByService: in.IssuedByService, IssuedAt: now(), ExpiresAt: in.ExpiresAt,
	}
	_, err := s.DB.Exec(
		`INSERT INTO ops_grants(`+opsGrantCols+`) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		g.ID, g.TenantID, g.PrincipalRef, stringListJSON(g.StoreScopeSet), stringListJSON(g.Commands),
		g.IssuedByService, g.IssuedAt, g.ExpiresAt, "", nil)
	if err != nil {
		return OpsGrant{}, err
	}
	s.bumpEpoch()
	return g, nil
}

// ListValidOpsGrants returns the UNREVOKED and UNEXPIRED grants of one
// (principal, tenant) pair — the access-resolution input. Expired/revoked
// rows and unparseable expiries are silently dropped (fail-closed).
func (s *Store) ListValidOpsGrants(principalRef, tenantID string, nowT time.Time) ([]OpsGrant, error) {
	rows, err := s.DB.Query(
		`SELECT `+opsGrantCols+` FROM ops_grants
		 WHERE principal_ref=? AND tenant_id=? AND revoked_at IS NULL`,
		principalRef, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]OpsGrant, 0)
	for rows.Next() {
		g, err := scanOpsGrant(rows)
		if err != nil {
			return nil, err
		}
		if g.ExpiresAt != "" {
			exp, perr := time.Parse(time.RFC3339, g.ExpiresAt)
			if perr != nil || !nowT.Before(exp) {
				continue
			}
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ListOpsGrantsByIssuer is the issuing service's management view (含已撤销
// 墓碑,可回查;不含他服务签发的行)。
func (s *Store) ListOpsGrantsByIssuer(serviceID string) ([]OpsGrant, error) {
	return s.listOpsGrants(`SELECT `+opsGrantCols+` FROM ops_grants WHERE issued_by_service=? ORDER BY issued_at DESC, id DESC`, serviceID)
}

func (s *Store) listOpsGrants(q string, arg string) ([]OpsGrant, error) {
	rows, err := s.DB.Query(q, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]OpsGrant, 0)
	for rows.Next() {
		g, err := scanOpsGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// RevokeOpsGrant tombstones the grant. Double revoke is an explicit conflict.
func (s *Store) RevokeOpsGrant(id, revokedBy string, nowT time.Time) (OpsGrant, error) {
	row := s.DB.QueryRow(`SELECT `+opsGrantCols+` FROM ops_grants WHERE id=?`, id)
	g, err := scanOpsGrant(row)
	if errors.Is(err, sql.ErrNoRows) {
		return OpsGrant{}, ErrNotFound
	}
	if err != nil {
		return OpsGrant{}, err
	}
	if g.RevokedAt != "" {
		return OpsGrant{}, ErrOpsGrantAlreadyRevoked
	}
	g.RevokedBy = revokedBy
	g.RevokedAt = nowT.UTC().Format(time.RFC3339Nano)
	_, err = s.DB.Exec(`UPDATE ops_grants SET revoked_by=?,revoked_at=? WHERE id=? AND revoked_at IS NULL`,
		g.RevokedBy, g.RevokedAt, g.ID)
	if err != nil {
		return OpsGrant{}, err
	}
	s.bumpEpoch()
	return g, nil
}
