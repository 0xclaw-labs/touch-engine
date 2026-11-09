package store

// store_content_production_test.go: HUI-4595 门店真实制作交接持久层验收。
//
// 覆盖:
//   - 发起登记幂等:同租户同 idempotency_key 重放返回同一行(replayed),
//     不同负载的同行不产生第二行;跨租户/跨店包拒绝;空包合法(T05);
//   - 受信回流登记:produced 落候选版本引用 + status→produced;
//     source_app+event_id 同键同摘要重放返回同一行(不新增);同键异负载
//     →ErrIdempotencyConflict;revision 水位(旧稿迟到)→ErrStaleResult;
//     canceled 终态拒新回流→ErrHandoffCanceled;task_ref 不符→ErrTaskRefMismatch;
//   - 失败/取消回执独立成行且驱动交接状态;重放不受终态影响;
//   - 查询面:租户作用域列表;GetProductionHandoffByID 无租户作用域(受信接缝),
//     未知统一 ErrNotFound。

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/db"
)

func newProductionStore(t *testing.T) *Store {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return New(d)
}

func seedProductionTenants(t *testing.T, s *Store) (string, string, string, string) {
	t.Helper()
	tenA, err := s.CreateTenant("制作商家A")
	if err != nil {
		t.Fatal(err)
	}
	tenB, err := s.CreateTenant("别家商家B")
	if err != nil {
		t.Fatal(err)
	}
	stA, err := s.CreateStore(tenA.ID, "制作旗舰店", "地址", "test")
	if err != nil {
		t.Fatal(err)
	}
	stB, err := s.CreateStore(tenB.ID, "别家门店", "地址二", "test")
	if err != nil {
		t.Fatal(err)
	}
	return tenA.ID, tenB.ID, stA.ID, stB.ID
}

func productionInput(storeID, packID string) ProductionHandoffInput {
	return ProductionHandoffInput{
		StoreID:           storeID,
		PackID:            packID,
		ContentBindingRef: "",
		QuoteRef:          "quote_confirmed_1",
		QuoteVerified:     true,
		Note:              "首次制作",
		IdempotencyKey:    "handoff-key-1",
	}
}

func TestCreateProductionHandoffIdempotentAndEmptyPackLegal(t *testing.T) {
	s := newProductionStore(t)
	tenA, tenB, stA, stB := seedProductionTenants(t, s)

	// 空包(零版本/零采用)合法:首次制作路径(T05)。
	pack, err := s.CreateStoreContentPack(tenA, stA, "", "空包首作", "usr_owner_a")
	if err != nil {
		t.Fatal(err)
	}
	h, replayed, err := s.CreateProductionHandoff(tenA, productionInput(stA, pack.ID), "usr_owner_a")
	if err != nil || replayed {
		t.Fatalf("create: replayed=%v err=%v", replayed, err)
	}
	if h.Status != ProductionStatusRecorded || h.QuoteVerified != true || h.PackID != pack.ID {
		t.Fatalf("unexpected handoff: %+v", h)
	}

	// 同键重放:同一行,不新建制作工程。
	h2, replayed2, err := s.CreateProductionHandoff(tenA, productionInput(stA, pack.ID), "usr_owner_a")
	if err != nil || !replayed2 {
		t.Fatalf("replay: replayed=%v err=%v", replayed2, err)
	}
	if h2.ID != h.ID {
		t.Fatalf("replay returned different handoff %s != %s", h2.ID, h.ID)
	}

	// 同键不同负载:拒绝(SHARED-BRIEF 幂等语义:同键异负载=IDEMPOTENCY_CONFLICT),
	// 绝不产生第二行。
	alt := productionInput(stA, pack.ID)
	alt.Note = "别的要求"
	if _, _, err := s.CreateProductionHandoff(tenA, alt, "usr_owner_a"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("alt payload: want ErrIdempotencyConflict, got %v", err)
	}
	altPack := productionInput(stA, pack.ID)
	altPack.QuoteRef = "quote_OTHER"
	if _, _, err := s.CreateProductionHandoff(tenA, altPack, "usr_owner_a"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("alt quote: want ErrIdempotencyConflict, got %v", err)
	}
	list, err := s.ListProductionHandoffs(tenA, stA)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 handoff, got %d", len(list))
	}

	// 包属于别家店/别租户:ErrNotFound(存在性不泄露)。
	if _, _, err := s.CreateProductionHandoff(tenA, productionInput(stB, pack.ID), "usr_owner_a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-store pack: want ErrNotFound, got %v", err)
	}
	if _, _, err := s.CreateProductionHandoff(tenB, productionInput(stB, pack.ID), "usr_owner_b"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant: want ErrNotFound, got %v", err)
	}
	// 门店跨租户同理。
	if _, _, err := s.CreateProductionHandoff(tenB, productionInput(stA, pack.ID), "usr_owner_b"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant store: want ErrNotFound, got %v", err)
	}
	// quote_ref 必填。
	noQuote := productionInput(stA, pack.ID)
	noQuote.QuoteRef = " "
	if _, _, err := s.CreateProductionHandoff(tenA, noQuote, "usr_owner_a"); err == nil {
		t.Fatal("empty quote_ref must be rejected")
	}
	// 幂等键必填。
	noKey := productionInput(stA, pack.ID)
	noKey.IdempotencyKey = ""
	if _, _, err := s.CreateProductionHandoff(tenA, noKey, "usr_owner_a"); err == nil {
		t.Fatal("empty idempotency_key must be rejected")
	}
}

