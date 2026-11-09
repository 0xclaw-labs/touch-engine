// 渠道适配:准入裁决决定计划形态,适配器不自建白名单。
//
// 计划形态(2026-11-09 v2 修订,3535 合同 §6):
//   - refused          Gate 拒绝(not_admitted/closed/expired/wrong_binding/
//     reward_play_not_authorized):不给交接入口;not_admitted 同时不提供本
//     玩法的手工发布步骤——不把手工路径做成被禁营销玩法的后门(票面红线);
//   - pending_external 资格未知(admission_unknown):等待平台批复——不承诺、
//     不显示可完成态、无手工指引(unknown 永不自动当 manual 许可);
//   - manual_only      manual 入口且玩法获准:合规导出+手动指引(PR#15 基线
//     语义),交接管道照常(恢复/取消/验签/幂等不停);
//   - automated        admitted 且 h5/miniapp 承接:按 entry_mode 构造深链或
//     小程序薄适配路径;只携带 capability=supported 的字段,unknown/unsupported
//     字段在交接前逐项解释(不承诺全部预填)。
//
// v2 动作面(POST /api/v1/public/actions/{actionId}/handoffs)的同类判定见
// plan2.go:plan.kind=refused|pending_external|manual_only|official_handoff|
// oauth_publish,基于 3535 动作级四分能力(JudgeActionRoute)。
package publishhandoff

import (
	"net/url"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
)

// Plan kinds. v2 (touch-handoff/v2, 3535 合同 §6 指令②) 增补
// PlanPendingExternal:admission_unknown 永不自动当 manual 许可——不承诺、
// 不显示可完成态、无手工指引。PlanAutomated 仅存在于旧 /c 参与面,不进 v2
// 响应(v2 五值:refused|pending_external|manual_only|official_handoff|
// oauth_publish,见 plan2.go)。
const (
	PlanAutomated       = "automated"
	PlanManualOnly      = "manual_only"
	PlanRefused         = "refused"
	PlanPendingExternal = "pending_external"
)

// Entry methods for automated plans.
const (
	EntryMethodH5      = "h5_deeplink"
	EntryMethodMiniapp = "miniapp_route"
)

// Media item kinds.
const (
	MediaVideo = "video"
	MediaCover = "cover"
	MediaImage = "image"
)

// MediaItem is one controlled media reference of the adopted version. The
// caller (httpapi) resolves it from the contentpack snapshot after the
// distributable check; this package never touches file bytes.
type MediaItem struct {
	Kind        string // video | cover | image
	AssetID     string
	SHA256      string
	ContentType string
	Filename    string
}

// TextInput is the copy snapshot carried per capability.
type TextInput struct {
	Title     string
	Body      string
	Topics    []string
	StoreName string // 门店 POI 文案(POI 能力未确认时不进深链)
}

// PlanInput is everything the adapter needs, already resolved by the caller.
type PlanInput struct {
	TenantID  string
	Platform  string
	EntryMode string
	Scenario  string
	Now       time.Time

	Text  TextInput
	Media []MediaItem
	// MediaURLs maps asset_id -> controlled download URL minted by the
	// contentpack handoff claim (短时效受控 URL;本包不铸字节).
	MediaURLs map[string]string

	// DeepLinkBase / MiniappBase are the configured entry bases for admitted
	// routes. Empty bases with an admitted h5/miniapp route answer a refused
	// plan (misconfiguration must not fake an entry).
	DeepLinkBase string
	MiniappBase  string

	// RewardPlay / MerchantAppRef are SERVER-DERIVED facts (3535 合同 §6 指令①):
	// 奖励性质按记录 scenario 派生、商户 App 引用来自部署配置,客户端标记
	// (reward=false 等)永不进入闸门。调用方不填时闸按缺省事实裁决
	// (RewardPlay=false / MerchantAppRef=""→merchant_authorized 拒绝)。
	RewardPlay     bool
	MerchantAppRef string
}

// EntryPayload is the automated handoff entry. URL 仅供 h5 深链;MiniappPath
// 是薄适配的路由描述(仍消费 Touch 的活动/参与/奖励权威,不复制经营系统)。
type EntryPayload struct {
	Method      string
	URL         string // h5 deep link (仅 supported 字段入参)
	MiniappPath string
	Params      map[string]string
	// Unsupported 逐项解释未携带的字段(票面:不支持的字段在交接前解释)。
	Unsupported []string
}

