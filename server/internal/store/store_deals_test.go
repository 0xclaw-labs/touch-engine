package store

import (
	"errors"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/deals"
)

// HUI-4590 门店团购套餐 store 层测试：三平台目录 CRUD、适用门店、公共投影
// （fail-closed + 价格核对态）、点击归因聚合、外部券核销边界（复用 1671 纪律：
// 缺授权不产事实、redeemed 终态回读、failed 留痕可重试、未知零行落库；
// 本地奖励账零写入）。

func seedDealsWorld(t *testing.T) (*Store, string, string, string, string, string) {
	t.Helper()
	s := openStore(t)
	tenA, err := s.CreateTenant("团购商家A")
	must(t, err)
	tenB, err := s.CreateTenant("团购商家B")
	must(t, err)
	stoA, err := s.CreateStore(tenA.ID, "A一店", "A址", "seed")
	must(t, err)
	stoB, err := s.CreateStore(tenA.ID, "A二店", "B址", "seed")
	must(t, err)
	stoC, err := s.CreateStore(tenB.ID, "B一店", "C址", "seed")
	must(t, err)
	return s, tenA.ID, stoA.ID, stoB.ID, tenB.ID, stoC.ID
}

func dealInput(storeIDs ...string) NewStoreDealOffer {
	return NewStoreDealOffer{
		Platform:        deals.PlatformMeituan,
		ProviderOfferID: "mt-1001",
		ProductURL:      "https://www.meituan.com/deal/1001",
		Title:           "双人套餐",
		PriceMinor:      8800,
		Currency:        "FEN",
		PriceSource:     "platform",
		CreatedBy:       "seed",
		StoreIDs:        storeIDs,
	}
}

func TestCreateStoreDealOfferValidations(t *testing.T) {
	s, tenA, stoA, _, _, stoC := seedDealsWorld(t)

	in := dealInput()
	off, err := s.CreateStoreDealOffer(tenA, stoA, in)
	must(t, err)
	if off.ID == "" || off.Status != deals.StatusActive || off.PriceVerifiedAt != "" {
		t.Fatalf("created offer = %+v", off)
	}
	if len(off.StoreIDs) != 1 || off.StoreIDs[0] != stoA {
		t.Fatalf("applicability default must be the creator store: %v", off.StoreIDs)
	}

	// 未知平台/跨平台 URL/http URL/内网 URL/空标题/负价/空 URL 全拒。
	bad := []NewStoreDealOffer{
		mutate(dealInput(), func(o *NewStoreDealOffer) { o.Platform = deals.PlatformDianping }),
		mutate(dealInput(), func(o *NewStoreDealOffer) { o.ProductURL = "http://www.meituan.com/deal/1" }),
		mutate(dealInput(), func(o *NewStoreDealOffer) { o.ProductURL = "https://127.0.0.1/deal/1" }),
		mutate(dealInput(), func(o *NewStoreDealOffer) { o.Title = "" }),
		mutate(dealInput(), func(o *NewStoreDealOffer) { o.PriceMinor = -1 }),
		mutate(dealInput(), func(o *NewStoreDealOffer) { o.ProductURL = "" }),
		mutate(dealInput(), func(o *NewStoreDealOffer) { o.CreatedBy = "" }),
	}
	for i, b := range bad {
		if _, err := s.CreateStoreDealOffer(tenA, stoA, b); !errors.Is(err, ErrDealInvalid) {
			t.Fatalf("bad input %d must refuse ErrDealInvalid, got %v", i, err)
		}
	}

	// 适用门店越租户：拒 ErrNotFound（不泄露他租户门店存在性）。
	if _, err := s.CreateStoreDealOffer(tenA, stoA, dealInput(stoC)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant store applicability must 404-mask: %v", err)
	}

	// 同租户同平台同平台商品 ID 唯一：冲突 ErrDealConflict。
	if _, err := s.CreateStoreDealOffer(tenA, stoA, dealInput()); !errors.Is(err, ErrDealConflict) {
		t.Fatalf("duplicate provider_offer_id must conflict: %v", err)
	}
	// 同平台商品 ID、不同平台：允许。
	ok := dealInput()
	ok.Platform, ok.ProductURL = deals.PlatformDianping, "https://www.dianping.com/shop/1001"
	if _, err := s.CreateStoreDealOffer(tenA, stoA, ok); err != nil {
		t.Fatalf("same id under another platform must be allowed: %v", err)
	}
}

