package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// 会让这个测试失败的生产改动：公共 services 面下发 target_ref/tenant/内部字段、
// 点评面板带任何内容模板字段（零模板零AIGC双拦失守）、Wi-Fi 需要留资/会话才可读、
// 事件端点出现任何成功语义、停用行/非法 config 行端给顾客、旧 revision 写入成功。

func seedServicesStorefront(t *testing.T, af *actionsFixture) string {
	t.Helper()
	// 门店动作：点评（启用）+ 关注（启用）+ 停用关注（不出现在公共面）。
	status, _, _ := af.f.do(t, "PUT", "/api/v1/stores/"+af.stoA+"/actions", af.sessOwner, af.tenA, `{"expected_revision":0,"items":[
		{"catalog_code":"A08","label":"大众点评","target_ref":"https://www.dianping.com/shop/9","order":1,"enabled":true},
		{"catalog_code":"A13","label":"抖音关注","target_ref":"https://www.douyin.com/user/painuo","order":2,"enabled":true},
		{"catalog_code":"A14","label":"快手关注","target_ref":"https://www.kuaishou.com/user/x","order":3,"enabled":false}
	]}`)
	mustEqual(t, status, http.StatusOK)
	// 门店服务：Wi-Fi + 企微 + QQ 群（审批中）+ 停用个人微信。
	status, _, body := af.f.do(t, "PUT", "/api/v1/stores/"+af.stoA+"/services", af.sessOwner, af.tenA, `{"expected_revision":0,"items":[
		{"kind":"wifi","catalog_code":"A17","label":"门店 Wi-Fi","order":1,"enabled":true,
		 "config":{"ssid":"Painuo-Guest","password":"passw0rd123","encryption":"wpa2"}},
		{"kind":"contact","variant":"wecom","catalog_code":"A18","label":"加企业微信","order":2,"enabled":true,
		 "config":{"url":"https://work.weixin.qq.com/ca/abc?source=nfc-card","source_note":"台卡来源"}},
		{"kind":"community","variant":"qq_group","catalog_code":"A29","label":"车主 QQ 群","order":3,"enabled":true,
		 "config":{"group_no":"123456789","invite_url":"https://qm.qq.com/j/abc","approval_note":"群主审核中"}},
		{"kind":"contact","variant":"personal","label":"加店长微信","order":4,"enabled":false,
		 "config":{"wechat_id":"painuo_store"}}
	]}`)
	mustEqual(t, status, http.StatusOK)
	rev, _ := body["config_revision"].(float64)
	if rev != 1 {
		t.Fatalf("services config_revision = %v", body["config_revision"])
	}
	// 公共面要 store_code。
	_, _, cfgDoc := af.f.do(t, "GET", "/api/v1/stores/"+af.stoA+"/actions", af.sessOwner, af.tenA, "")
	return cfgDoc["store_code"].(string)
}

func TestStoreServicesMerchantCRUD(t *testing.T) {
	af := newActionsHTTPFixture(t, true)
	f := af.f

	// 越权：乙店经理写甲店服务 → 403；邻租户 owner 读甲店 → 404 掩码。
	status, _, _ := f.do(t, "PUT", "/api/v1/stores/"+af.stoA+"/services", af.sessMgrB, af.tenA, `{"expected_revision":0,"items":[]}`)
	mustEqual(t, status, http.StatusForbidden)
	status, _, _ = f.do(t, "GET", "/api/v1/stores/"+af.stoA+"/services", af.sessOwnerB, af.tenB, "")
	mustEqual(t, status, http.StatusNotFound)

	// 正常保存（见 seed）；stale revision 409 零写入。
	seedServicesStorefront(t, af)
	status, _, _ = f.do(t, "PUT", "/api/v1/stores/"+af.stoA+"/services", af.sessOwner, af.tenA,
		`{"expected_revision":0,"items":[{"kind":"wifi","enabled":true,"config":{"ssid":"X","password":"passw0rd123","encryption":"wpa2"}}]}`)
	mustEqual(t, status, http.StatusConflict)

	// 非官方企微域 400；事件语义外 kind 400。
	status, _, bad := f.do(t, "PUT", "/api/v1/stores/"+af.stoA+"/services", af.sessOwner, af.tenA,
		`{"expected_revision":1,"items":[{"kind":"contact","variant":"wecom","enabled":true,"config":{"url":"https://evil.example.com/ca"}}]}`)
	mustEqual(t, status, http.StatusBadRequest)
	if bad["error"] != "bad_service_config" {
		t.Fatalf("bad config error = %v", bad["error"])
	}
	status, _, _ = f.do(t, "PUT", "/api/v1/stores/"+af.stoA+"/services", af.sessOwner, af.tenA,
		`{"expected_revision":1,"items":[{"kind":"followed","enabled":true,"config":{}}]}`)
	mustEqual(t, status, http.StatusBadRequest)

	// 幂等重放：同内容同 revision → 修订不推高（必须重发全集，replace 语义）。
	_, _, doc := f.do(t, "GET", "/api/v1/stores/"+af.stoA+"/services", af.sessOwner, af.tenA, "")
	if doc["config_revision"] != float64(1) {
		t.Fatalf("config_revision = %v", doc["config_revision"])
	}
	items, _ := doc["items"].([]any)
	if len(items) != 4 {
		t.Fatalf("items = %d", len(items))
	}
	rawItems, _ := json.Marshal(items)
	replayBody := `{"expected_revision":1,"items":` + string(rawItems) + `}`
	status, _, rdoc := f.do(t, "PUT", "/api/v1/stores/"+af.stoA+"/services", af.sessOwner, af.tenA, replayBody)
	mustEqual(t, status, http.StatusOK)
	if rdoc["config_revision"] != float64(1) {
		t.Fatalf("idempotent replay bumped revision: %v", rdoc["config_revision"])
	}

	// 店隔离：乙店无服务。
	_, _, docB := f.do(t, "GET", "/api/v1/stores/"+af.stoB+"/services", af.sessOwner, af.tenA, "")
	if itemsB, _ := docB["items"].([]any); len(itemsB) != 0 {
		t.Fatalf("store B services = %v", itemsB)
	}

	// 回调资质诚实态：无凭据 → pending_qualification（绝不显示已配置）。
	_, _, doc2 := f.do(t, "GET", "/api/v1/stores/"+af.stoA+"/services", af.sessOwner, af.tenA, "")
	cb, _ := doc2["wecom_callback"].(map[string]any)
	if cb["state"] != "pending_qualification" {
		t.Fatalf("wecom callback state = %v (凭据缺失必须诚实待资质)", cb)
	}
}

