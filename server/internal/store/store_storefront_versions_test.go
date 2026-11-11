package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/storefrontversion"
)

// 会让这个测试失败的生产改动：published 行被后续保存改写、指针切换后旧行状态
// 被回填、expected_revision 不匹配仍写入、重复 publish 推高修订、回滚复活已
// 撤权动作、发布/回滚改写 store_actions 行(旧参与保护被破坏)。

func newStorefrontFixture(t *testing.T) (*Store, string, string, string) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	st := New(d)
	ten, err := st.CreateTenant("首页商家")
	if err != nil {
		t.Fatalf("tenant: %v", err)
	}
	sto, err := st.CreateStore(ten.ID, "甲店", "", "usr_x")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	return st, ten.ID, sto.ID, sto.Name
}

func storefrontActions(t *testing.T, st *Store, tenantID, storeID string) map[string]string {
	t.Helper()
	_, items, err := st.ReplaceStoreActions(tenantID, storeID, 0, []StoreActionInput{
		{CatalogCode: "A01", Label: "抖音发布", TargetRef: "https://www.douyin.com/roi/poi", Enabled: true},
		{CatalogCode: "A19", Label: "美团团购", TargetRef: "https://www.meituan.com/deal", Enabled: true},
		{CatalogCode: "A08", Label: "大众点评", TargetRef: "https://www.dianping.com/shop/1", Enabled: true},
	})
	if err != nil {
		t.Fatalf("seed actions: %v", err)
	}
	byCode := map[string]string{}
	for _, it := range items {
		byCode[it.CatalogCode] = it.ID
	}
	return byCode
}

func storefrontDoc(actionIDs ...string) storefrontversion.Document {
	return storefrontversion.Document{
		Sections: []storefrontversion.Section{
			{ID: "s1", Type: storefrontversion.SectionActions, Title: "常用服务", ActionIDs: actionIDs},
		},
	}
}

func storefrontOffersDoc(offerRef string, actionIDs ...string) storefrontversion.Document {
	doc := storefrontDoc(actionIDs...)
	doc.Sections = append(doc.Sections, storefrontversion.Section{
		ID: "s2", Type: storefrontversion.SectionOffers, Title: "到店套餐", OfferRefs: []string{offerRef},
	})
	doc.OfferRefs = []string{offerRef}
	return doc
}

func TestSaveFirstDraftCreatesRevisionOneAndFreezesRefs(t *testing.T) {
	st, ten, store, _ := newStorefrontFixture(t)
	ids := storefrontActions(t, st, ten, store)
	doc := storefrontDoc(ids["A01"], ids["A19"])

	ver, err := st.SaveStorefrontDraft(ten, store, 0, doc, "owner-1")
	if err != nil {
		t.Fatalf("save draft: %v", err)
	}
	if ver.Revision != 1 || ver.Status != "draft" {
		t.Fatalf("revision/status = %d/%s, want 1/draft", ver.Revision, ver.Status)
	}
	if ver.ActionRefs[ids["A01"]] != 1 || ver.ActionRefs[ids["A19"]] != 1 {
		t.Fatalf("refs not frozen: %v", ver.ActionRefs)
	}
	if len(ver.Actions) != 2 || ver.Actions[0].Label != "抖音发布" || ver.Actions[0].SectionID != "s1" {
		t.Fatalf("snapshots wrong: %+v", ver.Actions)
	}
	if ver.StoreCode == "" {
		t.Fatalf("store_code not provisioned")
	}
}

func TestSaveDraftReplayIsIdempotentChangeBumpsRevision(t *testing.T) {
	st, ten, store, _ := newStorefrontFixture(t)
	ids := storefrontActions(t, st, ten, store)
	doc := storefrontDoc(ids["A01"])

	first, err := st.SaveStorefrontDraft(ten, store, 0, doc, "owner-1")
	if err != nil {
		t.Fatalf("save 1: %v", err)
	}
	replay, err := st.SaveStorefrontDraft(ten, store, first.Revision, doc, "owner-1")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replay.Revision != first.Revision || replay.ID != first.ID || replay.UpdatedAt != first.UpdatedAt {
		t.Fatalf("identical replay must be a no-op: %+v vs %+v", replay, first)
	}

	doc2 := storefrontDoc(ids["A01"], ids["A08"])
	second, err := st.SaveStorefrontDraft(ten, store, first.Revision, doc2, "owner-1")
	if err != nil {
		t.Fatalf("save 2: %v", err)
	}
	if second.Revision != first.Revision+1 {
		t.Fatalf("content change must bump revision: %d -> %d", first.Revision, second.Revision)
	}
}