func mutate(in NewStoreDealOffer, f func(*NewStoreDealOffer)) NewStoreDealOffer {
	f(&in)
	return in
}

func TestListStoreDealOffersByApplicability(t *testing.T) {
	s, tenA, stoA, stoB, _, _ := seedDealsWorld(t)
	must(t, func() error { _, err := s.CreateStoreDealOffer(tenA, stoA, dealInput(stoB)); return err }())
	onlyA := dealInput()
	onlyA.ProviderOfferID, onlyA.Title = "mt-2002", "仅一店"
	if _, err := s.CreateStoreDealOffer(tenA, stoA, onlyA); err != nil {
		t.Fatal(err)
	}

	forA, err := s.ListStoreDealOffers(tenA, stoA)
	must(t, err)
	if len(forA) != 2 {
		t.Fatalf("store A must see 2 offers: %d", len(forA))
	}
	forB, err := s.ListStoreDealOffers(tenA, stoB)
	must(t, err)
	if len(forB) != 1 || forB[0].ProviderOfferID != "mt-1001" {
		t.Fatalf("store B must see only the shared offer: %+v", forB)
	}
	// 跨租户读取恒空（不泄露）。
	if got, _ := s.ListStoreDealOffers("tnt_other", stoA); len(got) != 0 {
		t.Fatalf("foreign tenant must see nothing: %v", got)
	}
}

func TestUpdateStoreDealOfferAndStatus(t *testing.T) {
	s, tenA, stoA, _, _, _ := seedDealsWorld(t)
	off, err := s.CreateStoreDealOffer(tenA, stoA, dealInput())
	must(t, err)

	newURL := "https://m.meituan.com/deal/1001?x=1"
	got, err := s.UpdateStoreDealOffer(off.ID, tenA, UpdateStoreDealOffer{
		Title:               ptrStr("双人套餐（升级）"),
		ProductURL:          ptrStr(newURL),
		PriceMinor:          ptrI64(9900),
		MarkPriceVerifiedAt: "2026-11-10T08:00:00Z",
		StoreIDs:            &[]string{stoA},
	})
	must(t, err)
	if got.Title != "双人套餐（升级）" || got.ProductURL != newURL || got.PriceMinor != 9900 ||
		got.PriceVerifiedAt != "2026-11-10T08:00:00Z" {
		t.Fatalf("update = %+v", got)
	}

	// 更新侧同样守住 URL 门与价格约束。
	if _, err := s.UpdateStoreDealOffer(off.ID, tenA, UpdateStoreDealOffer{
		ProductURL: ptrStr("https://www.douyin.com/x"),
	}); !errors.Is(err, ErrDealInvalid) {
		t.Fatalf("cross-platform URL update must refuse: %v", err)
	}
	if _, err := s.UpdateStoreDealOffer(off.ID, tenA, UpdateStoreDealOffer{
		PriceMinor: ptrI64(-5),
	}); !errors.Is(err, ErrDealInvalid) {
		t.Fatalf("negative price update must refuse: %v", err)
	}

	// 上下架：pause→active；retired 不可逆。
	if _, err := s.SetStoreDealOfferStatus(off.ID, tenA, deals.StatusPaused); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetStoreDealOfferStatus(off.ID, tenA, deals.StatusActive); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetStoreDealOfferStatus(off.ID, tenA, deals.StatusRetired); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetStoreDealOfferStatus(off.ID, tenA, deals.StatusActive); !errors.Is(err, ErrDealInvalid) {
		t.Fatalf("retired must be irreversible: %v", err)
	}
	// 未知状态/未知行。
	if _, err := s.SetStoreDealOfferStatus(off.ID, tenA, "archived"); !errors.Is(err, ErrDealInvalid) {
		t.Fatalf("unknown status must refuse: %v", err)
	}
	if _, err := s.SetStoreDealOfferStatus("sdoff_missing", tenA, deals.StatusActive); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing offer must 404: %v", err)
	}
}

func ptrStr(v string) *string { return &v }
func ptrI64(v int64) *int64   { return &v }