func TestPublicStoreServicesContract(t *testing.T) {
	af := newActionsHTTPFixture(t, true)
	f := af.f
	code := seedServicesStorefront(t, af)

	status, _, doc := f.do(t, "GET", "/api/v1/public/storefronts/"+code+"/services", "", "", "")
	mustEqual(t, status, http.StatusOK)
	if doc["schema_version"] != "store-services.v1" {
		t.Fatalf("schema_version = %v", doc["schema_version"])
	}
	svcs, _ := doc["services"].([]any)
	// 2 actions（点评/关注）+ 3 服务行（停用个人微信不出现）。
	if len(svcs) != 5 {
		t.Fatalf("services = %d, want 5: %v", len(svcs), doc["services"])
	}
	raw := jsonString(doc)
	if strings.Contains(raw, af.tenA) || strings.Contains(raw, "tenant") || strings.Contains(raw, "target_ref") {
		t.Fatalf("public services leaked internal fields: %s", raw)
	}
	// 点评零模板零AIGC双拦（后端侧）：payload 无任何内容字段。
	for _, banned := range []string{"template", "copywriting", "draft", "suggest_text", "aigc"} {
		if strings.Contains(strings.ToLower(raw), banned) {
			t.Fatalf("review payload must never carry content field %q: %s", banned, raw)
		}
	}
	var review, follow, wifi, wecom, group map[string]any
	for _, s := range svcs {
		m, _ := s.(map[string]any)
		switch m["kind"] {
		case "review":
			review = m
		case "follow":
			follow = m
		case "wifi":
			wifi = m
		case "contact":
			wecom = m
		case "group":
			group = m
		}
	}
	// 点评：受控解析的 target_url + 3535 能力（navigation only，reward=unsupported）+ 指引。
	if review["target_url"] != "https://www.dianping.com/shop/9" {
		t.Fatalf("review target_url = %v", review["target_url"])
	}
	if review["catalog_code"] != "A08" {
		t.Fatalf("review catalog = %v", review["catalog_code"])
	}
	routes, _ := review["routes"].([]any)
	if len(routes) == 0 {
		t.Fatalf("review routes missing (3535 能力消费): %v", review)
	}
	r0, _ := routes[0].(map[string]any)
	if r0["method"] != "direct_open" || r0["reward_eligibility"] != "unsupported" {
		t.Fatalf("review route = %v (点评 reward 必须 unsupported)", r0)
	}
	if review["admission"] != "promised" {
		t.Fatalf("review admission = %v (A08 direct_open 种子=admitted/navigation supported)", review["admission"])
	}
	if _, ok := review["guidance"]; !ok {
		t.Fatalf("review guidance missing: %v", review)
	}
	// 关注：主页链接 + 无成功话术字段。
	if follow["target_url"] == "" || follow["admission"] == "followed" {
		t.Fatalf("follow item = %v", follow)
	}
	// Wi-Fi：密码明文仅展示（无留资门），带手动回退指引。
	wcfg, _ := wifi["wifi"].(map[string]any)
	if wcfg["ssid"] != "Painuo-Guest" || wcfg["password"] != "passw0rd123" || wcfg["encryption"] != "wpa2" {
		t.Fatalf("wifi block = %v", wifi["wifi"])
	}
	if _, ok := wifi["guidance"]; !ok {
		t.Fatalf("wifi manual fallback guidance missing: %v", wifi)
	}
	// 企微：官方链接 + 来源说明透传。
	if wecom["wecom_url"] != "https://work.weixin.qq.com/ca/abc?source=nfc-card" {
		t.Fatalf("wecom url = %v", wecom["wecom_url"])
	}
	// QQ 群：审批备注原样透传（审批中如实显示）。
	if group["approval_note"] != "群主审核中" || group["group_no"] != "123456789" {
		t.Fatalf("group item = %v", group)
	}

	// action_id 过滤：单面板。
	status, _, one := f.do(t, "GET", "/api/v1/public/storefronts/"+code+"/services?action_id="+review["service_id"].(string), "", "", "")
	mustEqual(t, status, http.StatusOK)
	ones, _ := one["services"].([]any)
	if len(ones) != 1 {
		t.Fatalf("filtered services = %d", len(ones))
	}
	// 非法环境 400。
	status, _, _ = f.do(t, "GET", "/api/v1/public/storefronts/"+code+"/services?environment=ipad", "", "", "")
	mustEqual(t, status, http.StatusBadRequest)
	// 未知码 404。
	status, _, _ = f.do(t, "GET", "/api/v1/public/storefronts/sf_missing/services", "", "", "")
	mustEqual(t, status, http.StatusNotFound)

	// 开关 off：路由不存在。
	off := newActionsHTTPFixture(t, false)
	status, _, _ = off.f.do(t, "GET", "/api/v1/public/storefronts/"+code+"/services", "", "", "")
	if status != http.StatusNotFound {
		t.Fatalf("flag-off services = %d", status)
	}
}

