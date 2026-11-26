// douyin_status.go: HUI-4585 抖音路线区分、结构化可用状态评估、官方 H5 URL
// 规范编码与事实分级模型。
//
// 纪律:
//   - 准入真源始终是 3535 (channeladmission.JudgeActionRoute),本文件绝不自建
//     白名单或绕过 store_promo=not_admitted;
//   - 明确区分 douyin_h5(通用官方 H5)与 douyin_miniapp_task(独立获准专项任务);
//   - 结构化返回六态(can_handoff/needs_auth/processing/failed/not_applicable/
//     qualification_pending)及缺项原因与允许下一步,绝不用模糊布尔开关;
//   - 严格区分唤起 App/编辑页、返回 H5(弱信号绝不确认发布)、官方发布回执、
//     审核/公开范围/后续删除,回执缺失保持 unknown;
//   - 任何公开载荷/状态说明/错误摘要零泄露 client_secret/open_ticket/token。
package adapters

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
	"github.com/bianjiefilm/touch-engine/server/internal/publishhandoff"
)

// Douyin route identifiers distinguishing official H5 share from the
// separately approved miniapp task route.
const (
	DouyinMethodH5          = "douyin_h5"
	DouyinMethodMiniappTask = "douyin_miniapp_task"
)

// DouyinAvailabilityStatus is the structured six-state availability enum
// required by HUI-4585 (never ambiguous booleans).
type DouyinAvailabilityStatus string

const (
	DouyinStatusCanHandoff           DouyinAvailabilityStatus = "can_handoff"
	DouyinStatusNeedsAuth            DouyinAvailabilityStatus = "needs_auth"
	DouyinStatusProcessing           DouyinAvailabilityStatus = "processing"
	DouyinStatusFailed               DouyinAvailabilityStatus = "failed"
	DouyinStatusNotApplicable        DouyinAvailabilityStatus = "not_applicable"
	DouyinStatusQualificationPending DouyinAvailabilityStatus = "qualification_pending"
)

// Allowed customer next-step identifiers returned alongside DouyinAvailabilityStatus.
const (
	DouyinNextConfirmAndInvoke    = "confirm_and_invoke_douyin"
	DouyinNextAuthorizeAccount    = "authorize_douyin_account"
	DouyinNextQueryOriginal       = "query_original_handoff"
	DouyinNextOpenInSystemBrowser = "open_in_system_browser"
	DouyinNextPreviewContent      = "preview_content"
	DouyinNextCancelHandoff       = "cancel_handoff"
	DouyinNextReturnToStore       = "return_to_store_services"
)

// DouyinFactLevel strictly separates weak client signals, moderation/visibility
// facts, subsequent deletion, missing receipts, and verified official publish.
type DouyinFactLevel string

const (
	DouyinFactAppInvoked            DouyinFactLevel = "app_invoked"
	DouyinFactEditorOpened          DouyinFactLevel = "editor_opened"
	DouyinFactReturnedToH5          DouyinFactLevel = "returned_to_h5"
	DouyinFactOfficialUnknown       DouyinFactLevel = "official_receipt_unknown"
	DouyinFactUnderReview           DouyinFactLevel = "under_review"
	DouyinFactVisibilityRestricted  DouyinFactLevel = "visibility_restricted"
	DouyinFactDeleted               DouyinFactLevel = "deleted"
	DouyinFactOfficialPublished     DouyinFactLevel = "official_published"
)

// Douyin sentinel errors for precise refusal & negative-case verification.
var (
	ErrDouyinRouteNotAdmitted      = errors.New("adapters: douyin route not admitted by channel admission")
	ErrDouyinWechatContainerBlocked = fmt.Errorf("%w: douyin handoff blocked inside WeChat container", ErrCustomerActionRequired)
	ErrDouyinShareNotFound         = errors.New("adapters: douyin share_id binding not found")
	ErrDouyinShareRevoked          = errors.New("adapters: douyin share_id binding revoked")
	ErrDouyinShareExpired          = errors.New("adapters: douyin share_id expired")
	ErrDouyinSignatureInvalid      = errors.New("adapters: douyin official signature missing or expired")
	ErrDouyinAppMismatch           = errors.New("adapters: douyin app binding mismatch")
	ErrDouyinTenantMismatch        = errors.New("adapters: douyin tenant binding mismatch")
	ErrDouyinStoreMismatch         = errors.New("adapters: douyin store binding mismatch")
	ErrDouyinContentVersionMismatch = errors.New("adapters: douyin content version mismatch")
	ErrDouyinContextMismatch       = errors.New("adapters: douyin handoff context mismatch")
)

