"use client";

// 公共活动页(路由区 /c/[code]):游客看到的是这家店的活动，不是平台后台。
// 顺序:门店 → 能得到什么 → 可做的动作 → 单独的留资同意 → 提交后的下一步。
// 碰一下、扫码、看视频不会变成留资。留资必须勾选本次告知;营销许可可拒绝。
//
// HUI-1896 三屏(同一页内的状态分区,不是任务大厅):
//   屏1 活动与优惠(ActivityIntro:先说明后操作,主动作「选择内容并参与」)
//   屏2 内容与发布(ContentPublish + 1670 PublishHandoff 挂载)
//   屏3 领取或请店员确认(RewardStatus)
// 参与生命周期(3546):普通浏览不建参与;lookup 恢复;换选只改未交接草稿;
// 取消可退;「我已完成」只请求核验(在屏3/交接组件内,都不能自己让奖励通过)。

import { Suspense, useCallback, useEffect, useRef, useState, type ReactElement } from "react";
import { useParams, useSearchParams } from "next/navigation";
import { classifyEntry, failureState, STATE_ACTION, STATE_TEXT, storeNoticeText, type PublicUiState } from "@/lib/public-state";
import { inSiteTargetFromParams } from "@/lib/safe-redirect";
import {
  actionClickRecord,
  activityBlocks,
  beaconChannel,
  guestActionLabel,
  guestActionsFromPayload,
  isOfficialActionUrl,
  leadDisclosureReady,
  leadOutcomeCopy,
  presentGuestActions,
  publicSectionOrder,
  publicVisitorColumns,
  settleClick,
  type GuestCapability,
  type PublicSection,
} from "@/lib/visitor-experience";
import {
  activityJumpActions,
  presentPrivateDomain,
  privateDomainClick,
  privateDomainLabel,
  privateDomainNote,
  settlePrivateDomainClick,
  type PrivateDomainGuide,
} from "@/lib/private-domain";
import { CustomerPublish } from "./customer-publish";
import { PublishHandoff } from "@/components/publish/PublishHandoff";
import { ActivityIntro } from "@/components/customer/ActivityIntro";
import { ContentPublish } from "@/components/customer/ContentPublish";
import { RewardStatus } from "@/components/customer/RewardStatus";
import {
  cancelParticipation,
  canSwitchDraft,
  journeyStage,
  lookupParticipation,
  startParticipation,
  switchDraft,
  type AttemptRef,
  type CustomerParticipation,
} from "@/lib/customer-journey";
import { publicVisitorCopy } from "@/lib/account-separation";
import { formatCampaignWindow } from "@/lib/public-time";
import { LEAD_REVOKED_COPY, leadOutcomeState } from "@/lib/lead-outcome";
import {
  authorizedReturnHref,
  canonicalHref,
  capabilityGapLines,
  closedNotices,
  honestOpen,
  openedEvidenceCopy,
  openMode,
  standingEvidenceCopy,
  type ClosedNotice,
} from "@/lib/jump-matrix";

interface PublicView {
  state: string;
  capabilities?: {
    participation?: boolean;
    content_packs?: boolean;
    reward_ops?: boolean;
    leads_capture?: boolean;
  };
  title?: string;
  public_content?: string;
  starts_at?: string;
  ends_at?: string;
  store_notice?: string;
  merchant_name?: string;
  store_name?: string;
  brand_shell?: { display_name?: string; support_name?: string; support_contact?: string };
}

interface LeadFormView {
  enabled: boolean;
  required_fields?: string[];
  purpose?: string;
  recipient?: { name?: string };
  notice?: { version: string; text: string };
  marketing_optin_enabled?: boolean;
  marketing_blocks_browse?: boolean;
}

interface LeadSubmitResult {
  submission_ref?: string;
  state?: string;
  duplicate?: boolean;
  crm_received?: boolean;
  submitted_to?: { name?: string };
}

