package httpapi

// HUI-1663 实体NFC与门店部署验收 —— 工程准备(可机验部分)。
// 硬件项(真实写卡/回读/真机碰触/机型矩阵/抗金属实测)无实机条件,一律
// NOT_VERIFIED(见 docs/evidence/hui-1663/),本文件只钉「仓内确定性语义」:
//   - 稳定 URL → /c/{code} 解析 → 正确活动(CSV 导出列 = QR 载荷 = 同 canonical URL);
//   - 换绑工作流:换绑 = URL 变、旧卡(URL 不变)仍指旧活动、原参与保留原权益;
//   - 单卡停用不影响其他卡(per-card link);共享 link 别名语义如实钉住;
//   - 暂停/到期/损坏码(坏短码)/缓存失效(管理面写 → 下一次公共请求可见)/旧码兼容。

import (
	"bytes"
	"fmt"
	"image/png"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/makiuchi-d/gozxing"
	qrcode "github.com/makiuchi-d/gozxing/qrcode"

	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/identity"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

// fixtureField:fixtureWithQR 的登记制开关变体 —— extra 里的 env 键值
// (FEATURE_* 开关)优先于默认夹具环境,其余完全同形。
func fixtureField(t *testing.T, extra map[string]string) *fixture {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	st := store.New(d)

	tenA, err := st.CreateTenant("商家A")
	mustNoErr(t, err)
	tenB, err := st.CreateTenant("商家B")
	mustNoErr(t, err)
	for _, m := range []struct{ tenant, principal, role string }{
		{tenA.ID, "usr_owner_a", "org_owner"},
		{tenA.ID, "usr_staff_a", "staff"},
		{tenB.ID, "usr_owner_b", "org_owner"},
	} {
		if _, err := st.CreateMember(m.tenant, m.principal, m.role, m.principal, "test", true); err != nil {
			t.Fatalf("seed member: %v", err)
		}
	}

	cfg := config.Load(func(k string) string {
		if v, ok := extra[k]; ok {
			return v
		}
		switch k {
		case "TOUCH_INTERNAL_TOKEN":
			return "test-internal-secret"
		case "PLATFORM_IDENTITY_BASE_URL":
			return "http://identity.test"
		case "PLATFORM_IDENTITY_TOKEN":
			return "identity-token"
		case "PUBLIC_BASE_URL":
			return "https://h5.test"
		}
		return ""
	})

	idsrv := fakeIdentity(t, map[string]identitySession{
		"sess-owner-a": {"usr_owner_a", "a@example.com"},
		"sess-staff-a": {"usr_staff_a", "staff@example.com"},
		"sess-owner-b": {"usr_owner_b", "b@example.com"},
	})
	idc := &identity.Client{BaseURL: idsrv.URL, Token: "identity-token", AppID: "touch-engine", HTTP: idsrv.Client()}
	upc := &upload.Client{BaseURL: "http://upload.test", Token: "upload-token", AppID: "touch-engine"}
	s := New(cfg, d, idc, upc, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return &fixture{s: s, ts: ts, tenA: tenA.ID, tenB: tenB.ID,
		ownA: "usr_owner_a", ownB: "usr_owner_b", staffA: "usr_staff_a"}
}

// fieldPublicGet:公共短码面 GET —— 返回 (status, machine state, view)。
func fieldPublicGet(t *testing.T, f *fixture, code string) (int, string, map[string]any) {
	t.Helper()
	status, _, view := f.do(t, "GET", "/api/v1/public/links/"+code, "", "", "")
	state, _ := view["state"].(string)
	return status, state, view
}

// fieldCSVURLs:导出 CSV 并回读 url 列 —— 与现场从 1665 管理面取稳定入口同一步骤。
func fieldCSVURLs(t *testing.T, f *fixture) []string {
	t.Helper()
	status, _, raw := f.doRaw(t, "GET", "/api/v1/nfc/tags/export.csv", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	recs := parseCSV(t, raw)
	urls := make([]string, 0, len(recs)-1)
	for _, rec := range recs[1:] {
		urls = append(urls, rec[2])
	}
	return urls
}

// codeFromEntryURL:从 canonical URL 提取短码(<base>/c/<code>) —— 手机打开
// URL 后服务端路由实际拿到的段。
func codeFromEntryURL(t *testing.T, entryURL string) string {
	t.Helper()
	const marker = "/c/"
	i := strings.LastIndex(entryURL, marker)
	if i < 0 {
		t.Fatalf("entry url %q has no %s segment", entryURL, marker)
	}
	return entryURL[i+len(marker):]
}

// decodeQRPayload:PNG 解码回读 QR 载荷(照 server_qr_test.go 的解码写法)。
func decodeQRPayload(t *testing.T, raw []byte) string {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(raw))
	mustNoErr(t, err)
	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	mustNoErr(t, err)
	res, err := qrcode.NewQRCodeReader().Decode(bmp, nil)
	mustNoErr(t, err)
	return res.GetText()
}

// TestFieldEntryChainFromCSVURLToCampaign:现场取号→写卡→手机打开 的仓内等价链。
// CSV url 列(qrentry.PublicEntryURL) → /c/{code} → 公共解析 → 正确活动;
// 同一 code 的 QR 兜底与 NFC 写入共用同一 canonical URL(旧码兼容的根基)。
func TestFieldEntryChainFromCSVURLToCampaign(t *testing.T) {
	f := fixtureWithQR(t, nil)
	_, campaignID, linkIDs, codes := seedNFCTenantCampaign(t, f, 1)

	status, _, _ := f.do(t, "POST", "/api/v1/nfc/tags/batch", "sess-owner-a", f.tenA,
		batchReq(campaignID, linkIDs[:1], 1, "", "", "桌贴-A01", "shared"))
	mustEqual(t, status, http.StatusCreated)

	urls := fieldCSVURLs(t, f)
	if len(urls) != 1 {
		t.Fatalf("csv urls = %v", urls)
	}
	// canonical:与 qrentry 构造逐字节一致,无 query/fragment(纯短码红线)
	want := "https://h5.test/c/" + codes[0]
	if urls[0] != want {
		t.Fatalf("entry url = %q, want %q", urls[0], want)
	}

	// 手机打开 → 服务端按 path 段解析 → 正确活动
	code := codeFromEntryURL(t, urls[0])
	if code != codes[0] {
		t.Fatalf("code from url = %q, want %q", code, codes[0])
	}
	got, state, view := fieldPublicGet(t, f, code)
	mustEqual(t, got, http.StatusOK)
	if state != "available" || view["title"] != "周年庆" {
		t.Fatalf("public = %d %v", got, view)
	}
	// 归属可见:游客能看到商家名(卡被摆错门店时的归属判据)
	if view["merchant_name"] != "商家A" {
		t.Fatalf("merchant = %v", view["merchant_name"])
	}

	// QR 兜底与 NFC 同 URL:PNG 载荷解码 == CSV url 列
	status, _, rawPNG := f.doRaw(t, "GET", "/api/v1/campaigns/"+campaignID+"/links/"+linkIDs[0]+"/qrcode", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	if payload := decodeQRPayload(t, rawPNG); payload != want {
		t.Fatalf("qr payload = %q, want %q", payload, want)
	}
}

// seedFieldCampaignWithStore:建门店 + 绑定该门店的 active 活动 + nLinks 条短码。
// 参与出生即绑门店(participations.store_id NOT NULL),换绑工作流的活动必须带店。
func seedFieldCampaignWithStore(t *testing.T, f *fixture, title string, nLinks int) (campaignID string, linkIDs, codes []string) {
	t.Helper()
	status, _, st := f.do(t, "POST", "/api/v1/stores", "sess-owner-a", f.tenA, `{"name":"旗舰店","address":"北京市"}`)
	mustEqual(t, status, http.StatusCreated)
	storeID := st["id"].(string)

	now := time.Now().UTC()
	payload := fmt.Sprintf(`{"title":%q,"public_content":"到店有礼","starts_at":%q,"ends_at":%q,"store_id":%q}`,
		title, now.Add(-1*time.Hour).Format(time.RFC3339), now.Add(48*time.Hour).Format(time.RFC3339), storeID)
	status, _, cmp := f.do(t, "POST", "/api/v1/campaigns", "sess-owner-a", f.tenA, payload)
	mustEqual(t, status, http.StatusCreated)
	campaignID = cmp["id"].(string)
	for i := 0; i < nLinks; i++ {
		status, _, lnk := f.do(t, "POST", "/api/v1/campaigns/"+campaignID+"/links", "sess-owner-a", f.tenA, "")
		mustEqual(t, status, http.StatusCreated)
		linkIDs = append(linkIDs, lnk["id"].(string))
		codes = append(codes, lnk["code"].(string))
	}
	status, _, _ = f.do(t, "POST", "/api/v1/campaigns/"+campaignID+"/status", "sess-owner-a", f.tenA, `{"status":"active"}`)
	mustEqual(t, status, http.StatusOK)
	return campaignID, linkIDs, codes
}

// TestFieldRebindWorkflowOldCardKeepsOldTarget:旧(标签/短码)路径的后台换绑工作流。
// 语义(1665 拍板「绑定以 link 为准」):换绑 = URL 变 —— 物理卡 URL 不变时,
// 访客仍进旧目标;原参与(旧活动)保留原规则权益,不受换绑影响。
// 因此旧路径换绑后必须重写卡/重印码;「同卡换活动、卡内URL不变」由 1665 增量的
// 稳定入口 /t/{code}(TouchpointAlias)承担,见 TestFieldRebindStableURLKeepsOriginalParticipation。
func TestFieldRebindWorkflowOldCardKeepsOldTarget(t *testing.T) {
	f := fixtureField(t, map[string]string{"FEATURE_PARTICIPATION": "on"})
	campaignA, linkIDsA, _ := seedFieldCampaignWithStore(t, f, "周年庆", 1)

	// 卡已按 URL_A 写出(仓内等价:导出 CSV 取号)
	status, _, batch := f.do(t, "POST", "/api/v1/nfc/tags/batch", "sess-owner-a", f.tenA,
		batchReq(campaignA, linkIDsA[:1], 1, "", "", "桌贴-A01", "shared"))
	mustEqual(t, status, http.StatusCreated)
	tagID := batch["items"].([]any)[0].(map[string]any)["id"].(string)
	urlA := fieldCSVURLs(t, f)[0]
	codeA := codeFromEntryURL(t, urlA)

	// 顾客通过旧卡参与(campaign A)
	status, hdr, started := f.doCookie(t, "POST", "/api/v1/participations", "", startBody(codeA, "idem-field-1", "cp_v1"))
	mustEqual(t, status, http.StatusCreated)
	partID := started["id"].(string)

	// 第二活动 B + link,把 tag 换绑过去
	status, _, c2 := f.do(t, "POST", "/api/v1/campaigns", "sess-owner-a", f.tenA, `{"title":"春季展"}`)
	mustEqual(t, status, http.StatusCreated)
	campaignB := c2["id"].(string)
	status, _, l2 := f.do(t, "POST", "/api/v1/campaigns/"+campaignB+"/links", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusCreated)
	linkB := l2["id"].(string)
	status, _, _ = f.do(t, "POST", "/api/v1/campaigns/"+campaignB+"/status", "sess-owner-a", f.tenA, `{"status":"active"}`)
	mustEqual(t, status, http.StatusOK)
	status, _, _ = f.do(t, "PATCH", "/api/v1/nfc/tags/"+tagID, "sess-owner-a", f.tenA, `{"link_id":"`+linkB+`"}`)
	mustEqual(t, status, http.StatusOK)

	// 换绑后:管理面导出的新 URL 指向 B(旧路径换绑=换 URL,需重写卡;
	// 同 URL 换标走 /t 稳定入口,见 TestFieldRebindStableURLKeepsOriginalParticipation)
	urlB := codeFromEntryURL(t, fieldCSVURLs(t, f)[0])
	if urlB == codeA {
		t.Fatal("legacy tag rebind must change the exported url")
	}
	got, state, view := fieldPublicGet(t, f, urlB)
	mustEqual(t, got, http.StatusOK)
	if state != "available" || view["title"] != "春季展" {
		t.Fatalf("new target = %d %v", got, view)
	}

	// 旧卡(URL 不变)仍指旧活动 —— 换绑不会把旧卡上的访客带去新活动
	got, state, view = fieldPublicGet(t, f, codeA)
	mustEqual(t, got, http.StatusOK)
	if state != "available" || view["title"] != "周年庆" {
		t.Fatalf("old card after rebind = %d %v", got, view)
	}

	// 原参与保留原权益:同访客 lookup 仍恢复旧 link 上的原参与
	cookie := cookiePair(hdr)
	status, _, looked := f.doCookie(t, "POST", "/api/v1/participations/lookup", cookie, `{"link_code":"`+codeA+`"}`)
	mustEqual(t, status, http.StatusOK)
	if looked["id"] != partID || looked["link_code"] != codeA {
		t.Fatalf("lookup = %v, want id %s on link %s", looked, partID, codeA)
	}
}

// TestFieldSingleCardDisableIsolation:per-card 1:1 link(rotate)时,停用一张卡
// 只影响它自己的入口 —— 现场规程「一卡一链」的依据(票面「单卡停用不影响其他卡」)。
func TestFieldSingleCardDisableIsolation(t *testing.T) {
	f := fixtureWithQR(t, nil)
	_, campaignID, linkIDs, codes := seedNFCTenantCampaign(t, f, 2)

	status, _, batch := f.do(t, "POST", "/api/v1/nfc/tags/batch", "sess-owner-a", f.tenA,
		batchReq(campaignID, linkIDs, 2, "", "", "桌贴", "rotate"))
	mustEqual(t, status, http.StatusCreated)
	items := batch["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("batch = %v", items)
	}
	tag1 := items[0].(map[string]any)["id"].(string)

	// 初始两卡都 available
	for _, c := range codes {
		got, state, _ := fieldPublicGet(t, f, c)
		mustEqual(t, got, http.StatusOK)
		if state != "available" {
			t.Fatalf("card %s = %s", c, state)
		}
	}

	// 停用卡 1 → 只有卡 1 的短码进入停用态,单字段形状(无内容泄漏)
	status, _, _ = f.do(t, "POST", "/api/v1/nfc/tags/"+tag1+"/status", "sess-owner-a", f.tenA, `{"status":"disabled"}`)
	mustEqual(t, status, http.StatusOK)
	got, state, view := fieldPublicGet(t, f, codes[0])
	mustEqual(t, got, http.StatusNotFound)
	if state != "link_disabled" || len(view) != 1 {
		t.Fatalf("card1 = %d %v", got, view)
	}
	got, state, _ = fieldPublicGet(t, f, codes[1])
	mustEqual(t, got, http.StatusOK)
	if state != "available" {
		t.Fatalf("card2 affected: %s", state)
	}

	// 恢复 → 复用
	status, _, _ = f.do(t, "POST", "/api/v1/nfc/tags/"+tag1+"/status", "sess-owner-a", f.tenA, `{"status":"active"}`)
	mustEqual(t, status, http.StatusOK)
	got, state, _ = fieldPublicGet(t, f, codes[0])
	mustEqual(t, got, http.StatusOK)
	if state != "available" {
		t.Fatalf("card1 restore = %s", state)
	}
}

// TestFieldSharedLinkAliasSemantics:两标签共享同一 link 时,停用其一即停用
// 共享入口(同一入口的别名,1665 拍板)—— 检查表据此强制「一卡一链」。
func TestFieldSharedLinkAliasSemantics(t *testing.T) {
	f := fixtureWithQR(t, nil)
	_, campaignID, linkIDs, codes := seedNFCTenantCampaign(t, f, 1)

	status, _, batch := f.do(t, "POST", "/api/v1/nfc/tags/batch", "sess-owner-a", f.tenA,
		batchReq(campaignID, linkIDs[:1], 2, "", "", "别名", "shared"))
	mustEqual(t, status, http.StatusCreated)
	items := batch["items"].([]any)
	tag1 := items[0].(map[string]any)["id"].(string)

	status, _, _ = f.do(t, "POST", "/api/v1/nfc/tags/"+tag1+"/status", "sess-owner-a", f.tenA, `{"status":"disabled"}`)
	mustEqual(t, status, http.StatusOK)
	got, state, _ := fieldPublicGet(t, f, codes[0])
	mustEqual(t, got, http.StatusNotFound)
	if state != "link_disabled" {
		t.Fatalf("shared alias disable = %s", state)
	}
}

// TestFieldDisableVisibleNextRequestWithCache:FEATURE_PUBLIC_CACHE 开启时,
// 标签停用(epoch bump)对下一次公共请求立即可见 —— 检查表「缓存失效」项依据。
func TestFieldDisableVisibleNextRequestWithCache(t *testing.T) {
	f := fixtureField(t, map[string]string{"FEATURE_PUBLIC_CACHE": "on"})
	_, campaignID, linkIDs, codes := seedNFCTenantCampaign(t, f, 1)

	status, _, batch := f.do(t, "POST", "/api/v1/nfc/tags/batch", "sess-owner-a", f.tenA,
		batchReq(campaignID, linkIDs[:1], 1, "", "", "", "shared"))
	mustEqual(t, status, http.StatusCreated)
	tagID := batch["items"].([]any)[0].(map[string]any)["id"].(string)

	// 先打一次进缓存
	got, state, _ := fieldPublicGet(t, f, codes[0])
	mustEqual(t, got, http.StatusOK)
	if state != "available" {
		t.Fatalf("pre-cache = %s", state)
	}

	// 停用 → 下一次请求(命中路径)必须已是停用态,不许吃旧缓存
	status, _, _ = f.do(t, "POST", "/api/v1/nfc/tags/"+tagID+"/status", "sess-owner-a", f.tenA, `{"status":"disabled"}`)
	mustEqual(t, status, http.StatusOK)
	got, state, _ = fieldPublicGet(t, f, codes[0])
	mustEqual(t, got, http.StatusNotFound)
	if state != "link_disabled" {
		t.Fatalf("cached disable = %s (stale cache served)", state)
	}
}

// TestFieldPauseExpireAndPrintedURLStability:现场「暂停/到期/旧二维码兼容」的
// 仓内语义 —— 已印刷/已写卡物料上的 URL 永不因管理面编辑而变化;状态全部由
// 解析时点重导出(到期靠时间窗,不靠配置冻结);损坏卡读出的坏短码得到统一
// 404 单字段形状(不区分,防探测)。
func TestFieldPauseExpireAndPrintedURLStability(t *testing.T) {
	f := fixtureWithQR(t, nil)
	now := time.Now().UTC()
	status, _, st := f.do(t, "POST", "/api/v1/stores", "sess-owner-a", f.tenA, `{"name":"旗舰店","address":"北京市"}`)
	mustEqual(t, status, http.StatusCreated)
	payload := fmt.Sprintf(`{"title":"周年庆","public_content":"到店有礼","starts_at":%q,"ends_at":%q,"store_id":%q}`,
		now.Add(-1*time.Hour).Format(time.RFC3339), now.Add(48*time.Hour).Format(time.RFC3339), st["id"].(string))
	status, _, cmp := f.do(t, "POST", "/api/v1/campaigns", "sess-owner-a", f.tenA, payload)
	mustEqual(t, status, http.StatusCreated)
	campaignID := cmp["id"].(string)
	status, _, lnk := f.do(t, "POST", "/api/v1/campaigns/"+campaignID+"/links", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusCreated)
	code := lnk["code"].(string)
	status, _, _ = f.do(t, "POST", "/api/v1/campaigns/"+campaignID+"/status", "sess-owner-a", f.tenA, `{"status":"active"}`)
	mustEqual(t, status, http.StatusOK)

	// 暂停 → paused 明确提示(非 available、也非 not_found)
	status, _, _ = f.do(t, "POST", "/api/v1/campaigns/"+campaignID+"/status", "sess-owner-a", f.tenA, `{"status":"paused"}`)
	mustEqual(t, status, http.StatusOK)
	got, state, _ := fieldPublicGet(t, f, code)
	mustEqual(t, got, http.StatusNotFound)
	if state != "paused" {
		t.Fatalf("paused = %s", state)
	}
	// 恢复 → 同一 URL 直接复活(旧物料零重印 —— 旧二维码/已写卡兼容)
	status, _, _ = f.do(t, "POST", "/api/v1/campaigns/"+campaignID+"/status", "sess-owner-a", f.tenA, `{"status":"active"}`)
	mustEqual(t, status, http.StatusOK)
	got, state, view := fieldPublicGet(t, f, code)
	mustEqual(t, got, http.StatusOK)
	if state != "available" {
		t.Fatalf("resume = %s", state)
	}

	// 管理面编辑(改标题) → 同一 URL 继续有效,内容更新(印刷物料不用换)
	status, _, _ = f.do(t, "PATCH", "/api/v1/campaigns/"+campaignID, "sess-owner-a", f.tenA, `{"title":"周年庆(延期)"}`)
	mustEqual(t, status, http.StatusOK)
	got, _, view = fieldPublicGet(t, f, code)
	mustEqual(t, got, http.StatusOK)
	if view["title"] != "周年庆(延期)" {
		t.Fatalf("edited title = %v", view["title"])
	}

	// 到期窗口:ends_at 已过 → expired(解析时点重导出)
	payload = fmt.Sprintf(`{"title":"快闪","public_content":"到店有礼","starts_at":%q,"ends_at":%q}`,
		now.Add(-2*time.Hour).Format(time.RFC3339), now.Add(-1*time.Hour).Format(time.RFC3339))
	status, _, c2 := f.do(t, "POST", "/api/v1/campaigns", "sess-owner-a", f.tenA, payload)
	mustEqual(t, status, http.StatusCreated)
	status, _, l2 := f.do(t, "POST", "/api/v1/campaigns/"+c2["id"].(string)+"/links", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusCreated)
	expCode := l2["code"].(string)
	status, _, _ = f.do(t, "POST", "/api/v1/campaigns/"+c2["id"].(string)+"/status", "sess-owner-a", f.tenA, `{"status":"active"}`)
	mustEqual(t, status, http.StatusOK)
	got, state, _ = fieldPublicGet(t, f, expCode)
	mustEqual(t, got, http.StatusNotFound)
	if state != "expired" {
		t.Fatalf("expired = %s", state)
	}

	// 损坏卡:读出错码/截断码 → 统一 404 not_found 单字段形状(防探测)
	got, state, view = fieldPublicGet(t, f, strings.ToUpper(code)+"x")
	mustEqual(t, got, http.StatusNotFound)
	if state != "not_found" || len(view) != 1 {
		t.Fatalf("damaged code = %d %v", got, view)
	}
}

// fieldTPCSVURLs:触点 CSV 导出的 url 列(/t 稳定入口)—— 现场从
// /work/touchpoints 触点管理台取号的同一步骤。
func fieldTPCSVURLs(t *testing.T, f *fixture) []string {
	t.Helper()
	status, _, raw := f.doRaw(t, "GET", "/api/v1/touchpoints/export.csv", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	recs := parseCSV(t, raw)
	urls := make([]string, 0, len(recs)-1)
	for _, rec := range recs[1:] {
		urls = append(urls, rec[2])
	}
	return urls
}

// TestFieldStableEntryChainTouchpointToCampaign:票面受控验证的主链 ——
// 触点管理取得稳定 HTTPS 入口(/t/{code}) → 公共解析放行活动码 →
// 既有 /c/{code} 解析 → 正确活动(标题+商家归属),并沿部署五态把
// 「写入→回读→真机验证」三阶段显式走完(与贴卡检查表阶段一一对应)。
func TestFieldStableEntryChainTouchpointToCampaign(t *testing.T) {
	f := newTPFixture(t, map[string]string{"FEATURE_TOUCHPOINT_ENTRY": "on"})
	_, _, code1, _, _, _, tp1, _ := tpSeedScene(t, f)

	// 取号:触点 CSV 的 url 列 = https://h5.test/t/{别名码},short_code 列 = 别名码
	urls := fieldTPCSVURLs(t, f.fixture)
	if len(urls) == 0 {
		t.Fatal("touchpoint csv has no rows")
	}
	entryURL := "https://h5.test/t/" + tpAliasCode(t, f, tp1)
	found := false
	for _, u := range urls {
		if u == entryURL {
			found = true
		}
	}
	if !found {
		t.Fatalf("csv urls %v missing stable entry %q", urls, entryURL)
	}

	// 部署五态随现场阶段显式单步推进(写卡→回读→真机验证),不冒充跳级
	for _, want := range []string{"awaiting_write", "written_awaiting_readback", "readback_done", "device_verified"} {
		status, _, _ := f.do(t, "POST", "/api/v1/touchpoints/"+tp1+"/stage", "sess-owner-a", f.tenA,
			`{"stage":"`+want+`","note":"现场阶段推进"}`)
		mustEqual(t, status, http.StatusOK)
	}

	// 顾客手机打开 /t/{code} → 公共解析:200 {state:available, code:活动短码}
	got, _, view := f.do(t, "GET", "/api/v1/public/touchpoints/"+tpAliasCode(t, f, tp1), "", "", "")
	mustEqual(t, got, http.StatusOK)
	if view["state"] != "available" || view["code"] != code1 {
		t.Fatalf("stable entry = %v, want available+%s", view, code1)
	}

	// 白名单临时跳转的落点:既有 /c/{code} → 正确活动 + 商家归属
	target, ok := view["code"].(string)
	if !ok {
		t.Fatalf("code = %v", view["code"])
	}
	got, state, view := fieldPublicGet(t, f.fixture, target)
	mustEqual(t, got, http.StatusOK)
	if state != "available" || view["title"] != "周年庆" {
		t.Fatalf("landing page = %d %v", got, view)
	}
	if view["merchant_name"] != "商家A" {
		t.Fatalf("merchant = %v", view["merchant_name"])
	}
}

// TestFieldRebindStableURLKeepsOriginalParticipation:票面「同卡后台换活动后
// 新访客进入新目标、原参与保留原规则权益;卡内URL不变」的可机验全链 ——
// 稳定入口别名(TouchpointAlias)的换绑语义:
//   - 换绑前后 /t URL 逐字节不变(卡不用重写);
//   - 新访客(以及同一张卡)解析到新活动的短码;
//   - 原参与仍在旧 link 上,lookup 原样恢复(权益不动);
//   - 旧短码仍 available(旧二维码/旧卡兼容,双入口并存)。
func TestFieldRebindStableURLKeepsOriginalParticipation(t *testing.T) {
	f := newTPFixture(t, map[string]string{
		"FEATURE_TOUCHPOINT_ENTRY": "on",
		"FEATURE_PARTICIPATION":    "on",
	})
	camp1, camp2, code1, _, _, _, tp1, _ := tpSeedScene(t, f)

	// 卡上印着的稳定 URL(取自管理面详情,与 CSV/QR 同源)
	before := tpDetail(t, f, tp1)
	stableURL, _ := before["url"].(string)
	if stableURL == "" {
		t.Fatalf("detail url missing: %v", before)
	}

	// 顾客经稳定入口解析到旧目标并开始参与(/t → code1 → /c 链)
	got, _, view := f.do(t, "GET", "/api/v1/public/touchpoints/"+tpAliasCode(t, f, tp1), "", "", "")
	mustEqual(t, got, http.StatusOK)
	oldCode, _ := view["code"].(string)
	if oldCode != code1 {
		t.Fatalf("pre-rebind resolve = %v, want %s", view, code1)
	}
	status, hdr, started := f.doCookie(t, "POST", "/api/v1/participations", "", startBody(oldCode, "idem-stable-1", "cp_v1"))
	mustEqual(t, status, http.StatusCreated)
	partID := started["id"].(string)
	cookie := cookiePair(hdr)

	// 后台换活动:预览 → 确认(expected 乐观并发)
	status, _, pv := f.do(t, "POST", "/api/v1/touchpoints/"+tp1+"/rebind-preview", "sess-owner-a", f.tenA,
		`{"campaign_id":"`+camp2+`"}`)
	mustEqual(t, status, http.StatusOK)
	if pv["started_participations"].(float64) != 1 {
		t.Fatalf("preview impact = %v, want 1", pv["started_participations"])
	}
	status, _, after := f.do(t, "POST", "/api/v1/touchpoints/"+tp1+"/rebind", "sess-owner-a", f.tenA,
		`{"campaign_id":"`+camp2+`","expected_campaign_id":"`+camp1+`"}`)
	mustEqual(t, status, http.StatusOK)

	// 卡内 URL 不变:换绑后详情 url 与换绑前逐字节一致
	if after["url"] != stableURL {
		t.Fatalf("rebind changed the printed url: %q -> %q", stableURL, after["url"])
	}

	// 同一张卡,新访客:解析到新活动的短码 → /c 落新活动
	got, _, view = f.do(t, "GET", "/api/v1/public/touchpoints/"+tpAliasCode(t, f, tp1), "", "", "")
	mustEqual(t, got, http.StatusOK)
	newCode, _ := view["code"].(string)
	if newCode == "" || newCode == code1 {
		t.Fatalf("post-rebind resolve = %v, want a code different from %s", view, code1)
	}
	got, state, view := fieldPublicGet(t, f.fixture, newCode)
	mustEqual(t, got, http.StatusOK)
	if state != "available" || view["title"] != "春季展" {
		t.Fatalf("new landing = %d %v", got, view)
	}

	// 原参与保留原权益:原访客 lookup 仍恢复旧 link 上的原参与
	status, _, looked := f.doCookie(t, "POST", "/api/v1/participations/lookup", cookie, `{"link_code":"`+oldCode+`"}`)
	mustEqual(t, status, http.StatusOK)
	if looked["id"] != partID || looked["link_code"] != oldCode {
		t.Fatalf("lookup = %v, want id %s on link %s", looked, partID, oldCode)
	}

	// 旧短码仍 available:已发出去的旧二维码/旧卡不因换绑失效
	got, state, view = fieldPublicGet(t, f.fixture, oldCode)
	mustEqual(t, got, http.StatusOK)
	if state != "available" || view["title"] != "周年庆" {
		t.Fatalf("old link after rebind = %d %v", got, view)
	}
}
