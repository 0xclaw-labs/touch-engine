"use client";

import { useCallback, useEffect, useState } from "react";
import { failureText, useSession } from "@/components/work/merchant-session";
import { CampaignInventoryPrepare } from "@/components/work/campaign-inventory-prepare";
import { ContentEditor } from "@/components/work/store-content-drawer";
import { formatMoney, moneyInputMinor } from "@/lib/reward-ops";
import { DateTimeField } from "@/components/ui/date-time-field";
import { surfaceLabel } from "@/lib/product-finish";
import {
  admissionNote,
  benefitKindLabel,
  budgetModeLabel,
  channelLabel,
  defaultSpec,
  dedupBasisLabel,
  fromRFC3339,
  modeExplanation,
  reviewProhibited,
  outOfStockLabel,
  perOrderAllowed,
  refusalCopy,
  staffScopeLabel,
  toServerSpec,
  verificationModeLabel,
  windowProblem,
  DEDUP_BASES,
  MODES,
  type ChannelAdmissionRecord,
  type PublishSpec,
  toPublishConfig,
} from "@/lib/campaign-config";

// HUI-1676 增量:发布兑奖配置面板(规则编辑器唯一维护方)。
// 纪律(票面增量节):
//   - 五步一次配置:活动与门店 → 准备内容 → 发布方式 → 给什么优惠 →
//     顾客预览与启用;不重复填门店资料;无技术 ID 输入;
//   - 草稿在服务端:挂载即 GET 恢复(刷新/重登/换浏览器都能回来),关闭
//     抽屉/离开页面不丢已保存草稿;保存失败如实报错,不假装成功;
//   - 内容只选 3553 已采用版本;渠道按 3535 真实可用 + 缺项原因展示,
//     不可用的渠道不给勾选(禁止只存平台字符串当已准入);
//   - 三核验模式配置时写顾客文案;启用是唯一放行点,拒绝落到具体字段;
//     预览是明确测试模式,不消耗真实奖励;
//   - 奖励口径只覆盖券/码/赠品/折扣,其余发放形式不在本面板;
//   - 保存只改编辑稿；暂停停新参与与新发放，恢复只用原线上版本。

interface PackVersion {
  id: string;
  version_no: number;
  status: string;
  title: string;
  store_name: string;
  price_note: string;
}

interface ContentPack {
  id: string;
  title: string;
  versions: PackVersion[];
  adoptions: Array<{ platform: string; version_id: string }>;
}

interface StoreRec {
  id: string;
  name: string;
}

