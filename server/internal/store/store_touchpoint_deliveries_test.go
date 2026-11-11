package store

// HUI-1663 实体交付 store 层测试(SQLite 真库)。
//   - 创建闸:触点本租户/门店归属/订单付款事实(4591 投影只读前置)/活交付唯一;
//   - 事件管线:七步不可跳跃(写卡前必须已出库+绑定);事实只追加;幂等重放
//     不推进两次;4591 投影同事务接线(shipment/write_card/readback/acceptance);
//   - 回读一致性:matches=false 只记事实不推进(错误域名/错误码不得当已验证);
//   - stale:换绑后 bound/实测绑定版本与触点当前版本漂移如实暴露;
//   - 补换:damaged_recoverable 同触点换载体保留逻辑入口;lost_uncontrolled
//     停用旧入口+新触点;审计行;参与/权益零触碰(结构保证)。

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/billing"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/delivery"
)

func dlSeed(t *testing.T) (*Store, string, string, string, string) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	s := New(d)
	ten, err := s.CreateTenant("交付测试租户")
	mustNoErr(t, err)
	sto, err := s.CreateStore(ten.ID, "交付店", "址", "u")
	mustNoErr(t, err)
	cID, links := seedCampaignWithLinks(t, s, ten.ID, "active", 1)
	tags, err := s.CreateTagsBatch(NewTagBatch{TenantID: ten.ID, CampaignID: cID,
		LinkIDs: []string{links[0].ID}, BindMode: "shared", Count: 1, StoreID: sto.ID, CreatedBy: "u"})
	mustNoErr(t, err)
	a, err := s.ProvisionTouchpoint(ten.ID, tags[0].ID, "前台立牌", "收银台", "u1")
	mustNoErr(t, err)
	orderID := dlSeedPaidOrder(t, s, ten.ID)
	return s, ten.ID, sto.ID, a.ID, orderID
}

// dlSeedPaidOrder = 4591 硬件订单 + 付款步事实(真实走 store 层投影)。
func dlSeedPaidOrder(t *testing.T, s *Store, tenID string) string {
	t.Helper()
	order, _, err := s.CreateHardwareOrder(NewBillingOrder{
		TenantID: tenID, Kind: billing.KindHardware, Action: billing.ActionPurchase,
		PriceSnapshot: `{"source":"merchant_declared"}`, AmountMinor: 4900, Currency: "CNY",
		IdempotencyKey: "idem-" + newID("k"),
	}, []BillingHardwareItem{{SKU: "SKU-CARD", Name: "碰一碰桌贴", Kind: "goods", Qty: 1, UnitAmountMinor: 4900}}, now())
	mustNoErr(t, err)
	if _, err := s.ApplyHardwareStep(tenID, order.ID, billing.HardwareStepInput{
		Step: billing.StepPayment, Operator: "authority:test", OccurredAt: now(),
	}, now()); err != nil {
		t.Fatalf("seed payment: %v", err)
	}
	return order.ID
}

func dlCreate(t *testing.T, s *Store, tenID, tpID, orderID string) TouchpointDelivery {
	t.Helper()
	d, err := s.CreateTouchpointDelivery(NewTouchpointDelivery{
		TenantID: tenID, TouchpointID: tpID, HardwareOrderID: orderID,
		PhysicalUnitRef: "unit-001", TagBatch: "batch-A", CreatedBy: "u1",
	})
	mustNoErr(t, err)
	return d
}

func dlEvent(t *testing.T, s *Store, tenID, dlvID, eventType, eventID string, mutate func(*DeliveryEventInput)) DeliveryEventResult {
	t.Helper()
	in := DeliveryEventInput{
		TenantID: tenID, DeliveryID: dlvID, EventType: eventType,
		Source: "merchant", EventID: eventID, OperatorRef: "usr_owner_a", DeviceRef: "dev-" + eventID,
	}
	if mutate != nil {
		mutate(&in)
	}
	res, err := s.ApplyTouchpointDeliveryEvent(in, now())
	mustNoErr(t, err)
	return res
}

