package publishflow

import (
	"crypto/subtle"
	"fmt"
	"strings"
	"time"
)

// RecoveryTrigger identifies how the customer returned to the flow.
type RecoveryTrigger string

const (
	TriggerRefresh            RecoveryTrigger = "refresh"
	TriggerPageClose          RecoveryTrigger = "page_close"
	TriggerReTap              RecoveryTrigger = "re_tap"
	TriggerNoAutoReturn       RecoveryTrigger = "no_auto_return"
	TriggerCrossBrowserResume RecoveryTrigger = "cross_browser_resume"
	TriggerTouchpointRebind   RecoveryTrigger = "touchpoint_rebind"
)

// RecoveryCredentialKind enumerates credential types presented during recovery.
// Only visitor_cookie and customer_resume are legal customer recovery secrets.
type RecoveryCredentialKind string

const (
	CredVisitorCookie       RecoveryCredentialKind = "visitor_cookie"
	CredCustomerResume      RecoveryCredentialKind = "customer_resume"
	CredStaffVerify         RecoveryCredentialKind = "staff_verify"
	CredStaffVerifyDisplay  RecoveryCredentialKind = "staff_verify_display"
	CredStoreCode           RecoveryCredentialKind = "store_qr_nfc_code"
	CredDeviceFingerprint   RecoveryCredentialKind = "device_fingerprint"
	CredSelfReportedAccount RecoveryCredentialKind = "self_reported_account"
	CredNone                RecoveryCredentialKind = "none"
)

// TouchpointAttribution identifies the store/campaign/action a touchpoint maps to.
type TouchpointAttribution struct {
	TouchpointID string `json:"touchpoint_id,omitempty"`
	StoreID      string `json:"store_id"`
	CampaignID   string `json:"campaign_id,omitempty"`
	ActionID     string `json:"action_id"`
}

// OriginalSessionSnapshot captures the immutable original session/attempt
// attribution frozen when the customer started their flow.
type OriginalSessionSnapshot struct {
	SessionID          string
	VisitorID          string
	Attribution        TouchpointAttribution
	AdoptedContentVer  string
	AttemptID          string
	HandoffID          string
	ParticipationID    string
	SessionEnded       bool
	AttemptInFlight    bool
	PublishFactStatus  PublishFactStatus
	RewardFactStatus   RewardFactStatus
}

// RecoveryCredential represents the credential presented by the returning caller.
type RecoveryCredential struct {
	Kind      RecoveryCredentialKind
	VisitorID string // For CredVisitorCookie
	Token     string // For CredCustomerResume
	Purpose   string // Must be "customer_resume" for resume tokens; "staff_verify*" is strictly forbidden
	ExpiresAt time.Time
	Used      bool
	Revoked   bool
}

// RecoveryInput is the server-side input to ValidateCustomerRecovery.
type RecoveryInput struct {
	Trigger               RecoveryTrigger
	Now                   time.Time
	Original              OriginalSessionSnapshot
	CurrentTouchpoint     *TouchpointAttribution // Live binding of tapped QR/NFC tag (may have been rebound)
	Credential            RecoveryCredential
	NetworkOutcomeUnknown bool
	PlatformNoAutoReturn  bool
}

// RecoveryDecision is the validated recovery outcome enforcing attribution
// preservation, identity security, and query-before-retry idempotency.
type RecoveryDecision struct {
	Allowed                    bool                  `json:"allowed"`
	Trigger                    RecoveryTrigger       `json:"trigger"`
	RestoredSessionID          string                `json:"restored_session_id"`
	RestoredAttribution        TouchpointAttribution `json:"restored_attribution"`
	RestoredAdoptedContentVer  string                `json:"restored_adopted_content_version"`
	RestoredAttemptID          string                `json:"restored_attempt_id,omitempty"`
	RestoredHandoffID          string                `json:"restored_handoff_id,omitempty"`
	RestoredParticipationID    string                `json:"restored_participation_id,omitempty"`
	AttributionPreserved       bool                  `json:"attribution_preserved"`
	TouchpointReboundDetected  bool                  `json:"touchpoint_rebound_detected"`
	ConsumeSingleUseCredential bool                  `json:"consume_single_use_credential"`

	// Query-before-retry guards for network unknown / in-flight attempts.
	MustQueryExistingAttempt    bool       `json:"must_query_existing_attempt"`
	AllowBlindRetry             bool       `json:"allow_blind_retry"`
	AllowNewAttempt             bool       `json:"allow_new_attempt"`
	AllowDuplicateSubmission    bool       `json:"allow_duplicate_submission"`
	AllowDuplicateParticipation bool       `json:"allow_duplicate_participation"`
	AllowDuplicateCouponIssue   bool       `json:"allow_duplicate_coupon_issue"`
	NextAction                  ActionKind `json:"next_action"`
	ReturnGuidance              string     `json:"return_guidance,omitempty"`
}

