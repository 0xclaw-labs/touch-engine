package store

// store_content_packs_test.go: HUI-3553 活动内容包持久层验收。
//
// 覆盖(票面验收映射,持久层部分):
//   - 建包/列包:活动归属(跨活动/跨租户一律 ErrNotFound,绝不泄露存在性);
//   - 版本不可变:内容列零 UPDATE 路径;改价/改文案 = 新版本;源素材更新
//     (同 ref 新 sha 新行)后已采用版本快照不漂移;
//   - 媒体缺失:引用的 lib_assets 行在同租户查无 = media_missing 拒绝;
//   - 回流候选:一律落 candidate 新版本;同幂等键重放返回同一行(幂等);
//     晚到旧稿不动当前采用指针;
//   - 明确采用:blocked / grant_expired 拒绝;换版 = 旧版 retired+superseded
//     事件,指针覆盖;历史可审计(事件只追加);
//   - block:原因必填,落事件,阻止新分发(Distributable);
//   - 弱网重登恢复:关库重开后包/版本/指针/事件完整;
//   - 同一交接重试不新建制作工程:handoff 幂等键重放返回同一行。

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/assetlib"
	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
)

func openPackStore(t *testing.T) *Store {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return New(d)
}

func packFixture(t *testing.T, s *Store) (tenantID, campaignID string, video, cover LibAsset) {
	t.Helper()
	ten, err := s.CreateTenant("内容包商家")
	must(t, err)
	st, err := s.CreateStore(ten.ID, "旗舰店", "地址A", "seed")
	must(t, err)
	cp, err := s.CreateCampaign(NewCampaign{TenantID: ten.ID, StoreID: st.ID, Title: "开业活动", PublicContent: "周末开业", CreatedBy: "seed"})
	must(t, err)
	video = mustCreateAsset(t, s, ten.ID, "up-video-1", strings.Repeat("a", 64), assetlib.MediaTypeVideo)
	cover = mustCreateAsset(t, s, ten.ID, "up-cover-1", strings.Repeat("b", 64), assetlib.MediaTypeImage)
	return ten.ID, cp.ID, video, cover
}

func packSpec(video, cover LibAsset) contentpack.VersionSpec {
	return contentpack.VersionSpec{
		ContentKind: contentpack.ContentKindVideo,
		Video:       contentpack.AssetSnapshot{AssetID: video.ID, AssetRef: video.AssetRef, SHA256: video.SHA256},
		Cover:       contentpack.AssetSnapshot{AssetID: cover.ID, AssetRef: cover.AssetRef, SHA256: cover.SHA256},
		Title:       "周末开业五折", Body: "本店周末全场五折。",
		Topics:    []string{"开业"},
		StoreName: "旗舰店", PriceNote: "全场五折", ValidNote: "2026-10-14 至 2026-10-20",
		PlatformVersions: []contentpack.PlatformVersion{
			{Platform: "douyin", Note: "竖版", SourceVersion: "src-1"},
			{Platform: "shipinhao", Note: "横版", SourceVersion: "src-1"},
		},
		GrantRefs: []string{"grant-2026-001"},
		Marking:   contentpack.Marking{AdRequired: true},
		Source:    contentpack.SourceMerchantUpload,
	}
}

func TestContentPackCRUDAndTenantIsolation(t *testing.T) {
	s := openPackStore(t)
	tenA, campaignA, _, _ := packFixture(t, s)

	pack, err := s.CreateContentPack(tenA, campaignA, "", "开业主题包", "owner-1")
	must(t, err)
	if pack.CampaignID != campaignA || pack.Title != "开业主题包" {
		t.Fatalf("pack fields: %+v", pack)
	}
	if _, err := s.CreateContentPack(tenA, campaignA, "", "开业主题包", "owner-1"); !errors.Is(err, ErrDuplicateContentPack) {
		t.Fatalf("duplicate pack title: %v", err)
	}

	// cross-tenant reads leak nothing
	tenBRec, err := s.CreateTenant("另一家")
	must(t, err)
	if _, err := s.GetContentPack(pack.ID, tenBRec.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant get: %v", err)
	}
	if rows, _ := s.ListContentPacks(tenBRec.ID, campaignA); len(rows) != 0 {
		t.Fatalf("cross-tenant list: %v", rows)
	}

	list, err := s.ListContentPacks(tenA, campaignA)
	must(t, err)
	if len(list) != 1 {
		t.Fatalf("list: %v", list)
	}
}

