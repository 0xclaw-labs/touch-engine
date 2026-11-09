package store

// store_content_packs_store_test.go: HUI-3553 门店根内容包持久层验收。
//
// 覆盖(裁定书+票面映射,持久层部分):
//   - 门店根建包(campaign 可空);同店同名唯一;跨租户一律 ErrNotFound;
//   - 列包查询适配:本店活动包(旧 campaign 域)一并返回并带 legacy 标记;
//   - 版本不可变:媒体快照(media_id/sha/mime/bytes/时长/宽高)创建时冻结,
//     内容列零 UPDATE;版本号单调;
//   - 媒体解析:同租户同店才可用,跨租户/跨店/缺失一律 media_missing;
//     kind 错配拒绝;
//   - 采用权威:(tenant, store, platform) 指针;记录 adopted_by 与时间;
//     blocked / grant_expired 拒绝;换版 = 旧版 retired + superseded 事件,
//     旧版本行内容零变化(已发布引用不受影响);
//   - 唯一候选登记事务(4595 接线点):回流一律落 candidate;同幂等键重放
//     返回同一行零新增;晚到旧稿绝不覆盖当前采用指针;
//   - 采用后建立绑定(BindActionContent):未采用拒;跨店拒;重绑覆盖;
//   - media-access 签发审计落库。

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

func openStorePackDB(t *testing.T) (*Store, func()) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return New(d), func() {}
}

func storePackFixture(t *testing.T, s *Store) (tenantID, storeID, campaignID string) {
	t.Helper()
	ten, err := s.CreateTenant("门店内容商家")
	must(t, err)
	st, err := s.CreateStore(ten.ID, "旗舰一店", "地址一", "seed")
	must(t, err)
	cp, err := s.CreateCampaign(NewCampaign{TenantID: ten.ID, StoreID: st.ID, Title: "门店活动", PublicContent: "内容", CreatedBy: "seed"})
	must(t, err)
	return ten.ID, st.ID, cp.ID
}

func mediaMeta(sha, mime string, kind upload.MediaKind, size int64) upload.MediaMeta {
	return upload.MediaMeta{
		SHA256: sha, MIME: mime, Kind: kind, ByteSize: size,
		DurationMS: 1234, Width: 64, Height: 64, MediaRef: sha,
	}
}

func mustRegisterMedia(t *testing.T, s *Store, tenantID, storeID, sha, mime string, kind upload.MediaKind, createdBy string) ContentMedia {
	t.Helper()
	m, _, err := s.RegisterContentMedia(tenantID, storeID, mediaMeta(sha, mime, kind, 1024), "菜单视频.mp4", createdBy)
	must(t, err)
	return m
}

func storeVideoSpec(video, cover ContentMedia) StoreVersionSpec {
	return StoreVersionSpec{
		ContentKind:  contentpack.ContentKindVideo,
		VideoMediaID: video.ID, CoverMediaID: cover.ID,
		Title: "门店开业视频", Body: "本店开业全场五折。",
		Topics: []string{"开业"}, StoreName: "旗舰一店",
		PriceNote: "五折", ValidNote: "2026-11-10 至 2026-11-30",
		PlatformVersions: []contentpack.PlatformVersion{
			{Platform: "douyin", Note: "竖版", SourceVersion: "src-1"},
		},
		GrantRefs: []string{"grant-001"},
		Marking:   contentpack.Marking{AdRequired: true},
		Source:    contentpack.SourceMerchantUpload,
	}
}

