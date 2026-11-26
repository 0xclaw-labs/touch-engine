package adapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/publishflow"
	"github.com/bianjiefilm/touch-engine/server/internal/publishhandoff"
	"github.com/bianjiefilm/touch-engine/server/internal/rewardops"
)

// adapterTestDB opens a migrated database (embedded migrations apply in
// filename order; fixture data lives only here — 测试入口).
func adapterTestDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "adapters.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	// FK 夹具:tenant/store 最小行(adapter 表引用它们)。
	if _, err := d.Exec(`INSERT INTO tenants (id, name, created_at) VALUES ('tnt_1','seed-tenant','2026-11-11T00:00:00Z')`); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO stores (id, tenant_id, name, created_at, updated_at) VALUES ('st_1','tnt_1','seed-store','2026-11-11T00:00:00Z','2026-11-11T00:00:00Z')`); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO store_storefronts(store_id,tenant_id,store_code,created_at,updated_at) VALUES ('st_1','tnt_1','fixture-store','2026-11-11T00:00:00Z','2026-11-11T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	return d
}

func douyinRequest(method string) publishhandoff.PrepareRequest {
	h := publishhandoff.ActionHandoff{
		Handoff: publishhandoff.Handoff{
			ID: "pha_test_douyin", TenantID: "tnt_1", StoreID: "st_1",
			ParticipationID: "par_1", AttemptID: "att_1",
			Platform: "douyin", Scenario: "store_promo",
			Status:               publishhandoff.StateHandoffReady,
			EvidenceSource:       publishhandoff.EvidenceCustomerAction,
			ContentVersion:       "fp-v1",
			ContentPackVersionID: "cpv_v1",
			PayloadKind:          publishhandoff.PayloadKindMedia,
		},
		ActionID: "sa_1", SessionID: "sess_1", PayloadKind: publishhandoff.PayloadKindMedia,
		Method: method, Environment: channeladmission.EnvIOSSafari, PlanKind: publishhandoff.PlanOfficialHandoff,
	}
	return publishhandoff.PrepareRequest{
		Handoff: h,
		Plan:    publishhandoff.ActionPlan{Kind: publishhandoff.PlanOfficialHandoff, Allowed: true},
		Media: []publishhandoff.MediaItem{
			{Kind: publishhandoff.MediaVideo, AssetID: "mvid", SHA256: strings.Repeat("a", 64), ContentType: "video/mp4", Filename: "demo.mp4"},
			{Kind: publishhandoff.MediaCover, AssetID: "mcov", SHA256: strings.Repeat("b", 64), ContentType: "image/jpeg", Filename: "cover.jpg"},
		},
		MediaRefs: map[string]string{"mvid": "https://media.example/v", "mcov": "https://media.example/c"},
		SessionID: "sess_1", Environment: channeladmission.EnvIOSSafari,
	}
}

func loadSeedCapability(t *testing.T, d *sql.DB, id string) channeladmission.ActionCapability {
	t.Helper()
	var rec channeladmission.ActionCapability
	var envJSON, fieldJSON, reqJSON, srcJSON, missJSON string
	var suggested, emergency int
	err := d.QueryRow(`SELECT id, catalog_code, platform, method, scenario, action_kind, label,
		suggested_only, app_binding, tenant_scope, status, environments, field_support,
		required_inputs, evidence_grade, official_sources, checked_at, valid_until,
		emergency_disabled, close_reason, missing, reason_code, reason_text, seed_version
		FROM channel_action_capabilities WHERE id=?`, id).
		Scan(&rec.ID, &rec.CatalogCode, &rec.Platform, &rec.Method, &rec.Scenario, &rec.ActionKind, &rec.Label,
			&suggested, &rec.AppBinding, &rec.TenantScope, &rec.Status, &envJSON, &fieldJSON,
			&reqJSON, &rec.EvidenceGrade, &srcJSON, &rec.CheckedAt, &rec.ValidUntil,
			&emergency, &rec.CloseReason, &missJSON, &rec.ReasonCode, &rec.ReasonText, &rec.SeedVersion)
	if err != nil {
		t.Fatalf("load seed capability %s: %v", id, err)
	}
	rec.SuggestedOnly = suggested == 1
	rec.EmergencyDisabled = emergency == 1
	if err := json.Unmarshal([]byte(envJSON), &rec.Environments); err != nil {
		t.Fatalf("unmarshal environments: %v", err)
	}
	if err := json.Unmarshal([]byte(fieldJSON), &rec.FieldSupport); err != nil {
		t.Fatalf("unmarshal field_support: %v", err)
	}
	if err := json.Unmarshal([]byte(reqJSON), &rec.RequiredInputs); err != nil {
		t.Fatalf("unmarshal required_inputs: %v", err)
	}
	if err := json.Unmarshal([]byte(srcJSON), &rec.OfficialSources); err != nil {
		t.Fatalf("unmarshal official_sources: %v", err)
	}
	if err := json.Unmarshal([]byte(missJSON), &rec.Missing); err != nil {
		t.Fatalf("unmarshal missing: %v", err)
	}
	return rec
}

func admittedDouyinCapability(t *testing.T, method, scenario string) channeladmission.ActionCapability {
	t.Helper()
	rewardCap := channeladmission.CapUnsupported
	if scenario == channeladmission.ActionScenarioRewardTask {
		rewardCap = channeladmission.CapSupported
	}
	appBinding := "platform_app"
	rec := channeladmission.ActionCapability{
		ID: "cap_test_douyin_" + method, CatalogCode: "A01", Platform: "douyin",
		Method: method, Scenario: scenario, ActionKind: channeladmission.ActionKindPublish,
		Label: "发抖音视频（获准测试）", AppBinding: appBinding, TenantScope: "*",
		Status: channeladmission.StatusAdmitted,
		Environments: map[string]channeladmission.EnvCapabilities{
			channeladmission.EnvDesktopBrowser: {Navigation: channeladmission.CapSupported, MediaHandoff: channeladmission.CapSupported, PlatformReceipt: channeladmission.CapSupported, RewardEligibility: rewardCap},
			channeladmission.EnvIOSSafari:      {Navigation: channeladmission.CapSupported, MediaHandoff: channeladmission.CapSupported, PlatformReceipt: channeladmission.CapSupported, RewardEligibility: rewardCap},
			channeladmission.EnvAndroidBrowser: {Navigation: channeladmission.CapSupported, MediaHandoff: channeladmission.CapSupported, PlatformReceipt: channeladmission.CapSupported, RewardEligibility: rewardCap},
			channeladmission.EnvIOSWechat:      {Navigation: channeladmission.CapUnsupported, MediaHandoff: channeladmission.CapUnsupported, PlatformReceipt: channeladmission.CapUnknown, RewardEligibility: rewardCap},
			channeladmission.EnvAndroidWechat:  {Navigation: channeladmission.CapUnsupported, MediaHandoff: channeladmission.CapUnsupported, PlatformReceipt: channeladmission.CapUnknown, RewardEligibility: rewardCap},
			channeladmission.EnvTargetApp:      {Navigation: channeladmission.CapSupported, MediaHandoff: channeladmission.CapSupported, PlatformReceipt: channeladmission.CapSupported, RewardEligibility: rewardCap},
		},
		FieldSupport: map[string]string{
			"media": channeladmission.CapSupported,
			"title": channeladmission.CapSupported,
			"body":  channeladmission.CapUnsupported,
			"topic": channeladmission.CapSupported,
			"poi":   channeladmission.CapUnsupported,
		},
		RequiredInputs: []string{"content_pack_version"},
		EvidenceGrade:  channeladmission.EvidenceOfficialDoc,
		CheckedAt:      "2026-11-09", ValidUntil: "2027-02-07",
	}
	if err := rec.Validate(); err != nil {
		t.Fatalf("admittedDouyinCapability invalid: %v", err)
	}
	return rec
}

func seedFrozenAttemptForDouyin(t *testing.T, d *sql.DB, tenantID, storeID, participationID, attemptID string) {
	t.Helper()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT OR IGNORE INTO campaigns (id, tenant_id, title, status, store_id, created_at, updated_at)
		  VALUES ('cmp_dy_1',?,'抖音测试活动','active',?,'2026-11-11T00:00:00Z','2026-11-11T00:00:00Z')`, []any{tenantID, storeID}},
		{`INSERT OR IGNORE INTO participations (id, tenant_id, store_id, campaign_id, link_code, touchpoint_source,
		  rule_version, visitor_id, status, idempotency_key, starts_at, expires_at, created_at, updated_at)
		  VALUES (?,?,?,'cmp_dy_1','lnk_dy_1','nfc',1,'vis_dy_1','pending','idem-par-dy-1',
		  '2026-11-11T00:00:00Z','2027-11-11T00:00:00Z','2026-11-11T00:00:00Z','2026-11-11T00:00:00Z')`, []any{participationID, tenantID, storeID}},
		{`INSERT OR IGNORE INTO participation_attempts (id, tenant_id, participation_id, platform, handoff_method,
		  rule_version, status, created_at, updated_at)
		  VALUES (?,?,?,'douyin','h5',1,'handed_off','2026-11-11T00:00:00Z','2026-11-11T00:00:00Z')`, []any{attemptID, tenantID, participationID}},
	} {
		if _, err := d.Exec(q.sql, q.args...); err != nil {
			t.Fatalf("seed frozen attempt: %v", err)
		}
	}
}

func TestDouyinPrepareRequiresClientKey(t *testing.T) {
	a := NewDouyinAdapter(nil, DouyinConfig{})
	if _, err := a.Prepare(context.Background(), douyinRequest("h5_share")); err == nil {
		t.Fatal("expected honest error without client_key")
	}
}

