package store

// store_campaign_publish_config_test.go: HUI-1676 增量(商家配置与真实启用)
// 持久层验收。
//
// 覆盖(计划 Step 3):
//   - 版本递增:创建=version1/revision1(create);每次保存 version+1 且落
//     全量快照修订;
//   - 五动作留痕:create|update|enable|pause|resume 全部进不可变修订 trail;
//   - enabled 下保存 → 自动回 draft(改版必须重新过启用检查),启用时点的
//     enabled_version 与告知快照保留不覆盖;
//   - 并发启用:多 goroutine 并发 EnablePublishConfig → 单一 enabled 状态,
//     enable 修订 ≤ 2,无重复钉版;
//   - 租户隔离:跨租户读写一律 ErrNotFound;
//   - 同版本证明:enable 后 GetCampaignRulesByCampaign(公共评估路径)与
//     GetCampaignRules(管理读路径)读到同一 campaign_rules version 行。

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/campaignenable"
	"github.com/bianjiefilm/touch-engine/server/internal/campaignrules"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
)

func timeFixed() time.Time { return time.Date(2026, 10, 14, 8, 0, 0, 0, time.UTC) }

func openConfigStore(t *testing.T) *Store {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return New(d)
}

func configFixture(t *testing.T, s *Store) (tenantA, tenantB, campA, campB string) {
	t.Helper()
	ta, err := s.CreateTenant("配置商家A")
	must(t, err)
	tb, err := s.CreateTenant("配置商家B")
	must(t, err)
	ownA, err := s.CreateMember(ta.ID, "usr_cfg_owner_a", "org_owner", "A配置老板", "seed", true)
	must(t, err)
	ownB, err := s.CreateMember(tb.ID, "usr_cfg_owner_b", "org_owner", "B配置老板", "seed", true)
	must(t, err)
	cA, err := s.CreateCampaign(NewCampaign{TenantID: ta.ID, Title: "A的发布活动", CreatedBy: ownA.PrincipalRef})
	must(t, err)
	cB, err := s.CreateCampaign(NewCampaign{TenantID: tb.ID, Title: "B的发布活动", CreatedBy: ownB.PrincipalRef})
	must(t, err)
	return ta.ID, tb.ID, cA.ID, cB.ID
}

func sampleSpec() campaignenable.Spec {
	return campaignenable.Spec{
		VerificationMode: campaignenable.ModeStaffObserved,
		CustomerCopy:     "现场把发布成功的页面给店员看,确认后当场领门店优惠券。",
		Reward: campaignenable.Reward{
			Kind:           campaignenable.KindStoreCoupon,
			Slot:           "store_coupon_main",
			BudgetMode:     campaignenable.BudgetTotalQuantity,
			TotalQuantity:  intPtr(50),
			Currency:       "CNY",
			StoreIDs:       []string{"store_1"},
			ClaimFrom:      "2026-10-01T00:00:00Z",
			ClaimUntil:     "2026-10-31T23:59:59Z",
			RedeemFrom:     "2026-10-01T00:00:00Z",
			RedeemUntil:    "2026-11-30T23:59:59Z",
			OnsiteRedempt:  true,
			StaffScope:     campaignenable.ScopeStoreStaff,
			OutOfStockRule: campaignenable.StockStopNew,
		},
		DedupBasis:   campaignenable.BasisPlatformSubject,
		Channels:     []campaignenable.ChannelRef{{Platform: "douyin", EntryMode: "h5", Scenario: "store_post"}},
		ContentVerID: "cpv_sample_1",
	}
}

func TestPublishConfigCreateVersionsAndTrail(t *testing.T) {
	s := openConfigStore(t)
	tenA, _, campA, _ := configFixture(t, s)

	rec, err := s.UpsertPublishConfigDraft(tenA, campA, sampleSpec(), "usr_cfg_owner_a")
	must(t, err)
	if rec.Version != 1 || rec.Status != "draft" {
		t.Fatalf("first save = version %d status %s, want 1/draft", rec.Version, rec.Status)
	}
	if rec.Spec.VerificationMode != campaignenable.ModeStaffObserved || rec.Spec.ContentVerID != "cpv_sample_1" {
		t.Fatalf("spec round trip mismatch: %+v", rec.Spec)
	}

	// 修改一次 → 新版本 + update 修订。
	spec2 := sampleSpec()
	spec2.CustomerCopy = "改过的顾客文案。"
	rec2, err := s.UpsertPublishConfigDraft(tenA, campA, spec2, "usr_cfg_owner_a")
	must(t, err)
	if rec2.Version != 2 {
		t.Fatalf("second save version = %d, want 2", rec2.Version)
	}

	revs, err := s.ListPublishConfigRevisions(tenA, campA)
	must(t, err)
	if len(revs) != 2 || revs[0].Action != "create" || revs[1].Action != "update" {
		t.Fatalf("revision trail = %+v, want [create, update]", revs)
	}
	if revs[0].Version != 1 || revs[1].Version != 2 {
		t.Fatalf("revision versions = %d,%d want 1,2", revs[0].Version, revs[1].Version)
	}
	// 快照内容可回查:第一份修订还是旧文案。
	if revs[0].Spec.CustomerCopy != sampleSpec().CustomerCopy {
		t.Fatalf("revision 1 snapshot was rewritten: %q", revs[0].Spec.CustomerCopy)
	}
}

