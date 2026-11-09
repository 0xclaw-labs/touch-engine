"use client";

import { useCallback, useEffect, useState } from "react";
import { failureText, useSession } from "@/components/work/merchant-session";
import { presentAccountSeparation, type SeparationView } from "@/lib/account-separation";
import { rewardPresentation, surfaceLabel } from "@/lib/product-finish";
import { rewardNotice, type RewardView } from "@/lib/publish-reward";
import {
  couponOutcomeText,
  offerStatusText,
  platformText,
  priceStateText,
  verifiedAtText,
  type DealCouponResultView,
  type StoreDealOfferView,
} from "@/lib/deals";
import {
  benefitStatusText,
  canConfirmRewards,
  canManageInventory,
  canReverseRewards,
  confirmRequestBody,
  evidenceText,
  externalStateText,
  faceAndCostText,
  inventoryKindText,
  inventoryLine,
  maskCode,
  redeemRequestBody,
  reverseRequestBody,
  slotText,
  summaryLines,
  type RewardBenefitView,
  type RewardInventoryView,
  type RewardReviewRequestView,
  type RewardSummaryView,
} from "@/lib/reward-ops";

interface Campaign {
  id: string;
  title: string;
  status: string;
}

interface StoreRec {
  id: string;
  name: string;
}

interface DealClickTotals {
  clicks: Record<string, number>;
}

interface Ruleset {
  reward_threshold?: number;
  daily_publish_limit?: number;
  duplicate_publish_window_hours?: number;
  per_contact_daily_submission_cap?: number;
}

type LoadPhase = "loading" | "error" | "empty" | "ready";

// HUI-1671: 门店券库存为主页签;外部团购是诚实占位 tab(配置与授权核销归
// HUI-4590,本页只显示事实状态,不代发也不本地核销)。
type RewardTab = "store" | "external";

interface ExternalIssuanceView {
  id: string;
  participation_id: string;
  offer_id: string;
  state: string; // issued | pending_unknown | failed
  provider?: string;
}

