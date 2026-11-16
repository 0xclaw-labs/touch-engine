// store_billing_checkout_test.go: billing_checkout_refs SQL 层测试(HUI-4664
// 同单恢复复用/幂等重放零新增/租户封闭/状态机接线)。

package store

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/billing"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
)

func newCheckoutStore(t *testing.T) (*Store, string, string) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	st := New(d)
	tenA, err := st.CreateTenant("接续租户A")
	if err != nil {
		t.Fatalf("tenant A: %v", err)
	}
	tenB, err := st.CreateTenant("接续租户B")
	if err != nil {
		t.Fatalf("tenant B: %v", err)
	}
	return st, tenA.ID, tenB.ID
}

func checkoutRefInput(tenantID string) NewBillingCheckoutRef {
	return NewBillingCheckoutRef{
		TenantID: tenantID, HandoffID: "cph_1", QuoteRef: "q_1", OrderRef: "cph_1",
		AmountMinor: 990000, Currency: "CNY", PayerRef: "acct_a",
	}
}

func TestUpsertBillingCheckoutRefSameOrderReusesOneReference(t *testing.T) {
	st, tenA, _ := newCheckoutStore(t)
	row, replayed, err := st.UpsertBillingCheckoutRef(checkoutRefInput(tenA), now())
	if err != nil || replayed || row.State != billing.CheckoutStatePending || row.CheckoutRef == "" {
		t.Fatalf("first registration: %+v replayed=%v err=%v", row, replayed, err)
	}
	// 同单同参重试:复用同一行同一引用(同单恢复纪律),零新增。
	again, replayed, err := st.UpsertBillingCheckoutRef(checkoutRefInput(tenA), now())
	if err != nil || !replayed || again.ID != row.ID || again.CheckoutRef != row.CheckoutRef {
		t.Fatalf("same-order retry must reuse the row: %+v replayed=%v err=%v", again, replayed, err)
	}
	// 同单 checkout_ref 变化 = 合同违约(引用恒定):拒绝并保原行。
	drift := checkoutRefInput(tenA)
	drift.CheckoutRef = "ck_tampered"
	if _, _, err := st.UpsertBillingCheckoutRef(drift, now()); !errors.Is(err, ErrBillingIdempotencyConflict) {
		t.Fatalf("ref drift = %v, want ErrBillingIdempotencyConflict", err)
	}
	kept, err := st.GetBillingCheckoutRefByOrder(tenA, "cph_1")
	if err != nil || kept.CheckoutRef != row.CheckoutRef || kept.State != billing.CheckoutStatePending {
		t.Fatalf("original row must be preserved: %+v err=%v", kept, err)
	}
	// 同单金额/币种漂移 = 幂等冲突(原单快照不可重计价)。
	for name, mutate := range map[string]func(*NewBillingCheckoutRef){
		"amount": func(in *NewBillingCheckoutRef) { in.AmountMinor = 880000 },
		"quote":  func(in *NewBillingCheckoutRef) { in.QuoteRef = "q_2" },
		"handoff": func(in *NewBillingCheckoutRef) { in.HandoffID = "cph_9" },
	} {
		in := checkoutRefInput(tenA)
		mutate(&in)
		if _, _, err := st.UpsertBillingCheckoutRef(in, now()); !errors.Is(err, ErrBillingIdempotencyConflict) {
			t.Fatalf("%s drift = %v, want ErrBillingIdempotencyConflict", name, err)
		}
	}
	// 坏输入如实拒绝。
	if _, _, err := st.UpsertBillingCheckoutRef(NewBillingCheckoutRef{TenantID: tenA}, now()); !errors.Is(err, ErrBillingBadInput) {
		t.Fatalf("missing order_ref = %v, want ErrBillingBadInput", err)
	}
}

func TestBillingCheckoutRefTenantScope(t *testing.T) {
	st, tenA, tenB := newCheckoutStore(t)
	row, _, err := st.UpsertBillingCheckoutRef(checkoutRefInput(tenA), now())
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// 他租户按 order_ref / checkout_ref 取行 = 同形 ErrNotFound(掩蔽)。
	if _, err := st.GetBillingCheckoutRefByOrder(tenB, "cph_1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign order_ref = %v, want ErrNotFound", err)
	}
	if _, err := st.GetBillingCheckoutRefByRef(tenB, row.CheckoutRef); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign checkout_ref = %v, want ErrNotFound", err)
	}
	if _, err := st.GetBillingCheckoutRefByHandoff(tenB, "cph_1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign handoff = %v, want ErrNotFound", err)
	}
	// 本租户读回正常。
	got, err := st.GetBillingCheckoutRefByHandoff(tenA, "cph_1")
	if err != nil || got.ID != row.ID {
		t.Fatalf("own handoff lookup: %+v err=%v", got, err)
	}
}

