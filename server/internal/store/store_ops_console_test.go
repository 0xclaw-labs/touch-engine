package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/billing"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/ops"
)

func newOpsStore(t *testing.T) *Store {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return New(d)
}

func TestOpsAuditAppendOnlyAndList(t *testing.T) {
	st := newOpsStore(t)
	e, err := st.AppendOpsAudit(OpsAuditEntry{
		ActorRef: "usr_ops", Action: "tenant.suspend", TargetType: "tenant", TargetID: "tnt_1",
		TenantID: "tnt_1", BeforeJSON: `"active"`, AfterJSON: `"suspended"`, Reason: "违规风险",
	})
	mustNoErr(t, err)
	if e.ID == "" || e.OccurredAt == "" {
		t.Fatal("audit row must be complete")
	}
	rows, err := st.ListOpsAudit(OpsAuditFilter{TargetID: "tnt_1"})
	mustNoErr(t, err)
	if len(rows) != 1 || rows[0].Reason != "违规风险" || rows[0].BeforeJSON != `"active"` {
		t.Fatalf("audit read-back = %+v", rows)
	}
}

func TestTenantRevisionGuard(t *testing.T) {
	st := newOpsStore(t)
	ten, err := st.CreateTenant("商家A")
	mustNoErr(t, err)
	rev, err := st.TenantRevision(ten.ID)
	mustNoErr(t, err)
	if rev != 1 {
		t.Fatalf("initial revision = %d", rev)
	}
	// 正确 revision → 生命周期落库且 revision 前进。
	rev2, err := st.SetTenantLifecycleOps(ten.ID, "suspended", rev)
	mustNoErr(t, err)
	if rev2 != rev+1 {
		t.Fatalf("revision after suspend = %d", rev2)
	}
	got, err := st.GetTenant(ten.ID)
	mustNoErr(t, err)
	if got.Lifecycle != "suspended" {
		t.Fatalf("lifecycle = %q", got.Lifecycle)
	}
	// 过期 revision → 409 语义(ErrOpsRevisionConflict),状态不被覆盖。
	if _, err := st.SetTenantLifecycleOps(ten.ID, "active", rev); err != ErrOpsRevisionConflict {
		t.Fatalf("stale revision = %v", err)
	}
	got, _ = st.GetTenant(ten.ID)
	if got.Lifecycle != "suspended" {
		t.Fatal("stale request must not overwrite new state")
	}
	// charge hold 同守卫。
	if _, err := st.SetChargeHoldOps(ten.ID, true, rev2); err != nil {
		t.Fatalf("charge hold = %v", err)
	}
	if _, err := st.SetChargeHoldOps(ten.ID, false, rev2); err != ErrOpsRevisionConflict {
		t.Fatalf("stale charge hold = %v", err)
	}
	// 非法 lifecycle 值拒绝(2053 词表)。
	if _, err := st.SetTenantLifecycleOps(ten.ID, "wifi_off", rev2+1); err == nil {
		t.Fatal("invalid lifecycle rejected")
	}
}

func TestSupportAccessLifecycle(t *testing.T) {
	st := newOpsStore(t)
	ten, _ := st.CreateTenant("商家B")
	nowT := time.Now()
	a, err := st.CreateSupportAccess(ten.ID, "usr_ops", "排查商户工单", 90*time.Minute, nowT)
	mustNoErr(t, err)
	if a.ExpiresAt == "" {
		t.Fatal("support access must carry expiry")
	}
	live, err := st.ActiveSupportAccess("usr_ops", ten.ID, nowT.Add(time.Minute))
	mustNoErr(t, err)
	if live.ID != a.ID {
		t.Fatalf("active = %+v", live)
	}
	// 到期立即停止。
	if _, err := st.ActiveSupportAccess("usr_ops", ten.ID, nowT.Add(2*time.Hour)); err != ErrNotFound {
		t.Fatalf("expired access = %v", err)
	}
	// 关闭 = 墓碑;二次关闭幂等读回。
	closed, err := st.CloseSupportAccess(a.ID, "usr_ops", "排查完成", nowT.Add(10*time.Minute))
	mustNoErr(t, err)
	if closed.ClosedAt == "" || closed.CloseReason != "排查完成" {
		t.Fatalf("closed = %+v", closed)
	}
	again, err := st.CloseSupportAccess(a.ID, "usr_ops", "重复关闭", nowT.Add(11*time.Minute))
	mustNoErr(t, err)
	if again.CloseReason != "排查完成" {
		t.Fatal("double close must be a no-op read-back")
	}
	if _, err := st.ActiveSupportAccess("usr_ops", ten.ID, nowT.Add(time.Minute)); err != ErrNotFound {
		t.Fatal("closed access must stop immediately")
	}
	// TTL 超上限/无原因拒绝。
	if _, err := st.CreateSupportAccess(ten.ID, "usr_ops", "x", 24*time.Hour, nowT); err == nil {
		t.Fatal("TTL above cap rejected")
	}
	if _, err := st.CreateSupportAccess(ten.ID, "usr_ops", "", time.Minute, nowT); err == nil {
		t.Fatal("missing reason rejected")
	}
}

