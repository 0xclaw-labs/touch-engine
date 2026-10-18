// Package mxopsview strictly reads the mx-ops-c0/v1 job_queue_view read model
// for the touch-engine production-handoff seam (HUI-3553 MX-OPS increment).
//
// Contract: mx-ops-c0/v1 @ 6ffc90ca (read-only consumption; no field invention).
// Rules:
//   - wrong contract_version/object_kind, missing required field or wrong JSON
//     type ⇒ the whole view is rejected (never partially defaulted);
//   - unknown stays null; a measured 0 stays 0;
//   - internal_debug is an internal-only field: accepted on input but ALWAYS
//     stripped from the public projection (whole key removed).
//
// The package does not decide scheduling; the authoritative queue belongs to the
// Runtime. This is a consumer-side projection for merchant-facing display only.
package mxopsview

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	ContractVersion = "mx-ops-c0/v1"
	ObjectKind      = "job_queue_view"
)

// ErrInvalid marks any non-consumable job_queue_view payload.
var ErrInvalid = errors.New("mxopsview: invalid job_queue_view")

var (
	queueKinds = map[string]bool{"fixed_card": true, "paid_elastic": true, "night_batch": true, "free_idle": true}
	phases     = map[string]bool{"admitted": true, "waiting": true, "dispatching": true, "running": true, "finishing": true}
	stages     = map[string]bool{"batch": true, "video": true, "generate": true, "enhance": true}
	waitReason = map[string]bool{
		"eligibility": true, "capacity": true, "budget": true, "authorization": true,
		"time_window": true, "borrowed_slot": true, "concurrency_limit": true,
		"material_unavailable": true, "unknown": true,
	}
	capacityStates = map[string]bool{"hot": true, "busy": true, "warming": true, "waiting_capacity": true, "unavailable": true}
)

// View is the strict parse result (internal_debug intentionally not public).
type View struct {
	ContractVersion    string          `json:"contract_version"`
	ObjectKind         string          `json:"object_kind"`
	JobRef             string          `json:"job_ref"`
	QueueRef           string          `json:"queue_ref"`
	AsOfUnix           int64           `json:"as_of_unix"`
	QueueKind          string          `json:"queue_kind"`
	Phase              string          `json:"phase"`
	Stage              string          `json:"stage"`
	WaitReason         *string         `json:"wait_reason"`
	Position           *int64          `json:"position"`
	JobsAhead          int64           `json:"jobs_ahead"`
	WaitingJobsAhead   *int64          `json:"waiting_jobs_ahead"`
	RunningJobs        *int64          `json:"running_jobs"`
	RunningJobsAhead   *int64          `json:"running_jobs_ahead"`
	EtaSecondsMin      *int64          `json:"eta_seconds_min"`
	EtaSecondsMax      *int64          `json:"eta_seconds_max"`
	NextEligibleAtUnix *int64          `json:"next_eligible_at_unix"`
	PolicySource       *string         `json:"policy_source"`
	PolicyVersion      *int64          `json:"policy_version"`
	CapacityState      *string         `json:"capacity_state"`
	Borrowed           *bool           `json:"borrowed"`
	InternalDebug      json.RawMessage `json:"internal_debug,omitempty"`
}