// ManualGuide is the honest fallback: compliance export + manual steps.
type ManualGuide struct {
	Steps         []string
	ExportCaption string // 标题+正文+话题,供顾客复制
	MediaNote     string // 受控媒体 URL 说明(含有效期限语义)
}

// Plan is the per-route handoff plan.
type Plan struct {
	Kind         string
	Allowed      bool // 只有 automated 为 true
	Reason       string
	ReasonText   string
	Missing      []string
	Notice       string // 顾客可读的诚实说明
	Entry        *EntryPayload
	Manual       *ManualGuide
	Capabilities channeladmission.Capabilities
}

// PlanRoute evaluates the admission record through the 3535 Gate (服务端
// 单点强制;前端隐藏按钮不算数) and shapes the resulting plan. 未登记路线
// 按 unknown 规则诚实拒绝(由 httpapi 层合成 unknown Record)。
func PlanRoute(in PlanInput, rec channeladmission.Record) Plan {
	dec := channeladmission.Gate(channeladmission.GateInput{
		Now: in.Now, TenantID: in.TenantID,
		RewardPlay:     in.RewardPlay,
		MerchantAppRef: in.MerchantAppRef,
	}, rec)
	plan := Plan{
		Reason: dec.Reason, ReasonText: dec.ReasonText, Missing: dec.Missing,
		Capabilities: rec.Capabilities,
	}
	if !dec.Allowed {
		if dec.Reason == channeladmission.ReasonNotAdmitted {
			// 官方规范明确排除该场景:拒绝交接且不给手工后门。
			plan.Kind = PlanRefused
			plan.Notice = "官方规范明确排除该场景，本活动不提供此平台的发布交接，也没有可替代的手动发布入口。"
			return plan
		}
		// 3535 合同 §6 指令②(2026-11-09):admission_unknown → pending_external
		// (旧 manual_only 降级映射废除)。资格未知=等待平台批复:顾客侧不承诺、
		// 不显示可完成态、无手工指引;渠道关闭/过期/错绑定仍 refused。
		if dec.Reason == channeladmission.ReasonAdmissionUnknown {
			plan.Kind = PlanPendingExternal
			plan.Notice = "该平台的承接资格未确认（等待平台批复），暂不能发起交接，也不提供替代的手动发布入口。"
			return plan
		}
		plan.Kind = PlanRefused
		plan.Notice = "该渠道当前不可用，已停止新交接；既有参与保留状态与处理入口。"
		return plan
	}

	// admitted:按 entry_mode 适配。
	switch rec.EntryMode {
	case channeladmission.EntryH5:
		if strings.TrimSpace(in.DeepLinkBase) == "" {
			plan.Kind = PlanRefused
			plan.Reason = "entry_not_configured"
			plan.Notice = "H5 承接入口未配置，暂不能发起自动交接。"
			return plan
		}
		plan.Kind = PlanAutomated
		plan.Allowed = true
		plan.Entry = h5Entry(in, rec.Capabilities)
		return plan
	case channeladmission.EntryMiniapp:
		if strings.TrimSpace(in.MiniappBase) == "" {
			plan.Kind = PlanRefused
			plan.Reason = "entry_not_configured"
			plan.Notice = "小程序承接入口未配置，暂不能发起自动交接。"
			return plan
		}
		plan.Kind = PlanAutomated
		plan.Allowed = true
		plan.Entry = miniappEntry(in, rec.Capabilities)
		return plan
	default:
		// manual / native:平台承接形态本身就是人工操作——即使玩法获准,
		// 承接方式也是导出+指引,不伪造自动入口。
		plan.Kind = PlanManualOnly
		plan.Notice = "该路线的官方承接方式为手动发布；可使用合规导出+手动发布指引，进度与核验照常记录。"
		plan.Manual = manualGuide(in)
		return plan
	}
}

// capabilityParam carries one field only when its cell is supported.
func capabilityParam(caps channeladmission.Capabilities, name, value string, into map[string]string, unsupported *[]string, label, advice string) {
	switch caps.Field(name) {
	case channeladmission.CapSupported:
		if value != "" {
			into[name] = value
		}
	case channeladmission.CapUnsupported:
		*unsupported = append(*unsupported, label+"："+advice)
	default:
		*unsupported = append(*unsupported, label+"：官方能力未确认，请在平台内手动填写，不承诺自动预填。")
	}
}

