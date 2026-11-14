package store

import (
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/contentpack"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

func TestNewStoreAdoptionCannotUndoContentBlock(t *testing.T) {
	s, _ := openStorePackDB(t)
	tenant, shop, _ := storePackFixture(t, s)
	pack, err := s.CreateStoreContentPack(tenant, shop, "", "撤回授权的内容", "owner")
	must(t, err)
	video := mustRegisterMedia(t, s, tenant, shop, strings.Repeat("c", 64), "video/mp4", upload.KindVideo, "owner")
	cover := mustRegisterMedia(t, s, tenant, shop, strings.Repeat("d", 64), "image/jpeg", upload.KindImage, "owner")
	old, err := s.CreateStoreContentPackVersion(tenant, pack.ID, storeVideoSpec(video, cover), contentpack.StatusCandidate, "owner")
	must(t, err)
	_, err = s.AdoptStoreContentPackVersion(tenant, pack.ID, old.ID, "douyin", "owner", time.Now())
	must(t, err)
	_, err = s.BlockStoreContentPackVersion(tenant, pack.ID, old.ID, "原授权已撤回", "owner", time.Now())
	must(t, err)
	next, err := s.CreateStoreContentPackVersion(tenant, pack.ID, storeVideoSpec(video, cover), contentpack.StatusCandidate, "owner")
	must(t, err)
	_, err = s.AdoptStoreContentPackVersion(tenant, pack.ID, next.ID, "douyin", "owner", time.Now())
	must(t, err)
	got, err := s.GetStoreContentPackVersion(old.ID, tenant)
	must(t, err)
	if got.Status != contentpack.StatusBlocked || got.BlockedReason != "原授权已撤回" || contentpack.Distributable(got.Status, got.GrantUntil, time.Now()) == nil {
		t.Fatalf("new adoption revoked the old distribution block: %+v", got)
	}
	if _, err = s.AdoptStoreContentPackVersion(tenant, pack.ID, old.ID, "douyin", "owner", time.Now()); err == nil {
		t.Fatal("blocked old version was re-adopted")
	}

	// A pre-fix database may already have the wrong retired status. Reapply the
	// real migration against that historical fixture, preserving all content.
	_, err = s.DB.Exec(`UPDATE store_content_pack_versions SET status='retired' WHERE id=?`, old.ID)
	must(t, err)
	if _, err = s.AdoptStoreContentPackVersion(tenant, pack.ID, old.ID, "douyin", "owner", time.Now()); err == nil {
		t.Fatal("historical block reason was ignored during adoption")
	}
	_, err = s.DB.Exec(`DELETE FROM schema_migrations WHERE name LIKE '%0137_%'`)
	must(t, err)
	must(t, db.Migrate(s.DB))
	got, err = s.GetStoreContentPackVersion(old.ID, tenant)
	must(t, err)
	if got.Status != contentpack.StatusBlocked || got.Body != old.Body || got.Video != old.Video {
		t.Fatalf("historical block recovery changed or released content: %+v", got)
	}
	must(t, db.Migrate(s.DB))

	third, err := s.CreateStoreContentPackVersion(tenant, pack.ID, storeVideoSpec(video, cover), contentpack.StatusCandidate, "owner")
	must(t, err)
	_, err = s.DB.Exec(`CREATE TRIGGER fail_adoption_event BEFORE INSERT ON store_content_pack_version_events
		WHEN NEW.kind='adopted' BEGIN SELECT RAISE(ABORT,'injected event persistence failure'); END`)
	must(t, err)
	if _, err = s.AdoptStoreContentPackVersion(tenant, pack.ID, third.ID, "douyin", "owner", time.Now()); err == nil {
		t.Fatal("expected failed adoption event persistence")
	}
	current, err := s.GetStoreAdoptedPackVersion(tenant, pack.ID)
	must(t, err)
	if current.ID != next.ID || current.Status != contentpack.StatusAdopted {
		t.Fatalf("failed adoption changed current content: %+v", current)
	}
	unadopted, err := s.GetStoreContentPackVersion(third.ID, tenant)
	must(t, err)
	if unadopted.Status != contentpack.StatusCandidate {
		t.Fatalf("failed adoption partially changed version status: %+v", unadopted)
	}
}

func TestNewLegacyAdoptionCannotUndoContentBlock(t *testing.T) {
	s := openPackStore(t)
	tenant, campaign, video, cover := packFixture(t, s)
	pack, err := s.CreateContentPack(tenant, campaign, "", "旧域撤权", "owner")
	must(t, err)
	old, err := s.CreateContentPackVersion(tenant, pack.ID, packSpec(video, cover), contentpack.StatusCandidate, "owner")
	must(t, err)
	_, err = s.AdoptContentPackVersion(tenant, pack.ID, old.ID, "douyin", "owner", time.Now())
	must(t, err)
	_, err = s.BlockContentPackVersion(tenant, pack.ID, old.ID, "原授权已撤回", "owner", time.Now())
	must(t, err)
	next, err := s.CreateContentPackVersion(tenant, pack.ID, packSpec(video, cover), contentpack.StatusCandidate, "owner")
	must(t, err)
	_, err = s.AdoptContentPackVersion(tenant, pack.ID, next.ID, "douyin", "owner", time.Now())
	must(t, err)
	got, err := s.GetContentPackVersion(old.ID, tenant)
	must(t, err)
	if got.Status != contentpack.StatusBlocked || got.BlockedReason != "原授权已撤回" {
		t.Fatalf("legacy adoption revoked the block: %+v", got)
	}
	if _, err = s.AdoptContentPackVersion(tenant, pack.ID, old.ID, "douyin", "owner", time.Now()); err == nil {
		t.Fatal("blocked legacy version was re-adopted")
	}
	_, err = s.DB.Exec(`UPDATE content_pack_versions SET status='retired' WHERE id=?`, old.ID)
	must(t, err)
	if _, err = s.AdoptContentPackVersion(tenant, pack.ID, old.ID, "douyin", "owner", time.Now()); err == nil {
		t.Fatal("legacy historical block reason was ignored during adoption")
	}
	_, err = s.DB.Exec(`DELETE FROM schema_migrations WHERE name LIKE '%0137_%'`)
	must(t, err)
	must(t, db.Migrate(s.DB))
	got, err = s.GetContentPackVersion(old.ID, tenant)
	must(t, err)
	if got.Status != contentpack.StatusBlocked || got.Body != old.Body {
		t.Fatalf("legacy historical block not recovered: %+v", got)
	}
	third, err := s.CreateContentPackVersion(tenant, pack.ID, packSpec(video, cover), contentpack.StatusCandidate, "owner")
	must(t, err)
	_, err = s.DB.Exec(`CREATE TRIGGER fail_legacy_adoption_event BEFORE INSERT ON content_pack_version_events
		WHEN NEW.kind='adopted' BEGIN SELECT RAISE(ABORT,'injected event persistence failure'); END`)
	must(t, err)
	if _, err = s.AdoptContentPackVersion(tenant, pack.ID, third.ID, "douyin", "owner", time.Now()); err == nil {
		t.Fatal("expected failed legacy adoption event persistence")
	}
	current, err := s.GetAdoptedVersion(tenant, campaign, "douyin")
	must(t, err)
	if current.ID != next.ID || current.Status != contentpack.StatusAdopted {
		t.Fatalf("failed legacy adoption changed current content: %+v", current)
	}
	unadopted, err := s.GetContentPackVersion(third.ID, tenant)
	must(t, err)
	if unadopted.Status != contentpack.StatusCandidate {
		t.Fatalf("failed legacy adoption partially changed version status: %+v", unadopted)
	}
}
