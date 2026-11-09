package participation

// T2: 匿名动作会话——目录端口、快照冻结、创建/同浏览器恢复。
//
// 覆盖面(裁定书):
//   - 目录未接线 fail-closed;未知动作统一 404 语义;停用动作实时拒绝;
//   - 创建即冻结 storefront/action revision + 规则快照 + 内容引用;
//   - 同浏览器(同 visitor)重复碰触恢复同一会话,不建新行;
//   - 幂等键同 visitor 重放回原行,换 visitor 冲突;
//   - 无活动照建会话,绝不伪造 campaign。

import (
	"context"
	"testing"
	"time"
)

// stubDirectory: 测试夹具只进测试入口;生产目录由 HUI-4584 动作目录接线。
type stubDirectory struct {
	ref   ActionRef
	ok    bool
	calls int
}

func (d *stubDirectory) ResolveAction(_ context.Context, actionID string) (ActionRef, bool, error) {
	d.calls++
	if d.ok && d.ref.ActionID != "" && d.ref.ActionID != actionID {
		return ActionRef{}, false, nil
	}
	return d.ref, d.ok, nil
}

func liveRef(actionID string) ActionRef {
	return ActionRef{
		ActionID: actionID, CatalogCode: "A04", Platform: "wechat", Kind: "share",
		TenantID: "tnt_act", StoreID: "sto_act",
		StorefrontRevision: 5, ActionRevision: 12,
		RuleVersion: 3, RuleSnapshot: `{"v":3}`,
		ContentPackVersion: "cp_v7",
		CampaignID:         "",
		Enabled:            true,
	}
}

func newSessionService(t *testing.T, dir ActionDirectory) *Service {
	t.Helper()
	d := openTestDB(t)
	seedSessionRef(t, d)
	s := New(d, nil, Options{Now: func() time.Time {
		return time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	}})
	if dir != nil {
		s.SetActionDirectory(dir)
	}
	return s
}

func sessionStart(visitor, idem, content string) SessionStartInput {
	return SessionStartInput{
		ActionID: "act_001", Source: SourceNFC, VisitorID: visitor,
		IdempotencyKey: idem, ContentPackVersion: content,
	}
}

func TestStartActionSessionDirectoryGate(t *testing.T) {
	ctx := context.Background()

	// 目录未接线:fail-closed,不建行。
	s := newSessionService(t, nil)
	if _, err := s.StartActionSession(ctx, sessionStart("visit_a", "", "")); err != ErrDirectoryUnavailable {
		t.Fatalf("no directory = %v, want ErrDirectoryUnavailable", err)
	}

	// 未知/未发布动作:统一 ErrNotFound(防枚举)。
	unknown := &stubDirectory{ok: false}
	s = newSessionService(t, unknown)
	if _, err := s.StartActionSession(ctx, sessionStart("visit_a", "", "")); err != ErrNotFound {
		t.Fatalf("unknown action = %v, want ErrNotFound", err)
	}

	// 已停用动作:实时拒绝,快照不掩盖当前状态。
	disabled := &stubDirectory{ref: liveRef("act_001"), ok: true}
	disabled.ref.Enabled = false
	s = newSessionService(t, disabled)
	if _, err := s.StartActionSession(ctx, sessionStart("visit_a", "", "")); err != ErrActionUnavailable {
		t.Fatalf("disabled action = %v, want ErrActionUnavailable", err)
	}
}

func TestStartActionSessionFreezesSnapshotAndRestores(t *testing.T) {
	ctx := context.Background()
	dir := &stubDirectory{ref: liveRef("act_001"), ok: true}
	s := newSessionService(t, dir)

	started, err := s.StartActionSession(ctx, sessionStart("visit_a", "", ""))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !started.Created {
		t.Fatal("first start must create")
	}
	sess := started.Session
	if sess.ID == "" || sess.Status != SessionActive {
		t.Fatalf("session = %+v", sess)
	}
	// 快照冻结:目录当前值逐字段落行。
	if sess.StorefrontRevision != 5 || sess.ActionRevision != 12 || sess.RuleVersion != 3 ||
		sess.RuleSnapshot != `{"v":3}` || sess.CatalogCode != "A04" || sess.Platform != "wechat" {
		t.Fatalf("frozen snapshot wrong: %+v", sess)
	}
	// 无顾客选择时落目录已采用版本;无活动动作 campaign 恒空。
	if sess.ContentPackVersion != "cp_v7" {
		t.Fatalf("content = %q, want adopted cp_v7", sess.ContentPackVersion)
	}

	// 同浏览器重复碰触:恢复同一会话,不建新行。
	again, err := s.StartActionSession(ctx, sessionStart("visit_a", "", ""))
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if again.Created || again.Session.ID != sess.ID {
		t.Fatalf("restore = created=%v id=%v, want resumed %v", again.Created, again.Session.ID, sess.ID)
	}

	// 换浏览器:是新会话,不是同一行。
	other, err := s.StartActionSession(ctx, sessionStart("visit_b", "", ""))
	if err != nil {
		t.Fatalf("second browser: %v", err)
	}
	if !other.Created || other.Session.ID == sess.ID {
		t.Fatal("another browser must open its own session")
	}

	// 事件:created + resumed 各留审计。
	n, err := s.countSessionEvents(sess.ID)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if n < 2 {
		t.Fatalf("events = %d, want created+resumed", n)
	}
}

