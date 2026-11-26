package publishflow

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/publishhandoff"
)

// TestProjectCustomerNextStep_FullMatrix verifies AC1 across all plan kinds
// (manual_only, official_handoff, oauth_publish, refused, pending_external) ×
// all 10 flow states (ready, in_progress, rejected, cancelled, app_not_installed,
// auth_denied, media_failed, under_review, unknown, completed):
//   - Exactly one non-empty PrimaryAction, never duplicated in SecondaryActions
//   - Honest behavioral copy: manual_only and official_handoff NEVER claim "发布完成"
//   - Customer JSON strips internal technical diagnostics (client_key, scope, OAuth, etc.)
//   - Restricted plans (refused/pending_external) never degrade to manual reward fallback
func TestProjectCustomerNextStep_FullMatrix(t *testing.T) {
	dirtyNotice := "douyin client_key=ck_live_999 scope=user_video_publish OAuth2 adapter photo_id=p123 sdk=douyin_h5 发布完成"
	dirtyReason := "oauth_server_push client_secret=sec_abc scope=video.create failed"
	dirtyParams := map[string]string{
		"client_key":    "ck_live_999",
		"client_secret": "sec_abc",
		"scope":         "user_video_publish",
		"oauth_state":   "st_123",
		"open_id":       "oid_999",
		"photo_id":      "ph_888",
		"share_id":      "sh_777",
		"sdk":           "douyin_h5",
		"caption_hint":  "周末烘焙节欢迎打卡",
	}

	for _, kind := range AllPlanKinds() {
		for _, state := range AllFlowStates() {
			t.Run(string(kind)+"/"+string(state), func(t *testing.T) {
				proj := ProjectCustomerNextStep(NextStepInput{
					PlanKind:        kind,
					State:           state,
					PlatformLabel:   "抖音",
					HasContent:      true,
					AssetCount:      3,
					OAuthAuthorized: false,
					HasCampaign:     true,
					NoAutoReturn:    true,
					ContentVersion:  "ver_1",
					RawReason:       dirtyReason,
					RawNotice:       dirtyNotice,
					RawParams:       dirtyParams,
				})

				// Exactly one primary action, marked Primary=true.
				if proj.PrimaryAction.Kind == "" || proj.PrimaryAction.Label == "" || !proj.PrimaryAction.Primary {
					t.Fatalf("missing valid primary action: %+v", proj.PrimaryAction)
				}
				if proj.Headline == "" || proj.Summary == "" {
					t.Fatalf("headline/summary must be non-empty: %+v", proj)
				}

				// Primary action must never be duplicated in secondary actions.
				seen := map[ActionKind]bool{proj.PrimaryAction.Kind: true}
				for _, sec := range proj.SecondaryActions {
					if sec.Primary {
						t.Fatalf("secondary action %q must not have Primary=true", sec.Kind)
					}
					if seen[sec.Kind] {
						t.Fatalf("duplicate action %q in projection", sec.Kind)
					}
					seen[sec.Kind] = true
				}

				// Serialize customer JSON and verify zero technical diagnostics leak.
				rawJSON, err := json.Marshal(proj)
				if err != nil {
					t.Fatalf("marshal projection: %v", err)
				}
				jsonStr := string(rawJSON)
				for _, forbidden := range []string{
					"client_key", "client_secret", "sec_abc", "ck_live_999",
					"scope", "OAuth", "open_id", "photo_id", "share_id",
					"douyin_h5", "oauth_server_push", "adapter",
				} {
					if strings.Contains(jsonStr, forbidden) {
						t.Fatalf("customer JSON leaked technical diagnostic %q: %s", forbidden, jsonStr)
					}
				}
				// Customer-visible prose fields must NEVER contain even lowercase "oauth" or any technical token.
				visibleCopy := []string{proj.Headline, proj.Summary, proj.PrimaryAction.Label, proj.PrimaryAction.Description, proj.ReturnGuidance}
				visibleCopy = append(visibleCopy, proj.Notices...)
				for _, sec := range proj.SecondaryActions {
					visibleCopy = append(visibleCopy, sec.Label, sec.Description)
				}
				for _, line := range visibleCopy {
					if ContainsTechnicalDiagnostic(line) {
						t.Fatalf("customer-visible text leaked technical diagnostic: %q", line)
					}
				}
				// CustomerParams keeps safe values, Diagnostics preserves raw for merchant/ops.
				if proj.CustomerParams["caption_hint"] != "周末烘焙节欢迎打卡" {
					t.Fatalf("safe customer param lost: %+v", proj.CustomerParams)
				}
				if proj.Diagnostics.RawParams["client_key"] != "ck_live_999" || proj.Diagnostics.RawReason != dirtyReason {
					t.Fatalf("merchant diagnostics must retain raw values: %+v", proj.Diagnostics)
				}

				// Honest copy invariant: manual_only and official_handoff NEVER claim "发布完成".
				if kind == PlanManualOnly || kind == PlanOfficialHandoff {
					if strings.Contains(jsonStr, "发布完成") {
						t.Fatalf("%s/%s must NEVER claim 发布完成: %s", kind, state, jsonStr)
					}
				}

				// Restricted plans never allow manual reward fallback or manual export actions.
				if kind == PlanRefused || kind == PlanPendingExternal || state == StateRejected {
					if proj.ManualRewardFallbackAllowed {
						t.Fatalf("restricted %s/%s must never allow manual reward fallback", kind, state)
					}
					if proj.PrimaryAction.Kind == ActionConfirmAndExport || proj.PrimaryAction.Kind == ActionConfirmAndOpenPlatform || proj.PrimaryAction.Kind == ActionConfirmAndSubmit {
						t.Fatalf("restricted %s/%s must not expose publish primary action %q", kind, state, proj.PrimaryAction.Kind)
					}
				}

				// Unknown / in-progress states must force query-before-retry.
				if proj.State == StateUnknown || proj.State == StateInProgress {
					if !proj.MustQueryBeforeRetry {
						t.Fatalf("state %s must set MustQueryBeforeRetry=true", proj.State)
					}
					if proj.PrimaryAction.Kind != ActionQueryStatus {
						t.Fatalf("state %s primary action = %q, want %q", proj.State, proj.PrimaryAction.Kind, ActionQueryStatus)
					}
				}

				// Active non-terminal states keep cancel reachable; preview stays reachable when content exists.
				isTerminal := proj.State == StateCompleted || proj.State == StateCancelled || proj.State == StateRejected
				if !isTerminal {
					if !hasAction(proj.SecondaryActions, ActionCancel) {
						t.Fatalf("active %s/%s must keep cancel reachable", kind, state)
					}
					if !hasAction(proj.SecondaryActions, ActionPreviewContent) {
						t.Fatalf("active %s/%s with content must keep preview reachable", kind, state)
					}
				}
			})
		}
	}
}