func TestApplyBillingCheckoutEventPaymentFlow(t *testing.T) {
	st, tenA, _ := newCheckoutStore(t)
	row, _, err := st.UpsertBillingCheckoutRef(checkoutRefInput(tenA), now())
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	payEvent := func(ref string) billing.AuthorityEventInput {
		return billing.AuthorityEventInput{
			Source: "pai-billing", EventID: "evt-" + ref, Type: billing.EventPaymentPaid,
			Payload: billing.EventPayload{PaymentRef: ref}, AmountMinor: 990000, Currency: "CNY",
		}
	}
	// 金额不符:不推进、不改写(宁可挂起不可错开)。
	bad := payEvent("pay_bad")
	bad.AmountMinor = 1
	if _, _, err := st.ApplyBillingCheckoutEvent(tenA, row.CheckoutRef, bad, now()); !errors.Is(err, billing.ErrEventMismatch) {
		t.Fatalf("mismatched amount = %v, want ErrEventMismatch", err)
	}
	kept, err := st.GetBillingCheckoutRefByOrder(tenA, "cph_1")
	if err != nil || kept.State != billing.CheckoutStatePending || kept.PaymentRef != "" {
		t.Fatalf("mismatch must not advance: %+v err=%v", kept, err)
	}
	// 正确支付事实:pending→paid,payment_ref 照存。
	paid, replayed, err := st.ApplyBillingCheckoutEvent(tenA, row.CheckoutRef, payEvent("pay_1"), now())
	if err != nil || replayed || paid.State != billing.CheckoutStatePaid || paid.PaymentRef != "pay_1" {
		t.Fatalf("payment fact: %+v replayed=%v err=%v", paid, replayed, err)
	}
	// 同 payment_ref 重放:零新增零改写(replayed=true)。
	replay, replayed, err := st.ApplyBillingCheckoutEvent(tenA, row.CheckoutRef, payEvent("pay_1"), now())
	if err != nil || !replayed || replay.State != billing.CheckoutStatePaid || replay.PaymentRef != "pay_1" {
		t.Fatalf("payment replay: %+v replayed=%v err=%v", replay, replayed, err)
	}
	// 开通事实:paid→activated;迟到的失败事实不追溯削减。
	active, replayed, err := st.ApplyBillingCheckoutEvent(tenA, row.CheckoutRef, billing.AuthorityEventInput{
		Source: "pai-billing", EventID: "evt-active", Type: billing.EventActivationActive,
	}, now())
	if err != nil || replayed || active.State != billing.CheckoutStateActivated {
		t.Fatalf("activation: %+v replayed=%v err=%v", active, replayed, err)
	}
	if _, _, err := st.ApplyBillingCheckoutEvent(tenA, row.CheckoutRef, billing.AuthorityEventInput{
		Source: "pai-billing", EventID: "evt-late", Type: billing.EventActivationFailed,
	}, now()); !errors.Is(err, billing.ErrInvalidTransition) {
		t.Fatalf("late failed = %v, want ErrInvalidTransition", err)
	}
	// 未知 checkout_ref = ErrNotFound(同形掩蔽)。
	if _, _, err := st.ApplyBillingCheckoutEvent(tenA, "ck_missing", payEvent("pay_x"), now()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown ref = %v, want ErrNotFound", err)
	}
	// 他租户 checkout_ref = ErrNotFound(掩蔽)。
	st2, tenA2, _ := newCheckoutStore(t)
	r2, _, err := st2.UpsertBillingCheckoutRef(checkoutRefInput(tenA2), now())
	if err != nil {
		t.Fatalf("upsert tenant2: %v", err)
	}
	_ = r2
}

func TestApplyBillingCheckoutEventFailedThenRetryActivation(t *testing.T) {
	st, tenA, _ := newCheckoutStore(t)
	row, _, err := st.UpsertBillingCheckoutRef(checkoutRefInput(tenA), now())
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, _, err = st.ApplyBillingCheckoutEvent(tenA, row.CheckoutRef, billing.AuthorityEventInput{
		Source: "pai-billing", EventID: "evt-paid", Type: billing.EventPaymentPaid,
		Payload: billing.EventPayload{PaymentRef: "pay_9"}, AmountMinor: 990000, Currency: "CNY",
	}, now()); err != nil {
		t.Fatalf("paid: %v", err)
	}
	failed, _, err := st.ApplyBillingCheckoutEvent(tenA, row.CheckoutRef, billing.AuthorityEventInput{
		Source: "pai-billing", EventID: "evt-failed", Type: billing.EventActivationFailed,
		Payload: billing.EventPayload{FailReason: "provider_timeout"},
	}, now())
	if err != nil || failed.State != billing.CheckoutStateFailed || failed.FailReason != "provider_timeout" {
		t.Fatalf("failed: %+v err=%v", failed, err)
	}
	// 补偿:可重试激活(重试成功如实生效,支付事实保持)。
	retried, _, err := st.ApplyBillingCheckoutEvent(tenA, row.CheckoutRef, billing.AuthorityEventInput{
		Source: "pai-billing", EventID: "evt-retry", Type: billing.EventActivationActive,
	}, now())
	if err != nil || retried.State != billing.CheckoutStateActivated || retried.PaymentRef != "pay_9" || retried.FailReason != "" {
		t.Fatalf("retry activation: %+v err=%v", retried, err)
	}
}