// DouyinRouteEvalInput feeds 3535 admission truth, runtime environment,
// deployment readiness, and optional handoff runtime state into
// EvaluateDouyinRoute.
type DouyinRouteEvalInput struct {
	Record           channeladmission.ActionCapability
	Query            channeladmission.ActionQuery
	Method           string               // h5_share | douyin_h5 | miniapp | douyin_miniapp_task | ...
	HandoffStatus    publishhandoff.State // optional existing handoff state
	NeedsLogin       bool                 // true when user has not completed required Douyin login/auth
	ClientKeyReady   bool                 // true when server holds client_key
	SignerReady      bool                 // true when official openTicket signer is wired
	MiniappPathReady bool                 // true when approved miniapp task route path is configured
}

// DouyinRouteAssessment is the structured, customer-safe status projection for
// a Douyin route (zero ambiguous booleans, zero secret leakage).
type DouyinRouteAssessment struct {
	RouteMethod             string                   `json:"route_method"`
	Scenario                string                   `json:"scenario"`
	Environment             string                   `json:"environment"`
	Status                  DouyinAvailabilityStatus `json:"status"`
	AdmissionReason         string                   `json:"admission_reason,omitempty"`
	ReasonCode              string                   `json:"reason_code,omitempty"`
	Summary                 string                   `json:"summary"`
	MissingReasons          []string                 `json:"missing_reasons"`
	AllowedNextSteps        []string                 `json:"allowed_next_steps"`
	ManualFallbackAllowed   bool                     `json:"manual_fallback_allowed"`
	ExecutablePayloadAllowed bool                    `json:"executable_payload_allowed"`
	EvidenceLevel           string                   `json:"evidence_level"`
}

// canonicalDouyinRoute maps channeladmission method names and HUI-4585 route
// names onto the two official Douyin route identifiers.
func canonicalDouyinRoute(method string) (string, bool) {
	switch strings.TrimSpace(method) {
	case channeladmission.ActionMethodH5Share, DouyinMethodH5:
		return DouyinMethodH5, true
	case channeladmission.ActionMethodMiniapp, DouyinMethodMiniappTask:
		return DouyinMethodMiniappTask, true
	default:
		return strings.TrimSpace(method), false
	}
}

// isWechatContainer reports whether env is a WeChat embedded WebView container
// where third-party Douyin URL scheme / miniapp invocation is blocked.
func isWechatContainer(env string) bool {
	switch strings.TrimSpace(env) {
	case channeladmission.EnvIOSWechat, channeladmission.EnvAndroidWechat:
		return true
	default:
		return false
	}
}

