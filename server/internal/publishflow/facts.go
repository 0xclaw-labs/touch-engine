package publishflow

import "strings"

// PublishFactStatus represents the honest publish-side fact layer, strictly
// separated from reward/coupon issuance facts.
type PublishFactStatus string

const (
	PublishFactNotStarted        PublishFactStatus = "not_started"
	PublishFactHandoffPrepared   PublishFactStatus = "handoff_prepared"
	PublishFactInProgress        PublishFactStatus = "in_progress"
	PublishFactManualVerified    PublishFactStatus = "manual_verified"
	PublishFactUnderReview       PublishFactStatus = "under_review"
	PublishFactUnknown           PublishFactStatus = "unknown"
	PublishFactOfficialConfirmed PublishFactStatus = "official_confirmed"
	PublishFactFailed            PublishFactStatus = "failed"
	PublishFactCancelled         PublishFactStatus = "cancelled"
	PublishFactRefused           PublishFactStatus = "refused"
)

// IsTerminalPublishFact reports whether st ends the publish lifecycle so late
// events can NEVER overwrite it.
func IsTerminalPublishFact(st PublishFactStatus) bool {
	switch st {
	case PublishFactOfficialConfirmed, PublishFactCancelled, PublishFactFailed, PublishFactRefused:
		return true
	default:
		return false
	}
}

// RewardFactStatus represents the independent reward/coupon ledger fact layer.
type RewardFactStatus string

const (
	RewardFactNone        RewardFactStatus = "none"
	RewardFactNotEligible RewardFactStatus = "not_eligible"
	RewardFactReserved    RewardFactStatus = "reserved"
	RewardFactPending     RewardFactStatus = "pending"
	RewardFactIssued      RewardFactStatus = "issued"
	RewardFactRedeemed    RewardFactStatus = "redeemed"
	RewardFactExpired     RewardFactStatus = "expired"
	RewardFactRevoked     RewardFactStatus = "revoked"
)

// SignalKind enumerates incoming events/signals evaluated against the publish
// fact state machine.
type SignalKind string

const (
	// Weak / client-observed signals — NEVER escalate to official_confirmed.
	SignalH5Return           SignalKind = "h5_return"
	SignalWindowBlur         SignalKind = "window_blur"
	SignalAssetDownload      SignalKind = "asset_download"
	SignalSystemShareResolve SignalKind = "system_share_resolve"
	SignalSelfReport         SignalKind = "self_report"
	SignalCustomerClick      SignalKind = "customer_click"
	SignalWakeAttempt        SignalKind = "wake_attempt"
	SignalMediaHandoff       SignalKind = "media_handoff"

	// Non-customer merchant matrix receipts — STRICTLY SEPARATED from customer
	// publish evidence and customer UGC rewards (HUI-2390).
	SignalMerchantMatrixReceipt SignalKind = "merchant_matrix_receipt"

	// Stronger lifecycle events.
	SignalStaffConfirm    SignalKind = "staff_confirm"
	SignalOfficialReceipt SignalKind = "official_receipt"
	SignalNetworkTimeout  SignalKind = "network_timeout"
	SignalCancel          SignalKind = "cancel"
	SignalMediaFailure    SignalKind = "media_failure"
)

// IsWeakSignal reports whether k is a client/weak or non-customer signal that
// must NEVER escalate customer publish status to official published confirmation.
func IsWeakSignal(k SignalKind) bool {
	switch k {
	case SignalH5Return,
		SignalWindowBlur,
		SignalAssetDownload,
		SignalSystemShareResolve,
		SignalSelfReport,
		SignalCustomerClick,
		SignalWakeAttempt,
		SignalMediaHandoff,
		SignalMerchantMatrixReceipt:
		return true
	default:
		return false
	}
}

// IncomingPublishEvent is one event observed during handoff/recovery.
type IncomingPublishEvent struct {
	Kind           SignalKind
	Verified       bool   // For SignalOfficialReceipt: cryptographic signature verified
	BindingMatched bool   // For SignalOfficialReceipt: 5-binding + handoff binding matched
	ReceiptOutcome string // For SignalOfficialReceipt: "published" vs "other"
}

// FactReconcileInput feeds current publish & reward facts plus any incoming
// events into ReconcilePublishAndRewardFacts.
type FactReconcileInput struct {
	CurrentPublishStatus PublishFactStatus
	CurrentRewardStatus  RewardFactStatus
	RewardBenefitID      string
	HasCampaign          bool
	CampaignID           string
	Events               []IncomingPublishEvent
}