func TestPublicStoreOffersProjection(t *testing.T) {
	s, tenA, stoA, _, _, _ := seedDealsWorld(t)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

	fresh := dealInput() // 未核实价 → platform
	freshOff, err := s.CreateStoreDealOffer(tenA, stoA, fresh)
	must(t, err)

	// 核对过的新鲜价 → show。
	verifiedOff, err := s.UpdateStoreDealOffer(freshOff.ID, tenA, UpdateStoreDealOffer{
		MarkPriceVerifiedAt: now.Add(-time.Hour).Format(time.RFC3339),
	})
	must(t, err)
	_ = verifiedOff

	// 停用的套餐：公共面整行不出现。
	paused := dealInput()
	paused.ProviderOfferID, paused.Title = "mt-3003", "已下架套餐"
	pausedOff, err := s.CreateStoreDealOffer(tenA, stoA, paused)
	must(t, err)
	if _, err := s.SetStoreDealOfferStatus(pausedOff.ID, tenA, deals.StatusPaused); err != nil {
		t.Fatal(err)
	}

	// 有效窗口未开始：不出现。
	scheduled := dealInput()
	scheduled.ProviderOfferID, scheduled.Title = "mt-4004", "未开始"
	scheduled.ValidFrom = now.Add(time.Hour).Format(time.RFC3339)
	if _, err := s.CreateStoreDealOffer(tenA, stoA, scheduled); err != nil {
		t.Fatal(err)
	}

	// 读出侧 URL 复验：绕过写面塞入坏 URL 的行不出现。
	var offID string
	must(t, s.DB.QueryRow(`SELECT id FROM store_deal_offers WHERE provider_offer_id='mt-1001' AND tenant_id=?`, tenA).Scan(&offID))
	if _, err := s.DB.Exec(`UPDATE store_deal_offers SET product_url='http://10.0.0.9/deal' WHERE id=?`, offID); err != nil {
		t.Fatal(err)
	}

	pub, err := s.ListPublicStoreOffers(stoA, now)
	must(t, err)
	if len(pub) != 0 {
		t.Fatalf("fail-closed projection must hide bad-url/stale-state rows, got %+v", pub)
	}

	// 恢复 URL 后：只出现 active+窗口内的那一条，价格 show；paused/未开始不出现。
	if _, err := s.DB.Exec(`UPDATE store_deal_offers SET product_url='https://www.meituan.com/deal/1001' WHERE id=?`, offID); err != nil {
		t.Fatal(err)
	}
	pub, err = s.ListPublicStoreOffers(stoA, now)
	must(t, err)
	if len(pub) != 1 || pub[0].ID != offID {
		t.Fatalf("public projection = %+v, want only %s", pub, offID)
	}
	if pub[0].PriceDisplay != deals.PriceShow || pub[0].PriceMinor != 8800 {
		t.Fatalf("verified price must show: %+v", pub[0])
	}

	// 校验时间过期（>=72h）→ 价格回落「到平台查看」，行仍在（替代可见）。
	must(t, func() error {
		_, err := s.DB.Exec(`UPDATE store_deal_offers SET price_verified_at=? WHERE id=?`,
			now.Add(-72*time.Hour).Format(time.RFC3339), offID)
		return err
	}())
	pub, err = s.ListPublicStoreOffers(stoA, now)
	must(t, err)
	if len(pub) != 1 || pub[0].PriceDisplay != deals.PricePlatform {
		t.Fatalf("stale verification must answer 到平台查看: %+v", pub)
	}

	// 其他门店不适用 → 空。
	if got, _ := s.ListPublicStoreOffers("sto_other", now); len(got) != 0 {
		t.Fatalf("non-applicable store must see nothing: %v", got)
	}
}

func TestStoreOfferClickStats(t *testing.T) {
	s, tenA, stoA, _, _, _ := seedDealsWorld(t)
	off, err := s.CreateStoreDealOffer(tenA, stoA, dealInput())
	must(t, err)

	must(t, s.IncrementStoreOfferClick(tenA, stoA, off.ID, "2026-11-10"))
	must(t, s.IncrementStoreOfferClick(tenA, stoA, off.ID, "2026-11-10"))
	must(t, s.IncrementStoreOfferClick(tenA, stoA, off.ID, "2026-11-11"))

	perOffer, byPlatform, err := s.StoreOfferClickTotals(tenA, stoA)
	must(t, err)
	if perOffer[off.ID] != 3 {
		t.Fatalf("click total = %d, want 3", perOffer[off.ID])
	}
	if byPlatform[deals.PlatformMeituan] != 3 {
		t.Fatalf("platform clicks = %d, want 3", byPlatform[deals.PlatformMeituan])
	}
	// 其他门店独立（归因按门店+渠道）。
	perA2, _, err := s.StoreOfferClickTotals(tenA, "sto_none")
	must(t, err)
	if len(perA2) != 0 {
		t.Fatalf("other store must have no clicks: %v", perA2)
	}
}

