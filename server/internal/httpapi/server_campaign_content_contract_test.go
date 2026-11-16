package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/bianjiefilm/touch-engine/server/internal/campaignenable"
	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
	"github.com/bianjiefilm/touch-engine/server/internal/publishhandoff"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Use the StoreContentDrawer HTTP write authority, retaining immutable IDs.
func TestCampaignContentContractStoreUploadAdoption(t *testing.T) {
	f := newStoreContentFixture(t)
	status, video := f.uploadFile(t, "sess-owner-a", f.tenA, f.storeA, "tiny.mp4", "tiny.mp4")
	if status != 201 {
		t.Fatalf("video: %d %v", status, video)
	}
	status, cover := f.uploadFile(t, "sess-owner-a", f.tenA, f.storeA, "pixel.png", "pixel.png")
	if status != 201 {
		t.Fatalf("cover: %d %v", status, cover)
	}
	status, pack := f.owner(t, "POST", "/api/v1/stores/"+f.storeA+"/content-packs", fmt.Sprintf(`{"title":"活动媒体合同","campaign_id":%q}`, f.campA))
	if status != 201 {
		t.Fatalf("pack: %d %v", status, pack)
	}
	path := "/api/v1/stores/" + f.storeA + "/content-packs/" + pack["id"].(string)
	status, version := f.owner(t, "POST", path+"/versions", storeVersionBody(video["id"].(string), cover["id"].(string)))
	if status != 201 {
		t.Fatalf("version: %d %v", status, version)
	}
	id := version["id"].(string)
	status, out := f.owner(t, "POST", path+"/versions/"+id+"/adopt", `{"platform":"douyin"}`)
	if status != 200 {
		t.Fatalf("adopt: %d %v", status, out)
	}
	status, out = f.owner(t, "GET", "/api/v1/campaigns/"+f.campA+"/content-packs", "")
	if status != http.StatusOK {
		t.Fatalf("list: %d %v", status, out)
	}
	items := out["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("campaign list lost store-root adoption: %v", out)
	}
	p := items[0].(map[string]any)
	if p["id"] != pack["id"] {
		t.Fatalf("pack identity copied: %v", p)
	}
	vs := p["versions"].([]any)
	if len(vs) != 1 || vs[0].(map[string]any)["id"] != id {
		t.Fatalf("version identity: %v", vs)
	}
	v := vs[0].(map[string]any)
	if v["video"].(map[string]any)["media_id"] != video["id"] || v["cover"].(map[string]any)["mime_type"] != "image/png" {
		t.Fatalf("real media lost: %v", v)
	}
	camp, err := f.s.St.GetCampaign(f.campA, f.tenA)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := f.s.assembleEnableFacts(f.tenA, camp, store.PublishConfig{Spec: campaignenable.Spec{ContentVerID: id}}, time.Now())
	if err != nil || !facts.ContentAdopted || !facts.ContentDistributable {
		t.Fatalf("enable facts did not see original adoption: %+v %v", facts, err)
	}
	var n int
	if err := f.s.St.DB.QueryRow(`SELECT COUNT(*) FROM content_pack_versions WHERE id=?`, id).Scan(&n); err != nil || n != 0 {
		t.Fatalf("read adapter must not duplicate authority: %d %v", n, err)
	}
}

