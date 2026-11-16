"use client";

import { useCallback, useEffect, useState } from "react";
import { failureText, useSession } from "@/components/work/merchant-session";
import { ensureContentPack, productionPayload, quoteIsUsable, type ProductionPayload, type ProductionQuote } from "@/lib/work/content-editor-flow";

interface Handoff { id: string; status: string; task_ref?: string; note?: string; created_at?: string; quote_verified?: boolean; results?: { id: string; outcome: string; version_id?: string; reason?: string }[] }
interface Caps { production_available: boolean; reason?: string }
function randomKey() { return `content-${crypto.randomUUID()}`; }
function statusCopy(status: string) { return ({ recorded: "已登记，尚未确认任务受理", submitted: "处理中", produced: "已产出，待预览采用", failed: "制作失败", canceled: "已取消" } as Record<string, string>)[status] || "状态待核实"; }
function productionFailure(status: number, data: Record<string, unknown>) {
  const messages: Record<string, string> = {
    payment_authorization_required: "当前账号没有制作付款授权。请有付款权限的负责人确认费用；已保存的内容仍可继续编辑。",
    quote_expired: "报价已过期。请重新获取并核对费用后确认，本次没有新建制作任务。",
    quote_changed: "报价或预占状态与原确认不一致。请先回查原账单和制作记录，不要另建收费请求。",
    quote_conflict: "原报价或请求状态尚需核实。请刷新回查同一请求，避免重复付款。",
    billing_insufficient_funds: "当前付款账户可用额度不足。请到原账单中心处理后恢复同一请求。",
    quote_projection_pending: "账务已确认预占，制作记录正在同步。请恢复同一请求，不要重新付款。",
    quote_configuration_missing: "当前组织的制作付款账户或交付服务尚未接通。请联系服务支持完成接线后重试。",
    production_unavailable: "制作服务暂不可用，本次未新预占费用。请稍后恢复同一请求。",
  };
  return messages[String(data.error)] || failureText(status, data);
}
function taskTitle(h: Handoff) {
  try { const brief = JSON.parse(h.note || "{}"); if (typeof brief.title === "string" && brief.title.trim()) return brief.title; }
  catch { /* Existing tasks may carry a plain-text brief. */ }
  return h.note?.trim().slice(0, 100) || "内容制作";
}