// fakeCouponAdapter counts calls: replay must NOT re-call the adapter.
type fakeCouponAdapter struct {
	calls    int
	next     deals.RedeemResult
	nextErr  error
	checkRes deals.CheckResult
	checkErr error
}

func (f *fakeCouponAdapter) Check(deals.CheckRequest) (deals.CheckResult, error) {
	return f.checkRes, f.checkErr
}

func (f *fakeCouponAdapter) Redeem(deals.RedeemRequest) (deals.RedeemResult, error) {
	f.calls++
	return f.next, f.nextErr
}

func authorizedOffer(t *testing.T, s *Store, tenA, stoA string) StoreDealOffer {
	t.Helper()
	in := dealInput()
	in.AuthorizationRef = "auth-meituan-2026"
	in.ProviderOfferID = "mt-auth-1"
	off, err := s.CreateStoreDealOffer(tenA, stoA, in)
	must(t, err)
	return off
}

func TestRedeemStoreDealCouponDiscipline(t *testing.T) {
	s, tenA, stoA, _, _, _ := seedDealsWorld(t)
	off := authorizedOffer(t, s, tenA, stoA)

	// 1) 无授权引用 → 拒，零行落库。
	unauth := dealInput()
	unauth.ProviderOfferID = "mt-noauth"
	unauthOff, err := s.CreateStoreDealOffer(tenA, stoA, unauth)
	must(t, err)
	ad := &fakeCouponAdapter{}
	if _, _, err := s.RedeemStoreDealCoupon(StoreCouponRedeemInput{
		TenantID: tenA, StoreID: stoA, OfferID: unauthOff.ID, CouponRef: "MT-1", Actor: "staff",
	}, ad.Redeem, "2026-11-10T09:00:00Z"); !errors.Is(err, ErrDealAuthorizationRequired) {
		t.Fatalf("unauthorized offer must refuse: %v", err)
	}

	// 2) 授权在 + 平台确认 → redeemed 事实行。
	ad.next = deals.RedeemResult{State: deals.RedeemRedeemed, Detail: "ok"}
	f1, replayed, err := s.RedeemStoreDealCoupon(StoreCouponRedeemInput{
		TenantID: tenA, StoreID: stoA, OfferID: off.ID, CouponRef: "MT-9", Actor: "staff",
	}, ad.Redeem, "2026-11-10T09:00:00Z")
	must(t, err)
	if replayed || f1.State != deals.RedeemRedeemed || ad.calls != 1 {
		t.Fatalf("first redeem = %+v replayed=%v calls=%d", f1, replayed, ad.calls)
	}

	// 3) 同券重放 → 终态回读，不再调适配器。
	f2, replayed, err := s.RedeemStoreDealCoupon(StoreCouponRedeemInput{
		TenantID: tenA, StoreID: stoA, OfferID: off.ID, CouponRef: "MT-9", Actor: "staff2",
	}, ad.Redeem, "2026-11-10T09:01:00Z")
	must(t, err)
	if !replayed || f2.ID != f1.ID || ad.calls != 1 {
		t.Fatalf("replay must read back without adapter call: %+v replayed=%v calls=%d", f2, replayed, ad.calls)
	}

	// 4) 平台 failed → failed 行留痕，可重试；重试成功出新 redeemed 行。
	ad.next = deals.RedeemResult{State: deals.RedeemFailed, Detail: "已被核销过"}
	if _, _, err := s.RedeemStoreDealCoupon(StoreCouponRedeemInput{
		TenantID: tenA, StoreID: stoA, OfferID: off.ID, CouponRef: "MT-10", Actor: "staff",
	}, ad.Redeem, "2026-11-10T09:02:00Z"); err != nil {
		t.Fatalf("failed platform answer must not error: %v", err)
	}
	ad.next = deals.RedeemResult{State: deals.RedeemRedeemed}
	f3, _, err := s.RedeemStoreDealCoupon(StoreCouponRedeemInput{
		TenantID: tenA, StoreID: stoA, OfferID: off.ID, CouponRef: "MT-10", Actor: "staff",
	}, ad.Redeem, "2026-11-10T09:03:00Z")
	must(t, err)
	if f3.State != deals.RedeemRedeemed {
		t.Fatalf("retry after failed must redeem: %+v", f3)
	}

	// 5) 传输失败/unknown → 诚实未知，零行落库（该券）。
	ad.next, ad.nextErr = deals.RedeemResult{State: deals.RedeemUnknown}, errors.New("timeout")
	if _, _, err := s.RedeemStoreDealCoupon(StoreCouponRedeemInput{
		TenantID: tenA, StoreID: stoA, OfferID: off.ID, CouponRef: "MT-11", Actor: "staff",
	}, ad.Redeem, "2026-11-10T09:04:00Z"); !errors.Is(err, ErrDealResultUnknown) {
		t.Fatalf("unknown must answer ErrDealResultUnknown: %v", err)
	}
	var n int
	must(t, s.DB.QueryRow(`SELECT COUNT(*) FROM store_deal_coupon_redemptions WHERE coupon_ref='MT-11'`).Scan(&n))
	if n != 0 {
		t.Fatalf("unknown must leave zero rows: %d", n)
	}

	// 6) 本店不适用该套餐的门店：404 掩码。
	if _, _, err := s.RedeemStoreDealCoupon(StoreCouponRedeemInput{
		TenantID: tenA, StoreID: "sto_other", OfferID: off.ID, CouponRef: "MT-12", Actor: "staff",
	}, ad.Redeem, "2026-11-10T09:05:00Z"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-applicable store must 404: %v", err)
	}

	// 7) 本地奖励账零写入（外部券核销不碰 reward_benefits）。
	var rb int
	must(t, s.DB.QueryRow(`SELECT COUNT(*) FROM reward_benefits`).Scan(&rb))
	if rb != 0 {
		t.Fatalf("external coupon redemption must never touch the local reward ledger: %d", rb)
	}
	// redeemed 终态唯一：直接插第二行 redeemed 违反部分唯一索引。
	if _, err := s.DB.Exec(`INSERT INTO store_deal_coupon_redemptions(id,tenant_id,store_id,offer_id,platform,coupon_ref,state,actor,created_at)
		VALUES('x2','` + tenA + `','` + stoA + `','` + off.ID + `','meituan','MT-9','redeemed','a','t')`); err == nil {
		t.Fatal("second redeemed row for the same coupon must violate the unique index")
	}
}