func TestDouyinPrepareH5ShareParams(t *testing.T) {
	d := adapterTestDB(t)
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	a := NewDouyinAdapter(d, DouyinConfig{Signer: fakeDouyinSigner{}, ClientKey: "ck_1", AppRef: "app_1", Now: func() time.Time { return now }})
	res, err := a.Prepare(context.Background(), douyinRequest("h5_share"))
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	act := res.ClientAction
	if act.Type != publishhandoff.ClientActionJSMediaHandoff {
		t.Fatalf("client action type = %q", act.Type)
	}
	if act.Params["client_key"] != "ck_1" || act.Params["share_id"] == "" {
		t.Fatalf("official params missing: %v", act.Params)
	}
	if act.Params["media_url"] != "https://media.example/v" {
		t.Fatalf("media url = %q", act.Params["media_url"])
	}
	if act.Params["signature"] == "" || act.Params["state"] != act.Params["share_id"] || act.Params["video_path"] != "https://media.example/v" {
		t.Fatalf("official signature/media/state missing: %+v", act.Params)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// A01 store_promo 反例拒绝: 复用 3535 channeladmission 种子,服务端严格拒绝,
// 零可执行外发 payload,绝不退化为"手动发完领券",零发布成功事件与零奖励。
func TestDouyinA01StorePromoRefusedEndToEndNoManualFallbackNoReward(t *testing.T) {
	d := adapterTestDB(t)
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	svc := publishhandoff.New(d, publishhandoff.Options{
		Now:    func() time.Time { return now },
		AppRef: "app_1",
	})
	adapter := NewDouyinAdapter(d, DouyinConfig{
		ClientKey: "ck_live_1", AppRef: "app_1",
		Signer: fakeDouyinSigner{}, Now: func() time.Time { return now },
	})
	svc.SetAdapter(adapter)

	h5Seed := loadSeedCapability(t, d, "cap_a01_h5_share")
	manualSeed := loadSeedCapability(t, d, "cap_a01_manual_material")

	// 1) 结构化路线评估: store_promo 通用 H5 与手动替代均严格 not_applicable, 零手动后门。
	q := channeladmission.ActionQuery{Now: now, TenantID: "tnt_1", Environment: channeladmission.EnvIOSSafari, RewardPlay: false}
	h5Assess := adapter.AssessRoute(DouyinRouteEvalInput{Record: h5Seed, Query: q, Method: DouyinMethodH5})
	if h5Assess.Status != DouyinStatusNotApplicable || h5Assess.AdmissionReason != channeladmission.ReasonNotAdmitted {
		t.Fatalf("A01 store_promo assessment = %+v", h5Assess)
	}
	if h5Assess.ManualFallbackAllowed || h5Assess.ExecutablePayloadAllowed {
		t.Fatalf("store_promo must never allow manual fallback or executable payload: %+v", h5Assess)
	}

	manualAssess := adapter.AssessRoute(DouyinRouteEvalInput{Record: manualSeed, Query: q, Method: channeladmission.ActionMethodManualMaterial})
	if manualAssess.Status != DouyinStatusNotApplicable || manualAssess.ManualFallbackAllowed || manualAssess.ExecutablePayloadAllowed {
		t.Fatalf("store_promo manual_material must never become reward backdoor: %+v", manualAssess)
	}

	// 2) Service.StartAction 走 3535 闸: 严格拒绝,零落行、零 share_id、零发布成功事件。
	planPayload := publishhandoff.ActionPlanInput{
		Text:      publishhandoff.TextInput{Title: "门店宣传", Body: "全场八折", Topics: []string{"探店"}},
		Media:     []publishhandoff.MediaItem{{Kind: publishhandoff.MediaVideo, AssetID: "mvid", SHA256: strings.Repeat("a", 64), ContentType: "video/mp4", Filename: "promo.mp4"}},
		MediaRefs: map[string]string{"mvid": "https://media.example/promo.mp4"},
	}
	confirm, err := publishhandoff.ConfirmationFor(publishhandoff.PayloadKindMedia, planPayload)
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range []channeladmission.ActionCapability{h5Seed, manualSeed} {
		_, err := svc.StartAction(context.Background(), publishhandoff.ActionStartInput{
			TenantID: "tnt_1", StoreID: "st_1", ActionID: "sa_a01", CatalogCode: "A01",
			Platform: "douyin", SessionID: "sess_a01", VisitorID: "vis_a01",
			ActionRevision: 1, PayloadKind: publishhandoff.PayloadKindMedia,
			Method: rec.Method, Environment: channeladmission.EnvIOSSafari, Scenario: rec.Scenario,
			IdempotencyKey: "idem-refuse-" + rec.Method, Confirmation: confirm,
			Record: rec, Query: q, PlanPayload: planPayload, ContentPackVersionID: "cpv_1",
		})
		var refused *publishhandoff.HandoffRefusedError
		if !errors.As(err, &refused) || refused.Reason != channeladmission.ReasonNotAdmitted {
			t.Fatalf("method=%s expected HandoffRefusedError(not_admitted), got %v", rec.Method, err)
		}
	}

	// 3) 纵深防御: 即使直接调用 adapter.Prepare(refused plan / manual_material),也零外发 payload、零 share_id。
	refusedReq := douyinRequest("h5_share")
	refusedReq.Plan = publishhandoff.JudgeActionPlan(q, h5Seed, planPayload)
	if _, err := adapter.Prepare(context.Background(), refusedReq); !errors.Is(err, ErrDouyinRouteNotAdmitted) {
		t.Fatalf("direct Prepare with refused plan must fail with ErrDouyinRouteNotAdmitted, got %v", err)
	}
	if _, err := adapter.Prepare(context.Background(), douyinRequest(channeladmission.ActionMethodManualMaterial)); !errors.Is(err, publishhandoff.ErrUnsupported) {
		t.Fatalf("manual_material must return ErrUnsupported, got %v", err)
	}

	// 4) 核查库表: 零交接行、零事件、零 share_id。
	for _, table := range []string{"publish_handoffs", "publish_handoff_events", "douyin_share_ids"} {
		var n int
		if err := d.QueryRow(`SELECT COUNT(1) FROM ` + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Fatalf("refused A01 store_promo must leave %s empty, got %d", table, n)
		}
	}

	// 5) 核查奖励门禁(HUI-1671): 未准入动作零奖励。
	verdict := rewardops.Adjudicate(rewardops.Snapshot{
		CampaignID: "cmp_1", Version: 1,
		AllowedEvidence: []string{rewardops.EvidenceOfficial, rewardops.EvidenceStaffVerified},
		Slots:           []string{"coupon_1"},
	}, rewardops.Input{
		TenantID: "tnt_1", CampaignID: "cmp_1", ParticipationID: "par_1",
		Slot: "coupon_1", RuleVersion: 1, EventID: "evt_refused_1",
		Evidence: rewardops.EvidenceStaffVerified, Actor: "staff_1", StoreID: "st_1",
		ActionKind: channeladmission.ActionKindPublish, ActionEligible: rewardops.ActionEligibleNo,
	})
	if verdict.Proceed || verdict.Reason != rewardops.ReasonRewardNotEligible {
		t.Fatalf("not_admitted A01 store_promo must grant zero reward: %+v", verdict)
	}
}

// 区分 douyin_h5 与独立获准专项路线 douyin_miniapp_task, 覆盖全部六态结构化状态
// (can_handoff / needs_auth / processing / failed / not_applicable / qualification_pending)。
func TestDouyinSixStateStructuredAssessmentAndMethodSeparation(t *testing.T) {
	d := adapterTestDB(t)
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	qSafari := channeladmission.ActionQuery{Now: now, TenantID: "tnt_1", Environment: channeladmission.EnvIOSSafari}
	qWechat := channeladmission.ActionQuery{Now: now, TenantID: "tnt_1", Environment: channeladmission.EnvIOSWechat}

	miniappSeed := loadSeedCapability(t, d, "cap_a01_miniapp")
	h5Admitted := admittedDouyinCapability(t, channeladmission.ActionMethodH5Share, channeladmission.ActionScenarioStorePromo)
	miniappAdmitted := admittedDouyinCapability(t, channeladmission.ActionMethodMiniapp, channeladmission.ActionScenarioRewardTask)

	fullAdapter := NewDouyinAdapter(d, DouyinConfig{
		ClientKey: "ck_1", AppRef: "app_1", MiniappPath: "pages/douyin-task/index",
		Signer: fakeDouyinSigner{}, Now: func() time.Time { return now },
	})

	// 1) qualification_pending: 专项路线 seed 为 unknown(pending_external)
	aPending := fullAdapter.AssessRoute(DouyinRouteEvalInput{
		Record: miniappSeed, Query: channeladmission.ActionQuery{Now: now, TenantID: "tnt_1", Environment: channeladmission.EnvIOSSafari, RewardPlay: true},
		Method: DouyinMethodMiniappTask,
	})
	if aPending.Status != DouyinStatusQualificationPending || aPending.RouteMethod != DouyinMethodMiniappTask ||
		aPending.ExecutablePayloadAllowed || aPending.ManualFallbackAllowed || len(aPending.MissingReasons) == 0 {
		t.Fatalf("unapproved miniapp task must be qualification_pending with missing reasons: %+v", aPending)
	}

	// 2) qualification_pending: 获准但部署缺少 MiniappPath(不能借用 douyin_h5 冒充)
	noMiniappAdapter := NewDouyinAdapter(d, DouyinConfig{ClientKey: "ck_1", AppRef: "app_1", Signer: fakeDouyinSigner{}})
	aNoPath := noMiniappAdapter.AssessRoute(DouyinRouteEvalInput{
		Record: miniappAdmitted, Query: channeladmission.ActionQuery{Now: now, TenantID: "tnt_1", Environment: channeladmission.EnvIOSSafari, RewardPlay: true},
		Method: DouyinMethodMiniappTask,
	})
	if aNoPath.Status != DouyinStatusQualificationPending || aNoPath.ExecutablePayloadAllowed {
		t.Fatalf("missing miniapp path must be qualification_pending: %+v", aNoPath)
	}

	// 3) not_applicable: 方法路线错配(不能拿 douyin_h5 准入冒充 douyin_miniapp_task)
	aCrossMethod := fullAdapter.AssessRoute(DouyinRouteEvalInput{
		Record: h5Admitted, Query: qSafari, Method: DouyinMethodMiniappTask,
	})
	if aCrossMethod.Status != DouyinStatusNotApplicable || aCrossMethod.ReasonCode != "route_method_mismatch" {
		t.Fatalf("cross-method borrowing must be not_applicable: %+v", aCrossMethod)
	}

	// 4) not_applicable: 微信容器阻拦
	aWechat := fullAdapter.AssessRoute(DouyinRouteEvalInput{
		Record: h5Admitted, Query: qWechat, Method: DouyinMethodH5,
	})
	if aWechat.Status != DouyinStatusNotApplicable || aWechat.ReasonCode != "wechat_container_blocked" || aWechat.ManualFallbackAllowed {
		t.Fatalf("wechat container must be not_applicable without manual fallback: %+v", aWechat)
	}
	if len(aWechat.AllowedNextSteps) == 0 || aWechat.AllowedNextSteps[0] != DouyinNextOpenInSystemBrowser {
		t.Fatalf("wechat container must guide user to system browser: %+v", aWechat.AllowedNextSteps)
	}

	// 5) can_handoff: 获准 + 非微信 + 凭据齐备
	aReady := fullAdapter.AssessRoute(DouyinRouteEvalInput{
		Record: h5Admitted, Query: qSafari, Method: DouyinMethodH5,
	})
	if aReady.Status != DouyinStatusCanHandoff || !aReady.ExecutablePayloadAllowed {
		t.Fatalf("ready route must be can_handoff: %+v", aReady)
	}

	// 6) needs_auth: 未登录/需账号确认
	aAuth := fullAdapter.AssessRoute(DouyinRouteEvalInput{
		Record: h5Admitted, Query: qSafari, Method: DouyinMethodH5, NeedsLogin: true,
	})
	if aAuth.Status != DouyinStatusNeedsAuth || aAuth.ExecutablePayloadAllowed {
		t.Fatalf("unauthenticated user must be needs_auth: %+v", aAuth)
	}

	// 7) processing: 已唤起/待核验/unknown → 必须先回查原任务
	for _, st := range []publishhandoff.State{publishhandoff.StateEditorOpened, publishhandoff.StateSubmitting, publishhandoff.StateAwaitingReview, publishhandoff.StateUnknown} {
		aProc := fullAdapter.AssessRoute(DouyinRouteEvalInput{
			Record: h5Admitted, Query: qSafari, Method: DouyinMethodH5, HandoffStatus: st,
		})
		if aProc.Status != DouyinStatusProcessing || len(aProc.AllowedNextSteps) == 0 || aProc.AllowedNextSteps[0] != DouyinNextQueryOriginal {
			t.Fatalf("state=%s expected processing + query_original_handoff: %+v", st, aProc)
		}
	}

	// 8) failed: 顾客取消或准备失败
	for _, st := range []publishhandoff.State{publishhandoff.StateCanceled, publishhandoff.StateFailed} {
		aFail := fullAdapter.AssessRoute(DouyinRouteEvalInput{
			Record: h5Admitted, Query: qSafari, Method: DouyinMethodH5, HandoffStatus: st,
		})
		if aFail.Status != DouyinStatusFailed || aFail.ExecutablePayloadAllowed {
			t.Fatalf("state=%s expected failed: %+v", st, aFail)
		}
	}
}

// 获准路线正例关联: 同一指定视频/hash 进入官方 H5 与独立获准专项路线,
// 服务器端 share_id 完整绑定 (tenant, store, action, session, attempt, content_pack_version, app_id),
// 用户确认后回执关联到同一次 handoff。
func TestDouyinAdmittedH5AndMiniappEndToEndBindingAndReceipt(t *testing.T) {
	d := adapterTestDB(t)
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	seedFrozenAttemptForDouyin(t, d, "tnt_1", "st_1", "par_1", "att_1")

	svc := publishhandoff.New(d, publishhandoff.Options{
		Now:    func() time.Time { return now },
		AppRef: "app_douyin_prod",
	})
	adapter := NewDouyinAdapter(d, DouyinConfig{
		ClientKey:   "ck_douyin_prod",
		AppRef:      "app_douyin_prod",
		MiniappPath: "pages/douyin-task/submit",
		Signer:      fakeDouyinSigner{},
		Query:       fakeDouyinQuery{state: "published", published: true, item: "aweme_item_888"},
		Now:         func() time.Time { return now },
	})
	svc.SetAdapter(adapter)

	rec := admittedDouyinCapability(t, channeladmission.ActionMethodH5Share, channeladmission.ActionScenarioStorePromo)
	q := channeladmission.ActionQuery{Now: now, TenantID: "tnt_1", Environment: channeladmission.EnvIOSSafari}
	planPayload := publishhandoff.ActionPlanInput{
		Text:      publishhandoff.TextInput{Title: "南岸咖啡探店", Body: "手冲买一送一", Topics: []string{"手冲咖啡", "南岸"}},
		Media:     []publishhandoff.MediaItem{{Kind: publishhandoff.MediaVideo, AssetID: "vid_1", SHA256: strings.Repeat("c", 64), ContentType: "video/mp4", Filename: "coffee.mp4"}},
		MediaRefs: map[string]string{"vid_1": "https://media.example/coffee.mp4"},
	}
	confirm, err := publishhandoff.ConfirmationFor(publishhandoff.PayloadKindMedia, planPayload)
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.StartAction(context.Background(), publishhandoff.ActionStartInput{
		TenantID: "tnt_1", StoreID: "st_1", ActionID: "sa_dy_1", CatalogCode: "A01",
		Platform: "douyin", SessionID: "sess_dy_1", VisitorID: "vis_dy_1",
		ActionRevision: 2, PayloadKind: publishhandoff.PayloadKindMedia,
		Method: channeladmission.ActionMethodH5Share, Environment: channeladmission.EnvIOSSafari,
		Scenario: channeladmission.ActionScenarioStorePromo,
		ParticipationID: "par_1", AttemptID: "att_1",
		IdempotencyKey: "idem-dy-admitted-1", Confirmation: confirm,
		Record: rec, Query: q, PlanPayload: planPayload, ContentPackVersionID: "cpv_adopted_9",
	})
	if err != nil {
		t.Fatalf("StartAction admitted H5: %v", err)
	}
	if res.Handoff.ClientAction == nil || res.Handoff.ClientAction.Params["sdk"] != DouyinMethodH5 {
		t.Fatalf("expected douyin_h5 client action, got %+v", res.Handoff.ClientAction)
	}
	shareID := res.Handoff.ClientAction.Params["share_id"]

	// 验证服务器端 share_id 全量权威映射: (tenant, store, action, session, attempt, content_pack_version, app_id)
	binding, err := adapter.ResolveShareBinding(context.Background(), shareID, DouyinBindingExpectation{
		TenantID: "tnt_1", StoreID: "st_1", HandoffID: res.Handoff.ID,
		ActionID: "sa_dy_1", SessionID: "sess_dy_1", AttemptID: "att_1",
		ContentVersion: confirm, ContentPackVersionID: "cpv_adopted_9",
		AppRef: "app_douyin_prod", AppID: "ck_douyin_prod", Now: now,
	})
	if err != nil {
		t.Fatalf("ResolveShareBinding: %v", err)
	}
	if binding.StoreID != "st_1" || binding.AttemptID != "att_1" || binding.ContentPackVersionID != "cpv_adopted_9" || binding.AppID != "ck_douyin_prod" {
		t.Fatalf("incomplete authoritative share binding: %+v", binding)
	}

	// 进入提交未知态后回查: 官方回执通过五绑定 + handoff_id 绑定推进为 platform_confirmed。
	if _, err := svc.MarkSubmitting(context.Background(), res.Handoff.ID, "vis_dy_1"); err != nil {
		t.Fatalf("MarkSubmitting: %v", err)
	}
	if _, err := svc.MarkTimedOut(context.Background(), res.Handoff.ID); err != nil {
		t.Fatalf("MarkTimedOut: %v", err)
	}
	queried, err := svc.QueryAction(context.Background(), res.Handoff.ID, "vis_dy_1")
	if err != nil {
		t.Fatalf("QueryAction: %v", err)
	}
	if queried.Status != publishhandoff.StatePlatformConfirmed || queried.EvidenceSource != publishhandoff.EvidenceOfficialReceipt {
		t.Fatalf("expected platform_confirmed via official_receipt, got status=%s evidence=%s", queried.Status, queried.EvidenceSource)
	}

	// 独立获准专项路线 douyin_miniapp_task 正例:
	miniRec := admittedDouyinCapability(t, channeladmission.ActionMethodMiniapp, channeladmission.ActionScenarioRewardTask)
	miniQ := channeladmission.ActionQuery{Now: now, TenantID: "tnt_1", Environment: channeladmission.EnvIOSSafari, RewardPlay: true}
	miniRes, err := svc.StartAction(context.Background(), publishhandoff.ActionStartInput{
		TenantID: "tnt_1", StoreID: "st_1", ActionID: "sa_dy_mini", CatalogCode: "A01",
		Platform: "douyin", SessionID: "sess_dy_mini", VisitorID: "vis_dy_1",
		ActionRevision: 1, PayloadKind: publishhandoff.PayloadKindMedia,
		Method: channeladmission.ActionMethodMiniapp, Environment: channeladmission.EnvIOSSafari,
		Scenario: channeladmission.ActionScenarioRewardTask,
		ParticipationID: "par_1", AttemptID: "att_1",
		IdempotencyKey: "idem-dy-mini-1", Confirmation: confirm,
		Record: miniRec, Query: miniQ, PlanPayload: planPayload, ContentPackVersionID: "cpv_adopted_9",
	})
	if err != nil {
		t.Fatalf("StartAction admitted miniapp task: %v", err)
	}
	if miniRes.Handoff.ClientAction == nil ||
		miniRes.Handoff.ClientAction.Type != publishhandoff.ClientActionOpenMiniapp ||
		miniRes.Handoff.ClientAction.Params["sdk"] != DouyinMethodMiniappTask ||
		miniRes.Handoff.ClientAction.MiniappPath != "pages/douyin-task/submit" {
		t.Fatalf("unexpected miniapp action: %+v", miniRes.Handoff.ClientAction)
	}
}

func TestDouyinShareIDIdempotentAndExpiry(t *testing.T) {
	d := adapterTestDB(t)
	now := time.Date(2026, 11, 11, 9, 0, 0, 500000000, time.UTC)
	a := NewDouyinAdapter(d, DouyinConfig{Signer: fakeDouyinSigner{}, ClientKey: "ck_1", AppRef: "app_1", ShareTTL: time.Hour, Now: func() time.Time { return now }})
	r1, err := a.Prepare(context.Background(), douyinRequest("h5_share"))
	if err != nil {
		t.Fatalf("prepare1: %v", err)
	}
	r2, err := a.Prepare(context.Background(), douyinRequest("h5_share"))
	if err != nil {
		t.Fatalf("prepare2: %v", err)
	}
	if r1.ClientAction.Params["share_id"] != r2.ClientAction.Params["share_id"] {
		t.Fatal("same handoff must replay the same share_id")
	}

	// 验证亚秒级精确过期比较(修复 RFC3339Nano 字符串比较边界)
	if _, err := a.ResolveShareBinding(context.Background(), r1.ClientAction.Params["share_id"], DouyinBindingExpectation{
		TenantID: "tnt_1", StoreID: "st_1", AppRef: "app_1", AppID: "ck_1",
		Now: now.Add(time.Hour), // 正好到期
	}); !errors.Is(err, ErrDouyinShareExpired) {
		t.Fatalf("exact expiry boundary must return ErrDouyinShareExpired, got %v", err)
	}

	// 过期后 Query 保持 unknown 且不铸 Receipt。
	a.query = fakeDouyinQuery{state: "published", published: true, item: "item_expired"}
	a.now = func() time.Time { return now.Add(2 * time.Hour) }
	qOut, err := a.Query(context.Background(), douyinRequest("h5_share").Handoff.Handoff)
	if err != nil {
		t.Fatalf("query expired: %v", err)
	}
	if qOut.State != "unknown" || qOut.Receipt != nil || qOut.Detail["reason"] != "share_id_expired" {
		t.Fatalf("expired share_id query must stay unknown without receipt: %+v", qOut)
	}

	// 过期:重铸新 share_id,旧行作废保留。
	now = now.Add(2 * time.Hour)
	a.now = func() time.Time { return now }
	r3, err := a.Prepare(context.Background(), douyinRequest("h5_share"))
	if err != nil {
		t.Fatalf("prepare3: %v", err)
	}
	if r3.ClientAction.Params["share_id"] == r1.ClientAction.Params["share_id"] {
		t.Fatal("expired share_id must not be reused")
	}
	var revoked int
	if err := d.QueryRow(`SELECT COUNT(1) FROM douyin_share_ids WHERE revoked_at!=''`).Scan(&revoked); err != nil {
		t.Fatal(err)
	}
	if revoked != 1 {
		t.Fatalf("revoked rows = %d", revoked)
	}
}

// 过期签名 / 篡改 state / 缺失签名字段拒绝。
func TestDouyinExpiredOrInvalidSignatureRejected(t *testing.T) {
	d := adapterTestDB(t)
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)

	// 签名器返回过期 timestamp(> 10 分钟前)
	staleSigner := stubDouyinSigner{
		signFn: func(_ context.Context, in DouyinShareSignInput) (map[string]string, error) {
			return map[string]string{
				"nonce_str": "n1",
				"timestamp": "1700000000", // 远早于 2026-11-11
				"signature": "stale-sig",
			}, nil
		},
	}
	a := NewDouyinAdapter(d, DouyinConfig{ClientKey: "ck_1", AppRef: "app_1", Signer: staleSigner, Now: func() time.Time { return now }})
	if _, err := a.Prepare(context.Background(), douyinRequest("h5_share")); !errors.Is(err, ErrDouyinSignatureInvalid) {
		t.Fatalf("expected ErrDouyinSignatureInvalid for expired signature timestamp, got %v", err)
	}

	// 合法签名但超过有效期后 BuildDouyinH5ShareURL 拒绝。
	goodAdapter := NewDouyinAdapter(d, DouyinConfig{ClientKey: "ck_1", AppRef: "app_1", Signer: fakeDouyinSigner{}, Now: func() time.Time { return now }})
	res, err := goodAdapter.Prepare(context.Background(), douyinRequest("h5_share"))
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if _, err := BuildDouyinH5ShareURL(res.ClientAction.Params, now.Add(15*time.Minute)); !errors.Is(err, ErrDouyinSignatureInvalid) {
		t.Fatalf("expected signature skew rejection after 15m, got %v", err)
	}
	tampered := map[string]string{}
	for k, v := range res.ClientAction.Params {
		tampered[k] = v
	}
	tampered["state"] = "forged_share_id"
	if _, err := BuildDouyinH5ShareURL(tampered, now); !errors.Is(err, ErrDouyinSignatureInvalid) {
		t.Fatalf("state != share_id must be rejected, got %v", err)
	}
}

// 错 App / 错租户 / 错门店 / 错上下文 / 内容换版 严格拒绝。
func TestDouyinWrongAppWrongTenantWrongStoreAndContentChangeRejected(t *testing.T) {
	d := adapterTestDB(t)
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	a := NewDouyinAdapter(d, DouyinConfig{
		ClientKey: "ck_1", AppRef: "app_1",
		Signer: fakeDouyinSigner{},
		Query:  fakeDouyinQuery{state: "published", published: true, item: "item_1"},
		Now:    func() time.Time { return now },
	})
	req := douyinRequest("h5_share")
	r1, err := a.Prepare(context.Background(), req)
	if err != nil {
		t.Fatalf("prepare1: %v", err)
	}
	shareID := r1.ClientAction.Params["share_id"]

	// 1) 错 App (app_ref 或 client_key 不符)
	if _, err := a.ResolveShareBinding(context.Background(), shareID, DouyinBindingExpectation{
		TenantID: "tnt_1", StoreID: "st_1", AppRef: "app_other", AppID: "ck_1", Now: now,
	}); !errors.Is(err, ErrDouyinAppMismatch) {
		t.Fatalf("wrong app_ref expected ErrDouyinAppMismatch, got %v", err)
	}
	if _, err := a.ResolveShareBinding(context.Background(), shareID, DouyinBindingExpectation{
		TenantID: "tnt_1", StoreID: "st_1", AppRef: "app_1", AppID: "ck_other", Now: now,
	}); !errors.Is(err, ErrDouyinAppMismatch) {
		t.Fatalf("wrong app_id expected ErrDouyinAppMismatch, got %v", err)
	}
	wrongAppAdapter := NewDouyinAdapter(d, DouyinConfig{
		ClientKey: "ck_other", AppRef: "app_other",
		Query: fakeDouyinQuery{state: "published", published: true, item: "item_1"},
		Now:   func() time.Time { return now },
	})
	qWrongApp, err := wrongAppAdapter.Query(context.Background(), req.Handoff.Handoff)
	if err != nil || qWrongApp.Receipt != nil || qWrongApp.State != "unknown" {
		t.Fatalf("wrong app Query must not confirm: %+v err=%v", qWrongApp, err)
	}

	// 2) 错租户
	if _, err := a.ResolveShareBinding(context.Background(), shareID, DouyinBindingExpectation{
		TenantID: "tnt_other", StoreID: "st_1", AppRef: "app_1", AppID: "ck_1", Now: now,
	}); !errors.Is(err, ErrDouyinTenantMismatch) {
		t.Fatalf("wrong tenant expected ErrDouyinTenantMismatch, got %v", err)
	}
	wrongTenantHandoff := req.Handoff.Handoff
	wrongTenantHandoff.TenantID = "tnt_other"
	qWrongTenant, err := a.Query(context.Background(), wrongTenantHandoff)
	if err != nil || qWrongTenant.Receipt != nil || qWrongTenant.State != "unknown" {
		t.Fatalf("wrong tenant Query must stay unknown: %+v err=%v", qWrongTenant, err)
	}

	// 3) 错门店
	if _, err := a.ResolveShareBinding(context.Background(), shareID, DouyinBindingExpectation{
		TenantID: "tnt_1", StoreID: "st_other", AppRef: "app_1", AppID: "ck_1", Now: now,
	}); !errors.Is(err, ErrDouyinStoreMismatch) {
		t.Fatalf("wrong store expected ErrDouyinStoreMismatch, got %v", err)
	}

	// 4) 错 attempt / action / session
	if _, err := a.ResolveShareBinding(context.Background(), shareID, DouyinBindingExpectation{
		TenantID: "tnt_1", StoreID: "st_1", AttemptID: "att_other", AppRef: "app_1", AppID: "ck_1", Now: now,
	}); !errors.Is(err, ErrDouyinContextMismatch) {
		t.Fatalf("wrong attempt expected ErrDouyinContextMismatch, got %v", err)
	}

	// 5) 内容换版: 旧 share_id 拒绝, Prepare 铸造新 share_id 且作废旧行
	if _, err := a.ResolveShareBinding(context.Background(), shareID, DouyinBindingExpectation{
		TenantID: "tnt_1", StoreID: "st_1", ContentVersion: "fp-v2", AppRef: "app_1", AppID: "ck_1", Now: now,
	}); !errors.Is(err, ErrDouyinContentVersionMismatch) {
		t.Fatalf("content version mismatch expected ErrDouyinContentVersionMismatch, got %v", err)
	}
	if _, err := a.ResolveShareBinding(context.Background(), shareID, DouyinBindingExpectation{
		TenantID: "tnt_1", StoreID: "st_1", ContentPackVersionID: "cpv_v2", AppRef: "app_1", AppID: "ck_1", Now: now,
	}); !errors.Is(err, ErrDouyinContentVersionMismatch) {
		t.Fatalf("content_pack_version_id mismatch expected ErrDouyinContentVersionMismatch, got %v", err)
	}

	reqV2 := douyinRequest("h5_share")
	reqV2.Handoff.ContentVersion = "fp-v2"
	reqV2.Handoff.ContentPackVersionID = "cpv_v2"
	r2, err := a.Prepare(context.Background(), reqV2)
	if err != nil {
		t.Fatalf("prepare2: %v", err)
	}
	if r2.ClientAction.Params["share_id"] == shareID {
		t.Fatal("content change must mint a fresh share_id")
	}
	// 旧 share_id 已被作废
	if _, err := a.ResolveShareBinding(context.Background(), shareID, DouyinBindingExpectation{
		TenantID: "tnt_1", StoreID: "st_1", ContentVersion: "fp-v1", AppRef: "app_1", AppID: "ck_1", Now: now,
	}); !errors.Is(err, ErrDouyinShareRevoked) {
		t.Fatalf("revoked old share_id expected ErrDouyinShareRevoked, got %v", err)
	}
}

func TestDouyinShareIDRevokedOnContentChange(t *testing.T) {
	d := adapterTestDB(t)
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	a := NewDouyinAdapter(d, DouyinConfig{Signer: fakeDouyinSigner{}, ClientKey: "ck_1", Now: func() time.Time { return now }})
	r1, err := a.Prepare(context.Background(), douyinRequest("h5_share"))
	if err != nil {
		t.Fatalf("prepare1: %v", err)
	}
	req := douyinRequest("h5_share")
	req.Handoff.ContentVersion = "fp-v2"
	r2, err := a.Prepare(context.Background(), req)
	if err != nil {
		t.Fatalf("prepare2: %v", err)
	}
	if r1.ClientAction.Params["share_id"] == r2.ClientAction.Params["share_id"] {
		t.Fatal("content change must mint a fresh share_id")
	}
}

// 微信容器阻拦: ios_wechat 与 android_wechat 严格拒发可执行载荷,不写 share_id。
func TestDouyinWechatContainerBlocked(t *testing.T) {
	d := adapterTestDB(t)
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	a := NewDouyinAdapter(d, DouyinConfig{
		ClientKey: "ck_1", AppRef: "app_1", MiniappPath: "pages/task/index",
		Signer: fakeDouyinSigner{}, Now: func() time.Time { return now },
	})
	for _, env := range []string{channeladmission.EnvIOSWechat, channeladmission.EnvAndroidWechat} {
		for _, method := range []string{"h5_share", "miniapp"} {
			req := douyinRequest(method)
			req.Environment = env
			req.Handoff.Environment = env
			_, err := a.Prepare(context.Background(), req)
			if !errors.Is(err, ErrDouyinWechatContainerBlocked) || !errors.Is(err, ErrCustomerActionRequired) {
				t.Fatalf("env=%s method=%s expected ErrDouyinWechatContainerBlocked, got %v", env, method, err)
			}
		}
	}
	var count int
	if err := d.QueryRow(`SELECT COUNT(1) FROM douyin_share_ids`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("blocked WeChat container must not mint share_ids, got %d", count)
	}
}

// 中文与特殊字符 URL 规范编码,以及非法/危险 URL 拒绝。
func TestDouyinChineseAndSpecialCharURLEncodingAndRejection(t *testing.T) {
	d := adapterTestDB(t)
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	a := NewDouyinAdapter(d, DouyinConfig{
		ClientKey: "ck_1", AppRef: "app_1", Signer: fakeDouyinSigner{}, Now: func() time.Time { return now },
	})

	req := douyinRequest("h5_share")
	req.FieldSupport = map[string]string{
		"media": channeladmission.CapSupported,
		"title": channeladmission.CapSupported,
		"body":  channeladmission.CapUnsupported,
		"topic": channeladmission.CapSupported,
	}
	req.Text = publishhandoff.TextInput{
		Title:  "中秋特惠 & 8折+满减 #南岸店",
		Body:   "正文不允许自动拼参",
		Topics: []string{"南岸探店", "中秋&国庆", "A+B=C"},
	}
	rawMedia := "https://media.example/门店视频/中秋 特惠+2026.mp4?门店=南岸旗舰店&折扣=8折&tag=a+b"
	req.MediaRefs = map[string]string{"mvid": rawMedia}

	res, err := a.Prepare(context.Background(), req)
	if err != nil {
		t.Fatalf("prepare chinese media url: %v", err)
	}
	vp := res.ClientAction.Params["video_path"]
	if vp != res.ClientAction.Params["media_url"] {
		t.Fatalf("video_path and media_url must match canonical form: %q vs %q", vp, res.ClientAction.Params["media_url"])
	}
	if strings.Contains(vp, "门店") || strings.Contains(vp, " ") {
		t.Fatalf("canonical URL must percent-encode Chinese and spaces: %q", vp)
	}
	if _, hasBody := res.ClientAction.Params["body"]; hasBody {
		t.Fatalf("unsupported body field must not enter params: %+v", res.ClientAction.Params)
	}
	if _, hasGenericMedia := res.ClientAction.Params["media"]; hasGenericMedia {
		t.Fatalf("generic media query param must not exist: %+v", res.ClientAction.Params)
	}

	// 验证官方唤起 schema URL 的规范编码与无损解码
	shareURL, err := BuildDouyinH5ShareURL(res.ClientAction.Params, now)
	if err != nil {
		t.Fatalf("BuildDouyinH5ShareURL: %v", err)
	}
	if !strings.HasPrefix(shareURL, "snssdk1128://openplatform/share?") {
		t.Fatalf("unexpected share schema: %q", shareURL)
	}
	parsed, err := url.Parse(shareURL)
	if err != nil {
		t.Fatalf("parse shareURL: %v", err)
	}
	if parsed.Query().Get("title") != req.Text.Title {
		t.Fatalf("title roundtrip mismatch: got %q want %q", parsed.Query().Get("title"), req.Text.Title)
	}
	if parsed.Query().Get("video_path") != vp {
		t.Fatalf("video_path roundtrip mismatch: got %q want %q", parsed.Query().Get("video_path"), vp)
	}
	var gotTopics []string
	if err := json.Unmarshal([]byte(parsed.Query().Get("hashtag_list")), &gotTopics); err != nil || len(gotTopics) != 3 || gotTopics[1] != "中秋&国庆" {
		t.Fatalf("hashtag_list roundtrip mismatch: %q err=%v", parsed.Query().Get("hashtag_list"), err)
	}

	// 危险/非法 URL 全部拒绝(且不浪费 share_id)
	badURLs := []string{
		"",
		"http://media.example/video.mp4",
		"javascript:alert(1)",
		"https://user:pass@media.example/video.mp4",
		"https://media.example/video.mp4#frag",
		"https://media.example/video.mp4\r\nX-Injected: 1",
		"https://media.example/video.mp4?bad=%ZZ",
	}
	for _, bad := range badURLs {
		if _, err := CanonicalizeDouyinMediaURL(bad); err == nil {
			t.Fatalf("expected rejection for bad media URL %q", bad)
		}
	}
}

// 绝不自造"抖音 HMAC 协议": Submit 与 VerifyCallback 始终返回 ErrUnsupported,
// 经 Service.ApplyPlatformCallback 始终返回 ErrReceiptUnverified。
func TestDouyinSubmitAndCallbackUnsupported(t *testing.T) {
	d := adapterTestDB(t)
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	a := NewDouyinAdapter(d, DouyinConfig{Signer: fakeDouyinSigner{}, ClientKey: "ck_1", AppRef: "app_1", Now: func() time.Time { return now }})
	if err := a.Submit(context.Background(), publishhandoff.Handoff{}, publishhandoff.PrepareResult{}); !isUnsupported(err) {
		t.Fatalf("submit err = %v", err)
	}
	if _, err := a.VerifyCallback(context.Background(), publishhandoff.CallbackPayload{
		Platform: "douyin",
		Headers:  map[string]string{"x-douyin-signature": "forged-hmac"},
		Body:     []byte(`{"share_id":"s1","event":"create_video"}`),
	}); !isUnsupported(err) {
		t.Fatalf("verify callback err = %v", err)
	}
	svc := publishhandoff.New(d, publishhandoff.Options{Now: func() time.Time { return now }, AppRef: "app_1"})
	svc.SetAdapter(a)
	if _, err := svc.ApplyPlatformCallback(context.Background(), "douyin", publishhandoff.CallbackPayload{
		Platform: "douyin",
		Headers:  map[string]string{"x-douyin-signature": "forged-hmac"},
		Body:     []byte(`{"share_id":"s1","outcome":"published"}`),
	}); !errors.Is(err, publishhandoff.ErrReceiptUnverified) {
		t.Fatalf("ApplyPlatformCallback must refuse forged douyin webhook with ErrReceiptUnverified, got %v", err)
	}
}

func isUnsupported(err error) bool {
	return err != nil && contains(err.Error(), "capability not supported")
}

// 事实分级: 审核中 / 公开范围受限 / 后续删除不被 create 抹平 / 回执缺失保持 unknown。
func TestDouyinFactClassificationModerationVisibilityDeletionAndUnknown(t *testing.T) {
	d := adapterTestDB(t)
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)

	qSrc := &mutableDouyinQuery{}
	a := NewDouyinAdapter(d, DouyinConfig{
		ClientKey: "ck_1", AppRef: "app_1",
		Signer: fakeDouyinSigner{}, Query: qSrc,
		Now: func() time.Time { return now },
	})
	req := douyinRequest("h5_share")
	if _, err := a.Prepare(context.Background(), req); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	h := req.Handoff.Handoff

	// 1) 审核中(即使平台误传 published=true,审核中绝不确认发布)
	qSrc.set("auditing", true, "item_100", map[string]string{"audit_status": "under_review"})
	out, err := a.Query(context.Background(), h)
	if err != nil || out.Receipt != nil || out.Detail["fact_level"] != string(DouyinFactUnderReview) {
		t.Fatalf("auditing must never mint confirming receipt: %+v err=%v", out, err)
	}

	// 2) 公开范围受限(仅自己可见 / 好友可见,即使 published=true 也不确认公开发布)
	qSrc.set("published", true, "item_100", map[string]string{"visibility_scope": "self_only"})
	out, err = a.Query(context.Background(), h)
	if err != nil || out.Receipt != nil || out.Detail["fact_level"] != string(DouyinFactVisibilityRestricted) {
		t.Fatalf("self_only visibility must never mint confirming receipt: %+v err=%v", out, err)
	}

	// 3) 明确公开发布 → 铸造 Receipt
	qSrc.set("published", true, "item_100", map[string]string{"visibility_scope": "public", "moderation_state": "approved"})
	out, err = a.Query(context.Background(), h)
	if err != nil || out.Receipt == nil || out.Detail["fact_level"] != string(DouyinFactOfficialPublished) {
		t.Fatalf("explicit public published must mint receipt: %+v err=%v", out, err)
	}

	// 4) 后续删除: 绝不被之前的 create/published 抹平, 落库 deleted_at 且不产出确认回执
	qSrc.set("deleted", false, "item_100", map[string]string{"deleted": "true"})
	out, err = a.Query(context.Background(), h)
	if err != nil || out.Receipt != nil || out.State != "deleted" || out.Detail["fact_level"] != string(DouyinFactDeleted) {
		t.Fatalf("subsequent deletion must be preserved without confirming receipt: %+v err=%v", out, err)
	}
	var dbState, dbDeletedAt string
	if err := d.QueryRow(`SELECT state, deleted_at FROM douyin_provider_refs WHERE handoff_id=? AND tenant_id=?`, h.ID, h.TenantID).Scan(&dbState, &dbDeletedAt); err != nil {
		t.Fatalf("read provider_ref: %v", err)
	}
	if dbState != "deleted" || dbDeletedAt == "" {
		t.Fatalf("deletion fact not persisted: state=%q deleted_at=%q", dbState, dbDeletedAt)
	}

	// 5) 客户端弱词(returned_to_h5 / editor_opened / app_invoked / created)绝不确认发布
	for _, weak := range []string{"", "unknown", "created", "editor_opened", "app_invoked", "returned_to_h5"} {
		obs := ClassifyDouyinObservation(weak, true, nil)
		if obs.ConfirmsPublish {
			t.Fatalf("weak state %q must never confirm publication: %+v", weak, obs)
		}
	}
}

