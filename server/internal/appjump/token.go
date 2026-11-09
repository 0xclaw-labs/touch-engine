package appjump

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

// 跳转凭据令牌:jtg_<22 字节 base64url 随机体>.<1 字节 base64url 校验位>。
// 与 HUI-3546 ptx_ 交接凭据同一纪律(package 域不同、威胁面不同,独立实现):
// 随机体 128-bit(crypto/rand);校验位 = SHA-256(前缀+随机体) 首字节,让读端
// 在不打库的前提下先拒错码/错别字。防枚举靠 128-bit 熵;数据库只存
// SHA-256(完整令牌) 的 hex,明文永不落库、永不进日志。

const tokenPrefix = "jtg_"

// ErrTokenShape rejects malformed or checksum-failing tokens before any
// database lookup (anti-enumeration: indistinguishable from unknown). The
// handler maps it together with ErrNotFound.
var ErrTokenShape = errors.New("appjump: malformed jump token")

// NewToken mints a fresh jump token (128-bit random + checksum byte).
func NewToken() (string, error) {
	var body [16]byte
	if _, err := rand.Read(body[:]); err != nil {
		return "", errors.New("appjump: entropy unavailable: " + err.Error())
	}
	b := tokenPrefix + base64.RawURLEncoding.EncodeToString(body[:])
	return b + "." + checksumPart(b), nil
}

// SplitToken splits a token into (body, checksum, ok). ok is true only when
// the checksum verifies.
func SplitToken(tok string) (body, check string, ok bool) {
	if !strings.HasPrefix(tok, tokenPrefix) {
		return "", "", false
	}
	rest := tok[len(tokenPrefix):]
	dot := strings.LastIndex(rest, ".")
	if dot <= 0 || dot == len(rest)-1 {
		return "", "", false
	}
	body, check = rest[:dot], rest[dot+1:]
	if len(body) != 22 { // 128-bit => base64url 22 chars
		return "", "", false
	}
	if _, err := base64.RawURLEncoding.DecodeString(body); err != nil {
		return "", "", false
	}
	if checksumPart(tokenPrefix+body) != check {
		return body, check, false
	}
	return body, check, true
}

// ValidTokenShape reports whether tok carries a verifying checksum.
func ValidTokenShape(tok string) bool {
	_, _, ok := SplitToken(tok)
	return ok
}

// HashToken is the storage form: sha256 hex of the full token. The plaintext
// token never touches the database or the logs.
func HashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

func checksumPart(s string) string {
	sum := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(sum[:1])
}