func TestStoreContentPackCRUDAndLegacyAdapter(t *testing.T) {
	s, _ := openStorePackDB(t)
	tenID, storeID, campaignID := storePackFixture(t, s)

	// 门店根建包:campaign 可空。
	pack, err := s.CreateStoreContentPack(tenID, storeID, "", "门店主题包A", "owner-1")
	must(t, err)
	if pack.StoreID != storeID || pack.CampaignID != "" || pack.Legacy {
		t.Fatalf("pack = %+v", pack)
	}
	// 同店同名唯一。
	if _, err := s.CreateStoreContentPack(tenID, storeID, "", "门店主题包A", "owner-1"); !errors.Is(err, ErrDuplicateContentPack) {
		t.Fatalf("duplicate = %v, want ErrDuplicateContentPack", err)
	}
	// 挂活动的门店包:活动必须存在且属于本店。
	if _, err := s.CreateStoreContentPack(tenID, storeID, campaignID, "门店主题包B", "owner-1"); err != nil {
		t.Fatalf("campaign-bound store pack: %v", err)
	}
	otherTen, otherStore, _ := storePackFixture(t, s)
	if _, err := s.CreateStoreContentPack(otherTen, otherStore, campaignID, "跨店挂活动", "owner-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-store campaign = %v, want ErrNotFound", err)
	}
	// 跨租户读取 = ErrNotFound(不泄露存在性)。
	if _, err := s.GetStoreContentPack(pack.ID, otherTen); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant get = %v", err)
	}
	// 查询适配:旧 campaign 域的活动包也出现在本店列表,legacy 标记区分。
	if _, err := s.CreateContentPack(tenID, campaignID, storeID, "旧活动包", "owner-1"); err != nil {
		t.Fatalf("legacy campaign pack: %v", err)
	}
	packs, err := s.ListStoreContentPacks(tenID, storeID)
	must(t, err)
	var sawLegacy, sawNew bool
	for _, p := range packs {
		if p.Title == "旧活动包" && p.Legacy {
			sawLegacy = true
		}
		if p.Title == "门店主题包A" && !p.Legacy {
			sawNew = true
		}
	}
	if !sawLegacy || !sawNew {
		t.Fatalf("list misses packs (legacy=%v new=%v): %+v", sawLegacy, sawNew, packs)
	}
}

func TestStorePackVersionsFreezeMediaSnapshots(t *testing.T) {
	s, _ := openStorePackDB(t)
	tenID, storeID, _ := storePackFixture(t, s)
	pack, err := s.CreateStoreContentPack(tenID, storeID, "", "版本包", "owner-1")
	must(t, err)
	video := mustRegisterMedia(t, s, tenID, storeID, strings.Repeat("1", 64), "video/mp4", upload.KindVideo, "owner-1")
	cover := mustRegisterMedia(t, s, tenID, storeID, strings.Repeat("2", 64), "image/jpeg", upload.KindImage, "owner-1")

	v1, err := s.CreateStoreContentPackVersion(tenID, pack.ID, storeVideoSpec(video, cover), contentpack.StatusCandidate, "owner-1")
	must(t, err)
	if v1.VersionNo != 1 || v1.Status != contentpack.StatusCandidate {
		t.Fatalf("v1 = %+v", v1)
	}
	// 快照冻结:mime/bytes/时长/宽高 来自媒体行实测值。
	if v1.Video.MediaID != video.ID || v1.Video.SHA256 != video.SHA256 || v1.Video.MimeType != "video/mp4" ||
		v1.Video.ByteSize != 1024 || v1.Video.DurationMS != 1234 || v1.Video.Width != 64 || v1.Video.Height != 64 {
		t.Fatalf("video snapshot = %+v", v1.Video)
	}
	// 版本号单调:第二个版本 = 2。
	v2, err := s.CreateStoreContentPackVersion(tenID, pack.ID, storeVideoSpec(video, cover), contentpack.StatusDraft, "owner-1")
	must(t, err)
	if v2.VersionNo != 2 || v2.Status != contentpack.StatusDraft {
		t.Fatalf("v2 = %+v", v2)
	}
	// 幂等键重复 = ErrDuplicatePackVersion(与旧契约一致)。
	spec := storeVideoSpec(video, cover)
	spec.IdempotencyKey = "idem-1"
	if _, err := s.CreateStoreContentPackVersion(tenID, pack.ID, spec, contentpack.StatusCandidate, "owner-1"); err != nil {
		t.Fatalf("idem v: %v", err)
	}
	if _, err := s.CreateStoreContentPackVersion(tenID, pack.ID, spec, contentpack.StatusCandidate, "owner-1"); !errors.Is(err, ErrDuplicatePackVersion) {
		t.Fatalf("idem replay = %v, want ErrDuplicatePackVersion", err)
	}

	// 媒体解析负例:缺失/跨租户/跨店/kind 错配。
	bad := storeVideoSpec(video, cover)
	bad.VideoMediaID = "cmd_missing"
	if _, err := s.CreateStoreContentPackVersion(tenID, pack.ID, bad, contentpack.StatusCandidate, "owner-1"); !isMediaMissing(err) {
		t.Fatalf("missing media = %v, want media_missing", err)
	}
	otherTen, otherStore, _ := storePackFixture(t, s)
	foreign := mustRegisterMedia(t, s, otherTen, otherStore, strings.Repeat("3", 64), "video/mp4", upload.KindVideo, "owner-2")
	bad = storeVideoSpec(video, cover)
	bad.VideoMediaID = foreign.ID
	if _, err := s.CreateStoreContentPackVersion(tenID, pack.ID, bad, contentpack.StatusCandidate, "owner-1"); !isMediaMissing(err) {
		t.Fatalf("cross-tenant media = %v, want media_missing", err)
	}
	bad = storeVideoSpec(video, cover)
	bad.VideoMediaID = cover.ID // 图片充当视频:kind 错配拒绝
	if _, err := s.CreateStoreContentPackVersion(tenID, pack.ID, bad, contentpack.StatusCandidate, "owner-1"); !isMediaMissing(err) {
		t.Fatalf("kind mismatch = %v, want media_missing", err)
	}
}