func TestTouchpointDeliveryCreateGates(t *testing.T) {
	s, tenID, stoID, tpID, orderID := dlSeed(t)

	d := dlCreate(t, s, tenID, tpID, orderID)
	if d.Status != delivery.StatusAwaitingDispatch {
		t.Fatalf("fresh status = %q", d.Status)
	}
	if d.StoreID != stoID || d.TouchpointID != tpID {
		t.Fatalf("derived facts = %+v", d)
	}

	// 跨租户触点掩码
	if _, err := s.CreateTouchpointDelivery(NewTouchpointDelivery{
		TenantID: "ten_other", TouchpointID: tpID, HardwareOrderID: orderID,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant = %v, want ErrNotFound", err)
	}
	// 订单不存在(掩码 404 语义由 handler 呈现,store 层 ErrNotFound)
	if _, err := s.CreateTouchpointDelivery(NewTouchpointDelivery{
		TenantID: tenID, TouchpointID: tpID, HardwareOrderID: "ord_missing",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing order = %v, want ErrNotFound", err)
	}
	// 非硬件订单(同租户订阅单 → 类型闸;跨租户订单已被上面掩码闸覆盖)
	subOrder, _, err := s.CreateBillingOrder(NewBillingOrder{
		TenantID: tenID, Kind: billing.KindSubscription, Action: billing.ActionNew,
		PlanCode: billing.PlanMonthly, PriceRef: "p", PriceSnapshot: "{}", AmountMinor: 1,
		Currency: "CNY", IdempotencyKey: "sub-ten-a",
	}, now())
	mustNoErr(t, err)
	if _, err := s.CreateTouchpointDelivery(NewTouchpointDelivery{
		TenantID: tenID, TouchpointID: tpID, HardwareOrderID: subOrder.ID,
	}); !errors.Is(err, ErrDeliveryBadInput) {
		t.Fatalf("subscription order = %v, want ErrDeliveryBadInput", err)
	}
	// 付款事实缺失(4591 投影只读前置)
	order2 := dlSeedUnpaidOrder(t, s, tenID)
	if _, err := s.CreateTouchpointDelivery(NewTouchpointDelivery{
		TenantID: tenID, TouchpointID: tpID, HardwareOrderID: order2,
	}); !errors.Is(err, ErrPaymentRequired) {
		t.Fatalf("unpaid = %v, want ErrPaymentRequired", err)
	}
	// 活交付唯一(库层部分唯一索引兜底)
	if _, err := s.CreateTouchpointDelivery(NewTouchpointDelivery{
		TenantID: tenID, TouchpointID: tpID, HardwareOrderID: orderID,
	}); !errors.Is(err, ErrLiveDeliveryExists) {
		t.Fatalf("duplicate live = %v, want ErrLiveDeliveryExists", err)
	}
}

func dlSeedUnpaidOrder(t *testing.T, s *Store, tenID string) string {
	t.Helper()
	order, _, err := s.CreateHardwareOrder(NewBillingOrder{
		TenantID: tenID, Kind: billing.KindHardware, Action: billing.ActionPurchase,
		PriceSnapshot: `{}`, AmountMinor: 100, Currency: "CNY", IdempotencyKey: "idem-" + newID("k"),
	}, []BillingHardwareItem{{SKU: "S", Name: "n", Kind: "goods", Qty: 1, UnitAmountMinor: 100}}, now())
	mustNoErr(t, err)
	return order.ID
}

func TestTouchpointDeliveryEventPipelineAndProjection(t *testing.T) {
	s, tenID, _, tpID, orderID := dlSeed(t)
	d := dlCreate(t, s, tenID, tpID, orderID)

	// 跳步:初始态直接写卡/交付 → 拒绝
	if _, err := s.ApplyTouchpointDeliveryEvent(DeliveryEventInput{
		TenantID: tenID, DeliveryID: d.ID, EventType: delivery.StepWritten,
		Source: "merchant", EventID: "e-skip", OperatorRef: "u", OccurredAt: now(),
	}, now()); !errors.Is(err, delivery.ErrStepSkipped) {
		t.Fatalf("skip write = %v, want ErrStepSkipped", err)
	}

	// 完整管线:出库→绑定→写卡→回读(一致)→验收→交付
	dlEvent(t, s, tenID, d.ID, delivery.StepDispatch, "e1", nil)
	dlEvent(t, s, tenID, d.ID, delivery.StepBound, "e2", nil)
	dlEvent(t, s, tenID, d.ID, delivery.StepWritten, "e3", nil)

	tp, err := s.GetTouchpointAlias(tpID, tenID)
	mustNoErr(t, err)
	res := dlEvent(t, s, tenID, d.ID, delivery.StepReadback, "e4", func(in *DeliveryEventInput) {
		in.ReadURL = "https://h5.test/t/" + tp.Code
		in.ExpectedURL = "https://h5.test/t/" + tp.Code
	})
	if !res.ReadbackMatches || !res.Advanced {
		t.Fatalf("readback = %+v, want matches+advance", res)
	}
	dlEvent(t, s, tenID, d.ID, delivery.StepAccepted, "e5", nil)
	res = dlEvent(t, s, tenID, d.ID, delivery.StepDelivered, "e6", nil)
	if res.Delivery.Status != delivery.StepDelivered {
		t.Fatalf("final status = %q", res.Delivery.Status)
	}

	// 4591 投影接线断言:四步同事务落位(操作者/设备与交付事实一致)。
	f, err := s.GetHardwareFulfillment(tenID, orderID)
	mustNoErr(t, err)
	projWant := map[string]struct {
		state  string
		device string
	}{
		"shipment":   {billing.StepDone, "dev-e1"},
		"write_card": {billing.StepDone, "dev-e3"},
		"readback":   {billing.StepDone, "dev-e4"},
		"acceptance": {billing.StepDone, "dev-e5"},
	}
	projGot := map[string]billing.HardwareStep{
		"shipment": f.Shipment, "write_card": f.WriteCard,
		"readback": f.Readback, "acceptance": f.Acceptance,
	}
	for name, want := range projWant {
		got := projGot[name]
		if got.State != want.state || got.DeviceRef != want.device {
			t.Fatalf("projection %s = %+v, want state=%s device=%s", name, got, want.state, want.device)
		}
	}

	// 绑定与实测版本被记录
	dv, err := s.GetTouchpointDeliveryView(tenID, d.ID)
	mustNoErr(t, err)
	if dv.BoundBindingRevision != tp.BindingRevision || dv.VerifiedBindingRevision != tp.BindingRevision {
		t.Fatalf("revisions bound=%d verified=%d want %d", dv.BoundBindingRevision, dv.VerifiedBindingRevision, tp.BindingRevision)
	}
	if dv.BindingStale {
		t.Fatalf("fresh delivery must not be stale")
	}
	if dv.Steps[delivery.StepDispatch].OperatorRef != "usr_owner_a" {
		t.Fatalf("steps = %+v", dv.Steps)
	}
	if dv.Payment.State != billing.StepDone {
		t.Fatalf("payment projection = %+v", dv.Payment)
	}

	// 终态拒写
	if _, err := s.ApplyTouchpointDeliveryEvent(DeliveryEventInput{
		TenantID: tenID, DeliveryID: d.ID, EventType: delivery.StepDispatch,
		Source: "merchant", EventID: "e-after", OperatorRef: "u", OccurredAt: now(),
	}, now()); !errors.Is(err, delivery.ErrDeliveryClosed) {
		t.Fatalf("post-delivery write = %v, want ErrDeliveryClosed", err)
	}
}

func TestTouchpointDeliveryEventIdempotencyNoDoublePush(t *testing.T) {
	s, tenID, _, tpID, orderID := dlSeed(t)
	d := dlCreate(t, s, tenID, tpID, orderID)

	in := DeliveryEventInput{
		TenantID: tenID, DeliveryID: d.ID, EventType: delivery.StepDispatch,
		Source: "merchant", EventID: "evt-1", OperatorRef: "usr_owner_a",
		DeviceRef: "dev-1", OccurredAt: "2026-11-11T00:00:00Z",
	}
	if _, err := s.ApplyTouchpointDeliveryEvent(in, now()); err != nil {
		t.Fatalf("first: %v", err)
	}
	// 同键同负载 → 幂等读回,状态不推进两次,4591 投影不重复写。
	res, err := s.ApplyTouchpointDeliveryEvent(in, now())
	mustNoErr(t, err)
	if !res.Replayed || res.Advanced {
		t.Fatalf("replay = %+v, want replayed without advance", res)
	}
	// 同键异负载 → IDEMPOTENCY_CONFLICT
	in.DeviceRef = "dev-other"
	if _, err := s.ApplyTouchpointDeliveryEvent(in, now()); !errors.Is(err, ErrDeliveryIdempotencyConflict) {
		t.Fatalf("conflict = %v, want ErrDeliveryIdempotencyConflict", err)
	}
	// 投影行只有一条 shipment 事实(操作者/设备仍是首次事实)。
	f, err := s.GetHardwareFulfillment(tenID, orderID)
	mustNoErr(t, err)
	if f.Shipment.State != billing.StepDone || f.Shipment.DeviceRef != "dev-1" {
		t.Fatalf("shipment after replay = %+v", f.Shipment)
	}
	// 同负载不同 event_id 但步已完成 → 步冲突(事实不覆盖)
	in2 := in
	in2.EventID = "evt-2"
	if _, err := s.ApplyTouchpointDeliveryEvent(in2, now()); !errors.Is(err, delivery.ErrStepSkipped) {
		t.Fatalf("re-fact same step = %v, want ErrStepSkipped", err)
	}
}

func TestTouchpointDeliveryReadbackMismatch(t *testing.T) {
	s, tenID, _, tpID, orderID := dlSeed(t)
	d := dlCreate(t, s, tenID, tpID, orderID)
	tp, err := s.GetTouchpointAlias(tpID, tenID)
	mustNoErr(t, err)

	dlEvent(t, s, tenID, d.ID, delivery.StepDispatch, "r1", nil)
	dlEvent(t, s, tenID, d.ID, delivery.StepBound, "r2", nil)
	dlEvent(t, s, tenID, d.ID, delivery.StepWritten, "r3", nil)

	// 错误域名回读:只记事实,不推进,不推 4591 readback。
	res := dlEvent(t, s, tenID, d.ID, delivery.StepReadback, "r4-bad", func(in *DeliveryEventInput) {
		in.ReadURL = "https://evil.example.com/t/" + tp.Code
		in.ExpectedURL = "https://h5.test/t/" + tp.Code
	})
	if res.ReadbackMatches || res.Advanced {
		t.Fatalf("host mismatch must not match/advance: %+v", res)
	}
	cur, err := s.GetTouchpointDelivery(tenID, d.ID)
	mustNoErr(t, err)
	if cur.Status != delivery.StepWritten {
		t.Fatalf("status after bad readback = %q, want written", cur.Status)
	}
	f, err := s.GetHardwareFulfillment(tenID, orderID)
	mustNoErr(t, err)
	if f.Readback.State == billing.StepDone {
		t.Fatalf("bad readback must not push projection")
	}
	// 失败事实已入账本
	evs, err := s.ListTouchpointDeliveryEvents(tenID, d.ID)
	mustNoErr(t, err)
	foundBad := false
	for _, e := range evs {
		if e.EventType == delivery.StepReadback && e.EventID == "r4-bad" {
			foundBad = true
		}
	}
	if !foundBad {
		t.Fatalf("bad readback fact missing from ledger: %+v", evs)
	}

	// 正确回读(新 event_id)→ 推进+投影落位。
	res = dlEvent(t, s, tenID, d.ID, delivery.StepReadback, "r4-ok", func(in *DeliveryEventInput) {
		in.ReadURL = "https://h5.test/t/" + tp.Code
		in.ExpectedURL = "https://h5.test/t/" + tp.Code
	})
	if !res.ReadbackMatches || !res.Advanced {
		t.Fatalf("good readback = %+v", res)
	}
	f, err = s.GetHardwareFulfillment(tenID, orderID)
	mustNoErr(t, err)
	if f.Readback.State != billing.StepDone {
		t.Fatalf("good readback projection = %+v", f.Readback)
	}
}

func TestTouchpointDeliveryBindingStaleAfterRebind(t *testing.T) {
	s, tenID, stoID, tpID, orderID := dlSeed(t)
	d := dlCreate(t, s, tenID, tpID, orderID)
	dlEvent(t, s, tenID, d.ID, delivery.StepDispatch, "s1", nil)
	dlEvent(t, s, tenID, d.ID, delivery.StepBound, "s2", nil)

	// 换绑到另一门店目标(改店=改目标)→ 当前绑定版本漂移,stale 如实暴露。
	cID2, links2 := seedCampaignWithLinks(t, s, tenID, "active", 1)
	tags2, err := s.CreateTagsBatch(NewTagBatch{TenantID: tenID, CampaignID: cID2,
		LinkIDs: []string{links2[0].ID}, BindMode: "shared", Count: 1, StoreID: stoID, CreatedBy: "u"})
	mustNoErr(t, err)
	tp2, err := s.ProvisionTouchpoint(tenID, tags2[0].ID, "二店卡", "", "u")
	mustNoErr(t, err)
	_, err = s.RebindTouchpointTarget(tpID, tenID, TouchpointTarget{Type: TargetStore, StoreID: stoID},
		TouchpointRebindExpectation{Revision: 1}, "u")
	// 同店同型目标会被 sameTarget 拒绝;这里换的是活动注记 → 用 legacy 目标验证漂移即可。
	if err != nil {
		// 退化路径:换绑到 legacy campaign 目标(同为改目标)。
		_, err = s.RebindTouchpointTarget(tpID, tenID, TouchpointTarget{Type: TargetLegacyCampaign, CampaignID: cID2},
			TouchpointRebindExpectation{Revision: 1}, "u")
		mustNoErr(t, err)
	}
	_ = tp2

	dv, err := s.GetTouchpointDeliveryView(tenID, d.ID)
	mustNoErr(t, err)
	if !dv.BindingStale {
		t.Fatalf("after rebind the delivery must be stale: %+v", dv)
	}
	if dv.CurrentBindingRevision == dv.BoundBindingRevision {
		t.Fatalf("current revision must differ from bound revision: %+v", dv)
	}
}

func TestTouchpointDeliveryReplaceRecoverable(t *testing.T) {
	s, tenID, _, tpID, orderID := dlSeed(t)
	d := dlCreate(t, s, tenID, tpID, orderID)
	dlEvent(t, s, tenID, d.ID, delivery.StepDispatch, "p1", nil)

	// damaged_recoverable:同触点换载体,保留逻辑入口。
	rep, err := s.ReplaceTouchpointDelivery(ReplaceDeliveryInput{
		TenantID: tenID, OldDeliveryID: d.ID, NewTouchpointID: tpID,
		PhysicalUnitRef: "unit-002", TagBatch: "batch-B",
		ReasonCategory: "damaged_recoverable", ReasonDetail: "卡面破损", CreatedBy: "u1",
	})
	mustNoErr(t, err)
	if rep.NewDelivery.ReplacementOf != d.ID || rep.NewDelivery.Status != delivery.StatusAwaitingDispatch {
		t.Fatalf("replacement = %+v", rep)
	}
	if rep.Audit.OldEntryRevoked != 0 {
		t.Fatalf("recoverable must not revoke entry: %+v", rep.Audit)
	}
	old, err := s.GetTouchpointDelivery(tenID, d.ID)
	mustNoErr(t, err)
	if old.Status != delivery.StatusSuperseded {
		t.Fatalf("old status = %q, want superseded", old.Status)
	}
	tp, err := s.GetTouchpointAlias(tpID, tenID)
	mustNoErr(t, err)
	if tp.Status != TagStatusActive {
		t.Fatalf("logical entry must stay active: %+v", tp)
	}
	// 审计行
	audits, err := s.ListTouchpointDeliveryReplacements(tenID, d.ID)
	mustNoErr(t, err)
	if len(audits) != 1 || audits[0].ReasonCategory != "damaged_recoverable" {
		t.Fatalf("audits = %+v", audits)
	}
	// 旧交付封存后拒写任何事实
	if _, err := s.ApplyTouchpointDeliveryEvent(DeliveryEventInput{
		TenantID: tenID, DeliveryID: d.ID, EventType: delivery.StepBound,
		Source: "merchant", EventID: "p-x", OperatorRef: "u", OccurredAt: now(),
	}, now()); !errors.Is(err, delivery.ErrDeliveryClosed) {
		t.Fatalf("superseded write = %v, want ErrDeliveryClosed", err)
	}
	// 新交付独立重走全流程(重新写入/回读/实测——结构保证)
	dlEvent(t, s, tenID, rep.NewDelivery.ID, delivery.StepDispatch, "p2", nil)
	if _, err := s.GetTouchpointDelivery(tenID, rep.NewDelivery.ID); err != nil {
		t.Fatalf("new delivery lookup: %v", err)
	}
}

func TestTouchpointDeliveryReplaceLostRevokesEntry(t *testing.T) {
	s, tenID, stoID, tpID, orderID := dlSeed(t)
	d := dlCreate(t, s, tenID, tpID, orderID)

	// 新触点(第二张卡)作为遗失补换目标
	cID, links := seedCampaignWithLinks(t, s, tenID, "active", 1)
	tags, err := s.CreateTagsBatch(NewTagBatch{TenantID: tenID, CampaignID: cID,
		LinkIDs: []string{links[0].ID}, BindMode: "shared", Count: 1, StoreID: stoID, CreatedBy: "u"})
	mustNoErr(t, err)
	tp2, err := s.ProvisionTouchpoint(tenID, tags[0].ID, "补换新卡", "", "u")
	mustNoErr(t, err)

	// lost_uncontrolled + 同触点 = 坏输入(必须换新入口)
	if _, err := s.ReplaceTouchpointDelivery(ReplaceDeliveryInput{
		TenantID: tenID, OldDeliveryID: d.ID, NewTouchpointID: tpID,
		ReasonCategory: "lost_uncontrolled", CreatedBy: "u1",
	}); !errors.Is(err, ErrReplacementBadInput) {
		t.Fatalf("lost same tp = %v, want ErrReplacementBadInput", err)
	}
	// damaged_recoverable + 异触点 = 坏输入(必须保留逻辑入口)
	if _, err := s.ReplaceTouchpointDelivery(ReplaceDeliveryInput{
		TenantID: tenID, OldDeliveryID: d.ID, NewTouchpointID: tp2.ID,
		ReasonCategory: "damaged_recoverable", CreatedBy: "u1",
	}); !errors.Is(err, ErrReplacementBadInput) {
		t.Fatalf("recoverable other tp = %v, want ErrReplacementBadInput", err)
	}

	rep, err := s.ReplaceTouchpointDelivery(ReplaceDeliveryInput{
		TenantID: tenID, OldDeliveryID: d.ID, NewTouchpointID: tp2.ID,
		PhysicalUnitRef: "unit-003", ReasonCategory: "lost_uncontrolled",
		ReasonDetail: "卡片遗失", CreatedBy: "u1",
	})
	mustNoErr(t, err)
	if rep.Audit.OldEntryRevoked != 1 {
		t.Fatalf("lost must revoke old entry: %+v", rep.Audit)
	}
	// 旧入口已停用(遗失卡不能继续成为有效入口)
	oldTp, err := s.GetTouchpointAlias(tpID, tenID)
	mustNoErr(t, err)
	if oldTp.Status != TagStatusDisabled {
		t.Fatalf("old entry status = %q, want disabled", oldTp.Status)
	}
	// 公共解析对新入口照常、旧入口停用(撤销即时可见)
	res := s.ResolveTouchpoint(oldTp.Code, time.Now())
	if res.Outcome != TpDisabled {
		t.Fatalf("old entry resolve = %v, want touchpoint_disabled", res.Outcome)
	}
	res = s.ResolveTouchpoint(tp2.Code, time.Now())
	if res.Outcome != TpRedirect || res.TargetType != TargetStore {
		t.Fatalf("new entry resolve = %+v", res)
	}
	// 参与快照零触碰的声明性检查:交付域没有触碰 participations 的路径(结构保证)。

	// 已补换的旧交付再次补换 → 拒(封存)
	if _, err := s.ReplaceTouchpointDelivery(ReplaceDeliveryInput{
		TenantID: tenID, OldDeliveryID: d.ID, NewTouchpointID: tp2.ID,
		ReasonCategory: "damaged_recoverable", CreatedBy: "u1",
	}); !errors.Is(err, delivery.ErrDeliveryClosed) {
		t.Fatalf("re-replace = %v, want ErrDeliveryClosed", err)
	}
}

func TestTouchpointDeliveryReplaceInheritsOrder(t *testing.T) {
	s, tenID, _, tpID, orderID := dlSeed(t)
	d := dlCreate(t, s, tenID, tpID, orderID)
	rep, err := s.ReplaceTouchpointDelivery(ReplaceDeliveryInput{
		TenantID: tenID, OldDeliveryID: d.ID, NewTouchpointID: tpID,
		PhysicalUnitRef: "unit-004", ReasonCategory: "damaged_recoverable", CreatedBy: "u1",
	})
	mustNoErr(t, err)
	if rep.NewDelivery.HardwareOrderID != orderID {
		t.Fatalf("inherit order = %q, want %q", rep.NewDelivery.HardwareOrderID, orderID)
	}
}