// PublishFactView is the customer-visible publish fact half of recovery.
type PublishFactView struct {
	Status            PublishFactStatus `json:"status"`
	OfficialConfirmed bool              `json:"official_confirmed"`
	ManualVerified    bool              `json:"manual_verified"`
	Terminal          bool              `json:"terminal"`
	Summary           string            `json:"summary"`
}

// RewardFactView is the customer-visible reward/coupon fact half of recovery.
type RewardFactView struct {
	Status                        RewardFactStatus `json:"status"`
	BenefitID                     string           `json:"benefit_id,omitempty"`
	CampaignAttached              bool             `json:"campaign_attached"`
	PreservedAcrossPublishUnknown bool             `json:"preserved_across_publish_unknown"`
	Summary                       string           `json:"summary"`
}

// ReconciledFacts holds both independently projected facts and guard verdicts.
type ReconciledFacts struct {
	PublishFact PublishFactView `json:"publish_fact"`
	RewardFact  RewardFactView  `json:"reward_fact"`

	// Explicit guard verdicts.
	MustNotCreateParticipation      bool         `json:"must_not_create_participation"`
	MustNotReserveInventory         bool         `json:"must_not_reserve_inventory"`
	RewardNeverImpliesOfficial      bool         `json:"reward_never_implies_official"`
	EscalationBlockedCount          int          `json:"escalation_blocked_count"`
	BlockedWeakSignals              []SignalKind `json:"blocked_weak_signals,omitempty"`
	LateEventsIgnored               int          `json:"late_events_ignored"`
	VerificationRequestRecordedOnly bool         `json:"verification_request_recorded_only"`
}

// ReconcilePublishAndRewardFacts enforces HUI-4660 fact separation and
// non-escalation invariants:
//  1. Publish fact and reward/coupon fact are reconciled independently:
//     - Publish outcome `unknown` NEVER degrades an already-issued/redeemed
//       coupon to `unknown`.
//     - Reward issuance NEVER back-infers official publish success.
//  2. Weak signals (h5_return, window_blur, asset_download, system_share_resolve,
//     self_report, customer_click, wake_attempt, media_handoff) NEVER escalate
//     publish status to official_confirmed.
//  3. Late events NEVER overwrite terminal publish states.
//  4. Campaign-less store handoff NEVER forces participation creation or
//     reserves coupon inventory.
func ReconcilePublishAndRewardFacts(in FactReconcileInput) ReconciledFacts {
	pub := in.CurrentPublishStatus
	if pub == "" {
		pub = PublishFactNotStarted
	}

	var (
		escalationBlocked int
		blockedSignals    []SignalKind
		lateIgnored       int
		verifyReqOnly     bool
	)

	for _, ev := range in.Events {
		// Invariant 3: terminal states are immutable; late events never overwrite them.
		if IsTerminalPublishFact(pub) {
			lateIgnored++
			continue
		}

		// Invariant 2: weak client signals NEVER escalate publish state.
		if IsWeakSignal(ev.Kind) {
			escalationBlocked++
			blockedSignals = append(blockedSignals, ev.Kind)
			if ev.Kind == SignalSelfReport {
				verifyReqOnly = true
			}
			continue
		}

		switch ev.Kind {
		case SignalStaffConfirm:
			// Manual verification NEVER escalates to official_confirmed.
			pub = PublishFactManualVerified
		case SignalOfficialReceipt:
			// Official confirmation requires signature verification, full
			// binding match, AND explicit "published" outcome.
			if ev.Verified && ev.BindingMatched && strings.EqualFold(strings.TrimSpace(ev.ReceiptOutcome), "published") {
				pub = PublishFactOfficialConfirmed
			} else if ev.Verified && ev.BindingMatched {
				// Non-published official receipt (e.g. under_review/other): record honestly, never confirm.
				pub = PublishFactUnderReview
			} else {
				escalationBlocked++
				blockedSignals = append(blockedSignals, ev.Kind)
			}
		case SignalNetworkTimeout:
			pub = PublishFactUnknown
		case SignalCancel:
			pub = PublishFactCancelled
		case SignalMediaFailure:
			pub = PublishFactFailed
		default:
			// Unknown signal kind fails closed (no escalation).
			escalationBlocked++
			blockedSignals = append(blockedSignals, ev.Kind)
		}
	}

	// Invariant 4: campaign-less store handoff NEVER forces participation or reserves coupon inventory.
	hasCampaign := in.HasCampaign && strings.TrimSpace(in.CampaignID) != ""
	rewardStatus := in.CurrentRewardStatus
	if rewardStatus == "" {
		rewardStatus = RewardFactNone
	}
	mustNotCreateParticipation := !hasCampaign
	mustNotReserveInventory := !hasCampaign
	if !hasCampaign {
		// Unless a historical benefit was already issued/redeemed before, a
		// campaign-less store handoff carries no active reward reservation.
		if rewardStatus != RewardFactIssued && rewardStatus != RewardFactRedeemed {
			rewardStatus = RewardFactNone
		}
	}

	// Invariant 1a: publish unknown/review/failed/cancelled NEVER degrades an
	// already-issued or redeemed coupon.
	preservedAcrossUnknown := false
	if (rewardStatus == RewardFactIssued || rewardStatus == RewardFactRedeemed) &&
		(pub == PublishFactUnknown || pub == PublishFactUnderReview || pub == PublishFactInProgress || pub == PublishFactFailed || pub == PublishFactCancelled) {
		preservedAcrossUnknown = true
	}

	// Invariant 1b: reward issuance NEVER back-infers official publish success.
	officialConfirmed := pub == PublishFactOfficialConfirmed
	manualVerified := pub == PublishFactManualVerified

	return ReconciledFacts{
		PublishFact: PublishFactView{
			Status:            pub,
			OfficialConfirmed: officialConfirmed,
			ManualVerified:    manualVerified,
			Terminal:          IsTerminalPublishFact(pub),
			Summary:           publishFactSummary(pub),
		},
		RewardFact: RewardFactView{
			Status:                        rewardStatus,
			BenefitID:                     strings.TrimSpace(in.RewardBenefitID),
			CampaignAttached:              hasCampaign,
			PreservedAcrossPublishUnknown: preservedAcrossUnknown,
			Summary:                       rewardFactSummary(rewardStatus, hasCampaign, preservedAcrossUnknown),
		},
		MustNotCreateParticipation:      mustNotCreateParticipation,
		MustNotReserveInventory:         mustNotReserveInventory,
		RewardNeverImpliesOfficial:      true,
		EscalationBlockedCount:          escalationBlocked,
		BlockedWeakSignals:              blockedSignals,
		LateEventsIgnored:               lateIgnored,
		VerificationRequestRecordedOnly: verifyReqOnly,
	}
}