// EvaluateDouyinRoute projects a deterministic, structured six-state assessment
// by consuming channeladmission.JudgeActionRoute as the single admission source.
func EvaluateDouyinRoute(in DouyinRouteEvalInput) DouyinRouteAssessment {
	methodInput := strings.TrimSpace(in.Method)
	if methodInput == "" {
		methodInput = strings.TrimSpace(in.Record.Method)
	}
	routeMethod, supportedRoute := canonicalDouyinRoute(methodInput)
	decision := channeladmission.JudgeActionRoute(in.Query, in.Record)

	out := DouyinRouteAssessment{
		RouteMethod:              routeMethod,
		Scenario:                 in.Record.Scenario,
		Environment:              in.Query.Environment,
		AdmissionReason:          decision.Reason,
		MissingReasons:           append([]string(nil), decision.Missing...),
		ManualFallbackAllowed:    false, // HUI-4585: 绝不退化为“手动发完领券”
		ExecutablePayloadAllowed: false,
		EvidenceLevel:            evidenceLevelFromCap(decision.Caps.PlatformReceipt),
	}

	// 1) Unsupported method on Douyin (e.g. manual_material) is never applicable.
	if !supportedRoute {
		out.Status = DouyinStatusNotApplicable
		out.ReasonCode = "method_not_applicable"
		out.Summary = "抖音当前动作不支持该交接方式，也不提供绕过准入的手动发完领券入口。"
		if len(out.MissingReasons) == 0 {
			out.MissingReasons = []string{"抖音仅区分官方 H5（douyin_h5）与独立获准专项任务（douyin_miniapp_task）路线"}
		}
		out.AllowedNextSteps = []string{DouyinNextReturnToStore}
		return out
	}

	// 2) Existing terminal handoff states take precedence for honest reporting.
	switch in.HandoffStatus {
	case publishhandoff.StateCanceled:
		out.Status = DouyinStatusFailed
		out.ReasonCode = "customer_canceled"
		out.Summary = "本次抖音交接已取消，不会产生发布成功事件或奖励。"
		out.AllowedNextSteps = []string{DouyinNextReturnToStore}
		return out
	case publishhandoff.StateFailed:
		out.Status = DouyinStatusFailed
		out.ReasonCode = "handoff_failed"
		out.Summary = "本次抖音交接准备失败，未生成可执行外发载荷。"
		out.AllowedNextSteps = []string{DouyinNextReturnToStore}
		return out
	}

	// 3) 3535 admission gate is the single authority (never bypassed).
	if !decision.Allowed {
		out.ReasonCode = decision.Reason
		out.AllowedNextSteps = []string{DouyinNextReturnToStore}
		switch {
		case decision.PendingExternal || decision.Reason == channeladmission.ReasonPendingExternal || decision.Reason == channeladmission.ReasonAdmissionUnknown:
			out.Status = DouyinStatusQualificationPending
			if routeMethod == DouyinMethodMiniappTask {
				out.Summary = "抖音专项任务路线尚未获得对应主体、场景与奖励玩法的正式批复，当前为资格待核实状态，不生成可执行载荷。"
			} else {
				out.Summary = "抖音该场景准入资格待核实，暂不能发起交接，也不提供替代的手动发完领券入口。"
			}
			if len(out.MissingReasons) == 0 {
				out.MissingReasons = []string{"平台官方能力与场景准入正式批复"}
			}
		case decision.Reason == channeladmission.ReasonAdmissionExpired:
			out.Status = DouyinStatusQualificationPending
			out.Summary = "抖音准入复检窗已过期，需重新核验官方准入记录后方可发起新交接。"
			out.MissingReasons = appendMissingUnique(out.MissingReasons, "更新后的有效官方准入核验记录")
		default:
			// not_admitted (including store_promo), emergency_disabled, channel_closed,
			// wrong_binding, reward_play_not_authorized
			out.Status = DouyinStatusNotApplicable
			out.Summary = sanitizeDouyinSummary(decision.ReasonText, "官方规范明确排除该场景或当前通道不可用，不生成可执行载荷，也不提供手动发完领券入口。")
		}
		return out
	}

	// 4) Method/route isolation: record method must match requested route.
	recordRoute, _ := canonicalDouyinRoute(in.Record.Method)
	if recordRoute != "" && recordRoute != routeMethod {
		out.Status = DouyinStatusNotApplicable
		out.ReasonCode = "route_method_mismatch"
		out.Summary = "当前准入记录与所选抖音路线不匹配，不能借用其他路线的获准资格。"
		out.MissingReasons = appendMissingUnique(out.MissingReasons, "所选路线的独立官方准入记录")
		out.AllowedNextSteps = []string{DouyinNextReturnToStore}
		return out
	}

	// 5) WeChat container restriction: third-party Douyin invoke is blocked inside WeChat WebView.
	if isWechatContainer(in.Query.Environment) {
		out.Status = DouyinStatusNotApplicable
		out.ReasonCode = "wechat_container_blocked"
		out.Summary = "当前处于微信内置浏览器容器，无法直接唤起抖音官方入口；请在系统浏览器中打开原页面继续，本页不降级为手动发完领券。"
		out.MissingReasons = appendMissingUnique(out.MissingReasons, "需切换至系统浏览器（微信容器阻拦抖音唤起协议）")
		out.AllowedNextSteps = []string{DouyinNextOpenInSystemBrowser, DouyinNextReturnToStore}
		return out
	}

	// 6) Server-side official credentials / route configuration readiness.
	var missingCreds []string
	if !in.ClientKeyReady {
		missingCreds = append(missingCreds, "服务端抖音官方应用凭据（client_key）未配置")
	}
	if routeMethod == DouyinMethodH5 && !in.SignerReady {
		missingCreds = append(missingCreds, "服务端抖音官方 openTicket 签名服务未配置")
	}
	if routeMethod == DouyinMethodMiniappTask && !in.MiniappPathReady {
		missingCreds = append(missingCreds, "已独立获准的抖音专项任务小程序路由未配置")
	}
	if len(missingCreds) > 0 {
		out.Status = DouyinStatusQualificationPending
		out.ReasonCode = "deployment_qualification_incomplete"
		out.MissingReasons = appendMissingUnique(out.MissingReasons, missingCreds...)
		if routeMethod == DouyinMethodMiniappTask {
			out.Summary = "抖音专项任务路线的部署凭证或获准路由未就绪，当前为资格待核实状态。"
		} else {
			out.Summary = "抖音官方 H5 签名或应用凭证未就绪，暂不生成可执行外发载荷。"
		}
		out.AllowedNextSteps = []string{DouyinNextReturnToStore}
		return out
	}

	// 7) Runtime login / in-flight states.
	if in.NeedsLogin {
		out.Status = DouyinStatusNeedsAuth
		out.ReasonCode = "douyin_account_auth_required"
		out.Summary = "请先在抖音客户端完成本人账号登录与确认后再继续交接；未登录或取消不会确认发布。"
		out.AllowedNextSteps = []string{DouyinNextAuthorizeAccount, DouyinNextCancelHandoff}
		return out
	}
	switch in.HandoffStatus {
	case publishhandoff.StateEditorOpened, publishhandoff.StateSubmitting,
		publishhandoff.StateAwaitingReview, publishhandoff.StateUnknown:
		out.Status = DouyinStatusProcessing
		out.ReasonCode = "handoff_in_progress"
		out.Summary = "已进入交接或结果待确认阶段，请先回查原记录状态；返回 H5 或打开编辑页不等于发布成功。"
		out.AllowedNextSteps = []string{DouyinNextQueryOriginal, DouyinNextCancelHandoff}
		return out
	}

	// 8) Admitted + non-WeChat + credentials ready + no blocking runtime state.
	out.Status = DouyinStatusCanHandoff
	out.ReasonCode = "admitted"
	out.ExecutablePayloadAllowed = true
	if routeMethod == DouyinMethodMiniappTask {
		out.Summary = "已核实专项任务路线资格，确认本次指定素材后可由你本人主动打开抖音专项任务入口。"
	} else {
		out.Summary = "已核实官方 H5 路线资格，确认本次指定素材后可由你本人主动唤起抖音继续编辑与发布。"
	}
	out.AllowedNextSteps = []string{DouyinNextConfirmAndInvoke, DouyinNextPreviewContent, DouyinNextCancelHandoff}
	return out
}

