"use client";

// HUI-4592 O4 渠道与健康:3535 能力行全量读面 + 紧急停用/恢复命令(统一
// 操作信封分派到 3535 唯一写面)。
//
// 纪律(票面):
//   - 判定全部来自 channeladmission 真值:本页不保存第二份 admitted 判定;
//   - 紧急停用只停选中的方法行(平台×方法×场景),无关方法与无关导航不受
//     影响——按钮按行渲染,不提供任何"一键全关";
//   - 到期(valid_until)/未核实证据档如实展示,unknown 不当 allowed。

import { useCallback, useEffect, useState } from "react";
import { useMerchantSession, type MerchantSession } from "@/components/work/merchant-session";
import {
  OperationFeedback,
  OpsReasonForm,
  oneIdempotencyKey,
  postOperation,
  type EnvelopeOutcome,
} from "@/components/ops/ops-shell";
import { evidenceGradeText, type OpsChannelRecord } from "@/lib/ops-console";

type Phase = "loading" | "error" | "off" | "ready";

export function ChannelsDesk() {
  const session = useMerchantSession();
  const [phase, setPhase] = useState<Phase>("loading");
  const [records, setRecords] = useState<OpsChannelRecord[]>([]);
  const [busy, setBusy] = useState(false);
  const [outcome, setOutcome] = useState<EnvelopeOutcome | null>(null);
  const [opError, setOpError] = useState("");
  const [target, setTarget] = useState<{ record: OpsChannelRecord; disable: boolean } | null>(null);

  const load = useCallback(async (ses: MerchantSession) => {
    setPhase("loading");
    const res = await ses.api("GET", "ops/channels");
    if (res.status === 404) {
      // FEATURE_CHANNEL_ADMISSION off:渠道准入域不存在(诚实,不假装健康)。
      setPhase("off");
      return;
    }
    if (!res.ok) {
      setPhase("error");
      return;
    }
    setRecords(Array.isArray(res.data.records) ? (res.data.records as OpsChannelRecord[]) : []);
    setPhase("ready");
  }, []);

  useEffect(() => {
    void load(session);
  }, [load, session]);

  const runOp = useCallback(
    async (ses: MerchantSession, record: OpsChannelRecord, disable: boolean, reason: string) => {
      setBusy(true);
      setOpError("");
      const res = await postOperation(ses, {
        action: disable ? "channel.disable" : "channel.restore",
        resource_ref: record.id,
        tenant_id: record.tenant_scope === "*" ? "" : record.tenant_scope,
        reason,
        idempotency_key: oneIdempotencyKey(),
      });
      setBusy(false);
      setTarget(null);
      if (!res.ok || !res.outcome) {
        setOpError(res.message);
        return;
      }
      setOutcome(res.outcome);
      await load(ses);
    },
    [load],
  );

  return (
    <section className="tk-section" data-view="ops-channels">
      <h2 className="tk-section-title">渠道与健康</h2>
      <p className="tk-note">
        准入判定来自 3535 渠道准入真值(平台×方法×场景×环境)。紧急停用/恢复只作用于选中的
        方法行;停用后该行的顾客侧承诺立即消失,其余动作与门店导航不受影响。
      </p>
      {phase === "loading" ? <p className="tk-state" data-state="loading">正在读取渠道能力…</p> : null}
      {phase === "off" ? (
        <p className="tk-state" data-state="feature-off">
          渠道准入域未启用( FEATURE_CHANNEL_ADMISSION off):没有可显示的准入真值。
        </p>
      ) : null}
      {phase === "error" ? <p className="tk-state tk-danger" data-state="error">渠道能力读取失败。</p> : null}
      {phase === "ready" && records.length === 0 ? (
        <p className="tk-state" data-state="empty">没有准入能力行。</p>
      ) : null}

      <ul className="tk-list" data-section="channel-list">
        {records.map((r) => (
          <li
            key={r.id}
            className="tk-row"
            data-capability-id={r.id}
            data-platform={r.platform}
            data-emergency={r.emergency_disabled ? "disabled" : "ok"}
          >
            <span data-field="route">
              {r.platform} · {r.method} · {r.scenario}({r.tenant_scope === "*" ? "平台" : r.tenant_scope})
            </span>
            <span data-field="verdict">
              {r.emergency_disabled
                ? "紧急停用中"
                : r.allowed
                  ? "放行"
                  : r.pending_external
                    ? "待外部核实"
                    : "不放行"}
              {r.reason ? ` · ${r.reason}` : ""}
            </span>
            <span className="tk-quiet" data-field="evidence">
              证据:{evidenceGradeText(r.evidence_grade)}
              {r.valid_until ? ` · 复检至 ${r.valid_until}` : " · 无复检窗"}
            </span>
            {r.emergency_disabled ? (
              <button className="tk-button" data-action="channel-restore" onClick={() => setTarget({ record: r, disable: false })}>
                恢复查验后放行
              </button>
            ) : (
              <button className="tk-button" data-action="channel-disable" onClick={() => setTarget({ record: r, disable: true })}>
                紧急停用此方法
              </button>
            )}
          </li>
        ))}
      </ul>

      {target ? (
        <div data-section="channel-action-form">
          <p className="tk-note">
            {target.disable
              ? `紧急停用 ${target.record.platform} ${target.record.method}(${target.record.scenario})——只停这一行。`
              : `恢复 ${target.record.platform} ${target.record.method}(${target.record.scenario})——恢复前应已完成重检。`}
          </p>
          <OpsReasonForm
            label={target.disable ? "确认紧急停用" : "确认恢复"}
            busy={busy}
            onSubmit={(reason) => void runOp(session, target.record, target.disable, reason)}
          />
        </div>
      ) : null}

      <OperationFeedback outcome={outcome} error={opError} />
    </section>
  );
}
