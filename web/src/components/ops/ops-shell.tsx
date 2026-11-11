"use client";

// HUI-4592 平台运营控制台共享壳:权限门 + 六视图导航 + 统一操作反馈。
//
// 纪律(裁定书/票面):
//   - /ops 不是 /admin 换标签:门 = access/effective 的 ops_permissions
//     非空(受信 ops grant,4593);无授权诚实 403 受限视图,不渲染任何
//     运营操作面;
//   - 所有运营写走统一操作信封:原因必填、幂等键按一次明确意图生成
//     (重试复用同键 → 服务端只读回,不重复执行)、结果按 operation_id
//     可追踪;页面不发明第二套写路径;
//   - 共享视图壳(O1-O6 复用),每个视图有自己的真实读面与操作。

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useCallback, useEffect, useState } from "react";
import { failureText, useMerchantSession, type MerchantSession } from "@/components/work/merchant-session";
import type { OpsOperation } from "@/lib/ops-console";

const OPS_TABS: { href: string; label: string; command: string }[] = [
  { href: "/ops/tenants", label: "商户与开通", command: "ops:tenants.read" },
  { href: "/ops/plans", label: "套餐与价格", command: "ops:plans.read" },
  { href: "/ops/orders", label: "订单与对账", command: "ops:orders.read" },
  { href: "/ops/channels", label: "渠道与健康", command: "ops:channels.read" },
  { href: "/ops/templates", label: "模板与内容", command: "ops:templates.read" },
  { href: "/ops/operations", label: "运行与交付", command: "ops:operations.read" },
];

export function opsCommandsOf(effective: unknown): string[] {
  const data = effective as { ops_permissions?: Array<{ command?: string }> } | null;
  if (!data || !Array.isArray(data.ops_permissions)) return [];
  return data.ops_permissions
    .map((p) => (typeof p?.command === "string" ? p.command : ""))
    .filter((c) => c.startsWith("ops:"));
}

// OpsGate: session + access/effective 双门。FEATURE_AUTHZ off(access 404)
// 与无 grant 同样受限——平台运营面只对持受信 grant 的人存在。
export function OpsGate({ children }: { children: React.ReactNode }) {
  const session = useMerchantSession();
  const [commands, setCommands] = useState<string[] | null>(null); // null = 判定中
  const [accessError, setAccessError] = useState("");

  const judge = useCallback(async (ses: MerchantSession) => {
    const res = await ses.api("GET", "access/effective");
    if (res.status === 404) {
      // 授权域未启用:平台运营面整体不存在(统一 404 语义,不假装)。
      setCommands([]);
      return;
    }
    if (!res.ok) {
      setAccessError(failureText(res.status, res.data));
      setCommands([]);
      return;
    }
    setCommands(opsCommandsOf(res.data));
  }, []);

  useEffect(() => {
    if (session.phase !== "ready") return;
    void judge(session);
  }, [session.phase, session, judge]);

  if (session.phase === "loading") {
    return (
      <main className="tk-page" data-state="loading">
        <p className="tk-state">正在读取会话…</p>
      </main>
    );
  }
  if (session.phase !== "ready") {
    return (
      <main className="tk-page" data-state="unauthenticated">
        <h1 className="tk-title">平台运营控制台</h1>
        <p className="tk-state">请先登录平台账号。商家工作台入口在 /work。</p>
      </main>
    );
  }
  if (commands === null) {
    return (
      <main className="tk-page" data-state="loading">
        <p className="tk-state">正在核对平台运营授权…</p>
      </main>
    );
  }
  if (commands.length === 0) {
    return (
      <main className="tk-page" data-state="restricted">
        <h1 className="tk-title">平台运营控制台</h1>
        <p className="tk-state tk-danger" data-field="forbidden">
          当前账号没有平台运营授权(403)。平台操作能力只来自受信签发的 ops
          grant,商家负责人、店员与代理都不会自动获得;如需授权请联系平台管理方。
          {accessError ? `(授权读取失败:${accessError})` : ""}
        </p>
      </main>
    );
  }
  return <>{children}</>;
}