export function CampaignConfigPanel({
  campaignId,
  campaignTitle,
  campaignBody,
  campaignStoreName,
  campaignStoreId,
  adoptedVersionId,
  onAuthorityChanged,
}: {
  campaignId: string;
  campaignTitle?: string;
  campaignBody?: string;
  campaignStoreName?: string;
  campaignStoreId?: string;
  adoptedVersionId?: string;
  onAuthorityChanged?: () => void;
}) {
  const session = useSession();
  const [phase, setPhase] = useState<"loading" | "error" | "ready">("loading");
  const [spec, setSpec] = useState<PublishSpec>(() => defaultSpec());
  const [publishedStatus,setPublishedStatus]=useState<string>("not_enabled");
  const [version, setVersion] = useState<number>(0);
  const [enabledVersion, setEnabledVersion] = useState<number>(0);
  const [packs, setPacks] = useState<ContentPack[]>([]);
  const [admissions, setAdmissions] = useState<ChannelAdmissionRecord[]>([]);
  const [stores, setStores] = useState<StoreRec[]>([]);
  const [ruleset, setRuleset] = useState<{ reward_threshold?: number; daily_publish_limit?: number; duplicate_publish_window_hours?: number; per_contact_daily_submission_cap?: number }>({});
  // 防薅羊毛规则集只有商家真改过才重存:规则端点每次 PUT 都会记一条版本
  // 修订,不制造无改动版本噪音。
  const [rulesDirty, setRulesDirty] = useState(false);
  const [busy, setBusy] = useState("");
  const [fieldError, setFieldError] = useState<{ field: string; text: string } | null>(null);
  const [formError, setFormError] = useState("");
  const [notice, setNotice] = useState("");
  const [contentOpen,setContentOpen]=useState(false);

  const load = useCallback(async () => {
    setPhase("loading");
    // 草稿恢复:服务端持久,刷新/重登回来还在;404 = 还没配置过,给空草稿。
    const cfgRes = await session.api("GET", `campaigns/${campaignId}/publish-config`);
    if (cfgRes.ok) {
      const cfg = toPublishConfig(cfgRes.data);
      if (cfg.spec) {
        setSpec({
          ...cfg.spec,
          content_version_id:adoptedVersionId || cfg.spec.content_version_id,
          reward: {
            ...cfg.spec.reward,
            slot: cfg.spec.reward.slot || "first_publish",
            claim_from: fromRFC3339(cfg.spec.reward.claim_from),
            claim_until: fromRFC3339(cfg.spec.reward.claim_until),
            redeem_from: fromRFC3339(cfg.spec.reward.redeem_from),
            redeem_until: fromRFC3339(cfg.spec.reward.redeem_until),
          },
        });
      }
      setPublishedStatus(cfg.published_status ?? "unknown");
      setVersion(cfg.version ?? 0);
      setEnabledVersion(cfg.enabled_version ?? 0);
    } else if (cfgRes.status !== 404) {
      setFormError(failureText(cfgRes.status, cfgRes.data));
      setPhase("error");
      return;
    }
    if (cfgRes.status === 404) setSpec(s => ({...s,reward:{...s.reward,store_ids:campaignStoreId?[campaignStoreId]:[]}}));
    // 防薅羊毛规则集(PR#8 既有端点,零改动复用)。
    const rulesRes = await session.api("GET", `campaigns/${campaignId}/rules`);
    if (rulesRes.ok) {
      const rs = (rulesRes.data as Record<string, unknown>).ruleset ?? rulesRes.data;
      setRuleset((rs ?? {}) as typeof ruleset);
    }
    const [packRes, admRes, storeRes] = await Promise.all([
      session.api("GET", `campaigns/${campaignId}/content-packs`),
      session.api("GET", `channel-admissions`),
      session.api("GET", `stores`),
    ]);
    setPacks(packRes.ok && Array.isArray(packRes.data.items) ? (packRes.data.items as ContentPack[]) : []);
    setAdmissions(admRes.ok && Array.isArray(admRes.data.records) ? (admRes.data.records as ChannelAdmissionRecord[]) : []);
    setStores(storeRes.ok && Array.isArray(storeRes.data.items) ? (storeRes.data.items as StoreRec[]) : []);
    setPhase("ready");
  }, [campaignId, campaignStoreId, adoptedVersionId, session]);

  useEffect(() => {
    void load().catch(() => {
      setFormError("没有连上服务。配置没有读到,不把它当成空的。");
      setPhase("error");
    });
  }, [load]);

  useEffect(()=>{if(adoptedVersionId)setSpec(s=>({...s,content_version_id:adoptedVersionId}));},[adoptedVersionId]);

  function patch(reward: Partial<PublishSpec["reward"]>) {
    setSpec((s) => ({ ...s, reward: { ...s.reward, ...reward } }));
  }

  const adoptedVersions = packs.flatMap((p) =>
    p.versions.length > 0
      ? p.versions
          .filter((v) => v.status === "adopted" && p.adoptions.some((a) => a.version_id === v.id))
          .map((v) => ({ packTitle: p.title, ...v }))
      : [],
  );

  function toggleChannel(platform: string, entry_mode: string, scenario: string, on: boolean) {
    setSpec((s) => {
      const rest = s.channels.filter((c) => !(c.platform === platform && c.entry_mode === entry_mode && c.scenario === scenario));
      return on
        ? { ...s, channels: [...rest, { platform, entry_mode, scenario }] }
        : { ...s, channels: rest };
    });
  }

  function toggleStore(id: string, on: boolean) {
    setSpec((s) => ({
      ...s,
      reward: {
        ...s.reward,
        store_ids: on ? [...new Set([...s.reward.store_ids, id])] : s.reward.store_ids.filter((x) => x !== id),
      },
    }));
  }

  async function saveDraft(notifyHost = true): Promise<number | null> {
    setBusy("save");
    setFieldError(null);
    setFormError("");
    setNotice("");
    try {
      // 反薅羊毛规则集(每日上限等)走 PR#8 既有端点;发布兑奖配置走新端点。
      // 只有真改过才 PUT(每次 PUT 都会记版本修订)。
      if (rulesDirty) {
        const rulesBody: Record<string, number | null> = {
          reward_threshold: ruleset.reward_threshold ?? null,
          daily_publish_limit: ruleset.daily_publish_limit ?? null,
          duplicate_publish_window_hours: ruleset.duplicate_publish_window_hours ?? null,
          per_contact_daily_submission_cap: ruleset.per_contact_daily_submission_cap ?? null,
        };
        const rulesRes = await session.api("PUT", `campaigns/${campaignId}/rules`, rulesBody);
        if (!rulesRes.ok && rulesRes.status !== 404) {
          setFormError(failureText(rulesRes.status, rulesRes.data) + "(防薅羊毛规则没有存上)");
          return null;
        }
        setRulesDirty(false);
      }
      const cfgRes = await session.api("PUT", `campaigns/${campaignId}/publish-config`, toServerSpec(spec));
      if (!cfgRes.ok) {
        setFormError(failureText(cfgRes.status, cfgRes.data));
        return null;
      }
      const cfg = toPublishConfig(cfgRes.data);
      // 保存只更新编辑稿；线上仍由原正式修订决定。
      setPublishedStatus(cfg.published_status ?? "unknown");
      setVersion(cfg.version ?? version);
      setEnabledVersion(cfg.enabled_version ?? 0);
      setNotice("草稿已保存。刷新或重新登录都会回到这里。");
      if(notifyHost)onAuthorityChanged?.();
      return cfg.version ?? version;
    } catch {
      setFormError("没有连上服务。草稿没有存上,内容还在这一页上。");
      return null;
    } finally {
      setBusy("");
    }
  }

  async function enable() {
    setBusy("enable");
    setFieldError(null);
    setFormError("");
    setNotice("");
    try {
      const expectedVersion = await saveDraft(false);
      if (expectedVersion == null) return;
      setBusy("enable");
      const res = await session.api("POST", `campaigns/${campaignId}/publish-config/enable`,{expected_version:expectedVersion});
      if (!res.ok) {
        setFieldError(refusalCopy(res.status, res.data));
        return;
      }
      const cfg = toPublishConfig(res.data);
      setPublishedStatus(cfg.published_status ?? "unknown");
      setVersion(cfg.version ?? version);
      setEnabledVersion(cfg.enabled_version ?? version);
      setNotice("已启用。顾客开始参与前会先申请权益预留,未预留成功不会先让顾客白跑。");
      onAuthorityChanged?.();
    } catch {
      setFormError("没有连上服务。启用没有完成。");
    } finally {
      setBusy("");
    }
  }

  async function pause() {
    setBusy("pause");
    setFormError("");
    try {
      const res = await session.api("POST", `campaigns/${campaignId}/publish-config/pause`);
      if (!res.ok) {
        setFormError(failureText(res.status, res.data));
        return;
      }
      setPublishedStatus("paused");
      onAuthorityChanged?.();
      setNotice("已暂停:停止新参与与新发放；在途事项按原冻结条件核对，需恢复活动后重新核验；已发有效券仍可使用。");
    } catch {
      setFormError("没有连上服务。暂停没有完成。");
    } finally {
      setBusy("");
    }
  }

  async function resume() {
    setBusy("resume");setFormError("");setNotice("");
    try {
      const res=await session.api("POST",`campaigns/${campaignId}/publish-config/resume`,{expected_version:enabledVersion});
      if(!res.ok){setFieldError(refusalCopy(res.status,res.data));return;}
      const cfg=toPublishConfig(res.data);setPublishedStatus(cfg.published_status ?? "unknown");setEnabledVersion(cfg.enabled_version ?? enabledVersion);
      setNotice(`已恢复线上第 ${cfg.enabled_version ?? enabledVersion} 版。编辑稿没有发布，仍须准备对应库存并明确启用。`);
      onAuthorityChanged?.();
    }catch {setFormError("没有连上服务。恢复没有完成，请回查原状态。");}finally {setBusy("");}
  }

  const windowWarn = windowProblem(spec);
  // 门店资料来自活动/门店列表(只读展示),商家不在这里重填资料。
  const boundStoreName = campaignStoreName ?? stores.find((st) => st.id === campaignStoreId)?.name;
  const channelOn = (p: string, m: string, sc: string) =>
    spec.channels.some((c) => c.platform === p && c.entry_mode === m && c.scenario === sc);

  return (
    <section id="publish-config" className="tk-section">
      <h2 className="tk-section-title">发布与启用</h2>
      <p className="tk-note">
        {campaignTitle ? `活动「${campaignTitle}」` : "本活动"}
        {boundStoreName ? ` · 门店资料用「${boundStoreName}」,不用重填` : ""}
        {version > 0 ? ` · 编辑稿第 ${version} 版` : " · 还没有保存过配置"}
        {enabledVersion > 0 ? ` · 线上第 ${enabledVersion} 版${publishedStatus === "enabled" ? "生效中" : publishedStatus === "paused" ? "已暂停" : "状态未读到"}` : " · 尚未启用"}
        {publishedStatus === "paused" ? " · 停止新参与与新发放，已发有效券仍可使用" : ""}
      </p>
      {phase === "loading" ? <p className="tk-state" data-state="loading">{surfaceLabel("loading")}</p> : null}
      {phase === "error" ? (
        <p className="tk-state tk-danger" data-state="error">
          {formError || surfaceLabel("error")}
          <button className="tk-quiet" type="button" data-action="retry" onClick={() => void load()}>重试</button>
        </p>
      ) : null}
      {phase === "ready" ? (
        <>
          {/* 第 1 步:活动与门店(只读展示,不重复填资料) */}
          <fieldset className="tk-fieldset" data-step="1">
            <legend>① 活动与门店</legend>
            <p className="tk-note">
              {boundStoreName
                ? `活动期与门店沿用活动资料(门店:${boundStoreName})。`
                : "这个活动还没有绑定门店。先在活动资料里选门店,这里才能继续。"}
            </p>
            <label className="tk-label" data-field="claim_window">
              领取时间（北京时间 UTC+8）
              <span className="tk-row tk-gap">
                <DateTimeField
                  name="claim_from"
                  aria-label="领取开始时间"
                  value={spec.reward.claim_from}
                  invalid={Boolean(windowWarn)}
                  errorId="window-problem-error"
                  onChange={(v) => patch({ claim_from: v })}
                />
                <span> 到 </span>
                <DateTimeField
                  name="claim_until"
                  aria-label="领取结束时间"
                  value={spec.reward.claim_until}
                  invalid={Boolean(windowWarn)}
                  errorId="window-problem-error"
                  onChange={(v) => patch({ claim_until: v })}
                />
              </span>
            </label>
            <label className="tk-label" data-field="redeem_window">
              优惠使用时间（北京时间 UTC+8）
              <span className="tk-row tk-gap">
                <DateTimeField
                  name="redeem_from"
                  aria-label="优惠使用开始时间"
                  value={spec.reward.redeem_from}
                  invalid={Boolean(windowWarn)}
                  errorId="window-problem-error"
                  onChange={(v) => patch({ redeem_from: v })}
                />
                <span> 到 </span>
                <DateTimeField
                  name="redeem_until"
                  aria-label="优惠使用结束时间"
                  value={spec.reward.redeem_until}
                  invalid={Boolean(windowWarn)}
                  errorId="window-problem-error"
                  onChange={(v) => patch({ redeem_until: v })}
                />
              </span>
            </label>
            {windowWarn ? <p className="tk-danger" id="window-problem-error" data-field="window_problem" role="alert" aria-live="polite">{windowWarn}</p> : null}
          </fieldset>

          {/* 第 2 步:准备内容(只选 3553 已采用版本,不复制状态) */}
          <fieldset className="tk-fieldset" data-step="2">
            <legend>② 准备内容</legend>
            {adoptedVersions.length === 0 ? (
              <p className="tk-state" data-state="empty">
                还没有已采用的内容。点下面「准备活动内容」，预览采用后继续；采用只更新候选，启用前仍须检查。
              </p>
            ) : (
              adoptedVersions.map((v) => (
                <label className="tk-label" key={v.id} data-field="content_version_id">
                  <input
                    type="radio"
                    name="content_version_id"
                    value={v.id}
                    checked={spec.content_version_id === v.id}
                    onChange={() => setSpec((s) => ({ ...s, content_version_id: v.id }))}
                  />
                  {v.packTitle} 第 {v.version_no} 版 · {v.title}
                </label>
              ))
            )}
          </fieldset>

          {campaignStoreId ? <><button type="button" className="tk-quiet" onClick={()=>setContentOpen(v=>!v)}>准备活动内容</button>{contentOpen?<ContentEditor storeId={campaignStoreId} storeName={boundStoreName} campaignId={campaignId} purpose="活动推广内容" initialTitle={campaignTitle} initialBody={campaignBody} returnHref={`/work/campaigns/${campaignId}#publish-config`} onAdopted={result=>{setNotice("已采用为候选内容，请保存并预检后启用。");setSpec(s=>({...s,content_version_id:result.versionId}));void session.api("GET",`campaigns/${campaignId}/content-packs`).then(res=>{if(res.ok && Array.isArray(res.data.items))setPacks(res.data.items as ContentPack[]);});}}/>:null}</>:null}

          {/* 第 3 步:发布方式(3535 真实可用 + 缺项原因;不可用不给勾) */}
          <fieldset className="tk-fieldset" data-step="3">
            <legend>③ 发布方式</legend>
            {admissions.length === 0 ? (
              <p className="tk-state" data-state="empty">
                渠道清单没有读到。启用前服务端会按准入真值把关,现在没有任何可用渠道。
              </p>
            ) : (
              <ul className="tk-list" data-field="channels">
                {admissions.map((rec) => {
                  // HUI-1676 全入口轮:点评(review)渠道永不进奖励配置——服务端
                  // 硬拒(3535 准入真值:点评场景从不携带奖励资格),前端同结论前置。
                  const selectable = rec.allowed && !reviewProhibited(rec.scenario);
                  const on = channelOn(rec.platform, rec.entry_mode, rec.scenario);
                  return (
                    <li key={`${rec.platform}|${rec.entry_mode}|${rec.scenario}`}>
                      <label className="tk-label">
                        <input
                          type="checkbox"
                          name="channel"
                          disabled={!selectable}
                          checked={on}
                          onChange={(e) => toggleChannel(rec.platform, rec.entry_mode, rec.scenario, e.target.checked)}
                        />
                        {channelLabel(rec)}
                        {" "}{reviewProhibited(rec.scenario) ? "点评不做奖励:点评入口只帮顾客到门店自主评价。" : admissionNote(rec)}
                      </label>
                    </li>
                  );
                })}
              </ul>
            )}
            <p className="tk-note">防重复领取按什么判定「同一个人」:</p>
            {DEDUP_BASES.map((basis) => (
              <label className="tk-label" key={basis} data-field="dedup_basis">
                <input
                  type="radio"
                  name="dedup_basis"
                  value={basis}
                  checked={spec.dedup_basis === basis}
                  onChange={() => setSpec((s) => ({ ...s, dedup_basis: basis, per_order_limit: false }))}
                />
                {dedupBasisLabel(basis)}
              </label>
            ))}
            <label className="tk-label" data-field="per_order_limit">
              <input
                type="checkbox"
                name="per_order_limit"
                disabled={!perOrderAllowed(spec.dedup_basis)}
                checked={spec.per_order_limit}
                onChange={(e) => setSpec((s) => ({ ...s, per_order_limit: e.target.checked }))}
              />
              按订单限领(一单一次,领取时核对订单凭据)
            </label>
          </fieldset>

          {/* 第 4 步:给什么优惠(口径与规则;只覆盖券/码/赠品/折扣) */}
          <fieldset className="tk-fieldset" data-step="4">
            <legend>④ 给什么优惠</legend>
            <label className="tk-label" data-field="benefit_kind">
              奖励种类
              <select
                className="tk-select"
                name="benefit_kind"
                value={spec.reward.kind}
                onChange={(e) => patch({ kind: e.target.value })}
              >
                {["store_coupon", "imported_code", "onsite_gift", "onsite_discount"].map((k) => (
                  <option key={k} value={k}>{benefitKindLabel(k)}</option>
                ))}
              </select>
            </label>
            <p className="tk-note">权益关联由系统维护，不需要手填槽位或规则版本。</p>
            <p className="tk-note" data-field="budget_mode">
              预算口径:
              <label className="tk-label">
                <input
                  type="radio"
                  name="budget_mode"
                  checked={spec.reward.budget_mode === "total_quantity"}
                  onChange={() => patch({ budget_mode: "total_quantity" })}
                />
                {budgetModeLabel("total_quantity")}
              </label>
              <label className="tk-label">
                <input
                  type="radio"
                  name="budget_mode"
                  checked={spec.reward.budget_mode === "cost_budget"}
                  onChange={() => patch({ budget_mode: "cost_budget" })}
                />
                {budgetModeLabel("cost_budget")}
              </label>
            </p>
            {spec.reward.budget_mode === "total_quantity" ? (
              <label className="tk-label" data-field="total_quantity">
                名额总数
                <input
                  className="tk-input"
                  type="text"
                  inputMode="numeric"
                  name="total_quantity"
                  value={spec.reward.total_quantity ?? ""}
                  onChange={(e) => {
                    const n = e.target.value.replace(/[^0-9]/g, "");
                    patch({ total_quantity: n === "" ? undefined : Number(n) });
                  }}
                />
              </label>
            ) : (
              <label className="tk-label" data-field="cost_budget_minor">
                成本预算（元）
                <input
                  className="tk-input"
                  type="text"
                  inputMode="numeric"
                  name="cost_budget_minor"
                  value={spec.reward.cost_budget_minor == null ? "" : spec.reward.cost_budget_minor / 100}
                  onChange={(e) => {
                    const n = moneyInputMinor(e.target.value);
                    if(n!==null) patch({ cost_budget_minor: n });
                  }}
                />
              </label>
            )}
            <p className="tk-note" data-field="store_ids">适用门店：{boundStoreName || "请先绑定活动门店"}。沿用活动门店，不扩大到其他门店。</p>
            <label className="tk-label" data-field="onsite_redemption">
              <input
                type="checkbox"
                name="onsite_redemption"
                checked={spec.reward.onsite_redemption}
                onChange={(e) => patch({ onsite_redemption: e.target.checked })}
              />
              现场兑现(店员当场给券/赠品)
            </label>
            <p className="tk-note">谁有权核验发奖:</p>
            {["store_staff", "org_owner"].map((scope) => (
              <label className="tk-label" key={scope} data-field="staff_scope">
                <input
                  type="radio"
                  name="staff_scope"
                  value={scope}
                  checked={spec.reward.staff_scope === scope}
                  onChange={() => patch({ staff_scope: scope })}
                />
                {staffScopeLabel(scope)}
              </label>
            ))}
            <p className="tk-note">缺货怎么办:</p>
            {["stop_new", "mark_sold_out"].map((rule) => (
              <label className="tk-label" key={rule} data-field="out_of_stock_rule">
                <input
                  type="radio"
                  name="out_of_stock_rule"
                  value={rule}
                  checked={spec.reward.out_of_stock_rule === rule}
                  onChange={() => patch({ out_of_stock_rule: rule })}
                />
                {outOfStockLabel(rule)}
              </label>
            ))}
            <p className="tk-note" data-field="ruleset">防薅羊毛(每日上限等,随规则版本留痕):</p>
            <label className="tk-label" data-field="daily_publish_limit">
              每日发布上限(留空不限)
              <input
                className="tk-input"
                type="text"
                inputMode="numeric"
                name="daily_publish_limit"
                value={ruleset.daily_publish_limit ?? ""}
                onChange={(e) => {
                  const n = e.target.value.replace(/[^0-9]/g, "");
                  setRuleset((r) => ({ ...r, daily_publish_limit: n === "" ? undefined : Number(n) }));
                  setRulesDirty(true);
                }}
              />
            </label>
            <label className="tk-label" data-field="per_contact_daily_submission_cap">
              每人每日提交上限(留空不限)
              <input
                className="tk-input"
                type="text"
                inputMode="numeric"
                name="per_contact_daily_submission_cap"
                value={ruleset.per_contact_daily_submission_cap ?? ""}
                onChange={(e) => {
                  const n = e.target.value.replace(/[^0-9]/g, "");
                  setRuleset((r) => ({ ...r, per_contact_daily_submission_cap: n === "" ? undefined : Number(n) }));
                  setRulesDirty(true);
                }}
              />
            </label>
          </fieldset>

          {/* 核验模式 + 顾客文案(配置时写死,不在完成后加严) */}
          <fieldset className="tk-fieldset" data-step="verify">
            <legend>怎么算「完成了」</legend>
            {MODES.map((mode) => (
              <label className="tk-label" key={mode} data-field="verification_mode">
                <input
                  type="radio"
                  name="verification_mode"
                  value={mode}
                  checked={spec.verification_mode === mode}
                  onChange={() => setSpec((s) => ({ ...s, verification_mode: mode }))}
                />
                {verificationModeLabel(mode)} —— {modeExplanation(mode)}
              </label>
            ))}
            <label className="tk-label" data-field="customer_copy">
              顾客文案(告诉顾客做完之后怎么领奖)
              <textarea
                className="tk-input"
                name="customer_copy"
                rows={3}
                value={spec.customer_copy}
                onChange={(e) => setSpec((s) => ({ ...s, customer_copy: e.target.value }))}
              />
            </label>
          </fieldset>

          {/* 第 5 步:顾客预览与启用(测试模式,不消耗真实奖励) */}
          <fieldset className="tk-fieldset" data-step="5">
            <legend>⑤ 顾客预览与启用</legend>
            <div className="tk-note" data-field="preview" data-state="preview">
              <p data-field="preview-banner" className="tk-note">【测试预览】下面是顾客会看到的告知,不会消耗任何真实奖励。</p>
              <p data-field="preview-mode">完成后: {verificationModeLabel(spec.verification_mode)}</p>
              <p data-field="preview-copy">{spec.customer_copy || "(顾客文案还没写)"}</p>
              <p data-field="preview-dedup">防重复: {dedupBasisLabel(spec.dedup_basis)}{spec.per_order_limit ? ",按订单限领" : ""}</p>
              <p>适用门店：{boundStoreName || "尚未选定"}；领取期：{spec.reward.claim_from.replace("T"," ") || "待填写"} 至 {spec.reward.claim_until.replace("T"," ") || "待填写"}；使用期：{spec.reward.redeem_from.replace("T"," ") || "待填写"} 至 {spec.reward.redeem_until.replace("T"," ") || "待填写"}（北京时间 UTC+8）。</p>
              <p>缺货处理：{outOfStockLabel(spec.reward.out_of_stock_rule)}。未完成或预留超时不会记为已发放，按参与时告知的期限处理；已发有效券保留原使用条件。</p>
              <p>可用路径预览：先阅读承诺、取得预留，再通过{spec.channels.length?spec.channels.map(channelLabel).join("、"):"尚未选定的获准渠道"}完成任务，经{verificationModeLabel(spec.verification_mode)}核验后领取，按使用期兑现。本预览不发起参与、不占库存。</p>
              <p data-field="preview-benefit">
                {benefitKindLabel(spec.reward.kind)}
                {spec.reward.total_quantity != null ? ` · 名额 ${spec.reward.total_quantity}` : ""}
                {spec.reward.cost_budget_minor != null ? ` · 预算 ${formatMoney(spec.reward.cost_budget_minor)}` : ""}
                {spec.reward.onsite_redemption ? " · 现场兑现" : ""}
              </p>
            </div>
            <p className="tk-note">保存草稿只保留编辑内容，不替换或关闭线上承诺。</p>
            <details className="tk-note" data-field="commitment-change-note">
              <summary>更改承诺、暂停或结束的影响</summary>
              <p>更改承诺后必须重新准备对应版本的库存并检查启用；已有参与保留原告知记录，已发有效券不改写。暂停或结束会停止新参与和新发放，在途预留不能承诺继续发奖，可先核对记录并按原条件恢复办理。</p>
            </details>
            <div className="tk-row">
              <button className="tk-quiet" type="button" data-action="save-draft" disabled={busy !== ""} onClick={() => void saveDraft()}>
                保存草稿
              </button>
              {enabledVersion === 0 || version !== enabledVersion ? (
                <button className="tk-button" type="button" data-action="enable" disabled={busy !== ""} onClick={() => void enable()}>
                  {version > 0 ? `启用第 ${version} 版草稿` : "检查并启用"}
                </button>
              ) : null}
              {publishedStatus === "paused" && enabledVersion > 0 ? (
                <button className="tk-quiet" type="button" data-action="resume" disabled={busy !== ""} onClick={() => void resume()}>恢复线上第 {enabledVersion} 版</button>
              ) : null}
              {enabledVersion > 0 && publishedStatus !== "paused" ? (
                <button className="tk-quiet" type="button" data-action="pause" disabled={busy !== ""} onClick={() => void pause()}>紧急暂停</button>
              ) : null}
            </div>
            {notice ? <p className="tk-ok" data-state="notice">{notice}</p> : null}
            {fieldError ? <p className="tk-danger" data-field={fieldError.field} data-state="refused">{fieldError.text}</p> : null}
            {formError ? <p className="tk-danger" data-state="error">{formError}</p> : null}
          </fieldset>
          {version > 0 ? <section aria-label="准备活动库存"><h3 className="tk-section-title">准备已保存承诺的库存</h3><p className="tk-note">先保存承诺，再准备库存，最后启用。库存准备成功而启用失败时，请按缺项修复后重试，不必再建同一批。</p><CampaignInventoryPrepare campaignId={campaignId} revision={version}/></section> : null}
        </>
      ) : null}
    </section>
  );
}
