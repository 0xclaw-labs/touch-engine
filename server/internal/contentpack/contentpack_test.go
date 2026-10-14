package contentpack

import (
	"strings"
	"testing"
	"time"
)

func validVideoSpec() VersionSpec {
	return VersionSpec{
		ContentKind: ContentKindVideo,
		Video:       AssetSnapshot{AssetID: "las_1", AssetRef: "up_1", SHA256: strings.Repeat("a", 64)},
		Cover:       AssetSnapshot{AssetID: "las_2", AssetRef: "up_2", SHA256: strings.Repeat("b", 64)},
		Title:       "周末开业五折",
		Body:        "本店周末全场五折，欢迎到店。",
		Topics:      []string{"开业", "五折"},
		StoreName:   "派诺旗舰店",
		PriceNote:   "全场五折",
		ValidNote:   "2026-10-14 至 2026-10-20",
		PlatformVersions: []PlatformVersion{
			{Platform: "douyin", Note: "竖版 9:16", SourceVersion: "src-1"},
			{Platform: "shipinhao", Note: "横版 16:9", SourceVersion: "src-1"},
		},
		GrantRefs: []string{"grant-self-1"},
		Marking:   Marking{AdRequired: true},
		Source:    SourceMerchantUpload,
	}
}

func TestValidateVersionSpec(t *testing.T) {
	if err := ValidateVersionSpec(validVideoSpec()); err != nil {
		t.Fatalf("valid video spec rejected: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*VersionSpec)
		reason string
	}{
		{"empty title", func(s *VersionSpec) { s.Title = " " }, ReasonMissingTitle},
		{"overlong title", func(s *VersionSpec) { s.Title = strings.Repeat("题", MaxTitle+1) }, ReasonBadTitle},
		{"empty body", func(s *VersionSpec) { s.Body = "" }, ReasonMissingBody},
		{"missing video ref", func(s *VersionSpec) { s.Video = AssetSnapshot{} }, ReasonMissingVideoRef},
		{"missing cover ref", func(s *VersionSpec) { s.Cover = AssetSnapshot{} }, ReasonMissingCoverRef},
		{"missing store name", func(s *VersionSpec) { s.StoreName = " " }, ReasonMissingStoreName},
		{"missing price note", func(s *VersionSpec) { s.PriceNote = "" }, ReasonMissingPriceNote},
		{"missing valid note", func(s *VersionSpec) { s.ValidNote = "" }, ReasonMissingValidNote},
		{"bad source", func(s *VersionSpec) { s.Source = Source("engine") }, ReasonBadSource},
		{"missing platform in adaptation", func(s *VersionSpec) {
			s.PlatformVersions[0].Platform = " "
		}, ReasonMissingPlatform},
		{"missing source traceability", func(s *VersionSpec) {
			s.PlatformVersions[1].SourceVersion = ""
		}, ReasonBadPlatformVersion},
		{"bad grant until", func(s *VersionSpec) { s.GrantUntil = "yesterday" }, ReasonBadGrantUntil},
		{"overlong idempotency key", func(s *VersionSpec) {
			s.IdempotencyKey = strings.Repeat("k", MaxIdempotencyKey+1)
		}, ReasonBadIdempotencyKey},
	}
	for _, tc := range cases {
		spec := validVideoSpec()
		tc.mutate(&spec)
		err := ValidateVersionSpec(spec)
		ve, ok := err.(*ValidationError)
		if !ok || ve.Reason != tc.reason {
			t.Fatalf("%s: want %s, got %v", tc.name, tc.reason, err)
		}
	}

	// imagetext: needs at least one image, no video/cover requirement.
	it := validVideoSpec()
	it.ContentKind = ContentKindImageText
	it.Video = AssetSnapshot{}
	it.Cover = AssetSnapshot{}
	it.ImageText = []AssetSnapshot{{AssetID: "las_3", AssetRef: "up_3", SHA256: strings.Repeat("c", 64)}}
	if err := ValidateVersionSpec(it); err != nil {
		t.Fatalf("valid imagetext spec rejected: %v", err)
	}
	itEmpty := it
	itEmpty.ImageText = nil
	if ve, ok := ValidateVersionSpec(itEmpty).(*ValidationError); !ok || ve.Reason != ReasonMissingImageText {
		t.Fatalf("empty imagetext: want %s, got %v", ReasonMissingImageText, ValidateVersionSpec(itEmpty))
	}

	// bad content kind
	bad := validVideoSpec()
	bad.ContentKind = ContentKind("slideshow")
	if ve, ok := ValidateVersionSpec(bad).(*ValidationError); !ok || ve.Reason != ReasonBadContentKind {
		t.Fatalf("bad kind: got %v", ValidateVersionSpec(bad))
	}
}

func TestSnapshotAnchorsValidated(t *testing.T) {
	spec := validVideoSpec()
	spec.Video.SHA256 = "nothex"
	if ve, ok := ValidateVersionSpec(spec).(*ValidationError); !ok || ve.Reason != ReasonBadSnapshot {
		t.Fatalf("bad snapshot sha: got %v", ValidateVersionSpec(spec))
	}
	spec = validVideoSpec()
	spec.Video.AssetID = ""
	if ve, ok := ValidateVersionSpec(spec).(*ValidationError); !ok || ve.Reason != ReasonBadSnapshot {
		t.Fatalf("missing snapshot id: got %v", ValidateVersionSpec(spec))
	}
}

func TestGrantExpired(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	if GrantExpired("", now) {
		t.Fatal("empty grant_until never expires")
	}
	if GrantExpired("2026-10-15T00:00:00Z", now) {
		t.Fatal("future grant_until must not be expired")
	}
	if !GrantExpired("2026-10-14T00:00:00Z", now) {
		t.Fatal("past grant_until must be expired")
	}
}

func TestDistributable(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	ok := []VersionStatus{StatusCandidate, StatusAdopted, StatusDraft}
	for _, s := range ok {
		if err := Distributable(s, "", now); err != nil {
			t.Fatalf("status %s should distribute: %v", s, err)
		}
	}
	if err := Distributable(StatusBlocked, "", now); err == nil {
		t.Fatal("blocked must not distribute")
	} else if ve, ok := err.(*ValidationError); !ok || ve.Reason != ReasonNotDistributable {
		t.Fatalf("blocked reason: got %v", err)
	}
	if err := Distributable(StatusAdopted, "2026-10-01T00:00:00Z", now); err == nil {
		t.Fatal("expired grant must not distribute")
	} else if ve, ok := err.(*ValidationError); !ok || ve.Reason != ReasonGrantExpired {
		t.Fatalf("expired reason: got %v", err)
	}
}

func TestValidateBlockReason(t *testing.T) {
	if err := ValidateBlockReason("素材授权被平台撤销"); err != nil {
		t.Fatalf("valid block reason rejected: %v", err)
	}
	if ve, ok := ValidateBlockReason("  ").(*ValidationError); !ok || ve.Reason != ReasonMissingBlockReason {
		t.Fatalf("empty block reason: got %v", ValidateBlockReason("  "))
	}
}

func TestValidateIdempotencyKey(t *testing.T) {
	if err := ValidateIdempotencyKey("return-20261014-0001"); err != nil {
		t.Fatalf("valid key rejected: %v", err)
	}
	if ve, ok := ValidateIdempotencyKey("").(*ValidationError); !ok || ve.Reason != ReasonMissingIdempotencyKey {
		t.Fatalf("empty key: got %v", ValidateIdempotencyKey(""))
	}
}

func TestHandoffRoundTrip(t *testing.T) {
	secret := "test-secret"
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	claim, err := NewHandoffClaim("cpv_1", "las_1", strings.Repeat("a", 64), "video/mp4", "attachment", "shop-video.mp4", now, 0)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	token, err := MintHandoff(secret, claim)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	got, err := VerifyHandoff(secret, token, now.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.VersionID != "cpv_1" || got.AssetID != "las_1" || got.ContentType != "video/mp4" ||
		got.Disposition != "attachment" || got.Filename != "shop-video.mp4" {
		t.Fatalf("claim mismatch: %+v", got)
	}
	if !strings.HasSuffix(got.ContentDisposition(), `attachment; filename="shop-video.mp4"`) {
		t.Fatalf("disposition: %q", got.ContentDisposition())
	}
}

func TestHandoffRejects(t *testing.T) {
	secret := "test-secret"
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

	// expired at verify
	claim, _ := NewHandoffClaim("cpv_1", "las_1", strings.Repeat("a", 64), "video/mp4", "inline", "v.mp4", now, time.Hour)
	token, err := MintHandoff(secret, claim)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if _, err := VerifyHandoff(secret, token, now.Add(2*time.Hour)); err == nil {
		t.Fatal("expired token must fail")
	}
	// tampered payload
	if _, err := VerifyHandoff(secret, token[:len(token)-2]+"xx", now); err == nil {
		t.Fatal("tampered token must fail")
	}
	// wrong secret
	if _, err := VerifyHandoff("other", token, now); err == nil {
		t.Fatal("wrong secret must fail")
	}

	// TTL above the cap
	if _, err := NewHandoffClaim("cpv_1", "las_1", strings.Repeat("a", 64), "video/mp4", "inline", "v.mp4", now, 25*time.Hour); err == nil {
		t.Fatal("ttl above cap must fail")
	}
	// bad content type
	if _, err := NewHandoffClaim("cpv_1", "las_1", strings.Repeat("a", 64), "application/octet-stream", "inline", "v.bin", now, 0); err == nil {
		t.Fatal("non-whitelisted content type must fail")
	}
	// attachment requires a filename
	if _, err := NewHandoffClaim("cpv_1", "las_1", strings.Repeat("a", 64), "video/mp4", "attachment", "", now, 0); err == nil {
		t.Fatal("attachment without filename must fail")
	}
}
