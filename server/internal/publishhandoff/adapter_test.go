package publishhandoff

import (
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
)

// T3: 渠道适配——准入裁决决定计划形态;不另写平台白名单。
//
// 票面: 按 3535 允许的具体玩法选择承接方式;未知/未准入诚实降级为
// 合规导出+手动指引,不在顾客页展示不可用路径;不支持的字段在交接前
// 解释;不把手工路径作为被禁营销玩法的后门。

func seedRecord(t *testing.T, mutate func(*channeladmission.Record)) channeladmission.Record {
	t.Helper()
	r := channeladmission.Record{
		Platform: "xiaohongshu", EntryMode: channeladmission.EntryMiniapp, Scenario: "store_promo",
		AppBinding: "platform_app", TenantScope: "*",
		Status: channeladmission.StatusUnknown, ReasonCode: channeladmission.ReasonAdmissionUnknown,
		ReasonText: "外部拉起、小程序发布、回调权限及奖励规则未确认",
		Missing:    []string{"外部拉起权限确认", "发布回调权限确认"},
		Capabilities: channeladmission.Capabilities{
			Media: channeladmission.CapUnknown, Title: channeladmission.CapUnknown,
			Body: channeladmission.CapUnknown, Topic: channeladmission.CapUnknown,
			POI: channeladmission.CapUnknown, Receipt: channeladmission.CapUnknown,
			Callback: channeladmission.CapUnknown,
		},
		OfficialSources: []string{"https://miniapp.xiaohongshu.com/doc/DC530301"},
		LastCheckedAt:   "2026-10-14", ValidUntil: "2027-01-12",
	}
	if mutate != nil {
		mutate(&r)
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("seed record: %v", err)
	}
	return r
}

var planNow = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

func planInput(rec channeladmission.Record) PlanInput {
	return PlanInput{
		TenantID: "tnt_seed", Platform: rec.Platform, EntryMode: rec.EntryMode, Scenario: rec.Scenario,
		Now: planNow,
		Text: TextInput{Title: "周末烘焙节", Body: "本周六全场八折", Topics: []string{"烘焙", "探店"}, StoreName: "南岸旗舰店"},
		Media: []MediaItem{
			{Kind: MediaVideo, AssetID: "as_v", SHA256: strings.Repeat("a", 64), ContentType: "video/mp4", Filename: "main.mp4"},
			{Kind: MediaCover, AssetID: "as_c", SHA256: strings.Repeat("b", 64), ContentType: "image/jpeg", Filename: "cover.jpg"},
		},
		MediaURLs: map[string]string{"as_v": "https://media.example.com/t1", "as_c": "https://media.example.com/t2"},
	}
}

// 3.1 抖音普通 H5 not_admitted → refused:拒绝交接,给官方原因;不提供
// 本玩法的手工发布步骤(不做成被禁营销玩法的后门)。
func TestPlanDouyinH5NotAdmittedRefused(t *testing.T) {
	rec := seedRecord(t, func(r *channeladmission.Record) {
		r.Platform, r.EntryMode = "douyin", channeladmission.EntryH5
		r.Status = channeladmission.StatusNotAdmitted
		r.ReasonCode = channeladmission.ReasonNotAdmitted
		r.ReasonText = "抖音发布能力使用规范明确排除商铺宣传、商品介绍等营销内容场景"
		r.Capabilities.POI = channeladmission.CapUnknown
	})
	plan := PlanRoute(planInput(rec), rec)
	if plan.Kind != PlanRefused || plan.Allowed {
		t.Fatalf("want refused plan, got %q allowed=%v", plan.Kind, plan.Allowed)
	}
	if plan.Reason != channeladmission.ReasonNotAdmitted {
		t.Fatalf("reason = %q", plan.Reason)
	}
	if plan.Entry != nil || plan.Manual != nil {
		t.Fatal("refused plan must carry neither an automated entry nor a manual publish backdoor")
	}
	if !strings.Contains(plan.Notice, "明确排除") {
		t.Fatalf("notice must state the official exclusion, got %q", plan.Notice)
	}
}

// 3.2 unknown 路线 → manual_only:合规导出+手动指引+缺项原因;没有自动化入口。
func TestPlanUnknownRouteManualOnly(t *testing.T) {
	rec := seedRecord(t, nil)
	plan := PlanRoute(planInput(rec), rec)
	if plan.Kind != PlanManualOnly || plan.Allowed {
		t.Fatalf("want manual_only, got %q allowed=%v", plan.Kind, plan.Allowed)
	}
	if plan.Entry != nil {
		t.Fatal("unknown route must not produce an automated entry")
	}
	if plan.Manual == nil || len(plan.Manual.Steps) == 0 {
		t.Fatal("manual_only must carry compliance export steps")
	}
	if len(plan.Missing) == 0 {
		t.Fatal("manual_only must carry the missing-item list from the admission record")
	}
	if !strings.Contains(plan.Notice, "未确认") {
		t.Fatalf("notice must explain the unknown honestly, got %q", plan.Notice)
	}
	// 导出包带文案与媒体引用(受控 URL),不带平台 post id。
	if plan.Manual.ExportCaption == "" {
		t.Fatal("manual export must carry the adopted caption")
	}
	if !strings.Contains(plan.Manual.MediaNote, "https://media.example.com/t1") {
		t.Fatal("manual export must reference the controlled media URL")
	}
}

