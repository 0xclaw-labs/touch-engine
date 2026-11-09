// media_access.go: HUI-3553 B05 受控媒体访问凭据(公开面)。
//
// 裁定书语义:
//   - 短 TTL(默认 10 分钟,上限 1 小时,同 handoff 纪律);
//   - 不依赖商家 cookie:凭据自证(签名+过期),匿名顾客与平台抓取同权;
//   - 凭据非一次性(平台重复取材,TTL 窗口内可 HEAD/Range/重试),但绑定
//     具体版本+媒体+用途+TTL;不复用 B08 单用途恢复码语义;
//   - 用途白名单 preview|download|platform_fetch;download 走 attachment,
//     其余 inline;续签只续同一合法版本(HTTP 层重验采用与撤权)。

package contentpack

import (
	"fmt"
	"strings"
	"time"
)

// Media purposes (B05 契约)。
const (
	MediaPurposePreview       = "preview"
	MediaPurposeDownload      = "download"
	MediaPurposePlatformFetch = "platform_fetch"
)

// Reason code for a non-legal purpose.
const ReasonBadMediaPurpose = "bad_media_purpose"

// ValidMediaPurpose reports whether p is one of the B05 purposes.
func ValidMediaPurpose(p string) bool {
	switch p {
	case MediaPurposePreview, MediaPurposeDownload, MediaPurposePlatformFetch:
		return true
	}
	return false
}

// MediaAccessClaim is the payload bound into a signed, expiring media-access
// token: one version, one media, one purpose, one TTL.
type MediaAccessClaim struct {
	VersionID  string    `json:"v"`
	MediaID    string    `json:"m"`
	Purpose    string    `json:"p"`
	SessionRef string    `json:"sr,omitempty"`
	ExpiresAt  time.Time `json:"exp"`
}

// NewMediaAccessClaim validates and fills a claim; ttl zero = default.
func NewMediaAccessClaim(versionID, mediaID, purpose, sessionRef string, now time.Time, ttl time.Duration) (MediaAccessClaim, error) {
	switch {
	case strings.TrimSpace(versionID) == "":
		return MediaAccessClaim{}, invalid(ReasonMediaMissing, "version_id")
	case strings.TrimSpace(mediaID) == "":
		return MediaAccessClaim{}, invalid(ReasonMediaMissing, "media_id")
	case !ValidMediaPurpose(purpose):
		return MediaAccessClaim{}, invalid(ReasonBadMediaPurpose, "purpose")
	}
	if ttl == 0 {
		ttl = HandoffTTLDefault
	}
	if ttl < 0 || ttl > HandoffTTLMax {
		return MediaAccessClaim{}, invalid(ReasonBadHandoffTTL, "ttl")
	}
	return MediaAccessClaim{
		VersionID:  strings.TrimSpace(versionID),
		MediaID:    strings.TrimSpace(mediaID),
		Purpose:    purpose,
		SessionRef: strings.TrimSpace(sessionRef),
		ExpiresAt:  now.Add(ttl),
	}, nil
}

// mediaAccessDomain separates this HMAC use from the media-handoff use
// (handoff tokens must never redeem as media-access tokens or vice versa).
const mediaAccessDomain = "contentpack/media-access/v1"

func signMediaAccess(secret string, payload []byte) string {
	return signHandoffWithDomain(mediaAccessDomain, secret, payload)
}

// MintMediaAccess returns "<base64url(payload)>.<hex(hmac)>".
func MintMediaAccess(secret string, claim MediaAccessClaim) (string, error) {
	if strings.TrimSpace(secret) == "" {
		return "", invalid(ReasonHandoffInvalid, "secret")
	}
	return mintSigned(mediaAccessDomain, secret, claim)
}

// VerifyMediaAccess checks signature, decodes and enforces expiry. Tokens are
// NOT one-time: the same token verifies repeatedly inside its TTL window.
func VerifyMediaAccess(secret, token string, now time.Time) (MediaAccessClaim, error) {
	var claim MediaAccessClaim
	if err := verifySigned(mediaAccessDomain, secret, token, now, &claim); err != nil {
		return MediaAccessClaim{}, err
	}
	if !ValidMediaPurpose(claim.Purpose) {
		return MediaAccessClaim{}, invalid(ReasonBadMediaPurpose, "purpose")
	}
	return claim, nil
}

// MediaAccessDisposition renders the Content-Disposition the purpose demands:
// download = attachment (保存/下载语义), preview/platform_fetch = inline.
func MediaAccessDisposition(purpose, filename string) string {
	if purpose == MediaPurposeDownload && strings.TrimSpace(filename) != "" {
		return fmt.Sprintf("attachment; filename=%q", filename)
	}
	if purpose == MediaPurposeDownload {
		return "attachment"
	}
	return "inline"
}
