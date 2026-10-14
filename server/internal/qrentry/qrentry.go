// Package qrentry renders the QR fallback entry (FEAT-0165 / HUI-1664):
// server-side PNG generation for a campaign link's canonical short-code URL.
//
// 红线:QR 载荷是纯短码 canonical URL(PUBLIC_BASE_URL + /c/{code}),载荷里
// 没有任何结构——不嵌服务凭证、客户信息、租户/主体标识、任意跳转 URL,也不
// 携带任何可被后台解释为权限的参数。本包与 T0 活动/短码模型共用 campaign
// 包的码型校验,不引入第二套码制。
package qrentry

import (
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/skip2/go-qrcode"

	"github.com/bianjiefilm/touch-engine/server/internal/campaign"
)

// Sentinel errors (stable for tests/handlers).
var (
	ErrBadBase = errors.New("qrentry: public base url invalid")
	ErrBadCode = errors.New("qrentry: short code invalid")
	ErrBadSize = errors.New("qrentry: png size not in whitelist")
)

// DefaultSize is used when the caller does not ask for a specific size.
const DefaultSize = 256

// Sizes is the closed whitelist of allowed PNG pixel sizes (edge length).
var Sizes = map[int]bool{128: true, 256: true, 512: true}

// PublicEntryURL builds the canonical QR payload for a short code:
// <base>/c/<code>. The base must be a clean http(s) origin-or-path prefix with
// no query/fragment/userinfo; the code must pass the T0 shortcode format gate.
func PublicEntryURL(base, code string) (string, error) {
	b, err := cleanBase(base)
	if err != nil {
		return "", err
	}
	if !campaign.ValidShortcode(code) {
		return "", ErrBadCode
	}
	return b + "/c/" + code, nil
}

// PublicTouchEntryURL (HUI-1665 增量) builds the stable touchpoint payload:
// <base>/t/<alias>. Same base discipline and same shortcode format gate (the
// alias code shares the shortcode entropy/shape), only the path differs. The
// payload stays a pure opaque URL: no credential, tenant marker, or jump target.
func PublicTouchEntryURL(base, code string) (string, error) {
	b, err := cleanBase(base)
	if err != nil {
		return "", err
	}
	if !campaign.ValidShortcode(code) {
		return "", ErrBadCode
	}
	return b + "/t/" + code, nil
}

// cleanBase validates and normalizes the public base URL.
func cleanBase(base string) (string, error) {
	b := strings.TrimSpace(base)
	b = strings.TrimRight(b, "/")
	if b == "" {
		return "", ErrBadBase
	}
	u, err := url.Parse(b)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", ErrBadBase
	}
	return b, nil
}

// ParseSize maps the raw query-string size to a whitelisted pixel size.
// Empty means DefaultSize; anything outside the whitelist is ErrBadSize.
func ParseSize(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultSize, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || !Sizes[n] {
		return 0, ErrBadSize
	}
	return n, nil
}

// PNG renders the payload as a square PNG of the requested size. Recovery
// level Highest: physical prints/贴纸 survive partial occlusion.
func PNG(payload string, size int) ([]byte, error) {
	if !Sizes[size] {
		return nil, ErrBadSize
	}
	qc, err := qrcode.New(payload, qrcode.Highest)
	if err != nil {
		return nil, err
	}
	return qc.PNG(size)
}
