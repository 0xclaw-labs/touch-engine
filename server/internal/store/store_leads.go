// store_leads.go: HUI-1747 T1 lead capture persistence (additive to T0).
//
// 纪律:
//   - SubmitLead 在**同一事务**内写入线索行与源 outbox 行(原子提交契约);
//     outbox 是转发器的唯一事实来源,不存在"线索落库但事件缺席"的状态。
//   - dedup_key UNIQUE 保证同 (活动,手机号指纹,单位时间) 只有一个逻辑 submission。
//   - 同步状态迁移全部走 compare-and-set(WHERE 限定来源态),撤销是终态。
//   - 本文件的查询不返回任何联系方式给公开面;公开响应形状由 httpapi 白名单决定。
package store

import (
	"database/sql"
	"errors"
	"strings"
)

// ---- lead forms (挂载点) ------------------------------------------------------

type LeadForm struct {
	ID                    string `json:"id"`
	TenantID              string `json:"tenant_id"`
	CampaignID            string `json:"campaign_id"`
	NoticeVersion         string `json:"notice_version"`
	MarketingOptinEnabled bool   `json:"marketing_optin_enabled"`
	Enabled               bool   `json:"enabled"`
	CreatedBy             string `json:"created_by"`
	CreatedAt             string `json:"created_at"`
	UpdatedAt             string `json:"updated_at"`
}

const leadFormCols = `id,tenant_id,campaign_id,notice_version,marketing_optin_enabled,enabled,created_by,created_at,updated_at`

func scanLeadForm(sc interface{ Scan(...any) error }) (LeadForm, error) {
	var f LeadForm
	var mkt, enabled int
	err := sc.Scan(&f.ID, &f.TenantID, &f.CampaignID, &f.NoticeVersion, &mkt, &enabled, &f.CreatedBy, &f.CreatedAt, &f.UpdatedAt)
	if err != nil {
		return LeadForm{}, err
	}
	f.MarketingOptinEnabled = mkt == 1
	f.Enabled = enabled == 1
	return f, nil
}

// UpsertLeadForm enables/configures the (single) fixed light form of a campaign.
func (s *Store) UpsertLeadForm(tenantID, campaignID, noticeVersion string, marketingEnabled bool, createdBy string) (LeadForm, error) {
	cur, err := scanLeadForm(s.DB.QueryRow(`SELECT `+leadFormCols+` FROM lead_forms WHERE campaign_id=?`, campaignID))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		f := LeadForm{ID: newID("lfrm_"), TenantID: tenantID, CampaignID: campaignID,
			NoticeVersion: noticeVersion, MarketingOptinEnabled: marketingEnabled, Enabled: true,
			CreatedBy: createdBy, CreatedAt: now(), UpdatedAt: now()}
		_, ierr := s.DB.Exec(
			`INSERT INTO lead_forms(`+leadFormCols+`) VALUES(?,?,?,?,?,?,?,?,?)`,
			f.ID, f.TenantID, f.CampaignID, f.NoticeVersion, boolInt(f.MarketingOptinEnabled),
			boolInt(f.Enabled), f.CreatedBy, f.CreatedAt, f.UpdatedAt)
		if ierr != nil {
			return LeadForm{}, ierr
		}
		s.bumpEpoch()
		return f, nil
	case err != nil:
		return LeadForm{}, err
	}
	if cur.TenantID != tenantID {
		return LeadForm{}, ErrNotFound
	}
	cur.NoticeVersion = noticeVersion
	cur.MarketingOptinEnabled = marketingEnabled
	cur.Enabled = true
	cur.UpdatedAt = now()
	_, err = s.DB.Exec(`UPDATE lead_forms SET notice_version=?,marketing_optin_enabled=?,enabled=?,updated_at=? WHERE id=?`,
		cur.NoticeVersion, boolInt(cur.MarketingOptinEnabled), boolInt(cur.Enabled), cur.UpdatedAt, cur.ID)
	if err == nil {
		s.bumpEpoch()
	}
	return cur, err
}

