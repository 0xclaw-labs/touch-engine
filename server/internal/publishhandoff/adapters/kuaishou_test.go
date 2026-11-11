package adapters

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/publishhandoff"
)

// fakeKsPlatform is the fixture platform seam (测试入口;fixture 只进测试).
type fakeKsPlatform struct {
	exchangeErr  error
	uploadErr    error
	createErr    error
	queryErr     error
	uploadCalls  int
	createCalls  int
	queryCalls   int
	photoID      string
	uploadRef    string
	queryState   string
	queryPublish bool
	createdVideo []byte
	lastCaption  string
}

func (f *fakeKsPlatform) ExchangeToken(ctx context.Context, code, redirectURI string) (KsToken, error) {
	if f.exchangeErr != nil {
		return KsToken{}, f.exchangeErr
	}
	return KsToken{OpenID: "open_1", AccessToken: "at_1", RefreshToken: "rt_1",
		ExpiresAt: time.Now().Add(time.Hour), Scope: "user_video_publish"}, nil
}

func (f *fakeKsPlatform) UploadMedia(ctx context.Context, token KsToken, filename, contentType string, b []byte) (string, error) {
	f.uploadCalls++
	if f.uploadErr != nil {
		return "", f.uploadErr
	}
	f.createdVideo = b
	return f.uploadRef, nil
}

func (f *fakeKsPlatform) CreatePhoto(ctx context.Context, token KsToken, videoRef, coverRef, caption string) (string, error) {
	f.createCalls++
	f.lastCaption = caption
	if f.createErr != nil {
		return "", f.createErr
	}
	if f.photoID == "" {
		f.photoID = "photo_" + f.uploadRef
	}
	return f.photoID, nil
}

func (f *fakeKsPlatform) QueryPhoto(ctx context.Context, token KsToken, photoID string) (string, bool, map[string]string, error) {
	f.queryCalls++
	if f.queryErr != nil {
		return "", false, nil, f.queryErr
	}
	return f.queryState, f.queryPublish, map[string]string{"official": "1"}, nil
}

func ksRequest(method string, withRefs bool) publishhandoff.PrepareRequest {
	return ksRequestBase(method, withRefs, "https://media.example")
}

func ksRequestBase(method string, withRefs bool, mediaBase string) publishhandoff.PrepareRequest {
	req := douyinRequest(method)
	req.Handoff.ID = "pha_test_ks"
	req.Handoff.Platform = "kuaishou"
	req.Handoff.SessionID = "sess_ks"
	if withRefs {
		req.MediaRefs = map[string]string{"mvid": mediaBase + "/kv", "mcov": mediaBase + "/kc"}
	} else {
		req.MediaRefs = nil
	}
	return req
}

