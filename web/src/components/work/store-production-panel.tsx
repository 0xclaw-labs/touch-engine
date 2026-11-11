"use client";

import { useCallback, useEffect, useState } from "react";
import { failureText, useSession } from "@/components/work/merchant-session";
import { surfaceLabel } from "@/lib/product-finish";

// HUI-4595 门店真实制作接线(/work/materials「帮我制作」区)。
// 纪律(票面/裁定书,复用 production-panel/production.ts 诚实表述家风):
//   - 能力如实申报:受信生产方未注册=按钮禁用+原因,绝不伪进度;
//   - 发起前必须已有「已确认报价引用」(quote_ref,来自账单权威;wave2 集成
//     后权威=本仓账单投影,核验不过发起即拒);账单权威未接入时如实标注
//     「报价须先经账单权威确认,费用状态以账单为准」;
//   - 反复恢复同一制作请求不新建制作工程:幂等键自动生成并在成功后原样
//     展示,重试走同一键(同一发起只回同一 handoff);
//   - 状态独立可辨:未提交/处理中/已产出/失败/已取消;task_ref 是真实平台
//     任务号,没有就不显示,不伪造;
//   - 回流产物只进候选:预览在门店内容包抽屉,采用必须显式;本区不自动
//     采用/外发;顾客触碰零新生成零计费。

interface PackOption {
  id: string;
  title: string;
  legacy?: boolean;
}

interface ProductionResultRow {
  id: string;
  source_app?: string;
  event_id?: string;
  outcome: string;
  task_ref?: string;
  result_revision?: number;
  charge_ref?: string;
  version_id?: string;
  reason?: string;
  created_at?: string;
}

interface ProductionHandoffRow {
  id: string;
  pack_id?: string;
  content_pack_ref?: string;
  content_binding_ref?: string;
  quote_ref: string;
  quote_verified: boolean;
  status: string;
  task_ref?: string;
  last_charge_ref?: string;
  last_result_revision?: number;
  note?: string;
  created_at?: string;
  results?: ProductionResultRow[];
}

interface Capabilities {
  production_available: boolean;
  producer_configured: boolean;
  quote_authority_configured: boolean;
  task_authority_configured: boolean;
  reason?: string;
}

function statusCopy(status: string): string {
  switch (status) {
    case "recorded":
      return "已登记(未提交)";
    case "submitted":
      return "处理中";
    case "produced":
      return "已产出(待采用)";
    case "failed":
      return "失败";
    case "canceled":
      return "已取消";
    default:
      return status;
  }
}

function outcomeCopy(outcome: string): string {
  switch (outcome) {
    case "produced":
      return "产出候选";
    case "failed":
      return "失败回执";
    case "canceled":
      return "取消回执";
    default:
      return outcome;
  }
}

function randomKey(): string {
  const rnd = Math.random().toString(36).slice(2, 10);
  return `cph-${Date.now().toString(36)}-${rnd}`;
}

