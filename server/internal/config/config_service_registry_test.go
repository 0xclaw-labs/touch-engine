package config

// HUI-3553 W5：受信服务生产身份 registry（默认关闭、fail-closed）。
// env: TOUCH_SERVICE_TOKENS="svc:token;..." / TOUCH_SERVICE_ROLES="svc:queue_producer;..."。
// 坏项/重复绝不静默丢弃：进 Gate() problem（启动/请求 fail-closed），且 token 不进日志。

import (
	"strings"
	"testing"
)

func serviceProblems(problems []string) []string {
	var out []string
	for _, p := range problems {
		if strings.Contains(p, "TOUCH_SERVICE") {
			out = append(out, p)
		}
	}
	return out
}

func TestLoadServiceRegistry(t *testing.T) {
	cfg := Load(func(k string) string {
		switch k {
		case "TOUCH_SERVICE_TOKENS":
			return " modelxing-runtime:tok-1 ; goboost:tok-2;; "
		case "TOUCH_SERVICE_ROLES":
			return "modelxing-runtime:queue_producer"
		}
		return ""
	})
	if got := cfg.ServiceTokens["modelxing-runtime"]; got != "tok-1" {
		t.Fatalf("token not parsed/trimmed: %q", got)
	}
	if got := cfg.ServiceTokens["goboost"]; got != "tok-2" {
		t.Fatalf("second token not parsed: %q", got)
	}
	if got := cfg.ServiceRoles["modelxing-runtime"]; got != "queue_producer" {
		t.Fatalf("role not parsed: %q", got)
	}
	if got := cfg.ServiceProducerRole("goboost"); got != "queue_producer" {
		t.Fatalf("missing role must default to queue_producer, got %q", got)
	}
	if problems := serviceProblems(cfg.Gate()); len(problems) != 0 {
		t.Fatalf("valid registry must not add problems: %v", problems)
	}
}

func TestLoadServiceRegistryDefaultsOff(t *testing.T) {
	cfg := Load(func(string) string { return "" })
	if cfg.ServiceTokens != nil || cfg.ServiceRoles != nil {
		t.Fatalf("empty env must leave registry nil (service path closed)")
	}
	if problems := serviceProblems(cfg.Gate()); len(problems) != 0 {
		t.Fatalf("unconfigured registry must not break old behaviour: %v", problems)
	}
	if got := cfg.ServiceProducerRole("anything"); got != "" {
		t.Fatalf("unregistered service must have no role, got %q", got)
	}
}

func TestLoadServiceRegistryBadEntriesFailClosed(t *testing.T) {
	cfg := Load(func(k string) string {
		switch k {
		case "TOUCH_SERVICE_TOKENS":
			return "svc-a:tok;svc-a:tok2;:tok3;svc-b:"
		}
		return ""
	})
	if len(cfg.ServiceRegistryErrors) == 0 {
		t.Fatal("bad registry entries must be reported, not silently dropped")
	}
	if problems := serviceProblems(cfg.Gate()); len(problems) == 0 {
		t.Fatalf("bad registry must surface in Gate(), got %v", cfg.Gate())
	}
	if _, ok := cfg.ServiceTokens["svc-a"]; ok {
		t.Fatal("duplicate service id must not be registered")
	}
	if _, ok := cfg.ServiceTokens[""]; ok {
		t.Fatal("empty service id must not be registered")
	}
	if _, ok := cfg.ServiceTokens["svc-b"]; ok {
		t.Fatal("empty token service must not be registered")
	}
}
