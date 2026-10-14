package store

// HUI-1665 增量(2026-10-14):稳定触点别名域存储层测试。
//   - 启用入口:从标签绑定 link 派生当前活动/门店,一标签一别名(重复 409 语义);
//   - 停用只关别名自身:共享 link 的其他卡零影响(票面「单卡停用不误停共享卡」);
//   - 换绑:乐观并发控制(expected 不符拒改),目标须本租户且至少一条短码;
//   - 部署五态:只许单步前进(跳级/倒退拒绝);
//   - 解析:形状非法=未知=not_found;停用=touchpoint_disabled;活动级五态交 /c;
//   - LoadTouchpointRows+DecideTouchpoint 与 ResolveTouchpoint 等价(缓存拆分)。

import (
	"errors"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/campaign"
)

func tpSeedTag(t *testing.T, s *Store, tenantID string, status string, nLinks int) (campaignID string, tagID string, links []CampaignLink) {
	t.Helper()
	cID, links := seedCampaignWithLinks(t, s, tenantID, status, nLinks)
	tags, err := s.CreateTagsBatch(NewTagBatch{TenantID: tenantID, CampaignID: cID,
		LinkIDs: []string{links[0].ID}, BindMode: "shared", Count: 1, CreatedBy: "u"})
	if err != nil {
		t.Fatalf("seed tag: %v", err)
	}
	return cID, tags[0].ID, links
}

func TestTouchpointProvisionDerivesFromTag(t *testing.T) {
	s := tagTestStore(t)
	ten, err := s.CreateTenant("A")
	mustNoErr(t, err)
	sto, err := s.CreateStore(ten.ID, "旗舰店", "", "u")
	mustNoErr(t, err)
	cID, links := seedCampaignWithLinks(t, s, ten.ID, "active", 1)
	tags, err := s.CreateTagsBatch(NewTagBatch{TenantID: ten.ID, CampaignID: cID,
		LinkIDs: []string{links[0].ID}, BindMode: "shared", Count: 1, StoreID: sto.ID, CreatedBy: "u"})
	mustNoErr(t, err)
	tagID := tags[0].ID

	a, err := s.ProvisionTouchpoint(ten.ID, tagID, "前台桌贴", "桌贴", "u1")
	mustNoErr(t, err)
	if a.ID == "" || a.Code == "" || len(a.Code) != campaign.ShortcodeLen || !campaign.ValidShortcode(a.Code) {
		t.Fatalf("alias code = %q", a.Code)
	}
	if a.CampaignID != cID || a.StoreID != sto.ID {
		t.Fatalf("derived = %+v, want campaign %s store %s", a, cID, sto.ID)
	}
	if a.Status != TagStatusActive || a.DeploymentStage != StageRegistered {
		t.Fatalf("fresh alias = %+v", a)
	}
	if a.TagID != tagID {
		t.Fatalf("tag binding = %q, want %q", a.TagID, tagID)
	}

	// 重复启用 → ErrTagAlreadyProvisioned
	if _, err := s.ProvisionTouchpoint(ten.ID, tagID, "x", "", "u1"); !errors.Is(err, ErrTagAlreadyProvisioned) {
		t.Fatalf("duplicate provision = %v, want ErrTagAlreadyProvisioned", err)
	}
	// 跨租户标签不可见
	ten2, err := s.CreateTenant("B")
	mustNoErr(t, err)
	if _, err := s.ProvisionTouchpoint(ten2.ID, tagID, "x", "", "u2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant provision = %v, want ErrNotFound", err)
	}
	// 不存在的标签
	if _, err := s.ProvisionTouchpoint(ten.ID, "nfc_missing", "x", "", "u1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing tag provision = %v, want ErrNotFound", err)
	}
	// 审计:created 事件落一条
	events, err := s.ListTouchpointEvents(ten.ID, a.ID)
	mustNoErr(t, err)
	if len(events) != 1 || events[0].Kind != "created" {
		t.Fatalf("events = %+v, want one created", events)
	}
}