func publishFactSummary(st PublishFactStatus) string {
	switch st {
	case PublishFactNotStarted:
		return "尚未发起发布交接。"
	case PublishFactHandoffPrepared:
		return "素材或链接交接已就绪，尚未收到平台确认。"
	case PublishFactInProgress:
		return "发布交接进行中，尚未收到平台最终结果。"
	case PublishFactManualVerified:
		return "已记录人工核验事实（不等于平台官方回执确认）。"
	case PublishFactUnderReview:
		return "平台或门店核验处理中，请稍后查询最新结果。"
	case PublishFactUnknown:
		return "发布结果暂未确认，请先回查原任务状态，切勿盲目重试。"
	case PublishFactOfficialConfirmed:
		return "已收到验签通过的平台官方确认回执。"
	case PublishFactFailed:
		return "本次交接准备或提交失败，记录已结束。"
	case PublishFactCancelled:
		return "本次交接已取消，记录已结束。"
	case PublishFactRefused:
		return "当前通道未获准开放，未发起交接。"
	default:
		return "发布状态待确认。"
	}
}

func rewardFactSummary(st RewardFactStatus, hasCampaign, preserved bool) string {
	switch st {
	case RewardFactIssued:
		if preserved {
			return "已发放的优惠券继续有效，不受发布状态待确认影响；领券状态也不反推官方已发布。"
		}
		return "优惠券已发放，可凭本人有效会话查看与出示核销码。"
	case RewardFactRedeemed:
		if preserved {
			return "该权益已完成核销，历史权益事实保留不变。"
		}
		return "该权益已完成核销。"
	case RewardFactReserved:
		return "权益名额已预留，待核验或确认后发放。"
	case RewardFactPending:
		return "权益发放处理中，请稍后刷新查看。"
	case RewardFactExpired:
		return "该权益已过期。"
	case RewardFactRevoked:
		return "该权益已撤销。"
	case RewardFactNotEligible:
		return "本次不满足活动领奖条件。"
	default:
		if !hasCampaign {
			return "本次门店服务未关联营销活动，不涉及优惠券参与或库存预留。"
		}
		return "暂未产生优惠券发放记录。"
	}
}
