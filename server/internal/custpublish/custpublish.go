// Package custpublish is the HUI-1670 customer preview and publish gate.
//
// The publisher is the customer who joined the activity. Merchant accounts are
// rejected. Official platform publish stays closed until an authorized app,
// customer OAuth, and a verifiable platform receipt exist. This deployment has
// none of those, and this ticket does not authorize production outbound calls
// or reward charges.
//
// A button click, an explicit confirmation, an export, or a client-supplied
// post id is not publish success. Only StatusPublishConfirmed plus an
// official_query receipt counts, and even that does not issue a reward here.
package custpublish

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Platforms the activity page can prepare a manual publish for.
const (
	PlatformDouyin      = Platform("douyin")
	PlatformKuaishou    = Platform("kuaishou")
	PlatformXiaohongshu = Platform("xiaohongshu")
	PlatformChannels    = Platform("channels")
)

// PublisherActivityCustomer is the only legal publish subject for this ticket.
const PublisherActivityCustomer = "activity_customer"

// Capability kinds shown in the per-platform matrix.
const (
	CapPreview           = Capability("preview")
	CapExport            = Capability("export")
	CapOpenEditor        = Capability("open_editor")
	CapAuthorizedPublish = Capability("authorized_publish")
	CapConfirmPublish    = Capability("confirm_publish")
)

// Attempt statuses. publish_requested / editor_opened / publish_confirmed are
// recognized so callers can tell them apart, but this deployment does not
// enter the success status: confirm_publish is closed on every platform.
const (
	StatusPreviewed        = "previewed"
	StatusExported         = "exported"
	StatusEditorOpened     = "editor_opened"
	StatusPublishRequested = "publish_requested"
	StatusPublishConfirmed = "publish_confirmed"
	StatusUnknown          = "unknown"
)

// Machine reason codes.
const (
	ReasonPublisherMustBeCustomer = "publisher_must_be_activity_customer"
	ReasonCapabilityClosed        = "capability_closed"
	ReasonQueryRequired           = "query_required"
	ReasonNoOfficialEvidence      = "no_official_publish_evidence"
	ReasonRewardOutOfScope        = "reward_issuance_out_of_scope"
	ReasonNotAwaiting             = "not_awaiting_platform"
)

// ReceiptOfficialQuery is the only receipt source that can count as published.
const ReceiptOfficialQuery = "official_query"

// Platform is a distribution channel key.
type Platform string

// Capability is one cell of the matrix.
type Capability string

// CapState is whether a cell is usable, and why.
type CapState struct {
	Enabled     bool
	Reason      string
	EvidenceURL string
}

// AdapterNote records that some adapter was registered. Registration never
// flips a capability on.
type AdapterNote struct {
	Platform string
	Name     string
}

// PlatformRow is one channel in the matrix.
type PlatformRow struct {
	Platform    Platform
	Publisher   string
	Caps        map[Capability]CapState
	ManualGuide string
}

// Capability returns one cell. A missing cell is closed.
func (r PlatformRow) Capability(kind Capability) CapState {
	if r.Caps == nil {
		return CapState{Reason: "capability not defined"}
	}
	return r.Caps[kind]
}

// Attempt is one customer preparation. It is not a platform post.
type Attempt struct {
	Platform                Platform
	Publisher               string
	Copy                    string
	AccountLabel            string
	ContentVersion          string
	Status                  string
	AssetUseAccepted        bool
	ConfirmedContentVersion string
	ConfirmedAccountLabel   string
	SelfReported            bool
	PlatformPostID          string
	ReceiptSource           string
	OutboundCalls           int
	QueryCount              int
	TimedOut                bool
	RewardTriggered         bool
}

// PreviewInput is the customer's content selection.
type PreviewInput struct {
	Platform     Platform
	Publisher    string
	Copy         string
	AccountLabel string
}

// Confirmation is the customer's explicit consent for this copy and account.
// It is not a platform receipt.
type Confirmation struct {
	ContentVersion   string
	AccountLabel     string
	Platform         Platform
	Publisher        string
	AssetUseAccepted bool
}

// Package is a compliant export: caption plus manual steps. PostID is empty
// because export does not create a platform post.
type Package struct {
	PostID  string
	Caption string
	Steps   []string
}

// Evidence is a platform result offered by a caller. While confirm_publish is
// closed it is discarded and never copied onto the attempt.
type Evidence struct {
	Source         string
	PostID         string
	Platform       Platform
	ContentVersion string
	AccountLabel   string
	Plays          *int64
	Likes          *int64
	Completion     *int64
	POIExposure    *int64
}

