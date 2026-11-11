// manual.go: HUI-4588 手动交接家族适配器——A04 朋友圈视频 / A05 朋友圈图文 /
// A06 视频号(必需) + A22 B站 / A24 微博图文 / A25 微博视频 / A27 QQ 好友群 /
// A28 QQ空间(建议补充,标注保留)。
//
// 交付的是实际可用的预览、素材保存/系统分享、用户主动复制及准确官方入口/
// 步骤;不能只显示平台图标,也不能把普通 H5 未核实的原生发布器写成已支持。
//
// 票面红线:
//   - Submit/Query/VerifyCallback 对无官方发布契约的方法明确 unsupported;
//     系统分享成功只记录 handoff_only(弱事实),用户"已完成"只存 customer_
//     self_report 声明,永不制造 post_id/platform_confirmed/自动奖励;
//   - 无已核实契约的(朋友圈普通 H5 视频直发)绝不编造 scheme/成功回执——
//     闸在 3535(A04/A05 h5_share 记录=unknown → pending_external,本
//     adapter 到不了那些行);
//   - A27/A28 纯链接分支无内容包依赖:link_share 的 share_url 由服务端从
//     已发布动作 TargetRef 生成,本 adapter 只交接+给复制回退;
//   - 建议平台(A22/A24/A25/A27/A28)步骤保留"建议补充"标注;关注/入群入口
//     (A23/A26/A29/A30)归 4584/服务票,分享按钮永不混记。
package adapters

import (
	"context"
	"fmt"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/publishhandoff"
)

// ManualFamilyPlatforms lists the platforms the 4588 family serves
// (A04/A05/A06 必需 + A22/A24/A25/A27/A28 建议补充;能力真源在 3535 记录,
// 本列表只是注册面)。
var ManualFamilyPlatforms = []string{
	"wechat", "wechat_channels", "bilibili", "weibo", "qq", "qzone",
}

// ManualAdapter implements the manual/web-share family for one platform.
type ManualAdapter struct {
	platform string
	steps    ManualPlatformSteps
}

// NewManualAdapter builds the family adapter for one platform (per-platform
// registration; Platform() 必须与 3535 记录的 platform 维度一致).
func NewManualAdapter(platform string) *ManualAdapter {
	return &ManualAdapter{platform: platform, steps: manualStepsFor(platform)}
}

// Platform implements Adapter.
func (a *ManualAdapter) Platform() string { return a.platform }

// Prepare builds the typed client action:
//   - manual_material → manual_export(冻结词表;结构化参数携 ordered_media/
//     copy_view/共享提示,渲染由 web publish-adapters 按实机能力降级);
//   - link_share → open_url(服务端已验证 share_url)+复制回退步骤;
//   - 其余方法诚实 unsupported。
func (a *ManualAdapter) Prepare(ctx context.Context, req publishhandoff.PrepareRequest) (publishhandoff.PrepareResult, error) {
	switch req.Handoff.Method {
	case "manual_material":
		return a.prepareManualMaterial(req), nil
	case "link_share":
		return a.prepareLinkShare(req)
	default:
		return publishhandoff.PrepareResult{}, fmt.Errorf("%w: %s method %q is not served by the manual family", publishhandoff.ErrUnsupported, a.platform, req.Handoff.Method)
	}
}

// prepareManualMaterial: 素材预览/有序保存/用户主动复制/系统分享(按实机降级)
// +准确平台步骤。环境差异:六环境枚举逐环境给真实操作说明(能力真值仍在
// 3535;步骤只说怎么做,不改判定)。
func (a *ManualAdapter) prepareManualMaterial(req publishhandoff.PrepareRequest) publishhandoff.PrepareResult {
	ordered := orderedMediaParams(req)
	caption := manualCaption(req.Text)
	params := map[string]string{
		"share_mode": "system_share_or_copy", // 前端按 navigator.canShare 实测降级
		"platform":   a.platform,
	}
	for k, v := range ordered {
		params[k] = v
	}
	steps := append([]string(nil), a.steps.Steps...)
	if len(ordered) > 0 {
		steps = append(steps, environmentSteps(req.Environment, a.steps)...)
	}
	act := publishhandoff.ClientAction{
		Type:      publishhandoff.ClientActionManualExport,
		Caption:   caption,
		Steps:     steps,
		MediaNote: a.steps.MediaNote,
		Params:    params,
		Note:      a.steps.NotePrefix + "系统分享成功只记录素材交接;「已完成」只存你本人的声明——都不生成平台确认,也不触发奖励。",
	}
	return publishhandoff.PrepareResult{
		ClientAction:      act,
		UnsupportedFields: append([]string(nil), req.Plan.UnsupportedFields...),
	}
}

