// store_template_assignment.go: HUI-4593 连锁模板分发引用存储。
//
// 纪律:
//   - 本存储只记录「分发与授权结果」:不写 storefront 版本表、不承载模板内容
//     (应用委托 HUI-1676 领域服务;发布仍走 1676 预检/确认/回滚);
//   - 幂等重放:带 request_key 的同请求(租户+店+模板+版本+key)唯一索引兜底,
//     重试返回既有行(replayed=true),不重复创建;无 key 的重复分发合法;
//   - 保留字段拒绝:门店目标/账号/二维码/Wi-Fi 密码永不进 allowed_override_fields
//     (跨店盲目复制的通道在存储层最后关死);
//   - status 生命周期:distributed → applied(带 applied_revision)/cancelled;
//     applied/cancelled 为终态(显式拒绝,不静默二次处理);
//   - 读面全部按租户掩码:他租户的分发行 = ErrNotFound。
package store

import (
	"database/sql"
	"errors"
)

var (
	// ErrTemplateReservedField: allowed_override_fields 含保留字段(门店目标/
	// 账号/二维码/Wi-Fi 密码永不跨店盲目复制)。
	ErrTemplateReservedField = errors.New("store: template assignment carries a reserved override field")
	// ErrTemplateStatus: 非法的状态迁移(终态不可再改)。
	ErrTemplateStatus = errors.New("store: template assignment status transition is not allowed")
)

// templateReservedFields 冻结:这些字段是门店自有目标事实(账号/二维码/
// Wi-Fi 密码/门店目标),模板下发永不触达。
var templateReservedFields = map[string]bool{
	"store_target":  true,
	"account":       true,
	"qr_code":       true,
	"wifi_password": true,
}

type TemplateAssignment struct {
	ID                    string   `json:"id"`
	TenantID              string   `json:"tenant_id"`
	StoreID               string   `json:"store_id"`
	TemplateRef           string   `json:"template_ref"`
	SourceVersion         string   `json:"source_version"`
	AllowedOverrideFields []string `json:"allowed_override_fields"`
	RequestKey            string   `json:"request_key,omitempty"`
	Status                string   `json:"status"`
	AppliedRevision       string   `json:"applied_revision,omitempty"`
	CreatedBy             string   `json:"created_by"`
	CreatedAt             string   `json:"created_at"`
	UpdatedAt             string   `json:"updated_at"`
}

const templateAssignmentCols = `id,tenant_id,store_id,template_ref,source_version,allowed_override_fields,request_key,status,applied_revision,created_by,created_at,updated_at`

func scanTemplateAssignment(sc interface{ Scan(...any) error }) (TemplateAssignment, error) {
	var t TemplateAssignment
	var fields, key string
	if err := sc.Scan(&t.ID, &t.TenantID, &t.StoreID, &t.TemplateRef, &t.SourceVersion,
		&fields, &key, &t.Status, &t.AppliedRevision, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return TemplateAssignment{}, err
	}
	t.AllowedOverrideFields = parseStringList(fields)
	t.RequestKey = key
	return t, nil
}

type NewTemplateAssignment struct {
	TenantID              string
	StoreID               string
	TemplateRef           string
	SourceVersion         string
	AllowedOverrideFields []string
	RequestKey            string
	CreatedBy             string
}

