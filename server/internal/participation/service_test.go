package participation

// T5-T10: Service 行为(真实 SQLite 文件库,非 mock)。
//
// 覆盖面:
//   - T5  开始幂等:同 (tenant,campaign,idempotency_key) 重放回原行;
//   - T6  读取/续接/换选/取消;裸 ID/跨 visitor/越店一律拒绝;
//   - T7  尝试与交接冻结;换稿 = 新 attempt;benefit_slot 不新增;
//   - T8  凭据兑换/撤销 + 负例矩阵(过期/已用/撤销/跨租户/目标不符);
//   - T9  预留联动四态;未确认绝不伪报;MarkFulfilled 幂等拒降级;
//   - T10 清理。

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type recordingReserver struct {
	calls int
	ref   ParticipationRef
	next  func() (ReserveResult, error)
}

func (r *recordingReserver) Reserve(ctx context.Context, ref ParticipationRef) (ReserveResult, error) {
	r.calls++
	r.ref = ref
	if r.next == nil {
		return ReserveResult{Reserved: true, ReservationRef: "rsv_default"}, nil
	}
	return r.next()
}

func newService(t *testing.T, res BenefitReserver) (*Service, string, string, string) {
	t.Helper()
	d := openTestDB(t)
	tenantID, storeID, campaignID := seedRef(t, d)
	return New(d, res, Options{Now: func() time.Time {
		return time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	}}), tenantID, storeID, campaignID
}

func startInput(tenant, store, campaign, visitor, idem, content string) StartInput {
	return StartInput{
		TenantID: tenant, StoreID: store, CampaignID: campaign, LinkCode: "link01",
		Source: SourceQR, RuleVersion: 1, ContentPackVersion: content,
		VisitorID: visitor, IdempotencyKey: idem,
		Window: Window{StartsAt: "2026-10-14T00:00:00Z", ExpiresAt: "2026-10-21T00:00:00Z"},
	}
}

func TestStartIdempotentReplay(t *testing.T) {
	res := &recordingReserver{}
	svc, tenant, store, campaign := newService(t, res)
	ctx := context.Background()

	first, err := svc.Start(ctx, startInput(tenant, store, campaign, "visit_a", "idem_1", "cp_v1"))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !first.Started || first.Participation.Status != StatusPending {
		t.Fatalf("first start: %+v", first)
	}
	if first.Participation.ReservationState != ReservationConfirmed || first.Participation.ReservationRef != "rsv_default" {
		t.Fatalf("reservation should be confirmed on success: %+v", first.Participation)
	}
	if first.Participation.BenefitSlot == "" || first.Participation.BenefitSlot != first.Participation.ID {
		t.Fatal("benefit slot must default to the participation id (one slot per participation)")
	}

	// 同键重放:回原行,不重复预留。
	second, err := svc.Start(ctx, startInput(tenant, store, campaign, "visit_a", "idem_1", "cp_v1"))
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if second.Started {
		t.Fatal("replay must not create a new row")
	}
	if second.Participation.ID != first.Participation.ID {
		t.Fatalf("replay returned %s, want %s", second.Participation.ID, first.Participation.ID)
	}
	if res.calls != 1 {
		t.Fatalf("replay must not re-reserve, calls=%d", res.calls)
	}

	// 不同键:新行。
	third, err := svc.Start(ctx, startInput(tenant, store, campaign, "visit_a", "idem_2", "cp_v1"))
	if err != nil || !third.Started {
		t.Fatalf("distinct key should start fresh: %+v err=%v", third, err)
	}
	if third.Participation.ID == first.Participation.ID {
		t.Fatal("distinct idempotency key must create a distinct participation")
	}

	// 浏览不建参与:不存在任何「读时建行」路径——包级无该 API 即为证。

	// 缺字段拒绝。
	if _, err := svc.Start(ctx, startInput(tenant, store, campaign, "", "idem_3", "cp")); !errors.Is(err, ErrNotEntitled) {
		t.Fatalf("empty visitor must be rejected, got %v", err)
	}
	if _, err := svc.Start(ctx, startInput(tenant, store, campaign, "v", "", "cp")); err == nil {
		t.Fatal("empty idempotency key must be rejected")
	}
	if _, err := svc.Start(ctx, func() StartInput {
		in := startInput(tenant, store, campaign, "v", "idem_4", "cp")
		in.Source = "fingerprint"
		return in
	}()); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("invalid source must be rejected, got %v", err)
	}

	// 换浏览器重放同键:拒绝(不向陌生 visitor 泄漏原参与)。
	if _, err := svc.Start(ctx, startInput(tenant, store, campaign, "visit_stranger", "idem_1", "cp")); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("same key from another visitor must conflict, got %v", err)
	}
}