func TestPackVersionImmutableAndMediaMissing(t *testing.T) {
	s := openPackStore(t)
	ten, campaign, video, cover := packFixture(t, s)
	pack := mustCreatePack(t, s, ten, campaign)

	v1, err := s.CreateContentPackVersion(ten, pack.ID, packSpec(video, cover), contentpack.StatusCandidate, "owner-1")
	must(t, err)
	if v1.VersionNo != 1 || v1.Status != contentpack.StatusCandidate {
		t.Fatalf("v1: %+v", v1)
	}

	// 改价 = 新版本,旧版本价格零变化
	edited := packSpec(video, cover)
	edited.PriceNote = "全场三折"
	v2, err := s.CreateContentPackVersion(ten, pack.ID, edited, contentpack.StatusDraft, "owner-1")
	must(t, err)
	if v2.VersionNo != 2 {
		t.Fatalf("v2 no: %d", v2.VersionNo)
	}
	got1, err := s.GetContentPackVersion(v1.ID, ten)
	must(t, err)
	if got1.PriceNote != "全场五折" || got1.Title != "周末开业五折" {
		t.Fatalf("v1 mutated: %+v", got1)
	}

	// 媒体缺失:引用不存在的 lib asset
	bad := packSpec(video, cover)
	bad.Video = contentpack.AssetSnapshot{AssetID: "las_missing", AssetRef: "up-x", SHA256: strings.Repeat("c", 64)}
	_, err = s.CreateContentPackVersion(ten, pack.ID, bad, contentpack.StatusCandidate, "owner-1")
	ve, ok := err.(*contentpack.ValidationError)
	if !ok || ve.Reason != contentpack.ReasonMediaMissing {
		t.Fatalf("media missing: %v", err)
	}

	// 跨租户素材引用拒绝:素材属于 ten,pack 属于另一租户
	tenOtherRec, err := s.CreateTenant("别家")
	must(t, err)
	otherStore, err := s.CreateStore(tenOtherRec.ID, "别家店", "地址B", "seed")
	must(t, err)
	otherCampaign, err := s.CreateCampaign(NewCampaign{TenantID: tenOtherRec.ID, StoreID: otherStore.ID, Title: "别家活动", CreatedBy: "seed"})
	must(t, err)
	otherPack := mustCreatePack(t, s, tenOtherRec.ID, otherCampaign.ID)
	_, err = s.CreateContentPackVersion(tenOtherRec.ID, otherPack.ID, packSpec(video, cover), contentpack.StatusCandidate, "owner-1")
	if ve, ok := err.(*contentpack.ValidationError); !ok || ve.Reason != contentpack.ReasonMediaMissing {
		t.Fatalf("cross-tenant asset ref must be media_missing: %v", err)
	}
}

func TestReturnCandidateIdempotentAndLateArrival(t *testing.T) {
	s := openPackStore(t)
	ten, campaign, video, cover := packFixture(t, s)
	pack := mustCreatePack(t, s, ten, campaign)

	// 商家先采用 v1
	v1, err := s.CreateContentPackVersion(ten, pack.ID, packSpec(video, cover), contentpack.StatusCandidate, "owner-1")
	must(t, err)
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	_, err = s.AdoptContentPackVersion(ten, pack.ID, v1.ID, "douyin", "owner-1", now)
	must(t, err)

	// 回流候选:同幂等键两次 = 同一行(幂等,不新建)
	ret := packSpec(video, cover)
	ret.Source = contentpack.SourceProductPhotoReturn
	ret.Title = "产品图回流主题"
	ret.IdempotencyKey = "return-1745-0001"
	cand, replayed, err := s.AddReturnCandidate(ten, pack.ID, ret, "product-photo-app")
	must(t, err)
	if replayed || cand.Status != contentpack.StatusCandidate || cand.VersionNo != 2 {
		t.Fatalf("cand: replayed=%v %+v", replayed, cand)
	}
	cand2, replayed2, err := s.AddReturnCandidate(ten, pack.ID, ret, "product-photo-app")
	must(t, err)
	if !replayed2 || cand2.ID != cand.ID {
		t.Fatalf("replay must return same row: replayed=%v %+v", replayed2, cand2)
	}
	if n, _ := s.ListContentPackVersions(ten, pack.ID); len(n) != 2 {
		t.Fatalf("duplicate return created rows: %d", len(n))
	}

	// 回流绝不覆盖当前采用指针(晚到旧稿同样)
	adopted, err := s.GetAdoptedVersion(ten, campaign, "douyin")
	must(t, err)
	if adopted.ID != v1.ID || adopted.Status != contentpack.StatusAdopted {
		t.Fatalf("pointer moved by return: %+v", adopted)
	}

	// 回流来源不实(声明为 merchant_upload)= 拒绝
	notRet := packSpec(video, cover)
	notRet.IdempotencyKey = "return-1745-0002"
	if _, _, err := s.AddReturnCandidate(ten, pack.ID, notRet, "x"); err == nil {
		t.Fatal("non-return source must be refused on return path")
	}
	// 回流缺幂等键 = 拒绝
	noKey := packSpec(video, cover)
	noKey.Source = contentpack.SourceProductPhotoReturn
	if _, _, err := s.AddReturnCandidate(ten, pack.ID, noKey, "x"); err == nil {
		t.Fatal("return without idempotency key must be refused")
	}
}

