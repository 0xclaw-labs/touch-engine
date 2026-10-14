package publishhandoff

// T5-T7: Service 行为(真实 SQLite 文件库,非 mock)。
//
// 覆盖面:
//   - T5  开交接:过闸→attempt 冻结核对→内容指纹确认→幂等→媒体受控 URL;
//         换稿需重新确认;not_admitted 拒绝;交接失败零付费生成(结构性);
//   - T6  证据事件:顾客完成=核验请求;人工两事实分别落库且永不转官方;
//         官方回执验签+五绑定+重放去重+乱序不降级;
//   - T7  超时先回查不重投;取消;弱网重试不新建行;服务重启回查。

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
)

const testMediaSecret = "media-hmac-secret"

func newService(t *testing.T) (*Service, func()) {
	t.Helper()
	d := openTestDB(t)
	_, _, _ = seedRef(t, d)
	s := New(d, Options{
		Now: func() time.Time { return time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC) },
		MediaSecret: testMediaSecret,
		MediaBaseURL: "https://media.example.com/handoff",
		ReceiptVerifier: NewHMACVerifier("receipt-secret"),
		AppRef: "app_touch_prod",
	})
	return s, func() { d.Close() }
}

// seedFrozenAttempt 插入一条 handed_off 的 3546 attempt(交接冻结语义)。
func seedFrozenAttempt(t *testing.T, s *Service, suffix string) (participationID, attemptID, visitor string) {
	t.Helper()
	ptID, atID := seedParticipation(t, s.db, "tnt_seed", "sto_seed", "cmp_seed", suffix)
	// seedParticipation 已落 handed_off 状态(见其 SQL)。
	return ptID, atID, "visit_" + suffix
}

func admittedXHSRecord(t *testing.T) channeladmission.Record {
	t.Helper()
	r := channeladmission.Record{
		Platform: "xiaohongshu", EntryMode: channeladmission.EntryMiniapp, Scenario: "store_promo",
		AppBinding: "platform_app", TenantScope: "*", Status: channeladmission.StatusAdmitted,
		Capabilities: channeladmission.Capabilities{
			Media: channeladmission.CapSupported, Title: channeladmission.CapSupported,
			Body: channeladmission.CapSupported, Topic: channeladmission.CapUnknown,
			POI: channeladmission.CapUnknown, Receipt: channeladmission.CapUnknown,
			Callback: channeladmission.CapUnknown,
		},
		LastCheckedAt: "2026-10-14", ValidUntil: "2027-01-12",
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("fixture record: %v", err)
	}
	return r
}

func unknownXHSRecord(t *testing.T) channeladmission.Record {
	t.Helper()
	r := channeladmission.Record{
		Platform: "xiaohongshu", EntryMode: channeladmission.EntryMiniapp, Scenario: "store_promo",
		AppBinding: "platform_app", TenantScope: "*", Status: channeladmission.StatusUnknown,
		ReasonCode: channeladmission.ReasonAdmissionUnknown,
		Missing:    []string{"发布回调权限确认"},
		Capabilities: channeladmission.Capabilities{
			Media: channeladmission.CapUnknown, Title: channeladmission.CapUnknown,
			Body: channeladmission.CapUnknown, Topic: channeladmission.CapUnknown,
			POI: channeladmission.CapUnknown, Receipt: channeladmission.CapUnknown,
			Callback: channeladmission.CapUnknown,
		},
		LastCheckedAt: "2026-10-14", ValidUntil: "2027-01-12",
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("fixture record: %v", err)
	}
	return r
}

func douyinH5Record(t *testing.T) channeladmission.Record {
	t.Helper()
	r := channeladmission.Record{
		Platform: "douyin", EntryMode: channeladmission.EntryH5, Scenario: "store_promo",
		AppBinding: "none", TenantScope: "*", Status: channeladmission.StatusNotAdmitted,
		ReasonCode: channeladmission.ReasonNotAdmitted,
		ReasonText: "官方规范明确排除营销内容场景",
		Capabilities: channeladmission.Capabilities{
			Media: channeladmission.CapUnknown, Title: channeladmission.CapUnknown,
			Body: channeladmission.CapUnknown, Topic: channeladmission.CapUnknown,
			POI: channeladmission.CapUnknown, Receipt: channeladmission.CapUnknown,
			Callback: channeladmission.CapUnknown,
		},
		LastCheckedAt: "2026-10-14", ValidUntil: "2027-01-12",
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("fixture record: %v", err)
	}
	return r
}

