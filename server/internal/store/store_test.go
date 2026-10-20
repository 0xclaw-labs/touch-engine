package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/campaign"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
)

func openStore(t *testing.T) *Store {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return New(d)
}

type tenantCtx struct {
	tenA, tenB Tenant
	ownA, ownB Member
}

func seedTwoTenants(t *testing.T, s *Store) tenantCtx {
	t.Helper()
	var err error
	var ctx tenantCtx
	ctx.tenA, err = s.CreateTenant("商家A")
	must(t, err)
	ctx.tenB, err = s.CreateTenant("商家B")
	must(t, err)
	ctx.ownA, err = s.CreateMember(ctx.tenA.ID, "usr_owner_a", "org_owner", "A老板", "seed", true)
	must(t, err)
	ctx.ownB, err = s.CreateMember(ctx.tenB.ID, "usr_owner_b", "org_owner", "B老板", "seed", true)
	must(t, err)
	return ctx
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCampaignTenantIsolation(t *testing.T) {
	s := openStore(t)
	ctx := seedTwoTenants(t, s)

	cA, err := s.CreateCampaign(NewCampaign{TenantID: ctx.tenA.ID, Title: "A的活动", PublicContent: "公开内容A", CreatedBy: ctx.ownA.PrincipalRef})
	must(t, err)
	cB, err := s.CreateCampaign(NewCampaign{TenantID: ctx.tenB.ID, Title: "B的活动", CreatedBy: ctx.ownB.PrincipalRef})
	must(t, err)

	// tenant A can read its own, never B's
	if got, err := s.GetCampaign(cA.ID, ctx.tenA.ID); err != nil || got.Title != "A的活动" {
		t.Fatalf("GetCampaign A: %v %+v", err, got)
	}
	if _, err := s.GetCampaign(cB.ID, ctx.tenA.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant campaign read must be ErrNotFound, got %v", err)
	}
	if _, err := s.GetCampaign(cA.ID, ctx.tenB.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant campaign read (B side) must be ErrNotFound, got %v", err)
	}
	listA, err := s.ListCampaigns(ctx.tenA.ID)
	must(t, err)
	if len(listA) != 1 || listA[0].ID != cA.ID {
		t.Fatalf("ListCampaigns A = %+v", listA)
	}

	// transition of B's campaign under A's scope must fail closed
	if _, err := s.TransitionCampaign(cB.ID, ctx.tenA.ID, campaign.StatusActive); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant transition must be ErrNotFound, got %v", err)
	}
}

func TestStoreTenantIsolationStoresAndMembers(t *testing.T) {
	s := openStore(t)
	ctx := seedTwoTenants(t, s)

	stoA, err := s.CreateStore(ctx.tenA.ID, "A门店", "A地址", ctx.ownA.PrincipalRef)
	must(t, err)
	// B cannot see or use A's store id
	if _, err := s.GetStore(stoA.ID, ctx.tenB.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant store read must be ErrNotFound, got %v", err)
	}
	// campaign referencing another tenant's store is refused by FK
	if _, err := s.CreateCampaign(NewCampaign{TenantID: ctx.tenA.ID, Title: "x", StoreID: "sto_missing", CreatedBy: ctx.ownA.PrincipalRef}); err == nil {
		t.Fatal("campaign with unknown store must be refused")
	}

	// member lookup is scoped per tenant: same principal cannot be conjured into B
	if _, err := s.GetMemberByPrincipal(ctx.tenB.ID, ctx.ownA.PrincipalRef); !errors.Is(err, ErrNotFound) {
		t.Fatalf("principal must have no membership in B, got %v", err)
	}
}

func TestCampaignLifecycle(t *testing.T) {
	s := openStore(t)
	ctx := seedTwoTenants(t, s)

	c, err := s.CreateCampaign(NewCampaign{TenantID: ctx.tenA.ID, Title: "t", CreatedBy: ctx.ownA.PrincipalRef})
	must(t, err)
	if c.Status != "draft" {
		t.Fatalf("new campaign status = %q, want draft", c.Status)
	}

	// draft -> paused is illegal
	if _, err := s.TransitionCampaign(c.ID, ctx.tenA.ID, campaign.StatusPaused); !errors.Is(err, campaign.ErrIllegalTransition) {
		t.Fatalf("draft->paused must be illegal, got %v", err)
	}
	// row unchanged after refused transition
	got, err := s.GetCampaign(c.ID, ctx.tenA.ID)
	must(t, err)
	if got.Status != "draft" {
		t.Fatalf("refused transition changed status to %q", got.Status)
	}

	// draft -> active -> paused -> active -> ended
	for _, step := range []campaign.Status{campaign.StatusActive, campaign.StatusPaused, campaign.StatusActive, campaign.StatusEnded} {
		c, err = s.TransitionCampaign(c.ID, ctx.tenA.ID, step)
		must(t, err)
	}
	if c.Status != "ended" {
		t.Fatalf("final status = %q", c.Status)
	}
	// ended is terminal
	if _, err := s.TransitionCampaign(c.ID, ctx.tenA.ID, campaign.StatusActive); !errors.Is(err, campaign.ErrIllegalTransition) {
		t.Fatalf("ended->active must be illegal, got %v", err)
	}
}