// 弱信号(回 H5 / 唤起 App / 点击 / 自报)绝不升级为发布确认; 顾客取消不可复活;
// 乱序重放(早到拒绝 / 终态后晚到拒绝 / 同 receipt_id 幂等去重);
// 人工先兑奖时晚到官方事件绝不重复发奖(不修改奖励账)。
func TestDouyinWeakSignalsCancelOutOfOrderReplayAndManualRewardNoDuplicate(t *testing.T) {
	d := adapterTestDB(t)
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	seedFrozenAttemptForDouyin(t, d, "tnt_1", "st_1", "par_1", "att_1")

	receiptSecret := "fixture-hmac-for-service-pipeline-only"
	svc := publishhandoff.New(d, publishhandoff.Options{
		Now:             func() time.Time { return now },
		AppRef:          "app_1",
		ReceiptVerifier: publishhandoff.NewHMACVerifier(receiptSecret),
	})
	qSrc := &mutableDouyinQuery{state: "published", published: true, item: "aweme_late_1"}
	adapter := NewDouyinAdapter(d, DouyinConfig{
		ClientKey: "ck_1", AppRef: "app_1",
		Signer: fakeDouyinSigner{}, Query: qSrc,
		Now: func() time.Time { return now },
	})
	svc.SetAdapter(adapter)

	rec := admittedDouyinCapability(t, channeladmission.ActionMethodH5Share, channeladmission.ActionScenarioStorePromo)
	q := channeladmission.ActionQuery{Now: now, TenantID: "tnt_1", Environment: channeladmission.EnvIOSSafari}
	planPayload := publishhandoff.ActionPlanInput{
		Text:      publishhandoff.TextInput{Title: "探店实拍", Body: "欢迎到店"},
		Media:     []publishhandoff.MediaItem{{Kind: publishhandoff.MediaVideo, AssetID: "vid_1", SHA256: strings.Repeat("d", 64), ContentType: "video/mp4", Filename: "v.mp4"}},
		MediaRefs: map[string]string{"vid_1": "https://media.example/v.mp4"},
	}
	confirm, _ := publishhandoff.ConfirmationFor(publishhandoff.PayloadKindMedia, planPayload)

	// --- 场景 A: 弱信号(点击/唤起/返回 H5/自报)永不升级 + 乱序早到拒绝 + 人工先兑奖后晚到官方事件不重复发奖 ---
	resA, err := svc.StartAction(context.Background(), publishhandoff.ActionStartInput{
		TenantID: "tnt_1", StoreID: "st_1", ActionID: "sa_1", CatalogCode: "A01",
		Platform: "douyin", SessionID: "sess_a", VisitorID: "vis_a",
		ActionRevision: 1, PayloadKind: publishhandoff.PayloadKindMedia,
		Method: channeladmission.ActionMethodH5Share, Environment: channeladmission.EnvIOSSafari,
		Scenario: channeladmission.ActionScenarioStorePromo,
		ParticipationID: "par_1", AttemptID: "att_1",
		IdempotencyKey: "idem-a", Confirmation: confirm,
		Record: rec, Query: q, PlanPayload: planPayload, ContentPackVersionID: "cpv_1",
	})
	if err != nil {
		t.Fatalf("start A: %v", err)
	}

	// 1) 记录弱信号: customer_click / wake_attempt / media_handoff / customer_self_report(h5_return)
	for i, kind := range []string{
		publishhandoff.EventCustomerClick,
		publishhandoff.EventWakeAttempt,
		publishhandoff.EventMediaHandoff,
		publishhandoff.EventCustomerSelfReport,
	} {
		hAfter, _, err := svc.RecordActionFact(context.Background(), resA.Handoff.ID, "vis_a", kind, "customer_h5", "evt-weak-"+string(rune('a'+i)), map[string]any{"signal": "h5_return"})
		if err != nil {
			t.Fatalf("record weak fact %s: %v", kind, err)
		}
		if hAfter.Status != publishhandoff.StateHandoffReady {
			t.Fatalf("weak signal %s must never advance state, got %s", kind, hAfter.Status)
		}
	}
	// 与 publishflow 联验: 回 H5 / 唤起 / 失焦 / 点击 / 自报全部被阻断,不升级为官方发布确认。
	reconciledWeak := publishflow.ReconcilePublishAndRewardFacts(publishflow.FactReconcileInput{
		CurrentPublishStatus: publishflow.PublishFactHandoffPrepared,
		CurrentRewardStatus:  publishflow.RewardFactNone,
		HasCampaign:          true,
		CampaignID:           "cmp_dy_1",
		Events: []publishflow.IncomingPublishEvent{
			{Kind: publishflow.SignalCustomerClick},
			{Kind: publishflow.SignalWakeAttempt},
			{Kind: publishflow.SignalWindowBlur},
			{Kind: publishflow.SignalMediaHandoff},
			{Kind: publishflow.SignalH5Return},
			{Kind: publishflow.SignalSelfReport},
		},
	})
	if reconciledWeak.PublishFact.OfficialConfirmed || reconciledWeak.PublishFact.Status != publishflow.PublishFactHandoffPrepared {
		t.Fatalf("weak H5/wake signals must never confirm publish: %+v", reconciledWeak.PublishFact)
	}
	if reconciledWeak.EscalationBlockedCount != 6 {
		t.Fatalf("expected 6 blocked weak signals, got %d", reconciledWeak.EscalationBlockedCount)
	}

	// 2) 乱序早到回执(handoff_ready 未进入 submitting/awaiting_review/unknown): 拒绝且状态不动。
	earlyReceipt := publishhandoff.Receipt{
		ReceiptID: "rcp-early-1", Platform: "douyin", AppRef: "app_1",
		TenantID: "tnt_1", ParticipationID: "par_1", AttemptID: "att_1",
		ContentVersion: confirm, HandoffID: resA.Handoff.ID, PostID: "aweme_late_1",
		Outcome: publishhandoff.ReceiptOutcomePublished, SignedAt: now,
	}
	sigEarly, _ := publishhandoff.SignReceiptForTest(receiptSecret, earlyReceipt)
	if _, err := svc.ApplyReceipt(context.Background(), "tnt_1", resA.Handoff.ID, publishhandoff.ReceiptWire{Receipt: earlyReceipt, Signature: sigEarly}); err == nil {
		t.Fatal("out-of-order receipt on handoff_ready must be rejected")
	}

	// 3) 人工先核验并先兑奖(记录到 reward_benefits 模拟 1671 已发放),随后晚到官方事件到达:
	//    交接可如实补记官方确认,但绝不修改奖励账、绝不重复发奖!
	afterStaff, err := svc.RecordStaffEvent(context.Background(), "tnt_1", resA.Handoff.ID, publishhandoff.StaffActor{ID: "stf_1", Name: "店员甲"}, publishhandoff.FactStaffVerifiedWork, "核对作品已兑奖")
	if err != nil || afterStaff.Status != publishhandoff.StateAwaitingReview {
		t.Fatalf("RecordStaffEvent: status=%v err=%v", afterStaff.Status, err)
	}
	// 记录人工先兑奖事实(1671 reward_benefits 唯一约束: tenant_id + participation_id + benefit_slot)
	if _, err := d.Exec(`INSERT INTO reward_inventory
		(id, tenant_id, campaign_id, store_id, kind, name, benefit_slot, rule_version, allowed_evidence,
		 total_qty, face_value_minor, cost_minor, currency, status, created_at)
		VALUES ('rinv_1','tnt_1','cmp_dy_1','st_1','store_coupon','探店券','slot_1',1,'staff_verified_post,official_publish_confirmed',
		 100,500,300,'CNY','active','2026-11-11T09:00:00Z')`); err != nil {
		t.Fatalf("seed reward inventory: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO reward_benefits
		(id, tenant_id, campaign_id, inventory_id, participation_id, benefit_slot, rule_version, evidence,
		 status, store_id, face_value_minor, cost_minor, currency, issued_at, issued_by, created_at, updated_at)
		VALUES ('rb_manual_1','tnt_1','cmp_dy_1','rinv_1','par_1','slot_1',1,'staff_verified_post',
		 'issued','st_1',500,300,'CNY','2026-11-11T09:01:00Z','stf_1','2026-11-11T09:01:00Z','2026-11-11T09:01:00Z')`); err != nil {
		t.Fatalf("seed manual issued benefit: %v", err)
	}

	// 晚到官方事件经 Query 产出的受信 Receipt 回流 confirmPipeline:
	qOut, err := adapter.Query(context.Background(), afterStaff)
	if err != nil || qOut.Receipt == nil {
		t.Fatalf("adapter.Query for late official result: out=%+v err=%v", qOut, err)
	}
	sigLate, _ := publishhandoff.SignReceiptForTest(receiptSecret, *qOut.Receipt)
	confirmed, err := svc.ApplyReceipt(context.Background(), "tnt_1", resA.Handoff.ID, publishhandoff.ReceiptWire{Receipt: *qOut.Receipt, Signature: sigLate})
	if err != nil || confirmed.Status != publishhandoff.StatePlatformConfirmed {
		t.Fatalf("late official receipt on awaiting_review failed: status=%v err=%v", confirmed.Status, err)
	}
	// 重复重放同一 receipt_id: 幂等,不新增第二条 platform_confirm 事件。
	replayed, err := svc.ApplyReceipt(context.Background(), "tnt_1", resA.Handoff.ID, publishhandoff.ReceiptWire{Receipt: *qOut.Receipt, Signature: sigLate})
	if err != nil || replayed.Status != publishhandoff.StatePlatformConfirmed {
		t.Fatalf("idempotent replay failed: status=%v err=%v", replayed.Status, err)
	}
	var confirmEvents int
	if err := d.QueryRow(`SELECT COUNT(1) FROM publish_handoff_events WHERE handoff_id=? AND kind=?`, resA.Handoff.ID, publishhandoff.EventPlatformConfirm).Scan(&confirmEvents); err != nil {
		t.Fatal(err)
	}
	if confirmEvents != 1 {
		t.Fatalf("replayed receipt must keep exactly 1 platform_confirm event, got %d", confirmEvents)
	}
	// 断言奖励账未被修改,且同 participation+benefit_slot 绝不可重复发奖!
	var benefitCount int
	var benefitEvidence, benefitStatus string
	if err := d.QueryRow(`SELECT COUNT(1) FROM reward_benefits WHERE tenant_id='tnt_1' AND participation_id='par_1'`).Scan(&benefitCount); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT evidence, status FROM reward_benefits WHERE id='rb_manual_1'`).Scan(&benefitEvidence, &benefitStatus); err != nil {
		t.Fatal(err)
	}
	if benefitCount != 1 || benefitEvidence != "staff_verified_post" || benefitStatus != "issued" {
		t.Fatalf("late official event must never alter or duplicate reward ledger: count=%d evidence=%q status=%q", benefitCount, benefitEvidence, benefitStatus)
	}
	// 尝试用晚到官方事件对同一 (tenant, participation, benefit_slot) 重复写入奖励账 → 被数据库唯一约束拒绝!
	if _, dupErr := d.Exec(`INSERT INTO reward_benefits
		(id, tenant_id, campaign_id, inventory_id, participation_id, benefit_slot, rule_version, evidence,
		 status, store_id, face_value_minor, cost_minor, currency, issued_at, issued_by, created_at, updated_at)
		VALUES ('rb_dup_official','tnt_1','cmp_dy_1','rinv_1','par_1','slot_1',1,'official_publish_confirmed',
		 'issued','st_1',500,300,'CNY','2026-11-11T09:05:00Z','sys','2026-11-11T09:05:00Z','2026-11-11T09:05:00Z')`); dupErr == nil {
		t.Fatal("duplicate grant on same participation+benefit_slot must be blocked by ledger uniqueness")
	}

	// --- 场景 B: 顾客取消(终态)后晚到官方查询/回执绝不复活 ---
	resB, err := svc.StartAction(context.Background(), publishhandoff.ActionStartInput{
		TenantID: "tnt_1", StoreID: "st_1", ActionID: "sa_1", CatalogCode: "A01",
		Platform: "douyin", SessionID: "sess_b", VisitorID: "vis_b",
		ActionRevision: 1, PayloadKind: publishhandoff.PayloadKindMedia,
		Method: channeladmission.ActionMethodH5Share, Environment: channeladmission.EnvIOSSafari,
		Scenario: channeladmission.ActionScenarioStorePromo,
		IdempotencyKey: "idem-b", Confirmation: confirm,
		Record: rec, Query: q, PlanPayload: planPayload, ContentPackVersionID: "cpv_1",
	})
	if err != nil {
		t.Fatalf("start B: %v", err)
	}
	canceled, err := svc.CancelAction(context.Background(), resB.Handoff.ID, "vis_b", "用户未登录取消")
	if err != nil || canceled.Status != publishhandoff.StateCanceled {
		t.Fatalf("cancel B: %+v err=%v", canceled, err)
	}
	// 终态查询绝不铸造确认回执
	qCanceled, err := adapter.Query(context.Background(), canceled.Handoff)
	if err != nil || qCanceled.Receipt != nil {
		t.Fatalf("canceled handoff must never mint confirming receipt: %+v err=%v", qCanceled, err)
	}
	// 作废 share_id 后反查严格返回 ErrDouyinShareRevoked
	if err := adapter.RevokeShareBinding(context.Background(), "tnt_1", resB.Handoff.ID); err != nil {
		t.Fatal(err)
	}
	shareB := resB.Handoff.ClientAction.Params["share_id"]
	if _, err := adapter.ResolveShareBinding(context.Background(), shareB, DouyinBindingExpectation{TenantID: "tnt_1", AppRef: "app_1", AppID: "ck_1", Now: now}); !errors.Is(err, ErrDouyinShareRevoked) {
		t.Fatalf("canceled share_id must be revoked, got %v", err)
	}
}

// 零 secret / open_ticket / token 泄露: 公开参数、错误消息、QueryOutcome.Detail、结构化评估全链路校验。
func TestDouyinNoSecretOrInternalDiagnosticLeakage(t *testing.T) {
	d := adapterTestDB(t)
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)

	const (
		secretVal = "super_secret_douyin_999"
		ticketVal = "open_ticket_private_888"
		bearerVal = "bearer_internal_777"
	)

	// 1) 签名器即使夹带敏感字段,白名单剥离后零泄露进 ClientAction.Params 与 ShareURL
	leakySigner := stubDouyinSigner{
		signFn: func(_ context.Context, in DouyinShareSignInput) (map[string]string, error) {
			return map[string]string{
				"nonce_str":     "safe-nonce",
				"timestamp":     in.TimestampSec,
				"signature":     "safe-sig",
				"client_secret": secretVal,
				"open_ticket":   ticketVal,
				"access_token":  "tok_private",
			}, nil
		},
	}
	a := NewDouyinAdapter(d, DouyinConfig{
		ClientKey: "ck_pub_1", AppRef: "app_1",
		Signer: leakySigner,
		Query: stubDouyinQuery{
			queryFn: func(_ context.Context, _ string) (string, bool, string, map[string]string, error) {
				return "", false, "", nil, errors.New("upstream rejected client_secret=" + secretVal + " Bearer " + bearerVal)
			},
		},
		Now: func() time.Time { return now },
	})

	res, err := a.Prepare(context.Background(), douyinRequest("h5_share"))
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	rawParams, _ := json.Marshal(res)
	for _, forbidden := range []string{secretVal, ticketVal, bearerVal, "client_secret", "open_ticket", "access_token"} {
		if strings.Contains(string(rawParams), forbidden) {
			t.Fatalf("public PrepareResult leaked %q: %s", forbidden, string(rawParams))
		}
	}

	// 2) Query 传输错误即使含 secret/bearer, Detail 中必须脱敏, 绝不泄露
	qOut, err := a.Query(context.Background(), douyinRequest("h5_share").Handoff.Handoff)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	rawQuery, _ := json.Marshal(qOut)
	for _, forbidden := range []string{secretVal, ticketVal, bearerVal} {
		if strings.Contains(string(rawQuery), forbidden) {
			t.Fatalf("QueryOutcome leaked %q: %s", forbidden, string(rawQuery))
		}
	}

	// 3) Query 成功返回里若混入敏感 key, sanitizeDouyinDetailMap 必须剥离
	a.query = stubDouyinQuery{
		queryFn: func(_ context.Context, _ string) (string, bool, string, map[string]string, error) {
			return "auditing", false, "item_x", map[string]string{
				"audit_status":  "under_review",
				"client_secret": secretVal,
				"access_token":  ticketVal,
				"note":          "ticket=" + ticketVal,
			}, nil
		},
	}
	qOut2, err := a.Query(context.Background(), douyinRequest("h5_share").Handoff.Handoff)
	if err != nil {
		t.Fatalf("query2: %v", err)
	}
	rawQuery2, _ := json.Marshal(qOut2)
	for _, forbidden := range []string{secretVal, ticketVal, "client_secret", "access_token"} {
		if strings.Contains(string(rawQuery2), forbidden) {
			t.Fatalf("QueryOutcome detail leaked %q: %s", forbidden, string(rawQuery2))
		}
	}

	// 4) DouyinHTTP 缺 secret 不发空凭据请求,且错误不泄露密钥
	httpCli := NewDouyinHTTP("ck_only", "", "", "")
	if _, err := httpCli.SignShare(context.Background(), DouyinShareSignInput{TimestampSec: "1700000000"}); err == nil {
		t.Fatal("DouyinHTTP without secret must refuse signing")
	}
}

