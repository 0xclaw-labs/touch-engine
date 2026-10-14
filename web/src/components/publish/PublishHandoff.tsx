"use client";

// HUI-1670 顾客发布交接(自足组件目录)。
//
// 职责:把「真实渠道交接」的顾客面收敛在一个组件里——
//   路线裁决预览(诚实能力面)→ 内容快照确认(指纹绑定)→ 开始交接
//   (过闸+attempt 冻结+受控媒体)→ 进入平台 → 我已完成(仅核验请求)/
//   取消 → 刷新查询(unknown 只查询,不重发)。
//
// 纪律(文案与安全判定全部走 ./state 的唯一出口):
//   - 只有 platform_confirmed 可以说「已发布」;申报/打开/超时都不算;
//   - refused 平台不渲染手动指引(没有手动后门);manual_only 给合规导出;
//   - 恢复令牌只在开始响应里出现一次,提示保存,绝不写 localStorage;
//   - 入口/媒体地址先过形状校验再渲染;组件不触碰奖励面,不请求
//     publish-reward / reward-proof;不向页面外泄漏任何 token。

import { useCallback, useEffect, useRef, useState } from "react";
import {
  CONFIRM_MISMATCH_COPY,
  DONE_NOTICE,
  canCancel,
  canRequestDone,
  entryOpenable,
  evidenceCopy,
  exportCaptionFor,
  isKnownHandoffStatus,
  manualStepsFor,
  mediaUrlSafe,
  qrPayloadSafe,
  refusalNotice,
  statusCopy,
  timeoutCopy,
  unsupportedLines,
  type HandoffView,
  type MediaRefView,
  type PlanView,
  type RoutesResponse,
  type StartResponse,
} from "./state";

const PLATFORMS: { id: string; label: string }[] = [
  { id: "douyin", label: "抖音" },
  { id: "kuaishou", label: "快手" },
  { id: "xiaohongshu", label: "小红书" },
  { id: "channels", label: "微信视频号" },
  { id: "bilibili", label: "B站" },
];

function platformLabel(id: string): string {
  return PLATFORMS.find((p) => p.id === id)?.label ?? id;
}

interface ParticipationToken {
  id: string;
  idempotencyKey: string;
}

