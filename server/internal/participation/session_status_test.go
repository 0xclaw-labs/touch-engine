package participation

// T4: 会话状态视图(只读)/升级参与/取消/清理。
//
// 裁定书硬性面:
//   - GET status 只读:进度/待处理/权益引用;动作停用后旧会话仍可读
//     (旧权益保护),resume_available=false 如实反映;
//   - 升级 participation 只在显式办理权益时发生:无活动拒绝(campaign_required),
//     活动窗口过期走既有 ErrFrozen;rule_version 用服务器实时值(客户端值
//     只是期望);幂等重复升级回同一参与;预约经受信端口,nil → not_requested;
//   - 清理:过期凭据过宽限删除;ended 会话过保留窗匿名 visitor。

import (
	"context"
	"testing"
	"time"
)

func TestSessionStatusView(t *testing.T) {
	ctx := context.Background()
	dir := &stubDirectory{ref: liveRef("act_001"), ok: true}
	s := newSessionService(t, dir)

	started, err := s.StartActionSession(ctx, sessionStart("visit_a", "", ""))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	id := started.Session.ID

	view, err := s.ActionSessionStatus(ctx, id, "visit_a")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if view.Session.ID != id || view.Session.Status != SessionActive {
		t.Fatalf("view session = %+v", view.Session)
	}
	if !view.ActionAvailable || !view.ResumeAvailable {
		t.Fatalf("live flags = %v/%v, want true/true", view.ActionAvailable, view.ResumeAvailable)
	}
	if view.Participation != nil {
		t.Fatal("no participation attached yet")
	}

	// 停用后:仍可读(旧权益保护),resume_available 如实变 false。
	dir.ref.Enabled = false
	view, err = s.ActionSessionStatus(ctx, id, "visit_a")
	if err != nil {
		t.Fatalf("status while disabled: %v", err)
	}
	if view.ActionAvailable || view.ResumeAvailable {
		t.Fatal("disabled action must answer false live flags without blocking the read")
	}
	dir.ref.Enabled = true

	// 陌生 visitor:统一 404。
	if _, err := s.ActionSessionStatus(ctx, id, "visit_b"); err != ErrNotFound {
		t.Fatalf("foreign status = %v, want ErrNotFound", err)
	}
}