func isMediaMissing(err error) bool {
	var ve *contentpack.ValidationError
	if errors.As(err, &ve) {
		return ve.Reason == contentpack.ReasonMediaMissing
	}
	return false
}

func TestStoreAdoptionFreezeAndSwitch(t *testing.T) {
	s, _ := openStorePackDB(t)
	tenID, storeID, _ := storePackFixture(t, s)
	pack, err := s.CreateStoreContentPack(tenID, storeID, "", "采用包", "owner-1")
	must(t, err)
	video := mustRegisterMedia(t, s, tenID, storeID, strings.Repeat("4", 64), "video/mp4", upload.KindVideo, "owner-1")
	cover := mustRegisterMedia(t, s, tenID, storeID, strings.Repeat("5", 64), "image/jpeg", upload.KindImage, "owner-1")

	v1, err := s.CreateStoreContentPackVersion(tenID, pack.ID, storeVideoSpec(video, cover), contentpack.StatusCandidate, "owner-1")
	must(t, err)
	v2Spec := storeVideoSpec(video, cover)
	v2Spec.Title = "第二版文案"
	v2, err := s.CreateStoreContentPackVersion(tenID, pack.ID, v2Spec, contentpack.StatusCandidate, "owner-1")
	must(t, err)

	at := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	adopted, err := s.AdoptStoreContentPackVersion(tenID, pack.ID, v1.ID, "douyin", "owner-1", at)
	must(t, err)
	if adopted.VersionID != v1.ID || adopted.AdoptedBy != "owner-1" {
		t.Fatalf("adoption = %+v", adopted)
	}
	cur, err := s.GetStoreAdoptedPackVersion(tenID, pack.ID)
	must(t, err)
	if cur.ID != v1.ID || cur.Status != contentpack.StatusAdopted {
		t.Fatalf("current = %+v", cur)
	}

	// 换版:v2 采用 → v1 retired + superseded 事件,内容行零变化。
	_, err = s.AdoptStoreContentPackVersion(tenID, pack.ID, v2.ID, "douyin", "owner-1", at.Add(time.Minute))
	must(t, err)
	v1After, err := s.GetStoreContentPackVersion(v1.ID, tenID)
	must(t, err)
	if v1After.Status != contentpack.StatusRetired {
		t.Fatalf("v1 status = %s, want retired", v1After.Status)
	}
	if v1After.Title != v1.Title || v1After.Body != v1.Body || v1After.Video.MediaID != v1.Video.MediaID {
		t.Fatal("old version content drifted after adoption switch (版本冻结破坏)")
	}
	events, err := s.ListStoreContentPackVersionEvents(tenID, v1.ID)
	must(t, err)
	var sawSuperseded bool
	for _, e := range events {
		if e.Kind == "superseded" {
			sawSuperseded = true
		}
	}
	if !sawSuperseded {
		t.Fatalf("no superseded event: %+v", events)
	}
	cur, err = s.GetStoreAdoptedPackVersion(tenID, pack.ID)
	must(t, err)
	if cur.ID != v2.ID {
		t.Fatalf("current after switch = %s, want %s", cur.ID, v2.ID)
	}

	// blocked 拒绝采用;grant 过期拒绝采用。
	blocked, err := s.BlockStoreContentPackVersion(tenID, pack.ID, v1.ID, "授权被撤回", "owner-1", at.Add(2*time.Minute))
	must(t, err)
	if blocked.Status != contentpack.StatusBlocked {
		t.Fatalf("block = %+v", blocked)
	}
	expSpec := storeVideoSpec(video, cover)
	expSpec.GrantUntil = "2026-01-01T00:00:00Z"
	expired, err := s.CreateStoreContentPackVersion(tenID, pack.ID, expSpec, contentpack.StatusCandidate, "owner-1")
	must(t, err)
	if _, err := s.AdoptStoreContentPackVersion(tenID, pack.ID, expired.ID, "douyin", "owner-1", at.Add(3*time.Minute)); err == nil {
		t.Fatal("grant-expired version adopted")
	}
	// 跨包版本采用拒绝。
	otherPack, err := s.CreateStoreContentPack(tenID, storeID, "", "另一个包", "owner-1")
	must(t, err)
	if _, err := s.AdoptStoreContentPackVersion(tenID, otherPack.ID, v2.ID, "douyin", "owner-1", at); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-pack adopt = %v, want ErrNotFound", err)
	}
}

