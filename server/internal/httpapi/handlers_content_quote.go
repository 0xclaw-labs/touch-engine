package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/billing"
	"github.com/bianjiefilm/touch-engine/server/internal/production"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

type productionOffer struct {
	Request production.QuoteRequest `json:"request"`
	Quote   production.Quote        `json:"quote"`
	Note    string                  `json:"note"`
}

func (s *Server) offerToken(offer productionOffer) string {
	raw, _ := json.Marshal(offer)
	body := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, []byte(s.Cfg.InternalToken))
	_, _ = mac.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (s *Server) readOffer(token string) (productionOffer, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return productionOffer{}, false
	}
	mac := hmac.New(sha256.New, []byte(s.Cfg.InternalToken))
	_, _ = mac.Write([]byte(parts[0]))
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(sig, mac.Sum(nil)) {
		return productionOffer{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	var offer productionOffer
	if err != nil || json.Unmarshal(raw, &offer) != nil {
		return productionOffer{}, false
	}
	return offer, true
}
func (s *Server) productionAccount(tenant string) string {
	var accounts map[string]string
	if json.Unmarshal([]byte(s.Cfg.ProductionBillingAccounts), &accounts) != nil {
		return ""
	}
	return strings.TrimSpace(accounts[tenant])
}
func productionQuoteFail(w http.ResponseWriter, err error) {
	var authority *production.AuthorityError
	switch {
	case errors.Is(err, production.ErrOfferExpired) || (errors.As(err, &authority) && authority.Code == "billing_source_quote_expired"):
		writeJSON(w, 409, map[string]any{"error": "quote_expired", "message": "报价已过期，请重新获取报价并确认；没有新建制作任务。", "can_requote": true})
	case errors.Is(err, production.ErrOfferChanged):
		fail(w, 409, "quote_changed", "报价或预占状态发生变化，请重新获取并确认；不会按未确认金额提交。")
	case errors.As(err, &authority) && authority.Status == 403:
		fail(w, 403, "payment_authorization_required", "公共账务未授权当前付款主体，请联系付款管理员。")
	case errors.As(err, &authority) && authority.Status == 402:
		fail(w, 402, "billing_insufficient_funds", "公共账务拒绝预占：可用额度不足。请到原账单中心处理后恢复同一请求。")
	case errors.As(err, &authority) && authority.Status == 409:
		fail(w, 409, "quote_conflict", "公共账务拒绝当前报价或幂等负载，请刷新回查同一请求，报价到期后重新获取并确认。")
	default:
		fail(w, 503, "quote_authority_unavailable", "没有取得可确认的公共账务报价。请稍后重试；不会估算价格或开始收费制作。")
	}
}

func (s *Server) handleStoreContentProductionQuote(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.storeContentPackManage(c, r.PathValue("id"), w) {
		return
	}
	if !s.callerHasPayerGrant(c) || !s.payerDecisionFor(c).Authorized {
		fail(w, 403, billing.PayerReasonRequired, "当前账号没有付款授权，可上传或选择已有内容。请付款管理员确认制作费用。")
		return
	}
	storeID := r.PathValue("id")
	tenant := c.Member.TenantID
	if _, err := s.St.GetStore(storeID, tenant); err != nil {
		packStoreFail(w, err)
		return
	}
	var in struct {
		ContentPackRef string `json:"content_pack_ref"`
		IdempotencyKey string `json:"idempotency_key"`
		Note           string `json:"note"`
	}
	ok, err := decodePackBody(r, &in)
	if err != nil || !ok || len(in.Note) > 8000 || strings.TrimSpace(in.Note) == "" {
		fail(w, 400, "bad_request", "请补充当前用途与制作内容。")
		return
	}
	packID, ok := resolveContentPackRef(in.ContentPackRef)
	if !ok {
		fail(w, 400, "bad_content_pack_ref", "内容准备未完成，请重试。")
		return
	}
	pack, err := s.St.GetStoreContentPack(packID, tenant)
	if err != nil || pack.StoreID != storeID {
		fail(w, 404, "not_found", "resource not found")
		return
	}
	requester, ok := s.ProductionQuotes.(production.QuoteRequester)
	account := s.productionAccount(tenant)
	if !ok || !requester.Ready() || account == "" || s.Cfg.ProductionPricingVersion == "" || s.Cfg.ProductionDeliverables == "" {
		fail(w, 503, "quote_configuration_missing", "公共账务的门店付款账户、制作价格版本或生产方交付合同尚未接通。请联系服务支持完成接线后重试；上传与选择已有内容仍可使用。")
		return
	}
	if len(in.IdempotencyKey) < 8 || len(in.IdempotencyKey) > 100 {
		fail(w, 400, "bad_request", "请求凭据无效，请重新打开内容编辑器。")
		return
	}
	digest := sha256.Sum256([]byte(strings.TrimSpace(in.Note)))
	keyHash := sha256.Sum256([]byte(tenant + ":" + storeID + ":" + in.IdempotencyKey))
	req := production.QuoteRequest{TenantID: tenant, StoreID: storeID, PackID: packID, PayerAccountID: account, PayerUserID: c.Principal.ID, PricingVersion: s.Cfg.ProductionPricingVersion, Deliverables: s.Cfg.ProductionDeliverables, IdempotencyKey: "touch-content-" + hex.EncodeToString(keyHash[:]), Brief: hex.EncodeToString(digest[:])}
	q, err := requester.RequestQuote(r.Context(), req)
	if err != nil {
		productionQuoteFail(w, err)
		return
	}
	offer := productionOffer{Request: req, Quote: q, Note: strings.TrimSpace(in.Note)}
	if err := s.St.SaveProductionOffer(tenant, q.Ref, s.offerToken(offer)); err != nil {
		productionQuoteFail(w, err)
		return
	}
	payerContext := "当前组织"
	if org, orgErr := s.St.GetTenant(tenant); orgErr == nil && strings.TrimSpace(org.Name) != "" {
		payerContext = org.Name
	}
	writeJSON(w, 200, map[string]any{"quote": map[string]any{"quote_ref": q.Ref, "amount_minor": q.AmountMinor, "currency": q.Currency, "expires_at": q.ExpiresAt, "payer": account, "payer_context": payerContext, "deliverables": q.Deliverables, "offer_token": s.offerToken(offer)}, "notice": "仅登记并报价，尚未预占或扣费；确认后按此金额预占，实际结算以公共账务事实为准。"})
}

// confirmProductionOffer accepts a server-signed offer only. The public Billing
// hold response, never browser claims, supplies the existing quote projection.
func (s *Server) confirmProductionOffer(w http.ResponseWriter, r *http.Request, in contentProductionHandoffBody, packID string) bool {
	c := callerFrom(r)
	if !s.callerHasPayerGrant(c) || !s.payerDecisionFor(c).Authorized {
		fail(w, 403, billing.PayerReasonRequired, "当前账号没有付款授权。")
		return false
	}
	offer, ok := s.readOffer(in.OfferToken)
	requestKeyHash := sha256.Sum256([]byte(c.Member.TenantID + ":" + r.PathValue("id") + ":" + strings.TrimSpace(in.IdempotencyKey)))
	if !ok {
		fail(w, 422, "quote_context_mismatch", "报价凭据无效，请重新获取并确认。")
		return false
	}
	if offer.Request.IdempotencyKey != "touch-content-"+hex.EncodeToString(requestKeyHash[:]) {
		fail(w, 409, "idempotency_conflict", "报价确认必须恢复同一请求凭据，不能另建收费请求。")
		return false
	}
	if !ok || offer.Request.TenantID != c.Member.TenantID || offer.Request.StoreID != r.PathValue("id") || offer.Request.PackID != packID || offer.Request.PayerUserID != c.Principal.ID || offer.Request.PayerAccountID != s.productionAccount(c.Member.TenantID) || offer.Note != strings.TrimSpace(in.Note) {
		fail(w, 422, "quote_context_mismatch", "报价与当前用途不一致，请重新获取并确认。")
		return false
	}
	if in.AcceptedAmountMinor == nil || *in.AcceptedAmountMinor != offer.Quote.AmountMinor || in.AcceptedCurrency != offer.Quote.Currency || in.AcceptedExpiresAt != offer.Quote.ExpiresAt || in.QuoteRef != offer.Quote.Ref {
		fail(w, 409, "quote_changed", "金额或有效期发生变化，请重新获取并确认。")
		return false
	}
	if !s.contentProducerConfigured() || !s.Cfg.FeatureTask || !s.ProductionTasks.Ready() {
		fail(w, 503, "production_unavailable", "制作生产方或公共任务尚未接通，未预占新费用。请恢复接线后重试同一请求。")
		return false
	}
	requester, ok := s.ProductionQuotes.(production.QuoteRequester)
	if !ok {
		productionQuoteFail(w, production.ErrUnavailable)
		return false
	}
	hold, err := requester.ConfirmOffer(r.Context(), offer.Request, offer.Quote)
	if err != nil {
		productionQuoteFail(w, err)
		return false
	}
	if err := s.St.SaveProductionHold(c.Member.TenantID, offer.Quote.Ref, hold.ID); err != nil {
		fail(w, 503, "quote_projection_pending", "公共账务已预占，恢复资料同步尚未完成。请恢复同一请求，不要重新付款。")
		return false
	}
	_, _, err = s.St.RecordBillingAIUsage(store.NewBillingAIUsage{TenantID: c.Member.TenantID, QuoteRef: offer.Quote.Ref, Capability: "content_production", Quantity: 1, AmountMinor: hold.AmountMinor, Currency: offer.Quote.Currency, PricingVersion: offer.Quote.PricingVersion, State: billing.QuoteStateConfirmed, BusinessRef: "pack:" + packID, OccurredAt: nowRFC3339()}, nowRFC3339())
	if err != nil {
		fail(w, 503, "quote_projection_pending", "公共账务已预占，记录同步尚未完成。请恢复同一请求，不要重新获取报价或重复付款。")
		return false
	}
	return true
}

func (s *Server) productionTaskParams(h store.ProductionHandoff) (map[string]string, error) {
	params := map[string]string{"handoff_id": h.ID, "store_id": h.StoreID, "pack_id": h.PackID, "quote_ref": h.QuoteRef, "brief": h.Note}
	token, err := s.St.ProductionOfferToken(h.TenantID, h.QuoteRef)
	if errors.Is(err, sql.ErrNoRows) {
		return params, nil
	} // Existing pre-4657 handoffs keep their original contract.
	if err != nil {
		return nil, err
	}
	offer, valid := s.readOffer(token)
	if !valid {
		return nil, production.ErrUnavailable
	}
	holdID, err := s.St.ProductionHoldRef(h.TenantID, h.QuoteRef)
	if err != nil || holdID == "" {
		return nil, production.ErrUnavailable
	}
	params["usage_id"] = offer.Quote.UsageID
	params["hold_id"] = holdID
	params["delivery_terms"] = offer.Quote.Deliverables
	params["pricing_version"] = offer.Quote.PricingVersion
	params["accepted_amount_minor"] = strconv.FormatInt(offer.Quote.AmountMinor, 10)
	params["funding_version"] = production.SourceReservationV1
	params["payer_account_id"] = offer.Request.PayerAccountID
	params["payer_user_id"] = offer.Request.PayerUserID
	return params, nil
}