func TestPeekCredentialDoesNotConsume(t *testing.T) {
	svc, tenant, store, campaign := newService(t, nil)
	ctx := context.Background()
	started, _ := svc.Start(ctx, startInput(tenant, store, campaign, "visit_a", "idem_1", "cp"))
	id := started.Participation.ID
	h, _ := svc.CreateAttempt(ctx, AttemptInput{ParticipationID: id, VisitorID: "visit_a", Platform: "douyin", Method: MethodH5})
	hk, err := svc.BeginHandoff(ctx, HandoffInput{ParticipationID: id, VisitorID: "visit_a", AttemptID: h.ID})
	if err != nil {
		t.Fatalf("handoff: %v", err)
	}

	// Peek 返回参与与凭据绑定,不消费。
	p, cred, err := svc.PeekCredential(ctx, hk.Token)
	if err != nil || p.ID != id || cred.Purpose != PurposeCustomerResume {
		t.Fatalf("peek: p=%+v cred=%+v err=%v", p, cred, err)
	}
	// Peek 之后仍可正常兑换(未被消费)。
	if _, err := svc.Redeem(ctx, RedeemInput{Token: hk.Token, Purpose: PurposeCustomerResume, TargetScope: "h5", NewVisitorID: "visit_b"}); err != nil {
		t.Fatalf("redeem after peek must succeed, got %v", err)
	}
	// 伪造 token:统一 NotFound。
	if _, _, err := svc.PeekCredential(ctx, "garbage"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("garbage peek must be ErrNotFound, got %v", err)
	}
}

func TestLookupAndAccessControl(t *testing.T) {
	svc, tenant, store, campaign := newService(t, nil)
	ctx := context.Background()
	started, err := svc.Start(ctx, startInput(tenant, store, campaign, "visit_a", "idem_1", "cp_v1"))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	id := started.Participation.ID

	// 同 visitor lookup 恢复未完参与。
	got, ok, err := svc.LookupByVisitor(ctx, tenant, "link01", "visit_a")
	if err != nil || !ok || got.ID != id {
		t.Fatalf("lookup: ok=%v id=%s err=%v", ok, got.ID, err)
	}
	// 另一个浏览器(visitor_b)不能自动读到。
	if _, ok, _ := svc.LookupByVisitor(ctx, tenant, "link01", "visit_b"); ok {
		t.Fatal("another browser must not auto-recover the participation")
	}
	// 裸 ID:无绑定读取拒绝(顺序/猜测 ID 无探测面)。
	if _, err := svc.GetForVisitor(ctx, id, "visit_b"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign visitor read must be ErrNotFound, got %v", err)
	}
	if _, err := svc.GetForVisitor(ctx, "pt_missing", "visit_a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id must be ErrNotFound, got %v", err)
	}
	// 租户边界来自参与行本身:访客面没有可伪造的租户入参,
	// 唯一的访问键是 visitor 绑定(上面 foreign visitor 已断言)。
	// 自身会话读取正常。
	if p, err := svc.GetForVisitor(ctx, id, "visit_a"); err != nil || p.ID != id {
		t.Fatalf("own read: %v", err)
	}
}