// HUI-1671 B3: /work/rewards 从只读说明页升级为真实奖励管理页。
// 纪律不变:未知不是已发放;核销没有事实时保持未知;内部 token 不进页面;
// 券码默认掩码,店员点开才看;预算与更正撤销只归 org_owner。
export default function RewardsPage() {
  const session = useSession();
  const [phase, setPhase] = useState<LoadPhase>("loading");
  const [error, setError] = useState("");
  const [campaigns, setCampaigns] = useState<Campaign[]>([]);
  const [campaignId, setCampaignId] = useState("");
  const [rulesNote, setRulesNote] = useState("规则还没读取");
  const [reward, setReward] = useState<RewardView>({ status: "unknown" });
  const [separation, setSeparation] = useState("");
  const [attempt, setAttempt] = useState(0);

  const [tab, setTab] = useState<RewardTab>("store");
  const [inventory, setInventory] = useState<RewardInventoryView[]>([]);
  const [benefits, setBenefits] = useState<RewardBenefitView[]>([]);
  const [summary, setSummary] = useState<RewardSummaryView | null>(null);
  const [reviewQueue, setReviewQueue] = useState<RewardReviewRequestView[]>([]);
  const [externalIssuances, setExternalIssuances] = useState<ExternalIssuanceView[]>([]);
  const [revealed, setRevealed] = useState<Record<string, boolean>>({});
  const [actionNote, setActionNote] = useState("");
  // HUI-4590 外部团购：套餐按门店维护（store-scoped，无活动也可用）。
  const [stores, setStores] = useState<StoreRec[]>([]);
  const [storeId, setStoreId] = useState("");
  const [offers, setOffers] = useState<StoreDealOfferView[]>([]);

  const loadLedger = useCallback(
    async (cid: string) => {
      if (!cid) return;
      const [invRes, benRes, sumRes, revRes, extRes] = await Promise.all([
        session.api("GET", `campaigns/${cid}/reward-inventory`),
        session.api("GET", `campaigns/${cid}/reward-benefits`),
        session.api("GET", `campaigns/${cid}/reward-summary`),
        session.api("GET", `campaigns/${cid}/reward-review-requests`),
        session.api("GET", `campaigns/${cid}/reward-external-issuances`),
      ]);
      setInventory(invRes.ok && Array.isArray(invRes.data.items) ? (invRes.data.items as RewardInventoryView[]) : []);
      setBenefits(benRes.ok && Array.isArray(benRes.data.items) ? (benRes.data.items as RewardBenefitView[]) : []);
      setSummary(sumRes.ok ? (sumRes.data as unknown as RewardSummaryView) : null);
      setReviewQueue(revRes.ok && Array.isArray(revRes.data.items) ? (revRes.data.items as RewardReviewRequestView[]) : []);
      setExternalIssuances(extRes.ok && Array.isArray(extRes.data.items) ? (extRes.data.items as ExternalIssuanceView[]) : []);
      if (!invRes.ok && invRes.status !== 404) {
        setActionNote(failureText(invRes.status, invRes.data));
      }
    },
    [session],
  );

  useEffect(() => {
    let alive = true;
    void (async () => {
      const [campaignRes, accountRes, storesRes] = await Promise.all([
        session.api("GET", "campaigns"),
        session.api("GET", "account-separation"),
        session.api("GET", "stores").catch(() => null),
      ]);
      if (!alive) return;
      if (!campaignRes.ok) {
        setError(failureText(campaignRes.status, campaignRes.data));
        setPhase("error");
        return;
      }
      const items = Array.isArray(campaignRes.data.items) ? (campaignRes.data.items as Campaign[]) : [];
      setCampaigns(items);
      setCampaignId(items[0]?.id ?? "");
      if (storesRes && storesRes.ok && Array.isArray(storesRes.data.items)) {
        const recs = storesRes.data.items as StoreRec[];
        setStores(recs);
        setStoreId(recs[0]?.id ?? "");
      }
      if (accountRes.ok) {
        const presented = presentAccountSeparation(accountRes.data as unknown as SeparationView);
        setSeparation(presented.rewardText);
      } else {
        setSeparation("营销奖励账本没有读到。不把它写成已发放。");
      }
      setPhase(items.length === 0 ? "empty" : "ready");
    })().catch(() => {
      if (alive) setPhase("error");
    });
    return () => {
      alive = false;
    };
  }, [session, attempt]);

  useEffect(() => {
    if (!campaignId) return;
    let alive = true;
    void (async () => {
      const rulesRes = await session.api("GET", `campaigns/${campaignId}/rules`);
      if (!alive) return;
      if (rulesRes.status === 404) {
        setRulesNote("还没有活动规则，或规则未开通。缺失不是已核销，也不是不限已经生效的证明。");
      } else if (!rulesRes.ok) {
        setRulesNote(failureText(rulesRes.status, rulesRes.data));
      } else {
        const ruleset = (rulesRes.data.ruleset ?? {}) as Ruleset;
        setRulesNote(
          [
            line("奖励门槛", ruleset.reward_threshold),
            line("每日发布上限", ruleset.daily_publish_limit),
            line("重复发布窗口（小时）", ruleset.duplicate_publish_window_hours),
            line("每人每日提交上限", ruleset.per_contact_daily_submission_cap),
            "奖励批次上保存的规则版本引用是审计快照，这里不编辑活动规则。",
          ].join(" "),
        );
      }
      await loadLedger(campaignId);
    })().catch(() => {
      if (alive) setActionNote("奖励账本没有读到。不把它写成已发放。");
    });
    return () => {
      alive = false;
    };
  }, [campaignId, session, attempt, loadLedger]);

  const loadOffers = useCallback(
    async (sid: string) => {
      if (!sid) return;
      const res = await session.api("GET", `stores/${sid}/deal-offers`);
      if (res.ok && Array.isArray(res.data.items)) {
        setOffers(res.data.items as StoreDealOfferView[]);
      } else {
        setOffers([]);
        if (res.status !== 404) setActionNote(failureText(res.status, res.data));
      }
    },
    [session],
  );

  useEffect(() => {
    if (!storeId) return;
    void loadOffers(storeId).catch(() => setActionNote("团购套餐没有读到。不把它写成已上架。"));
  }, [storeId, loadOffers, attempt]);

  const runDealAction = useCallback(
    async (sid: string, fn: () => Promise<void>) => {
      setActionNote("");
      try {
        await fn();
      } catch {
        setActionNote("操作没有完成。没有事实不写成功。");
      }
      await loadOffers(sid);
    },
    [loadOffers],
  );

  const presented = rewardPresentation(reward);
  const manage = canManageInventory(session.role);
  const confirmAllowed = canConfirmRewards(session.role);
  const reverseAllowed = canReverseRewards(session.role);

  const runAction = useCallback(
    async (fn: () => Promise<void>) => {
      setActionNote("");
      try {
        await fn();
      } catch {
        setActionNote("操作没有完成。没有事实不写成功。");
      }
    },
    [],
  );

  const redeemBenefit = (ben: RewardBenefitView) =>
    void runAction(async () => {
      const res = await session.api(
        "POST",
        `campaigns/${campaignId}/reward-benefits/${ben.id}/redeem`,
        redeemRequestBody(ben.store_id ?? ""),
      );
      if (res.ok) {
        const outcome = String(res.data.outcome ?? "");
        setActionNote(outcome === "redeemed" ? "核销完成（一条事实）。" : "该权益已核销过，回读已处理结果。");
      } else {
        setActionNote(failureText(res.status, res.data));
      }
      await loadLedger(campaignId);
    });

  const reverseBenefit = (ben: RewardBenefitView) => {
    const reason = window.prompt("更正撤销必须注明原因（保留原发放事实，不删账）：");
    if (!reason || reason.trim() === "") {
      setActionNote("没有原因就不撤销。");
      return;
    }
    void runAction(async () => {
      const res = await session.api(
        "POST",
        `campaigns/${campaignId}/reward-benefits/${ben.id}/reverse`,
        reverseRequestBody(reason.trim()),
      );
      if (res.ok) {
        setActionNote("已更正撤销（追加更正记录，原发放事实保留）。");
      } else {
        setActionNote(failureText(res.status, res.data));
      }
      await loadLedger(campaignId);
    });
  };

  const setBatchStatus = (inv: RewardInventoryView, status: string) =>
    void runAction(async () => {
      const res = await session.api(
        "POST",
        `campaigns/${campaignId}/reward-inventory/${inv.id}/status`,
        { status },
      );
      if (!res.ok) setActionNote(failureText(res.status, res.data));
      await loadLedger(campaignId);
    });

  return (
    <main>
      <h1 className="tk-title">奖励和核销</h1>
      <p className="tk-lead">
        未知奖励不是已发放。发放与核销是两条事实；更正撤销保留原发放事实，不删账。
        {confirmAllowed ? "" : "当前角色只能查看。"}
      </p>
      {phase === "loading" ? <p className="tk-state" data-state="loading">{surfaceLabel("loading")}</p> : null}
      {phase === "error" ? (
        <p className="tk-state tk-danger" data-state="error">
          {error || surfaceLabel("error")}
          <button className="tk-quiet" type="button" data-action="retry" onClick={() => setAttempt((a) => a + 1)}>重试</button>
        </p>
      ) : null}
      {phase === "empty" ? <p className="tk-state" data-state="empty">还没有活动，所以没有可管理的奖励。</p> : null}
      {phase === "ready" ? (
        <>
          <label className="tk-label">
            活动
            <select className="tk-select" value={campaignId} onChange={(event) => setCampaignId(event.target.value)}>
              {campaigns.map((item) => <option key={item.id} value={item.id}>{item.title}</option>)}
            </select>
          </label>

          <div role="tablist" aria-label="奖励管理页签" data-field="reward-tabs">
            <button role="tab" type="button" data-action="tab-store" aria-selected={tab === "store"} onClick={() => setTab("store")}>门店券库存</button>
            <button role="tab" type="button" data-action="tab-external" aria-selected={tab === "external"} onClick={() => setTab("external")}>外部团购（HUI-4590）</button>
          </div>

          {tab === "external" ? (
            <ExternalGroupBuyTab
              stores={stores}
              storeId={storeId}
              onStoreChange={setStoreId}
              offers={offers}
              externalIssuances={externalIssuances}
              manage={manage}
              session={session}
              onNote={(note) => setActionNote(note)}
              onReload={() => runDealAction(storeId, async () => {})}
            />
          ) : (
            <>
          <section aria-label="奖励汇总">
            <h2 className="tk-subtitle">奖励汇总</h2>
            {summary ? (
              <p className="tk-note" data-field="reward-summary">
                {summaryLines(summary).join(" ")}
                {summary.committed_face_minor !== undefined
                  ? ` 已承诺面额 ${summary.committed_face_minor}（不是成本），成本 ${summary.committed_cost_minor}。`
                  : ""}
              </p>
            ) : (
              <p className="tk-note">汇总没有读到。不把它写成已发放。</p>
            )}
          </section>

          <RewardConfirmForm
            allowed={confirmAllowed}
            inventory={inventory}
            campaignId={campaignId}
            session={session}
            onDone={(note) => {
              setActionNote(note);
              void loadLedger(campaignId);
            }}
          />

          <section aria-label="待核验">
            <h2 className="tk-subtitle">待核验（顾客自报）</h2>
            {reviewQueue.length === 0 ? (
              <p className="tk-note" data-field="review-empty">没有待核验的自报。空不是已处理。</p>
            ) : (
              <ul className="tk-list" data-field="review-queue">
                {reviewQueue.map((item) => (
                  <li key={item.attempt_id}>
                    自报 {item.platform} · 参与/尝试引用 {item.participation_ref}
                    <span className="tk-note">（在确认表单里按活动允许的证据核对后确认）</span>
                  </li>
                ))}
              </ul>
            )}
          </section>

          <section aria-label="奖励批次">
            <h2 className="tk-subtitle">奖励批次（库存）</h2>
            {inventory.length === 0 ? (
              <p className="tk-note" data-field="inventory-empty">还没有奖励批次。空不是不限量。</p>
            ) : (
              <ul className="tk-list" data-field="inventory-list">
                {inventory.map((inv) => (
                  <li key={inv.id} data-inventory-id={inv.id}>
                    <span data-field="inventory-line">{inventoryLine(inv)}</span>
                    <span className="tk-note"> 允许证据：{inv.allowed_evidence.map(evidenceText).join("、")}</span>
                    <span className="tk-note"> {faceAndCostText(inv.face_value_minor, inv.cost_minor, inv.currency)}</span>
                    {manage && inv.status === "active" ? (
                      <button className="tk-quiet" type="button" data-action="pause-batch" onClick={() => setBatchStatus(inv, "paused")}>暂停批次</button>
                    ) : null}
                    {manage && inv.status === "paused" ? (
                      <button className="tk-quiet" type="button" data-action="resume-batch" onClick={() => setBatchStatus(inv, "active")}>恢复批次</button>
                    ) : null}
                  </li>
                ))}
              </ul>
            )}
            {manage ? (
              <InventoryCreateForm
                campaignId={campaignId}
                onDone={(note) => {
                  setActionNote(note);
                  void loadLedger(campaignId);
                }}
                session={session}
              />
            ) : (
              <p className="tk-note">批次（预算）管理只归总部角色；店员不因此获得改全店预算的权限。</p>
            )}
          </section>

          <section aria-label="权益记录">
            <h2 className="tk-subtitle">权益记录</h2>
            {benefits.length === 0 ? (
              <p className="tk-note" data-field="benefits-empty">还没有权益记录。没有事实时不写已发放。</p>
            ) : (
              <ul className="tk-list" data-field="benefits-list">
                {benefits.map((ben) => (
                  <li key={ben.id} data-benefit-id={ben.id} data-benefit-status={ben.status}>
                    <span data-field="benefit-status">{benefitStatusText(ben.status)}</span>
                    {" · "}
                    <span data-field="benefit-evidence">{evidenceText(ben.evidence)}</span>
                    {" · slot "}
                    {slotText(ben.benefit_slot)}（规则 v{ben.rule_version}）
                    {" · 参与引用 "}
                    {ben.participation_id}
                    {ben.store_id ? ` · 门店 ${ben.store_id}` : ""}
                    {ben.reserved_until ? ` · 预留至 ${ben.reserved_until}` : ""}
                    {ben.reverse_reason ? ` · 撤销原因：${ben.reverse_reason}` : ""}
                    <span className="tk-note"> {faceAndCostText(ben.face_value_minor, ben.cost_minor, ben.currency)}</span>
                    <span className="tk-note" data-field="benefit-code">
                      {" 券码 "}
                      {revealed[ben.id] ? (ben.code || "（无码：现场赠品/折扣）") : maskCode(ben.code ?? "")}
                    </span>
                    {ben.code && !revealed[ben.id] ? (
                      <button className="tk-quiet" type="button" data-action="reveal-code" onClick={() => setRevealed((m) => ({ ...m, [ben.id]: true }))}>显示券码</button>
                    ) : null}
                    {confirmAllowed && ben.status === "issued" ? (
                      <button className="tk-quiet" type="button" data-action="redeem" onClick={() => redeemBenefit(ben)}>核销</button>
                    ) : null}
                    {reverseAllowed && ben.status !== "reversed" && ben.status !== "expired" ? (
                      <button className="tk-quiet" type="button" data-action="reverse" onClick={() => reverseBenefit(ben)}>更正撤销</button>
                    ) : null}
                  </li>
                ))}
              </ul>
            )}
          </section>

          {actionNote ? (
            <p className="tk-note" data-field="action-note" data-state={actionNote.includes("完成") || actionNote.includes("核销完成") || actionNote.includes("撤销") ? "ok" : "warn"}>
              {actionNote}
            </p>
          ) : null}

          <p className={presented.className} data-reward-tone={presented.tone} data-success={presented.success ? "true" : "false"}>
            {rewardNotice(reward)} 访客侧状态仍然只读；这一页不替访客发布。
          </p>
          <p className="tk-note">{rulesNote}</p>
          {campaignId ? (
            <p className="tk-note" data-field="publish-config-link">
              活动的发布与启用（内容、渠道、优惠条件、顾客预览）在 <a href={`/work/campaigns/${campaignId}#publish-config`}>活动详情「发布与启用」</a>里配置。
            </p>
          ) : null}
          <p className="tk-note">{separation}</p>
          <p className="tk-gap"><a href={campaignId ? `/work/campaigns/${campaignId}` : "/work/campaigns"}>回到活动</a></p>
          </>
          )}
        </>
      ) : null}
    </main>
  );
}