func TestAttachParticipationUpgrade(t *testing.T) {
	ctx := context.Background()
	dir := &stubDirectory{ref: liveRef("act_001"), ok: true}
	dir.ref.CampaignID = "cmp_act"
	dir.ref.Window = Window{StartsAt: "2026-11-10T00:00:00Z", ExpiresAt: "2026-11-20T00:00:00Z"}
	s := newSessionService(t, dir)
	// 升级建 participation 需要真实 campaign 行(参与表 FK);目录引用它。
	if _, err := s.db.Exec(`INSERT INTO campaigns
		(id, tenant_id, title, status, store_id, created_at, updated_at)
		VALUES ('cmp_act','tnt_act','seed','active','sto_act','2026-11-10T00:00:00Z','2026-11-10T00:00:00Z')`); err != nil {
		t.Fatalf("seed campaign: %v", err)
	}

	// 无活动的动作拒绝升级:绝不伪造 campaign。
	noCamp := &stubDirectory{ref: liveRef("act_001"), ok: true}
	s2 := newSessionService(t, noCamp)
	st2, err := s2.StartActionSession(ctx, sessionStart("visit_z", "", ""))
	if err != nil {
		t.Fatalf("start(no campaign): %v", err)
	}
	if _, err := s2.AttachParticipation(ctx, AttachInput{
		SessionID: st2.Session.ID, VisitorID: "visit_z", Source: SourceQR,
	}); err != ErrCampaignRequired {
		t.Fatalf("no-campaign attach = %v, want ErrCampaignRequired", err)
	}

	started, err := s.StartActionSession(ctx, sessionStart("visit_a", "", ""))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	id := started.Session.ID

	// 显式升级:建 participation(既有语义),会话关联同一参与。
	att, err := s.AttachParticipation(ctx, AttachInput{
		SessionID: id, VisitorID: "visit_a", Source: SourceQR,
	})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if att.Session.ParticipationID == "" || att.Participation.ID == "" {
		t.Fatalf("attach = %+v", att)
	}
	if att.Participation.Status != StatusPending {
		t.Fatalf("participation status = %q, want pending", att.Participation.Status)
	}
	// rule_version 用服务器实时值(目录 3),不是客户端期望。
	if att.Participation.RuleVersion != 3 {
		t.Fatalf("rule version = %d, want server-confirmed 3", att.Participation.RuleVersion)
	}
	// BenefitReserver 为 nil:not_requested,绝不伪报已保留。
	if att.Participation.ReservationState != ReservationNotRequested {
		t.Fatalf("reservation = %q, want not_requested (nil reserver)", att.Participation.ReservationState)
	}
	if att.Participation.CampaignID != "cmp_act" {
		t.Fatalf("campaign = %q, want live directory value", att.Participation.CampaignID)
	}

	// 幂等重复升级:回同一参与,不建第二行。
	again, err := s.AttachParticipation(ctx, AttachInput{
		SessionID: id, VisitorID: "visit_a", Source: SourceQR,
	})
	if err != nil {
		t.Fatalf("attach again: %v", err)
	}
	if again.Participation.ID != att.Participation.ID {
		t.Fatalf("second attach built %q, want same %q", again.Participation.ID, att.Participation.ID)
	}

	// 陌生 visitor:统一 404。
	if _, err := s.AttachParticipation(ctx, AttachInput{
		SessionID: id, VisitorID: "visit_b", Source: SourceQR,
	}); err != ErrNotFound {
		t.Fatalf("foreign attach = %v, want ErrNotFound", err)
	}

	// 状态视图现在携带权益引用(只读)。
	view, err := s.ActionSessionStatus(ctx, id, "visit_a")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if view.Participation == nil || view.Participation.ID != att.Participation.ID ||
		view.Participation.Status != string(StatusPending) ||
		view.Participation.BenefitReservationState != string(ReservationNotRequested) {
		t.Fatalf("status participation ref = %+v", view.Participation)
	}

	// 活动窗口过期:升级走既有 ErrFrozen。
	dir.ref.Window = Window{StartsAt: "2026-11-01T00:00:00Z", ExpiresAt: "2026-11-02T00:00:00Z"}
	fresh, err := s.StartActionSession(ctx, func() SessionStartInput {
		in := sessionStart("visit_c", "", "")
		return in
	}())
	if err != nil {
		t.Fatalf("start expired window: %v", err)
	}
	if _, err := s.AttachParticipation(ctx, AttachInput{
		SessionID: fresh.Session.ID, VisitorID: "visit_c", Source: SourceQR,
	}); err != ErrFrozen {
		t.Fatalf("expired attach = %v, want ErrFrozen", err)
	}
}

func TestCancelActionSessionRevokesCredentials(t *testing.T) {
	ctx := context.Background()
	dir := &stubDirectory{ref: liveRef("act_001"), ok: true}
	s := newSessionService(t, dir)

	id, tok := issueResume(t, s, "visit_a")
	if _, err := s.CancelActionSession(ctx, id, "visit_a"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	// 取消后凭据全废(已在 T3 覆盖兑换拒绝),这里补重复取消幂等。
	if _, err := s.CancelActionSession(ctx, id, "visit_a"); err != nil {
		t.Fatalf("idempotent cancel: %v", err)
	}
	_ = tok
}

func TestCleanupSessions(t *testing.T) {
	ctx := context.Background()
	dir := &stubDirectory{ref: liveRef("act_001"), ok: true}
	s := newSessionService(t, dir)

	id, tok := issueResume(t, s, "visit_a")
	now := s.now()

	// 宽限期内:过期凭据保留(争议窗口)。
	anonymized, deleted, err := s.CleanupSessions(ctx, now.Add(MaxResumeTTL+time.Hour), 90*24*time.Hour)
	if err != nil {
		t.Fatalf("cleanup within grace: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("deleted = %d, want 0 within grace", deleted)
	}
	_ = anonymized

	// 过宽限期:过期凭据删除;ended 会话过保留窗匿名。
	if _, err := s.CancelActionSession(ctx, id, "visit_a"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	_ = tok
	anonymized, deleted, err = s.CleanupSessions(ctx, now.Add(MaxResumeTTL+30*time.Hour), 24*time.Hour)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1 (expired+graced credential)", deleted)
	}
	if anonymized != 1 {
		t.Fatalf("anonymized = %d, want 1 (ended session past retention)", anonymized)
	}
	// 匿名后:原 visitor 不再能读到(全部凭据丢失不可认回的落库体现)。
	if _, err := s.ActionSessionStatus(ctx, id, "visit_a"); err != ErrNotFound {
		t.Fatalf("status after anonymize = %v, want ErrNotFound", err)
	}
}