func TestAdoptionLifecycle(t *testing.T) {
	s := openPackStore(t)
	ten, campaign, video, cover := packFixture(t, s)
	pack := mustCreatePack(t, s, ten, campaign)

	v1, err := s.CreateContentPackVersion(ten, pack.ID, packSpec(video, cover), contentpack.StatusCandidate, "owner-1")
	must(t, err)
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

	// blocked / grant_expired 拒绝采用
	vBlocked, err := s.CreateContentPackVersion(ten, pack.ID, packSpec(video, cover), contentpack.StatusCandidate, "owner-1")
	must(t, err)
	_, err = s.BlockContentPackVersion(ten, pack.ID, vBlocked.ID, "素材授权被平台撤销", "owner-1", now)
	must(t, err)
	if _, err := s.AdoptContentPackVersion(ten, pack.ID, vBlocked.ID, "douyin", "owner-1", now); err == nil {
		t.Fatal("blocked must not be adoptable")
	}
	expired := packSpec(video, cover)
	expired.GrantUntil = "2026-10-01T00:00:00Z"
	vExpired, err := s.CreateContentPackVersion(ten, pack.ID, expired, contentpack.StatusCandidate, "owner-1")
	must(t, err)
	if _, err := s.AdoptContentPackVersion(ten, pack.ID, vExpired.ID, "douyin", "owner-1", now); err == nil {
		t.Fatal("expired grant must not be adoptable")
	}

	// 明确采用 → 指针;换版 → 旧版 retired + superseded 事件
	v2, err := s.CreateContentPackVersion(ten, pack.ID, func() contentpack.VersionSpec {
		spec := packSpec(video, cover)
		spec.Title = "第二版文案"
		return spec
	}(), contentpack.StatusCandidate, "owner-1")
	must(t, err)

	ad1, err := s.AdoptContentPackVersion(ten, pack.ID, v1.ID, "douyin", "owner-1", now)
	must(t, err)
	if ad1.VersionID != v1.ID || ad1.Platform != "douyin" {
		t.Fatalf("ad1: %+v", ad1)
	}
	got, err := s.GetAdoptedVersion(ten, campaign, "douyin")
	must(t, err)
	if got.ID != v1.ID {
		t.Fatalf("adopted: %+v", got)
	}

	// 源素材更新(同 ref 新 sha 新行)不漂移已采用版本快照
	newSha := mustCreateAsset(t, s, ten, "up-video-1", strings.Repeat("d", 64), assetlib.MediaTypeVideo)
	if newSha.SHA256 == video.SHA256 {
		t.Fatal("fixture: new sha expected")
	}
	got, err = s.GetAdoptedVersion(ten, campaign, "douyin")
	must(t, err)
	if got.Video.SHA256 != video.SHA256 || got.Video.AssetID != video.ID {
		t.Fatalf("adopted snapshot drifted: %+v", got.Video)
	}

	ad2, err := s.AdoptContentPackVersion(ten, pack.ID, v2.ID, "douyin", "owner-1", now)
	must(t, err)
	if ad2.VersionID != v2.ID {
		t.Fatalf("ad2: %+v", ad2)
	}
	old1, err := s.GetContentPackVersion(v1.ID, ten)
	must(t, err)
	if old1.Status != contentpack.StatusRetired {
		t.Fatalf("v1 must be retired after supersede: %+v", old1)
	}
	events, err := s.ListContentPackVersionEvents(ten, v1.ID)
	must(t, err)
	kinds := []string{}
	for _, e := range events {
		kinds = append(kinds, e.Kind)
	}
	if len(kinds) < 2 || kinds[len(kinds)-1] != "superseded" {
		t.Fatalf("v1 events: %v", kinds)
	}

	// 跨租户指针读 = ErrNotFound
	tenBRec, err := s.CreateTenant("别家2")
	must(t, err)
	if _, err := s.GetAdoptedVersion(tenBRec.ID, campaign, "douyin"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant adopted: %v", err)
	}

	// block 已采用版:指针保留(旧参与保留原版本),但新分发被拒
	if _, err := s.BlockContentPackVersion(ten, pack.ID, v2.ID, "内容与事实不符", "owner-1", now); err != nil {
		t.Fatalf("block adopted: %v", err)
	}
	blocked, err := s.GetAdoptedVersion(ten, campaign, "douyin")
	must(t, err)
	if blocked.ID != v2.ID {
		t.Fatalf("pointer must be preserved on block: %+v", blocked)
	}
	if err := contentpack.Distributable(blocked.Status, blocked.GrantUntil, now); err == nil {
		t.Fatal("blocked adopted version must refuse new distribution")
	}
}

