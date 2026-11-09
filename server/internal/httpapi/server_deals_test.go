package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/deals"
)

// HUI-4590 HTTP 级测试：商家面套餐 CRUD（Read/Update 作用域 + 404 掩码）、
// 顾客公共读面（白名单字段 + FEATURE_STOREFRONT 双保险 + 价格核对态）、
// 点击 beacon（点击≠成交）、外部券 check/redeem 诚实矩阵
// （未接通/未授权/未知→零事实；redeemed 终态回读；本地奖励账零写入）。

func createDealOffer(t *testing.T, af *actionsFixture, session, storeID string, body string) (int, map[string]any) {
	t.Helper()
	status, _, doc := af.f.do(t, "POST", "/api/v1/stores/"+storeID+"/deal-offers", session, af.tenA, body)
	return status, doc
}

func dealBody() string {
	return `{"platform":"meituan","provider_offer_id":"mt-1001","product_url":"https://www.meituan.com/deal/1001",
	"title":"双人套餐","price_minor":8800,"currency":"FEN","price_source":"platform"}`
}

func TestStoreDealOffersCRUD(t *testing.T) {
	af := newActionsHTTPFixture(t, true)

	status, doc := createDealOffer(t, af, af.sessOwner, af.stoA, dealBody())
	mustEqual(t, status, http.StatusOK)
	offerID, _ := doc["id"].(string)
	if offerID == "" || doc["platform"] != "meituan" || doc["status"] != "active" {
		t.Fatalf("created = %v", doc)
	}
	if _, ok := doc["authorization_ref"]; ok {
		t.Fatalf("empty authorization_ref must be omitted: %v", doc)
	}

	// 校验负例：未知平台/跨平台 URL/http/内网/负价/空标题。
	for name, body := range map[string]string{
		"bad platform":     `{"platform":"eleme","title":"x","product_url":"https://www.meituan.com/d","price_minor":1}`,
		"cross platform":   `{"platform":"meituan","title":"x","product_url":"https://www.douyin.com/d","price_minor":1}`,
		"http url":         `{"platform":"meituan","title":"x","product_url":"http://www.meituan.com/d","price_minor":1}`,
		"loopback url":     `{"platform":"meituan","title":"x","product_url":"https://127.0.0.1/d","price_minor":1}`,
		"negative price":   `{"platform":"meituan","title":"x","product_url":"https://www.meituan.com/d","price_minor":-1}`,
		"empty title":      `{"platform":"meituan","title":"","product_url":"https://www.meituan.com/d","price_minor":1}`,
		"missing url":      `{"platform":"meituan","title":"x","price_minor":1}`,
		"bad valid window": `{"platform":"meituan","title":"x","product_url":"https://www.meituan.com/d","price_minor":1,"valid_until":"not-a-time"}`,
	} {
		status, _ = createDealOffer(t, af, af.sessOwner, af.stoA, body)
		if status != http.StatusUnprocessableEntity {
			t.Fatalf("%s must 422, got %d", name, status)
		}
	}

	// 同平台商品 ID 唯一 → 409。
	status, _ = createDealOffer(t, af, af.sessOwner, af.stoA, dealBody())
	mustEqual(t, status, http.StatusConflict)

	// 列表：字段齐全 + 点击归因结构 + 诚实注记（点击不是成交）。
	status, _, doc = af.f.do(t, "GET", "/api/v1/stores/"+af.stoA+"/deal-offers", af.sessOwner, af.tenA, "")
	mustEqual(t, status, http.StatusOK)
	items, _ := doc["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("list = %v", doc)
	}
	item, _ := items[0].(map[string]any)
	for _, key := range []string{"id", "platform", "provider_offer_id", "product_url", "title", "price_minor",
		"price_verified_at", "price_state", "status", "store_ids", "click_total"} {
		if _, ok := item[key]; !ok {
			t.Fatalf("item missing %q: %v", key, item)
		}
	}
	if item["price_state"] != deals.PricePlatform {
		t.Fatalf("unverified price_state = %v", item["price_state"])
	}
	if _, ok := doc["by_platform"].(map[string]any); !ok {
		t.Fatalf("by_platform missing: %v", doc)
	}
	if raw := jsonString(doc); !strings.Contains(raw, "点击不是成交") {
		t.Fatalf("deals note missing: %s", raw)
	}

	// 编辑 + 标记价格已核对（校验时间=服务端时钟）。
	status, _, doc = af.f.do(t, "PATCH", "/api/v1/stores/"+af.stoA+"/deal-offers/"+offerID, af.sessOwner, af.tenA,
		`{"title":"双人套餐（升级）","price_minor":9900,"mark_price_verified":true}`)
	mustEqual(t, status, http.StatusOK)
	if doc["title"] != "双人套餐（升级）" || doc["price_minor"] != float64(9900) {
		t.Fatalf("patched = %v", doc)
	}
	if v, _ := doc["price_verified_at"].(string); v == "" {
		t.Fatalf("mark_price_verified must set the server verification time: %v", doc)
	}
	if v, _ := doc["price_state"].(string); v != deals.PriceShow {
		t.Fatalf("fresh verification must show the price: %v", doc)
	}

	// 上下架：paused → active → retired；retired 不可逆。
	status, _, _ = af.f.do(t, "POST", "/api/v1/stores/"+af.stoA+"/deal-offers/"+offerID+"/status", af.sessOwner, af.tenA, `{"status":"paused"}`)
	mustEqual(t, status, http.StatusOK)
	status, _, doc = af.f.do(t, "POST", "/api/v1/stores/"+af.stoA+"/deal-offers/"+offerID+"/status", af.sessOwner, af.tenA, `{"status":"retired"}`)
	mustEqual(t, status, http.StatusOK)
	if doc["status"] != "retired" {
		t.Fatalf("retired = %v", doc)
	}
	status, _, _ = af.f.do(t, "POST", "/api/v1/stores/"+af.stoA+"/deal-offers/"+offerID+"/status", af.sessOwner, af.tenA, `{"status":"active"}`)
	mustEqual(t, status, http.StatusUnprocessableEntity)
	// 未知状态 / 未知行。
	status, _, _ = af.f.do(t, "POST", "/api/v1/stores/"+af.stoA+"/deal-offers/"+offerID+"/status", af.sessOwner, af.tenA, `{"status":"hidden"}`)
	mustEqual(t, status, http.StatusUnprocessableEntity)
	status, _, _ = af.f.do(t, "POST", "/api/v1/stores/"+af.stoA+"/deal-offers/sdoff_missing/status", af.sessOwner, af.tenA, `{"status":"paused"}`)
	mustEqual(t, status, http.StatusNotFound)
}

