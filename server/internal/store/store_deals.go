package store

// HUI-4590: 门店面团购套餐（store_deal_offers）与外部券核销事实。
//
// 纪律：
//   - 与 HUI-1671 的 deal_offers（活动强绑奖励供给）互不改写；本文件只碰
//     0084-0087 四张新表。
//   - platform 固定三渠道；美团与点评分渠道存行（归因分渠道保留）。
//   - 公共投影 fail-closed：active ∧ 有效窗口 ∧ 读出侧 URL 复验通过才出现；
//     价格只在 deals.PriceState=="show"（核对未过期）时对顾客可见。
//   - 点击只是点击：点击进聚合统计表，无任何"成交"语义。
//   - 外部券核销事实只来自授权适配器闭包的返回（复用 1671 边界纪律）：
//     redeemed 终态唯一并回读、failed 留痕可重试、未知零行落库；本地奖励
//     账（reward_benefits 等）结构性不写入。coupon_ref 敏感：不进日志、
//     不进公共面、错误信息不携带。

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/deals"
)

// Deals-domain refusals (HTTP layer maps them onto stable codes).
var (
	// ErrDealInvalid: 校验不过（平台/URL/价格/状态机）。
	ErrDealInvalid = errors.New("store: deal offer invalid")
	// ErrDealConflict: 同租户同平台同平台商品 ID 已存在。
	ErrDealConflict = errors.New("store: deal offer conflicts with an existing row")
	// ErrDealAuthorizationRequired: 套餐缺外部授权引用，核销边界拒绝。
	ErrDealAuthorizationRequired = errors.New("store: deal offer lacks an authorization ref")
	// ErrDealResultUnknown: 适配器传输失败/未知——诚实未知，零事实。
	ErrDealResultUnknown = errors.New("store: external coupon result unknown")
	// ErrDealNotConnected: 本部署没有接任何授权适配器——诚实未接通。
	ErrDealNotConnected = errors.New("store: no authorized platform adapter is connected")
)

// StoreDealOffer is one storefront group-buy offer row plus its applicable
// stores.
type StoreDealOffer struct {
	ID               string   `json:"id"`
	TenantID         string   `json:"-"`
	Platform         string   `json:"platform"`
	ProviderOfferID  string   `json:"provider_offer_id,omitempty"`
	ProductURL       string   `json:"product_url"`
	Title            string   `json:"title"`
	ImageRef         string   `json:"image_ref,omitempty"`
	PriceMinor       int64    `json:"price_minor"`
	Currency         string   `json:"currency,omitempty"`
	PriceSource      string   `json:"price_source,omitempty"`
	PriceVerifiedAt  string   `json:"price_verified_at,omitempty"`
	ValidFrom        string   `json:"valid_from,omitempty"`
	ValidUntil       string   `json:"valid_until,omitempty"`
	Status           string   `json:"status"`
	AuthorizationRef string   `json:"authorization_ref,omitempty"`
	Note             string   `json:"note,omitempty"`
	CreatedBy        string   `json:"created_by"`
	CreatedAt        string   `json:"created_at"`
	UpdatedAt        string   `json:"updated_at"`
	StoreIDs         []string `json:"store_ids"`
}

// NewStoreDealOffer is the creation input. StoreIDs lists additional tenant
// stores; the creator store is always included.
type NewStoreDealOffer struct {
	Platform         string
	ProviderOfferID  string
	ProductURL       string
	Title            string
	ImageRef         string
	PriceMinor       int64
	Currency         string
	PriceSource      string
	ValidFrom        string
	ValidUntil       string
	AuthorizationRef string
	Note             string
	CreatedBy        string
	StoreIDs         []string
}

// UpdateStoreDealOffer carries optional edits; nil = unchanged.
// MarkPriceVerifiedAt is the server-side verification timestamp (校验时间管理).
// AuthorizationRef records an adapter grant once the merchant obtains an
// official authorization (空串=显式清除).
type UpdateStoreDealOffer struct {
	Title               *string
	ProductURL          *string
	ImageRef            *string
	ProviderOfferID     *string
	PriceMinor          *int64
	Currency            *string
	PriceSource         *string
	ValidFrom           *string
	ValidUntil          *string
	Note                *string
	AuthorizationRef    *string
	MarkPriceVerifiedAt string
	StoreIDs            *[]string
}

// PublicStoreOffer is the whitelisted customer projection. PriceMinor is
// meaningful only when PriceDisplay == deals.PriceShow.
type PublicStoreOffer struct {
	ID           string
	Platform     string
	Title        string
	ImageRef     string
	ProductURL   string
	PriceDisplay deals.PriceDisplay
	PriceMinor   int64
	Currency     string
	ValidUntil   string
}

