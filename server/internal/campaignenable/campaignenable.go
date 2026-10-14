// Package campaignenable is the HUI-1676 增量(商家配置与真实启用) leaf domain:
// the publish-reward configuration a merchant fills once, the server-enforced
// pre-enable check, and the disclosure snapshot that pins what customers were
// told.
//
// 纪律(设计文档 docs/superpowers/specs/2026-10-14-hui-1676-design.md):
//   - 纯函数叶子包:零 SQL、零 HTTP。事实集由调用方(HTTP 层)从既有真值组装
//     (contentpack 3553 / channeladmission 3535 / rewardops 库存读取 1671),
//     本包不 import 任何仓内包,不复制任何状态;
//   - 三个核验模式与 rewardops 的三个 Evidence 类逐字对齐,配置时写死顾客
//     文案,不在用户完成后临时加严;
//   - Check 固定顺序,第一个阻塞原因胜出;只拒绝,不罚款;原因码 + field 名
//     一起返回,web 把错误落到具体字段;
//   - 权益只存引用(benefit_slot)与已告知快照:不建库存、不写参与/奖励表
//     (G2/G3 权威);生产启用遇未知资格/库存仍拒绝。
package campaignenable

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Verification modes: the three evidence classes, verbatim to rewardops.
const (
	ModeStaffObserved = "staff_observed_submission"  // 现场见到提交
	ModeStaffVerified = "staff_verified_post"        // 现场见到作品
	ModeOfficial      = "official_publish_confirmed" // 官方确认
)

// Benefit kinds (门店券/合法导入券码/现场赠品/现场折扣). No cash, no lottery, no POS.
const (
	KindStoreCoupon    = "store_coupon"
	KindImportedCode   = "imported_code"
	KindOnsiteGift     = "onsite_gift"
	KindOnsiteDiscount = "onsite_discount"
)

// Budget accounting modes: exactly one of quantity / cost budget.
const (
	BudgetTotalQuantity = "total_quantity"
	BudgetCostMinor     = "cost_budget"
)

// Dedup judgment bases. Cookie/IP is deliberately not an option: 匿名访问只能
// 做到有限去重,「同一人一次」必须声明判定基础.
const (
	BasisReceipt         = "verifiable_receipt"          // 可验证消费凭据(按订单限领挂这里)
	BasisPlatformSubject = "platform_authorized_subject" // 平台授权主体
	BasisOnsiteManual    = "onsite_manual"               // 现场人工判定
)

// Staff verification scope (核验人员权限) and out-of-stock handling.
const (
	ScopeStoreStaff = "store_staff"
	ScopeOrgOwner   = "org_owner"

	StockStopNew = "stop_new"      // 缺货即停新参与
	StockMarkOut = "mark_sold_out" // 显著标记售罄并保留页面
)

// Machine reason codes returned by Check (fixed refusal order below).
const (
	ReasonCopyMissing            = "customer_copy_missing"
	ReasonContentNotReady        = "content_not_ready"
	ReasonContentBlocked         = "content_blocked"
	ReasonContentGrantExpired    = "content_grant_expired"
	ReasonChannelClosed          = "channel_closed"
	ReasonChannelNotAdmitted     = "channel_not_admitted"
	ReasonNoRunnableChannel      = "no_runnable_channel"
	ReasonBenefitRefMissing      = "benefit_ref_missing"
	ReasonBenefitVersionMismatch = "benefit_version_mismatch"
	ReasonBenefitStockMissing    = "benefit_stock_missing"
	ReasonWindowConflict         = "window_conflict"
	ReasonCampaignEnded          = "campaign_ended"
	ReasonStoreMismatch          = "store_mismatch"
)

// ChannelRef is one selected publish route. The triple must come from the
// channel-admission truth (3535): a bare platform string is not a shape this
// package can even represent.
type ChannelRef struct {
	Platform  string `json:"platform"`
	EntryMode string `json:"entry_mode"`
	Scenario  string `json:"scenario"`
}

func (c ChannelRef) empty() bool {
	return strings.TrimSpace(c.Platform) == "" || strings.TrimSpace(c.EntryMode) == "" || strings.TrimSpace(c.Scenario) == ""
}