// 并发 -race 安全测试: 多 goroutine 并发 Prepare / Query / ResolveShareBinding / AssessRoute。
func TestDouyinConcurrentPrepareQueryAndResolveRaceSafe(t *testing.T) {
	d := adapterTestDB(t)
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	a := NewDouyinAdapter(d, DouyinConfig{
		ClientKey: "ck_race", AppRef: "app_race",
		Signer: fakeDouyinSigner{},
		Query:  fakeDouyinQuery{state: "auditing", published: false, item: "item_race"},
		Now:    func() time.Time { return now },
	})
	rec := admittedDouyinCapability(t, channeladmission.ActionMethodH5Share, channeladmission.ActionScenarioStorePromo)
	q := channeladmission.ActionQuery{Now: now, TenantID: "tnt_1", Environment: channeladmission.EnvIOSSafari}

	const workers = 8
	shareIDs := make([]string, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			res, err := a.Prepare(context.Background(), douyinRequest("h5_share"))
			if err != nil {
				errs[idx] = err
				return
			}
			shareIDs[idx] = res.ClientAction.Params["share_id"]
			if _, err := a.ResolveShareBinding(context.Background(), shareIDs[idx], DouyinBindingExpectation{
				TenantID: "tnt_1", StoreID: "st_1", AppRef: "app_race", AppID: "ck_race", Now: now,
			}); err != nil {
				errs[idx] = err
				return
			}
			if _, err := a.Query(context.Background(), douyinRequest("h5_share").Handoff.Handoff); err != nil {
				errs[idx] = err
				return
			}
			_ = a.AssessRoute(DouyinRouteEvalInput{Record: rec, Query: q, Method: DouyinMethodH5})
		}(i)
	}
	wg.Wait()
	for i := 0; i < workers; i++ {
		if errs[i] != nil {
			t.Fatalf("worker %d err: %v", i, errs[i])
		}
		if shareIDs[i] == "" || shareIDs[i] != shareIDs[0] {
			t.Fatalf("concurrent Prepare must idempotently share one share_id: got %v", shareIDs)
		}
	}
}