// GetLeadFormByCampaign is the public-route lookup: the campaign row has
// already been resolved through the controlled short-code mapping, so no
// tenant parameter is needed (and none is trusted from the guest).
func (s *Store) GetLeadFormByCampaign(campaignID string) (LeadForm, error) {
	f, err := scanLeadForm(s.DB.QueryRow(`SELECT `+leadFormCols+` FROM lead_forms WHERE campaign_id=?`, campaignID))
	if errors.Is(err, sql.ErrNoRows) {
		return LeadForm{}, ErrNotFound
	}
	return f, err
}

// ---- lead submissions ---------------------------------------------------------

type LeadSubmission struct {
	ID                   string `json:"id"`
	TenantID             string `json:"tenant_id"`
	CampaignID           string `json:"campaign_id"`
	StoreID              string `json:"store_id"`
	LinkID               string `json:"link_id"`
	FormID               string `json:"form_id"`
	SubmissionRef        string `json:"submission_ref"`
	Name                 string `json:"name"`
	Phone                string `json:"phone"`
	Wechat               string `json:"wechat,omitempty"`
	MarketingOptin       bool   `json:"marketing_optin"`
	ConsentNoticeVersion string `json:"consent_notice_version"`
	ConsentAt            string `json:"consent_at"`
	SyncState            string `json:"sync_state"`
	SyncError            string `json:"sync_error,omitempty"`
	Attempts             int    `json:"attempts"`
	SourceVersion        int    `json:"source_version"`
	RevokedAt            string `json:"revoked_at,omitempty"`
	CreatedAt            string `json:"created_at"`
	UpdatedAt            string `json:"updated_at"`
}

const leadCols = `id,tenant_id,campaign_id,store_id,link_id,form_id,submission_ref,name,phone,wechat,` +
	`marketing_optin,consent_notice_version,consent_at,sync_state,sync_error,attempts,source_version,revoked_at,created_at,updated_at`

func scanLead(sc interface{ Scan(...any) error }) (LeadSubmission, error) {
	var l LeadSubmission
	var wechat, syncErr, revokedAt sql.NullString
	var mkt int
	err := sc.Scan(&l.ID, &l.TenantID, &l.CampaignID, &l.StoreID, &l.LinkID, &l.FormID, &l.SubmissionRef,
		&l.Name, &l.Phone, &wechat, &mkt, &l.ConsentNoticeVersion, &l.ConsentAt,
		&l.SyncState, &syncErr, &l.Attempts, &l.SourceVersion, &revokedAt, &l.CreatedAt, &l.UpdatedAt)
	if err != nil {
		return LeadSubmission{}, err
	}
	l.Wechat, l.SyncError, l.RevokedAt = wechat.String, syncErr.String, revokedAt.String
	l.MarketingOptin = mkt == 1
	return l, nil
}

const leadColsPlaceholders = `id,tenant_id,campaign_id,store_id,link_id,form_id,submission_ref,dedup_key,name,phone,wechat,
	marketing_optin,consent_notice_version,consent_at,consent_ip_fp,sync_state,source_version,created_at,updated_at`

// NewLeadSubmission carries everything SubmitLead writes in ONE transaction:
// the lead row and its outbox event (payload pre-built by the caller).
type NewLeadSubmission struct {
	TenantID       string
	CampaignID     string
	StoreID        string
	LinkID         string
	FormID         string
	SubmissionRef  string
	DedupKey       string
	Name           string
	Phone          string
	Wechat         string
	MarketingOptin bool
	NoticeVersion  string
	ConsentAt      string
	ConsentIPFP    string // fingerprint of the submitting client IP (rate-limit/audit), not raw IP
	OutboxEventID  string
	OutboxPayload  string // directed-event envelope JSON (reference-only, PII-scanned upstream)
}