func TestStoreReturnCandidateNeverOverwritesAdoption(t *testing.T) {
	s, _ := openStorePackDB(t)
	tenID, storeID, _ := storePackFixture(t, s)
	pack, err := s.CreateStoreContentPack(tenID, storeID, "", "回流包", "owner-1")
	must(t, err)
	video := mustRegisterMedia(t, s, tenID, storeID, strings.Repeat("6", 64), "video/mp4", upload.KindVideo, "owner-1")
	cover := mustRegisterMedia(t, s, tenID, storeID, strings.Repeat("7", 64), "image/jpeg", upload.KindImage, "owner-1")

	spec := storeVideoSpec(video, cover)
	spec.Source = contentpack.SourceProductPhotoReturn
	spec.IdempotencyKey = "prod-job-1"

	// 先采用 v1(商家已明确采用)。
	v1, err := s.CreateStoreContentPackVersion(tenID, pack.ID, storeVideoSpec(video, cover), contentpack.StatusCandidate, "owner-1")
	must(t, err)
	at := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)
	if _, err := s.AdoptStoreContentPackVersion(tenID, pack.ID, v1.ID, "douyin", "owner-1", at); err != nil {
		t.Fatalf("adopt: %v", err)
	}

	// 回流:一律落 candidate 新版本;同键重放零新增;指针不动。
	ret, replayed, err := s.AddStoreReturnCandidate(tenID, pack.ID, spec, "production-service")
	must(t, err)
	if replayed || ret.Status != contentpack.StatusCandidate || ret.ID == v1.ID {
		t.Fatalf("return = %+v replayed=%v", ret, replayed)
	}
	ret2, replayed2, err := s.AddStoreReturnCandidate(tenID, pack.ID, spec, "production-service")
	must(t, err)
	if !replayed2 || ret2.ID != ret.ID {
		t.Fatalf("replay = %+v replayed=%v, want same row replayed", ret2, replayed2)
	}
	versions, err := s.ListStoreContentPackVersions(tenID, pack.ID)
	must(t, err)
	if len(versions) != 2 { // v1 + 回流候选,重放零新增
		t.Fatalf("versions = %d, want 2", len(versions))
	}
	cur, err := s.GetStoreAdoptedPackVersion(tenID, pack.ID)
	must(t, err)
	if cur.ID != v1.ID {
		t.Fatal("late return overwrote the current adoption (旧回流覆盖新采用)")
	}
	// 非回流 source 拒绝走本入口。
	wrong := storeVideoSpec(video, cover)
	if _, _, err := s.AddStoreReturnCandidate(tenID, pack.ID, wrong, "x"); err == nil {
		t.Fatal("non-return source accepted")
	}
}

