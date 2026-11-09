package publishhandoff

// plan2_test.go: v2 动作级计划(JudgeActionPlan)——3535 合同 §6 消费方。
//
// 冻结契约(docs/contracts/publish-handoff/v2/handoff-contract.md):
//   - plan.kind 五值:refused|pending_external|manual_only|official_handoff|
//     oauth_publish;
//   - pending_external 不承诺、无手工指引(unknown 永不自动当 manual 许可);
//   - client_action 是类型化描述(绝不返回可执行 JS);
//   - evidence_level 镜像 3535 platform_receipt 单元(supported=official_receipt/
//     unsupported=none/unknown=unknown),绝不越过准入真值升级;
//   - unsupported_fields 逐项解释 unknown/unsupported 字段。

import (
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
)

func envCaps(nav, media, receipt, reward string) channeladmission.EnvCapabilities {
	return channeladmission.EnvCapabilities{
		Navigation: nav, MediaHandoff: media, PlatformReceipt: receipt, RewardEligibility: reward,
	}
}

// seedAction builds an ActionCapability with all six environments set.
func seedAction(t *testing.T, mutate func(*channeladmission.ActionCapability)) channeladmission.ActionCapability {
	t.Helper()
	r := channeladmission.ActionCapability{
		ID: "cap_test", CatalogCode: "A27", Platform: "wechat", Method: channeladmission.ActionMethodLinkShare,
		Scenario: channeladmission.ActionScenarioStorePromo, ActionKind: "publish",
		Label: "分享门店链接", AppBinding: "none", TenantScope: "*",
		Status: channeladmission.StatusAdmitted,
		Environments: map[string]channeladmission.EnvCapabilities{
			channeladmission.EnvDesktopBrowser: envCaps(channeladmission.CapSupported, channeladmission.CapUnsupported, channeladmission.CapUnsupported, channeladmission.CapUnsupported),
			channeladmission.EnvIOSSafari:      envCaps(channeladmission.CapSupported, channeladmission.CapUnsupported, channeladmission.CapUnsupported, channeladmission.CapUnsupported),
			channeladmission.EnvAndroidBrowser: envCaps(channeladmission.CapSupported, channeladmission.CapUnsupported, channeladmission.CapUnsupported, channeladmission.CapUnsupported),
			channeladmission.EnvIOSWechat:      envCaps(channeladmission.CapSupported, channeladmission.CapUnsupported, channeladmission.CapUnsupported, channeladmission.CapUnsupported),
			channeladmission.EnvAndroidWechat:  envCaps(channeladmission.CapSupported, channeladmission.CapUnsupported, channeladmission.CapUnsupported, channeladmission.CapUnsupported),
			channeladmission.EnvTargetApp:      envCaps(channeladmission.CapSupported, channeladmission.CapUnsupported, channeladmission.CapUnsupported, channeladmission.CapUnsupported),
		},
		FieldSupport:   map[string]string{"media": channeladmission.CapUnsupported, "title": channeladmission.CapUnsupported},
		RequiredInputs: []string{},
		EvidenceGrade:  channeladmission.EvidenceOfficialDoc,
		CheckedAt:      "2026-11-09", ValidUntil: "2027-02-07",
	}
	if mutate != nil {
		mutate(&r)
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("seed action: %v", err)
	}
	return r
}

func actionQuery(env string, reward bool) channeladmission.ActionQuery {
	return channeladmission.ActionQuery{
		Now:      time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC),
		TenantID: "tnt_v2", Environment: env,
		RewardPlay: reward, MerchantAppRef: "",
	}
}

// 1. admitted link_share → official_handoff + open_url(服务端 share_url),
// evidence_level=none(receipt=unsupported),无内容包要求。
func TestActionPlanLinkShareOfficialHandoff(t *testing.T) {
	rec := seedAction(t, nil)
	plan := JudgeActionPlan(actionQuery(channeladmission.EnvIOSWechat, false), rec, ActionPlanInput{
		ShareURL: "https://store.example.com/page",
	})
	if plan.Kind != PlanOfficialHandoff || !plan.Allowed {
		t.Fatalf("want official_handoff allowed, got %q allowed=%v", plan.Kind, plan.Allowed)
	}
	if plan.PendingExternal {
		t.Fatal("admitted route is never pending_external")
	}
	if plan.ClientAction == nil || plan.ClientAction.Type != "open_url" || plan.ClientAction.URL != "https://store.example.com/page" {
		t.Fatalf("client action = %+v", plan.ClientAction)
	}
	if plan.EvidenceLevel != EvidenceLevelNone {
		t.Fatalf("receipt=unsupported must map to none, got %q", plan.EvidenceLevel)
	}
}

