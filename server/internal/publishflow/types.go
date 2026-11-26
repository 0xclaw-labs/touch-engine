// Package publishflow implements the HUI-4660 customer publish next-step
// projection, confirmation/account guard, publish-vs-reward fact separation,
// and secure session recovery guards on top of existing touch-engine contracts
// (publishhandoff, participation, rewardops).
//
// Design boundaries:
//   - Pure, stateless, concurrency-safe domain logic (zero SQL, zero network,
//     zero second session token system, zero second dispatcher, zero second
//     reward ledger).
//   - Reuses existing touch-handoff/v2 plan kinds, states, and customer_resume
//     credential semantics.
package publishflow

import (
	"errors"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/publishhandoff"
)

// PlanKind mirrors the existing server-derived plan.kind vocabulary from
// publishhandoff without creating a second protocol version.
type PlanKind string

const (
	PlanManualOnly      PlanKind = publishhandoff.PlanManualOnly
	PlanOfficialHandoff PlanKind = publishhandoff.PlanOfficialHandoff
	PlanOAuthPublish    PlanKind = publishhandoff.PlanOAuthPublish
	PlanRefused         PlanKind = publishhandoff.PlanRefused
	PlanPendingExternal PlanKind = publishhandoff.PlanPendingExternal
)

// FlowState is the customer-visible publish flow state projected across all
// plan kinds (HUI-4660 AC1).
type FlowState string

const (
	StateReady           FlowState = "ready"
	StateInProgress      FlowState = "in_progress"
	StateRejected        FlowState = "rejected"
	StateCancelled       FlowState = "cancelled"
	StateAppNotInstalled FlowState = "app_not_installed"
	StateAuthDenied      FlowState = "auth_denied"
	StateMediaFailed     FlowState = "media_failed"
	StateUnderReview     FlowState = "under_review"
	StateUnknown         FlowState = "unknown"
	StateCompleted       FlowState = "completed"
)

// AllFlowStates returns every customer flow state in deterministic order.
func AllFlowStates() []FlowState {
	return []FlowState{
		StateReady,
		StateInProgress,
		StateRejected,
		StateCancelled,
		StateAppNotInstalled,
		StateAuthDenied,
		StateMediaFailed,
		StateUnderReview,
		StateUnknown,
		StateCompleted,
	}
}

// AllPlanKinds returns the four primary plan kinds plus pending_external.
func AllPlanKinds() []PlanKind {
	return []PlanKind{
		PlanManualOnly,
		PlanOfficialHandoff,
		PlanOAuthPublish,
		PlanRefused,
		PlanPendingExternal,
	}
}

// ActionKind enumerates typed customer UI actions (never executable JS).
type ActionKind string

const (
	ActionConfirmAndExport       ActionKind = "confirm_and_export"
	ActionConfirmAndOpenPlatform ActionKind = "confirm_and_open_platform"
	ActionAuthorizeAccount       ActionKind = "authorize_account"
	ActionConfirmAndSubmit       ActionKind = "confirm_and_submit"
	ActionReconfirmSelection     ActionKind = "reconfirm_selection"
	ActionQueryStatus            ActionKind = "query_status"
	ActionInstallOrSwitchManual  ActionKind = "install_or_switch_manual"
	ActionReauthorizeOrManual    ActionKind = "reauthorize_or_manual"
	ActionRetryMediaExport       ActionKind = "retry_media_export"
	ActionReturnToStore          ActionKind = "return_to_store"
	ActionViewResult             ActionKind = "view_result"
	ActionViewHistory            ActionKind = "view_history"
	ActionPreviewContent         ActionKind = "preview_content"
	ActionSwitchAsset            ActionKind = "switch_asset"
	ActionCopyCaption            ActionKind = "copy_caption"
	ActionDownloadMedia          ActionKind = "download_media"
	ActionCancel                 ActionKind = "cancel"
	ActionReturnGuide            ActionKind = "return_guide"
	ActionSelfReportForReview    ActionKind = "self_report_for_review"
)

// AccountSource describes where a target account identifier originated.
// Only server-verified platform bindings are trusted; free-text or
// self-reported account strings are NEVER trusted account ownership.
type AccountSource string

