// Package actioncatalog is the fixed 30-entry action catalog (HUI-4584 G1).
//
// The catalog is a code table, not a management object: A01-A30 are compile
// constants. A store action instance references one catalog code; the same
// store may hold multiple instances of the same catalog_code+platform+variant
// (five review entries, several follow entries coexist and never overwrite
// each other). navigate/return stay on the legacy extra-jump path and are
// deliberately NOT catalog entries.
//
// LegacyMapping maps the old (campaign_id,kind) extra-jump rows onto the
// catalog for the one-time data migration. Deterministic kinds map directly;
// platform-ambiguous kinds infer the platform from the href host; an
// un-inferable row maps to nothing (ok=false) so the merchant adopts it
// explicitly in M4 — the migration never guesses a platform.
package actioncatalog

import (
	"net/url"
	"strings"
)

// Kind is the action category. It is a classification dimension and is never
// a uniqueness key (the old UNIQUE(campaign_id,kind) is gone).
const (
	KindPublish   = "publish"    // 发布（视频/图文）
	KindReview    = "review"     // 点评
	KindFollow    = "follow"     // 关注
	KindWifi      = "wifi"       // 门店 Wi-Fi
	KindContact   = "contact"    // 加微信（企微是其 variant 方法）
	KindGroupBuy  = "groupbuy"   // 团购
	KindShare     = "share"      // 分享（QQ 好友/群是接收对象）
	KindJoinGroup = "join_group" // 加QQ群
)

// Entry is one catalog row.
type Entry struct {
	Code     string // "A01".."A30", stable forever
	Label    string // 语义名（产品文案）
	Platform string // "" = 非平台项（Wi-Fi）
	Kind     string
	Variant  string // 同平台同类别下的方法变体（企微、公众号、视频号、图文/视频）
}

var catalog = []Entry{
	{Code: "A01", Label: "抖音视频", Platform: "douyin", Kind: KindPublish, Variant: "video"},
	{Code: "A02", Label: "小红书视频", Platform: "xiaohongshu", Kind: KindPublish, Variant: "video"},
	{Code: "A03", Label: "快手视频", Platform: "kuaishou", Kind: KindPublish, Variant: "video"},
	{Code: "A04", Label: "朋友圈视频", Platform: "wechat", Kind: KindPublish, Variant: "video"},
	{Code: "A05", Label: "朋友圈图文", Platform: "wechat", Kind: KindPublish, Variant: "imagetext"},
	{Code: "A06", Label: "视频号发布", Platform: "wechat", Kind: KindPublish, Variant: "video"},
	{Code: "A07", Label: "小红书图文", Platform: "xiaohongshu", Kind: KindPublish, Variant: "imagetext"},
	{Code: "A08", Label: "大众点评", Platform: "dianping", Kind: KindReview},
	{Code: "A09", Label: "抖音点评", Platform: "douyin", Kind: KindReview},
	{Code: "A10", Label: "高德点评", Platform: "amap", Kind: KindReview},
	{Code: "A11", Label: "百度点评", Platform: "baidu", Kind: KindReview},
	{Code: "A12", Label: "美团点评", Platform: "meituan", Kind: KindReview},
	{Code: "A13", Label: "抖音关注", Platform: "douyin", Kind: KindFollow},
	{Code: "A14", Label: "快手关注", Platform: "kuaishou", Kind: KindFollow},
	{Code: "A15", Label: "小红书关注", Platform: "xiaohongshu", Kind: KindFollow},
	{Code: "A16", Label: "公众号关注", Platform: "wechat", Kind: KindFollow, Variant: "official_account"},
	{Code: "A17", Label: "门店 Wi-Fi", Platform: "", Kind: KindWifi},
	{Code: "A18", Label: "加微信", Platform: "wechat", Kind: KindContact},
	{Code: "A19", Label: "美团团购", Platform: "meituan", Kind: KindGroupBuy},
	{Code: "A20", Label: "大众点评团购", Platform: "dianping", Kind: KindGroupBuy},
	{Code: "A21", Label: "抖音团购", Platform: "douyin", Kind: KindGroupBuy},
	// A22-A30 为同版建议补充（票面：不是 30 项真实平台全通过的承诺）。
	{Code: "A22", Label: "B站视频", Platform: "bilibili", Kind: KindPublish, Variant: "video"},
	{Code: "A23", Label: "B站关注", Platform: "bilibili", Kind: KindFollow},
	{Code: "A24", Label: "微博图文", Platform: "weibo", Kind: KindPublish, Variant: "imagetext"},
	{Code: "A25", Label: "微博视频", Platform: "weibo", Kind: KindPublish, Variant: "video"},
	{Code: "A26", Label: "微博关注", Platform: "weibo", Kind: KindFollow},
	{Code: "A27", Label: "QQ好友/群分享", Platform: "qq", Kind: KindShare},
	{Code: "A28", Label: "QQ空间图文", Platform: "qzone", Kind: KindPublish, Variant: "imagetext"},
	{Code: "A29", Label: "加QQ群", Platform: "qq", Kind: KindJoinGroup},
	{Code: "A30", Label: "视频号关注", Platform: "wechat", Kind: KindFollow, Variant: "channels"},
}

