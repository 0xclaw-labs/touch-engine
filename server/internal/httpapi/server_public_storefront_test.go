package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

// 会让这个测试失败的生产改动：公共门店面泄漏 tenant 密钥/target_ref/内部 scope、
// 把停用门店/停用动作/待映射行端给顾客、或撤销后旧快照继续开放。

// TestPublicStorefrontWhitelistAndGating 覆盖冻结契约字段、开关双保险、
// 停用/越权状态的顾客口径。
func TestPublicStorefrontWhitelistAndGating(t *testing.T) {
	af := newActionsHTTPFixture(t, true)
	f := af.f

	// 商家配置：两启用 + 一停用；再造一条待映射 legacy 行与一条坏目标 legacy 行。
	status, _, _ := f.do(t, "PUT", "/api/v1/stores/"+af.stoA+"/actions", af.sessOwner, af.tenA, `{"expected_revision":0,"items":[
		{"catalog_code":"A08","label":"大众点评","target_ref":"https://www.dianping.com/shop/ok","order":1,"enabled":true},
		{"catalog_code":"A17","label":"门店 Wi-Fi","target_ref":"https://shop.example.com/wifi","order":2,"enabled":true},
		{"catalog_code":"A13","label":"抖音关注","target_ref":"https://www.douyin.com/user/9","order":3,"enabled":false}
	]}`)
	mustEqual(t, status, http.StatusOK)
	tenantID := af.tenA
	if _, err := f.s.St.DB.Exec(`INSERT INTO store_actions(id,tenant_id,store_id,catalog_code,platform,kind,variant,label,target_ref,
		presentation,sort_order,enabled,revision,revoked,expires_at,legacy_campaign_id,legacy_kind,created_at,updated_at)
		VALUES('sa_pending_pub','` + tenantID + `','` + af.stoA + `','','','review','','待映射','https://shop.example.com/p',
		'link',9,1,1,0,'','cmp_old','review','now','now')`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.St.DB.Exec(`INSERT INTO store_actions(id,tenant_id,store_id,catalog_code,platform,kind,variant,label,target_ref,
		presentation,sort_order,enabled,revision,revoked,expires_at,legacy_campaign_id,legacy_kind,created_at,updated_at)
		VALUES('sa_evil_pub','` + tenantID + `','` + af.stoA + `','A09','douyin','review','','旧点评','http://127.0.0.1/x',
		'link',10,1,1,0,'','cmp_old','review','now','now')`); err != nil {
		t.Fatal(err)
	}

	// 先经商家面拿到稳定 store_code。
	status, _, cfgDoc := f.do(t, "GET", "/api/v1/stores/"+af.stoA+"/actions", af.sessOwner, af.tenA, "")
	mustEqual(t, status, http.StatusOK)
	storeCode := cfgDoc["store_code"].(string)

	// 顾客读面：无会话。
	status, _, doc := f.do(t, "GET", "/api/v1/public/storefronts/"+storeCode, "", "", "")
	mustEqual(t, status, http.StatusOK)
	raw := jsonString(doc)
	for _, key := range []string{"schema_version", "store_code", "storefront_revision", "store", "sections", "actions", "activity_cards", "policy_links"} {
		if _, ok := doc[key]; !ok {
			t.Fatalf("public payload missing %q: %v", key, doc)
		}
	}
	if doc["store_code"] != storeCode || doc["storefront_revision"] != float64(0) {
		t.Fatalf("code/revision = %v/%v", doc["store_code"], doc["storefront_revision"])
	}
	st, _ := doc["store"].(map[string]any)
	if st["name"] != "甲一店" || st["state"] != "active" {
		t.Fatalf("store block = %v", st)
	}
	if _, ok := st["opening_hours"]; !ok {
		t.Fatalf("store block missing opening_hours: %v", st)
	}
	pubActions, ok := doc["actions"].([]any)
	if !ok || len(pubActions) != 2 {
		t.Fatalf("public actions = %v (停用/待映射/坏目标行不得出现)", doc["actions"])
	}
	first, _ := pubActions[0].(map[string]any)
	for _, key := range []string{"action_id", "action_revision", "catalog_code", "platform", "kind", "variant", "label", "presentation", "capability_ref"} {
		if _, present := first[key]; !present {
			t.Fatalf("public action missing %q: %v", key, first)
		}
	}
	if first["catalog_code"] != "A08" || first["presentation"] != "link" {
		t.Fatalf("first public action = %v", first)
	}
	sections, _ := doc["sections"].([]any)
	if len(sections) != 1 {
		t.Fatalf("sections = %v (本票默认单 section)", doc["sections"])
	}
	section, _ := sections[0].(map[string]any)
	sectionIDs, _ := section["action_ids"].([]any)
	if len(sectionIDs) != 2 {
		t.Fatalf("section action_ids = %v", section["action_ids"])
	}
	if len(doc["activity_cards"].([]any)) != 0 || len(doc["policy_links"].([]any)) != 0 {
		t.Fatalf("activity_cards/policy_links must be empty arrays this ticket")
	}
	// 泄漏断言：tenant id、target_ref、legacy 内部字段绝不出现。
	if strings.Contains(raw, tenantID) || strings.Contains(raw, "target_ref") ||
		strings.Contains(raw, "legacy") || strings.Contains(raw, "tenant_id") {
		t.Fatalf("public payload leaked internal fields: %s", raw)
	}

	// 停用动作不会出现在顾客面；撤销同理（先撤销再验）。
	if _, err := f.s.St.DB.Exec(`UPDATE store_actions SET revoked=1 WHERE catalog_code='A17' AND store_id='` + af.stoA + `'`); err != nil {
		t.Fatal(err)
	}
	status, _, afterRevoke := f.do(t, "GET", "/api/v1/public/storefronts/"+storeCode, "", "", "")
	mustEqual(t, status, http.StatusOK)
	if actions, _ := afterRevoke["actions"].([]any); len(actions) != 1 {
		t.Fatalf("revoked action still offered: %v", afterRevoke["actions"])
	}

	// 门店停用：state=disabled 且 actions 空（存量不级联，公共面如实标注）。
	status, _, _ = f.do(t, "POST", "/api/v1/stores/"+af.stoA+"/status", af.sessOwner, af.tenA, `{"status":"disabled"}`)
	mustEqual(t, status, http.StatusOK)
	status, _, disabled := f.do(t, "GET", "/api/v1/public/storefronts/"+storeCode, "", "", "")
	rawDisabled := jsonString(disabled)
	mustEqual(t, status, http.StatusOK)
	if disabled["store"].(map[string]any)["state"] != "disabled" {
		t.Fatalf("disabled store state = %v", disabled["store"])
	}
	if actions, _ := disabled["actions"].([]any); len(actions) != 0 {
		t.Fatalf("disabled store still offers actions: %v", disabled["actions"])
	}
	if strings.Contains(rawDisabled, "dianping.com") {
		t.Fatalf("disabled store leaked targets: %s", rawDisabled)
	}

	// 未知码 404；第二家店隔离（乙店未配置 → actions 空）。
	status, _, missing := f.do(t, "GET", "/api/v1/public/storefronts/sf_missing", "", "", "")
	if status != http.StatusNotFound {
		t.Fatalf("unknown code = %d %v", status, missing)
	}
	status, _, cfgB := f.do(t, "GET", "/api/v1/stores/"+af.stoB+"/actions", af.sessOwner, af.tenA, "")
	mustEqual(t, status, http.StatusOK)
	codeB := cfgB["store_code"].(string)
	if codeB == storeCode {
		t.Fatal("store codes must differ per store")
	}
	status, _, docB := f.do(t, "GET", "/api/v1/public/storefronts/"+codeB, "", "", "")
	mustEqual(t, status, http.StatusOK)
	if actions, _ := docB["actions"].([]any); len(actions) != 0 {
		t.Fatalf("store B must be isolated/empty: %v", docB["actions"])
	}

	// 开关 off：路由不存在（默认关，双保险之第一层）。
	off := newActionsHTTPFixture(t, false)
	status, _, offDoc := off.f.do(t, "GET", "/api/v1/public/storefronts/"+storeCode, "", "", "")
	if status != http.StatusNotFound {
		t.Fatalf("flag-off storefront = %d %v", status, offDoc)
	}
}
