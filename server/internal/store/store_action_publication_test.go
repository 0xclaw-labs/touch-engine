package store

import (
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
	"github.com/bianjiefilm/touch-engine/server/internal/storefrontversion"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

func TestFreezeActionContentUsesExplicitPlatformAdoption(t *testing.T) {
	s, _ := openStorePackDB(t)
	tenant, shop, _ := storePackFixture(t, s)
	video := mustRegisterMedia(t, s, tenant, shop, strings.Repeat("a", 64), "video/mp4", upload.KindVideo, "owner")
	cover := mustRegisterMedia(t, s, tenant, shop, strings.Repeat("b", 64), "image/jpeg", upload.KindImage, "owner")
	makeVersion := func(title, adoptedPlatform string, imageText bool) (string, string) {
		t.Helper()
		pack, err := s.CreateStoreContentPack(tenant, shop, "", title, "owner")
		must(t, err)
		spec := storeVideoSpec(video, cover)
		spec.PlatformVersions = []contentpack.PlatformVersion{{Platform: "douyin", SourceVersion: "source"}, {Platform: "wechat", SourceVersion: "source"}}
		if imageText {
			spec.ContentKind = contentpack.ContentKindImageText
			spec.VideoMediaID, spec.CoverMediaID = "", ""
			spec.ImageTextMediaIDs = []string{cover.ID}
		}
		version, err := s.CreateStoreContentPackVersion(tenant, pack.ID, spec, contentpack.StatusCandidate, "owner")
		must(t, err)
		_, err = s.AdoptStoreContentPackVersion(tenant, pack.ID, version.ID, adoptedPlatform, "owner", time.Now())
		must(t, err)
		return pack.ID, version.ID
	}
	pack, selected := makeVersion("抖音视频", "douyin", false)
	_, wechatOnly := makeVersion("声明两平台但仅采用微信", "wechat", false)
	_, wrongKind := makeVersion("抖音图文", "douyin", true)
	_, alternative := makeVersion("另一个抖音视频", "douyin", false)
	snap := storefrontversion.ActionSnapshot{Kind: "publish", Platform: "douyin", Variant: "video", ContentBindingRef: "pack:" + pack}
	must(t, s.FreezeActionContent(tenant, shop, &snap))
	if snap.ContentVersionID != selected || len(snap.ContentCandidates) != 2 {
		t.Fatalf("wrong platform/kind pool: %+v", snap)
	}
	seen := map[string]bool{}
	for _, id := range snap.ContentCandidates {
		seen[id] = true
	}
	if !seen[alternative] || seen[wechatOnly] || seen[wrongKind] {
		t.Fatalf("candidate adoption mismatch: %+v", snap.ContentCandidates)
	}
	// A previous choice cannot survive after changing the action to a platform
	// for which its bound pack was never adopted.
	snap.Platform = "wechat"
	must(t, s.FreezeActionContent(tenant, shop, &snap))
	if snap.ContentVersionID != "" || len(snap.ContentCandidates) != 0 {
		t.Fatalf("unadopted platform inherited old selection: %+v", snap)
	}
	// Existing immutable bad snapshots are refused, never silently reassigned.
	row := StoreAction{TenantID: tenant, StoreID: shop, Kind: "publish", Platform: "douyin", Variant: "video", ContentVersionID: wrongKind, ContentCandidates: []string{wrongKind, selected}}
	must(t, s.filterFrozenActionContent(&row, time.Now().UTC().Format(time.RFC3339Nano)))
	if row.ContentVersionID != "" || len(row.ContentCandidates) != 1 || row.ContentCandidates[0] != selected {
		t.Fatalf("incompatible frozen default remained usable: %+v", row)
	}
	row.ContentVersionID, row.ContentCandidates = wechatOnly, []string{wechatOnly}
	must(t, s.filterFrozenActionContent(&row, time.Now().UTC().Format(time.RFC3339Nano)))
	if row.ContentVersionID != "" || len(row.ContentCandidates) != 0 {
		t.Fatalf("platform declaration was mistaken for historical adoption: %+v", row)
	}
}

func TestStagedLegacyPublicationKeepsItsFrozenContent(t *testing.T) {
	s, _ := openStorePackDB(t)
	tenant, shop, _ := storePackFixture(t, s)
	video := mustRegisterMedia(t, s, tenant, shop, strings.Repeat("a", 64), "video/mp4", upload.KindVideo, "owner")
	cover := mustRegisterMedia(t, s, tenant, shop, strings.Repeat("b", 64), "image/jpeg", upload.KindImage, "owner")
	pack, err := s.CreateStoreContentPack(tenant, shop, "", "迁移前线上内容", "owner")
	must(t, err)
	v, err := s.CreateStoreContentPackVersion(tenant, pack.ID, storeVideoSpec(video, cover), contentpack.StatusCandidate, "owner")
	must(t, err)
	_, err = s.AdoptStoreContentPackVersion(tenant, pack.ID, v.ID, "douyin", "owner", time.Now())
	must(t, err)
	in := StoreActionInput{CatalogCode: "A01", Label: "原线上入口", TargetRef: "https://www.douyin.com/poi/123", ContentBindingRef: "pack:" + pack.ID, Enabled: true}
	rev, actions, err := s.ReplaceStoreActions(tenant, shop, 0, []StoreActionInput{in})
	must(t, err)
	before, err := s.ListPublishedStoreActions(shop, time.Now())
	must(t, err)
	if len(before) != 1 || before[0].ContentVersionID != v.ID {
		t.Fatalf("legacy starting point: %+v", before)
	}
	in.ID = actions[0].ID
	in.Label = "尚未发布的修改"
	_, _, err = s.StageStoreActions(tenant, shop, rev, []StoreActionInput{in})
	must(t, err)
	after, err := s.ListPublishedStoreActions(shop, time.Now())
	must(t, err)
	if len(after) != 1 || after[0].ContentVersionID != v.ID || after[0].Label != "原线上入口" {
		t.Fatalf("staging removed or changed the frozen legacy publication: %+v", after)
	}
	next, err := s.CreateStoreContentPackVersion(tenant, pack.ID, storeVideoSpec(video, cover), contentpack.StatusCandidate, "owner")
	must(t, err)
	_, err = s.AdoptStoreContentPackVersion(tenant, pack.ID, next.ID, "douyin", "owner", time.Now())
	must(t, err)
	after, err = s.ListPublishedStoreActions(shop, time.Now())
	must(t, err)
	if len(after) != 1 || after[0].ContentVersionID != v.ID || len(after[0].ContentCandidates) != 1 {
		t.Fatalf("later adoption refilled the captured legacy publication: %+v", after)
	}
}