// OpsNav: 六视图导航(共享壳;导航入口是否进全局菜单归 2628,本票只做
// /ops 内部视图切换)。
export function OpsNav() {
  const pathname = usePathname();
  return (
    <nav className="tk-nav" data-section="ops-nav" aria-label="平台运营视图">
      {OPS_TABS.map((tab) => (
        <Link
          key={tab.href}
          href={tab.href}
          className="tk-button"
          data-active={pathname === tab.href ? "true" : undefined}
          data-ops-command={tab.command}
        >
          {tab.label}
        </Link>
      ))}
    </nav>
  );
}

// ---- 统一操作信封客户端 -----------------------------------------------------------

// oneIdempotencyKey:一次明确意图 = 一个键;重试按钮复用键 → 服务端幂等读回。
export function oneIdempotencyKey(): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) {
    return crypto.randomUUID();
  }
  return `ops-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
}

export interface EnvelopeOutcome {
  operation: OpsOperation;
  replayed: boolean;
}

export async function postOperation(
  ses: MerchantSession,
  body: Record<string, unknown>,
): Promise<{ ok: boolean; status: number; message: string; outcome?: EnvelopeOutcome }> {
  const res = await ses.api("POST", "ops/operations", body);
  if (!res.ok) {
    const err = res.data["error"];
    const msg = res.data["message"];
    return {
      ok: false,
      status: res.status,
      message: typeof msg === "string" ? msg : typeof err === "string" ? `操作被拒绝(${err})` : "操作被拒绝",
    };
  }
  const operation = res.data["operation"] as OpsOperation | undefined;
  if (!operation || typeof operation.id !== "string") {
    return { ok: false, status: res.status, message: "操作缺少可追踪的 operation_id" };
  }
  return {
    ok: true,
    status: res.status,
    message: "",
    outcome: { operation, replayed: res.data["replayed"] === true },
  };
}

// OperationFeedback:结果按 operation_id 呈现(权威结果追踪面)。
export function OperationFeedback({ outcome, error }: { outcome: EnvelopeOutcome | null; error: string }) {
  if (error) {
    return (
      <p className="tk-state tk-danger" data-field="operation-error">
        {error}
      </p>
    );
  }
  if (!outcome) return null;
  const op = outcome.operation;
  const stateText =
    op.state === "succeeded" ? "执行成功" : op.state === "failed" ? "执行失败(操作已记录,可重试)" : op.state;
  return (
    <p
      className={op.state === "succeeded" ? "tk-state tk-ok" : "tk-state tk-danger"}
      data-field="operation-result"
      data-operation-state={op.state}
    >
      {outcome.replayed ? "重试读回既有操作(未重复执行)。" : ""}
      {stateText} · operation_id {op.id}
      {op.error_code ? ` · ${op.error_code}` : ""}
    </p>
  );
}

// OpsReasonForm:统一原因输入(审计三件套的"原因"半边在前端先挡空值,
// 服务端仍是最后一道闸)。
export function OpsReasonForm({
  label,
  busy,
  onSubmit,
  extraField,
}: {
  label: string;
  busy: boolean;
  onSubmit: (reason: string) => void;
  extraField?: React.ReactNode;
}) {
  const [reason, setReason] = useState("");
  return (
    <form
      className="tk-form"
      data-section="ops-reason-form"
      onSubmit={(e) => {
        e.preventDefault();
        if (!reason.trim()) return;
        onSubmit(reason.trim());
        setReason("");
      }}
    >
      <label className="tk-label" htmlFor={`reason-${label}`}>{`操作原因(必填)`}</label>
      {extraField}
      <textarea
        id={`reason-${label}`}
        className="tk-input"
        data-field="ops-reason"
        value={reason}
        rows={2}
        maxLength={500}
        onChange={(e) => setReason(e.target.value)}
        placeholder="写下这次操作的业务原因,将进入审计"
      />
      <button className="tk-button" type="submit" disabled={busy || !reason.trim()} data-action={label}>
        {busy ? "提交中…" : label}
      </button>
    </form>
  );
}