func TestTouchpointDisableIsIndependentOfLink(t *testing.T) {
	s := tagTestStore(t)
	ten, err := s.CreateTenant("A")
	mustNoErr(t, err)
	cID, _, links := tpSeedTag(t, s, ten.ID, "active", 1)

	// 两张卡共享同一 link(旧模型),各自启用稳定别名
	tags, err := s.CreateTagsBatch(NewTagBatch{TenantID: ten.ID, CampaignID: cID,
		LinkIDs: []string{links[0].ID}, BindMode: "shared", Count: 2, CreatedBy: "u"})
	mustNoErr(t, err)
	a1, err := s.ProvisionTouchpoint(ten.ID, tags[0].ID, "卡1", "", "u")
	mustNoErr(t, err)
	a2, err := s.ProvisionTouchpoint(ten.ID, tags[1].ID, "卡2", "", "u")
	mustNoErr(t, err)

	// 停 A:只关 A 自己;B 的 /t 解析不受影响;底层 link enabled 不变
	if _, err := s.SetTouchpointStatus(a1.ID, ten.ID, TagStatusDisabled); err != nil {
		t.Fatalf("disable a1: %v", err)
	}
	if got := s.ResolveTouchpoint(a1.Code, time.Now()); got.Outcome != TpDisabled {
		t.Fatalf("a1 resolve = %s, want touchpoint_disabled", got.Outcome)
	}
	if got := s.ResolveTouchpoint(a2.Code, time.Now()); got.Outcome != TpRedirect {
		t.Fatalf("a2 resolve = %s, want redirect", got.Outcome)
	}
	l, err := s.GetLink(links[0].ID, ten.ID)
	mustNoErr(t, err)
	if !l.Enabled {
		t.Fatal("shared link must stay enabled after alias disable")
	}
	// 恢复
	if _, err := s.SetTouchpointStatus(a1.ID, ten.ID, TagStatusActive); err != nil {
		t.Fatalf("enable a1: %v", err)
	}
	if got := s.ResolveTouchpoint(a1.Code, time.Now()); got.Outcome != TpRedirect {
		t.Fatalf("a1 restored = %s, want redirect", got.Outcome)
	}
	// 旧标签停用语义(传播 link)不受别名影响 —— 旧路径行为逐字节不动
	if _, err := s.SetTagStatus(tags[1].ID, ten.ID, TagStatusDisabled); err != nil {
		t.Fatalf("legacy disable: %v", err)
	}
	if got := s.ResolveTouchpoint(a1.Code, time.Now()); got.Outcome != TpRedirect {
		t.Fatalf("a1 after legacy tag disable = %s, alias independent of tag status", got.Outcome)
	}
	if got := s.ResolveLink(links[0].Code, time.Now()); got.Outcome != OutcomeLinkDisabled {
		t.Fatalf("legacy path broken: %s, want link_disabled", got.Outcome)
	}
}

