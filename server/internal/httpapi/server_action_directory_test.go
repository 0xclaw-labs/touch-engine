package httpapi

// server_action_directory_test.go: 中央集成验收(2026-11-10)。
// 生产装配(New)把 HUI-4584 已发布动作目录接进 HUI-3546 ActionDirectory 端口:
//   - 接线后(不注入任何测试桩)会话能解析已启用动作:201 + 冻结真实行修订;
//   - 停用动作 fail-closed:创建 404(端口 !ok → 统一 not-found,反枚举);
//     已有会话的状态读不被拦(旧权益保护),实时位如实回 false;
//   - 未知动作与停用同形 404;
//   - ResolveAction 逐字段来源核对(目录/平台/kind/修订/店面修订/启用)。
//
// 会让本测试失败的生产改动:装配处删掉 SetActionDirectory 接线(会话面回
// 503)、适配器把停用/未发布动作解析成 ok=true(快照掩盖现状)、或字段来源
// 改用客户端输入。

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/identity"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

// newDirectoryFixture: 真实 SQLite + 生产装配(目录经 New 接线,无测试桩)。
// 返回 fixture 与已启用动作 id。
func newDirectoryFixture(t *testing.T) (*fixture, string) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	mustNoErr(t, err)
	t.Cleanup(func() { d.Close() })
	st := store.New(d)

	tenA, err := st.CreateTenant("目录接线商家")
	mustNoErr(t, err)
	if _, err := st.CreateMember(tenA.ID, "usr_owner_a", "org_owner", "usr_owner_a", "test", true); err != nil {
		t.Fatalf("seed member: %v", err)
	}
	stoA, err := st.CreateStore(tenA.ID, "接线一店", "接线址", "usr_owner_a")
	mustNoErr(t, err)

	cfg := config.Load(func(k string) string {
		switch k {
		case "TOUCH_INTERNAL_TOKEN":
			return "test-internal-secret"
		case "PLATFORM_IDENTITY_BASE_URL":
			return "http://identity.test"
		case "PLATFORM_IDENTITY_TOKEN":
			return "identity-token"
		case "FEATURE_PARTICIPATION":
			return "on"
		case "PUBLIC_BASE_URL":
			return "https://h5.example.com"
		}
		return ""
	})
	idsrv := fakeIdentity(t, map[string]identitySession{
		"sess-owner-a": {"usr_owner_a", "a@example.com"},
	})
	idc := &identity.Client{BaseURL: idsrv.URL, Token: "identity-token", AppID: "touch-engine", HTTP: idsrv.Client()}
	s := New(cfg, d, idc, &upload.Client{}, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	// 种一条已启用动作(生产装配的目录必须能解析它)。
	_, items, err := st.ReplaceStoreActions(tenA.ID, stoA.ID, 0, []store.StoreActionInput{{
		CatalogCode: "A17", Label: "门店 Wi-Fi",
		TargetRef: "https://shop.example.com/wifi", Enabled: true,
	}})
	mustNoErr(t, err)
	if len(items) != 1 || items[0].Revision != 1 {
		t.Fatalf("seed action: %d items", len(items))
	}
	return &fixture{s: s, ts: ts, tenA: tenA.ID, ownA: "usr_owner_a"}, items[0].ID
}

func TestActionDirectoryWiringResolvesEnabledAction(t *testing.T) {
	f, actionID := newDirectoryFixture(t)

	// 适配器直查:逐字段来源。
	ref, ok, err := f.s.ResolveAction(context.Background(), actionID)
	if err != nil || !ok {
		t.Fatalf("ResolveAction = %v, %v; want ok", ok, err)
	}
	if ref.ActionID != actionID || ref.TenantID != f.tenA || ref.CatalogCode != "A17" ||
		ref.Platform != "" || ref.Kind != "wifi" || ref.ActionRevision != 1 ||
		ref.StorefrontRevision != 0 || !ref.Enabled || ref.ContentPackVersion != "" {
		t.Fatalf("ref fields wrong: %+v", ref)
	}

	// 接线后会话能解析已启用动作:201 + 冻结真实行修订。
	status, hdr, body := f.doCookie(t, "POST", "/api/v1/public/actions/"+actionID+"/sessions", "", sessionCreateBody(""))
	if status != http.StatusCreated {
		t.Fatalf("create session = %d (%v); directory wiring must resolve enabled actions", status, body)
	}
	cookie := cookiePair(hdr)
	sid, _ := body["action_session_id"].(string)
	if sid == "" || body["action_revision"] != float64(1) || body["resume_available"] != true {
		t.Fatalf("session body wrong: %v", body)
	}

	// 状态读:实时位 true。
	status, _, body = f.doCookie(t, "GET", "/api/v1/public/action-sessions/"+sid+"/status", cookie, "")
	if status != http.StatusOK || body["action_available"] != true || body["resume_available"] != true {
		t.Fatalf("status = %d %v", status, body)
	}
}

func TestActionDirectoryDisabledActionFailsClosed(t *testing.T) {
	f, actionID := newDirectoryFixture(t)
	tenantID, storeID, err := f.s.St.StoreActionScope(actionID)
	mustNoErr(t, err)

	// 停用前的既成事实:先建一个会话。
	status, hdr, body := f.doCookie(t, "POST", "/api/v1/public/actions/"+actionID+"/sessions", "", sessionCreateBody(""))
	if status != http.StatusCreated {
		t.Fatalf("create session = %d (%v)", status, body)
	}
	cookie := cookiePair(hdr)
	sid, _ := body["action_session_id"].(string)

	// 停用动作(原地更新,不换实例)。
	_, _, err = f.s.St.ReplaceStoreActions(tenantID, storeID, 1, []store.StoreActionInput{{
		ID: actionID, CatalogCode: "A17", Label: "门店 Wi-Fi",
		TargetRef: "https://shop.example.com/wifi", Enabled: false,
	}})
	mustNoErr(t, err)

	// 适配器 fail-closed:!ok(端口注释即语义)。
	if _, ok, err := f.s.ResolveAction(context.Background(), actionID); err != nil || ok {
		t.Fatalf("disabled ResolveAction = ok=%v err=%v; want !ok nil", ok, err)
	}

	// 新会话创建:404,与未知动作同形(反枚举)。
	status, _, body = f.doCookie(t, "POST", "/api/v1/public/actions/"+actionID+"/sessions", "", sessionCreateBody(""))
	if status != http.StatusNotFound || body["error"] != "not_found" {
		t.Fatalf("disabled create = %d %v; want 404 not_found", status, body)
	}

	// 既存会话的状态读不被拦(旧权益保护),实时位如实回 false——快照不掩盖现状。
	status, _, body = f.doCookie(t, "GET", "/api/v1/public/action-sessions/"+sid+"/status", cookie, "")
	if status != http.StatusOK || body["action_available"] != false || body["resume_available"] != false {
		t.Fatalf("status after disable = %d %v; want honest false flags", status, body)
	}
}

func TestActionDirectoryUnknownActionFailsClosed(t *testing.T) {
	f, _ := newDirectoryFixture(t)
	status, _, body := f.doCookie(t, "POST", "/api/v1/public/actions/sa_missing012345/sessions", "", sessionCreateBody(""))
	if status != http.StatusNotFound {
		t.Fatalf("unknown action create = %d, want 404", status)
	}
	if body["error"] != "not_found" {
		t.Fatalf("error = %v, want not_found (统一 not-found,反枚举)", body["error"])
	}
}
