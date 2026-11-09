package publishhandoff

// service2_test.go: touch-handoff/v2 服务面——Adapter 接口契约、事件六层
// source+event_id 去重、StartAction(payload 分流/幂等冲突/gate-first 零落行)、
// Submit/Query(unsupported 诚实)、平台回调验签到 platform_confirm。
//
// 冻结命名(docs/contracts/publish-handoff/v2/handoff-contract.md):
//   - 事件六层 kind:customer_click/wake_attempt/media_handoff/
//     customer_self_report/staff_confirm/platform_confirm;
//   - Adapter 四方法 Prepare/Submit/Query/VerifyCallback,不适用=ErrUnsupported;
//   - plan.kind 五值(T2 已锁)。

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
)

// ---- fixtures -----------------------------------------------------------------

func nowV2() time.Time { return time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC) }

// newV2Service opens a fresh database wired for v2 tests (seed refs included).
func newV2Service(t *testing.T) (*Service, string) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	tenantID, storeID := "tnt_v2", "sto_v2"
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT OR IGNORE INTO tenants (id, name, created_at) VALUES (?,?,?)`, []any{tenantID, "v2-tenant", "2026-11-10T00:00:00Z"}},
		{`INSERT OR IGNORE INTO stores (id, tenant_id, name, created_at, updated_at) VALUES (?,?,?,?,?)`, []any{storeID, tenantID, "v2-store", "2026-11-10T00:00:00Z", "2026-11-10T00:00:00Z"}},
	} {
		if _, err := d.Exec(q.sql, q.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	s := New(d, Options{
		Now:             nowV2,
		MediaSecret:     testMediaSecret,
		MediaBaseURL:    "https://media.example.com/handoff",
		ReceiptVerifier: NewHMACVerifier("v2-receipt-secret"),
		AppRef:          "app_touch_prod",
	})
	return s, tenantID
}

// seedFrozenAttemptV2 seeds a campaign + participation + handed_off attempt
// (3546 冻结语义) so StartAction may carry participation/attempt.
func (s *Service) seedFrozenAttemptV2(participationID, attemptID string) error {
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT OR IGNORE INTO campaigns (id, tenant_id, title, status, store_id, created_at, updated_at)
		  VALUES ('cmp_v2','tnt_v2','v2-campaign','active','sto_v2','2026-11-10T00:00:00Z','2026-11-10T00:00:00Z')`, nil},
		{`INSERT OR IGNORE INTO participations (id, tenant_id, store_id, campaign_id, link_code, touchpoint_source,
		  rule_version, visitor_id, status, idempotency_key, starts_at, expires_at, created_at, updated_at)
		  VALUES (?,'tnt_v2','sto_v2','cmp_v2','c_v2','nfc', 0,'vis_v2','pending','idem-p-v2',
		  '2026-11-10T00:00:00Z','2027-11-10T00:00:00Z','2026-11-10T00:00:00Z','2026-11-10T00:00:00Z')`, []any{participationID}},
		{`INSERT OR IGNORE INTO participation_attempts (id, tenant_id, participation_id, platform, handoff_method,
		  rule_version, status, created_at, updated_at)
		  VALUES (?,'tnt_v2',?,'xiaohongshu','h5', 0,'handed_off','2026-11-10T00:00:00Z','2026-11-10T00:00:00Z')`, []any{attemptID, participationID}},
	} {
		if _, err := s.db.Exec(q.sql, q.args...); err != nil {
			return err
		}
	}
	return nil
}