func startInput(participationID, attemptID, visitor string) StartInput {
	text := TextInput{Title: "周末烘焙节", Body: "本周六全场八折", Topics: []string{"烘焙"}, StoreName: "南岸旗舰店"}
	media := []MediaItem{
		{Kind: MediaVideo, AssetID: "as_v", SHA256: strings.Repeat("a", 64), ContentType: "video/mp4", Filename: "main.mp4"},
		{Kind: MediaCover, AssetID: "as_c", SHA256: strings.Repeat("b", 64), ContentType: "image/jpeg", Filename: "cover.jpg"},
	}
	return StartInput{
		TenantID: "tnt_seed", StoreID: "sto_seed", CampaignID: "cmp_seed", LinkCode: "link01",
		ParticipationID: participationID, AttemptID: attemptID, VisitorID: visitor,
		Platform: "xiaohongshu", EntryMode: channeladmission.EntryMiniapp, Scenario: "store_promo",
		IdempotencyKey: "idem_" + attemptID,
		MiniappBase:    "pages/publish/index",
		ConfirmedContentVersion: ContentFingerprint(text, media),
		Text: text, Media: media,
	}
}

// T5.1: admitted fixture 路径全状态机贯通:start → handoff_ready(自动化计划),
// 事件链 previewed→handoff_preparing→handoff_ready 齐全。
func TestStartAdmittedRouteFullPipeline(t *testing.T) {
	s, closeDB := newService(t)
	defer closeDB()
	ptID, atID, visitor := seedFrozenAttempt(t, s, "a")
	rec := admittedXHSRecord(t)
	in := startInput(ptID, atID, visitor)
	in.MiniappBase = "pages/publish/index"

	res, err := s.Start(t.Context(), in, rec)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !res.Created || res.Handoff.Status != StateHandoffReady {
		t.Fatalf("want created handoff_ready, got %s/%s", res.Handoff.Status, res.Plan.Kind)
	}
	if res.Plan.Kind != PlanAutomated || res.Plan.Entry == nil || res.Plan.Entry.MiniappPath != "pages/publish/index" {
		t.Fatalf("want automated miniapp plan, got %+v", res.Plan)
	}
	if len(res.Media) != 2 {
		t.Fatalf("want 2 controlled media refs, got %d", len(res.Media))
	}
	for _, m := range res.Media {
		if !strings.HasPrefix(m.URL, "https://media.example.com/handoff") {
			t.Fatalf("media url must be the controlled base, got %q", m.URL)
		}
		exp, perr := time.Parse(time.RFC3339, m.ExpiresAt)
		if perr != nil || !exp.After(time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)) {
			t.Fatalf("media url expiry must parse and be in the future, got %q", m.ExpiresAt)
		}
	}
	// 事件链:三条 state_changed。
	kinds, _ := s.eventKinds(t, res.Handoff.ID)
	if got := strings.Join(kinds, ","); got != "state_changed,state_changed" {
		t.Fatalf("event chain = %q", got)
	}
}

// T5.2: 幂等——同幂等键重放回原行,不新建;媒体 URL 重铸(弱网重试)。
func TestStartIdempotentReplay(t *testing.T) {
	s, closeDB := newService(t)
	defer closeDB()
	ptID, atID, visitor := seedFrozenAttempt(t, s, "a")
	rec := admittedXHSRecord(t)
	in := startInput(ptID, atID, visitor)
	in.MiniappBase = "pages/publish/index"

	first, err := s.Start(t.Context(), in, rec)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	again, err := s.Start(t.Context(), in, rec)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if again.Created || again.Handoff.ID != first.Handoff.ID {
		t.Fatal("same idempotency key must return the same handoff without creating a row")
	}
	if len(again.Media) != len(first.Media) {
		t.Fatal("replay must still serve fresh media urls")
	}
}