func TestSaveDraftStaleExpectedRevisionRejected(t *testing.T) {
	st, ten, store, _ := newStorefrontFixture(t)
	ids := storefrontActions(t, st, ten, store)
	if _, err := st.SaveStorefrontDraft(ten, store, 0, storefrontDoc(ids["A01"]), "o"); err != nil {
		t.Fatalf("save 1: %v", err)
	}
	// 过期 expected = 0，当前已是 1 → 拒绝且零写入。
	if _, err := st.SaveStorefrontDraft(ten, store, 0, storefrontDoc(ids["A19"]), "o"); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale save = %v, want ErrStaleRevision", err)
	}
	ver, _, err := st.GetStorefrontDraft(ten, store)
	if err != nil || ver.Revision != 1 || len(ver.Sections[0].ActionIDs) != 1 {
		t.Fatalf("rejected save must not touch the row: %+v err %v", ver, err)
	}
}

func TestSaveDraftRejectsUnknownActionAndOfferRefs(t *testing.T) {
	st, ten, store, _ := newStorefrontFixture(t)
	ids := storefrontActions(t, st, ten, store)
	if _, err := st.SaveStorefrontDraft(ten, store, 0, storefrontDoc(ids["A01"], "sa_unknown"), "o"); err == nil {
		t.Fatalf("unknown action ref must be refused")
	}
	if _, err := st.SaveStorefrontDraft(ten, store, 0, storefrontOffersDoc("off_unknown", ids["A01"]), "o"); !errors.Is(err, ErrUnknownOfferRef) {
		t.Fatalf("unknown offer ref = %v, want ErrUnknownOfferRef", err)
	}
}

func TestPublishSwitchesPointerFreezesRowAndWritesEvent(t *testing.T) {
	st, ten, store, _ := newStorefrontFixture(t)
	ids := storefrontActions(t, st, ten, store)
	actionsBefore, _, err := st.ListStoreActions(ten, store)
	if err != nil {
		t.Fatalf("list actions: %v", err)
	}
	draft, err := st.SaveStorefrontDraft(ten, store, 0, storefrontDoc(ids["A01"], ids["A19"]), "o")
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	summary := storefrontversion.ChangeSummary(storefrontversion.Document{}, draftDocOf(draft))
	res, err := st.PublishStorefrontVersion(ten, store, draft.Revision, 0, storefrontversion.Fingerprint(summary), "o", time.Now())
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if res.Revision != 1 || res.Replayed || res.PreviousRevision != 0 {
		t.Fatalf("publish result wrong: %+v", res)
	}
	// 指针切换。
	pub, err := st.GetStorefrontPublishedByCode(draft.StoreCode)
	if err != nil || pub.Revision != 1 || pub.Status != "published" || pub.PublishedAt == "" {
		t.Fatalf("published row wrong: %+v err %v", pub, err)
	}
	// 行冻结:发布后保存草稿 → 新行 rev2,rev1 逐字段不变。
	d2, err := st.SaveStorefrontDraft(ten, store, 1, storefrontDoc(ids["A01"]), "o")
	if err != nil || d2.Revision != 2 || d2.Status != "draft" {
		t.Fatalf("next draft wrong: %+v err %v", d2, err)
	}
	still, err := st.GetStorefrontPublishedByCode(draft.StoreCode)
	if err != nil || still.Revision != 1 || len(still.Sections[0].ActionIDs) != 2 {
		t.Fatalf("published row was rewritten: %+v err %v", still, err)
	}
	// 事件审计。
	events, err := st.ListStorefrontPublishEvents(ten, store)
	if err != nil || len(events) != 1 || events[0].Action != "publish" || events[0].Revision != 1 || events[0].PreviousRevision != 0 {
		t.Fatalf("events = %+v err %v", events, err)
	}
	// 旧参与保护:发布零改写 store_actions 行。
	actionsAfter, _, err := st.ListStoreActions(ten, store)
	if err != nil {
		t.Fatalf("list actions after: %v", err)
	}
	if actionsFingerprint(actionsBefore) != actionsFingerprint(actionsAfter) {
		t.Fatalf("publish rewrote live action rows (participation snapshot protection broken)")
	}
}

