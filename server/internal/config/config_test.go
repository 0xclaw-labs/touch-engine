package config

import (
	"strings"
	"testing"
)

func TestDefaults(t *testing.T) {
	cfg := Load(func(string) string { return "" })
	if cfg.HTTPAddr != "127.0.0.1:18240" {
		t.Fatalf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.DBPath != "data/touch.db" {
		t.Fatalf("DBPath = %q", cfg.DBPath)
	}
	if cfg.AppID != "touch-engine" {
		t.Fatalf("AppID = %q", cfg.AppID)
	}
	if cfg.SessionCookie != "touch_session" {
		t.Fatalf("SessionCookie = %q", cfg.SessionCookie)
	}
	if cfg.FeatureUpload || cfg.FeatureNotify || cfg.FeatureTask || cfg.FeatureLeadsCapture {
		t.Fatal("feature flags must default to off")
	}
	if cfg.FeatureDashboard || cfg.FeatureCampaignRules {
		t.Fatal("feature flags must default to off")
	}
	if cfg.FeatureAssetLib {
		t.Fatal("feature flags must default to off")
	}
	if cfg.FeatureVideoTemplates {
		t.Fatal("feature flags must default to off")
	}
}

// HUI-1676 FEAT-0177: FEATURE_CAMPAIGN_RULES is a registry-style flag, default
// off. It needs NO extra platform configuration (rules live entirely inside
// this app), so it must never add Gate() entries.
func TestFeatureCampaignRulesFlag(t *testing.T) {
	base := map[string]string{
		"TOUCH_INTERNAL_TOKEN":       "s",
		"PLATFORM_IDENTITY_BASE_URL": "http://127.0.0.1:18101",
		"PLATFORM_IDENTITY_TOKEN":    "t",
	}
	cfg := Load(func(k string) string { return base[k] })
	if cfg.FeatureCampaignRules {
		t.Fatal("campaign rules flag must default to off")
	}
	base["FEATURE_CAMPAIGN_RULES"] = "on"
	cfg = Load(func(k string) string { return base[k] })
	if !cfg.FeatureCampaignRules {
		t.Fatal("FEATURE_CAMPAIGN_RULES=on must enable the flag")
	}
	for _, p := range cfg.Gate() {
		if strings.Contains(p, "CAMPAIGN_RULES") {
			t.Fatalf("campaign rules must not add gate entries: %v", p)
		}
	}
}

// HUI-1666 FEAT-0167: FEATURE_ASSET_LIB is a registry-style flag, default off.
// Like FEATURE_CAMPAIGN_RULES it needs NO extra platform configuration of its
// own (asset rows are reference records; reference VALIDATION reuses
// FEATURE_UPLOAD's client), so it must never add Gate() entries.
func TestFeatureAssetLibFlag(t *testing.T) {
	base := map[string]string{
		"TOUCH_INTERNAL_TOKEN":       "s",
		"PLATFORM_IDENTITY_BASE_URL": "http://127.0.0.1:18101",
		"PLATFORM_IDENTITY_TOKEN":    "t",
	}
	cfg := Load(func(k string) string { return base[k] })
	if cfg.FeatureAssetLib {
		t.Fatal("asset lib flag must default to off")
	}
	base["FEATURE_ASSET_LIB"] = "on"
	cfg = Load(func(k string) string { return base[k] })
	if !cfg.FeatureAssetLib {
		t.Fatal("FEATURE_ASSET_LIB=on must enable the flag")
	}
	for _, p := range cfg.Gate() {
		if strings.Contains(p, "ASSET_LIB") {
			t.Fatalf("asset lib must not add gate entries: %v", p)
		}
	}
}

// HUI-1669 FEAT-0170: FEATURE_VIDEO_TEMPLATES is a registry-style flag, default
// off. It needs NO extra platform configuration of its own (templates are
// reference organizations over the existing asset library), so it must never
// add Gate() entries.
func TestFeatureVideoTemplatesFlag(t *testing.T) {
	base := map[string]string{
		"TOUCH_INTERNAL_TOKEN":       "s",
		"PLATFORM_IDENTITY_BASE_URL": "http://127.0.0.1:18101",
		"PLATFORM_IDENTITY_TOKEN":    "t",
	}
	cfg := Load(func(k string) string { return base[k] })
	if cfg.FeatureVideoTemplates {
		t.Fatal("video templates flag must default to off")
	}
	base["FEATURE_VIDEO_TEMPLATES"] = "on"
	cfg = Load(func(k string) string { return base[k] })
	if !cfg.FeatureVideoTemplates {
		t.Fatal("FEATURE_VIDEO_TEMPLATES=on must enable the flag")
	}
	for _, p := range cfg.Gate() {
		if strings.Contains(p, "VIDEO_TEMPLATES") {
			t.Fatalf("video templates must not add gate entries: %v", p)
		}
	}
}

func TestGate(t *testing.T) {
	// empty config: three required keys missing
	cfg := Load(func(string) string { return "" })
	problems := cfg.Gate()
	if len(problems) != 3 {
		t.Fatalf("gate problems = %v, want 3 entries", problems)
	}

	// fully configured base: no problems
	full := map[string]string{
		"TOUCH_INTERNAL_TOKEN":       "s",
		"PLATFORM_IDENTITY_BASE_URL": "http://127.0.0.1:18101",
		"PLATFORM_IDENTITY_TOKEN":    "t",
	}
	cfg = Load(func(k string) string { return full[k] })
	if problems := cfg.Gate(); len(problems) != 0 {
		t.Fatalf("gate problems = %v, want none", problems)
	}

	// feature upload on without upload config: explicit gate entries
	full["FEATURE_UPLOAD"] = "on"
	cfg = Load(func(k string) string { return full[k] })
	problems = cfg.Gate()
	if len(problems) != 2 {
		t.Fatalf("gate problems = %v, want 2 upload entries", problems)
	}
	full["PLATFORM_UPLOAD_BASE_URL"] = "http://127.0.0.1:18104"
	full["PLATFORM_UPLOAD_TOKEN"] = "ut"
	cfg = Load(func(k string) string { return full[k] })
	if problems := cfg.Gate(); len(problems) != 0 {
		t.Fatalf("gate problems = %v, want none", problems)
	}
}

func TestGateNotifyTaskScaffolding(t *testing.T) {
	env := map[string]string{
		"TOUCH_INTERNAL_TOKEN":       "s",
		"PLATFORM_IDENTITY_BASE_URL": "http://127.0.0.1:18101",
		"PLATFORM_IDENTITY_TOKEN":    "t",
		"FEATURE_NOTIFY":             "1",
		"FEATURE_TASK":               "true",
	}
	cfg := Load(func(k string) string { return env[k] })
	if len(cfg.Gate()) != 4 {
		t.Fatalf("gate problems = %v, want 4 (2 notify + 2 task)", cfg.Gate())
	}
	env["PLATFORM_NOTIFY_BASE_URL"] = "http://127.0.0.1:18105"
	env["PLATFORM_NOTIFY_TOKEN"] = "n"
	env["PLATFORM_TASK_BASE_URL"] = "http://127.0.0.1:18103"
	env["PLATFORM_TASK_TOKEN"] = "k"
	cfg = Load(func(k string) string { return env[k] })
	if len(cfg.Gate()) != 0 {
		t.Fatalf("gate problems = %v, want none", cfg.Gate())
	}
}

func TestGateLeadsCapture(t *testing.T) {
	base := map[string]string{
		"TOUCH_INTERNAL_TOKEN":       "s",
		"PLATFORM_IDENTITY_BASE_URL": "http://127.0.0.1:18101",
		"PLATFORM_IDENTITY_TOKEN":    "t",
	}
	// off (default): no leads-specific gate entries
	cfg := Load(func(k string) string { return base[k] })
	for _, p := range cfg.Gate() {
		if strings.Contains(p, "LEADS") {
			t.Fatalf("leads gate entry with feature off: %v", p)
		}
	}
	// on without config: 4 explicit entries (notify url/token, target app, pepper)
	base["FEATURE_LEADS_CAPTURE"] = "on"
	cfg = Load(func(k string) string { return base[k] })
	if len(cfg.Gate()) != 4 {
		t.Fatalf("gate problems = %v, want 4 leads entries", cfg.Gate())
	}
	base["PLATFORM_NOTIFY_BASE_URL"] = "http://127.0.0.1:18105"
	base["PLATFORM_NOTIFY_TOKEN"] = "n"
	base["LEADS_TARGET_APP_ID"] = "crm-app"
	base["LEADS_PHONE_PEPPER"] = "dev-pepper"
	cfg = Load(func(k string) string { return base[k] })
	if problems := cfg.Gate(); len(problems) != 0 {
		t.Fatalf("gate problems = %v, want none", problems)
	}
}

func TestProductionFlag(t *testing.T) {
	cfg := Load(func(k string) string {
		if k == "TOUCH_ENV" {
			return "production"
		}
		return ""
	})
	if !cfg.Production() {
		t.Fatal("Production() = false for TOUCH_ENV=production")
	}
}

// HUI-1664: PUBLIC_BASE_URL is the QR payload base. It is deliberately NOT in
// Gate(): QR availability must not take down the whole authenticated surface;
// the QR endpoint fails closed on its own (503 qr_not_configured).
func TestPublicBaseURL(t *testing.T) {
	cfg := Load(func(string) string { return "" })
	if cfg.PublicBaseURL != "" {
		t.Fatalf("PublicBaseURL default = %q, want empty", cfg.PublicBaseURL)
	}
	cfg = Load(func(k string) string {
		if k == "PUBLIC_BASE_URL" {
			return " https://h5.example.com/ "
		}
		return ""
	})
	if cfg.PublicBaseURL != "https://h5.example.com/" {
		t.Fatalf("PublicBaseURL = %q, want whitespace-trimmed (trailing slash is handled by qrentry)", cfg.PublicBaseURL)
	}
	// and it must not affect the gate either way
	if problems := cfg.Gate(); len(problems) != 3 {
		t.Fatalf("gate with only PUBLIC_BASE_URL set = %v, want 3 base entries", problems)
	}
}

// HUI-1671 B3: FEATURE_REWARD_OPS gates only the guest-reachable public
// reward surface (summary). It needs no extra platform configuration, so it
// must never add Gate() entries. Default off.
func TestFeatureRewardOpsDefaultOff(t *testing.T) {
	cfg := Load(func(string) string { return "" })
	if cfg.FeatureRewardOps {
		t.Fatal("FEATURE_REWARD_OPS must default to off")
	}
	on := Load(func(key string) string {
		if key == EnvFeatureRewardOps {
			return "1"
		}
		return ""
	})
	if !on.FeatureRewardOps {
		t.Fatal("FEATURE_REWARD_OPS=1 must turn the flag on")
	}
}

// HUI-2222: FEATURE_SERVICE_DESK gates the merchant-facing "确认与核销" entry
// slot (web nav) only. The /work/service page itself belongs to HUI-3562, so
// this flag registers no route and needs no extra platform configuration —
// it must never add Gate() entries. Default off.
func TestFeatureServiceDeskDefaultOff(t *testing.T) {
	cfg := Load(func(string) string { return "" })
	if cfg.FeatureServiceDesk {
		t.Fatal("FEATURE_SERVICE_DESK must default to off")
	}
	on := Load(func(key string) string {
		if key == EnvFeatureServiceDesk {
			return "on"
		}
		return ""
	})
	if !on.FeatureServiceDesk {
		t.Fatal("FEATURE_SERVICE_DESK=on must turn the flag on")
	}
	for _, problem := range on.Gate() {
		if strings.Contains(problem, "FEATURE_SERVICE_DESK") {
			t.Fatalf("FEATURE_SERVICE_DESK must not add Gate entries, got: %v", problem)
		}
	}
}
