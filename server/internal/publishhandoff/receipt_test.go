package publishhandoff

import (
	"strings"
	"testing"
	"time"
)

// T4: 官方回执验签——验签接口 + fixture 回执测试。
//
// 票面: 官方回调验签或按官方机制校验来源,核对 app/tenant/participation/
// attempt/内容版本;不接受顾客伪造的官方 success。真实平台确认保持
// NOT_VERIFIED 残差:未配置生产密钥时验签一律拒绝。

// fixtureSign 用与 HMACVerifier 相同的域分离规范签一条回执(测试专用;
// 生产密钥由部署配置,真实平台签名走同一 Verify 接口)。
func fixtureSign(t *testing.T, secret string, r Receipt) string {
	t.Helper()
	sig, err := SignReceiptForTest(secret, r)
	if err != nil {
		t.Fatalf("sign fixture: %v", err)
	}
	return sig
}

func fixtureReceipt() Receipt {
	return Receipt{
		ReceiptID: "rcp_1", Platform: "xiaohongshu", AppRef: "app_touch_prod",
		TenantID: "tnt_seed", ParticipationID: "pt_a", AttemptID: "pta_a",
		ContentVersion: "cv1", PostID: "note_123", Outcome: ReceiptOutcomePublished,
		SignedAt: time.Date(2026, 10, 14, 12, 5, 0, 0, time.UTC),
	}
}

func TestHMACVerifierAcceptsWellFormedReceipt(t *testing.T) {
	v := NewHMACVerifier("test-secret")
	r := fixtureReceipt()
	sig := fixtureSign(t, "test-secret", r)
	got, err := v.Verify(ReceiptWire{Receipt: r, Signature: sig})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.ReceiptID != "rcp_1" || got.PostID != "note_123" {
		t.Fatalf("verified receipt mismatch: %+v", got)
	}
}

func TestHMACVerifierRejectsForgedReceipts(t *testing.T) {
	v := NewHMACVerifier("test-secret")
	r := fixtureReceipt()

	// 错签名(顾客伪造)。
	if _, err := v.Verify(ReceiptWire{Receipt: r, Signature: "deadbeef"}); err == nil {
		t.Fatal("forged signature must be rejected: 不接受顾客伪造的官方 success")
	}
	// 空签名。
	if _, err := v.Verify(ReceiptWire{Receipt: r, Signature: ""}); err == nil {
		t.Fatal("empty signature must be rejected")
	}
	// 签名与载荷不符(换 post id 再带旧签名)。
	r2 := r
	r2.PostID = "note_999"
	if _, err := v.Verify(ReceiptWire{Receipt: r2, Signature: fixtureSign(t, "test-secret", r)}); err == nil {
		t.Fatal("signature must bind the exact payload")
	}
	// 错误密钥(另一个"平台")签的。
	if _, err := v.Verify(ReceiptWire{Receipt: r, Signature: fixtureSign(t, "other-secret", r)}); err == nil {
		t.Fatal("wrong-key signature must be rejected")
	}
	// 空 receipt id / 空 platform:形状不完整。
	bad := r
	bad.ReceiptID = ""
	if _, err := v.Verify(ReceiptWire{Receipt: bad, Signature: fixtureSign(t, "test-secret", bad)}); err == nil {
		t.Fatal("receipt without id must be rejected")
	}
}

func TestNoSecretMeansNotVerified(t *testing.T) {
	v := NewHMACVerifier("") // 生产未配置平台验签密钥
	r := fixtureReceipt()
	// 未配置密钥时无论签名长什么样(哪怕自洽)一律拒绝。
	if _, err := v.Verify(ReceiptWire{Receipt: r, Signature: "anything"}); err == nil {
		t.Fatal("unconfigured verifier must refuse everything (NOT_VERIFIED residual)")
	}
}

func TestBindReceiptChecksAllFiveBindings(t *testing.T) {
	want := ReceiptBinding{
		AppRef: "app_touch_prod", TenantID: "tnt_seed",
		ParticipationID: "pt_a", AttemptID: "pta_a", ContentVersion: "cv1",
	}
	cases := []struct {
		name string
		mut  func(*Receipt)
	}{
		{"wrong app", func(r *Receipt) { r.AppRef = "app_other" }},
		{"wrong tenant", func(r *Receipt) { r.TenantID = "tnt_evil" }},
		{"wrong participation", func(r *Receipt) { r.ParticipationID = "pt_evil" }},
		{"wrong attempt", func(r *Receipt) { r.AttemptID = "pta_evil" }},
		{"wrong content version", func(r *Receipt) { r.ContentVersion = "cv_old" }},
	}
	for _, tc := range cases {
		r := fixtureReceipt()
		tc.mut(&r)
		if err := BindReceipt(r, want); err == nil {
			t.Fatalf("%s must be refused", tc.name)
		} else if !strings.Contains(err.Error(), "binding") {
			t.Fatalf("%s error should name the binding, got %v", tc.name, err)
		}
	}
	// 全部相符才通过。
	if err := BindReceipt(fixtureReceipt(), want); err != nil {
		t.Fatalf("matching receipt must bind: %v", err)
	}
}

func TestReceiptOutcomeWhitelist(t *testing.T) {
	// 只有 published 语义触发确认;其他回执结果照录但不确认。
	if !ReceiptConfirms(ReceiptOutcomePublished) {
		t.Fatal("published outcome should confirm")
	}
	for _, o := range []string{"rejected", "under_review", "removed", "", "success(fake)"} {
		if ReceiptConfirms(o) {
			t.Fatalf("outcome %q must not confirm", o)
		}
	}
}