func TestTouchpointRebindOptimisticConcurrency(t *testing.T) {
	s := tagTestStore(t)
	ten, err := s.CreateTenant("A")
	mustNoErr(t, err)
	_, tagID, _ := tpSeedTag(t, s, ten.ID, "active", 1)
	a, err := s.ProvisionTouchpoint(ten.ID, tagID, "卡", "", "u")
	mustNoErr(t, err)

	// 目标活动:另一租户的不可见;本租户无短码的拒绝
	c2, _ := seedCampaignWithLinks(t, s, ten.ID, "active", 1)
	ten2, err := s.CreateTenant("B")
	mustNoErr(t, err)
	cB, _ := seedCampaignWithLinks(t, s, ten2.ID, "active", 1)
	if _, err := s.RebindTouchpoint(a.ID, ten.ID, cB, a.CampaignID, "u"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant rebind = %v, want ErrNotFound", err)
	}
	draftCID, _ := seedCampaignWithLinks(t, s, ten.ID, "draft", 1)
	if _, err := s.DB.Exec(`DELETE FROM campaign_links WHERE campaign_id=?`, draftCID); err != nil {
		t.Fatalf("strip link: %v", err)
	}
	if _, err := s.RebindTouchpoint(a.ID, ten.ID, draftCID, a.CampaignID, "u"); !errors.Is(err, ErrNoEntryLink) {
		t.Fatalf("no-entry rebind = %v, want ErrNoEntryLink", err)
	}

	// 正常换绑:URL(别名码)不变,目标变化,审计落 rebind
	rebound, err := s.RebindTouchpoint(a.ID, ten.ID, c2, a.CampaignID, "u1")
	mustNoErr(t, err)
	if rebound.Code != a.Code || rebound.CampaignID != c2 {
		t.Fatalf("rebound = %+v", rebound)
	}
	// 并发:expected 与当前不符 → 拒绝
	if _, err := s.RebindTouchpoint(a.ID, ten.ID, a.CampaignID, a.CampaignID, "u2"); !errors.Is(err, ErrRebindConflict) {
		t.Fatalf("stale expected = %v, want ErrRebindConflict", err)
	}
	// 同目标幂等拒绝?同目标+同 expected 属无操作,显式拒绝避免审计噪音
	if _, err := s.RebindTouchpoint(a.ID, ten.ID, c2, c2, "u1"); !errors.Is(err, ErrRebindConflict) {
		t.Fatalf("no-op rebind = %v, want ErrRebindConflict", err)
	}
	events, err := s.ListTouchpointEvents(ten.ID, a.ID)
	mustNoErr(t, err)
	if len(events) != 2 || events[0].Kind != "created" || events[1].Kind != "rebind" {
		t.Fatalf("events = %+v, want created+rebind", events)
	}
}

func TestTouchpointStageSingleStep(t *testing.T) {
	s := tagTestStore(t)
	ten, err := s.CreateTenant("A")
	mustNoErr(t, err)
	_, tagID, _ := tpSeedTag(t, s, ten.ID, "active", 1)
	a, err := s.ProvisionTouchpoint(ten.ID, tagID, "卡", "", "u")
	mustNoErr(t, err)

	steps := []string{StageAwaitingWrite, StageWrittenAwaitingReadback, StageReadbackDone, StageDeviceVerified}
	for _, next := range steps {
		got, err := s.AdvanceTouchpointStage(a.ID, ten.ID, next, "操作员登记", "op-1")
		mustNoErr(t, err)
		if got.DeploymentStage != next {
			t.Fatalf("stage = %s, want %s", got.DeploymentStage, next)
		}
	}
	// 终态再前进 → 拒绝
	if _, err := s.AdvanceTouchpointStage(a.ID, ten.ID, StageDeviceVerified, "", "op"); !errors.Is(err, ErrStageTransition) {
		t.Fatalf("past-end advance = %v, want ErrStageTransition", err)
	}
	// 跳级 → 拒绝(从 registered 直接到 readback)
	_, tagID2, _ := tpSeedTag(t, s, ten.ID, "active", 1)
	a2, err := s.ProvisionTouchpoint(ten.ID, tagID2, "卡2", "", "u")
	mustNoErr(t, err)
	if _, err := s.AdvanceTouchpointStage(a2.ID, ten.ID, StageReadbackDone, "", "op"); !errors.Is(err, ErrStageTransition) {
		t.Fatalf("skip advance = %v, want ErrStageTransition", err)
	}
	// 倒退 → 拒绝
	if _, err := s.AdvanceTouchpointStage(a.ID, ten.ID, StageRegistered, "", "op"); !errors.Is(err, ErrStageTransition) {
		t.Fatalf("rollback = %v, want ErrStageTransition", err)
	}
	// 未知值 → 拒绝
	if _, err := s.AdvanceTouchpointStage(a.ID, ten.ID, "deployed", "", "op"); !errors.Is(err, ErrStageTransition) {
		t.Fatalf("unknown stage = %v, want ErrStageTransition", err)
	}
}