// Metric is a platform engagement number. Unavailable metrics keep a nil
// value so callers cannot render a fabricated zero.
type Metric struct {
	Available bool
	Value     *int64
	Reason    string
}

// Reward is the decision to call the published-reward service. This ticket
// never triggers it.
type Reward struct {
	Trigger bool
	Reason  string
}

type gateError struct{ Reason string }

func (e *gateError) Error() string {
	if e == nil {
		return ""
	}
	return e.Reason
}

func closed(reason string) error { return &gateError{Reason: reason} }

// ReasonOf returns a machine reason when err came from this package.
func ReasonOf(err error) string {
	var g *gateError
	if errors.As(err, &g) && g != nil {
		return g.Reason
	}
	return ""
}

// NoteAdapter appends a registration note. The matrix does not read it.
func NoteAdapter(existing []AdapterNote, platform, name string) []AdapterNote {
	return append(existing, AdapterNote{Platform: platform, Name: strings.TrimSpace(name)})
}

// Matrix returns the four channels in a fixed order. Adapter notes are
// accepted so callers can show that registration happened, and are ignored
// when deciding which capabilities are enabled.
func Matrix(_ []AdapterNote) []PlatformRow {
	return []PlatformRow{
		row(PlatformDouyin, "抖音",
			"https://developer.open-douyin.com/docs/resource/zh-CN/dop/develop/sdk/mobile-app/share/android",
			"https://developer.open-douyin.com/docs/resource/zh-CN/dop/develop/openapi/video-management/douyin/create-video/video-create",
			"抖音分享 SDK 可唤起编辑页，但需要已审核移动应用的 client_key。本部署没有已验证的抖音应用，不能唤起编辑器。",
			"代替用户发布需要 scope video.create.bind、顾客 OAuth 与内容审核。本部署没有已验证应用和顾客授权，本票也不授权生产外发。",
			"创建视频后平台仍要审核；分享 SDK 的客户端回调不是发布回执。没有授权应用就无法查询视频状态。",
		),
		row(PlatformKuaishou, "快手",
			"https://open.kuaishou.com/platformDocs/openAbility/contentManagement/createAVideo",
			"https://open.kuaishou.com/platformDocs/openAbility/contentManagement/createAVideo",
			"本部署没有已审核的快手应用，不能代顾客唤起快手编辑器。",
			"发布视频需要已开通的 user_video_publish、顾客 access_token，且接口返回不等于已出现在用户主页。本部署没有该授权，也不外发。",
			"快手文档写明发布接口返回后不代表已同步到用户主页，必须另行查询。没有授权就无法查询，不能把请求回包当成发布成功。",
		),
		row(PlatformXiaohongshu, "小红书",
			"https://openaccount.xiaohongshu.com/docs/scope",
			"https://openaccount.xiaohongshu.com/docs/scope",
			"小红书 write_notes 仍是规划中的敏感权限，本部署没有可唤起编辑器的已审核应用。",
			"发布笔记 scope write_notes 标注为规划中，需人工审核且仅特定场景开放。本部署未获该权限，不能授权发布。",
			"没有已开放的笔记发布回执可查。不能用客户端自报代替平台确认。",
		),
		row(PlatformChannels, "视频号",
			"https://developers.weixin.qq.com/doc/store/shop/dev_before/guide.html",
			"https://developers.weixin.qq.com/doc/store/shop/dev_before/guide.html",
			"公开的微信接口是微信小店商家能力，不是顾客视频号编辑器。不能唤起。",
			"没有把顾客视频号内容代发出来的已授权接口。微信小店商家接口不能改成顾客 UGC。本票不外发。",
			"没有顾客视频号发布结果的官方查询。商家小店回执不能记成这位顾客的发布成功。",
		),
	}
}

func row(p Platform, appName, editorURL, publishURL, editorReason, publishReason, confirmReason string) PlatformRow {
	return PlatformRow{
		Platform:    p,
		Publisher:   PublisherActivityCustomer,
		ManualGuide: fmt.Sprintf("请顾客手动打开%s，用自己的账号新建作品，粘贴导出的文案后自行发布。不要用商家账号代替，本系统不会代发。", appName),
		Caps: map[Capability]CapState{
			CapPreview: {
				Enabled: true,
				Reason:  "活动页本地预览文案与发布步骤，不调用平台接口。",
			},
			CapExport: {
				Enabled: true,
				Reason:  "合规导出文案和手动发布步骤，不上传到平台，也不生成 post_id。",
			},
			CapOpenEditor:        {Enabled: false, Reason: editorReason, EvidenceURL: editorURL},
			CapAuthorizedPublish: {Enabled: false, Reason: publishReason, EvidenceURL: publishURL},
			CapConfirmPublish:    {Enabled: false, Reason: confirmReason, EvidenceURL: publishURL},
		},
	}
}

