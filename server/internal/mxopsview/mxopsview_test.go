package mxopsview

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "job-queue-view.waiting.json"))
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return raw
}

func withPatch(t *testing.T, patch map[string]any) []byte {
	t.Helper()
	var base map[string]any
	if err := json.Unmarshal(fixture(t), &base); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	for k, v := range patch {
		base[k] = v
	}
	raw, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("encode patch: %v", err)
	}
	return raw
}

func TestParseAcceptsContractView(t *testing.T) {
	v, err := Parse(fixture(t))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if v.JobsAhead != 3 || v.WaitingJobsAhead == nil || *v.WaitingJobsAhead != 2 {
		t.Fatalf("jobs_ahead/waiting_jobs_ahead lost: %+v", v)
	}
	if v.EtaSecondsMin != nil || v.EtaSecondsMax != nil {
		t.Fatalf("unknown ETA must stay nil: %+v", v)
	}
	if v.WaitReason == nil || *v.WaitReason != "capacity" {
		t.Fatalf("wait_reason lost")
	}
}

func TestParseRejectsNonConsumablePayloads(t *testing.T) {
	cases := map[string][]byte{
		"wrong contract version": withPatch(t, map[string]any{"contract_version": "mx-ops-c0/v2"}),
		"wrong object kind":      withPatch(t, map[string]any{"object_kind": "execution_policy"}),
		"missing job_ref":        withPatch(t, map[string]any{"job_ref": ""}),
		"missing queue_ref":      withPatch(t, map[string]any{"queue_ref": ""}),
		"missing as_of":          withPatch(t, map[string]any{"as_of_unix": 0}),
		"bad phase":              withPatch(t, map[string]any{"phase": "paused"}),
		"bad queue_kind":         withPatch(t, map[string]any{"queue_kind": "priority"}),
		"eta inverted":           withPatch(t, map[string]any{"eta_seconds_min": 10, "eta_seconds_max": 5}),
		"negative jobs ahead":    withPatch(t, map[string]any{"jobs_ahead": -1}),
		"wrong type":             withPatch(t, map[string]any{"jobs_ahead": "three"}),
		"trailing data":          append(fixture(t), []byte("{}")...),
		"not json":               []byte("not-json"),
	}
	for name, raw := range cases {
		if _, err := Parse(raw); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: want ErrInvalid, got %v", name, err)
		}
	}
}

func TestPublicJSONStripsInternalDebugAndKeepsZero(t *testing.T) {
	raw := withPatch(t, map[string]any{
		"eta_seconds_min":    0,
		"eta_seconds_max":    0,
		"waiting_jobs_ahead": 0,
		"running_jobs":       0,
		"internal_debug":     map[string]any{"ready_workers": 4, "busy_workers": 1, "starting_workers": 0},
	})
	v, err := Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	public, err := v.PublicJSON()
	if err != nil {
		t.Fatalf("public json: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(public, &decoded); err != nil {
		t.Fatalf("decode public: %v", err)
	}
	if _, exists := decoded["internal_debug"]; exists {
		t.Fatalf("internal_debug must not appear in the public projection")
	}
	// measured 0 stays 0 (never collapsed to null/unknown).
	if decoded["eta_seconds_min"].(float64) != 0 || decoded["eta_seconds_max"].(float64) != 0 {
		t.Fatalf("measured 0 ETA lost: %+v", decoded)
	}
	if decoded["waiting_jobs_ahead"].(float64) != 0 {
		t.Fatalf("measured 0 waiting_jobs_ahead lost")
	}
	// unknown stays null.
	if decoded["eta_seconds_min"] == nil {
		t.Fatalf("unexpected")
	}
}
