"use client";

// HUI-4592 O2 套餐与价格:目录全集读面 + 窗口发布/下架命令(统一操作信封)。
//
// 纪律(票面):
//   - 金额全部来自 4591 目录投影,本页零价格常量、不能编辑金额/条款——
//     价格内容归公共价格权威,运营台只做生效窗口的生命周期控制;
//   - 发布/下架不改历史订单:订单钉自己的价格快照(页面明示该纪律)。

import { useCallback, useEffect, useState } from "react";
import { useMerchantSession, type MerchantSession } from "@/components/work/merchant-session";
import {
  OperationFeedback,
  OpsReasonForm,
  oneIdempotencyKey,
  postOperation,
  type EnvelopeOutcome,
} from "@/components/ops/ops-shell";
import { formatAmount, type OpsPlanView } from "@/lib/ops-console";

type Phase = "loading" | "error" | "ready";

export function PlansDesk() {
  const session = useMerchantSession();
  const [phase, setPhase] = useState<Phase>("loading");
  const [error, setError] = useState("");
  const [plans, setPlans] = useState<OpsPlanView[]>([]);
  const [busy, setBusy] = useState(false);
  const [outcome, setOutcome] = useState<EnvelopeOutcome | null>(null);
  const [opError, setOpError] = useState("");

  const load = useCallback(async (ses: MerchantSession) => {
    setPhase("loading");
    setError("");
    const res = await ses.api("GET", "ops/plans");
    if (!res.ok) {
      setError(`目录读取失败(${res.status})`);
      setPhase("error");
      return;
    }
    setPlans(Array.isArray(res.data.plans) ? (res.data.plans as OpsPlanView[]) : []);
    setPhase("ready");
  }, []);

  useEffect(() => {
    void load(session);
  }, [load, session]);

  const runOp = useCallback(
    async (ses: MerchantSession, action: string, ref: string, reason: string) => {
      setBusy(true);
      setOpError("");
      const res = await postOperation(ses, {
        action,
        resource_ref: ref,
        reason,
        idempotency_key: oneIdempotencyKey(),
      });
      setBusy(false);
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
    <section className="tk-section" data-view="ops-plans">
      <h2 className="tk-section-title">套餐与价格</h2>
      <p className="tk-note">
        目录行来自公共价格权威投影;这里只控制生效窗口(发布生效/下架停售),不能修改金额与条款。
        历史订单引用下单时的价格快照,窗口变更不会改写任何已支付订单。
      </p>
      {phase === "loading" ? <p className="tk-state" data-state="loading">正在读取目录…</p> : null}
      {phase === "error" ? <p className="tk-state tk-danger" data-state="error">{error}</p> : null}
      {phase === "ready" && plans.length === 0 ? (
        <p className="tk-state" data-state="empty">
          目录为空:价格由公共价格权威经受信缝发布;空目录 = 还没有发布过任何价格版本。
        </p>
      ) : null}
      <ul className="tk-list" data-section="plan-list">
        {plans.map((p) => (
          <li key={p.id} className="tk-row" data-plan-id={p.id} data-window={p.window_state}>
            <span data-field="plan">
              {p.plan_code} · {p.pricing_version}
            </span>
            <span data-field="price">
              {formatAmount(p.price_minor, p.currency)} / {p.service_days} 天 · 含 {p.included_store_count} 店
            </span>
            <span data-field="scope">{p.scope_tenant ? `租户定向 ${p.scope_tenant}` : "全局默认"}</span>
            <span data-field="window">
              {p.effective ? "生效中" : p.retired ? "已停售" : "未生效"}
              {p.effective_from ? ` · 自 ${p.effective_from}` : ""}
              {p.effective_to ? ` · 至 ${p.effective_to}` : ""}
            </span>
            {p.effective || p.window_state === "not_yet_effective" ? (
              <OpsReasonForm
                label="下架停售"
                busy={busy}
                onSubmit={(reason) => void runOp(session, "plan.retire", p.id, reason)}
              />
            ) : null}
            {p.retired || (!p.effective && p.window_state !== "not_yet_effective") ? (
              <OpsReasonForm
                label="发布生效"
                busy={busy}
                onSubmit={(reason) => void runOp(session, "plan.publish", p.id, reason)}
              />
            ) : null}
          </li>
        ))}
      </ul>
      <OperationFeedback outcome={outcome} error={opError} />
    </section>
  );
}