func draftDocOf(v StorefrontVersion) storefrontversion.Document {
	return storefrontversion.Document{Sections: v.Sections, Actions: v.Actions, ActionRefs: v.ActionRefs,
		OfferRefs: v.OfferRefs, CampaignRefs: v.CampaignRefs, BrandRef: v.BrandRef}
}

func actionsFingerprint(items []StoreAction) string {
	var b strings.Builder
	for _, it := range items {
		b.WriteString(it.ID + it.Label + it.TargetRef + it.ContentBindingRef + timeText(it.UpdatedAt))
	}
	return b.String()
}

func timeText(s string) string { return s }

func TestPublishIdempotentReplayAndConflict(t *testing.T) {
	st, ten, store, _ := newStorefrontFixture(t)
	ids := storefrontActions(t, st, ten, store)
	draft, err := st.SaveStorefrontDraft(ten, store, 0, storefrontDoc(ids["A01"]), "o")
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	fp := fingerprintFor(t, st, ten, store, draft)
	first, err := st.PublishStorefrontVersion(ten, store, draft.Revision, 0, fp, "o", time.Now())
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	// 同请求重复:指针已等于候选 → 同一发布结果,不推修订、不重复记事件。
	replay, err := st.PublishStorefrontVersion(ten, store, draft.Revision, 0, fp, "o", time.Now())
	if err != nil || !replay.Replayed || replay.Revision != first.Revision {
		t.Fatalf("replay = %+v err %v", replay, err)
	}
	events, _ := st.ListStorefrontPublishEvents(ten, store)
	if len(events) != 1 {
		t.Fatalf("replay must not append events: %d", len(events))
	}
	// 指针漂移(他人已发布 rev2):expected_published_revision=0 → 409 语义。
	d2, err := st.SaveStorefrontDraft(ten, store, 1, storefrontDoc(ids["A19"]), "o")
	if err != nil {
		t.Fatalf("save draft2: %v", err)
	}
	if _, err := st.PublishStorefrontVersion(ten, store, 2, 1, fingerprintFor(t, st, ten, store, d2), "o", time.Now()); err != nil {
		t.Fatalf("publish 2: %v", err)
	}
	if _, err := st.PublishStorefrontVersion(ten, store, 1, 0, fp, "o", time.Now()); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale expected_published = %v, want ErrStaleRevision", err)
	}
}

// fingerprintFor is the publish confirmation fingerprint for a draft row:
// the server recomputes the diff against the CURRENTLY published version, so
// the test does the same (prev = published row doc; empty when never published).
func fingerprintFor(t *testing.T, st *Store, tenantID, storeID string, draft StorefrontVersion) string {
	t.Helper()
	sf, err := st.StorefrontForStore(tenantID, storeID)
	if err != nil {
		t.Fatalf("storefront: %v", err)
	}
	prev := storefrontversion.Document{}
	if sf.StorefrontRevision > 0 {
		row, err := st.storefrontVersionRow(tenantID, storeID, sf.StorefrontRevision)
		if err != nil {
			t.Fatalf("published row: %v", err)
		}
		prev = row.DraftDocument()
	}
	return storefrontversion.Fingerprint(storefrontversion.ChangeSummary(prev, draft.DraftDocument()))
}

