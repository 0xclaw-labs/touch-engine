package httpapi

// HUI-4590: 团购套餐（美团/大众点评/抖音）商家面与顾客面端点。
//
// 纪律：
//   - 鉴权复用既有 authz（不新建权限体系）：读=ReadRecord(store scope)、
//     写=Update(store scope)；他租户/他店 404 掩码同 store actions。
//     作用域事实在服务端：带门店 scope 的成员不得把套餐适用面挂到他店。
//   - 顾客面 GET-only 白名单 + 点击 beacon（匿名聚合，点击≠成交），挂
//     FEATURE_STOREFRONT 双保险（路由不注册 + handler gate 统一 404）。
//   - 外部券 check/redeem 只走授权适配器事实：适配器未接线=503 not_connected、
//     套餐缺授权=422 authorization_required、适配器未知=503 result_unknown——
//     三种情况零事实落库，永不本地假核销（复用 HUI-1671 边界语义）。
//   - 价格只在核对未过期（deals 72h）时下发顾客；过期/未核实=「到平台查看」。

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/deals"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// dealsSchemaVersion freezes the public offers payload shape.
const dealsSchemaVersion = "store_deals.v1"

func (s *Server) registerDealRoutes(mux *http.ServeMux) {
	// 商家面（session + authz；无开关，同 reward ops 管理面）。
	mux.Handle("GET /api/v1/stores/{id}/deal-offers", s.requireSession(s.handleStoreDealOffersGet))
	mux.Handle("POST /api/v1/stores/{id}/deal-offers", s.requireSession(s.handleStoreDealOfferCreate))
	mux.Handle("PATCH /api/v1/stores/{id}/deal-offers/{offerId}", s.requireSession(s.handleStoreDealOfferUpdate))
	mux.Handle("POST /api/v1/stores/{id}/deal-offers/{offerId}/status", s.requireSession(s.handleStoreDealOfferStatus))
	// 店员外部券检查/核销（授权适配器边界；未接通=诚实，不假核销）。
	// 路由名对齐票面 external_vouchers（保留店级前缀=受信作用域解析）；
	// attempt 由核销请求创建，GET 按 attemptId 轮询（unknown 行自动 Lookup 回查）。
	mux.Handle("POST /api/v1/stores/{id}/external-vouchers/inspect", s.requireSession(s.handleExternalVoucherInspect))
	mux.Handle("POST /api/v1/stores/{id}/external-vouchers/redeem", s.requireSession(s.handleExternalVoucherRedeem))
	mux.Handle("GET /api/v1/stores/{id}/external-vouchers/{attemptId}", s.requireSession(s.handleExternalVoucherGet))
	// 官方能力刷新（只用已开通官方能力；无授权返回明确能力状态，不编造平台响应）。
	mux.Handle("POST /api/v1/stores/{id}/deal-offers/{offerId}/refresh", s.requireSession(s.handleStoreDealOfferRefresh))
	// 顾客面（登记制开关 FEATURE_STOREFRONT 双保险，同公共门店面）。
	if s.Cfg.FeatureStorefront {
		mux.Handle("GET /api/v1/public/storefronts/{storeCode}/offers", http.HandlerFunc(s.handlePublicStoreOffers))
		mux.Handle("POST /api/v1/public/storefronts/{storeCode}/offers/{offerId}/clicks", http.HandlerFunc(s.handlePublicOfferClick))
	}
}

// dealsError maps the deals domain refusals onto honest HTTP answers.
func dealsError(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		return
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "not_found", "deal offer not found")
	case errors.Is(err, store.ErrDealInvalid):
		fail(w, http.StatusUnprocessableEntity, "invalid", "deal offer fields are not acceptable")
	case errors.Is(err, store.ErrDealConflict):
		fail(w, http.StatusConflict, "conflict", "another offer already carries this platform product")
	case errors.Is(err, store.ErrDealAuthorizationRequired):
		fail(w, http.StatusUnprocessableEntity, "authorization_required",
			"this offer has no external authorization yet; the coupon cannot be checked or redeemed here")
	case errors.Is(err, store.ErrDealResultUnknown):
		fail(w, http.StatusServiceUnavailable, "result_unknown",
			"the platform did not return a result; nothing was recorded, try again later")
	case errors.Is(err, store.ErrDealNotConnected):
		fail(w, http.StatusServiceUnavailable, "not_connected",
			"no authorized platform adapter is configured; the coupon cannot be checked or redeemed here")
	default:
		fail(w, http.StatusInternalServerError, "internal", "deal lookup failed")
	}
}