func seedCampaignStoreContent(t *testing.T, f *storeContentFixture, title, campaign, platform string) (string, string, string, string) {
	t.Helper()
	status, video := f.uploadFile(t, "sess-owner-a", f.tenA, f.storeA, "tiny.mp4", "tiny.mp4")
	if status != 201 {
		t.Fatalf("upload: %d %v", status, video)
	}
	status, cover := f.uploadFile(t, "sess-owner-a", f.tenA, f.storeA, "pixel.png", "pixel.png")
	if status != 201 {
		t.Fatalf("upload: %d %v", status, cover)
	}
	status, p := f.owner(t, "POST", "/api/v1/stores/"+f.storeA+"/content-packs", fmt.Sprintf(`{"title":%q,"campaign_id":%q}`, title, campaign))
	if status != 201 {
		t.Fatalf("pack: %d %v", status, p)
	}
	pack := p["id"].(string)
	path := "/api/v1/stores/" + f.storeA + "/content-packs/" + pack
	status, v := f.owner(t, "POST", path+"/versions", storeVersionBody(video["id"].(string), cover["id"].(string)))
	if status != 201 {
		t.Fatalf("version: %d %v", status, v)
	}
	version := v["id"].(string)
	status, a := f.owner(t, "POST", path+"/versions/"+version+"/adopt", fmt.Sprintf(`{"platform":%q}`, platform))
	if status != 200 {
		t.Fatalf("adopt: %d %v", status, a)
	}
	return pack, version, video["id"].(string), cover["id"].(string)
}

func newCampaignHandoffContract(t *testing.T) (*handoffFixture, *storeContentFixture, string) {
	t.Helper()
	f := newHandoffFixture(t, true)
	f.s.Media = upload.NewMediaStore(t.TempDir(), maxMediaUploadBytes)
	f.s.Cfg.FeatureContentPacks = true
	f.s.Cfg.FeatureParticipation = true
	f.s.Cfg.PublicBaseURL = ""
	f.ts.Close()
	f.ts = httptest.NewServer(f.s.Handler())
	t.Cleanup(f.ts.Close)
	res := f.s.St.ResolveLink(f.code, time.Now())
	return f, &storeContentFixture{s: f.s, ts: f.ts, tenA: f.tenA, storeA: res.Campaign.StoreID, campA: res.Campaign.ID}, res.Campaign.ID
}

