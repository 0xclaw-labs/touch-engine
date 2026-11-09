package actioncatalog

import (
	"fmt"
	"testing"
)

// 会让这个测试失败的生产改动：目录缺项/重号、语义名缺失、旧 kind 映射猜测平台
// （映射不出必须显式返回 none，让商家明确采用，而不是默认塞一个）。

// TestCatalogHasThirtyDistinctCodes 锁死 A01-A30 连续编号、无重复、字段齐全。
func TestCatalogHasThirtyDistinctCodes(t *testing.T) {
	all := All()
	if len(all) != 30 {
		t.Fatalf("catalog size = %d, want 30", len(all))
	}
	seen := map[string]bool{}
	wantLabels := map[string]string{
		"A01": "抖音视频", "A02": "小红书视频", "A03": "快手视频",
		"A04": "朋友圈视频", "A05": "朋友圈图文", "A06": "视频号发布", "A07": "小红书图文",
		"A08": "大众点评", "A09": "抖音点评", "A10": "高德点评", "A11": "百度点评", "A12": "美团点评",
		"A13": "抖音关注", "A14": "快手关注", "A15": "小红书关注", "A16": "公众号关注",
		"A17": "门店 Wi-Fi", "A18": "加微信",
		"A19": "美团团购", "A20": "大众点评团购", "A21": "抖音团购",
		"A22": "B站视频", "A23": "B站关注",
		"A24": "微博图文", "A25": "微博视频", "A26": "微博关注",
		"A27": "QQ好友/群分享", "A28": "QQ空间图文", "A29": "加QQ群", "A30": "视频号关注",
	}
	for i, entry := range all {
		want := fmt.Sprintf("A%02d", i+1)
		if entry.Code != want {
			t.Fatalf("catalog[%d].code = %s, want %s (编号必须连续)", i, entry.Code, want)
		}
		if seen[entry.Code] {
			t.Fatalf("duplicate catalog code %s", entry.Code)
		}
		seen[entry.Code] = true
		if entry.Label == "" {
			t.Fatalf("%s label empty", entry.Code)
		}
		if wantLabels[entry.Code] != "" && entry.Label != wantLabels[entry.Code] {
			t.Fatalf("%s label = %q, want %q", entry.Code, entry.Label, wantLabels[entry.Code])
		}
		if !validKind(entry.Kind) {
			t.Fatalf("%s kind %q not in allowed set", entry.Code, entry.Kind)
		}
	}
}

// TestCatalogPlatformSemantics 锁死关键平台归类：五点评、四关注、 Wi-Fi 无外部平台。
func TestCatalogPlatformSemantics(t *testing.T) {
	cases := map[string]struct{ platform, kind, variant string }{
		"A08": {"dianping", KindReview, ""},
		"A09": {"douyin", KindReview, ""},
		"A10": {"amap", KindReview, ""},
		"A11": {"baidu", KindReview, ""},
		"A12": {"meituan", KindReview, ""},
		"A13": {"douyin", KindFollow, ""},
		"A14": {"kuaishou", KindFollow, ""},
		"A15": {"xiaohongshu", KindFollow, ""},
		"A16": {"wechat", KindFollow, "official_account"},
		"A17": {"", KindWifi, ""},
		"A18": {"wechat", KindContact, ""},
		"A19": {"meituan", KindGroupBuy, ""},
		"A20": {"dianping", KindGroupBuy, ""},
		"A21": {"douyin", KindGroupBuy, ""},
		"A01": {"douyin", KindPublish, "video"},
		"A05": {"wechat", KindPublish, "imagetext"},
		"A06": {"wechat", KindPublish, "video"},
		"A07": {"xiaohongshu", KindPublish, "imagetext"},
		"A30": {"wechat", KindFollow, "channels"},
	}
	for code, want := range cases {
		entry, ok := Lookup(code)
		if !ok {
			t.Fatalf("lookup %s missing", code)
		}
		if entry.Platform != want.platform || entry.Kind != want.kind || entry.Variant != want.variant {
			t.Fatalf("%s = (%s,%s,%s), want (%s,%s,%s)", code, entry.Platform, entry.Kind, entry.Variant,
				want.platform, want.kind, want.variant)
		}
	}
	// 企微是 A18 的一种方法（variant），不重复计数。
	a18, _ := Lookup("A18")
	a16, _ := Lookup("A16")
	if a18.Label == a16.Label {
		t.Fatal("A18 and A16 labels must stay distinct")
	}
}