func TestOpsOperationEnvelopeStorage(t *testing.T) {
	st := newOpsStore(t)
	op, err := st.RecordOpsOperation(OpsOperation{
		IdempotencyKey: "k1", Action: ops.ActionTenantSuspend, ResourceRef: "tnt_x",
		ExpectedRevision: "1", Reason: "风险停用", ActorRef: "usr_ops", AuthoritySource: "og_1",
	})
	mustNoErr(t, err)
	if op.State != "running" || op.ID == "" {
		t.Fatalf("recorded = %+v", op)
	}
	// 同键读回(重试查询同一操作)。
	got, err := st.GetOpsOperationByIdempotencyKey("k1")
	mustNoErr(t, err)
	if got.ID != op.ID {
		t.Fatalf("read-back = %+v", got)
	}
	// 完成 → 终态可追踪。
	done, err := st.FinishOpsOperation(op.ID, "succeeded", "", `{"revision":2}`)
	mustNoErr(t, err)
	if done.State != "succeeded" || done.FinishedAt == "" {
		t.Fatalf("finished = %+v", done)
	}
	// 列表。
	rows, err := st.ListOpsOperations(10)
	mustNoErr(t, err)
	if len(rows) != 1 {
		t.Fatalf("list = %d", len(rows))
	}
}

func TestCatalogAndOrderProjections(t *testing.T) {
	st := newOpsStore(t)
	ten, _ := st.CreateTenant("商家C")
	_, err := st.UpsertBillingCatalogEntry(billing.CatalogEntry{
		ScopeTenant: "", PlanCode: billing.PlanMonthly, PricingVersion: "v1",
		PriceRef: "p1", Currency: "CNY", PriceMinor: 990, ServiceDays: 30,
	}, time.Now().UTC().Format(time.RFC3339))
	mustNoErr(t, err)
	all, err := st.ListCatalogAll()
	mustNoErr(t, err)
	if len(all) != 1 || all[0].PriceMinor != 990 {
		t.Fatalf("catalog all = %+v", all)
	}
	row, err := st.GetCatalogEntryByID(all[0].ID)
	mustNoErr(t, err)
	if row.PlanCode != billing.PlanMonthly {
		t.Fatalf("by id = %+v", row)
	}
	if _, err := st.GetCatalogEntryByID("nope"); err != ErrNotFound {
		t.Fatalf("missing entry = %v", err)
	}
	_ = ten
	orders, err := st.ListOrdersAll("", 10)
	mustNoErr(t, err)
	if len(orders) != 0 {
		t.Fatalf("empty tenant must list zero orders, got %d", len(orders))
	}
}

func TestTicketAndDrillStorage(t *testing.T) {
	st := newOpsStore(t)
	nowT := time.Now()
	tk, err := st.CreateOpsTicket("", "domain_probe", "high", "媒体域名 403", "探针 degraded", "usr_ops", "probe", "usr_ops", nowT)
	mustNoErr(t, err)
	if tk.Status != ops.TicketOpen {
		t.Fatalf("new ticket = %+v", tk)
	}
	// 状态机走全:open→confirmed→resolved→closed。
	_, err = st.TransitionOpsTicket(tk.ID, ops.TicketResolved, "说明", "", "usr_ops", nowT)
	if err != ops.ErrTicketTransition {
		t.Fatalf("skip confirm = %v", err)
	}
	_, err = st.TransitionOpsTicket(tk.ID, ops.TicketConfirmed, "", "", "usr_ops", nowT)
	mustNoErr(t, err)
	_, err = st.TransitionOpsTicket(tk.ID, ops.TicketResolved, "已切换媒体源,探针恢复", "", "usr_ops", nowT)
	mustNoErr(t, err)
	_, err = st.TransitionOpsTicket(tk.ID, ops.TicketClosed, "", "恢复确认", "usr_ops", nowT)
	mustNoErr(t, err)
	got, _ := st.GetOpsTicket(tk.ID)
	if got.Status != ops.TicketClosed || got.ClosedReason != "恢复确认" {
		t.Fatalf("closed = %+v", got)
	}
	// 演练:restore 缺报告在存储层就拒绝。
	if _, err := st.CreateOpsDrill(ops.DrillRestore, "db主库", "ok", "", "usr_ops", "", nowT); err == nil {
		t.Fatal("restore without report rejected")
	}
	d, err := st.CreateOpsDrill(ops.DrillRestore, "db主库", "ok", "回读校验 5/5 表校验和一致", "usr_ops", "季度演练", nowT)
	mustNoErr(t, err)
	if d.VerificationReport == "" {
		t.Fatal("report persisted")
	}
	drills, err := st.ListOpsDrills(10)
	mustNoErr(t, err)
	if len(drills) != 1 {
		t.Fatalf("drills = %d", len(drills))
	}
}
