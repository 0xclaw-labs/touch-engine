// store_lead_receipts.go: HUI-4594 persisted receiver ACKs (持久化 ACK).
//
// 纪律(裁定书):
//   - Notify 投递成功≠落库: sync 只在受信 Leads 接收方的五元组 ACK
//     (receipt_id/source_event_id/submission_id/source_revision/receiver_state)
//     到达时结束(crm_received);缺一不可;
//   - ACK 写回执、投影 lead 行、迁移同步状态、追加审计在**同一事务**内
//     (原子提交契约,同 SubmitLead);
//   - receipt_id 是接收方铸造的主键:同键同负载重放幂等(duplicate),同键
//     不同负载拒绝(ErrAckConflict,SHARED-BRIEF IDEMPOTENCY_CONFLICT 语义);
//   - source_event_id 必须指向本侧真实发过的 submit 事实(outbox event_id 或
//     已记录的 notify_event_id,且已转发)——接收方不能凭空结束同步;
//   - 撤回水位: 已撤销线索的 stored-ACK 一律 ErrRevocationWaterline
//     (晚到重放不能复活联系人);revoked-ACK 只记账接收方申报,不发明本地状态。
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// envelopeEventID extracts event_profile.event_id from a stored directed-event
// payload. Our own writer always produces this shape; an unreadable payload
// yields "" so the row simply cannot match through the envelope path.
func envelopeEventID(payload string) string {
	var env struct {
		EventProfile struct {
			EventID string `json:"event_id"`
		} `json:"event_profile"`
	}
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		return ""
	}
	return env.EventProfile.EventID
}

// Receiver states accepted on an ACK.
const (
	ReceiverStateStored  = "stored"
	ReceiverStateRevoked = "revoked"
)

// LeadAck is the receiver's persisted-acknowledgement five-tuple. All five
// members are required; a partial ACK never ends sync.
type LeadAck struct {
	ReceiptID      string
	SourceEventID  string
	SubmissionID   string
	SourceRevision int
	ReceiverState  string
}

// LeadReceipt is one stored ACK row (the receipt ledger; no contact columns).
type LeadReceipt struct {
	ID             string `json:"receipt_id"`
	TenantID       string `json:"tenant_id"`
	SubmissionRef  string `json:"submission_ref"`
	SourceEventID  string `json:"source_event_id"`
	SourceRevision int    `json:"source_revision"`
	ReceiverState  string `json:"receiver_state"`
	ReceiverID     string `json:"receiver_id"`
	CreatedAt      string `json:"created_at"`
}

// Ack outcomes.
var (
	// ErrAckIncomplete: a member of the five-tuple is missing/malformed. The
	// sync state is untouched — a partial ACK is not an ACK.
	ErrAckIncomplete = errors.New("store: lead ack five-tuple incomplete")
	// ErrAckConflict: same receipt id replayed with a different payload.
	ErrAckConflict = errors.New("store: lead ack receipt id conflict")
	// ErrUnknownSourceEvent: the ack references an event this side never sent
	// (or that was never forwarded to notify).
	ErrUnknownSourceEvent = errors.New("store: lead ack references an unknown source event")
	// ErrRevocationWaterline: a stored ack arrived for a revoked lead — the
	// late replay of the submit fact must not resurrect the contact.
	ErrRevocationWaterline = errors.New("store: lead ack below revocation waterline refused")
)

const receiptCols = `id,tenant_id,submission_ref,source_event_id,source_revision,receiver_state,receiver_id,created_at`

func scanReceipt(sc interface{ Scan(...any) error }) (LeadReceipt, error) {
	var r LeadReceipt
	err := sc.Scan(&r.ID, &r.TenantID, &r.SubmissionRef, &r.SourceEventID, &r.SourceRevision, &r.ReceiverState, &r.ReceiverID, &r.CreatedAt)
	return r, err
}

