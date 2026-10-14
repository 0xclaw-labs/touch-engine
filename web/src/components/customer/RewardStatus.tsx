"use client";

// HUI-1896 屏3「领取或请店员确认」:顾客三屏第三屏。
//
// 职责:读自身 3546 参与(受控 visitor 会话)与 1671 公共奖励面,给顾客
// 真实进度。人工路径:参与编号 + 出示参与码 + 「请店员确认」;官方/店员
// 路径:真实状态标签。奖励实际发放(benefit 记录可读)才显示券码(掩码)/
// 门店/期限;已兑现不再引导重复领券。
//
// 纪律:
//   - 「我已完成」只刷新自身参与并提醒核对,确认永远由店员/平台做出,
//     这一按钮不能自己让奖励通过;
//   - 券码走 rewardCardCopy 唯一出口(掩码);fulfilled 但无券码读面时
//     走 staffFulfilledCopy(现场出示为准),不编造;
//   - 恢复二维码经 isSafeQrPayload 形状校验才渲染;凭据只进内存,
//     绝不写 localStorage/sessionStorage;
//   - 不发店员/商家会话请求(staff/*、reward-confirm 禁触)。

import { useCallback, useEffect, useState } from "react";
import { isSafeQrPayload, statusLabel } from "@/lib/participation";
import {
  exceptionCopy,
  resumeCredential,
  staffFulfilledCopy,
  stockLine,
  type CustomerParticipation,
  type RewardBenefitPublic,
  rewardCardCopy,
} from "@/lib/customer-journey";

export interface PublicRewardSummary {
  counts?: Record<string, number>;
  slots?: { benefit_slot: string; rule_version: number; available_qty: number }[];
}

export interface RewardStatusProps {
  code: string;
  participation: CustomerParticipation | null;
  /** 顾客面可读到的权益记录(今天没有公共读面;由页面在未来接入时传入)。 */
  benefit?: RewardBenefitPublic | null;
  /** 外部发布状态(unknown 不拖黑本系统已发放)。 */
  externalPublish?: string;
}

export function RewardStatus({ code, participation, benefit, externalPublish }: RewardStatusProps) {
  const [summary, setSummary] = useState<PublicRewardSummary | null>(null);
  const [qrPayload, setQrPayload] = useState("");
  const [verifyNote, setVerifyNote] = useState("");
  const [busy, setBusy] = useState(false);

  const id = participation?.id ?? "";

  // 1671 公共 summary:off/不可达时保持 null → 只显示领取条件说明。
  useEffect(() => {
    if (!code || !id) return;
    let alive = true;
    fetch(`/api/public/links/${encodeURIComponent(code)}/reward-summary`)
      .then(async (res) => {
        if (!alive) return;
        const data = await res.json().catch(() => null);
        if (alive && res.ok && data) setSummary(data as PublicRewardSummary);
      })
      .catch(() => undefined);
    return () => {
      alive = false;
    };
  }, [code, id]);

  // 「我已完成」= 请求核验:重读自身参与(状态以服务端为准)。
  const requestVerify = useCallback(async () => {
    if (!id) return;
    setBusy(true);
    try {
      const res = await fetch(`/api/participations/${encodeURIComponent(id)}`);
      const data = await res.json().catch(() => null);
      if (res.ok && data?.status) {
        setVerifyNote(`当前状态:${statusLabel(data.status as string)}。这一步只是请求核验，不会自己让奖励通过。`);
      } else {
        setVerifyNote("状态暂时读不到。请出示参与编号请店员核对。");
      }
    } catch {
      setVerifyNote(exceptionCopy("network_unknown"));
    } finally {
      setBusy(false);
    }
  }, [id]);

  // 出示参与码:一次性签发恢复凭据;形状校验过才渲染链接。
  const showCode = useCallback(async () => {
    if (!id || busy) return;
    setBusy(true);
    try {
      const cred = await resumeCredential(id);
      const payload = cred?.qr_payload ?? "";
      setQrPayload(isSafeQrPayload(payload) ? payload : "");
      setVerifyNote(qrPayload ? "" : "参与码暂时签发不出来。直接把下方参与编号给店员看即可。");
    } catch {
      setVerifyNote("参与码暂时签发不出来。直接把下方参与编号给店员看即可。");
    } finally {
      setBusy(false);
    }
  }, [busy, id, qrPayload]);

  if (!participation) return null;

  const status = participation.status ?? "";
  const fulfilledWithoutRecord = status === "fulfilled" && !benefit;
  const rewardLine = fulfilledWithoutRecord ? staffFulfilledCopy() : rewardCardCopy(benefit ?? null, { externalPublish: externalPublish ?? "unknown" });
  const redeemed = (benefit?.status ?? "") === "redeemed";
  const slot = summary?.slots?.[0];
  const stock = stockLine(slot?.available_qty);

  return (
    <section className="tk-section" data-testid="reward-status" data-status={status}>
      <h2 className="tk-section-title">领取或请店员确认</h2>
      <p className="tk-lead" data-testid="reward-progress">当前状态:{statusLabel(status)}</p>
      <p className="tk-note">参与编号 {id}</p>
      {participation.expires_at && <p className="tk-note">保留期限至 {participation.expires_at}</p>}

      {status === "awaiting_review" && <p className="tk-note">店员核对中。核对的结论由店员做出，这里不会提前宣布。</p>}

      <div data-testid="reward-card">
        <p className={rewardLine.includes("已发放") || rewardLine.includes("已兑现") ? "tk-ok" : "tk-note"}>{rewardLine}</p>
        {!redeemed && (
          <p className="tk-note">领取条件:店员确认完成后由门店发放;发放前不会先展示券码。</p>
        )}
      </div>

      {!redeemed && (
        <div className="tk-row tk-gap">
          <button type="button" className="tk-quiet" disabled={busy} data-testid="reward-verify" onClick={requestVerify}>
            我已完成
          </button>
          <button type="button" className="tk-quiet" disabled={busy} data-testid="reward-show-code" onClick={showCode}>
            出示参与码
          </button>
        </div>
      )}
      {qrPayload && (
        <p className="tk-note" data-testid="reward-qr">
          恢复入口(只显示这一次,请保存):
          {" "}
          <a href={qrPayload} target="_blank" rel="noopener,noreferrer">打开参与码</a>
        </p>
      )}
      {verifyNote && <p className="tk-note" data-testid="reward-verify-note">{verifyNote}</p>}
      {stock && <p className="tk-note">{stock}</p>}
      <p className="tk-note">有问题请直接请店员确认,或联系门店。</p>
    </section>
  );
}
