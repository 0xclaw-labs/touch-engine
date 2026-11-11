"use client";

// HUI-4592 O3 订单与对账:订阅/AI/硬件分明细、支付/退款/发票状态、差异
// 队列与只读重查(操作信封 order.recheck)。
//
// 纪律(票面):
//   - 全部是 4591 投影的诚实复述:本页永不修改账单金额/权益投影,
//     付款不等于硬件交付(硬件五状态独立展示);
//   - 重查 = 只读重读投影并把快照落到 operation 结果,不是"修复";
//     获准退款仍由公共权威缝推进,运营台无退款写面。

import { useCallback, useEffect, useState } from "react";
import { useMerchantSession, type MerchantSession } from "@/components/work/merchant-session";
import {
  OperationFeedback,
  OpsReasonForm,
  oneIdempotencyKey,
  postOperation,
  type EnvelopeOutcome,
} from "@/components/ops/ops-shell";
import {
  formatAmount,
  invoiceStatusText,
  orderKindText,
  orderStatusText,
  type OpsAIUsageView,
  type OpsOrderView,
  type OpsRefundView,
} from "@/lib/ops-console";

type Phase = "loading" | "error" | "ready";

interface OrdersPayload {
  orders: OpsOrderView[];
  refunds: OpsRefundView[];
  ai_usage: OpsAIUsageView[];
  discrepancy_queue: Array<{ order_id: string; tenant_id: string; kind: string; status: string; fail_reason: string; discrepancy: string }>;
}