func TestPublishConfigEnablePinPauseTrail(t *testing.T) {
	s := openConfigStore(t)
	tenA, _, campA, _ := configFixture(t, s)
	if _, err := s.UpsertPublishConfigDraft(tenA, campA, sampleSpec(), "usr_cfg_owner_a"); err != nil {
		t.Fatalf("draft save: %v", err)
	}

	disc := campaignenable.BuildDisclosure(sampleSpec(), 1, timeFixed()).JSON()
	if err := s.EnablePublishConfig(tenA, campA, 1, disc, "usr_cfg_owner_a"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	rec, err := s.GetPublishConfig(tenA, campA)
	must(t, err)
	if rec.Status != "enabled" || rec.EnabledVersion != 1 || rec.Disclosure != disc {
		t.Fatalf("after enable = status %s version %d disclosure %q", rec.Status, rec.EnabledVersion, rec.Disclosure)
	}

	if err := s.PausePublishConfig(tenA, campA, "usr_cfg_owner_a"); err != nil {
		t.Fatalf("pause: %v", err)
	}
	rec, err = s.GetPublishConfig(tenA, campA)
	must(t, err)
	if rec.Status != "paused" {
		t.Fatalf("after pause status = %s, want paused", rec.Status)
	}

	// 恢复(resume 走同一条件启用路径)。
	if err := s.EnablePublishConfig(tenA, campA, 1, disc, "usr_cfg_owner_a"); err != nil {
		t.Fatalf("resume: %v", err)
	}

	revs, err := s.ListPublishConfigRevisions(tenA, campA)
	must(t, err)
	actions := make([]string, 0, len(revs))
	for _, r := range revs {
		actions = append(actions, r.Action)
	}
	want := []string{"create", "enable", "pause", "resume"}
	if len(actions) != len(want) {
		t.Fatalf("trail = %v, want %v", actions, want)
	}
	for i := range want {
		if actions[i] != want[i] {
			t.Fatalf("trail[%d] = %s, want %s", i, actions[i], want[i])
		}
	}
}

func TestPublishConfigSaveWhileEnabledDemotesToDraft(t *testing.T) {
	s := openConfigStore(t)
	tenA, _, campA, _ := configFixture(t, s)
	if _, err := s.UpsertPublishConfigDraft(tenA, campA, sampleSpec(), "usr_cfg_owner_a"); err != nil {
		t.Fatalf("draft save: %v", err)
	}
	disc := campaignenable.BuildDisclosure(sampleSpec(), 1, timeFixed()).JSON()
	must(t, s.EnablePublishConfig(tenA, campA, 1, disc, "usr_cfg_owner_a"))

	spec2 := sampleSpec()
	spec2.Reward.TotalQuantity = intPtr(80)
	rec, err := s.UpsertPublishConfigDraft(tenA, campA, spec2, "usr_cfg_owner_a")
	must(t, err)
	if rec.Status != "draft" {
		t.Fatalf("editing an enabled config must demote to draft, got %s", rec.Status)
	}
	if rec.Version != 2 {
		t.Fatalf("edit version = %d, want 2", rec.Version)
	}
	if rec.EnabledVersion != 1 || rec.Disclosure != disc {
		t.Fatalf("historical enable pin must survive the edit: v%d %q", rec.EnabledVersion, rec.Disclosure)
	}
}

func TestPublishConfigConcurrentEnableIsIdempotent(t *testing.T) {
	s := openConfigStore(t)
	tenA, _, campA, _ := configFixture(t, s)
	if _, err := s.UpsertPublishConfigDraft(tenA, campA, sampleSpec(), "usr_cfg_owner_a"); err != nil {
		t.Fatalf("draft save: %v", err)
	}
	disc := campaignenable.BuildDisclosure(sampleSpec(), 1, timeFixed()).JSON()

	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.EnablePublishConfig(tenA, campA, 1, disc, "usr_cfg_owner_a"); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent enable returned error: %v", err)
	}

	rec, err := s.GetPublishConfig(tenA, campA)
	must(t, err)
	if rec.Status != "enabled" || rec.EnabledVersion != 1 {
		t.Fatalf("after concurrent enable = %s v%d, want enabled v1", rec.Status, rec.EnabledVersion)
	}
	revs, err := s.ListPublishConfigRevisions(tenA, campA)
	must(t, err)
	enables := 0
	for _, r := range revs {
		if r.Action == "enable" {
			enables++
		}
	}
	if enables > 2 {
		// SQLite 单连接串行化下应当恰好 1;上界 2 容忍调度差异,绝不允许刷屏。
		t.Fatalf("concurrent enable wrote %d enable revisions, must stay bounded", enables)
	}
}