type fakeDouyinQuery struct {
	state     string
	published bool
	item      string
	err       error
}

func (f fakeDouyinQuery) QueryShare(ctx context.Context, shareID string) (string, bool, string, map[string]string, error) {
	if f.err != nil {
		return "", false, "", nil, f.err
	}
	return f.state, f.published, f.item, map[string]string{"official": "1"}, nil
}

type mutableDouyinQuery struct {
	mu        sync.Mutex
	state     string
	published bool
	item      string
	detail    map[string]string
}

func (m *mutableDouyinQuery) set(state string, published bool, item string, detail map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state, m.published, m.item, m.detail = state, published, item, detail
}

func (m *mutableDouyinQuery) QueryShare(_ context.Context, _ string) (string, bool, string, map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make(map[string]string, len(m.detail))
	for k, v := range m.detail {
		cp[k] = v
	}
	return m.state, m.published, m.item, cp, nil
}

type stubDouyinQuery struct {
	queryFn func(ctx context.Context, shareID string) (string, bool, string, map[string]string, error)
}

func (s stubDouyinQuery) QueryShare(ctx context.Context, shareID string) (string, bool, string, map[string]string, error) {
	return s.queryFn(ctx, shareID)
}

type stubDouyinSigner struct {
	signFn func(ctx context.Context, in DouyinShareSignInput) (map[string]string, error)
}