func TestLookupAndValidCode(t *testing.T) {
	if _, ok := Lookup("A01"); !ok {
		t.Fatal("A01 must resolve")
	}
	if _, ok := Lookup("A31"); ok {
		t.Fatal("A31 must not resolve")
	}
	if _, ok := Lookup(""); ok {
		t.Fatal("empty must not resolve")
	}
	if _, ok := Lookup("a08"); ok {
		t.Fatal("lowercase must not resolve (codes are exact)")
	}
	if !ValidCode("A30") || ValidCode("A00") || ValidCode("") || ValidCode("B08") {
		t.Fatal("ValidCode boundary wrong")
	}
}

// TestLegacyMapping 旧 extrajump kind 的迁移映射：确定项直映，平台歧义项按
// href 域名推断，推不出显式 none（不猜选，商家在 M4 明确采用）。
func TestLegacyMapping(t *testing.T) {
	cases := []struct {
		kind, href, wantCode, wantPlatform, wantVariant string
		wantOK                                          bool
	}{
		{"wifi", "https://shop.example.com/wifi", "A17", "", "", true},
		{"wecom", "https://work.weixin.qq.com/ca/demo", "A18", "wechat", "wecom", true},
		{"review", "https://www.dianping.com/shop/1", "A08", "dianping", "", true},
		{"review", "https://www.douyin.com/roi/poi", "A09", "douyin", "", true},
		{"review", "https://www.amap.com/poi", "A10", "amap", "", true},
		{"review", "https://map.baidu.com/poi", "A11", "baidu", "", true},
		{"review", "https://www.meituan.com/poi", "A12", "meituan", "", true},
		{"review", "https://shop.example.com/review", "", "", "", false},
		{"follow", "https://www.douyin.com/user/x", "A13", "douyin", "", true},
		{"follow", "https://www.kuaishou.com/profile/x", "A14", "kuaishou", "", true},
		{"follow", "https://www.xiaohongshu.com/user/x", "A15", "xiaohongshu", "", true},
		{"follow", "https://mp.weixin.qq.com/mp/xx", "A16", "wechat", "official_account", true},
		{"follow", "https://shop.example.com/follow", "", "", "", false},
		// navigate/return 是兼容辅助能力，保留旧路径，不进 A01-A30。
		{"navigate", "https://uri.amap.com/marker", "", "", "", false},
		{"return", "/c/welcome", "", "", "", false},
		{"unknown", "https://www.douyin.com/x", "", "", "", false},
	}
	for _, tc := range cases {
		got, ok := LegacyMapping(tc.kind, tc.href)
		if ok != tc.wantOK {
			t.Fatalf("LegacyMapping(%s,%s) ok = %v, want %v", tc.kind, tc.href, ok, tc.wantOK)
		}
		if !tc.wantOK {
			continue
		}
		if got.Code != tc.wantCode || got.Platform != tc.wantPlatform || got.Variant != tc.wantVariant {
			t.Fatalf("LegacyMapping(%s,%s) = (%s,%s,%s), want (%s,%s,%s)", tc.kind, tc.href,
				got.Code, got.Platform, got.Variant, tc.wantCode, tc.wantPlatform, tc.wantVariant)
		}
	}
}

func validKind(k string) bool {
	switch k {
	case KindPublish, KindReview, KindFollow, KindWifi, KindContact, KindGroupBuy, KindShare, KindJoinGroup:
		return true
	}
	return false
}
