package store

// store_billing_test.go: HUI-4591 订购收费账单 SQL 层测试(可重建投影/幂等纪律)。

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/billing"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
)

func newBillingStore(t *testing.T) (*Store, string) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	st := New(d)
	ten, err := st.CreateTenant("账单测试租户")
	if err != nil {
		t.Fatalf("tenant: %v", err)
	}
	return st, ten.ID
}

func catalogEntryGlobal() billing.CatalogEntry {
	return billing.CatalogEntry{
		ScopeTenant: "", PlanCode: billing.PlanMonthly, PricingVersion: "v2026",
		PriceRef: "price_m_v2026", Currency: "CNY", PriceMinor: 990000, ServiceDays: 365,
		IncludedStoreCount: 3, ExtraStorePriceMinor: 60000, TierJSON: "[]", GraceDays: 30,
	}
}

func TestUpsertAndResolveCatalog(t *testing.T) {
	st, tenID := newBillingStore(t)
	if _, err := st.UpsertBillingCatalogEntry(catalogEntryGlobal(), now()); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	entries, err := st.ListBillingCatalog(tenID)
	if err != nil || len(entries) != 1 || entries[0].PriceRef != "price_m_v2026" {
		t.Fatalf("list: %v %+v", err, entries)
	}
	// 同键 upsert = 价格修订覆盖(投影可重建)
	e := catalogEntryGlobal()
	e.PriceMinor = 880000
	if _, err := st.UpsertBillingCatalogEntry(e, now()); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	entries, _ = st.ListBillingCatalog(tenID)
	if len(entries) != 1 || entries[0].PriceMinor != 880000 {
		t.Fatalf("catalog revision: %+v", entries)
	}
}

func TestCreateBillingOrderIdempotencyKeyReplay(t *testing.T) {
	st, tenID := newBillingStore(t)
	in := NewBillingOrder{
		TenantID: tenID, Kind: billing.KindSubscription, Action: billing.ActionNew,
		PlanCode: billing.PlanMonthly, PriceRef: "price_m_v2026", PriceSnapshot: `{"price_minor":990000}`,
		AmountMinor: 990000, Currency: "CNY", IdempotencyKey: "idem-1",
		ActorPrincipal: "usr_owner", ActorRole: "org_owner",
	}
	row, replayed, err := st.CreateBillingOrder(in, now())
	if err != nil || replayed || row.Status != billing.OrderProcessing {
		t.Fatalf("create: %v replayed=%v %+v", err, replayed, row)
	}
	row2, replayed2, err := st.CreateBillingOrder(in, now())
	if err != nil || !replayed2 || row2.ID != row.ID {
		t.Fatalf("replay: %v replayed=%v", err, replayed2)
	}
	// 不同键 = 新订单
	in.IdempotencyKey = "idem-2"
	row3, _, err := st.CreateBillingOrder(in, now())
	if err != nil || row3.ID == row.ID {
		t.Fatalf("second key: %v", err)
	}
}