func (s stubDouyinSigner) SignShare(ctx context.Context, in DouyinShareSignInput) (map[string]string, error) {
	return s.signFn(ctx, in)
}

func (s stubDouyinSigner) NewShareID(context.Context) (string, error) {
	return newAdapterID("fixture_share_"), nil
}

func TestDouyinQueryUnknownWithoutSource(t *testing.T) {
	d := adapterTestDB(t)
	a := NewDouyinAdapter(d, DouyinConfig{Signer: fakeDouyinSigner{}, ClientKey: "ck_1"})
	out, err := a.Query(context.Background(), douyinRequest("h5_share").Handoff.Handoff)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if out.State != "unknown" || out.Receipt != nil {
		t.Fatalf("state=%q receipt=%v", out.State, out.Receipt)
	}
}

func TestDouyinQueryPublishedMintsBoundReceipt(t *testing.T) {
	d := adapterTestDB(t)
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	a := NewDouyinAdapter(d, DouyinConfig{
		Signer: fakeDouyinSigner{}, ClientKey: "ck_1", AppRef: "app_1",
		Query: fakeDouyinQuery{state: "published", published: true, item: "item_9"},
		Now:   func() time.Time { return now },
	})
	h := douyinRequest("h5_share").Handoff.Handoff
	// Prepare 先铸 share_id 映射(StartAction 真实时序)。
	if _, err := a.Prepare(context.Background(), douyinRequest("h5_share")); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	out, err := a.Query(context.Background(), h)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if out.Receipt == nil {
		t.Fatal("expected receipt for explicit published result")
	}
	r := out.Receipt
	if r.HandoffID != h.ID || r.TenantID != h.TenantID || r.PostID != "item_9" {
		t.Fatalf("receipt bindings wrong: %+v", r)
	}
	if r.Outcome != publishhandoff.ReceiptOutcomePublished {
		t.Fatalf("outcome = %q", r.Outcome)
	}
	var item string
	if err := d.QueryRow(`SELECT provider_item FROM douyin_provider_refs WHERE handoff_id=?`, h.ID).Scan(&item); err != nil {
		t.Fatalf("provider ref not saved: %v", err)
	}
	if item != "item_9" {
		t.Fatalf("provider item = %q", item)
	}
}

func TestDouyinQueryFailureStaysUnknown(t *testing.T) {
	d := adapterTestDB(t)
	a := NewDouyinAdapter(d, DouyinConfig{Signer: fakeDouyinSigner{}, ClientKey: "ck_1", Query: fakeDouyinQuery{err: errFake{}}})
	out, err := a.Query(context.Background(), douyinRequest("h5_share").Handoff.Handoff)
	if err != nil {
		t.Fatalf("query must not surface transport errors as state changes: %v", err)
	}
	if out.State != "unknown" || out.Receipt != nil {
		t.Fatalf("state=%q receipt=%v", out.State, out.Receipt)
	}
}

type errFake struct{}

func (errFake) Error() string { return "network down" }

type fakeDouyinSigner struct{}

func (fakeDouyinSigner) SignShare(_ context.Context, in DouyinShareSignInput) (map[string]string, error) {
	return map[string]string{"signature": "fixture-signature", "nonce_str": "fixture-nonce", "timestamp": in.TimestampSec}, nil
}
func (fakeDouyinSigner) NewShareID(context.Context) (string, error) {
	return newAdapterID("fixture_share_"), nil
}