const storeDealOfferCols = `id,tenant_id,platform,provider_offer_id,product_url,title,image_ref,price_minor,currency,price_source,price_verified_at,valid_from,valid_until,status,authorization_ref,note,created_by,created_at,updated_at`

func scanStoreDealOffer(sc interface{ Scan(...any) error }) (StoreDealOffer, error) {
	var o StoreDealOffer
	var providerOfferID, productURL, imageRef, currency, priceSource, verifiedAt, from, until, authRef, note, createdBy sql.NullString
	err := sc.Scan(&o.ID, &o.TenantID, &o.Platform, &providerOfferID, &productURL, &o.Title,
		&imageRef, &o.PriceMinor, &currency, &priceSource, &verifiedAt, &from, &until,
		&o.Status, &authRef, &note, &createdBy, &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		return StoreDealOffer{}, err
	}
	o.ProviderOfferID, o.ProductURL, o.ImageRef = providerOfferID.String, productURL.String, imageRef.String
	o.Currency, o.PriceSource, o.PriceVerifiedAt = currency.String, priceSource.String, verifiedAt.String
	o.ValidFrom, o.ValidUntil = from.String, until.String
	o.AuthorizationRef, o.Note, o.CreatedBy = authRef.String, note.String, createdBy.String
	return o, nil
}

// validateDealWindow enforces RFC3339 (or empty) on both window bounds.
func validateDealWindow(from, until string) error {
	for _, raw := range []string{from, until} {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if _, err := time.Parse(time.RFC3339, raw); err != nil {
			if _, err2 := time.Parse(time.RFC3339Nano, raw); err2 != nil {
				return fmt.Errorf("%w: valid window must be RFC3339", ErrDealInvalid)
			}
		}
	}
	return nil
}

