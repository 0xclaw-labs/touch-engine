// 官方回执验签:验签接口 + HMAC fixture 实现。
//
// 票面: 官方回调验签或按官方机制校验来源,核对 app/tenant/participation/
// attempt/内容版本;不接受顾客伪造的官方 success;重放、乱序和超时先回查。
//
// 残差声明: 真实平台(抖音/小红书/B站/快手)的回执签名机制各不相同且均未
// 获准接入;本文件提供的是验签接口与 HMAC-SHA256 fixture 实现(测试锁定
// 语义)。生产部署未配置平台验签密钥时,Verifier 拒绝一切回执——状态停留
// unknown/awaiting_review,绝不算平台确认(NOT_VERIFIED 残差,如实挂账)。
package publishhandoff

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Receipt outcomes (fixture 语义;真实平台结果词表在接入时映射到这两类).
const (
	ReceiptOutcomePublished = "published" // 平台确认内容已发布
	ReceiptOutcomeOther     = "other"     // 其余一切(审核中/被拒/已删…)——照录不确认
)

// Receipt is the verified-shape of an official platform callback.
type Receipt struct {
	ReceiptID      string    // 平台回执唯一 ID(重放去重键)
	Platform       string
	AppRef         string    // 官方 app 身份(错 app 拒绝)
	TenantID       string    // 租户绑定
	ParticipationID string   // 参与绑定
	AttemptID      string    // 尝试绑定
	ContentVersion string    // 内容版本绑定(换稿后旧回执不认)
	PostID         string
	Outcome        string    // published | other
	SignedAt       time.Time
}

// ReceiptWire is what arrives on the wire: payload + signature.
type ReceiptWire struct {
	Receipt   Receipt
	Signature string
}

// ReceiptVerifier verifies the SOURCE of a callback. 实现必须先验签再谈
// 绑定;任何验证失败都返回错误(调用方记 receipt_rejected 事件)。
type ReceiptVerifier interface {
	Verify(wire ReceiptWire) (Receipt, error)
}

// ReceiptBinding is what the receipt must match: 五绑定
// (app/tenant/participation/attempt/content_version)。
type ReceiptBinding struct {
	AppRef          string
	TenantID        string
	ParticipationID string
	AttemptID       string
	ContentVersion  string
}

// ErrReceiptUnverified is returned for every signature failure (含未配置密钥).
var ErrReceiptUnverified = errors.New("publishhandoff: receipt signature not verified")

// receiptDomain separates this HMAC use from every other secret use
// (contentpack handoffDomain 同款纪律).
const receiptDomain = "publishhandoff/official-receipt/v1"

// canonicalReceipt is the exact byte string the signature covers: 字段顺序
// 固定,任何载荷改动都破坏签名。
func canonicalReceipt(r Receipt) string {
	return strings.Join([]string{
		r.ReceiptID, r.Platform, r.AppRef, r.TenantID, r.ParticipationID,
		r.AttemptID, r.ContentVersion, r.PostID, r.Outcome,
		r.SignedAt.UTC().Format(time.RFC3339Nano),
	}, "\x1f")
}

func signReceipt(secret string, payload string) string {
	mac := hmac.New(sha256.New, []byte(receiptDomain+"\x00"+secret))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// SignReceiptForTest exposes canonical signing to fixture tests. 生产代码
// 不使用本函数(平台侧才持有签名密钥)。
func SignReceiptForTest(secret string, r Receipt) (string, error) {
	if strings.TrimSpace(secret) == "" {
		return "", ErrReceiptUnverified
	}
	return signReceipt(secret, canonicalReceipt(r)), nil
}

// HMACVerifier is the fixture verifier: HMAC-SHA256 over the canonical
// receipt, domain-separated. An empty secret refuses EVERYTHING — the
// deployment without a platform verification secret stays honestly
// NOT_VERIFIED instead of trusting unverified callbacks.
type HMACVerifier struct {
	secret string
}

// NewHMACVerifier builds the verifier. Empty secret = refuse-all mode.
func NewHMACVerifier(secret string) *HMACVerifier {
	return &HMACVerifier{secret: strings.TrimSpace(secret)}
}

// Verify checks signature presence, binding of signature to payload, minimal
// shape, and only then returns the receipt.
func (v *HMACVerifier) Verify(wire ReceiptWire) (Receipt, error) {
	if v.secret == "" {
		return Receipt{}, ErrReceiptUnverified
	}
	if strings.TrimSpace(wire.Signature) == "" {
		return Receipt{}, ErrReceiptUnverified
	}
	r := wire.Receipt
	if strings.TrimSpace(r.ReceiptID) == "" || strings.TrimSpace(r.Platform) == "" {
		return Receipt{}, fmt.Errorf("%w: receipt id/platform required", ErrReceiptUnverified)
	}
	want := signReceipt(v.secret, canonicalReceipt(r))
	if !hmac.Equal([]byte(want), []byte(strings.ToLower(strings.TrimSpace(wire.Signature)))) {
		return Receipt{}, ErrReceiptUnverified
	}
	return r, nil
}

// BindReceipt enforces the five bindings. Any mismatch names "binding" so
// callers can record a precise receipt_rejected reason.
func BindReceipt(r Receipt, want ReceiptBinding) error {
	bad := ""
	switch {
	case r.AppRef != want.AppRef:
		bad = "app"
	case r.TenantID != want.TenantID:
		bad = "tenant"
	case r.ParticipationID != want.ParticipationID:
		bad = "participation"
	case r.AttemptID != want.AttemptID:
		bad = "attempt"
	case r.ContentVersion != want.ContentVersion:
		bad = "content_version"
	}
	if bad != "" {
		return fmt.Errorf("publishhandoff: receipt %s binding mismatch", bad)
	}
	return nil
}

// ReceiptConfirms reports whether a verified+bound receipt outcome carries
// the platform-confirmed semantics. Everything else is recorded, never
// confirming (照录不确认).
func ReceiptConfirms(outcome string) bool {
	return outcome == ReceiptOutcomePublished
}