export function StoreProductionPanel({ storeId, campaignId, packId, title = "门店内容", brief = {}, onChanged }: {
  storeId: string; campaignId?: string; packId?: string; title?: string; brief?: Record<string, unknown>; onChanged?: () => void;
}) {
  const session = useSession();
  const storageKey = `touch-content-production:${session.tenantId}:${storeId}:${campaignId || "store"}`;
  const note = JSON.stringify(brief);
  const [caps, setCaps] = useState<Caps | null>(null);
  const [handoffs, setHandoffs] = useState<Handoff[]>([]);
  const [quote, setQuote] = useState<(ProductionQuote & { offer_token: string }) | null>(null);
  const [quotedNote, setQuotedNote] = useState("");
  const [quotePack, setQuotePack] = useState("");
  const [requestKey, setRequestKey] = useState("");
  const [pending, setPending] = useState<ProductionPayload | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [listError, setListError] = useState("");

  const load = useCallback(async () => {
    const [cap, list] = await Promise.all([
      session.api("GET", `stores/${storeId}/content-production-handoffs/capabilities`),
      session.api("GET", `stores/${storeId}/content-production-handoffs`),
    ]);
    setCaps(cap.ok ? cap.data as unknown as Caps : null);
    if (!list.ok) { setListError(failureText(list.status, list.data)); return; }
    setHandoffs(Array.isArray(list.data.handoffs) ? list.data.handoffs as Handoff[] : []);
    setListError("");
  }, [session, storeId]);
  useEffect(() => {
    setQuote(null); setError(""); setNotice("");
    try {
      const saved = JSON.parse(localStorage.getItem(storageKey) || "null") as { key?: string; pending?: ProductionPayload } | null;
      setRequestKey(saved?.key || randomKey()); setPending(saved?.pending || null);
    } catch { setRequestKey(randomKey()); setPending(null); }
    void load().catch(() => setListError("制作记录尚未读到，请刷新回查；不能把断网当作没有任务。"));
  }, [storageKey, load]);

  async function getQuote() {
    if (!title.trim() || !String(brief.body || "").trim() || !String(brief.store_name || "").trim()) {
      setError("请先补充标题、正文和门店资料，再获取当前用途的制作报价。"); return;
    }
    setBusy(true); setError(""); setNotice(""); setQuote(null);
    try {
      const packsResult = await session.api("GET", `stores/${storeId}/content-packs`);
      if (!packsResult.ok) throw new Error(failureText(packsResult.status, packsResult.data));
      const packs = Array.isArray(packsResult.data.items) ? packsResult.data.items as { id: string; title: string; legacy?: boolean; campaign_id?: string }[] : [];
      const targetPack = packId || await ensureContentPack(session.api, storeId, packs, title, campaignId);
      // A deliberate new quote after a prior offer gets a fresh usage key.
      const key = quotedNote ? randomKey() : requestKey || randomKey();
      setQuotedNote("");
      setRequestKey(key); localStorage.setItem(storageKey, JSON.stringify({ key }));
      const res = await session.api("POST", `stores/${storeId}/content-production-handoffs/quote`, { content_pack_ref: `pack:${targetPack}`, idempotency_key: key, note });
      if (!res.ok) {
        setError(productionFailure(res.status, res.data));
        if (res.data.can_requote === true) { const renewedKey=randomKey(); setRequestKey(renewedKey); localStorage.setItem(storageKey, JSON.stringify({ key: renewedKey })); }
        return;
      }
      const next = res.data.quote as ProductionQuote & { offer_token: string };
      if (!quoteIsUsable(next) || !next.offer_token) { setError("公共账务报价缺少金额、付款主体、交付内容或有效期，请稍后重新获取。没有开始制作或预占。"); return; }
      setQuote(next); setQuotedNote(note); setQuotePack(targetPack);
      setNotice(String(res.data.notice || "尚未预占或扣费。请核对报价后明确确认。"));
    } catch (e) { setError(e instanceof Error ? e.message : "报价响应未确认，请重试获取同一请求。不会开始收费制作。"); }
    finally { setBusy(false); }
  }

  async function submit(payload: ProductionPayload) {
    setBusy(true); setError("");
    // Save before sending: a lost response/reload must replay identical payload.
    try { localStorage.setItem(storageKey, JSON.stringify({ key: payload.idempotency_key, pending: payload })); }
    catch {
      setError("此浏览器暂时无法保存原请求，本次尚未提交或预占费用。请恢复浏览器存储后再确认。");
      setBusy(false); return;
    }
    setPending(payload);
    try {
      const res = await session.api("POST", `stores/${storeId}/content-production-handoffs`, payload);
      const h = res.data.handoff as Handoff | undefined;
      if (!h) {
        setError(productionFailure(res.status, res.data));
        if (res.data.can_requote === true) {
          setPending(null); setQuote(null); setQuotedNote(note);
          localStorage.setItem(storageKey, JSON.stringify({ key: randomKey() }));
        }
        return;
      }
      setNotice(`${res.data.replayed ? "已找回同一制作请求，不重复预占或创建任务。" : "制作请求已登记。"}${statusCopy(h.status)}。成片只进入候选，需预览后明确采用。`);
      setPending(null); setQuote(null); setQuotedNote("");
      const key = randomKey(); setRequestKey(key);
      try { localStorage.setItem(storageKey, JSON.stringify({ key })); } catch { /* The persisted original request remains safe to replay. */ }
      await load().catch(() => setListError("制作请求已登记，列表暂未刷新。请刷新制作状态回查原记录。")); onChanged?.();
    } catch { setError("制作结果尚未确认。已保留同一请求，请点「恢复同一请求」回查；不要重新付款或新建制作。"); }
    finally { setBusy(false); }
  }
  async function retry(id: string) {
    setBusy(true); setError("");
    try {
      const res = await session.api("POST", `stores/${storeId}/content-production-handoffs/${id}/retry`, {});
      if (!res.ok) setError(productionFailure(res.status, res.data));
      else setNotice("已恢复同一制作任务，公共任务按同一凭据去重；不会重复预占。");
      await load(); onChanged?.();
    } catch { setError("任务受理结果尚未确认，请刷新回查同一记录。"); }
    finally { setBusy(false); }
  }
  const currentQuote = quotedNote === note && quoteIsUsable(quote);
  return <section className="tk-section" data-testid="store-production-panel" data-store-id={storeId}>
    <h3 className="tk-label">当前用途，就地报价制作</h3>
    <p className="tk-note">复用上方文案与平台。首次所需内容包由系统准备；获取报价不会预占或扣费，明确确认后按公共账务合同预占。</p>
    <p className="tk-note tk-unknown" data-field="produce-reason">{caps?.reason || "制作能力尚未读到，请刷新重试。上传与选择已有内容仍可使用。"}</p>
    {!pending ? <button type="button" className="tk-button" data-action="request-quote" disabled={busy || !caps?.production_available} onClick={() => void getQuote()}>获取制作报价</button> : null}
    {quote ? <div className="tk-section" data-field="production-quote">
      <p className="tk-note">交付内容：{quote.deliverables}</p>
      <p className="tk-note">金额：{quote.currency} {(quote.amount_minor / 100).toFixed(2)}</p>
      <p className="tk-note">付款安排：使用「{quote.payer_context || session.tenantName || "当前组织"}」配置的制作付款账户。</p>
      <details><summary>核对付款账户</summary><p className="tk-note">账户标识：{quote.payer}。账户名称未由账务服务返回，请有付款权限的负责人核对。</p></details>
      <p className="tk-note">报价有效期：{new Date(quote.expires_at).toLocaleString("zh-CN")}</p>
      <p className="tk-note">确认后预占上述费用并提交制作，结算由原公共账务与生产方完成。回流候选不会自动采用或上线。</p>
      {!currentQuote ? <p className="tk-unknown">用途资料已变化或报价已过期，请重新获取并确认。</p> : null}
      <button type="button" className="tk-button" data-action="produce" disabled={busy || !currentQuote || Boolean(pending)} onClick={() => {
        if (currentQuote && quote) void submit(productionPayload(quotePack, quote, requestKey, note));
      }}>确认费用并提交制作</button>
    </div> : null}
    {pending ? <div className="tk-section"><p className="tk-note tk-unknown">有一笔已确认请求待回查。恢复只发送原始负载，不重新获取报价或预占第二笔费用。</p><button type="button" className="tk-button" disabled={busy} onClick={() => void submit(pending)}>恢复同一请求</button></div> : null}
    {notice ? <p className="tk-note" role="status">{notice}</p> : null}
    {error ? <p className="tk-danger" role="alert">{error}</p> : null}
    {listError ? <p className="tk-danger">{listError}</p> : null}
    <button type="button" className="tk-quiet" onClick={() => void load().catch(() => setListError("制作记录尚未读到，请稍后重试。"))}>刷新制作状态</button>
    <ul className="tk-list" data-field="handoff-list">{handoffs.map((h) => <li key={h.id} data-handoff-id={h.id} data-status={h.status}>
      <p className="tk-note"><strong>{taskTitle(h)}</strong>{h.created_at ? ` · ${new Date(h.created_at).toLocaleString("zh-CN")}` : ""}</p>
      <p className="tk-note">{statusCopy(h.status)}{h.quote_verified ? " · 公共账务报价已核验" : " · 费用状态以账单为准"}</p>
      {h.status === "recorded" ? <button type="button" className="tk-quiet" disabled={busy} onClick={() => void retry(h.id)}>恢复提交同一任务</button> : null}
      {h.status === "failed" || h.status === "canceled" ? <p className="tk-note">当前任务已终止；费用释放或退款以公共账务事实为准。请先查看原账单或联系服务支持，重新制作需重新报价与确认。</p> : null}
      {(h.results || []).map((result) => <p key={result.id} className="tk-note">{result.version_id ? "候选已回流，请在下方预览并采用。" : result.reason || "等待可用成片，未取得成片不视为交付完成。"}</p>)}
      <details><summary>受控诊断</summary><p className="tk-note">请求：{h.id} · {h.task_ref ? `公共任务：${h.task_ref}` : "尚无公共任务受理引用"}</p></details>
    </li>)}</ul>
  </section>;
}