// TestProjectCustomerNextStep_OAuthTwoStepAndSingleAssetFriction verifies:
//   - oauth_publish ready splits into authorize -> confirm_submit honestly
//   - single-asset scenario omits redundant asset-picker friction
//   - multi-asset scenario keeps asset switching reachable
func TestProjectCustomerNextStep_OAuthTwoStepAndSingleAssetFriction(t *testing.T) {
	// Single asset: omit asset picker.
	single := ProjectCustomerNextStep(NextStepInput{
		PlanKind:        PlanOAuthPublish,
		State:           StateReady,
		PlatformLabel:   "快手",
		HasContent:      true,
		AssetCount:      1,
		OAuthAuthorized: false,
		ContentVersion:  "v1",
	})
	if !single.OmitAssetPicker {
		t.Fatal("single asset must set OmitAssetPicker=true")
	}
	if hasAction(single.SecondaryActions, ActionSwitchAsset) {
		t.Fatal("single asset must omit redundant switch_asset action")
	}
	if single.PrimaryAction.Kind != ActionAuthorizeAccount {
		t.Fatalf("unauthorized oauth_publish primary = %q, want %q", single.PrimaryAction.Kind, ActionAuthorizeAccount)
	}

	// Authorized oauth_publish with multi-asset: submit primary + switch reachable + trusted account shown.
	multi := ProjectCustomerNextStep(NextStepInput{
		PlanKind:         PlanOAuthPublish,
		State:            StateReady,
		PlatformLabel:    "快手",
		HasContent:       true,
		AssetCount:       2,
		OAuthAuthorized:  true,
		ContentVersion:   "v1",
		TargetAccountRef: "ks_verified_acc_1",
		AccountSource:    AccountSourceVerifiedOAuth,
	})
	if multi.OmitAssetPicker {
		t.Fatal("multi asset must keep asset picker available")
	}
	if !hasAction(multi.SecondaryActions, ActionSwitchAsset) {
		t.Fatal("multi asset must include switch_asset in secondary actions")
	}
	if multi.PrimaryAction.Kind != ActionConfirmAndSubmit {
		t.Fatalf("authorized oauth_publish primary = %q, want %q", multi.PrimaryAction.Kind, ActionConfirmAndSubmit)
	}
	if multi.TrustedAccountRef != "ks_verified_acc_1" {
		t.Fatalf("trusted account = %q, want ks_verified_acc_1", multi.TrustedAccountRef)
	}
	if multi.ShowFreeTextAccountInput || multi.FreeTextAccountTrusted {
		t.Fatal("free-text account input must never be shown or trusted")
	}
}

