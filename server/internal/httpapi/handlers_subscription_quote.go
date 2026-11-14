package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/billing"
)

// Read-only presentation of the existing billing catalog and ExpansionQuote.
// No order, reservation, payer permission or payment is created by a preview.
func (s *Server) handleBillingSubscriptionQuote(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if d := authz.Can(s.resolveAccess(c), string(authz.PermBillingView), ""); !d.Allowed {
		fail(w, 403, d.Reason, "billing quote is not allowed for this member")
		return
	}
	var in billingSubscriptionOrderBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		fail(w, 400, "bad_request", "invalid quote request")
		return
	}
	action := strings.TrimSpace(in.Action)
	if action != billing.ActionNew && action != billing.ActionRenew && action != billing.ActionExpand {
		fail(w, 400, "bad_action", "invalid subscription action")
		return
	}
	entries, err := s.St.ListBillingCatalog(c.Member.TenantID)
	if err != nil {
		fail(w, 503, "catalog_unavailable", "price catalog is temporarily unavailable")
		return
	}
	entry, err := billing.ResolveCatalog(entries, c.Member.TenantID, strings.TrimSpace(in.PlanCode), nowRFC3339())
	if err != nil {
		fail(w, 409, "catalog_unavailable", "请联系平台运营发布生效价格后重试。")
		return
	}
	amount := entry.PriceMinor
	if action == billing.ActionExpand {
		amount, err = billing.ExpansionQuote(entry, in.AdditionalStores)
		if err != nil {
			fail(w, 400, "bad_expand", "扩店数量或阶梯价格无效，请核对后重试。")
			return
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"amount_minor": amount, "currency": entry.Currency, "price_ref": entry.PriceRef, "pricing_version": entry.PricingVersion, "service_days": entry.ServiceDays, "included_store_count": entry.IncludedStoreCount, "action": action, "plan_code": entry.PlanCode, "additional_stores": in.AdditionalStores})
}
