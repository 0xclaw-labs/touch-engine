package httpapi

import (
	"net/http"
	"testing"
)

// HUI-2222: whoami 是 feature 开关下发 web 的单缝。features.service_desk 必须恒存在
// （web 靠布尔值区分 off 与缺省），off=false、on=true；其余既有键零回归。

func TestWhoamiCarriesFeaturesServiceDeskDefaultOff(t *testing.T) {
	f := newFixture(t, false)
	status, _, body := f.do(t, "GET", "/api/v1/whoami", "sess-owner-a", f.tenA, "")
	if status != http.StatusOK {
		t.Fatalf("whoami = %d %v", status, body)
	}
	feats, ok := body["features"].(map[string]any)
	if !ok {
		t.Fatalf("whoami missing features object: %v", body)
	}
	if feats["service_desk"] != false {
		t.Fatalf("service_desk = %v, want false by default", feats["service_desk"])
	}
	// 既有键不回归
	for _, key := range []string{"principal_ref", "email", "tenant_id", "role", "enabled"} {
		if _, ok := body[key]; !ok {
			t.Fatalf("whoami lost key %q: %v", key, body)
		}
	}
}

func TestWhoamiCarriesFeaturesServiceDeskOn(t *testing.T) {
	f := newFixtureEnv(t, false, map[string]string{"FEATURE_SERVICE_DESK": "on"})
	status, _, body := f.do(t, "GET", "/api/v1/whoami", "sess-staff-a", f.tenA, "")
	if status != http.StatusOK {
		t.Fatalf("whoami = %d %v", status, body)
	}
	feats, ok := body["features"].(map[string]any)
	if !ok {
		t.Fatalf("whoami missing features object: %v", body)
	}
	if feats["service_desk"] != true {
		t.Fatalf("service_desk = %v, want true when FEATURE_SERVICE_DESK=on", feats["service_desk"])
	}
	if role, _ := body["role"].(string); role != "staff" {
		t.Fatalf("role = %v, want staff fixture", body["role"])
	}
}
