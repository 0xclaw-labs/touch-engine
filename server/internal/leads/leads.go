// Package leads implements HUI-1747 T1: authorized lead capture on the public
// activity page and its reliable, revocable delivery to the acquisition
// (CRM) side via the platform-notify directed-event profile.
//
// 纪律(票面红线):
//   - source_app 服务端固定为本应用 app id,不存在用户可提供的路径;
//   - target_tenant 不存在任何输入路径:投递租户 = 活动归属租户(服务端取);
//   - 联系方式只在授权源/目标域受控传输:事件只携带 submission_ref +
//     consent/provenance 引用与元数据,本包的 PII 预检在发送前再拒一次;
//   - 同意与后续营销分开表达:marketing_optin 是独立勾选位;
//   - 撤销终态:撤销后转发器拒绝补投;重放旧版本事件不会恢复营销权限
//     (目标侧 source_version 单调守卫)。
package leads

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ProfileVersion is the directed-event profile this app speaks (HUI-1734 E4).
const ProfileVersion = "directed-event/v1"

// Event types. lead.authorized_submitted is E4-frozen; lead.consent_revoked is
// a PROVISIONAL extension of the frozen enum required by HUI-1747 (撤销/停止
// 营销必须可送达目标域). platform-notify now admits the fact (public-ai
// HUI-3013 PR #255), so a real notify accepts the revoke event end to end.
const (
	EventLeadAuthorizedSubmitted = "lead.authorized_submitted"
	EventLeadConsentRevoked      = "lead.consent_revoked" // accepted by platform-notify since HUI-3013 (PR #255)
)

// Sync states of a lead submission (lead_submissions.sync_state).
const (
	StateAccepted    = "accepted"     // 本地已接受
	StatePendingSync = "pending_sync" // 已交 notify,待 CRM 确认
	StateCRMReceived = "crm_received" // CRM 已接收
	StateRejected    = "rejected"     // 目标拒绝/死信
	StateRevoked     = "revoked"      // 已撤销(终态)
)

// Notice is the fixed minimal consent notice shown to the consumer. Version is
// recorded on every submission (告知版本/时间).
type Notice struct {
	Version string `json:"version"`
	Text    string `json:"text"`
}

// CurrentNotice is v1 of the capture notice. Changing the text requires a new
// version constant; old submissions keep the version they consented to.
func CurrentNotice() Notice {
	return Notice{
		Version: "v1",
		Text: "提交即表示同意商家为本次活动联系您(用途:活动服务与咨询)。" +
			"信息仅提供给该活动的商家,后续营销需另行勾选,可随时撤销。",
	}
}

// ---- errors -------------------------------------------------------------------

var (
	// ErrConsentRequired: a submission without explicit consent is never stored.
	ErrConsentRequired = errors.New("leads: consent is required and must reference the shown notice")
	// ErrNoticeVersionMismatch: the consumer consented to a version we never showed.
	ErrNoticeVersionMismatch = errors.New("leads: consent references an unknown notice version")
	// ErrInvalidContact: fixed minimal schema validation failed.
	ErrInvalidContact = errors.New("leads: name/phone validation failed")
	// ErrPIIDetected: an outbound envelope carries contact-shaped data. This is
	// the local second line of defense (platform-notify enforces the hard gate).
	ErrPIIDetected = errors.New("leads: outbound envelope contains contact-shaped data")
	// ErrTargetTenantRejected: the request body tried to steer the delivery tenant.
	ErrTargetTenantRejected = errors.New("leads: target_tenant/tenant fields are not accepted")
)

// ---- fingerprints ---------------------------------------------------------------