func TestStoreDealOffersScopes(t *testing.T) {
	af := newActionsHTTPFixture(t, true)

	// 无会话 401。
	status, _, _ := af.f.do(t, "GET", "/api/v1/stores/"+af.stoA+"/deal-offers", "", af.tenA, "")
	mustEqual(t, status, http.StatusUnauthorized)

	// 他租户 owner：404 掩码（读/写/券）。
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/v1/stores/" + af.stoA + "/deal-offers"},
		{"POST", "/api/v1/stores/" + af.stoA + "/deal-offers"},
		{"POST", "/api/v1/stores/" + af.stoA + "/deal-coupons/redeem"},
	} {
		status, _, _ = af.f.do(t, tc.method, tc.path, af.sessOwnerB, af.tenB, `{"platform":"meituan","title":"x","product_url":"https://www.meituan.com/d","price_minor":1,"code":"C"}`)
		if status != http.StatusNotFound {
			t.Fatalf("%s cross-tenant must 404, got %d", tc.method, status)
		}
	}

	// 店经理本店可管理；他店 404 掩码。
	status, _ = createDealOffer(t, af, af.sessMgrA, af.stoA, dealBody())
	mustEqual(t, status, http.StatusOK)
	status, _, _ = af.f.do(t, "GET", "/api/v1/stores/"+af.stoA+"/deal-offers", af.sessMgrA, af.tenA, "")
	mustEqual(t, status, http.StatusOK)
	status, _, _ = af.f.do(t, "GET", "/api/v1/stores/"+af.stoB+"/deal-offers", af.sessMgrA, af.tenA, "")
	mustEqual(t, status, http.StatusNotFound)

	// 店经理不得把适用面挂到别的门店（作用域事实在服务端）。
	status, _, doc := af.f.do(t, "POST", "/api/v1/stores/"+af.stoA+"/deal-offers", af.sessMgrA, af.tenA,
		`{"platform":"meituan","provider_offer_id":"mt-scope","product_url":"https://www.meituan.com/s","title":"x","price_minor":1,"store_ids":["`+af.stoB+`"]}`)
	if status != http.StatusForbidden {
		t.Fatalf("manager cross-store applicability must 403, got %d: %v", status, doc)
	}
	// 总部 owner 可以。
	status, _, _ = af.f.do(t, "POST", "/api/v1/stores/"+af.stoA+"/deal-offers", af.sessOwner, af.tenA,
		`{"platform":"meituan","provider_offer_id":"mt-scope2","product_url":"https://www.meituan.com/s2","title":"x","price_minor":1,"store_ids":["`+af.stoB+`"]}`)
	mustEqual(t, status, http.StatusOK)
}

