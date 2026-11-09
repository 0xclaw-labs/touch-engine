// forwarder.go: reliable, revocable delivery of lead facts to the acquisition
// side via platform-notify directed events.
//
// 语义(HUI-1747 票面 + HUI-1734 + HUI-4594 裁定):
//   - notify 持久接受(2xx)即确认源 → 线索 pending_sync("待同步");
//   - HUI-4594: notify 投递成功(delivered)≠落库——delivered 只记传输层事实
//     (notify_delivered 审计),同步状态停留在待同步;crm_received 只由受信
//     接收方的持久化 ACK(五元组)写成(store.AckLeadReceipt);
//   - deliveries dead / subscription_disabled → rejected;已拒绝的行若后来
//     同一 notify 事件变成 delivered,说明死信判定错了 → 修复回待同步
//     (接收方 ACK 与否仍未知,不跳到 crm_received);
//   - 传输失败/未确认 → 停留在待同步并记账,**不伪报入库**,不阻塞内容预览;
//   - 撤销是终态:forwarder 发现撤销行时把未发的 submit 事实标记 suppressed
//     (零网络);已发出的靠 revoke 事实(更高 source_version)收敛,
//     目标侧版本单调守卫保证重放旧事件不能恢复营销权限。
package leads

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/notifytask"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// Poster is the notify transport the forwarder depends on (satisfied by
// notifytask.DirectedClient; an interface keeps tests stub-driven).
type Poster = notifytask.DirectedPoster

// MaxForwardAttempts bounds retries per outbox row before it stops being picked
// (kept small for T1; the notify side owns the real bounded-backoff ladder).
const MaxForwardAttempts = 8

// Forwarder drives the leads outbox.
type Forwarder struct {
	St       *store.Store
	Poster   Poster
	MaxTries int
	Log      *log.Logger
}

// NewForwarder assembles a forwarder over the store and a notify client.
func NewForwarder(st *store.Store, poster Poster, logger *log.Logger) *Forwarder {
	if logger == nil {
		logger = log.Default()
	}
	return &Forwarder{St: st, Poster: poster, MaxTries: MaxForwardAttempts, Log: logger}
}

// TickResult summarizes one pass (for tests and observability).
type TickResult struct {
	Published  int // facts accepted by notify this pass
	Rejected   int // dead/disabled deliveries this pass
	Repaired   int // rejected rows restored to pending this pass (verdict was wrong)
	Suppressed int // facts withheld because their lead was revoked
	Failed     int // transport failures this pass
}

// Tick performs one outbox pass. It never returns early on row-level errors:
// one bad row must not starve the others.
func (f *Forwarder) Tick(ctx context.Context) TickResult {
	var res TickResult
	rows, err := f.St.ListLeadsOutboxDue(50)
	if err != nil {
		f.Log.Printf("leads forwarder: list outbox: %v", err)
		return res
	}
	for _, row := range rows {
		if row.Attempts >= f.MaxTries {
			// leave the row pending (visible debt) — notify owns retry policy upstream
			continue
		}
		lead, err := f.St.GetLeadSubmissionByRef(row.SubmissionRef)
		if err != nil {
			f.Log.Printf("leads forwarder: lead %s missing for outbox %s: %v", row.SubmissionRef, row.EventID, err)
			continue
		}
		// 撤销终态:本地未发的 submit 事实一律拦下(零网络,阻止未投递数据继续同步)。
		// revoke 事实恰恰是撤销语义的载体,必须照发(目标按版本守卫收敛)。
		if lead.SyncState == StateRevoked && row.Kind != "revoke" {
			if err := f.St.MarkOutboxSuppressed(row.EventID); err == nil {
				res.Suppressed++
				_ = f.St.AppendLeadAudit(lead.TenantID, lead.SubmissionRef, "suppressed", `{"reason":"revoked_before_send"}`, "system")
			}
			continue
		}
		notifyID, err := f.Poster.PostEvent(ctx, []byte(row.PayloadJSON))
		if err != nil {
			res.Failed++
			_ = f.St.RecordOutboxError(row.EventID, err.Error())
			_ = f.St.RecordLeadSyncError(lead.SubmissionRef, err.Error())
			f.Log.Printf("leads forwarder: publish %s failed (kept pending): %v", row.EventID, err)
			continue
		}
		if notifyID == "" {
			res.Failed++
			msg := "notify accepted without event id"
			_ = f.St.RecordOutboxError(row.EventID, msg)
			_ = f.St.RecordLeadSyncError(lead.SubmissionRef, msg)
			continue
		}
		if err := f.St.MarkOutboxForwarded(row.EventID, notifyID); err != nil {
			f.Log.Printf("leads forwarder: mark forwarded %s: %v", row.EventID, err)
			continue
		}
		res.Published++
		if row.Kind == "submit" {
			if err := f.St.MarkLeadSyncPending(lead.SubmissionRef); err != nil {
				f.Log.Printf("leads forwarder: state pending %s: %v", lead.SubmissionRef, err)
			}
			_ = f.St.AppendLeadAudit(lead.TenantID, lead.SubmissionRef, "sync_pending", `{"via":"notify"}`, "system")
		}
	}
	// second phase: transport verdicts for facts already accepted by notify.
	// HUI-4594: a delivered verdict is transport-layer truth only; ending the
	// sync requires the receiver's persisted ACK.
	f.confirmDeliveries(ctx, &res)
	return res
}