// resolveDealStores validates the applicability set: every store must exist in
// THIS tenant; the creator store is always included; duplicates collapse.
func (s *Store) resolveDealStores(tenantID, creatorStoreID string, extra []string) ([]string, error) {
	seen := map[string]bool{creatorStoreID: true}
	out := []string{creatorStoreID}
	for _, raw := range extra {
		id := strings.TrimSpace(raw)
		if id == "" || seen[id] {
			continue
		}
		if _, err := s.GetStore(id, tenantID); err != nil {
			return nil, ErrNotFound
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

// CreateStoreDealOffer validates and inserts one offer plus applicability rows.
func (s *Store) CreateStoreDealOffer(tenantID, creatorStoreID string, in NewStoreDealOffer) (StoreDealOffer, error) {
	o := StoreDealOffer{}
	if _, err := s.GetStore(creatorStoreID, tenantID); err != nil {
		return o, ErrNotFound
	}
	platform := strings.TrimSpace(in.Platform)
	title := strings.TrimSpace(in.Title)
	url := strings.TrimSpace(in.ProductURL)
	if !deals.ValidPlatform(platform) {
		return o, fmt.Errorf("%w: platform must be meituan|dianping|douyin", ErrDealInvalid)
	}
	if title == "" || url == "" || strings.TrimSpace(in.CreatedBy) == "" {
		return o, fmt.Errorf("%w: title, product_url and created_by are required", ErrDealInvalid)
	}
	if !deals.ProductURLValid(platform, url) {
		return o, fmt.Errorf("%w: product_url must be an official public https page of the same platform", ErrDealInvalid)
	}
	if in.PriceMinor < 0 {
		return o, fmt.Errorf("%w: negative price", ErrDealInvalid)
	}
	priceSource := strings.TrimSpace(in.PriceSource)
	switch priceSource {
	case "", "platform", "merchant":
	default:
		return o, fmt.Errorf("%w: price_source must be platform|merchant", ErrDealInvalid)
	}
	if priceSource == "" {
		priceSource = "merchant"
	}
	if err := validateDealWindow(in.ValidFrom, in.ValidUntil); err != nil {
		return o, err
	}
	storeIDs, err := s.resolveDealStores(tenantID, creatorStoreID, in.StoreIDs)
	if err != nil {
		return o, err
	}
	o = StoreDealOffer{
		ID: newID("sdoff_"), TenantID: tenantID, Platform: platform,
		ProviderOfferID: strings.TrimSpace(in.ProviderOfferID), ProductURL: url,
		Title: title, ImageRef: strings.TrimSpace(in.ImageRef),
		PriceMinor: in.PriceMinor, Currency: strings.TrimSpace(in.Currency),
		PriceSource: priceSource, ValidFrom: strings.TrimSpace(in.ValidFrom),
		ValidUntil: strings.TrimSpace(in.ValidUntil), Status: deals.StatusActive,
		AuthorizationRef: strings.TrimSpace(in.AuthorizationRef), Note: strings.TrimSpace(in.Note),
		CreatedBy: strings.TrimSpace(in.CreatedBy), CreatedAt: now(), UpdatedAt: now(),
		StoreIDs: storeIDs,
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return StoreDealOffer{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		`INSERT INTO store_deal_offers(`+storeDealOfferCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		o.ID, o.TenantID, o.Platform, o.ProviderOfferID, o.ProductURL, o.Title, o.ImageRef,
		o.PriceMinor, o.Currency, o.PriceSource, o.PriceVerifiedAt, o.ValidFrom, o.ValidUntil,
		o.Status, o.AuthorizationRef, o.Note, o.CreatedBy, o.CreatedAt, o.UpdatedAt); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return o, ErrDealConflict
		}
		return StoreDealOffer{}, err
	}
	for _, sid := range storeIDs {
		if _, err := tx.Exec(
			`INSERT INTO store_deal_offer_stores(offer_id,store_id,tenant_id,created_at) VALUES(?,?,?,?)`,
			o.ID, sid, tenantID, o.CreatedAt); err != nil {
			return StoreDealOffer{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return StoreDealOffer{}, err
	}
	return o, nil
}

// loadOfferStoreIDs loads the applicability set of one offer.
func (s *Store) loadOfferStoreIDs(tenantID, offerID string) ([]string, error) {
	rs, err := s.DB.Query(
		`SELECT store_id FROM store_deal_offer_stores WHERE offer_id=? AND tenant_id=? ORDER BY store_id`,
		offerID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := []string{}
	for rs.Next() {
		var id string
		if err := rs.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rs.Err()
}

// GetStoreDealOffer loads one offer within the tenant scope.
func (s *Store) GetStoreDealOffer(id, tenantID string) (StoreDealOffer, error) {
	row := s.DB.QueryRow(`SELECT `+storeDealOfferCols+` FROM store_deal_offers WHERE id=? AND tenant_id=?`, id, tenantID)
	o, err := scanStoreDealOffer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return StoreDealOffer{}, ErrNotFound
	}
	if err != nil {
		return StoreDealOffer{}, err
	}
	o.StoreIDs, err = s.loadOfferStoreIDs(tenantID, o.ID)
	return o, err
}

// ListStoreDealOffers returns the offers applicable to one store (live first,
// retired never). Retired rows stay queryable via GetStoreDealOffer for audit.
func (s *Store) ListStoreDealOffers(tenantID, storeID string) ([]StoreDealOffer, error) {
	rs, err := s.DB.Query(
		`SELECT `+storeDealOfferCols+` FROM store_deal_offers o
		 WHERE o.tenant_id=? AND o.status<>? AND EXISTS(
			SELECT 1 FROM store_deal_offer_stores m WHERE m.offer_id=o.id AND m.store_id=? AND m.tenant_id=o.tenant_id)
		 ORDER BY o.created_at, o.id`, tenantID, deals.StatusRetired, storeID)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := []StoreDealOffer{}
	for rs.Next() {
		o, err := scanStoreDealOffer(rs)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	if err := rs.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		ids, err := s.loadOfferStoreIDs(tenantID, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].StoreIDs = ids
	}
	return out, nil
}

// UpdateStoreDealOffer applies optional edits. An empty replacement is
// expressed by a pointer to "" (the nil pointer means unchanged).
func (s *Store) UpdateStoreDealOffer(id, tenantID string, in UpdateStoreDealOffer) (StoreDealOffer, error) {
	cur, err := s.GetStoreDealOffer(id, tenantID)
	if err != nil {
		return StoreDealOffer{}, err
	}
	if cur.Status == deals.StatusRetired {
		return StoreDealOffer{}, fmt.Errorf("%w: retired offers are immutable", ErrDealInvalid)
	}
	title := cur.Title
	if in.Title != nil {
		if strings.TrimSpace(*in.Title) == "" {
			return StoreDealOffer{}, fmt.Errorf("%w: title cannot be empty", ErrDealInvalid)
		}
		title = strings.TrimSpace(*in.Title)
	}
	url := cur.ProductURL
	if in.ProductURL != nil {
		url = strings.TrimSpace(*in.ProductURL)
	}
	if !deals.ProductURLValid(cur.Platform, url) {
		return StoreDealOffer{}, fmt.Errorf("%w: product_url must be an official public https page of the same platform", ErrDealInvalid)
	}
	price := cur.PriceMinor
	if in.PriceMinor != nil {
		if *in.PriceMinor < 0 {
			return StoreDealOffer{}, fmt.Errorf("%w: negative price", ErrDealInvalid)
		}
		price = *in.PriceMinor
	}
	priceSource := cur.PriceSource
	if in.PriceSource != nil {
		switch strings.TrimSpace(*in.PriceSource) {
		case "platform", "merchant":
			priceSource = strings.TrimSpace(*in.PriceSource)
		default:
			return StoreDealOffer{}, fmt.Errorf("%w: price_source must be platform|merchant", ErrDealInvalid)
		}
	}
	validFrom, validUntil := cur.ValidFrom, cur.ValidUntil
	if in.ValidFrom != nil {
		validFrom = strings.TrimSpace(*in.ValidFrom)
	}
	if in.ValidUntil != nil {
		validUntil = strings.TrimSpace(*in.ValidUntil)
	}
	if err := validateDealWindow(validFrom, validUntil); err != nil {
		return StoreDealOffer{}, err
	}
	verifiedAt := cur.PriceVerifiedAt
	if strings.TrimSpace(in.MarkPriceVerifiedAt) != "" {
		verifiedAt = strings.TrimSpace(in.MarkPriceVerifiedAt)
	}
	authRef := cur.AuthorizationRef
	if in.AuthorizationRef != nil {
		authRef = strings.TrimSpace(*in.AuthorizationRef)
	}
	storeIDs := cur.StoreIDs
	if in.StoreIDs != nil {
		// 更新侧适用面是"完整替换"语义：列表内每店都必须在本租户内，且至少
		// 一家（空=套餐挂不到任何门店，直接拒绝）。创建侧的"必含创建店"锚点
		// 不在此处复用——总部可以把套餐从原创建店摘下换挂他店。
		resolved := []string{}
		seen := map[string]bool{}
		for _, raw := range *in.StoreIDs {
			id := strings.TrimSpace(raw)
			if id == "" || seen[id] {
				continue
			}
			if _, err := s.GetStore(id, tenantID); err != nil {
				return StoreDealOffer{}, ErrNotFound
			}
			seen[id] = true
			resolved = append(resolved, id)
		}
		if len(resolved) == 0 {
			return StoreDealOffer{}, fmt.Errorf("%w: at least one applicable store is required", ErrDealInvalid)
		}
		storeIDs = resolved
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return StoreDealOffer{}, err
	}
	defer tx.Rollback()
	ts := now()
	if _, err := tx.Exec(
		`UPDATE store_deal_offers SET title=?, product_url=?, image_ref=?, provider_offer_id=?, price_minor=?,
		 currency=?, price_source=?, valid_from=?, valid_until=?, price_verified_at=?, note=?, authorization_ref=?, updated_at=?
		 WHERE id=? AND tenant_id=?`,
		title, url, pickStr(in.ImageRef, cur.ImageRef), pickStr(in.ProviderOfferID, cur.ProviderOfferID),
		price, pickStr(in.Currency, cur.Currency), priceSource, validFrom, validUntil, verifiedAt,
		pickStr(in.Note, cur.Note), authRef, ts, id, tenantID); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return StoreDealOffer{}, ErrDealConflict
		}
		return StoreDealOffer{}, err
	}
	if in.StoreIDs != nil {
		if _, err := tx.Exec(`DELETE FROM store_deal_offer_stores WHERE offer_id=? AND tenant_id=?`, id, tenantID); err != nil {
			return StoreDealOffer{}, err
		}
		for _, sid := range storeIDs {
			if _, err := tx.Exec(
				`INSERT INTO store_deal_offer_stores(offer_id,store_id,tenant_id,created_at) VALUES(?,?,?,?)`,
				id, sid, tenantID, ts); err != nil {
				return StoreDealOffer{}, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return StoreDealOffer{}, err
	}
	return s.GetStoreDealOffer(id, tenantID)
}

func pickStr(p *string, cur string) string {
	if p == nil {
		return cur
	}
	return strings.TrimSpace(*p)
}

// SetStoreDealOfferStatus moves the offer through active|paused|retired.
// Retired is irreversible (下架/退役不再回到货架).
func (s *Store) SetStoreDealOfferStatus(id, tenantID, status string) (StoreDealOffer, error) {
	switch status {
	case deals.StatusActive, deals.StatusPaused, deals.StatusRetired:
	default:
		return StoreDealOffer{}, fmt.Errorf("%w: unknown status %q", ErrDealInvalid, status)
	}
	cur, err := s.GetStoreDealOffer(id, tenantID)
	if err != nil {
		return StoreDealOffer{}, err
	}
	if cur.Status == deals.StatusRetired {
		return StoreDealOffer{}, fmt.Errorf("%w: retired offers cannot change status", ErrDealInvalid)
	}
	if _, err := s.DB.Exec(
		`UPDATE store_deal_offers SET status=?, updated_at=? WHERE id=? AND tenant_id=?`,
		status, now(), id, tenantID); err != nil {
		return StoreDealOffer{}, err
	}
	return s.GetStoreDealOffer(id, tenantID)
}

// ListPublicStoreOffers is the customer projection: only active offers inside
// their validity window whose product URL still passes the official-address
// gate. Rows that fail any check stay invisible (fail-closed). The price is
// shown only when deals.PriceState answers show; otherwise the caller renders
// 「到平台查看」.
func (s *Store) ListPublicStoreOffers(storeID string, now time.Time) ([]PublicStoreOffer, error) {
	rs, err := s.DB.Query(
		`SELECT o.id, o.platform, o.title, o.image_ref, o.product_url, o.price_minor, o.currency,
		 o.price_verified_at, o.valid_from, o.valid_until
		 FROM store_deal_offers o
		 WHERE o.status=? AND EXISTS(
			SELECT 1 FROM store_deal_offer_stores m WHERE m.offer_id=o.id AND m.store_id=? AND m.tenant_id=o.tenant_id)
		 ORDER BY o.created_at, o.id`, deals.StatusActive, storeID)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := []PublicStoreOffer{}
	for rs.Next() {
		var o PublicStoreOffer
		var imageRef, currency, verifiedAt, from, until sql.NullString
		if err := rs.Scan(&o.ID, &o.Platform, &o.Title, &imageRef, &o.ProductURL, &o.PriceMinor,
			&currency, &verifiedAt, &from, &until); err != nil {
			return nil, err
		}
		o.ImageRef, o.Currency, o.ValidUntil = imageRef.String, currency.String, until.String
		// 读出侧复验（保存门之外的第二道）：坏 URL 整行不出现。
		if !deals.ProductURLValid(o.Platform, o.ProductURL) {
			continue
		}
		// 失效（窗口未开始/已结束）停购买入口：整行不出现（替代=其余在列行）。
		if !deals.WindowOpen(from.String, until.String, now) {
			continue
		}
		o.PriceDisplay = deals.PriceState(verifiedAt.String, from.String, until.String, deals.StatusActive, now)
		out = append(out, o)
	}
	return out, rs.Err()
}

// IsStoreOfferApplicable reports whether the offer applies to the store.
// 语义=核销/授权面（1671 F18）：paused 套餐已发券必须继续可查验与兑现，
// 外部有效性归平台适配器裁决——这里只判映射，不判本地展示状态。
func (s *Store) IsStoreOfferApplicable(tenantID, storeID, offerID string) (bool, error) {
	var n int
	err := s.DB.QueryRow(
		`SELECT COUNT(*) FROM store_deal_offer_stores WHERE offer_id=? AND store_id=? AND tenant_id=?`,
		offerID, storeID, tenantID).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// IsStoreOfferClickable (HUI-3531 Walk D adv06/adv07) is the CLICK beacon
// validity: 仅在售（active）计数——下架/暂停/隐藏的旧地址重放一律 404 不计数。
// 与核销面（IsStoreOfferApplicable）语义刻意分离。
func (s *Store) IsStoreOfferClickable(tenantID, storeID, offerID string) (bool, error) {
	var n int
	err := s.DB.QueryRow(
		`SELECT COUNT(*) FROM store_deal_offer_stores m
		 JOIN store_deal_offers o ON o.id=m.offer_id AND o.tenant_id=m.tenant_id
		 WHERE m.offer_id=? AND m.store_id=? AND m.tenant_id=? AND o.status=?`,
		offerID, storeID, tenantID, deals.StatusActive).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// IncrementStoreOfferClick adds one anonymous click to the day aggregate.
// A click is a click: no deal semantics anywhere downstream.
func (s *Store) IncrementStoreOfferClick(tenantID, storeID, offerID, day string) error {
	var platform string
	if err := s.DB.QueryRow(
		`SELECT platform FROM store_deal_offers WHERE id=? AND tenant_id=?`, offerID, tenantID).Scan(&platform); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if _, err := s.DB.Exec(
		`INSERT INTO store_deal_click_stats(tenant_id,store_id,offer_id,platform,day,clicks,updated_at)
		 VALUES(?,?,?,?,?,1,?)
		 ON CONFLICT(offer_id,day) DO UPDATE SET clicks=clicks+1, updated_at=excluded.updated_at`,
		tenantID, storeID, offerID, platform, day, now()); err != nil {
		return err
	}
	return nil
}

// StoreOfferClickTotals aggregates clicks for one store: per offer and per
// platform (分渠道归因保留). Deals are NOT counted anywhere: 下单在平台完成.
func (s *Store) StoreOfferClickTotals(tenantID, storeID string) (map[string]int64, map[string]int64, error) {
	rs, err := s.DB.Query(
		`SELECT offer_id, platform, clicks FROM store_deal_click_stats WHERE tenant_id=? AND store_id=?`,
		tenantID, storeID)
	if err != nil {
		return nil, nil, err
	}
	defer rs.Close()
	perOffer := map[string]int64{}
	byPlatform := map[string]int64{}
	for rs.Next() {
		var offerID, platform string
		var clicks int64
		if err := rs.Scan(&offerID, &platform, &clicks); err != nil {
			return nil, nil, err
		}
		perOffer[offerID] += clicks
		byPlatform[platform] += clicks
	}
	return perOffer, byPlatform, rs.Err()
}

// VoucherAttemptInput is one staff redemption attempt toward the platform.
// The natural idempotency key is (tenant, offer, coupon_ref): the same coupon
// re-submitted is the same platform request, never a second one.
type VoucherAttemptInput struct {
	TenantID  string
	StoreID   string
	OfferID   string
	CouponRef string
	Actor     string
}

// ExternalVoucherAttempt is one persisted redemption attempt
// (external_voucher_attempt). coupon_ref is sensitive: it never serializes
// into any JSON response, log, or analytics event.
type ExternalVoucherAttempt struct {
	ID        string `json:"id"`
	TenantID  string `json:"-"`
	StoreID   string `json:"-"`
	OfferID   string `json:"offer_id"`
	Platform  string `json:"platform"`
	AssetRef  string `json:"-"`
	State     string `json:"state"`
	Detail    string `json:"detail,omitempty"`
	Actor     string `json:"-"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// AuthorizeStoreCouponTarget resolves the redemption target: the offer must
// exist, apply to this store, and carry an authorization ref. Paused/retired
// display state does NOT block redemption — the customer already bought the
// voucher on the platform; pausing only stops new exposure.
func (s *Store) AuthorizeStoreCouponTarget(tenantID, storeID, offerID string) (StoreDealOffer, error) {
	ok, err := s.IsStoreOfferApplicable(tenantID, storeID, offerID)
	if err != nil {
		return StoreDealOffer{}, err
	}
	if !ok {
		return StoreDealOffer{}, ErrNotFound
	}
	off, err := s.GetStoreDealOffer(offerID, tenantID)
	if err != nil {
		return StoreDealOffer{}, err
	}
	if strings.TrimSpace(off.AuthorizationRef) == "" {
		return StoreDealOffer{}, ErrDealAuthorizationRequired
	}
	return off, nil
}

const voucherAttemptCols = `id,tenant_id,store_id,offer_id,platform,coupon_ref,state,detail,actor,created_at,updated_at`

func scanVoucherAttempt(sc interface{ Scan(...any) error }) (ExternalVoucherAttempt, error) {
	var a ExternalVoucherAttempt
	var couponRef, detail, actor sql.NullString
	err := sc.Scan(&a.ID, &a.TenantID, &a.StoreID, &a.OfferID, &a.Platform, &couponRef, &a.State,
		&detail, &actor, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return ExternalVoucherAttempt{}, err
	}
	a.AssetRef, a.Detail, a.Actor = couponRef.String, detail.String, actor.String
	return a, nil
}

// insertVoucherAttempt persists one attempt row (redeemed|failed|unknown).
func (s *Store) insertVoucherAttempt(off StoreDealOffer, in VoucherAttemptInput, state, detail, nowTS string) (ExternalVoucherAttempt, error) {
	a := ExternalVoucherAttempt{
		ID: newID("evat_"), TenantID: in.TenantID, StoreID: in.StoreID, OfferID: in.OfferID,
		Platform: off.Platform, AssetRef: strings.TrimSpace(in.CouponRef), State: state,
		Detail: strings.TrimSpace(detail), Actor: in.Actor, CreatedAt: nowTS, UpdatedAt: nowTS,
	}
	if _, err := s.DB.Exec(
		`INSERT INTO external_voucher_attempts(`+voucherAttemptCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.TenantID, a.StoreID, a.OfferID, a.Platform, a.AssetRef, a.State, a.Detail, a.Actor, a.CreatedAt, a.UpdatedAt); err != nil {
		return ExternalVoucherAttempt{}, err
	}
	return a, nil
}

// upgradeVoucherAttempt upgrades an unknown row to its confirmed terminal
// state (Lookup 已确认才升级；仍未知保持待确认原样).
func (s *Store) upgradeVoucherAttempt(prev ExternalVoucherAttempt, res deals.RedeemResult, nowTS string) (ExternalVoucherAttempt, error) {
	state := deals.RedeemUnknown
	switch res.State {
	case deals.RedeemRedeemed:
		state = deals.RedeemRedeemed
	case deals.RedeemFailed:
		state = deals.RedeemFailed
	}
	if state == deals.RedeemUnknown {
		return prev, nil
	}
	if _, err := s.DB.Exec(
		`UPDATE external_voucher_attempts SET state=?, detail=?, updated_at=? WHERE id=? AND tenant_id=?`,
		state, strings.TrimSpace(res.Detail), nowTS, prev.ID, prev.TenantID); err != nil {
		return ExternalVoucherAttempt{}, err
	}
	return s.getVoucherAttemptTenant(prev.TenantID, prev.ID)
}

// GetVoucherAttempt loads one attempt strictly within its tenant+store
// (跨租户/跨店 attempt ID 读写拒绝，404 同形掩码).
func (s *Store) GetVoucherAttempt(tenantID, storeID, attemptID string) (ExternalVoucherAttempt, error) {
	row := s.DB.QueryRow(
		`SELECT `+voucherAttemptCols+` FROM external_voucher_attempts WHERE id=? AND tenant_id=? AND store_id=?`,
		attemptID, tenantID, storeID)
	a, err := scanVoucherAttempt(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ExternalVoucherAttempt{}, ErrNotFound
	}
	return a, err
}

// getVoucherAttemptTenant loads one attempt within the tenant regardless of
// the submitting store: the same platform coupon may be re-checked by staff at
// another applicable branch (券是商家级事实，不是提交门店私产).
func (s *Store) getVoucherAttemptTenant(tenantID, attemptID string) (ExternalVoucherAttempt, error) {
	row := s.DB.QueryRow(
		`SELECT `+voucherAttemptCols+` FROM external_voucher_attempts WHERE id=? AND tenant_id=?`,
		attemptID, tenantID)
	a, err := scanVoucherAttempt(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ExternalVoucherAttempt{}, ErrNotFound
	}
	return a, err
}

// RedeemExternalVoucher runs the honest external redemption discipline
// (mirrors HUI-1671 GrantExternalCoupon; 票面硬约束：核销超时先查同一请求，
// 不重发核销):
//   - offer missing / not applicable → ErrNotFound (404 mask);
//   - no authorization ref → ErrDealAuthorizationRequired, zero rows;
//   - an existing redeemed row is terminal: read back, adapter NOT called;
//   - an existing unknown row → Lookup with the SAME request first; only a
//     confirming answer upgrades it, and the redeem closure is NOT re-run;
//   - fresh request: redeem exactly once;
//     redeemed → one redeemed row (unique per coupon);
//     failed → one failed row (audit trail, retry allowed);
//     unknown/transport error → one unknown row persisted (待确认);
//   - the local reward ledger is never touched (本地券不能核销外部券，反之亦然).
//
// replayed=true reports a terminal read-back (already redeemed); upgraded=true
// reports that a previously unknown attempt was just confirmed by Lookup.
func (s *Store) RedeemExternalVoucher(in VoucherAttemptInput, adapter deals.Adapter, nowTS string) (ExternalVoucherAttempt, bool, bool, error) {
	if strings.TrimSpace(in.TenantID) == "" || strings.TrimSpace(in.StoreID) == "" ||
		strings.TrimSpace(in.OfferID) == "" || strings.TrimSpace(in.CouponRef) == "" ||
		strings.TrimSpace(in.Actor) == "" {
		return ExternalVoucherAttempt{}, false, false, fmt.Errorf("%w: missing fields", ErrDealInvalid)
	}
	off, err := s.AuthorizeStoreCouponTarget(in.TenantID, in.StoreID, in.OfferID)
	if err != nil {
		return ExternalVoucherAttempt{}, false, false, err
	}
	req := deals.RedeemRequest{Platform: off.Platform, OfferRef: off.ProviderOfferID, CouponRef: strings.TrimSpace(in.CouponRef)}

	// Terminal read-back: the platform already confirmed this coupon once.
	var prevID string
	err = s.DB.QueryRow(
		`SELECT id FROM external_voucher_attempts
		 WHERE tenant_id=? AND offer_id=? AND coupon_ref=? AND state='redeemed'`,
		in.TenantID, in.OfferID, req.CouponRef).Scan(&prevID)
	if err == nil {
		a, gerr := s.getVoucherAttemptTenant(in.TenantID, prevID)
		return a, true, false, gerr
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ExternalVoucherAttempt{}, false, false, err
	}

	// Unresolved attempt for the same coupon: Lookup ONLY (不重发核销).
	var unknownID string
	err = s.DB.QueryRow(
		`SELECT id FROM external_voucher_attempts
		 WHERE tenant_id=? AND offer_id=? AND coupon_ref=? AND state='unknown'`,
		in.TenantID, in.OfferID, req.CouponRef).Scan(&unknownID)
	if err == nil {
		prev, gerr := s.getVoucherAttemptTenant(in.TenantID, unknownID)
		if gerr != nil {
			return ExternalVoucherAttempt{}, false, false, gerr
		}
		res, lerr := adapter.Lookup(req)
		if lerr != nil {
			// 回查本身失败仍是未知：保持待确认，不升级也不重发。
			return prev, false, false, nil
		}
		updated, uerr := s.upgradeVoucherAttempt(prev, res, nowTS)
		return updated, false, updated.State != deals.RedeemUnknown, uerr
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ExternalVoucherAttempt{}, false, false, err
	}

	// Fresh request: redeem exactly once, persist the honest outcome.
	res, rerr := adapter.Redeem(req)
	if rerr != nil {
		// 传输失败=结果未知：落 unknown 行（待确认），绝不冒充核销。
		a, ierr := s.insertVoucherAttempt(off, in, deals.RedeemUnknown, "platform_result_unknown", nowTS)
		return a, false, false, ierr
	}
	switch res.State {
	case deals.RedeemRedeemed:
		a, ierr := s.insertVoucherAttempt(off, in, deals.RedeemRedeemed, res.Detail, nowTS)
		return a, false, false, ierr
	case deals.RedeemFailed:
		a, ierr := s.insertVoucherAttempt(off, in, deals.RedeemFailed, res.Detail, nowTS)
		return a, false, false, ierr
	default:
		a, ierr := s.insertVoucherAttempt(off, in, deals.RedeemUnknown, res.Detail, nowTS)
		return a, false, false, ierr
	}
}

// RequeryVoucherAttempt re-queries one unknown attempt once via Lookup
// (轮询回查；晚到的平台答案确认后升级，仍未知保持待确认).
func (s *Store) RequeryVoucherAttempt(tenantID, storeID, attemptID string, adapter deals.Adapter, nowTS string) (ExternalVoucherAttempt, error) {
	a, err := s.GetVoucherAttempt(tenantID, storeID, attemptID)
	if err != nil {
		return ExternalVoucherAttempt{}, err
	}
	if a.State != deals.RedeemUnknown {
		return a, nil
	}
	off, err := s.AuthorizeStoreCouponTarget(tenantID, storeID, a.OfferID)
	if err != nil {
		return ExternalVoucherAttempt{}, err
	}
	res, lerr := adapter.Lookup(deals.RedeemRequest{
		Platform: off.Platform, OfferRef: off.ProviderOfferID, CouponRef: a.AssetRef,
	})
	if lerr != nil {
		return a, nil
	}
	return s.upgradeVoucherAttempt(a, res, nowTS)
}

// RefreshStoreDealOffer re-pulls an offer's official price via the adapter's
// official capability and stamps the verification time. Without an adapter or
// authorization the caller answers an explicit capability status
// (ErrDealNotConnected / ErrDealAuthorizationRequired); a transport failure
// answers ErrDealResultUnknown with nothing changed — a fabricated platform
// price is never written. Availability stays advisory (detail only): the
// merchant decides about pausing, the system does not auto-unlist.
func (s *Store) RefreshStoreDealOffer(tenantID, storeID, offerID string, refresh func(offerRef string) (deals.RefreshResult, error), nowTS string) (StoreDealOffer, error) {
	off, err := s.AuthorizeStoreCouponTarget(tenantID, storeID, offerID)
	if err != nil {
		return StoreDealOffer{}, err
	}
	res, err := refresh(off.ProviderOfferID)
	if err != nil {
		return StoreDealOffer{}, ErrDealResultUnknown
	}
	if res.PriceMinor < 0 {
		return StoreDealOffer{}, fmt.Errorf("%w: platform returned a negative price", ErrDealInvalid)
	}
	if _, err := s.UpdateStoreDealOffer(offerID, tenantID, UpdateStoreDealOffer{
		PriceMinor:          &res.PriceMinor,
		PriceSource:         ptrToStr("platform"),
		MarkPriceVerifiedAt: nowTS,
	}); err != nil {
		return StoreDealOffer{}, err
	}
	return s.GetStoreDealOffer(offerID, tenantID)
}

func ptrToStr(v string) *string { return &v }