func TestPublicStoreServiceEventsEvidenceOnly(t *testing.T) {
	af := newActionsHTTPFixture(t, true)
	f := af.f
	code := seedServicesStorefront(t, af)
	_, _, doc := f.do(t, "GET", "/api/v1/public/storefronts/"+code+"/services", "", "", "")
	svcs, _ := doc["services"].([]any)
	var reviewID, wifiID string
	for _, s := range svcs {
		m, _ := s.(map[string]any)
		if m["kind"] == "review" {
			reviewID = m["service_id"].(string)
		}
		if m["kind"] == "wifi" {
			wifiID = m["service_id"].(string)
		}
	}

	// 点评 open：记录为点击证据；回执明示不代表成功。
	status, _, ev := f.do(t, "POST", "/api/v1/public/storefronts/"+code+"/services/events", "", "",
		`{"service_id":"`+reviewID+`","event":"open","environment":"ios_safari"}`)
	mustEqual(t, status, http.StatusOK)
	if ev["recorded"] != true {
		t.Fatalf("event doc = %v", ev)
	}
	if ev["evidence_tier"] != "click" {
		t.Fatalf("evidence_tier = %v", ev["evidence_tier"])
	}
	note := jsonString(ev)
	for _, must := range []string{"不代表已关注", "不代表已连接", "不代表已添加", "不代表已点评"} {
		if !strings.Contains(note, must) {
			t.Fatalf("event note missing negation %q: %s", must, note)
		}
	}

	// Wi-Fi copy：也是点击证据（主动复制）。
	status, _, _ = f.do(t, "POST", "/api/v1/public/storefronts/"+code+"/services/events", "", "",
		`{"service_id":"`+wifiID+`","event":"copy"}`)
	mustEqual(t, status, http.StatusOK)
	// jump_attempt 层。
	status, _, ev2 := f.do(t, "POST", "/api/v1/public/storefronts/"+code+"/services/events", "", "",
		`{"service_id":"`+reviewID+`","event":"jump_attempt"}`)
	mustEqual(t, status, http.StatusOK)
	if ev2["evidence_tier"] != "jump_attempt" {
		t.Fatalf("jump tier = %v", ev2["evidence_tier"])
	}
	// 成功语义枚举 400；未知服务 404；事件条数 3。
	status, _, _ = f.do(t, "POST", "/api/v1/public/storefronts/"+code+"/services/events", "", "",
		`{"service_id":"`+reviewID+`","event":"followed"}`)
	mustEqual(t, status, http.StatusBadRequest)
	status, _, _ = f.do(t, "POST", "/api/v1/public/storefronts/"+code+"/services/events", "", "",
		`{"service_id":"sv_ghost0000000000000ff","event":"open"}`)
	mustEqual(t, status, http.StatusNotFound)
	n, err := f.s.St.CountServiceEvents(af.tenA, af.stoA, reviewID)
	if err != nil || n != 2 {
		t.Fatalf("review events = %d err %v", n, err)
	}
}