// prepareLinkShare: A27/A28 纯链接——share_url 服务端已验证(TargetRef),无
// 内容包;复制回退 + 打开目标(open_url=冻结词表)。
func (a *ManualAdapter) prepareLinkShare(req publishhandoff.PrepareRequest) (publishhandoff.PrepareResult, error) {
	share := strings.TrimSpace(req.ShareURL)
	if share == "" || !strings.HasPrefix(share, "https://") {
		return publishhandoff.PrepareResult{}, fmt.Errorf("adapters: %s link_share requires the server-verified share_url", a.platform)
	}
	act := publishhandoff.ClientAction{
		Type: publishhandoff.ClientActionOpenURL,
		URL:  share,
		Params: map[string]string{
			"platform":   a.platform,
			"share_mode": "link_copy_fallback",
		},
		Steps: a.steps.LinkSteps,
		Note:  a.steps.NotePrefix + "链接由服务端给出;系统分享/只打开目标/点「已完成」都不是发布成功,也不自动发消息给任何联系人。",
	}
	return publishhandoff.PrepareResult{
		ClientAction:      act,
		UnsupportedFields: append([]string(nil), req.Plan.UnsupportedFields...),
	}, nil
}

// Submit: 手动家族没有官方发布契约——诚实 unsupported(状态不推进)。
func (a *ManualAdapter) Submit(ctx context.Context, h publishhandoff.Handoff, p publishhandoff.PrepareResult) error {
	return fmt.Errorf("%w: %s manual family has no official publish contract", publishhandoff.ErrUnsupported, a.platform)
}

// Query: 无官方查询契约——契约语义的诚实答案就是 unsupported(调用方 422,
// 状态不推进;手动交接没有"查询"这一能力,不伪造结果面)。
func (a *ManualAdapter) Query(ctx context.Context, h publishhandoff.Handoff) (publishhandoff.QueryOutcome, error) {
	return publishhandoff.QueryOutcome{}, fmt.Errorf("%w: %s manual family has no official query contract", publishhandoff.ErrUnsupported, a.platform)
}

// VerifyCallback: 无已核实回执契约——拒绝一切回调(验签在先,不伪造验签器)。
func (a *ManualAdapter) VerifyCallback(ctx context.Context, raw publishhandoff.CallbackPayload) (publishhandoff.Receipt, error) {
	return publishhandoff.Receipt{}, fmt.Errorf("%w: %s manual family has no verified callback contract", publishhandoff.ErrUnsupported, a.platform)
}

// orderedMediaParams flattens the frozen snapshot into ordered structured
// params (图片保序;媒体引用是短时效受控 URL,过期由读面重铸).
func orderedMediaParams(req publishhandoff.PrepareRequest) map[string]string {
	out := map[string]string{}
	var order []string
	for i, m := range req.Media {
		ref := ""
		if req.MediaRefs != nil {
			ref = req.MediaRefs[m.AssetID]
		}
		if strings.TrimSpace(ref) == "" {
			continue
		}
		key := fmt.Sprintf("media_%02d", i)
		out[key] = m.Kind + ":" + ref
		order = append(order, key)
	}
	if len(order) > 0 {
		out["media_order"] = strings.Join(order, ",")
	}
	return out
}

// manualCaption assembles the reference copy for the user-initiated copy view.
func manualCaption(t publishhandoff.TextInput) string {
	parts := make([]string, 0, 3)
	if s := strings.TrimSpace(t.Title); s != "" {
		parts = append(parts, s)
	}
	if s := strings.TrimSpace(t.Body); s != "" {
		parts = append(parts, s)
	}
	if len(t.Topics) > 0 {
		tags := make([]string, 0, len(t.Topics))
		for _, tp := range t.Topics {
			tp = strings.TrimSpace(tp)
			if tp == "" {
				continue
			}
			if !strings.HasPrefix(tp, "#") {
				tp = "#" + tp
			}
			tags = append(tags, tp)
		}
		if len(tags) > 0 {
			parts = append(parts, strings.Join(tags, " "))
		}
	}
	return strings.Join(parts, "\n")
}

// environmentSteps gives the per-environment honest how-to (六环境三态的
// 工程面;能力判定真值仍在 3535 记录).
func environmentSteps(environment string, steps ManualPlatformSteps) []string {
	switch environment {
	case "ios_wechat", "android_wechat":
		return []string{
			"微信内打开本页时部分系统能力受限：如保存/分享不可用，请点右上角菜单选择「在浏览器打开」后重试",
			steps.TargetHint,
		}
	case "desktop_browser":
		return []string{
			"桌面浏览器无系统分享面板：请下载素材到手机后再进入" + steps.TargetName + "发布",
			steps.TargetHint,
		}
	case "ios_safari", "android_browser":
		return []string{steps.TargetHint}
	case "target_app":
		return []string{"已在目标 App 环境打开：直接使用当前素材继续" + steps.TargetName + "的发布流程"}
	default:
		return []string{"当前环境未识别：按素材保存与手动发布步骤操作即可"}
	}
}

// ManualPlatformSteps is one platform's honest instruction set
// (platform_steps.go 提供逐平台差异).
type ManualPlatformSteps struct {
	TargetName string   // 平台显示名
	Steps      []string // 平台主步骤
	LinkSteps  []string // link_share 步骤
	TargetHint string   // 环境步骤里的平台提示
	MediaNote  string   // 素材保存说明
	NotePrefix string   // 诚实说明前缀(含建议补充标注)
}