function line(name: string, value: number | undefined): string {
  if (typeof value !== "number") return `${name}未设置（空表示不限）。这不是已核销。`;
  return `${name} ${value}。`;
}

// 懒加载 session 类型,避免把整个 MerchantSession 拖进纯函数层。
type SessionLike = {
  api: (method: string, path: string, body?: unknown) => Promise<{ ok: boolean; status: number; data: Record<string, unknown> }>;
  role: string;
};

function RewardConfirmForm(props: {
  allowed: boolean;
  inventory: RewardInventoryView[];
  campaignId: string;
  session: SessionLike;
  onDone: (note: string) => void;
}) {
  const { allowed, inventory, campaignId, session, onDone } = props;
  const [participationId, setParticipationId] = useState("");
  const [batchKey, setBatchKey] = useState("");
  const [evidence, setEvidence] = useState("");
  const [storeId, setStoreId] = useState("");
  const [fulfillNow, setFulfillNow] = useState(false);
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);

  const selected = inventory.find((inv) => `${inv.benefit_slot}:${inv.rule_version}` === batchKey);

  if (!allowed) {
    return (
      <section aria-label="确认发放">
        <h2 className="tk-subtitle">确认并发放</h2>
        <p className="tk-note" data-field="confirm-forbidden">当前角色不能确认发放（agent 只读）。</p>
      </section>
    );
  }

  const submit = () =>
    void (async () => {
      if (!selected || !participationId.trim() || !evidence || !storeId.trim()) {
        onDone("参与引用、批次、证据和门店都要填。没有事实不写成功。");
        return;
      }
      setBusy(true);
      try {
        const res = await session.api("POST", `campaigns/${campaignId}/reward-confirm`, {
          ...confirmRequestBody({
            campaignId,
            participationId: participationId.trim(),
            benefitSlot: selected.benefit_slot,
            ruleVersion: selected.rule_version,
            evidence,
            storeId: storeId.trim(),
            fulfillNow,
            note: note.trim(),
          }),
        });
        if (res.ok) {
          const outcome = String(res.data.outcome ?? "");
          onDone(
            outcome === "issued_and_redeemed"
              ? "确认并现场兑现完成（同一事务两条事实：发放+核销）。"
              : outcome === "already_issued"
                ? "该参与此 slot 已有权益，回读已处理结果，没有第二份。"
                : "已发放真实权益（一条发放事实）。",
          );
          setParticipationId("");
        } else {
          onDone(failureText(res.status, res.data));
        }
      } finally {
        setBusy(false);
      }
    })();

  return (
    <section aria-label="确认发放">
      <h2 className="tk-subtitle">确认并发放</h2>
      <p className="tk-note">按活动批次允许的证据确认；仅官方模式的批次不接受目测或核对，也不接受自报。</p>
      <label className="tk-label">
        参与 / 尝试引用
        <input className="tk-input" data-field="confirm-participation" value={participationId} onChange={(e) => setParticipationId(e.target.value)} />
      </label>
      <label className="tk-label">
        奖励批次
        <select className="tk-select" data-field="confirm-batch" value={batchKey} onChange={(e) => { setBatchKey(e.target.value); setEvidence(""); }}>
          <option value="">选择批次…</option>
          {inventory.map((inv) => (
            <option key={inv.id} value={`${inv.benefit_slot}:${inv.rule_version}`}>
              {inv.name}（slot {slotText(inv.benefit_slot)} · 规则 v{inv.rule_version} · 余量 {inv.available_qty}）
            </option>
          ))}
        </select>
      </label>
      <label className="tk-label">
        证据
        <select className="tk-select" data-field="confirm-evidence" value={evidence} onChange={(e) => setEvidence(e.target.value)}>
          <option value="">选择活动允许的证据…</option>
          {(selected?.allowed_evidence ?? []).map((ev) => (
            <option key={ev} value={ev}>{evidenceText(ev)}</option>
          ))}
        </select>
      </label>
      <label className="tk-label">
        确认门店 ID
        <input className="tk-input" data-field="confirm-store" value={storeId} onChange={(e) => setStoreId(e.target.value)} />
      </label>
      <label className="tk-label">
        <input type="checkbox" data-field="confirm-fulfill" checked={fulfillNow} onChange={(e) => setFulfillNow(e.target.checked)} />
        现场已兑现（同一事务再记一条核销事实）
      </label>
      <label className="tk-label">
        说明（可空）
        <input className="tk-input" data-field="confirm-note" value={note} onChange={(e) => setNote(e.target.value)} />
      </label>
      <button className="tk-quiet" type="button" data-action="confirm-reward" disabled={busy} onClick={submit}>
        {busy ? "提交中…" : "确认并发放"}
      </button>
      <p className="tk-note">官方证据需要服务端已有的官方事实引用；填不出的回执不会变成发放。</p>
    </section>
  );
}

