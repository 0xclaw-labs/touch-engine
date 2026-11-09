// renewal.go: 续期起算/扩店差价/降档生效日(HUI-4591 F17)。
//
// 规则(裁定书:按价格版本+事先明示规则;已承诺权益不追溯削减):
//   - 未到期续费从 paid_until 起算(剩余天数不丢);已到期或首购从付款确认
//     时刻起算。
//   - 扩店差价按价格快照的加店单价/阶梯;快照=下单时刻的目录行引用。
//   - 降档生效日=当前服务期届满(届满前不削减任何已承诺权益)。

package billing

import (
	"errors"
	"time"
)

var (
	// ErrRenewalBadInput:起算上下文缺关键事实(时间不可解析/服务期非正)。
	ErrRenewalBadInput = errors.New("billing: renewal_bad_input")
	// ErrExpansionBadInput:扩店数量必须为正。
	ErrExpansionBadInput = errors.New("billing: expansion_bad_input")
)

// RenewalContext carries the facts renewal start is judged from. PaidUntil is
// the tenant's current service-period end (” = 无有效服务期);PaymentConfirmedAt
// is the authoritative payment fact time (trusted-seam event, never client input).
type RenewalContext struct {
	Now                string
	PaidUntil          string
	PaymentConfirmedAt string
	ServiceDays        int
}

// RenewalStart returns the RFC3339 moment the new service period starts.
func RenewalStart(in RenewalContext) (string, error) {
	confirmed, err := time.Parse(time.RFC3339, in.PaymentConfirmedAt)
	if err != nil {
		return "", ErrRenewalBadInput
	}
	if in.PaidUntil == "" {
		return FormatRFC3339(confirmed), nil
	}
	until, err := time.Parse(time.RFC3339, in.PaidUntil)
	if err != nil {
		return "", ErrRenewalBadInput
	}
	if until.After(confirmed) {
		// 未到期续费:剩余天数不丢,从服务期届满起算。
		return FormatRFC3339(until), nil
	}
	// 已到期续费:从付款确认时刻起算。
	return FormatRFC3339(confirmed), nil
}

// ServicePeriodEnd adds the catalog's service days to a start moment. The
// period length is data (catalog), never a code constant.
func ServicePeriodEnd(startRFC3339 string, serviceDays int) (string, error) {
	start, err := time.Parse(time.RFC3339, startRFC3339)
	if err != nil || serviceDays <= 0 {
		return "", ErrRenewalBadInput
	}
	return FormatRFC3339(start.AddDate(0, 0, serviceDays)), nil
}

// ExpansionQuote prices AdditionalStores extra stores from the catalog
// snapshot: ladder steps first (cumulative caps on extra stores), falling back
// to the flat extra-store price beyond the ladder.
func ExpansionQuote(e CatalogEntry, additionalStores int) (int64, error) {
	if additionalStores <= 0 {
		return 0, ErrExpansionBadInput
	}
	steps, err := TierSteps(e)
	if err != nil {
		return 0, err
	}
	remaining := additionalStores
	var total int64
	prevCap := 0
	for _, s := range steps {
		if remaining <= 0 {
			break
		}
		band := s.Upto - prevCap
		if band <= 0 {
			continue
		}
		take := remaining
		if take > band {
			take = band
		}
		total += int64(take) * s.PriceMinor
		remaining -= take
		prevCap = s.Upto
	}
	if remaining > 0 {
		total += int64(remaining) * e.ExtraStorePriceMinor
	}
	return total, nil
}

// DowngradeEffectiveAt returns when a plan downgrade takes effect: the current
// service period's end. With no service period there is no committed benefit
// to cut, so it is effective immediately (now).
func DowngradeEffectiveAt(paidUntil, now string) (string, error) {
	if paidUntil == "" {
		nowT, err := time.Parse(time.RFC3339, now)
		if err != nil {
			return "", ErrRenewalBadInput
		}
		return FormatRFC3339(nowT), nil
	}
	until, err := time.Parse(time.RFC3339, paidUntil)
	if err != nil {
		return "", ErrRenewalBadInput
	}
	return FormatRFC3339(until), nil
}
