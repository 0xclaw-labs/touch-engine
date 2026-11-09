// actioncapability.go: HUI-3535 (2026-11-09 增量) 动作级渠道能力真源 ——
// platform × action(catalog_code A01–A30) × method × environment × scenario ×
// app_binding 维度上的版本化准入记录与四分能力判定。
//
// 纪律(裁定书拍板):
//   - 四分能力独立判定,绝不互相推断:navigation(能否打开目标)/media_handoff
//     (能否带素材)/platform_receipt(有无官方回执)/reward_eligibility(该场景
//     奖励是否允许)各自独立存储、原样返回;「能打开」不推出「能带素材发布」,
//     「有 SDK」不推出「奖励准入」——判定代码里没有任何一列由其他列派生;
//   - 六类环境(desktop_browser/ios_safari/android_browser/ios_wechat/
//     android_wechat/target_app,T07 口径)按环境分别给值;未声明的环境键在
//     写入即拒绝,读取未知环境 fail-closed 为 pending;
//   - unknown 永不自动当 manual 许可:资格未知返回 PENDING_EXTERNAL 语义
//     (pending_external),顾客侧表现=不承诺、不显示可完成态;
//   - 种子初值=迁移 0060–0063 权威落库;平台端更新走受控记录端点
//     (internal token,管理员权限),写必留审计(channel_admission_control_log);
//   - 真实平台资格当前无账号=未验证:evidence_grade 诚实标注,不伪造已获批。
package channeladmission

import (
	"fmt"
	"time"
)

// Reason codes added by the action-capability layer (gate.go carries the v1
// set; these are additive, same table semantics).
const (
	// ReasonEmergencyDisabled: 平台紧急停用 —— 独立否决,固定顺序第一位;
	// 只停对应方法/动作,无关导航与其他动作不受影响。
	ReasonEmergencyDisabled = "emergency_disabled"
	// ReasonPendingExternal: 资格未知(PENDING_EXTERNAL)——等待外部平台批复
	// 或核验;不承诺、不显示可完成态,也绝不降级为手动发布许可。
	ReasonPendingExternal = "pending_external"
)

// Environments: the fixed six-environment enum (T07 口径,裁定书). Capability
// values are given per environment; there is no "default" environment.
const (
	EnvDesktopBrowser = "desktop_browser"
	EnvIOSSafari      = "ios_safari"
	EnvAndroidBrowser = "android_browser"
	EnvIOSWechat      = "ios_wechat"
	EnvAndroidWechat  = "android_wechat"
	EnvTargetApp      = "target_app"
)

// Environments is the fixed-order environment list (T07 口径).
var Environments = [...]string{
	EnvDesktopBrowser, EnvIOSSafari, EnvAndroidBrowser,
	EnvIOSWechat, EnvAndroidWechat, EnvTargetApp,
}

// isEnvironment reports whether s is one of the six canonical environments.
func isEnvironment(s string) bool {
	for _, e := range Environments {
		if s == e {
			return true
		}
	}
	return false
}

// Handoff methods for the action catalog. The "how" of the route.
const (
	ActionMethodH5Share        = "h5_share"          // 平台 H5/SDK 分享投稿(通用路线)
	ActionMethodMiniapp        = "miniapp"           // 小程序路线(发布器/任务/服务接口)
	ActionMethodJSMedia        = "js_media_handoff"  // 官方 JS SDK 媒体交接(小红书)
	ActionMethodOAuthPush      = "oauth_server_push" // 网站应用 OAuth + 服务端投稿(快手)
	ActionMethodNativeSDK      = "native_sdk"        // 移动应用 SDK 路线
	ActionMethodLinkShare      = "link_share"        // 纯链接/系统分享(无需内容包)
	ActionMethodManualMaterial = "manual_material"   // 合规导出+顾客手动发布
	ActionMethodDirectOpen     = "direct_open"       // 直接打开目标(点评/关注/服务/团购)
)