function InventoryCreateForm(props: {
  campaignId: string;
  session: SessionLike;
  onDone: (note: string) => void;
}) {
  const { campaignId, session, onDone } = props;
  const [kind, setKind] = useState("store_coupon");
  const [name, setName] = useState("");
  const [slot, setSlot] = useState("first_publish");
  const [version, setVersion] = useState("1");
  const [evidence, setEvidence] = useState("staff_observed_submission,staff_verified_post");
  const [qty, setQty] = useState("1");
  const [codes, setCodes] = useState("");
  const [face, setFace] = useState("0");
  const [cost, setCost] = useState("0");
  const [currency, setCurrency] = useState("FEN");
  const [storeId, setStoreId] = useState("");
  const [ruleRef, setRuleRef] = useState("");
  const [validUntil, setValidUntil] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = () =>
    void (async () => {
      setBusy(true);
      try {
        const body: Record<string, unknown> = {
          kind,
          name: name.trim(),
          benefit_slot: slot.trim(),
          rule_version: Number(version) || 0,
          allowed_evidence: evidence.split(",").map((s) => s.trim()).filter(Boolean),
          total_qty: Number(qty) || 0,
          face_value_minor: Number(face) || 0,
          cost_minor: Number(cost) || 0,
          currency: currency.trim(),
          store_id: storeId.trim(),
          rule_ref: ruleRef.trim(),
          valid_until: validUntil.trim(),
        };
        if (kind === "imported_code") {
          body.codes = codes.split(/[\n,]/).map((s) => s.trim()).filter(Boolean);
        }
        const res = await session.api("POST", `campaigns/${campaignId}/reward-inventory`, body);
        if (res.ok) {
          onDone("批次已建立（库存、面额、成本分开记）。");
          setName("");
          setCodes("");
        } else {
          onDone(failureText(res.status, res.data));
        }
      } finally {
        setBusy(false);
      }
    })();

  return (
    <div data-field="inventory-create">
      <p className="tk-note">批次绑定 slot 与规则版本（审计快照引用可填链接）；导入券码池必须给真实有效码。券面额不是成本，也不是可花的现金。</p>
      <label className="tk-label">
        类型
        <select className="tk-select" data-field="batch-kind" value={kind} onChange={(e) => setKind(e.target.value)}>
          <option value="store_coupon">门店券（系统内核销）</option>
          <option value="imported_code">导入券码池（合法导入）</option>
          <option value="gift_discount">现场赠品/折扣</option>
        </select>
      </label>
      <label className="tk-label">
        名称
        <input className="tk-input" data-field="batch-name" value={name} onChange={(e) => setName(e.target.value)} />
      </label>
      <label className="tk-label">
        权益 slot
        <input className="tk-input" data-field="batch-slot" value={slot} onChange={(e) => setSlot(e.target.value)} />
      </label>
      <label className="tk-label">
        规则版本
        <input className="tk-input" data-field="batch-version" value={version} onChange={(e) => setVersion(e.target.value)} />
      </label>
      <label className="tk-label">
        允许证据（逗号分隔：staff_observed_submission / staff_verified_post / official_publish_confirmed）
        <input className="tk-input" data-field="batch-evidence" value={evidence} onChange={(e) => setEvidence(e.target.value)} />
      </label>
      {kind === "imported_code" ? (
        <label className="tk-label">
          券码（每行一个或逗号分隔，1..500，敏感不入日志）
          <textarea className="tk-input" data-field="batch-codes" value={codes} onChange={(e) => setCodes(e.target.value)} />
        </label>
      ) : (
        <label className="tk-label">
          数量
          <input className="tk-input" data-field="batch-qty" value={qty} onChange={(e) => setQty(e.target.value)} />
        </label>
      )}
      <label className="tk-label">
        券面额（分）
        <input className="tk-input" data-field="batch-face" value={face} onChange={(e) => setFace(e.target.value)} />
      </label>
      <label className="tk-label">
        实际成本（分）
        <input className="tk-input" data-field="batch-cost" value={cost} onChange={(e) => setCost(e.target.value)} />
      </label>
      <label className="tk-label">
        币种单位
        <input className="tk-input" data-field="batch-currency" value={currency} onChange={(e) => setCurrency(e.target.value)} />
      </label>
      <label className="tk-label">
        适用门店 ID（空 = 全部门店）
        <input className="tk-input" data-field="batch-store" value={storeId} onChange={(e) => setStoreId(e.target.value)} />
      </label>
      <label className="tk-label">
        规则快照引用（可空）
        <input className="tk-input" data-field="batch-rule-ref" value={ruleRef} onChange={(e) => setRuleRef(e.target.value)} />
      </label>
      <label className="tk-label">
        有效期至（RFC3339，可空）
        <input className="tk-input" data-field="batch-valid-until" value={validUntil} onChange={(e) => setValidUntil(e.target.value)} />
      </label>
      <button className="tk-quiet" type="button" data-action="create-batch" disabled={busy} onClick={submit}>
        {busy ? "提交中…" : "建立批次"}
      </button>
    </div>
  );
}