func lookup(p Platform, kind Capability) CapState {
	for _, row := range Matrix(nil) {
		if row.Platform == p {
			return row.Capability(kind)
		}
	}
	return CapState{Reason: "unknown platform"}
}

// Preview records a local preview. It does not contact a platform.
func Preview(in PreviewInput) (Attempt, error) {
	if in.Publisher != PublisherActivityCustomer {
		return Attempt{}, closed(ReasonPublisherMustBeCustomer)
	}
	if !known(in.Platform) {
		return Attempt{}, closed("unknown_platform")
	}
	copy := strings.TrimSpace(in.Copy)
	account := strings.TrimSpace(in.AccountLabel)
	if copy == "" {
		return Attempt{}, closed("missing_copy_or_account")
	}
	return Attempt{
		Platform: in.Platform, Publisher: in.Publisher, Copy: copy, AccountLabel: account,
		ContentVersion: fingerprint(copy), Status: StatusPreviewed,
	}, nil
}

func known(p Platform) bool {
	switch p {
	case PlatformDouyin, PlatformKuaishou, PlatformXiaohongshu, PlatformChannels:
		return true
	}
	return false
}

func fingerprint(copy string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(copy)))
	return hex.EncodeToString(sum[:])
}

// Export builds a manual-publish package. It does not upload or invent a post id.
func Export(a Attempt) (Attempt, Package, error) {
	if a.Status != StatusPreviewed && a.Status != StatusExported {
		return a, Package{}, closed("export_requires_preview")
	}
	a.Status = StatusExported
	a.PlatformPostID = ""
	name := appName(a.Platform)
	return a, Package{
		Caption: a.Copy,
		Steps: []string{
			fmt.Sprintf("打开%s", name),
			"使用你自己的账号，不要切换成商家账号",
			"新建作品并粘贴导出的文案",
			"在官方 App 里自行发布；本系统看不到发布结果",
		},
	}, nil
}

func appName(p Platform) string {
	switch p {
	case PlatformDouyin:
		return "抖音"
	case PlatformKuaishou:
		return "快手"
	case PlatformXiaohongshu:
		return "小红书"
	case PlatformChannels:
		return "微信视频号"
	default:
		return "对应平台 App"
	}
}

// Confirm stores explicit consent for the current copy and account.
// The status does not become publish_confirmed.
func Confirm(a Attempt, c Confirmation) (Attempt, error) {
	if c.Publisher != PublisherActivityCustomer || a.Publisher != PublisherActivityCustomer {
		return a, closed(ReasonPublisherMustBeCustomer)
	}
	if !c.AssetUseAccepted {
		return a, closed("asset_use_not_accepted")
	}
	if c.Platform != a.Platform || c.ContentVersion != a.ContentVersion || c.AccountLabel != a.AccountLabel {
		return a, closed("confirmation_mismatch")
	}
	a.AssetUseAccepted = true
	a.ConfirmedContentVersion = a.ContentVersion
	a.ConfirmedAccountLabel = a.AccountLabel
	a.PlatformPostID = ""
	a.RewardTriggered = false
	return a, nil
}

// ConfirmationCurrent reports whether the stored consent still matches the
// copy and account the customer would publish.
func (a Attempt) ConfirmationCurrent() bool {
	return a.AssetUseAccepted &&
		a.ConfirmedContentVersion != "" &&
		a.ConfirmedContentVersion == a.ContentVersion &&
		a.ConfirmedAccountLabel == a.AccountLabel
}

// ChangeCopy invalidates consent. The customer must confirm the new copy.
// An empty copy is rejected and the attempt is left unchanged.
func ChangeCopy(a Attempt, copy string) (Attempt, error) {
	copy = strings.TrimSpace(copy)
	if copy == "" {
		return a, closed("missing_copy_or_account")
	}
	a.Copy = copy
	a.ContentVersion = fingerprint(a.Copy)
	a.Status = StatusPreviewed
	clearConfirmation(&a)
	return a, nil
}

// ChangeAccount invalidates consent. A different account is a new publish.
func ChangeAccount(a Attempt, account string) Attempt {
	a.AccountLabel = strings.TrimSpace(account)
	a.Status = StatusPreviewed
	clearConfirmation(&a)
	return a
}

