package httpapi

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestWorkspaceServicesDraftPublishIsolation(t *testing.T) {
	f, _ := newSfFixture(t, true, false)
	base := "/api/v1/stores/" + f.store1ID
	put := func(rev int, id, ssid, mode string) map[string]any {
		t.Helper()
		status, out := f.owner(t, "PUT", base+"/services", fmt.Sprintf(`{"expected_revision":%d,"publication_mode":%q,"items":[{"service_id":%q,"kind":"wifi","catalog_code":"A17","label":"门店网络","enabled":true,"order":1,"config":{"ssid":%q,"encryption":"open"}}]}`, rev, mode, id, ssid))
		if status != 200 {
			t.Fatalf("service save %d %v", status, out)
		}
		return out
	}
	old := put(0, "", "旧网络", "")
	id := old["items"].([]any)[0].(map[string]any)["service_id"].(string)
	code := old["store_code"].(string)
	publicPath := "/api/v1/public/storefronts/" + code + "/services"
	assertSSID := func(want string) {
		t.Helper()
		status, out := f.do(t, "GET", publicPath, "", "", "")
		raw, _ := json.Marshal(out)
		if status != 200 || !containsSSID(out, want) {
			t.Fatalf("public want %s got %d %s", want, status, raw)
		}
	}
	assertSSID("旧网络")
	put(1, id, "新网络", "draft")
	assertSSID("旧网络")
	status, draft := f.owner(t, "PUT", base+"/storefront-draft", `{"expected_revision":0,"sections":[]}`)
	if status != 200 {
		t.Fatalf("draft %d %v", status, draft)
	}
	status, preview := f.owner(t, "GET", base+"/storefront-preview", "")
	if status != 200 || !containsSSID(preview, "新网络") {
		t.Fatalf("preview %d %v", status, preview)
	}
	_, pre := f.owner(t, "POST", base+"/storefront/preflight", `{}`)
	status, pub := f.owner(t, "POST", base+"/storefront/publish", fmt.Sprintf(`{"confirm_changes":true,"candidate_revision":%v,"expected_published_revision":0,"summary_fingerprint":%q}`, draft["revision"], pre["summary_fingerprint"]))
	if status != 200 {
		t.Fatalf("publish %d %v; pre=%v draft=%v", status, pub, pre, draft)
	}
	assertSSID("新网络")
	put(2, id, "第三版网络", "draft")
	assertSSID("新网络")
	status, draft = f.owner(t, "PUT", base+"/storefront-draft", fmt.Sprintf(`{"expected_revision":%v,"sections":[]}`, draft["revision"]))
	if status != 200 {
		t.Fatalf("draft2 %d %v", status, draft)
	}
	_, pre = f.owner(t, "POST", base+"/storefront/preflight", `{}`)
	put(3, id, "并发网络", "draft")
	status, _ = f.owner(t, "POST", base+"/storefront/publish", fmt.Sprintf(`{"confirm_changes":true,"candidate_revision":%v,"expected_published_revision":1,"summary_fingerprint":%q}`, draft["revision"], pre["summary_fingerprint"]))
	if status != 409 {
		t.Fatalf("stale services published: %d", status)
	}
	assertSSID("新网络")
	status, _ = f.owner(t, "POST", base+"/status", `{"status":"disabled"}`)
	if status != 200 {
		t.Fatalf("disable %d", status)
	}
	for _, path := range []string{publicPath, "/api/v1/public/storefronts/" + code} {
		_, out := f.do(t, "GET", path, "", "", "")
		if containsSSID(out, "新网络") {
			t.Fatalf("disabled store exposed frozen service: %v", out)
		}
	}
}
func containsSSID(out map[string]any, want string) bool {
	items, _ := out["services"].([]any)
	for _, raw := range items {
		v, _ := raw.(map[string]any)
		wifi, _ := v["wifi"].(map[string]any)
		if wifi["ssid"] == want {
			return true
		}
	}
	return false
}

