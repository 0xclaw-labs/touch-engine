package leads

import (
	"strings"
	"testing"
)

func TestAttachSourceTraceKeepsContactOut(t *testing.T) {
	e := BuildSubmitEnvelope("touch-engine", "leads-engine", "tnt_a", "cmp_1", "sto_1", "qr", "", "", "sub_1", "v1", "2026-10-10T00:00:00Z", true, "张三", "13800138000", "wxid", 1)
	before := e.EventProfile.SourceVersion
	e.AttachSourceTrace(SourceTrace{
		TraceID:         "lead:sub_1",
		GrantRef:        "ord_opaque",
		ReturnTarget:    "/c/abc",
		CampaignVersion: "7",
		AssetRef:        "ast_trace",
	})
	if e.EventProfile.SourceVersion != before || e.EventProfile.SourceApp != "touch-engine" || e.EventProfile.TargetApp != "leads-engine" {
		t.Fatalf("routing or version changed: %+v", e.EventProfile)
	}
	raw, err := MarshalEnvelope(e)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, key := range []string{`"trace_id":"lead:sub_1"`, `"grant_ref":"ord_opaque"`, `"return_target":"/c/abc"`, `"campaign_version":"7"`, `"asset_ref":"ast_trace"`} {
		if !strings.Contains(text, key) {
			t.Fatalf("missing %s in %s", key, text)
		}
	}
	for _, forbidden := range []string{"13800138000", "张三", "wxid", `"phone"`, `"name"`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("contact leaked %q in %s", forbidden, text)
		}
	}
}