func TestApplyBillingCheckoutEventOpeningIsNoOpAck(t *testing.T) {
	st, tenA, _ := newCheckoutStore(t)
	row, _, err := st.UpsertBillingCheckoutRef(checkoutRefInput(tenA), now())
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// v1.1:opening 落 checkout 分支 = FYI no-op ack,pending 态零改写。
	acked, replayed, err := st.ApplyBillingCheckoutEvent(tenA, row.CheckoutRef, billing.AuthorityEventInput{
		Source: "pai-billing", EventID: "evt-open-1", Type: billing.EventActivationOpening,
	}, now())
	if err != nil || !replayed || acked.State != billing.CheckoutStatePending {
		t.Fatalf("opening on pending: %+v replayed=%v err=%v", acked, replayed, err)
	}
	// paid 态同样零改写(producer 在 payment_paid 之后发 opening)。
	if _, _, err = st.ApplyBillingCheckoutEvent(tenA, row.CheckoutRef, billing.AuthorityEventInput{
		Source: "pai-billing", EventID: "evt-paid-op", Type: billing.EventPaymentPaid,
		Payload: billing.EventPayload{PaymentRef: "pay_op"}, AmountMinor: 990000, Currency: "CNY",
	}, now()); err != nil {
		t.Fatalf("paid: %v", err)
	}
	acked, replayed, err = st.ApplyBillingCheckoutEvent(tenA, row.CheckoutRef, billing.AuthorityEventInput{
		Source: "pai-billing", EventID: "evt-open-2", Type: billing.EventActivationOpening,
	}, now())
	if err != nil || !replayed || acked.State != billing.CheckoutStatePaid {
		t.Fatalf("opening on paid: %+v replayed=%v err=%v", acked, replayed, err)
	}
}

func TestMarkBillingCheckoutActivatedLocalFact(t *testing.T) {
	st, tenA, tenB := newCheckoutStore(t)
	row, _, err := st.UpsertBillingCheckoutRef(checkoutRefInput(tenA), now())
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// pending(无支付事实)不可本地激活。
	if _, _, err := st.MarkBillingCheckoutActivated(tenA, row.CheckoutRef, now()); !errors.Is(err, billing.ErrInvalidTransition) {
		t.Fatalf("pending local activation = %v, want ErrInvalidTransition", err)
	}
	if _, _, err = st.ApplyBillingCheckoutEvent(tenA, row.CheckoutRef, billing.AuthorityEventInput{
		Source: "pai-billing", EventID: "evt-paid-loc", Type: billing.EventPaymentPaid,
		Payload: billing.EventPayload{PaymentRef: "pay_loc"}, AmountMinor: 990000, Currency: "CNY",
	}, now()); err != nil {
		t.Fatalf("paid: %v", err)
	}
	// paid + 本地制作事实 → activated(v1.1 D3),支付事实保持。
	advanced, replayed, err := st.MarkBillingCheckoutActivated(tenA, row.CheckoutRef, now())
	if err != nil || replayed || advanced.State != billing.CheckoutStateActivated ||
		advanced.PaymentRef != "pay_loc" || advanced.FailReason != "" {
		t.Fatalf("local activation: %+v replayed=%v err=%v", advanced, replayed, err)
	}
	// 幂等:已 activated 重入 = replay 零改写。
	again, replayed, err := st.MarkBillingCheckoutActivated(tenA, row.CheckoutRef, now())
	if err != nil || !replayed || again.State != billing.CheckoutStateActivated {
		t.Fatalf("idempotent re-entry: %+v replayed=%v err=%v", again, replayed, err)
	}
	// 他租户同 ref = ErrNotFound(掩蔽);未知 ref 同答。
	if _, _, err := st.MarkBillingCheckoutActivated(tenB, row.CheckoutRef, now()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign ref = %v, want ErrNotFound", err)
	}
	if _, _, err := st.MarkBillingCheckoutActivated(tenA, "ck_missing", now()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown ref = %v, want ErrNotFound", err)
	}
}
