// tokens.go: shared signed-token plumbing for the contentpack domain. The
// per-purpose HMAC domains (media-handoff vs media-access) keep one token
// kind from ever redeeming as the other. Existing HandoffClaim behaviour is
// unchanged — the original MintHandoff/VerifyHandoff stay as-is.

package contentpack

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

// expiring is implemented by every claim so verifySigned can enforce expiry
// without knowing the concrete type.
type expiring interface {
	Expiry() time.Time
}

// Expiry reports when the handoff claim dies.
func (c HandoffClaim) Expiry() time.Time { return c.ExpiresAt }

// Expiry reports when the media-access claim dies.
func (c MediaAccessClaim) Expiry() time.Time { return c.ExpiresAt }

func signHandoffWithDomain(domain, secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(domain+"\x00"+secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func mintSigned(domain, secret string, claim expiring) (string, error) {
	payload, err := json.Marshal(claim)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	return body + "." + signHandoffWithDomain(domain, secret, []byte(body)), nil
}

func verifySigned(domain, secret, token string, now time.Time, out expiring) error {
	switch {
	case strings.TrimSpace(secret) == "":
		return invalid(ReasonHandoffInvalid, "secret")
	case len(strings.Split(token, ".")) != 2:
		return invalid(ReasonHandoffInvalid, "token")
	}
	parts := strings.Split(token, ".")
	if !hmac.Equal([]byte(signHandoffWithDomain(domain, secret, []byte(parts[0]))), []byte(parts[1])) {
		return invalid(ReasonHandoffInvalid, "token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return invalid(ReasonHandoffInvalid, "token")
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return invalid(ReasonHandoffInvalid, "token")
	}
	if !now.Before(out.Expiry()) {
		return invalid(ReasonHandoffExpired, "expires_at")
	}
	return nil
}