func TestStaffAccessScope(t *testing.T) {
	svc, tenant, store, campaign := newService(t, nil)
	ctx := context.Background()
	started, _ := svc.Start(ctx, startInput(tenant, store, campaign, "visit_a", "idem_1", "cp"))
	id := started.Participation.ID

	if p, err := svc.GetForStaff(ctx, id, StaffView{TenantID: tenant, Role: "org_owner"}); err != nil || p.ID != id {
		t.Fatalf("org_owner reads own tenant: %v", err)
	}
	if p, err := svc.GetForStaff(ctx, id, StaffView{TenantID: tenant, Role: "store_manager", StoreScope: store}); err != nil || p.ID != id {
		t.Fatalf("store_manager reads own store: %v", err)
	}
	// 越店负例。
	if _, err := svc.GetForStaff(ctx, id, StaffView{TenantID: tenant, Role: "store_manager", StoreScope: "sto_other"}); !errors.Is(err, ErrNotEntitled) {
		t.Fatalf("cross-store staff read must be ErrNotEntitled, got %v", err)
	}
	if _, err := svc.GetForStaff(ctx, id, StaffView{TenantID: "tnt_other", Role: "org_owner"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant staff read must be ErrNotFound (no existence leak), got %v", err)
	}
	// 撤权即失效。
	if _, err := svc.GetForStaff(ctx, id, StaffView{TenantID: tenant, Role: "org_owner", Revoked: true}); !errors.Is(err, ErrNotEntitled) {
		t.Fatalf("revoked member must fail closed, got %v", err)
	}
}

func TestContentChangeAndAttempts(t *testing.T) {
	svc, tenant, store, campaign := newService(t, nil)
	ctx := context.Background()
	started, _ := svc.Start(ctx, startInput(tenant, store, campaign, "visit_a", "idem_1", "cp_v1"))
	id := started.Participation.ID

	// 预览期自由换选。
	if _, err := svc.UpdateContent(ctx, id, "visit_a", "cp_v2"); err != nil {
		t.Fatalf("content change before handoff: %v", err)
	}
	p, _ := svc.GetForVisitor(ctx, id, "visit_a")
	if p.ContentPackVersion != "cp_v2" {
		t.Fatalf("selection = %s, want cp_v2", p.ContentPackVersion)
	}

	// attempt 1:h5 交接,冻结 cp_v2。
	h1, err := svc.CreateAttempt(ctx, AttemptInput{ParticipationID: id, VisitorID: "visit_a", Platform: "douyin", Method: MethodH5})
	if err != nil || h1.Status != AttemptPrepared {
		t.Fatalf("CreateAttempt: %+v err=%v", h1, err)
	}
	tok1, err := svc.BeginHandoff(ctx, HandoffInput{ParticipationID: id, VisitorID: "visit_a", AttemptID: h1.ID, PublicBaseURL: "https://h5.example.com"})
	if err != nil {
		t.Fatalf("BeginHandoff: %v", err)
	}
	if !ValidTokenShape(tok1.Token) {
		t.Fatal("handoff must return a well-shaped token")
	}
	if tok1.QRPayload == "" || !strings.HasPrefix(tok1.QRPayload, "https://h5.example.com") || strings.Contains(tok1.QRPayload, "?") {
		t.Fatalf("qr payload must be the canonical short URL, got %q", tok1.QRPayload)
	}
	if !CanTransition(StatusPending, StatusInProgress) {
		t.Fatal("sanity")
	}
	p, _ = svc.GetForVisitor(ctx, id, "visit_a")
	if p.Status != StatusInProgress {
		t.Fatalf("status after first handoff = %s, want in_progress", p.Status)
	}

	// 冻结后换选:参与当前选择变化,attempt 冻结行不变。
	if _, err := svc.UpdateContent(ctx, id, "visit_a", "cp_v3"); err != nil {
		t.Fatalf("content change after handoff must move the current selection: %v", err)
	}
	// 换稿/换平台 = 新 attempt。
	a2, err := svc.CreateAttempt(ctx, AttemptInput{ParticipationID: id, VisitorID: "visit_a", Platform: "xiaohongshu", Method: MethodPlatformMini})
	if err != nil {
		t.Fatalf("second attempt: %v", err)
	}
	tok2, err := svc.BeginHandoff(ctx, HandoffInput{ParticipationID: id, VisitorID: "visit_a", AttemptID: a2.ID})
	if err != nil {
		t.Fatalf("second handoff: %v", err)
	}
	if tok2.Token == tok1.Token {
		t.Fatal("each handoff mints a fresh credential")
	}
	attempts, err := svc.ListAttempts(ctx, id, "visit_a")
	if err != nil || len(attempts) != 2 {
		t.Fatalf("want 2 attempts, got %d err=%v", len(attempts), err)
	}
	// 冻结语义:按内容版本定位 attempt。attempt(cp_v2) 保持冻结;新 attempt
	// 冻结交接时刻的当前选择 cp_v3。
	byVersion := map[string]Attempt{}
	for _, a := range attempts {
		byVersion[a.ContentPackVersion] = a
	}
	if a, ok := byVersion["cp_v2"]; !ok || a.Status != AttemptHandedOff {
		t.Fatalf("frozen cp_v2 attempt = %+v ok=%v", a, ok)
	}
	if _, ok := byVersion["cp_v3"]; !ok {
		t.Fatalf("attempt2 should freeze current selection cp_v3, got %+v", byVersion)
	}
	// benefit_slot 不新增:两个凭据同属一个参与,槽位仍是参与 id。
	if tok2.Participation.BenefitSlot != id {
		t.Fatalf("slot must stay %s, got %s", id, tok2.Participation.BenefitSlot)
	}
	// 他人 attempt 无权;prepared 不能直接当凭据(必须 BeginHandoff)。
	if _, err := svc.CreateAttempt(ctx, AttemptInput{ParticipationID: id, VisitorID: "visit_b", Platform: "douyin", Method: MethodH5}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign attempt creation must fail, got %v", err)
	}
}

func TestReservationFourStatesAndNoFalseConfirmed(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		next  func() (ReserveResult, error)
		want  ReservationState
		still bool // Start 是否照常成功落 pending 行
	}{
		{"success", func() (ReserveResult, error) { return ReserveResult{Reserved: true, ReservationRef: "rsv_ok"}, nil }, ReservationConfirmed, true},
		{"out_of_stock", func() (ReserveResult, error) { return ReserveResult{Reason: ReasonOutOfStock}, nil }, ReservationFailed, true},
		{"timeout", func() (ReserveResult, error) { return ReserveResult{Reason: ReasonTimeout}, nil }, ReservationFailed, true},
		{"conflict", func() (ReserveResult, error) { return ReserveResult{Reason: ReasonConflict}, nil }, ReservationFailed, true},
		{"transport_error", func() (ReserveResult, error) { return ReserveResult{}, errors.New("dial fail") }, ReservationPending, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := &recordingReserver{next: tc.next}
			svc, tenant, store, campaign := newService(t, res)
			started, err := svc.Start(ctx, startInput(tenant, store, campaign, "visit_a", "idem_"+tc.name, "cp"))
			if err != nil || !started.Started {
				t.Fatalf("Start must persist even when reservation fails: %+v err=%v", started, err)
			}
			if started.Participation.ReservationState != tc.want {
				t.Fatalf("state = %s, want %s", started.Participation.ReservationState, tc.want)
			}
			if started.Participation.ReservationState == ReservationConfirmed {
				if started.Participation.ReservationRef != "rsv_ok" {
					t.Fatal("confirmed requires the reservation ref")
				}
			} else if started.Participation.ReservationRef != "" {
				t.Fatal("unconfirmed reservation must not carry a ref (never 已保留)")
			}
			// nil reserver:not_requested,参与照建。
			if tc.name == "success" {
				svc2, t2, s2, c2 := newService(t, nil)
				st2, err := svc2.Start(ctx, startInput(t2, s2, c2, "visit_a", "idem_nil", "cp"))
				if err != nil || st2.Participation.ReservationState != ReservationNotRequested {
					t.Fatalf("nil reserver: %+v err=%v", st2, err)
				}
			}
		})
	}
}