// linkShareAction is the admitted A27-style link_share record.
func linkShareAction(t *testing.T, mutate ...func(*channeladmission.ActionCapability)) channeladmission.ActionCapability {
	t.Helper()
	r := channeladmission.ActionCapability{
		ID: "cap_a27", CatalogCode: "A27", Platform: "wechat", Method: channeladmission.ActionMethodLinkShare,
		Scenario: channeladmission.ActionScenarioStorePromo, ActionKind: "publish",
		Label: "分享门店链接", AppBinding: "none", TenantScope: "*",
		Status: channeladmission.StatusAdmitted,
		Environments: map[string]channeladmission.EnvCapabilities{
			channeladmission.EnvDesktopBrowser: {Navigation: channeladmission.CapSupported, MediaHandoff: channeladmission.CapUnsupported, PlatformReceipt: channeladmission.CapUnsupported, RewardEligibility: channeladmission.CapUnsupported},
			channeladmission.EnvIOSSafari:      {Navigation: channeladmission.CapSupported, MediaHandoff: channeladmission.CapUnsupported, PlatformReceipt: channeladmission.CapUnsupported, RewardEligibility: channeladmission.CapUnsupported},
			channeladmission.EnvAndroidBrowser: {Navigation: channeladmission.CapSupported, MediaHandoff: channeladmission.CapUnsupported, PlatformReceipt: channeladmission.CapUnsupported, RewardEligibility: channeladmission.CapUnsupported},
			channeladmission.EnvIOSWechat:      {Navigation: channeladmission.CapSupported, MediaHandoff: channeladmission.CapUnsupported, PlatformReceipt: channeladmission.CapUnsupported, RewardEligibility: channeladmission.CapUnsupported},
			channeladmission.EnvAndroidWechat:  {Navigation: channeladmission.CapSupported, MediaHandoff: channeladmission.CapUnsupported, PlatformReceipt: channeladmission.CapUnsupported, RewardEligibility: channeladmission.CapUnsupported},
			channeladmission.EnvTargetApp:      {Navigation: channeladmission.CapSupported, MediaHandoff: channeladmission.CapUnsupported, PlatformReceipt: channeladmission.CapUnsupported, RewardEligibility: channeladmission.CapUnsupported},
		},
		FieldSupport:   map[string]string{},
		RequiredInputs: []string{},
		EvidenceGrade:  channeladmission.EvidenceOfficialDoc,
		CheckedAt:      "2026-11-09", ValidUntil: "2027-02-07",
	}
	for _, m := range mutate {
		m(&r)
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("linkShareAction: %v", err)
	}
	return r
}

// mediaAction is an admitted js_media_handoff record with media capability.
func mediaAction(t *testing.T) channeladmission.ActionCapability {
	t.Helper()
	r := channeladmission.ActionCapability{
		ID: "cap_media", CatalogCode: "A07", Platform: "xiaohongshu", Method: channeladmission.ActionMethodJSMedia,
		Scenario: channeladmission.ActionScenarioStorePromo, ActionKind: "publish",
		Label: "小红书图文", AppBinding: "platform_app", TenantScope: "*",
		Status: channeladmission.StatusAdmitted,
		Environments: map[string]channeladmission.EnvCapabilities{
			channeladmission.EnvDesktopBrowser: {Navigation: channeladmission.CapSupported, MediaHandoff: channeladmission.CapSupported, PlatformReceipt: channeladmission.CapUnknown, RewardEligibility: channeladmission.CapUnknown},
			channeladmission.EnvIOSSafari:      {Navigation: channeladmission.CapSupported, MediaHandoff: channeladmission.CapSupported, PlatformReceipt: channeladmission.CapUnknown, RewardEligibility: channeladmission.CapUnknown},
			channeladmission.EnvAndroidBrowser: {Navigation: channeladmission.CapSupported, MediaHandoff: channeladmission.CapSupported, PlatformReceipt: channeladmission.CapUnknown, RewardEligibility: channeladmission.CapUnknown},
			channeladmission.EnvIOSWechat:      {Navigation: channeladmission.CapSupported, MediaHandoff: channeladmission.CapSupported, PlatformReceipt: channeladmission.CapUnknown, RewardEligibility: channeladmission.CapUnknown},
			channeladmission.EnvAndroidWechat:  {Navigation: channeladmission.CapSupported, MediaHandoff: channeladmission.CapSupported, PlatformReceipt: channeladmission.CapUnknown, RewardEligibility: channeladmission.CapUnknown},
			channeladmission.EnvTargetApp:      {Navigation: channeladmission.CapSupported, MediaHandoff: channeladmission.CapSupported, PlatformReceipt: channeladmission.CapUnknown, RewardEligibility: channeladmission.CapUnknown},
		},
		FieldSupport:   map[string]string{"media": channeladmission.CapSupported, "title": channeladmission.CapUnsupported, "body": channeladmission.CapUnsupported, "topic": channeladmission.CapUnsupported},
		RequiredInputs: []string{"content_pack_version"},
		EvidenceGrade:  channeladmission.EvidenceOfficialSnapshot,
		CheckedAt:      "2026-11-09", ValidUntil: "2027-02-07",
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("mediaAction: %v", err)
	}
	return r
}

