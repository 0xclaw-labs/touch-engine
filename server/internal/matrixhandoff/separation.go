package matrixhandoff

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ForbiddenCustomerFieldKeys lists merchant account, scheduling credential,
// quota, billing/payer, matrix publish plan, and internal governance keys
// that MUST NEVER appear in any customer-facing payload.
var ForbiddenCustomerFieldKeys = []string{
	"account_pool",
	"merchant_account",
	"merchant_accounts",
	"brand_account",
	"brand_accounts",
	"social_account",
	"social_accounts",
	"social_secret",
	"schedule_token",
	"schedule_credential",
	"schedule_credentials",
	"scheduling_token",
	"scheduling_credential",
	"matrix_plan",
	"matrix_plan_id",
	"plan_ref",
	"plan_id",
	"publish_plan",
	"publish_plan_id",
	"merchant_quota",
	"brand_quota",
	"tool_quota",
	"quota",
	"quota_remaining",
	"payer_id",
	"license_ref",
	"license_expires_at",
	"grant_ref",
	"grant_refs",
	"grant_until",
	"access_token",
	"refresh_token",
	"client_secret",
	"app_secret",
	"created_by",
	"adopted_by",
	"blocked_reason",
	"idempotency_key",
	"tenant_id",
	"order_ref",
	"internal_notes",
	"operator_admin",
	"delegate_id",
	"eco_top_nav",
	"ecotopnav",
}

var forbiddenCustomerKeySet = func() map[string]bool {
	m := make(map[string]bool, len(ForbiddenCustomerFieldKeys))
	for _, k := range ForbiddenCustomerFieldKeys {
		m[k] = true
	}
	return m
}()

// IsForbiddenCustomerKey reports whether a JSON field key carries merchant
// account, scheduling credential, quota, matrix plan, or internal management
// metadata that must be stripped from customer-facing responses.
func IsForbiddenCustomerKey(key string) bool {
	lk := strings.ToLower(strings.TrimSpace(key))
	if lk == "" {
		return false
	}
	if forbiddenCustomerKeySet[lk] {
		return true
	}
	for _, frag := range []string{
		"account_pool",
		"merchant_account",
		"brand_account",
		"social_secret",
		"schedule_cred",
		"schedule_token",
		"matrix_plan",
		"publish_plan",
		"quota",
		"payer_id",
		"license_ref",
		"grant_ref",
		"access_token",
		"refresh_token",
		"ecotopnav",
	} {
		if strings.Contains(lk, frag) {
			return true
		}
	}
	return false
}

// StripMerchantFields recursively removes all merchant-only keys (accounts,
// scheduling credentials, quotas, matrix plans, internal management metadata)
// from a map payload before customer-side rendering. Input is not mutated.
func StripMerchantFields(in map[string]any) (clean map[string]any, stripped []string) {
	if in == nil {
		return nil, nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		if IsForbiddenCustomerKey(k) {
			stripped = append(stripped, k)
			continue
		}
		sanitizedVal, nestedStripped := stripValue(v)
		if len(nestedStripped) > 0 {
			stripped = append(stripped, nestedStripped...)
		}
		out[k] = sanitizedVal
	}
	return out, stripped
}

func stripValue(v any) (any, []string) {
	switch tv := v.(type) {
	case map[string]any:
		return StripMerchantFields(tv)
	case []any:
		out := make([]any, len(tv))
		var stripped []string
		for i, item := range tv {
			sv, st := stripValue(item)
			out[i] = sv
			if len(st) > 0 {
				stripped = append(stripped, st...)
			}
		}
		return out, stripped
	case []map[string]any:
		out := make([]map[string]any, len(tv))
		var stripped []string
		for i, item := range tv {
			sv, st := StripMerchantFields(item)
			out[i] = sv
			if len(st) > 0 {
				stripped = append(stripped, st...)
			}
		}
		return out, stripped
	default:
		return v, nil
	}
}

// AssertCustomerPayloadClean decodes a JSON payload and verifies that no
// merchant account, scheduling credential, quota, matrix plan, or internal
// management field leaked into a customer-facing response.
func AssertCustomerPayloadClean(raw []byte) error {
	var parsed any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return fmt.Errorf("customer payload is not valid JSON: %w", err)
	}
	var leaked []string
	walkCustomerPayload("", parsed, &leaked)
	if len(leaked) > 0 {
		return fmt.Errorf("customer payload leaked merchant/internal keys: %s", strings.Join(leaked, ", "))
	}
	return nil
}

func walkCustomerPayload(prefix string, v any, leaked *[]string) {
	switch tv := v.(type) {
	case map[string]any:
		for k, val := range tv {
			path := k
			if prefix != "" {
				path = prefix + "." + k
			}
			if IsForbiddenCustomerKey(k) {
				*leaked = append(*leaked, path)
				continue
			}
			walkCustomerPayload(path, val, leaked)
		}
	case []any:
		for i, item := range tv {
			walkCustomerPayload(fmt.Sprintf("%s[%d]", prefix, i), item, leaked)
		}
	}
}