func TestWorkspaceNewStoreServicesNeedExplicitPublish(t *testing.T) {
	f, _ := newSfFixture(t, true, false)
	base := "/api/v1/stores/" + f.store1ID
	status, services := f.owner(t, "PUT", base+"/services", `{"expected_revision":0,"publication_mode":"draft","items":[{"kind":"wifi","catalog_code":"A17","label":"顾客网络","enabled":true,"order":1,"config":{"ssid":"新店顾客网","encryption":"open"}},{"kind":"wifi","catalog_code":"A17","label":"二楼网络","enabled":true,"order":2,"config":{"ssid":"新店二楼网","encryption":"open"}}]}`)
	if status != 200 {
		t.Fatalf("new services %d %v", status, services)
	}
	code := services["store_code"].(string)
	path := "/api/v1/public/storefronts/" + code + "/services"
	_, public := f.do(t, "GET", path, "", "", "")
	if containsSSID(public, "新店顾客网") || containsSSID(public, "新店二楼网") {
		t.Fatalf("unpublished config leaked: %v", public)
	}
	status, draft := f.owner(t, "PUT", base+"/storefront-draft", `{"expected_revision":0,"sections":[]}`)
	if status != 200 {
		t.Fatalf("new draft %d %v", status, draft)
	}
	_, pre := f.owner(t, "POST", base+"/storefront/preflight", `{}`)
	status, out := f.owner(t, "POST", base+"/storefront/publish", fmt.Sprintf(`{"confirm_changes":true,"candidate_revision":%v,"expected_published_revision":0,"summary_fingerprint":%q}`, draft["revision"], pre["summary_fingerprint"]))
	if status != 200 {
		t.Fatalf("new publish %d %v", status, out)
	}
	_, public = f.do(t, "GET", path, "", "", "")
	if !containsSSID(public, "新店顾客网") || !containsSSID(public, "新店二楼网") {
		t.Fatalf("multi-instance snapshot lost: %v", public)
	}
	status, _ = f.owner(t, "PUT", base+"/services", `{"expected_revision":0,"publication_mode":"draft","items":[]}`)
	if status != 409 {
		t.Fatalf("stale services accepted %d", status)
	}
	_, public = f.do(t, "GET", path, "", "", "")
	if !containsSSID(public, "新店顾客网") || !containsSSID(public, "新店二楼网") {
		t.Fatal("stale save changed published services")
	}
	status, _ = f.do(t, "PUT", base+"/services", "sess-owner-b", f.tenB, `{"expected_revision":1,"publication_mode":"draft","items":[]}`)
	if status != 404 {
		t.Fatalf("cross-tenant service write %d", status)
	}
}

func TestWorkspacePublishedActionTargetIsolationAndLiveVeto(t *testing.T) {
	f, d := newSfFixture(t, true, false)
	base := "/api/v1/stores/" + f.store1ID
	ids := f.seedActions(t)
	review := ids["A08"]
	save := func(expected int) map[string]any {
		t.Helper()
		status, out := f.owner(t, "PUT", base+"/storefront-draft", sfDraftBody(expected, review))
		if status != 200 {
			t.Fatalf("save %d %v", status, out)
		}
		return out
	}
	publish := func(rev, published int) {
		t.Helper()
		_, pre := f.owner(t, "POST", base+"/storefront/preflight", `{}`)
		status, out := f.owner(t, "POST", base+"/storefront/publish", fmt.Sprintf(`{"candidate_revision":%d,"expected_published_revision":%d,"confirm_changes":true,"summary_fingerprint":%q}`, rev, published, pre["summary_fingerprint"]))
		if status != 200 {
			t.Fatalf("publish %d %v", status, out)
		}
	}
	draft := save(0)
	code := draft["store_code"].(string)
	publish(1, 0)
	path := "/api/v1/public/storefronts/" + code + "/services"
	assertTarget := func(want string) {
		t.Helper()
		_, out := f.do(t, "GET", path, "", "", "")
		items, _ := out["services"].([]any)
		found := false
		for _, raw := range items {
			item := raw.(map[string]any)
			if item["service_id"] == review {
				found = true
				if item["target_url"] != want {
					t.Fatalf("target want %s got %v", want, item)
				}
			}
		}
		if !found {
			t.Fatalf("review missing %v", out)
		}
	}
	assertTarget("https://www.dianping.com/shop/1")
	status, actions := f.owner(t, "GET", base+"/actions", "")
	if status != 200 {
		t.Fatal(actions)
	}
	rows := actions["items"].([]any)
	for _, raw := range rows {
		item := raw.(map[string]any)
		if item["action_id"] == review {
			item["target_ref"] = "https://www.dianping.com/shop/2"
		}
	}
	body, _ := json.Marshal(map[string]any{"expected_revision": actions["config_revision"], "items": rows})
	status, out := f.owner(t, "PUT", base+"/actions", string(body))
	if status != 200 {
		t.Fatalf("edit target %d %v", status, out)
	}
	assertTarget("https://www.dianping.com/shop/1")
	draft = save(1)
	if draft["revision"] != float64(2) {
		t.Fatalf("target change did not create second draft %v", draft)
	}
	_, preview := f.owner(t, "GET", base+"/storefront-preview", "")
	visible := false
	for _, raw := range preview["services"].([]any) {
		item := raw.(map[string]any)
		if item["service_id"] == review && item["target_url"] == "https://www.dianping.com/shop/2" {
			visible = true
		}
	}
	if !visible {
		t.Fatalf("preview target did not match candidate %v", preview)
	}
	assertTarget("https://www.dianping.com/shop/1")
	publish(2, 1)
	assertTarget("https://www.dianping.com/shop/2")
	// Real-time revocation remains an independent authority, not a draft field.
	d.exec(t, `UPDATE store_actions SET revoked=1 WHERE id=?`, review)
	_, public := f.do(t, "GET", path, "", "", "")
	for _, raw := range public["services"].([]any) {
		if raw.(map[string]any)["service_id"] == review {
			t.Fatal("published snapshot revived revoked action")
		}
	}
}