func TestCampaignPatchAndOrderRefNullable(t *testing.T) {
	s := openStore(t)
	ctx := seedTwoTenants(t, s)

	// 无订单活动:order_ref stays empty
	standalone, err := s.CreateCampaign(NewCampaign{TenantID: ctx.tenA.ID, Title: "无订单", CreatedBy: ctx.ownA.PrincipalRef})
	must(t, err)
	if standalone.OrderRef != "" {
		t.Fatalf("order_ref = %q, want empty", standalone.OrderRef)
	}

	withRef, err := s.CreateCampaign(NewCampaign{TenantID: ctx.tenA.ID, Title: "挂单", OrderRef: "ord-opaque-1", CreatedBy: ctx.ownA.PrincipalRef})
	must(t, err)
	got, err := s.GetCampaign(withRef.ID, ctx.tenA.ID)
	must(t, err)
	if got.OrderRef != "ord-opaque-1" {
		t.Fatalf("order_ref = %q", got.OrderRef)
	}

	// patch fields
	title := "新标题"
	content := "新公开内容"
	starts := "2026-09-01T00:00:00Z"
	ends := "2026-09-30T00:00:00Z"
	patched, err := s.UpdateCampaign(got.ID, ctx.tenA.ID, CampaignPatch{
		Title: &title, PublicContent: &content, StartsAt: &starts, EndsAt: &ends,
	})
	must(t, err)
	if patched.Title != title || patched.PublicContent != content || patched.StartsAt != starts || patched.EndsAt != ends {
		t.Fatalf("patched = %+v", patched)
	}
	// cross-tenant patch fails
	if _, err := s.UpdateCampaign(got.ID, ctx.tenB.ID, CampaignPatch{Title: &title}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant patch must be ErrNotFound, got %v", err)
	}
}

func TestLinkLifecycleAndSetEnabled(t *testing.T) {
	s := openStore(t)
	ctx := seedTwoTenants(t, s)
	c, err := s.CreateCampaign(NewCampaign{TenantID: ctx.tenA.ID, Title: "t", CreatedBy: ctx.ownA.PrincipalRef})
	must(t, err)

	l, err := s.CreateLink(ctx.tenA.ID, c.ID, ctx.ownA.PrincipalRef)
	must(t, err)
	if !l.Enabled || len(l.Code) != 12 {
		t.Fatalf("link = %+v", l)
	}
	// creating a link for another tenant's campaign scope must fail
	if _, err := s.CreateLink(ctx.tenB.ID, c.ID, "usr_x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant link creation must fail, got %v", err)
	}

	// disable then re-enable
	off, err := s.SetLinkEnabled(l.ID, ctx.tenA.ID, false)
	must(t, err)
	if off.Enabled {
		t.Fatal("link still enabled after disable")
	}
	back, err := s.SetLinkEnabled(l.ID, ctx.tenA.ID, true)
	must(t, err)
	if !back.Enabled {
		t.Fatal("link not re-enabled")
	}

	// disabling under foreign tenant scope must fail
	if _, err := s.SetLinkEnabled(l.ID, ctx.tenB.ID, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant link disable must fail, got %v", err)
	}
}