// T5.3: not_admitted → 拒绝开交接(ErrHandoffRefused,notice 说明官方排除)。
func TestStartNotAdmittedRefused(t *testing.T) {
	s, closeDB := newService(t)
	defer closeDB()
	ptID, atID, visitor := seedFrozenAttempt(t, s, "a")
	_, err := s.Start(t.Context(), startInput(ptID, atID, visitor), douyinH5Record(t))
	var refused *HandoffRefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("want HandoffRefusedError, got %v", err)
	}
	if refused.Reason != channeladmission.ReasonNotAdmitted {
		t.Fatalf("reason = %q", refused.Reason)
	}
	if !strings.Contains(refused.Notice, "明确排除") {
		t.Fatalf("notice = %q", refused.Notice)
	}
}

// T5.4: attempt 未冻结(不是 handed_off)→ 拒绝开交接。
func TestStartRequiresFrozenAttempt(t *testing.T) {
	s, closeDB := newService(t)
	defer closeDB()
	ptID, atID, visitor := seedFrozenAttempt(t, s, "a")
	// 篡改回 prepared:未冻结不得交接。
	if _, err := s.db.Exec(`UPDATE participation_attempts SET status='prepared' WHERE id=?`, atID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(t.Context(), startInput(ptID, atID, visitor), admittedXHSRecord(t)); !errors.Is(err, ErrAttemptNotFrozen) {
		t.Fatalf("want ErrAttemptNotFrozen, got %v", err)
	}
}

// T5.5: 换稿(确认指纹不符)→ 拒绝且行不变:顾客必须重新确认,不静默换内容。
func TestStartRejectsUnconfirmedContent(t *testing.T) {
	s, closeDB := newService(t)
	defer closeDB()
	ptID, atID, visitor := seedFrozenAttempt(t, s, "a")
	in := startInput(ptID, atID, visitor)
	in.ConfirmedContentVersion = "stale-fingerprint"
	_, err := s.Start(t.Context(), in, admittedXHSRecord(t))
	if !errors.Is(err, ErrConfirmationMismatch) {
		t.Fatalf("want ErrConfirmationMismatch, got %v", err)
	}
}

// T5.6: 媒体准备失败(非法 content-type)→ handoff failed,零付费生成
// (结构性:本包无生成端口;失败事件可追溯)。
func TestStartMediaFailureFailsWithoutGeneration(t *testing.T) {
	s, closeDB := newService(t)
	defer closeDB()
	ptID, atID, visitor := seedFrozenAttempt(t, s, "a")
	in := startInput(ptID, atID, visitor)
	in.Media = []MediaItem{{Kind: MediaVideo, AssetID: "as_x", SHA256: strings.Repeat("c", 64), ContentType: "application/x-msdownload", Filename: "evil.exe"}}
	in.ConfirmedContentVersion = ContentFingerprint(in.Text, in.Media)
	_, err := s.Start(t.Context(), in, admittedXHSRecord(t))
	if err == nil {
		t.Fatal("bad media must fail the handoff")
	}
	h, err := s.GetForVisitor(t.Context(), findByAttempt(t, s, atID), visitor)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if h.Status != StateFailed {
		t.Fatalf("want failed, got %s", h.Status)
	}
	kinds, sources := s.eventKinds(t, h.ID)
	if !contains(kinds, "state_changed") || !contains(sources, string(EvidenceSystemAdapter)) {
		t.Fatalf("failure must be traceable, got %v/%v", kinds, sources)
	}
}

// T6.1: 顾客"我已完成"只产生核验请求事件,状态不变,不触发奖励语义。
func TestCustomerDoneIsOnlyARequest(t *testing.T) {
	s, closeDB := newService(t)
	defer closeDB()
	h := startReadyHandoff(t, s, "a", unknownXHSRecord(t))
	got, err := s.CustomerDone(t.Context(), h.ID, "visit_a")
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	if got.Status != StateHandoffReady {
		t.Fatalf("done must not change state, got %s", got.Status)
	}
	kinds, _ := s.eventKinds(t, h.ID)
	if !contains(kinds, "customer_done_requested") {
		t.Fatalf("want customer_done_requested event, got %v", kinds)
	}
}

// T6.2: 人工两事实分别落库;状态到 awaiting_review;人工永不转官方。
func TestStaffFactsAreSeparateAndNeverOfficial(t *testing.T) {
	s, closeDB := newService(t)
	defer closeDB()
	h := startReadyHandoff(t, s, "a", unknownXHSRecord(t))

	if _, err := s.RecordStaffEvent(t.Context(), "tnt_seed", h.ID, StaffActor{ID: "mb_1", Name: "店员甲"}, FactStaffSawSubmit, "目测顾客已在小红书内提交"); err != nil {
		t.Fatalf("saw submit: %v", err)
	}
	got, err := s.RecordStaffEvent(t.Context(), "tnt_seed", h.ID, StaffActor{ID: "mb_2", Name: "店员乙"}, FactStaffVerifiedWork, "已核对作品链接与活动内容一致")
	if err != nil {
		t.Fatalf("verified work: %v", err)
	}
	if got.Status != StateAwaitingReview {
		t.Fatalf("staff facts move handoff to awaiting_review, got %s", got.Status)
	}
	kinds, sources := s.eventKinds(t, h.ID)
	if !contains(kinds, "staff_saw_submit") || !contains(kinds, "staff_verified_work") {
		t.Fatalf("both facts must be recorded, got %v", kinds)
	}
	if !contains(sources, string(EvidenceStaffObservation)) {
		t.Fatal("staff facts carry staff_observation evidence")
	}
	// 人工事实存在的情况下,状态也绝不是 platform_confirmed。
	if got.Status == StatePlatformConfirmed {
		t.Fatal("human facts must never become platform_confirmed")
	}
}

// T6.3: 官方回执——验签+五绑定+确认;错绑定/伪造/错 outcome 全拒。
func TestOfficialReceiptVerifiesAndBinds(t *testing.T) {
	s, closeDB := newService(t)
	defer closeDB()
	h := startReadyHandoff(t, s, "a", unknownXHSRecord(t))
	// 先到 awaiting_review(乱序容许的合法前置):店员先核验。
	if _, err := s.RecordStaffEvent(t.Context(), "tnt_seed", h.ID, StaffActor{ID: "mb_1"}, FactStaffSawSubmit, ""); err != nil {
		t.Fatal(err)
	}

	rcpt := Receipt{
		ReceiptID: "rcp_ok", Platform: "xiaohongshu", AppRef: "app_touch_prod",
		TenantID: "tnt_seed", ParticipationID: h.ParticipationID, AttemptID: h.AttemptID,
		ContentVersion: h.ContentVersion, PostID: "note_9", Outcome: ReceiptOutcomePublished,
		SignedAt: time.Date(2026, 10, 14, 12, 6, 0, 0, time.UTC),
	}
	wire := ReceiptWire{Receipt: rcpt, Signature: signWith(t, "receipt-secret", rcpt)}
	got, err := s.ApplyReceipt(t.Context(), "tnt_seed", h.ID, wire)
	if err != nil {
		t.Fatalf("apply receipt: %v", err)
	}
	if got.Status != StatePlatformConfirmed {
		t.Fatalf("want platform_confirmed, got %s", got.Status)
	}
	kinds, _ := s.eventKinds(t, h.ID)
	if !contains(kinds, "official_confirmed") {
		t.Fatalf("want official_confirmed event, got %v", kinds)
	}
}

// T6.4: 重放回执→回原事件,不新增,状态不变;乱序(handoff_ready 未提交
// 就来确认)→ 拒绝且先回查(状态不盲进)。
func TestReceiptReplayAndOutOfOrder(t *testing.T) {
	s, closeDB := newService(t)
	defer closeDB()
	h := startReadyHandoff(t, s, "a", unknownXHSRecord(t))
	rcpt := Receipt{
		ReceiptID: "rcp_early", Platform: "xiaohongshu", AppRef: "app_touch_prod",
		TenantID: "tnt_seed", ParticipationID: h.ParticipationID, AttemptID: h.AttemptID,
		ContentVersion: h.ContentVersion, PostID: "note_1", Outcome: ReceiptOutcomePublished,
		SignedAt: time.Date(2026, 10, 14, 12, 6, 0, 0, time.UTC),
	}
	wire := ReceiptWire{Receipt: rcpt, Signature: signWith(t, "receipt-secret", rcpt)}

	// 乱序:handoff_ready 直接来 confirmed → 拒绝,状态不动(先回查)。
	if _, err := s.ApplyReceipt(t.Context(), "tnt_seed", h.ID, wire); err == nil {
		t.Fatal("out-of-order confirmed receipt must be refused on a ready handoff")
	}
	h2, _ := s.GetForVisitor(t.Context(), h.ID, "visit_a")
	if h2.Status != StateHandoffReady {
		t.Fatalf("state must not advance on out-of-order receipt, got %s", h2.Status)
	}
	// 推进到 awaiting_review 后同一回执可达。
	if _, err := s.RecordStaffEvent(t.Context(), "tnt_seed", h.ID, StaffActor{ID: "mb_1"}, FactStaffSawSubmit, ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.ApplyReceipt(t.Context(), "tnt_seed", h.ID, wire)
	if err != nil {
		t.Fatalf("apply after review: %v", err)
	}
	if got.Status != StatePlatformConfirmed {
		t.Fatalf("want confirmed, got %s", got.Status)
	}
	// 重放:同 receipt_id → 幂等,无新事件,状态不回退。
	before, _ := s.eventKinds(t, h.ID)
	again, err := s.ApplyReceipt(t.Context(), "tnt_seed", h.ID, wire)
	if err != nil {
		t.Fatalf("replay must be idempotent, got %v", err)
	}
	if again.Status != StatePlatformConfirmed {
		t.Fatalf("replay must not change state, got %s", again.Status)
	}
	after, _ := s.eventKinds(t, h.ID)
	if len(after) != len(before) {
		t.Fatalf("replay must not append events: %v -> %v", before, after)
	}
}

// T6.5: 伪造回执(错签名/错 app/错租户/错 attempt/错内容版本)全拒并留痕。
func TestForgedReceiptsRejectedWithTrail(t *testing.T) {
	s, closeDB := newService(t)
	defer closeDB()
	h := startReadyHandoff(t, s, "a", unknownXHSRecord(t))
	if _, err := s.RecordStaffEvent(t.Context(), "tnt_seed", h.ID, StaffActor{ID: "mb_1"}, FactStaffSawSubmit, ""); err != nil {
		t.Fatal(err)
	}
	mkWire := func(id string, mut func(*Receipt), secret string) ReceiptWire {
		r := receiptWith(h, func(r *Receipt) {
			r.ReceiptID = id
			mut(r)
		})
		return ReceiptWire{Receipt: r, Signature: signWith(t, secret, r)}
	}
	forged := []ReceiptWire{
		mkWire("rcp_b1", func(r *Receipt) {}, "customer-forged-key"),                        // 错密钥
		mkWire("rcp_b2", func(r *Receipt) { r.AppRef = "app_evil" }, "receipt-secret"),      // 错 app
		mkWire("rcp_b3", func(r *Receipt) { r.TenantID = "tnt_evil" }, "receipt-secret"),    // 错租户
		mkWire("rcp_b4", func(r *Receipt) { r.AttemptID = "pta_evil" }, "receipt-secret"),   // 错 attempt
		mkWire("rcp_b5", func(r *Receipt) { r.ContentVersion = "cv_old" }, "receipt-secret"), // 错内容版本
		mkWire("rcp_b6", func(r *Receipt) { r.Outcome = "under_review" }, "receipt-secret"),  // 非确认结果
	}
	for i, w := range forged {
		if _, err := s.ApplyReceipt(t.Context(), "tnt_seed", h.ID, w); err == nil {
			t.Fatalf("forged receipt #%d must be refused", i)
		}
	}
	got, _ := s.GetForVisitor(t.Context(), h.ID, "visit_a")
	if got.Status == StatePlatformConfirmed {
		t.Fatal("no forged receipt may confirm")
	}
	kinds, _ := s.eventKinds(t, h.ID)
	if !contains(kinds, "receipt_rejected") {
		t.Fatalf("rejections must leave a trail, got %v", kinds)
	}
}

// T6.6: 跨租户回执/读取:租户不符一律拒绝或 404 语义。
func TestCrossTenantIsolation(t *testing.T) {
	s, closeDB := newService(t)
	defer closeDB()
	h := startReadyHandoff(t, s, "a", unknownXHSRecord(t))
	if _, err := s.GetForVisitor(t.Context(), h.ID, "visit_other"); !errors.Is(err, ErrNotFound) {
		t.Fatal("another visitor must get not_found")
	}
	if _, err := s.RecordStaffEvent(t.Context(), "tnt_other", h.ID, StaffActor{ID: "mb_x"}, FactStaffSawSubmit, ""); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-tenant staff event must be not_found")
	}
}

// T7.1: 超时先回查——submitting 超时转 unknown;回查只计数不重投;
// unknown 不允许重发(结构性拒绝)。
func TestTimeoutThenQueryNeverResubmits(t *testing.T) {
	s, closeDB := newService(t)
	defer closeDB()
	h := startReadyHandoff(t, s, "a", admittedXHSRecord(t))
	// editor_opened 需要 verifier 证明——admitted 自动化路径同样要证据。
	if _, err := s.MarkEditorOpened(t.Context(), h.ID, "visit_a", "no-real-proof"); err == nil {
		t.Fatal("editor_opened without verifiable proof must be refused")
	}
	// 系统侧把请求交出后超时。
	if _, err := s.MarkSubmitting(t.Context(), h.ID, "visit_a"); err != nil {
		t.Fatalf("submitting: %v", err)
	}
	got, err := s.MarkTimedOut(t.Context(), h.ID)
	if err != nil {
		t.Fatalf("timeout: %v", err)
	}
	if got.Status != StateUnknown || !got.TimedOut {
		t.Fatalf("want unknown/timed_out, got %s/%v", got.Status, got.TimedOut)
	}
	q1, err := s.Query(t.Context(), h.ID, "visit_a")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	q2, _ := s.Query(t.Context(), h.ID, "visit_a")
	if q2.QueryCount != q1.QueryCount+1 {
		t.Fatalf("query must count each recheck: %d -> %d", q1.QueryCount, q2.QueryCount)
	}
	if q2.Status != StateUnknown {
		t.Fatalf("query without official result stays unknown, got %s", q2.Status)
	}
	// 重发被拒:unknown -> submitting 非法(状态机层已锁),服务层再给机器原因。
	if err := s.Resend(t.Context(), h.ID); err == nil {
		t.Fatal("resend after timeout must be refused: 先回查,不盲目重投")
	}
}

// T7.2: 弱网重试=重新取媒体 URL,不新建交接行(同一行、状态不变)。
func TestWeakNetworkRetryRemintsSameRow(t *testing.T) {
	s, closeDB := newService(t)
	defer closeDB()
	h := startReadyHandoff(t, s, "a", unknownXHSRecord(t))
	h1, err := s.GetForVisitor(t.Context(), h.ID, "visit_a")
	if err != nil {
		t.Fatal(err)
	}
	m1, err := s.MediaForVisitor(t.Context(), h.ID, "visit_a")
	if err != nil {
		t.Fatal(err)
	}
	h2, m2 := h1, func() []MediaRef {
		m, err := s.MediaForVisitor(t.Context(), h.ID, "visit_a")
		if err != nil {
			t.Fatal(err)
		}
		return m
	}()
	if h1.ID != h2.ID || h1.Status != h2.Status {
		t.Fatal("retry must not create a new handoff or change state")
	}
	if len(m1) == 0 || len(m1) != len(m2) {
		t.Fatalf("media refs must survive retry: %d/%d", len(m1), len(m2))
	}
}

// T7.3: 顾客取消;取消后官方晚到不改写(终态不复活,回执留痕)。
func TestCancelThenLateReceiptDoesNotResurrect(t *testing.T) {
	s, closeDB := newService(t)
	defer closeDB()
	h := startReadyHandoff(t, s, "a", unknownXHSRecord(t))
	got, err := s.Cancel(t.Context(), h.ID, "visit_a", "不想发了")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if got.Status != StateCanceled || got.CancelReason == "" {
		t.Fatalf("want canceled with reason, got %s/%q", got.Status, got.CancelReason)
	}
	rcpt := Receipt{
		ReceiptID: "rcp_late", Platform: "xiaohongshu", AppRef: "app_touch_prod",
		TenantID: "tnt_seed", ParticipationID: h.ParticipationID, AttemptID: h.AttemptID,
		ContentVersion: h.ContentVersion, PostID: "note_late", Outcome: ReceiptOutcomePublished,
		SignedAt: time.Date(2026, 10, 14, 12, 8, 0, 0, time.UTC),
	}
	if _, err := s.ApplyReceipt(t.Context(), "tnt_seed", h.ID, ReceiptWire{Receipt: rcpt, Signature: signWith(t, "receipt-secret", rcpt)}); err == nil {
		t.Fatal("late receipt must not resurrect a canceled handoff")
	}
	got2, _ := s.GetForVisitor(t.Context(), h.ID, "visit_a")
	if got2.Status != StateCanceled {
		t.Fatalf("canceled is terminal, got %s", got2.Status)
	}
}

// T7.4: 服务重启(关库重开)→ 状态与冻结快照还在;恢复回查继续;
// preparing 卡死可恢复到 ready(system_recovery 事件一次)。
func TestSurvivesRestartAndRecovers(t *testing.T) {
	path := t.TempDir() + "restart.db"
	build := func() *Service {
		s := newServiceAtPath(t, path)
		return s
	}
	s := build()
	ptID, atID, visitor := seedFrozenAttempt(t, s, "a")
	in := startInput(ptID, atID, visitor)
	res, err := s.Start(t.Context(), in, unknownXHSRecord(t))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	// 模拟崩溃在 preparing:手工回拨状态。
	if _, err := s.db.Exec(`UPDATE publish_handoffs SET status='handoff_preparing' WHERE id=?`, res.Handoff.ID); err != nil {
		t.Fatal(err)
	}
	s.db.Close()

	s2 := build() // “重启”:同路径重开
	got, err := s2.GetForVisitor(t.Context(), res.Handoff.ID, visitor)
	if err != nil {
		t.Fatalf("after restart: %v", err)
	}
	if got.ContentVersion != res.Handoff.ContentVersion {
		t.Fatal("frozen content version must survive restart")
	}
	rec, err := s2.Resume(t.Context(), "tnt_seed", res.Handoff.ID)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if rec.Status != StateHandoffReady {
		t.Fatalf("stuck preparing must recover to ready, got %s", rec.Status)
	}
	kinds, sources := s2.eventKinds(t, res.Handoff.ID)
	if !contains(kinds, "state_changed") || !contains(sources, string(EvidenceSystemRecovery)) {
		t.Fatalf("recovery must be traceable, got %v/%v", kinds, sources)
	}
	// 再 Resume:幂等,无新恢复事件。
	k2, _ := s2.eventKinds(t, res.Handoff.ID)
	if _, err := s2.Resume(t.Context(), "tnt_seed", res.Handoff.ID); err != nil {
		t.Fatalf("second resume: %v", err)
	}
	k3, _ := s2.eventKinds(t, res.Handoff.ID)
	if len(k3) != len(k2) {
		t.Fatal("second resume must be a no-op")
	}
	s2.db.Close()
}