// TestConfirmationInvalidationAndUntrustedFreeTextAccount verifies AC4:
//   - Changing selected content invalidates prior confirmation (ErrConfirmationStale)
//   - Changing target account invalidates prior confirmation (ErrConfirmationStale)
//   - Free-text / self-reported account string is NEVER treated as trusted ownership
//   - Unchanged selection keeps confirmation valid without redundant re-prompt
func TestConfirmationInvalidationAndUntrustedFreeTextAccount(t *testing.T) {
	digestV1AccA := ConfirmationDigest(PlanOAuthPublish, "content_v1", "verified_acc_a")

	// Unchanged content + account = valid.
	if err := ValidateConfirmation(ConfirmationInput{
		PlanKind:             PlanOAuthPublish,
		ContentVersion:       "content_v1",
		TargetAccountRef:     "verified_acc_a",
		AccountSource:        AccountSourceVerifiedOAuth,
		PriorConfirmedDigest: digestV1AccA,
		PriorContentVersion:  "content_v1",
		PriorAccountRef:      "verified_acc_a",
	}); err != nil {
		t.Fatalf("unchanged selection should stay valid, got %v", err)
	}

	// Same selection projected with valid PriorConfirmedDigest avoids redundant re-confirmation.
	freshProj := ProjectCustomerNextStep(NextStepInput{
		PlanKind:             PlanOAuthPublish,
		State:                StateReady,
		PlatformLabel:        "快手",
		HasContent:           true,
		AssetCount:           1,
		OAuthAuthorized:      true,
		ContentVersion:       "content_v1",
		TargetAccountRef:     "verified_acc_a",
		AccountSource:        AccountSourceVerifiedOAuth,
		PriorConfirmedDigest: digestV1AccA,
	})
	if freshProj.ConfirmationStale || freshProj.NeedsConfirmation {
		t.Fatalf("unchanged valid confirmation should not force redundant confirmation: stale=%v needs=%v", freshProj.ConfirmationStale, freshProj.NeedsConfirmation)
	}

	// Changing content invalidates prior confirmation -> ErrConfirmationStale.
	if err := ValidateConfirmation(ConfirmationInput{
		PlanKind:             PlanOAuthPublish,
		ContentVersion:       "content_v2",
		TargetAccountRef:     "verified_acc_a",
		AccountSource:        AccountSourceVerifiedOAuth,
		PriorConfirmedDigest: digestV1AccA,
		PriorContentVersion:  "content_v1",
		PriorAccountRef:      "verified_acc_a",
	}); !errors.Is(err, ErrConfirmationStale) {
		t.Fatalf("content change must return ErrConfirmationStale, got %v", err)
	}

	// Changing target account invalidates prior confirmation -> ErrConfirmationStale.
	if err := ValidateConfirmation(ConfirmationInput{
		PlanKind:             PlanOAuthPublish,
		ContentVersion:       "content_v1",
		TargetAccountRef:     "verified_acc_b",
		AccountSource:        AccountSourceVerifiedOAuth,
		PriorConfirmedDigest: digestV1AccA,
		PriorContentVersion:  "content_v1",
		PriorAccountRef:      "verified_acc_a",
	}); !errors.Is(err, ErrConfirmationStale) {
		t.Fatalf("account change must return ErrConfirmationStale, got %v", err)
	}

	// Projection forces fresh reconfirmation when content changed.
	staleProj := ProjectCustomerNextStep(NextStepInput{
		PlanKind:             PlanOfficialHandoff,
		State:                StateReady,
		PlatformLabel:        "抖音",
		HasContent:           true,
		AssetCount:           2,
		ContentVersion:       "content_v2",
		PriorConfirmedDigest: ConfirmationDigest(PlanOfficialHandoff, "content_v1", ""),
		PriorContentVersion:  "content_v1",
	})
	if !staleProj.ConfirmationStale || !staleProj.NeedsConfirmation || staleProj.PrimaryAction.Kind != ActionReconfirmSelection {
		t.Fatalf("content change must invalidate confirmation and require reconfirm: %+v", staleProj)
	}

	// Free-text self-reported account is NEVER trusted.
	for _, tc := range []struct {
		name string
		in   ConfirmationInput
	}{
		{
			name: "self_reported_string",
			in: ConfirmationInput{
				PlanKind:            PlanOfficialHandoff,
				ContentVersion:      "content_v1",
				SelfReportedAccount: "@my_handle_123",
			},
		},
		{
			name: "free_text_source",
			in: ConfirmationInput{
				PlanKind:         PlanOAuthPublish,
				ContentVersion:   "content_v1",
				TargetAccountRef: "@my_handle_123",
				AccountSource:    AccountSourceFreeText,
			},
		},
		{
			name: "self_reported_source",
			in: ConfirmationInput{
				PlanKind:         PlanOAuthPublish,
				ContentVersion:   "content_v1",
				TargetAccountRef: "13800138000",
				AccountSource:    AccountSourceSelfReported,
			},
		},
		{
			name: "unverified_none_source_with_account",
			in: ConfirmationInput{
				PlanKind:         PlanOAuthPublish,
				ContentVersion:   "content_v1",
				TargetAccountRef: "raw_id",
				AccountSource:    AccountSourceNone,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateConfirmation(tc.in); !errors.Is(err, ErrUntrustedAccountSource) {
				t.Fatalf("want ErrUntrustedAccountSource, got %v", err)
			}
		})
	}

	// Projection strips untrusted free-text account from TrustedAccountRef.
	untrustedProj := ProjectCustomerNextStep(NextStepInput{
		PlanKind:            PlanOfficialHandoff,
		State:               StateReady,
		PlatformLabel:       "小红书",
		HasContent:          true,
		AssetCount:          1,
		ContentVersion:      "v1",
		TargetAccountRef:    "@fake_free_text",
		AccountSource:       AccountSourceFreeText,
		SelfReportedAccount: "@fake_free_text",
	})
	if untrustedProj.TrustedAccountRef != "" || untrustedProj.FreeTextAccountTrusted || untrustedProj.ShowFreeTextAccountInput {
		t.Fatalf("free-text account must never be trusted in projection: %+v", untrustedProj)
	}
}

