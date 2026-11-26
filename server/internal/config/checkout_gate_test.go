// checkout_gate_test.go: FEATURE_BILLING_CHECKOUT Gate 成对校验(HUI-4664 /
// ADV-9 PR12)。半环防线:只通 checkout 通道而不登记 billing_authority 事件
// 生产者 = 付款成功但 Touch 永不开通(GoBoost UNIFIED_BRIDGE 教训移植)。

package config

import (
	"strings"
	"testing"
)

func TestGateBillingCheckoutPairing(t *testing.T) {
	base := map[string]string{
		"TOUCH_INTERNAL_TOKEN":       "s",
		"PLATFORM_IDENTITY_BASE_URL": "http://127.0.0.1:18101",
		"PLATFORM_IDENTITY_TOKEN":    "t",
	}
	load := func(env map[string]string) Config {
		merged := map[string]string{}
		for k, v := range base {
			merged[k] = v
		}
		for k, v := range env {
			merged[k] = v
		}
		return Load(func(k string) string { return merged[k] })
	}

	// off(默认):零 Gate 项,现行为逐字节不变。
	if cfg := load(nil); len(cfg.Gate()) != 0 {
		t.Fatalf("off gate = %v, want none", cfg.Gate())
	}
	// on 但三件套都缺:三条问题(BASE_URL + TOKEN + billing_authority 生产者)。
	env := map[string]string{"FEATURE_BILLING_CHECKOUT": "on"}
	problems := load(env).Gate()
	if len(problems) != 3 {
		t.Fatalf("half-wired gate = %v, want 3 problems", problems)
	}
	// 只配 BASE_URL(缺 TOKEN 和生产者):两条问题。
	env["PLATFORM_BILLING_BASE_URL"] = "https://billing.test"
	problems = load(env).Gate()
	if len(problems) != 2 {
		t.Fatalf("missing token+producer gate = %v, want 2 problems", problems)
	}
	// 配齐 BASE_URL + TOKEN 但缺生产者(半环:付款成功永不开通):仍拒绝启动。
	env["PLATFORM_BILLING_TOKEN"] = "billing-app-token"
	problems = load(env).Gate()
	if len(problems) != 1 || !strings.Contains(problems[0], "billing_authority") {
		t.Fatalf("missing producer gate = %v, want 1 billing_authority problem", problems)
	}
	// 配了服务但角色不对:同一拒绝。
	env["TOUCH_SERVICE_TOKENS"] = "pai-billing:tok"
	env["TOUCH_SERVICE_ROLES"] = "pai-billing:queue_producer"
	if problems = load(env).Gate(); len(problems) != 1 {
		t.Fatalf("wrong role gate = %v, want 1 problem", problems)
	}
	// 全件齐:零问题。
	env["TOUCH_SERVICE_ROLES"] = "pai-billing:billing_authority"
	if cfg := load(env); len(cfg.Gate()) != 0 {
		t.Fatalf("wired gate = %v, want none", cfg.Gate())
	}
	// 未开开关但已登记生产者:零 Gate 项(入站缝按 503/403 fail-closed 既有纪律)。
	delete(env, "FEATURE_BILLING_CHECKOUT")
	if cfg := load(env); len(cfg.Gate()) != 0 {
		t.Fatalf("producer-only gate = %v, want none", cfg.Gate())
	}
}