func TestApplyBillingOrderEventFullActivationLoop(t *testing.T) {
	st, tenID := newBillingStore(t)
	in := NewBillingOrder{
		TenantID: tenID, Kind: billing.KindSubscription, Action: billing.ActionNew,
		PlanCode: billing.PlanMonthly, AmountMinor: 990000, Currency: "CNY",
		IdempotencyKey: "idem-10", ActorPrincipal: "usr_owner", ActorRole: "org_owner",
	}
	row, _, err := st.CreateBillingOrder(in, now())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	at := now()
	// 支付事实 → opening
	row, replayed, err := st.ApplyBillingOrderEvent(tenID, row.ID, billing.AuthorityEventInput{
		Source: "eco-billing", EventID: "ev_pay", Type: billing.EventPaymentPaid,
		OccurredAt: at, Payload: billing.EventPayload{OrderRef: "ord_x", PaymentRef: "pay_x"},
	}, at)
	if err != nil || replayed || row.Status != billing.OrderOpening {
		t.Fatalf("pay: %v replayed=%v %+v", err, replayed, row)
	}
	// 同事件重放 → 幂等读回
	_, replayed2, err := st.ApplyBillingOrderEvent(tenID, row.ID, billing.AuthorityEventInput{
		Source: "eco-billing", EventID: "ev_pay", Type: billing.EventPaymentPaid,
		OccurredAt: at, Payload: billing.EventPayload{OrderRef: "ord_x", PaymentRef: "pay_x"},
	}, at)
	if err != nil || !replayed2 {
		t.Fatalf("replay pay: %v replayed=%v", err, replayed2)
	}
	// 同键异负载 → IDEMPOTENCY_CONFLICT
	_, _, err = st.ApplyBillingOrderEvent(tenID, row.ID, billing.AuthorityEventInput{
		Source: "eco-billing", EventID: "ev_pay", Type: billing.EventPaymentPaid,
		OccurredAt: at, Payload: billing.EventPayload{OrderRef: "ord_DIFFERENT", PaymentRef: "pay_x"},
	}, at)
	if !errors.Is(err, ErrBillingIdempotencyConflict) {
		t.Fatalf("conflict = %v", err)
	}
	// 开通事实 → active + 订阅投影
	row, _, err = st.ApplyBillingOrderEvent(tenID, row.ID, billing.AuthorityEventInput{
		Source: "eco-billing", EventID: "ev_act", Type: billing.EventActivationActive,
		OccurredAt: at, Payload: billing.EventPayload{
			SubscriptionRef: "sub_eco_1", PlanCode: billing.PlanMonthly, PricingVersion: "v2026",
			PaidUntil: "2027-06-01T00:00:00Z", StoreLimit: 5, GraceDays: 30,
			EntitlementProjection: `{"plan":"monthly"}`,
		},
	}, at)
	if err != nil || row.Status != billing.OrderActive {
		t.Fatalf("activate: %v %+v", err, row)
	}
	fact, err := st.GetBillingSubscription(tenID)
	if err != nil || fact.SubscriptionRef != "sub_eco_1" || fact.Status != billing.SubStatusActive ||
		fact.PaidUntil != "2027-06-01T00:00:00Z" || fact.StoreLimit != 5 {
		t.Fatalf("subscription projection: %v %+v", err, fact)
	}
	limit, live, err := st.BillingStoreUsage(tenID)
	if err != nil || limit != 5 || live != 0 {
		t.Fatalf("store usage: %v %d %d", err, limit, live)
	}
}