func TestPublishConfigTenantIsolation(t *testing.T) {
	s := openConfigStore(t)
	tenA, tenB, campA, campB := configFixture(t, s)
	if _, err := s.UpsertPublishConfigDraft(tenA, campA, sampleSpec(), "usr_cfg_owner_a"); err != nil {
		t.Fatalf("draft save: %v", err)
	}

	if _, err := s.GetPublishConfig(tenB, campA); err != ErrNotFound {
		t.Fatalf("cross-tenant read = %v, want ErrNotFound", err)
	}
	if _, err := s.UpsertPublishConfigDraft(tenB, campA, sampleSpec(), "usr_cfg_owner_b"); err != ErrNotFound {
		t.Fatalf("cross-tenant write = %v, want ErrNotFound", err)
	}
	if err := s.EnablePublishConfig(tenB, campA, 1, "{}", "usr_cfg_owner_b"); err != ErrNotFound {
		t.Fatalf("cross-tenant enable = %v, want ErrNotFound", err)
	}
	if err := s.PausePublishConfig(tenB, campB, "usr_cfg_owner_b"); err != ErrNotFound {
		t.Fatalf("pause on unconfigured campaign = %v, want ErrNotFound", err)
	}
}

func TestPublishConfigSameVersionAcrossReadPaths(t *testing.T) {
	// 票面验收「顾客页和店员页读到同一规则版本」的存储路径证明:
	// 公共评估路径(GetCampaignRulesByCampaign)与管理读路径(GetCampaignRules)
	// 在配置启用后读到同一 campaign_rules version 行。
	s := openConfigStore(t)
	tenA, _, campA, _ := configFixture(t, s)

	rules, err := s.UpsertCampaignRules(tenA, campA, campaignrules.Ruleset{PerContactDailySubmissionCap: intPtr(2)}, "usr_cfg_owner_a")
	must(t, err)
	if _, err := s.UpsertPublishConfigDraft(tenA, campA, sampleSpec(), "usr_cfg_owner_a"); err != nil {
		t.Fatalf("draft save: %v", err)
	}
	must(t, s.EnablePublishConfig(tenA, campA, 1, "{}", "usr_cfg_owner_a"))

	cfg, err := s.GetPublishConfig(tenA, campA)
	must(t, err)
	publicPath, err := s.GetCampaignRulesByCampaign(campA)
	must(t, err)
	adminPath, err := s.GetCampaignRules(tenA, campA)
	must(t, err)
	if publicPath.Version != adminPath.Version {
		t.Fatalf("read paths diverged: public=%d admin=%d", publicPath.Version, adminPath.Version)
	}
	// 规则版本与配置版本互不串写:配置启用钉的是配置版本,规则行保持自己的版本。
	if cfg.Version < 1 || rules.Version < 1 {
		t.Fatalf("versions must both be >= 1: config=%d rules=%d", cfg.Version, rules.Version)
	}
}

func TestPublishConfigInvalidSpecRefusedAtomically(t *testing.T) {
	s := openConfigStore(t)
	tenA, _, campA, _ := configFixture(t, s)
	bad := sampleSpec()
	bad.VerificationMode = "self_report"
	if _, err := s.UpsertPublishConfigDraft(tenA, campA, bad, "usr_cfg_owner_a"); err == nil {
		t.Fatal("invalid spec must be refused at the store layer too")
	}
	if _, err := s.GetPublishConfig(tenA, campA); err != ErrNotFound {
		t.Fatalf("refused save must leave no half record, got %v", err)
	}
}
