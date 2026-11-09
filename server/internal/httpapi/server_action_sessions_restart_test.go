package httpapi

// T6 重启持久化验收(会话面):真实 SQLite 文件库 + 真实 httpapi.Server,
// 杀进程(关库)后重启:同一会话/冻结快照/凭据消费记录原样保留;
// 重放保护在库里,不在内存。

import (
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

	"github.com/bianjiefilm/touch-engine/server/internal/campaign"
	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/participation"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

func TestActionSessionSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "restart.db")

	// 目录桩跨重启持有同一动作引用(生产由 4584 接线;重启不换绑定)。
	var dirStub *stubASDirectory
	newSrv := func() (*Server, *httptest.Server) {
		t.Helper()
		d, err := db.Open(dbPath)
		mustNoErr(t, err)
		cfg := config.Load(func(k string) string {
			switch k {
			case "TOUCH_INTERNAL_TOKEN":
				return "restart-secret"
			case "FEATURE_PARTICIPATION":
				return "on"
			case "PUBLIC_BASE_URL":
				return "https://h5.example.com"
			}
			return ""
		})
		s := New(cfg, d, nil, nil, log.New(io.Discard, "", 0))
		if dirStub != nil {
			s.Participation.SetActionDirectory(dirStub)
		}
		ts := httptest.NewServer(s.Handler())
		return s, ts
	}
	// call: restart-secret 内部头 + cookie 的最小请求助手。
	call := func(ts *httptest.Server, method, path, cookie, body string) (int, http.Header, map[string]any) {
		t.Helper()
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		req, err := http.NewRequest(method, ts.URL+path, rd)
		mustNoErr(t, err)
		req.Header.Set("X-Internal-Token", "restart-secret")
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := ts.Client().Do(req)
		mustNoErr(t, err)
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return res.StatusCode, res.Header, out
	}

	s1, ts1 := newSrv()
	// 直接经 store 造租户/门店/活动,目录引用真实 campaign。
	st := store.New(s1.St.DB)
	ten, err := st.CreateTenant("会话重启商家")
	mustNoErr(t, err)
	sto, err := st.CreateStore(ten.ID, "会话重启店", "", "test")
	mustNoErr(t, err)
	now := time.Now().UTC()
	cmp, err := st.CreateCampaign(store.NewCampaign{
		TenantID: ten.ID, Title: "会话重启活动", PublicContent: "内容",
		StartsAt: now.Add(-time.Hour).Format(time.RFC3339),
		EndsAt:   now.Add(48 * time.Hour).Format(time.RFC3339),
		StoreID:  sto.ID, CreatedBy: "test",
	})
	mustNoErr(t, err)
	_, err = st.TransitionCampaign(cmp.ID, ten.ID, campaign.StatusActive)
	mustNoErr(t, err)

	dirStub = &stubASDirectory{ok: true, ref: participation.ActionRef{
		ActionID: "act_001", CatalogCode: "A04", Platform: "wechat", Kind: "share",
		TenantID: ten.ID, StoreID: sto.ID,
		StorefrontRevision: 5, ActionRevision: 12, RuleVersion: 3, RuleSnapshot: `{"v":3}`,
		ContentPackVersion: "cp_v7",
		CampaignID:         cmp.ID,
		Window:             participation.Window{StartsAt: cmp.StartsAt, ExpiresAt: cmp.EndsAt},
		Enabled:            true,
	}}
	// 注意:newSrv 里 dirStub 尚为 nil 时构造的第一个 Server 需要补接线。
	s1.Participation.SetActionDirectory(dirStub)

	// 第一次生命:创建(冻结快照)+ 签发恢复凭据。
	status, hdr, body := call(ts1, "POST", "/api/v1/public/actions/act_001/sessions", "", sessionCreateBody(""))
	mustEqual(t, status, http.StatusCreated)
	cookieA := cookiePair(hdr)
	sid := body["action_session_id"].(string)
	status, _, cred := call(ts1, "POST", "/api/v1/public/action-sessions/"+sid+"/resume-credentials", cookieA, "")
	mustEqual(t, status, http.StatusCreated)
	token := cred["token"].(string)

	// 杀进程:关 HTTP 与库句柄。
	ts1.Close()
	s1.Close()

	// 重启:同一 SQLite 文件,新 Server(目录桩原样)。
	s2, ts2 := newSrv()
	t.Cleanup(func() { ts2.Close(); s2.Close() })

	// 同浏览器(cookieA)恢复同一会话:冻结版本原样,不建新行。
	status, _, restored := call(ts2, "POST", "/api/v1/public/actions/act_001/sessions", cookieA, sessionCreateBody(""))
	mustEqual(t, status, http.StatusOK)
	if restored["action_session_id"] != sid {
		t.Fatalf("restored session = %v, want %s", restored["action_session_id"], sid)
	}
	if restored["storefront_revision"] != float64(5) || restored["action_revision"] != float64(12) {
		t.Fatalf("frozen snapshot drifted across restart: %v", restored)
	}

	// 凭据跨重启仍可单次兑换(消费记录在库里)。
	status, hdrB, ex := call(ts2, "POST", "/api/v1/public/resume/exchange", "", fmt.Sprintf(`{"token":%q}`, token))
	mustEqual(t, status, http.StatusOK)
	if ex["action_session_id"] != sid {
		t.Fatalf("exchanged session = %v, want %s", ex["action_session_id"], sid)
	}
	cookieB := cookiePair(hdrB)

	// 重放跨重启仍拒绝(used_at 落库)。
	status, _, _ = call(ts2, "POST", "/api/v1/public/resume/exchange", "", fmt.Sprintf(`{"token":%q}`, token))
	mustEqual(t, status, http.StatusConflict)

	// 换绑后的浏览器 B 能读同一会话;旧浏览器 A 不再能读。
	status, _, _ = call(ts2, "GET", "/api/v1/public/action-sessions/"+sid+"/status", cookieB, "")
	mustEqual(t, status, http.StatusOK)
	status, _, _ = call(ts2, "GET", "/api/v1/public/action-sessions/"+sid+"/status", cookieA, "")
	mustEqual(t, status, http.StatusNotFound)
}
