package httpapi

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/identity"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
	"github.com/bianjiefilm/touch-engine/server/internal/wecom"
)

// 会让这个测试失败的生产改动：回调把任何事件升级为「客户已添加」类成功、
// 凭据缺失时伪造 URL 验证应答（伪造 echo = 伪造验证）、日志落回调明文、
// 坏签名回显解密结果。

const (
	wecomTestToken  = "test-callback-token"
	wecomTestAESKey = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ" // 43 chars
	wecomTestCorpID = "corp_test_0001"
	wecomEchoPlain  = "verify-echo-778899"
	wecomEventXML   = `<xml><ToUserName><![CDATA[corp_test_0001]]></ToUserName><FromUserName><![CDATA[u_customer_1]]></FromUserName><CreateTime>1700000000</CreateTime><MsgType><![CDATA[event]]></MsgType><Event><![CDATA[change_external_contact]]></Event><ChangeType><![CDATA[add_external_contact]]></ChangeType></xml>`
)

// newWecomCallbackFixture：独立整栈（FEATURE_WECOM_CALLBACK on），凭据可选。
func newWecomCallbackFixture(t *testing.T, withCredentials bool) *actionsFixture {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	mustNoErr(t, err)
	t.Cleanup(func() { d.Close() })
	st := store.New(d)
	ten, err := st.CreateTenant("回调商家")
	mustNoErr(t, err)
	if _, err := st.CreateStore(ten.ID, "回调店", "", "usr_x"); err != nil {
		t.Fatalf("store: %v", err)
	}

	extra := map[string]string{"FEATURE_WECOM_CALLBACK": "on"}
	if withCredentials {
		extra["TOUCH_WECOM_CALLBACK_TOKEN"] = wecomTestToken
		extra["TOUCH_WECOM_CALLBACK_AES_KEY"] = wecomTestAESKey
		extra["TOUCH_WECOM_CALLBACK_CORP_ID"] = wecomTestCorpID
	}
	cfg := config.Load(func(k string) string {
		switch k {
		case "TOUCH_INTERNAL_TOKEN":
			return "test-internal-secret"
		case "PLATFORM_IDENTITY_BASE_URL":
			return "http://identity.test"
		case "PLATFORM_IDENTITY_TOKEN":
			return "identity-token"
		case "PLATFORM_UPLOAD_BASE_URL":
			return "http://upload.test"
		case "PLATFORM_UPLOAD_TOKEN":
			return "upload-token"
		case "PUBLIC_BASE_URL":
			return "https://h5.test"
		}
		if v, ok := extra[k]; ok {
			return v
		}
		return ""
	})
	idsrv := fakeIdentity(t, map[string]identitySession{})
	upsrv := fakeUpload(t, map[string]string{})
	idc := &identity.Client{BaseURL: idsrv.URL, Token: "identity-token", AppID: "touch-engine", HTTP: idsrv.Client()}
	upc := &upload.Client{BaseURL: upsrv.URL, Token: "upload-token", AppID: "touch-engine", HTTP: upsrv.Client()}
	s := New(cfg, d, idc, upc, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	stores, _ := st.ListStores(ten.ID)
	return &actionsFixture{
		f:    &fixture{s: s, ts: ts, tenA: ten.ID, ownA: "usr_x"},
		tenA: ten.ID, stoA: stores[0].ID,
		sessOwner: "", // 回调面无会话
	}
}

// urlQueryEscape escapes one query value (test convenience).
func urlQueryEscape(v string) string {
	return url.QueryEscape(v)
}

// cbGet performs a raw GET and returns the body text.
func cbGet(t *testing.T, f *fixture, path string) string {
	t.Helper()
	res, err := f.ts.Client().Get(f.ts.URL + path)
	mustNoErr(t, err)
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return string(raw)
}

// cbPostXML posts raw XML and returns (status, body).
func cbPostXML(t *testing.T, f *fixture, path, body string) (int, string) {
	t.Helper()
	res, err := f.ts.Client().Post(f.ts.URL+path, "text/xml", strings.NewReader(body))
	mustNoErr(t, err)
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(raw)
}

// cbLogStates reads the callback log decrypt_state column in insertion order.
func cbLogStates(t *testing.T, f *fixture) []string {
	t.Helper()
	rows := f.s.St.DB
	rs, err := rows.Query(`SELECT decrypt_state FROM wecom_callback_log ORDER BY occurred_at, id`)
	mustNoErr(t, err)
	defer rs.Close()
	var out []string
	for rs.Next() {
		var s string
		mustNoErr(t, rs.Scan(&s))
		out = append(out, s)
	}
	return out
}

// cbStoreContains scans every digest-bearing table for a plaintext needle.
func cbStoreContains(t *testing.T, f *fixture, needle string) bool {
	t.Helper()
	rs, err := f.s.St.DB.Query(`SELECT payload_digest, note FROM wecom_callback_log`)
	mustNoErr(t, err)
	defer rs.Close()
	for rs.Next() {
		var digest, note string
		mustNoErr(t, rs.Scan(&digest, &note))
		if strings.Contains(digest, needle) || strings.Contains(note, needle) {
			return true
		}
	}
	return false
}

func TestWecomCallbackFlagOff(t *testing.T) {
	af := newActionsHTTPFixture(t, false)
	status, _, _ := af.f.do(t, "GET", "/api/v1/public/wecom/callback?msg_signature=x&timestamp=1&nonce=n&echostr=e", "", "", "")
	if status != http.StatusNotFound {
		t.Fatalf("flag-off GET = %d (默认 off=路由不注册)", status)
	}
	status, _, _ = af.f.do(t, "POST", "/api/v1/public/wecom/callback", "", "", `<xml/>`)
	if status != http.StatusNotFound {
		t.Fatalf("flag-off POST = %d", status)
	}
}

func TestWecomCallbackPendingQualification(t *testing.T) {
	af := newWecomCallbackFixture(t, false)

	// GET URL 验证：凭据缺失 → 不能伪造 echo（伪造=谎称验证通过），诚实待资质。
	body := cbGet(t, af.f, "/api/v1/public/wecom/callback?msg_signature=sig&timestamp=1700000000&nonce=n&echostr=abc")
	if strings.Contains(body, "verify-echo") || body == "abc" {
		t.Fatalf("pending GET must not fabricate an echo: %q", body)
	}
	if !strings.Contains(body, "pending_qualification") {
		t.Fatalf("pending GET body = %q (必须诚实待资质)", body)
	}
	if got := cbLogStates(t, af.f); len(got) != 1 || got[0] != "pending_qualification" {
		t.Fatalf("pending GET log = %v", got)
	}

	// POST：同样待资质，只落 digest-only 证据，无成功语义。
	status, _ := cbPostXML(t, af.f, "/api/v1/public/wecom/callback", wecomEventXML)
	if status != http.StatusOK {
		t.Fatalf("pending POST = %d", status)
	}
	if got := cbLogStates(t, af.f); len(got) != 2 || got[1] != "pending_qualification" {
		t.Fatalf("pending POST log = %v", got)
	}
}

func TestWecomCallbackVerifiedEvidence(t *testing.T) {
	af := newWecomCallbackFixture(t, true)

	// GET URL 验证：合法签名 + 加密 echostr → 真验签真解密，回明文。
	// （echostr 是 base64，进 query 必须按 URL 规则转义——真实平台同样转义。）
	enc, err := wecom.Encrypt(wecomTestAESKey, wecomEchoPlain, wecomTestCorpID)
	mustNoErr(t, err)
	sig := wecom.Signature(wecomTestToken, "1700000000", "nonce1", enc)
	path := "/api/v1/public/wecom/callback?msg_signature=" + sig + "&timestamp=1700000000&nonce=nonce1&echostr=" + urlQueryEscape(enc)
	if body := cbGet(t, af.f, path); body != wecomEchoPlain {
		t.Fatalf("GET echo body = %q, want %q (真解密回显)", body, wecomEchoPlain)
	}
	// 坏签名：拒绝 + signature_mismatch 证据，绝不回明文。
	badPath := "/api/v1/public/wecom/callback?msg_signature=abababababababababababababababababababab&timestamp=1700000000&nonce=nonce1&echostr=" + urlQueryEscape(enc)
	if b := cbGet(t, af.f, badPath); strings.Contains(b, wecomEchoPlain) || b == wecomEchoPlain {
		t.Fatalf("bad-signature GET must not echo, got %q", b)
	}

	// POST 事件：合法签名 → verified 证据（digest-only）；「添加成功」语义在
	// 存储结构上不存在，这里只验证证据行与响应。
	encXML, err := wecom.Encrypt(wecomTestAESKey, wecomEventXML, wecomTestCorpID)
	mustNoErr(t, err)
	postBody := `<xml><ToUserName><![CDATA[` + wecomTestCorpID + `]]></ToUserName><Encrypt><![CDATA[` + encXML + `]]></Encrypt><AgentID><![CDATA[1000001]]></AgentID><InfoType><![CDATA[change_external_contact]]></InfoType></xml>`
	sigXML := wecom.Signature(wecomTestToken, "1700000001", "nonce2", encXML)
	status, respBody := cbPostXML(t, af.f, "/api/v1/public/wecom/callback?msg_signature="+sigXML+"&timestamp=1700000001&nonce=nonce2", postBody)
	if status != http.StatusOK {
		t.Fatalf("verified POST = %d %q", status, respBody)
	}
	// 坏签名 POST：signature_mismatch 证据。
	status, respBody = cbPostXML(t, af.f, "/api/v1/public/wecom/callback?msg_signature=abababababababababababababababababababab&timestamp=1700000001&nonce=nonce2", postBody)
	if status != http.StatusOK || !strings.Contains(respBody, "signature_mismatch") {
		t.Fatalf("bad-signature POST = %d %q", status, respBody)
	}

	states := cbLogStates(t, af.f)
	if len(states) != 4 || states[0] != "verified" || states[1] != "signature_mismatch" ||
		states[2] != "verified" || states[3] != "signature_mismatch" {
		t.Fatalf("callback log = %v", states)
	}
	// 日志 digest-only：全库搜不到顾客明文/事件类型。
	if cbStoreContains(t, af.f, "u_customer_1") || cbStoreContains(t, af.f, "add_external_contact") {
		t.Fatal("callback log must be digest-only (no plaintext events)")
	}
}

func TestWecomCryptoRoundTrip(t *testing.T) {
	msg := `<xml><x><![CDATA[hello]]></x></xml>`
	enc, err := wecom.Encrypt(wecomTestAESKey, msg, wecomTestCorpID)
	mustNoErr(t, err)
	got, receive, err := wecom.Decrypt(wecomTestAESKey, enc)
	mustNoErr(t, err)
	if got != msg || receive != wecomTestCorpID {
		t.Fatalf("roundtrip = %q/%q", got, receive)
	}
	// 验签函数：确定性，篡改任一输入即不同。
	sig1 := wecom.Signature(wecomTestToken, "1", "n", enc)
	sig2 := wecom.Signature(wecomTestToken, "1", "n", enc)
	sig3 := wecom.Signature(wecomTestToken, "2", "n", enc)
	if sig1 != sig2 || sig1 == sig3 {
		t.Fatalf("signature determinism broken: %v/%v/%v", sig1, sig2, sig3)
	}
}
