// Package channeladmission is the HUI-3535 渠道准入与承接清单: the versioned,
// deterministic record of which platform + entry_mode + scenario routes are
// admitted for the 碰一碰门店交付 flows, and which are not.
//
// 纪律(票面拍板,root 裁决):
//   - unknown 永不等价 allowed:技术可用 ≠ 玩法获准;同平台"技术可用但玩法
//     未获准"不能启用奖励活动;准入过期、错 app/tenant、未知规则同样拒绝;
//   - 种子真源是 docs/contracts/channel-admission/v1/seed.json(版本化入库);
//     本包 go:embed 一份 vendored 副本,测试逐字节锁定(HUI-2227 vendored
//     truth 同款),启动时装载进库,不用 migration INSERT(本仓迁移纯 DDL);
//   - 不调真实平台 API、不伪造官方结果:记录只陈述官方依据 URL + 核查日期,
//     平台审批残差如实挂账,不制造"已申请/已获准"状态;
//   - 服务端单点强制:Gate 是纯函数,HTTP 层零业务判断;前端隐藏按钮不算数。
//
// 机器原因码表(写入拒绝响应的 reason 字段,对齐 campaignrules 惯例):
//
//	not_admitted              官方规范明确排除该场景(如抖音 H5 商铺宣传/营销内容)
//	admission_unknown         能力/资格未确认,不默认允许也不默认没有
//	admission_expired         核查记录已过 valid_until,须重新核验
//	wrong_binding             app/tenant 绑定不匹配(多租户/白标不得误用他人 AppID)
//	channel_closed            渠道已关闭:停止新交接,既有参与保留状态与履约处理入口(执行归 HUI-1670)
//	reward_play_not_authorized 奖励玩法未获平台批复,不得启用奖励活动
//	missing_fields            可执行但缺字段提示(随记录 missing 清单输出)
package channeladmission

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed seed/v1/seed.json
var seedFS embed.FS

// seedEmbedPath is the vendored seed copy inside this package.
const seedEmbedPath = "seed/v1/seed.json"

// Admission statuses. Only these three exist; "allowed" is deliberately NOT a
// status — the closest thing is admitted, which requires an affirmative record.
const (
	StatusAdmitted    = "admitted"
	StatusNotAdmitted = "not_admitted"
	StatusUnknown     = "unknown"
)

// Entry modes (HUI-2251 思路): how the customer reaches the platform publish
// surface from Touch.
const (
	EntryH5      = "h5"
	EntryMiniapp = "miniapp"
	EntryNative  = "native"
	EntryManual  = "manual"
)

// Capability states: what has been VERIFIED end to end on real devices per the
// official sources. Not yet verified stays unknown — never guessed.
const (
	CapSupported   = "supported"
	CapUnsupported = "unsupported"
	CapUnknown     = "unknown"
)