export function OrdersDesk() {
  const session = useMerchantSession();
  const [phase, setPhase] = useState<Phase>("loading");
  const [error, setError] = useState("");
  const [kind, setKind] = useState("");
  const [data, setData] = useState<OrdersPayload | null>(null);
  const [busy, setBusy] = useState(false);
  const [outcome, setOutcome] = useState<EnvelopeOutcome | null>(null);
  const [opError, setOpError] = useState("");
  const [recheckTarget, setRecheckTarget] = useState<OpsOrderView | null>(null);

  const load = useCallback(
    async (ses: MerchantSession, k: string) => {
      setPhase("loading");
      setError("");
      const suffix = k ? `?kind=${encodeURIComponent(k)}` : "";
      const res = await ses.api("GET", `ops/orders${suffix}`);
      if (!res.ok) {
        setError(`订单读取失败(${res.status})`);
        setPhase("error");
        return;
      }
      setData({
        orders: Array.isArray(res.data.orders) ? (res.data.orders as OpsOrderView[]) : [],
        refunds: Array.isArray(res.data.refunds) ? (res.data.refunds as OpsRefundView[]) : [],
        ai_usage: Array.isArray(res.data.ai_usage) ? (res.data.ai_usage as OpsAIUsageView[]) : [],
        discrepancy_queue: Array.isArray(res.data.discrepancy_queue)
          ? (res.data.discrepancy_queue as OrdersPayload["discrepancy_queue"])
          : [],
      });
      setPhase("ready");
    },
    [],
  );

  useEffect(() => {
    void load(session, kind);
  }, [load, session, kind]);

  const recheck = useCallback(
    async (ses: MerchantSession, order: OpsOrderView, reason: string) => {
      setBusy(true);
      setOpError("");
      const res = await postOperation(ses, {
        action: "order.recheck",
        resource_ref: order.id,
        tenant_id: order.tenant_id,
        reason,
        idempotency_key: oneIdempotencyKey(),
      });
      setBusy(false);
      setRecheckTarget(null);
      if (!res.ok || !res.outcome) {
        setOpError(res.message);
        return;
      }
      setOutcome(res.outcome);
    },
    [],
  );

  return (
    <section className="tk-section" data-view="ops-orders">
      <h2 className="tk-section-title">订单与对账</h2>
      <p className="tk-note">
        本页是公共订单权威的投影读面:重查只读刷新并留下对账快照(按 operation_id
        可追踪);退款/开票事实仍由受信权威缝写入,运营台没有改账通道。
      </p>
      <div className="tk-row tk-gap" data-section="order-kind-filter">
        {[
          { key: "", label: "全部" },
          { key: "subscription", label: "订阅" },
          { key: "hardware", label: "硬件" },
        ].map((tab) => (
          <button
            key={tab.key}
            className="tk-button"
            data-action={`kind-${tab.key || "all"}`}
            onClick={() => setKind(tab.key)}
          >
            {tab.label}
          </button>
        ))}
      </div>

      {phase === "loading" ? <p className="tk-state" data-state="loading">正在读取订单…</p> : null}
      {phase === "error" ? <p className="tk-state tk-danger" data-state="error">{error}</p> : null}
      {phase === "ready" && data && data.orders.length === 0 ? (
        <p className="tk-state" data-state="empty">还没有订单投影。</p>
      ) : null}

      {data && data.discrepancy_queue.length > 0 ? (
        <div data-section="discrepancy-queue">
          <h3 className="tk-section-title">差异队列</h3>
          <ul className="tk-list">
            {data.discrepancy_queue.map((q) => (
              <li key={q.order_id} data-discrepancy={q.discrepancy}>
                订单 {q.order_id}({orderKindText(q.kind)}) · {orderStatusText(q.status)}
                {q.fail_reason ? ` · 原因:${q.fail_reason}` : ""} · 商户 {q.tenant_id}
              </li>
            ))}
          </ul>
        </div>
      ) : null}

      <ul className="tk-list" data-section="order-list">
        {(data?.orders ?? []).map((o) => (
          <li key={o.id} className="tk-row" data-order-id={o.id} data-status={o.status}>
            <span data-field="order-title">
              {orderKindText(o.kind)} · {orderStatusText(o.status)} · {formatAmount(o.amount_minor, o.currency)}
            </span>
            <span className="tk-quiet" data-field="order-meta">
              商户 {o.tenant_id} · {o.created_at}
              {o.fail_reason ? ` · 失败原因:${o.fail_reason}` : ""}
              {o.refund_total_minor > 0 ? ` · 已退 ${formatAmount(o.refund_total_minor, o.currency)}` : ""}
            </span>
            <button
              className="tk-button"
              data-action="order-recheck"
              onClick={() => setRecheckTarget(o)}
            >
              重查对账
            </button>
          </li>
        ))}
      </ul>

      {recheckTarget ? (
        <div data-section="recheck-form">
          <p className="tk-note">重查订单 {recheckTarget.id}(商户 {recheckTarget.tenant_id})</p>
          <OpsReasonForm label="重查对账" busy={busy} onSubmit={(reason) => void recheck(session, recheckTarget, reason)} />
        </div>
      ) : null}

      <div className="tk-section" data-section="refund-list">
        <h3 className="tk-section-title">退款与发票</h3>
        {(data?.refunds.length ?? 0) === 0 ? (
          <p className="tk-state" data-state="empty">没有退款投影。</p>
        ) : (
          <ul className="tk-list">
            {data?.refunds.map((r) => (
              <li key={r.id} className="tk-row" data-refund-id={r.id}>
                <span>
                  {formatAmount(r.amount_minor, r.currency)} · 订单 {r.order_id} ·{" "}
                  {invoiceStatusText(r.invoice_status)}
                </span>
                <span className="tk-quiet">原因:{r.reason || "未填写"} · 审核人 {r.auditor}</span>
              </li>
            ))}
          </ul>
        )}
      </div>

      <div className="tk-section" data-section="ai-usage">
        <h3 className="tk-section-title">AI 用量</h3>
        {(data?.ai_usage.length ?? 0) === 0 ? (
          <p className="tk-state" data-state="empty">没有 AI 用量投影。</p>
        ) : (
          <ul className="tk-list">
            {data?.ai_usage.map((u) => (
              <li key={u.id} data-quote-ref={u.quote_ref} data-state={u.state}>
                {u.capability} × {u.quantity} · {formatAmount(u.amount_minor, u.currency)} · {u.state}
              </li>
            ))}
          </ul>
        )}
      </div>

      <OperationFeedback outcome={outcome} error={opError} />
    </section>
  );
}
