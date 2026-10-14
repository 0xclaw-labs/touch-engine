// 渠道适配:准入裁决决定计划形态,适配器不自建白名单。
//
// 三分计划(设计裁决 Q3):
//   - refused     Gate 拒绝(not_admitted/closed/expired/wrong_binding…):
//     不给交接入口;not_admitted 同时不提供本玩法的手工发布
//     步骤——不把手工路径做成被禁营销玩法的后门(票面红线);
//   - manual_only unknown 或 manual 入口:诚实降级为合规导出+手动指引
//     (PR#15 基线语义),交接管道照常(恢复/取消/验签/幂等不停);
//   - automated   admitted:按 entry_mode 构造 H5 深链或小程序薄适配路径;
//     只携带 capability=supported 的字段,unknown/unsupported
//     字段在交接前逐项解释(不承诺全部预填)。
package publishhandoff

import (
	"net/url"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
)

// Plan kinds.
const (
	PlanAutomated  = "automated"
	PlanManualOnly = "manual_only"
	PlanRefused    = "refused"
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
		// closed/expired/wrong_binding/unknown…:拒绝自动交接;unknown 降级
		// 手动指引,其余(渠道关闭/过期/错绑定)连手动指引也不展示为可执行。
		if dec.Reason == channeladmission.ReasonAdmissionUnknown {
			plan.Kind = PlanManualOnly
			plan.Notice = "该平台承接能力未确认，暂不能自动带入发布；可使用合规导出+手动发布指引，进度与核验照常记录。"
			plan.Manual = manualGuide(in)
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