// Marketing scenarios: the "why/what for" of the route.
const (
	ActionScenarioStorePromo = "store_promo" // 发布宣传内容
	ActionScenarioRewardTask = "reward_task" // 任务激励玩法(需平台批复)
	ActionScenarioReview     = "review"      // 点评(不可作奖励触发)
	ActionScenarioFollow     = "follow"      // 关注/社群
	ActionScenarioService    = "service"     // Wi-Fi/添加微信等门店服务
	ActionScenarioGroupBuy   = "group_buy"   // 团购(外部平台订单权威)
)

// Action kinds for the 30-action catalog.
const (
	ActionKindPublish  = "publish"
	ActionKindReview   = "review"
	ActionKindFollow   = "follow"
	ActionKindService  = "service"
	ActionKindGroupBuy = "group_buy"
)

// Evidence grades: how the record's facts were established. Honest labeling —
// an approval that does not exist is never recorded as one.
const (
	EvidenceOfficialSpec     = "official_spec"     // 平台规范明确条款(如抖音 useclue)
	EvidenceOfficialDoc      = "official_doc"      // 官方能力文档
	EvidenceOfficialSnapshot = "official_snapshot" // 官方检索快照(含暂停新接入)
	EvidenceDeviceTest       = "device_test"       // 真机验证
	EvidenceUnverified       = "unverified"        // 未核实(不编造契约)
)

// fieldSupportKeys is the whitelist for the field_support map (与 v1
// Capabilities 的字段面一致,外加 media 的正交表达).
var fieldSupportKeys = map[string]bool{
	"media": true, "title": true, "body": true, "topic": true, "poi": true,
}

// EnvCapabilities is the four-way capability split for ONE environment. All
// four cells are tri-state (supported|unsupported|unknown) and independently
// judged — nothing here is derived from anything else.
type EnvCapabilities struct {
	Navigation        string `json:"navigation"`         // 能否打开目标
	MediaHandoff      string `json:"media_handoff"`      // 能否带素材
	PlatformReceipt   string `json:"platform_receipt"`   // 有无官方回执
	RewardEligibility string `json:"reward_eligibility"` // 该场景奖励是否允许
}

// validTri reports whether s is a legal capability state.
func validTri(s string) bool {
	return s == CapSupported || s == CapUnsupported || s == CapUnknown
}

func (c EnvCapabilities) validate(where string) error {
	for name, v := range map[string]string{
		"navigation": c.Navigation, "media_handoff": c.MediaHandoff,
		"platform_receipt": c.PlatformReceipt, "reward_eligibility": c.RewardEligibility,
	} {
		if !validTri(v) {
			return fmt.Errorf("channeladmission: %s capability %s = %q must be supported|unsupported|unknown", where, name, v)
		}
	}
	return nil
}

// ActionCapability is one admission record over the extended dimension set.
// JSON tags describe the controlled-endpoint wire shape (admin write API).
type ActionCapability struct {
	ID          string `json:"id"`
	CatalogCode string `json:"catalog_code"` // A01–A30
	Platform    string `json:"platform"`
	Method      string `json:"method"`
	Scenario    string `json:"scenario"`
	ActionKind  string `json:"action_kind"`
	// Label is the catalog action name (发抖音视频 等) for honest display.
	Label string `json:"label"`
	// SuggestedOnly marks the 9 建议补充 actions (A22–A30): they must never be
	// silently upgraded into approved auto-publish commitments.
	SuggestedOnly bool   `json:"suggested_only"`
	AppBinding    string `json:"app_binding"`
	TenantScope   string `json:"tenant_scope"`
	Status        string `json:"status"`
	// Environments gives the four-way split per environment. All six keys are
	// required at write time; reads of unrecorded environments fail closed.
	Environments map[string]EnvCapabilities `json:"environments"`
	// FieldSupport: media/title/body/topic/poi tri-states (B04 field_support).
	FieldSupport map[string]string `json:"field_support"`
	// RequiredInputs lists the payload kinds this route requires (e.g.
	// content_pack_version). Empty for link_share/service routes — A27 纯链接
	// 分享合法地无需内容包/媒体授权,不得被「视频库为空」拦截.
	RequiredInputs  []string `json:"required_inputs"`
	EvidenceGrade   string   `json:"evidence_grade"`
	OfficialSources []string `json:"official_sources"`
	// CheckedAt/ValidUntil: 实际读取日期 + 复检窗 (过窗即 admission_expired).
	CheckedAt  string `json:"checked_at"`
	ValidUntil string `json:"valid_until"`
	// EmergencyDisabled: 平台紧急停用,独立否决第一位;只停对应方法/动作。
	EmergencyDisabled bool     `json:"emergency_disabled"`
	CloseReason       string   `json:"close_reason"`
	Missing           []string `json:"missing"`
	ReasonCode        string   `json:"reason_code"`
	// ReasonText is the human explanation — every refused answer carries it.
	ReasonText  string `json:"reason_text"`
	SeedVersion int    `json:"seed_version"`
}