// SubmitLead inserts lead + outbox atomically. When dedup_key already exists it
// returns (existing row, true /*duplicate*/, nil) and writes nothing.
func (s *Store) SubmitLead(n NewLeadSubmission) (LeadSubmission, bool, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return LeadSubmission{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	l := LeadSubmission{
		ID: newID("lead_"), TenantID: n.TenantID, CampaignID: n.CampaignID,
		StoreID: n.StoreID, LinkID: n.LinkID, FormID: n.FormID,
		SubmissionRef: n.SubmissionRef, Name: n.Name, Phone: n.Phone, Wechat: n.Wechat,
		MarketingOptin: n.MarketingOptin, ConsentNoticeVersion: n.NoticeVersion,
		ConsentAt: n.ConsentAt, SyncState: "accepted", SourceVersion: 1,
		CreatedAt: now(), UpdatedAt: now(),
	}
	_, err = tx.Exec(
		`INSERT INTO lead_submissions(`+leadColsPlaceholders+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		l.ID, l.TenantID, l.CampaignID, n.StoreID, n.LinkID, n.FormID,
		l.SubmissionRef, n.DedupKey, l.Name, l.Phone, n.Wechat, boolInt(l.MarketingOptin),
		l.ConsentNoticeVersion, l.ConsentAt, n.ConsentIPFP, l.SyncState,
		l.SourceVersion, l.CreatedAt, l.UpdatedAt)
	if err != nil {
		// The tx holds the single sqlite connection: it MUST be released before
		// any pool-level lookup, or the process deadlocks (MaxOpenConns=1).
		_ = tx.Rollback()
		if isUniqueViolation(err, "dedup_key") {
			if existing, gerr := s.leadByDedupKey(n.DedupKey); gerr == nil {
				return existing, true, nil
			}
		}
		return LeadSubmission{}, false, err
	}
	if _, err = tx.Exec(
		`INSERT INTO leads_outbox(event_id,submission_ref,kind,payload_json,forwarded,created_at,updated_at)
		 VALUES(?,?,?, ?,0,?,?)`,
		n.OutboxEventID, l.SubmissionRef, "submit", n.OutboxPayload, l.CreatedAt, l.UpdatedAt); err != nil {
		return LeadSubmission{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return LeadSubmission{}, false, err
	}
	return l, false, nil
}

func (s *Store) leadByDedupKey(key string) (LeadSubmission, error) {
	l, err := scanLead(s.DB.QueryRow(`SELECT `+leadCols+` FROM lead_submissions WHERE dedup_key=?`, key))
	if errors.Is(err, sql.ErrNoRows) {
		return LeadSubmission{}, ErrNotFound
	}
	return l, err
}

// GetLeadSubmissionByDedupKey is the exported replay probe (HUI-1676 FEAT-0177):
// the submission-frequency control must short-circuit idempotent replays BEFORE
// evaluating/counting them. Not found = first submission of the (campaign,
// contact, UTC day) key.
func (s *Store) GetLeadSubmissionByDedupKey(key string) (LeadSubmission, error) {
	return s.leadByDedupKey(key)
}

// CountContactSubmissionsOnDay counts the contact's lead rows inside one UTC
// day across the tenant (HUI-1676 FEAT-0177 frequency-control counter). The
// phone is a bound parameter only — it never reaches logs or URLs. created_at
// is always RFC3339 UTC, so the first 10 characters are the UTC date.
func (s *Store) CountContactSubmissionsOnDay(tenantID, phone, utcDay string) (int, error) {
	var n int
	err := s.DB.QueryRow(
		`SELECT COUNT(1) FROM lead_submissions WHERE tenant_id=? AND phone=? AND substr(created_at,1,10)=?`,
		tenantID, phone, utcDay).Scan(&n)
	return n, err
}

// isUniqueViolation reports whether err is a sqlite UNIQUE constraint failure
// on the given column (modernc/sqlite text format).
func isUniqueViolation(err error, column string) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") && strings.Contains(msg, column)
}

func (s *Store) GetLeadSubmissionByRef(ref string) (LeadSubmission, error) {
	l, err := scanLead(s.DB.QueryRow(`SELECT `+leadCols+` FROM lead_submissions WHERE submission_ref=?`, ref))
	if errors.Is(err, sql.ErrNoRows) {
		return LeadSubmission{}, ErrNotFound
	}
	return l, err
}

// ListLeadSubmissionsByState is forwarder-facing: the outbox is this process's
// own queue, so it is not tenant-scoped (each row carries its tenant for audit).
func (s *Store) ListLeadSubmissionsByState(state string, limit int) ([]LeadSubmission, error) {
	rows, err := s.DB.Query(
		`SELECT `+leadCols+` FROM lead_submissions WHERE sync_state=? ORDER BY created_at LIMIT ?`,
		state, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]LeadSubmission, 0)
	for rows.Next() {
		l, err := scanLead(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ListLeadSubmissions is tenant+campaign scoped (admin surface only).
func (s *Store) ListLeadSubmissions(tenantID, campaignID string) ([]LeadSubmission, error) {
	rows, err := s.DB.Query(
		`SELECT `+leadCols+` FROM lead_submissions WHERE tenant_id=? AND campaign_id=? ORDER BY created_at DESC`,
		tenantID, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]LeadSubmission, 0)
	for rows.Next() {
		l, err := scanLead(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// LeadStateCount is a tenant summary bucket. It deliberately has no contact
// columns: the workbench must not load names or phones to draw a card.
type LeadStateCount struct {
	CampaignID string
	SyncState  string
	Count      int
}

func (s *Store) CountLeadStates(tenantID string) ([]LeadStateCount, error) {
	rows, err := s.DB.Query(
		`SELECT campaign_id, sync_state, COUNT(1) FROM lead_submissions WHERE tenant_id=? GROUP BY campaign_id, sync_state`,
		tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]LeadStateCount, 0)
	for rows.Next() {
		var row LeadStateCount
		if err := rows.Scan(&row.CampaignID, &row.SyncState, &row.Count); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// transitionLead is the compare-and-set core of the sync state machine.
// no-op when the row already sits in the target state (idempotent), conflict
// when it sits anywhere else.
func (s *Store) transitionLead(ref string, from []string, to, syncErr string) error {
	args := []any{to, syncErr, now(), ref}
	conds := ""
	for i, f := range from {
		if i > 0 {
			conds += ` OR `
		}
		conds += `sync_state=?`
		args = append(args, f)
	}
	res, err := s.DB.Exec(
		`UPDATE lead_submissions SET sync_state=?,sync_error=?,updated_at=? WHERE submission_ref=? AND (`+conds+`)`,
		args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return nil
	}
	cur, gerr := s.GetLeadSubmissionByRef(ref)
	if gerr != nil {
		return ErrNotFound
	}
	if cur.SyncState == to {
		return nil // already there: idempotent
	}
	return errors.New("store: lead sync state conflict " + cur.SyncState + "->" + to)
}

// MarkLeadSyncPending: accepted -> pending_sync (notify accepted the event).
func (s *Store) MarkLeadSyncPending(ref string) error {
	return s.transitionLead(ref, []string{"accepted", "pending_sync"}, "pending_sync", "")
}

// MarkLeadDelivered: pending_sync -> crm_received when deliveries report
// delivered. A row already closed as rejected can still be repaired when a
// later read of the same notify event says delivered. It never invents receipt.
func (s *Store) MarkLeadDelivered(ref string) error {
	return s.transitionLead(ref, []string{"pending_sync", "rejected", "crm_received"}, "crm_received", "")
}

// MarkLeadRejected: target refused (dead letter / publish rejection).
func (s *Store) MarkLeadRejected(ref, reason string) error {
	return s.transitionLead(ref, []string{"accepted", "pending_sync"}, "rejected", reason)
}

// RecordLeadSyncError keeps the row in its current (live) state but remembers
// the transport failure and bumps the attempt counter.
func (s *Store) RecordLeadSyncError(ref, msg string) error {
	_, err := s.DB.Exec(
		`UPDATE lead_submissions SET sync_error=?,attempts=attempts+1,updated_at=? WHERE submission_ref=? AND sync_state IN ('accepted','pending_sync')`,
		msg, now(), ref)
	return err
}

// RevokeLeadSubmission flips a live or rejected submission to revoked and,
// when the submit fact was already handed to notify (forwarded=1), enqueues the
// revocation/stop-marketing event in the SAME transaction so the target
// converges even if the original event is still in flight. When the fact was
// never forwarded, the pending outbox row is suppressed instead (never sent).
func (s *Store) RevokeLeadSubmission(ref, revokeEventID, revokePayload string) (LeadSubmission, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return LeadSubmission{}, err
	}
	defer func() { _ = tx.Rollback() }()
	cur, err := scanLead(tx.QueryRow(`SELECT `+leadCols+` FROM lead_submissions WHERE submission_ref=?`, ref))
	if errors.Is(err, sql.ErrNoRows) {
		return LeadSubmission{}, ErrNotFound
	}
	if err != nil {
		return LeadSubmission{}, err
	}
	if cur.SyncState == "revoked" {
		return cur, nil // idempotent: already revoked
	}
	switch cur.SyncState {
	case "accepted", "pending_sync", "crm_received", "rejected":
	default:
		return LeadSubmission{}, errors.New("store: cannot revoke lead in state " + cur.SyncState)
	}
	rv := now()
	if _, err := tx.Exec(
		`UPDATE lead_submissions SET sync_state='revoked',revoked_at=?,updated_at=? WHERE submission_ref=?`,
		rv, rv, ref); err != nil {
		return LeadSubmission{}, err
	}
	var forwarded int
	if err := tx.QueryRow(`SELECT forwarded FROM leads_outbox WHERE submission_ref=? AND kind='submit'`, ref).Scan(&forwarded); err == nil && forwarded == 1 {
		// fact already at notify: a local block cannot unsend it — enqueue the
		// explicit revoke/stop-marketing fact (higher source_version).
		if _, err := tx.Exec(
			`INSERT INTO leads_outbox(event_id,submission_ref,kind,payload_json,forwarded,created_at,updated_at)
			 VALUES(?,?, 'revoke',?,0,?,?)`,
			revokeEventID, ref, revokePayload, rv, rv); err != nil {
			return LeadSubmission{}, err
		}
	} else if err == nil && forwarded == 0 {
		if _, err := tx.Exec(`UPDATE leads_outbox SET forwarded=2,updated_at=? WHERE submission_ref=? AND kind='submit'`,
			rv, ref); err != nil {
			return LeadSubmission{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return LeadSubmission{}, err
	}
	return s.GetLeadSubmissionByRef(ref)
}

// ---- lead audit ---------------------------------------------------------------

func (s *Store) AppendLeadAudit(tenantID, ref, action, detail, actor string) error {
	_, err := s.DB.Exec(
		`INSERT INTO lead_audit(id,tenant_id,submission_ref,action,detail,actor,created_at) VALUES(?,?,?,?,?,?,?)`,
		newID("laud_"), tenantID, ref, action, detail, actor, now())
	return err
}

// ListLeadAudit is tenant-scoped and returns the minimal audit trail.
func (s *Store) ListLeadAudit(tenantID, ref string) ([]map[string]string, error) {
	rows, err := s.DB.Query(
		`SELECT action,detail,actor,created_at FROM lead_audit WHERE tenant_id=? AND submission_ref=? ORDER BY created_at`,
		tenantID, ref)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]map[string]string, 0)
	for rows.Next() {
		var action, detail, actor, at string
		if err := rows.Scan(&action, &detail, &actor, &at); err != nil {
			return nil, err
		}
		out = append(out, map[string]string{"action": action, "detail": detail, "actor": actor, "created_at": at})
	}
	return out, rows.Err()
}

// ---- leads outbox (forwarder queue) --------------------------------------------

type LeadsOutboxRow struct {
	EventID       string `json:"event_id"`
	SubmissionRef string `json:"submission_ref"`
	Kind          string `json:"kind"`
	PayloadJSON   string `json:"payload_json"`
	Forwarded     int    `json:"forwarded"`
	Attempts      int    `json:"attempts"`
}

// ListLeadsOutboxDue returns unsent rows oldest-first. Suppressed (2) and
// sent (1) rows never come back.
func (s *Store) ListLeadsOutboxDue(limit int) ([]LeadsOutboxRow, error) {
	rows, err := s.DB.Query(
		`SELECT event_id,submission_ref,kind,payload_json,forwarded,attempts FROM leads_outbox WHERE forwarded=0 ORDER BY created_at LIMIT ?`,
		limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]LeadsOutboxRow, 0)
	for rows.Next() {
		var r LeadsOutboxRow
		if err := rows.Scan(&r.EventID, &r.SubmissionRef, &r.Kind, &r.PayloadJSON, &r.Forwarded, &r.Attempts); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) MarkOutboxForwarded(eventID, notifyEventID string) error {
	_, err := s.DB.Exec(`UPDATE leads_outbox SET forwarded=1,notify_event_id=?,updated_at=? WHERE event_id=?`, notifyEventID, now(), eventID)
	return err
}

// LeadOutboxPayload returns the fact already stored for one submission kind.
// Callers copy that document; they do not rebuild it from the current campaign.
func (s *Store) LeadOutboxPayload(submissionRef, kind string) (string, error) {
	var payload string
	err := s.DB.QueryRow(
		`SELECT payload_json FROM leads_outbox WHERE submission_ref=? AND kind=? ORDER BY created_at LIMIT 1`,
		submissionRef, kind).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return payload, err
}

// NotifyEventID returns the platform-notify id stored for one forwarded fact.
// An empty string means that fact was never accepted; callers must not invent
// a receipt from the submission ref.
func (s *Store) NotifyEventID(submissionRef, kind string) (string, error) {
	var id string
	err := s.DB.QueryRow(
		`SELECT notify_event_id FROM leads_outbox WHERE submission_ref=? AND kind=? AND forwarded=1 ORDER BY updated_at DESC LIMIT 1`,
		submissionRef, kind).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// MarkOutboxSuppressed marks a row as permanently withheld (revoked before
// first send). It never goes to the network afterwards.
func (s *Store) MarkOutboxSuppressed(eventID string) error {
	_, err := s.DB.Exec(`UPDATE leads_outbox SET forwarded=2,updated_at=? WHERE event_id=? AND forwarded=0`, now(), eventID)
	return err
}

func (s *Store) RecordOutboxError(eventID, msg string) error {
	_, err := s.DB.Exec(`UPDATE leads_outbox SET attempts=attempts+1,last_error=?,updated_at=? WHERE event_id=?`,
		msg, now(), eventID)
	return err
}

// ---- anonymous view stats ------------------------------------------------------

// IncrementViewStat bumps the pure aggregate counter (code, day, channel).
// There is no identity column in this table by construction.
func (s *Store) IncrementViewStat(code, day, channel string) error {
	_, err := s.DB.Exec(
		`INSERT INTO public_view_stats(code,day,channel,views) VALUES(?,?,?,1)
		 ON CONFLICT(code,day,channel) DO UPDATE SET views=views+1`, code, day, channel)
	return err
}

func (s *Store) GetViewStat(code, day, channel string) (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT views FROM public_view_stats WHERE code=? AND day=? AND channel=?`, code, day, channel).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return n, err
}

// LeadStats: submissions vs views, deliberately two separate counters
// (线索与浏览统计分开).
func (s *Store) LeadStats(tenantID, campaignID string) (submissions int, views int, err error) {
	err = s.DB.QueryRow(
		`SELECT COUNT(1) FROM lead_submissions WHERE tenant_id=? AND campaign_id=?`,
		tenantID, campaignID).Scan(&submissions)
	if err != nil {
		return 0, 0, err
	}
	err = s.DB.QueryRow(
		`SELECT COALESCE(SUM(v.views),0) FROM public_view_stats v
		 JOIN campaign_links l ON l.code=v.code
		 JOIN campaigns c ON c.id=l.campaign_id
		 WHERE c.tenant_id=? AND c.id=?`, tenantID, campaignID).Scan(&views)
	return submissions, views, err
}
