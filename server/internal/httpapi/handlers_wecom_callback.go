package httpapi

// HUI-4589 企微客户事件回调端点（FEATURE_WECOM_CALLBACK 默认 off = 路由不注册
// 且 gate 再答统一 404 双保险）。
//
// 纪律（裁定书：工程面留回调端点+验签骨架，官方权限未核实处诚实标注「待资质」）：
//   - 验签/解密是真实现（sha1 lexsort 签名 + AES-256-CBC 信封，wecom 包），
//     本地 fixture 可对拍；但凭据由部署侧持有，缺失时绝不伪造验证结果。
//   - 待资质诚实态：凭据未配置 → GET URL 验证不回显（伪造 echo = 谎称验证
//     通过）、POST 不解析事件，只答 pending_qualification 并落 digest-only
//     证据行。
//   - 日志只落摘要（payload_digest）与诚实状态；绝不落回调明文，绝无任何
//     「客户已添加」类成功列——添加结果以企业微信官方侧为准。

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/wecom"
)

type wecomCallbackXML struct {
	XMLName    xml.Name `xml:"xml"`
	ToUserName string   `xml:"ToUserName"`
	Encrypt    string   `xml:"Encrypt"`
}

// extractEncrypt pulls the <Encrypt> value out of the callback body.
func extractEncrypt(body []byte) string {
	var doc wecomCallbackXML
	if err := xml.Unmarshal(body, &doc); err != nil {
		return ""
	}
	return strings.TrimSpace(doc.Encrypt)
}

var wecomQuerySig = regexp.MustCompile(`^[0-9a-f]{40}$`)

func (s *Server) handleWecomCallback(w http.ResponseWriter, r *http.Request) {
	if !s.Cfg.FeatureWecomCallback {
		fail(w, http.StatusNotFound, "not_found", "callback is not available")
		return
	}
	q := r.URL.Query()
	msgSignature := strings.TrimSpace(q.Get("msg_signature"))
	timestamp := strings.TrimSpace(q.Get("timestamp"))
	nonce := strings.TrimSpace(q.Get("nonce"))

	// 凭据未配置 = 待资质：诚实应答，绝不伪造验证。
	if s.Cfg.WecomCallbackToken == "" || s.Cfg.WecomCallbackAESKey == "" || s.Cfg.WecomCallbackCorpID == "" {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		_ = s.St.RecordWecomCallback(store.WecomCallbackPendingQualification, msgSignature, false,
			sha256Hex(body), "待资质：回调凭据未配置，未解析事件内容")
		writeJSON(w, http.StatusOK, map[string]any{
			"state":    "pending_qualification",
			"verified": false,
			"recorded": false,
			"message":  "企微回调资质未配置：URL 验证无法完成；资质核实前本系统不记录任何「客户已添加」。",
		})
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.handleWecomVerify(w, r, msgSignature, timestamp, nonce)
	case http.MethodPost:
		s.handleWecomEvent(w, r, msgSignature, timestamp, nonce)
	default:
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET or POST only")
	}
}

// handleWecomVerify answers the WeCom URL verification handshake: verify the
// signature, decrypt echostr, echo the PLAINTEXT back.
func (s *Server) handleWecomVerify(w http.ResponseWriter, r *http.Request, msgSignature, timestamp, nonce string) {
	echostr := strings.TrimSpace(r.URL.Query().Get("echostr"))
	if echostr == "" || !wecomQuerySig.MatchString(strings.ToLower(msgSignature)) {
		fail(w, http.StatusBadRequest, "bad_request", "msg_signature and echostr are required")
		return
	}
	if !wecom.VerifySignature(s.Cfg.WecomCallbackToken, timestamp, nonce, echostr, msgSignature) {
		_ = s.St.RecordWecomCallback(store.WecomCallbackSignatureMismatch, msgSignature, false, "", "URL 验证签名不匹配")
		writeJSON(w, http.StatusOK, map[string]any{
			"state": "signature_mismatch", "verified": false,
			"message": "URL 验证签名不匹配；本系统不回显解密内容。",
		})
		return
	}
	msg, receiveID, err := wecom.Decrypt(s.Cfg.WecomCallbackAESKey, echostr)
	if err != nil || receiveID != s.Cfg.WecomCallbackCorpID {
		_ = s.St.RecordWecomCallback(store.WecomCallbackDecryptFailed, msgSignature, true, sha256Hex([]byte(echostr)), "URL 验证解密失败或 receiveid 不匹配")
		writeJSON(w, http.StatusOK, map[string]any{
			"state": "decrypt_failed", "verified": false,
			"message": "URL 验证解密失败；本系统不回显解密内容。",
		})
		return
	}
	_ = s.St.RecordWecomCallback(store.WecomCallbackVerified, msgSignature, true, "", "URL 验证通过（回显明文）")
	// 协议要求回显解密后的明文（纯文本 body）。
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(msg))
}

// handleWecomEvent receives one encrypted event push. Verified events land as
// digest-only evidence rows; nothing here can express or record a customer
// added/connected/followed success.
func (s *Server) handleWecomEvent(w http.ResponseWriter, r *http.Request, msgSignature, timestamp, nonce string) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil || len(body) == 0 {
		fail(w, http.StatusBadRequest, "bad_request", "callback body required")
		return
	}
	encrypt := extractEncrypt(body)
	if encrypt == "" {
		fail(w, http.StatusBadRequest, "bad_request", "Encrypt field required")
		return
	}
	digest := sha256Hex(body)
	if !wecom.VerifySignature(s.Cfg.WecomCallbackToken, timestamp, nonce, encrypt, msgSignature) {
		_ = s.St.RecordWecomCallback(store.WecomCallbackSignatureMismatch, msgSignature, false, digest, "事件签名不匹配")
		writeJSON(w, http.StatusOK, map[string]any{
			"state": "signature_mismatch", "verified": false, "recorded": true,
			"message": "事件签名不匹配；仅落证据行，未解析内容。",
		})
		return
	}
	_, receiveID, err := wecom.Decrypt(s.Cfg.WecomCallbackAESKey, encrypt)
	if err != nil || receiveID != s.Cfg.WecomCallbackCorpID {
		_ = s.St.RecordWecomCallback(store.WecomCallbackDecryptFailed, msgSignature, true, digest, "事件解密失败或 receiveid 不匹配")
		writeJSON(w, http.StatusOK, map[string]any{
			"state": "decrypt_failed", "verified": false, "recorded": true,
			"message": "事件解密失败；仅落证据行。",
		})
		return
	}
	// verified：证据留存（digest-only）。企业微信协议应答任意 200；这里不解析
	// 事件语义——「客户事件」的证据价值由平台侧权威状态决定，本系统绝不因
	// 回调写任何「已添加」成功（成功语义在存储结构上不存在）。
	_ = s.St.RecordWecomCallback(store.WecomCallbackVerified, msgSignature, true, digest, "客户事件回调验签通过（digest-only 证据）")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("success"))
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