// HUI-4590: 外部团购页签落地（上下架/编辑/校验时间管理 + 外部券授权核销工具）。
// 诚实纪律：点击不是成交；平台成交与核销数本地不可知；未授权不接通——
// 不代发也不本地核销；核销事实只来自授权适配器；待确认不是已发放。
function ExternalGroupBuyTab(props: {
  stores: StoreRec[];
  storeId: string;
  onStoreChange: (id: string) => void;
  offers: StoreDealOfferView[];
  externalIssuances: ExternalIssuanceView[];
  manage: boolean;
  session: SessionLike;
  onNote: (note: string) => void;
  onReload: () => void;
}) {
  const { stores, storeId, onStoreChange, offers, externalIssuances, manage, session, onNote, onReload } = props;

  const act = (fn: () => Promise<void>) =>
    void (async () => {
      onNote("");
      try {
        await fn();
      } catch {
        onNote("操作没有完成。没有事实不写成功。");
      }
      onReload();
    })();

  const setOfferStatus = (offer: StoreDealOfferView, status: string) =>
    act(async () => {
      const res = await session.api("POST", `stores/${storeId}/deal-offers/${offer.id}/status`, { status });
      if (res.ok) {
        onNote(status === "paused" ? "已下架（顾客不再看到新曝光）。" : status === "active" ? "已上架。" : "已退役（不可逆）。");
      } else {
        onNote(failureText(res.status, res.data));
      }
    });

  const editOffer = (offer: StoreDealOfferView, field: "title" | "price", raw: string) =>
    act(async () => {
      const body: Record<string, unknown> =
        field === "title" ? { title: raw.trim() } : { price_minor: Number(raw) || 0 };
      const res = await session.api("PATCH", `stores/${storeId}/deal-offers/${offer.id}`, body);
      if (res.ok) onNote(field === "title" ? "标题已更新。" : "价格已更新（未重新核对前顾客仍看到「到平台查看」）。");
      else onNote(failureText(res.status, res.data));
    });

  const verifyPrice = (offer: StoreDealOfferView) =>
    act(async () => {
      const res = await session.api("PATCH", `stores/${storeId}/deal-offers/${offer.id}`, { mark_price_verified: true });
      if (res.ok) onNote("已记录本次价格核对（校验时间=服务器时间；72 小时内顾客可见该价）。");
      else onNote(failureText(res.status, res.data));
    });

  const recordAuth = (offer: StoreDealOfferView, raw: string) =>
    act(async () => {
      const res = await session.api("PATCH", `stores/${storeId}/deal-offers/${offer.id}`, { authorization_ref: raw.trim() });
      if (res.ok) onNote("授权引用已记录。获得官方授权后核销才走适配器；未接通时核销仍会诚实拒绝。");
      else onNote(failureText(res.status, res.data));
    });

  const createOffer = (form: DealCreateFormState) =>
    act(async () => {
      if (!form.platform || !form.title.trim() || !form.product_url.trim()) {
        onNote("平台、标题和商品链接都要填。没有事实不写成功。");
        return;
      }
      const res = await session.api("POST", `stores/${storeId}/deal-offers`, {
        platform: form.platform,
        provider_offer_id: form.provider_offer_id.trim(),
        product_url: form.product_url.trim(),
        title: form.title.trim(),
        image_ref: form.image_ref.trim(),
        price_minor: Number(form.price) || 0,
        currency: form.currency.trim(),
        price_source: form.price_source,
        valid_until: form.valid_until.trim(),
      });
      if (res.ok) onNote("套餐已上架（顾客门店页可以看到，点击会跳到平台商品页）。");
      else onNote(failureText(res.status, res.data));
    });

  return (
    <>
      <section aria-label="外部团购套餐管理">
        <h2 className="tk-subtitle">外部团购（美团 / 大众点评 / 抖音）</h2>
        <p className="tk-note" data-field="external-placeholder">
          外部团购配置与授权核销归 HUI-4590 的授权适配器；顾客点击套餐卡只是点击，不计为成交。
          价格只在核对未超过 72 小时时展示，其余显示「到平台查看」。未接通的券不代发也不本地核销。
        </p>
        {stores.length === 0 ? (
          <p className="tk-note" data-field="external-empty-store">还没有门店，先到门店页建立门店。</p>
        ) : (
          <>
            <label className="tk-label">
              门店
              <select className="tk-select" data-field="external-store" value={storeId} onChange={(e) => onStoreChange(e.target.value)}>
                {stores.map((s) => (
                  <option key={s.id} value={s.id}>{s.name}</option>
                ))}
              </select>
            </label>
            {offers.length === 0 ? (
              <p className="tk-note" data-field="external-empty">该门店还没有团购套餐。空不是没有生意。</p>
            ) : (
              <ul className="tk-list" data-field="external-offers">
                {offers.map((offer) => (
                  <li key={offer.id} data-offer-id={offer.id} data-offer-status={offer.status}>
                    <span data-field="offer-platform">{platformText(offer.platform)}</span>
                    {" · "}
                    <span data-field="offer-title">{offer.title}</span>
                    {" · "}
                    <span data-field="offer-status">{offerStatusText(offer.status)}</span>
                    {" · "}
                    <span data-field="offer-price">{priceStateText(offer.price_state)}</span>
                    {" · "}
                    <span data-field="offer-verified">{verifiedAtText(offer.price_verified_at)}</span>
                    {" · 点击 "}
                    <span data-field="offer-clicks">{offer.click_total ?? 0}</span>
                    （点击不是成交）
                    {manage && offer.status === "active" ? (
                      <button className="tk-quiet" type="button" data-action="offer-pause" onClick={() => setOfferStatus(offer, "paused")}>下架</button>
                    ) : null}
                    {manage && offer.status === "paused" ? (
                      <button className="tk-quiet" type="button" data-action="offer-resume" onClick={() => setOfferStatus(offer, "active")}>上架</button>
                    ) : null}
                    {manage && offer.status !== "retired" ? (
                      <button className="tk-quiet" type="button" data-action="offer-retire" onClick={() => setOfferStatus(offer, "retired")}>退役</button>
                    ) : null}
                    {manage ? (
                      <button className="tk-quiet" type="button" data-action="offer-retitle" onClick={() => {
                        const raw = window.prompt("新标题：", offer.title);
                        if (raw && raw.trim()) editOffer(offer, "title", raw);
                      }}>改标题</button>
                    ) : null}
                    {manage ? (
                      <button className="tk-quiet" type="button" data-action="offer-reprice" onClick={() => {
                        const raw = window.prompt("新价格（分）：", String(offer.price_minor));
                        if (raw !== null && raw.trim() !== "") editOffer(offer, "price", raw);
                      }}>改价格</button>
                    ) : null}
                    {manage ? (
                      <button className="tk-quiet" type="button" data-action="offer-verify" onClick={() => verifyPrice(offer)}>标记已核对价格</button>
                    ) : null}
                    {manage ? (
                      <button className="tk-quiet" type="button" data-action="offer-auth" onClick={() => {
                        const raw = window.prompt("外部授权引用（从平台授权适配器获得；留空取消）：", offer.authorization_ref || "");
                        if (raw && raw.trim()) recordAuth(offer, raw);
                      }}>记录授权</button>
                    ) : null}
                  </li>
                ))}
              </ul>
            )}
            {manage ? <DealOfferCreateForm storeId={storeId} onCreate={createOffer} /> : null}
            <CouponTool
              storeId={storeId}
              offers={offers}
              session={session}
              onNote={onNote}
            />
          </>
        )}
        <h2 className="tk-subtitle">外部券发放登记</h2>
        {externalIssuances.length === 0 ? (
          <p className="tk-note" data-field="external-empty">还没有外部券发放登记。没有事实不写成功。</p>
        ) : (
          <ul className="tk-list" data-field="external-list">
            {externalIssuances.map((item) => (
              <li key={item.id} data-external-id={item.id} data-external-state={item.state}>
                <span data-field="external-state">{externalStateText(item.state)}</span>
                {" · "}
                {item.provider || "外部平台"}
                {" · 参与/尝试引用 "}
                {item.participation_id}
              </li>
            ))}
          </ul>
        )}
      </section>
    </>
  );
}