func TestAssetsReferenceScope(t *testing.T) {
	s := openStore(t)
	ctx := seedTwoTenants(t, s)
	c, err := s.CreateCampaign(NewCampaign{TenantID: ctx.tenA.ID, Title: "t", CreatedBy: ctx.ownA.PrincipalRef})
	must(t, err)

	_, err = s.AddCampaignAsset(ctx.tenA.ID, c.ID, "ast_1", "sha256-aaa", ctx.ownA.PrincipalRef)
	must(t, err)
	// duplicate (campaign, asset, version) is rejected by the UNIQUE constraint
	if _, err := s.AddCampaignAsset(ctx.tenA.ID, c.ID, "ast_1", "sha256-aaa", ctx.ownA.PrincipalRef); err == nil {
		t.Fatal("duplicate asset ref must be rejected")
	}
	// same asset different version allowed
	_, err = s.AddCampaignAsset(ctx.tenA.ID, c.ID, "ast_1", "sha256-bbb", ctx.ownA.PrincipalRef)
	must(t, err)

	// cross-tenant asset add on A's campaign must fail
	if _, err := s.AddCampaignAsset(ctx.tenB.ID, c.ID, "ast_9", "v9", "usr_b"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant asset add must fail, got %v", err)
	}

	list, err := s.ListCampaignAssets(ctx.tenA.ID, c.ID)
	must(t, err)
	if len(list) != 2 {
		t.Fatalf("assets = %+v", list)
	}

	// remove scoped
	must(t, s.RemoveCampaignAsset(list[0].ID, ctx.tenA.ID, c.ID))
	if err := s.RemoveCampaignAsset(list[0].ID, ctx.tenA.ID, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double remove must be ErrNotFound, got %v", err)
	}
	// remove under foreign tenant must not delete
	rem, err := s.ListCampaignAssets(ctx.tenA.ID, c.ID)
	must(t, err)
	if len(rem) != 1 {
		t.Fatalf("after remove assets = %+v", rem)
	}
	if err := s.RemoveCampaignAsset(rem[0].ID, ctx.tenB.ID, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant remove must be ErrNotFound, got %v", err)
	}
	after, err := s.ListCampaignAssets(ctx.tenA.ID, c.ID)
	must(t, err)
	if len(after) != 1 {
		t.Fatalf("cross-tenant remove deleted a row: %+v", after)
	}
}

