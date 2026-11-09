// plan2.go: HUI-1670 touch-handoff/v2 动作级计划面 —— 3535 合同 §6 的消费方
// 接线(纯函数,零 SQL、零网络、零平台外呼)。
//
// 与 v1 PlanRoute(channeladmission.Record 五维)并存:v2 基于 3535 动作级
// 四分能力(ActionCapability × JudgeActionRoute),产出
//
//	plan.kind = refused|pending_external|manual_only|official_handoff|oauth_publish
//
// 加结构化 client_action(类型化描述,绝不返回可执行 JS)、unsupported_fields
// (逐项解释不可自动携带的字段)与 evidence_level(结果可证实档,镜像 3535
// platform_receipt 单元)。
//
// 纪律(裁定书):
//   - Gate 上下文(ActionQuery.RewardPlay/MerchantAppRef)由服务端派生冻结,
//     客户端 reward=false 永远进不了闸;
//   - unknown → pending_external(不承诺、不显示可完成态、无手工指引),
//     绝不降级为 manual 许可(3535 合同 §6 指令②);
//   - 自动化通道(h5/miniapp/js/oauth)未接 adapter 时诚实回退为合规导出
//     (玩法已获准,手动路径合法;绝不编造深链/OAuth 入口);
//   - link_share/direct_open 的 open_url 只指向服务端已验证的发布目标
//     (share_url 由 httpapi 从 4584 已发布动作 TargetRef 解析),不伪建。
package publishhandoff

import (
	"fmt"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
)

// v2 plan kinds (frozen contract for HUI-4585..4588; see handoff-contract.md).
const (
	PlanOfficialHandoff = "official_handoff" // 官方承接:导航打开/媒体交接/纯链接
	PlanOAuthPublish    = "oauth_publish"    // OAuth+服务端投稿(4587 快手)
)

// Evidence levels (frozen): the plan's honest statement of achievable result
// provability — mirrors the 3535 platform_receipt cell, never above it.
const (
	EvidenceLevelOfficialReceipt = "official_receipt" // 平台官方回执可证实
	EvidenceLevelNone            = "none"             // 无可证实回执(结果只到人工/自报层)
	EvidenceLevelUnknown         = "unknown"          // 未确认(不承诺)
)

// ClientAction types (typed descriptors — never executable JS).
const (
	ClientActionOpenURL        = "open_url"         // 打开服务端给出的合法 https 目标
	ClientActionOpenMiniapp    = "open_miniapp"     // 打开小程序路由(薄适配)
	ClientActionJSMediaHandoff = "js_media_handoff" // 平台官方 JS SDK 媒体交接
	ClientActionOAuthConsent   = "oauth_consent"    // OAuth 授权确认(4587)
	ClientActionManualExport   = "manual_export"    // 合规导出+手动发布
)

// ClientAction is the structured instruction the customer page renders. It is
// a typed descriptor: url/miniapp_path/params plus export text — never a
// script payload (裁定书: 不返回任意可执行 JS)。
type ClientAction struct {
	Type        string            `json:"type"`
	URL         string            `json:"url,omitempty"`
	MiniappPath string            `json:"miniapp_path,omitempty"`
	Params      map[string]string `json:"params,omitempty"`
	Caption     string            `json:"caption,omitempty"` // 手动导出文案(标题+正文+话题)
	Steps       []string          `json:"steps,omitempty"`   // 手动步骤
	MediaNote   string            `json:"media_note,omitempty"`
	Note        string            `json:"note,omitempty"` // 诚实说明(回退/未接入)
}

// ActionPlanInput carries what the plan needs beyond the admission record:
// resolved snapshot text/media with handler-minted controlled refs, and the
// server-built share target. All optional — preview works without content.
type ActionPlanInput struct {
	Text      TextInput
	Media     []MediaItem
	MediaRefs map[string]string // media id -> controlled URL (httpapi 铸造)
	ShareURL  string            // link_share/direct_open 的服务端已验证目标
}