// Capabilities is the per-field media/copy/receipt support matrix. Every cell
// is one of supported/unsupported/unknown; unknown means "unconfirmed", not
// "missing" and not "granted" (小红书回调即此例:不默认没有,也不默认允许).
type Capabilities struct {
	Media    string `json:"media"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	Topic    string `json:"topic"`
	POI      string `json:"poi"`
	Receipt  string `json:"receipt"`
	Callback string `json:"callback"`
}

// Field returns one capability cell by its snake_case name.
func (c Capabilities) Field(name string) string {
	switch name {
	case "media":
		return c.Media
	case "title":
		return c.Title
	case "body":
		return c.Body
	case "topic":
		return c.Topic
	case "poi":
		return c.POI
	case "receipt":
		return c.Receipt
	case "callback":
		return c.Callback
	}
	return ""
}

// Record is one channel admission row: identity key + capability matrix +
// audit trail. JSON shape is the seed contract.
type Record struct {
	Platform  string `json:"platform"`
	EntryMode string `json:"entry_mode"`
	Scenario  string `json:"scenario"`
	// AppBinding distinguishes 自有平台应用 from 商家授权应用 (platform_app /
	// merchant_authorized / none). 多租户与白标不得共用错账号。
	AppBinding string `json:"app_binding"`
	// TenantScope is "*" for platform-level truth or a concrete tenant id.
	TenantScope string `json:"tenant_scope"`
	Status      string `json:"status"`
	// RewardAdmitted: 该具体奖励玩法是否已获平台批复。false unless an
	// affirmative approval exists; unknown/not_admitted routes can never be true.
	RewardAdmitted bool         `json:"reward_admitted"`
	ReasonCode     string       `json:"reason_code"`
	ReasonText     string       `json:"reason_text"`
	Capabilities   Capabilities `json:"capabilities"`
	// Missing lists what is absent before this route can be admitted (feeds
	// the application checklist and the merchant-facing 缺项原因 display).
	Missing []string `json:"missing"`
	// OfficialSources are the exact URLs consulted; LastCheckedAt is the date
	// they were actually read (票面: 登记实际读取日期). ValidUntil is the
	// re-check horizon after which Gate refuses (admission_expired).
	OfficialSources []string `json:"official_sources"`
	LastCheckedAt   string   `json:"last_checked_at"`
	ValidUntil      string   `json:"valid_until"`
	// CloseReason is non-empty only when the channel has been closed: new
	// handoffs stop, existing participations keep their state and their
	// fulfillment entry (语义文档化,执行归 HUI-1670).
	CloseReason string `json:"close_reason"`
}

// IdentityKey is the "platform|entry_mode|scenario" triple.
func (r Record) IdentityKey() string { return r.Platform + "|" + r.EntryMode + "|" + r.Scenario }

// UnmarshalJSON validates the enum fields on the way in so a seed typo can
// never introduce a fourth status or a bogus capability state.
func (r *Record) UnmarshalJSON(b []byte) error {
	type alias Record
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*r = Record(a)
	return r.Validate()
}

// Validate enforces the enum matrices.
func (r *Record) Validate() error {
	switch r.Status {
	case StatusAdmitted, StatusNotAdmitted, StatusUnknown:
	default:
		return fmt.Errorf("channeladmission: %s status %q must be admitted|not_admitted|unknown (allowed is not a status; unknown never equals allowed)", r.IdentityKey(), r.Status)
	}
	switch r.EntryMode {
	case EntryH5, EntryMiniapp, EntryNative, EntryManual:
	default:
		return fmt.Errorf("channeladmission: %s entry_mode %q must be h5|miniapp|native|manual", r.IdentityKey(), r.EntryMode)
	}
	if strings.TrimSpace(r.Platform) == "" || strings.TrimSpace(r.Scenario) == "" {
		return fmt.Errorf("channeladmission: platform and scenario are required")
	}
	for _, name := range []string{"media", "title", "body", "topic", "poi", "receipt", "callback"} {
		switch r.Capabilities.Field(name) {
		case CapSupported, CapUnsupported, CapUnknown:
		default:
			return fmt.Errorf("channeladmission: %s capability %s must be supported|unsupported|unknown", r.IdentityKey(), name)
		}
	}
	if r.RewardAdmitted && r.Status != StatusAdmitted {
		return fmt.Errorf("channeladmission: %s reward_admitted=true requires status=admitted (unknown never enables reward play)", r.IdentityKey())
	}
	return nil
}

// Seed is the versioned contract document.
type Seed struct {
	Version     int      `json:"version"`
	GeneratedAt string   `json:"generated_at"`
	Records     []Record `json:"records"`
}

// EmbeddedSeed parses the vendored seed copy compiled into this package.
func EmbeddedSeed() (*Seed, error) {
	raw, err := seedFS.ReadFile(seedEmbedPath)
	if err != nil {
		return nil, fmt.Errorf("channeladmission: read embedded seed: %w", err)
	}
	return ParseSeed(raw)
}

// ParseSeed decodes and validates a seed document.
func ParseSeed(raw []byte) (*Seed, error) {
	var s Seed
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("channeladmission: parse seed: %w", err)
	}
	if s.Version < 1 {
		return nil, fmt.Errorf("channeladmission: seed version must be >= 1")
	}
	if len(s.Records) == 0 {
		return nil, fmt.Errorf("channeladmission: seed carries no records")
	}
	seen := map[string]bool{}
	for _, r := range s.Records {
		k := r.IdentityKey() + "|" + r.TenantScope
		if seen[k] {
			return nil, fmt.Errorf("channeladmission: duplicate seed record %s", k)
		}
		seen[k] = true
	}
	return &s, nil
}