// Reward carries the 口径 only: references and budgets, never stock.
type Reward struct {
	Kind            BenefitKind `json:"kind"`
	Slot            string      `json:"slot"`
	BudgetMode      string      `json:"budget_mode"`
	TotalQuantity   *int        `json:"total_quantity,omitempty"`
	CostBudgetMinor *int        `json:"cost_budget_minor,omitempty"`
	Currency        string      `json:"currency"`
	StoreIDs        []string    `json:"store_ids"`
	ClaimFrom       string      `json:"claim_from"`
	ClaimUntil      string      `json:"claim_until"`
	RedeemFrom      string      `json:"redeem_from"`
	RedeemUntil     string      `json:"redeem_until"`
	OnsiteRedempt   bool        `json:"onsite_redemption"`
	StaffScope      string      `json:"staff_scope"`
	OutOfStockRule  string      `json:"out_of_stock_rule"`
}

// BenefitKind is a distinct named type so unknown kinds cannot slip in as ints.
type BenefitKind = string

// Spec is the full merchant configuration for the publish-reward mode.
type Spec struct {
	VerificationMode string       `json:"verification_mode"`
	CustomerCopy     string       `json:"customer_copy"`
	Reward           Reward       `json:"reward"`
	DedupBasis       string       `json:"dedup_basis"`
	PerOrderLimit    bool         `json:"per_order_limit"`
	Channels         []ChannelRef `json:"channels"`
	ContentVerID     string       `json:"content_version_id"`
}

// ChannelVerdict is the caller-assembled Gate outcome for one selected route.
type ChannelVerdict struct {
	Ref     ChannelRef
	Allowed bool
	Reason  string
}

// Facts is the world state the HTTP layer assembles from existing truths.
// Check consumes it; the package never reads a database.
type Facts struct {
	Now time.Time
	// ContentAdopted: ContentVerID is a current adoption of this campaign (3553).
	ContentAdopted bool
	// ContentDistributable: contentpack.Distributable passed (授权未过期且未 blocked).
	ContentDistributable bool
	// ContentBlocked: the version is blocked (撤权/重大问题) — reported distinctly.
	ContentBlocked bool
	// ChannelVerdicts: per selected route, the channeladmission.Gate verdict.
	ChannelVerdicts []ChannelVerdict
	// BenefitRefPresent: a reward batch/benefit reference exists for Reward.Slot.
	BenefitRefPresent bool
	// BenefitVersionOK: that reference is bound to the rule version being enabled.
	BenefitVersionOK bool
	// BenefitStockOK: the referenced batch has available quantity / budget left.
	BenefitStockOK bool
	// WindowOK: claim/redeem windows lie inside the campaign period and are ordered.
	WindowOK bool
	// CampaignEnded: the campaign's own period is already over.
	CampaignEnded bool
	// StoreScopeOK: every configured store is inside the tenant/campaign scope.
	StoreScopeOK bool
	// RuleVersion is the config version being enabled (goes into the snapshot).
	RuleVersion int
}

// Decision is the enablement verdict: allow, or exactly one machine reason plus
// the config field the web should highlight.
type Decision struct {
	Allowed bool
	Reason  string
	Field   string
}

func refuse(reason, field string) Decision { return Decision{Reason: reason, Field: field} }

// ValidateSpec enforces the shape matrix (drafts must be savable, so this is
// deliberately weaker than Check: shape only, no world facts).
func ValidateSpec(s Spec) error {
	switch s.VerificationMode {
	case ModeStaffObserved, ModeStaffVerified, ModeOfficial:
	default:
		return fmt.Errorf("campaignenable: verification_mode must be one of the three evidence classes, got %q", s.VerificationMode)
	}
	switch s.Reward.Kind {
	case KindStoreCoupon, KindImportedCode, KindOnsiteGift, KindOnsiteDiscount:
	default:
		return fmt.Errorf("campaignenable: benefit kind %q is not a supported reward kind", s.Reward.Kind)
	}
	// 权益引用(benefit_slot)的「有没有」归启用检查;这里不拦空草稿。
	switch s.Reward.BudgetMode {
	case BudgetTotalQuantity:
		if s.Reward.TotalQuantity == nil {
			return fmt.Errorf("campaignenable: total_quantity budget mode requires a quantity")
		}
	case BudgetCostMinor:
		if s.Reward.CostBudgetMinor == nil {
			return fmt.Errorf("campaignenable: cost_budget budget mode requires a budget amount")
		}
	default:
		return fmt.Errorf("campaignenable: budget_mode must be total_quantity or cost_budget, got %q", s.Reward.BudgetMode)
	}
	if s.Reward.TotalQuantity != nil && *s.Reward.TotalQuantity < 0 {
		return fmt.Errorf("campaignenable: total_quantity must be >= 0")
	}
	if s.Reward.CostBudgetMinor != nil && *s.Reward.CostBudgetMinor < 0 {
		return fmt.Errorf("campaignenable: cost_budget_minor must be >= 0")
	}
	switch s.DedupBasis {
	case BasisReceipt, BasisPlatformSubject, BasisOnsiteManual:
	default:
		return fmt.Errorf("campaignenable: dedup_basis must name a judgment basis, got %q", s.DedupBasis)
	}
	if s.PerOrderLimit && s.DedupBasis != BasisReceipt {
		return fmt.Errorf("campaignenable: per_order_limit requires the verifiable_receipt judgment basis")
	}
	// 草稿允许不完整:渠道/内容/权益引用/门店的「有没有」归启用检查(Check),
	// 这里只管「给了的值形状对不对」。
	for _, c := range s.Channels {
		if c.empty() {
			return fmt.Errorf("campaignenable: every channel needs platform + entry_mode + scenario (a bare platform string is not an admission)")
		}
	}
	switch s.Reward.StaffScope {
	case ScopeStoreStaff, ScopeOrgOwner:
	default:
		return fmt.Errorf("campaignenable: staff_scope must be store_staff or org_owner, got %q", s.Reward.StaffScope)
	}
	switch s.Reward.OutOfStockRule {
	case StockStopNew, StockMarkOut:
	default:
		return fmt.Errorf("campaignenable: out_of_stock_rule must be stop_new or mark_sold_out, got %q", s.Reward.OutOfStockRule)
	}
	for _, w := range [4]string{s.Reward.ClaimFrom, s.Reward.ClaimUntil, s.Reward.RedeemFrom, s.Reward.RedeemUntil} {
		if w != "" {
			if _, err := time.Parse(time.RFC3339, w); err != nil {
				return fmt.Errorf("campaignenable: window value %q is not RFC3339", w)
			}
		}
	}
	return nil
}