func evidenceLevelFromCap(receiptCap string) string {
	switch receiptCap {
	case channeladmission.CapSupported:
		return publishhandoff.EvidenceLevelOfficialReceipt
	case channeladmission.CapUnsupported:
		return publishhandoff.EvidenceLevelNone
	default:
		return publishhandoff.EvidenceLevelUnknown
	}
}

func appendMissingUnique(base []string, extra ...string) []string {
	seen := make(map[string]bool, len(base))
	for _, s := range base {
		seen[s] = true
	}
	out := append([]string(nil), base...)
	for _, item := range extra {
		item = strings.TrimSpace(item)
		if item != "" && !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	return out
}

// CanonicalizeDouyinMediaURL validates and percent-encodes a controlled HTTPS
// media URL, properly encoding Chinese characters and special symbols while
// rejecting non-HTTPS schemes, control characters, embedded credentials,
// fragments, or malformed percent escapes.
func CanonicalizeDouyinMediaURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("adapters: douyin https media required")
	}
	for _, r := range trimmed {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("adapters: douyin media url contains control character")
		}
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("adapters: douyin media url invalid")
	}
	if !strings.EqualFold(u.Scheme, "https") || strings.TrimSpace(u.Host) == "" || u.User != nil || u.Fragment != "" {
		return "", fmt.Errorf("adapters: douyin https media required")
	}
	u.Scheme = "https"
	// Re-encode path strictly via RFC 3986 (percent-encoding UTF-8 / spaces / special chars).
	u.RawPath = ""
	if u.RawQuery != "" {
		vals, qerr := url.ParseQuery(u.RawQuery)
		if qerr != nil {
			return "", fmt.Errorf("adapters: douyin media query invalid")
		}
		u.RawQuery = vals.Encode()
	}
	return u.String(), nil
}