// storeDealScope resolves the store within the caller's tenant and authorizes
// action on it. mask404 matches store-actions discipline (out-of-scope reads
// of existing stores answer 404, target-parameter writes answer 403).
func (s *Server) storeDealScope(w http.ResponseWriter, r *http.Request, action authz.Action, mask404 bool) (*caller, store.StoreRecord, bool) {
	c := callerFrom(r)
	rec, ok := s.storeForActions(w, r, c)
	if !ok {
		return nil, store.StoreRecord{}, false
	}
	if !s.requireScopedAction(c, action, authz.RecordScope{TenantID: c.Member.TenantID, StoreID: rec.ID}, mask404, w) {
		return nil, store.StoreRecord{}, false
	}
	return c, rec, true
}

// dealStoreIDsGuard enforces the server-side scope fact: a store-scoped member
// (store_manager / scoped staff) may never attach the offer to other stores.
func dealStoreIDsGuard(c *caller, storeID string, raw []string, w http.ResponseWriter) bool {
	if c.Member.StoreScope == "" {
		return true
	}
	for _, id := range raw {
		if strings.TrimSpace(id) != "" && strings.TrimSpace(id) != storeID {
			fail(w, http.StatusForbidden, "out_of_scope", "this member cannot attach offers to other stores")
			return false
		}
	}
	return true
}

type dealOfferBody struct {
	Platform         string   `json:"platform"`
	ProviderOfferID  string   `json:"provider_offer_id"`
	ProductURL       string   `json:"product_url"`
	Title            string   `json:"title"`
	ImageRef         string   `json:"image_ref"`
	PriceMinor       *int64   `json:"price_minor"`
	Currency         string   `json:"currency"`
	PriceSource      string   `json:"price_source"`
	ValidFrom        string   `json:"valid_from"`
	ValidUntil       string   `json:"valid_until"`
	AuthorizationRef string   `json:"authorization_ref"`
	Note             string   `json:"note"`
	StoreIDs         []string `json:"store_ids"`
	// patch-only
	MarkPriceVerified bool `json:"mark_price_verified"`
}

func decodeDealBody(w http.ResponseWriter, r *http.Request) (dealOfferBody, bool) {
	var body dealOfferBody
	dec := json.NewDecoder(ioLimit(r))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid or disallowed field in body")
		return body, false
	}
	return body, true
}

func (s *Server) handleStoreDealOfferCreate(w http.ResponseWriter, r *http.Request) {
	c, rec, ok := s.storeDealScope(w, r, authz.ActionUpdate, false)
	if !ok {
		return
	}
	body, ok := decodeDealBody(w, r)
	if !ok {
		return
	}
	if body.PriceMinor == nil {
		fail(w, http.StatusBadRequest, "bad_request", "price_minor is required")
		return
	}
	if !dealStoreIDsGuard(c, rec.ID, body.StoreIDs, w) {
		return
	}
	off, err := s.St.CreateStoreDealOffer(c.Member.TenantID, rec.ID, store.NewStoreDealOffer{
		Platform:         body.Platform,
		ProviderOfferID:  body.ProviderOfferID,
		ProductURL:       body.ProductURL,
		Title:            body.Title,
		ImageRef:         body.ImageRef,
		PriceMinor:       *body.PriceMinor,
		Currency:         body.Currency,
		PriceSource:      body.PriceSource,
		ValidFrom:        body.ValidFrom,
		ValidUntil:       body.ValidUntil,
		AuthorizationRef: body.AuthorizationRef,
		Note:             body.Note,
		CreatedBy:        c.Member.PrincipalRef,
		StoreIDs:         body.StoreIDs,
	})
	if err != nil {
		dealsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, off)
}

func (s *Server) handleStoreDealOffersGet(w http.ResponseWriter, r *http.Request) {
	c, rec, ok := s.storeDealScope(w, r, authz.ActionReadRecord, true)
	if !ok {
		return
	}
	offers, err := s.St.ListStoreDealOffers(c.Member.TenantID, rec.ID)
	if err != nil {
		dealsError(w, err)
		return
	}
	perOffer, byPlatform, err := s.St.StoreOfferClickTotals(c.Member.TenantID, rec.ID)
	if err != nil {
		dealsError(w, err)
		return
	}
	now := time.Now().UTC()
	items := make([]map[string]any, 0, len(offers))
	for _, o := range offers {
		doc := dealItemDocument(o, now)
		doc["click_total"] = perOffer[o.ID]
		items = append(items, doc)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"store_id":   rec.ID,
		"store_name": rec.Name,
		"items":      items,
		"by_platform": map[string]any{
			"clicks":     byPlatform,
			"deals":      nil,
			"deals_note": "点击不是成交；平台成交与核销数本地不可知（授权适配器返回的事实除外）。",
		},
		"price_policy": "核对未超过 72 小时的价格才对顾客展示；过期/未核实显示「到平台查看」。",
	})
}

