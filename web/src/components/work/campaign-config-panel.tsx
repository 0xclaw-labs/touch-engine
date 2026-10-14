"use client";

import { useCallback, useEffect, useState } from "react";
import { failureText, useSession } from "@/components/work/merchant-session";
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
//   - 暂停后老参与的核验/履约入口不受影响(本面板不动参与/奖励)。

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
  campaignStoreName,
  campaignStoreId,
}: {
  campaignId: string;
  campaignTitle?: string;
  campaignStoreName?: string;
  campaignStoreId?: string;
}) {
  const session = useSession();
  const [phase, setPhase] = useState<"loading" | "error" | "ready">("loading");
  const [spec, setSpec] = useState<PublishSpec>(() => defaultSpec());
  const [status, setStatus] = useState<string>("draft");
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

  const load = useCallback(async () => {
    setPhase("loading");
    // 草稿恢复:服务端持久,刷新/重登回来还在;404 = 还没配置过,给空草稿。
    const cfgRes = await session.api("GET", `campaigns/${campaignId}/publish-config`);
    if (cfgRes.ok) {
      const cfg = toPublishConfig(cfgRes.data);
      if (cfg.spec) {
        setSpec({
          ...cfg.spec,
          reward: {
            ...cfg.spec.reward,
            claim_from: fromRFC3339(cfg.spec.reward.claim_from),
            claim_until: fromRFC3339(cfg.spec.reward.claim_until),
            redeem_from: fromRFC3339(cfg.spec.reward.redeem_from),
            redeem_until: fromRFC3339(cfg.spec.reward.redeem_until),
          },
        });
      }
      setStatus(cfg.status ?? "draft");
      setVersion(cfg.version ?? 0);
      setEnabledVersion(cfg.enabled_version ?? 0);
    } else if (cfgRes.status !== 404) {
      setFormError(failureText(cfgRes.status, cfgRes.data));
      setPhase("error");
      return;
    }
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
  }, [campaignId, session]);

  useEffect(() => {
    void load().catch(() => {
      setFormError("没有连上服务。配置没有读到,不把它当成空的。");
      setPhase("error");
    });
  }, [load]);

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

  async function saveDraft(): Promise<boolean> {
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
          return false;
        }
        setRulesDirty(false);
      }
      const cfgRes = await session.api("PUT", `campaigns/${campaignId}/publish-config`, toServerSpec(spec));
      if (!cfgRes.ok) {
        setFormError(failureText(cfgRes.status, cfgRes.data));
        return false;
      }
      const cfg = toPublishConfig(cfgRes.data);
      // enabled 下保存会被服务端收回草稿(改版必须重新启用)——如实展示。
      setStatus(cfg.status ?? "draft");
      setVersion(cfg.version ?? version);
      setNotice("草稿已保存。刷新或重新登录都会回到这里。");
      return true;
    } catch {
      setFormError("没有连上服务。草稿没有存上,内容还在这一页上。");
      return false;
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
      if (!(await saveDraft())) return;
      const res = await session.api("POST", `campaigns/${campaignId}/publish-config/enable`);
      if (!res.ok) {
        setFieldError(refusalCopy(res.status, res.data));
        return;
      }
      const cfg = toPublishConfig(res.data);
      setStatus(cfg.status ?? "enabled");
      setEnabledVersion(cfg.enabled_version ?? version);
      setNotice("已启用。顾客开始参与前会先申请权益预留,未预留成功不会先让顾客白跑。");
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
      setStatus("paused");
      setNotice("已暂停:不再放新参与;已经参与的顾客照常核对与履约。");
    } catch {
      setFormError("没有连上服务。暂停没有完成。");
    } finally {
      setBusy("");
    }
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
        {version > 0 ? ` · 当前第 ${version} 版` : " · 还没有保存过配置"}
        {status === "enabled" && enabledVersion > 0 ? ` · 已启用(第 ${enabledVersion} 版的告知快照对已参与的顾客不变)` : ""}
        {status === "paused" ? " · 已暂停(新参与停止,老参与照常核对履约)" : ""}
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
              领取时间
              <input
                className="tk-input"
                type="datetime-local"
                name="claim_from"
                value={spec.reward.claim_from}
                onChange={(e) => patch({ claim_from: e.target.value })}
              />
              <span> 到 </span>
              <input
                className="tk-input"
                type="datetime-local"
                name="claim_until"
                value={spec.reward.claim_until}
                onChange={(e) => patch({ claim_until: e.target.value })}
              />
            </label>
            <label className="tk-label" data-field="redeem_window">
              兑换时间
              <input
                className="tk-input"
                type="datetime-local"
                name="redeem_from"
                value={spec.reward.redeem_from}
                onChange={(e) => patch({ redeem_from: e.target.value })}
              />
              <span> 到 </span>
              <input
                className="tk-input"
                type="datetime-local"
                name="redeem_until"
                value={spec.reward.redeem_until}
                onChange={(e) => patch({ redeem_until: e.target.value })}
              />
            </label>
            {windowWarn ? <p className="tk-danger" data-field="window_problem">{windowWarn}</p> : null}
          </fieldset>

          {/* 第 2 步:准备内容(只选 3553 已采用版本,不复制状态) */}
          <fieldset className="tk-fieldset" data-step="2">
            <legend>② 准备内容</legend>
            {adoptedVersions.length === 0 ? (
              <p className="tk-state" data-state="empty">
                还没有已采用的内容。先在本页上方「活动内容」里预览并采用一版,再回到这里选它。
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
                  const selectable = rec.allowed;
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
                        {" "}{admissionNote(rec)}
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
            <label className="tk-label" data-field="benefit_slot">
              权益名称(给奖励批次用的名字)
              <input
                className="tk-input"
                type="text"
                name="benefit_slot"
                value={spec.reward.slot}
                onChange={(e) => patch({ slot: e.target.value })}
                placeholder="例如:store_coupon_main"
              />
            </label>
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
                成本预算(分)
                <input
                  className="tk-input"
                  type="text"
                  inputMode="numeric"
                  name="cost_budget_minor"
                  value={spec.reward.cost_budget_minor ?? ""}
                  onChange={(e) => {
                    const n = e.target.value.replace(/[^0-9]/g, "");
                    patch({ cost_budget_minor: n === "" ? undefined : Number(n) });
                  }}
                />
              </label>
            )}
            <p className="tk-note">可用门店(必须勾上活动绑定的门店):</p>
            {stores.map((st) => (
              <label className="tk-label" key={st.id} data-field="store_ids">
                <input
                  type="checkbox"
                  name="store_ids"
                  checked={spec.reward.store_ids.includes(st.id)}
                  onChange={(e) => toggleStore(st.id, e.target.checked)}
                />
                {st.name}
              </label>
            ))}
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
              <p data-field="preview-benefit">
                {benefitKindLabel(spec.reward.kind)}
                {spec.reward.total_quantity != null ? ` · 名额 ${spec.reward.total_quantity}` : ""}
                {spec.reward.cost_budget_minor != null ? ` · 预算 ${spec.reward.cost_budget_minor} 分` : ""}
                {spec.reward.onsite_redemption ? " · 现场兑现" : ""}
              </p>
            </div>
            <div className="tk-row">
              <button className="tk-quiet" type="button" data-action="save-draft" disabled={busy !== ""} onClick={() => void saveDraft()}>
                保存草稿
              </button>
              {status !== "enabled" ? (
                <button className="tk-button" type="button" data-action="enable" disabled={busy !== ""} onClick={() => void enable()}>
                  {status === "paused" ? "重新启用" : "启用"}
                </button>
              ) : (
                <button className="tk-quiet" type="button" data-action="pause" disabled={busy !== ""} onClick={() => void pause()}>
                  紧急暂停
                </button>
              )}
            </div>
            {notice ? <p className="tk-ok" data-state="notice">{notice}</p> : null}
            {fieldError ? <p className="tk-danger" data-field={fieldError.field} data-state="refused">{fieldError.text}</p> : null}
            {formError ? <p className="tk-danger" data-state="error">{formError}</p> : null}
          </fieldset>
        </>
      ) : null}
    </section>
  );
}