// A04 与 A06 都挂在微信生态：朋友圈 vs 视频号用 variant 区分展示面。
// A06 是视频号发布、A30 是视频号关注，variant=channels 与票面编号一一对应。

func init() {
	// Compile-time sanity: the table is the authority, keep it ordered and complete.
	if len(catalog) != 30 {
		panic("actioncatalog: catalog must hold exactly 30 entries")
	}
	for i, entry := range catalog {
		if want := codeOf(i + 1); entry.Code != want {
			panic("actioncatalog: catalog code out of order at index " + itoa(i))
		}
	}
}

func codeOf(n int) string {
	return "A" + string(rune('0'+n/10)) + string(rune('0'+n%10))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

// All returns the 30 catalog entries in code order. Callers must not mutate.
func All() []Entry {
	out := make([]Entry, len(catalog))
	copy(out, catalog)
	return out
}

// Lookup resolves one catalog code exactly (case-sensitive).
func Lookup(code string) (Entry, bool) {
	for _, entry := range catalog {
		if entry.Code == code {
			return entry, true
		}
	}
	return Entry{}, false
}

// ValidCode reports whether code is a known catalog code.
func ValidCode(code string) bool {
	_, ok := Lookup(code)
	return ok
}

// Kind-aware host inference for the one-time migration: the old generic
// "review"/"follow" rows get a platform only when the href host names one
// unambiguously. No match -> ok=false (catalog_code=” in storage; the
// merchant picks the entry explicitly in M4 — the migration never guesses).
var legacyReviewHosts = []hostRule{
	{"dianping.com", "A08"},
	{"douyin.com", "A09"},
	{"amap.com", "A10"},
	{"baidu.com", "A11"},
	{"meituan.com", "A12"},
}

var legacyFollowHosts = []hostRule{
	{"douyin.com", "A13"},
	{"kuaishou.com", "A14"},
	{"xiaohongshu.com", "A15"},
	{"weixin.qq.com", "A16"},
}

type hostRule struct {
	fragment string
	code     string
}

func catalogByHost(rules []hostRule, href string) (Entry, bool) {
	parsed, err := url.Parse(strings.TrimSpace(href))
	host := ""
	if err == nil {
		host = strings.ToLower(parsed.Hostname())
	}
	if host == "" {
		return Entry{}, false
	}
	for _, rule := range rules {
		if strings.Contains(host, rule.fragment) {
			return Lookup(rule.code)
		}
	}
	return Entry{}, false
}

// LegacyMapping maps one old extra-jump row onto the catalog.
//
//   - wifi -> A17, wecom -> A18 (variant=wecom): deterministic.
//   - review -> A08-A12 by href host; follow -> A13-A16 by href host.
//   - navigate/return stay on the legacy path: never mapped, never migrated.
//
// ok=false means "cannot map without guessing" — the caller keeps the row on
// the legacy compatibility surface and lets the merchant adopt it explicitly.
func LegacyMapping(kind, href string) (Entry, bool) {
	switch kind {
	case "wifi":
		return Lookup("A17")
	case "wecom":
		entry, ok := Lookup("A18")
		if ok {
			entry.Variant = "wecom"
		}
		return entry, ok
	case "review":
		return catalogByHost(legacyReviewHosts, href)
	case "follow":
		return catalogByHost(legacyFollowHosts, href)
	default:
		return Entry{}, false
	}
}