func dealItemDocument(o store.StoreDealOffer, now time.Time) map[string]any {
	return map[string]any{
		"id":                o.ID,
		"platform":          o.Platform,
		"provider_offer_id": o.ProviderOfferID,
		"product_url":       o.ProductURL,
		"title":             o.Title,
		"image_ref":         o.ImageRef,
		"price_minor":       o.PriceMinor,
		"currency":          o.Currency,
		"price_source":      o.PriceSource,
		"price_verified_at": o.PriceVerifiedAt,
		"price_state":       deals.PriceState(o.PriceVerifiedAt, o.ValidFrom, o.ValidUntil, o.Status, now),
		"valid_from":        o.ValidFrom,
		"valid_until":       o.ValidUntil,
		"status":            o.Status,
		"authorization_ref": o.AuthorizationRef,
		"note":              o.Note,
		"created_at":        o.CreatedAt,
		"updated_at":        o.UpdatedAt,
		"store_ids":         o.StoreIDs,
	}
}

func (s *Server) handleStoreDealOfferUpdate(w http.ResponseWriter, r *http.Request) {
	c, rec, ok := s.storeDealScope(w, r, authz.ActionUpdate, true)
	if !ok {
		return
	}
	offerID := r.PathValue("offerId")
	// 作用域事实：套餐必须适用本店（不适用=404 掩码，越店不可管理）。
	applicable, err := s.St.IsStoreOfferApplicable(c.Member.TenantID, rec.ID, offerID)
	if err != nil {
		dealsError(w, err)
		return
	}
	if !applicable {
		fail(w, http.StatusNotFound, "not_found", "deal offer not found")
		return
	}
	body, ok := decodeDealBody(w, r)
	if !ok {
		return
	}
	if body.StoreIDs != nil && !dealStoreIDsGuard(c, rec.ID, body.StoreIDs, w) {
		return
	}
	in := store.UpdateStoreDealOffer{
		Title:           optionalStr(body.Title),
		ProductURL:      optionalStr(body.ProductURL),
		ImageRef:        optionalStr(body.ImageRef),
		ProviderOfferID: optionalStr(body.ProviderOfferID),
		Currency:        optionalStr(body.Currency),
		PriceSource:     optionalStr(body.PriceSource),
		ValidFrom:       optionalStr(body.ValidFrom),
		ValidUntil:      optionalStr(body.ValidUntil),
		Note:            optionalStr(body.Note),
	}
	if body.PriceMinor != nil {
		in.PriceMinor = body.PriceMinor
	}
	if body.MarkPriceVerified {
		in.MarkPriceVerifiedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if body.StoreIDs != nil {
		in.StoreIDs = &body.StoreIDs
	}
	if body.AuthorizationRef != "" {
		ref := body.AuthorizationRef
		in.AuthorizationRef = &ref
	}
	off, err := s.St.UpdateStoreDealOffer(offerID, c.Member.TenantID, in)
	if err != nil {
		dealsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dealItemDocument(off, time.Now().UTC()))
}

func optionalStr(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func (s *Server) handleStoreDealOfferStatus(w http.ResponseWriter, r *http.Request) {
	c, rec, ok := s.storeDealScope(w, r, authz.ActionUpdate, true)
	if !ok {
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	dec := json.NewDecoder(ioLimit(r))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil || strings.TrimSpace(body.Status) == "" {
		fail(w, http.StatusBadRequest, "bad_request", "status is required")
		return
	}
	// 作用域事实：套餐必须适用本店；停用/暂停中的门店仍可管理自己的套餐行。
	applicable, err := s.St.IsStoreOfferApplicable(c.Member.TenantID, rec.ID, r.PathValue("offerId"))
	if err != nil {
		dealsError(w, err)
		return
	}
	if !applicable {
		fail(w, http.StatusNotFound, "not_found", "deal offer not found")
		return
	}
	off, err := s.St.SetStoreDealOfferStatus(r.PathValue("offerId"), c.Member.TenantID, strings.TrimSpace(body.Status))
	if err != nil {
		dealsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dealItemDocument(off, time.Now().UTC()))
}

// ---- 店员外部券 check / redeem -------------------------------------------------

type dealCouponBody struct {
	OfferID string `json:"offer_id"`
	Code    string `json:"code"`
}

func decodeDealCouponBody(w http.ResponseWriter, r *http.Request) (dealCouponBody, bool) {
	var body dealCouponBody
	dec := json.NewDecoder(ioLimit(r))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid or disallowed field in body")
		return body, false
	}
	if strings.TrimSpace(body.OfferID) == "" || strings.TrimSpace(body.Code) == "" {
		fail(w, http.StatusBadRequest, "bad_request", "offer_id and code are required")
		return body, false
	}
	return body, true
}

func (s *Server) handleExternalVoucherInspect(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.storeDealScope(w, r, authz.ActionReadRecord, true)
	if !ok {
		return
	}
	body, ok := decodeDealCouponBody(w, r)
	if !ok {
		return
	}
	off, err := s.St.AuthorizeStoreCouponTarget(c.Member.TenantID, r.PathValue("id"), body.OfferID)
	if err != nil {
		dealsError(w, err)
		return
	}
	if s.DealCoupons == nil {
		dealsError(w, store.ErrDealNotConnected)
		return
	}
	res, err := s.DealCoupons.Check(deals.CheckRequest{
		Platform: off.Platform, OfferRef: off.ProviderOfferID, CouponRef: strings.TrimSpace(body.Code),
	})
	if err != nil {
		// 适配器故障是诚实未知：不下结论，也不落任何事实。
		dealsError(w, store.ErrDealResultUnknown)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"valid": res.Valid, "detail": res.Detail})
}

func (s *Server) handleExternalVoucherRedeem(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.storeDealScope(w, r, authz.ActionUpdate, true)
	if !ok {
		return
	}
	body, ok := decodeDealCouponBody(w, r)
	if !ok {
		return
	}
	// 先给授权面事实（缺授权 422），再答未接通（503）——两者都不落任何事实。
	if _, err := s.St.AuthorizeStoreCouponTarget(c.Member.TenantID, r.PathValue("id"), body.OfferID); err != nil {
		dealsError(w, err)
		return
	}
	if s.DealCoupons == nil {
		dealsError(w, store.ErrDealNotConnected)
		return
	}
	attempt, replayed, upgraded, err := s.St.RedeemExternalVoucher(store.VoucherAttemptInput{
		TenantID: c.Member.TenantID, StoreID: r.PathValue("id"),
		OfferID: body.OfferID, CouponRef: strings.TrimSpace(body.Code),
		Actor: c.Member.PrincipalRef,
	}, s.DealCoupons, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		dealsError(w, err)
		return
	}
	outcome := attempt.State
	if replayed {
		outcome = "already_redeemed"
	} else if upgraded {
		outcome = "redeemed" // 晚到确认升级（同一请求回查所得，不是第二次核销）
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"attempt_id": attempt.ID, "outcome": outcome, "state": attempt.State, "detail": attempt.Detail,
	})
}

