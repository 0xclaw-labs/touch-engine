package httpapi

import (
	"fmt"
	"net/http"
	"testing"
)

func TestActionHandoffV2PlanRejectsPayloadMismatch(t *testing.T) {
	f := newHandoffV2Fixture(t, true)
	for _, tc := range []struct{ name, action, session, method, payload string }{
		{"link-cannot-carry-media", f.actA27, f.sidA, "link_share", "media"},
		{"media-cannot-be-link", f.actA07, f.sidB, "js_media_handoff", "link_share"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := fmt.Sprintf("/api/v1/public/actions/%s/handoffs/plan?method=%s&environment=ios_safari&payload_kind=%s&action_session_id=%s", tc.action, tc.method, tc.payload, tc.session)
			status, _, out := f.do(t, http.MethodGet, path, f.cookie, "")
			if status != http.StatusUnprocessableEntity || out["error"] != "payload_mismatch" || out["confirmation_required"] != nil {
				t.Fatalf("plan promised a payload that start rejects: %d %v", status, out)
			}
		})
	}
	var n int
	if err := f.d.QueryRow(`SELECT COUNT(*) FROM publish_handoffs`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("plan wrote a handoff: count=%d err=%v", n, err)
	}
}