func TestPublishRequiresConfirmedChangeSummary(t *testing.T) {
	st, ten, store, _ := newStorefrontFixture(t)
	ids := storefrontActions(t, st, ten, store)
	draft, err := st.SaveStorefrontDraft(ten, store, 0, storefrontDoc(ids["A01"], ids["A19"]), "o")
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	// 错误指纹(草稿在确认后变过的形状)→ 拒绝。
	if _, err := st.PublishStorefrontVersion(ten, store, draft.Revision, 0, "mismatched", "o", time.Now()); !errors.Is(err, ErrStorefrontConfirmRequired) {
		t.Fatalf("confirm check = %v, want ErrStorefrontConfirmRequired", err)
	}
	// 正确指纹 → 放行。
	summary := storefrontversion.ChangeSummary(storefrontversion.Document{}, draftDocOf(draft))
	if _, err := st.PublishStorefrontVersion(ten, store, draft.Revision, 0, storefrontversion.Fingerprint(summary), "o", time.Now()); err != nil {
		t.Fatalf("publish with right fingerprint: %v", err)
	}
}

func TestPublishCandidateMustBeLatestRevision(t *testing.T) {
	st, ten, store, _ := newStorefrontFixture(t)
	ids := storefrontActions(t, st, ten, store)
	// rev1 发布 → rev2 草稿发布 → rev3 草稿在案:候选=历史行 rev1(存在但非最新)。
	d1, err := st.SaveStorefrontDraft(ten, store, 0, storefrontDoc(ids["A01"]), "o")
	if err != nil {
		t.Fatalf("save 1: %v", err)
	}
	if _, err := st.PublishStorefrontVersion(ten, store, d1.Revision, 0, fingerprintFor(t, st, ten, store, d1), "o", time.Now()); err != nil {
		t.Fatalf("publish 1: %v", err)
	}
	d2, err := st.SaveStorefrontDraft(ten, store, 1, storefrontDoc(ids["A01"], ids["A19"]), "o")
	if err != nil {
		t.Fatalf("save 2: %v", err)
	}
	if _, err := st.PublishStorefrontVersion(ten, store, d2.Revision, 1, fingerprintFor(t, st, ten, store, d2), "o", time.Now()); err != nil {
		t.Fatalf("publish 2: %v", err)
	}
	d3, err := st.SaveStorefrontDraft(ten, store, 2, storefrontDoc(ids["A08"]), "o")
	if err != nil {
		t.Fatalf("save 3: %v", err)
	}
	if _, err := st.PublishStorefrontVersion(ten, store, 1, 2, fingerprintFor(t, st, ten, store, d3), "o", time.Now()); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("non-latest candidate = %v, want ErrStaleRevision", err)
	}
	if _, err := st.PublishStorefrontVersion(ten, store, 99, 2, "fp", "o", time.Now()); !errors.Is(err, ErrStorefrontRevisionUnknown) {
		t.Fatalf("unknown candidate = %v, want ErrStorefrontRevisionUnknown", err)
	}
}

// revokeAction simulates the platform-side revocation truth writing the live
// row (the revocation writer is an upstream/platform concern; the veto reads
// the row regardless of who set it).
func revokeAction(t *testing.T, st *Store, actionID string) {
	t.Helper()
	if _, err := st.DB.Exec(`UPDATE store_actions SET revoked=1 WHERE id=?`, actionID); err != nil {
		t.Fatalf("revoke %s: %v", actionID, err)
	}
}

func TestPublishVetoOnRevokedAction(t *testing.T) {
	st, ten, store, _ := newStorefrontFixture(t)
	ids := storefrontActions(t, st, ten, store)
	draft, err := st.SaveStorefrontDraft(ten, store, 0, storefrontDoc(ids["A01"], ids["A08"]), "o")
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	summary := storefrontversion.ChangeSummary(storefrontversion.Document{}, draftDocOf(draft))
	// 撤销 A08(平台撤权真值,模拟实时否决)。
	revokeAction(t, st, ids["A08"])
	_, err = st.PublishStorefrontVersion(ten, store, draft.Revision, 0, storefrontversion.Fingerprint(summary), "o", time.Now())
	var blocked *StorefrontBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("publish = %v, want StorefrontBlockedError", err)
	}
	if len(blocked.Actions) != 1 || blocked.Actions[0].ActionID != ids["A08"] {
		t.Fatalf("blocked = %+v, want only A08", blocked.Actions)
	}
	// 指针未动。
	if sf, err := st.StorefrontForStore(ten, store); err != nil || sf.StorefrontRevision != 0 {
		t.Fatalf("pointer moved on blocked publish: %+v err %v", sf, err)
	}
	// 排除撤权动作形成新修订 → 发布成功。
	d2, err := st.SaveStorefrontDraft(ten, store, draft.Revision, storefrontDoc(ids["A01"]), "o")
	if err != nil {
		t.Fatalf("save 2: %v", err)
	}
	summary2 := storefrontversion.ChangeSummary(storefrontversion.Document{}, draftDocOf(d2))
	if _, err := st.PublishStorefrontVersion(ten, store, d2.Revision, 0, storefrontversion.Fingerprint(summary2), "o", time.Now()); err != nil {
		t.Fatalf("publish healthy subset: %v", err)
	}
}

