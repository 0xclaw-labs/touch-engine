// Package billing: HUI-4591 订购、收费与账单的领域核(纯函数,不触库不看 HTTP)。
//
// 纪律(裁定书,逐条硬性):
//   - 本仓只存可重建投影(subscription_ref/entitlement_projection/order_ref+
//     幂等键),不自增余额;公共订单权威=派诺生态统一账单,本包只做诚实投影
//     的判定逻辑。
//   - 价格目录配置不写死:一切金额/币种/服务期/门店档位来自 CatalogEntry
//     (经受信权威缝写入的目录行);本包源代码零价格常量。
//   - 订单引用价格快照:目录变更不改历史订单。
//
// 本文件:价格目录解析(租户专属>全局默认,生效窗口,最新修订)与阶梯加店价。
package billing

import (
	"encoding/json"
	"errors"
	"sort"
	"time"
)

// PlanCode 冻结值(裁定书:store.plan.month / store.plan.year)。
const (
	PlanMonthly = "store.plan.month"
	PlanYearly  = "store.plan.year"
)

var (
	// ErrCatalogNotFound:租户+套餐在生效窗口内没有任何目录行。
	ErrCatalogNotFound = errors.New("billing: catalog_not_found")
	// ErrCatalogNotEffective:行存在但 at 不在生效窗口内。
	ErrCatalogNotEffective = errors.New("billing: catalog_not_effective")
	// ErrTierMalformed:阶梯 JSON 无法解析(保守失败,绝不静默用错价)。
	ErrTierMalformed = errors.New("billing: tier_malformed")
)

// CatalogEntry is one price-catalog projection row. Zero values are legitimate
// data (a zero price is a fact, not a missing config): nothing here invents
// defaults.
type CatalogEntry struct {
	ID                   string `json:"id"`
	ScopeTenant          string `json:"scope_tenant"` // '' = 全局默认价
	PlanCode             string `json:"plan_code"`
	PricingVersion       string `json:"pricing_version"`
	PriceRef             string `json:"price_ref"`
	Currency             string `json:"currency"`
	PriceMinor           int64  `json:"price_minor"`
	ServiceDays          int    `json:"service_days"`
	IncludedStoreCount   int    `json:"included_store_count"`
	ExtraStorePriceMinor int64  `json:"extra_store_price_minor"`
	TierJSON             string `json:"tier_json"`
	GraceDays            int    `json:"grace_days"`
	EffectiveFrom        string `json:"effective_from"` // '' = 早已生效
	EffectiveTo          string `json:"effective_to"`   // '' = 不设截止
}

// TierStep is one ladder step: stores beyond the previous step and up to Upto
// (cumulative, counting EXTRA stores beyond the plan's included count) cost
// PriceMinor each.
type TierStep struct {
	Upto       int   `json:"upto"`
	PriceMinor int64 `json:"price_minor"`
}

// TierSteps parses the entry's ladder. Malformed JSON fails closed — the
// caller must never silently fall back to a wrong unit price.
func TierSteps(e CatalogEntry) ([]TierStep, error) {
	raw := e.TierJSON
	if raw == "" {
		raw = "[]"
	}
	var steps []TierStep
	if err := json.Unmarshal([]byte(raw), &steps); err != nil {
		return nil, ErrTierMalformed
	}
	for _, s := range steps {
		if s.Upto <= 0 || s.PriceMinor < 0 {
			return nil, ErrTierMalformed
		}
	}
	// 升序规范化(解析宽容,判定严格)
	sort.Slice(steps, func(i, j int) bool { return steps[i].Upto < steps[j].Upto })
	return steps, nil
}

func catalogEffective(e CatalogEntry, now time.Time) bool {
	if e.EffectiveFrom != "" {
		from, err := time.Parse(time.RFC3339, e.EffectiveFrom)
		if err != nil || now.Before(from) {
			return false
		}
	}
	if e.EffectiveTo != "" {
		to, err := time.Parse(time.RFC3339, e.EffectiveTo)
		if err != nil || now.After(to) {
			return false
		}
	}
	return true
}

// ResolveCatalog picks the effective catalog row for tenant+plan at now.
// Resolution order: tenant-scoped rows beat global defaults; among several
// effective rows the latest effective_from wins (price revisions take effect
// by their own window), ties broken by the higher pricing_version string so
// the choice is deterministic.
func ResolveCatalog(entries []CatalogEntry, tenantID, planCode, now string) (CatalogEntry, error) {
	nowT, err := time.Parse(time.RFC3339, now)
	if err != nil {
		return CatalogEntry{}, ErrCatalogNotEffective
	}
	best := -1
	for i, e := range entries {
		if e.PlanCode != planCode {
			continue
		}
		// 租户专属行只服务本租户;全局行服务所有人。
		if e.ScopeTenant != "" && e.ScopeTenant != tenantID {
			continue
		}
		// 租户有专属行时,全局行不参与竞争(专属>全局,不是混排)。
		if e.ScopeTenant == "" && hasTenantScopedFor(entries, tenantID, planCode, nowT) {
			continue
		}
		if !catalogEffective(e, nowT) {
			continue
		}
		if best == -1 || catalogNewer(e, entries[best]) {
			best = i
		}
	}
	if best == -1 {
		// 有行但全部不在生效窗口 = not_effective(与"根本没有这个套餐"区分)。
		for _, e := range entries {
			if e.PlanCode == planCode && (e.ScopeTenant == "" || e.ScopeTenant == tenantID) {
				return CatalogEntry{}, ErrCatalogNotEffective
			}
		}
		return CatalogEntry{}, ErrCatalogNotFound
	}
	return entries[best], nil
}

func hasTenantScopedFor(entries []CatalogEntry, tenantID, planCode string, nowT time.Time) bool {
	for _, e := range entries {
		if e.ScopeTenant == tenantID && e.PlanCode == planCode && catalogEffective(e, nowT) {
			return true
		}
	}
	return false
}

func catalogNewer(a, b CatalogEntry) bool {
	at := rfc3339OrZero(a.EffectiveFrom)
	bt := rfc3339OrZero(b.EffectiveFrom)
	if !at.Equal(bt) {
		return at.After(bt)
	}
	return a.PricingVersion > b.PricingVersion
}