func h5Entry(in PlanInput, caps channeladmission.Capabilities) *EntryPayload {
	params := map[string]string{}
	var unsupported []string
	capabilityParam(caps, "media", firstURL(in, MediaVideo), params, &unsupported, "视频", "该平台此入口不支持自动带视频，请在平台内选择本地文件。")
	capabilityParam(caps, "title", in.Text.Title, params, &unsupported, "标题", "该平台此入口不支持自动带标题，请在平台内手动填写。")
	capabilityParam(caps, "body", in.Text.Body, params, &unsupported, "正文", "该平台此入口不支持自动带正文，请在平台内粘贴导出的文案。")
	capabilityParam(caps, "topic", strings.Join(in.Text.Topics, ","), params, &unsupported, "话题", "该平台此入口不支持自动带话题，请在平台内手动添加。")
	capabilityParam(caps, "poi", in.Text.StoreName, params, &unsupported, "门店 POI", "该平台此入口不支持自动带 POI，请在平台内手动关联门店。")

	q := url.Values{}
	for k, v := range params {
		q.Set(k, v)
	}
	return &EntryPayload{
		Method: EntryMethodH5, URL: in.DeepLinkBase + "?" + q.Encode(), Params: params,
		Unsupported: unsupported,
	}
}

func miniappEntry(in PlanInput, caps channeladmission.Capabilities) *EntryPayload {
	params := map[string]string{}
	var unsupported []string
	capabilityParam(caps, "media", firstURL(in, MediaVideo), params, &unsupported, "视频", "该小程序此入口不支持自动带视频，请在平台内选择本地文件。")
	capabilityParam(caps, "title", in.Text.Title, params, &unsupported, "标题", "该小程序此入口不支持自动带标题，请在平台内手动填写。")
	capabilityParam(caps, "body", in.Text.Body, params, &unsupported, "正文", "该小程序此入口不支持自动带正文，请在平台内粘贴导出的文案。")
	capabilityParam(caps, "topic", strings.Join(in.Text.Topics, ","), params, &unsupported, "话题", "该小程序此入口不支持自动带话题，请在平台内手动添加。")
	capabilityParam(caps, "poi", in.Text.StoreName, params, &unsupported, "门店 POI", "该小程序此入口不支持自动带 POI，请在平台内手动关联门店。")
	return &EntryPayload{
		Method: EntryMethodMiniapp, MiniappPath: in.MiniappBase, Params: params,
		Unsupported: unsupported,
	}
}

func manualGuide(in PlanInput) *ManualGuide {
	name := "对应平台"
	switch in.Platform {
	case "douyin":
		name = "抖音"
	case "kuaishou":
		name = "快手"
	case "xiaohongshu":
		name = "小红书"
	case "channels":
		name = "微信视频号"
	case "bilibili":
		name = "B站"
	}
	caption := in.Text.Title
	if caption != "" {
		caption += "\n"
	}
	caption += in.Text.Body
	if len(in.Text.Topics) > 0 {
		caption += "\n" + strings.Join(prefixAll(in.Text.Topics, "#"), " ")
	}
	media := firstURL(in, MediaVideo)
	note := "素材通过短时效受控链接提供，请及时保存到本地；链接过期可在交接页重试获取，不会新建交接。"
	if media != "" {
		note = "视频/图片素材：" + media + "（" + note + "）"
	}
	return &ManualGuide{
		Steps: []string{
			"打开" + name + "，使用你自己的账号（商家账号不能代替你发布）",
			"把导出的文案粘贴到新建作品的正文",
			"保存下方受控链接里的素材到手机后，在平台内选择该文件",
			"在官方 App 内自行完成发布；本系统看不到发布结果，也不把点击当作发布成功",
		},
		ExportCaption: caption,
		MediaNote:     note,
	}
}

func prefixAll(in []string, p string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, p+s)
	}
	return out
}

func firstURL(in PlanInput, kind string) string {
	for _, m := range in.Media {
		if m.Kind == kind {
			return in.MediaURLs[m.AssetID]
		}
	}
	return ""
}