func TestTouchpointResolutionStates(t *testing.T) {
	s := tagTestStore(t)
	ten, err := s.CreateTenant("A")
	mustNoErr(t, err)
	now := time.Now()

	// 形状非法 = 未知 = not_found(不可区分)
	if got := s.ResolveTouchpoint("short", now); got.Outcome != TpNotFound {
		t.Fatalf("malformed = %s", got.Outcome)
	}
	if got := s.ResolveTouchpoint("AAAAAAAAAAAAAAAAAAAA", now); got.Outcome != TpNotFound {
		t.Fatalf("unknown = %s", got.Outcome)
	}

	// paused(draft -> active -> paused 状态机合法路径)
	cPaused, _, tagIDP := func() (string, []CampaignLink, string) {
		cID, tagID, links := tpSeedTag(t, s, ten.ID, "active", 1)
		return cID, links, tagID
	}()
	if _, err := s.TransitionCampaign(cPaused, ten.ID, campaign.StatusPaused); err != nil {
		t.Fatalf("pause: %v", err)
	}
	a, err := s.ProvisionTouchpoint(ten.ID, tagIDP, "卡", "", "u")
	mustNoErr(t, err)
	if got := s.ResolveTouchpoint(a.Code, now); got.Outcome != TpRedirect || got.State != OutcomePaused {
		t.Fatalf("paused = %+v", got)
	}
	// expired(窗口已过)
	cID, links := seedCampaignWithLinks(t, s, ten.ID, "active", 1)
	if _, err := s.DB.Exec(`UPDATE campaigns SET starts_at=?, ends_at=? WHERE id=? AND tenant_id=?`,
		now.Add(-48*time.Hour).Format(time.RFC3339), now.Add(-1*time.Hour).Format(time.RFC3339), cID, ten.ID); err != nil {
		t.Fatalf("window: %v", err)
	}
	tags, err := s.CreateTagsBatch(NewTagBatch{TenantID: ten.ID, CampaignID: cID,
		LinkIDs: []string{links[0].ID}, BindMode: "shared", Count: 1, CreatedBy: "u"})
	mustNoErr(t, err)
	ae, err := s.ProvisionTouchpoint(ten.ID, tags[0].ID, "卡", "", "u")
	mustNoErr(t, err)
	if got := s.ResolveTouchpoint(ae.Code, now); got.Outcome != TpRedirect || got.State != OutcomeExpired {
		t.Fatalf("expired = %+v", got)
	}
	// ended
	_, tagID3, _ := tpSeedTag(t, s, ten.ID, "ended", 1)
	a3, err := s.ProvisionTouchpoint(ten.ID, tagID3, "卡", "", "u")
	mustNoErr(t, err)
	if got := s.ResolveTouchpoint(a3.Code, now); got.State != OutcomeEnded {
		t.Fatalf("ended = %+v", got)
	}
	// draft
	_, tagID4, _ := tpSeedTag(t, s, ten.ID, "draft", 1)
	a4, err := s.ProvisionTouchpoint(ten.ID, tagID4, "卡", "", "u")
	mustNoErr(t, err)
	if got := s.ResolveTouchpoint(a4.Code, now); got.State != OutcomeDraft {
		t.Fatalf("draft = %+v", got)
	}

	// available:redirect 带规范短码(最早启用 link)
	c5, links5 := seedCampaignWithLinks(t, s, ten.ID, "active", 2)
	tags5, err := s.CreateTagsBatch(NewTagBatch{TenantID: ten.ID, CampaignID: c5,
		LinkIDs: []string{links5[0].ID, links5[1].ID}, BindMode: "rotate", Count: 2, CreatedBy: "u"})
	mustNoErr(t, err)
	a5, err := s.ProvisionTouchpoint(ten.ID, tags5[0].ID, "卡", "", "u")
	mustNoErr(t, err)
	got := s.ResolveTouchpoint(a5.Code, now)
	if got.Outcome != TpRedirect || got.State != OutcomeAvailable || got.Code != links5[0].Code {
		t.Fatalf("available = %+v, want code %s", got, links5[0].Code)
	}
	// 最早启用 link 被停 → 取次早启用;全部停 → state=link_disabled 仍 redirect
	if _, err := s.SetLinkEnabled(links5[0].ID, ten.ID, false); err != nil {
		t.Fatalf("disable link: %v", err)
	}
	if got := s.ResolveTouchpoint(a5.Code, now); got.Code != links5[1].Code {
		t.Fatalf("fallback code = %s, want %s", got.Code, links5[1].Code)
	}
	if _, err := s.SetLinkEnabled(links5[1].ID, ten.ID, false); err != nil {
		t.Fatalf("disable link2: %v", err)
	}
	if got := s.ResolveTouchpoint(a5.Code, now); got.State != OutcomeLinkDisabled {
		t.Fatalf("all links disabled = %+v, want link_disabled", got)
	}
	// 停用别名 → touchpoint_disabled
	if _, err := s.SetTouchpointStatus(a5.ID, ten.ID, TagStatusDisabled); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if got := s.ResolveTouchpoint(a5.Code, now); got.Outcome != TpDisabled {
		t.Fatalf("disabled = %s", got.Outcome)
	}
}

