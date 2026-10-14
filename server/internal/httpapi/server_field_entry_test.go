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
	"image/png"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

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
