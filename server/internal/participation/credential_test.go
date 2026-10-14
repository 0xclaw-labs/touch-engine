package participation

// T3: 交接凭据密码学。128-bit 随机 + 校验位防枚举;库内只存 SHA-256;
// 明文永不进日志(展示走 redact.MaskCode)。

import (
	"strings"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/redact"
)

func TestNewTokenShapeAndUniqueness(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		tok, err := NewToken()
		if err != nil {
			t.Fatalf("NewToken: %v", err)
		}
		if !strings.HasPrefix(tok, tokenPrefix) {
			t.Fatalf("token %q missing prefix", tok)
		}
		body, _, ok := SplitToken(tok)
		if !ok {
			t.Fatalf("token %q fails its own checksum", tok)
		}
		if seen[tok] {
			t.Fatalf("token repeated at iteration %d", i)
		}
		seen[tok] = true
		// 128-bit 随机体 => base64url 22 字符。
		if len(body) != 22 {
			t.Fatalf("token body %q length = %d, want 22 (128-bit)", body, len(body))
		}
	}
}

func TestValidateTokenShape(t *testing.T) {
	tok, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if !ValidTokenShape(tok) {
		t.Fatal("fresh token must pass shape")
	}
	// 篡改一位:校验位必须拒绝(读端先拒,不打库)。
	bad := tok[:len(tok)-1]
	if bad[len(bad)-1] == 'A' {
		bad = bad[:len(bad)-1] + "B"
	} else {
		bad += "A"
	}
	if ValidTokenShape(bad) {
		t.Fatal("tampered checksum must fail")
	}
	for _, s := range []string{"", "ptx_", "ptx_AAAA.AAAA", "nonsense", tok + "x"} {
		if ValidTokenShape(s) {
			t.Fatalf("shape %q must fail", s)
		}
	}
}

func TestHashTokenStableAndCovering(t *testing.T) {
	tok, _ := NewToken()
	h1, h2 := HashToken(tok), HashToken(tok)
	if h1 != h2 || len(h1) != 64 {
		t.Fatal("hash must be stable sha256 hex")
	}
	other, _ := NewToken()
	if HashToken(other) == h1 {
		t.Fatal("different tokens must hash differently")
	}
	// 哈希不回吐明文。
	if strings.Contains(h1, tok) {
		t.Fatal("hash must not contain the token")
	}
}

func TestCredentialNeverLoggedPlain(t *testing.T) {
	// 对外展示一律走既有 redact 惯例;本包不持有 logger。
	tok, _ := NewToken()
	masked := redact.MaskCode(tok)
	if strings.Contains(masked, tok) || masked == tok {
		t.Fatal("masked form must not equal or contain the token")
	}
}