// TestReconcilePublishAndRewardFacts verifies AC2 & AC5:
//   - Publish unknown NEVER degrades an already-issued or redeemed coupon to unknown
//   - Reward issuance NEVER back-infers official publish success
//   - Weak signals (h5_return, window_blur, asset_download, system_share_resolve, self_report, etc.)
//     NEVER escalate to official_confirmed
//   - Staff confirmation moves to manual_verified, NEVER official_confirmed
//   - Late events NEVER overwrite terminal states (official_confirmed, cancelled, failed, refused)
//   - Campaign-less store handoff NEVER forces participation or reserves coupon inventory
func TestReconcilePublishAndRewardFacts(t *testing.T) {
	// 1) Publish unknown preserves already-issued coupon AND issued coupon never implies official publish.
	sep := ReconcilePublishAndRewardFacts(FactReconcileInput{
		CurrentPublishStatus: PublishFactInProgress,
		CurrentRewardStatus:  RewardFactIssued,
		RewardBenefitID:      "ben_001",
		HasCampaign:          true,
		CampaignID:           "cmp_001",
		Events: []IncomingPublishEvent{
			{Kind: SignalNetworkTimeout},
		},
	})
	if sep.PublishFact.Status != PublishFactUnknown || sep.PublishFact.OfficialConfirmed {
		t.Fatalf("publish fact should stay unknown and not official: %+v", sep.PublishFact)
	}
	if sep.RewardFact.Status != RewardFactIssued || !sep.RewardFact.PreservedAcrossPublishUnknown {
		t.Fatalf("issued coupon must NOT degrade when publish is unknown: %+v", sep.RewardFact)
	}
	if !sep.RewardNeverImpliesOfficial {
		t.Fatal("RewardNeverImpliesOfficial must be true")
	}

	// 2) Redeemed coupon + manual verification still does NOT imply official publish.
	manualWithCoupon := ReconcilePublishAndRewardFacts(FactReconcileInput{
		CurrentPublishStatus: PublishFactHandoffPrepared,
		CurrentRewardStatus:  RewardFactRedeemed,
		RewardBenefitID:      "ben_002",
		HasCampaign:          true,
		CampaignID:           "cmp_001",
		Events: []IncomingPublishEvent{
			{Kind: SignalStaffConfirm},
		},
	})
	if manualWithCoupon.PublishFact.Status != PublishFactManualVerified || manualWithCoupon.PublishFact.OfficialConfirmed || !manualWithCoupon.PublishFact.ManualVerified {
		t.Fatalf("staff confirm must stay manual_verified and never official: %+v", manualWithCoupon.PublishFact)
	}
	if manualWithCoupon.RewardFact.Status != RewardFactRedeemed {
		t.Fatalf("redeemed coupon must stay redeemed: %+v", manualWithCoupon.RewardFact)
	}

	// 3) Every weak signal is blocked from escalating to official published status.
	weakEvents := []IncomingPublishEvent{
		{Kind: SignalH5Return},
		{Kind: SignalWindowBlur},
		{Kind: SignalAssetDownload},
		{Kind: SignalSystemShareResolve},
		{Kind: SignalSelfReport},
		{Kind: SignalCustomerClick},
		{Kind: SignalWakeAttempt},
		{Kind: SignalMediaHandoff},
		// Unverified or mismatched official receipt must also be blocked.
		{Kind: SignalOfficialReceipt, Verified: false, BindingMatched: true, ReceiptOutcome: "published"},
		{Kind: SignalOfficialReceipt, Verified: true, BindingMatched: false, ReceiptOutcome: "published"},
	}
	nonEsc := ReconcilePublishAndRewardFacts(FactReconcileInput{
		CurrentPublishStatus: PublishFactHandoffPrepared,
		CurrentRewardStatus:  RewardFactPending,
		HasCampaign:          true,
		CampaignID:           "cmp_001",
		Events:               weakEvents,
	})
	if nonEsc.PublishFact.Status != PublishFactHandoffPrepared || nonEsc.PublishFact.OfficialConfirmed {
		t.Fatalf("weak/unverified events must not escalate publish status: %+v", nonEsc.PublishFact)
	}
	if nonEsc.EscalationBlockedCount != len(weakEvents) {
		t.Fatalf("blocked count = %d, want %d", nonEsc.EscalationBlockedCount, len(weakEvents))
	}
	if !nonEsc.VerificationRequestRecordedOnly {
		t.Fatal("self_report must be recorded as verification request only")
	}

	// 4) Verified + bound non-published official receipt records under_review, not official_confirmed.
	reviewRec := ReconcilePublishAndRewardFacts(FactReconcileInput{
		CurrentPublishStatus: PublishFactInProgress,
		Events: []IncomingPublishEvent{
			{Kind: SignalOfficialReceipt, Verified: true, BindingMatched: true, ReceiptOutcome: "other"},
		},
	})
	if reviewRec.PublishFact.Status != PublishFactUnderReview || reviewRec.PublishFact.OfficialConfirmed {
		t.Fatalf("non-published receipt must record under_review, not official_confirmed: %+v", reviewRec.PublishFact)
	}

	// 5) Late events NEVER overwrite any terminal state.
	for _, term := range []PublishFactStatus{
		PublishFactOfficialConfirmed,
		PublishFactCancelled,
		PublishFactFailed,
		PublishFactRefused,
	} {
		t.Run("terminal_"+string(term), func(t *testing.T) {
			res := ReconcilePublishAndRewardFacts(FactReconcileInput{
				CurrentPublishStatus: term,
				Events: []IncomingPublishEvent{
					{Kind: SignalOfficialReceipt, Verified: true, BindingMatched: true, ReceiptOutcome: "published"},
					{Kind: SignalStaffConfirm},
					{Kind: SignalNetworkTimeout},
					{Kind: SignalCancel},
					{Kind: SignalMediaFailure},
					{Kind: SignalSelfReport},
				},
			})
			if res.PublishFact.Status != term {
				t.Fatalf("terminal state %s was overwritten to %s", term, res.PublishFact.Status)
			}
			if res.LateEventsIgnored != 6 {
				t.Fatalf("LateEventsIgnored = %d, want 6", res.LateEventsIgnored)
			}
		})
	}

	// 6) Campaign-less store handoff NEVER forces participation or reserves coupon inventory.
	noCamp := ReconcilePublishAndRewardFacts(FactReconcileInput{
		CurrentPublishStatus: PublishFactHandoffPrepared,
		CurrentRewardStatus:  RewardFactReserved, // Even if caller mistakenly passed reserved
		HasCampaign:          false,
		CampaignID:           "",
	})
	if !noCamp.MustNotCreateParticipation || !noCamp.MustNotReserveInventory {
		t.Fatalf("campaign-less handoff must forbid participation and inventory reservation: %+v", noCamp)
	}
	if noCamp.RewardFact.Status != RewardFactNone || noCamp.RewardFact.CampaignAttached {
		t.Fatalf("campaign-less handoff must project RewardFactNone: %+v", noCamp.RewardFact)
	}
}

