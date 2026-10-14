package httpapi

// server_campaign_config_test.go: HUI-1676 增量(商家配置与真实启用)HTTP 验收。
//
// 覆盖(计划 Step 4):
//   - off 基线:新端点路由不注册 404;
//   - authz:仅 org_owner 可写可启用;staff 403;
//   - 跨租户 404;
//   - 草稿 CRUD + 修订留痕;形状校验矩阵(非法枚举/未知字段);
//   - 启用前检查全负例:内容未采用/blocked、渠道 closed/未准入、权益引用
//     缺失/版本不齐/无库存、窗口冲突、活动已结束、错店 —— 各带机器原因码
//     与 field;正路径一次启用;
//   - G3 快照契约:enabled 才有 snapshot;JSON 形状 = rewardops.Snapshot
//     (campaign_id/version/allowed_evidence/slots)+ disclosure;
//   - 暂停:状态 paused、快照 409 not_enabled、参与表零触碰(老参与核验
//     入口保留的存储面证明)、修订留痕。

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/assetlib"
	"github.com/bianjiefilm/touch-engine/server/internal/campaignenable"
	"github.com/bianjiefilm/touch-engine/server/internal/channeladmission"
	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/identity"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

type configFixture struct {
	s        *Server
	ts       *httptest.Server
	tenA     string
	tenB     string
	store1ID string
	camp1ID  string
	camp2ID  string
	campPast string
}

func cfgTestConfig(rulesOn bool) config.Config {
	flag := "off"
	if rulesOn {
		flag = "on"
	}
	return config.Load(func(k string) string {
		switch k {
		case "TOUCH_INTERNAL_TOKEN":
			return "test-internal-secret"
		case "PLATFORM_IDENTITY_BASE_URL":
			return "http://identity.test"
		case "PLATFORM_IDENTITY_TOKEN":
			return "identity-token"
		case "PLATFORM_NOTIFY_BASE_URL":
			return "http://notify.test"
		case "PLATFORM_NOTIFY_TOKEN":
			return "notify-token"
		case "LEADS_TARGET_APP_ID":
			return "crm-app"
		case "LEADS_PHONE_PEPPER":
			return "test-pepper"
		case "FEATURE_CAMPAIGN_RULES":
			return flag
		case "FEATURE_CHANNEL_ADMISSION":
			return "on"
		}
		return ""
	})
}