func v2Query(env string) channeladmission.ActionQuery {
	return channeladmission.ActionQuery{
		Now: nowV2(), TenantID: "tnt_v2", Environment: env,
		RewardPlay: false, MerchantAppRef: "",
	}
}

func startInputV2(rec channeladmission.ActionCapability, payloadKind, confirmation string) ActionStartInput {
	in := ActionStartInput{
		TenantID: "tnt_v2", StoreID: "sto_v2",
		ActionID: "sa_v2", CatalogCode: rec.CatalogCode, Platform: rec.Platform,
		SessionID: "ases_v2", ActionRevision: 3,
		PayloadKind: payloadKind, Method: rec.Method, Environment: channeladmission.EnvIOSSafari,
		Scenario: rec.Scenario, IdempotencyKey: "idem-" + payloadKind, VisitorID: "vis_v2",
		Confirmation: confirmation,
	}
	in.Record = rec
	in.Query = v2Query(channeladmission.EnvIOSSafari)
	in.PlanPayload = ActionPlanInput{ShareURL: "https://store.example.com/page"}
	if payloadKind == PayloadKindMedia {
		in.PlanPayload = ActionPlanInput{
			Text:      TextInput{Title: "周末烘焙节", Body: "全场八折", Topics: []string{"烘焙"}, StoreName: "南岸旗舰店"},
			Media:     []MediaItem{{Kind: MediaVideo, AssetID: "md_v1", SHA256: strings.Repeat("a", 64), ContentType: "video/mp4", Filename: "video.mp4"}},
			MediaRefs: map[string]string{"md_v1": "https://media.example.com/t1"},
		}
	}
	return in
}

// ---- T3: Adapter 接口契约 ------------------------------------------------------

type stubAdapter struct {
	platform string
	prep     PrepareResult
	submitOK bool
	query    QueryOutcome
	receipt  *Receipt
}

func (a *stubAdapter) Platform() string { return a.platform }
func (a *stubAdapter) Prepare(_ context.Context, _ PrepareRequest) (PrepareResult, error) {
	return a.prep, nil
}
func (a *stubAdapter) Submit(_ context.Context, _ Handoff, _ PrepareResult) error {
	if !a.submitOK {
		return ErrUnsupported
	}
	return nil
}
func (a *stubAdapter) Query(_ context.Context, _ Handoff) (QueryOutcome, error) {
	if a.query.State == "" && a.query.Receipt == nil {
		return QueryOutcome{}, ErrUnsupported
	}
	return a.query, nil
}
func (a *stubAdapter) VerifyCallback(_ context.Context, _ CallbackPayload) (Receipt, error) {
	if a.receipt == nil {
		return Receipt{}, ErrUnsupported
	}
	return *a.receipt, nil
}