// TestValidateCustomerRecovery verifies AC3, AC4, AC5, AC6:
//   - Restores original session, adopted content version, and in-flight attempt across
//     refresh, page_close, re_tap, no_auto_return, cross_browser_resume, touchpoint_rebind
//   - Preserves original attribution even when touchpoint was rebound to a different store/campaign/action
//   - Strictly rejects staff_verify*, store QR/NFC code alone, device fingerprint, self-reported account
//   - Fails closed when all credentials are lost
//   - Enforces single-use, expiry, revocation, and ended-session rules on customer_resume
//   - Forces query-before-retry on network unknown / in-flight attempts so retries never duplicate
func TestValidateCustomerRecovery(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	orig := OriginalSessionSnapshot{
		SessionID: "as_orig_1",
		VisitorID: "vis_cookie_secret_1",
		Attribution: TouchpointAttribution{
			TouchpointID: "tp_nfc_01",
			StoreID:      "sto_orig",
			CampaignID:   "cmp_orig",
			ActionID:     "sa_orig",
		},
		AdoptedContentVer: "cpv_adopted_v3",
		AttemptID:         "pta_orig_1",
		HandoffID:         "pha_orig_1",
		ParticipationID:   "pt_orig_1",
		AttemptInFlight:   true,
		PublishFactStatus: PublishFactUnknown,
	}

	// 1) All legitimate same-browser & no-auto-return triggers restore original session & attribution.
	for _, trig := range []RecoveryTrigger{
		TriggerRefresh,
		TriggerPageClose,
		TriggerReTap,
		TriggerNoAutoReturn,
		TriggerTouchpointRebind,
	} {
		t.Run("legit_"+string(trig), func(t *testing.T) {
			reboundTP := &TouchpointAttribution{
				TouchpointID: "tp_nfc_01",
				StoreID:      "sto_new_after_rebind",
				CampaignID:   "cmp_new_after_rebind",
				ActionID:     "sa_new_after_rebind",
			}
			var curTP *TouchpointAttribution
			if trig == TriggerTouchpointRebind {
				curTP = reboundTP
			}
			dec, err := ValidateCustomerRecovery(RecoveryInput{
				Trigger:               trig,
				Now:                   now,
				Original:              orig,
				CurrentTouchpoint:     curTP,
				Credential:            RecoveryCredential{Kind: CredVisitorCookie, VisitorID: "vis_cookie_secret_1"},
				NetworkOutcomeUnknown: true,
				PlatformNoAutoReturn:  trig == TriggerNoAutoReturn,
			})
			if err != nil {
				t.Fatalf("expected recovery success, got %v", err)
			}
			if !dec.Allowed || !dec.AttributionPreserved {
				t.Fatalf("recovery decision must preserve attribution: %+v", dec)
			}
			if dec.RestoredSessionID != orig.SessionID ||
				dec.RestoredAdoptedContentVer != orig.AdoptedContentVer ||
				dec.RestoredAttemptID != orig.AttemptID ||
				dec.RestoredHandoffID != orig.HandoffID {
				t.Fatalf("failed to restore exact original records: %+v", dec)
			}
			if dec.RestoredAttribution.StoreID != "sto_orig" ||
				dec.RestoredAttribution.CampaignID != "cmp_orig" ||
				dec.RestoredAttribution.ActionID != "sa_orig" {
				t.Fatalf("attribution leaked new touchpoint binding: %+v", dec.RestoredAttribution)
			}
			if trig == TriggerTouchpointRebind && !dec.TouchpointReboundDetected {
				t.Fatal("expected TouchpointReboundDetected=true")
			}
			if trig == TriggerNoAutoReturn && dec.ReturnGuidance == "" {
				t.Fatal("no_auto_return must include return guidance")
			}
			// Network unknown forces query-before-retry; never blind retry / duplicate submission / participation / coupon.
			if !dec.MustQueryExistingAttempt || dec.AllowBlindRetry || dec.AllowNewAttempt ||
				dec.AllowDuplicateSubmission || dec.AllowDuplicateParticipation || dec.AllowDuplicateCouponIssue ||
				dec.NextAction != ActionQueryStatus {
				t.Fatalf("network unknown must force query-before-retry without duplication: %+v", dec)
			}
		})
	}

	// 2) Cross-browser customer_resume credential: valid single-use succeeds; expired/used/revoked/ended fail.
	validResume, err := ValidateCustomerRecovery(RecoveryInput{
		Trigger:  TriggerCrossBrowserResume,
		Now:      now,
		Original: orig,
		Credential: RecoveryCredential{
			Kind:      CredCustomerResume,
			Token:     "ptx_valid_token",
			Purpose:   "customer_resume",
			ExpiresAt: now.Add(10 * time.Minute),
		},
	})
	if err != nil || !validResume.Allowed || !validResume.ConsumeSingleUseCredential {
		t.Fatalf("valid customer_resume must succeed and mark single-use consume: dec=%+v err=%v", validResume, err)
	}

	resumeNegatives := []struct {
		name string
		orig OriginalSessionSnapshot
		cred RecoveryCredential
		want error
	}{
		{
			name: "expired",
			orig: orig,
			cred: RecoveryCredential{Kind: CredCustomerResume, Token: "ptx_exp", Purpose: "customer_resume", ExpiresAt: now.Add(-1 * time.Second)},
			want: ErrRecoveryCredentialExpired,
		},
		{
			name: "already_used",
			orig: orig,
			cred: RecoveryCredential{Kind: CredCustomerResume, Token: "ptx_used", Purpose: "customer_resume", ExpiresAt: now.Add(10 * time.Minute), Used: true},
			want: ErrRecoveryCredentialUsed,
		},
		{
			name: "revoked",
			orig: orig,
			cred: RecoveryCredential{Kind: CredCustomerResume, Token: "ptx_rev", Purpose: "customer_resume", ExpiresAt: now.Add(10 * time.Minute), Revoked: true},
			want: ErrRecoveryCredentialRevoked,
		},
		{
			name: "ended_session",
			orig: func() OriginalSessionSnapshot { o := orig; o.SessionEnded = true; return o }(),
			cred: RecoveryCredential{Kind: CredCustomerResume, Token: "ptx_end", Purpose: "customer_resume", ExpiresAt: now.Add(10 * time.Minute)},
			want: ErrRecoverySessionEnded,
		},
		{
			name: "wrong_visitor_cookie",
			orig: orig,
			cred: RecoveryCredential{Kind: CredVisitorCookie, VisitorID: "vis_attacker"},
			want: ErrRecoveryVisitorMismatch,
		},
		{
			name: "all_credentials_lost",
			orig: orig,
			cred: RecoveryCredential{Kind: CredNone},
			want: ErrRecoveryCredentialsLost,
		},
	}
	for _, tc := range resumeNegatives {
		t.Run("resume_neg_"+tc.name, func(t *testing.T) {
			if _, err := ValidateCustomerRecovery(RecoveryInput{
				Trigger:    TriggerCrossBrowserResume,
				Now:        now,
				Original:   tc.orig,
				Credential: tc.cred,
			}); !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}

	// 3) Forbidden recovery identity sources MUST ALWAYS return ErrRecoveryIdentityForbidden (AC6).
	forbiddenCreds := []struct {
		name string
		cred RecoveryCredential
	}{
		{"staff_verify_kind", RecoveryCredential{Kind: CredStaffVerify, Token: "ptx_staff", Purpose: "staff_verify", ExpiresAt: now.Add(10 * time.Minute)}},
		{"staff_verify_display_kind", RecoveryCredential{Kind: CredStaffVerifyDisplay, Token: "disp_1", Purpose: "staff_verify_display", ExpiresAt: now.Add(10 * time.Minute)}},
		{"staff_verify_disguised_as_resume", RecoveryCredential{Kind: CredCustomerResume, Token: "ptx_staff", Purpose: "staff_verify", ExpiresAt: now.Add(10 * time.Minute)}},
		{"store_qr_nfc_code_alone", RecoveryCredential{Kind: CredStoreCode, Token: "NFC_STORE_001"}},
		{"device_fingerprint", RecoveryCredential{Kind: CredDeviceFingerprint, Token: "fp_canvas_ua_ip_123"}},
		{"self_reported_account", RecoveryCredential{Kind: CredSelfReportedAccount, Token: "@my_douyin_name"}},
	}
	for _, tc := range forbiddenCreds {
		t.Run("forbidden_"+tc.name, func(t *testing.T) {
			if _, err := ValidateCustomerRecovery(RecoveryInput{
				Trigger:    TriggerReTap,
				Now:        now,
				Original:   orig,
				Credential: tc.cred,
			}); !errors.Is(err, ErrRecoveryIdentityForbidden) {
				t.Fatalf("forbidden credential %s must return ErrRecoveryIdentityForbidden, got %v", tc.name, err)
			}
		})
	}
}

// TestFlowStateFromHandoff verifies clean bridging from publishhandoff.State.
func TestFlowStateFromHandoff(t *testing.T) {
	cases := []struct {
		status publishhandoff.State
		kind   PlanKind
		cond   string
		want   FlowState
	}{
		{publishhandoff.StateHandoffReady, PlanManualOnly, "", StateReady},
		{publishhandoff.StateSubmitting, PlanOAuthPublish, "", StateInProgress},
		{publishhandoff.StateAwaitingReview, PlanManualOnly, "", StateUnderReview},
		{publishhandoff.StatePlatformConfirmed, PlanOfficialHandoff, "", StateCompleted},
		{publishhandoff.StateCanceled, PlanOfficialHandoff, "", StateCancelled},
		{publishhandoff.StateFailed, PlanManualOnly, "", StateMediaFailed},
		{publishhandoff.StateUnknown, PlanOAuthPublish, "", StateUnknown},
		{publishhandoff.StateHandoffReady, PlanRefused, "", StateRejected},
		{publishhandoff.StateHandoffReady, PlanPendingExternal, "", StateRejected},
		{publishhandoff.StateHandoffReady, PlanOfficialHandoff, "app_not_installed", StateAppNotInstalled},
		{publishhandoff.StateHandoffReady, PlanOAuthPublish, "auth_denied", StateAuthDenied},
	}
	for _, tc := range cases {
		if got := FlowStateFromHandoff(tc.status, tc.kind, tc.cond); got != tc.want {
			t.Fatalf("FlowStateFromHandoff(%q,%q,%q) = %q, want %q", tc.status, tc.kind, tc.cond, got, tc.want)
		}
	}
}

// TestConcurrentRaceSafety exercises projection, fact reconciliation, and
// recovery guards concurrently under -race to confirm zero shared mutable state.
func TestConcurrentRaceSafety(t *testing.T) {
	now := time.Now().UTC()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			params := map[string]string{"client_key": "ck_race", "scope": "s", "label": "ok"}
			p := ProjectCustomerNextStep(NextStepInput{
				PlanKind:       AllPlanKinds()[idx%len(AllPlanKinds())],
				State:          AllFlowStates()[idx%len(AllFlowStates())],
				PlatformLabel:  "抖音",
				HasContent:     true,
				AssetCount:     (idx % 3) + 1,
				ContentVersion: "ver_race",
				RawParams:      params,
			})
			if p.PrimaryAction.Kind == "" {
				t.Errorf("empty primary action in goroutine %d", idx)
			}
			r := ReconcilePublishAndRewardFacts(FactReconcileInput{
				CurrentPublishStatus: PublishFactUnknown,
				CurrentRewardStatus:  RewardFactIssued,
				HasCampaign:          true,
				CampaignID:           "cmp_race",
				Events:               []IncomingPublishEvent{{Kind: SignalWindowBlur}, {Kind: SignalSelfReport}},
			})
			if r.RewardFact.Status != RewardFactIssued || r.PublishFact.OfficialConfirmed {
				t.Errorf("fact invariant broken in goroutine %d: %+v", idx, r)
			}
			_, err := ValidateCustomerRecovery(RecoveryInput{
				Trigger: TriggerRefresh,
				Now:     now,
				Original: OriginalSessionSnapshot{
					SessionID: "as_1", VisitorID: "vis_1", AdoptedContentVer: "v1",
					Attribution: TouchpointAttribution{StoreID: "s1", ActionID: "a1"},
				},
				Credential: RecoveryCredential{Kind: CredVisitorCookie, VisitorID: "vis_1"},
			})
			if err != nil {
				t.Errorf("recovery error in goroutine %d: %v", idx, err)
			}
		}(i)
	}
	wg.Wait()
}

