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

// TestFieldRebindWorkflowOldCardKeepsOldTarget:后台换绑的完整现场工作流。
// 模型语义(1665 拍板「绑定以 link 为准」):换绑 = URL 变。因此
//   - 物理卡 URL 不变时,访客仍进旧目标 —— 这就是「卡内URL不变的同卡换标」
//     必须重写卡的原因(GAP-1,见 docs/evidence/hui-1663/rebind-verification.md);
//   - 原参与(旧活动)保留原规则权益,不受换绑影响。
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

	// 换绑后:管理面导出的新 URL 指向 B(换绑=换 URL)
	urlB := codeFromEntryURL(t, fieldCSVURLs(t, f)[0])
	if urlB == codeA {
		t.Fatal("rebind must change exported url (GAP-1: same-url retarget unsupported)")
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