func TestCampaignContentContractHandoffRealBytesAndFrozenParticipation(t *testing.T) {
	f, fc, campaign := newCampaignHandoffContract(t)
	pack, v1, video, cover := seedCampaignStoreContent(t, fc, "真实交接", campaign, "xiaohongshu")
	route := "/api/v1/public/links/" + f.code
	status, p := f.public(t, "POST", "/api/v1/participations", visitorCookie, mustJSON(t, map[string]any{"link_code": f.code, "touchpoint_source": "qr", "content_pack_version": v1, "idempotency_key": "real-media"}))
	if status != 201 {
		t.Fatalf("participation: %d %v", status, p)
	}
	pid := p["id"].(string)
	// A new adoption must not replace this existing participation's content.
	path := "/api/v1/stores/" + fc.storeA + "/content-packs/" + pack
	status, v := fc.owner(t, "POST", path+"/versions", storeVersionBody(video, cover))
	if status != 201 {
		t.Fatal(status, v)
	}
	v2 := v["id"].(string)
	status, a := fc.owner(t, "POST", path+"/versions/"+v2+"/adopt", `{"platform":"xiaohongshu"}`)
	if status != 200 {
		t.Fatal(status, a)
	}
	status, snap := f.public(t, "GET", route+"/content-snapshot?participation_id="+pid+"&version_id="+v1+"&platform=xiaohongshu", visitorCookie, "")
	if status != 200 || snap["pack_version_id"] != v1 || snap["cover"].(map[string]any)["mime_type"] != "image/png" {
		t.Fatalf("frozen snapshot: %d %v", status, snap)
	}
	frozen, err := f.s.St.GetCampaignContentVersion(f.tenA, campaign, v1)
	if err != nil {
		t.Fatal(err)
	}
	text, media := versionSnapshot(frozen)
	status, started := f.public(t, "POST", route+"/publish-handoff", visitorCookie, mustJSON(t, map[string]any{"participation_id": pid, "platform": "xiaohongshu", "pack_id": pack, "confirmed_content_version": publishhandoff.ContentFingerprint(text, media)}))
	if status != 201 {
		t.Fatalf("handoff real media: %d %v", status, started)
	}
	refs := started["media"].([]any)
	if len(refs) != 2 {
		t.Fatalf("refs: %v", started)
	}
	for i, ref := range refs {
		m := ref.(map[string]any)
		url := m["url"].(string)
		resp, err := f.ts.Client().Get(f.ts.URL + url)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		name := "tiny.mp4"
		mime := "video/mp4"
		if i == 1 {
			name = "pixel.png"
			mime = "image/png"
		}
		expected, err := os.ReadFile(filepath.Join("..", "upload", "testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != mime || !bytes.Equal(data, expected) {
			t.Fatalf("media %d bytes/mime: status %d type %s bytes %d", i, resp.StatusCode, resp.Header.Get("Content-Type"), len(data))
		}
	}

	status, other := f.public(t, "POST", "/api/v1/participations", visitorCookie, mustJSON(t, map[string]any{"link_code": f.code, "touchpoint_source": "qr", "content_pack_version": v2, "idempotency_key": "another-participation"}))
	if status != 201 {
		t.Fatalf("other participation: %d %v", status, other)
	}
	fresh, err := f.s.St.GetCampaignContentVersion(f.tenA, campaign, v2)
	if err != nil {
		t.Fatal(err)
	}
	freshText, freshMedia := versionSnapshot(fresh)
	status, other = f.public(t, "POST", route+"/publish-handoff", visitorCookie, mustJSON(t, map[string]any{"participation_id": other["id"], "platform": "xiaohongshu", "pack_id": pack, "idempotency_key": "ph:" + pid + ":xiaohongshu", "confirmed_content_version": publishhandoff.ContentFingerprint(freshText, freshMedia)}))
	if status != 404 {
		t.Fatalf("another participation replayed frozen handoff key: %d %v", status, other)
	}
	var storedVersion string
	if err := f.s.St.DB.QueryRow(`SELECT content_pack_version_id FROM publish_handoffs WHERE participation_id=?`, pid).Scan(&storedVersion); err != nil || storedVersion != v1 {
		t.Fatalf("handoff silently changed: %q %v", storedVersion, err)
	}
	if _, err := f.s.St.BlockStoreContentPackVersion(f.tenA, pack, v1, "授权撤回", "owner", time.Now()); err != nil {
		t.Fatal(err)
	}
	// Both fresh issuance and an already-issued URL must fail after revocation.
	id := started["handoff"].(map[string]any)["id"].(string)
	status, _ = f.public(t, "GET", route+"/publish-handoff/"+id, visitorCookie, "")
	if status < 400 {
		t.Fatalf("blocked refresh accepted: %d", status)
	}
	resp, err := f.ts.Client().Get(f.ts.URL + refs[0].(map[string]any)["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 409 {
		t.Fatalf("cached blocked token: %d", resp.StatusCode)
	}
}

func TestCampaignContentContractEnabledConfigAndScope(t *testing.T) {
	f, fc, campaign := newCampaignHandoffContract(t)
	pack, v1, video, cover := seedCampaignStoreContent(t, fc, "启用冻结", campaign, "xiaohongshu")
	// Explicit fixture isolates the frozen content contract. The production
	// enable gate is covered separately, including real channel/inventory facts.
	spec := validEnableSpec(v1)
	spec.Reward.StoreIDs = []string{fc.storeA}
	spec.Channels = []campaignenable.ChannelRef{{Platform: "xiaohongshu", EntryMode: "miniapp", Scenario: "store_post"}}

	now := time.Now().UTC()
	spec.Reward.ClaimFrom = now.Add(-time.Hour).Format(time.RFC3339)
	spec.Reward.ClaimUntil = now.Add(time.Hour).Format(time.RFC3339)
	spec.Reward.RedeemFrom = spec.Reward.ClaimFrom
	spec.Reward.RedeemUntil = now.Add(24 * time.Hour).Format(time.RFC3339)
	// Controlled engineering admission and stock facts are now required by the
	// server's live-participation preflight, not inferred from an enabled flag.
	if _, err := f.s.St.LoadAdmissionSeed(&channeladmission.Seed{Version: 10, GeneratedAt: "2026-11-14", Records: []channeladmission.Record{{Platform: "xiaohongshu", EntryMode: "miniapp", Scenario: "store_post", AppBinding: "platform_app", TenantScope: "*", Status: channeladmission.StatusAdmitted, RewardAdmitted: true, ReasonCode: "ok", ReasonText: "受控工程fixture", Capabilities: channeladmission.Capabilities{Media: "supported", Title: "supported", Body: "supported", Topic: "supported", POI: "supported", Receipt: "supported", Callback: "supported"}, OfficialSources: []string{"https://example.com/test-admission"}, LastCheckedAt: "2026-11-14", ValidUntil: "2030-01-01"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.St.CreateRewardInventory(f.tenA, store.NewRewardInventory{CampaignID: campaign, StoreID: fc.storeA, Kind: store.RewardKindStoreCoupon, Name: "冻结测试库存", BenefitSlot: spec.Reward.Slot, RuleVersion: 1, AllowedEvidence: []string{spec.VerificationMode}, TotalQty: 10, CreatedBy: "test"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := f.s.St.UpsertPublishConfigDraft(f.tenA, campaign, spec, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.St.EnablePublishConfig(f.tenA, campaign, cfg.Version, campaignenable.BuildDisclosure(spec, cfg.Version, time.Now().UTC()).JSON(), "test"); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/stores/" + fc.storeA + "/content-packs/" + pack
	status, v := fc.owner(t, "POST", path+"/versions", storeVersionBody(video, cover))
	if status != 201 {
		t.Fatal(status, v)
	}
	v2 := v["id"].(string)
	status, a := fc.owner(t, "POST", path+"/versions/"+v2+"/adopt", `{"platform":"xiaohongshu"}`)
	if status != 200 {
		t.Fatal(status, a)
	}
	route := "/api/v1/public/links/" + f.code
	status, out := f.public(t, "GET", route+"/content-choices", "", "")
	if status != 200 {
		t.Fatal(status, out)
	}
	items := out["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["version_id"] != v1 {
		t.Fatalf("unpublished adoption leaked: %v", out)
	}
	status, out = f.public(t, "GET", route+"/content-snapshot?pack_id="+pack+"&platform=xiaohongshu", "", "")
	if status != 200 || out["pack_version_id"] != v1 {
		t.Fatalf("published snapshot: %d %v", status, out)
	}
	for _, body := range []map[string]any{
		{"link_code": f.code, "touchpoint_source": "qr", "content_pack_version": v2},
		{"link_code": f.code, "touchpoint_source": "qr", "content_pack_version": v1, "rule_version": 99},
	} {
		status, out = f.public(t, "POST", "/api/v1/participations", visitorCookie, mustJSON(t, body))
		if status != 409 {
			t.Fatalf("injected content/rule: %d %v", status, out)
		}
	}
	status, out = f.public(t, "POST", "/api/v1/participations", visitorCookie, mustJSON(t, map[string]any{"link_code": f.code, "touchpoint_source": "qr", "idempotency_key": "enabled"}))
	if status != 201 || out["content_pack_version"] != v1 || out["rule_version"] != float64(cfg.Version) {
		t.Fatalf("server freeze: %d %v", status, out)
	}
	pid := out["id"].(string)
	status, out = f.public(t, "PATCH", "/api/v1/participations/"+pid+"/content", visitorCookie, `{"content_pack_version":""}`)
	if status != 200 || out["content_pack_version"] != v1 {
		t.Fatalf("empty client selection cleared enabled freeze: %d %v", status, out)
	}
	// Saving another draft preserves the currently published version for new and old participation.
	spec.ContentVerID = v2
	_, err = f.s.St.UpsertPublishConfigDraft(f.tenA, campaign, spec, "test")
	if err != nil {
		t.Fatal(err)
	}
	status, out = f.public(t, "POST", "/api/v1/participations", visitorCookie, mustJSON(t, map[string]any{"link_code": f.code, "touchpoint_source": "qr", "idempotency_key": "draft"}))
	if status != 201 || out["content_pack_version"] != v1 || out["rule_version"] != float64(cfg.Version) {
		t.Fatalf("draft replaced the live participation contract: %d %v", status, out)
	}
	status, out = f.public(t, "GET", route+"/content-snapshot?participation_id="+pid+"&version_id="+v1+"&platform=xiaohongshu", visitorCookie, "")
	if status != 200 || out["pack_version_id"] != v1 {
		t.Fatalf("draft changed old participation: %d %v", status, out)
	}
	status, out = f.public(t, "PATCH", "/api/v1/participations/"+pid+"/content", visitorCookie, mustJSON(t, map[string]any{"content_pack_version": v2}))
	if status != 409 {
		t.Fatalf("old participant injected draft: %d %v", status, out)
	}
	general, _, _, _ := seedCampaignStoreContent(t, fc, "通用门店包", "", "xiaohongshu")
	if _, err := f.s.St.GetCampaignContentPack(f.tenA, campaign, general); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("general pack leaked: %v", err)
	}
	other, err := f.s.St.CreateCampaign(store.NewCampaign{TenantID: f.tenA, StoreID: fc.storeA, Title: "同店另一活动", CreatedBy: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.St.GetCampaignContentVersion(f.tenA, other.ID, v1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross campaign leaked: %v", err)
	}
	if _, err := f.s.St.GetCampaignContentVersion("other-tenant", campaign, v1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross tenant leaked: %v", err)
	}
}

func TestCampaignContentContractEnableHTTP(t *testing.T) {
	f, _ := newCfgFixture(t, true)
	spec := f.seedEnableableWorld(t, 1)
	f.s.Media = upload.NewMediaStore(t.TempDir(), maxMediaUploadBytes)
	fc := &storeContentFixture{s: f.s, ts: f.ts, tenA: f.tenA, storeA: f.store1ID, campA: f.camp1ID}
	_, id, _, _ := seedCampaignStoreContent(t, fc, "正式启用的门店内容", f.camp1ID, "douyin")
	spec.ContentVerID = id
	spec.Reward.StoreIDs = []string{f.store1ID}
	status, out := f.owner(t, "PUT", "/api/v1/campaigns/"+f.camp1ID+"/publish-config", mustJSON(t, spec))
	if status != 200 {
		t.Fatalf("save: %d %v", status, out)
	}
	status, out = f.owner(t, "POST", "/api/v1/campaigns/"+f.camp1ID+"/publish-config/enable", "")
	if status != 200 {
		t.Fatalf("store-root media could not enable via real facts: %d %v", status, out)
	}
	cfg, err := f.s.St.GetPublishConfig(f.tenA, f.camp1ID)
	if err != nil || cfg.Status != "enabled" || cfg.Spec.ContentVerID != id || cfg.EnabledVersion != cfg.Version {
		t.Fatalf("enabled original version: %+v %v", cfg, err)
	}
}

func TestCampaignContentContractExactPlatformAndMultiplePackChoices(t *testing.T) {
	f, fc, campaign := newCampaignHandoffContract(t)
	p1, v1, video, cover := seedCampaignStoreContent(t, fc, "小红书主题一", campaign, "xiaohongshu")
	p2, v2, _, _ := seedCampaignStoreContent(t, fc, "小红书主题二", campaign, "xiaohongshu")
	path := "/api/v1/stores/" + fc.storeA + "/content-packs/" + p1
	status, v := fc.owner(t, "POST", path+"/versions", storeVersionBody(video, cover))
	if status != 201 {
		t.Fatal(status, v)
	}
	douyin := v["id"].(string)
	status, a := fc.owner(t, "POST", path+"/versions/"+douyin+"/adopt", `{"platform":"douyin"}`)
	if status != 200 {
		t.Fatal(status, a)
	}
	for _, want := range []struct{ pack, platform, version string }{{p1, "xiaohongshu", v1}, {p1, "douyin", douyin}, {p2, "xiaohongshu", v2}} {
		status, out := f.public(t, "GET", "/api/v1/public/links/"+f.code+"/content-snapshot?pack_id="+want.pack+"&platform="+want.platform, "", "")
		if status != 200 || out["pack_version_id"] != want.version {
			t.Fatalf("wrong platform/pack: %+v -> %d %v", want, status, out)
		}
	}
	if _, err := f.s.St.GetCampaignAdoptedContent(f.tenA, campaign, "xiaohongshu", ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ambiguous pack silently selected: %v", err)
	}
	status, out := f.public(t, "GET", "/api/v1/public/links/"+f.code+"/content-choices", "", "")
	if status != 200 || len(out["items"].([]any)) != 4 {
		t.Fatalf("lost valid pack/platform choices: %d %v", status, out)
	} // includes fixture legacy adoption
}

func TestCampaignContentContractHandoffRejectsOtherCampaignLink(t *testing.T) {
	f := newHandoffFixture(t, true)
	pid := f.startParticipation(t)
	fp := f.fingerprintOf(t, "xiaohongshu")
	status, started := f.public(t, "POST", "/api/v1/public/links/"+f.code+"/publish-handoff", visitorCookie, mustJSON(t, map[string]any{"participation_id": pid, "platform": "xiaohongshu", "confirmed_content_version": fp}))
	if status != 201 {
		t.Fatalf("handoff: %d %v", status, started)
	}
	res := f.s.St.ResolveLink(f.code, time.Now())
	camp, err := f.s.St.CreateCampaign(store.NewCampaign{TenantID: f.tenA, StoreID: res.Campaign.StoreID, Title: "同店另一活动链接", CreatedBy: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.St.TransitionCampaign(camp.ID, f.tenA, "active"); err != nil {
		t.Fatal(err)
	}
	link, err := f.s.St.CreateLink(f.tenA, camp.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	id := started["handoff"].(map[string]any)["id"].(string)
	status, out := f.public(t, "GET", "/api/v1/public/links/"+link.Code+"/publish-handoff/"+id, visitorCookie, "")
	if status != 404 {
		t.Fatalf("other campaign link read original handoff: %d %v", status, out)
	}
}

func TestCampaignContentContractLegacyAdapterReceivesRealStoreMedia(t *testing.T) {
	f, fc, campaign := newCampaignHandoffContract(t)
	_, version, _, _ := seedCampaignStoreContent(t, fc, "适配器真实媒体", campaign, "xiaohongshu")
	status, p := f.public(t, "POST", "/api/v1/participations", visitorCookie, mustJSON(t, map[string]any{"link_code": f.code, "touchpoint_source": "qr", "content_pack_version": version, "idempotency_key": "adapter-real"}))
	if status != 201 {
		t.Fatal(status, p)
	}
	f.s.Cfg.PublishHandoffMerchantAppRef = "test-app"
	cap := channeladmission.ActionCapability{ID: "test-real-c4-js", SeedVersion: 1, CatalogCode: "A02", Platform: "xiaohongshu", Method: channeladmission.ActionMethodJSMedia, Scenario: "store_promo", ActionKind: "publish", Label: "test", AppBinding: "platform_app", TenantScope: "*", Status: channeladmission.StatusAdmitted, RequiredInputs: []string{"content_pack_version"}, FieldSupport: map[string]string{"body": "unsupported"}, EvidenceGrade: channeladmission.EvidenceOfficialSnapshot, CheckedAt: "2026-11-09", ValidUntil: "2027-02-07", Environments: map[string]channeladmission.EnvCapabilities{"desktop_browser": handoffV2EnvCaps("supported", "supported", "unknown", "unknown")}}
	for _, env := range []string{"ios_safari", "android_browser", "ios_wechat", "android_wechat", "target_app"} {
		cap.Environments[env] = cap.Environments["desktop_browser"]
	}
	if _, _, err := f.s.St.UpsertActionCapability(cap, store.ControlAudit{Actor: "test", Reason: "受控准入fixture"}); err != nil {
		t.Fatal(err)
	}
	adapter := &legacyCapture{}
	f.s.PublishHandoff.SetAdapter(adapter)
	route := "/api/v1/public/links/" + f.code + "/publish-handoff"
	status, plan := f.public(t, "GET", route+"/routes?platform=xiaohongshu&with_snapshot=1&adapter_mode=1&environment=desktop_browser&participation_id="+p["id"].(string), visitorCookie, "")
	if status != 200 || plan["confirmation_required"] == nil {
		t.Fatalf("adapter plan: %d %v", status, plan)
	}
	status, out := f.public(t, "POST", route, visitorCookie, mustJSON(t, map[string]any{"participation_id": p["id"], "platform": "xiaohongshu", "adapter_mode": true, "environment": "desktop_browser", "confirmed_content_version": plan["confirmation_required"]}))
	if status != 201 || len(adapter.requests) != 1 {
		t.Fatalf("adapter start: %d %v %#v", status, out, adapter.requests)
	}
	req := adapter.requests[0]
	if len(req.MediaRefs) != 2 || req.Media[1].ContentType != "image/png" {
		t.Fatalf("adapter lost real media contract: %+v", req)
	}
	for _, m := range req.Media {
		resp, err := f.ts.Client().Get(f.ts.URL + req.MediaRefs[m.AssetID])
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		name := "tiny.mp4"
		if m.Kind == "cover" {
			name = "pixel.png"
		}
		expected, err := os.ReadFile(filepath.Join("..", "upload", "testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != m.ContentType || !bytes.Equal(data, expected) {
			t.Fatalf("adapter unpullable bytes: %d %s", resp.StatusCode, m.ContentType)
		}
	}
}

func TestCampaignContentContractGrantExpiryRefusesSnapshotAndIssuedBytes(t *testing.T) {
	f, fc, campaign := newCampaignHandoffContract(t)
	pack, _, video, cover := seedCampaignStoreContent(t, fc, "短期授权", campaign, "xiaohongshu")
	deadline := time.Now().UTC().Add(3 * time.Second)
	var body map[string]any
	if err := json.Unmarshal([]byte(storeVersionBody(video, cover)), &body); err != nil {
		t.Fatal(err)
	}
	body["grant_until"] = deadline.Format(time.RFC3339Nano)
	path := "/api/v1/stores/" + fc.storeA + "/content-packs/" + pack
	status, v := fc.owner(t, "POST", path+"/versions", mustJSON(t, body))
	if status != 201 {
		t.Fatal(status, v)
	}
	version := v["id"].(string)
	status, a := fc.owner(t, "POST", path+"/versions/"+version+"/adopt", `{"platform":"xiaohongshu"}`)
	if status != 200 {
		t.Fatal(status, a)
	}
	status, p := f.public(t, "POST", "/api/v1/participations", visitorCookie, mustJSON(t, map[string]any{"link_code": f.code, "touchpoint_source": "qr", "content_pack_version": version, "idempotency_key": "expiry-real"}))
	if status != 201 {
		t.Fatal(status, p)
	}
	frozen, err := f.s.St.GetCampaignContentVersion(f.tenA, campaign, version)
	if err != nil {
		t.Fatal(err)
	}
	text, media := versionSnapshot(frozen)
	route := "/api/v1/public/links/" + f.code
	status, out := f.public(t, "POST", route+"/publish-handoff", visitorCookie, mustJSON(t, map[string]any{"participation_id": p["id"], "platform": "xiaohongshu", "pack_id": pack, "confirmed_content_version": publishhandoff.ContentFingerprint(text, media)}))
	if status != 201 {
		t.Fatalf("unexpired handoff: %d %v", status, out)
	}
	tokenURL := out["media"].([]any)[0].(map[string]any)["url"].(string)
	<-time.NewTimer(time.Until(deadline) + 20*time.Millisecond).C
	status, out = f.public(t, "GET", route+"/content-snapshot?pack_id="+pack+"&platform=xiaohongshu", "", "")
	if status != 409 || out["error"] != "grant_expired" {
		t.Fatalf("expired snapshot: %d %v", status, out)
	}
	resp, err := f.ts.Client().Get(f.ts.URL + tokenURL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 409 {
		t.Fatalf("already issued token ignored grant expiry: %d", resp.StatusCode)
	}
	status, out = f.public(t, "POST", "/api/v1/participations", visitorCookie, mustJSON(t, map[string]any{"link_code": f.code, "touchpoint_source": "qr", "content_pack_version": version, "idempotency_key": "expiry-new"}))
	if status != 409 || out["error"] != "grant_expired" {
		t.Fatalf("expired new participation: %d %v", status, out)
	}
}

func TestCampaignContentContractHistoricalBlockedReasonFailsClosed(t *testing.T) {
	f, fc, campaign := newCampaignHandoffContract(t)
	pack, version, _, _ := seedCampaignStoreContent(t, fc, "历史撤权错态", campaign, "xiaohongshu")
	if _, err := f.s.St.BlockStoreContentPackVersion(f.tenA, pack, version, "版权方撤回", "test", time.Now()); err != nil {
		t.Fatal(err)
	}
	// Model a historical row corrupted by the former supersede UPDATE. The
	// campaign adapter must refuse it without rewriting either content authority.
	if _, err := f.s.St.DB.Exec(`UPDATE store_content_pack_versions SET status='retired' WHERE id=?`, version); err != nil {
		t.Fatal(err)
	}
	v, err := f.s.St.GetCampaignContentVersion(f.tenA, campaign, version)
	if err != nil || v.Status != contentpack.StatusBlocked || v.BlockedReason != "版权方撤回" {
		t.Fatalf("historical blocked version resurrected: %+v %v", v, err)
	}
	status, out := f.public(t, "GET", "/api/v1/public/links/"+f.code+"/content-snapshot?pack_id="+pack+"&platform=xiaohongshu", "", "")
	if status != 409 || out["error"] != "not_distributable" {
		t.Fatalf("corrupt retired status distributed: %d %v", status, out)
	}
	var raw string
	if err := f.s.St.DB.QueryRow(`SELECT status FROM store_content_pack_versions WHERE id=?`, version).Scan(&raw); err != nil || raw != "retired" {
		t.Fatalf("adapter rewrote authority: %s %v", raw, err)
	}
}

func TestCampaignContentContractStoreAndTenantReadScope(t *testing.T) {
	fc := newStoreContentFixture(t)
	_, version, _, _ := seedCampaignStoreContent(t, fc, "门店范围", fc.campA, "douyin")
	other, err := fc.s.St.CreateCampaign(store.NewCampaign{TenantID: fc.tenA, StoreID: fc.storeA2, Title: "另一门店活动", CreatedBy: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fc.s.St.GetCampaignContentVersion(fc.tenA, other.ID, version); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-store version visible: %v", err)
	}
	status, out := fc.do(t, "GET", "/api/v1/campaigns/"+fc.campA+"/content-packs", "sess-owner-b", fc.tenB, "")
	if status != 404 {
		t.Fatalf("cross-tenant list: %d %v", status, out)
	}
	status, out = fc.do(t, "GET", "/api/v1/campaigns/"+other.ID+"/content-packs", "sess-manager-a", fc.tenA, "")
	if status != 403 {
		t.Fatalf("store-scoped manager read other store: %d %v", status, out)
	}
	status, out = fc.owner(t, "POST", "/api/v1/stores/"+fc.storeA2+"/content-packs", fmt.Sprintf(`{"title":"跨店绑定","campaign_id":%q}`, fc.campA))
	if status != 404 {
		t.Fatalf("cross-store campaign binding: %d %v", status, out)
	}
}