func hasAction(actions []StepAction, kind ActionKind) bool {
	for _, a := range actions {
		if a.Kind == kind {
			return true
		}
	}
	return false
}

// TestMerchantMatrixReceiptNeverEscalatesCustomerPublishOrReward verifies HUI-2390:
// merchant matrix publish receipts are strictly separated from customer publish
// facts and can NEVER escalate customer publish status to official_confirmed or
// trigger/alter customer UGC rewards.
func TestMerchantMatrixReceiptNeverEscalatesCustomerPublishOrReward(t *testing.T) {
	if !IsWeakSignal(SignalMerchantMatrixReceipt) {
		t.Fatal("SignalMerchantMatrixReceipt must be treated as non-escalating weak signal on customer path")
	}
	got := ReconcilePublishAndRewardFacts(FactReconcileInput{
		CurrentPublishStatus: PublishFactHandoffPrepared,
		CurrentRewardStatus:  RewardFactNone,
		HasCampaign:          true,
		CampaignID:           "cmp_1",
		Events: []IncomingPublishEvent{
			{
				Kind:           SignalMerchantMatrixReceipt,
				Verified:       true,
				BindingMatched: true,
				ReceiptOutcome: "published",
			},
		},
	})
	if got.PublishFact.Status != PublishFactHandoffPrepared || got.PublishFact.OfficialConfirmed || got.PublishFact.ManualVerified {
		t.Fatalf("merchant matrix receipt escalated customer publish fact: %+v", got.PublishFact)
	}
	if got.RewardFact.Status != RewardFactNone {
		t.Fatalf("merchant matrix receipt altered customer reward fact: %+v", got.RewardFact)
	}
	if got.EscalationBlockedCount != 1 || len(got.BlockedWeakSignals) != 1 || got.BlockedWeakSignals[0] != SignalMerchantMatrixReceipt {
		t.Fatalf("expected merchant matrix receipt blocked, got count=%d signals=%v", got.EscalationBlockedCount, got.BlockedWeakSignals)
	}
}

