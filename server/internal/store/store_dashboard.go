// store_dashboard.go: HUI-1677 FEAT-0178 数据统计看板的只读 SQL 汇总层。
//
// 纪律:
//   - 只读:本文件没有任何写路径;汇总按需计算,不落任何汇总表(原始事实
//     与汇总分离,同窗口可重算)。
//   - 租户隔离:所有查询以 tenant_id 为必选绑定参数;storeScope 非空时进一步
//     收窄到该门店(门店经理),跨租户/跨店行从不进入聚合(fail-closed 0 行)。
//     门店归属一律取 campaigns.store_id 的当前绑定(与 authz 裁决一致),
//     客户端输入永不参与。
//   - 窗口语义:RFC3339(Nano) UTC 列用 datetime(col) 与 datetime(?) 比较
//     (整秒粒度,边界含);public_view_stats.day 用日期字符串比较(日粒度,
//     边界含)。语义与 dashboard 包的 metric definition 逐字对应。
package store

import (
	"database/sql"
	"fmt"
	"time"
)

// DimBucket is one drill-down aggregate. Key is the dimension id
// (or "" for 租户级/未绑定); Value is the aggregated fact count.
type DimBucket struct {
	Key   string
	Value int64
}

// DashboardFacts is the store-computed touch-domain fact set for one window
// and scope. Zero values are TRUE zeros (the source tables exist and the
// aggregate over the scope is empty) — never to be confused with UNKNOWN.
type DashboardFacts struct {
	// 碰/扫码触发:public_view_stats 总数与下钻。
	TriggerTotal      int64
	TriggerByChannel  []DimBucket
	TriggerByCampaign []DimBucket
	TriggerByStore    []DimBucket
	TriggerByTag      []DimBucket

	// 留资访问:挂载启用表单活动的访问。
	LeadVisitTotal      int64
	LeadVisitByCampaign []DimBucket
	LeadVisitByStore    []DimBucket

	// 授权留资提交:正向计数(排除 revoked)与唯一联系人分母。
	SubmissionTotal          int64
	SubmissionUniqueContacts int64
	SubmissionByCampaign     []DimBucket
	SubmissionByStore        []DimBucket
	SubmissionByState        []DimBucket // 全状态披露(含 revoked/rejected,不隐藏)

	// CRM 接收:pending_sync→crm_received 真实状态迁移。
	CRMReceivedTotal      int64
	CRMReceivedByCampaign []DimBucket
	CRMReceivedByStore    []DimBucket
}

// DashboardDimensions carries the reference rows (来源引用) the breakdown keys
// point into, already narrowed to the caller's scope.
type DashboardDimensions struct {
	Campaigns []DashboardCampaignRef `json:"campaigns,omitempty"`
	Stores    []DashboardStoreRef    `json:"stores,omitempty"`
	Tags      []DashboardTagRef      `json:"tags,omitempty"`
	// Actions (TOUCH-ALL-20261109-v1): 4584 store-action reference rows for
	// the action_id drill-down filter (scope-narrowed, includes suspended
	// rows so the merchant can still see why a filter matches nothing).
	Actions []DashboardActionRef `json:"actions,omitempty"`
}

type DashboardCampaignRef struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	StoreID string `json:"store_id,omitempty"`
}

type DashboardStoreRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// DashboardActionRef is one 4584 store-action instance reference.
type DashboardActionRef struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Platform string `json:"platform,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Enabled  bool   `json:"enabled"`
}

type DashboardTagRef struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	GroupID string `json:"group_id,omitempty"`
	StoreID string `json:"store_id,omitempty"`
	Code    string `json:"code"`
}

// dashboardWindow is the store-side window parameters (pre-validated by the
// dashboard package).
type dashboardWindow struct {
	startDay, endDay string // day-granular facts (inclusive)
	startTS, endTS   string // second-granular facts (inclusive, UTC RFC3339)
}

func newDashboardWindow(start, end time.Time) dashboardWindow {
	return dashboardWindow{
		startDay: start.Format("2006-01-02"),
		endDay:   end.Format("2006-01-02"),
		startTS:  start.Format(time.RFC3339),
		endTS:    end.Format(time.RFC3339),
	}
}

// dashCampaignScope / dashTagScope build the mandatory tenant condition plus
// the optional store narrowing (store_manager scope). Every value is a bound
// parameter; the scope comes from the server-side membership row, never from
// request input.
func dashCampaignScope(tenantID, storeScope string) (string, []any) {
	cond := "c.tenant_id=?"
	args := []any{tenantID}
	if storeScope != "" {
		cond += " AND c.store_id=?"
		args = append(args, storeScope)
	}
	return cond, args
}

func dashTagScope(tenantID, storeScope string) (string, []any) {
	cond := "t.tenant_id=?"
	args := []any{tenantID}
	if storeScope != "" {
		cond += " AND t.store_id=?"
		args = append(args, storeScope)
	}
	return cond, args
}

// dashScalar / dashBuckets run one labeled statement; errors carry the label
// so a failing aggregation is identifiable in logs.
func (s *Store) dashScalar(label, q string, args []any) (n int64, err error) {
	err = s.DB.QueryRow(q, args...).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("dashboard[%s]: %w", label, err)
	}
	return n, nil
}

func (s *Store) dashBuckets(label, q string, args []any) (out []DimBucket, err error) {
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("dashboard[%s]: %w", label, err)
	}
	defer rows.Close()
	out = make([]DimBucket, 0)
	for rows.Next() {
		var b DimBucket
		if err := rows.Scan(&b.Key, &b.Value); err != nil {
			return nil, fmt.Errorf("dashboard[%s]: %w", label, err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func dashRefs[T any](label string, rows *sql.Rows, err error, scan func(*T) error) (out []T, err2 error) {
	if err != nil {
		return nil, fmt.Errorf("dashboard[%s]: %w", label, err)
	}
	defer rows.Close()
	out = make([]T, 0)
	for rows.Next() {
		var r T
		if serr := scan(&r); serr != nil {
			return nil, fmt.Errorf("dashboard[%s]: %w", label, serr)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DashboardFacts aggregates the touch-domain facts of one tenant (optionally
// narrowed to one store) over the window. It is read-only and idempotent:
// same inputs -> same outputs. tenantID is mandatory (fail-closed: an empty
// tenant matches no rows); storeScope "" means 总部/租户级.
func (s *Store) DashboardFacts(tenantID, storeScope string, start, end time.Time) (facts DashboardFacts, dims DashboardDimensions, err error) {
	if tenantID == "" {
		return facts, dims, nil // fail-closed: no tenant, no rows
	}
	w := newDashboardWindow(start, end)
	campCond, campArgs := dashCampaignScope(tenantID, storeScope)
	tagCond, tagArgs := dashTagScope(tenantID, storeScope)
	dayArgs := func(base []any) []any { return append(append([]any{}, base...), w.startDay, w.endDay) }
	tsArgs := func(base []any) []any { return append(append([]any{}, base...), w.startTS, w.endTS) }

	// ---- 碰/扫码触发(public_view_stats,日粒度窗口)----
	triggerFrom := `
		FROM public_view_stats v
		JOIN campaign_links l ON l.code=v.code
		JOIN campaigns c ON c.id=l.campaign_id
		WHERE ` + campCond + ` AND v.day>=? AND v.day<=?`
	if facts.TriggerTotal, err = s.dashScalar("trigger_total",
		`SELECT COALESCE(SUM(v.views),0)`+triggerFrom, dayArgs(campArgs)); err != nil {
		return facts, dims, err
	}
	if facts.TriggerByChannel, err = s.dashBuckets("trigger_by_channel",
		`SELECT v.channel, SUM(v.views)`+triggerFrom+` GROUP BY v.channel ORDER BY 1`, dayArgs(campArgs)); err != nil {
		return facts, dims, err
	}
	if facts.TriggerByCampaign, err = s.dashBuckets("trigger_by_campaign",
		`SELECT c.id, SUM(v.views)`+triggerFrom+` GROUP BY c.id ORDER BY 1`, dayArgs(campArgs)); err != nil {
		return facts, dims, err
	}
	if facts.TriggerByStore, err = s.dashBuckets("trigger_by_store",
		`SELECT COALESCE(c.store_id,''), SUM(v.views)`+triggerFrom+` GROUP BY COALESCE(c.store_id,'') ORDER BY 1`, dayArgs(campArgs)); err != nil {
		return facts, dims, err
	}
	// by_tag:标签作用域按标签自己的门店绑定(与标签面 authz 一致);口径是
	// 「作用域内标签所挂短码的触发」,不承诺与总量可加。
	if facts.TriggerByTag, err = s.dashBuckets("trigger_by_tag",
		`SELECT t.id, COALESCE(SUM(v.views),0)
		 FROM nfc_tags t
		 JOIN campaign_links l ON l.id=t.link_id
		 JOIN public_view_stats v ON v.code=l.code
		 WHERE `+tagCond+` AND v.day>=? AND v.day<=?
		 GROUP BY t.id ORDER BY 1`, dayArgs(tagArgs)); err != nil {
		return facts, dims, err
	}

	// ---- 留资访问(挂载启用表单的活动页访问)----
	leadViewFrom := `
		FROM public_view_stats v
		JOIN campaign_links l ON l.code=v.code
		JOIN campaigns c ON c.id=l.campaign_id
		JOIN lead_forms f ON f.campaign_id=c.id AND f.enabled=1
		WHERE ` + campCond + ` AND v.day>=? AND v.day<=?`
	if facts.LeadVisitTotal, err = s.dashScalar("lead_visit_total",
		`SELECT COALESCE(SUM(v.views),0)`+leadViewFrom, dayArgs(campArgs)); err != nil {
		return facts, dims, err
	}
	if facts.LeadVisitByCampaign, err = s.dashBuckets("lead_visit_by_campaign",
		`SELECT c.id, SUM(v.views)`+leadViewFrom+` GROUP BY c.id ORDER BY 1`, dayArgs(campArgs)); err != nil {
		return facts, dims, err
	}
	if facts.LeadVisitByStore, err = s.dashBuckets("lead_visit_by_store",
		`SELECT COALESCE(c.store_id,''), SUM(v.views)`+leadViewFrom+` GROUP BY COALESCE(c.store_id,'') ORDER BY 1`, dayArgs(campArgs)); err != nil {
		return facts, dims, err
	}

	// ---- 授权留资提交(秒粒度窗口;正向排除 revoked;全状态披露)----
	subFrom := `
		FROM lead_submissions s
		JOIN campaigns c ON c.id=s.campaign_id
		WHERE ` + campCond + `
		  AND datetime(s.created_at)>=datetime(?) AND datetime(s.created_at)<=datetime(?)`
	if facts.SubmissionTotal, err = s.dashScalar("submission_total",
		`SELECT COUNT(1)`+subFrom+` AND s.sync_state<>'revoked'`, tsArgs(campArgs)); err != nil {
		return facts, dims, err
	}
	if facts.SubmissionUniqueContacts, err = s.dashScalar("submission_unique_contacts",
		`SELECT COUNT(DISTINCT s.phone)`+subFrom+` AND s.sync_state<>'revoked'`, tsArgs(campArgs)); err != nil {
		return facts, dims, err
	}
	if facts.SubmissionByCampaign, err = s.dashBuckets("submission_by_campaign",
		`SELECT s.campaign_id, COUNT(1)`+subFrom+` AND s.sync_state<>'revoked' GROUP BY s.campaign_id ORDER BY 1`, tsArgs(campArgs)); err != nil {
		return facts, dims, err
	}
	if facts.SubmissionByStore, err = s.dashBuckets("submission_by_store",
		`SELECT COALESCE(c.store_id,''), COUNT(1)`+subFrom+` AND s.sync_state<>'revoked' GROUP BY COALESCE(c.store_id,'') ORDER BY 1`, tsArgs(campArgs)); err != nil {
		return facts, dims, err
	}
	// 全状态披露(含 revoked):不隐藏撤销事实,只不计入正向。
	if facts.SubmissionByState, err = s.dashBuckets("submission_by_state",
		`SELECT s.sync_state, COUNT(1)`+subFrom+` GROUP BY s.sync_state ORDER BY 1`, tsArgs(campArgs)); err != nil {
		return facts, dims, err
	}

	// ---- CRM 接收(真实状态机状态;状态迁移时刻落窗)----
	crmFrom := `
		FROM lead_submissions s
		JOIN campaigns c ON c.id=s.campaign_id
		WHERE ` + campCond + `
		  AND s.sync_state='crm_received'
		  AND datetime(s.updated_at)>=datetime(?) AND datetime(s.updated_at)<=datetime(?)`
	if facts.CRMReceivedTotal, err = s.dashScalar("crm_received_total",
		`SELECT COUNT(1)`+crmFrom, tsArgs(campArgs)); err != nil {
		return facts, dims, err
	}
	if facts.CRMReceivedByCampaign, err = s.dashBuckets("crm_received_by_campaign",
		`SELECT s.campaign_id, COUNT(1)`+crmFrom+` GROUP BY s.campaign_id ORDER BY 1`, tsArgs(campArgs)); err != nil {
		return facts, dims, err
	}
	if facts.CRMReceivedByStore, err = s.dashBuckets("crm_received_by_store",
		`SELECT COALESCE(c.store_id,''), COUNT(1)`+crmFrom+` GROUP BY COALESCE(c.store_id,'') ORDER BY 1`, tsArgs(campArgs)); err != nil {
		return facts, dims, err
	}

	// ---- 维度来源引用(来源引用贯穿)----
	dims.Campaigns, err = s.dashCampaignRefs(campCond, campArgs)
	if err != nil {
		return facts, dims, err
	}
	storeCond := "tenant_id=?"
	storeArgs := []any{tenantID}
	if storeScope != "" {
		storeCond += " AND id=?"
		storeArgs = append(storeArgs, storeScope)
	}
	dims.Stores, err = s.dashStoreRefs(storeCond, storeArgs)
	if err != nil {
		return facts, dims, err
	}
	dims.Tags, err = s.dashTagRefs(tagCond, tagArgs)
	if err != nil {
		return facts, dims, err
	}
	return facts, dims, nil
}

func (s *Store) dashCampaignRefs(cond string, args []any) ([]DashboardCampaignRef, error) {
	rows, err := s.DB.Query(`SELECT id, title, COALESCE(store_id,'') FROM campaigns c WHERE `+cond+` ORDER BY id`, args...)
	if err != nil {
		return nil, fmt.Errorf("dashboard[dim_campaigns]: %w", err)
	}
	defer rows.Close()
	out := make([]DashboardCampaignRef, 0)
	for rows.Next() {
		var r DashboardCampaignRef
		if err := rows.Scan(&r.ID, &r.Title, &r.StoreID); err != nil {
			return nil, fmt.Errorf("dashboard[dim_campaigns]: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) dashStoreRefs(cond string, args []any) ([]DashboardStoreRef, error) {
	rows, err := s.DB.Query(`SELECT id, name FROM stores WHERE `+cond+` ORDER BY id`, args...)
	if err != nil {
		return nil, fmt.Errorf("dashboard[dim_stores]: %w", err)
	}
	defer rows.Close()
	out := make([]DashboardStoreRef, 0)
	for rows.Next() {
		var r DashboardStoreRef
		if err := rows.Scan(&r.ID, &r.Name); err != nil {
			return nil, fmt.Errorf("dashboard[dim_stores]: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) dashTagRefs(cond string, args []any) ([]DashboardTagRef, error) {
	rows, err := s.DB.Query(`SELECT t.id, t.label, COALESCE(t.group_id,''), COALESCE(t.store_id,''), l.code
		 FROM nfc_tags t
		 JOIN campaign_links l ON l.id=t.link_id
		 WHERE `+cond+` ORDER BY t.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("dashboard[dim_tags]: %w", err)
	}
	defer rows.Close()
	out := make([]DashboardTagRef, 0)
	for rows.Next() {
		var r DashboardTagRef
		if err := rows.Scan(&r.ID, &r.Label, &r.GroupID, &r.StoreID, &r.Code); err != nil {
			return nil, fmt.Errorf("dashboard[dim_tags]: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