func TestHardwareOrderAndFiveSteps(t *testing.T) {
	st, tenID := newBillingStore(t)
	items := []BillingHardwareItem{
		{SKU: "NFC-30", Name: "碰一碰 NFC 卡 30 张", Kind: "goods", Qty: 30, UnitAmountMinor: 500},
		{SKU: "SVC-INSTALL", Name: "现场写卡服务", Kind: "service", Qty: 1, UnitAmountMinor: 20000},
	}
	in := NewBillingOrder{
		TenantID: tenID, Kind: billing.KindHardware, Action: billing.ActionPurchase,
		AmountMinor: 35000, Currency: "CNY", IdempotencyKey: "hw-1",
		ActorPrincipal: "usr_owner", ActorRole: "org_owner",
	}
	row, _, err := st.CreateHardwareOrder(in, items, now())
	if err != nil {
		t.Fatalf("hardware order: %v", err)
	}
	got, err := st.ListHardwareItems(tenID, row.ID)
	if err != nil || len(got) != 2 {
		t.Fatalf("items: %v %+v", err, got)
	}
	names := map[string]bool{}
	for _, it := range got {
		names[it.Name] = true
	}
	if !names["碰一碰 NFC 卡 30 张"] || !names["现场写卡服务"] {
		t.Fatalf("item lines missing: %+v", got)
	}
	// 跳步拒绝:未付款不能出库
	if err := st.ApplyHardwareStepRetry(t, tenID, row.ID, billing.HardwareStepInput{
		Step: billing.StepShipment, Operator: "op1", OccurredAt: now()}); err == nil {
		t.Fatal("shipment before payment must fail")
	}
	// 支付事实(order 事件)同时落履约首步
	at := now()
	if _, _, err := st.ApplyBillingOrderEvent(tenID, row.ID, billing.AuthorityEventInput{
		Source: "eco-billing", EventID: "hw_pay", Type: billing.EventPaymentPaid,
		OccurredAt: at, Payload: billing.EventPayload{PaymentRef: "p_hw"},
	}, at); err != nil {
		t.Fatalf("hw pay: %v", err)
	}
	f, err := st.GetHardwareFulfillment(tenID, row.ID)
	if err != nil || f.Payment.State != billing.StepDone || f.Payment.Operator != "authority:eco-billing" {
		t.Fatalf("payment step: %v %+v", err, f)
	}
	// 出库→写卡→回读→验收
	for _, step := range []string{billing.StepShipment, billing.StepWriteCard, billing.StepReadback, billing.StepAcceptance} {
		if _, err := st.ApplyHardwareStep(tenID, row.ID, billing.HardwareStepInput{
			Step: step, Operator: "op1", OccurredAt: now(), DeviceRef: "dev_1"}, now()); err != nil {
			t.Fatalf("step %s: %v", step, err)
		}
	}
	f, err = st.GetHardwareFulfillment(tenID, row.ID)
	if err != nil || f.Acceptance.State != billing.StepDone {
		t.Fatalf("final fulfillment: %v %+v", err, f)
	}
}

// ApplyHardwareStepRetry unwraps the store call for the assertion above.
func (s *Store) ApplyHardwareStepRetry(t *testing.T, tenantID, orderID string, in billing.HardwareStepInput) error {
	t.Helper()
	_, err := s.ApplyHardwareStep(tenantID, orderID, in, now())
	return err
}

func TestRefundIdempotentAndCapped(t *testing.T) {
	st, tenID := newBillingStore(t)
	in := NewBillingOrder{
		TenantID: tenID, Kind: billing.KindSubscription, Action: billing.ActionNew,
		AmountMinor: 990000, Currency: "CNY", IdempotencyKey: "idem-r1",
		ActorPrincipal: "usr_owner", ActorRole: "org_owner",
	}
	row, _, err := st.CreateBillingOrder(in, now())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	refund := billing.RefundInput{
		RefundRef: "ref_1", AmountMinor: 400000, Currency: "CNY", Reason: "七天无理由",
		InvoiceTitle: "派诺商业管理有限公司", InvoiceStatus: billing.InvoicePending,
		Auditor: "auditor_wang", Source: "eco-billing", EventID: "re_ev_1", OccurredAt: now(),
	}
	got, replayed, err := st.ApplyBillingRefund(tenID, row.ID, refund, now())
	if err != nil || replayed || got.AmountMinor != 400000 {
		t.Fatalf("refund: %v replayed=%v %+v", err, replayed, got)
	}
	// 同事件重放:不重复退款
	_, replayed2, err := st.ApplyBillingRefund(tenID, row.ID, refund, now())
	if err != nil || !replayed2 {
		t.Fatalf("replay refund: %v replayed=%v", err, replayed2)
	}
	// 超余额拒绝(990000-400000=590000 < 600000)
	refund2 := refund
	refund2.RefundRef = "ref_2"
	refund2.EventID = "re_ev_2"
	refund2.AmountMinor = 600000
	if _, _, err := st.ApplyBillingRefund(tenID, row.ID, refund2, now()); !errors.Is(err, ErrBillingBadInput) && !errors.Is(err, billing.ErrRefundExceedsOrder) {
		t.Fatalf("cap = %v", err)
	}
	// 余额内第二笔退款成功,且总额守恒
	refund2.AmountMinor = 590000
	if _, _, err := st.ApplyBillingRefund(tenID, row.ID, refund2, now()); err != nil {
		t.Fatalf("second refund: %v", err)
	}
	rows, err := st.ListBillingRefunds(tenID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("refund list: %v %d", err, len(rows))
	}
}