export function StoreProductionPanel({ storeId }: { storeId: string }) {
  const session = useSession();
  const [phase, setPhase] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState("");
  const [caps, setCaps] = useState<Capabilities | null>(null);
  const [packs, setPacks] = useState<PackOption[]>([]);
  const [handoffs, setHandoffs] = useState<ProductionHandoffRow[]>([]);
  const [packID, setPackID] = useState("");
  const [quoteRef, setQuoteRef] = useState("");
  const [idempotencyKey, setIdempotencyKey] = useState(randomKey);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState("");
  const [noteKind, setNoteKind] = useState<"ok" | "unknown" | "danger">("ok");

  const load = useCallback(async () => {
    setPhase("loading");
    const capsRes = await session.api("GET", `stores/${storeId}/content-production-handoffs/capabilities`).catch(() => null);
    if (capsRes && capsRes.ok) {
      setCaps(capsRes.data as unknown as Capabilities);
    } else {
      // 能力读不到 = 不可用如实呈现,不猜。
      setCaps(null);
    }
    const packsRes = await session.api("GET", `stores/${storeId}/content-packs`).catch(() => null);
    if (packsRes && packsRes.ok && Array.isArray(packsRes.data.items)) {
      const options = (packsRes.data.items as PackOption[]).filter((p) => !p.legacy);
      setPacks(options);
      setPackID((cur) => cur || options[0]?.id || "");
    } else {
      setPacks([]);
    }
    const listRes = await session.api("GET", `stores/${storeId}/content-production-handoffs`).catch(() => null);
    setHandoffs(listRes && listRes.ok && Array.isArray(listRes.data.handoffs) ? (listRes.data.handoffs as ProductionHandoffRow[]) : []);
    setError("");
    setPhase("ready");
  }, [session, storeId]);

  useEffect(() => {
    void load().catch(() => {
      setError("没有连上服务。制作能力与发起记录没有读到,不把它们当成空。");
      setPhase("error");
    });
  }, [load]);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    if (!packID || !quoteRef.trim()) {
      setError("先选内容包并填入已确认的报价引用,再发起制作。");
      return;
    }
    setBusy(true);
    setError("");
    setNote("");
    try {
      const res = await session.api("POST", `stores/${storeId}/content-production-handoffs`, {
        content_pack_ref: `pack:${packID}`,
        quote_ref: quoteRef.trim(),
        idempotency_key: idempotencyKey,
      });
      const data = (res.data ?? {}) as {
        handoff?: ProductionHandoffRow;
        replayed?: boolean;
        notes?: string[];
        error?: string;
        message?: string;
      };
      if (res.status === 201 || res.status === 200) {
        const h = data.handoff;
        const taskNote = h?.task_ref ? `任务 ${h.task_ref}` : "";
        setNoteKind("ok");
        setNote(
          (data.replayed ? "同一发起已存在,返回原记录(不重复建工程、不重复报价)。" : "发起已记录。") +
            (h?.status === "submitted" ? `已提交平台任务${taskNote ? "(" + taskNote + ")" : ""},处理中。` : "") +
            (h && h.status !== "submitted" ? "公共任务权威未接通,制作暂不开始——这是如实的未接通状态。" : "") +
            (data.notes && data.notes.length ? " " + data.notes.join(" ") : ""),
        );
        // 成功后换新幂等键:下一次发起是新的制作请求。
        setIdempotencyKey(randomKey());
        setQuoteRef("");
        await load();
        return;
      }
      if (res.status === 503) {
        // 诚实降级:发起已落库,但生产方/权威未接通,制作没有开始。
        setNoteKind("unknown");
        setNote(
          (data.message || "制作生产方尚未接入:这次发起已记下但没有送去做。") +
            (data.notes && data.notes.length ? " " + data.notes.join(" ") : ""),
        );
        setIdempotencyKey(randomKey());
        await load();
        return;
      }
      setError(failureText(res.status, data));
    } catch {
      setError("没有完成(没有连上服务)。这次发起还没有提交。");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="tk-section" data-testid="store-production-panel" data-store-id={storeId}>
      <h3 className="tk-label">发起真实制作(不需要先有活动或已采用内容)</h3>
      <p className="tk-note">
        选一个内容包(可以是空包),填入账单里已确认的报价引用,提交后由受信制作生产方按真实任务制作;成片回到候选区预览后由你明确采用。
      </p>
      {phase === "loading" ? <p className="tk-state" data-state="loading">{surfaceLabel("loading")}</p> : null}
      {phase === "error" ? (
        <p className="tk-state tk-danger" data-state="error">
          {error}
          <button className="tk-quiet" type="button" data-action="retry" onClick={() => void load()}>重试</button>
        </p>
      ) : null}
      {phase === "ready" ? (
        <>
          <p className="tk-note tk-unknown" data-field="produce-reason">
            {caps ? caps.reason : "制作能力没有读到:发起按钮先保持不可用。上传已有内容和从素材库选择不受影响。"}
          </p>
          <form className="tk-form" data-entry="production-initiate" onSubmit={(event) => void submit(event)}>
            <label className="tk-label">
              内容包
              <select
                className="tk-select"
                aria-label="选择内容包"
                value={packID}
                onChange={(event) => setPackID(event.target.value)}
                required
              >
                <option value="">选择内容包</option>
                {packs.map((p) => (
                  <option key={p.id} value={p.id}>{p.title}</option>
                ))}
              </select>
            </label>
            {packs.length === 0 ? (
              <p className="tk-note tk-unknown">这个门店还没有内容包。先在下面「新包」里起个名字——空包也能发起首次制作。</p>
            ) : null}
            <label className="tk-label">
              已确认报价引用(quote_ref,来自账单中心)
              <input
                className="tk-input"
                name="quote_ref"
                type="text"
                value={quoteRef}
                onChange={(event) => setQuoteRef(event.target.value)}
                aria-label="已确认报价引用"
                required
              />
            </label>
            <p className="tk-note" data-field="quote-note">
              报价在生成前确认、生成前可见;重复提交同一发起不会重复计费。
              {caps && !caps.quote_authority_configured ? " 账单权威未接入:报价引用须先经账单权威确认并落进投影,否则发起会被拒;费用状态以账单为准。" : ""}
            </p>
            <button
              className="tk-button"
              type="submit"
              data-action="produce"
              disabled={busy || !caps?.production_available}
            >
              发起制作
            </button>
            <p className="tk-note" data-field="idempotency-note">本次发起凭据:{idempotencyKey}(重试走同一凭据,不会建两个制作工程)</p>
          </form>
          {note ? (
            <p className={noteKind === "ok" ? "tk-note tk-ok" : "tk-note tk-unknown"} data-field="production-result">{note}</p>
          ) : null}
          {error && phase === "ready" ? <p className="tk-danger" data-state="error">{error}</p> : null}

          <ul className="tk-list" data-field="handoff-list">
            {handoffs.length === 0 ? (
              <p className="tk-note">这个门店还没有制作发起记录。</p>
            ) : null}
            {handoffs.map((h) => (
              <li key={h.id} data-handoff-id={h.id} data-status={h.status}>
                <p className="tk-note">
                  发起 {h.id} · {statusCopy(h.status)}
                  {h.quote_verified ? " · 报价已核验" : " · 报价按申报记录(费用以账单为准)"}
                </p>
                <p className="tk-note">
                  内容包 {h.content_pack_ref ?? h.pack_id}
                  {h.task_ref ? ` · 平台任务 ${h.task_ref}` : " · 尚无平台任务(未接通不伪造)"}
                  {h.last_charge_ref ? ` · 费用引用 ${h.last_charge_ref}` : ""}
                </p>
                {(h.results ?? []).length > 0 ? (
                  <ul className="tk-list">
                    {(h.results ?? []).map((r) => (
                      <li key={r.id} data-result-id={r.id} data-outcome={r.outcome}>
                        <p className="tk-note">
                          {outcomeCopy(r.outcome)} 第 {r.result_revision} 期
                          {r.version_id ? " · 已登记候选(到上方门店内容包预览、采用)" : ""}
                          {r.reason ? ` · ${r.reason}` : ""}
                        </p>
                      </li>
                    ))}
                  </ul>
                ) : null}
              </li>
            ))}
          </ul>
        </>
      ) : null}
    </section>
  );
}