// AckLeadReceipt applies one receiver ACK atomically: receipt row + lead
// projection + sync transition + audit, all in ONE transaction. duplicate=true
// means an identical receipt id was replayed (idempotent, nothing re-written).
func (s *Store) AckLeadReceipt(tenantID, submissionRef, receiverID string, in LeadAck) (LeadReceipt, bool, error) {
	// five-tuple completeness is a store-level invariant, not only a handler
	// shape check: a partial ack must be structurally impossible to persist.
	if strings.TrimSpace(in.ReceiptID) == "" || strings.TrimSpace(in.SourceEventID) == "" ||
		strings.TrimSpace(in.SubmissionID) == "" || in.SubmissionID != submissionRef ||
		in.SourceRevision < 1 {
		return LeadReceipt{}, false, ErrAckIncomplete
	}
	if in.ReceiverState != ReceiverStateStored && in.ReceiverState != ReceiverStateRevoked {
		return LeadReceipt{}, false, ErrAckIncomplete
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return LeadReceipt{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	lead, err := scanLead(tx.QueryRow(`SELECT `+leadCols+` FROM lead_submissions WHERE submission_ref=?`, submissionRef))
	if errors.Is(err, sql.ErrNoRows) {
		return LeadReceipt{}, false, ErrNotFound
	}
	if err != nil {
		return LeadReceipt{}, false, err
	}
	if lead.TenantID != tenantID {
		return LeadReceipt{}, false, ErrNotFound // another tenant's lead is invisible
	}

	// replay of the same receipt id: identical payload is idempotent, a
	// different payload under the same id is a conflict.
	existing, err := scanReceipt(tx.QueryRow(`SELECT `+receiptCols+` FROM lead_receipts WHERE id=?`, in.ReceiptID))
	switch {
	case err == nil:
		same := existing.SubmissionRef == submissionRef &&
			existing.SourceEventID == in.SourceEventID &&
			existing.SourceRevision == in.SourceRevision &&
			existing.ReceiverState == in.ReceiverState &&
			existing.ReceiverID == receiverID
		if !same {
			return LeadReceipt{}, false, ErrAckConflict
		}
		return existing, true, nil
	case errors.Is(err, sql.ErrNoRows):
		// new receipt: continue
	default:
		return LeadReceipt{}, false, err
	}

	// the ack must reference a fact this side actually sent AND that reached
	// notify (forwarded=1). An unsent event cannot carry a receiver receipt.
	// The receiver quotes the id it saw on the fact, which may be any of:
	//   - the envelope's event_profile.event_id (what the payload carries),
	//   - this side's outbox event id,
	//   - platform-notify's own event id (recorded on the forwarded row).
	rows, err := tx.Query(
		`SELECT event_id,notify_event_id,payload_json,forwarded FROM leads_outbox WHERE submission_ref=?`,
		submissionRef)
	if err != nil {
		return LeadReceipt{}, false, err
	}
	known := false
	for rows.Next() {
		var eventID, notifyEventID, payload string
		var forwarded int
		if err := rows.Scan(&eventID, &notifyEventID, &payload, &forwarded); err != nil {
			_ = rows.Close()
			return LeadReceipt{}, false, err
		}
		if forwarded != 1 {
			continue
		}
		if in.SourceEventID == eventID || (notifyEventID != "" && in.SourceEventID == notifyEventID) ||
			in.SourceEventID == envelopeEventID(payload) {
			known = true
			break
		}
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return LeadReceipt{}, false, err
	}
	if !known {
		return LeadReceipt{}, false, ErrUnknownSourceEvent
	}

	// revocation waterline: a stored ack for a revoked lead is the late replay
	// path — refuse it so the contact cannot resurrect past the watermark.
	if in.ReceiverState == ReceiverStateStored && lead.RevokeSourceRevision > 0 {
		return LeadReceipt{}, false, ErrRevocationWaterline
	}

	rec := LeadReceipt{
		ID: in.ReceiptID, TenantID: lead.TenantID, SubmissionRef: submissionRef,
		SourceEventID: in.SourceEventID, SourceRevision: in.SourceRevision,
		ReceiverState: in.ReceiverState, ReceiverID: receiverID, CreatedAt: now(),
	}
	if _, err := tx.Exec(
		`INSERT INTO lead_receipts(`+receiptCols+`) VALUES(?,?,?,?,?,?,?,?)`,
		rec.ID, rec.TenantID, rec.SubmissionRef, rec.SourceEventID, rec.SourceRevision,
		rec.ReceiverState, rec.ReceiverID, rec.CreatedAt); err != nil {
		return LeadReceipt{}, false, err
	}

	// projection on the lead row. A revoked ack only records the receiver's
	// claim; the local sync state is never invented from it.
	endsSync := in.ReceiverState == ReceiverStateStored && lead.RevokeSourceRevision == 0
	stmt := `UPDATE lead_submissions SET receiver_state=?,receipt_id=?,updated_at=?`
	if endsSync {
		stmt += `,sync_state='crm_received'`
	}
	stmt += ` WHERE submission_ref=?`
	if _, err := tx.Exec(stmt, rec.ReceiverState, rec.ID, now(), submissionRef); err != nil {
		return LeadReceipt{}, false, err
	}

	if _, err := tx.Exec(
		`INSERT INTO lead_audit(id,tenant_id,submission_ref,action,detail,actor,created_at) VALUES(?,?,?,?,?,?,?)`,
		newID("laud_"), lead.TenantID, submissionRef, "receiver_ack",
		`{"receipt_id":"`+rec.ID+`","source_event_id":"`+rec.SourceEventID+`","source_revision":`+
			strconv.Itoa(rec.SourceRevision)+`,"receiver_state":"`+rec.ReceiverState+`"}`,
		"receiver:"+receiverID, now()); err != nil {
		return LeadReceipt{}, false, err
	}

	if err := tx.Commit(); err != nil {
		return LeadReceipt{}, false, err
	}
	return rec, false, nil
}