// Env returns the four-way split for one environment. Unknown environments
// fail closed (error) so a typo can never silently borrow another
// environment's verdict.
func (r ActionCapability) Env(environment string) (EnvCapabilities, error) {
	c, ok := r.Environments[environment]
	if !ok {
		return EnvCapabilities{}, fmt.Errorf("channeladmission: %s has no %s environment record", r.IdentityKey(), environment)
	}
	return c, nil
}

// IdentityKey is the record's natural key (without tenant scope).
func (r ActionCapability) IdentityKey() string {
	return r.CatalogCode + "|" + r.Platform + "|" + r.Method + "|" + r.Scenario
}

// Validate enforces the extended enum matrices: statuses/methods/scenarios/
// kinds/evidence grades, exactly six environment keys with legal four-way
// states, the field_support whitelist, and the reward invariant (unknown /
// not_admitted routes can never carry a supported reward cell).
func (r *ActionCapability) Validate() error {
	switch r.Status {
	case StatusAdmitted, StatusNotAdmitted, StatusUnknown:
	default:
		return fmt.Errorf("channeladmission: %s status %q must be admitted|not_admitted|unknown", r.IdentityKey(), r.Status)
	}
	switch r.Method {
	case ActionMethodH5Share, ActionMethodMiniapp, ActionMethodJSMedia, ActionMethodOAuthPush,
		ActionMethodNativeSDK, ActionMethodLinkShare, ActionMethodManualMaterial, ActionMethodDirectOpen:
	default:
		return fmt.Errorf("channeladmission: %s method %q unknown", r.IdentityKey(), r.Method)
	}
	switch r.Scenario {
	case ActionScenarioStorePromo, ActionScenarioRewardTask, ActionScenarioReview,
		ActionScenarioFollow, ActionScenarioService, ActionScenarioGroupBuy:
	default:
		return fmt.Errorf("channeladmission: %s scenario %q unknown", r.IdentityKey(), r.Scenario)
	}
	switch r.ActionKind {
	case ActionKindPublish, ActionKindReview, ActionKindFollow, ActionKindService, ActionKindGroupBuy:
	default:
		return fmt.Errorf("channeladmission: %s action_kind %q unknown", r.IdentityKey(), r.ActionKind)
	}
	switch r.EvidenceGrade {
	case EvidenceOfficialSpec, EvidenceOfficialDoc, EvidenceOfficialSnapshot, EvidenceDeviceTest, EvidenceUnverified:
	default:
		return fmt.Errorf("channeladmission: %s evidence_grade %q unknown", r.IdentityKey(), r.EvidenceGrade)
	}
	switch r.AppBinding {
	case "platform_app", "merchant_authorized", "none":
	default:
		return fmt.Errorf("channeladmission: %s app_binding %q must be platform_app|merchant_authorized|none", r.IdentityKey(), r.AppBinding)
	}
	if r.TenantScope == "" {
		r.TenantScope = "*"
	}
	if len(r.Environments) != len(Environments) {
		return fmt.Errorf("channeladmission: %s environments must carry exactly %d keys, got %d", r.IdentityKey(), len(Environments), len(r.Environments))
	}
	rewardOpen := false
	for env, c := range r.Environments {
		if !isEnvironment(env) {
			return fmt.Errorf("channeladmission: %s environment key %q unknown", r.IdentityKey(), env)
		}
		if err := c.validate(r.IdentityKey() + "/" + env); err != nil {
			return err
		}
		if c.RewardEligibility == CapSupported {
			rewardOpen = true
		}
	}
	if rewardOpen && r.Status != StatusAdmitted {
		return fmt.Errorf("channeladmission: %s reward_eligibility=supported requires status=admitted (unknown never opens reward play)", r.IdentityKey())
	}
	for k, v := range r.FieldSupport {
		if !fieldSupportKeys[k] {
			return fmt.Errorf("channeladmission: %s field_support key %q unknown", r.IdentityKey(), k)
		}
		if !validTri(v) {
			return fmt.Errorf("channeladmission: %s field_support %s = %q must be supported|unsupported|unknown", r.IdentityKey(), k, v)
		}
	}
	if r.Label == "" {
		return fmt.Errorf("channeladmission: %s label required (动作目录名,诚实展示)", r.IdentityKey())
	}
	if r.CatalogCode == "" || r.Platform == "" {
		return fmt.Errorf("channeladmission: catalog_code and platform are required")
	}
	return nil
}

