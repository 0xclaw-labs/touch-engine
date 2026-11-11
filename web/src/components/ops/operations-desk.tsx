"use client";

// HUI-4592 O6 运行与交付台(核心验收 T20):域名/媒体探针、硬件履约只读
// 投影(4591 五状态,1663 seam)、触点库存、异常工单、备份恢复演练、
// 近期操作与审计回查。
//
// 纪律(票面):
//   - 聚合状态 + 修复入口,不是纯指标大屏:每条告警可定位(目标/配置键/
//     错误),可开单、认领、修复确认、带理由关闭;
//   - 探针如实报 degraded/unreachable,不把错误填 0 或"正常";
//   - 恢复演练必带恢复校验报告(只写"已备份"不算演练);
//   - 硬件履约是 1663 未合流前的只读投影,来源如实标注。

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
  drillKindText,
  orderKindText,
  orderStatusText,
  ticketStatusText,
  type OperationsOverview,
  type OpsAuditEntry,
  type OpsOperation,
  type OpsTicket,
} from "@/lib/ops-console";

type Phase = "loading" | "error" | "ready";

export function OperationsDesk() {
  const session = useMerchantSession();
  const [phase, setPhase] = useState<Phase>("loading");
  const [error, setError] = useState("");
  const [view, setView] = useState<OperationsOverview | null>(null);
  const [audit, setAudit] = useState<OpsAuditEntry[]>([]);
  const [busy, setBusy] = useState(false);
  const [outcome, setOutcome] = useState<EnvelopeOutcome | null>(null);
  const [opError, setOpError] = useState("");
  const [ticketForm, setTicketForm] = useState<"open" | { action: string; ticket: OpsTicket } | null>(null);

  const load = useCallback(async (ses: MerchantSession) => {
    setPhase("loading");
    setError("");
    const [opsRes, auditRes] = await Promise.all([
      ses.api("GET", "ops/operations"),
      ses.api("GET", "ops/audit?limit=30"),
    ]);
    if (!opsRes.ok) {
      setError(`运行视图读取失败(${opsRes.status})`);
      setPhase("error");
      return;
    }
    setView(opsRes.data as unknown as OperationsOverview);
    if (auditRes.ok && Array.isArray(auditRes.data.audit)) {
      setAudit(auditRes.data.audit as OpsAuditEntry[]);
    }
    setPhase("ready");
  }, []);

  useEffect(() => {
    void load(session);
  }, [load, session]);

  const runOp = useCallback(
    async (ses: MerchantSession, body: Record<string, unknown>) => {
      setBusy(true);
      setOpError("");
      const res = await postOperation(ses, { ...body, idempotency_key: oneIdempotencyKey() });
      setBusy(false);
      setTicketForm(null);
      if (!res.ok || !res.outcome) {
        setOpError(res.message);
        return;
      }
      setOutcome(res.outcome);
      await load(ses);
    },
    [load],
  );

  return (
    <section className="tk-section" data-view="ops-operations">
      <h2 className="tk-section-title">运行与交付</h2>
      <p className="tk-note">
        域名/媒体探针、硬件履约、触点库存、异常工单与演练的聚合台:每条异常可定位到目标,
        可开单跟踪、确认处理、带理由关闭;恢复演练必须附恢复校验报告。
      </p>
      {phase === "loading" ? <p className="tk-state" data-state="loading">正在读取运行状态…</p> : null}
      {phase === "error" ? <p className="tk-state tk-danger" data-state="error">{error}</p> : null}

      {view ? (
        <>
          <div data-section="domain-health" data-status={view.domain_health.status}>
            <h3 className="tk-section-title">域名与媒体链</h3>
            <p data-field="health-overall">
              探针结论:{view.domain_health.status === "ok" ? "正常" : view.domain_health.status === "degraded" ? "降级(存在不可达/未配置目标)" : view.domain_health.status}
            </p>
            <ul className="tk-list">
              {view.domain_health.targets.map((t) => (
                <li key={t.config_key} data-probe-status={t.status} className="tk-row">
                  <span>
                    {t.target_kind}({t.config_key}) ·{" "}
                    {t.status === "reachable"
                      ? `可达${typeof t.latency_ms === "number" ? ` ${t.latency_ms}ms` : ""}`
                      : t.status === "not_configured"
                        ? "未配置"
                        : t.status === "same_origin"
                          ? "同源媒体(BFF 相对路径)"
                          : t.status === "invalid_config"
                            ? "配置不可探测"
                            : `不可达${t.http_status ? `(HTTP ${t.http_status})` : ""}`}
                  </span>
                  {t.error ? <span className="tk-quiet">{t.error}</span> : null}
                </li>
              ))}
            </ul>
          </div>

          <div className="tk-section" data-section="hardware-fulfillment">
            <h3 className="tk-section-title">触点库存与硬件履约</h3>
            <p className="tk-note" data-field="hardware-source">
              来源:{view.hardware_fulfillment.source}(1663 实体履约合流前的只读投影;付款≠交付,
              五状态独立:付款/出库/写卡/回读/现场验收)。
            </p>
            <p data-field="touchpoint-inventory">
              触点库存:
              {Object.keys(view.touchpoint_inventory).length === 0
                ? "无触点(零记录)"
                : Object.entries(view.touchpoint_inventory)
                    .map(([k, n]) => `${k} ${n}`)
                    .join(" · ")}
            </p>
            {(view.hardware_fulfillment.orders ?? []).length === 0 ? (
              <p className="tk-state" data-state="empty">没有硬件订单投影。</p>
            ) : (
              <ul className="tk-list">
                {view.hardware_fulfillment.orders.map((o) => (
                  <li key={o.id} className="tk-row" data-order-id={o.id}>
                    <span>
                      硬件订单 {o.id} · {orderStatusText(o.status)}
                    </span>
                    <span className="tk-quiet">
                      {o.fulfillment?.steps
                        ? o.fulfillment.steps.map((s) => `${s.step}:${s.state}`).join(" / ")
                        : "履约投影未推进(仅下单)"}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </div>

          <div className="tk-section" data-section="tickets">
            <h3 className="tk-section-title">异常工单</h3>
            <button className="tk-button" data-action="ticket-open-form" onClick={() => setTicketForm("open")}>
              开新工单
            </button>
            {ticketForm === "open" ? (
              <div data-section="ticket-open-form">
                <TicketOpenForm busy={busy} onSubmit={(body) => void runOp(session, body)} />
              </div>
            ) : null}
            {(view.tickets ?? []).length === 0 ? (
              <p className="tk-state" data-state="empty">没有工单。</p>
            ) : (
              <ul className="tk-list">
                {view.tickets.map((t) => (
                  <li key={t.id} className="tk-row" data-ticket-id={t.id} data-status={t.status}>
                    <span>
                      [{ticketStatusText(t.status)}] {t.title}({t.kind || "未分类"} · {t.severity})
                    </span>
                    <span className="tk-quiet">
                      {t.tenant_id ? `商户 ${t.tenant_id} · ` : "平台级 · "}
                      来源 {t.source} · {t.opened_at}
                      {t.resolution ? ` · 处理:${t.resolution}` : ""}
                      {t.closed_reason ? ` · 关闭理由:${t.closed_reason}` : ""}
                    </span>
                    {t.status === "open" ? (
                      <button className="tk-button" data-action="ticket-confirm" onClick={() => setTicketForm({ action: "ticket.confirm", ticket: t })}>
                        认领处理
                      </button>
                    ) : null}
                    {t.status === "confirmed" ? (
                      <button className="tk-button" data-action="ticket-resolve" onClick={() => setTicketForm({ action: "ticket.resolve", ticket: t })}>
                        标记已修复
                      </button>
                    ) : null}
                    {t.status === "resolved" || t.status === "open" ? (
                      <button className="tk-button" data-action="ticket-close" onClick={() => setTicketForm({ action: "ticket.close", ticket: t })}>
                        关闭工单
                      </button>
                    ) : null}
                    {ticketForm !== null && ticketForm !== "open" && ticketForm.ticket.id === t.id ? (
                      <OpsReasonForm
                        label={ticketForm.action === "ticket.confirm" ? "确认认领" : ticketForm.action === "ticket.resolve" ? "确认修复" : "确认关闭"}
                        busy={busy}
                        onSubmit={(reason) => {
                          void runOp(session, {
                            action: ticketForm.action,
                            resource_ref: t.id,
                            reason,
                            resolution: ticketForm.action === "ticket.resolve" ? reason : "",
                            close_reason: ticketForm.action === "ticket.close" ? reason : "",
                          });
                        }}
                      />
                    ) : null}
                  </li>
                ))}
              </ul>
            )}
          </div>

          <div className="tk-section" data-section="drills">
            <h3 className="tk-section-title">备份恢复演练</h3>
            <DrillForm busy={busy} onSubmit={(body) => void runOp(session, body)} />
            {(view.drills ?? []).length === 0 ? (
              <p className="tk-state" data-state="empty">还没有演练记录。</p>
            ) : (
              <ul className="tk-list">
                {view.drills.map((d) => (
                  <li key={d.id} data-drill-id={d.id} data-result={d.result}>
                    {drillKindText(d.kind)} · {d.target} · {d.result === "ok" ? "通过" : "失败"}
                    <span className="tk-quiet">
                      {d.verification_report ? ` · 校验报告:${d.verification_report}` : ""}
                      {d.notes ? ` · ${d.notes}` : ""}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </div>

          <div className="tk-section" data-section="recent-operations">
            <h3 className="tk-section-title">近期操作(按 operation_id 可追踪)</h3>
            {(view.recent_operations ?? []).length === 0 ? (
              <p className="tk-state" data-state="empty">还没有运营操作记录。</p>
            ) : (
              <ul className="tk-list">
                {view.recent_operations.map((op: OpsOperation) => (
                  <li key={op.id} data-operation-id={op.id} data-state={op.state}>
                    {op.action} → {op.resource_ref || "(未定位)"} ·{" "}
                    {op.state === "succeeded" ? "成功" : op.state === "failed" ? `失败(${op.error_code})` : op.state}
                    <span className="tk-quiet"> {op.finished_at || op.created_at}</span>
                  </li>
                ))}
              </ul>
            )}
          </div>

          <div className="tk-section" data-section="audit-trail">
            <h3 className="tk-section-title">审计回查(前后值/操作者/时间/原因)</h3>
            {audit.length === 0 ? (
              <p className="tk-state" data-state="empty">还没有审计行。</p>
            ) : (
              <ul className="tk-list">
                {audit.map((a) => (
                  <li key={a.id} data-audit-id={a.id}>
                    {a.occurred_at} · {a.actor_ref}
                    {a.actor_role ? `(${a.actor_role})` : ""} · {a.action} · {a.target_type}{" "}
                    {a.target_id} · 原因:{a.reason || "(未填)"}
                    {a.authority_source ? ` · 授权:${a.authority_source}` : ""}
                  </li>
                ))}
              </ul>
            )}
          </div>
        </>
      ) : null}

      <OperationFeedback outcome={outcome} error={opError} />
    </section>
  );
}

function TicketOpenForm({ busy, onSubmit }: { busy: boolean; onSubmit: (body: Record<string, unknown>) => void }) {
  const [title, setTitle] = useState("");
  const [kind, setKind] = useState("domain_probe");
  const [severity, setSeverity] = useState("normal");
  const [detail, setDetail] = useState("");
  const [tenant, setTenant] = useState("");
  return (
    <form
      className="tk-form"
      data-section="ticket-open"
      onSubmit={(e) => {
        e.preventDefault();
        if (!title.trim()) return;
        onSubmit({
          action: "ticket.open",
          title: title.trim(),
          kind,
          severity,
          detail: detail.trim(),
          tenant_id: tenant.trim(),
          reason: title.trim(),
        });
        setTitle("");
        setDetail("");
        setTenant("");
      }}
    >
      <input className="tk-input" data-field="ticket-title" value={title} onChange={(e) => setTitle(e.target.value)} placeholder="工单标题(如:媒体域名 403)" aria-label="工单标题" />
      <input className="tk-input" data-field="ticket-kind" value={kind} onChange={(e) => setKind(e.target.value)} placeholder="类别" aria-label="工单类别" />
      <select className="tk-select" data-field="ticket-severity" value={severity} onChange={(e) => setSeverity(e.target.value)} aria-label="严重程度">
        <option value="normal">一般</option>
        <option value="high">高</option>
        <option value="critical">严重</option>
      </select>
      <input className="tk-input" data-field="ticket-tenant" value={tenant} onChange={(e) => setTenant(e.target.value)} placeholder="关联商户 ID(平台级可留空)" aria-label="关联商户" />
      <textarea className="tk-input" data-field="ticket-detail" value={detail} onChange={(e) => setDetail(e.target.value)} rows={2} placeholder="现象与定位信息" aria-label="工单详情" />
      <button className="tk-button" type="submit" disabled={busy || !title.trim()} data-action="ticket-open-submit">
        提交工单
      </button>
    </form>
  );
}

function DrillForm({ busy, onSubmit }: { busy: boolean; onSubmit: (body: Record<string, unknown>) => void }) {
  const [kind, setKind] = useState("backup");
  const [targetText, setTargetText] = useState("");
  const [result, setResult] = useState("ok");
  const [report, setReport] = useState("");
  const [notes, setNotes] = useState("");
  return (
    <form
      className="tk-form"
      data-section="drill-form"
      onSubmit={(e) => {
        e.preventDefault();
        if (!targetText.trim()) return;
        onSubmit({
          action: "drill.record",
          drill_kind: kind,
          drill_target: targetText.trim(),
          drill_result: result,
          verification_report: report.trim(),
          notes: notes.trim(),
          reason: "备份恢复演练登记",
        });
        setTargetText("");
        setReport("");
        setNotes("");
      }}
    >
      <label className="tk-label" htmlFor="drill-kind">演练类型</label>
      <select id="drill-kind" className="tk-select" data-field="drill-kind" value={kind} onChange={(e) => setKind(e.target.value)}>
        <option value="backup">备份演练</option>
        <option value="restore">恢复演练</option>
      </select>
      <input className="tk-input" data-field="drill-target" value={targetText} onChange={(e) => setTargetText(e.target.value)} placeholder="演练对象(如:db主库)" aria-label="演练对象" />
      <label className="tk-label" htmlFor="drill-result">演练结果</label>
      <select id="drill-result" className="tk-select" data-field="drill-result" value={result} onChange={(e) => setResult(e.target.value)}>
        <option value="ok">通过</option>
        <option value="failed">失败(如实记录)</option>
      </select>
      <input className="tk-input" data-field="drill-report" value={report} onChange={(e) => setReport(e.target.value)} placeholder="恢复校验报告(恢复演练必填,如:回读校验 5/5 表校验和一致)" aria-label="恢复校验报告" />
      <input className="tk-input" data-field="drill-notes" value={notes} onChange={(e) => setNotes(e.target.value)} placeholder="备注(可选)" aria-label="演练备注" />
      <button className="tk-button" type="submit" disabled={busy || !targetText.trim()} data-action="drill-record">
        记录演练
      </button>
    </form>
  );
}
