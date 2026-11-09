package contentpack

// media_access_test.go: HUI-3553 B05 受控媒体凭据语义。
//
// 裁定书:短 TTL;不依赖商家 cookie;凭据非一次性(平台重复取材),但绑定
// 具体媒体+用途+TTL;续签只续同一合法版本;不复用 B08 单用途恢复码。

import (
	"strings"
	"testing"
	"time"
)

func TestNewMediaAccessClaimBasics(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	claim, err := NewMediaAccessClaim("cpv_1", "cmd_1", "preview", "", now, 0)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claim.VersionID != "cpv_1" || claim.MediaID != "cmd_1" || claim.Purpose != "preview" {
		t.Fatalf("claim = %+v", claim)
	}
	if claim.ExpiresAt.Sub(now) != HandoffTTLDefault {
		t.Fatalf("default TTL = %v, want %v", claim.ExpiresAt.Sub(now), HandoffTTLDefault)
	}
}

func TestNewMediaAccessClaimPurposeWhitelist(t *testing.T) {
	now := time.Now()
	for _, purpose := range []string{"preview", "download", "platform_fetch"} {
		if _, err := NewMediaAccessClaim("cpv_1", "cmd_1", purpose, "", now, 0); err != nil {
			t.Fatalf("purpose %s refused: %v", purpose, err)
		}
	}
	for _, purpose := range []string{"", "generate", "PAY", "revenue_share"} {
		if _, err := NewMediaAccessClaim("cpv_1", "cmd_1", purpose, "", now, 0); err == nil {
			t.Fatalf("purpose %q accepted (only preview/download/platform_fetch are legal)", purpose)
		}
	}
}

func TestNewMediaAccessClaimTTLCaps(t *testing.T) {
	now := time.Now()
	if _, err := NewMediaAccessClaim("v", "m", "preview", "", now, -time.Second); err == nil {
		t.Fatal("negative TTL accepted")
	}
	if _, err := NewMediaAccessClaim("v", "m", "preview", "", now, 2*HandoffTTLMax); err == nil {
		t.Fatal("TTL above cap accepted")
	}
	claim, err := NewMediaAccessClaim("v", "m", "preview", "", now, HandoffTTLMax)
	if err != nil {
		t.Fatalf("cap TTL refused: %v", err)
	}
	if !claim.ExpiresAt.Equal(now.Add(HandoffTTLMax)) {
		t.Fatalf("cap TTL = %v", claim.ExpiresAt.Sub(now))
	}
}

func TestNewMediaAccessClaimRequiresVersionAndMedia(t *testing.T) {
	now := time.Now()
	if _, err := NewMediaAccessClaim("", "m", "preview", "", now, 0); err == nil {
		t.Fatal("empty version accepted")
	}
	if _, err := NewMediaAccessClaim("v", "", "preview", "", now, 0); err == nil {
		t.Fatal("empty media accepted")
	}
}

func TestMediaAccessMintVerifyRoundTrip(t *testing.T) {
	now := time.Now()
	claim, _ := NewMediaAccessClaim("cpv_1", "cmd_9", "platform_fetch", "ses_1", now, time.Minute)
	token, err := MintMediaAccess("secret", claim)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	got, err := VerifyMediaAccess("secret", token, now.Add(30*time.Second))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.MediaID != "cmd_9" || got.VersionID != "cpv_1" || got.Purpose != "platform_fetch" || got.SessionRef != "ses_1" {
		t.Fatalf("roundtrip = %+v", got)
	}
	// 非一次性:同一 token 可重复验证(平台重复取材)
	if _, err := VerifyMediaAccess("secret", token, now.Add(30*time.Second)); err != nil {
		t.Fatalf("second use refused (credentials are not one-time): %v", err)
	}
}

func TestMediaAccessVerifyExpiryAndTamper(t *testing.T) {
	now := time.Now()
	claim, _ := NewMediaAccessClaim("v", "m", "preview", "", now, time.Minute)
	token, _ := MintMediaAccess("secret", claim)

	if _, err := VerifyMediaAccess("secret", token, now.Add(2*time.Minute)); err == nil {
		t.Fatal("expired token accepted")
	} else if ve, ok := err.(*ValidationError); !ok || ve.Reason != ReasonHandoffExpired {
		t.Fatalf("expiry reason = %v", err)
	}
	tampered := token[:len(token)-2] + "zz"
	if _, err := VerifyMediaAccess("secret", tampered, now); err == nil {
		t.Fatal("tampered token accepted")
	}
	if _, err := VerifyMediaAccess("other-secret", token, now); err == nil {
		t.Fatal("wrong secret accepted")
	}
}

func TestMediaAccessDispositionFollowsPurpose(t *testing.T) {
	if got := MediaAccessDisposition("download", "菜单视频.mp4"); !strings.HasPrefix(got, "attachment; filename=") {
		t.Fatalf("download disposition = %q, want attachment", got)
	}
	if got := MediaAccessDisposition("preview", "x.mp4"); got != "inline" {
		t.Fatalf("preview disposition = %q, want inline", got)
	}
	if got := MediaAccessDisposition("platform_fetch", "x.mp4"); got != "inline" {
		t.Fatalf("platform_fetch disposition = %q, want inline", got)
	}
}