// TestSanitizeStripsMerchantMatrixAndQuotaDiagnostics verifies that merchant
// matrix plan, account pool, scheduling credentials, quota, and payer/license
// terms are stripped from customer-visible copy and params.
func TestSanitizeStripsMerchantMatrixAndQuotaDiagnostics(t *testing.T) {
	raw := "已关联 matrix_plan=pln_1 account_pool=dy_pool merchant_quota=99 payer_id=pay_1 schedule_token=tok_x"
	cleaned := SanitizeCustomerText(raw, "可继续查看门店内容")
	if ContainsTechnicalDiagnostic(cleaned) {
		t.Fatalf("cleaned text still contains merchant/internal diagnostic: %q", cleaned)
	}
	safe, diag := SanitizeCustomerParams(map[string]string{
		"label":               "门店视频",
		"matrix_plan_id":      "pln_1",
		"publish_plan":        "pp_1",
		"account_pool":        "brand_acc",
		"merchant_account":    "dy_official",
		"schedule_credential": "cred_1",
		"merchant_quota":      "100",
		"payer_id":            "pay_1",
		"license_ref":         "lic_1",
		"grant_ref":           "grant_1",
	})
	if len(safe) != 1 || safe["label"] != "门店视频" {
		t.Fatalf("safe params leaked merchant keys: %+v", safe)
	}
	if len(diag) != 9 {
		t.Fatalf("expected 9 merchant diagnostic keys segregated, got %d: %+v", len(diag), diag)
	}
}
