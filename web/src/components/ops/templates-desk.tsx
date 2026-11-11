"use client";

// HUI-4592 O5 模板与内容:跨租户模板目录/分发引用/内容版本风险态读面 +
// 撤回命令(统一操作信封分派到各域唯一写面)。
//
// 纪律(票面):
//   - 本页管理目录引用、分发与撤回命令;资产与发布归 1669/3553/1676,
//     不复制资产、不代写点评文案;
//   - 撤回分发 = 4593 分发行 cancelled(总部撤回);风险下架 = 3553 版本
//     blocked(blocked_reason = 运营原因),历史证据不被覆盖。

import { useCallback, useEffect, useState } from "react";
import { useMerchantSession, type MerchantSession } from "@/components/work/merchant-session";
import {
  OperationFeedback,
  OpsReasonForm,
  oneIdempotencyKey,
  postOperation,
  type EnvelopeOutcome,
} from "@/components/ops/ops-shell";
import type { OpsAssignmentRow, OpsContentVersionRow, OpsTemplateRow } from "@/lib/ops-console";

type Phase = "loading" | "error" | "ready";

interface TemplatesPayload {
  templates: OpsTemplateRow[];
  content_versions: OpsContentVersionRow[];
  assignments: OpsAssignmentRow[];
}

export function TemplatesDesk() {
  const session = useMerchantSession();
  const [phase, setPhase] = useState<Phase>("loading");
  const [error, setError] = useState("");
  const [data, setData] = useState<TemplatesPayload | null>(null);
  const [busy, setBusy] = useState(false);
  const [outcome, setOutcome] = useState<EnvelopeOutcome | null>(null);
  const [opError, setOpError] = useState("");
  const [target, setTarget] = useState<{ kind: "assignment" | "content"; row: OpsAssignmentRow | OpsContentVersionRow } | null>(null);

  const load = useCallback(async (ses: MerchantSession) => {
    setPhase("loading");
    setError("");
    const res = await ses.api("GET", "ops/templates");
    if (!res.ok) {
      setError(`模板目录读取失败(${res.status})`);
      setPhase("error");
      return;
    }
    setData({
      templates: Array.isArray(res.data.templates) ? (res.data.templates as OpsTemplateRow[]) : [],
      content_versions: Array.isArray(res.data.content_versions)
        ? (res.data.content_versions as OpsContentVersionRow[])
        : [],
      assignments: Array.isArray(res.data.assignments) ? (res.data.assignments as OpsAssignmentRow[]) : [],
    });
    setPhase("ready");
  }, []);

  useEffect(() => {
    void load(session);
  }, [load, session]);

  const runOp = useCallback(
    async (ses: MerchantSession, action: string, ref: string, tenantId: string, packId: string, reason: string) => {
      setBusy(true);
      setOpError("");
      const res = await postOperation(ses, {
        action,
        resource_ref: ref,
        tenant_id: tenantId,
        pack_id: packId,
        reason,
        idempotency_key: oneIdempotencyKey(),
      });
      setBusy(false);
      setTarget(null);
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
    <section className="tk-section" data-view="ops-templates">
      <h2 className="tk-section-title">模板与内容</h2>
      <p className="tk-note">
        行业首页模板与内容模板的目录引用、门店分发与撤回命令;模板资产与发布归各自域,
        本页不复制资产、不生成点评文案。已采用/正在办理的内容按冻结版本策略处理,撤回不覆盖历史证据。
      </p>
      {phase === "loading" ? <p className="tk-state" data-state="loading">正在读取模板目录…</p> : null}
      {phase === "error" ? <p className="tk-state tk-danger" data-state="error">{error}</p> : null}
      {phase === "ready" && data && data.templates.length === 0 ? (
        <p className="tk-state" data-state="empty">还没有模板目录行。</p>
      ) : null}

      <ul className="tk-list" data-section="template-list">
        {(data?.templates ?? []).map((t) => (
          <li key={`${t.tenant_id}:${t.id}`} className="tk-row" data-template-id={t.id} data-tenant={t.tenant_id}>
            <span data-field="template-name">{t.name}</span>
            <span className="tk-quiet" data-field="template-meta">
              商户 {t.tenant_id} · 版本 {t.version_count}(已发布 {t.published_count}) · 门店分发引用 {t.assignment_count}
            </span>
          </li>
        ))}
      </ul>

      <div className="tk-section" data-section="assignment-list">
        <h3 className="tk-section-title">门店分发</h3>
        {(data?.assignments.length ?? 0) === 0 ? (
          <p className="tk-state" data-state="empty">没有分发引用。</p>
        ) : (
          <ul className="tk-list">
            {data?.assignments.map((a) => (
              <li key={a.id} className="tk-row" data-assignment-id={a.id} data-status={a.status}>
                <span>
                  {a.template_ref} v{a.source_version} → 门店 {a.store_id}
                </span>
                <span className="tk-quiet">商户 {a.tenant_id} · {a.status === "distributed" ? "已分发" : a.status === "applied" ? "已应用" : a.status}</span>
                {a.status === "distributed" ? (
                  <button
                    className="tk-button"
                    data-action="assignment-cancel"
                    onClick={() => setTarget({ kind: "assignment", row: a })}
                  >
                    撤回分发
                  </button>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </div>

      <div className="tk-section" data-section="content-versions">
        <h3 className="tk-section-title">内容版本风险态</h3>
        {(data?.content_versions.length ?? 0) === 0 ? (
          <p className="tk-state" data-state="empty">没有内容版本投影。</p>
        ) : (
          <ul className="tk-list">
            {data?.content_versions.map((v) => (
              <li
                key={v.version_id}
                className="tk-row"
                data-version-id={v.version_id}
                data-status={v.status}
              >
                <span>
                  包 {v.pack_id} v{v.version} · 商户 {v.tenant_id} ·{" "}
                  {v.status === "blocked" ? `已下架(${v.blocked_reason})` : v.status}
                </span>
                {v.status !== "blocked" ? (
                  <button
                    className="tk-button"
                    data-action="content-retract"
                    onClick={() => setTarget({ kind: "content", row: v })}
                  >
                    风险下架
                  </button>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </div>

      {target ? (
        <div data-section="template-action-form">
          <p className="tk-note">
            {target.kind === "assignment"
              ? `撤回分发 ${(target.row as OpsAssignmentRow).template_ref} → 门店 ${(target.row as OpsAssignmentRow).store_id}:撤回后门店不再收到新分发,已应用版本按冻结策略保留。`
              : `风险下架内容版本 ${(target.row as OpsContentVersionRow).version_id}:下架原因写入 blocked_reason,历史参与证据不被覆盖。`}
          </p>
          <OpsReasonForm
            label={target.kind === "assignment" ? "确认撤回分发" : "确认风险下架"}
            busy={busy}
            onSubmit={(reason) => {
              if (target.kind === "assignment") {
                const a = target.row as OpsAssignmentRow;
                void runOp(session, "template.assignment.cancel", a.id, a.tenant_id, "", reason);
              } else {
                const v = target.row as OpsContentVersionRow;
                void runOp(session, "content.retract", v.version_id, v.tenant_id, v.pack_id, reason);
              }
            }}
          />
        </div>
      ) : null}

      <OperationFeedback outcome={outcome} error={opError} />
    </section>
  );
}