// Check is the server-enforced pre-enable gate. Fixed refusal order:
//
//	copy/verification -> content(not_ready -> blocked -> grant_expired)
//	-> channel(closed -> not_admitted/unknown -> none runnable)
//	-> benefit(ref -> version -> stock) -> window -> campaign end -> store.
//
// 启用是唯一的放行点:预览不经过这里也绝不消耗真实奖励。
func Check(s Spec, f Facts) Decision {
	if strings.TrimSpace(s.CustomerCopy) == "" {
		return refuse(ReasonCopyMissing, "customer_copy")
	}
	if strings.TrimSpace(s.ContentVerID) == "" || !f.ContentAdopted {
		return refuse(ReasonContentNotReady, "content_version_id")
	}
	if f.ContentBlocked {
		return refuse(ReasonContentBlocked, "content_version_id")
	}
	if !f.ContentDistributable {
		return refuse(ReasonContentGrantExpired, "content_version_id")
	}
	if len(s.Channels) == 0 || len(f.ChannelVerdicts) == 0 {
		return refuse(ReasonNoRunnableChannel, "channels")
	}
	anyAllowed := false
	for _, v := range f.ChannelVerdicts {
		if v.Allowed {
			anyAllowed = true
			break
		}
	}
	if !anyAllowed {
		// 第一个阻塞渠道的既有 Gate 原因原样上抛(closed 优先于未准入语义由
		// channeladmission.Gate 的固定顺序保证,这里不重排)。
		for _, v := range f.ChannelVerdicts {
			switch v.Reason {
			case "channel_closed":
				return refuse(ReasonChannelClosed, "channels")
			case "not_admitted", "admission_unknown", "admission_expired", "wrong_binding", "reward_play_not_authorized":
				return refuse(ReasonChannelNotAdmitted, "channels")
			}
		}
		return refuse(ReasonChannelNotAdmitted, "channels")
	}
	if strings.TrimSpace(s.Reward.Slot) == "" || !f.BenefitRefPresent {
		return refuse(ReasonBenefitRefMissing, "benefit_slot")
	}
	if !f.BenefitVersionOK {
		return refuse(ReasonBenefitVersionMismatch, "benefit_slot")
	}
	if !f.BenefitStockOK {
		return refuse(ReasonBenefitStockMissing, "reward_budget")
	}
	if f.CampaignEnded {
		return refuse(ReasonCampaignEnded, "campaign_window")
	}
	if !f.WindowOK {
		return refuse(ReasonWindowConflict, "claim_window")
	}
	if len(s.Reward.StoreIDs) == 0 || !f.StoreScopeOK {
		return refuse(ReasonStoreMismatch, "store_ids")
	}
	return Decision{Allowed: true}
}