func TestRollbackRestoresPointerVetoesRevokedAndReplays(t *testing.T) {
	st, ten, store, _ := newStorefrontFixture(t)
	ids := storefrontActions(t, st, ten, store)
	d1, err := st.SaveStorefrontDraft(ten, store, 0, storefrontDoc(ids["A01"], ids["A19"]), "o")
	if err != nil {
		t.Fatalf("save 1: %v", err)
	}
	now := time.Now()
	if _, err := st.PublishStorefrontVersion(ten, store, d1.Revision, 0, fingerprintFor(t, st, ten, store, d1), "o", now); err != nil {
		t.Fatalf("publish 1: %v", err)
	}
	d2, err := st.SaveStorefrontDraft(ten, store, 1, storefrontDoc(ids["A01"]), "o")
	if err != nil {
		t.Fatalf("save 2: %v", err)
	}
	if _, err := st.PublishStorefrontVersion(ten, store, d2.Revision, 1, fingerprintFor(t, st, ten, store, d2), "o", now); err != nil {
		t.Fatalf("publish 2: %v", err)
	}
	// 回滚到草稿行 → 拒绝(回滚只能回到已发布版本)。
	d3, err := st.SaveStorefrontDraft(ten, store, 2, storefrontDoc(ids["A08"]), "o")
	if err != nil {
		t.Fatalf("save draft 3: %v", err)
	}
	if _, err := st.RollbackStorefrontVersion(ten, store, d3.Revision, 2, "o", now); !errors.Is(err, ErrStorefrontNotPublished) {
		t.Fatalf("rollback to draft = %v, want ErrStorefrontNotPublished", err)
	}
	// expected 漂移 → 409。
	if _, err := st.RollbackStorefrontVersion(ten, store, 1, 0, "o", now); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale rollback = %v, want ErrStaleRevision", err)
	}
	// 回滚不复活已撤权动作:撤销 rev1 引用的 A19 后,回滚被实时否决(指针未动)。
	revokeAction(t, st, ids["A19"])
	_, err = st.RollbackStorefrontVersion(ten, store, 1, 2, "o", now)
	var blocked *StorefrontBlockedError
	if !errors.As(err, &blocked) || len(blocked.Actions) != 1 || blocked.Actions[0].ActionID != ids["A19"] {
		t.Fatalf("rollback veto = %v, want blocked with A19", err)
	}
	if sf, err := st.StorefrontForStore(ten, store); err != nil || sf.StorefrontRevision != 2 {
		t.Fatalf("pointer moved on blocked rollback: %+v err %v", sf, err)
	}
	// 撤权恢复(平台侧真值变化)后回滚放行。
	unRevokeAction(t, st, ids["A19"])
	res, err := st.RollbackStorefrontVersion(ten, store, 1, 2, "o", now)
	if err != nil || res.Revision != 1 || res.PreviousRevision != 2 || res.Replayed {
		t.Fatalf("rollback = %+v err %v", res, err)
	}
	sf, err := st.StorefrontForStore(ten, store)
	if err != nil || sf.StorefrontRevision != 1 {
		t.Fatalf("pointer after rollback = %+v err %v", sf, err)
	}
	// 事件(最新在前):rollback,publish2,publish1。
	events, _ := st.ListStorefrontPublishEvents(ten, store)
	if len(events) != 3 || events[0].Action != "rollback" || events[0].Revision != 1 {
		t.Fatalf("events = %+v", events)
	}
	// 回滚重放:指针已等于目标 → 同一结果。
	replay, err := st.RollbackStorefrontVersion(ten, store, 1, 1, "o", now)
	if err != nil || !replay.Replayed || replay.Revision != 1 {
		t.Fatalf("rollback replay = %+v err %v", replay, err)
	}
	// 历史版本行未被改写(rev1 仍引用 A19;rev3 草稿在案)。
	versions, err := st.ListStorefrontVersions(ten, store)
	if err != nil || len(versions) != 3 {
		t.Fatalf("versions = %+v err %v", versions, err)
	}
	if versions[2].Revision != 1 || versions[2].ActionRefs[ids["A19"]] != 1 {
		t.Fatalf("history rewritten: %+v", versions)
	}
}

