package qrentry

import (
	"bytes"
	"image/png"
	"strings"
	"testing"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
)

// HUI-1664 红线:QR 载荷 = 纯短码 canonical URL。载荷里没有任何结构——
// 不嵌服务凭证、客户信息、租户/主体标识或任意跳转 URL。

const testBase = "https://h5.example.com"

func TestPublicEntryURL(t *testing.T) {
	// happy path: base with and without trailing slash
	got, err := PublicEntryURL(testBase, "ABC123XYZ789")
	if err != nil {
		t.Fatalf("PublicEntryURL: %v", err)
	}
	if got != testBase+"/c/ABC123XYZ789" {
		t.Fatalf("url = %q", got)
	}
	got, err = PublicEntryURL(testBase+"/", "ABC123XYZ789")
	if err != nil {
		t.Fatalf("PublicEntryURL trailing slash: %v", err)
	}
	if got != testBase+"/c/ABC123XYZ789" {
		t.Fatalf("url trailing slash = %q", got)
	}

	// refusal matrix: anything that is not a clean http(s) base
	for _, tc := range []struct {
		name, base, code string
		wantErr          error
	}{
		{"empty base", "", "ABC123XYZ789", ErrBadBase},
		{"space base", "   ", "ABC123XYZ789", ErrBadBase},
		{"ftp scheme", "ftp://h5.example.com", "ABC123XYZ789", ErrBadBase},
		{"javascript scheme", "javascript:alert(1)", "ABC123XYZ789", ErrBadBase},
		{"base with query", testBase + "?x=1", "ABC123XYZ789", ErrBadBase},
		{"base with fragment", testBase + "#frag", "ABC123XYZ789", ErrBadBase},
		{"base with userinfo", "https://u:p@" + strings.TrimPrefix(testBase, "https://"), "ABC123XYZ789", ErrBadBase},
		{"bare host no scheme", "h5.example.com", "ABC123XYZ789", ErrBadBase},
		{"empty code", testBase, "", ErrBadCode},
		{"lowercase code", testBase, "abc123xyz789", ErrBadCode},
		{"short code", testBase, "ABC123", ErrBadCode},
		{"code with separator", testBase, "ABC1-3XYZ789", ErrBadCode},
		{"code path escape", testBase, "../../admin", ErrBadCode},
		{"code with query", testBase, "ABC123XYZ789?x=1", ErrBadCode},
	} {
		if _, err := PublicEntryURL(tc.base, tc.code); err != tc.wantErr {
			t.Fatalf("%s: err = %v, want %v", tc.name, err, tc.wantErr)
		}
	}
}

func TestPublicEntryURLPayloadHygiene(t *testing.T) {
	// the payload is derived from base+code ONLY: prove no query, no fragment,
	// and nothing beyond the canonical shape can appear in it.
	got, err := PublicEntryURL(testBase, "ABC123XYZ789")
	if err != nil {
		t.Fatalf("PublicEntryURL: %v", err)
	}
	if strings.ContainsAny(got, "?&#@ ") {
		t.Fatalf("payload carries query/fragment/userinfo/space: %q", got)
	}
	if got != testBase+"/c/ABC123XYZ789" {
		t.Fatalf("payload is not the canonical url: %q", got)
	}
	// path after host must be exactly /c/<12 chars of Crockford base32>
	idx := strings.Index(got, "://")
	rest := got[idx+3+len("h5.example.com"):]
	if rest != "/c/ABC123XYZ789" {
		t.Fatalf("path = %q, want /c/ABC123XYZ789", rest)
	}
}

func TestParseSizeWhitelist(t *testing.T) {
	for _, ok := range []string{"", "128", "256", "512"} {
		if _, err := ParseSize(ok); err != nil {
			t.Fatalf("ParseSize(%q) = %v, want ok", ok, err)
		}
	}
	if got, _ := ParseSize(""); got != DefaultSize {
		t.Fatalf("ParseSize(\"\") = %d, want default %d", got, DefaultSize)
	}
	for _, bad := range []string{"100", "64", "1024", "0", "-1", "abc", "25.6", "99999999999999999999"} {
		if _, err := ParseSize(bad); err != ErrBadSize {
			t.Fatalf("ParseSize(%q) = %v, want ErrBadSize", bad, err)
		}
	}
}

func TestPNG(t *testing.T) {
	u, err := PublicEntryURL(testBase, "ABC123XYZ789")
	if err != nil {
		t.Fatalf("PublicEntryURL: %v", err)
	}
	for _, size := range []int{128, 256, 512} {
		b, err := PNG(u, size)
		if err != nil {
			t.Fatalf("PNG(%d): %v", size, err)
		}
		if len(b) < 8 || !bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")) {
			t.Fatalf("PNG(%d) is not a PNG (len=%d)", size, len(b))
		}
		img, err := png.Decode(bytes.NewReader(b))
		if err != nil {
			t.Fatalf("PNG(%d) not decodable as image: %v", size, err)
		}
		if img.Bounds().Dx() < size/2 {
			t.Fatalf("PNG(%d) suspiciously small: %v", size, img.Bounds())
		}
	}
	if _, err := PNG(u, 100); err != ErrBadSize {
		t.Fatalf("PNG(100) = %v, want ErrBadSize", err)
	}
}

// TestPNGDecodeRoundTrip decodes the generated PNG back to text and asserts it
// equals the canonical short-code URL (机器验收:解码回读内容一致).
func TestPNGDecodeRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		base, code string
	}{
		{testBase, "ABC123XYZ789"},
		{"http://127.0.0.1:18340", "0123456789AV"},
	} {
		u, err := PublicEntryURL(tc.base, tc.code)
		if err != nil {
			t.Fatalf("PublicEntryURL: %v", err)
		}
		for _, size := range []int{128, 256, 512} {
			b, err := PNG(u, size)
			if err != nil {
				t.Fatalf("PNG(%d): %v", size, err)
			}
			img, err := png.Decode(bytes.NewReader(b))
			if err != nil {
				t.Fatalf("png.Decode: %v", err)
			}
			bmp, err := gozxing.NewBinaryBitmapFromImage(img)
			if err != nil {
				t.Fatalf("binary bitmap: %v", err)
			}
			res, err := qrcode.NewQRCodeReader().Decode(bmp, nil)
			if err != nil {
				t.Fatalf("decode PNG(%d) for %q: %v", size, u, err)
			}
			if res.GetText() != u {
				t.Fatalf("decoded %q, want %q", res.GetText(), u)
			}
		}
	}
}

// TestPublicTouchEntryURL(HUI-1665 增量):稳定触点入口 <base>/t/<alias>。
// 与短码同 base 纪律、同码形闸;载荷仍是纯不透明 URL。
func TestPublicTouchEntryURL(t *testing.T) {
	got, err := PublicTouchEntryURL(testBase+"/", "ABC123XYZ789")
	if err != nil || got != testBase+"/t/ABC123XYZ789" {
		t.Fatalf("PublicTouchEntryURL = %q %v", got, err)
	}
	// 坏 base 与坏码形逐一拒绝(与 PublicEntryURL 同一裁决)
	for _, base := range []string{"", "ftp://x.com", "https://x.com/?a=1", "https://x.com/#f", "not a url"} {
		if _, err := PublicTouchEntryURL(base, "ABC123XYZ789"); err == nil {
			t.Fatalf("base %q accepted", base)
		}
	}
	if _, err := PublicTouchEntryURL(testBase, "bad code!"); err == nil {
		t.Fatal("bad code accepted")
	}
}