// NormalizePhone canonicalizes the loose mainland-CN mobile input into the
// bare 11-digit form BEFORE validation: separators (space, hyphen, parentheses,
// dot, and their full-width variants) are dropped, a single leading + is
// stripped, and an 86 country prefix is stripped when it prefixes a full
// 13-digit form (86 + 11 digits). The result must be exactly 11 digits
// starting with 1 — otherwise the input is refused. This mirrors the
// leads-engine public form normalizer (共测 D-X1): the same subscriber input
// must yield the same stored phone, fingerprint and dedup key on both sides.
// Note the discipline: every derivation point (storage, PhoneFingerprint,
// DedupKey, revoke verification) consumes THIS function's output, so widening
// the tolerated input forms cannot split an identity in two.
func NormalizePhone(raw string) (string, error) {
	var b strings.Builder
	for _, r := range strings.TrimSpace(raw) {
		switch r {
		case ' ', '-', '(', ')', '.', '　', '－', '（', '）':
			continue
		}
		b.WriteRune(r)
	}
	p := b.String()
	p = strings.TrimPrefix(p, "+")
	if len(p) == 13 && strings.HasPrefix(p, "86") {
		p = p[2:]
	}
	if len(p) != 11 || p[0] != '1' {
		return "", fmt.Errorf("%w: phone must be a mainland mobile number", ErrInvalidContact)
	}
	for _, r := range p {
		if r < '0' || r > '9' {
			return "", fmt.Errorf("%w: phone must be digits", ErrInvalidContact)
		}
	}
	return p, nil
}

// PhoneFingerprint = HMAC-SHA256(pepper, phone). The raw phone never leaves the
// local store; the fingerprint is only used for dedup and rate limiting.
func PhoneFingerprint(pepper, phone string) string {
	m := hmac.New(sha256.New, []byte(pepper))
	m.Write([]byte(phone))
	return hex.EncodeToString(m.Sum(nil))
}

// DedupKey = sha256(pepper|campaign|fingerprint|day-bucket). The day bucket is
// the "单位时间" of the ticket: the same phone in the same campaign inside one
// UTC day is the same logical submission (idempotent), afterwards a new one may
// be captured.
func DedupKey(pepper, campaignID, phoneFP string, at time.Time) string {
	h := sha256.New()
	h.Write([]byte(pepper))
	h.Write([]byte{0x1f})
	h.Write([]byte(campaignID))
	h.Write([]byte{0x1f})
	h.Write([]byte(phoneFP))
	h.Write([]byte{0x1f})
	h.Write([]byte(at.UTC().Format("2006-01-02")))
	return hex.EncodeToString(h.Sum(nil))
}

// NewSubmissionRef mints the opaque public reference of a submission.
func NewSubmissionRef() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("leads: entropy unavailable: %w", err)
	}
	return "sub_" + hex.EncodeToString(b[:]), nil
}

// ---- outbound envelope ----------------------------------------------------------

// Envelope is the directed-event/v1 top level: exactly event_profile + payload.
// The payload carries references and metadata only — by construction it cannot
// carry contact data (all fields below are non-contact provenance).
type Envelope struct {
	EventProfile EventProfile    `json:"event_profile"`
	Payload      PayloadMetadata `json:"payload"`
}

type EventProfile struct {
	ProfileVersion string      `json:"profile_version"`
	EventID        string      `json:"event_id"`
	EventType      string      `json:"event_type"`
	SourceApp      string      `json:"source_app"`
	TargetApp      string      `json:"target_app"`
	TenantScope    string      `json:"tenant_scope"`
	SourceRef      string      `json:"source_ref"`
	SourceVersion  int         `json:"source_version"`
	Correlation    Correlation `json:"correlation"`
	PayloadRef     PayloadRef  `json:"payload_ref"`
}

type Correlation struct {
	HandoffID string `json:"handoff_id,omitempty"`
	Ref       string `json:"ref,omitempty"`
}

type PayloadRef struct {
	Ref    string `json:"ref"`
	SHA256 string `json:"sha256"`
}

// PayloadMetadata: non-sensitive provenance attached alongside the reference.
// Field names intentionally mirror the L-side (leads-engine) expectation list:
// campaign_ref/store_ref/channel/tag/asset/source_version + consent references.
type PayloadMetadata struct {
	CampaignRef    string `json:"campaign_ref,omitempty"`
	StoreRef       string `json:"store_ref,omitempty"`
	Channel        string `json:"channel,omitempty"`
	Tag            string `json:"tag,omitempty"`
	AssetRef       string `json:"asset_ref,omitempty"`
	SourceVersion  int    `json:"source_version"`
	ConsentRef     string `json:"consent_ref"`
	ConsentVersion string `json:"consent_version"`
	ConsentAt      string `json:"consent_at"`
	MarketingOptin bool   `json:"marketing_optin"`
	Revoked        bool   `json:"revoked,omitempty"`
	// Brand display is template copy only. Routing stays on source_app /
	// target_app / tenant_scope and is never the display name.
	BrandDisplayName     string `json:"brand_display_name,omitempty"`
	NotificationBrandRef string `json:"notification_brand_ref,omitempty"`
	// Source trace shared with the campaign handoff. These are references,
	// not a second asset library and not contact fields.
	TraceID         string `json:"trace_id,omitempty"`
	GrantRef        string `json:"grant_ref,omitempty"`
	ReturnTarget    string `json:"return_target,omitempty"`
	CampaignVersion string `json:"campaign_version,omitempty"`
}

