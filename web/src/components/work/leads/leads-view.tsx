"use client";

// HUI-4594 M11 线索页(/work/leads)。
//
// 消费 GET /api/v1/work/leads(经 BFF catch-all 透传;FEATURE_LEADS_CAPTURE
// 默认 off,off 时端点答统一 404 → 显示未开通文案)。纪律(裁定书):
//   - 口径诚实: 已获客只数接收方确认(持久化 ACK),待同步单列且**不计入
//     已获客**;口径注记必渲染;
//   - 来源两分: 活动线索与门店(无活动)留资分开标注,不伪造活动归属;
//   - 撤回是终态: revoked 行显著标注,不再显示为可跟进线索;
//   - 拿不到的就是未知: 没有 items 时显示空态,不用 0 冒充;
//   - 移动优先(tk- 体系,无固定像素宽);本组件只渲染,不发裸 fetch。

import { useEffect, useState } from "react";
import { failureText, useSession } from "@/components/work/merchant-session";

export const LEADS_NOT_ENABLED_COPY = "线索功能未开通。开通后这里显示顾客授权留下的联系方式。";
export const LEADS_EMPTY_COPY = "还没有授权线索。顾客在活动页或门店页自愿留资后会出现在这里。";
export const LEADS_PENDING_NOT_ACQUIRED_COPY = "「待同步」是还没交到商家获客系统的线索，不计入已获客。";

export interface WorkLeadItem {
  submission_ref: string;
  capture_source: string;
  campaign_id: string;
  store_id: string;
  name: string;
  phone: string;
  marketing_optin: boolean;
  sync_state: string;
  receiver_state: string;
  has_receipt: boolean;
  revoked: boolean;
  created_at: string;
}

export interface WorkLeadsStats {
  authorized_total: number;
  pending_sync: number;
  receiver_received: number;
  revoked: number;
}

export function leadSourceLabel(item: Pick<WorkLeadItem, "capture_source" | "campaign_id" | "store_id">): string {
  if (item.capture_source === "store") return `门店留资 ${item.store_id}`;
  if (item.campaign_id) return `活动 ${item.campaign_id}`;
  return "来源未知";
}

export function leadSyncLabel(item: Pick<WorkLeadItem, "sync_state" | "receiver_state" | "revoked">): string {
  if (item.revoked || item.sync_state === "revoked") return "已撤销";
  switch (item.sync_state) {
    case "crm_received":
      return item.receiver_state === "stored" ? "接收方已确认入库" : "接收方已确认";
    case "pending_sync":
      return "待同步";
    case "rejected":
      return "同步被拒";
    case "accepted":
      return "已接受，还没交给通知服务";
    default:
      return "状态未知";
  }
}

const STATE_TONE: Record<string, string> = {
  received: "tk-ok",
  pending: "tk-unknown",
  revoked: "tk-danger",
  unknown: "tk-unknown",
};

export function leadsTone(item: Pick<WorkLeadItem, "sync_state" | "receiver_state" | "revoked">): string {
  if (item.revoked || item.sync_state === "revoked") return STATE_TONE.revoked;
  if (item.sync_state === "crm_received") return STATE_TONE.received;
  return STATE_TONE.unknown;
}

export function LeadsView() {
  const session = useSession();
  const [phase, setPhase] = useState<"loading" | "error" | "disabled" | "ready">("loading");
  const [loadError, setLoadError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const [items, setItems] = useState<WorkLeadItem[] | null>(null);
  const [stats, setStats] = useState<WorkLeadsStats | null>(null);

  useEffect(() => {
    let alive = true;
    setPhase("loading");
    session
      .api("GET", "work/leads")
      .then((res) => {
        if (!alive) return;
        if (res.status === 404) {
          setPhase("disabled");
          return;
        }
        if (!res.ok) {
          setLoadError(failureText(res.status, res.data));
          setPhase("error");
          return;
        }
        const rows = (res.data as Record<string, unknown>).items;
        const statRow = (res.data as Record<string, unknown>).stats;
        setItems(Array.isArray(rows) ? (rows as WorkLeadItem[]) : []);
        setStats(statRow ? (statRow as WorkLeadsStats) : null);
        setPhase("ready");
      })
      .catch(() => {
        if (!alive) return;
        setLoadError("没有连上服务。没有读到任何线索，请检查网络后重试。");
        setPhase("error");
      });
    return () => {
      alive = false;
    };
  }, [session, attempt]);

  if (phase === "loading") {
    return (
      <main>
        <h1 className="tk-title">线索</h1>
        <p className="tk-state" data-state="loading">正在读取…</p>
      </main>
    );
  }

  if (phase === "error") {
    return (
      <main>
        <h1 className="tk-title">线索</h1>
        <p className="tk-state tk-danger" data-state="error">
          {loadError || "没有读到线索。"}
          <button className="tk-quiet" type="button" data-action="retry" onClick={() => setAttempt((a) => a + 1)}>
            重试
          </button>
        </p>
      </main>
    );
  }

  if (phase === "disabled") {
    return (
      <main>
        <h1 className="tk-title">线索</h1>
        <p className="tk-state tk-unknown" data-state="disabled">{LEADS_NOT_ENABLED_COPY}</p>
      </main>
    );
  }

  if (!items) return null;

  return (
    <main>
      <h1 className="tk-title">线索</h1>
      <p className="tk-lead">顾客授权留下的联系方式。只统计已经发生的真实事实。</p>

      {stats ? (
        <section aria-label="线索口径" data-section="stats">
          <ul className="tk-gap" data-testid="leads-stats">
            <li>已获客（接收方确认入库）<strong data-field="stat-received">{stats.receiver_received}</strong></li>
            <li>待同步<strong data-field="stat-pending">{stats.pending_sync}</strong></li>
            <li>已撤销<strong data-field="stat-revoked">{stats.revoked}</strong></li>
            <li>授权线索合计<strong data-field="stat-total">{stats.authorized_total}</strong></li>
          </ul>
          <p className="tk-note" data-field="pending-note">{LEADS_PENDING_NOT_ACQUIRED_COPY}</p>
        </section>
      ) : null}

      {items.length === 0 ? (
        <section aria-label="线索列表" data-section="items">
          <p className="tk-state" data-state="empty">{LEADS_EMPTY_COPY}</p>
        </section>
      ) : (
        <section aria-label="线索列表" data-section="items">
          <ul className="tk-gap" data-testid="leads-items">
            {items.map((item) => (
              <li key={item.submission_ref} className="tk-gap" data-testid="lead-row" data-revoked={item.revoked || undefined}>
                <p className="tk-lead">
                  {item.name} · {item.phone}
                </p>
                <p className="tk-note">来源：{leadSourceLabel(item)}</p>
                <p className={`tk-note ${leadsTone(item)}`} data-field="lead-sync">
                  {leadSyncLabel(item)}
                  {item.marketing_optin ? " · 顾客同意后续营销" : " · 未同意后续营销"}
                  {item.has_receipt ? " · 有接收回执" : ""}
                </p>
                <p className="tk-note">{item.created_at}</p>
              </li>
            ))}
          </ul>
        </section>
      )}
    </main>
  );
}