func clearConfirmation(a *Attempt) {
	a.AssetUseAccepted = false
	a.ConfirmedContentVersion = ""
	a.ConfirmedAccountLabel = ""
}

// SelfReport records that the customer says they published. The supplied post
// id is discarded. This is not platform evidence.
func SelfReport(a Attempt, _ string) Attempt {
	a.SelfReported = true
	a.PlatformPostID = ""
	a.ReceiptSource = ""
	a.RewardTriggered = false
	return a
}

// RequestPublish refuses to call a platform while authorized publish is closed.
// It does not move the attempt to publish_requested, because no request was sent.
func RequestPublish(a Attempt) (Attempt, error) {
	if !lookup(a.Platform, CapAuthorizedPublish).Enabled {
		a.PlatformPostID = ""
		a.RewardTriggered = false
		return a, closed(ReasonCapabilityClosed)
	}
	return a, closed(ReasonCapabilityClosed)
}

// OpenEditor refuses to launch a platform editor while that capability is closed.
func OpenEditor(a Attempt) (Attempt, error) {
	if !lookup(a.Platform, CapOpenEditor).Enabled {
		return a, closed(ReasonCapabilityClosed)
	}
	return a, closed(ReasonCapabilityClosed)
}

// MarkTimedOut moves an in-flight platform request to unknown without sending
// another request.
func MarkTimedOut(a Attempt, _ time.Time) (Attempt, error) {
	if a.Status != StatusPublishRequested {
		return a, closed(ReasonNotAwaiting)
	}
	a.Status = StatusUnknown
	a.TimedOut = true
	a.PlatformPostID = ""
	return a, nil
}

// Resend is rejected after a timeout until the caller queries. This deployment
// also has no authorized publish, so resend never places a second outbound call.
func Resend(a Attempt) (Attempt, error) {
	if a.Status == StatusUnknown || a.TimedOut {
		return a, closed(ReasonQueryRequired)
	}
	return a, closed(ReasonCapabilityClosed)
}

// Query asks for a platform result and does not send the publish again.
// With confirm_publish closed, the result stays unknown and post id stays empty.
func Query(a Attempt) (Attempt, error) {
	if a.Status != StatusPublishRequested && a.Status != StatusUnknown {
		return a, closed(ReasonNotAwaiting)
	}
	a.QueryCount++
	a.Status = StatusUnknown
	a.PlatformPostID = ""
	a.ReceiptSource = ""
	return a, nil
}

// ApplyOfficialEvidence accepts a platform receipt only when confirm_publish
// is enabled for that platform. It is not, so the post id is dropped.
func ApplyOfficialEvidence(a Attempt, _ Evidence) (Attempt, error) {
	if !lookup(a.Platform, CapConfirmPublish).Enabled {
		a.PlatformPostID = ""
		a.ReceiptSource = ""
		if a.Status == StatusPublishRequested {
			a.Status = StatusUnknown
		}
		return a, closed(ReasonCapabilityClosed)
	}
	return a, closed(ReasonCapabilityClosed)
}

// CountsAsPublished is true only for a confirmed attempt that carries an
// official query receipt and a non-empty platform post id, and whose publisher
// is the activity customer.
func (a Attempt) CountsAsPublished() bool {
	return a.Status == StatusPublishConfirmed &&
		a.Publisher == PublisherActivityCustomer &&
		a.ReceiptSource == ReceiptOfficialQuery &&
		strings.TrimSpace(a.PlatformPostID) != ""
}

// RewardDecision never triggers issuance. Without an official receipt the
// reason is the missing evidence; with one, reward issuance belongs to
// HUI-1671 and is still not called from here.
func RewardDecision(a Attempt) Reward {
	if !a.CountsAsPublished() {
		return Reward{Trigger: false, Reason: ReasonNoOfficialEvidence}
	}
	return Reward{Trigger: false, Reason: ReasonRewardOutOfScope}
}

// InterpretMetric hides numbers the platform has not authorized, and treats a
// missing authorized field as unknown rather than zero.
func InterpretMetric(authorized bool, raw *int64) Metric {
	if !authorized || raw == nil {
		reason := "平台未授权，不能把接口数字或缺失当成已知的完播、点赞或 POI 曝光。"
		if authorized && raw == nil {
			reason = "平台结果未返回该指标，不能记成 0。"
		}
		return Metric{Available: false, Value: nil, Reason: reason}
	}
	v := *raw
	return Metric{Available: true, Value: &v}
}