// SourceTrace is the campaign version, grant and return kept on one trace.
type SourceTrace struct {
	TraceID         string
	GrantRef        string
	ReturnTarget    string
	CampaignVersion string
	AssetRef        string
}

// AttachSourceTrace copies server-derived provenance onto the fact.
// It does not change source_app, target_app, or tenant_scope.
func (e *Envelope) AttachSourceTrace(trace SourceTrace) {
	if e == nil {
		return
	}
	if trace.AssetRef != "" {
		e.Payload.AssetRef = trace.AssetRef
	}
	e.Payload.TraceID = trace.TraceID
	e.Payload.GrantRef = trace.GrantRef
	e.Payload.ReturnTarget = trace.ReturnTarget
	e.Payload.CampaignVersion = trace.CampaignVersion
}

// RecordRef is the source-private URI the target may resolve under its own
// authorization (fetch channel = future L-side integration, out of T1 scope).
func RecordRef(submissionRef string) string { return "touch://leads/" + submissionRef }

// RecordSHA256 hashes the canonical local record JSON (provenance hash; the
// content itself never travels).
func RecordSHA256(name, phone, wechat string) string {
	b, _ := json.Marshal(map[string]string{"name": name, "phone": phone, "wechat": wechat})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// BuildEnvelope assembles the outbound fact. sourceApp/targetApp/tenant are
// server-owned values; no caller parameter can introduce a tenant or contact.
func BuildSubmitEnvelope(sourceApp, targetApp, tenantScope, campaignID, storeID, channel, tag, assetRef, submissionRef, noticeVersion, consentAt string, marketingOptin bool, name, phone, wechat string, version int) Envelope {
	return Envelope{
		EventProfile: EventProfile{
			ProfileVersion: ProfileVersion,
			EventID:        submissionRef,
			EventType:      EventLeadAuthorizedSubmitted,
			SourceApp:      sourceApp,
			TargetApp:      targetApp,
			TenantScope:    tenantScope,
			SourceRef:      submissionRef,
			SourceVersion:  version,
			Correlation:    Correlation{Ref: campaignID},
			PayloadRef:     PayloadRef{Ref: RecordRef(submissionRef), SHA256: RecordSHA256(name, phone, wechat)},
		},
		Payload: PayloadMetadata{
			CampaignRef: campaignID, StoreRef: storeID, Channel: channel, Tag: tag, AssetRef: assetRef,
			SourceVersion: version,
			ConsentRef:    RecordRef(submissionRef), ConsentVersion: noticeVersion, ConsentAt: consentAt,
			MarketingOptin: marketingOptin,
		},
	}
}

// ApplyBrandTemplate attaches user-visible brand copy. It does not change
// source_app, target_app, or tenant_scope.
func (e *Envelope) ApplyBrandTemplate(displayName, notificationRef string) {
	if e == nil {
		return
	}
	e.Payload.BrandDisplayName = displayName
	e.Payload.NotificationBrandRef = notificationRef
}

// BuildRevokeEnvelope states the consent withdrawal / stop-marketing fact for a
// previously delivered submission (higher source_version; the target's
// monotonic guard makes stale replays of the submit fact inert). The record
// contact values pin the same provenance hash the submit fact carried: the
// target re-fetches the record and verifies payload_ref.sha256 before it
// converges the withdrawal, so a hash that does not describe the served record
// makes the revoke fact unfetchable (HUI-3013: the empty-record hash
// dead-lettered every revocation with 409 payload_hash_mismatch once the
// platform-notify enum admitted the fact).
func BuildRevokeEnvelope(sourceApp, targetApp, tenantScope, campaignID, submissionRef, consentAt string, version int, name, phone, wechat string) Envelope {
	e := Envelope{
		EventProfile: EventProfile{
			ProfileVersion: ProfileVersion,
			EventID:        submissionRef + "-revoke",
			EventType:      EventLeadConsentRevoked,
			SourceApp:      sourceApp,
			TargetApp:      targetApp,
			TenantScope:    tenantScope,
			SourceRef:      submissionRef,
			SourceVersion:  version,
			Correlation:    Correlation{Ref: campaignID},
			PayloadRef:     PayloadRef{Ref: RecordRef(submissionRef), SHA256: RecordSHA256(name, phone, wechat)},
		},
		Payload: PayloadMetadata{
			CampaignRef: campaignID, SourceVersion: version,
			ConsentRef: RecordRef(submissionRef), ConsentAt: consentAt, Revoked: true,
		},
	}
	return e
}

// MarshalEnvelope serializes and PII-scans an outbound fact in one step: a
// fact that carries contact-shaped data is refused before it can leave.
func MarshalEnvelope(e Envelope) ([]byte, error) {
	b, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	if hits := ScanForContactPII(string(b)); len(hits) > 0 {
		return nil, fmt.Errorf("%w: %v", ErrPIIDetected, hits)
	}
	return b, nil
}

// ---- PII scan (local mirror of the notify-side heuristic gate) --------------------

var piiKeyNames = []string{
	"phone", "mobile", "tel", "telephone", "email", "wechat", "weixin",
	"whatsapp", "contact", "msisdn", "手机", "电话", "微信", "联系方式",
}

var (
	emailShape = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	// Word boundaries keep hex identifiers (sha256/event ids) out of the shape
	// checks: digit runs inside a hex string are letter-bounded, not word-bounded.
	e164Shape    = regexp.MustCompile(`\b\+?[1-9][0-9]{7,14}\b`)
	cnMobileOnly = regexp.MustCompile(`\b1[3-9][0-9]{9}\b`)
	// JSON object keys, e.g. "contact_phone" :
	keyPattern = regexp.MustCompile(`"([^"]+)"\s*:`)
)

// ScanForContactPII returns the list of reasons a string must not travel as an
// event (contact-shaped key names, email/E.164/CN-mobile-shaped values, and
// phone-shaped numeric payloads). Heuristic by design and documented as such;
// the structural defense is that references are the only sanctioned carrier.
func ScanForContactPII(s string) []string {
	var reasons []string
	seen := map[string]bool{}
	for _, m := range keyPattern.FindAllStringSubmatch(s, -1) {
		key := strings.ToLower(m[1])
		for _, k := range piiKeyNames {
			if strings.Contains(key, k) && !seen["key:"+m[1]] {
				reasons = append(reasons, "key:"+m[1])
				seen["key:"+m[1]] = true
			}
		}
	}
	add := func(r string) {
		if !seen[r] {
			reasons = append(reasons, r)
			seen[r] = true
		}
	}
	if emailShape.MatchString(s) {
		add("email-shape")
	}
	if cnMobileOnly.MatchString(s) {
		add("cn-mobile-shape")
	} else if e164Shape.MatchString(s) {
		add("e164-shape")
	}
	return reasons
}

// ---- client IP rate limit (single instance, in-memory sliding window) --------------

// RateLimiter allows max events per window per key (client IP), single process.
type RateLimiter struct {
	max    int
	window time.Duration
	now    func() time.Time
	hits   map[string][]time.Time
}

// NewRateLimiter builds a limiter of max events per window.
func NewRateLimiter(max int, window time.Duration) *RateLimiter {
	return &RateLimiter{max: max, window: window, now: time.Now, hits: map[string][]time.Time{}}
}

// Allow records a hit for key and reports whether it is within budget.
func (l *RateLimiter) Allow(key string) bool {
	t := l.now()
	cutoff := t.Add(-l.window)
	kept := l.hits[key][:0]
	for _, h := range l.hits[key] {
		if h.After(cutoff) {
			kept = append(kept, h)
		}
	}
	if len(kept) >= l.max {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, t)
	return true
}