func TestMarkFulfilledIdempotentNoDowngrade(t *testing.T) {
	svc, tenant, store, campaign := newService(t, nil)
	ctx := context.Background()
	started, _ := svc.Start(ctx, startInput(tenant, store, campaign, "visit_a", "idem_1", "cp"))
	id := started.Participation.ID

	// pending 直接 fulfilled 非法(预留不等于兑现)。
	if _, err := svc.MarkFulfilled(ctx, tenant, id, "ful_1"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("pending -> fulfilled must be illegal, got %v", err)
	}
	// 走到 awaiting_review。
	if _, err := svc.BeginHandoff(ctx, HandoffInput{
		ParticipationID: id, VisitorID: "visit_a", Purpose: PurposeStaffVerify, TargetScope: tenant,
	}); err != nil {
		t.Fatalf("staff handoff: %v", err)
	}
	if _, err := svc.Redeem(ctx, RedeemInput{Token: mustStaffToken(t, svc, tenant, id, "visit_a"), Purpose: PurposeStaffVerify, TargetScope: tenant, StaffTenantID: tenant}); err != nil {
		t.Fatalf("staff redeem: %v", err)
	}
	p, _ := svc.GetForStaff(ctx, id, StaffView{TenantID: tenant, Role: "org_owner"})
	if p.Status != StatusAwaitingReview {
		t.Fatalf("after staff redeem status = %s, want awaiting_review", p.Status)
	}
	// 兑现 + 幂等重放;不同 ref 拒绝(不改写已兑现)。
	if _, err := svc.MarkFulfilled(ctx, tenant, id, "ful_1"); err != nil {
		t.Fatalf("MarkFulfilled: %v", err)
	}
	if _, err := svc.MarkFulfilled(ctx, tenant, id, "ful_1"); err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	if _, err := svc.MarkFulfilled(ctx, tenant, id, "ful_2"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("different ref on fulfilled must not rewrite, got %v", err)
	}
}