// confirmDeliveries polls notify's deliveries endpoint for pending_sync and
// rejected leads. A rejected row is polled again so a later delivered status on
// the same notify event can repair the wrong dead-letter verdict. Queries are
// read-only on the notify side (零副作用).
func (f *Forwarder) confirmDeliveries(ctx context.Context, res *TickResult) {
	f.confirmState(ctx, res, StatePendingSync, false)
	f.confirmState(ctx, res, StateRejected, true)
}

func (f *Forwarder) confirmState(ctx context.Context, res *TickResult, state string, repair bool) {
	rows, err := f.St.ListLeadSubmissionsByState(state, 50)
	if err != nil {
		f.Log.Printf("leads forwarder: list %s: %v", state, err)
		return
	}
	for _, lead := range rows {
		notifyID, err := f.St.NotifyEventID(lead.SubmissionRef, "submit")
		if err != nil {
			f.Log.Printf("leads forwarder: notify id %s: %v", lead.SubmissionRef, err)
			continue
		}
		if notifyID == "" {
			continue
		}
		deliveries, err := f.Poster.Deliveries(ctx, notifyID)
		if err != nil {
			if repair {
				continue
			}
			res.Failed++
			_ = f.St.RecordLeadSyncError(lead.SubmissionRef, err.Error())
			continue
		}
		verdict, reason := resolveDeliveries(deliveries)
		switch verdict {
		case "delivered":
			if repair {
				// the dead verdict was wrong: back to 待同步 (ingestion still
				// unproven — only the receiver ACK ends the sync)
				if err := f.St.RepairLeadToPending(lead.SubmissionRef); err == nil {
					res.Repaired++
					_ = f.St.AppendLeadAudit(lead.TenantID, lead.SubmissionRef, "sync_repaired", `{"by":"deliveries"}`, "system")
				}
				continue
			}
			// 传输层事实留痕(只记一次);同步状态不动(待同步可见),入库以 ACK 为准
			if seen, serr := f.St.HasLeadAuditAction(lead.TenantID, lead.SubmissionRef, "notify_delivered"); serr == nil && !seen {
				_ = f.St.AppendLeadAudit(lead.TenantID, lead.SubmissionRef, "notify_delivered", `{"by":"deliveries"}`, "system")
			}
		case "dead":
			if repair {
				continue
			}
			if err := f.St.MarkLeadRejected(lead.SubmissionRef, reason); err == nil {
				res.Rejected++
				reasonJSON, _ := json.Marshal(reason)
				_ = f.St.AppendLeadAudit(lead.TenantID, lead.SubmissionRef, "sync_rejected", `{"reason":`+string(reasonJSON)+`}`, "system")
			}
		default:
			// still pending/in_flight: keep waiting, never fake receipt
		}
	}
}

// resolveDeliveries folds per-route delivery rows into one verdict. Rules:
// any delivered row wins (the fact reached the target); otherwise any dead /
// subscription_disabled row rejects; otherwise keep waiting.
func resolveDeliveries(items []notifytask.DeliveryStatus) (verdict, reason string) {
	deadReason := ""
	for _, d := range items {
		switch d.Status {
		case "delivered":
			return "delivered", ""
		case "dead", "subscription_disabled":
			if deadReason == "" {
				deadReason = d.Status
				if d.LastError != "" {
					deadReason += ": " + d.LastError
				}
			}
		}
	}
	if deadReason != "" {
		return "dead", deadReason
	}
	return "", ""
}

// Run drives Tick on an interval until ctx is done (production loop).
func (f *Forwarder) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			f.Tick(ctx)
		}
	}
}
