"use client";

// HUI-3562 店员确认与核销台(/work/service)。
//
// 首页只有三组高频动作:扫参与码/手输、待确认(自报队列)、已发放/已核销记录。
// 纪律:
//   - 店员默认自己门店(whoami 的 store_scope);店经理读取与记录按本店过滤;
//   - 扫码进参与卡(peek 不消费,重复扫码幂等),扫券码进核销卡(by-code);
//     全程不让店员手填数据库 ID;
//   - 相机不可用/权限拒绝/浏览器不支持 → 手输凭据(校验位预检+服务端限流);
//   - 确认/发放/核销全部走 1671 账本端点;网络未知先回查,
//     永不把未知当成功,也绝不让顾客再领一次;前端不写成功状态;
//   - 更正撤销仅 org_owner;不展示经营报表与顾客隐私字段。

import { useCallback, useEffect, useRef, useState } from "react";
import { failureText, useSession } from "@/components/work/merchant-session";
import { benefitStatusText, canConfirmRewards, evidenceText, maskCode } from "@/lib/reward-ops";
import {
  extractHandoffToken,
  looksLikeHandoffToken,
  networkUnknownCopy,
  ownStoreBenefits,
  outcomeNotice,
  redeemRequestBody,
  reverseRequestBody,
  type ParticipationCard as Card,
  type ServiceBatch,
} from "@/lib/service-desk";
import { ParticipationCardView, type ParticipationBenefit } from "./participation-card";
import { RedemptionCardView, type RedemptionBenefit } from "./redemption-card";

interface CampaignItem {
  id: string;
  title?: string;
  store_id?: string;
}

interface ReviewRequestItem {
  attempt_id?: string;
  participation_ref?: string;
  platform?: string;
  created_at?: string;
}

interface BenefitItem extends RedemptionBenefit {
  benefit_slot?: string;
  rule_version?: number;
  face_value_minor?: number;
  cost_minor?: number;
  currency?: string;
}

type BarcodeDetectorLike = { detect: (video: HTMLVideoElement) => Promise<Array<{ rawValue: string }>> };
type BarcodeDetectorCtor = new (options: { formats?: string[] }) => BarcodeDetectorLike;

function barcodeDetectorCtor(): BarcodeDetectorCtor | null {
  if (typeof window === "undefined") return null;
  return (window as unknown as { BarcodeDetector?: BarcodeDetectorCtor }).BarcodeDetector ?? null;
}

function rateLimitText(status: number, data: Record<string, unknown>): string {
  if (status === 429) return "尝试太频繁，请稍等一会再试。";
  return failureText(status, data);
}

