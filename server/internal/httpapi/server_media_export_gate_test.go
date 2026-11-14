package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
	"github.com/bianjiefilm/touch-engine/server/internal/publishhandoff/adapters"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

func TestManualResponseProjectsCurrentDownloadFilesForBrowser(t *testing.T) {
	for _, tc := range []struct {
		name, code, platform string
		adapter              bool
	}{
		{"registered-wechat", "A04", "wechat", true},
		{"no-adapter", "A02", "xiaohongshu", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, action, sid, cookie, _, _ := liveExportFixtureFor(t, tc.code, tc.platform)
			if tc.adapter {
				f.s.PublishHandoff.SetAdapter(adapters.NewManualAdapter("wechat"))
			}
			opened := startExportHandoff(t, f, action, sid, cookie, "manual_material")
			hid := opened["handoff"].(map[string]any)["handoff_id"].(string)
			h, err := f.s.PublishHandoff.LoadMediaSource(hid)
			if err != nil {
				t.Fatal(err)
			}
			_, snapshot, err := f.s.PublishHandoff.SnapshotOf(context.Background(), hid, f.tenA)
			if err != nil {
				t.Fatal(err)
			}
			check := func(phase string, out map[string]any) {
				t.Helper()
				media := out["media"].([]any)
				if len(media) != len(snapshot) {
					t.Fatalf("%s media count=%d want%d", phase, len(media), len(snapshot))
				}
				for _, surface := range []string{"handoff", "plan"} {
					view := out[surface].(map[string]any)
					act, _ := view["client_action"].(map[string]any)
					params, _ := act["params"].(map[string]any)
					if params["media_order"] != "media_00,media_01" {
						t.Errorf("%s %s missing browser ordered files: %v", phase, surface, params)
					}
					if params["media_99"] != nil {
						t.Errorf("%s stale media parameter survived: %v", phase, params)
					}
					if act["caption"] == "" || len(act["steps"].([]any)) == 0 {
						t.Errorf("%s copy/steps lost: %v", phase, act)
					}
					for i, entry := range media {
						m := entry.(map[string]any)
						url := m["url"].(string)
						if m["purpose"] != "download" || m["media_id"] != snapshot[i].AssetID {
							t.Errorf("%s snapshot order/purpose changed: %v", phase, m)
						}
						key := fmt.Sprintf("media_%02d", i)
						if params[key] != snapshot[i].Kind+":"+url {
							t.Errorf("%s %s %s is not this response's download URL", phase, surface, key)
						}
						status, header, data := f.rawRequest(t, "GET", strings.TrimPrefix(url, "https://h5.example.com"), nil)
						if status != 200 || header.Get("Content-Type") != m["content_type"] || fmt.Sprintf("%x", sha256.Sum256(data)) != m["sha256"] {
							t.Errorf("%s browser file %d cannot fetch exact bytes: %d", phase, i, status)
						}
					}
				}
			}
			check("start", opened)
			if !tc.adapter {
				// Simulate old cached executable params in this isolated fixture;
				// renewal must discard them without modifying the stored snapshot.
				h.ClientAction.Params = map[string]string{"media_order": "media_99", "media_99": "video:https://expired.example/old-platform-fetch", "keep": "original"}
				raw, _ := json.Marshal(h.ClientAction)
				if _, err := f.s.St.DB.Exec(`UPDATE publish_handoffs SET client_action_json=? WHERE id=?`, string(raw), hid); err != nil {
					t.Fatal(err)
				}
			}
			status, refreshed := exportJSON(t, f, "GET", "/api/v1/public/handoffs/"+hid, cookie, "")
			if status != 200 {
				t.Fatalf("refresh=%d %v", status, refreshed)
			}
			check("refresh", refreshed)
			if !tc.adapter {
				stored, err := f.s.PublishHandoff.LoadMediaSource(hid)
				if err != nil {
					t.Fatal(err)
				}
				if stored.ClientAction.Params["media_99"] == "" || stored.ClientAction.Params["keep"] != "original" {
					t.Error("response projection rewrote persistent client action")
				}
				params := refreshed["handoff"].(map[string]any)["client_action"].(map[string]any)["params"].(map[string]any)
				if params["keep"] != "original" {
					t.Error("non-media parameter lost")
				}
			}
			fresh, err := f.s.PublishHandoff.LoadMediaSource(hid)
			if err != nil {
				t.Fatal(err)
			}
			if fresh.ContentVersion != h.ContentVersion || fresh.ContentPackVersionID != h.ContentPackVersionID {
				t.Error("response projection changed frozen content")
			}
			rec := f.s.resolveActionCapability(f.tenA, tc.code, tc.platform, "manual_material", channeladmission.ActionScenarioStorePromo)
			rec.Status = channeladmission.StatusNotAdmitted
			if _, _, err := f.s.St.UpsertActionCapability(rec, store.ControlAudit{Actor: "test", Reason: "manual response revocation"}); err != nil {
				t.Fatal(err)
			}
			status, denied := exportJSON(t, f, "GET", "/api/v1/public/handoffs/"+hid, cookie, "")
			if status != 200 || denied["execution"].(map[string]any)["allowed"] != false || len(denied["media"].([]any)) != 0 {
				t.Errorf("revoked response did not refuse: %d %v", status, denied)
			}
			for _, surface := range []string{"handoff", "plan"} {
				if denied[surface].(map[string]any)["client_action"] != nil {
					t.Errorf("revoked %s executable action survived", surface)
				}
			}
		})
	}
}