func TestAdapterRegistryReplaceAndMissing(t *testing.T) {
	s, _ := newV2Service(t)
	if a := s.AdapterFor("douyin"); a != nil {
		t.Fatal("unregistered platform must answer nil adapter")
	}
	s.SetAdapter(&stubAdapter{platform: "douyin"})
	s.SetAdapter(&stubAdapter{platform: "douyin", prep: PrepareResult{ClientAction: ClientAction{Type: ClientActionOpenURL, URL: "https://x"}}})
	if got := s.AdapterFor("douyin"); got == nil {
		t.Fatal("registered adapter missing")
	}
	// 每平台至多一个:后注册替换先注册。
	p, err := s.AdapterFor("douyin").Prepare(context.Background(), PrepareRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if p.ClientAction.URL != "https://x" {
		t.Fatalf("later registration must replace: %+v", p)
	}
}

// ---- T4: 事件六层去重 -----------------------------------------------------------

func TestAppendFactDedupAndConflict(t *testing.T) {
	s, tenant := newV2Service(t)
	if _, err := s.db.Exec(`INSERT INTO publish_handoffs
		(id, tenant_id, store_id, platform, scenario, idempotency_key, status, evidence_source,
		 payload_kind, created_at, updated_at)
		VALUES ('ph_ev', ?, 'sto_v2', 'wechat', 'store_promo', 'idem-ev', 'handoff_ready', 'system_adapter',
		 'link_share', '2026-11-10T00:00:00Z', '2026-11-10T00:00:00Z')`, tenant); err != nil {
		t.Fatal(err)
	}
	// 首写。
	deduped, err := s.AppendFact(tenant, "ph_ev", EventCustomerClick, "customer_page", "evt-1", "customer", EvidenceCustomerAction, nil)
	if err != nil || deduped {
		t.Fatalf("first write: deduped=%v err=%v", deduped, err)
	}
	// 同键同 kind:静默去重。
	deduped, err = s.AppendFact(tenant, "ph_ev", EventCustomerClick, "customer_page", "evt-1", "customer", EvidenceCustomerAction, nil)
	if err != nil || !deduped {
		t.Fatalf("replay: deduped=%v err=%v", deduped, err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM publish_handoff_events WHERE handoff_id='ph_ev'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("dedup must not insert twice, rows=%d", n)
	}
	// 同键不同 kind:IDEMPOTENCY_CONFLICT。
	if _, err := s.AppendFact(tenant, "ph_ev", EventWakeAttempt, "customer_page", "evt-1", "customer", EvidenceCustomerAction, nil); !errors.Is(err, ErrEventConflict) {
		t.Fatalf("want ErrEventConflict, got %v", err)
	}
	// 六层 kind 全部可落。
	for _, kind := range []string{EventWakeAttempt, EventMediaHandoff, EventCustomerSelfReport, EventStaffConfirm, EventPlatformConfirm} {
		if _, err := s.AppendFact(tenant, "ph_ev", kind, "src", "evt-"+kind, "", EvidenceCustomerAction, nil); err != nil {
			t.Fatalf("kind %s: %v", kind, err)
		}
	}
	// 缺 source/event_id 拒绝。
	if _, err := s.AppendFact(tenant, "ph_ev", EventCustomerClick, "", "x", "", EvidenceCustomerAction, nil); !errors.Is(err, ErrBadInput) {
		t.Fatalf("missing source must be bad input, got %v", err)
	}
}

// ---- T5: StartAction -------------------------------------------------------------

// link_share:A27 纯链接——无参与无尝试,不伪建内容包;计划冻结在行。
func TestStartActionLinkShareNoParticipation(t *testing.T) {
	s, _ := newV2Service(t)
	rec := linkShareAction(t)
	res, err := s.StartAction(context.Background(), startInputV2(rec, PayloadKindLinkShare, "https://store.example.com/page"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !res.Created || res.Handoff.Status != StateHandoffReady {
		t.Fatalf("res=%+v", res)
	}
	if res.Handoff.ParticipationID != "" || res.Handoff.AttemptID != "" {
		t.Fatalf("link_share must not require participation: %+v", res.Handoff)
	}
	if res.Plan.Kind != PlanOfficialHandoff || res.Plan.ClientAction == nil || res.Plan.ClientAction.URL != "https://store.example.com/page" {
		t.Fatalf("plan=%+v", res.Plan)
	}
	// 冻结面回读。
	h, err := s.GetActionForVisitor(context.Background(), res.Handoff.ID, "vis_v2")
	if err != nil {
		t.Fatal(err)
	}
	if h.PlanKind != PlanOfficialHandoff || h.PayloadKind != PayloadKindLinkShare || h.ActionRevision != 3 || h.SessionID != "ases_v2" {
		t.Fatalf("frozen row = %+v", h)
	}
}

// media:指纹确认;参与+尝试需已冻结(可选路径)。
func TestStartActionMediaWithFrozenAttempt(t *testing.T) {
	s, _ := newV2Service(t)
	rec := mediaAction(t)
	in := startInputV2(rec, PayloadKindMedia, "")
	snap, _ := ConfirmationFor(PayloadKindMedia, in.PlanPayload)
	in.ParticipationID, in.AttemptID = "prt_v2", "atm_v2"
	in.Confirmation = snap
	if err := s.seedFrozenAttemptV2("prt_v2", "atm_v2"); err != nil {
		t.Fatalf("seed attempt: %v", err)
	}
	res, err := s.StartAction(context.Background(), in)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Plan.Kind != PlanOfficialHandoff {
		t.Fatalf("plan kind = %q", res.Plan.Kind)
	}
	if res.Handoff.ContentVersion != snap {
		t.Fatal("content version must equal the confirmed fingerprint")
	}
}

// media:确认指纹不符 = 换稿未重新确认。
func TestStartActionMediaConfirmationMismatch(t *testing.T) {
	s, _ := newV2Service(t)
	rec := mediaAction(t)
	in := startInputV2(rec, PayloadKindMedia, "deadbeef")
	if err := s.seedFrozenAttemptV2("prt_v2", "atm_v2"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartAction(context.Background(), in); !errors.Is(err, ErrConfirmationMismatch) {
		t.Fatalf("want ErrConfirmationMismatch, got %v", err)
	}
}

// 参与给了但尝试未冻结 → 拒绝(确认合法的前提下走到尝试核验)。
func TestStartActionAttemptNotFrozen(t *testing.T) {
	s, _ := newV2Service(t)
	rec := mediaAction(t)
	in := startInputV2(rec, PayloadKindMedia, "")
	snap, _ := ConfirmationFor(PayloadKindMedia, in.PlanPayload)
	in.ParticipationID, in.AttemptID = "prt_v2", "atm_v2"
	in.Confirmation = snap
	if _, err := s.StartAction(context.Background(), in); !errors.Is(err, ErrAttemptNotFrozen) {
		t.Fatalf("want ErrAttemptNotFrozen, got %v", err)
	}
}

// payload 与路线 required_inputs 不匹配(link_share on media route / media on link route)。
func TestStartActionPayloadRouteMismatch(t *testing.T) {
	s, _ := newV2Service(t)
	media := mediaAction(t)
	in := startInputV2(media, PayloadKindLinkShare, "https://store.example.com/page")
	if _, err := s.StartAction(context.Background(), in); err == nil || !strings.Contains(err.Error(), "payload") {
		t.Fatalf("link_share on media route must refuse: %v", err)
	}
	link := linkShareAction(t)
	in2 := startInputV2(link, PayloadKindMedia, "f")
	if _, err := s.StartAction(context.Background(), in2); err == nil || !strings.Contains(err.Error(), "payload") {
		t.Fatalf("media on link route must refuse: %v", err)
	}
}

// 幂等:同键同负载回原行;同键不同负载 = IDEMPOTENCY_CONFLICT。
func TestStartActionIdempotency(t *testing.T) {
	s, _ := newV2Service(t)
	rec := linkShareAction(t)
	in := startInputV2(rec, PayloadKindLinkShare, "https://store.example.com/page")
	first, err := s.StartAction(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.StartAction(context.Background(), in)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replay.Created || replay.Handoff.ID != first.Handoff.ID {
		t.Fatalf("replay must return the original row: created=%v id=%s/%s", replay.Created, replay.Handoff.ID, first.Handoff.ID)
	}
	in2 := in
	in2.PlanPayload = ActionPlanInput{ShareURL: "https://store.example.com/other"}
	in2.Confirmation = "https://store.example.com/other" // 顾客确认了不同内容
	if _, err := s.StartAction(context.Background(), in2); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("same key different payload must conflict, got %v", err)
	}
}

// gate-first:refused / pending_external 零落行。
func TestStartActionGateRefusalZeroRow(t *testing.T) {
	s, tenant := newV2Service(t)
	rec := linkShareAction(t, func(r *channeladmission.ActionCapability) {
		r.Status = channeladmission.StatusUnknown
	})
	in := startInputV2(rec, PayloadKindLinkShare, "https://store.example.com/page")
	_, err := s.StartAction(context.Background(), in)
	var refused *HandoffRefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("want HandoffRefusedError, got %v", err)
	}
	if !refused.PendingExternal {
		t.Fatal("unknown must surface pending_external")
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM publish_handoffs WHERE tenant_id=?`, tenant).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("refusal must leave zero rows, got %d", n)
	}
}

// ---- T5: Submit / Query(unsupported 诚实) ----------------------------------------

func startReadyV2(t *testing.T, s *Service, adapter Adapter) ActionHandoff {
	t.Helper()
	rec := linkShareAction(t)
	res, err := s.StartAction(context.Background(), startInputV2(rec, PayloadKindLinkShare, "https://store.example.com/page"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if adapter != nil {
		s.SetAdapter(adapter)
	}
	return res.Handoff
}

func TestSubmitUnsupportedHonest(t *testing.T) {
	s, _ := newV2Service(t)
	h := startReadyV2(t, s, &stubAdapter{platform: "wechat"}) // submit 未实现
	if _, err := s.SubmitAction(context.Background(), h.ID, "vis_v2"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
	got, _ := s.GetActionForVisitor(context.Background(), h.ID, "vis_v2")
	if got.Status != StateHandoffReady {
		t.Fatalf("failed submit must not move the state, got %s", got.Status)
	}
}

func TestSubmitAndQueryHappyPath(t *testing.T) {
	s, _ := newV2Service(t)
	ad := &stubAdapter{platform: "wechat", submitOK: true, query: QueryOutcome{State: "public"}}
	h := startReadyV2(t, s, ad)
	next, err := s.SubmitAction(context.Background(), h.ID, "vis_v2")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if next.Status != StateSubmitting {
		t.Fatalf("submit must reach submitting, got %s", next.Status)
	}
	q, err := s.QueryAction(context.Background(), h.ID, "vis_v2")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if q.QueryCount != 1 {
		t.Fatalf("query must count, got %d", q.QueryCount)
	}
	// query 留痕事件(platform_query,照录不确认)。
	kinds, _ := s.eventKinds(t, h.ID)
	if !contains(kinds, "platform_query") {
		t.Fatalf("query must be recorded: %v", kinds)
	}
	if contains(kinds, EventPlatformConfirm) {
		t.Fatal("query alone must never confirm")
	}
}

// ---- T5: 平台回调验签 → platform_confirm ------------------------------------------

func TestPlatformCallbackVerifiesAndConfirms(t *testing.T) {
	s, _ := newV2Service(t)
	rec := mediaAction(t)
	in := startInputV2(rec, PayloadKindMedia, "")
	snap, _ := ConfirmationFor(PayloadKindMedia, in.PlanPayload)
	if err := s.seedFrozenAttemptV2("prt_v2", "atm_v2"); err != nil {
		t.Fatal(err)
	}
	in.ParticipationID, in.AttemptID = "prt_v2", "atm_v2"
	in.Confirmation = snap
	res, err := s.StartAction(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	// 提交到 submitting(回执迁移的合法前态)。
	s.SetAdapter(&stubAdapter{platform: "xiaohongshu", submitOK: true})
	if _, err := s.SubmitAction(context.Background(), res.Handoff.ID, "vis_v2"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	// fixture 回执(stub 验签),五绑定+handoff id 绑定全匹配行。
	h, _ := s.GetActionForVisitor(context.Background(), res.Handoff.ID, "vis_v2")
	rcp := Receipt{
		ReceiptID: "rcp_v2_1", Platform: "xiaohongshu", AppRef: "app_touch_prod",
		TenantID: h.TenantID, ParticipationID: h.ParticipationID, AttemptID: h.AttemptID,
		ContentVersion: h.ContentVersion, HandoffID: h.ID,
		PostID: "post_1", Outcome: ReceiptOutcomePublished,
	}
	ad := &verifierStub{stubAdapter: stubAdapter{platform: "xiaohongshu"}, receipt: rcp}
	s.SetAdapter(ad)
	next, err := s.ApplyPlatformCallback(context.Background(), "xiaohongshu", CallbackPayload{Platform: "xiaohongshu", Body: []byte("{}")})
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	if next.Status != StatePlatformConfirmed {
		t.Fatalf("verified callback must confirm, got %s", next.Status)
	}
	kinds, _ := s.eventKinds(t, h.ID)
	if !contains(kinds, EventPlatformConfirm) {
		t.Fatalf("platform_confirm event missing: %v", kinds)
	}
	// 重放:同回执幂等回原状态,不新增确认事件。
	before, _ := s.eventKinds(t, h.ID)
	if _, err := s.ApplyPlatformCallback(context.Background(), "xiaohongshu", CallbackPayload{Platform: "xiaohongshu", Body: []byte("{}")}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	after, _ := s.eventKinds(t, h.ID)
	if len(before) != len(after) {
		t.Fatalf("replay must not add events: %v vs %v", before, after)
	}
	// 未注册平台/无验签能力的回调:一律拒绝(NOT_VERIFIED 残差)。
	if _, err := s.ApplyPlatformCallback(context.Background(), "bilibili", CallbackPayload{Platform: "bilibili"}); err == nil {
		t.Fatal("callback without adapter must refuse")
	}
	s2, _ := newV2Service(t)
	if _, err := s2.ApplyPlatformCallback(context.Background(), "wechat", CallbackPayload{Platform: "wechat"}); err == nil {
		t.Fatal("callback without verifier must refuse")
	}
}

type verifierStub struct {
	stubAdapter
	receipt Receipt
}

func (v *verifierStub) VerifyCallback(_ context.Context, _ CallbackPayload) (Receipt, error) {
	return v.receipt, nil
}

// 店员确认(六层之第 5 层):v2 行落 staff_confirm(fact 区分 见提交/核对作品),
// 人工永不转官方;官方晚到仍由验签回执确认。
func TestStaffConfirmOnV2Row(t *testing.T) {
	s, _ := newV2Service(t)
	rec := linkShareAction(t)
	res, err := s.StartAction(context.Background(), startInputV2(rec, PayloadKindLinkShare, "https://store.example.com/page"))
	if err != nil {
		t.Fatal(err)
	}
	next, err := s.RecordStaffEvent(context.Background(), res.Handoff.TenantID, res.Handoff.ID, StaffActor{ID: "stf_1", Name: "店员甲"}, FactStaffVerifiedWork, "看过作品")
	if err != nil {
		t.Fatalf("staff event: %v", err)
	}
	if next.Status != StateAwaitingReview {
		t.Fatalf("staff event must reach awaiting_review, got %s", next.Status)
	}
	kinds, _ := s.eventKinds(t, res.Handoff.ID)
	if !contains(kinds, EventStaffConfirm) {
		t.Fatalf("v2 rows must record staff_confirm, got %v", kinds)
	}
	// 非法 kind 拒绝。
	if _, err := s.RecordStaffEvent(context.Background(), res.Handoff.TenantID, res.Handoff.ID, StaffActor{ID: "stf_1"}, "made_up", ""); !errors.Is(err, ErrBadInput) {
		t.Fatalf("unknown staff kind must be bad input, got %v", err)
	}
	// 人工事实到不了 platform_confirm:同一交接只能等官方回执。
	if next.Status == StatePlatformConfirmed {
		t.Fatal("staff observation must never confirm")
	}
}
