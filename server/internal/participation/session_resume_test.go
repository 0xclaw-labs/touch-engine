package participation

// T3: 跨浏览器恢复凭据——签发/撤销/兑换。
//
// 裁定书硬性面:
//   - 高熵随机串 + 短 TTL(≤15 分钟) + 单用途 + 服务端哈希存储;
//   - 明文只在签发响应出现一次,永不进 URL 查询参数/日志;
//   - 凭据丢失不可认回:兑换只认凭据本身;
//   - 店员码(staff_verify)与参与交接凭据结构性进不了会话兑换面;
//   - 停用/撤权实时检查:签发与兑换都复核目录当前状态;
//   - 并发同兑只有一方成功;任何拒绝不烧单用途。

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// issueResume 是测试内签发助手:先开会话再给 visitor 签恢复凭据。
func issueResume(t *testing.T, s *Service, visitor string) (string, SessionResumeToken) {
	t.Helper()
	started, err := s.StartActionSession(context.Background(), sessionStart(visitor, "", ""))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	tok, err := s.IssueResumeCredential(context.Background(), ResumeCredentialInput{
		SessionID: started.Session.ID, VisitorID: visitor,
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	return started.Session.ID, tok
}

// newMutableSessionService: 时钟可拨的会话服务(过期语义测试用)。
func newMutableSessionService(t *testing.T, dir ActionDirectory) (*Service, *time.Time) {
	t.Helper()
	d := openTestDB(t)
	seedSessionRef(t, d)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	s := New(d, nil, Options{Now: func() time.Time { return now }})
	if dir != nil {
		s.SetActionDirectory(dir)
	}
	return s, &now
}

func TestIssueResumeCredentialBasics(t *testing.T) {
	ctx := context.Background()
	dir := &stubDirectory{ref: liveRef("act_001"), ok: true}
	s := newSessionService(t, dir)

	id, tok := issueResume(t, s, "visit_a")
	if tok.Token == "" || !ValidTokenShape(tok.Token) {
		t.Fatal("issued token must be present and well-formed")
	}
	if tok.Credential.SessionID != id {
		t.Fatalf("credential bound to %q, want %q", tok.Credential.SessionID, id)
	}
	// TTL 硬上限:≤ 15 分钟。
	exp, err := time.Parse(time.RFC3339, tok.Credential.ExpiresAt)
	if err != nil {
		t.Fatalf("expires_at: %v", err)
	}
	if d := exp.Sub(s.now()); d <= 0 || d > MaxResumeTTL {
		t.Fatalf("ttl = %v, want (0, %v]", d, MaxResumeTTL)
	}

	// 只有本人能签:陌生 visitor 统一 404。
	if _, err := s.IssueResumeCredential(ctx, ResumeCredentialInput{SessionID: id, VisitorID: "visit_b"}); err != ErrNotFound {
		t.Fatalf("foreign issue = %v, want ErrNotFound", err)
	}
	// 无 cookie 语义(空 visitor):同样 404。
	if _, err := s.IssueResumeCredential(ctx, ResumeCredentialInput{SessionID: id, VisitorID: ""}); err != ErrNotFound {
		t.Fatalf("empty visitor issue = %v, want ErrNotFound", err)
	}
	// 停用后不能签发新凭据(实时检查)。
	dir.ref.Enabled = false
	if _, err := s.IssueResumeCredential(ctx, ResumeCredentialInput{SessionID: id, VisitorID: "visit_a"}); err != ErrActionUnavailable {
		t.Fatalf("disabled issue = %v, want ErrActionUnavailable", err)
	}
	dir.ref.Enabled = true
}

func TestExchangeResumeCredentialHappyPath(t *testing.T) {
	ctx := context.Background()
	dir := &stubDirectory{ref: liveRef("act_001"), ok: true}
	s := newSessionService(t, dir)

	id, tok := issueResume(t, s, "visit_a")

	// 兑换:新浏览器换绑同一会话。
	res, err := s.ExchangeResumeCredential(ctx, SessionExchangeInput{Token: tok.Token, NewVisitorID: "visit_b"})
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if res.Session.ID != id {
		t.Fatalf("exchange opened %q, want same session %q", res.Session.ID, id)
	}
	if res.Session.VisitorID != "visit_b" {
		t.Fatalf("visitor = %q, want rebound to visit_b", res.Session.VisitorID)
	}
	// 快照原样保留:冻结版本不变。
	if res.Session.ActionRevision != 12 || res.Session.RuleVersion != 3 || res.Session.StorefrontRevision != 5 {
		t.Fatalf("frozen snapshot drifted: %+v", res.Session)
	}

	// 重放拒绝(单用途):第二次兑换被拒,会话仍归 visit_b。
	if _, err := s.ExchangeResumeCredential(ctx, SessionExchangeInput{Token: tok.Token, NewVisitorID: "visit_c"}); err != ErrCredentialUsed {
		t.Fatalf("replay = %v, want ErrCredentialUsed", err)
	}
	after, err := s.getSessionForVisitor(ctx, id, "visit_b")
	if err != nil {
		t.Fatalf("session after replay: %v", err)
	}
	if after.VisitorID != "visit_b" {
		t.Fatalf("replay must not rebind: visitor = %q", after.VisitorID)
	}
}

func TestExchangeResumeCredentialNegatives(t *testing.T) {
	ctx := context.Background()
	dir := &stubDirectory{ref: liveRef("act_001"), ok: true}

	// 垃圾/未知 token:统一 ErrNotFound(防枚举)。
	s := newSessionService(t, dir)
	if _, err := s.ExchangeResumeCredential(ctx, SessionExchangeInput{Token: "garbage", NewVisitorID: "v"}); err != ErrNotFound {
		t.Fatalf("garbage = %v, want ErrNotFound", err)
	}
	tokUnknown, err := NewToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if _, err := s.ExchangeResumeCredential(ctx, SessionExchangeInput{Token: tokUnknown, NewVisitorID: "v"}); err != ErrNotFound {
		t.Fatalf("unknown token = %v, want ErrNotFound", err)
	}

	// 过期:拨钟到 TTL 之后。
	sExp, now := newMutableSessionService(t, dir)
	_, tokExp := issueResume(t, sExp, "visit_a")
	*now = now.Add(MaxResumeTTL + time.Minute)
	if _, err := sExp.ExchangeResumeCredential(ctx, SessionExchangeInput{Token: tokExp.Token, NewVisitorID: "visit_b"}); err != ErrCredentialExpired {
		t.Fatalf("expired = %v, want ErrCredentialExpired", err)
	}
	// 拒绝不烧单用途:拨回后仍可兑换(只证拒绝语义,真实世界不拨钟)。
	*now = now.Add(-MaxResumeTTL - time.Minute)
	if _, err := sExp.ExchangeResumeCredential(ctx, SessionExchangeInput{Token: tokExp.Token, NewVisitorID: "visit_b"}); err != nil {
		t.Fatalf("refusal must not burn the single use: %v", err)
	}

	// 撤销后拒绝;他人不能撤他人会话的凭据。
	sRev := newSessionService(t, dir)
	idRev, tokRev := issueResume(t, sRev, "visit_a")
	if err := sRev.RevokeResumeCredential(ctx, idRev, "visit_a", tokRev.Credential.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := sRev.ExchangeResumeCredential(ctx, SessionExchangeInput{Token: tokRev.Token, NewVisitorID: "visit_b"}); err != ErrCredentialRevoked {
		t.Fatalf("revoked = %v, want ErrCredentialRevoked", err)
	}
	_, tokOther := issueResume(t, sRev, "visit_a")
	if err := sRev.RevokeResumeCredential(ctx, idRev, "visit_b", tokOther.Credential.ID); err != ErrNotFound {
		t.Fatalf("foreign revoke = %v, want ErrNotFound", err)
	}

	// 会话已取消:取消即全废——未用凭据被撤销,兑换拒 ErrCredentialRevoked;
	// 若凭据因任何路径幸存(防御纵深),兑换仍以会话终态拒绝。
	sEnd := newSessionService(t, dir)
	idEnd, tokEnd := issueResume(t, sEnd, "visit_a")
	if _, err := sEnd.CancelActionSession(ctx, idEnd, "visit_a"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := sEnd.ExchangeResumeCredential(ctx, SessionExchangeInput{Token: tokEnd.Token, NewVisitorID: "visit_b"}); err != ErrCredentialRevoked {
		t.Fatalf("canceled exchange = %v, want ErrCredentialRevoked", err)
	}
	survivor, err := NewToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if _, err := sEnd.db.Exec(`INSERT INTO action_session_credentials
		(id, tenant_id, session_id, purpose, target_scope, token_hash, expires_at, used_at, revoked_at, created_at)
		VALUES ('rsc_surv','tnt_act',?,'customer_resume','h5',?,'2026-11-10T13:00:00Z','','','2026-11-10T12:00:00Z')`,
		idEnd, HashToken(survivor)); err != nil {
		t.Fatalf("seed survivor credential: %v", err)
	}
	if _, err := sEnd.ExchangeResumeCredential(ctx, SessionExchangeInput{Token: survivor, NewVisitorID: "visit_b"}); err != ErrSessionEnded {
		t.Fatalf("ended exchange = %v, want ErrSessionEnded", err)
	}

	// 动作实时停用:兑换拒绝(快照不掩盖当前状态)。
	sOff := newSessionService(t, dir)
	_, tokOff := issueResume(t, sOff, "visit_a")
	dir.ref.Enabled = false
	if _, err := sOff.ExchangeResumeCredential(ctx, SessionExchangeInput{Token: tokOff.Token, NewVisitorID: "visit_b"}); err != ErrActionUnavailable {
		t.Fatalf("disabled exchange = %v, want ErrActionUnavailable", err)
	}
	dir.ref.Enabled = true
}

// TestExchangeRefusesParticipationAndStaffTokens: 店员码(staff_verify)与
// 参与交接凭据在 handoff_credentials(参与域),会话兑换只查
// action_session_credentials —— 结构性分权,店员码不能接管顾客会话。
func TestExchangeRefusesParticipationAndStaffTokens(t *testing.T) {
	ctx := context.Background()
	dir := &stubDirectory{ref: liveRef("act_001"), ok: true}
	s := newSessionService(t, dir)

	// 会话服务所用租户下种一个真实参与行(handoff_credentials 的 FK 所需)。
	if _, err := s.db.Exec(`INSERT INTO campaigns
		(id, tenant_id, title, status, store_id, created_at, updated_at)
		VALUES ('cmp_act','tnt_act','seed','active','sto_act','2026-11-10T00:00:00Z','2026-11-10T00:00:00Z')`); err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	if _, err := s.db.Exec(`INSERT INTO participations
		(id, tenant_id, store_id, campaign_id, link_code, touchpoint_source, rule_version,
		 content_pack_version, visitor_id, status, idempotency_key, benefit_slot,
		 benefit_reservation_ref, benefit_reservation_state, fulfillment_ref,
		 starts_at, expires_at, created_at, updated_at)
		VALUES ('pt_none','tnt_act','sto_act','cmp_act','link01','nfc',1,'','visit_a',
			'pending','','','','not_requested','','2026-11-10T00:00:00Z','2026-11-11T00:00:00Z','2026-11-10T00:00:00Z','2026-11-10T00:00:00Z')`); err != nil {
		t.Fatalf("seed participation: %v", err)
	}

	// 店员核验凭据:明文只在测试内生成,库内哈希。
	staffTok, err := NewToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if _, err := s.db.Exec(`INSERT INTO handoff_credentials
		(id, tenant_id, participation_id, attempt_id, purpose, target_scope, token_hash, expires_at, created_at)
		VALUES ('phc_staff','tnt_act','pt_none','','staff_verify','tnt_act',?,?,?)`,
		HashToken(staffTok), "2026-11-10T13:00:00Z", "2026-11-10T12:00:00Z"); err != nil {
		t.Fatalf("seed staff credential: %v", err)
	}
	if _, err := s.ExchangeResumeCredential(ctx, SessionExchangeInput{Token: staffTok, NewVisitorID: "visit_b"}); err != ErrNotFound {
		t.Fatalf("staff token on session exchange = %v, want ErrNotFound", err)
	}

	// 参与的 customer_resume 凭据同样进不了会话兑换面。
	partTok, err := NewToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if _, err := s.db.Exec(`INSERT INTO handoff_credentials
		(id, tenant_id, participation_id, attempt_id, purpose, target_scope, token_hash, expires_at, created_at)
		VALUES ('phc_cust','tnt_act','pt_none','','customer_resume','h5',?,?,?)`,
		HashToken(partTok), "2026-11-10T13:00:00Z", "2026-11-10T12:00:00Z"); err != nil {
		t.Fatalf("seed participation credential: %v", err)
	}
	if _, err := s.ExchangeResumeCredential(ctx, SessionExchangeInput{Token: partTok, NewVisitorID: "visit_b"}); err != ErrNotFound {
		t.Fatalf("participation token on session exchange = %v, want ErrNotFound", err)
	}
}

func TestExchangeConcurrentSingleWinner(t *testing.T) {
	dir := &stubDirectory{ref: liveRef("act_001"), ok: true}
	s := newSessionService(t, dir)
	_, tok := issueResume(t, s, "visit_a")

	var wg sync.WaitGroup
	results := make([]error, 2)
	bound := make([]string, 2)
	for i, visitor := range []string{"visit_b", "visit_c"} {
		wg.Add(1)
		go func(i int, visitor string) {
			defer wg.Done()
			res, err := s.ExchangeResumeCredential(context.Background(), SessionExchangeInput{Token: tok.Token, NewVisitorID: visitor})
			results[i] = err
			if err == nil {
				bound[i] = res.Session.VisitorID
			}
		}(i, visitor)
	}
	wg.Wait()
	wins := 0
	for i := range results {
		if results[i] == nil {
			wins++
			continue
		}
		if !errors.Is(results[i], ErrCredentialUsed) {
			t.Fatalf("loser error = %v, want ErrCredentialUsed", results[i])
		}
	}
	if wins != 1 {
		t.Fatalf("wins = %d, want exactly 1", wins)
	}
	for i := range bound {
		if bound[i] != "" && bound[i] != "visit_b" && bound[i] != "visit_c" {
			t.Fatalf("unexpected binding %q", bound[i])
		}
	}
}
