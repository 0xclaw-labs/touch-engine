package leads

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestApplyBrandTemplateDoesNotReroute(t *testing.T) {
	e := BuildSubmitEnvelope("touch-engine", "leads-engine", "tnt_a", "cmp", "", "web", "", "", "sub", "v1", "2026-09-27T00:00:00Z", false, "n", "13800138000", "", 1)
	e.ApplyBrandTemplate("甲牌", "notif-brand-a")
	if e.EventProfile.SourceApp != "touch-engine" || e.EventProfile.TargetApp != "leads-engine" || e.EventProfile.TenantScope != "tnt_a" {
		t.Fatalf("routing changed: %+v", e.EventProfile)
	}
	if e.Payload.BrandDisplayName != "甲牌" || e.Payload.NotificationBrandRef != "notif-brand-a" {
		t.Fatalf("template %+v", e.Payload)
	}
}

func TestNormalizePhone(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"13800138000", "13800138000", true},
		{"138-0013-8000", "13800138000", true},
		{" 13800138000 ", "13800138000", true},
		// D-X1: 国码前缀与常见分隔符容忍(对齐 leads 公共表单语义),归一为裸 11 位
		{"+8613800138000", "13800138000", true},
		{"8613800138000", "13800138000", true},
		{"+86 138 0013 8000", "13800138000", true},
		{"+86-138-0013-8000", "13800138000", true},
		{"86-13800138000", "13800138000", true},
		{"（+86）138 0013 8000", "13800138000", true},
		// 无法归一出合法 11 位 CN 手机号的仍拒绝
		{"+86138001380", "", false},   // 12 位:前缀不完整,非 13 位前缀形态
		{"86138001380", "", false},    // 11 位但 8 开头:不是前缀形态也不是合法手机号
		{"8623800138000", "", false},  // 86 前缀剥掉后 2 开头:仍不合法
		{"86138001380001", "", false}, // 14 位:超长
		{"23800138000", "", false},
		{"1380013800", "", false},
		{"138001380001", "", false},
		{"abcdefghijk", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, err := NormalizePhone(c.in)
		if c.ok && (err != nil || got != c.want) {
			t.Fatalf("NormalizePhone(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
		if !c.ok && err == nil {
			t.Fatalf("NormalizePhone(%q) accepted, want refused", c.in)
		}
	}
}

// D-X1: the same subscriber number in prefix/separator forms must normalize to
// the same bare value, hence the same fingerprint and the same dedup key —
// otherwise the "+86 form" would bypass the bare form's idempotency.
func TestPhoneFormsShareFingerprintAndDedupKey(t *testing.T) {
	const pepper = "pepper-x"
	now := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	bare, err := NormalizePhone("13800138000")
	if err != nil {
		t.Fatalf("bare: %v", err)
	}
	for _, form := range []string{"+8613800138000", "8613800138000", "+86 138-0013 8000"} {
		got, err := NormalizePhone(form)
		if err != nil || got != bare {
			t.Fatalf("NormalizePhone(%q) = %q, %v; want %q", form, got, err, bare)
		}
		// production derivation: fingerprint/dedup consume the NORMALIZED output
		fpForm := PhoneFingerprint(pepper, got)
		fpBare := PhoneFingerprint(pepper, bare)
		if fpForm != fpBare {
			t.Fatalf("fingerprint diverges for %q", form)
		}
		if DedupKey(pepper, "cmp_1", fpForm, now) != DedupKey(pepper, "cmp_1", fpBare, now) {
			t.Fatalf("dedup key diverges for %q", form)
		}
	}
}

func TestFingerprintsStableAndSalted(t *testing.T) {
	fp1 := PhoneFingerprint("pepper-x", "13800138000")
	fp2 := PhoneFingerprint("pepper-x", "13800138000")
	fp3 := PhoneFingerprint("pepper-y", "13800138000")
	if fp1 != fp2 || fp1 == fp3 || len(fp1) != 64 {
		t.Fatalf("fingerprint determinism/salting broken: %q %q %q", fp1, fp2, fp3)
	}
	now := time.Date(2026, 9, 19, 10, 59, 0, 0, time.UTC)
	dk1 := DedupKey("p", "cmp_1", fp1, now)
	dk2 := DedupKey("p", "cmp_1", fp1, now.Add(time.Hour))    // same UTC day
	dk3 := DedupKey("p", "cmp_1", fp1, now.Add(14*time.Hour)) // next UTC day
	dk4 := DedupKey("p", "cmp_2", fp1, now)                   // other campaign
	if dk1 != dk2 {
		t.Fatal("same-day dedup key must be stable")
	}
	if dk1 == dk3 || dk1 == dk4 {
		t.Fatal("dedup key must rotate by day bucket and campaign")
	}
}

func TestNoticeVersionPinned(t *testing.T) {
	n := CurrentNotice()
	if n.Version != "v1" || n.Text == "" {
		t.Fatalf("notice = %+v", n)
	}
}

func buildTestEnvelope() Envelope {
	return BuildSubmitEnvelope(
		"touch-engine", "crm-app", "tnt_1", "cmp_1", "sto_1", "wecom", "tag", "ast_1",
		"sub_abc", "v1", "2026-09-19T00:00:00Z", true, "张三", "13800138000", "", 1)
}

func TestEnvelopeShapeContract(t *testing.T) {
	e := buildTestEnvelope()
	if e.EventProfile.ProfileVersion != ProfileVersion {
		t.Fatalf("profile version = %q", e.EventProfile.ProfileVersion)
	}
	if e.EventProfile.EventType != EventLeadAuthorizedSubmitted {
		t.Fatalf("event type = %q", e.EventProfile.EventType)
	}
	if e.EventProfile.SourceApp != "touch-engine" || e.EventProfile.TargetApp != "crm-app" {
		t.Fatalf("app routing = %q -> %q", e.EventProfile.SourceApp, e.EventProfile.TargetApp)
	}
	if e.EventProfile.TenantScope != "tnt_1" {
		t.Fatalf("tenant scope = %q, want the campaign owner tenant", e.EventProfile.TenantScope)
	}
	if e.EventProfile.PayloadRef.Ref != RecordRef("sub_abc") || len(e.EventProfile.PayloadRef.SHA256) != 64 {
		t.Fatalf("payload ref = %+v", e.EventProfile.PayloadRef)
	}
	if !e.Payload.MarketingOptin || e.Payload.ConsentVersion != "v1" {
		t.Fatalf("payload consent refs = %+v", e.Payload)
	}
}

func TestMarshalEnvelopeRejectsContactPII(t *testing.T) {
	// The legit envelope must marshal clean even though it was built from a record that holds a phone.
	b, err := MarshalEnvelope(buildTestEnvelope())
	if err != nil {
		t.Fatalf("legit envelope refused: %v", err)
	}
	raw := string(b)
	if strings.Contains(raw, "13800138000") || strings.Contains(raw, "张三") {
		t.Fatal("envelope carries contact plaintext")
	}

	// contact-shaped values smuggled into free fields are refused before leaving
	for name, mut := range map[string]func(*Envelope){
		"phone-value": func(e *Envelope) { e.Payload.Tag = "13800138000" },
		"email-value": func(e *Envelope) { e.Payload.Tag = "reach me at a@b.com" },
	} {
		e := buildTestEnvelope()
		mut(&e)
		if _, err := MarshalEnvelope(e); err == nil {
			t.Fatalf("%s: expected refusal", name)
		}
	}
	// key-name detection is asserted at the scanner level (struct keys are fixed
	// and clean by construction): contact-named keys are flagged.
	if hits := ScanForContactPII(`{"contact_phone":"x"}`); len(hits) == 0 {
		t.Fatal("contact-named key must be flagged")
	}
}

func TestScanForContactPIIReasons(t *testing.T) {
	cases := map[string]bool{
		`{"contact_phone":"13800138000"}`:       true,
		`{"email":"a@b.com"}`:                   true,
		`{"note":"call +8613800138000 now"}`:    true,
		`{"payload":{"wechat":"abc"}}`:          true,
		`{"hash":"1f2e3d4c5b6a798812345678ab"}`: false, // hex runs are not phone shapes
		`{"campaign_ref":"cmp_abc"}`:            false,
		`{"consent_at":"2026-09-19T00:00:00Z"}`: false,
	}
	for raw, wantHit := range cases {
		hits := ScanForContactPII(raw)
		if wantHit && len(hits) == 0 {
			t.Fatalf("%s must be flagged", raw)
		}
		if !wantHit && len(hits) > 0 {
			t.Fatalf("%s falsely flagged: %v", raw, hits)
		}
	}
}

func TestRevokeEnvelopeHigherVersion(t *testing.T) {
	e := BuildRevokeEnvelope("touch-engine", "crm-app", "tnt_1", "cmp_1", "sub_abc", "2026-09-19T00:00:00Z", 2, "访客甲", "13800001234", "")
	if e.EventProfile.EventType != EventLeadConsentRevoked {
		t.Fatalf("event type = %q", e.EventProfile.EventType)
	}
	if e.EventProfile.SourceVersion != 2 || !e.Payload.Revoked {
		t.Fatalf("revoke = %+v", e)
	}
	// HUI-3013: the target re-fetches the record and verifies payload_ref.sha256
	// against the fetched values BEFORE it converges the withdrawal. The revoke
	// fact must therefore pin the SAME record provenance hash as the submit
	// fact; an empty-record hash is a guaranteed 409 payload_hash_mismatch and
	// the revoke dead-letters (observed end to end).
	if want := RecordSHA256("访客甲", "13800001234", ""); e.EventProfile.PayloadRef.SHA256 != want {
		t.Fatalf("payload sha = %q, want record provenance hash %q", e.EventProfile.PayloadRef.SHA256, want)
	}
	if e.EventProfile.PayloadRef.SHA256 == RecordSHA256("", "", "") {
		t.Fatal("empty-record hash must never travel on a revoke fact")
	}
	b, err := MarshalEnvelope(e)
	if err != nil {
		t.Fatalf("revoke envelope refused: %v", err)
	}
	var probe map[string]any
	if err := json.Unmarshal(b, &probe); err != nil {
		t.Fatal(err)
	}
	if _, ok := probe["payload"]; !ok {
		t.Fatal("top-level payload key missing")
	}
}

func TestRateLimiterWindow(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewRateLimiter(2, time.Minute)
	l.now = func() time.Time { return now }
	if !l.Allow("1.2.3.4") || !l.Allow("1.2.3.4") {
		t.Fatal("first two hits must pass")
	}
	if l.Allow("1.2.3.4") {
		t.Fatal("third hit inside window must be refused")
	}
	if !l.Allow("5.6.7.8") {
		t.Fatal("other keys independent")
	}
	now = now.Add(time.Minute + time.Second)
	if !l.Allow("1.2.3.4") {
		t.Fatal("window must slide")
	}
}