func TestCouponTargetAuthorizationHelper(t *testing.T) {
	s, tenA, stoA, _, _, _ := seedDealsWorld(t)
	off := authorizedOffer(t, s, tenA, stoA)
	got, err := s.AuthorizeStoreCouponTarget(tenA, stoA, off.ID)
	must(t, err)
	if got.ID != off.ID || got.AuthorizationRef == "" {
		t.Fatalf("authorize target = %+v", got)
	}
	if _, err := s.AuthorizeStoreCouponTarget(tenA, "sto_other", off.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-applicable store must 404: %v", err)
	}
	// 停用的套餐仍可核销（顾客已在平台购买；下架只停新增曝光）。
	if _, err := s.SetStoreDealOfferStatus(off.ID, tenA, deals.StatusPaused); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthorizeStoreCouponTarget(tenA, stoA, off.ID); err != nil {
		t.Fatalf("paused offer must stay redeemable: %v", err)
	}
}

func TestUpdateDealApplicabilityFullReplacement(t *testing.T) {
	s, tenA, stoA, stoB, _, _ := seedDealsWorld(t)
	off, err := s.CreateStoreDealOffer(tenA, stoA, dealInput(stoB))
	must(t, err)

	// 完整替换：可以把创建店摘下，换成其他本租户门店。
	got, err := s.UpdateStoreDealOffer(off.ID, tenA, UpdateStoreDealOffer{
		StoreIDs: &[]string{stoB},
	})
	must(t, err)
	if len(got.StoreIDs) != 1 || got.StoreIDs[0] != stoB {
		t.Fatalf("replacement = %v, want [stoB]", got.StoreIDs)
	}
	// 原创建店不再适用（换挂成功）。
	forA, err := s.ListStoreDealOffers(tenA, stoA)
	must(t, err)
	if len(forA) != 0 {
		t.Fatalf("store A must no longer see the offer: %v", forA)
	}

	// 空列表 → 拒（至少一家适用门店）。
	if _, err := s.UpdateStoreDealOffer(off.ID, tenA, UpdateStoreDealOffer{
		StoreIDs: &[]string{""},
	}); !errors.Is(err, ErrDealInvalid) {
		t.Fatalf("empty applicability must refuse: %v", err)
	}
	// 未知门店 → 404。
	if _, err := s.UpdateStoreDealOffer(off.ID, tenA, UpdateStoreDealOffer{
		StoreIDs: &[]string{"sto_missing"},
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown store in replacement must 404: %v", err)
	}
}