// TestResolveLinkFiveStateFamily pins the public resolution verdicts.
func TestResolveLinkFiveStateFamily(t *testing.T) {
	s := openStore(t)
	ctx := seedTwoTenants(t, s)
	at := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)

	// not_found: unknown + malformed + id-shaped codes
	for _, code := range []string{"ZZZZZZZZZZZZ", "../etc/passwd", "tnt_abc123", ""} {
		if got := s.ResolveLink(code, at); got.Outcome != OutcomeNotFound {
			t.Fatalf("code %q -> %q, want not_found", code, got.Outcome)
		}
	}

	mkCampaign := func(title string) Campaign {
		c, err := s.CreateCampaign(NewCampaign{TenantID: ctx.tenA.ID, Title: title, CreatedBy: ctx.ownA.PrincipalRef})
		must(t, err)
		return c
	}

	// draft campaign with a link -> draft outcome (暂停 family)
	draft := mkCampaign("草稿")
	ld, err := s.CreateLink(ctx.tenA.ID, draft.ID, ctx.ownA.PrincipalRef)
	must(t, err)
	if got := s.ResolveLink(ld.Code, at); got.Outcome != OutcomeDraft {
		t.Fatalf("draft -> %q", got.Outcome)
	}

	// active without window -> available
	act := mkCampaign("进行中")
	if _, err := s.TransitionCampaign(act.ID, ctx.tenA.ID, campaign.StatusActive); err != nil {
		t.Fatal(err)
	}
	la, err := s.CreateLink(ctx.tenA.ID, act.ID, ctx.ownA.PrincipalRef)
	must(t, err)
	got := s.ResolveLink(la.Code, at)
	if got.Outcome != OutcomeAvailable || got.Campaign.Title != "进行中" {
		t.Fatalf("active -> %+v", got)
	}

	// expired window
	exp := mkCampaign("过期")
	pastEnd := "2026-09-10T00:00:00Z"
	if _, err := s.UpdateCampaign(exp.ID, ctx.tenA.ID, CampaignPatch{EndsAt: &pastEnd}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TransitionCampaign(exp.ID, ctx.tenA.ID, campaign.StatusActive); err != nil {
		t.Fatal(err)
	}
	le, err := s.CreateLink(ctx.tenA.ID, exp.ID, ctx.ownA.PrincipalRef)
	must(t, err)
	if got := s.ResolveLink(le.Code, at); got.Outcome != OutcomeExpired {
		t.Fatalf("expired -> %q", got.Outcome)
	}

	// not started
	ns := mkCampaign("未开始")
	futureStart := "2026-09-30T00:00:00Z"
	if _, err := s.UpdateCampaign(ns.ID, ctx.tenA.ID, CampaignPatch{StartsAt: &futureStart}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TransitionCampaign(ns.ID, ctx.tenA.ID, campaign.StatusActive); err != nil {
		t.Fatal(err)
	}
	ln, err := s.CreateLink(ctx.tenA.ID, ns.ID, ctx.ownA.PrincipalRef)
	must(t, err)
	if got := s.ResolveLink(ln.Code, at); got.Outcome != OutcomeNotStarted {
		t.Fatalf("not_started -> %q", got.Outcome)
	}

	// paused
	pau := mkCampaign("暂停")
	if _, err := s.TransitionCampaign(pau.ID, ctx.tenA.ID, campaign.StatusActive); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TransitionCampaign(pau.ID, ctx.tenA.ID, campaign.StatusPaused); err != nil {
		t.Fatal(err)
	}
	lp, err := s.CreateLink(ctx.tenA.ID, pau.ID, ctx.ownA.PrincipalRef)
	must(t, err)
	if got := s.ResolveLink(lp.Code, at); got.Outcome != OutcomePaused {
		t.Fatalf("paused -> %q", got.Outcome)
	}

	// ended
	end := mkCampaign("结束")
	if _, err := s.TransitionCampaign(end.ID, ctx.tenA.ID, campaign.StatusEnded); err != nil {
		t.Fatal(err)
	}
	lx, err := s.CreateLink(ctx.tenA.ID, end.ID, ctx.ownA.PrincipalRef)
	must(t, err)
	if got := s.ResolveLink(lx.Code, at); got.Outcome != OutcomeEnded {
		t.Fatalf("ended -> %q", got.Outcome)
	}

	// disabled link beats available campaign
	if _, err := s.SetLinkEnabled(la.ID, ctx.tenA.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := s.ResolveLink(la.Code, at); got.Outcome != OutcomeLinkDisabled {
		t.Fatalf("disabled -> %q", got.Outcome)
	}
}

func TestShortcodeUniquenessAcrossCampaigns(t *testing.T) {
	s := openStore(t)
	ctx := seedTwoTenants(t, s)
	c, err := s.CreateCampaign(NewCampaign{TenantID: ctx.tenA.ID, Title: "t", CreatedBy: ctx.ownA.PrincipalRef})
	must(t, err)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		l, err := s.CreateLink(ctx.tenA.ID, c.ID, ctx.ownA.PrincipalRef)
		must(t, err)
		if seen[l.Code] {
			t.Fatalf("duplicate code %q", l.Code)
		}
		seen[l.Code] = true
	}
}

// HUI-2628: a short-code collision is a retry, not a 500. The generator seam
// lets the test force the first candidate to collide with an existing row.
func TestCreateLinkRetriesShortcodeCollision(t *testing.T) {
	s := openStore(t)
	ctx := seedTwoTenants(t, s)
	c, err := s.CreateCampaign(NewCampaign{TenantID: ctx.tenA.ID, Title: "collision", CreatedBy: ctx.ownA.PrincipalRef})
	must(t, err)
	first, err := s.CreateLink(ctx.tenA.ID, c.ID, ctx.ownA.PrincipalRef)
	must(t, err)

	original := newShortcode
	t.Cleanup(func() { newShortcode = original })
	calls := 0
	newShortcode = func() (string, error) {
		calls++
		if calls == 1 {
			return first.Code, nil
		}
		return "ZZZZZZZZZZZZ", nil
	}
	second, err := s.CreateLink(ctx.tenA.ID, c.ID, ctx.ownA.PrincipalRef)
	must(t, err)
	if calls != 2 {
		t.Fatalf("shortcode attempts = %d, want 2", calls)
	}
	if second.Code != "ZZZZZZZZZZZZ" || second.Code == first.Code {
		t.Fatalf("collision retry produced %+v", second)
	}
}

// HUI-2628: retry is bounded. A generator that only ever collides must return
// a real error after the cap instead of looping or reporting success.
func TestCreateLinkCollisionExhaustionReturnsError(t *testing.T) {
	s := openStore(t)
	ctx := seedTwoTenants(t, s)
	c, err := s.CreateCampaign(NewCampaign{TenantID: ctx.tenA.ID, Title: "exhaust", CreatedBy: ctx.ownA.PrincipalRef})
	must(t, err)
	first, err := s.CreateLink(ctx.tenA.ID, c.ID, ctx.ownA.PrincipalRef)
	must(t, err)

	original := newShortcode
	t.Cleanup(func() { newShortcode = original })
	newShortcode = func() (string, error) { return first.Code, nil }
	if _, err := s.CreateLink(ctx.tenA.ID, c.ID, ctx.ownA.PrincipalRef); err == nil {
		t.Fatal("exhausted collision retries must return an error")
	}
}