// 2. unknown → pending_external:无 client_action、无手工指引、缺项照透、
// 不承诺文案。
func TestActionPlanUnknownPendingExternal(t *testing.T) {
	rec := seedAction(t, func(r *channeladmission.ActionCapability) {
		r.Method = channeladmission.ActionMethodJSMedia
		r.Status = channeladmission.StatusUnknown
		r.Missing = []string{"主体控制台核验"}
		for env := range r.Environments {
			r.Environments[env] = envCaps(channeladmission.CapSupported, channeladmission.CapSupported, channeladmission.CapUnknown, channeladmission.CapUnknown)
		}
	})
	plan := JudgeActionPlan(actionQuery(channeladmission.EnvIOSSafari, false), rec, ActionPlanInput{})
	if plan.Kind != PlanPendingExternal || plan.Allowed || !plan.PendingExternal {
		t.Fatalf("want pending_external, got %q", plan.Kind)
	}
	if plan.ClientAction != nil {
		t.Fatalf("pending_external must not carry a client action: %+v", plan.ClientAction)
	}
	if len(plan.Missing) == 0 {
		t.Fatal("pending_external must carry missing items")
	}
	if strings.Contains(plan.Notice, "手动发布指引") || strings.Contains(plan.Notice, "可") && strings.Contains(plan.Notice, "完成") {
		t.Fatalf("notice must not promise a completable path: %q", plan.Notice)
	}
	if plan.EvidenceLevel != EvidenceLevelUnknown {
		t.Fatalf("receipt=unknown must map to unknown, got %q", plan.EvidenceLevel)
	}
}

// 3. not_admitted → refused:无入口无指引。
func TestActionPlanNotAdmittedRefused(t *testing.T) {
	rec := seedAction(t, func(r *channeladmission.ActionCapability) {
		r.Platform, r.Method = "douyin", channeladmission.ActionMethodH5Share
		r.Status = channeladmission.StatusNotAdmitted
		r.ReasonText = "官方规范明确排除商铺宣传"
	})
	plan := JudgeActionPlan(actionQuery(channeladmission.EnvIOSSafari, false), rec, ActionPlanInput{})
	if plan.Kind != PlanRefused || plan.Allowed {
		t.Fatalf("want refused, got %q", plan.Kind)
	}
	if plan.ClientAction != nil {
		t.Fatal("refused must not carry a client action")
	}
	if plan.Reason != channeladmission.ReasonNotAdmitted {
		t.Fatalf("reason = %q", plan.Reason)
	}
}

// 4. admitted manual_material → manual_only + manual_export client action。
func TestActionPlanManualMaterialManualOnly(t *testing.T) {
	rec := seedAction(t, func(r *channeladmission.ActionCapability) {
		r.Method = channeladmission.ActionMethodManualMaterial
		for env := range r.Environments {
			r.Environments[env] = envCaps(channeladmission.CapUnsupported, channeladmission.CapSupported, channeladmission.CapUnsupported, channeladmission.CapUnsupported)
		}
		r.FieldSupport = map[string]string{"media": channeladmission.CapSupported, "title": channeladmission.CapUnknown, "body": channeladmission.CapUnsupported}
	})
	plan := JudgeActionPlan(actionQuery(channeladmission.EnvIOSSafari, false), rec, ActionPlanInput{
		Text:      TextInput{Title: "周末烘焙节", Body: "全场八折", Topics: []string{"烘焙"}},
		Media:     []MediaItem{{Kind: MediaVideo, AssetID: "md_1", SHA256: strings.Repeat("a", 64), ContentType: "video/mp4"}},
		MediaRefs: map[string]string{"md_1": "https://media.example.com/t1"},
	})
	if plan.Kind != PlanManualOnly || !plan.Allowed {
		t.Fatalf("want manual_only, got %q", plan.Kind)
	}
	if plan.ClientAction == nil || plan.ClientAction.Type != "manual_export" {
		t.Fatalf("client action = %+v", plan.ClientAction)
	}
	if !strings.Contains(plan.ClientAction.Caption, "周末烘焙节") {
		t.Fatalf("caption = %q", plan.ClientAction.Caption)
	}
	if !strings.Contains(plan.ClientAction.MediaNote, "https://media.example.com/t1") {
		t.Fatalf("media note = %q", plan.ClientAction.MediaNote)
	}
	// unsupported_fields:unknown/unsupported 字段逐项解释(title=unknown)。
	found := false
	for _, u := range plan.UnsupportedFields {
		if strings.Contains(u, "标题") {
			found = true
		}
	}
	if !found {
		t.Fatalf("unsupported_fields must explain unknown title: %v", plan.UnsupportedFields)
	}
}