interface DealCreateFormState {
  platform: string;
  provider_offer_id: string;
  product_url: string;
  title: string;
  image_ref: string;
  price: string;
  currency: string;
  price_source: string;
  valid_until: string;
}

function DealOfferCreateForm(props: {
  storeId: string;
  onCreate: (form: DealCreateFormState) => void;
}) {
  const initial: DealCreateFormState = {
    platform: "meituan",
    provider_offer_id: "",
    product_url: "",
    title: "",
    image_ref: "",
    price: "0",
    currency: "FEN",
    price_source: "platform",
    valid_until: "",
  };
  const [form, setForm] = useState<DealCreateFormState>(initial);
  const [busy, setBusy] = useState(false);
  const set = (k: keyof DealCreateFormState) => (e: { target: { value: string } }) =>
    setForm((f) => ({ ...f, [k]: e.target.value }));

  return (
    <div data-field="offer-create">
      <p className="tk-note">
        商品链接必须是该平台官方域名的公网 https 页面（美团↔美团、点评↔点评、抖音↔抖音），错挂渠道会被拒绝。
      </p>
      <label className="tk-label">
        平台
        <select className="tk-select" data-field="offer-platform-input" value={form.platform} onChange={set("platform")}>
          <option value="meituan">美团</option>
          <option value="dianping">大众点评</option>
          <option value="douyin">抖音</option>
        </select>
      </label>
      <label className="tk-label">
        平台商品 ID（可空）
        <input className="tk-input" data-field="offer-provider-id" value={form.provider_offer_id} onChange={set("provider_offer_id")} />
      </label>
      <label className="tk-label">
        商品链接（https）
        <input className="tk-input" data-field="offer-url" value={form.product_url} onChange={set("product_url")} />
      </label>
      <label className="tk-label">
        展示标题
        <input className="tk-input" data-field="offer-title-input" value={form.title} onChange={set("title")} />
      </label>
      <label className="tk-label">
        展示图引用（可空）
        <input className="tk-input" data-field="offer-image" value={form.image_ref} onChange={set("image_ref")} />
      </label>
      <label className="tk-label">
        价格（分）
        <input className="tk-input" data-field="offer-price" value={form.price} onChange={set("price")} />
      </label>
      <label className="tk-label">
        币种单位
        <input className="tk-input" data-field="offer-currency" value={form.currency} onChange={set("currency")} />
      </label>
      <label className="tk-label">
        价格来源
        <select className="tk-select" data-field="offer-price-source" value={form.price_source} onChange={set("price_source")}>
          <option value="platform">平台页实价</option>
          <option value="merchant">商家手填</option>
        </select>
      </label>
      <label className="tk-label">
        有效期至（RFC3339，可空）
        <input className="tk-input" data-field="offer-valid-until" value={form.valid_until} onChange={set("valid_until")} />
      </label>
      <button className="tk-quiet" type="button" data-action="create-offer" disabled={busy} onClick={() => { setBusy(true); try { props.onCreate(form); } finally { setBusy(false); } setForm(initial); }}>
        {busy ? "提交中…" : "上架套餐"}
      </button>
    </div>
  );
}

