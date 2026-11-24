"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { failureText, useSession } from "@/components/work/merchant-session";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { presentAccountSeparation, type SeparationView } from "@/lib/account-separation";
import { CampaignInventoryPrepare } from "@/components/work/campaign-inventory-prepare";
import { toRFC3339,benefitKindLabel,verificationModeLabel,type PublishConfigRecord } from "@/lib/campaign-config";
import { DateTimeField } from "@/components/ui/date-time-field";
import { formatCampaignWindow } from "@/lib/public-time";
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
  formatMoney,
  moneyInputMinor,
  benefitStatusText,
  canConfirmRewards,
  canManageInventory,
  canReverseRewards,
  evidenceText,
  externalStateText,
  faceAndCostText,
  inventoryKindText,
  inventoryLine,
  maskCode,
  reverseRequestBody,
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
  const [promise,setPromise]=useState<PublishConfigRecord|null>(null);
  const [rulesNote, setRulesNote] = useState("规则还没读取");
  const [reward, setReward] = useState<RewardView>({ status: "unknown" });
  const [separation, setSeparation] = useState("");
  const [attempt, setAttempt] = useState(0);

  const [tab, setTab] = useState<RewardTab>("store");
  // 批次发放 / 权益记录核销 分区走 Radix Tabs;与上方门店券/外部团购页签互不替代。
  const [rewardTab, setRewardTab] = useState<"issue" | "records">("issue");
  const ledgerRequest=useRef(0);
  const [ledgerPhase,setLedgerPhase]=useState<"loading"|"error"|"ready">("loading");
  const [ledgerError,setLedgerError]=useState("");
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
      const request=++ledgerRequest.current;setLedgerPhase(previous=>previous==="ready"?"ready":"loading");setLedgerError("");
      try {
      const [invRes, benRes, sumRes, revRes, extRes] = await Promise.all([
        session.api("GET", `campaigns/${cid}/reward-inventory`),
        session.api("GET", `campaigns/${cid}/reward-benefits`),
        session.api("GET", `campaigns/${cid}/reward-summary`),
        session.api("GET", `campaigns/${cid}/reward-review-requests`),
        session.api("GET", `campaigns/${cid}/reward-external-issuances`),
      ]);
      if(request!==ledgerRequest.current)return;
      const failed=[invRes,benRes,sumRes,revRes].find(result=>!result.ok);
      if(failed){setLedgerError(failureText(failed.status,failed.data));setLedgerPhase("error");return;}
      setLedgerPhase("ready");
      setInventory(invRes.ok && Array.isArray(invRes.data.items) ? (invRes.data.items as RewardInventoryView[]) : []);
      setBenefits(benRes.ok && Array.isArray(benRes.data.items) ? (benRes.data.items as RewardBenefitView[]) : []);
      setSummary(sumRes.ok ? (sumRes.data as unknown as RewardSummaryView) : null);
      setReviewQueue(revRes.ok && Array.isArray(revRes.data.items) ? (revRes.data.items as RewardReviewRequestView[]) : []);
      setExternalIssuances(extRes.ok && Array.isArray(extRes.data.items) ? (extRes.data.items as ExternalIssuanceView[]) : []);
      if (!invRes.ok && invRes.status !== 404) {
        setActionNote(failureText(invRes.status, invRes.data));
      }
      } catch {if(request===ledgerRequest.current){setLedgerPhase("error");setLedgerError("没有连上库存与履约服务，请重试。这不是空记录。");}}
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
      const requested=new URLSearchParams(window.location.search).get("campaign");
      setCampaignId(items.some(item=>item.id===requested)?requested!:items[0]?.id ?? "");
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
    setLedgerPhase("loading");
    let alive = true;
    void (async () => {
      setPromise(null);setRulesNote("正在读取活动承诺…");
      const rulesRes=await session.api("GET",`campaigns/${campaignId}/publish-config`);
      if(!alive)return;
      if(rulesRes.ok){setPromise(rulesRes.data as PublishConfigRecord);setRulesNote("活动承诺来自唯一活动配置，库存只处理数量与履约事务。");}
      else setRulesNote(rulesRes.status===404?"尚未保存顾客承诺，请先到活动页配置。":failureText(rulesRes.status,rulesRes.data));
      await loadLedger(campaignId);
    })().catch(() => {
      if (alive) {setLedgerPhase("error");setLedgerError("奖励账本没有读到，请重试。这不是空记录。");}
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
      <h1 className="tk-title">优惠库存与履约</h1>
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
            <button role="tab" type="button" data-action="tab-external" aria-selected={tab === "external"} onClick={() => setTab("external")}>外部团购</button>
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
          {ledgerPhase==="loading"?<p className="tk-state" data-state="loading">正在读取优惠库存与履约记录…</p>:ledgerPhase==="error"?<p className="tk-danger" data-state="error">{ledgerError} 记录尚未读到，不视为没有库存或义务。<button className="tk-quiet" type="button" onClick={()=>void loadLedger(campaignId)}>重试读取</button></p>:<>
          <section aria-label="奖励汇总">
            <h2 className="tk-subtitle">奖励汇总</h2>
            {summary ? (
              <p className="tk-note" data-field="reward-summary">
                {summaryLines(summary).join(" ")}
                {summary.committed_face_minor !== undefined
                  ? ` 已承诺面额 ${formatMoney(summary.committed_face_minor)}（不是成本），实际成本 ${summary.committed_cost_unrecorded===0 && summary.committed_cost_minor!==undefined ? formatMoney(summary.committed_cost_minor) : "未知（含未录入成本或未读到完整性）"}。`
                  : ""}
              </p>
            ) : (
              <p className="tk-note">汇总没有读到。不把它写成已发放。</p>
            )}
          </section>

          <section aria-label="活动承诺"><h2 className="tk-subtitle">同一份顾客承诺</h2><p className="tk-note">{rulesNote}</p>{promise ? <p className="tk-note">{promise.enabled_version ? `线上第 ${promise.enabled_version} 版${promise.published_status === "enabled" ? "生效中" : promise.published_status === "paused" ? "已暂停" : "状态未读到"}。` : "尚无已启用版本。"}{promise.version !== promise.enabled_version ? ` 下方展示第 ${promise.version} 版编辑稿，保存和备库存不会替换线上承诺；须明确启用后才生效。` : " 下方为同一版本的承诺摘要。"}</p> : null}{promise?.spec?<><p>{promise.spec.customer_copy || "顾客说明尚未填写"}</p><p>{benefitKindLabel(promise.spec.reward.kind)} · {verificationModeLabel(promise.spec.verification_mode)} · 第 {promise.version} 版 · 适用门店：{stores.filter(store=>promise.spec?.reward.store_ids.includes(store.id)).map(store=>store.name).join("、") || "门店资料尚未读到"}</p><p>领取期：{formatCampaignWindow(promise.spec.reward.claim_from,promise.spec.reward.claim_until,{timeZone:"Asia/Shanghai"})}；使用期：{formatCampaignWindow(promise.spec.reward.redeem_from,promise.spec.reward.redeem_until,{timeZone:"Asia/Shanghai"})}（北京时间 UTC+8）。</p></>:null}<p><a href={`/work/campaigns/${campaignId}#publish-config`}>修改活动承诺</a></p><p className="tk-note">活动暂停、结束或订阅到期会停止新发放；已发有效券仍可从下方履约记录找到并办理，券使用期限独立。在途预留需按冻结条件核对，不代表已发放。</p></section>
          <p><a className="tk-button" href={`/work/service?campaign=${encodeURIComponent(campaignId)}&return=${encodeURIComponent(`/work/rewards?campaign=${campaignId}`)}`}>到店员台扫码确认或核销</a></p>

          <section aria-label="待核验">
            <h2 className="tk-subtitle">待处理（顾客申请核验）</h2>
            {reviewQueue.length === 0 ? (
              <p className="tk-note" data-field="review-empty">没有待核验的自报。空不是已处理。</p>
            ) : (
              <ul className="tk-list" data-field="review-queue">
                {reviewQueue.map((item) => (
                  <li key={item.attempt_id}>
                    {item.platform} · 申请时间 {new Date(item.created_at).toLocaleString("zh-CN",{timeZone:"Asia/Shanghai"})}
                    <span className="tk-note">（到店员台按活动承诺核对证据）</span>
                  </li>
                ))}
              </ul>
            )}
          </section>

          <Tabs value={rewardTab} onValueChange={(v) => setRewardTab(v === "records" ? "records" : "issue")} className="tk-gap">
            <TabsList>
              <TabsTrigger value="issue">发放与批次</TabsTrigger>
              <TabsTrigger value="records">权益记录（核销）</TabsTrigger>
            </TabsList>
            <TabsContent value="issue" className="tk-gap">
          <section aria-label="奖励批次">
            <h2 className="tk-subtitle">优惠库存</h2>
            {inventory.length === 0 ? (
              <p className="tk-note" data-field="inventory-empty">还没有奖励批次。空不是不限量。</p>
            ) : (
              <ul className="tk-list" data-field="inventory-list">
                {inventory.map((inv) => (
                  <li key={inv.id} data-inventory-id={inv.id}>
                    <span data-field="inventory-line">{inventoryLine(inv)}</span>
                    <span className="tk-note"> 适用门店：{inv.store_id?stores.find(store=>store.id===inv.store_id)?.name || "门店资料尚未读到":"活动承诺适用门店"}</span>
                    <span className="tk-note"> 允许证据：{inv.allowed_evidence.map(evidenceText).join("、")}</span>
                    <span className="tk-note"> {faceAndCostText(inv.face_value_minor, inv.cost_minor, inv.currency, inv.cost_recorded)}</span>
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
              <CampaignInventoryPrepare campaignId={campaignId} onDone={note=>{setActionNote(note);void loadLedger(campaignId);}} />
            ) : (
              <p className="tk-note">批次（预算）管理只归总部角色；店员不因此获得改全店预算的权限。</p>
            )}
          </section>
            </TabsContent>
            <TabsContent value="records" className="tk-gap">
          <section aria-label="权益记录">
            <h2 className="tk-subtitle">履约记录与异常</h2>
            {benefits.length === 0 ? (
              <p className="tk-note" data-field="benefits-empty">还没有权益记录。没有事实时不写已发放。</p>
            ) : (
              <ul className="tk-list" data-field="benefits-list">
                {benefits.map((ben) => (
                  <li key={ben.id} data-benefit-id={ben.id} data-benefit-status={ben.status}>
                    <span data-field="benefit-status">{benefitStatusText(ben.status)}</span>
                    {" · "}
                    <span data-field="benefit-evidence">{evidenceText(ben.evidence)}</span>
                    {" · "}{inventory.find(inv=>inv.id===(ben as RewardBenefitView & {inventory_id?:string}).inventory_id)?.name || "已冻结的顾客权益"}
                    {" · "}{stores.find(store=>store.id===ben.store_id)?.name || "活动适用门店"}
                    {ben.reserved_until ? ` · 预留至 ${new Date(ben.reserved_until).toLocaleString("zh-CN",{timeZone:"Asia/Shanghai"})}（北京时间）` : ""}
                    {ben.issued_at ? ` · 发放于 ${new Date(ben.issued_at).toLocaleString("zh-CN",{timeZone:"Asia/Shanghai"})}（北京时间）` : ""}
                    {ben.redeemed_at ? ` · 核销于 ${new Date(ben.redeemed_at).toLocaleString("zh-CN",{timeZone:"Asia/Shanghai"})}（北京时间）` : ""}
                    {ben.reverse_reason ? ` · 撤销原因：${ben.reverse_reason}` : ""}
                    <span className="tk-note"> {faceAndCostText(ben.face_value_minor, ben.cost_minor, ben.currency, ben.cost_recorded)}</span>
                    <span className="tk-note" data-field="benefit-code">
                      {" 券码 "}
                      {revealed[ben.id] ? (ben.code || "（无码：现场赠品/折扣）") : maskCode(ben.code ?? "")}
                    </span>
                    {ben.code && !revealed[ben.id] ? (
                      <button className="tk-quiet" type="button" data-action="reveal-code" onClick={() => setRevealed((m) => ({ ...m, [ben.id]: true }))}>显示券码</button>
                    ) : null}
                    {confirmAllowed && ben.status === "issued" ? (
                      <a className="tk-quiet" data-action="redeem" href={`/work/service?benefit=${encodeURIComponent(ben.id)}&return=${encodeURIComponent(`/work/rewards?campaign=${campaignId}`)}`}>到店员台办理</a>
                    ) : null}
                    {reverseAllowed && ben.status !== "reversed" && ben.status !== "expired" ? (
                      <button className="tk-quiet" type="button" data-action="reverse" onClick={() => reverseBenefit(ben)}>更正撤销</button>
                    ) : null}
                  </li>
                ))}
              </ul>
            )}
          </section>
            </TabsContent>
          </Tabs>



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
          </>}
          </>
          )}
          {actionNote ? (
            <p className="tk-note" data-field="action-note" data-state={actionNote.includes("完成") || actionNote.includes("核销完成") || actionNote.includes("撤销") ? "ok" : "warn"}>
              {actionNote}
            </p>
          ) : null}
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
      if (field === "price") {
        if (moneyInputMinor(raw)==null) {
          onNote("价格请用元，最多两位小数。没有改成就不写。");
          return;
        }
      }
      const body: Record<string, unknown> =
        field === "title" ? { title: raw.trim() } : { price_minor: moneyInputMinor(raw) };
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
      const priceMinor = moneyInputMinor(form.price);
      // Walk D 对抗门 adv01：负数/非法价格前端先拦并给可见反馈，不再静默复位。
      if (priceMinor == null || priceMinor <= 0) {
        onNote("价格（元）必须大于 0，最多两位小数。没有事实不写成功。");
        return;
      }
      const res = await session.api("POST", `stores/${storeId}/deal-offers`, {
        platform: form.platform,
        provider_offer_id: form.provider_offer_id.trim(),
        product_url: form.product_url.trim(),
        title: form.title.trim(),
        image_ref: form.image_ref.trim(),
        price_minor: priceMinor,
        currency: form.currency.trim(),
        price_source: form.price_source,
        valid_until: toRFC3339(form.valid_until),
      });
      if (res.ok) onNote("套餐已上架（顾客门店页可以看到，点击会跳到平台商品页）。");
      else onNote(failureText(res.status, res.data));
    });

  return (
    <>
      <section aria-label="外部团购套餐管理">
        <h2 className="tk-subtitle">外部团购（美团 / 大众点评 / 抖音）</h2>
        <p className="tk-note" data-field="external-placeholder">
          外部团购配置与核销通过已授权平台；顾客点击套餐卡只是点击，不计为成交。
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
                        const raw = window.prompt("新价格（元）：", String(offer.price_minor / 100));
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
    price: "",
    currency: "CNY",
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
        商品链接（https）
        <input className="tk-input" data-field="offer-url" value={form.product_url} onChange={set("product_url")} />
      </label>
      <label className="tk-label">
        展示标题
        <input className="tk-input" data-field="offer-title-input" value={form.title} onChange={set("title")} />
      </label>
      <label className="tk-label">
        价格（元）
        <input className="tk-input" data-field="offer-price" value={form.price} onChange={set("price")} />
      </label>
      <label className="tk-label">
        价格来源
        <select className="tk-select" data-field="offer-price-source" value={form.price_source} onChange={set("price_source")}>
          <option value="platform">平台页实价</option>
          <option value="merchant">商家手填</option>
        </select>
      </label>
      <label className="tk-label">
        有效期至（北京时间 UTC+8，可空）
        <span data-field="offer-valid-until">
          <DateTimeField
            name="valid_until"
            aria-label="套餐有效期至（北京时间 UTC+8，可空）"
            value={form.valid_until}
            onChange={(value) => setForm({ ...form, valid_until: value })}
          />
        </span>
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

  const run = (kind: "inspect" | "redeem") =>
    void (async () => {
      if (!offerId || !code.trim()) {
        onNote("选择套餐并输入券码。没有事实不写成功。");
        return;
      }
      setBusy(true);
      try {
        const res = await session.api("POST", `stores/${storeId}/external-vouchers/${kind}`, {
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
      <button className="tk-quiet" type="button" data-action="coupon-check" disabled={busy} onClick={() => run("inspect")}>查券</button>
      <button className="tk-quiet" type="button" data-action="coupon-redeem" disabled={busy} onClick={() => run("redeem")}>核销</button>
    </div>
  );
}