// ActionQuery is the fact set for judging one action route. RewardPlay and
// MerchantAppRef are SERVER-DERIVED facts (营销场景性质与真实 App 绑定由服务端
// 冻结派生) — a client-supplied reward=false never bypasses the reward check.
type ActionQuery struct {
	Now            time.Time
	TenantID       string
	Environment    string
	RewardPlay     bool
	MerchantAppRef string
}

// RouteDecision is the deterministic verdict for one action route in one
// environment. PendingExternal marks the PENDING_EXTERNAL semantics (资格未知:
// 顾客侧不承诺、不显示可完成态);Promised marks the only states a customer may
// see as completable.
type RouteDecision struct {
	Allowed         bool
	Reason          string
	ReasonText      string
	Missing         []string
	PendingExternal bool
	Promised        bool
	Caps            EnvCapabilities
}

// JudgeActionRoute is the action-level server-side enforcement point (与 v1
// Gate 同为纯函数,HTTP 层零业务判断). Fixed refusal order:
//
//	emergency_disabled -> channel_closed -> not_admitted -> pending_external
//	-> admission_expired -> wrong_binding -> reward_play_not_authorized
func JudgeActionRoute(q ActionQuery, r ActionCapability) RouteDecision {
	dec := RouteDecision{ReasonText: r.ReasonText, Missing: append([]string(nil), r.Missing...)}
	caps, err := r.Env(q.Environment)
	dec.Caps = caps
	if err != nil {
		// 未知/未声明的环境 fail-closed 为资格未知,绝不借用其他环境的值。
		dec.Reason = ReasonPendingExternal
		dec.PendingExternal = true
		return dec
	}
	if r.EmergencyDisabled {
		dec.Reason = ReasonEmergencyDisabled
		return dec
	}
	if r.CloseReason != "" {
		dec.Reason = ReasonChannelClosed
		return dec
	}
	switch r.Status {
	case StatusNotAdmitted:
		dec.Reason = ReasonNotAdmitted
		return dec
	case StatusUnknown:
		dec.Reason = ReasonPendingExternal
		dec.PendingExternal = true
		return dec
	}
	fresh, known := admissionFresh(q.Now, r.ValidUntil)
	if !known {
		// 无有效期 = 未确立核查窗:与 v1 同纪律按未知规则 fail-closed。
		dec.Reason = ReasonPendingExternal
		dec.PendingExternal = true
		return dec
	}
	if !fresh {
		dec.Reason = ReasonAdmissionExpired
		return dec
	}
	if !scopeBindingMatch(r.TenantScope, r.AppBinding, q.TenantID, q.MerchantAppRef) {
		dec.Reason = ReasonWrongBinding
		return dec
	}
	if q.RewardPlay && caps.RewardEligibility != CapSupported {
		dec.Reason = ReasonRewardNotAuthorized
		return dec
	}
	dec.Allowed = true
	// 顾客侧可完成态只有两种诚实来源:系统打开目标 supported,或
	// manual_material 的顾客自足路径(合规导出+自主发布)。其余一律不承诺。
	if caps.Navigation == CapSupported || r.Method == ActionMethodManualMaterial {
		dec.Promised = true
	}
	return dec
}