// publicView is the ONLY shape returned to clients: internal_debug is absent.
type publicView struct {
	ContractVersion    string  `json:"contract_version"`
	ObjectKind         string  `json:"object_kind"`
	JobRef             string  `json:"job_ref"`
	QueueRef           string  `json:"queue_ref"`
	AsOfUnix           int64   `json:"as_of_unix"`
	QueueKind          string  `json:"queue_kind"`
	Phase              string  `json:"phase"`
	Stage              string  `json:"stage"`
	WaitReason         *string `json:"wait_reason"`
	Position           *int64  `json:"position"`
	JobsAhead          int64   `json:"jobs_ahead"`
	WaitingJobsAhead   *int64  `json:"waiting_jobs_ahead"`
	RunningJobs        *int64  `json:"running_jobs"`
	RunningJobsAhead   *int64  `json:"running_jobs_ahead"`
	EtaSecondsMin      *int64  `json:"eta_seconds_min"`
	EtaSecondsMax      *int64  `json:"eta_seconds_max"`
	NextEligibleAtUnix *int64  `json:"next_eligible_at_unix"`
	PolicySource       *string `json:"policy_source"`
	PolicyVersion      *int64  `json:"policy_version"`
	CapacityState      *string `json:"capacity_state"`
	Borrowed           *bool   `json:"borrowed"`
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Parse strictly decodes a job_queue_view. Unknown additive fields are ignored
// (v1 consumers MUST ignore unknown fields), but every contract field that is
// present must type-check; required fields must be present.
func Parse(raw []byte) (View, error) {
	var v View
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&v); err != nil {
		return View{}, invalid("decode: %v", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return View{}, invalid("trailing data")
	}
	return validate(v)
}

func validate(v View) (View, error) {
	if v.ContractVersion != ContractVersion {
		return View{}, invalid("contract_version %q", v.ContractVersion)
	}
	if v.ObjectKind != ObjectKind {
		return View{}, invalid("object_kind %q", v.ObjectKind)
	}
	if v.JobRef == "" {
		return View{}, invalid("job_ref missing")
	}
	if v.QueueRef == "" {
		return View{}, invalid("queue_ref missing")
	}
	if v.AsOfUnix <= 0 {
		return View{}, invalid("as_of_unix missing")
	}
	if !queueKinds[v.QueueKind] {
		return View{}, invalid("queue_kind %q", v.QueueKind)
	}
	if !phases[v.Phase] {
		return View{}, invalid("phase %q", v.Phase)
	}
	if !stages[v.Stage] {
		return View{}, invalid("stage %q", v.Stage)
	}
	if v.WaitReason != nil && !waitReason[*v.WaitReason] {
		return View{}, invalid("wait_reason %q", *v.WaitReason)
	}
	if v.CapacityState != nil && !capacityStates[*v.CapacityState] {
		return View{}, invalid("capacity_state %q", *v.CapacityState)
	}
	for name, value := range map[string]*int64{
		"position":              v.Position,
		"waiting_jobs_ahead":    v.WaitingJobsAhead,
		"running_jobs":          v.RunningJobs,
		"running_jobs_ahead":    v.RunningJobsAhead,
		"eta_seconds_min":       v.EtaSecondsMin,
		"eta_seconds_max":       v.EtaSecondsMax,
		"next_eligible_at_unix": v.NextEligibleAtUnix,
		"policy_version":        v.PolicyVersion,
	} {
		if value != nil && *value < 0 {
			return View{}, invalid("%s negative", name)
		}
	}
	if v.JobsAhead < 0 {
		return View{}, invalid("jobs_ahead negative")
	}
	if v.EtaSecondsMin != nil && v.EtaSecondsMax != nil && *v.EtaSecondsMin > *v.EtaSecondsMax {
		return View{}, invalid("eta_seconds_min > eta_seconds_max")
	}
	return v, nil
}

// PublicJSON returns the client-safe projection: internal_debug is removed as a
// whole key; unknown stays null and measured 0 stays 0.
func (v View) PublicJSON() ([]byte, error) {
	return json.Marshal(publicView{
		ContractVersion: v.ContractVersion, ObjectKind: v.ObjectKind,
		JobRef: v.JobRef, QueueRef: v.QueueRef, AsOfUnix: v.AsOfUnix,
		QueueKind: v.QueueKind, Phase: v.Phase, Stage: v.Stage,
		WaitReason: v.WaitReason, Position: v.Position, JobsAhead: v.JobsAhead,
		WaitingJobsAhead: v.WaitingJobsAhead, RunningJobs: v.RunningJobs,
		RunningJobsAhead: v.RunningJobsAhead,
		EtaSecondsMin:    v.EtaSecondsMin, EtaSecondsMax: v.EtaSecondsMax,
		NextEligibleAtUnix: v.NextEligibleAtUnix,
		PolicySource:       v.PolicySource, PolicyVersion: v.PolicyVersion,
		CapacityState: v.CapacityState, Borrowed: v.Borrowed,
	})
}
