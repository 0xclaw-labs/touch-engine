package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// HUI-2222 (wave2, M11): the redeem side of the two-phase app switch. The
// mint side keeps its session/shape/authority tests in
// server_public_visitor_test.go; this file pins the single-use credential
// discipline (3546 semantics) on the redemption boundary.

func mintJumpToken(t *testing.T, f *fixture, session, tenant string) string {
	t.Helper()
	status, _, out := f.do(t, "POST", "/api/v1/eco-nav/switch", session, tenant,
		`{"app_id":"goboost","launch_target_id":"ti-goboost-web"}`)
	if status != http.StatusOK {
		t.Fatalf("mint = %d %v", status, out)
	}
	token, _ := out["jump_token"].(string)
	if token == "" {
		t.Fatalf("mint returned no jump_token: %v", out)
	}
	return token
}

func redeemJump(t *testing.T, f *fixture, session, tenant, token string) (int, map[string]any) {
	t.Helper()
	status, _, out := f.do(t, "POST", "/api/v1/eco-nav/switch/redeem", session, tenant,
		`{"jump_token":"`+token+`"}`)
	return status, out
}

func TestJumpRedeemIsSingleUse(t *testing.T) {
	f := newFixture(t, false)
	token := mintJumpToken(t, f, "sess-owner-a", f.tenA)
	status, out := redeemJump(t, f, "sess-owner-a", f.tenA, token)
	if status != http.StatusOK {
		t.Fatalf("first redeem = %d %v", status, out)
	}
	status, out = redeemJump(t, f, "sess-owner-a", f.tenA, token)
	if status != http.StatusNotFound {
		t.Fatalf("second redeem = %d %v, want uniform 404", status, out)
	}
	if strings.Contains(JSONString(out), "launch_url") || strings.Contains(JSONString(out), "goboost.modelxing.com") {
		t.Fatalf("used-grant refusal must not leak the launch URL: %v", out)
	}
}

func TestJumpRedeemBindsPrincipalAndTenant(t *testing.T) {
	f := newFixture(t, false)
	token := mintJumpToken(t, f, "sess-owner-a", f.tenA)
	// 换 principal(同租户)与换租户(同为本人)出示一律同形 404,防凭据搬运。
	status, _ := redeemJump(t, f, "sess-staff-a", f.tenA, token)
	if status != http.StatusNotFound {
		t.Fatalf("cross-principal redeem = %d, want 404", status)
	}
	status, _ = redeemJump(t, f, "sess-owner-a", f.tenB, token)
	if status != http.StatusForbidden {
		// sess-owner-a 在 tenB 无成员关系:requireSession 先答 403 not_member,
		// 同样进不了凭据兑换面。两种门都算拒绝,这里钉住具体形态。
		t.Fatalf("cross-tenant redeem = %d, want 403 (not a member of tenB)", status)
	}
	// 被拒绝的出示不烧凭据:本人随后仍可兑换。
	status, out := redeemJump(t, f, "sess-owner-a", f.tenA, token)
	if status != http.StatusOK {
		t.Fatalf("rejected presentment burned the grant: %d %v", status, out)
	}
}

func TestJumpRedeemExpiredAnswersGone(t *testing.T) {
	f := newFixture(t, false)
	token := mintJumpToken(t, f, "sess-owner-a", f.tenA)
	// 快进 10 分钟:超过 60s TTL(凭据过期是诚实信号,同 3546 的 410)。
	f.s.Jumps.Now = func() time.Time { return time.Now().Add(10 * time.Minute) }
	status, out := redeemJump(t, f, "sess-owner-a", f.tenA, token)
	if status != http.StatusGone {
		t.Fatalf("expired redeem = %d %v, want 410", status, out)
	}
}

func TestJumpRedeemMalformedShapeIsUniformNotFound(t *testing.T) {
	f := newFixture(t, false)
	for _, token := range []string{"garbage", "jtg_short.aa", "ptx_wrong_prefix.aa"} {
		status, _ := redeemJump(t, f, "sess-owner-a", f.tenA, token)
		if status != http.StatusNotFound {
			t.Fatalf("malformed redeem(%q) = %d, want 404", token, status)
		}
	}
}

func TestJumpRedeemRejectsUnknownFields(t *testing.T) {
	f := newFixture(t, false)
	status, _, out := f.do(t, "POST", "/api/v1/eco-nav/switch/redeem", "sess-owner-a", f.tenA,
		`{"jump_token":"jtg_x.aa","launch_target_id":"ti-goboost-web"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("unknown-field redeem = %d %v, want 400", status, out)
	}
}

func TestJumpRedeemRequiresSession(t *testing.T) {
	f := newFixture(t, false)
	token := mintJumpToken(t, f, "sess-owner-a", f.tenA)
	status, _, _ := f.do(t, "POST", "/api/v1/eco-nav/switch/redeem", "", f.tenA,
		`{"jump_token":"`+token+`"}`)
	if status != http.StatusUnauthorized && status != http.StatusForbidden {
		t.Fatalf("anonymous redeem = %d, want 401/403", status)
	}
}

// JSONString is a tiny helper keeping leak assertions readable.
func JSONString(m map[string]any) string {
	raw, _ := json.Marshal(m)
	return string(raw)
}