func TestTouchpointRowsDecideEquivalent(t *testing.T) {
	s := tagTestStore(t)
	ten, err := s.CreateTenant("A")
	mustNoErr(t, err)
	_, tagID, _ := tpSeedTag(t, s, ten.ID, "active", 1)
	a, err := s.ProvisionTouchpoint(ten.ID, tagID, "卡", "", "u")
	mustNoErr(t, err)
	now := time.Now()
	direct := s.ResolveTouchpoint(a.Code, now)
	split := DecideTouchpoint(s.LoadTouchpointRows(a.Code), now)
	if direct.Outcome != split.Outcome || direct.Code != split.Code || direct.State != split.State {
		t.Fatalf("split mismatch: %+v vs %+v", direct, split)
	}
	// 未命中行
	if got := DecideTouchpoint(TouchpointRows{}, now); got.Outcome != TpNotFound {
		t.Fatalf("empty rows = %s", got.Outcome)
	}
}

func TestTouchpointViewAndListAndParticipationCount(t *testing.T) {
	s := tagTestStore(t)
	ten, err := s.CreateTenant("A")
	mustNoErr(t, err)
	sto, err := s.CreateStore(ten.ID, "旗舰店", "", "u")
	mustNoErr(t, err)
	ten2, err := s.CreateTenant("B")
	mustNoErr(t, err)
	nowT := time.Now()

	cID, links := seedCampaignWithLinks(t, s, ten.ID, "active", 1)
	tags, err := s.CreateTagsBatch(NewTagBatch{TenantID: ten.ID, CampaignID: cID,
		LinkIDs: []string{links[0].ID}, BindMode: "shared", Count: 2, StoreID: sto.ID, CreatedBy: "u"})
	mustNoErr(t, err)
	a1, err := s.ProvisionTouchpoint(ten.ID, tags[0].ID, "桌贴A", "桌贴", "u")
	mustNoErr(t, err)
	a2, err := s.ProvisionTouchpoint(ten.ID, tags[1].ID, "立牌B", "立牌", "u")
	mustNoErr(t, err)

	// 参与只读统计(3546:参与绑定活动,换绑预览的影响面)
	for i := 0; i < 3; i++ {
		if _, err := s.DB.Exec(`INSERT INTO participations(id,tenant_id,store_id,campaign_id,link_code,touchpoint_source,rule_version,content_pack_version,visitor_id,status,idempotency_key,starts_at,expires_at,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			"pt_"+links[0].Code+string(rune('a'+i)), ten.ID, sto.ID, cID, links[0].Code, "nfc", 1, "", "visitor-"+string(rune('a'+i)),
			"in_progress", "idem-"+string(rune('a'+i)), nowT.Format(time.RFC3339), nowT.Add(72*time.Hour).Format(time.RFC3339), nowT.Format(time.RFC3339Nano), nowT.Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("seed participation: %v", err)
		}
	}
	n, err := s.CountStartedParticipations(ten.ID, cID)
	mustNoErr(t, err)
	if n != 3 {
		t.Fatalf("count = %d, want 3", n)
	}
	if n2, err := s.CountStartedParticipations(ten2.ID, cID); err != nil || n2 != 0 {
		t.Fatalf("cross-tenant count = %d %v", n2, err)
	}

	// 视图 JOIN:目标活动标题/状态/门店名/规范码与检查态
	v, err := s.GetTouchpointView(a1.ID, ten.ID)
	mustNoErr(t, err)
	if v.CampaignTitle != "活动" || v.CampaignStatus != "active" || v.StoreName != "旗舰店" {
		t.Fatalf("view = %+v", v)
	}
	if v.EntryCode != links[0].Code || v.EntryState != string(OutcomeAvailable) {
		t.Fatalf("entry = %s/%s, want %s/%s", v.EntryCode, v.EntryState, links[0].Code, OutcomeAvailable)
	}
	if v.Position != "桌贴" {
		t.Fatalf("position = %q", v.Position)
	}

	// 列表 + 筛选
	all, err := s.ListTouchpoints(ten.ID, TouchpointFilter{})
	mustNoErr(t, err)
	if len(all) != 2 {
		t.Fatalf("list = %d, want 2", len(all))
	}
	byCampaign, err := s.ListTouchpoints(ten.ID, TouchpointFilter{CampaignID: cID})
	mustNoErr(t, err)
	if len(byCampaign) != 2 {
		t.Fatalf("byCampaign = %d", len(byCampaign))
	}
	byStore, err := s.ListTouchpoints(ten.ID, TouchpointFilter{StoreID: sto.ID})
	mustNoErr(t, err)
	if len(byStore) != 2 {
		t.Fatalf("byStore = %d", len(byStore))
	}
	if _, err := s.SetTouchpointStatus(a2.ID, ten.ID, TagStatusDisabled); err != nil {
		t.Fatalf("disable: %v", err)
	}
	disabled, err := s.ListTouchpoints(ten.ID, TouchpointFilter{Status: TagStatusDisabled})
	mustNoErr(t, err)
	if len(disabled) != 1 || disabled[0].ID != a2.ID {
		t.Fatalf("disabled list = %+v", disabled)
	}
	// 坏状态过滤拒绝
	if _, err := s.ListTouchpoints(ten.ID, TouchpointFilter{Status: "bogus"}); err == nil {
		t.Fatal("bogus status filter accepted")
	}
	// 跨租户:不可见
	other, err := s.ListTouchpoints(ten2.ID, TouchpointFilter{})
	mustNoErr(t, err)
	if len(other) != 0 {
		t.Fatalf("tenant B sees %d", len(other))
	}
	if _, err := s.GetTouchpointView(a1.ID, ten2.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant view = %v, want ErrNotFound", err)
	}
	// 换绑后列表按新活动筛
	c2, _ := seedCampaignWithLinks(t, s, ten.ID, "active", 1)
	if _, err := s.RebindTouchpoint(a1.ID, ten.ID, c2, cID, "u"); err != nil {
		t.Fatalf("rebind: %v", err)
	}
	byNew, err := s.ListTouchpoints(ten.ID, TouchpointFilter{CampaignID: c2})
	mustNoErr(t, err)
	if len(byNew) != 1 || byNew[0].ID != a1.ID {
		t.Fatalf("byNew = %+v", byNew)
	}
	if v, err := s.GetTouchpointView(a1.ID, ten.ID); err != nil || v.CampaignID != c2 {
		t.Fatalf("view after rebind = %+v %v", v, err)
	}
}