func merchantMediaGrant(t *testing.T, f *publicContentFixture, version, media, purpose string) (int, map[string]any) {
	t.Helper()
	v, err := f.s.St.GetStoreContentPackVersion(version, f.tenA)
	if err != nil {
		t.Fatal(err)
	}
	pack, err := f.s.St.GetStoreContentPack(v.PackID, f.tenA)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/stores/%s/content-packs/%s/versions/%s/media-access", f.ts.URL, pack.StoreID, pack.ID, version), strings.NewReader(fmt.Sprintf(`{"media_ids":[%q],"purpose":%q}`, media, purpose)))
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	req.Header.Set("X-Session-Token", "sess-owner-a")
	req.Header.Set("X-Tenant-ID", f.tenA)
	res, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func TestMediaExportExactVersionTenantAndActionRevocation(t *testing.T) {
	for _, veto := range []string{"action-disabled", "content-blocked"} {
		t.Run(veto, func(t *testing.T) {
			f, action, sid, cookie, version, video := liveExportFixture(t)
			opened := startExportHandoff(t, f, action, sid, cookie, "manual_material")
			url := strings.TrimPrefix(opened["media"].([]any)[0].(map[string]any)["url"].(string), "https://h5.example.com")
			// The signed source cannot be reassigned to a new version, even if the
			// new version uses the same actual files and belongs to the same store.
			_, other, _, _ := f.seedAdoptedContent(t, f.tenA, f.storeA, "", "另一版本", true)
			claim, err := contentpack.VerifyMediaAccess(f.s.Cfg.InternalToken, strings.TrimPrefix(url, "/api/v1/public/content/media/"), time.Now())
			if err != nil {
				t.Fatal(err)
			}
			claim.VersionID = other
			token, err := contentpack.MintMediaAccess(f.s.Cfg.InternalToken, claim)
			if err != nil {
				t.Fatal(err)
			}
			status, _, _ := f.rawRequest(t, "GET", "/api/v1/public/content/media/"+token, nil)
			if status != 404 {
				t.Errorf("different version/source accepted: %d", status)
			}
			// Separate tenant media rows may share a content hash/file, never authorization.
			packB, err := f.s.St.CreateStoreContentPack(f.tenB, f.storeB, "", "B素材", "seed")
			if err != nil {
				t.Fatal(err)
			}
			vA, err := f.s.St.GetStoreContentPackVersion(version, f.tenA)
			if err != nil {
				t.Fatal(err)
			}
			copyMedia := func(m store.StoreMediaSnapshot, kind upload.MediaKind) string {
				row, _, err := f.s.St.RegisterContentMedia(f.tenB, f.storeB, upload.MediaMeta{SHA256: m.SHA256, MIME: m.MimeType, Kind: kind, ByteSize: m.ByteSize, DurationMS: m.DurationMS, Width: m.Width, Height: m.Height, MediaRef: m.SHA256}, "fixture", "seed")
				if err != nil {
					t.Fatal(err)
				}
				return row.ID
			}
			vB, err := f.s.St.CreateStoreContentPackVersion(f.tenB, packB.ID, store.StoreVersionSpec{ContentKind: contentpack.ContentKindVideo, VideoMediaID: copyMedia(vA.Video, upload.KindVideo), CoverMediaID: copyMedia(vA.Cover, upload.KindImage), Title: "B内容", Body: "B正文", StoreName: "B店", PriceNote: "店内", ValidNote: "有效期内", GrantRefs: []string{"grant-b"}, Source: contentpack.SourceMerchantUpload}, contentpack.StatusCandidate, "seed")
			if err != nil {
				t.Fatal(err)
			}
			status, out := mediaPurposeRequest(t, f, vB.ID, sid, cookie, vB.Video.MediaID, "download")
			if status != 404 {
				t.Errorf("cross tenant issue=%d %v", status, out)
			}
			claim.VersionID, claim.MediaID = vB.ID, vB.Video.MediaID
			token, err = contentpack.MintMediaAccess(f.s.Cfg.InternalToken, claim)
			if err != nil {
				t.Fatal(err)
			}
			status, _, _ = f.rawRequest(t, "GET", "/api/v1/public/content/media/"+token, nil)
			if status != 404 {
				t.Errorf("cross tenant source redemption=%d", status)
			}
			want := 404
			if veto == "action-disabled" {
				_, rev, err := f.s.St.ListStoreActions(f.tenA, f.storeA)
				if err != nil {
					t.Fatal(err)
				}
				_, _, err = f.s.St.ReplaceStoreActions(f.tenA, f.storeA, rev, []store.StoreActionInput{{ID: action, CatalogCode: "A02", Label: "已停用", Enabled: false}})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				want = 409
				if _, err := f.s.St.BlockStoreContentPackVersion(f.tenA, vA.PackID, version, "撤回授权", "owner", time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			status, out = mediaPurposeRequest(t, f, version, sid, cookie, video, "download")
			if status != want {
				t.Errorf("%s issuance=%d want%d %v", veto, status, want, out)
			}
			status, _, _ = f.rawRequest(t, "GET", url, nil)
			if status != want {
				t.Errorf("%s cached redemption=%d want%d", veto, status, want)
			}
		})
	}
}

func TestAdapterStartMediaSourceUsesExactStoredHandoff(t *testing.T) {
	f, action, sid, cookie, version, _ := liveExportFixture(t)
	opened := startExportHandoff(t, f, action, sid, cookie, "manual_material")
	h, err := f.s.PublishHandoff.LoadMediaSource(opened["handoff"].(map[string]any)["handoff_id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	_, media, err := f.s.PublishHandoff.SnapshotOf(context.Background(), h.ID, h.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	// manual routes cannot borrow the adapter's platform_fetch pre-mint.
	refs := f.s.mintAdapterMediaRefs(f.tenA, version, sid, media, contentpack.MediaSourceActionStart, "export-manual_material")
	for _, url := range refs {
		status, _, _ := f.rawRequest(t, "GET", strings.TrimPrefix(url, "https://h5.example.com"), nil)
		if status != 422 {
			t.Errorf("manual source upgraded to platform fetch: %d", status)
		}
	}
}

func exportJSON(t *testing.T, f *publicContentFixture, method, path, cookie, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, f.ts.URL+path, strings.NewReader(body))
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookie)
	res, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

// All bytes enter through the existing merchant upload. A02's actual seeded
// manual route supplies admission; no permissive capability or business success is invented.
func liveExportFixture(t *testing.T) (*publicContentFixture, string, string, string, string, string) {
	return liveExportFixtureFor(t, "A02", "xiaohongshu")
}

func liveExportFixtureFor(t *testing.T, catalog, platform string) (*publicContentFixture, string, string, string, string, string) {
	t.Helper()
	f := newPublicContentFixture(t, true, "FEATURE_PARTICIPATION", "on", "FEATURE_PUBLISH_HANDOFF", "on", "PUBLIC_BASE_URL", "https://h5.example.com")
	_, acts, err := f.s.St.ReplaceStoreActions(f.tenA, f.storeA, 0, []store.StoreActionInput{{CatalogCode: catalog, Label: "合法视频手动交接", TargetRef: "https://example.com/publish", Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	pack, _, video, cover := f.seedAdoptedContent(t, f.tenA, f.storeA, "", "合法素材", false)
	v, err := f.s.St.CreateStoreContentPackVersion(f.tenA, pack, store.StoreVersionSpec{ContentKind: contentpack.ContentKindVideo, VideoMediaID: video, CoverMediaID: cover, Title: "门店视频", Body: "门店近况", StoreName: "公共一店", PriceNote: "以店内为准", ValidNote: "活动期内", PlatformVersions: []contentpack.PlatformVersion{{Platform: platform, Note: "竖版", SourceVersion: "source-v1"}}, GrantRefs: []string{"merchant-grant"}, Source: contentpack.SourceMerchantUpload}, contentpack.StatusCandidate, "usr_owner_a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.St.AdoptStoreContentPackVersion(f.tenA, pack, v.ID, platform, "usr_owner_a", time.Now()); err != nil {
		t.Fatal(err)
	}
	f.bindAction(t, acts[0].ID, pack)
	sid, cookie, status, out := f.createSession(t, acts[0].ID)
	if status != 201 {
		t.Fatalf("session=%d %v", status, out)
	}
	return f, acts[0].ID, sid, cookie, v.ID, video
}

func startExportHandoff(t *testing.T, f *publicContentFixture, action, sid, cookie, method string) map[string]any {
	t.Helper()
	status, out := exportJSON(t, f, "GET", "/api/v1/public/actions/"+action+"/handoffs/plan?action_session_id="+sid+"&method="+method+"&environment=ios_safari&payload_kind=media", cookie, "")
	if status != 200 {
		t.Fatalf("plan=%d %v", status, out)
	}
	confirmation, _ := out["confirmation_required"].(string)
	if confirmation == "" {
		t.Fatalf("plan did not admit route: %v", out)
	}
	body, _ := json.Marshal(map[string]any{"action_session_id": sid, "action_revision": 1, "payload_kind": "media", "method": method, "environment": "ios_safari", "confirmation": confirmation, "idempotency_key": "export-" + method})
	status, out = exportJSON(t, f, "POST", "/api/v1/public/actions/"+action+"/handoffs", cookie, string(body))
	if status != 201 {
		t.Fatalf("handoff=%d %v", status, out)
	}
	return out
}

func TestManualMediaExportRequiresOriginalHandoffAndLiveAdmission(t *testing.T) {
	f, action, sid, cookie, version, video := liveExportFixture(t)
	status, out := mediaPurposeRequest(t, f, version, sid, cookie, video, "download")
	if status != 422 {
		t.Fatalf("admitted session alone cannot export: %d %v", status, out)
	}
	opened := startExportHandoff(t, f, action, sid, cookie, "manual_material")
	url := opened["media"].([]any)[0].(map[string]any)["url"].(string)
	url = strings.TrimPrefix(url, "https://h5.example.com")
	status, header, data := f.rawRequest(t, "GET", url, nil)
	if status != 200 || len(data) == 0 || !strings.HasPrefix(header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("manual bytes=%d %v", status, header)
	}
	status, out = mediaPurposeRequest(t, f, version, sid, cookie, video, "download")
	if status != 200 {
		t.Fatalf("legal renewal=%d %v", status, out)
	}
	status, out = mediaPurposeRequest(t, f, version, sid, "touch_participation_visitor=ffffffffffffffffffffffffffffffff", video, "download")
	if status != 404 {
		t.Fatalf("foreign visitor=%d %v", status, out)
	}
	status, out = mediaPurposeRequest(t, f, version, sid, cookie, video, "platform_fetch")
	if status != 422 {
		t.Fatalf("manual handoff cannot upgrade purpose: %d %v", status, out)
	}
	// Revoke this exact method, retaining the original handoff and all media.
	rec := f.s.resolveActionCapability(f.tenA, "A02", "xiaohongshu", "manual_material", channeladmission.ActionScenarioStorePromo)
	rec.Status = channeladmission.StatusNotAdmitted
	if _, _, err := f.s.St.UpsertActionCapability(rec, store.ControlAudit{Actor: "test", Reason: "test revocation"}); err != nil {
		t.Fatal(err)
	}
	status, out = mediaPurposeRequest(t, f, version, sid, cookie, video, "download")
	if status != 422 {
		t.Fatalf("revoked issue=%d %v", status, out)
	}
	status, _, _ = f.rawRequest(t, "GET", url, nil)
	if status != 422 {
		t.Errorf("cached manual token must rejudge even without adapter: %d", status)
	}
	hid := opened["handoff"].(map[string]any)["handoff_id"].(string)
	status, out = exportJSON(t, f, "GET", "/api/v1/public/handoffs/"+hid, cookie, "")
	execution, _ := out["execution"].(map[string]any)
	if status != 200 || execution["allowed"] != false || execution["reason"] != "not_admitted" {
		t.Errorf("history needs an explicit current refusal: %d %v", status, out)
	}
	plan, _ := out["plan"].(map[string]any)
	history, _ := out["handoff"].(map[string]any)
	if plan["allowed"] != false || plan["client_action"] != nil || history["client_action"] != nil || len(out["media"].([]any)) != 0 {
		t.Errorf("refused history must not contain executable parameters: %v", out)
	}
	replay, _ := json.Marshal(map[string]any{"action_session_id": sid, "action_revision": 1, "payload_kind": "media", "method": "manual_material", "environment": "ios_safari", "confirmation": opened["handoff"].(map[string]any)["content_version"], "idempotency_key": "export-manual_material"})
	status, out = exportJSON(t, f, "POST", "/api/v1/public/actions/"+action+"/handoffs", cookie, string(replay))
	if status != 422 || out["reason"] != "not_admitted" {
		t.Errorf("old idempotency key must not restore execution: %d %v", status, out)
	}
}

func TestOfficialMediaFetchCannotBecomeGeneralDownload(t *testing.T) {
	f, action, sid, cookie, version, video := liveExportFixture(t)
	// An engineering capability fixture exercises an admitted official route;
	// it does not claim this deployment has real platform admission or receipts.
	rec := f.s.resolveActionCapability(f.tenA, "A02", "xiaohongshu", "js_media_handoff", channeladmission.ActionScenarioStorePromo)
	rec.ID = "cap_export_official_test"
	rec.TenantScope = f.tenA
	rec.Status = channeladmission.StatusAdmitted
	if _, _, err := f.s.St.UpsertActionCapability(rec, store.ControlAudit{Actor: "test", Reason: "official admitted contract fixture"}); err != nil {
		t.Fatal(err)
	}
	opened := startExportHandoff(t, f, action, sid, cookie, "js_media_handoff")
	url := strings.TrimPrefix(opened["media"].([]any)[0].(map[string]any)["url"].(string), "https://h5.example.com")
	status, _, bytes := f.rawRequest(t, "GET", url, nil)
	if status != 200 || len(bytes) == 0 {
		t.Fatalf("platform fetch without cookie=%d", status)
	}
	h, err := f.s.PublishHandoff.LoadMediaSource(opened["handoff"].(map[string]any)["handoff_id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	_, items, err := f.s.PublishHandoff.SnapshotOf(context.Background(), h.ID, h.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	refs := f.s.mintAdapterMediaRefs(f.tenA, version, sid, items, contentpack.MediaSourceActionStart, "export-js_media_handoff")
	for _, ref := range refs {
		code, _, data := f.rawRequest(t, "GET", strings.TrimPrefix(ref, "https://h5.example.com"), nil)
		if code != 200 || len(data) == 0 {
			t.Errorf("original adapter pre-mint source lost: %d", code)
		}
	}
	status, out := mediaPurposeRequest(t, f, version, sid, cookie, video, "download")
	if status != 422 {
		t.Fatalf("official-only route must not gain download: %d %v", status, out)
	}
	status, out = mediaPurposeRequest(t, f, version, sid, cookie, video, "platform_fetch")
	if status != 200 {
		t.Fatalf("official renewal=%d %v", status, out)
	}
}

func mediaPurposeRequest(t *testing.T, f *publicContentFixture, version, sid, cookie, media, purpose string) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"action_session_id": sid, "media_ids": []string{media}, "purpose": purpose})
	req, _ := http.NewRequest("POST", f.ts.URL+"/api/v1/public/content/"+version+"/media-access", strings.NewReader(string(body)))
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookie)
	res, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func TestPublicMediaExportCannotBypassNotAdmittedRoute(t *testing.T) {
	f := newPublicContentFixture(t, true, "FEATURE_PARTICIPATION", "on", "FEATURE_PUBLISH_HANDOFF", "on", "PUBLIC_BASE_URL", "https://h5.example.com")
	_, acts, err := f.s.St.ReplaceStoreActions(f.tenA, f.storeA, 0, []store.StoreActionInput{{CatalogCode: "A01", Label: "受限视频玩法", TargetRef: "https://example.com/publish", Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	pack, version, video, _ := f.seedAdoptedContent(t, f.tenA, f.storeA, "", "受限素材", true)
	f.bindAction(t, acts[0].ID, pack)
	sid, cookie, status, out := f.createSession(t, acts[0].ID)
	if status != http.StatusCreated {
		t.Fatalf("session=%d %v", status, out)
	}
	for _, scope := range []struct{ name, sid, cookie string }{{"anonymous", "", ""}, {"own-session", sid, cookie}} {
		for _, purpose := range []string{"download", "platform_fetch", "preview"} {
			t.Run(scope.name+"/"+purpose, func(t *testing.T) {
				status, out := mediaPurposeRequest(t, f, version, scope.sid, scope.cookie, video, purpose)
				if purpose == "preview" {
					if status != http.StatusOK {
						t.Fatalf("preview must remain available: %d %v", status, out)
					}
					url := out["items"].([]any)[0].(map[string]any)["url"].(string)
					code, _, body := f.rawRequest(t, "GET", url, nil)
					if code != http.StatusOK || len(body) == 0 {
						t.Fatalf("preview bytes=%d len=%d", code, len(body))
					}
				} else if status != http.StatusUnprocessableEntity {
					t.Errorf("unapproved %s must be denied, got %d %v", purpose, status, out)
				}
			})
		}
	}
}

func TestOldMediaExportWithoutVerifiableSourceRequiresRenewal(t *testing.T) {
	f := newPublicContentFixture(t, true)
	act := f.seedActions(t, "act_wechat")[0]
	pack, version, video, _ := f.seedAdoptedContent(t, f.tenA, f.storeA, act, "旧票据", true)
	f.bindAction(t, act, pack)
	for _, purpose := range []string{"download", "platform_fetch", "preview"} {
		claim, err := contentpack.NewMediaAccessClaim(version, video, purpose, "", time.Now(), 0)
		if err != nil {
			t.Fatal(err)
		}
		token, err := contentpack.MintMediaAccess(f.s.Cfg.InternalToken, claim)
		if err != nil {
			t.Fatal(err)
		}
		status, _, body := f.rawRequest(t, "GET", "/api/v1/public/content/media/"+token, nil)
		if purpose == "preview" {
			if status != 200 || len(body) == 0 {
				t.Errorf("preview=%d", status)
			}
		} else if status != http.StatusGone {
			t.Errorf("ambiguous %s token=%d, want410", purpose, status)
		}
	}
}

// 商家素材管理仍沿已有权限和门店范围签发，兑换不要求平台携带商家cookie。
func TestMerchantMediaExportKeepsScopedAuthority(t *testing.T) {
	f := newPublicContentFixture(t, true)
	pack, version, video, _ := f.seedAdoptedContent(t, f.tenA, f.storeA, "", "商家候选", false)
	body := fmt.Sprintf(`{"media_ids":[%q],"purpose":"download"}`, video)
	req, _ := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/stores/%s/content-packs/%s/versions/%s/media-access", f.ts.URL, f.storeA, pack, version), strings.NewReader(body))
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	req.Header.Set("X-Session-Token", "sess-owner-a")
	req.Header.Set("X-Tenant-ID", f.tenA)
	res, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("merchant export=%d %s", res.StatusCode, raw)
	}
	url := out["items"].([]any)[0].(map[string]any)["url"].(string)
	status, header, data := f.rawRequest(t, "GET", url, nil)
	if status != 200 || len(data) == 0 || !strings.HasPrefix(header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("merchant fetch=%d %v", status, header)
	}
	member, err := f.s.St.GetMemberByPrincipal(f.tenA, "usr_owner_a")
	if err != nil {
		t.Fatal(err)
	}
	enabled := false
	if _, err := f.s.St.UpdateMember(member.ID, nil, &enabled, nil); err != nil {
		t.Fatal(err)
	}
	status, _, _ = f.rawRequest(t, "GET", url, nil)
	if status != http.StatusForbidden {
		t.Errorf("revoked merchant cached token=%d, want403", status)
	}
}