func productionResultInput(handoffID string) ProductionResultInput {
	return ProductionResultInput{
		HandoffID:      handoffID,
		SourceApp:      "test-producer",
		EventID:        "evt-1",
		Outcome:        ProductionOutcomeProduced,
		TaskRef:        "task_prod_9",
		ResultRevision: 1,
		ChargeRef:      "charge_1",
		VersionID:      "scpv_x1",
		Digest:         "digest-1",
		DeclaredSHA:    "aa",
		MeasuredSHA:    "aa",
	}
}

func TestRegisterProductionResultLifecycle(t *testing.T) {
	s := newProductionStore(t)
	tenA, _, stA, _ := seedProductionTenants(t, s)
	pack, err := s.CreateStoreContentPack(tenA, stA, "", "回流包", "usr_owner_a")
	if err != nil {
		t.Fatal(err)
	}
	h, _, err := s.CreateProductionHandoff(tenA, productionInput(stA, pack.ID), "usr_owner_a")
	if err != nil {
		t.Fatal(err)
	}

	// produced 回流:落行 + 交接状态 → produced + 水位/费用引用投影更新。
	r1, replayed, err := s.RegisterProductionResult(productionResultInput(h.ID))
	if err != nil || replayed {
		t.Fatalf("register: replayed=%v err=%v", replayed, err)
	}
	if r1.VersionID != "scpv_x1" {
		t.Fatalf("version ref lost: %+v", r1)
	}
	cur, err := s.GetProductionHandoff(tenA, h.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cur.Status != ProductionStatusProduced || cur.LastResultRevision != 1 || cur.LastChargeRef != "charge_1" {
		t.Fatalf("handoff not updated: %+v", cur)
	}

	// 同键同摘要重放:同一行,不新增,不受终态影响。
	r1b, replayed2, err := s.RegisterProductionResult(productionResultInput(h.ID))
	if err != nil || !replayed2 || r1b.ID != r1.ID {
		t.Fatalf("replay: replayed=%v err=%v id=%s", replayed2, err, r1b.ID)
	}

	// 同键异负载:IDEMPOTENCY_CONFLICT(digest 由 HTTP 层按整个负载算出,
	// 不同负载必然不同摘要;同摘要即同负载=重放)。
	conflict := productionResultInput(h.ID)
	conflict.ChargeRef = "charge_CHANGED"
	conflict.Digest = "digest-1-changed"
	if _, _, err := s.RegisterProductionResult(conflict); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflict: want ErrIdempotencyConflict, got %v", err)
	}

	// 新事件但 revision 不升:旧稿迟到 → ErrStaleResult(旧回流不覆盖新采用)。
	stale := productionResultInput(h.ID)
	stale.EventID = "evt-2"
	stale.ResultRevision = 1
	if _, _, err := s.RegisterProductionResult(stale); !errors.Is(err, ErrStaleResult) {
		t.Fatalf("stale: want ErrStaleResult, got %v", err)
	}

	// 新事件 + 更高 revision:接受,水位推进。
	higher := productionResultInput(h.ID)
	higher.EventID = "evt-3"
	higher.ResultRevision = 2
	higher.VersionID = "scpv_x2"
	higher.Digest = "digest-3"
	if _, _, err := s.RegisterProductionResult(higher); err != nil {
		t.Fatalf("higher revision: %v", err)
	}

	// 行上记录了真实 task_ref 后,回执必须指名同一任务(票面 task_ref 校验)。
	if _, err := s.MarkProductionHandoffSubmitted(tenA, h.ID, "task_prod_9"); err != nil {
		t.Fatal(err)
	}

	// task_ref 与交接行已记录值不符:ErrTaskRefMismatch。
	mismatch := productionResultInput(h.ID)
	mismatch.EventID = "evt-4"
	mismatch.ResultRevision = 3
	mismatch.TaskRef = "task_OTHER"
	mismatch.Digest = "digest-4"
	if _, _, err := s.RegisterProductionResult(mismatch); !errors.Is(err, ErrTaskRefMismatch) {
		t.Fatalf("task mismatch: want ErrTaskRefMismatch, got %v", err)
	}

	// failed 回执独立成行,驱动交接状态 → failed。
	fail := productionResultInput(h.ID)
	fail.EventID = "evt-5"
	fail.ResultRevision = 3
	fail.Outcome = ProductionOutcomeFailed
	fail.VersionID = ""
	fail.Digest = "digest-5"
	fail.Reason = "渲染失败"
	if _, _, err := s.RegisterProductionResult(fail); err != nil {
		t.Fatalf("failed receipt: %v", err)
	}
	cur, err = s.GetProductionHandoff(tenA, h.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cur.Status != ProductionStatusFailed {
		t.Fatalf("want failed status, got %s", cur.Status)
	}

	// failed 后再次 produced(重试成功)合法且推动状态回升。
	retry := productionResultInput(h.ID)
	retry.EventID = "evt-6"
	retry.ResultRevision = 4
	retry.VersionID = "scpv_x3"
	retry.Digest = "digest-6"
	if _, _, err := s.RegisterProductionResult(retry); err != nil {
		t.Fatalf("retry after failure: %v", err)
	}
	cur, _ = s.GetProductionHandoff(tenA, h.ID)
	if cur.Status != ProductionStatusProduced {
		t.Fatalf("want produced after retry, got %s", cur.Status)
	}

	// canceled 终态:拒新回流;重放旧事件仍返回原行。
	cancel := productionResultInput(h.ID)
	cancel.EventID = "evt-7"
	cancel.ResultRevision = 5
	cancel.Outcome = ProductionOutcomeCanceled
	cancel.Digest = "digest-7"
	cancel.Reason = "商家取消"
	if _, _, err := s.RegisterProductionResult(cancel); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	after := productionResultInput(h.ID)
	after.EventID = "evt-8"
	after.ResultRevision = 6
	after.Digest = "digest-8"
	if _, _, err := s.RegisterProductionResult(after); !errors.Is(err, ErrHandoffCanceled) {
		t.Fatalf("after cancel: want ErrHandoffCanceled, got %v", err)
	}
	if _, replayed, err := s.RegisterProductionResult(productionResultInput(h.ID)); err != nil || !replayed {
		t.Fatalf("replay after cancel: replayed=%v err=%v", replayed, err)
	}

	// 结果列表只追加且有序(evt-1/3/5/6/7 落行;重放/冲突/迟到/取消后新流零新增)。
	results, err := s.ListProductionResults(tenA, h.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 5 {
		t.Fatalf("want 5 result rows, got %d", len(results))
	}

	// 未知 handoff:ErrNotFound。
	ghost := productionResultInput("cph_ghost")
	if _, _, err := s.RegisterProductionResult(ghost); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ghost handoff: want ErrNotFound, got %v", err)
	}
}

func TestGetProductionHandoffByIDTrustedSeam(t *testing.T) {
	s := newProductionStore(t)
	tenA, tenB, stA, _ := seedProductionTenants(t, s)
	pack, err := s.CreateStoreContentPack(tenA, stA, "", "受信包", "usr_owner_a")
	if err != nil {
		t.Fatal(err)
	}
	h, _, err := s.CreateProductionHandoff(tenA, productionInput(stA, pack.ID), "usr_owner_a")
	if err != nil {
		t.Fatal(err)
	}
	// 受信接缝:无租户作用域,行=租户真值。
	byID, err := s.GetProductionHandoffByID(h.ID)
	if err != nil || byID.TenantID != tenA {
		t.Fatalf("byID: err=%v tenant=%s", err, byID.TenantID)
	}
	// 未知/跨租户作用域统一 ErrNotFound。
	if _, err := s.GetProductionHandoffByID("cph_none"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown: want ErrNotFound, got %v", err)
	}
	if _, err := s.GetProductionHandoff(tenB, h.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant get: want ErrNotFound, got %v", err)
	}
}

func TestMarkProductionHandoffSubmitted(t *testing.T) {
	s := newProductionStore(t)
	tenA, tenB, stA, _ := seedProductionTenants(t, s)
	pack, err := s.CreateStoreContentPack(tenA, stA, "", "提交包", "usr_owner_a")
	if err != nil {
		t.Fatal(err)
	}
	h, _, err := s.CreateProductionHandoff(tenA, productionInput(stA, pack.ID), "usr_owner_a")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.MarkProductionHandoffSubmitted(tenA, h.ID, "task_real_1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != ProductionStatusSubmitted || got.TaskRef != "task_real_1" {
		t.Fatalf("submit: %+v", got)
	}
	// task_ref 一经真实落定不可被静默改写:再次提交须携带同一引用。
	if _, err := s.MarkProductionHandoffSubmitted(tenA, h.ID, "task_other"); !errors.Is(err, ErrTaskRefMismatch) {
		t.Fatalf("re-submit mismatch: want ErrTaskRefMismatch, got %v", err)
	}
	if _, err := s.MarkProductionHandoffSubmitted(tenB, h.ID, "task_x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant submit: want ErrNotFound, got %v", err)
	}
}