// Disclosure is the 告知快照: what customers were told for this rule version.
// It is frozen at enable time; in-flight participations keep the snapshot they
// started under (改版不追溯,重发不发生).
type Disclosure struct {
	RuleVersion    int    `json:"rule_version"`
	Mode           string `json:"verification_mode"`
	CustomerCopy   string `json:"customer_copy"`
	DedupStatement string `json:"dedup_statement"`
	BenefitSummary string `json:"benefit_summary"`
	ClaimFrom      string `json:"claim_from"`
	ClaimUntil     string `json:"claim_until"`
	RedeemFrom     string `json:"redeem_from"`
	RedeemUntil    string `json:"redeem_until"`
	GeneratedAt    string `json:"generated_at"`
}

// BuildDisclosure freezes the customer-facing terms. The dedup statement is
// plain merchant/customer language naming the judgment basis — never cookie/IP.
func BuildDisclosure(s Spec, ruleVersion int, now time.Time) Disclosure {
	return Disclosure{
		RuleVersion:    ruleVersion,
		Mode:           s.VerificationMode,
		CustomerCopy:   s.CustomerCopy,
		DedupStatement: dedupStatement(s),
		BenefitSummary: benefitSummary(s),
		ClaimFrom:      s.Reward.ClaimFrom,
		ClaimUntil:     s.Reward.ClaimUntil,
		RedeemFrom:     s.Reward.RedeemFrom,
		RedeemUntil:    s.Reward.RedeemUntil,
		GeneratedAt:    now.UTC().Format(time.RFC3339),
	}
}

func dedupStatement(s Spec) string {
	switch s.DedupBasis {
	case BasisReceipt:
		if s.PerOrderLimit {
			return "同一消费凭据(订单)只领一次;领取时核对订单,填一个订单号不算数。"
		}
		return "以可验证的消费凭据判定同一人;凭据核对通过才算同一人已领。"
	case BasisPlatformSubject:
		return "以平台授权主体判定同一人,同一账号只领一次。"
	case BasisOnsiteManual:
		return "以现场工作人员的核对判定同一人,同一人当天只领一次。"
	default:
		return "以现场工作人员的核对判定同一人。"
	}
}

func benefitSummary(s Spec) string {
	kind := map[string]string{
		KindStoreCoupon:    "门店优惠券",
		KindImportedCode:   "券码",
		KindOnsiteGift:     "现场赠品",
		KindOnsiteDiscount: "现场折扣",
	}[s.Reward.Kind]
	scope := "限指定门店可用"
	if len(s.Reward.StoreIDs) == 1 {
		scope = "限单店可用"
	}
	var amount string
	if s.Reward.TotalQuantity != nil {
		amount = "名额 " + strconv.Itoa(*s.Reward.TotalQuantity)
	} else if s.Reward.CostBudgetMinor != nil {
		amount = "成本预算 " + strconv.Itoa(*s.Reward.CostBudgetMinor) + " 分"
	}
	onsite := ""
	if s.Reward.OnsiteRedempt {
		onsite = "现场兑现"
	}
	return strings.TrimSpace(strings.Join([]string{kind, scope, amount, onsite}, " · "))
}

// JSON serializes the disclosure for the frozen config column.
func (d Disclosure) JSON() string {
	b, err := json.Marshal(d)
	if err != nil {
		// 结构化字段不可能失败;兜底仍是诚实快照(原样字段拼接)。
		return fmt.Sprintf(`{"rule_version":%d,"mode":%q,"customer_copy":%q,"generated_at":%q}`,
			d.RuleVersion, d.Mode, d.CustomerCopy, d.GeneratedAt)
	}
	return string(b)
}

// ParseDisclosure reads a frozen snapshot back (draft recovery / staff views).
func ParseDisclosure(raw string) (Disclosure, error) {
	var d Disclosure
	if strings.TrimSpace(raw) == "" {
		return d, fmt.Errorf("campaignenable: empty disclosure snapshot")
	}
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return Disclosure{}, fmt.Errorf("campaignenable: disclosure snapshot unreadable: %w", err)
	}
	return d, nil
}

// SnapshotJSON is the G3 sync contract: the rewardops.Snapshot shape for the
// version being enabled. G3 批次绑定快照时按此记录,版本不齐在兑付侧被
// rewardops.Adjudicate 拒绝(规则版本错配),与本包的启用前检查互为两道闸。
func SnapshotJSON(campaignID string, ruleVersion int, mode string, slot string) string {
	payload := map[string]any{
		"campaign_id":      campaignID,
		"version":          ruleVersion,
		"allowed_evidence": []string{mode},
		"slots":            []string{slot},
	}
	b, _ := json.Marshal(payload)
	return string(b)
}

// FingerprintSpec gives a stable content hash for revision comparison (the
// store layer uses it to skip no-op saves rather than mint empty versions).
func FingerprintSpec(s Spec) string {
	b, _ := json.Marshal(s)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
