// Package rewardops is the HUI-1671 B3 real-reward domain kernel.
//
// Three evidence classes exist. A campaign's rule snapshot (carried by the
// inventory batches bound to a benefit slot and rule version) declares which
// of them may satisfy the reward. A staff observation and a customer
// self-report never become an official confirmation; an official-only rule
// stays unsatisfied until a real server-side publish fact exists. A
// self-report can only ask for review.
//
// This package decides; the store layer executes against the SQLite ledger
// with database-level uniqueness (one reward per tenant+participation+slot).
// Coupon face value and actual cost are recorded separately and stay inside
// the touch marketing domain: no billing, no wallet, no cash.
package rewardops

import (
	"errors"
	"strings"
)

// Evidence classes (three, fixed).
const (
	EvidenceStaffObserved = "staff_observed_submission"
	EvidenceStaffVerified = "staff_verified_post"
	EvidenceOfficial      = "official_publish_confirmed"
)

// Reasons returned on verdicts.
const (
	ReasonMissingField             = "missing_field"
	ReasonUnknownEvidence          = "unknown_evidence"
	ReasonEvidenceNotAllowed       = "evidence_not_allowed"
	ReasonOfficialEvidenceRequired = "official_evidence_required"
	ReasonFabricatedReceipt        = "fabricated_official_receipt"
	ReasonSelfReportPending        = "self_report_pending_review"
	ReasonCampaignMismatch         = "campaign_mismatch"
	ReasonSlotUnknown              = "slot_unknown"
	ReasonVersionMismatch          = "rule_version_mismatch"
)

// ReceiptOfficialQuery is the only receipt source this system trusts for an
// official confirmation. It mirrors the HUI-1670 confirm path.
const ReceiptOfficialQuery = "official_query"

var (
	ErrMissingField       = errors.New(ReasonMissingField)
	ErrUnknownEvidence    = errors.New(ReasonUnknownEvidence)
	ErrEvidenceNotAllowed = errors.New(ReasonEvidenceNotAllowed)
)

// OfficialRef is the anchor of an official confirmation. The caller may only
// reference a server-side publish fact; typed receipts are never trusted.
type OfficialRef struct {
	FactID         string
	PlatformPostID string
	ReceiptSource  string
}

// OfficialRefForAttempt builds the trusted reference for a confirmed publish
// attempt loaded from the local store.
func OfficialRefForAttempt(attemptID, platformPostID string) OfficialRef {
	return OfficialRef{FactID: attemptID, PlatformPostID: platformPostID, ReceiptSource: ReceiptOfficialQuery}
}

// Snapshot is the rule binding G3 was told, stored on inventory batches: which
// evidence classes this campaign version accepts, and which benefit slots it
// promises. G3 never edits campaign rules; it records the version it was given.
type Snapshot struct {
	CampaignID      string
	Version         int
	AllowedEvidence []string
	Slots           []string
}

// Input is one staff confirmation choice. EventID is the caller's idempotency
// key for this confirmation event (replays are answered from the ledger,
// not re-executed).
type Input struct {
	TenantID        string
	CampaignID      string
	ParticipationID string
	Slot            string
	RuleVersion     int
	EventID         string
	Evidence        string
	Official        OfficialRef
	Actor           string
	StoreID         string
	Note            string
}

// Verdict is the adjudication result. At most one of Proceed / PendingReview
// is true; a refusal carries a machine Reason.
type Verdict struct {
	Proceed       bool
	PendingReview bool
	Reason        string
}

// Adjudicate decides whether in may fund snap. It checks shape, evidence class
// membership, and the official receipt anchor. Stock, dedup, and the one-
// benefit-per-slot constraint live in the ledger, not here.
func Adjudicate(snap Snapshot, in Input) Verdict {
	if missing(in.TenantID, in.CampaignID, in.ParticipationID, in.Slot, in.EventID, in.Actor, in.StoreID) || in.RuleVersion < 1 {
		return Verdict{Reason: ReasonMissingField}
	}
	if snap.CampaignID != strings.TrimSpace(in.CampaignID) {
		return Verdict{Reason: ReasonCampaignMismatch}
	}
	if !snapHasSlot(snap, in.Slot) {
		return Verdict{Reason: ReasonSlotUnknown}
	}
	if snap.Version != in.RuleVersion {
		return Verdict{Reason: ReasonVersionMismatch}
	}
	switch in.Evidence {
	case EvidenceStaffObserved, EvidenceStaffVerified:
		if !snapAllows(snap, in.Evidence) {
			if snapAllows(snap, EvidenceOfficial) {
				return Verdict{Reason: ReasonOfficialEvidenceRequired}
			}
			return Verdict{Reason: ReasonEvidenceNotAllowed}
		}
		return Verdict{Proceed: true}
	case EvidenceOfficial:
		if !snapAllows(snap, EvidenceOfficial) {
			return Verdict{Reason: ReasonEvidenceNotAllowed}
		}
		if !officialRefComplete(in.Official) {
			return Verdict{Reason: ReasonFabricatedReceipt}
		}
		return Verdict{Proceed: true}
	default:
		return Verdict{Reason: ReasonUnknownEvidence}
	}
}

// SelfReportVerdict is the only outcome a customer self-report can reach: a
// review request. It never proceeds and never becomes official.
func SelfReportVerdict() Verdict {
	return Verdict{PendingReview: true, Reason: ReasonSelfReportPending}
}

// ValidateSnapshot refuses a snapshot that could not adjudicate anything:
// empty campaign, no evidence classes, or no slots.
func ValidateSnapshot(snap Snapshot) error {
	if strings.TrimSpace(snap.CampaignID) == "" || snap.Version < 1 {
		return ErrMissingField
	}
	if len(snap.AllowedEvidence) == 0 {
		return ErrEvidenceNotAllowed
	}
	for _, ev := range snap.AllowedEvidence {
		switch ev {
		case EvidenceStaffObserved, EvidenceStaffVerified, EvidenceOfficial:
		default:
			return ErrUnknownEvidence
		}
	}
	if len(snap.Slots) == 0 {
		return ErrMissingField
	}
	for _, s := range snap.Slots {
		if strings.TrimSpace(s) == "" {
			return ErrMissingField
		}
	}
	return nil
}

func officialRefComplete(ref OfficialRef) bool {
	return strings.TrimSpace(ref.FactID) != "" &&
		strings.TrimSpace(ref.PlatformPostID) != "" &&
		ref.ReceiptSource == ReceiptOfficialQuery
}

func snapAllows(snap Snapshot, evidence string) bool {
	for _, ev := range snap.AllowedEvidence {
		if ev == evidence {
			return true
		}
	}
	return false
}

func snapHasSlot(snap Snapshot, slot string) bool {
	for _, s := range snap.Slots {
		if s == slot {
			return true
		}
	}
	return false
}

func missing(fields ...string) bool {
	for _, f := range fields {
		if strings.TrimSpace(f) == "" {
			return true
		}
	}
	return false
}