// 3.3 admitted H5 路线(fixture 注入)→ automated:深链只携带 supported
// 能力字段;unknown 字段在交接前解释。
func TestPlanAdmittedH5Automated(t *testing.T) {
	rec := seedRecord(t, func(r *channeladmission.Record) {
		r.Platform, r.EntryMode = "douyin", channeladmission.EntryH5
		r.Scenario = "store_promo"
		r.Status = channeladmission.StatusAdmitted
		r.ReasonCode = ""
		r.Capabilities = channeladmission.Capabilities{
			Media: channeladmission.CapSupported, Title: channeladmission.CapSupported,
			Body: channeladmission.CapSupported, Topic: channeladmission.CapUnknown,
			POI: channeladmission.CapUnsupported, Receipt: channeladmission.CapUnknown,
			Callback: channeladmission.CapUnknown,
		}
	})
	in := planInput(rec)
	in.DeepLinkBase = "https://partner.example.com/publish"
	plan := PlanRoute(in, rec)
	if plan.Kind != PlanAutomated || !plan.Allowed {
		t.Fatalf("want automated, got %q allowed=%v (%s)", plan.Kind, plan.Allowed, plan.Reason)
	}
	if plan.Entry == nil || plan.Entry.URL == "" {
		t.Fatal("automated h5 plan must build a deep link")
	}
	if !strings.Contains(plan.Entry.URL, "title=") || !strings.Contains(plan.Entry.URL, "body=") {
		t.Fatal("supported title/body must ride the deep link")
	}
	if !strings.Contains(plan.Entry.URL, "media=https%3A%2F%2Fmedia.example.com%2Ft1") {
		t.Fatal("supported media must ride the deep link as the controlled URL")
	}
	if strings.Contains(plan.Entry.URL, "topic=") {
		t.Fatal("unknown topic capability must not be carried as if supported")
	}
	if strings.Contains(plan.Entry.URL, "poi=") {
		t.Fatal("unsupported poi capability must not be carried")
	}
	foundTopic, foundPOI := false, false
	for _, u := range plan.Entry.Unsupported {
		if strings.Contains(u, "话题") {
			foundTopic = true
		}
		if strings.Contains(u, "POI") || strings.Contains(u, "门店") {
			foundPOI = true
		}
	}
	if !foundTopic || !foundPOI {
		t.Fatalf("unsupported fields must be explained before handoff, got %v", plan.Entry.Unsupported)
	}
}

// 3.4 admitted miniapp 路线(fixture)→ automated:薄适配路径描述,不复制经营系统。
func TestPlanAdmittedMiniappAutomated(t *testing.T) {
	rec := seedRecord(t, func(r *channeladmission.Record) {
		r.Status = channeladmission.StatusAdmitted
		r.ReasonCode = ""
	})
	in := planInput(rec)
	in.MiniappBase = "pages/publish/index"
	plan := PlanRoute(in, rec)
	if plan.Kind != PlanAutomated || plan.Entry == nil {
		t.Fatalf("want automated miniapp entry, got %q", plan.Kind)
	}
	if plan.Entry.Method != EntryMethodMiniapp || plan.Entry.MiniappPath != "pages/publish/index" {
		t.Fatalf("miniapp entry method/path = %q/%q", plan.Entry.Method, plan.Entry.MiniappPath)
	}
	if plan.Entry.URL != "" {
		t.Fatal("miniapp entry must not fabricate an h5 deep link")
	}
}

// 3.5 admitted manual 入口 → manual 指引(承认平台承接形态就是手动)。
func TestPlanAdmittedManualEntry(t *testing.T) {
	rec := seedRecord(t, func(r *channeladmission.Record) {
		r.Status = channeladmission.StatusAdmitted
		r.ReasonCode = ""
		r.EntryMode = channeladmission.EntryManual
	})
	plan := PlanRoute(planInput(rec), rec)
	if plan.Kind != PlanManualOnly || plan.Manual == nil {
		t.Fatalf("want manual_only for manual entry, got %q", plan.Kind)
	}
}

// 3.6 准入过期 → refused(与 not_admitted 同为拒绝,原因 admission_expired)。
func TestPlanExpiredAdmissionRefused(t *testing.T) {
	rec := seedRecord(t, func(r *channeladmission.Record) {
		r.Status = channeladmission.StatusAdmitted // 曾获准,核查窗已过
		r.ReasonCode = ""
		r.ValidUntil = "2026-10-01" // 早于 planNow
	})
	plan := PlanRoute(planInput(rec), rec)
	if plan.Kind != PlanRefused || plan.Reason != channeladmission.ReasonAdmissionExpired {
		t.Fatalf("want refused/expired, got %q/%q", plan.Kind, plan.Reason)
	}
}

// 3.7 平台关闭(channel_closed)→ refused:新交接停止。
func TestPlanClosedChannelRefused(t *testing.T) {
	rec := seedRecord(t, func(r *channeladmission.Record) {
		r.CloseReason = "平台合作终止"
	})
	plan := PlanRoute(planInput(rec), rec)
	if plan.Kind != PlanRefused || plan.Reason != channeladmission.ReasonChannelClosed {
		t.Fatalf("want refused/closed, got %q/%q", plan.Kind, plan.Reason)
	}
}
