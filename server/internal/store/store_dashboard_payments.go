package store

import (
	"database/sql"
	"fmt"
)

type opsMoneyFacts struct {
	Count    int64
	Amount   *int64
	Currency string
	// 本租户/订单类型中无法归入任何时间窗口的付款单或退款笔数。
	// Count 只保留确定落窗的事实；TimeUnknown > 0 时金额合计不可知。
	TimeUnknown int64
}

// 付款以原权威事件为准。开通状态不能证明金额，也不能抹掉已经发生的付款。
// 同单重复回调仍是一笔付款；晚到回调按原发生时间落窗。
func (s *Store) opsPaidOrders(tenantID, kind string, w opsWindow) (opsMoneyFacts, error) {
	const source = `
		FROM billing_orders o
		JOIN (
			SELECT tenant_id,order_id,MIN(datetime(occurred_at)) AS paid_at,
			       COUNT(*)-COUNT(datetime(occurred_at)) AS unknown_times
			FROM billing_order_events
			WHERE tenant_id=? AND event_type='payment_paid'
			  AND TRIM(source)<>'' AND TRIM(event_id)<>''
			GROUP BY tenant_id,order_id
		) p ON p.order_id=o.id AND p.tenant_id=o.tenant_id
		WHERE o.tenant_id=? AND o.kind=?`
	facts, err := s.opsMoneyTotal("paid_orders", "o.amount_minor", "o.currency", source+`
		  AND p.unknown_times=0 AND p.paid_at>=datetime(?) AND p.paid_at<=datetime(?)`,
		[]any{tenantID, tenantID, kind, w.startTS, w.endTS})
	if err != nil {
		return facts, err
	}
	// 有无法解析的付款时刻时，MIN 忽略 NULL 后的值不一定是原付款日。
	// 不用接收日替代，也不把其余订单的部分和声称为完整支出。
	facts.TimeUnknown, err = s.opsScalar("paid_orders_unknown_time", "SELECT COUNT(*) "+source+" AND p.unknown_times>0", []any{tenantID, tenantID, kind})
	if facts.TimeUnknown > 0 {
		facts.Amount, facts.Currency = nil, ""
	}
	return facts, err
}

// 退款仍按原退款事实分列。订单类型决定订阅/硬件归属，不与付款相减。
func (s *Store) opsRefunds(tenantID, kind string, w opsWindow) (opsMoneyFacts, error) {
	const source = `
		FROM billing_refunds r
		JOIN billing_orders o ON o.id=r.order_id AND o.tenant_id=r.tenant_id
		WHERE r.tenant_id=? AND o.kind=?`
	facts, err := s.opsMoneyTotal("refunds", "r.amount_minor", "r.currency", source+`
		  AND datetime(r.occurred_at)>=datetime(?) AND datetime(r.occurred_at)<=datetime(?)`,
		[]any{tenantID, kind, w.startTS, w.endTS})
	if err != nil {
		return facts, err
	}
	facts.TimeUnknown, err = s.opsScalar("refunds_unknown_time", "SELECT COUNT(*) "+source+" AND datetime(r.occurred_at) IS NULL", []any{tenantID, kind})
	if facts.TimeUnknown > 0 {
		facts.Amount, facts.Currency = nil, ""
	} else if err == nil && facts.Count == 0 {
		facts.Amount = new(int64) // 已读取本地退款账，确无记录；不是缺金额的业务零。
	}
	return facts, err
}

// 单一币种且每条金额已知时才有合计。不换汇，不把部分 SUM 当完整金额。
// 列名与查询片段均为本包静态常量，外部输入只通过绑定参数进入查询。
func (s *Store) opsMoneyTotal(label, amountColumn, currencyColumn, fromWhere string, args []any) (facts opsMoneyFacts, err error) {
	var amount sql.NullInt64
	var recorded, currencies int64
	var currency string
	valid := "typeof(" + amountColumn + ")='integer' AND " + amountColumn + ">=0"
	query := `SELECT COUNT(*), SUM(CASE WHEN ` + valid + ` THEN ` + amountColumn + ` END),
		COUNT(CASE WHEN ` + valid + ` AND TRIM(` + currencyColumn + `)<>'' THEN 1 END),
		COUNT(DISTINCT TRIM(` + currencyColumn + `)), COALESCE(MIN(TRIM(` + currencyColumn + `)),'') ` + fromWhere
	if err = s.DB.QueryRow(query, args...).Scan(&facts.Count, &amount, &recorded, &currencies, &currency); err != nil {
		return facts, fmt.Errorf("dashboard_ops[%s]: %w", label, err)
	}
	if amount.Valid && recorded == facts.Count && currencies == 1 {
		value := amount.Int64
		facts.Amount, facts.Currency = &value, currency
	}
	return facts, nil
}