// handleExternalVoucherGet polls one attempt (跨店/跨租户 ID 404 掩码)；
// unknown 行在适配器在位时自动 Lookup 回查一次——晚到的平台答案确认后升级，
// 仍未知保持待确认（不是已核销）。
func (s *Server) handleExternalVoucherGet(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.storeDealScope(w, r, authz.ActionReadRecord, true)
	if !ok {
		return
	}
	attempt, err := s.St.GetVoucherAttempt(c.Member.TenantID, r.PathValue("id"), r.PathValue("attemptId"))
	if err != nil {
		dealsError(w, err)
		return
	}
	if attempt.State == deals.RedeemUnknown && s.DealCoupons != nil {
		if attempt, err = s.St.RequeryVoucherAttempt(c.Member.TenantID, r.PathValue("id"), attempt.ID, s.DealCoupons, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			dealsError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"attempt_id": attempt.ID, "offer_id": attempt.OfferID, "platform": attempt.Platform,
		"state": attempt.State, "detail": attempt.Detail, "created_at": attempt.CreatedAt,
	})
}

// handleStoreDealOfferRefresh re-pulls official price facts for one offer.
// 未接适配器=503 not_connected、缺授权=422 authorization_required（明确能力
// 状态）；适配器故障=503 result_unknown 且零改动；成功=官方价+校验时间落库。
func (s *Server) handleStoreDealOfferRefresh(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.storeDealScope(w, r, authz.ActionUpdate, true)
	if !ok {
		return
	}
	if s.DealCoupons == nil {
		dealsError(w, store.ErrDealNotConnected)
		return
	}
	var platformDetail string
	refresh := func(offerRef string) (deals.RefreshResult, error) {
		res, err := s.DealCoupons.RefreshOffer(offerRef)
		if err == nil {
			platformDetail = res.Detail
		}
		return res, err
	}
	off, err := s.St.RefreshStoreDealOffer(c.Member.TenantID, r.PathValue("id"), r.PathValue("offerId"), refresh, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		dealsError(w, err)
		return
	}
	doc := dealItemDocument(off, time.Now().UTC())
	if platformDetail != "" {
		doc["platform_detail"] = platformDetail
	}
	writeJSON(w, http.StatusOK, doc)
}