const (
	AccountSourceNone            AccountSource = "none"
	AccountSourceVerifiedOAuth   AccountSource = "verified_oauth"
	AccountSourcePlatformReceipt AccountSource = "platform_receipt"
	AccountSourceFreeText        AccountSource = "free_text"
	AccountSourceSelfReported    AccountSource = "self_reported"
)

// IsTrustedAccountSource reports whether src represents server-verified
// platform account ownership.
func IsTrustedAccountSource(src AccountSource) bool {
	return src == AccountSourceVerifiedOAuth || src == AccountSourcePlatformReceipt
}

// Sentinel errors enforced across projection, fact reconciliation, and recovery.
var (
	// ErrConfirmationStale is returned when selected content or trusted target
	// account changed after a prior confirmation, requiring fresh confirmation.
	ErrConfirmationStale = errors.New("publishflow: prior confirmation invalidated by content or account change")
	// ErrUntrustedAccountSource is returned when a caller attempts to treat a
	// free-text or self-reported account string as trusted ownership.
	ErrUntrustedAccountSource = errors.New("publishflow: free-text or self-reported account cannot be trusted")
	// ErrRecoveryIdentityForbidden is returned when recovery is attempted with
	// staff_verify, store QR/NFC code alone, device fingerprint, or self-reported
	// account.
	ErrRecoveryIdentityForbidden = errors.New("publishflow: forbidden identity source for customer recovery")
	// ErrRecoveryCredentialsLost is returned when all valid customer credentials
	// (visitor cookie and customer_resume) are absent; fail-closed.
	ErrRecoveryCredentialsLost = errors.New("publishflow: all customer recovery credentials lost")
	// ErrRecoveryCredentialExpired indicates an expired customer_resume token.
	ErrRecoveryCredentialExpired = errors.New("publishflow: customer_resume credential expired")
	// ErrRecoveryCredentialUsed indicates an already-consumed single-use token.
	ErrRecoveryCredentialUsed = errors.New("publishflow: customer_resume credential already used")
	// ErrRecoveryCredentialRevoked indicates a revoked customer_resume token.
	ErrRecoveryCredentialRevoked = errors.New("publishflow: customer_resume credential revoked")
	// ErrRecoverySessionEnded indicates the target session was already ended.
	ErrRecoverySessionEnded = errors.New("publishflow: target session has ended")
	// ErrRecoveryVisitorMismatch indicates a visitor cookie mismatch.
	ErrRecoveryVisitorMismatch = errors.New("publishflow: visitor cookie does not match original session")
	// ErrTerminalStateImmutable indicates a late event tried to overwrite a
	// terminal publish state.
	ErrTerminalStateImmutable = errors.New("publishflow: terminal publish state cannot be overwritten")
)

// FlowStateFromHandoff maps an existing publishhandoff.State plus plan kind
// and optional runtime condition (app_not_installed|auth_denied|media_failed|rejected)
// onto the customer-facing FlowState without introducing a second state machine.
func FlowStateFromHandoff(status publishhandoff.State, planKind PlanKind, condition string) FlowState {
	cond := strings.ToLower(strings.TrimSpace(condition))
	switch cond {
	case string(StateAppNotInstalled):
		return StateAppNotInstalled
	case string(StateAuthDenied):
		return StateAuthDenied
	case string(StateMediaFailed):
		return StateMediaFailed
	case string(StateRejected):
		return StateRejected
	}
	if planKind == PlanRefused || planKind == PlanPendingExternal {
		switch status {
		case publishhandoff.StateCanceled:
			return StateCancelled
		case publishhandoff.StatePlatformConfirmed:
			return StateCompleted
		default:
			return StateRejected
		}
	}
	switch status {
	case "", publishhandoff.StatePreviewed, publishhandoff.StateHandoffPreparing, publishhandoff.StateHandoffReady:
		return StateReady
	case publishhandoff.StateEditorOpened, publishhandoff.StateSubmitting:
		return StateInProgress
	case publishhandoff.StateAwaitingReview:
		return StateUnderReview
	case publishhandoff.StatePlatformConfirmed:
		return StateCompleted
	case publishhandoff.StateCanceled:
		return StateCancelled
	case publishhandoff.StateFailed:
		return StateMediaFailed
	case publishhandoff.StateUnknown:
		return StateUnknown
	default:
		return StateUnknown
	}
}