func TestPublicStoreOffersEndpoint(t *testing.T) {
	af := newActionsHTTPFixture(t, true)

	// 未核实价 + 已核实价 + 停用 三条。
	status, _, doc := af.f.do(t, "POST", "/api/v1/stores/"+af.stoA+"/deal-offers", af.sessOwner, af.tenA, dealBody())
	mustEqual(t, status, http.StatusOK)
	verifiedID, _ := doc["id"].(string)
	status, _, _ = af.f.do(t, "PATCH", "/api/v1/stores/"+af.stoA+"/deal-offers/"+verifiedID, af.sessOwner, af.tenA, `{"mark_price_verified":true}`)
	mustEqual(t, status, http.StatusOK)
	status, _, _ = af.f.do(t, "POST", "/api/v1/stores/"+af.stoA+"/deal-offers", af.sessOwner, af.tenA,
		`{"platform":"dianping","provider_offer_id":"dp-1","product_url":"https://www.dianping.com/p","title":"点评套餐","price_minor":6600}`)
	mustEqual(t, status, http.StatusOK)
	status, _, pausedDoc := af.f.do(t, "POST", "/api/v1/stores/"+af.stoA+"/deal-offers", af.sessOwner, af.tenA,
		`{"platform":"douyin","provider_offer_id":"dy-1","product_url":"https://www.douyin.com/p","title":"抖音套餐","price_minor":5500}`)
	mustEqual(t, status, http.StatusOK)
	pausedID, _ := pausedDoc["id"].(string)
	status, _, _ = af.f.do(t, "POST", "/api/v1/stores/"+af.stoA+"/deal-offers/"+pausedID+"/status", af.sessOwner, af.tenA, `{"status":"paused"}`)
	mustEqual(t, status, http.StatusOK)

	status, _, cfgDoc := af.f.do(t, "GET", "/api/v1/stores/"+af.stoA+"/actions", af.sessOwner, af.tenA, "")
	storeCode, _ := cfgDoc["store_code"].(string)

	// 顾客读：无会话；分渠道全在（美团/点评分渠道保留）；停用行不出现；
	// 价格只对已核对的行下发。
	status, _, doc = af.f.do(t, "GET", "/api/v1/public/storefronts/"+storeCode+"/offers", "", "", "")
	mustEqual(t, status, http.StatusOK)
	offers, _ := doc["offers"].([]any)
	if len(offers) != 2 {
		t.Fatalf("public offers = %v", doc)
	}
	// 白名单结构断言：顶层与每行 offer 只允许冻结字段（无租户/授权/审计键）。
	for key := range doc {
		switch key {
		case "schema_version", "store_code", "store", "price_policy", "deals_note", "offers":
		default:
			t.Fatalf("unexpected top-level key %q in public payload", key)
		}
	}
	for _, o := range offers {
		m, _ := o.(map[string]any)
		for key := range m {
			switch key {
			case "offer_id", "platform", "title", "image_ref", "product_url", "price_display", "price_minor", "currency", "valid_until":
			default:
				t.Fatalf("offer leaks unexpected key %q: %v", key, m)
			}
		}
	}
	raw := jsonString(doc)
	if strings.Contains(raw, "authorization") || strings.Contains(raw, `"note":`) || strings.Contains(raw, "tenant_id") {
		t.Fatalf("public payload leaks internals: %s", raw)
	}
	// 未核实那条（点评）不携带价格字段，price_display=platform。
	var dianping map[string]any
	for _, o := range offers {
		m, _ := o.(map[string]any)
		if m["platform"] == "dianping" {
			dianping = m
		}
	}
	if dianping == nil || dianping["price_display"] != deals.PricePlatform {
		t.Fatalf("dianping offer = %v", dianping)
	}
	if _, ok := dianping["price_minor"]; ok {
		t.Fatalf("unverified price must not be served: %v", dianping)
	}
	if _, ok := dianping["product_url"]; !ok {
		t.Fatalf("verified product jump missing: %v", dianping)
	}
	// 已核实那条价格可见。
	for _, o := range offers {
		m, _ := o.(map[string]any)
		if m["platform"] == "meituan" {
			if m["price_display"] != deals.PriceShow || m["price_minor"] != float64(8800) {
				t.Fatalf("meituan offer = %v", m)
			}
		}
	}
	// 点击计数不出现在公共面（点击≠成交，也不暴露商家统计）。
	if strings.Contains(raw, "click") {
		t.Fatalf("public payload must not carry click stats: %s", raw)
	}

	// 停用门店 → offers 空 + state disabled。
	status, _, _ = af.f.do(t, "POST", "/api/v1/stores/"+af.stoA+"/status", af.sessOwner, af.tenA, `{"status":"disabled"}`)
	mustEqual(t, status, http.StatusOK)
	status, _, doc = af.f.do(t, "GET", "/api/v1/public/storefronts/"+storeCode+"/offers", "", "", "")
	mustEqual(t, status, http.StatusOK)
	if offers, _ = doc["offers"].([]any); len(offers) != 0 {
		t.Fatalf("disabled store must serve no offers: %v", doc)
	}
	if doc["store"].(map[string]any)["state"] != "disabled" {
		t.Fatalf("disabled store state = %v", doc["store"])
	}

	// 未知门店码 → 404。
	status, _, _ = af.f.do(t, "GET", "/api/v1/public/storefronts/sf_missing/offers", "", "", "")
	mustEqual(t, status, http.StatusNotFound)

	// 悬置租户 → 404（fail-closed 同公共门店面）。
	if _, err := af.f.s.St.DB.Exec(`UPDATE tenants SET lifecycle='suspended' WHERE id=?`, af.tenA); err != nil {
		t.Fatal(err)
	}
	status, _, _ = af.f.do(t, "GET", "/api/v1/public/storefronts/"+storeCode+"/offers", "", "", "")
	mustEqual(t, status, http.StatusNotFound)
}