// 5. admitted oauth_server_push(未接 adapter)→ oauth_publish kind + 诚实
// 手动导出回退(自动化通道未接入,不承诺自动投稿)。
func TestActionPlanOAuthPublishFallbackWithoutAdapter(t *testing.T) {
	rec := seedAction(t, func(r *channeladmission.ActionCapability) {
		r.Platform, r.Method = "kuaishou", channeladmission.ActionMethodOAuthPush
		for env := range r.Environments {
			r.Environments[env] = envCaps(channeladmission.CapSupported, channeladmission.CapSupported, channeladmission.CapUnknown, channeladmission.CapUnknown)
		}
	})
	plan := JudgeActionPlan(actionQuery(channeladmission.EnvDesktopBrowser, false), rec, ActionPlanInput{
		Text: TextInput{Body: "正文"},
	})
	if plan.Kind != PlanOAuthPublish {
		t.Fatalf("want oauth_publish, got %q", plan.Kind)
	}
	if plan.ClientAction == nil || plan.ClientAction.Type != "manual_export" {
		t.Fatalf("unwired oauth must fall back to manual export, got %+v", plan.ClientAction)
	}
	if !strings.Contains(plan.ClientAction.Note, "未接入") {
		t.Fatalf("fallback note must be honest: %q", plan.ClientAction.Note)
	}
}

// 6. reward bypass:RewardPlay 是服务端派生事实——reward_task 场景记录在
// reward 单元非 supported 时拒绝,调用方无法用客户端 reward=false 绕过
// (ActionQuery 由服务端组装,本测试锁定 JudgeActionPlan 尊重该输入)。
func TestActionPlanRewardPlayNotBypassable(t *testing.T) {
	rec := seedAction(t, func(r *channeladmission.ActionCapability) {
		r.Scenario = channeladmission.ActionScenarioRewardTask
		for env := range r.Environments {
			r.Environments[env] = envCaps(channeladmission.CapSupported, channeladmission.CapSupported, channeladmission.CapSupported, channeladmission.CapUnsupported)
		}
	})
	plan := JudgeActionPlan(actionQuery(channeladmission.EnvIOSSafari, true), rec, ActionPlanInput{})
	if plan.Kind != PlanRefused || plan.Reason != channeladmission.ReasonRewardNotAuthorized {
		t.Fatalf("want reward refusal, got kind=%q reason=%q", plan.Kind, plan.Reason)
	}
	// RewardPlay=false(无奖励场景派生)同一路线可走——奖励闸只随服务端派生事实。
	plan = JudgeActionPlan(actionQuery(channeladmission.EnvIOSSafari, false), rec, ActionPlanInput{})
	if !plan.Allowed {
		t.Fatalf("non-reward play must pass the gate: %+v", plan)
	}
}

// 7. evidence_level 镜像:receipt=supported → official_receipt。
func TestActionPlanEvidenceLevelMirrorsReceipt(t *testing.T) {
	rec := seedAction(t, func(r *channeladmission.ActionCapability) {
		for env := range r.Environments {
			r.Environments[env] = envCaps(channeladmission.CapSupported, channeladmission.CapUnsupported, channeladmission.CapSupported, channeladmission.CapUnsupported)
		}
	})
	plan := JudgeActionPlan(actionQuery(channeladmission.EnvIOSSafari, false), rec, ActionPlanInput{
		ShareURL: "https://store.example.com/page",
	})
	if plan.EvidenceLevel != EvidenceLevelOfficialReceipt {
		t.Fatalf("receipt=supported must map to official_receipt, got %q", plan.EvidenceLevel)
	}
}

// 8. 未知环境 fail-closed → pending_external(JudgeActionRoute 语义穿透)。
func TestActionPlanUnknownEnvironmentPendingExternal(t *testing.T) {
	rec := seedAction(t, nil)
	plan := JudgeActionPlan(actionQuery("not_a_real_env", false), rec, ActionPlanInput{})
	if plan.Kind != PlanPendingExternal {
		t.Fatalf("unknown environment must fail closed to pending_external, got %q", plan.Kind)
	}
}
