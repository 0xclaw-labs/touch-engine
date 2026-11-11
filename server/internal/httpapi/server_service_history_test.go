package httpapi

import (
	"net/http"
	"testing"
)

// HUI-3562 S2:本店处理历史(台账投影,只读)。owner 可读本店;无会话 401;
// 他租户 404 掩码(storeForActions 统一口径)。
func TestStoreServiceHistoryScopes(t *testing.T) {
	af := newActionsHTTPFixture(t, true)

	codeNoSess, _, _ := af.f.do(t, "GET", "/api/v1/stores/"+af.stoA+"/service-history", "", af.tenA, "")
	if codeNoSess != http.StatusUnauthorized {
		t.Fatalf("no session: want 401, got %d", codeNoSess)
	}
	codeOwner, _, outOwner := af.f.do(t, "GET", "/api/v1/stores/"+af.stoA+"/service-history", af.sessOwner, af.tenA, "")
	if codeOwner != http.StatusOK {
		t.Fatalf("owner read: want 200, got %d", codeOwner)
	}
	items, ok := outOwner["items"].([]any)
	if !ok {
		t.Fatalf("owner read: items missing: %+v", outOwner)
	}
	_ = items
	codeB, _, _ := af.f.do(t, "GET", "/api/v1/stores/"+af.stoA+"/service-history", af.sessOwnerB, af.tenB, "")
	if codeB != http.StatusNotFound {
		t.Fatalf("cross-tenant: want 404, got %d", codeB)
	}
}