// mediaFixtureServer serves deterministic fake bytes for controlled refs
// (fixture 只进测试入口).
func mediaFixtureServer(t *testing.T) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/kv", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("fake-video-bytes"))
	})
	mux.HandleFunc("/kc", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("fake-cover-bytes"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func ksConfig(p KuaishouPlatform) KuaishouConfig {
	return KuaishouConfig{
		ClientID: "cid_1", ClientSecret: "sec_1", AppRef: "app_ks",
		AuthorizeBase:  "https://open.kuaishou.com/oauth2/authorize",
		ReturnBase:     "https://pub.example.com",
		AuthorizeScope: "user_video_publish,user_video_info",
		Platform:       p,
	}
}

func TestKsPrepareBuildsControlledAuthorizeURL(t *testing.T) {
	d := adapterTestDB(t)
	a := NewKuaishouAdapter(d, ksConfig(&fakeKsPlatform{}))
	res, err := a.Prepare(context.Background(), ksRequest("oauth_server_push", true))
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	act := res.ClientAction
	if act.Type != publishhandoff.ClientActionOAuthConsent || act.Params["phase"] != "authorize" {
		t.Fatalf("client action = %+v", act)
	}
	if !strings.HasPrefix(act.URL, "https://open.kuaishou.com/oauth2/authorize?") {
		t.Fatalf("authorize url = %q", act.URL)
	}
	if !strings.Contains(act.URL, "client_id=cid_1") || !strings.Contains(act.URL, "state=") {
		t.Fatalf("authorize url missing official params: %q", act.URL)
	}
	if strings.Contains(act.URL, "sec_1") || strings.Contains(act.URL, "client_secret") {
		t.Fatal("secret must never appear in the authorize URL")
	}
	var used, scope string
	state := act.Params["state"]
	if err := d.QueryRow(`SELECT used_at, scope FROM kuaishou_oauth_states WHERE state=?`, state).Scan(&used, &scope); err != nil {
		t.Fatalf("state row missing: %v", err)
	}
	if used != "" || scope != "user_video_publish,user_video_info" {
		t.Fatalf("state row wrong: used=%q scope=%q", used, scope)
	}
}

func TestKsPrepareRetryReplaysFreshState(t *testing.T) {
	d := adapterTestDB(t)
	a := NewKuaishouAdapter(d, ksConfig(&fakeKsPlatform{}))
	r1, err := a.Prepare(context.Background(), ksRequest("oauth_server_push", true))
	if err != nil {
		t.Fatalf("prepare1: %v", err)
	}
	r2, err := a.Prepare(context.Background(), ksRequest("oauth_server_push", false))
	if err != nil {
		t.Fatalf("prepare2: %v", err)
	}
	if r1.ClientAction.Params["state"] != r2.ClientAction.Params["state"] {
		t.Fatal("fresh state must be replayed within TTL")
	}
}

func TestKsOAuthReplayRefused(t *testing.T) {
	d := adapterTestDB(t)
	fx := &fakeKsPlatform{}
	a := NewKuaishouAdapter(d, ksConfig(fx))
	res, err := a.Prepare(context.Background(), ksRequest("oauth_server_push", true))
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	state := res.ClientAction.Params["state"]
	if _, err := a.FinishOAuth(context.Background(), "code_1", state); err != nil {
		t.Fatalf("finish oauth: %v", err)
	}
	// 同 state 二次兑换=重放拒绝。
	if _, err := a.FinishOAuth(context.Background(), "code_1", state); err == nil {
		t.Fatal("state replay must be refused")
	}
	// 未知 state 拒绝。
	if _, err := a.FinishOAuth(context.Background(), "code_1", "deadbeef"); err == nil {
		t.Fatal("unknown state must be refused")
	}
}

func TestKsFinishOAuthPersistsServerSideToken(t *testing.T) {
	d := adapterTestDB(t)
	a := NewKuaishouAdapter(d, ksConfig(&fakeKsPlatform{}))
	res, _ := a.Prepare(context.Background(), ksRequest("oauth_server_push", true))
	state := res.ClientAction.Params["state"]
	returnTo, err := a.FinishOAuth(context.Background(), "code_1", state)
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	if !strings.HasPrefix(returnTo, "https://pub.example.com/handoff/pha_test_ks?") {
		t.Fatalf("returnTo = %q", returnTo)
	}
	if !strings.Contains(returnTo, "handoff_id=pha_test_ks") || !strings.Contains(returnTo, "oauth=ok") {
		t.Fatalf("returnTo flags missing: %q", returnTo)
	}
	var openID, at string
	if err := d.QueryRow(`SELECT open_id, access_token FROM kuaishou_oauth_tokens WHERE handoff_id=?`,
		"pha_test_ks").Scan(&openID, &at); err != nil {
		t.Fatalf("token not persisted: %v", err)
	}
	if openID != "open_1" || at != "at_1" {
		t.Fatalf("token row wrong: %s/%s", openID, at)
	}
}

func TestKsSubmitFlowOnceAndQueryConfirmsOnlyExplicitPublished(t *testing.T) {
	d := adapterTestDB(t)
	base := mediaFixtureServer(t)
	fx := &fakeKsPlatform{uploadRef: "up_1", queryState: "public", queryPublish: true}
	a := NewKuaishouAdapter(d, ksConfig(fx))
	res, err := a.Prepare(context.Background(), ksRequestBase("oauth_server_push", true, base))
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	state := res.ClientAction.Params["state"]
	if _, err := a.FinishOAuth(context.Background(), "code_1", state); err != nil {
		t.Fatalf("finish: %v", err)
	}
	h := ksRequestBase("oauth_server_push", true, base).Handoff.Handoff
	// 提交一次:上传+创建各一次,photo_id 落库。
	if err := a.Submit(context.Background(), h, res); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if fx.uploadCalls != 2 || fx.createCalls != 1 {
		t.Fatalf("upload=%d create=%d (视频+封面各一次;创建一次=一次平台任务)", fx.uploadCalls, fx.createCalls)
	}
	var photoID, stage string
	if err := d.QueryRow(`SELECT photo_id, stage FROM kuaishou_publish_jobs WHERE handoff_id=?`, h.ID).Scan(&photoID, &stage); err != nil {
		t.Fatal(err)
	}
	if photoID == "" || stage != "accepted" {
		t.Fatalf("job after submit: photo=%q stage=%q", photoID, stage)
	}
	// 再次提交(重试/重启):photo_id 已存在=直接复用,不重复创建。
	if err := a.Submit(context.Background(), h, res); err != nil {
		t.Fatalf("resubmit: %v", err)
	}
	if fx.createCalls != 1 {
		t.Fatalf("resubmit must not create again: create=%d", fx.createCalls)
	}
	// 查询:显式 published 才铸回执。
	out, err := a.Query(context.Background(), h)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if out.Receipt == nil || out.Receipt.PostID != photoID || out.Receipt.HandoffID != h.ID {
		t.Fatalf("receipt = %+v", out.Receipt)
	}
	if fx.queryCalls != 1 {
		t.Fatalf("query calls = %d", fx.queryCalls)
	}
}

func TestKsSubmitWithoutAuthHonest(t *testing.T) {
	d := adapterTestDB(t)
	a := NewKuaishouAdapter(d, ksConfig(&fakeKsPlatform{uploadRef: "up_1"}))
	if _, err := a.Prepare(context.Background(), ksRequest("oauth_server_push", true)); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	h := ksRequest("oauth_server_push", true).Handoff.Handoff
	err := a.Submit(context.Background(), h, publishhandoff.PrepareResult{})
	if err == nil || !strings.Contains(err.Error(), "授权缺失") {
		t.Fatalf("submit without token err = %v", err)
	}
}

func TestKsCreateInFlightRefusesDuplicate(t *testing.T) {
	d := adapterTestDB(t)
	fx := &fakeKsPlatform{uploadRef: "up_1"}
	a := NewKuaishouAdapter(d, ksConfig(fx))
	res, _ := a.Prepare(context.Background(), ksRequest("oauth_server_push", true))
	h := ksRequest("oauth_server_push", true).Handoff.Handoff
	if _, err := a.FinishOAuth(context.Background(), "code_1", res.ClientAction.Params["state"]); err != nil {
		t.Fatal(err)
	}
	// 人为制造 creating 态(崩溃窗口):重复提交必须拒绝。
	now := time.Now().UTC()
	if _, err := d.Exec(`UPDATE kuaishou_publish_jobs SET stage='creating' WHERE handoff_id=?`, h.ID); err != nil {
		t.Fatal(err)
	}
	_ = now
	err := a.Submit(context.Background(), h, res)
	if err == nil || !strings.Contains(err.Error(), "不重复创建") {
		t.Fatalf("creating-stage resubmit err = %v", err)
	}
	if fx.createCalls != 0 {
		t.Fatalf("create calls = %d (必须不重复创建)", fx.createCalls)
	}
}

func TestKsQueryStatesAreRecordedNotConfirmed(t *testing.T) {
	d := adapterTestDB(t)
	base := mediaFixtureServer(t)
	fx := &fakeKsPlatform{uploadRef: "up_1", queryState: "reviewing", queryPublish: false}
	a := NewKuaishouAdapter(d, ksConfig(fx))
	res, _ := a.Prepare(context.Background(), ksRequestBase("oauth_server_push", true, base))
	h := ksRequestBase("oauth_server_push", true, base).Handoff.Handoff
	if _, err := a.FinishOAuth(context.Background(), "code_1", res.ClientAction.Params["state"]); err != nil {
		t.Fatal(err)
	}
	if err := a.Submit(context.Background(), h, res); err != nil {
		t.Fatalf("submit: %v", err)
	}
	// 异步接受(result=1 语义)不等于最终公开:reviewing 照录,零回执。
	out, err := a.Query(context.Background(), h)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if out.State != "reviewing" || out.Receipt != nil {
		t.Fatalf("state=%q receipt=%v (接受≠公开)", out.State, out.Receipt)
	}
	var saved string
	if err := d.QueryRow(`SELECT photo_state FROM kuaishou_publish_jobs WHERE handoff_id=?`, h.ID).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	if saved != "reviewing" {
		t.Fatalf("photo_state saved = %q", saved)
	}
}

func TestKsQueryWithoutPhotoPending(t *testing.T) {
	d := adapterTestDB(t)
	a := NewKuaishouAdapter(d, ksConfig(&fakeKsPlatform{}))
	if _, err := a.Prepare(context.Background(), ksRequest("oauth_server_push", true)); err != nil {
		t.Fatal(err)
	}
	out, err := a.Query(context.Background(), ksRequest("oauth_server_push", true).Handoff.Handoff)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if out.State != "pending" || out.Receipt != nil {
		t.Fatalf("no photo yet must be pending: %+v", out)
	}
}

func TestKsCallbackUnsupported(t *testing.T) {
	a := NewKuaishouAdapter(nil, ksConfig(&fakeKsPlatform{}))
	if _, err := a.VerifyCallback(context.Background(), publishhandoff.CallbackPayload{Body: []byte(`{"result":1}`)}); !isUnsupported(err) {
		t.Fatalf("verify callback err = %v", err)
	}
	if _, err := a.Prepare(context.Background(), ksRequest("manual_material", true)); !isUnsupported(err) {
		t.Fatalf("prepare other method err = %v", err)
	}
	if _, err := a.FinishOAuth(context.Background(), "", ""); err == nil {
		t.Fatal("empty code/state must be refused")
	}
}

func TestKsMissingCredentialsHonest(t *testing.T) {
	a := NewKuaishouAdapter(nil, KuaishouConfig{})
	if _, err := a.Prepare(context.Background(), ksRequest("oauth_server_push", true)); err == nil ||
		!strings.Contains(err.Error(), "凭据未配置") {
		t.Fatalf("missing credentials err = %v", err)
	}
}

func TestKsTokenExchangeFailureConsumesState(t *testing.T) {
	d := adapterTestDB(t)
	fx := &fakeKsPlatform{exchangeErr: errors.New("provider down")}
	a := NewKuaishouAdapter(d, ksConfig(fx))
	res, _ := a.Prepare(context.Background(), ksRequest("oauth_server_push", true))
	state := res.ClientAction.Params["state"]
	if _, err := a.FinishOAuth(context.Background(), "code_1", state); err == nil {
		t.Fatal("exchange failure must surface honestly")
	}
	var used string
	if err := d.QueryRow(`SELECT used_at FROM kuaishou_oauth_states WHERE state=?`, state).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if used == "" {
		t.Fatal("failed exchange must consume the one-time state (防重放)")
	}
}

func TestKsSubmitPreconditionErrorsAreCustomerRecoverable(t *testing.T) {
	d := adapterTestDB(t)
	a := NewKuaishouAdapter(d, ksConfig(&fakeKsPlatform{uploadRef: "up_1"}))
	if _, err := a.Prepare(context.Background(), ksRequest("oauth_server_push", true)); err != nil {
		t.Fatal(err)
	}
	h := ksRequest("oauth_server_push", true).Handoff.Handoff
	// 未授权提交:顾客可恢复(重新授权),不是服务器错误。
	err := a.Submit(context.Background(), h, publishhandoff.PrepareResult{})
	if !errors.Is(err, ErrCustomerActionRequired) {
		t.Fatalf("submit without token must wrap ErrCustomerActionRequired, got %v", err)
	}
	// creating 态拒重复创建:同样顾客可恢复(先用查询)。
	now := time.Now().UTC()
	if _, err := d.Exec(`UPDATE kuaishou_publish_jobs SET stage='creating' WHERE handoff_id=?`, h.ID); err != nil {
		t.Fatal(err)
	}
	_ = now
	err = a.Submit(context.Background(), h, publishhandoff.PrepareResult{})
	if !errors.Is(err, ErrCustomerActionRequired) {
		t.Fatalf("creating-stage resubmit must wrap ErrCustomerActionRequired, got %v", err)
	}
}