func unRevokeAction(t *testing.T, st *Store, actionID string) {
	t.Helper()
	if _, err := st.DB.Exec(`UPDATE store_actions SET revoked=0 WHERE id=?`, actionID); err != nil {
		t.Fatalf("un-revoke %s: %v", actionID, err)
	}
}

func TestGetStorefrontPublishedByCodeWithoutPublication(t *testing.T) {
	st, ten, store, _ := newStorefrontFixture(t)
	storefrontActions(t, st, ten, store)
	sf, err := st.StorefrontForStore(ten, store)
	if err != nil {
		t.Fatalf("storefront: %v", err)
	}
	if _, err := st.GetStorefrontPublishedByCode(sf.StoreCode); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unpublished storefront = %v, want ErrNotFound", err)
	}
	if _, err := st.GetStorefrontPublishedByCode("sf_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown code = %v, want ErrNotFound", err)
	}
}

func TestListStoreCampaignDealOffersByStore(t *testing.T) {
	st, ten, store, _ := newStorefrontFixture(t)
	cp, err := st.CreateCampaign(NewCampaign{TenantID: ten, StoreID: store, Title: "套餐活动", PublicContent: "c", CreatedBy: "seed"})
	if err != nil {
		t.Fatalf("campaign: %v", err)
	}
	off, err := st.CreateDealOffer(ten, NewDealOffer{CampaignID: cp.ID, Kind: DealKindExternalProduct,
		Issuer: "meituan", Provider: "meituan", ProviderOfferID: "po-1", Title: "双人套餐",
		FaceValueMinor: 8800, CreatedBy: "seed"})
	if err != nil {
		t.Fatalf("offer: %v", err)
	}
	// 其它店的活动与套餐不进本店列表。
	sto2, err := st.CreateStore(ten, "乙店", "", "usr_x")
	if err != nil {
		t.Fatalf("store 2: %v", err)
	}
	cp2, err := st.CreateCampaign(NewCampaign{TenantID: ten, StoreID: sto2.ID, Title: "别家活动", PublicContent: "c", CreatedBy: "seed"})
	if err != nil {
		t.Fatalf("campaign 2: %v", err)
	}
	if _, err := st.CreateDealOffer(ten, NewDealOffer{CampaignID: cp2.ID, Kind: DealKindMerchantBenefit,
		Issuer: "merchant", Title: "别家券", FaceValueMinor: 100, CreatedBy: "seed"}); err != nil {
		t.Fatalf("offer 2: %v", err)
	}
	got, err := st.ListStoreCampaignDealOffers(ten, store)
	if err != nil || len(got) != 1 || got[0].ID != off.ID {
		t.Fatalf("offers = %+v err %v", got, err)
	}
}

func TestSaveDraftAcceptsEmptyDocument(t *testing.T) {
	st, ten, store, _ := newStorefrontFixture(t)
	storefrontActions(t, st, ten, store)
	ver, err := st.SaveStorefrontDraft(ten, store, 0, storefrontversion.Document{}, "o")
	if err != nil {
		t.Fatalf("empty draft save: %v", err)
	}
	if ver.Revision != 1 || len(ver.Sections) != 0 {
		t.Fatalf("empty draft = %+v", ver)
	}
}