func newCfgFixture(t *testing.T, rulesOn bool) (*configFixture, *sqlDB) {
	t.Helper()
	cfg := cfgTestConfig(rulesOn)
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	st := store.New(d)

	tenA, err := st.CreateTenant("配置商家A")
	if err != nil {
		t.Fatal(err)
	}
	tenB, err := st.CreateTenant("配置商家B")
	if err != nil {
		t.Fatal(err)
	}
	s1, err := st.CreateStore(tenA.ID, "旗舰店", "地址1", "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ tenant, principal, role string }{
		{tenA.ID, "usr_owner_a", "org_owner"},
		{tenA.ID, "usr_staff_a", "staff"},
		{tenB.ID, "usr_owner_b", "org_owner"},
	} {
		if _, err := st.CreateMember(m.tenant, m.principal, m.role, m.principal, "test", true); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	idsrv := fakeIdentity(t, map[string]identitySession{
		"sess-owner-a": {"usr_owner_a", "a@example.com"},
		"sess-staff-a": {"usr_staff_a", "staff@example.com"},
		"sess-owner-b": {"usr_owner_b", "b@example.com"},
	})
	idc := &identity.Client{BaseURL: idsrv.URL, Token: "identity-token", AppID: "touch-engine", HTTP: idsrv.Client()}
	upc := &upload.Client{BaseURL: "http://upload.test", Token: "upload-token", AppID: "touch-engine"}

	srv := New(cfg, d, idc, upc, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	mkCamp := func(title, storeID, startsAt, endsAt string) string {
		body := fmt.Sprintf(`{"title":%q,"public_content":"到店发布礼","starts_at":%q,"ends_at":%q}`, title, startsAt, endsAt)
		if storeID != "" {
			body = fmt.Sprintf(`{"title":%q,"public_content":"到店发布礼","store_id":%q,"starts_at":%q,"ends_at":%q}`, title, storeID, startsAt, endsAt)
		}
		status, out := doCfg(t, ts, "POST", "/api/v1/campaigns", "sess-owner-a", tenA.ID, body)
		if status != 201 {
			t.Fatalf("campaign create %s: %d %v", title, status, out)
		}
		return out["id"].(string)
	}
	f := &configFixture{
		s: srv, ts: ts, tenA: tenA.ID, tenB: tenB.ID, store1ID: s1.ID,
		camp1ID:  mkCamp("配置活动一", s1.ID, "2026-01-01T00:00:00Z", "2030-01-01T00:00:00Z"),
		camp2ID:  mkCamp("配置活动二", "", "2026-01-01T00:00:00Z", "2030-01-01T00:00:00Z"),
		campPast: mkCamp("已结束活动", "", "2019-01-01T00:00:00Z", "2020-01-01T00:00:00Z"),
	}
	return f, &sqlDB{d}
}

// sqlDB is a tiny direct-SQL escape hatch for cross-domain seeding/inspection
// that the store API deliberately does not expose (fixtures only).
type sqlDB struct{ d *sql.DB }

func (w *sqlDB) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := w.d.Exec(q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

func (w *sqlDB) count(t *testing.T, q string) int {
	t.Helper()
	var n int
	if err := w.d.QueryRow(q).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", q, err)
	}
	return n
}

func doCfg(t *testing.T, ts *httptest.Server, method, path, session, tenant, body string) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, ts.URL+path, rd)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	if session != "" {
		req.Header.Set("X-Session-Token", session)
	}
	if tenant != "" {
		req.Header.Set("X-Tenant-ID", tenant)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

func (f *configFixture) do(t *testing.T, method, path, session, tenant, body string) (int, map[string]any) {
	t.Helper()
	return doCfg(t, f.ts, method, path, session, tenant, body)
}

func (f *configFixture) owner(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	return f.do(t, method, path, "sess-owner-a", f.tenA, body)
}

// seedEnableableWorld builds the smallest truthful world where enabling camp1
// passes: adopted content pack version, admitted channel, matching reward
// batch. It returns the spec to PUT.
func (f *configFixture) seedEnableableWorld(t *testing.T, ruleVersion int) campaignenable.Spec {
	t.Helper()
	// 内容包 + 已采用版本(3553)。
	video := f.createAsset(t, "up-video-cfg", strings.Repeat("c", 64), assetlib.MediaTypeVideo)
	cover := f.createAsset(t, "up-cover-cfg", strings.Repeat("d", 64), assetlib.MediaTypeImage)
	pack, err := f.s.St.CreateContentPack(f.tenA, f.camp1ID, "", "配置主题包", "usr_owner_a")
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	spec := contentpack.VersionSpec{
		ContentKind: contentpack.ContentKindVideo,
		Video:       contentpack.AssetSnapshot{AssetID: video.ID, AssetRef: video.AssetRef, SHA256: video.SHA256},
		Cover:       contentpack.AssetSnapshot{AssetID: cover.ID, AssetRef: cover.AssetRef, SHA256: cover.SHA256},
		Title:       "周末发布礼", Body: "发布视频领门店券。",
		Topics:    []string{"开业"},
		StoreName: "旗舰店", PriceNote: "到店礼", ValidNote: "2026-10-01 至 2026-11-30",
		PlatformVersions: []contentpack.PlatformVersion{{Platform: "douyin", Note: "竖版", SourceVersion: "src-1"}},
		GrantRefs:        []string{"grant-2026-001"},
		Marking:          contentpack.Marking{AdRequired: true},
		Source:           contentpack.SourceMerchantUpload,
	}
	ver, err := f.s.St.CreateContentPackVersion(f.tenA, pack.ID, spec, contentpack.StatusCandidate, "usr_owner_a")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if _, err := f.s.St.AdoptContentPackVersion(f.tenA, pack.ID, ver.ID, "douyin", "usr_owner_a", time.Now().UTC()); err != nil {
		t.Fatalf("adopt: %v", err)
	}

	// 渠道准入真值(3535):一条 admitted + 奖励批复的抖音路线。
	seed := &channeladmission.Seed{Version: 9, GeneratedAt: "2026-10-14"}
	seed.Records = append(seed.Records, channeladmission.Record{
		Platform: "douyin", EntryMode: "h5", Scenario: "store_post",
		AppBinding: "platform_app", TenantScope: "*", Status: channeladmission.StatusAdmitted,
		RewardAdmitted: true, ReasonCode: "ok", ReasonText: "准入",
		Capabilities: channeladmission.Capabilities{
			Media: "supported", Title: "supported", Body: "supported", Topic: "supported",
			POI: "supported", Receipt: "supported", Callback: "supported",
		},
		OfficialSources: []string{"https://example.com/admission"}, LastCheckedAt: "2026-10-14",
		ValidUntil: "2030-01-01",
	})
	if _, err := f.s.St.LoadAdmissionSeed(seed); err != nil {
		t.Fatalf("admission seed: %v", err)
	}

	// 奖励批次(1671,只读方):绑定 rule_version 与 slot。
	specOut := validEnableSpec(ver.ID)
	_, err = f.s.St.CreateRewardInventory(f.tenA, store.NewRewardInventory{
		CampaignID: f.camp1ID, Kind: "store_coupon", Name: "门店券",
		BenefitSlot: specOut.Reward.Slot, RuleVersion: ruleVersion,
		AllowedEvidence: []string{specOut.VerificationMode},
		TotalQty:        50, FaceValueMinor: 500, CostMinor: 500, Currency: "CNY",
		CreatedBy: "usr_owner_a",
	})
	if err != nil {
		t.Fatalf("inventory: %v", err)
	}
	return specOut
}

func (f *configFixture) createAsset(t *testing.T, ref, sha string, mt assetlib.MediaType) store.LibAsset {
	t.Helper()
	a, err := f.s.St.CreateLibAsset(f.tenA, assetlib.Registration{
		AssetRef: ref, SHA256: sha, MediaType: mt,
		Source: assetlib.SourceAiCut, Purpose: "campaign_publish", GrantRef: "grant-2026-001",
	}, "", "usr_owner_a")
	if err != nil {
		t.Fatalf("create asset %s: %v", ref, err)
	}
	return a
}

func validEnableSpec(contentVerID string) campaignenable.Spec {
	return campaignenable.Spec{
		VerificationMode: campaignenable.ModeStaffObserved,
		CustomerCopy:     "现场把发布成功的页面给店员看,确认后当场领门店优惠券。",
		Reward: campaignenable.Reward{
			Kind:           campaignenable.KindStoreCoupon,
			Slot:           "store_coupon_main",
			BudgetMode:     campaignenable.BudgetTotalQuantity,
			TotalQuantity:  intPtr(50),
			Currency:       "CNY",
			StoreIDs:       []string{"NEEDS_STORE"},
			ClaimFrom:      "2026-10-01T00:00:00Z",
			ClaimUntil:     "2026-10-31T23:59:59Z",
			RedeemFrom:     "2026-10-01T00:00:00Z",
			RedeemUntil:    "2026-11-30T23:59:59Z",
			OnsiteRedempt:  true,
			StaffScope:     campaignenable.ScopeStoreStaff,
			OutOfStockRule: campaignenable.StockStopNew,
		},
		DedupBasis:   campaignenable.BasisPlatformSubject,
		Channels:     []campaignenable.ChannelRef{{Platform: "douyin", EntryMode: "h5", Scenario: "store_post"}},
		ContentVerID: contentVerID,
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func intPtr(n int) *int { return &n }

// ---- tests ---------------------------------------------------------------------

func TestPublishConfigOffBootRoutesNotRegistered(t *testing.T) {
	f, _ := newCfgFixture(t, false)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/v1/campaigns/" + f.camp1ID + "/publish-config"},
		{"PUT", "/api/v1/campaigns/" + f.camp1ID + "/publish-config"},
		{"POST", "/api/v1/campaigns/" + f.camp1ID + "/publish-config/enable"},
		{"POST", "/api/v1/campaigns/" + f.camp1ID + "/publish-config/pause"},
		{"GET", "/api/v1/campaigns/" + f.camp1ID + "/publish-config/snapshot"},
		{"GET", "/api/v1/campaigns/" + f.camp1ID + "/publish-config/revisions"},
	} {
		status, out := f.owner(t, tc.method, tc.path, ``)
		if status != 404 {
			t.Fatalf("%s %s off-boot = %d %v, want 404", tc.method, tc.path, status, out)
		}
	}
}

func TestPublishConfigAuthzOwnerOnly(t *testing.T) {
	f, _ := newCfgFixture(t, true)
	spec := validEnableSpec("cpv_any")
	status, out := f.do(t, "PUT", "/api/v1/campaigns/"+f.camp1ID+"/publish-config", "sess-staff-a", f.tenA, mustJSON(t, spec))
	if status != 403 {
		t.Fatalf("staff PUT = %d %v, want 403", status, out)
	}
	status, out = f.do(t, "POST", "/api/v1/campaigns/"+f.camp1ID+"/publish-config/enable", "sess-staff-a", f.tenA, ``)
	if status != 403 {
		// 授权先于配置存在性检查(campaignScoped 掩码):staff 一律 403。
		t.Fatalf("staff enable = %d %v, want 403", status, out)
	}
}

func TestPublishConfigCrossTenantMasked(t *testing.T) {
	f, _ := newCfgFixture(t, true)
	status, out := f.do(t, "PUT", "/api/v1/campaigns/"+f.camp1ID+"/publish-config", "sess-owner-b", f.tenB, mustJSON(t, validEnableSpec("cpv_x")))
	if status != 404 {
		t.Fatalf("cross-tenant PUT = %d %v, want 404", status, out)
	}
	status, _ = f.owner(t, "GET", "/api/v1/campaigns/"+f.camp1ID+"/publish-config", ``)
	if status != 404 {
		t.Fatalf("unconfigured GET = %d, want 404", status)
	}
}

func TestPublishConfigDraftCRUDAndRevisions(t *testing.T) {
	f, _ := newCfgFixture(t, true)
	world := f.seedEnableableWorld(t, 1)
	world.Reward.StoreIDs = []string{f.store1ID}

	status, out := f.owner(t, "PUT", "/api/v1/campaigns/"+f.camp1ID+"/publish-config", mustJSON(t, world))
	if status != 200 {
		t.Fatalf("first PUT = %d %v", status, out)
	}
	if out["version"].(float64) != 1 || out["status"] != "draft" {
		t.Fatalf("first save = v%v %v, want v1 draft", out["version"], out["status"])
	}
	// GET 回读(刷新/重登恢复的服务端真值)。
	status, out = f.owner(t, "GET", "/api/v1/campaigns/"+f.camp1ID+"/publish-config", ``)
	if status != 200 || out["version"].(float64) != 1 {
		t.Fatalf("GET after save = %d %v", status, out)
	}
	gotSpec := out["spec"].(map[string]any)
	if gotSpec["verification_mode"] != campaignenable.ModeStaffObserved {
		t.Fatalf("spec round trip lost mode: %v", gotSpec)
	}

	// 形状校验矩阵。
	bad := world
	bad.VerificationMode = "self_report"
	if status, _ = f.owner(t, "PUT", "/api/v1/campaigns/"+f.camp1ID+"/publish-config", mustJSON(t, bad)); status != 400 {
		t.Fatalf("invalid mode PUT = %d, want 400", status)
	}
	if status, _ = f.owner(t, "PUT", "/api/v1/campaigns/"+f.camp1ID+"/publish-config", `{"verification_mode":"staff_observed_submission","surprise":1}`); status != 400 {
		t.Fatalf("unknown field PUT = %d, want 400", status)
	}

	// 第二次保存 → version 2 + update 修订。
	world.CustomerCopy = "改过的文案。"
	status, out = f.owner(t, "PUT", "/api/v1/campaigns/"+f.camp1ID+"/publish-config", mustJSON(t, world))
	if status != 200 || out["version"].(float64) != 2 {
		t.Fatalf("second PUT = %d v%v", status, out["version"])
	}
	status, out = f.owner(t, "GET", "/api/v1/campaigns/"+f.camp1ID+"/publish-config/revisions", ``)
	if status != 200 {
		t.Fatalf("revisions GET = %d", status)
	}
	items := out["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["action"] != "create" || items[1].(map[string]any)["action"] != "update" {
		t.Fatalf("revision trail = %v", items)
	}
}

func TestPublishConfigEnableRefusalMatrix(t *testing.T) {
	t.Run("content not adopted", func(t *testing.T) {
		f, _ := newCfgFixture(t, true)
		spec := f.seedEnableableWorld(t, 1)
		spec.Reward.StoreIDs = []string{f.store1ID}
		spec.ContentVerID = "cpv_not_this_one"
		f.owner(t, "PUT", "/api/v1/campaigns/"+f.camp1ID+"/publish-config", mustJSON(t, spec))
		assertRefusal(t, f, "content_not_ready", "content_version_id")
	})
	t.Run("content blocked", func(t *testing.T) {
		f, _ := newCfgFixture(t, true)
		spec := f.seedEnableableWorld(t, 1)
		spec.Reward.StoreIDs = []string{f.store1ID}
		// block 已采用版本 → blocked 阻止新分发。
		packs, err := f.s.St.ListContentPacks(f.tenA, f.camp1ID)
		if err != nil || len(packs) == 0 {
			t.Fatalf("list packs: %v", err)
		}
		vers, err := f.s.St.ListContentPackVersions(f.tenA, packs[0].ID)
		if err != nil || len(vers) == 0 {
			t.Fatalf("list versions: %v", err)
		}
		if _, err := f.s.St.BlockContentPackVersion(f.tenA, packs[0].ID, vers[0].ID, "撤权", "usr_owner_a", time.Now().UTC()); err != nil {
			t.Fatalf("block: %v", err)
		}
		f.owner(t, "PUT", "/api/v1/campaigns/"+f.camp1ID+"/publish-config", mustJSON(t, spec))
		assertRefusal(t, f, "content_blocked", "content_version_id")
	})
	t.Run("channel not admitted without seed truth", func(t *testing.T) {
		f, _ := newCfgFixture(t, true)
		// 只建内容,不落准入种子 → 全部路线 unknown/fail-closed。
		video := f.createAsset(t, "up-v2", strings.Repeat("e", 64), assetlib.MediaTypeVideo)
		cover := f.createAsset(t, "up-c2", strings.Repeat("f", 64), assetlib.MediaTypeImage)
		pack, err := f.s.St.CreateContentPack(f.tenA, f.camp1ID, "", "包二", "usr_owner_a")
		if err != nil {
			t.Fatalf("pack: %v", err)
		}
		ver, err := f.s.St.CreateContentPackVersion(f.tenA, pack.ID, contentpack.VersionSpec{
			ContentKind: contentpack.ContentKindVideo,
			Video:       contentpack.AssetSnapshot{AssetID: video.ID, AssetRef: video.AssetRef, SHA256: video.SHA256},
			Cover:       contentpack.AssetSnapshot{AssetID: cover.ID, AssetRef: cover.AssetRef, SHA256: cover.SHA256},
			Title: "标题", Body: "正文", Topics: []string{"t"},
			StoreName: "旗舰店", PriceNote: "价", ValidNote: "有效期",
			PlatformVersions: []contentpack.PlatformVersion{{Platform: "douyin", Note: "n", SourceVersion: "s"}},
			GrantRefs:        []string{"g"}, Marking: contentpack.Marking{AdRequired: true},
			Source: contentpack.SourceMerchantUpload,
		}, contentpack.StatusCandidate, "usr_owner_a")
		if err != nil {
			t.Fatalf("version: %v", err)
		}
		if _, err := f.s.St.AdoptContentPackVersion(f.tenA, pack.ID, ver.ID, "douyin", "usr_owner_a", time.Now().UTC()); err != nil {
			t.Fatalf("adopt: %v", err)
		}
		spec := validEnableSpec(ver.ID)
		spec.Reward.StoreIDs = []string{f.store1ID}
		f.owner(t, "PUT", "/api/v1/campaigns/"+f.camp1ID+"/publish-config", mustJSON(t, spec))
		assertRefusal(t, f, "channel_not_admitted", "channels")
	})
	t.Run("benefit ref missing then version mismatch", func(t *testing.T) {
		f, _ := newCfgFixture(t, true)
		spec := f.seedEnableableWorld(t, 1)
		spec.Reward.StoreIDs = []string{f.store1ID}
		f.owner(t, "PUT", "/api/v1/campaigns/"+f.camp1ID+"/publish-config", mustJSON(t, spec))
		// 版本不齐:批次绑定 v1,但改版到 v2 → 版本错配拒绝(改版必须重新对齐)。
		spec.CustomerCopy = "第二版文案。"
		status, _ := f.owner(t, "PUT", "/api/v1/campaigns/"+f.camp1ID+"/publish-config", mustJSON(t, spec))
		if status != 200 {
			t.Fatalf("second PUT = %d", status)
		}
		assertRefusal(t, f, "benefit_version_mismatch", "benefit_slot")
	})
	t.Run("benefit stock drained", func(t *testing.T) {
		f, w := newCfgFixture(t, true)
		spec := f.seedEnableableWorld(t, 1)
		spec.Reward.StoreIDs = []string{f.store1ID}
		f.owner(t, "PUT", "/api/v1/campaigns/"+f.camp1ID+"/publish-config", mustJSON(t, spec))
		w.exec(t, `UPDATE reward_inventory SET total_qty=0`)
		assertRefusal(t, f, "benefit_stock_missing", "reward_budget")
	})
	t.Run("window conflict", func(t *testing.T) {
		f, _ := newCfgFixture(t, true)
		spec := f.seedEnableableWorld(t, 1)
		spec.Reward.StoreIDs = []string{f.store1ID}
		spec.Reward.ClaimUntil = "2031-06-30T23:59:59Z" // 领取窗越过活动结束(2030-01-01)
		f.owner(t, "PUT", "/api/v1/campaigns/"+f.camp1ID+"/publish-config", mustJSON(t, spec))
		assertRefusal(t, f, "window_conflict", "claim_window")
	})
	t.Run("campaign ended", func(t *testing.T) {
		f, _ := newCfgFixture(t, true)
		spec := f.seedEnableableWorld(t, 1)
		spec.Reward.StoreIDs = []string{f.store1ID}
		// 把整套世界搭到已结束活动上:直接换 campaign 维度成本高,复用
		// campaignPast + 手工内容/批次(世界真值由 store 直建)。
		pack, err := f.s.St.CreateContentPack(f.tenA, f.campPast, "", "过去包", "usr_owner_a")
		if err != nil {
			t.Fatalf("pack: %v", err)
		}
		video := f.createAsset(t, "up-v3", strings.Repeat("1", 64), assetlib.MediaTypeVideo)
		cover := f.createAsset(t, "up-c3", strings.Repeat("2", 64), assetlib.MediaTypeImage)
		ver, err := f.s.St.CreateContentPackVersion(f.tenA, pack.ID, contentpack.VersionSpec{
			ContentKind: contentpack.ContentKindVideo,
			Video:       contentpack.AssetSnapshot{AssetID: video.ID, AssetRef: video.AssetRef, SHA256: video.SHA256},
			Cover:       contentpack.AssetSnapshot{AssetID: cover.ID, AssetRef: cover.AssetRef, SHA256: cover.SHA256},
			Title: "标题", Body: "正文", Topics: []string{"t"},
			StoreName: "旗舰店", PriceNote: "价", ValidNote: "有效期",
			PlatformVersions: []contentpack.PlatformVersion{{Platform: "douyin", Note: "n", SourceVersion: "s"}},
			GrantRefs:        []string{"g"}, Marking: contentpack.Marking{AdRequired: true},
			Source: contentpack.SourceMerchantUpload,
		}, contentpack.StatusCandidate, "usr_owner_a")
		if err != nil {
			t.Fatalf("version: %v", err)
		}
		if _, err := f.s.St.AdoptContentPackVersion(f.tenA, pack.ID, ver.ID, "douyin", "usr_owner_a", time.Now().UTC()); err != nil {
			t.Fatalf("adopt: %v", err)
		}
		spec.ContentVerID = ver.ID
		if _, err := f.s.St.CreateRewardInventory(f.tenA, store.NewRewardInventory{
			CampaignID: f.campPast, Kind: "store_coupon", Name: "旧券",
			BenefitSlot: spec.Reward.Slot, RuleVersion: 1,
			AllowedEvidence: []string{spec.VerificationMode},
			TotalQty:        5, CreatedBy: "usr_owner_a",
		}); err != nil {
			t.Fatalf("inventory: %v", err)
		}
		status, _ := f.owner(t, "PUT", "/api/v1/campaigns/"+f.campPast+"/publish-config", mustJSON(t, spec))
		if status != 200 {
			t.Fatalf("past PUT = %d", status)
		}
		assertRefusalPath(t, f, "/api/v1/campaigns/"+f.campPast+"/publish-config/enable", "campaign_ended")
	})
	t.Run("store mismatch", func(t *testing.T) {
		f, _ := newCfgFixture(t, true)
		spec := f.seedEnableableWorld(t, 1)
		spec.Reward.StoreIDs = []string{"store_ghost"}
		f.owner(t, "PUT", "/api/v1/campaigns/"+f.camp1ID+"/publish-config", mustJSON(t, spec))
		assertRefusal(t, f, "store_mismatch", "store_ids")
	})
	t.Run("bound store must be included", func(t *testing.T) {
		f, _ := newCfgFixture(t, true)
		// camp1 绑定 store1;配置只给一个不存在的店 → store_mismatch。
		spec := f.seedEnableableWorld(t, 1)
		spec.Reward.StoreIDs = []string{"store_other"}
		f.owner(t, "PUT", "/api/v1/campaigns/"+f.camp1ID+"/publish-config", mustJSON(t, spec))
		assertRefusal(t, f, "store_mismatch", "store_ids")
	})
}

func assertRefusal(t *testing.T, f *configFixture, reason, field string) {
	t.Helper()
	assertRefusalPath(t, f, "/api/v1/campaigns/"+f.camp1ID+"/publish-config/enable", reason, field)
}

func assertRefusalPath(t *testing.T, f *configFixture, path, reason string, field ...string) {
	t.Helper()
	status, out := f.owner(t, "POST", path, ``)
	if status != 422 {
		t.Fatalf("enable = %d %v, want 422 %s", status, out, reason)
	}
	if out["error"] != reason {
		t.Fatalf("enable reason = %v, want %s", out["error"], reason)
	}
	if len(field) > 0 && out["field"] != field[0] {
		t.Fatalf("enable field = %v, want %s", out["field"], field[0])
	}
	// 拒绝后配置仍是草稿,绝无半启用状态。
	gstatus, gout := f.owner(t, "GET", strings.TrimSuffix(path, "/enable"), ``)
	if gstatus != 200 || gout["status"] != "draft" {
		t.Fatalf("refused enable left status %v (%d)", gout["status"], gstatus)
	}
}

func TestPublishConfigEnableHappyPathSnapshotAndPause(t *testing.T) {
	f, w := newCfgFixture(t, true)
	spec := f.seedEnableableWorld(t, 1)
	spec.Reward.StoreIDs = []string{f.store1ID}
	if status, out := f.owner(t, "PUT", "/api/v1/campaigns/"+f.camp1ID+"/publish-config", mustJSON(t, spec)); status != 200 {
		t.Fatalf("PUT = %d %v", status, out)
	}

	// 草稿期快照 = not_enabled(不制造已承诺假象)。
	status, out := f.owner(t, "GET", "/api/v1/campaigns/"+f.camp1ID+"/publish-config/snapshot", ``)
	if status != 409 || out["state"] != "not_enabled" {
		t.Fatalf("draft snapshot = %d %v, want 409 not_enabled", status, out)
	}

	status, out = f.owner(t, "POST", "/api/v1/campaigns/"+f.camp1ID+"/publish-config/enable", ``)
	if status != 200 || out["status"] != "enabled" {
		t.Fatalf("enable = %d %v", status, out)
	}
	if out["enabled_version"].(float64) != 1 {
		t.Fatalf("enabled_version = %v, want 1", out["enabled_version"])
	}

	// 幂等重放:再次 enable 仍 200,修订不重复。
	status, _ = f.owner(t, "POST", "/api/v1/campaigns/"+f.camp1ID+"/publish-config/enable", ``)
	if status != 200 {
		t.Fatalf("replay enable = %d", status)
	}

	// G3 快照契约:rewardops.Snapshot 同形。
	status, out = f.owner(t, "GET", "/api/v1/campaigns/"+f.camp1ID+"/publish-config/snapshot", ``)
	if status != 200 {
		t.Fatalf("snapshot = %d %v", status, out)
	}
	if out["campaign_id"] != f.camp1ID || out["version"].(float64) != 1 {
		t.Fatalf("snapshot identity = %v %v", out["campaign_id"], out["version"])
	}
	if ev := out["allowed_evidence"].([]any); len(ev) != 1 || ev[0] != campaignenable.ModeStaffObserved {
		t.Fatalf("allowed_evidence = %v", out["allowed_evidence"])
	}
	if slots := out["slots"].([]any); len(slots) != 1 || slots[0] != "store_coupon_main" {
		t.Fatalf("slots = %v", out["slots"])
	}
	disc := out["disclosure"].(map[string]any)
	if disc["rule_version"].(float64) != 1 || disc["customer_copy"] != spec.CustomerCopy {
		t.Fatalf("disclosure = %v", disc)
	}

	// 紧急暂停:配置 paused、快照 409、参与表零触碰。
	before := w.count(t, `SELECT COUNT(*) FROM participations`)
	status, out = f.owner(t, "POST", "/api/v1/campaigns/"+f.camp1ID+"/publish-config/pause", ``)
	if status != 200 || out["status"] != "paused" {
		t.Fatalf("pause = %d %v", status, out)
	}
	after := w.count(t, `SELECT COUNT(*) FROM participations`)
	if before != after {
		t.Fatalf("pause touched participations: %d -> %d", before, after)
	}
	status, out = f.owner(t, "GET", "/api/v1/campaigns/"+f.camp1ID+"/publish-config/snapshot", ``)
	if status != 409 || out["state"] != "not_enabled" {
		t.Fatalf("paused snapshot = %d %v", status, out)
	}

	// 修订留痕:create, enable, pause。
	status, out = f.owner(t, "GET", "/api/v1/campaigns/"+f.camp1ID+"/publish-config/revisions", ``)
	items := out["items"].([]any)
	var actions []string
	for _, it := range items {
		actions = append(actions, it.(map[string]any)["action"].(string))
	}
	if len(actions) != 3 || actions[0] != "create" || actions[1] != "enable" || actions[2] != "pause" {
		t.Fatalf("trail = %v", actions)
	}
}