interface PublicRewardSummary {
  slots?: { benefit_slot: string; rule_version: number; available_qty: number }[];
}

export function PublicCampaign({ lane = "activity" }: { lane?: "activity" | "contact" }) {
  return (
    <Suspense fallback={<main className="tk-public" data-state="loading" data-lane={lane}><p className="tk-note">正在读取</p></main>}>
      <PublicCampaignInner lane={lane} />
    </Suspense>
  );
}

function PublicCampaignInner({ lane }: { lane: "activity" | "contact" }) {
  const params = useParams<{ code: string }>();
  const code = params?.code ?? "";
  const searchParams = useSearchParams();
  const entry = classifyEntry(searchParams.get("entry"));
  const source = beaconChannel(entry, searchParams.get("tenant_id") ?? searchParams.get("tenant"));
  const inSiteTarget = inSiteTargetFromParams((k) => searchParams.get(k));

  const [view, setView] = useState<PublicView | null>(null);
  const [leadForm, setLeadForm] = useState<LeadFormView | null>(null);
  const [uiState, setUiState] = useState<PublicUiState>("loading");
  const [reloadTick, setReloadTick] = useState(0);

  const [name, setName] = useState("");
  const [phone, setPhone] = useState("");
  const [wechat, setWechat] = useState("");
  const [consent, setConsent] = useState(false);
  const [marketing, setMarketing] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState("");
  const [submitted, setSubmitted] = useState<LeadSubmitResult | null>(null);
  const [actionNote, setActionNote] = useState("");
  const [privateNote, setPrivateNote] = useState("");
  const [guestActions, setGuestActions] = useState<GuestCapability[]>([]);
  const [jumpClosed, setJumpClosed] = useState<ClosedNotice[]>([]);
  const [returnHref, setReturnHref] = useState("");
  const [privateDomain, setPrivateDomain] = useState<PrivateDomainGuide>({
    entries: [],
    connected: false,
    redemption: "unknown",
  });

  // ---- HUI-1896 三屏状态:自身参与(受控 visitor 会话)+ 1671 公共奖励面 ----
  const [participation, setParticipation] = useState<CustomerParticipation | null>(null);
  const [attempts, setAttempts] = useState<AttemptRef[]>([]);
  const [rewardSummary, setRewardSummary] = useState<PublicRewardSummary | null>(null);
  const [participationNote, setParticipationNote] = useState("");
  const [participationBusy, setParticipationBusy] = useState(false);
  const lookupSent = useRef(false);

  const [revoking, setRevoking] = useState(false);
  const [revokeDone, setRevokeDone] = useState(false);
  const [revokeError, setRevokeError] = useState("");
  const viewSent = useRef(false);
  const width = usePageWidth();
  const columns = publicVisitorColumns(width);

  useEffect(() => {
    if (!code || entry === "unsupported") return;
    let alive = true;
    fetch(`/api/public/links/${encodeURIComponent(code)}`)
      .then(async (res) => {
        if (!alive) return;
        if (res.status === 502 || res.status === 503 || res.status === 504) {
          setUiState(failureState(res.status));
          return;
        }
        const data = await res.json().catch(() => null);
        if (data && typeof data.state === "string") {
          setView(data as PublicView);
          setUiState(data.state as PublicUiState);
        } else {
          setUiState("not_found");
        }
      })
      .catch(() => {
        if (alive) setUiState(failureState(null));
      });
    return () => {
      alive = false;
    };
  }, [code, entry, reloadTick]);

  useEffect(() => {
    if (!code || uiState !== "available" || viewSent.current) return;
    viewSent.current = true;
    fetch(`/api/public/links/${encodeURIComponent(code)}/view-events`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ channel: source.channel }),
    }).catch(() => undefined);
    fetch(`/api/public/links/${encodeURIComponent(code)}/lead-form`)
      .then(async (res) => {
        if (!res.ok) return null;
        const data = await res.json().catch(() => null);
        if (data && data.enabled) setLeadForm(data as LeadFormView);
      })
      .catch(() => undefined);
  }, [code, source.channel, uiState]);

  // 重复碰触/刷新恢复:一次性 lookup;404=这个浏览器还没有参与(全新开始,
  // 不是错误,不打扰浏览)。网络失败保持静默浏览面,可稍后重试。
  useEffect(() => {
    if (!code || uiState !== "available" || lane !== "activity" || lookupSent.current) return;
    if (view?.capabilities?.participation !== true) return;
    lookupSent.current = true;
    lookupParticipation(code).then((result) => {
      if (result && !("error" in result)) setParticipation(result);
    });
  }, [code, lane, uiState, view?.capabilities?.participation]);

  // 1671 公共奖励面:off/不可达 → null(屏1 不显示编造库存,屏3 只留条件说明)。
  useEffect(() => {
    if (!code || uiState !== "available" || lane !== "activity") return;
    if (view?.capabilities?.reward_ops !== true) return;
    let alive = true;
    fetch(`/api/public/links/${encodeURIComponent(code)}/reward-summary`)
      .then(async (res) => {
        if (!alive) return;
        const data = await res.json().catch(() => null);
        if (alive && res.ok && data) setRewardSummary(data as PublicRewardSummary);
      })
      .catch(() => undefined);
    return () => {
      alive = false;
    };
  }, [code, lane, uiState, view?.capabilities?.reward_ops]);

  useEffect(() => {
    if (!code || uiState !== "available") return;
    let alive = true;
    fetch(`/api/public/links/${encodeURIComponent(code)}/extra-jumps`)
      .then(async (res) => (res.ok ? res.json() : null))
      .then((data) => {
        if (!alive) return;
        setGuestActions(guestActionsFromPayload(data));
        setJumpClosed(closedNotices(data?.closed));
        setReturnHref(authorizedReturnHref(data?.return));
      })
      .catch(() => {
        if (!alive) return;
        setGuestActions([]);
        setJumpClosed([]);
        setReturnHref("");
      });
    fetch(`/api/public/links/${encodeURIComponent(code)}/private-domain?channel=${encodeURIComponent(source.channel)}`)
      .then(async (res) => (res.ok ? res.json() : null))
      .then((data) => {
        if (alive) setPrivateDomain(presentPrivateDomain(data, source.channel));
      })
      .catch(() => {
        if (alive) setPrivateDomain(presentPrivateDomain(null, source.channel));
      });
    return () => {
      alive = false;
    };
  }, [code, source.channel, uiState]);

  const retry = useCallback(() => {
    viewSent.current = false;
    lookupSent.current = false;
    setUiState("loading");
    setReloadTick((t) => t + 1);
  }, []);

  // ---- 三屏动作:开始/换选/取消(唯一显式路径;浏览路径绝不建参与) ----------

  const refreshDetail = useCallback(async (id: string) => {
    try {
      const res = await fetch(`/api/participations/${encodeURIComponent(id)}`);
      const data = await res.json().catch(() => null);
      if (res.ok && data?.id) {
        setParticipation(data as CustomerParticipation);
        setAttempts(Array.isArray(data.attempts) ? (data.attempts as AttemptRef[]) : []);
      }
    } catch {
      // 状态读取失败不打断参与;屏3 的「我已完成」会再次请求核验。
    }
  }, []);

  const canParticipate = view?.capabilities?.participation === true;
  const canReadContent = view?.capabilities?.content_packs === true;

  const handleParticipationStart = useCallback(async () => {
    if (!code || participationBusy) return;
    if (view?.capabilities?.participation !== true) {
      setParticipationNote("这家店还没有开放在线参与。可以按门店现场说明参与，或请店员协助。");
      return;
    }
    setParticipationBusy(true);
    setParticipationNote("");
    // HUI-2628: pin the merchant-adopted version at the moment participation
    // starts. Without this the participation row carried "", and the content
    // panel silently fell back to whatever was current later.
    let version = participation?.content_pack_version ?? "";
    if (!version && canReadContent) {
      try {
        const res = await fetch(`/api/public/links/${encodeURIComponent(code)}/content-choices`);
        const data = await res.json().catch(() => null);
        const items = Array.isArray(data?.items) ? data.items : [];
        version = typeof items[0]?.version_id === "string" ? items[0].version_id : "";
      } catch {
        version = "";
      }
    }
    const result = await startParticipation(code, version);
    if (result && !("error" in result)) {
      setParticipation(result);
      await refreshDetail(result.id ?? "");
    } else if (result && "error" in result && result.error === "start_failed") {
      setParticipationNote(result.message);
    } else if (result) {
      setParticipationNote("网络没有给出明确结果。先当作还没开始；稍后重试即可。");
    }
    setParticipationBusy(false);
  }, [canReadContent, code, participation, participationBusy, refreshDetail, view?.capabilities?.participation]);

  const handleSwitchVersion = useCallback(async (versionId: string) => {
    if (!participation?.id || participationBusy) return;
    setParticipationBusy(true);
    setParticipationNote("");
    const result = await switchDraft(participation.id, versionId);
    if (result && !("error" in result)) {
      setParticipation(result);
    } else if (result && "error" in result && result.error === "frozen") {
      setParticipationNote("这个参与的版本已经交接，不能换选。已交接的内容保持不变。");
    } else {
      setParticipationNote("换选没有完成。稍后再试，或保持当前选择继续。");
    }
    setParticipationBusy(false);
  }, [participation, participationBusy]);

  const handleCancelParticipation = useCallback(async () => {
    if (!participation?.id || participationBusy) return;
    setParticipationBusy(true);
    setParticipationNote("");
    const result = await cancelParticipation(participation.id);
    if (result && !("error" in result)) {
      setParticipation(result);
      setAttempts([]);
    } else {
      setParticipationNote("取消没有完成。已发生的记录不会被抹掉；可以请店员协助。");
    }
    setParticipationBusy(false);
  }, [participation, participationBusy]);

  const disclosure = {
    merchant: leadForm?.recipient?.name || view?.merchant_name || "",
    purpose: leadForm?.purpose || "",
    requiredFields: leadForm?.required_fields ?? [],
    noticeVersion: leadForm?.notice?.version || "",
  };
  const disclosureReady = Boolean(leadForm?.enabled) && leadDisclosureReady(disclosure);
  const leadBlocked = activityBlocks({ marketingOptIn: marketing, consent: consent }).includes("lead");

  const submitLead = useCallback(() => {
    if (!code || !disclosureReady || !leadForm?.notice) return;
    setSubmitError("");
    if (leadBlocked) {
      setSubmitError("请先阅读并同意告知内容。拒绝营销通知不会挡住看活动。");
      return;
    }
    setSubmitting(true);
    fetch(`/api/public/links/${encodeURIComponent(code)}/lead-submissions`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        name,
        phone,
        wechat,
        consent: true,
        consent_version: leadForm.notice.version,
        marketing_optin: marketing,
        channel: source.channel === "web" ? "web" : source.channel,
      }),
    })
      .then(async (res) => {
        const data = (await res.json().catch(() => null)) as LeadSubmitResult | null;
        if (res.ok && data?.submission_ref) {
          setSubmitted(data);
        } else {
          setSubmitError(errorMessage(data) ?? "提交失败,请稍后再试");
        }
      })
      .catch(() => setSubmitError("网络异常,请稍后再试"))
      .finally(() => setSubmitting(false));
  }, [code, disclosureReady, leadBlocked, leadForm, marketing, name, phone, source.channel, wechat]);

  const revokeLead = useCallback(() => {
    if (!code || !submitted?.submission_ref) return;
    setRevokeError("");
    setRevoking(true);
    fetch(`/api/public/links/${encodeURIComponent(code)}/lead-revocations`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ submission_ref: submitted.submission_ref, phone }),
    })
      .then(async (res) => {
        const data = await res.json().catch(() => null);
        if (res.ok && data?.state === "revoked") setRevokeDone(true);
        else setRevokeError(errorMessage(data) ?? "撤销失败,请核对手机号后重试");
      })
      .catch(() => setRevokeError("网络异常,请稍后再试"))
      .finally(() => setRevoking(false));
  }, [code, phone, submitted]);

  const onPrivateDomain = (kind: string, href: string) => {
    const record = privateDomainClick(kind);
    const entry = privateDomain.entries.find((item) => item.kind === kind && item.href === href);
    if (!entry || record.success || record.platformResult !== "unknown" || record.redemption !== "unknown") {
      setPrivateNote("没有打开。这一下也不是添加成功、进群成功、新增联系人或核销。");
      return;
    }
    setPrivateNote("正在打开。这一下只是点击，还不是添加成功。");
    const params = new URLSearchParams({ channel: source.channel });
    fetch(`/api/public/links/${encodeURIComponent(code)}/private-domain/${encodeURIComponent(kind)}/clicks?${params.toString()}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: "{}",
    })
      .then(async (res) => {
        const data = await res.json().catch(() => null);
        const settled = settlePrivateDomainClick(res.ok ? data : null);
        if (!settled.open || settled.event !== entry.event) {
          setPrivateNote("平台结果还是未知。没有把它当成添加成功、进群成功、新增联系人或核销。");
          return;
        }
        setPrivateNote(privateDomainNote(privateDomain, settled));
        window.open(href, "_blank", "noopener,noreferrer");
      })
      .catch(() => {
        setPrivateNote("没有打开。这一下也不是添加成功、进群成功、新增联系人或核销。");
      });
  };

  const onGuestAction = (kind: string, href: string) => {
    const record = actionClickRecord(kind);
    if (!isOfficialActionUrl(href) || record.success || record.platformResult !== "unknown") {
      setActionNote("这个动作没有真实地址，没有打开，也没有记成成功。");
      return;
    }
    setActionNote("正在打开。这一下还不是添加成功、关注成功，也不是留资。");
    fetch(`/api/public/links/${encodeURIComponent(code)}/extra-jumps/${encodeURIComponent(kind)}/clicks`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: "{}",
    })
      .then(async (res) => {
        const data = await res.json().catch(() => null);
        const settled = settleClick(res.ok ? data : null);
        const target = canonicalHref(href, data?.href);
        if (!settled.open || !honestOpen(href, data) || openMode(target) !== "blank") {
          setActionNote("平台结果还是未知。没有把它当成添加成功、关注成功或留资。");
          return;
        }
        setActionNote(openedEvidenceCopy());
        window.open(target, "_blank", "noopener,noreferrer");
      })
      .catch(() => {
        setActionNote("没有打开。这一下也不是添加成功、关注成功或留资。");
      });
  };

  const onReturn = (href: string) => {
    setActionNote("正在打开返回地址。这一下还不是添加成功、关注成功，也不是留资。");
    fetch(`/api/public/links/${encodeURIComponent(code)}/returns/clicks`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: "{}",
    })
      .then(async (res) => {
        const data = await res.json().catch(() => null);
        const target = canonicalHref(href, data?.href);
        const mode = openMode(target);
        if (!honestOpen(href, data) || mode === "refuse") {
          setActionNote("平台结果还是未知。没有把它当成添加成功、关注成功或留资。");
          return;
        }
        setActionNote(openedEvidenceCopy());
        if (mode === "assign") window.location.assign(target);
        else window.open(target, "_blank", "noopener,noreferrer");
      })
      .catch(() => {
        setActionNote("没有打开。这一下也不是添加成功、关注成功或留资。");
      });
  };

  const state: PublicUiState = entry === "unsupported" ? "entry_unsupported" : uiState;
  const headline = STATE_TEXT[state] ?? "活动不存在";
  const action = STATE_ACTION[state] ?? "";
  const showActivity = state === "available" && Boolean(view?.title);
  const visibleActions = activityJumpActions(presentGuestActions(guestActions));
  const merchantLabel = view?.merchant_name || "这家店";
  // 不传确认接收数。客户系统未确认时，文案保持待同步。
  const outcome = submitted
    ? leadOutcomeCopy({
        merchant: submitted.submitted_to?.name || disclosure.merchant || merchantLabel,
        state: submitted.duplicate ? "duplicate" : submitted.state || "accepted",
        duplicate: submitted.duplicate,
        crmReceived: submitted.crm_received,
        salesReceived: "unknown",
        ownerFollowedUp: "unknown",
      })
    : "";
  const nextOutcomeState = leadOutcomeState(revokeDone ? LEAD_REVOKED_COPY : outcome);

  // ---- 三屏派生:阶段与门控 ----
  const stage = journeyStage(participation, {});
  const participationOpen = stage !== "browse";
  const switchable = canSwitchDraft(participation, attempts);
  const stockQty = rewardSummary?.slots?.[0]?.available_qty;
  const evidenceKinds = ["staff_observed_submission", "staff_verified_post", "official_publish_confirmed"];

  const sectionNodes: Record<PublicSection, ReactElement | null> = {
    store: (
      <header key="store" data-testid="store-identity">
        <p className="tk-public-merchant">{merchantLabel}</p>
        {view?.store_name && <p className="tk-public-store">{view.store_name}</p>}
        <h1 className="tk-public-title">{showActivity && view?.title ? view.title : headline}</h1>
      </header>
    ),
    value: showActivity ? (
      <section key="value" data-testid="store-value">
        <p className="tk-public-value">{view?.public_content || "请按门店现场说明参与这次活动。"}</p>
        <p className="tk-note">碰一下、扫码或看视频只是打开这个活动，不会自动留下联系方式。</p>
        {(view?.starts_at || view?.ends_at) && (
          <p className="tk-note">活动时间:{formatCampaignWindow(view?.starts_at, view?.ends_at)}</p>
        )}
        {storeNoticeText(view?.store_notice) && (
          <p className="tk-warn">{storeNoticeText(view?.store_notice)}</p>
        )}
      </section>
    ) : null,
    actions: showActivity && lane === "activity" ? (
      <section key="actions" data-testid="activity-actions">
        {/* 屏1 结构化面板:权益/动作/核验/限额/条件(基线门店头与说明区承载其余) */}
        <ActivityIntro
          evidenceKinds={evidenceKinds}
          availableQty={stockQty}
          benefitReservationState={participation?.benefit_reservation_state}
          canStart={canParticipate && !participationOpen}
          stockExhausted={canParticipate && stockQty === 0}
          closedReason={
            canParticipate
              ? participationOpen
                ? "你已经开始参与。往下看内容与发布。"
                : undefined
              : "这家店还没有开放在线参与。可以按门店现场说明参与，或请店员协助。"
          }
          onParticipationStart={handleParticipationStart}
        />
        {participationNote && <p className="tk-note" data-testid="participation-note">{participationNote}</p>}
        {participationOpen && (
          <div data-testid="customer-journey" className="tk-gap">
            {/* 屏2:内容与发布(内容包未开通时不请求、不展示死区块) */}
            {canReadContent ? (
              <ContentPublish
                code={code}
                selectedVersion={participation?.content_pack_version}
                canSwitch={switchable}
                onSwitchVersion={handleSwitchVersion}
              />
            ) : (
              <section className="tk-section" data-testid="content-unavailable">
                <h2 className="tk-section-title">内容与发布</h2>
                <p className="tk-note">这家店还没有开放在线内容。可以按门店现场说明参与，或请店员协助。</p>
              </section>
            )}
            {/* 1670 交接组件(自足;挂载基线保留) */}
            {canParticipate ? <PublishHandoff code={code} /> : null}
            {/* 屏3:领取或请店员确认 */}
            <RewardStatus code={code} participation={participation} />
            {stage !== "awaiting_review" && stage !== "publishing" && (
              <div className="tk-row">
                <button type="button" className="tk-quiet" disabled={participationBusy} data-testid="participation-cancel" onClick={handleCancelParticipation}>
                  取消参与
                </button>
              </div>
            )}
          </div>
        )}
        {/* PR#15 基线:预览/合规导出(保留挂载) */}
        <CustomerPublish code={code} defaultCopy={view?.public_content ?? ""} />
        {visibleActions.length > 0 && (
          <div className="tk-row tk-gap">
            {visibleActions.map((item) => (
              <button key={item.kind} type="button" className="tk-quiet" data-testid={`extra-jump-${item.kind}`} onClick={() => onGuestAction(item.kind, item.href)}>
                {guestActionLabel(item.kind)}
              </button>
            ))}
          </div>
        )}
        {jumpClosed.length > 0 && (
          <div className="tk-row tk-gap">
            {jumpClosed.map((item) => (
              <button key={`${item.kind}:${item.reason}`} type="button" disabled data-testid={`extra-jump-closed-${item.kind}`} className="tk-quiet">
                {item.text}
              </button>
            ))}
          </div>
        )}
        {returnHref && (
          <div className="tk-gap">
            <button type="button" data-testid="authorized-return" className="tk-quiet" onClick={() => onReturn(returnHref)}>
              返回
            </button>
          </div>
        )}
        <div data-testid="jump-evidence">
          <p className="tk-note">{standingEvidenceCopy()}</p>
        </div>
        <div data-testid="jump-capability-gaps">
          {capabilityGapLines().map((line) => (
            <p key={line} className="tk-note">{line}</p>
          ))}
        </div>
        {actionNote && <p className="tk-note">{actionNote}</p>}
        {privateDomain.entries.length > 0 && (
          <div data-testid="private-domain" className="tk-section">
            <h2 className="tk-section-title">加企微或进社群</h2>
            <p className="tk-note">点一下才会打开。拒绝留资也可以继续看活动。这里不会自动加好友，也不会自动进群。</p>
            <div className="tk-row">
              {privateDomain.entries.map((item) => (
                <button
                  key={item.kind}
                  type="button"
                  className="tk-quiet"
                  data-testid={`private-domain-${item.kind}`}
                  onClick={() => onPrivateDomain(item.kind, item.href)}
                >
                  {privateDomainLabel(item.kind)}
                </button>
              ))}
            </div>
            {privateNote && <p className="tk-note">{privateNote}</p>}
          </div>
        )}
      </section>
    ) : null,
    lead: showActivity && !submitted && disclosureReady ? (
      <section key="lead" data-testid="lead-disclosure" className="tk-gap">
        <h2 className="tk-section-title">把联系方式交给{disclosure.merchant}</h2>
        <p className="tk-note">{disclosure.purpose}</p>
        <p className="tk-note">
          接收方:{disclosure.merchant}。必填:{fieldLabels(disclosure.requiredFields)}。告知版本 {disclosure.noticeVersion}。
        </p>
        <p className="tk-note">{leadForm?.notice?.text}</p>
        <label className="tk-label">
          姓名
          <input className="tk-input" value={name} onChange={(e) => setName(e.target.value)} placeholder="怎么称呼你" />
        </label>
        <label className="tk-label">
          手机号
          <input className="tk-input" value={phone} onChange={(e) => setPhone(e.target.value)} placeholder="商家用来联系你" inputMode="numeric" />
        </label>
        <label className="tk-label">
          微信号(选填)
          <input className="tk-input" value={wechat} onChange={(e) => setWechat(e.target.value)} placeholder="可选" />
        </label>
        <label className="tk-check">
          <input type="checkbox" checked={consent} onChange={(e) => setConsent(e.target.checked)} data-testid="consent" />
          我同意把上面的联系方式交给{disclosure.merchant}，用于这次告知里的用途
        </label>
        {leadForm?.marketing_optin_enabled && (
          <label className="tk-check">
            <input type="checkbox" checked={marketing} onChange={(e) => setMarketing(e.target.checked)} data-testid="marketing-optin" />
            另外同意以后接收活动通知。不勾选也可以继续看活动，也可以只提交这次联系方式
          </label>
        )}
        {submitError && <p className="tk-danger" data-state="error">{submitError}</p>}
        <button type="button" onClick={submitLead} disabled={submitting || leadBlocked} data-testid="lead-submit" className="tk-primary">
          {submitting ? "提交中…" : "提交给这家店"}
        </button>
      </section>
    ) : showActivity && !submitted && lane === "contact" ? (
      <section key="lead" className="tk-state" data-state="empty">这家店还没有打开留资。可以继续看活动。</section>
    ) : null,
    next: submitted ? (
      <section key="next" data-testid="lead-outcome" data-state={nextOutcomeState} data-tone={nextOutcomeState} className={nextOutcomeState === "received" ? "tk-outcome tk-ok" : "tk-outcome"}>
        {revokeDone ? (
          <p className="tk-ok" data-state="revoked">{LEAD_REVOKED_COPY}</p>
        ) : (
          <>
            <p className="tk-lead">{outcome}</p>
            <p className="tk-note">留资编号 {submitted.submission_ref}。可以在本页撤销。</p>
            {revokeError && <p className="tk-danger">{revokeError}</p>}
            <button type="button" onClick={revokeLead} disabled={revoking} className="tk-quiet">
              {revoking ? "撤销中…" : "撤销我的授权"}
            </button>
          </>
        )}
      </section>
    ) : null,
  };

  const visitorNote = publicVisitorCopy({ action: submitted ? "lead" : "browse" });

  return (
    <main data-testid="public-activity" data-pattern="public_consumer" data-columns={columns} data-viewport={width} data-state={state} data-lane={lane} className="tk-public">
      <div className="tk-public-card">
        {state !== "available" && (
          <div data-testid="degraded-state">
            <h1 className="tk-public-title">{headline}</h1>
            {action && <p className="tk-note">{action}</p>}
            {state === "network_error" && (
              <button type="button" onClick={retry} className="tk-quiet">
                重试
              </button>
            )}
            {inSiteTarget && (
              <p className="tk-gap">
                <a href={inSiteTarget}>返回</a>
              </p>
            )}
          </div>
        )}
        {state === "available" && (
          <p data-testid="visitor-no-balance" className="tk-note">{visitorNote.text}</p>
        )}
        {state === "available" && publicSectionOrder().map((section) => sectionNodes[section])}
        {view?.brand_shell?.display_name && (
          <footer className="tk-public-foot">
            技术服务 {view.brand_shell.display_name}
            {view.brand_shell.support_name ? ` · ${view.brand_shell.support_name}` : ""}
            {view.brand_shell.support_contact ? ` · ${view.brand_shell.support_contact}` : ""}
          </footer>
        )}
      </div>
    </main>
  );
}

function usePageWidth(): number {
  const [width, setWidth] = useState(390);
  useEffect(() => {
    const apply = () => setWidth(window.innerWidth || 390);
    apply();
    window.addEventListener("resize", apply);
    return () => window.removeEventListener("resize", apply);
  }, []);
  return width;
}

function fieldLabels(fields: string[]): string {
  return fields
    .map((field) => (field === "name" ? "姓名" : field === "phone" ? "手机号" : field))
    .join("、");
}

function errorMessage(data: unknown): string | null {
  if (data && typeof data === "object" && "message" in (data as Record<string, unknown>)) {
    const message = (data as Record<string, unknown>).message;
    if (typeof message === "string") return message;
  }
  return null;
}
