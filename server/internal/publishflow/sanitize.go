package publishflow

import (
	"regexp"
	"strings"
)

// technicalTokens lists internal protocol, credential, adapter, and deployment
// diagnostic identifiers that MUST NOT surface in customer-facing text.
var technicalTokens = []string{
	"client_key",
	"client_secret",
	"access_token",
	"refresh_token",
	"oauth2",
	"oauth",
	"scope",
	"open_id",
	"photo_id",
	"share_id",
	"nonce_str",
	"signature",
	"idempotency_key",
	"visitor_id",
	"tenant_id",
	"action_session_id",
	"publish_handoff",
	"kuaishou_oauth",
	"douyin_h5",
	"js_media_handoff",
	"oauth_server_push",
	"manual_material",
	"platform_fetch",
	"adapter",
	"sdk",
	"plan_ref",
	"matrix_plan",
	"publish_plan",
	"payer_id",
	"license_ref",
	"grant_ref",
	"account_pool",
	"merchant_account",
	"brand_account",
	"schedule_token",
	"schedule_credential",
	"merchant_quota",
	"tool_quota",
}

// technicalParamKeys lists parameter keys reserved for execution/diagnostics
// rather than customer-facing display text.
var technicalParamKeys = map[string]bool{
	"client_key":          true,
	"client_secret":       true,
	"scope":               true,
	"scopes":              true,
	"oauth":               true,
	"oauth_state":         true,
	"state":               true,
	"access_token":        true,
	"refresh_token":       true,
	"open_id":             true,
	"photo_id":            true,
	"share_id":            true,
	"nonce_str":           true,
	"signature":           true,
	"sdk":                 true,
	"adapter":             true,
	"tenant_id":           true,
	"visitor_id":          true,
	"idempotency_key":     true,
	"app_ref":             true,
	"merchant_app":        true,
	"plan_ref":            true,
	"matrix_plan":         true,
	"matrix_plan_id":      true,
	"publish_plan":        true,
	"publish_plan_id":     true,
	"payer_id":            true,
	"license_ref":         true,
	"grant_ref":           true,
	"account_pool":        true,
	"merchant_account":    true,
	"brand_account":       true,
	"schedule_token":      true,
	"schedule_credential": true,
	"merchant_quota":      true,
	"tool_quota":          true,
	"quota":               true,
}

var (
	reKeyValueDiagnostic = regexp.MustCompile(`(?i)\b(?:client_key|client_secret|access_token|refresh_token|scope|scopes|open_id|photo_id|share_id|nonce_str|signature|idempotency_key|visitor_id|tenant_id|action_session_id|sdk|adapter|plan_ref|matrix_plan|publish_plan|payer_id|license_ref|grant_ref|account_pool|merchant_account|brand_account|schedule_token|schedule_credential|merchant_quota|tool_quota)\s*[:=]\s*[^\s,;，；。)]+`)
	reDiagnosticWord     = regexp.MustCompile(`(?i)\b(?:client_key|client_secret|access_token|refresh_token|oauth2|oauth|scope|scopes|open_id|photo_id|share_id|nonce_str|signature|idempotency_key|visitor_id|tenant_id|action_session_id|publish_handoff|kuaishou_oauth|douyin_h5|js_media_handoff|oauth_server_push|manual_material|platform_fetch|adapter|sdk|plan_ref|matrix_plan|publish_plan|payer_id|license_ref|grant_ref|account_pool|merchant_account|brand_account|schedule_token|schedule_credential|merchant_quota|tool_quota)\b`)
	reMultiSpace         = regexp.MustCompile(`\s{2,}`)
)

// ContainsTechnicalDiagnostic reports whether s contains any internal
// technical/deployment term that should be kept out of customer copy.
func ContainsTechnicalDiagnostic(s string) bool {
	low := strings.ToLower(s)
	for _, tok := range technicalTokens {
		if strings.Contains(low, tok) {
			return true
		}
	}
	return false
}

// SanitizeCustomerText strips internal technical diagnostics (client_key,
// scope, OAuth internals, tokens, SDK/adapter jargon) from customer-visible
// text and falls back to fallback if stripping leaves an empty string.
func SanitizeCustomerText(raw, fallback string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return fallback
	}
	// First drop key=value / key:value diagnostic fragments completely.
	s = reKeyValueDiagnostic.ReplaceAllString(s, "")
	// Replace known user-facing concepts where stripping alone would leave
	// awkward grammar, then scrub any residual technical tokens.
	replacer := strings.NewReplacer(
		"OAuth 授权投稿", "账号授权提交",
		"OAuth授权投稿", "账号授权提交",
		"OAuth 授权", "账号授权",
		"OAuth授权", "账号授权",
		"OAuth2", "账号授权",
		"OAuth", "账号授权",
		"oauth2", "账号授权",
		"oauth", "账号授权",
		"官方适配器", "官方通道",
		"适配器", "官方通道",
		"JS SDK", "官方组件",
		"SDK", "官方组件",
	)
	s = replacer.Replace(s)
	s = reDiagnosticWord.ReplaceAllString(s, "")
	// Clean up dangling parentheses/punctuation left by stripped terms.
	s = strings.ReplaceAll(s, "()", "")
	s = strings.ReplaceAll(s, "（）", "")
	s = strings.ReplaceAll(s, "  ", " ")
	s = reMultiSpace.ReplaceAllString(s, " ")
	s = strings.Trim(s, " \t\r\n,:;，；")
	if s == "" || ContainsTechnicalDiagnostic(s) {
		return fallback
	}
	return s
}

// SanitizeCustomerParams splits a parameter map into customer-safe display
// metadata vs. merchant/ops-only technical diagnostics. The caller's map is
// never mutated.
func SanitizeCustomerParams(params map[string]string) (customerSafe map[string]string, diagnostics map[string]string) {
	customerSafe = map[string]string{}
	diagnostics = map[string]string{}
	for k, v := range params {
		lk := strings.ToLower(strings.TrimSpace(k))
		if technicalParamKeys[lk] || ContainsTechnicalDiagnostic(k) || ContainsTechnicalDiagnostic(v) {
			diagnostics[k] = v
			continue
		}
		customerSafe[k] = v
	}
	return customerSafe, diagnostics
}