export function PublishHandoff({ code }: { code: string }) {
  const [platform, setPlatform] = useState("xiaohongshu");
  const [routes, setRoutes] = useState<RoutesResponse | null>(null);
  const [routesMissing, setRoutesMissing] = useState(false);
  const [confirmed, setConfirmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");
  const [doneNotice, setDoneNotice] = useState("");
  const [handoff, setHandoff] = useState<HandoffView | null>(null);
  const [plan, setPlan] = useState<PlanView | null>(null);
  const [media, setMedia] = useState<MediaRefView[]>([]);
  const [recoveryShown, setRecoveryShown] = useState(false);
  const [recoveryQr, setRecoveryQr] = useState("");
  const participation = useRef<ParticipationToken | null>(null);
  const idemKey = useRef<string>("");

  // 路线裁决预览:平台切换即刷新;404 = 交接未开通,组件整体不可见。
  useEffect(() => {
    if (!code) return;
    let alive = true;
    setConfirmed(false);
    fetch(
      `/api/public/links/${encodeURIComponent(code)}/publish-handoff/routes?platform=${encodeURIComponent(platform)}&with_snapshot=1`,
    )
      .then(async (res) => {
        if (!alive) return;
        if (res.status === 404) {
          setRoutesMissing(true);
          setRoutes(null);
          return;
        }
        const data = await res.json().catch(() => null);
        if (alive && res.ok && data) {
          setRoutesMissing(false);
          setRoutes(data as RoutesResponse);
        } else if (alive) {
          setRoutesMissing(true);
        }
      })
      .catch(() => {
        if (alive) setRoutesMissing(true);
      });
    return () => {
      alive = false;
    };
  }, [code, platform]);

  const refresh = useCallback(
    (handoffID: string) => {
      fetch(`/api/public/links/${encodeURIComponent(code)}/publish-handoff/${encodeURIComponent(handoffID)}`)
        .then(async (res) => {
          const data = await res.json().catch(() => null);
          if (res.ok && data?.handoff) {
            setHandoff(data.handoff as HandoffView);
            setPlan(data.plan as PlanView);
            setMedia(Array.isArray(data.media) ? (data.media as MediaRefView[]) : []);
          }
        })
        .catch(() => undefined);
    },
    [code],
  );

  // 参与记录:同一浏览器同一活动复用同一幂等键(重放回原记录)。
  const ensureParticipation = useCallback(async (): Promise<string | null> => {
    if (participation.current) return participation.current.id;
    if (!idemKey.current) idemKey.current = "ph-web-" + crypto.randomUUID();
    const res = await fetch("/api/participations", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        link_code: code,
        touchpoint_source: "manual",
        rule_version: 1,
        idempotency_key: idemKey.current,
      }),
    });
    const data = await res.json().catch(() => null);
    if (!res.ok || !data?.id) {
      setNotice("发布交接还未开通，或活动暂不可参与。");
      return null;
    }
    participation.current = { id: data.id as string, idempotencyKey: idemKey.current };
    return data.id as string;
  }, [code]);

  const start = useCallback(async () => {
    const snapshotVersion = routes?.snapshot?.content_version ?? "";
    if (!snapshotVersion || !confirmed) return;
    setBusy(true);
    setNotice("");
    setDoneNotice("");
    try {
      const ptID = await ensureParticipation();
      if (!ptID) return;
      const res = await fetch(`/api/public/links/${encodeURIComponent(code)}/publish-handoff`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          participation_id: ptID,
          platform,
          confirmed_content_version: snapshotVersion,
        }),
      });
      const data = (await res.json().catch(() => null)) as (StartResponse & { error?: string; notice?: string; message?: string }) | null;
      if (res.status === 422 && data?.error === "confirmation_mismatch") {
        setNotice(CONFIRM_MISMATCH_COPY);
        return;
      }
      if (data?.error === "handoff_refused") {
        setNotice(data.notice || data.message || "该平台此场景不能通过本服务交接。这一步没有开始，也不是发布成功。");
        return;
      }
      if (!res.ok || !data?.handoff) {
        setNotice(data?.message ?? "交接没有开始。这一步不是发布成功。");
        return;
      }
      setHandoff(data.handoff);
      setPlan(data.plan ?? null);
      setMedia(Array.isArray(data.media) ? data.media : []);
      if (data.recovery?.token) setRecoveryShown(true);
      setRecoveryQr(qrPayloadSafe(data.recovery?.qr_payload) ? (data.recovery?.qr_payload ?? "") : "");
    } catch {
      setNotice("网络异常。交接没有开始，也没有发布。");
    } finally {
      setBusy(false);
    }
  }, [code, confirmed, ensureParticipation, platform, routes]);

  const requestDone = useCallback(async () => {
    if (!handoff?.id) return;
    setBusy(true);
    try {
      const res = await fetch(
        `/api/public/links/${encodeURIComponent(code)}/publish-handoff/${encodeURIComponent(handoff.id)}/done`,
        { method: "POST", headers: { "Content-Type": "application/json" }, body: "{}" },
      );
      if (res.ok) {
        setDoneNotice(DONE_NOTICE);
        refresh(handoff.id);
      } else {
        setDoneNotice("申报没有记录成功，可以稍后再试。这不影响已发生的交接。");
      }
    } catch {
      setDoneNotice("网络异常，申报没有记录。这不影响已发生的交接。");
    } finally {
      setBusy(false);
    }
  }, [code, handoff, refresh]);

  const cancel = useCallback(async () => {
    if (!handoff?.id) return;
    setBusy(true);
    try {
      const res = await fetch(
        `/api/public/links/${encodeURIComponent(code)}/publish-handoff/${encodeURIComponent(handoff.id)}/cancel`,
        { method: "POST", headers: { "Content-Type": "application/json" }, body: "{}" },
      );
      const data = await res.json().catch(() => null);
      if (res.ok && data?.handoff) {
        setHandoff(data.handoff as HandoffView);
        setDoneNotice("");
      } else {
        setNotice("取消没有成功。已发生的交接记录不会被抹掉。");
      }
    } catch {
      setNotice("网络异常，取消没有成功。");
    } finally {
      setBusy(false);
    }
  }, [code, handoff]);

  if (routesMissing && !handoff) return null; // 开关关闭/未开通:页面布局不变,组件不可见

  const snapshot = routes?.snapshot;
  const chosen = pickPlan(routes);
  const refused = chosen?.kind === "refused";
  const steps = manualStepsFor(chosen);
  const entry = chosen?.entry;
  const status = handoff?.status;
  const known = isKnownHandoffStatus(status);
  const openable = entryOpenable(entry);
  const safeMedia = media.filter((m) => mediaUrlSafe(m.url));

  return (
    <div className="tk-section" data-testid="publish-handoff">
      <h2 className="tk-section-title">发布交接</h2>
      <p className="tk-note">
        这里把本次活动的素材和文案交接给你，由你本人在平台内发布。交接、申报或超时都不算发布成功，也不触发奖励。
      </p>
      <label className="tk-label">
        发布到
        <select className="tk-select" value={platform} onChange={(e) => setPlatform(e.target.value)}>
          {PLATFORMS.map((p) => (
            <option key={p.id} value={p.id}>
              {p.label}
            </option>
          ))}
        </select>
      </label>
      {routes?.notice && <p className="tk-unknown" data-testid="handoff-routes-notice">{routes.notice}</p>}
      {refused ? (
        <p className="tk-warn" data-testid="handoff-refused">
          {refusalNotice(chosen)}
        </p>
      ) : (
        snapshot && (
          <div data-testid="handoff-snapshot">
            <p className="tk-note">
              将要交接的内容:〈{snapshot.title || "未命名"}〉{snapshot.store_name ? ` @ ${snapshot.store_name}` : ""}
            </p>
            <label className="tk-check">
              <input
                type="checkbox"
                checked={confirmed}
                onChange={(e) => setConfirmed(e.target.checked)}
                data-testid="handoff-confirm"
              />
              我确认交接以上内容，并用我自己的账号在平台内发布
            </label>
          </div>
        )
      )}
      {!handoff && !refused && (
        <button
          type="button"
          className="tk-quiet"
          disabled={busy || !confirmed || !snapshot}
          data-testid="handoff-start"
          onClick={start}
        >
          {busy ? "处理中…" : "开始发布交接"}
        </button>
      )}
      {handoff && (
        <div data-testid="handoff-status" data-status={known ? status : "unknown"}>
          <p className={status === "platform_confirmed" ? "tk-ok" : "tk-note"}>{statusCopy(status)}</p>
          {evidenceCopy(handoff.evidence_source) && <p className="tk-note">{evidenceCopy(handoff.evidence_source)}</p>}
          {timeoutCopy(handoff.timed_out, handoff.query_count) && (
            <p className="tk-unknown">{timeoutCopy(handoff.timed_out, handoff.query_count)}</p>
          )}
          <button
            type="button"
            className="tk-quiet"
            disabled={busy}
            data-testid="handoff-refresh"
            onClick={() => handoff.id && refresh(handoff.id)}
          >
            刷新结果
          </button>
          {canRequestDone(status) && (
            <button type="button" className="tk-quiet" disabled={busy} data-testid="handoff-done" onClick={requestDone}>
              我已完成
            </button>
          )}
          {canCancel(status) && (
            <button type="button" className="tk-quiet" disabled={busy} data-testid="handoff-cancel" onClick={cancel}>
              取消交接
            </button>
          )}
        </div>
      )}
      {handoff && recoveryShown && (
        <p className="tk-warn" data-testid="handoff-recovery">
          恢复方式已生成，只显示这一次:请保存下方入口，换设备可凭它恢复参与，不会重复计参与。
          {recoveryQr && (
            <>
              {" "}
              <a href={recoveryQr} target="_blank" rel="noopener,noreferrer" data-testid="handoff-recovery-link">
                换设备恢复入口
              </a>
            </>
          )}
        </p>
      )}
      {openable && entry && (
        <a
          className="tk-quiet"
          href={entry.method === "h5" ? entry.url : undefined}
          target="_blank"
          rel="noopener,noreferrer"
          data-testid="handoff-entry"
        >
          进入{platformLabel(platform)}发布（这一步还不是发布成功）
        </a>
      )}
      {unsupportedLines(entry).map((line) => (
        <p key={line} className="tk-note">
          {line}
        </p>
      ))}
      {steps.length > 0 && (
        <div data-testid="handoff-manual">
          {exportCaptionFor(chosen) && <p className="tk-note">{exportCaptionFor(chosen)}</p>}
          <ol className="tk-list">
            {steps.map((step) => (
              <li key={step}>{step}</li>
            ))}
          </ol>
        </div>
      )}
      {safeMedia.length > 0 && (
        <ul className="tk-list" data-testid="handoff-media">
          {safeMedia.map((m, i) => (
            <li key={`${m.asset_id ?? "asset"}-${i}`}>
              {m.kind === "video" ? "视频" : m.kind === "cover" ? "封面" : "图片"}:{m.filename}
              （{m.content_type}）
            </li>
          ))}
        </ul>
      )}
      {doneNotice && (
        <p className="tk-note" data-testid="handoff-done-notice">
          {doneNotice}
        </p>
      )}
      {notice && (
        <p className="tk-note" data-testid="handoff-notice">
          {notice}
        </p>
      )}
    </div>
  );
}

// pickPlan 选出当前平台最诚实的路线:有 allowed 路线选它;全被拒选第一个
// refused;没有登记(unknown)→ 服务端 start 会按 manual_only 处理,这里给 null。
function pickPlan(routes: RoutesResponse | null | undefined): PlanView | null {
  const plans = (routes?.routes ?? []).map((r) => r.plan).filter(Boolean) as PlanView[];
  if (plans.length === 0) return null;
  return plans.find((p) => p.allowed) ?? plans[0];
}