// VerifyShareSignatureFreshness enforces official H5 signature presence,
// share_id == state binding, non-expired share_expires_at, and timestamp skew.
func VerifyShareSignatureFreshness(params map[string]string, now time.Time, maxSkew time.Duration) error {
	if params == nil {
		return fmt.Errorf("%w: empty params", ErrDouyinSignatureInvalid)
	}
	if maxSkew <= 0 {
		maxSkew = 10 * time.Minute
	}
	for _, k := range []string{"client_key", "share_id", "state", "nonce_str", "timestamp", "signature", "share_expires_at"} {
		if strings.TrimSpace(params[k]) == "" {
			return fmt.Errorf("%w: missing %s", ErrDouyinSignatureInvalid, k)
		}
	}
	if params["state"] != params["share_id"] {
		return fmt.Errorf("%w: state/share_id mismatch", ErrDouyinSignatureInvalid)
	}
	exp, err := time.Parse(time.RFC3339Nano, params["share_expires_at"])
	if err != nil || !now.Before(exp) {
		return fmt.Errorf("%w: share_id expired", ErrDouyinShareExpired)
	}
	ts, err := strconv.ParseInt(strings.TrimSpace(params["timestamp"]), 10, 64)
	if err != nil || ts <= 0 {
		return fmt.Errorf("%w: invalid timestamp", ErrDouyinSignatureInvalid)
	}
	signedAt := time.Unix(ts, 0).UTC()
	diff := now.UTC().Sub(signedAt)
	if diff < 0 {
		diff = -diff
	}
	if diff > maxSkew {
		return fmt.Errorf("%w: signature timestamp expired", ErrDouyinSignatureInvalid)
	}
	return nil
}

// BuildDouyinH5ShareURL encodes only the documented official Douyin H5 share
// fields with canonical query escaping (supporting Chinese/special characters).
// Generic media/body params or sensitive secrets are strictly excluded.
func BuildDouyinH5ShareURL(params map[string]string, now time.Time) (string, error) {
	if params["sdk"] != DouyinMethodH5 {
		return "", fmt.Errorf("adapters: douyin_h5 sdk marker required")
	}
	if err := VerifyShareSignatureFreshness(params, now, 10*time.Minute); err != nil {
		return "", err
	}
	mediaKey := ""
	switch {
	case strings.TrimSpace(params["video_path"]) != "":
		mediaKey = "video_path"
	case strings.TrimSpace(params["image_path"]) != "":
		mediaKey = "image_path"
	default:
		return "", fmt.Errorf("adapters: douyin https media required")
	}
	canonicalMedia, err := CanonicalizeDouyinMediaURL(params[mediaKey])
	if err != nil {
		return "", err
	}
	q := url.Values{}
	q.Set("share_type", "h5")
	q.Set("client_key", params["client_key"])
	q.Set("nonce_str", params["nonce_str"])
	q.Set("timestamp", params["timestamp"])
	q.Set("signature", params["signature"])
	q.Set("state", params["state"])
	q.Set(mediaKey, canonicalMedia)
	if title := strings.TrimSpace(params["title"]); title != "" {
		q.Set("title", title)
	}
	if hashtags := strings.TrimSpace(params["hashtag_list"]); hashtags != "" {
		q.Set("hashtag_list", hashtags)
	}
	return "snssdk1128://openplatform/share?" + q.Encode(), nil
}

// DouyinObservation captures the classified platform-side fact from an official
// share-result query so moderation, visibility scope, and subsequent deletion
// are never flattened into a single "create" event.
type DouyinObservation struct {
	Level           DouyinFactLevel
	CanonicalState  string
	ModerationState string
	VisibilityScope string
	Deleted         bool
	ConfirmsPublish bool
}