// ValidateCustomerRecovery enforces HUI-4660 AC3/AC4/AC5/AC6:
//  1. Strictly rejects staff_verify*, store QR/NFC code alone, device
//     fingerprint, or self-reported account (ErrRecoveryIdentityForbidden).
//  2. Fails closed when all legitimate customer credentials are lost
//     (ErrRecoveryCredentialsLost).
//  3. Enforces customer_resume single-use, expiry, revocation, and purpose
//     semantics across browsers.
//  4. Restores original session, adopted content version, and in-flight
//     attempt across refresh, page close, re-tap, no-auto-return, and
//     touchpoint rebind while preserving original attribution.
//  5. On network unknown / in-flight attempt, forces query-before-retry so
//     retries NEVER duplicate publish submissions, participations, or coupons.
func ValidateCustomerRecovery(in RecoveryInput) (RecoveryDecision, error) {
	// 1) Structural identity boundary: reject forbidden credential kinds or
	// staff_verify purposes outright, regardless of trigger.
	if isForbiddenRecoveryCredential(in.Credential) {
		return RecoveryDecision{}, fmt.Errorf("%w: credential kind=%q purpose=%q cannot recover customer identity",
			ErrRecoveryIdentityForbidden, in.Credential.Kind, in.Credential.Purpose)
	}

	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	consumeSingleUse := false
	switch in.Credential.Kind {
	case CredNone, "":
		return RecoveryDecision{}, ErrRecoveryCredentialsLost

	case CredVisitorCookie:
		origVisitor := strings.TrimSpace(in.Original.VisitorID)
		gotVisitor := strings.TrimSpace(in.Credential.VisitorID)
		if origVisitor == "" || gotVisitor == "" {
			return RecoveryDecision{}, ErrRecoveryCredentialsLost
		}
		if subtle.ConstantTimeCompare([]byte(origVisitor), []byte(gotVisitor)) != 1 {
			return RecoveryDecision{}, ErrRecoveryVisitorMismatch
		}

	case CredCustomerResume:
		if strings.TrimSpace(in.Credential.Token) == "" {
			return RecoveryDecision{}, ErrRecoveryCredentialsLost
		}
		if strings.TrimSpace(in.Credential.Purpose) != "customer_resume" {
			return RecoveryDecision{}, fmt.Errorf("%w: resume token purpose must be customer_resume, got %q",
				ErrRecoveryIdentityForbidden, in.Credential.Purpose)
		}
		if in.Credential.ExpiresAt.IsZero() || !now.Before(in.Credential.ExpiresAt) {
			return RecoveryDecision{}, ErrRecoveryCredentialExpired
		}
		if in.Credential.Used {
			return RecoveryDecision{}, ErrRecoveryCredentialUsed
		}
		if in.Credential.Revoked {
			return RecoveryDecision{}, ErrRecoveryCredentialRevoked
		}
		if in.Original.SessionEnded {
			return RecoveryDecision{}, ErrRecoverySessionEnded
		}
		consumeSingleUse = true

	default:
		// Any unrecognized credential kind fails closed as forbidden.
		return RecoveryDecision{}, fmt.Errorf("%w: unrecognized credential kind %q", ErrRecoveryIdentityForbidden, in.Credential.Kind)
	}

	// 2) Detect touchpoint rebind, always preserving original frozen attribution.
	rebound := in.Trigger == TriggerTouchpointRebind
	if in.CurrentTouchpoint != nil {
		cur := *in.CurrentTouchpoint
		orig := in.Original.Attribution
		if (cur.StoreID != "" && cur.StoreID != orig.StoreID) ||
			(cur.CampaignID != "" && cur.CampaignID != orig.CampaignID) ||
			(cur.ActionID != "" && cur.ActionID != orig.ActionID) {
			rebound = true
		}
	}

	// 3) Network unknown / in-flight attempt guard: force query-before-retry.
	mustQuery := in.NetworkOutcomeUnknown ||
		in.Original.AttemptInFlight ||
		in.Original.PublishFactStatus == PublishFactUnknown ||
		in.Original.PublishFactStatus == PublishFactInProgress ||
		in.Original.PublishFactStatus == PublishFactUnderReview

	nextAction := ActionConfirmAndOpenPlatform
	if mustQuery {
		nextAction = ActionQueryStatus
	} else if in.Original.PublishFactStatus == PublishFactOfficialConfirmed {
		nextAction = ActionViewResult
	} else if in.Original.PublishFactStatus == PublishFactCancelled || in.Original.PublishFactStatus == PublishFactFailed {
		nextAction = ActionViewHistory
	}

	var returnGuide string
	if in.Trigger == TriggerNoAutoReturn || in.PlatformNoAutoReturn {
		returnGuide = "目标平台未自动回跳时，请从应用切换回原浏览器页面，或在新浏览器提交单次有效的恢复凭据继续原会话与原尝试。"
	} else if rebound {
		returnGuide = "门店触点已更新绑定，但系统已为你恢复原会话归属、原已选内容版本与原尝试记录。"
	}

	return RecoveryDecision{
		Allowed:                     true,
		Trigger:                     in.Trigger,
		RestoredSessionID:           in.Original.SessionID,
		RestoredAttribution:         in.Original.Attribution, // ALWAYS original frozen attribution
		RestoredAdoptedContentVer:   in.Original.AdoptedContentVer,
		RestoredAttemptID:           in.Original.AttemptID,
		RestoredHandoffID:           in.Original.HandoffID,
		RestoredParticipationID:     in.Original.ParticipationID,
		AttributionPreserved:        true,
		TouchpointReboundDetected:   rebound,
		ConsumeSingleUseCredential:  consumeSingleUse,
		MustQueryExistingAttempt:    mustQuery,
		AllowBlindRetry:             !mustQuery,
		AllowNewAttempt:             !mustQuery && !in.Original.SessionEnded,
		AllowDuplicateSubmission:    false,
		AllowDuplicateParticipation: false,
		AllowDuplicateCouponIssue:   false,
		NextAction:                  nextAction,
		ReturnGuidance:              returnGuide,
	}, nil
}

func isForbiddenRecoveryCredential(cred RecoveryCredential) bool {
	switch cred.Kind {
	case CredStaffVerify, CredStaffVerifyDisplay, CredStoreCode, CredDeviceFingerprint, CredSelfReportedAccount:
		return true
	}
	purpose := strings.ToLower(strings.TrimSpace(cred.Purpose))
	if purpose == "staff_verify" || purpose == "staff_verify_display" || strings.HasPrefix(purpose, "staff_") {
		return true
	}
	return false
}
