"use client";

import { useCallback, useEffect, useState } from "react";
import { failureText, useSession } from "@/components/work/merchant-session";
import { presentAccountSeparation, type SeparationView } from "@/lib/account-separation";
import { rewardPresentation, surfaceLabel } from "@/lib/product-finish";
import { rewardNotice, type RewardView } from "@/lib/publish-reward";
import {
  benefitStatusText,
  canConfirmRewards,
  canManageInventory,
  canReverseRewards,
  confirmRequestBody,
  evidenceText,
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

interface Ruleset {
  reward_threshold?: number;
  daily_publish_limit?: number;
  duplicate_publish_window_hours?: number;
  per_contact_daily_submission_cap?: number;
}

type LoadPhase = "loading" | "error" | "empty" | "ready";

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

  const [inventory, setInventory] = useState<RewardInventoryView[]>([]);
  const [benefits, setBenefits] = useState<RewardBenefitView[]>([]);
  const [summary, setSummary] = useState<RewardSummaryView | null>(null);
  const [reviewQueue, setReviewQueue] = useState<RewardReviewRequestView[]>([]);
  const [revealed, setRevealed] = useState<Record<string, boolean>>({});
  const [actionNote, setActionNote] = useState("");

  const loadLedger = useCallback(
    async (cid: string) => {
      if (!cid) return;
      const [invRes, benRes, sumRes, revRes] = await Promise.all([
        session.api("GET", `campaigns/${cid}/reward-inventory`),
        session.api("GET", `campaigns/${cid}/reward-benefits`),
        session.api("GET", `campaigns/${cid}/reward-summary`),
        session.api("GET", `campaigns/${cid}/reward-review-requests`),
      ]);
      setInventory(invRes.ok && Array.isArray(invRes.data.items) ? (invRes.data.items as RewardInventoryView[]) : []);
      setBenefits(benRes.ok && Array.isArray(benRes.data.items) ? (benRes.data.items as RewardBenefitView[]) : []);
      setSummary(sumRes.ok ? (sumRes.data as unknown as RewardSummaryView) : null);
      setReviewQueue(revRes.ok && Array.isArray(revRes.data.items) ? (revRes.data.items as RewardReviewRequestView[]) : []);
      if (!invRes.ok && invRes.status !== 404) {
        setActionNote(failureText(invRes.status, invRes.data));
      }
    },
    [session],
  );

  useEffect(() => {
    let alive = true;
    void (async () => {
      const [campaignRes, accountRes] = await Promise.all([
        session.api("GET", "campaigns"),
        session.api("GET", "account-separation"),
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