// mustStaffToken creates a staff_verify credential through the customer path
// and returns the plaintext token.
func mustStaffToken(t *testing.T, svc *Service, tenant, id, visitor string) string {
	t.Helper()
	tok, err := svc.BeginHandoff(context.Background(), HandoffInput{
		ParticipationID: id, VisitorID: visitor,
		Purpose: PurposeStaffVerify, TargetScope: tenant,
	})
	if err != nil {
		t.Fatalf("BeginHandoff(staff): %v", err)
	}
	// staff_verify 的 BeginHandoff 直接同时生成凭据(不要求先建 attempt)。
	return tok.Token
}

func TestHandoffRedeemMatrix(t *testing.T) {
	svc, tenant, store, campaign := newService(t, nil)
	ctx := context.Background()
	started, _ := svc.Start(ctx, startInput(tenant, store, campaign, "visit_a", "idem_1", "cp"))
	id := started.Participation.ID

	// customer_resume 交接。
	h, _ := svc.CreateAttempt(ctx, AttemptInput{ParticipationID: id, VisitorID: "visit_a", Platform: "douyin", Method: MethodH5})
	hk, err := svc.BeginHandoff(ctx, HandoffInput{ParticipationID: id, VisitorID: "visit_a", AttemptID: h.ID})
	if err != nil {
		t.Fatalf("handoff: %v", err)
	}

	// 首兑成功:换绑到新设备 visitor_b。
	res, err := svc.Redeem(ctx, RedeemInput{Token: hk.Token, Purpose: PurposeCustomerResume, TargetScope: "h5", NewVisitorID: "visit_b"})
	if err != nil || res.Participation.ID != id {
		t.Fatalf("redeem: %+v err=%v", res, err)
	}
	if p, _ := svc.GetForVisitor(ctx, id, "visit_b"); p.ID != id {
		t.Fatal("new device should own the participation after redeem")
	}
	if _, err := svc.GetForVisitor(ctx, id, "visit_a"); !errors.Is(err, ErrNotFound) {
		t.Fatal("old device binding is transferred (single credential)")
	}

	// 一次性:重放被拒。
	if _, err := svc.Redeem(ctx, RedeemInput{Token: hk.Token, Purpose: PurposeCustomerResume, TargetScope: "h5", NewVisitorID: "visit_c"}); !errors.Is(err, ErrCredentialUsed) {
		t.Fatalf("replay must be ErrCredentialUsed, got %v", err)
	}

	// 撤销路径。
	h2, _ := svc.CreateAttempt(ctx, AttemptInput{ParticipationID: id, VisitorID: "visit_b", Platform: "douyin", Method: MethodH5})
	hk2, _ := svc.BeginHandoff(ctx, HandoffInput{ParticipationID: id, VisitorID: "visit_b", AttemptID: h2.ID})
	if err := svc.Revoke(ctx, id, "visit_b", hk2.Credential.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := svc.Redeem(ctx, RedeemInput{Token: hk2.Token, Purpose: PurposeCustomerResume, TargetScope: "h5", NewVisitorID: "visit_c"}); !errors.Is(err, ErrCredentialRevoked) {
		t.Fatalf("revoked redeem must fail, got %v", err)
	}

	// 过期路径(把时钟拨到凭据过期后)。
	svc.opts.Now = func() time.Time { return time.Date(2026, 10, 14, 12, 11, 0, 0, time.UTC) } // >10min TTL
	h3, _ := svc.CreateAttempt(ctx, AttemptInput{ParticipationID: id, VisitorID: "visit_b", Platform: "douyin", Method: MethodH5})
	hk3, _ := svc.BeginHandoff(ctx, HandoffInput{ParticipationID: id, VisitorID: "visit_b", AttemptID: h3.ID})
	svc.opts.Now = func() time.Time { return time.Date(2026, 10, 14, 12, 21, 0, 0, time.UTC) }
	if _, err := svc.Redeem(ctx, RedeemInput{Token: hk3.Token, Purpose: PurposeCustomerResume, TargetScope: "h5", NewVisitorID: "visit_c"}); !errors.Is(err, ErrCredentialExpired) {
		t.Fatalf("expired redeem must fail, got %v", err)
	}

	// 目标不符(customer_resume 凭据拿去 staff 通道)。
	h4, _ := svc.CreateAttempt(ctx, AttemptInput{ParticipationID: id, VisitorID: "visit_b", Platform: "douyin", Method: MethodH5})
	hk4, _ := svc.BeginHandoff(ctx, HandoffInput{ParticipationID: id, VisitorID: "visit_b", AttemptID: h4.ID})
	if _, err := svc.Redeem(ctx, RedeemInput{Token: hk4.Token, Purpose: PurposeStaffVerify, TargetScope: tenant}); !errors.Is(err, ErrTargetMismatch) {
		t.Fatalf("purpose mismatch must fail, got %v", err)
	}

	// 伪造/未知 token:统一 ErrNotFound(防枚举,不区分形状)。
	if _, err := svc.Redeem(ctx, RedeemInput{Token: "ptx_0000000000000000000000.AAAA", Purpose: PurposeCustomerResume, TargetScope: "h5", NewVisitorID: "v"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown token must be ErrNotFound, got %v", err)
	}
	if _, err := svc.Redeem(ctx, RedeemInput{Token: "garbage", Purpose: PurposeCustomerResume, TargetScope: "h5", NewVisitorID: "v"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("garbage token must be ErrNotFound, got %v", err)
	}

	// staff_verify 凭据 + staff 兑换 → awaiting_review(见 MarkFulfilled 测试)。
	// 时钟拨回窗口内。
	svc.opts.Now = func() time.Time { return time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC) }
	staffTok := mustStaffToken(t, svc, tenant, id, "visit_b")
	if _, err := svc.Redeem(ctx, RedeemInput{Token: staffTok, Purpose: PurposeStaffVerify, TargetScope: tenant, StaffTenantID: tenant}); err != nil {
		t.Fatalf("staff redeem: %v", err)
	}
	// staff 凭据被错误租户兑换:目标不符。
	staffTok2 := mustStaffToken(t, svc, tenant, id, "visit_b")
	if _, err := svc.Redeem(ctx, RedeemInput{Token: staffTok2, Purpose: PurposeStaffVerify, TargetScope: "tnt_other", StaffTenantID: "tnt_other"}); !errors.Is(err, ErrTargetMismatch) {
		t.Fatalf("cross-tenant staff redeem must fail, got %v", err)
	}
}

func TestCancellation(t *testing.T) {
	svc, tenant, store, campaign := newService(t, nil)
	ctx := context.Background()
	started, _ := svc.Start(ctx, startInput(tenant, store, campaign, "visit_a", "idem_1", "cp"))
	id := started.Participation.ID

	if _, err := svc.Cancel(ctx, id, "visit_b"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign cancel must fail, got %v", err)
	}
	p, err := svc.Cancel(ctx, id, "visit_a")
	if err != nil || p.Status != StatusCanceled {
		t.Fatalf("cancel: %+v err=%v", p, err)
	}
	// 终态后不能再交接。
	if _, err := svc.CreateAttempt(ctx, AttemptInput{ParticipationID: id, VisitorID: "visit_a", Platform: "douyin", Method: MethodH5}); err == nil {
		t.Fatal("canceled participation must not open attempts")
	}
}

func TestExpiredWindowBlocksHandoff(t *testing.T) {
	svc, tenant, store, campaign := newService(t, nil)
	ctx := context.Background()
	started, err := svc.Start(ctx, startInput(tenant, store, campaign, "visit_a", "idem_1", "cp"))
	if err != nil {
		t.Fatalf("Start within window: %v", err)
	}
	// 时钟拨过有效期。
	svc.opts.Now = func() time.Time { return time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC) }
	if _, err := svc.CreateAttempt(ctx, AttemptInput{ParticipationID: started.Participation.ID, VisitorID: "visit_a", Platform: "douyin", Method: MethodH5}); !errors.Is(err, ErrFrozen) {
		t.Fatalf("expired window must block handoff, got %v", err)
	}
	// 读取仍允许(顾客能看见发生了什么)。
	if _, err := svc.GetForVisitor(ctx, started.Participation.ID, "visit_a"); err != nil {
		t.Fatalf("read after expiry must stay allowed, got %v", err)
	}
	// 过窗后的新开始直接拒绝(fail-closed)。
	if _, err := svc.Start(ctx, startInput(tenant, store, campaign, "visit_a", "idem_2", "cp")); !errors.Is(err, ErrFrozen) {
		t.Fatalf("start on expired window must fail, got %v", err)
	}
}

func TestCleanupAnonymizesAndDeletes(t *testing.T) {
	svc, tenant, store, campaign := newService(t, nil)
	svc.opts.Now = func() time.Time { return time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC) }
	ctx := context.Background()
	started, _ := svc.Start(ctx, startInput(tenant, store, campaign, "visit_a", "idem_1", "cp"))
	id := started.Participation.ID
	h, _ := svc.CreateAttempt(ctx, AttemptInput{ParticipationID: id, VisitorID: "visit_a", Platform: "douyin", Method: MethodH5})
	hk, _ := svc.BeginHandoff(ctx, HandoffInput{ParticipationID: id, VisitorID: "visit_a", AttemptID: h.ID})
	_, _ = svc.Redeem(ctx, RedeemInput{Token: hk.Token, Purpose: PurposeCustomerResume, TargetScope: "h5", NewVisitorID: "visit_b"})
	if _, err := svc.Cancel(ctx, id, "visit_b"); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	// 参与保留窗内不动;已过期的凭据按 24h 宽限删除。
	anon, deleted, err := svc.Cleanup(ctx, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), 90*24*time.Hour)
	if err != nil || anon != 0 || deleted == 0 {
		t.Fatalf("first sweep: anon=%d deleted=%d err=%v", anon, deleted, err)
	}
	// 超保留窗:匿名化(参与还在,visitor 置空)。
	anon, deleted, err = svc.Cleanup(ctx, time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC), 90*24*time.Hour)
	if err != nil || anon == 0 || deleted != 0 {
		t.Fatalf("second sweep: anon=%d deleted=%d err=%v", anon, deleted, err)
	}
	p, err := svc.GetForStaff(ctx, id, StaffView{TenantID: tenant, Role: "org_owner"})
	if err != nil {
		t.Fatalf("record survives anonymization: %v", err)
	}
	if p.VisitorID != "" {
		t.Fatalf("visitor id must be anonymized, got %q", p.VisitorID)
	}
}