func TestContentHandoffIdempotent(t *testing.T) {
	s := openPackStore(t)
	ten, campaign, _, _ := packFixture(t, s)

	in := HandoffInput{
		Purpose: "开业宣传", StoreBrief: `{"store_name":"旗舰店"}`,
		DesiredForm: "15s 竖版视频", AssetIDs: []string{"las_1"},
		IdempotencyKey: "handoff-0001",
	}
	h1, replayed, err := s.CreateContentHandoff(ten, campaign, in, "owner-1")
	must(t, err)
	if replayed {
		t.Fatal("first handoff must not be a replay")
	}
	h2, replayed2, err := s.CreateContentHandoff(ten, campaign, in, "owner-1")
	must(t, err)
	if !replayed2 || h2.ID != h1.ID {
		t.Fatalf("handoff replay: replayed=%v %+v vs %+v", replayed2, h2, h1)
	}
	if n, err := s.ListContentHandoffs(ten, campaign); err != nil || len(n) != 1 {
		t.Fatalf("handoffs: %v %d", err, len(n))
	}
	// 缺幂等键拒绝(制作工程去重必须可键控)
	in.IdempotencyKey = ""
	if _, _, err := s.CreateContentHandoff(ten, campaign, in, "owner-1"); err == nil {
		t.Fatal("handoff without idempotency key must be refused")
	}
	// 跨租户活动归属拒绝
	tenBRec, err := s.CreateTenant("别家3")
	must(t, err)
	in.IdempotencyKey = "handoff-0002"
	if _, _, err := s.CreateContentHandoff(tenBRec.ID, campaign, in, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant handoff: %v", err)
	}
}

func TestPackStoreReopenRecovery(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "touch.db")
	d0, err := db.Open(dir)
	must(t, err)
	s := New(d0)
	ten, campaign, video, cover := packFixture(t, s)
	pack := mustCreatePack(t, s, ten, campaign)
	v1, err := s.CreateContentPackVersion(ten, pack.ID, packSpec(video, cover), contentpack.StatusCandidate, "owner-1")
	must(t, err)
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	if _, err := s.AdoptContentPackVersion(ten, pack.ID, v1.ID, "douyin", "owner-1", now); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	// 重登(关库重开)恢复
	d0.Close()
	d, err := db.Open(dir)
	must(t, err)
	defer d.Close()
	s2 := New(d)
	got, err := s2.GetAdoptedVersion(ten, campaign, "douyin")
	must(t, err)
	if got.ID != v1.ID {
		t.Fatalf("reopened adopted: %+v", got)
	}
	packs, err := s2.ListContentPacks(ten, campaign)
	must(t, err)
	if len(packs) != 1 {
		t.Fatalf("reopened packs: %v", packs)
	}
}

func mustCreatePack(t *testing.T, s *Store, tenantID, campaignID string) ContentPack {
	t.Helper()
	p, err := s.CreateContentPack(tenantID, campaignID, "", "主题包-"+newID("t"), "owner-1")
	if err != nil {
		t.Fatalf("create pack: %v", err)
	}
	return p
}
