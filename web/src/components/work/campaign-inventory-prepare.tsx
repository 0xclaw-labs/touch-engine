"use client";

import { useEffect, useState } from "react";
import { failureText, useSession } from "@/components/work/merchant-session";
import { benefitKindLabel, verificationModeLabel, type PublishConfigRecord } from "@/lib/campaign-config";
import { formatMoney, inventoryPreparationBody, type RewardInventoryView } from "@/lib/reward-ops";
import { formatCampaignWindow } from "@/lib/public-time";

type Draft = { requestKey: string; name: string; quantity: string; codes: string; face: string; cost: string };
type RequestPhase = "draft" | "pending" | "confirmed";
const freshDraft = (): Draft => ({ requestKey: crypto.randomUUID(), name: "", quantity: "", codes: "", face: "", cost: "" });

export function CampaignInventoryPrepare({ campaignId, revision = 0, onDone }: { campaignId: string; revision?: number; onDone?: (message: string) => void }) {
  const session = useSession();
  const storageKey = `touch.inventory.prepare:${session.tenantId}:${campaignId}`;
  const [config, setConfig] = useState<PublishConfigRecord | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(true);
  const [recovering, setRecovering] = useState(true);
  const [recoveryFailed, setRecoveryFailed] = useState(false);
  const [draft, setDraft] = useState<Draft>({ requestKey: "", name: "", quantity: "", codes: "", face: "", cost: "" });
  const [requestPhase, setRequestPhase] = useState<RequestPhase>("draft");
  const [requestVersion,setRequestVersion]=useState(0);
  const [batch, setBatch] = useState<RewardInventoryView | null>(null);
  const [reload, setReload] = useState(0);

  function checkpoint(next: Draft, phase: RequestPhase, expectedVersion = requestVersion) {
    localStorage.setItem(storageKey, JSON.stringify({ ...next, requestPhase: phase, requestVersion: expectedVersion }));
  }

  useEffect(() => {
    let alive = true;
    setLoading(true); setError(""); setConfig(null);
    void session.api("GET", `campaigns/${campaignId}/publish-config`).then((res) => {
      if (!alive) return;
      if (!res.ok) { setError(res.status === 404 ? "先在活动页保存顾客承诺，再准备关联库存。" : failureText(res.status, res.data)); return; }
      setConfig(res.data as PublishConfigRecord);
    }).catch(() => { if (alive) setError("活动承诺没有读到，请重试。"); }).finally(() => { if (alive) setLoading(false); });
    return () => { alive = false; };
  }, [campaignId, revision, reload, session]);

  useEffect(() => {
    let alive = true;
    let next = freshDraft();
    let phase: RequestPhase = "draft";
    let hasCheckpoint = false;
    let originalVersion=0;
    try {
      const saved = JSON.parse(localStorage.getItem(storageKey) || "null") as Record<string, unknown> | null;
      if (saved && Object.keys(next).every((key) => typeof saved[key] === "string") && saved.requestKey) {
        hasCheckpoint = true;
        if(typeof saved.requestVersion === "number" && saved.requestVersion>0)originalVersion=saved.requestVersion;
        next = Object.fromEntries(Object.keys(next).map((key) => [key, saved[key]])) as Draft;
        if (saved.requestPhase === "pending" || saved.requestPhase === "confirmed") phase = saved.requestPhase;
      }
    } catch { /* A damaged local draft cannot assert that stock was created. */ }
    setRequestVersion(originalVersion);
    setDraft(next); setRequestPhase(phase); setBatch(null); setRecovering(true); setRecoveryFailed(false);
    if (!hasCheckpoint) { setRecovering(false); return () => { alive = false; }; }
    // Read the original record even when an old editable form no longer matches
    // the accepted payload. A read never prepares stock or consumes a budget.
    void session.api("GET", `campaigns/${campaignId}/reward-inventory/prepare?request_key=${encodeURIComponent(next.requestKey)}`).then((res) => {
      if (!alive) return;
      if (res.ok) { setBatch(res.data as unknown as RewardInventoryView); setRequestPhase("confirmed"); }
      else if (res.status === 404 && phase === "pending" && originalVersion === 0) {
        setRequestPhase("draft");
      } else if (res.status !== 404 || phase === "confirmed") {
        setRecoveryFailed(true); setError("上次库存请求还没有回查成功。请重新读取并核对库存，暂不提交新批次。");
      }
    }).catch(() => { if (alive) { setRecoveryFailed(true); setError("上次库存请求还没有回查成功，请重新读取。"); } }).finally(() => { if (alive) setRecovering(false); });
    return () => { alive = false; };
  }, [campaignId, storageKey, session, reload]);

  function change(field: keyof Draft, value: string) {
    if (requestPhase !== "draft" || batch || busy || recovering || recoveryFailed) return;
    const next = { ...draft, [field]: value };
    setDraft(next);
    try { checkpoint(next, "draft"); } catch { setError("浏览器无法保存当前草稿，请保留此页并允许网站存储后再提交。"); }
  }

  async function submit() {
    if (busy || recovering || recoveryFailed || batch) return;
    const body = inventoryPreparationBody(draft);
    if (!body) { setError("填写库存名称、正整数数量；金额请用元，最多两位小数。未知成本可以留空。"); return; }
    const expectedVersion=requestPhase === "pending" ? requestVersion : config?.version;
    if(!expectedVersion){setError("本次草稿版本还未确认，请重新读取承诺与原库存请求。");return;}
    setRequestVersion(expectedVersion);
    setBusy(true); setError("");
    try { checkpoint(draft, "pending",expectedVersion); } catch { setBusy(false); setError("无法保存本次请求，尚未提交。请允许网站存储后重试。"); return; }
    setRequestPhase("pending");
    try {
      const res = await session.api("POST", `campaigns/${campaignId}/reward-inventory/prepare`, {...body,expected_version:expectedVersion});
      if (!res.ok) {
        const recovered = await session.api("GET", `campaigns/${campaignId}/reward-inventory/prepare?request_key=${encodeURIComponent(draft.requestKey)}`);
        if (recovered.ok) { setBatch(recovered.data as unknown as RewardInventoryView); setRequestPhase("confirmed"); checkpoint(draft, "confirmed",expectedVersion); onDone?.("已找回原库存批次，未重复增加。"); return; }
        if (recovered.status === 404 && [400, 409, 422].includes(res.status)) { setRequestPhase("draft"); checkpoint(draft, "draft",expectedVersion); }
        setError(`${failureText(res.status, res.data)} 请核对已保存的承诺上限和现有库存；改变承诺请回活动页。`);
        return;
      }
      setBatch(res.data as unknown as RewardInventoryView); setRequestPhase("confirmed"); checkpoint(draft, "confirmed",expectedVersion);
      onDone?.("库存已准备。");
    } catch { setError("本次结果尚未确认。保留原请求重试，将回查同一批库存，不新增批次。"); }
    finally { setBusy(false); }
  }

  function anotherBatch() {
    if (!batch || recovering || busy) return;
    const next = { ...freshDraft(), name: batch.name, face: batch.face_value_minor ? String(batch.face_value_minor / 100) : "", cost: batch.cost_recorded ? String(batch.cost_minor / 100) : "" };
    try { checkpoint(next, "draft"); } catch { setError("无法保存新的库存请求，请允许网站存储后重试。"); return; }
    setDraft(next); setRequestVersion(0); setRequestPhase("draft"); setBatch(null); setError("");
  }

  const reward = config?.spec?.reward;
  const locked = requestPhase !== "draft" || busy || recovering || recoveryFailed;
  return <div data-field="inventory-create">
    <p><a href={`/work/campaigns/${campaignId}#publish-config`}>查看或修改唯一活动承诺</a></p>
    {loading || recovering ? <p data-state="loading">正在读取已保存的承诺与库存请求…</p> : null}
    {error ? <p className="tk-danger" data-state="error">{error}<button type="button" className="tk-quiet" onClick={() => setReload((n) => n + 1)}>重新读取</button></p> : null}
    {reward ? <>
      <p className="tk-note">{benefitKindLabel(reward.kind)} · 第 {config?.version} 版承诺 · {verificationModeLabel(config?.spec?.verification_mode ?? "")}。承诺上限：{reward.budget_mode === "total_quantity" ? `${reward.total_quantity ?? "未填写"} 份` : reward.cost_budget_minor == null ? "尚未录入" : formatMoney(reward.cost_budget_minor)}。门店和使用条件由承诺自动带入，补库存不改变承诺上限。</p>
      <p className="tk-note">领取期：{formatCampaignWindow(reward.claim_from, reward.claim_until, { timeZone: "Asia/Shanghai" })}；使用期：{formatCampaignWindow(reward.redeem_from, reward.redeem_until, { timeZone: "Asia/Shanghai" })}（北京时间 UTC+8）。</p>
      {batch ? <div role="status"><p className="tk-note">已找回第 {batch.rule_version} 版原批次「{batch.name}」：本批共 {batch.total_qty} 份，当前可用 {batch.available_qty} 份。成本：{batch.cost_recorded ? formatMoney(batch.cost_minor, batch.currency) : "未录入"}。不会重复增加这一批。</p>{batch.rule_version !== config?.version ? <p className="tk-warn">这是原版本库存，不属于当前第 {config?.version} 版草稿；启用新草稿前须另准备对应库存。</p> : null}<button className="tk-quiet" type="button" disabled={recovering || busy} onClick={anotherBatch}>{batch.rule_version !== config?.version ? `为第 ${config?.version} 版另准备库存` : "另准备一批库存"}</button></div> : <>
        <label className="tk-label">优惠库存名称<input className="tk-input" data-field="batch-name" disabled={locked} value={draft.name} onChange={(e) => change("name", e.target.value)} /></label>
        {reward.kind === "imported_code" ? <label className="tk-label">合法券码，每行一个<textarea className="tk-input" data-field="batch-codes" disabled={locked} value={draft.codes} onChange={(e) => change("codes", e.target.value)} /></label> : <label className="tk-label">本次准备数量<input className="tk-input" data-field="batch-qty" disabled={locked} type="number" min="1" value={draft.quantity} onChange={(e) => change("quantity", e.target.value)} /></label>}
        <label className="tk-label">每份券面额（元，可不填）<input className="tk-input" data-field="batch-face" disabled={locked} inputMode="decimal" value={draft.face} onChange={(e) => change("face", e.target.value)} /></label>
        <label className="tk-label">每份实际成本（元，未知留空）<input className="tk-input" data-field="batch-cost" disabled={locked} inputMode="decimal" value={draft.cost} onChange={(e) => change("cost", e.target.value)} /></label>
        <p className="tk-note">成本留空会记为未知；明确填 0 才表示真实零成本。按成本预算时必须录入成本。若尚未到使用期，原履约规则会阻止提前领取与发放，请勿先让顾客开始任务。</p>
        {requestPhase !== "draft" ? <p className="tk-note">先回查上次请求。恢复只使用原始资料；确认原批次后才可另准备库存。</p> : null}
        <button className="tk-button" data-action="create-batch" type="button" disabled={busy || loading || recovering || recoveryFailed || requestPhase === "confirmed"} onClick={() => void submit()}>{busy ? "正在准备…" : requestPhase === "pending" ? "恢复同一库存请求" : "准备关联库存"}</button>
      </>}
    </> : null}
  </div>;
}