func TestAIUsageProjectionAndQuoteSeam(t *testing.T) {
	st, tenID := newBillingStore(t)
	u := NewBillingAIUsage{
		TenantID: tenID, QuoteRef: "q_1", Capability: "content_production",
		Quantity: 1, AmountMinor: 800, Currency: "CNY", PricingVersion: "pv1",
		BusinessRef: "snap_1", OccurredAt: now(),
	}
	row, replayed, err := st.RecordBillingAIUsage(u, now())
	if err != nil || replayed || row.State != billing.QuoteStateConfirmed {
		t.Fatalf("record: %v replayed=%v %+v", err, replayed, row)
	}
	// 重试幂等:同 quote_ref 只有一行
	if _, replayed2, _ := st.RecordBillingAIUsage(u, now()); !replayed2 {
		t.Fatal("retry must replay")
	}
	// seam:取行→判定
	proj, err := st.QuoteProjectionFor(tenID, "q_1")
	if err != nil || proj == nil {
		t.Fatalf("projection: %v", err)
	}
	v := billing.AdjudicateQuoteRef(proj, billing.QuoteRefQuery{TenantID: tenID, QuoteRef: "q_1", RequireUnconsumed: true})
	if !v.Allowed {
		t.Fatalf("verdict: %+v", v)
	}
	// 消费:confirmed→consumed;二次消费拒绝
	if err := st.ConsumeQuoteProjection(tenID, "q_1", "h_1", now()); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if err := st.ConsumeQuoteProjection(tenID, "q_1", "h_2", now()); !errors.Is(err, ErrBillingQuoteNotConsumable) {
		t.Fatalf("double consume = %v", err)
	}
	// 未知引用 = nil 行(不泄漏)
	if proj, err := st.QuoteProjectionFor(tenID, "q_missing"); err != nil || proj != nil {
		t.Fatalf("missing: %v %+v", err, proj)
	}
}

func TestBillingOrderRejectsChangedIntent(t *testing.T) {
	st, tenant := newBillingStore(t)
	original := NewBillingOrder{TenantID: tenant, Kind: billing.KindSubscription, Action: billing.ActionRenew, PlanCode: billing.PlanMonthly, PriceRef: "price-1", PriceSnapshot: `{"reason":"续费","additional_stores":0}`, AmountMinor: 100, Currency: "CNY", IdempotencyKey: "same-intent", ActorPrincipal: "owner", ActorRole: "org_owner", PayerPrincipal: "payer", PayerGrantRef: "grant", AgencyRelationRef: "relation"}
	if _, _, err := st.CreateBillingOrder(original, now()); err != nil {
		t.Fatal(err)
	}
	changes := map[string]func(*NewBillingOrder){"actor": func(i *NewBillingOrder) { i.ActorPrincipal = "other" }, "role": func(i *NewBillingOrder) { i.ActorRole = "agent" }, "payer": func(i *NewBillingOrder) { i.PayerPrincipal = "other" }, "grant": func(i *NewBillingOrder) { i.PayerGrantRef = "other" }, "relation": func(i *NewBillingOrder) { i.AgencyRelationRef = "other" }, "action": func(i *NewBillingOrder) { i.Action = billing.ActionNew }, "plan": func(i *NewBillingOrder) { i.PlanCode = billing.PlanYearly }, "price": func(i *NewBillingOrder) { i.PriceRef = "other" }, "amount": func(i *NewBillingOrder) { i.AmountMinor++ }, "currency": func(i *NewBillingOrder) { i.Currency = "USD" }, "reason": func(i *NewBillingOrder) { i.PriceSnapshot = `{"reason":"不同","additional_stores":0}` }}
	changes["kind"] = func(i *NewBillingOrder) { i.Kind = billing.KindHardware }
	changes["additional_stores"] = func(i *NewBillingOrder) { i.PriceSnapshot = `{"reason":"续费","additional_stores":1}` }
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			in := original
			change(&in)
			_, _, err := st.CreateBillingOrder(in, now())
			if !errors.Is(err, ErrBillingIdempotencyConflict) {
				t.Fatalf("changed %s accepted: %v", name, err)
			}
		})
	}
}