// ActionPlan is the v2 per-route handoff plan.
type ActionPlan struct {
	Kind              string
	Allowed           bool
	PendingExternal   bool
	Reason            string
	ReasonText        string
	Missing           []string
	Notice            string
	EvidenceLevel     string
	ClientAction      *ClientAction
	UnsupportedFields []string
	Caps              channeladmission.EnvCapabilities
}

// JudgeActionPlan evaluates one action route through the 3535 action-level
// gate and shapes the resulting v2 plan. The ActionQuery is SERVER-ASSEMBLED
// (RewardPlay from the record's scenario, MerchantAppRef from deployment
// config): 客户端标记永不参与裁决。
func JudgeActionPlan(q channeladmission.ActionQuery, rec channeladmission.ActionCapability, in ActionPlanInput) ActionPlan {
	d := channeladmission.JudgeActionRoute(q, rec)
	plan := ActionPlan{
		Reason: d.Reason, ReasonText: d.ReasonText, Missing: d.Missing,
		Caps:          d.Caps,
		EvidenceLevel: evidenceLevelFor(d.Caps.PlatformReceipt),
	}
	if !d.Allowed {
		if d.PendingExternal {
			plan.Kind = PlanPendingExternal
			plan.PendingExternal = true
			plan.Notice = "该平台的承接资格未确认（等待平台批复），暂不能发起交接，也不提供替代的手动发布入口。"
			return plan
		}
		if d.Reason == channeladmission.ReasonNotAdmitted {
			plan.Kind = PlanRefused
			plan.Notice = "官方规范明确排除该场景，本动作不提供此平台的发布交接，也没有可替代的手动发布入口。"
			return plan
		}
		plan.Kind = PlanRefused
		plan.Notice = "该渠道当前不可用，已停止新交接；既有交接保留状态与处理入口。"
		return plan
	}

	plan.Allowed = true
	unsupported := unsupportedFieldNotes(rec)
	switch rec.Method {
	case channeladmission.ActionMethodOAuthPush:
		plan.Kind = PlanOAuthPublish
		plan.ClientAction = oauthAction(in)
	case channeladmission.ActionMethodManualMaterial:
		plan.Kind = PlanManualOnly
		plan.ClientAction = manualExportAction(in)
	case channeladmission.ActionMethodLinkShare, channeladmission.ActionMethodDirectOpen:
		plan.Kind = PlanOfficialHandoff
		plan.ClientAction = linkAction(in)
	case channeladmission.ActionMethodH5Share, channeladmission.ActionMethodMiniapp,
		channeladmission.ActionMethodJSMedia, channeladmission.ActionMethodNativeSDK:
		// 自动化承接形态。base 面(未接 adapter)诚实回退为合规导出——玩法
		// 已获准所以手动路径合法;adapter(4585-4588)接入后由 Prepare 升级为
		// 官方交接动作,绝不在此编造深链/SDK 入口。
		plan.Kind = PlanOfficialHandoff
		plan.ClientAction = manualExportAction(in)
		plan.ClientAction.Note = "该平台的自动交接通道未接入，暂提供合规导出与手动发布；接入官方适配器后此路线升级为官方交接。"
	default:
		// 未登记的承接方法 fail-closed(Validate 已挡;防御性保底)。
		plan.Kind = PlanPendingExternal
		plan.PendingExternal = true
		plan.Notice = "该路线的承接方式未确认，暂不能发起交接。"
		return plan
	}
	plan.UnsupportedFields = unsupported
	return plan
}

// evidenceLevelFor mirrors the platform_receipt cell (never derived from any
// other cell — 独立判定纪律).
func evidenceLevelFor(receipt string) string {
	switch receipt {
	case channeladmission.CapSupported:
		return EvidenceLevelOfficialReceipt
	case channeladmission.CapUnsupported:
		return EvidenceLevelNone
	default:
		return EvidenceLevelUnknown
	}
}

