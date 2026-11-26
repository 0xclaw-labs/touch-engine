package publishflow

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// ConfirmationInput captures the current selection and any prior confirmation
// so we can guarantee that changing selected content or target account
// invalidates prior confirmations and rejects untrusted free-text accounts.
type ConfirmationInput struct {
	PlanKind            PlanKind
	ContentVersion      string        // Adopted content version / frozen snapshot fingerprint
	TargetAccountRef    string        // Server-verified target account (empty if route has no account binding)
	AccountSource       AccountSource // Must be verified_oauth|platform_receipt when TargetAccountRef != ""
	SelfReportedAccount string        // Any free-text account string supplied by client (NEVER trusted)

	PriorConfirmedDigest string // Digest captured at previous customer confirmation
	PriorContentVersion  string // Optional prior content version for explicit change detection
	PriorAccountRef      string // Optional prior account ref for explicit change detection
}

// ConfirmationDigest computes the canonical confirmation binding covering
// plan kind, content version, and server-verified target account.
func ConfirmationDigest(planKind PlanKind, contentVersion, trustedAccountRef string) string {
	payload := strings.Join([]string{
		string(planKind),
		strings.TrimSpace(contentVersion),
		strings.TrimSpace(trustedAccountRef),
	}, "\x1f")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

// ValidateConfirmation verifies that:
//  1. No free-text / self-reported account string is treated as trusted ownership.
//  2. If a prior confirmation exists, neither selected content nor target
//     account has changed; any change returns ErrConfirmationStale.
func ValidateConfirmation(in ConfirmationInput) error {
	if strings.TrimSpace(in.SelfReportedAccount) != "" {
		return fmt.Errorf("%w: free-text account declaration cannot prove ownership", ErrUntrustedAccountSource)
	}
	if in.AccountSource == AccountSourceFreeText || in.AccountSource == AccountSourceSelfReported {
		return fmt.Errorf("%w: account source %q is not verified", ErrUntrustedAccountSource, in.AccountSource)
	}
	if strings.TrimSpace(in.TargetAccountRef) != "" && !IsTrustedAccountSource(in.AccountSource) {
		return fmt.Errorf("%w: target account requires verified source, got %q", ErrUntrustedAccountSource, in.AccountSource)
	}

	curContent := strings.TrimSpace(in.ContentVersion)
	curAccount := strings.TrimSpace(in.TargetAccountRef)
	priorContent := strings.TrimSpace(in.PriorContentVersion)
	priorAccount := strings.TrimSpace(in.PriorAccountRef)
	priorDigest := strings.TrimSpace(in.PriorConfirmedDigest)

	if priorContent != "" && priorContent != curContent {
		return fmt.Errorf("%w: content changed from %q to %q", ErrConfirmationStale, priorContent, curContent)
	}
	if priorAccount != "" && priorAccount != curAccount {
		return fmt.Errorf("%w: target account changed", ErrConfirmationStale)
	}
	if priorDigest != "" {
		want := ConfirmationDigest(in.PlanKind, curContent, curAccount)
		if priorDigest != want {
			return fmt.Errorf("%w: confirmation digest no longer matches current selection", ErrConfirmationStale)
		}
	}
	return nil
}

// StepAction describes one customer-facing action button/control.
type StepAction struct {
	Kind        ActionKind `json:"kind"`
	Label       string     `json:"label"`
	Description string     `json:"description,omitempty"`
	Primary     bool       `json:"primary"`
	Destructive bool       `json:"destructive,omitempty"`
}

// NextStepInput is the server-assembled input to ProjectCustomerNextStep.
type NextStepInput struct {
	PlanKind         PlanKind
	State            FlowState
	PlatformLabel    string // Human-readable platform name, e.g. "抖音", "小红书", "快手", "微信"
	HasContent       bool   // Whether this route carries a content snapshot (media vs pure link)
	AssetCount       int    // Number of selectable assets/versions (<=1 omits asset picker)
	OAuthAuthorized  bool   // For oauth_publish: whether server holds a valid verified OAuth token
	OfficialVerified bool   // True ONLY when an official verified platform receipt confirmed publication
	HasCampaign      bool   // Whether an active store campaign/benefit is attached
	NoAutoReturn     bool   // True when platform app does not automatically return to H5

	ContentVersion       string
	TargetAccountRef     string
	AccountSource        AccountSource
	SelfReportedAccount  string
	PriorConfirmedDigest string
	PriorContentVersion  string
	PriorAccountRef      string

	// RawReason and RawNotice may contain backend/adapter messages; customer
	// output ALWAYS sanitizes them and retains raw strings in Diagnostics only.
	RawReason string
	RawNotice string
	RawParams map[string]string
}

// MerchantDiagnostics holds internal technical details strictly excluded from
// customer JSON views (exposed only to authorized merchant/ops handlers).
type MerchantDiagnostics struct {
	RawReason string            `json:"raw_reason,omitempty"`
	RawNotice string            `json:"raw_notice,omitempty"`
	RawParams map[string]string `json:"raw_params,omitempty"`
}

// NextStepProjection is the customer-safe single-primary-action projection
// (HUI-4660 AC1/AC2/AC4).
type NextStepProjection struct {
	PlanKind      PlanKind  `json:"plan_kind"`
	State         FlowState `json:"state"`
	Headline      string    `json:"headline"`
	Summary       string    `json:"summary"`
	PrimaryAction StepAction `json:"primary_action"`
	// SecondaryActions keeps preview, asset-switch (when >1 asset), cancel,
	// return-guide, and helper controls reachable without competing visually
	// with the single primary action.
	SecondaryActions []StepAction `json:"secondary_actions"`

	NeedsConfirmation           bool   `json:"needs_confirmation"`
	ConfirmationStale           bool   `json:"confirmation_stale"`
	ConfirmationDigest          string `json:"confirmation_digest,omitempty"`
	OmitAssetPicker             bool   `json:"omit_asset_picker"`
	ShowFreeTextAccountInput    bool   `json:"show_free_text_account_input"`
	FreeTextAccountTrusted      bool   `json:"free_text_account_trusted"`
	TrustedAccountRef           string `json:"trusted_account_ref,omitempty"`
	ManualRewardFallbackAllowed bool   `json:"manual_reward_fallback_allowed"`
	MustQueryBeforeRetry        bool   `json:"must_query_before_retry"`
	ReturnGuidance              string `json:"return_guidance,omitempty"`
	Notices                     []string `json:"notices,omitempty"`
	CustomerParams              map[string]string `json:"customer_params,omitempty"`

	// Diagnostics is never serialized into customer JSON responses.
	Diagnostics MerchantDiagnostics `json:"-"`
}

// ProjectCustomerNextStep computes a deterministic, customer-safe projection
// highlighting exactly ONE primary action while preserving all required
// supporting actions, honest behavioral copy, confirmation freshness, and
// diagnostic sanitization.
func ProjectCustomerNextStep(in NextStepInput) NextStepProjection {
	platform := strings.TrimSpace(in.PlatformLabel)
	if platform == "" {
		platform = "目标平台"
	}

	// Sanitize params and isolate diagnostics for authorized ops/merchant view.
	safeParams, diagParams := SanitizeCustomerParams(in.RawParams)

	// Never treat free-text / self-reported account strings as trusted ownership.
	trustedAccount := ""
	if IsTrustedAccountSource(in.AccountSource) && strings.TrimSpace(in.SelfReportedAccount) == "" {
		trustedAccount = strings.TrimSpace(in.TargetAccountRef)
	}

	// Evaluate confirmation freshness.
	confErr := ValidateConfirmation(ConfirmationInput{
		PlanKind:             in.PlanKind,
		ContentVersion:       in.ContentVersion,
		TargetAccountRef:     trustedAccount,
		AccountSource:        effectiveAccountSource(in.AccountSource, trustedAccount),
		PriorConfirmedDigest: in.PriorConfirmedDigest,
		PriorContentVersion:  in.PriorContentVersion,
		PriorAccountRef:      in.PriorAccountRef,
	})
	confirmationStale := confErr != nil
	currentDigest := ConfirmationDigest(in.PlanKind, in.ContentVersion, trustedAccount)

	// Normalize restricted plan kinds so refused / pending_external NEVER
	// degrade to manual-export reward completion.
	effectiveState := in.State
	if effectiveState == "" {
		effectiveState = StateReady
	}
	if in.PlanKind == PlanRefused || in.PlanKind == PlanPendingExternal {
		if effectiveState != StateCancelled && effectiveState != StateCompleted {
			effectiveState = StateRejected
		}
	}

	needsConfirmation := false
	if effectiveState == StateReady && in.PlanKind != PlanRefused && in.PlanKind != PlanPendingExternal {
		if confirmationStale || strings.TrimSpace(in.PriorConfirmedDigest) == "" {
			needsConfirmation = true
		}
	}

	proj := NextStepProjection{
		PlanKind:                    in.PlanKind,
		State:                       effectiveState,
		OmitAssetPicker:             in.AssetCount <= 1,
		ShowFreeTextAccountInput:    false, // HUI-4660: free-text account never trusted or prompted as ownership
		FreeTextAccountTrusted:      false,
		TrustedAccountRef:           trustedAccount,
		ManualRewardFallbackAllowed: false,
		NeedsConfirmation:           needsConfirmation,
		ConfirmationStale:           confirmationStale,
		CustomerParams:              safeParams,
		Diagnostics: MerchantDiagnostics{
			RawReason: in.RawReason,
			RawNotice: in.RawNotice,
			RawParams: diagParams,
		},
	}
	if in.PlanKind != PlanRefused && in.PlanKind != PlanPendingExternal {
		proj.ConfirmationDigest = currentDigest
	}

	// Select honest headline, summary, and ONE primary action by (PlanKind, State).
	proj.Headline, proj.Summary, proj.PrimaryAction = buildPrimaryStep(in, effectiveState, platform, needsConfirmation, confirmationStale)

	// On unknown / in-flight states, enforce query-before-retry guidance.
	if effectiveState == StateUnknown || effectiveState == StateInProgress {
		proj.MustQueryBeforeRetry = true
	}

	// Return guidance when platform app does not auto-return or task is active away from H5.
	if in.NoAutoReturn || effectiveState == StateInProgress || effectiveState == StateUnderReview || effectiveState == StateUnknown {
		proj.ReturnGuidance = "若平台未自动返回本页，请切换回原浏览器页面或通过原会话恢复入口继续，系统会保留本次已选内容与进度。"
	}

	// Build reachable secondary/helper actions without duplicating PrimaryAction.
	proj.SecondaryActions = buildSecondaryActions(in, effectiveState, proj.PrimaryAction.Kind, proj.OmitAssetPicker)

	// Attach sanitized customer-safe notices.
	proj.Notices = buildCustomerNotices(in, effectiveState, confirmationStale)

	return proj
}

func effectiveAccountSource(src AccountSource, trustedRef string) AccountSource {
	if trustedRef == "" {
		return AccountSourceNone
	}
	return src
}

func sanitizeForPlan(kind PlanKind, raw, fallback string) string {
	cleaned := SanitizeCustomerText(raw, fallback)
	if cleaned == "" {
		return fallback
	}
	if kind == PlanManualOnly || kind == PlanOfficialHandoff || kind == PlanRefused || kind == PlanPendingExternal {
		if strings.Contains(cleaned, "发布完成") {
			if kind == PlanRefused || kind == PlanPendingExternal {
				return fallback
			}
			cleaned = strings.ReplaceAll(cleaned, "发布完成", "交接记录已留存")
		}
	}
	return cleaned
}

func buildPrimaryStep(in NextStepInput, state FlowState, platform string, needsConfirm, confirmStale bool) (headline, summary string, primary StepAction) {
	// 1) Refused / pending_external: strictly no manual-publish reward backdoor.
	if in.PlanKind == PlanRefused || in.PlanKind == PlanPendingExternal || state == StateRejected {
		if in.PlanKind == PlanPendingExternal {
			headline = "当前平台通道待确认"
			summary = sanitizeForPlan(PlanPendingExternal, in.RawNotice, "该平台发布通道尚未完成外部资格确认，暂不支持发起交接，也不会降级为手动发完领奖。")
		} else {
			headline = "当前动作不支持此发布通道"
			summary = sanitizeForPlan(PlanRefused, in.RawNotice, "该场景当前未开放此平台发布交接，也不会提供手动发布后领奖的替代入口。")
		}
		if state == StateCancelled {
			headline = "本次发布交接已取消"
			summary = "本次交接记录已结束并保留历史状态，你可以返回门店页重新选择其他服务。"
		} else if state == StateCompleted {
			headline = "历史交接记录已留存"
			summary = "历史已确认记录仍可查看；当前渠道不再开放新交接。"
		}
		return headline, summary, StepAction{
			Kind:        ActionReturnToStore,
			Label:       "返回门店服务页",
			Description: "查看门店其他可用服务或咨询入口",
			Primary:     true,
		}
	}

	// 2) Terminal & cross-plan states.
	switch state {
	case StateCancelled:
		return "本次交接已取消",
			"本次操作已结束且不会再次提交，你可以返回门店页重新开始。",
			StepAction{Kind: ActionReturnToStore, Label: "返回门店页", Description: "返回门店首页查看其他可用服务", Primary: true}

	case StateMediaFailed:
		return "素材准备失败，请重试准备",
			SanitizeCustomerText(in.RawReason, "当前素材暂无法完成交接准备，不会产生额外扣费或重复提交，可重新获取素材或更换其他素材。"),
			StepAction{Kind: ActionRetryMediaExport, Label: "重新准备素材", Description: "重新拉取当前已选素材，不重复新建提交", Primary: true}

	case StateAppNotInstalled:
		if in.PlanKind == PlanOfficialHandoff {
			return "未检测到" + platform + "客户端",
				"当前设备未安装或无法唤起" + platform + "，请先安装打开对应应用，或按提示保存素材后手动前往" + platform + "发布。",
				StepAction{Kind: ActionInstallOrSwitchManual, Label: "查看安装或手动发布指引", Description: "保留当前已选内容，可安装后继续或改为手动交接", Primary: true}
		}
		return "未能打开" + platform + "应用",
			"请确认已安装" + platform + "后重试，或使用已导出的文案与素材手动前往发布。",
			StepAction{Kind: ActionInstallOrSwitchManual, Label: "查看手动交接步骤", Description: "使用当前已选素材与文案继续", Primary: true}

	case StateAuthDenied:
		return platform + "账号授权未通过",
			"你取消或拒绝了" + platform + "账号授权，本次不会提交内容，已选素材仍为你保留。",
			StepAction{Kind: ActionReauthorizeOrManual, Label: "重新授权账号", Description: "重新完成" + platform + "账号授权，或在下方改为手动交接", Primary: true}

	case StateUnknown:
		return "提交结果待确认，请先查询状态",
			"网络或平台返回暂时中断，当前结果尚未确认。请先查询原记录状态，系统不会盲目重复投稿或重复发券。",
			StepAction{Kind: ActionQueryStatus, Label: "查询原提交状态", Description: "优先核对本次原记录，避免重复提交", Primary: true}

	case StateUnderReview:
		if in.PlanKind == PlanManualOnly {
			return "素材已交接，等待核验结果",
				"素材导出或完成申报仅代表已交接，不代表平台结果已确认，请等待门店核验或稍后查询最新状态。",
				StepAction{Kind: ActionQueryStatus, Label: "刷新核验进度", Description: "查看本次交接与核验的最新状态", Primary: true}
		}
		return "内容审核或核验处理中",
			"平台或门店正在处理本次记录，审核期间请勿重复提交，可随时刷新当前进度。",
			StepAction{Kind: ActionQueryStatus, Label: "查询最新进度", Description: "回查同一条记录的审核与确认结果", Primary: true}

	case StateInProgress:
		switch in.PlanKind {
		case PlanManualOnly:
			return "请前往" + platform + "完成手动发布",
				"素材与文案已准备就绪，导出或复制仅代表交接进行中，不代表已在平台生效。",
				StepAction{Kind: ActionQueryStatus, Label: "查看当前交接进度", Description: "从平台返回后可在此核对本次记录进度", Primary: true}
		case PlanOfficialHandoff:
			return "已交接至" + platform + "，等待平台结果",
				"页面跳转或唤起仅表示已交接给" + platform + "，返回本页后请查询状态，不代表自动生效。",
				StepAction{Kind: ActionQueryStatus, Label: "查询平台处理状态", Description: "回查同一条交接记录，避免重复发起", Primary: true}
		case PlanOAuthPublish:
			return "正在向" + platform + "提交内容",
				"已发起授权提交请求，异步受理不等于最终公开，请查询原任务结果。",
				StepAction{Kind: ActionQueryStatus, Label: "查询提交结果", Description: "按原任务回查最终公开状态，不重复投稿", Primary: true}
		}

	case StateCompleted:
		// Honest copy invariant: manual_only and official_handoff NEVER claim "发布完成".
		switch in.PlanKind {
		case PlanManualOnly:
			return "本次素材交接与核验记录已留存",
				"已记录本次手动交接的核验结果；如需查看权益或历史记录，可点击下方查看。",
				StepAction{Kind: ActionViewResult, Label: "查看记录详情", Description: "查看本次交接记录与权益状态", Primary: true}
		case PlanOfficialHandoff:
			if in.OfficialVerified {
				return "已收到" + platform + "官方确认回执",
					"平台已返回核验通过的官方结果记录，可在详情中查看本次交接与权益事实。",
					StepAction{Kind: ActionViewResult, Label: "查看确认记录", Description: "查看本次官方回执记录与权益状态", Primary: true}
			}
			return "本次官方交接记录已归档",
				"本次交接记录已留存，可在详情页分别核对交接状态与权益状态。",
				StepAction{Kind: ActionViewResult, Label: "查看交接记录", Description: "查看本次记录详情", Primary: true}
		case PlanOAuthPublish:
			return platform + "已确认本次授权投稿结果",
				"平台已返回最终确认结果，你可以查看本次记录及关联权益状态。",
				StepAction{Kind: ActionViewResult, Label: "查看投稿结果", Description: "查看本次投稿与权益记录", Primary: true}
		}
	}

	// 3) Ready state by PlanKind (and stale confirmation re-confirmation).
	if confirmStale {
		return "所选内容或账号已变更，请重新确认",
			"更换内容或目标账号后，旧确认已失效。请核对本次内容后再执行下一步。",
			StepAction{Kind: ActionReconfirmSelection, Label: "重新确认本次内容", Description: "核对最新选定内容后继续当前交接步骤", Primary: true}
	}

	switch in.PlanKind {
	case PlanManualOnly:
		label := "导出素材并复制文案"
		if needsConfirm {
			label = "确认内容并导出素材"
		}
		return "准备手动前往" + platform + "发布",
			"当前通道为素材导出与手动指引：导出或复制仅代表素材交接，请随后自行打开" + platform + "完成操作。",
			StepAction{Kind: ActionConfirmAndExport, Label: label, Description: "导出当前已选素材与参考文案，手动前往" + platform + "发布", Primary: true}

	case PlanOfficialHandoff:
		label := "打开" + platform + "继续交接"
		if needsConfirm {
			label = "确认内容并打开" + platform
		}
		return "准备交接至" + platform,
			"确认后将交接至" + platform + "官方入口，最终发布操作由你在" + platform + "内自主完成，打开动作本身不代表已在平台生效。",
			StepAction{Kind: ActionConfirmAndOpenPlatform, Label: label, Description: "携带已支持字段交接给" + platform + "官方入口", Primary: true}

	case PlanOAuthPublish:
		if !in.OAuthAuthorized {
			return "先授权你的" + platform + "账号",
				"当前为授权服务端提交路径：需先由你本人授权" + platform + "账号，授权后回到本页再次确认本次内容才会提交。",
				StepAction{Kind: ActionAuthorizeAccount, Label: "前往授权" + platform + "账号", Description: "授权后返回本页确认内容再提交，不会自动发布", Primary: true}
		}
		label := "确认内容并提交至" + platform
		return "已就绪，请确认本次授权提交",
			"目标账号已由服务端核验就绪，确认本次内容后将提交至" + platform + "处理，异步受理不等于最终公开。",
			StepAction{Kind: ActionConfirmAndSubmit, Label: label, Description: "确认当前内容与目标账号后提交，并按原记录回查结果", Primary: true}

	default:
		return "当前通道暂不可用",
			"未识别到可用的发布通道，请返回门店页查看其他可用服务。",
			StepAction{Kind: ActionReturnToStore, Label: "返回门店页", Primary: true}
	}
}

func buildSecondaryActions(in NextStepInput, state FlowState, primaryKind ActionKind, omitAssetPicker bool) []StepAction {
	out := make([]StepAction, 0, 5)
	add := func(a StepAction) {
		if a.Kind == primaryKind {
			return
		}
		for _, existing := range out {
			if existing.Kind == a.Kind {
				return
			}
		}
		out = append(out, a)
	}

	isRestricted := in.PlanKind == PlanRefused || in.PlanKind == PlanPendingExternal || state == StateRejected
	isTerminal := state == StateCompleted || state == StateCancelled || state == StateRejected

	// Preview stays reachable whenever content exists (even on review/in-progress).
	if in.HasContent && !isRestricted {
		add(StepAction{Kind: ActionPreviewContent, Label: "预览本次内容", Description: "核对当前将要交接的图文或视频内容"})
	}

	// Single-asset scenario omits redundant asset-picker friction; multi-asset keeps switch reachable.
	if in.HasContent && !omitAssetPicker && !isRestricted && (state == StateReady || state == StateMediaFailed || state == StateAuthDenied || state == StateAppNotInstalled) {
		add(StepAction{Kind: ActionSwitchAsset, Label: "更换素材", Description: "切换其他已采用素材版本（更换后需重新确认）"})
	}

	// Plan-specific helpers (never added for refused/pending_external).
	if !isRestricted && !isTerminal {
		switch in.PlanKind {
		case PlanManualOnly:
			if in.HasContent {
				add(StepAction{Kind: ActionCopyCaption, Label: "复制参考文案"})
				add(StepAction{Kind: ActionDownloadMedia, Label: "保存素材文件"})
			}
			if state == StateInProgress || state == StateAppNotInstalled {
				add(StepAction{Kind: ActionSelfReportForReview, Label: "我已前往操作，请求核验", Description: "仅提交核验申请，不代表官方已确认"})
			}
		case PlanOfficialHandoff:
			if state == StateAppNotInstalled && in.HasContent {
				add(StepAction{Kind: ActionCopyCaption, Label: "复制参考文案"})
				add(StepAction{Kind: ActionDownloadMedia, Label: "保存素材备用"})
			}
		case PlanOAuthPublish:
			if state == StateAuthDenied && in.HasContent {
				add(StepAction{Kind: ActionCopyCaption, Label: "复制文案改手动处理"})
			}
		}
	}

	// Return guidance helper when platform may not auto-return.
	if in.NoAutoReturn || state == StateInProgress || state == StateUnderReview || state == StateUnknown {
		add(StepAction{Kind: ActionReturnGuide, Label: "如何返回原页面", Description: "查看未自动回跳时回到原会话与原尝试的方法"})
	}

	// History view when terminal.
	if isTerminal {
		add(StepAction{Kind: ActionViewHistory, Label: "查看历史记录"})
	}

	// Cancel remains reachable for every active (non-terminal) flow.
	if !isTerminal {
		add(StepAction{Kind: ActionCancel, Label: "取消本次操作", Destructive: true})
	}

	return out
}

func buildCustomerNotices(in NextStepInput, state FlowState, confirmationStale bool) []string {
	var notices []string
	if confirmationStale {
		notices = append(notices, "已选内容或目标账号发生变化，原有确认已自动失效，请重新确认后再继续。")
	}
	if strings.TrimSpace(in.SelfReportedAccount) != "" || in.AccountSource == AccountSourceFreeText || in.AccountSource == AccountSourceSelfReported {
		notices = append(notices, "手动填写的账号名称仅作参考，不会作为可信账号归属或官方确认依据。")
	}
	if !in.HasCampaign && in.PlanKind != PlanRefused && in.PlanKind != PlanPendingExternal {
		notices = append(notices, "本次为门店内容服务交接，未关联营销活动，不会强制创建活动参与或预留优惠券库存。")
	}
	if cleaned := sanitizeForPlan(in.PlanKind, in.RawNotice, ""); cleaned != "" {
		if state == StateRejected && strings.Contains(cleaned, "交接记录已留存") {
			// Do not attach contradictory completion-like notice to rejected states.
		} else {
			notices = append(notices, cleaned)
		}
	}
	if state == StateUnknown {
		notices = append(notices, "提交结果未知时请先回查原任务状态，避免重复投稿、重复参与或重复发券。")
	}
	return notices
}