// ClassifyDouyinObservation classifies a platform-reported state + detail map
// into a strict fact level. Only explicit public/published states confirm
// publication; auditing/review, private/restricted visibility, deletion, or
// unknown/missing states NEVER confirm publication.
func ClassifyDouyinObservation(state string, published bool, detail map[string]string) DouyinObservation {
	normState := strings.ToLower(strings.TrimSpace(state))
	mod := strings.ToLower(strings.TrimSpace(detail["moderation_state"]))
	if mod == "" {
		mod = strings.ToLower(strings.TrimSpace(detail["audit_status"]))
	}
	vis := strings.ToLower(strings.TrimSpace(detail["visibility_scope"]))
	if vis == "" {
		vis = strings.ToLower(strings.TrimSpace(detail["visibility"]))
	}
	if vis == "" {
		vis = strings.ToLower(strings.TrimSpace(detail["private_status"]))
	}

	// 1) Subsequent deletion / takedown MUST NOT be flattened by a prior create/published flag.
	if normState == "deleted" || normState == "removed" || normState == "takedown" ||
		strings.EqualFold(strings.TrimSpace(detail["deleted"]), "true") ||
		strings.TrimSpace(detail["deleted_at"]) != "" {
		return DouyinObservation{
			Level:           DouyinFactDeleted,
			CanonicalState:  "deleted",
			ModerationState: firstNonEmptyDouyin(mod, "deleted"),
			VisibilityScope: vis,
			Deleted:         true,
			ConfirmsPublish: false,
		}
	}

	// 2) Moderation / review / rejected states never confirm publish.
	switch normState {
	case "auditing", "under_review", "reviewing", "pending_review", "in_review", "rejected", "review_rejected", "blocked":
		return DouyinObservation{
			Level:           DouyinFactUnderReview,
			CanonicalState:  normState,
			ModerationState: firstNonEmptyDouyin(mod, normState),
			VisibilityScope: vis,
			ConfirmsPublish: false,
		}
	}
	switch mod {
	case "auditing", "under_review", "reviewing", "pending", "rejected", "blocked":
		return DouyinObservation{
			Level:           DouyinFactUnderReview,
			CanonicalState:  firstNonEmptyDouyin(normState, mod),
			ModerationState: mod,
			VisibilityScope: vis,
			ConfirmsPublish: false,
		}
	}

	// 3) Visibility restriction (private / self-only / friends-only) never confirms public publication.
	switch normState {
	case "private", "self_only", "self_visible", "friends_only", "mutual_only":
		return DouyinObservation{
			Level:           DouyinFactVisibilityRestricted,
			CanonicalState:  normState,
			ModerationState: mod,
			VisibilityScope: firstNonEmptyDouyin(vis, normState),
			ConfirmsPublish: false,
		}
	}
	switch vis {
	case "private", "self_only", "self_visible", "friends_only", "mutual_only", "1", "2":
		return DouyinObservation{
			Level:           DouyinFactVisibilityRestricted,
			CanonicalState:  firstNonEmptyDouyin(normState, vis),
			ModerationState: mod,
			VisibilityScope: vis,
			ConfirmsPublish: false,
		}
	}

	// 4) Weak client-side words must never confirm publication.
	switch normState {
	case "", "unknown", "missing", "created", "draft", "editing", "editor_opened", "app_invoked", "returned_to_h5", "canceled", "cancelled", "failed":
		canon := normState
		if canon == "" {
			canon = "unknown"
		}
		return DouyinObservation{
			Level:           DouyinFactOfficialUnknown,
			CanonicalState:  canon,
			ModerationState: mod,
			VisibilityScope: vis,
			ConfirmsPublish: false,
		}
	}

	// 5) Explicit published confirmation requires BOTH published==true AND explicit published/public state.
	if published && (normState == "published" || normState == "public") {
		return DouyinObservation{
			Level:           DouyinFactOfficialPublished,
			CanonicalState:  "published",
			ModerationState: firstNonEmptyDouyin(mod, "approved"),
			VisibilityScope: firstNonEmptyDouyin(vis, "public"),
			ConfirmsPublish: true,
		}
	}

	return DouyinObservation{
		Level:           DouyinFactOfficialUnknown,
		CanonicalState:  normState,
		ModerationState: mod,
		VisibilityScope: vis,
		ConfirmsPublish: false,
	}
}

func firstNonEmptyDouyin(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// sanitizeDouyinDiagnostic strips sensitive credential fragments so secrets or
// internal tokens never leak into public payloads, status summaries, or event rows.
func sanitizeDouyinDiagnostic(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	lower := strings.ToLower(s)
	for _, marker := range []string{"client_secret", "open_ticket", "ticket=", "access_token", "access-token", "bearer ", "authorization"} {
		if strings.Contains(lower, marker) {
			return "redacted_internal_detail"
		}
	}
	return s
}

func sanitizeDouyinSummary(raw, fallback string) string {
	s := sanitizeDouyinDiagnostic(raw)
	if s == "" || s == "redacted_internal_detail" {
		return fallback
	}
	return s
}

func sanitizeDouyinDetailMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		lk := strings.ToLower(strings.TrimSpace(k))
		if lk == "" || strings.Contains(lk, "secret") || strings.Contains(lk, "ticket") ||
			strings.Contains(lk, "token") || strings.Contains(lk, "bearer") || strings.Contains(lk, "authorization") {
			continue
		}
		out[k] = sanitizeDouyinDiagnostic(v)
	}
	return out
}