// unsupportedFieldNotes explains every non-supported payload field (交接前逐
// 项解释,不承诺自动预填).
func unsupportedFieldNotes(rec channeladmission.ActionCapability) []string {
	advices := map[string]struct{ label, unsupported, unknown string }{
		"media": {"视频/图片素材", "该平台此入口不支持自动带素材，请在平台内选择本地文件。", ""},
		"title": {"标题", "该平台此入口不支持自动带标题，请在平台内手动填写。", "标题：官方能力未确认，请在平台内手动填写，不承诺自动预填。"},
		"body":  {"正文", "该平台此入口不支持自动带正文，请在平台内粘贴导出的文案。", "正文：官方能力未确认，请在平台内手动填写，不承诺自动预填。"},
		"topic": {"话题", "该平台此入口不支持自动带话题，请在平台内手动添加。", "话题：官方能力未确认，请在平台内手动添加，不承诺自动预填。"},
		"poi":   {"门店 POI", "该平台此入口不支持自动带 POI，请在平台内手动关联门店。", "门店 POI：官方能力未确认，请在平台内手动关联，不承诺自动预填。"},
	}
	order := []string{"media", "title", "body", "topic", "poi"}
	out := make([]string, 0, len(order))
	for _, k := range order {
		adv, ok := advices[k]
		if !ok {
			continue
		}
		v, recorded := rec.FieldSupport[k]
		if !recorded {
			continue // 未登记的字段不进解释面(记录说了才算)
		}
		switch v {
		case channeladmission.CapUnsupported:
			out = append(out, adv.label+"："+adv.unsupported)
		case channeladmission.CapUnknown:
			out = append(out, adv.unknown)
		}
	}
	return out
}

// linkAction opens the server-verified target (share_url from the published
// action's TargetRef; direct_open identical shape). Empty target = honest
// nil (misconfiguration must not fake an entry).
func linkAction(in ActionPlanInput) *ClientAction {
	if strings.TrimSpace(in.ShareURL) == "" {
		return nil
	}
	return &ClientAction{
		Type: ClientActionOpenURL, URL: in.ShareURL,
		Params: map[string]string{},
		Note:   "打开下方链接完成该动作；系统只能确认你已打开，不能替你在平台内完成操作。",
	}
}

// oauthAction is the OAuth consent descriptor. Without the platform adapter
// (4587) the OAuth entry does not exist: honest manual-export fallback with
// an explicit note (绝不编造授权入口)。
func oauthAction(in ActionPlanInput) *ClientAction {
	act := manualExportAction(in)
	act.Type = ClientActionManualExport
	act.Note = "该平台的自动投稿通道未接入，暂提供合规导出与手动发布；接入官方适配器后此路线升级为 OAuth 授权投稿。"
	return act
}

// manualExportAction is the honest fallback: compliance export + manual steps.
func manualExportAction(in ActionPlanInput) *ClientAction {
	guide := manualGuide(PlanInput{Text: in.Text, Media: in.Media, MediaURLs: in.MediaRefs})
	return &ClientAction{
		Type:      ClientActionManualExport,
		Caption:   guide.ExportCaption,
		Steps:     guide.Steps,
		MediaNote: guide.MediaNote,
		Params:    map[string]string{},
	}
}

// confirmationFor names the exact confirmation token a customer must echo for
// one payload kind (换内容=换 token=必须重新确认):
//   - media:     the frozen snapshot fingerprint (ContentFingerprint)
//   - link_share: the server-built share_url itself
func ConfirmationFor(payloadKind string, in ActionPlanInput) (string, error) {
	switch payloadKind {
	case PayloadKindMedia:
		return ContentFingerprint(in.Text, in.Media), nil
	case PayloadKindLinkShare:
		if strings.TrimSpace(in.ShareURL) == "" {
			return "", fmt.Errorf("publishhandoff: link_share requires a server-built share_url")
		}
		return in.ShareURL, nil
	default:
		return "", fmt.Errorf("publishhandoff: payload_kind must be media|link_share")
	}
}

// Payload kinds (frozen).
const (
	PayloadKindMedia     = "media"
	PayloadKindLinkShare = "link_share"
)