// CreateTemplateAssignment validates + inserts one distribution row, with the
// replay unique index as the idempotency backstop. Returns replayed=true when
// an existing row for the same keyed request is returned instead.
func (s *Store) CreateTemplateAssignment(in NewTemplateAssignment) (TemplateAssignment, bool, error) {
	if in.TemplateRef == "" || in.SourceVersion == "" {
		return TemplateAssignment{}, false, errors.New("store: template_ref and source_version are required")
	}
	for _, f := range in.AllowedOverrideFields {
		if templateReservedFields[f] {
			return TemplateAssignment{}, false, ErrTemplateReservedField
		}
	}
	if _, err := s.GetStore(in.StoreID, in.TenantID); err != nil {
		return TemplateAssignment{}, false, ErrNotFound // 他店/他租户:掩码拒绝
	}

	// replay path (keyed requests only; 唯一索引是并发兜底)
	if in.RequestKey != "" {
		cur, err := s.getTemplateAssignmentByKey(in)
		if err == nil {
			return cur, true, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return TemplateAssignment{}, false, err
		}
	}

	t := TemplateAssignment{
		ID: newID("tas_"), TenantID: in.TenantID, StoreID: in.StoreID,
		TemplateRef: in.TemplateRef, SourceVersion: in.SourceVersion,
		AllowedOverrideFields: in.AllowedOverrideFields, RequestKey: in.RequestKey,
		Status: "distributed", CreatedBy: in.CreatedBy, CreatedAt: now(), UpdatedAt: now(),
	}
	_, err := s.DB.Exec(
		`INSERT INTO template_assignment(`+templateAssignmentCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.TenantID, t.StoreID, t.TemplateRef, t.SourceVersion,
		stringListJSON(t.AllowedOverrideFields), t.RequestKey, t.Status, t.AppliedRevision,
		t.CreatedBy, t.CreatedAt, t.UpdatedAt)
	if err != nil {
		// 并发重放兜底:唯一索引冲突 → 回读既有行按 replayed 回答
		if in.RequestKey != "" {
			if cur, gerr := s.getTemplateAssignmentByKey(in); gerr == nil {
				return cur, true, nil
			}
		}
		return TemplateAssignment{}, false, err
	}
	s.bumpEpoch()
	return t, false, nil
}

func (s *Store) getTemplateAssignmentByKey(in NewTemplateAssignment) (TemplateAssignment, error) {
	row := s.DB.QueryRow(
		`SELECT `+templateAssignmentCols+` FROM template_assignment
		 WHERE tenant_id=? AND store_id=? AND template_ref=? AND source_version=? AND request_key=?`,
		in.TenantID, in.StoreID, in.TemplateRef, in.SourceVersion, in.RequestKey)
	t, err := scanTemplateAssignment(row)
	if errors.Is(err, sql.ErrNoRows) {
		return TemplateAssignment{}, ErrNotFound
	}
	return t, err
}

// GetTemplateAssignment reads one row masked by tenant (他租户 = ErrNotFound).
func (s *Store) GetTemplateAssignment(id, tenantID string) (TemplateAssignment, error) {
	row := s.DB.QueryRow(`SELECT `+templateAssignmentCols+` FROM template_assignment WHERE id=? AND tenant_id=?`, id, tenantID)
	t, err := scanTemplateAssignment(row)
	if errors.Is(err, sql.ErrNoRows) {
		return TemplateAssignment{}, ErrNotFound
	}
	return t, err
}

// ListTemplateAssignments lists a tenant's rows, newest first; storeID "" =
// 全租户(总部视图),非空 = 本店视图(服务端过滤,客户端参数只做收窄)。
func (s *Store) ListTemplateAssignments(tenantID, storeID string) ([]TemplateAssignment, error) {
	q := `SELECT ` + templateAssignmentCols + ` FROM template_assignment WHERE tenant_id=?`
	args := []any{tenantID}
	if storeID != "" {
		q += ` AND store_id=?`
		args = append(args, storeID)
	}
	q += ` ORDER BY created_at DESC, id DESC`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]TemplateAssignment, 0)
	for rows.Next() {
		t, err := scanTemplateAssignment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateTemplateAssignmentStatus moves distributed → applied (with the 1676
// applied revision) or → cancelled (总部撤回). Terminal states are refused.
func (s *Store) UpdateTemplateAssignmentStatus(id, tenantID, status, appliedRevision, actor string) (TemplateAssignment, error) {
	cur, err := s.GetTemplateAssignment(id, tenantID)
	if err != nil {
		return TemplateAssignment{}, err
	}
	switch {
	case cur.Status == "distributed" && status == "applied":
		if appliedRevision == "" {
			return TemplateAssignment{}, ErrTemplateStatus // applied 必须带 1676 版本引用
		}
	case cur.Status == "distributed" && status == "cancelled":
		appliedRevision = ""
	default:
		return TemplateAssignment{}, ErrTemplateStatus
	}
	if _, err := s.DB.Exec(
		`UPDATE template_assignment SET status=?, applied_revision=?, updated_at=? WHERE id=? AND tenant_id=? AND status='distributed'`,
		status, appliedRevision, now(), cur.ID, tenantID); err != nil {
		return TemplateAssignment{}, err
	}
	s.bumpEpoch()
	cur.Status, cur.AppliedRevision, cur.UpdatedAt = status, appliedRevision, now()
	_ = actor
	return cur, nil
}