func TestHardwareOrderAtomicAndItemIntent(t *testing.T) {
	st, tenant := newBillingStore(t)
	in := NewBillingOrder{TenantID: tenant, Kind: billing.KindHardware, Action: billing.ActionPurchase, AmountMinor: 200, Currency: "CNY", IdempotencyKey: "atomic-hardware", ActorPrincipal: "owner", ActorRole: "org_owner"}
	// Both lines validate, but the second insert fails its primary-key constraint.
	items := []BillingHardwareItem{{ID: "duplicate-item-id", SKU: "A", Name: "卡A", Kind: "goods", Qty: 1, UnitAmountMinor: 100}, {ID: "duplicate-item-id", SKU: "B", Name: "卡B", Kind: "goods", Qty: 1, UnitAmountMinor: 100}}
	if _, _, err := st.CreateHardwareOrder(in, items, now()); err == nil {
		t.Fatal("expected line insertion failure")
	}
	orders, err := st.ListBillingOrders(tenant, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 0 {
		t.Fatalf("failed hardware left partial order: %v", orders)
	}
	var lineCount int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM billing_hardware_items WHERE tenant_id=?`, tenant).Scan(&lineCount); err != nil || lineCount != 0 {
		t.Fatalf("failed transaction left lines=%d err=%v", lineCount, err)
	}
	items[0].ID = ""
	items[1].ID = ""
	first, replay, err := st.CreateHardwareOrder(in, items, now())
	if err != nil || replay {
		t.Fatalf("retry after rollback: %v %v", replay, err)
	}
	reversed := []BillingHardwareItem{items[1], items[0]}
	got, replay, err := st.CreateHardwareOrder(in, reversed, now())
	if err != nil || !replay || got.ID != first.ID {
		t.Fatalf("reordered same lines: %v %v", replay, err)
	}
	changed := append([]BillingHardwareItem(nil), items...)
	changed[0].SKU = "SAME-PRICE-OTHER-SKU"
	_, _, err = st.CreateHardwareOrder(in, changed, now())
	if !errors.Is(err, ErrBillingIdempotencyConflict) {
		t.Fatalf("same total different SKU accepted: %v", err)
	}
}

func TestHardwareSameIntentConcurrentRequests(t *testing.T) {
	st, tenant := newBillingStore(t)
	in := NewBillingOrder{TenantID: tenant, Kind: billing.KindHardware, Action: billing.ActionPurchase, AmountMinor: 100, Currency: "CNY", IdempotencyKey: "parallel-hardware", ActorPrincipal: "owner", ActorRole: "org_owner"}
	items := []BillingHardwareItem{{SKU: "A", Name: "前台卡", Kind: "goods", Qty: 1, UnitAmountMinor: 100}}
	type result struct {
		row    BillingOrderRow
		replay bool
		err    error
	}
	results := make(chan result, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			row, replay, err := st.CreateHardwareOrder(in, items, now())
			results <- result{row, replay, err}
		}()
	}
	wg.Wait()
	close(results)
	id := ""
	created := 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if id != "" && result.row.ID != id {
			t.Fatal("multiple orders for one key")
		}
		id = result.row.ID
		if !result.replay {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("created=%d", created)
	}
	lines, err := st.ListHardwareItems(tenant, id)
	if err != nil || len(lines) != 1 {
		t.Fatalf("partial or duplicate lines: %v %v", lines, err)
	}
}