func TestPublicStoreOffersFlagOff(t *testing.T) {
	af := newActionsHTTPFixture(t, false)
	status, _, _ := af.f.do(t, "GET", "/api/v1/public/storefronts/sf_x/offers", "", "", "")
	mustEqual(t, status, http.StatusNotFound)
	status, _, _ = af.f.do(t, "POST", "/api/v1/public/storefronts/sf_x/offers/o1/clicks", "", "", "")
	mustEqual(t, status, http.StatusNotFound)
}

func TestPublicOfferClickBeacon(t *testing.T) {
	af := newActionsHTTPFixture(t, true)
	status, _, doc := af.f.do(t, "POST", "/api/v1/stores/"+af.stoA+"/deal-offers", af.sessOwner, af.tenA, dealBody())
	mustEqual(t, status, http.StatusOK)
	offerID, _ := doc["id"].(string)

	status, _, cfgDoc := af.f.do(t, "GET", "/api/v1/stores/"+af.stoA+"/actions", af.sessOwner, af.tenA, "")
	storeCode, _ := cfgDoc["store_code"].(string)

	// 点击两次 = 计数 2；点击只是点击（无任何成交语义响应）。
	for i := 0; i < 2; i++ {
		status, _, _ = af.f.do(t, "POST", "/api/v1/public/storefronts/"+storeCode+"/offers/"+offerID+"/clicks", "", "", "")
		mustEqual(t, status, http.StatusNoContent)
	}
	perOffer, byPlatform, err := af.f.s.St.StoreOfferClickTotals(af.tenA, af.stoA)
	mustNoErr(t, err)
	if perOffer[offerID] != 2 || byPlatform["meituan"] != 2 {
		t.Fatalf("clicks = %v / %v", perOffer, byPlatform)
	}

	// 未知套餐 / 非适用门店 → 404 掩码，零计数。
	status, _, _ = af.f.do(t, "POST", "/api/v1/public/storefronts/"+storeCode+"/offers/sdoff_missing/clicks", "", "", "")
	mustEqual(t, status, http.StatusNotFound)

	// 带禁字段的 body → 400，不计点击。
	status, _, _ = af.f.do(t, "POST", "/api/v1/public/storefronts/"+storeCode+"/offers/"+offerID+"/clicks", "", "", `{"tenant_id":"x"}`)
	mustEqual(t, status, http.StatusBadRequest)
	perOffer, _, _ = af.f.s.St.StoreOfferClickTotals(af.tenA, af.stoA)
	if perOffer[offerID] != 2 {
		t.Fatalf("rejected beacon must not count: %v", perOffer)
	}
}

