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
  resolveServiceCredential,
  serviceReturnPath,
  stableServiceOperation,
  serviceMutationNeedsRecovery,
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

interface ServiceHistoryRow {
  id: string;
  op: string;
  benefit_id: string;
  actor: string;
  reason: string;
  created_at: string;
}

function opText(op: string): string {
  switch (op) {
    case "reserve": return "预留";
    case "issue": return "发放";
    case "redeem": return "核销";
    case "reverse": return "更正撤销";
    case "expire": return "到期";
    default: return op;
  }
}

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
  const [tab, setTab] = useState<"serve" | "records" | "history">("serve");
  const [stores, setStores] = useState<Array<{ id: string; name?: string }>>([]);
  const [historyStoreId, setHistoryStoreId] = useState("");
  const [returnPath, setReturnPath] = useState<string | null>(null);
  const [scanNote, setScanNote] = useState("");
  // HUI-3562 S2:本店处理历史(台账投影,只读;交班统计=当日按操作者聚合)。
  const [history, setHistory] = useState<ServiceHistoryRow[]>([]);
  const [historyLoadedAt, setHistoryLoadedAt] = useState("");
  const [historyState, setHistoryState] = useState<"idle" | "loading" | "ready" | "error">("idle");
  const historyRequest = useRef(0);
  const [actionNote, setActionNote] = useState("");
  const [cameraMode, setCameraMode] = useState<"idle" | "camera" | "manual">("idle");

  const [card, setCard] = useState<Card | null>(null);
  const [cardBatches, setCardBatches] = useState<ServiceBatch[]>([]);
  const [campaignTitle, setCampaignTitle] = useState("");
  const [redemption, setRedemption] = useState<BenefitItem | null>(null);
  const [busy, setBusy] = useState(false);
  const [pendingConfirmation, setPendingConfirmation] = useState<Record<string, unknown> | null>(null);
  const [historyError, setHistoryError] = useState("");

  const videoRef = useRef<HTMLVideoElement | null>(null);
  const streamRef = useRef<MediaStream | null>(null);
  const scanningRef = useRef(false);
  const busyRef = useRef(false);
  const operationBodies = useRef(new Map<string, Record<string, unknown>>());
  const setOperationBusy = (value: boolean) => { busyRef.current = value; setBusy(value); };

  const stopCamera = useCallback(() => {
    scanningRef.current = false;
    streamRef.current?.getTracks().forEach((track) => track.stop());
    streamRef.current = null;
  }, []);

  useEffect(() => stopCamera, [stopCamera]);

  useEffect(() => {
    if (tab !== "serve") { stopCamera(); setCameraMode("idle"); }
  }, [tab, stopCamera]);

  const loadRecords = useCallback(
    async (cid: string): Promise<BenefitItem[]> => {
      if (!cid) return [];
      const [revRes, benRes] = await Promise.all([
        session.api("GET", `campaigns/${cid}/reward-review-requests`),
        session.api("GET", `campaigns/${cid}/reward-benefits`),
      ]);
      if (!benRes.ok) throw new Error(failureText(benRes.status, benRes.data));
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
      const [whoRes, campRes, storesRes] = await Promise.all([
        session.api("GET", "whoami"),
        session.api("GET", "campaigns"),
        session.api("GET", "stores"),
      ]);
      if (!alive) return;
      if (!campRes.ok) {
        setLoadError(failureText(campRes.status, campRes.data));
        setPhase("error");
        return;
      }
      if (whoRes.ok) {
        const scope = typeof whoRes.data.store_scope === "string" ? whoRes.data.store_scope : "";
        setStoreScope(scope);
        setHistoryStoreId(scope);
        setRole(typeof whoRes.data.role === "string" ? whoRes.data.role : session.role);
      }
      const items = Array.isArray(campRes.data.items) ? (campRes.data.items as CampaignItem[]) : [];
      setCampaigns(items);
      const query = new URLSearchParams(window.location.search);
      const requestedCampaign = query.get("campaign") ?? "";
      setReturnPath(serviceReturnPath(query.get("return")));
      setStores(storesRes.ok ? ((storesRes.data.stores ?? storesRes.data.items ?? []) as Array<{ id: string; name?: string }>) : []);
      if (requestedCampaign && query.get("benefit")) {
        // M9 交接只读原有授权列表。URL 中的引用从不充当授权，也不触发办理。
        const records = await loadRecords(requestedCampaign);
        if (!alive) return;
        const original = records.find((b) => b.id === query.get("benefit"));
        if (original) {
          setRedemption(original);
          setCampaignId(requestedCampaign);
          setScanNote("已打开原权益记录。请核对顾客凭证后明确操作；打开本页不会发放或核销。");
        } else setScanNote("没有读到此权益，或当前账号无权访问。请核对顾客出示的凭证。");
      }
      setPhase("ready");
    })().catch(() => {
      if (alive) setPhase("error");
    });
    return () => {
      alive = false;
    };
  }, [session, attempt, loadRecords]);

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

  const loadHistory = useCallback(async () => {
    const request = ++historyRequest.current;
    const scope = storeScope || historyStoreId;
    setHistory([]); setHistoryLoadedAt(""); setHistoryError("");
    if (!scope) { setHistoryState("idle"); return; }
    setHistoryState("loading");
    const res = await session.api("GET", `stores/${scope}/service-history?limit=200`).catch(() => null);
    if (request !== historyRequest.current) return;
    if (res && res.ok && Array.isArray(res.data.items)) {
      setHistoryError("");
      setHistory(res.data.items as ServiceHistoryRow[]);
      setHistoryLoadedAt(new Date().toLocaleTimeString("zh-CN", { hour12: false }));
      setHistoryState("ready");
    } else { setHistoryError("处理历史暂未读到。请重查，未读到不能当作没有处理。"); setHistoryState("error"); }
  }, [session, storeScope, historyStoreId]);

  useEffect(() => {
    void loadHistory().catch(() => {});
    return () => { historyRequest.current++; };
  }, [loadHistory]);

  const reloadCardContext = useCallback(
    async (cid: string) => {
      const [invRes, benRes, campRes] = await Promise.all([
        session.api("GET", `campaigns/${cid}/reward-inventory`),
        session.api("GET", `campaigns/${cid}/reward-benefits`),
        session.api("GET", `campaigns/${cid}`),
      ]);
      if (!invRes.ok || !benRes.ok || !campRes.ok) throw new Error("参与上下文没有完整读到，请重试。");
      setCardBatches(invRes.ok && Array.isArray(invRes.data.items) ? (invRes.data.items as ServiceBatch[]) : []);
      setBenefits(benRes.ok && Array.isArray(benRes.data.items) ? (benRes.data.items as BenefitItem[]) : []);
      if (typeof campRes.data.title === "string") setCampaignTitle(campRes.data.title);
    },
    [session],
  );

  const cardBenefit: ParticipationBenefit | undefined = card
    ? benefits.find((b) => b.participation_id === card.id)
    : undefined;

  // 顾客核验凭据、短时展示码和旧券码共用入口；解析只读。
  const openCredential = useCallback(async (raw: string) => {
    if (busyRef.current) return;
    setOperationBusy(true);
    setCard(null);
    setRedemption(null);
    setActionNote("");
    setPendingConfirmation(null);
    try {
      const { kind, response: res } = await resolveServiceCredential(session.api, raw);
      if (kind === "invalid") {
        setScanNote("没有识别出顾客凭证。请输入完整核验凭据、展示码或券码，不需要内部编号。");
      } else if (res.ok && typeof res.data.id === "string") {
        if (kind === "participation") {
          const opened = res.data as unknown as Card;
          await reloadCardContext(opened.campaign_id);
          setCard(opened);
          setPendingConfirmation(operationBodies.current.get(`confirm:${opened.id}`) ?? null);
          setCampaignId(opened.campaign_id);
          setScanNote("已识别本次参与。请核对冻结条件后明确确认；扫描本身不会发放。");
        } else {
          const benefit = res.data as unknown as BenefitItem;
          setRedemption(benefit);
          if (benefit.campaign_id) { setCampaignId(benefit.campaign_id); await loadRecords(benefit.campaign_id); }
          setScanNote("已识别原权益。核销需要下面明确确认，扫描本身不会核销。");
        }
      } else if (res.status === 410 && kind === "participation") {
        setScanNote("参与核验凭据已到期，请顾客回原参与页重新出示核验凭据；仍办理原参与。");
      } else if (res.status === 404) {
        setScanNote("没有读到这个顾客凭证：可能无效、已到期或不在你的门店权限范围。请核对完整凭证，或请顾客回原权益页刷新展示码，仍处理同一张券。");
      } else if (res.status === 403) {
        setScanNote("当前账号无权办理这个凭证。请核对所属门店，或由该门店已授权的店员处理。");
      } else {
        setScanNote(rateLimitText(res.status, res.data));
      }
    } catch {
      setScanNote("没有连上服务，尚未打开凭证。请检查网络后重试。");
    } finally { setOperationBusy(false); }
  }, [session, reloadCardContext, loadRecords]);

  const startCamera = useCallback(async () => {
    const ctor = barcodeDetectorCtor();
    if (!ctor) {
      setCameraMode("manual");
      setScanNote("此浏览器不支持扫码，请手输顾客凭证。");
      return;
    }
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ video: { facingMode: "environment" } });
      streamRef.current = stream;
      setCameraMode("camera");
      setScanNote("对准顾客出示的核验凭据或券码。");
      scanningRef.current = true;
      const waitVideo = () =>
        new Promise<HTMLVideoElement | null>((resolve) => {
          let tries = 0;
          const probe = () => {
            if (videoRef.current) return resolve(videoRef.current);
            if (tries++ > 100) return resolve(null);
            window.requestAnimationFrame(probe);
          };
          probe();
        });
      const video = await waitVideo();
      if (!video) {
        stopCamera();
        setCameraMode("manual");
        setScanNote("相机画面没有就绪，请手输顾客凭证。");
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
              void openCredential(codes[0].rawValue);
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
      setScanNote("相机不可用或权限被拒绝，请手输顾客凭证。");
    }
  }, [stopCamera, openCredential]);

  // 主动作:确认并发奖 / 确认并现场兑现。网络未知先回查(回查用回读到的
  // 新列表判断,不用渲染闭包里的旧列表),不提示顾客重领。
  const confirmCard = useCallback(
    async (body: Record<string, unknown>, _evidence: string) => {
      if (!card || busyRef.current) return;
      const cid = card.campaign_id;
      const participationId = card.id;
      setOperationBusy(true);
      try {
        const originalBody = stableServiceOperation(operationBodies.current, `confirm:${participationId}`, () => body);
        setPendingConfirmation(originalBody);
        const res = await session.api("POST", `campaigns/${cid}/reward-confirm`, originalBody);
        if (serviceMutationNeedsRecovery(res)) throw new Error("confirmation result unknown");
        setPendingConfirmation(null);
        if (res.ok) {
          const notice = outcomeNotice(String(res.data.outcome ?? ""), Boolean(res.data.applied), Boolean(res.data.replay));
          setActionNote(notice.text);
          operationBodies.current.delete(`confirm:${participationId}`);
        } else {
          setActionNote(rateLimitText(res.status, res.data));
          // 明确拒绝不留未知操作：下一次核对可以使用修正后的证据。
          operationBodies.current.delete(`confirm:${participationId}`);
        }
        await reloadCardContext(cid);
        void loadHistory();
      } catch {
        let found = false;
        try {
          const fresh = await loadRecords(cid);
          found = fresh.some((b) => b.participation_id === participationId && ["issued", "redeemed", "reversed"].includes(b.status));
          if (found) setPendingConfirmation(null);
        } catch {
          setActionNote("刚才操作结果未知，回查也未完成。请保留本次记录，恢复网络后用同一次操作重试查询。");
          return;
        }
        setActionNote(found ? networkUnknownCopy(true) : "已回查原参与，尚未读到发放事实。请使用下方同一次确认重试；预留不算发放。");
      } finally {
        setOperationBusy(false);
      }
    },
    [card, session, reloadCardContext, loadRecords, loadHistory],
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
      const cid = benefit.campaign_id;
      if (!cid) return;
      if (busyRef.current) return;
      setOperationBusy(true);
      try {
        const res = await session.api("POST", `campaigns/${cid}/reward-benefits/${benefit.id}/redeem`, stableServiceOperation(operationBodies.current, `redeem:${benefit.id}`, () => redeemRequestBody(storeId)));
        if (serviceMutationNeedsRecovery(res)) throw new Error("redemption result unknown");
        if (res.ok) {
          const outcome = String(res.data.outcome ?? "");
          setActionNote(
            outcome === "redeemed"
              ? "核销完成（一条事实）。"
              : "这个权益已核销过：回读已处理结果，没有第二份。",
          );
          if (res.data.benefit) setRedemption(res.data.benefit as BenefitItem);
          operationBodies.current.delete(`redeem:${benefit.id}`);
        } else {
          setActionNote(rateLimitText(res.status, res.data));
        }
        await loadRecords(cid);
        void loadHistory();
      } catch {
        let found = false;
        try {
          const fresh = await loadRecords(cid);
          const original = fresh.find((b) => b.id === benefit.id);
          found = original?.status === "redeemed";
          if (original) setRedemption(original);
        } catch {
          setActionNote("刚才操作结果未知，回查也未完成。请保留本次记录，恢复网络后用同一次操作重试查询。");
          return;
        }
        setActionNote(networkUnknownCopy(found));
      } finally {
        setOperationBusy(false);
      }
    },
    [session, loadRecords, loadHistory],
  );

  const reverseBenefit = useCallback(
    async (benefit: RedemptionBenefit, reason: string) => {
      const cid = benefit.campaign_id;
      if (!cid) return;
      if (busyRef.current) return;
      setOperationBusy(true);
      try {
        const res = await session.api("POST", `campaigns/${cid}/reward-benefits/${benefit.id}/reverse`, stableServiceOperation(operationBodies.current, `reverse:${benefit.id}`, () => reverseRequestBody(reason)));
        if (serviceMutationNeedsRecovery(res)) throw new Error("reversal result unknown");
        if (res.ok) {
          setActionNote("已更正撤销（追加更正记录，原发放事实保留）。");
          if (res.data.benefit) setRedemption(res.data.benefit as BenefitItem);
        } else {
          setActionNote(rateLimitText(res.status, res.data));
        }
        await loadRecords(cid);
        void loadHistory();
      } catch {
        try {
          const original = (await loadRecords(cid)).find((b) => b.id === benefit.id);
          if (original) setRedemption(original);
          setActionNote(original?.status === "reversed" ? "回查原权益：已更正撤销，原发放与核销事实保留。" : "更正结果尚待确认。请保留同一记录，使用同一次操作回查重试。");
        } catch { setActionNote("更正结果未知，回查未完成。请恢复网络后回到同一记录继续查询。"); }
      } finally {
        setOperationBusy(false);
      }
    },
    [session, loadRecords, loadHistory],
  );

  const visibleBenefits = ownStoreBenefits(benefits, storeScope);
  const confirmAllowed = canConfirmRewards(role || session.role);
  const reverseAllowed = (role || session.role) === "org_owner";

  return (
    <main>
      <h1 className="tk-title">确认与核销</h1>
      <p className="tk-lead">
        {storeScope ? "你正在为本店服务。" : "你正在为全门店服务。"}
        扫描或输入顾客凭证，自动识别本店参与与权益；核对后明确办理，发放与核销分别留有记录。
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
          {returnPath ? <p><a className="tk-quiet" href={returnPath}>返回原权益记录</a></p> : null}
          <nav className="tk-gap" aria-label="服务台页签">
            <button className="tk-quiet" type="button" aria-current={tab === "serve" ? "page" : undefined} onClick={() => setTab("serve")}>办理</button>
            <button className="tk-quiet" type="button" aria-current={tab === "records" ? "page" : undefined} onClick={() => setTab("records")}>待确认与权益记录</button>
            <button className="tk-quiet" type="button" aria-current={tab === "history" ? "page" : undefined} onClick={() => setTab("history")}>历史与交班</button>
          </nav>
          <div hidden={tab !== "serve"}>
          <section aria-label="扫描顾客凭证" data-section="scan">
            <h2 className="tk-subtitle">扫描顾客凭证</h2>
            <p className="tk-note">无需预选活动。参与核验凭据、权益展示码和旧券码都在这里办理；识别后不会自动发放或核销。</p>
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
                扫描顾客二维码
              </button>
            )}
            <div data-field="manual-entry">
              <label className="tk-label">
                手输顾客凭证（相机不可用时）
                <input
                  className="tk-input"
                  data-field="scan-input"
                  value={scanText}
                  onChange={(event) => setScanText(event.target.value)}
                  placeholder="核验凭据、展示码或券码"
                  autoComplete="off"
                  inputMode="text"
                />
              </label>
              <button className="tk-quiet" type="button" data-action="open-credential" disabled={busy} onClick={() => void openCredential(scanText)}>
                识别并打开凭证
              </button>
            </div>
            {scanNote ? <p className="tk-note" data-field="scan-note">{scanNote}</p> : null}
          </section>

          {card ? (
            <ParticipationCardView
              key={card.id}
              card={card}
              campaignTitle={campaignTitle || "活动"}
              batches={cardBatches}
              benefit={cardBenefit}
              canConfirm={confirmAllowed && card.confirmation_allowed === true}
              busy={busy}
              pendingConfirmation={pendingConfirmation?.participation_id === card.id ? pendingConfirmation : null}
              onConfirm={(body, evidence) => void confirmCard(body, evidence)}
              onNotFit={notFit}
              onClose={() => setCard(null)}
            />
          ) : null}

          {redemption ? (
            <RedemptionCardView
              key={redemption.id}
              benefit={redemption}
              reverseAllowed={reverseAllowed}
              busy={busy}
              onRedeem={(storeId) => void redeemBenefit(redemption, storeId)}
              onReverse={(reason) => void reverseBenefit(redemption, reason)}
              onClose={() => setRedemption(null)}
            />
          ) : null}

          </div>
          <div hidden={tab !== "records"}>
          <label className="tk-label">查看活动的待确认与权益记录
            <select className="tk-select" value={campaignId} onChange={(event) => setCampaignId(event.target.value)}>
              <option value="">请选择要查看的活动</option>
              {campaigns.map((item) => <option key={item.id} value={item.id}>{item.title || "未命名活动"}</option>)}
            </select>
          </label>
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
                        setTab("serve");
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

          </div>
          <div hidden={tab !== "history"}>
          {!storeScope ? <label className="tk-label">交班门店
            <select className="tk-select" value={historyStoreId} onChange={(event) => { historyRequest.current++; setHistoryStoreId(event.target.value); setHistory([]); setHistoryLoadedAt(""); setHistoryState("idle"); setHistoryError(""); }}>
              <option value="">请选择有权查看的门店</option>
              {stores.map((store) => <option key={store.id} value={store.id}>{store.name || "未命名门店"}</option>)}
            </select>
          </label> : null}
          <section className="tk-section" data-field="service-history" aria-label="处理历史">
            <h2 className="tk-subtitle">处理历史(本店台账,只读)</h2>
            <p className="tk-note">
              交班参考:以下为本店最近处理记录与当日按操作者聚合(仅本地事实,不含平台证实)。
              {historyLoadedAt ? ` 拉取于 ${historyLoadedAt}。` : ""}
            </p>
            {historyError ? <p className="tk-note tk-danger" role="status">{historyError}</p> : null}
            {!storeScope && !historyStoreId ? <p className="tk-note">请先选择交班门店，再读取原处理记录。</p> : null}
            {historyState === "loading" ? <p className="tk-note">正在读取所选门店的处理记录…</p> : null}
            <button className="tk-quiet" type="button" data-action="refresh-history" disabled={!storeScope && !historyStoreId || historyState === "loading"} onClick={() => void loadHistory()}>
              重查处理历史
            </button>
            {(() => {
              if (historyState !== "ready") return null;
              const today = new Date().toISOString().slice(0, 10);
              const perActor = new Map<string, number>();
              for (const row of history) {
                if (row.created_at.slice(0, 10) === today) {
                  perActor.set(row.actor, (perActor.get(row.actor) ?? 0) + 1);
                }
              }
              if (perActor.size === 0) return <p className="tk-note" data-field="shift-empty">今日本店暂无处理记录。</p>;
              return (
                <ul className="tk-list" data-field="shift-stats">
                  {[...perActor.entries()].map(([actor, n]) => (
                    <li key={actor}>今日 {actor || "(未记操作者)"}: {n} 笔</li>
                  ))}
                </ul>
              );
            })()}
            {historyState !== "ready" ? null : history.length === 0 ? (
              <p className="tk-note" data-field="history-empty">还没有处理记录。</p>
            ) : (
              <ul className="tk-list" data-field="history-list">
                {history.slice(0, 50).map((row) => (
                  <li key={row.id} data-op={row.op}>
                    {row.created_at.slice(0, 19).replace("T", " ")} · {opText(row.op)} · 操作者 {row.actor || "(未记)"}
                    {row.reason ? ` · ${row.reason}` : ""}
                  </li>
                ))}
              </ul>
            )}
          </section>

          </div>
          {actionNote ? (
            <p className="tk-note" data-field="action-note" role="status" data-state={!/未知|没有|未完成|未读到|尚未|待确认/.test(actionNote) && /完成|已处理|已更正/.test(actionNote) ? "ok" : "warn"}>
              {actionNote}
            </p>
          ) : null}
        </>
      ) : null}
    </main>
  );
}