export function ServiceDesk() {
  const session = useSession();
  const [phase, setPhase] = useState<"loading" | "error" | "ready">("loading");
  const [loadError, setLoadError] = useState("");
  const [attempt, setAttempt] = useState(0);

  const [storeScope, setStoreScope] = useState("");
  const [role, setRole] = useState("");
  const [campaigns, setCampaigns] = useState<CampaignItem[]>([]);
  const [campaignId, setCampaignId] = useState("");
  const [reviewQueue, setReviewQueue] = useState<ReviewRequestItem[]>([]);
  const [benefits, setBenefits] = useState<BenefitItem[]>([]);

  const [scanText, setScanText] = useState("");
  const [couponText, setCouponText] = useState("");
  const [scanNote, setScanNote] = useState("");
  const [actionNote, setActionNote] = useState("");
  const [cameraMode, setCameraMode] = useState<"idle" | "camera" | "manual">("idle");

  const [card, setCard] = useState<Card | null>(null);
  const [cardBatches, setCardBatches] = useState<ServiceBatch[]>([]);
  const [campaignTitle, setCampaignTitle] = useState("");
  const [redemption, setRedemption] = useState<BenefitItem | null>(null);
  const [busy, setBusy] = useState(false);

  const videoRef = useRef<HTMLVideoElement | null>(null);
  const streamRef = useRef<MediaStream | null>(null);
  const scanningRef = useRef(false);

  const stopCamera = useCallback(() => {
    scanningRef.current = false;
    streamRef.current?.getTracks().forEach((track) => track.stop());
    streamRef.current = null;
  }, []);

  useEffect(() => stopCamera, [stopCamera]);

  const loadRecords = useCallback(
    async (cid: string): Promise<BenefitItem[]> => {
      if (!cid) return [];
      const [revRes, benRes] = await Promise.all([
        session.api("GET", `campaigns/${cid}/reward-review-requests`),
        session.api("GET", `campaigns/${cid}/reward-benefits`),
      ]);
      const freshBenefits = benRes.ok && Array.isArray(benRes.data.items) ? (benRes.data.items as BenefitItem[]) : [];
      setReviewQueue(revRes.ok && Array.isArray(revRes.data.items) ? (revRes.data.items as ReviewRequestItem[]) : []);
      setBenefits(freshBenefits);
      return freshBenefits;
    },
    [session],
  );

  // 会话与门店作用域:店员默认自己门店;店经理只进本店活动(服务端已过滤)。
  useEffect(() => {
    let alive = true;
    void (async () => {
      const [whoRes, campRes] = await Promise.all([
        session.api("GET", "whoami"),
        session.api("GET", "campaigns"),
      ]);
      if (!alive) return;
      if (!campRes.ok) {
        setLoadError(failureText(campRes.status, campRes.data));
        setPhase("error");
        return;
      }
      if (whoRes.ok) {
        setStoreScope(typeof whoRes.data.store_scope === "string" ? whoRes.data.store_scope : "");
        setRole(typeof whoRes.data.role === "string" ? whoRes.data.role : session.role);
      }
      const items = Array.isArray(campRes.data.items) ? (campRes.data.items as CampaignItem[]) : [];
      setCampaigns(items);
      setCampaignId(items[0]?.id ?? "");
      setPhase("ready");
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
    void loadRecords(campaignId)
      .then(() => {
        if (alive) setPhase("ready");
      })
      .catch(() => {
        if (alive) setActionNote("记录没有读到。不把它当成已处理。");
      });
    return () => {
      alive = false;
    };
  }, [campaignId, loadRecords]);

  const reloadCardContext = useCallback(
    async (cid: string) => {
      const [invRes, benRes, campRes] = await Promise.all([
        session.api("GET", `campaigns/${cid}/reward-inventory`),
        session.api("GET", `campaigns/${cid}/reward-benefits`),
        session.api("GET", `campaigns/${cid}`),
      ]);
      setCardBatches(invRes.ok && Array.isArray(invRes.data.items) ? (invRes.data.items as ServiceBatch[]) : []);
      setBenefits(benRes.ok && Array.isArray(benRes.data.items) ? (benRes.data.items as BenefitItem[]) : []);
      if (campRes.ok && typeof campRes.data.title === "string") setCampaignTitle(campRes.data.title);
    },
    [session],
  );

  const cardBenefit: ParticipationBenefit | undefined = card
    ? benefits.find((b) => b.participation_id === card.id)
    : undefined;

  const startCamera = useCallback(async () => {
    const ctor = barcodeDetectorCtor();
    if (!ctor) {
      setCameraMode("manual");
      setScanNote("此浏览器不支持扫码，请手输参与凭据。");
      return;
    }
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ video: { facingMode: "environment" } });
      streamRef.current = stream;
      setCameraMode("camera");
      setScanNote("对准顾客出示的参与二维码。");
      scanningRef.current = true;
      const waitVideo = () =>
        new Promise<HTMLVideoElement | null>((resolve) => {
          let tries = 0;
          const probe = () => {
            if (videoRef.current && videoRef.current.readyState >= 2) return resolve(videoRef.current);
            if (tries++ > 100) return resolve(null);
            window.requestAnimationFrame(probe);
          };
          probe();
        });
      const video = await waitVideo();
      if (!video) {
        stopCamera();
        setCameraMode("manual");
        setScanNote("相机画面没有就绪，请手输参与凭据。");
        return;
      }
      video.srcObject = stream;
      await video.play().catch(() => undefined);
      const detector = new ctor({ formats: ["qr_code"] });
      const tick = () => {
        if (!scanningRef.current) return;
        void detector
          .detect(video)
          .then((codes) => {
            if (codes.length > 0 && scanningRef.current) {
              scanningRef.current = false;
              stopCamera();
              setCameraMode("idle");
              setScanText(codes[0].rawValue);
              setScanNote("已读到参与码。");
            } else if (scanningRef.current) {
              window.requestAnimationFrame(tick);
            }
          })
          .catch(() => {
            if (scanningRef.current) window.requestAnimationFrame(tick);
          });
      };
      window.requestAnimationFrame(tick);
    } catch {
      stopCamera();
      setCameraMode("manual");
      setScanNote("相机不可用或权限被拒绝，请手输参与凭据。");
    }
  }, [stopCamera]);

  // 扫参与码 → 参与卡。peek 不消费凭据:重复扫码幂等回同一张卡。
  const openParticipation = useCallback(
    async (raw: string) => {
      const token = extractHandoffToken(raw);
      if (!token || !looksLikeHandoffToken(token)) {
        setScanNote("没有识别出参与凭据。请扫顾客出示的参与二维码，或让顾客报出完整凭据后核对。");
        return;
      }
      setBusy(true);
      try {
        const res = await session.api("POST", "participations/staff/peek", { token });
        if (res.ok && typeof res.data.id === "string") {
          const opened = res.data as unknown as Card;
          setCard(opened);
          setRedemption(null);
          setScanNote("参与卡已打开。重复扫码会回到同一张卡。");
          await reloadCardContext(opened.campaign_id);
        } else {
          setScanNote(rateLimitText(res.status, res.data));
        }
      } catch {
        setScanNote("没有连上服务。没有打开任何参与卡，请检查网络后重试。");
      } finally {
        setBusy(false);
      }
    },
    [session, reloadCardContext],
  );

  // 扫券码 → 核销卡。核销动作必须打回券所属活动(campaign_id 来自权益行)。
  const openCoupon = useCallback(
    async (raw: string) => {
      const code = raw.trim();
      if (!code) {
        setScanNote("请输入或扫描券码。");
        return;
      }
      setBusy(true);
      try {
        const res = await session.api("GET", `reward-benefits/by-code?code=${encodeURIComponent(code)}`);
        if (res.ok && typeof res.data.id === "string") {
          const benefit = res.data as unknown as BenefitItem;
          setRedemption(benefit);
          setCard(null);
          setScanNote("核销卡已打开。");
          if (benefit.campaign_id) await loadRecords(benefit.campaign_id);
        } else {
          setScanNote(rateLimitText(res.status, res.data));
        }
      } catch {
        setScanNote("没有连上服务。没有打开核销卡，请检查网络后重试。");
      } finally {
        setBusy(false);
      }
    },
    [session, loadRecords],
  );

  // 主动作:确认并发奖 / 确认并现场兑现。网络未知先回查(回查用回读到的
  // 新列表判断,不用渲染闭包里的旧列表),不提示顾客重领。
  const confirmCard = useCallback(
    async (body: Record<string, unknown>, _evidence: string) => {
      if (!card) return;
      const cid = card.campaign_id;
      const participationId = card.id;
      setBusy(true);
      try {
        const res = await session.api("POST", `campaigns/${cid}/reward-confirm`, body);
        if (res.ok) {
          const notice = outcomeNotice(String(res.data.outcome ?? ""), Boolean(res.data.applied), Boolean(res.data.replay));
          setActionNote(notice.text);
        } else {
          setActionNote(rateLimitText(res.status, res.data));
        }
        await reloadCardContext(cid);
      } catch {
        let found = false;
        try {
          const fresh = await loadRecords(cid);
          found = fresh.some((b) => b.participation_id === participationId);
        } catch {
          found = false;
        }
        setActionNote(networkUnknownCopy(found));
      } finally {
        setBusy(false);
      }
    },
    [card, session, reloadCardContext, loadRecords],
  );

  const notFit = useCallback(
    (reason: string) => {
      setCard(null);
      setActionNote(`暂不符合（${reason}）。未发放任何奖励；顾客补做后可再次出示参与码。`);
    },
    [],
  );

  // 独立核销入口:1671 单事务,另一端读到已处理。活动取权益自己的
  // campaign_id(扫券码进来的卡可能不属于当前选中的活动)。
  const redeemBenefit = useCallback(
    async (benefit: RedemptionBenefit, storeId: string) => {
      const cid = benefit.campaign_id || campaignId;
      if (!cid) return;
      setBusy(true);
      try {
        const res = await session.api("POST", `campaigns/${cid}/reward-benefits/${benefit.id}/redeem`, redeemRequestBody(storeId || storeScope));
        if (res.ok) {
          const outcome = String(res.data.outcome ?? "");
          setActionNote(
            outcome === "redeemed"
              ? "核销完成（一条事实）。"
              : "这个权益已核销过：回读已处理结果，没有第二份。",
          );
          if (res.data.benefit) setRedemption(res.data.benefit as BenefitItem);
        } else {
          setActionNote(rateLimitText(res.status, res.data));
        }
        await loadRecords(cid);
      } catch {
        let found = false;
        try {
          const fresh = await loadRecords(cid);
          found = fresh.some((b) => b.id === benefit.id && b.status === "redeemed");
        } catch {
          found = false;
        }
        setActionNote(networkUnknownCopy(found));
      } finally {
        setBusy(false);
      }
    },
    [campaignId, session, storeScope, loadRecords],
  );

  const reverseBenefit = useCallback(
    async (benefit: RedemptionBenefit, reason: string) => {
      const cid = benefit.campaign_id || campaignId;
      if (!cid) return;
      setBusy(true);
      try {
        const res = await session.api("POST", `campaigns/${cid}/reward-benefits/${benefit.id}/reverse`, reverseRequestBody(reason));
        if (res.ok) {
          setActionNote("已更正撤销（追加更正记录，原发放事实保留）。");
          if (res.data.benefit) setRedemption(res.data.benefit as BenefitItem);
        } else {
          setActionNote(rateLimitText(res.status, res.data));
        }
        await loadRecords(cid);
      } catch {
        setActionNote("撤销没有完成。没有事实不写成功，可以再试一次。");
      } finally {
        setBusy(false);
      }
    },
    [campaignId, session, loadRecords],
  );

  const visibleBenefits = ownStoreBenefits(benefits, storeScope);
  const confirmAllowed = canConfirmRewards(role || session.role);
  const reverseAllowed = (role || session.role) === "org_owner";

  return (
    <main>
      <h1 className="tk-title">确认与核销</h1>
      <p className="tk-lead">
        {storeScope ? "你正在为本店服务。" : "你正在为全门店服务。"}
        扫顾客出示的参与码核对后确认；扫券码核销。确认与核销都记进奖励账本。
      </p>

      {phase === "loading" ? <p className="tk-state" data-state="loading">正在读取…</p> : null}
      {phase === "error" ? (
        <p className="tk-state tk-danger" data-state="error">
          {loadError || "没有读到门店数据。"}
          <button className="tk-quiet" type="button" data-action="retry" onClick={() => setAttempt((a) => a + 1)}>重试</button>
        </p>
      ) : null}

      {phase === "ready" ? (
        <>
          <section aria-label="扫参与码" data-section="scan">
            <h2 className="tk-subtitle">扫参与码</h2>
            {campaigns.length > 1 ? (
              <label className="tk-label">
                活动
                <select className="tk-select" data-field="campaign-select" value={campaignId} onChange={(event) => setCampaignId(event.target.value)}>
                  {campaigns.map((item) => (
                    <option key={item.id} value={item.id}>{item.title || item.id}</option>
                  ))}
                </select>
              </label>
            ) : null}
            {cameraMode === "camera" ? (
              <div data-field="camera-view">
                <video ref={videoRef} playsInline muted data-field="scan-video" className="tk-video" />
                <button
                  className="tk-quiet"
                  type="button"
                  data-action="stop-camera"
                  onClick={() => {
                    stopCamera();
                    setCameraMode("idle");
                  }}
                >
                  停止扫码
                </button>
              </div>
            ) : (
              <button className="tk-button" type="button" data-action="start-camera" disabled={busy} onClick={() => void startCamera()}>
                扫参与二维码
              </button>
            )}
            <div data-field="manual-entry">
              <label className="tk-label">
                手输参与凭据（相机不可用时）
                <input
                  className="tk-input"
                  data-field="scan-input"
                  value={scanText}
                  onChange={(event) => setScanText(event.target.value)}
                  placeholder="ptx_… 或 https://…/p/ptx_…"
                  autoComplete="off"
                  inputMode="text"
                />
              </label>
              <button className="tk-quiet" type="button" data-action="open-participation" disabled={busy} onClick={() => void openParticipation(scanText)}>
                打开参与卡
              </button>
            </div>
            {scanNote ? <p className="tk-note" data-field="scan-note">{scanNote}</p> : null}
          </section>

          {card ? (
            <ParticipationCardView
              card={card}
              campaignTitle={campaignTitle || "活动"}
              batches={cardBatches}
              benefit={cardBenefit}
              canConfirm={confirmAllowed}
              busy={busy}
              onConfirm={(body, evidence) => void confirmCard(body, evidence)}
              onNotFit={notFit}
              onClose={() => setCard(null)}
            />
          ) : null}

          <section aria-label="扫券码核销" data-section="coupon">
            <h2 className="tk-subtitle">扫券码核销</h2>
            <label className="tk-label">
              券码
              <input
                className="tk-input"
                data-field="coupon-input"
                value={couponText}
                onChange={(event) => setCouponText(event.target.value)}
                autoComplete="off"
              />
            </label>
            <button className="tk-quiet" type="button" data-action="open-coupon" disabled={busy} onClick={() => void openCoupon(couponText)}>
              打开核销卡
            </button>
          </section>

          {redemption ? (
            <RedemptionCardView
              benefit={redemption}
              reverseAllowed={reverseAllowed}
              busy={busy}
              onRedeem={(storeId) => void redeemBenefit(redemption, storeId)}
              onReverse={(reason) => void reverseBenefit(redemption, reason)}
              onClose={() => setRedemption(null)}
            />
          ) : null}

          <section aria-label="待确认" data-section="review">
            <h2 className="tk-subtitle">待确认（顾客自报）</h2>
            {reviewQueue.length === 0 ? (
              <p className="tk-note" data-field="review-empty">没有待核验的自报。空不是已处理。</p>
            ) : (
              <ul className="tk-list" data-field="review-list">
                {reviewQueue.map((item) => (
                  <li key={item.attempt_id}>
                    自报 {item.platform} · 参与引用 {item.participation_ref}
                    <span className="tk-note">（自报不是官方回执；请让顾客出示参与码，核对后在参与卡里确认）</span>
                  </li>
                ))}
              </ul>
            )}
          </section>

          <section aria-label="已发放与已核销记录" data-section="records">
            <h2 className="tk-subtitle">已发放 / 已核销记录</h2>
            {visibleBenefits.length === 0 ? (
              <p className="tk-note" data-field="benefits-empty">
                {storeScope ? "本店还没有权益记录。没有事实不写已发放。" : "还没有权益记录。没有事实不写已发放。"}
              </p>
            ) : (
              <ul className="tk-list" data-field="benefit-list">
                {visibleBenefits.map((ben) => (
                  <li key={ben.id} data-benefit-id={ben.id} data-benefit-status={ben.status}>
                    <span data-field="benefit-line">
                      {benefitStatusText(ben.status)} · {evidenceText(ben.evidence)} · 券码 {maskCode(ben.code ?? "")}
                      {ben.store_id ? ` · 门店 ${ben.store_id}` : ""}
                    </span>
                    <button
                      className="tk-quiet"
                      type="button"
                      data-action="open-record"
                      onClick={() => {
                        setRedemption(ben);
                        setCard(null);
                      }}
                    >
                      打开核销卡
                    </button>
                  </li>
                ))}
              </ul>
            )}
            {reverseAllowed ? (
              <p className="tk-note" data-field="reverse-note">更正撤销在核销卡里，仅总部角色可用，且必须注明原因。</p>
            ) : null}
          </section>

          {actionNote ? (
            <p className="tk-note" data-field="action-note" data-state={actionNote.includes("完成") || actionNote.includes("已处理") ? "ok" : "warn"}>
              {actionNote}
            </p>
          ) : null}
        </>
      ) : null}
    </main>
  );
}