// ---- 顾客公共面 ---------------------------------------------------------------

func (s *Server) handlePublicStoreOffers(w http.ResponseWriter, r *http.Request) {
	if !s.Cfg.FeatureStorefront {
		fail(w, http.StatusNotFound, "not_found", "storefront is not available")
		return
	}
	pub, err := s.St.GetStorefrontPublicByCode(r.PathValue("storeCode"))
	if err != nil {
		fail(w, http.StatusNotFound, "not_found", "storefront is not available")
		return
	}
	if pub.TenantLifecycle != "active" {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": "not_found", "state": "unauthorized",
			"message": "storefront is not available",
		})
		return
	}
	state := "active"
	if pub.StoreStatus != "active" {
		state = "disabled"
	}
	offers := []map[string]any{}
	if state == "active" {
		rows, err := s.St.ListPublicStoreOffers(pub.StoreID, time.Now().UTC())
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "storefront lookup failed")
			return
		}
		for _, o := range rows {
			item := map[string]any{
				"offer_id":      o.ID,
				"platform":      o.Platform,
				"title":         o.Title,
				"product_url":   o.ProductURL,
				"price_display": o.PriceDisplay,
			}
			if o.ImageRef != "" {
				item["image_ref"] = o.ImageRef
			}
			if o.ValidUntil != "" {
				item["valid_until"] = o.ValidUntil
			}
			// 价格只对核对未过期的套餐下发；过期/未核实=「到平台查看」。
			if o.PriceDisplay == deals.PriceShow {
				item["price_minor"] = o.PriceMinor
				item["currency"] = o.Currency
			}
			offers = append(offers, item)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version": dealsSchemaVersion,
		"store_code":     pub.StoreCode,
		"store": map[string]any{
			"name":  pub.StoreName,
			"state": state,
		},
		"price_policy": "仅展示核对未超过 72 小时的价格；其余请到平台查看实时价。",
		"deals_note":   "下单与核销在平台完成；这里的点击不计为成交。",
		"offers":       offers,
	})
}

func (s *Server) handlePublicOfferClick(w http.ResponseWriter, r *http.Request) {
	if !s.Cfg.FeatureStorefront {
		fail(w, http.StatusNotFound, "not_found", "storefront is not available")
		return
	}
	pub, err := s.St.GetStorefrontPublicByCode(r.PathValue("storeCode"))
	if err != nil {
		fail(w, http.StatusNotFound, "not_found", "storefront is not available")
		return
	}
	if pub.TenantLifecycle != "active" {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": "not_found", "state": "unauthorized",
			"message": "storefront is not available",
		})
		return
	}
	// body 可空；禁字段（tenant/principal…）拒收，绝不计入点击。
	dec := json.NewDecoder(ioLimit(r))
	dec.DisallowUnknownFields()
	var in struct {
		Channel string `json:"channel"`
	}
	if err := dec.Decode(&in); err != nil && !errors.Is(err, io.EOF) {
		fail(w, http.StatusBadRequest, "bad_request", "invalid or disallowed field in body")
		return
	}
	applicable, err := s.St.IsStoreOfferApplicable(pub.TenantID, pub.StoreID, r.PathValue("offerId"))
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "storefront lookup failed")
		return
	}
	if !applicable {
		fail(w, http.StatusNotFound, "not_found", "offer is not available")
		return
	}
	day := time.Now().UTC().Format("2006-01-02")
	if err := s.St.IncrementStoreOfferClick(pub.TenantID, pub.StoreID, r.PathValue("offerId"), day); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "click stat failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