// T14 复审补强:取消是显式终态—— outstanding 凭据不得把已取消参与
// 「复活」到新设备;已兑现参与同样拒绝重演兑换。
func TestRedeemRefusesTerminalParticipation(t *testing.T) {
	svc, tenant, store, campaign := newService(t, nil)
	svc.opts.Now = func() time.Time { return time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC) }
	ctx := context.Background()
	started, _ := svc.Start(ctx, startInput(tenant, store, campaign, "visit_a", "idem_1", "cp"))
	id := started.Participation.ID

	h, _ := svc.CreateAttempt(ctx, AttemptInput{ParticipationID: id, VisitorID: "visit_a", Platform: "douyin", Method: MethodH5})
	hk, err := svc.BeginHandoff(ctx, HandoffInput{ParticipationID: id, VisitorID: "visit_a", AttemptID: h.ID})
	if err != nil {
		t.Fatalf("handoff: %v", err)
	}
	if _, err := svc.Cancel(ctx, id, "visit_a"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := svc.Redeem(ctx, RedeemInput{Token: hk.Token, Purpose: PurposeCustomerResume, TargetScope: "h5", NewVisitorID: "visit_b"}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("redeem on canceled must be ErrInvalidTransition, got %v", err)
	}
	// 凭据未被消费:幂等守卫在消费前判定(重放矩阵不互相干扰)。
	if _, err := svc.Redeem(ctx, RedeemInput{Token: hk.Token, Purpose: PurposeCustomerResume, TargetScope: "h5", NewVisitorID: "visit_c"}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("second attempt must also refuse, got %v", err)
	}
}