function CouponTool(props: {
  storeId: string;
  offers: StoreDealOfferView[];
  session: SessionLike;
  onNote: (note: string) => void;
}) {
  const { storeId, offers, session, onNote } = props;
  const [offerId, setOfferId] = useState("");
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);

  if (offers.length === 0) return null;

  const run = (kind: "check" | "redeem") =>
    void (async () => {
      if (!offerId || !code.trim()) {
        onNote("选择套餐并输入券码。没有事实不写成功。");
        return;
      }
      setBusy(true);
      try {
        const res = await session.api("POST", `stores/${storeId}/deal-coupons/${kind}`, {
          offer_id: offerId,
          code: code.trim(),
        });
        if (res.ok) {
          onNote(couponOutcomeText(res.data as unknown as DealCouponResultView));
        } else {
          onNote(failureText(res.status, res.data));
        }
      } catch {
        onNote("操作没有完成。没有事实不写成功。");
      } finally {
        setBusy(false);
      }
    })();

  return (
    <div data-field="coupon-tool">
      <p className="tk-note">
        外部券检查/核销走授权适配器返回平台事实；未接通或未授权会诚实拒绝（不本地假核销）。券码不进日志。
      </p>
      <label className="tk-label">
        套餐
        <select className="tk-select" data-field="coupon-offer" value={offerId} onChange={(e) => setOfferId(e.target.value)}>
          <option value="">选择套餐…</option>
          {offers.map((o) => (
            <option key={o.id} value={o.id}>{platformText(o.platform)} · {o.title}</option>
          ))}
        </select>
      </label>
      <label className="tk-label">
        券码
        <input className="tk-input" data-field="coupon-code" value={code} onChange={(e) => setCode(e.target.value)} />
      </label>
      <button className="tk-quiet" type="button" data-action="coupon-check" disabled={busy} onClick={() => run("check")}>查券</button>
      <button className="tk-quiet" type="button" data-action="coupon-redeem" disabled={busy} onClick={() => run("redeem")}>核销</button>
    </div>
  );
}