func TestStoreBindActionContentRequiresAdoption(t *testing.T) {
	s, _ := openStorePackDB(t)
	tenID, storeID, _ := storePackFixture(t, s)
	pack, err := s.CreateStoreContentPack(tenID, storeID, "", "绑定包", "owner-1")
	must(t, err)
	video := mustRegisterMedia(t, s, tenID, storeID, strings.Repeat("8", 64), "video/mp4", upload.KindVideo, "owner-1")
	cover := mustRegisterMedia(t, s, tenID, storeID, strings.Repeat("9", 64), "image/jpeg", upload.KindImage, "owner-1")
	v1, err := s.CreateStoreContentPackVersion(tenID, pack.ID, storeVideoSpec(video, cover), contentpack.StatusCandidate, "owner-1")
	must(t, err)

	// 未采用:建立绑定拒绝(采用后才建立对外内容绑定)。
	if _, err := s.BindActionContent(tenID, storeID, "act_1", pack.ID, v1.ID, 3, "owner-1", time.Now()); err == nil {
		t.Fatal("binding created for non-adopted version")
	}
	at := time.Date(2026, 11, 10, 11, 0, 0, 0, time.UTC)
	if _, err := s.AdoptStoreContentPackVersion(tenID, pack.ID, v1.ID, "douyin", "owner-1", at); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	b, err := s.BindActionContent(tenID, storeID, "act_1", pack.ID, v1.ID, 3, "owner-1", at)
	must(t, err)
	if b.ActionID != "act_1" || b.PackID != pack.ID || b.LastVersionID != v1.ID || b.ActionRevision != 3 {
		t.Fatalf("binding = %+v", b)
	}
	got, err := s.GetActionContentBinding(tenID, "act_1")
	must(t, err)
	if got.PackID != pack.ID {
		t.Fatalf("got = %+v", got)
	}
	// 跨租户/未知动作 = ErrNotFound。
	if _, err := s.GetActionContentBinding(otherTenantID(s), "act_1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant binding = %v", err)
	}
	if _, err := s.GetActionContentBinding(tenID, "act_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown binding = %v", err)
	}
	// 跨店 pack 拒绝绑定。
	otherTen, otherStore, _ := storePackFixture(t, s)
	if _, err := s.BindActionContent(otherTen, otherStore, "act_2", pack.ID, v1.ID, 1, "owner-2", at); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant bind = %v, want ErrNotFound", err)
	}
	// 重绑(新采用后再绑)覆盖同一行。
	v2Spec := storeVideoSpec(video, cover)
	v2Spec.Title = "再采用"
	v2, err := s.CreateStoreContentPackVersion(tenID, pack.ID, v2Spec, contentpack.StatusCandidate, "owner-1")
	must(t, err)
	if _, err := s.AdoptStoreContentPackVersion(tenID, pack.ID, v2.ID, "douyin", "owner-1", at.Add(time.Minute)); err != nil {
		t.Fatalf("adopt v2: %v", err)
	}
	b2, err := s.BindActionContent(tenID, storeID, "act_1", pack.ID, v2.ID, 4, "owner-1", at.Add(time.Minute))
	must(t, err)
	if b2.ID != b.ID || b2.LastVersionID != v2.ID || b2.ActionRevision != 4 {
		t.Fatalf("rebind = %+v (want upsert of %s)", b2, b.ID)
	}
}

func otherTenantID(s *Store) string {
	ten, err := s.CreateTenant("另一个租户")
	if err != nil {
		return "tenant_missing"
	}
	return ten.ID
}

func TestStoreMediaAccessGrantLedger(t *testing.T) {
	s, _ := openStorePackDB(t)
	tenID, storeID, _ := storePackFixture(t, s)
	pack, err := s.CreateStoreContentPack(tenID, storeID, "", "审计包", "owner-1")
	must(t, err)
	video := mustRegisterMedia(t, s, tenID, storeID, strings.Repeat("a", 64), "video/mp4", upload.KindVideo, "owner-1")
	cover := mustRegisterMedia(t, s, tenID, storeID, strings.Repeat("b", 64), "image/jpeg", upload.KindImage, "owner-1")
	v1, err := s.CreateStoreContentPackVersion(tenID, pack.ID, storeVideoSpec(video, cover), contentpack.StatusCandidate, "owner-1")
	must(t, err)
	at := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	if _, err := s.AdoptStoreContentPackVersion(tenID, pack.ID, v1.ID, "douyin", "owner-1", at); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	expires := at.Add(10 * time.Minute)
	must(t, s.RecordMediaAccessGrant(tenID, v1.ID, video.ID, "preview", "ses_1", expires))
	grants, err := s.ListMediaAccessGrants(tenID, v1.ID)
	must(t, err)
	if len(grants) != 1 || grants[0].MediaID != video.ID || grants[0].Purpose != "preview" || grants[0].SessionRef != "ses_1" {
		t.Fatalf("grants = %+v", grants)
	}
	// 续签/重试各成一行,只追加。
	must(t, s.RecordMediaAccessGrant(tenID, v1.ID, video.ID, "platform_fetch", "", expires.Add(5*time.Minute)))
	grants, err = s.ListMediaAccessGrants(tenID, v1.ID)
	must(t, err)
	if len(grants) != 2 {
		t.Fatalf("grants after renewal = %d, want 2", len(grants))
	}
}