func TestStartActionSessionIdempotencyMatrix(t *testing.T) {
	ctx := context.Background()
	dir := &stubDirectory{ref: liveRef("act_001"), ok: true}
	s := newSessionService(t, dir)

	first, err := s.StartActionSession(ctx, sessionStart("visit_a", "idem_1", ""))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	// 同键同 visitor:重放回原行。
	replay, err := s.StartActionSession(ctx, sessionStart("visit_a", "idem_1", ""))
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replay.Created || replay.Session.ID != first.Session.ID {
		t.Fatal("same key same visitor must replay the original row")
	}
	// 同键换 visitor:冲突,不向陌生 visitor 泄漏。
	if _, err := s.StartActionSession(ctx, sessionStart("visit_b", "idem_1", "")); err != ErrIdempotencyConflict {
		t.Fatalf("cross-visitor replay = %v, want ErrIdempotencyConflict", err)
	}
}

func TestStartActionSessionStaleExpectation(t *testing.T) {
	ctx := context.Background()
	dir := &stubDirectory{ref: liveRef("act_001"), ok: true}
	s := newSessionService(t, dir)

	// 期望版本只是期望值:与服务器不符 → stale_version。
	stale := sessionStart("visit_a", "", "")
	stale.ExpectedActionRevision = 11
	if _, err := s.StartActionSession(ctx, stale); err != ErrStaleVersion {
		t.Fatalf("stale action expectation = %v, want ErrStaleVersion", err)
	}
	stale2 := sessionStart("visit_a", "", "")
	stale2.ExpectedStorefrontRevision = 4
	if _, err := s.StartActionSession(ctx, stale2); err != ErrStaleVersion {
		t.Fatalf("stale storefront expectation = %v, want ErrStaleVersion", err)
	}
	// 相符或未声明(0)→ 放行。
	okIn := sessionStart("visit_a", "", "")
	okIn.ExpectedActionRevision, okIn.ExpectedStorefrontRevision = 12, 5
	if _, err := s.StartActionSession(ctx, okIn); err != nil {
		t.Fatalf("matching expectation: %v", err)
	}
}

func TestStartActionSessionWithoutCampaignIsLegit(t *testing.T) {
	ctx := context.Background()
	ref := liveRef("act_001")
	ref.CampaignID = "" // 纯门店服务动作:无活动
	dir := &stubDirectory{ref: ref, ok: true}
	s := newSessionService(t, dir)

	started, err := s.StartActionSession(ctx, sessionStart("visit_a", "", ""))
	if err != nil {
		t.Fatalf("no-campaign start: %v", err)
	}
	if started.Session.ParticipationID != "" {
		t.Fatal("no-campaign session must never carry a participation")
	}
	// 顾客自选内容覆盖默认(预览期换选是顾客权利;权威校验在内容服务/交接面)。
	picked, err := s.StartActionSession(ctx, func() SessionStartInput {
		in := sessionStart("visit_b", "", "cp_customer_pick")
		return in
	}())
	if err != nil || picked.Session.ContentPackVersion != "cp_customer_pick" {
		t.Fatalf("customer pick = %v %q", err, picked.Session.ContentPackVersion)
	}
}

func TestUpdateActionSessionContent(t *testing.T) {
	ctx := context.Background()
	dir := &stubDirectory{ref: liveRef("act_001"), ok: true}
	s := newSessionService(t, dir)

	started, err := s.StartActionSession(ctx, sessionStart("visit_a", "", ""))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	id := started.Session.ID

	// 本人换选:ok + 审计。
	up, err := s.UpdateActionSessionContent(ctx, id, "visit_a", "cp_v9")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if up.ContentPackVersion != "cp_v9" {
		t.Fatalf("content = %q, want cp_v9", up.ContentPackVersion)
	}

	// 陌生 visitor:统一 404。
	if _, err := s.UpdateActionSessionContent(ctx, id, "visit_b", "cp_v9"); err != ErrNotFound {
		t.Fatalf("foreign visitor = %v, want ErrNotFound", err)
	}

	// 动作实时停用:换选拒绝(实时检查,非快照)。
	dir.ok, dir.ref.Enabled = true, false
	if _, err := s.UpdateActionSessionContent(ctx, id, "visit_a", "cp_v10"); err != ErrActionUnavailable {
		t.Fatalf("disabled update = %v, want ErrActionUnavailable", err)
	}
	dir.ref.Enabled = true

	// 已结束会话:不可再换。
	if _, err := s.CancelActionSession(ctx, id, "visit_a"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := s.UpdateActionSessionContent(ctx, id, "visit_a", "cp_v11"); err != ErrSessionEnded {
		t.Fatalf("ended update = %v, want ErrSessionEnded", err)
	}
}