func TestDealCouponsHonestNotConnected(t *testing.T) {
	af := newActionsHTTPFixture(t, true)

	// 未授权套餐：check/redeem 都答 authorization_required，零事实。
	status, _, doc := af.f.do(t, "POST", "/api/v1/stores/"+af.stoA+"/deal-offers", af.sessOwner, af.tenA, dealBody())
	mustEqual(t, status, http.StatusOK)
	unauthID, _ := doc["id"].(string)

	status, _, doc = af.f.do(t, "POST", "/api/v1/stores/"+af.stoA+"/deal-coupons/check", af.sessOwner, af.tenA,
		`{"offer_id":"`+unauthID+`","code":"MT-1"}`)
	mustEqual(t, status, http.StatusUnprocessableEntity)
	if doc["error"] != "authorization_required" {
		t.Fatalf("check error = %v", doc)
	}
	status, _, doc = af.f.do(t, "POST", "/api/v1/stores/"+af.stoA+"/deal-coupons/redeem", af.sessOwner, af.tenA,
		`{"offer_id":"`+unauthID+`","code":"MT-1"}`)
	mustEqual(t, status, http.StatusUnprocessableEntity)
	if doc["error"] != "authorization_required" {
		t.Fatalf("redeem error = %v", doc)
	}

	// 已授权但本部署未接适配器（生产默认）：诚实 503 not_connected，零事实。
	status, _, _ = af.f.do(t, "PATCH", "/api/v1/stores/"+af.stoA+"/deal-offers/"+unauthID, af.sessOwner, af.tenA,
		`{"authorization_ref":"auth-mt-1"}`)
	mustEqual(t, status, http.StatusOK)
	status, _, doc = af.f.do(t, "POST", "/api/v1/stores/"+af.stoA+"/deal-coupons/check", af.sessOwner, af.tenA,
		`{"offer_id":"`+unauthID+`","code":"MT-1"}`)
	mustEqual(t, status, http.StatusServiceUnavailable)
	if doc["error"] != "not_connected" {
		t.Fatalf("check (no adapter) = %v", doc)
	}
	status, _, doc = af.f.do(t, "POST", "/api/v1/stores/"+af.stoA+"/deal-coupons/redeem", af.sessOwner, af.tenA,
		`{"offer_id":"`+unauthID+`","code":"MT-1"}`)
	mustEqual(t, status, http.StatusServiceUnavailable)
	if doc["error"] != "not_connected" {
		t.Fatalf("redeem (no adapter) = %v", doc)
	}

	// 缺 code / 缺 offer_id → 400；未知行 → 404。
	status, _, _ = af.f.do(t, "POST", "/api/v1/stores/"+af.stoA+"/deal-coupons/redeem", af.sessOwner, af.tenA, `{"offer_id":"`+unauthID+`"}`)
	mustEqual(t, status, http.StatusBadRequest)
	status, _, _ = af.f.do(t, "POST", "/api/v1/stores/"+af.stoA+"/deal-coupons/redeem", af.sessOwner, af.tenA, `{"offer_id":"sdoff_missing","code":"x"}`)
	mustEqual(t, status, http.StatusNotFound)

	var n int
	if err := af.f.s.St.DB.QueryRow(`SELECT COUNT(*) FROM store_deal_coupon_redemptions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("honest refusals must leave zero facts: %d", n)
	}
}

// countingAdapter is a test-only adapter double (fixture/mock 只进测试入口).
type countingAdapter struct {
	calls  int
	check  func() (deals.CheckResult, error)
	redeem func() (deals.RedeemResult, error)
}

func (c *countingAdapter) Check(deals.CheckRequest) (deals.CheckResult, error) { return c.check() }
func (c *countingAdapter) Redeem(deals.RedeemRequest) (deals.RedeemResult, error) {
	c.calls++
	return c.redeem()
}

func TestDealCouponsWithAdapter(t *testing.T) {
	af := newActionsHTTPFixture(t, true)
	status, _, doc := af.f.do(t, "POST", "/api/v1/stores/"+af.stoA+"/deal-offers", af.sessOwner, af.tenA, dealBody())
	mustEqual(t, status, http.StatusOK)
	offerID, _ := doc["id"].(string)
	status, _, _ = af.f.do(t, "PATCH", "/api/v1/stores/"+af.stoA+"/deal-offers/"+offerID, af.sessOwner, af.tenA,
		`{"authorization_ref":"auth-mt-1"}`)
	mustEqual(t, status, http.StatusOK)

	ad := &countingAdapter{
		check: func() (deals.CheckResult, error) { return deals.CheckResult{Valid: true, Detail: "可用"}, nil },
		redeem: func() (deals.RedeemResult, error) {
			return deals.RedeemResult{State: deals.RedeemRedeemed, Detail: "ok"}, nil
		},
	}
	af.f.s.DealCoupons = ad

	// check → 平台事实。
	status, _, doc = af.f.do(t, "POST", "/api/v1/stores/"+af.stoA+"/deal-coupons/check", af.sessOwner, af.tenA,
		`{"offer_id":"`+offerID+`","code":"MT-9"}`)
	mustEqual(t, status, http.StatusOK)
	if doc["valid"] != true || doc["detail"] != "可用" {
		t.Fatalf("check = %v", doc)
	}

	// redeem → redeemed 事实；重放 → already_redeemed，不再调适配器。
	body := `{"offer_id":"` + offerID + `","code":"MT-9"}`
	status, _, doc = af.f.do(t, "POST", "/api/v1/stores/"+af.stoA+"/deal-coupons/redeem", af.sessMgrA, af.tenA, body)
	mustEqual(t, status, http.StatusOK)
	if doc["outcome"] != "redeemed" {
		t.Fatalf("redeem = %v", doc)
	}
	if ad.calls != 1 {
		t.Fatalf("adapter calls = %d", ad.calls)
	}
	status, _, doc = af.f.do(t, "POST", "/api/v1/stores/"+af.stoA+"/deal-coupons/redeem", af.sessMgrA, af.tenA, body)
	mustEqual(t, status, http.StatusOK)
	if doc["outcome"] != "already_redeemed" || ad.calls != 1 {
		t.Fatalf("replay = %v calls=%d", doc, ad.calls)
	}

	// 平台 failed → 200 outcome=failed（真实平台答案，非本地伪造）。
	ad.redeem = func() (deals.RedeemResult, error) {
		return deals.RedeemResult{State: deals.RedeemFailed, Detail: "券已退款"}, nil
	}
	status, _, doc = af.f.do(t, "POST", "/api/v1/stores/"+af.stoA+"/deal-coupons/redeem", af.sessOwner, af.tenA,
		`{"offer_id":"`+offerID+`","code":"MT-10"}`)
	mustEqual(t, status, http.StatusOK)
	if doc["outcome"] != "failed" || doc["detail"] != "券已退款" {
		t.Fatalf("failed = %v", doc)
	}

	// 传输失败 → 503 result_unknown，零行。
	ad.redeem = func() (deals.RedeemResult, error) { return deals.RedeemResult{}, errors.New("timeout") }
	status, _, doc = af.f.do(t, "POST", "/api/v1/stores/"+af.stoA+"/deal-coupons/redeem", af.sessOwner, af.tenA,
		`{"offer_id":"`+offerID+`","code":"MT-11"}`)
	mustEqual(t, status, http.StatusServiceUnavailable)
	if doc["error"] != "result_unknown" {
		t.Fatalf("unknown = %v", doc)
	}

	// 全程本地奖励账零写入；核销事实仅外部券两行（MT-9/MT-10）。
	var rb, cr int
	if err := af.f.s.St.DB.QueryRow(`SELECT COUNT(*) FROM reward_benefits`).Scan(&rb); err != nil {
		t.Fatal(err)
	}
	if err := af.f.s.St.DB.QueryRow(`SELECT COUNT(*) FROM store_deal_coupon_redemptions`).Scan(&cr); err != nil {
		t.Fatal(err)
	}
	if rb != 0 || cr != 2 {
		t.Fatalf("ledger isolation broken: reward_benefits=%d redemptions=%d", rb, cr)
	}

	// 店员（本店经理）可 check（只读）；他店经理 404 掩码。
	status, _, _ = af.f.do(t, "POST", "/api/v1/stores/"+af.stoA+"/deal-coupons/check", af.sessMgrA, af.tenA,
		`{"offer_id":"`+offerID+`","code":"MT-9"}`)
	mustEqual(t, status, http.StatusOK)
	status, _, _ = af.f.do(t, "POST", "/api/v1/stores/"+af.stoA+"/deal-coupons/redeem", af.sessMgrB, af.tenA, body)
	mustEqual(t, status, http.StatusNotFound)

	// staff（租户级）可 check；redeem 走 Update 权限（同 T0 语义允许）。
	status, _, _ = af.f.do(t, "POST", "/api/v1/stores/"+af.stoA+"/deal-coupons/check", af.sessStaff, af.tenA,
		`{"offer_id":"`+offerID+`","code":"MT-9"}`)
	mustEqual(t, status, http.StatusOK)
}
