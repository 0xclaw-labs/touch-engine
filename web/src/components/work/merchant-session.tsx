"use client";

import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { failureText } from "@/lib/failure-copy";
import { surfaceLabel } from "@/lib/product-finish";
import {
  beginTenantOp,
  currentTenantOp,
  decideTenantConfirm,
  resolveWorkspaceTenant,
  TENANT_STORAGE_KEY,
  writeTenantMemory,
  type MembershipScope,
} from "@/lib/workspace-tenant";

export interface MerchantResult {
  ok: boolean;
  status: number;
  data: Record<string, unknown>;
}

export interface MerchantSession {
  tenantId: string;
  role: string;
  email: string;
  tenantName: string;
  phase: "loading" | "anonymous" | "choose" | "none" | "ready" | "error";
  error: string;
  options: MembershipScope[];
  login: (email: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
  switchTenant: (tenantId: string) => void;
  api: (method: string, path: string, body?: unknown) => Promise<MerchantResult>;
  retryConnection: () => void;
}

const SessionContext = createContext<MerchantSession | null>(null);

export function useSession(): MerchantSession {
  const value = useContext(SessionContext);
  if (!value) throw new Error("merchant session missing");
  return value;
}

// failureText 已收口到 lib/failure-copy（fix2：错误面产品语句，不泄漏状态码/机器码）。
// 这里 re-export 保持页面既有 import 路径不变。

export function MerchantGate({ children }: { children: ReactNode }) {
  const session = useMerchantSession();
  if (session.phase === "loading") {
    return (
      <main className="tk-page">
        <p className="tk-state" data-state="loading">{surfaceLabel("loading")}</p>
      </main>
    );
  }
  if (session.phase === "error") {
    return (
      <main className="tk-page">
        <p className="tk-state tk-danger" data-state="error">
          {session.error}
          <button className="tk-quiet" type="button" data-action="retry" onClick={session.retryConnection}>重试</button>
        </p>
      </main>
    );
  }
  if (session.phase === "choose") return <OrgChoices session={session} />;
  if (session.phase === "none") {
    return (
      <main className="tk-page">
        <p className="tk-state" data-state="empty">当前账号没有可进入的组织。</p>
      </main>
    );
  }
  if (session.phase !== "ready") return <Login session={session} />;
  return <SessionContext.Provider value={session}>{children}</SessionContext.Provider>;
}

function Login({ session }: { session: MerchantSession }) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");

  return (
    <main className="tk-page">
      <p className="tk-kicker">碰一碰</p>
      <h1 className="tk-title">今天要做的事</h1>
      <p className="tk-lead">登录后看今天的活动、门店、素材、触达、留资和还不能当成完成的奖励。</p>
      <form
        className="tk-form"
        onSubmit={(event) => {
          event.preventDefault();
          setError("");
          void session.login(email, password).catch((err: unknown) => {
            setError(err instanceof Error ? err.message : "没有登录");
          });
        }}
      >
        <label className="tk-label">
          邮箱
          <input className="tk-input" value={email} onChange={(event) => setEmail(event.target.value)} autoComplete="username" required />
        </label>
        <label className="tk-label">
          密码
          <input className="tk-input" type="password" value={password} onChange={(event) => setPassword(event.target.value)} autoComplete="current-password" required />
        </label>
        <button className="tk-button" type="submit">登录并查看今天</button>
      </form>
      {error ? <p className="tk-danger" data-state="error">{error}</p> : null}
    </main>
  );
}

function OrgChoices({ session }: { session: MerchantSession }) {
  return (
    <main className="tk-page">
      <h1 className="tk-title">选择组织</h1>
      <p className="tk-lead">用组织名称进入。不需要填写编号。</p>
      {session.options.map((item) => (
        <button key={item.tenant_id} className="tk-button" type="button" onClick={() => session.switchTenant(item.tenant_id)}>
          {item.display_name}
        </button>
      ))}
    </main>
  );
}

async function readScopes(): Promise<MembershipScope[] | null> {
  const res = await fetch("/api/session/memberships");
  if (res.status === 401) return null;
  if (!res.ok) throw new Error("没有读到组织");
  const data = (await res.json().catch(() => ({}))) as { items?: MembershipScope[] };
  return data.items ?? [];
}

function useMerchantSession(): MerchantSession {
  const [tenantId, setTenantId] = useState("");
  const [role, setRole] = useState("");
  const [email, setEmail] = useState("");
  const [tenantName, setTenantName] = useState("");
  const [phase, setPhase] = useState<MerchantSession["phase"]>("loading");
  const [error, setError] = useState("");
  const [options, setOptions] = useState<MembershipScope[]>([]);
  const [attempt, setAttempt] = useState(0);

  const confirmTenant = useCallback(async (seq: number, nextTenant: string, displayName: string, items: MembershipScope[]) => {
    let res: Response;
    try {
      res = await fetch("/api/whoami", { headers: { "x-tenant-id": nextTenant } });
    } catch {
      if (seq !== currentTenantOp()) return;
      writeTenantMemory("reject", nextTenant, window.localStorage);
      setRole("");
      setTenantId("");
      setTenantName("");
      setError("没有读到当前组织。没有沿用上一户的数据。");
      setPhase("error");
      return;
    }
    const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
    const decision = decideTenantConfirm({
      seq,
      whoamiOk: res.ok,
      enabled: data.enabled,
      role: data.role,
      tenantId: nextTenant,
      items,
    });
    if (decision === "stale") return;
    writeTenantMemory(decision, nextTenant, window.localStorage);
    if (decision !== "accept") {
      setRole("");
      setTenantId("");
      setTenantName("");
      setError("没有读到当前组织。没有沿用上一户的数据。");
      setPhase("error");
      return;
    }
    setRole(typeof data.role === "string" ? data.role : "");
    setEmail(typeof data.email === "string" ? data.email : "");
    setTenantName(typeof data.tenant_name === "string" && data.tenant_name ? data.tenant_name : displayName);
    setTenantId(nextTenant);
    setError("");
    setPhase("ready");
  }, []);

  const applyScopes = useCallback(async (items: MembershipScope[], remembered: string | null, seq: number) => {
    if (seq !== currentTenantOp()) return;
    setOptions(items);
    const resolved = resolveWorkspaceTenant(items, remembered);
    if (resolved.status === "selected") {
      await confirmTenant(seq, resolved.tenantId, resolved.displayName, items);
      return;
    }
    if (seq !== currentTenantOp()) return;
    setTenantId("");
    setRole("");
    setTenantName("");
    setPhase(resolved.status === "choose" ? "choose" : "none");
  }, [confirmTenant]);

  useEffect(() => {
    const seq = beginTenantOp();
    const remembered = window.localStorage.getItem(TENANT_STORAGE_KEY);
    void readScopes().then((items) => {
      if (seq !== currentTenantOp()) return;
      if (items === null) {
        setPhase("anonymous");
        return;
      }
      return applyScopes(items, remembered, seq);
    }).catch(() => {
      if (seq !== currentTenantOp()) return;
      setError("没有连上服务。登录状态未知，没有把它当成已登录。");
      setPhase("error");
    });
  }, [applyScopes, attempt]);

  const retryConnection = useCallback(() => {
    setPhase("loading");
    setError("");
    setAttempt((a) => a + 1);
  }, []);

  const api = useCallback(async (method: string, path: string, body?: unknown) => {
    const res = await fetch("/api/" + path.replace(/^\//, ""), {
      method,
      headers: {
        "x-tenant-id": tenantId,
        ...(body ? { "content-type": "application/json" } : {}),
      },
      body: body ? JSON.stringify(body) : undefined,
    });
    const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
    return { ok: res.ok, status: res.status, data };
  }, [tenantId]);

  const login = useCallback(async (nextEmail: string, password: string) => {
    const res = await fetch("/api/auth/login", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ email: nextEmail, password }),
    });
    const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
    if (!res.ok) throw new Error(failureText(res.status, data));
    const seq = beginTenantOp();
    const items = await readScopes();
    if (seq !== currentTenantOp()) return;
    if (items === null) throw new Error("登录后没有读到组织");
    await applyScopes(items, window.localStorage.getItem(TENANT_STORAGE_KEY), seq);
  }, [applyScopes]);

  const logout = useCallback(async () => {
    await fetch("/api/auth/logout", { method: "POST" });
    setRole("");
    setEmail("");
    setPhase("anonymous");
  }, []);

  const switchTenant = useCallback((nextTenant: string) => {
    const items = options;
    const hit = items.find((item) => item.tenant_id === nextTenant && item.enabled && item.source === "membership");
    if (!hit) return;
    const seq = beginTenantOp();
    setTenantId("");
    setRole("");
    setTenantName("");
    setPhase("loading");
    void confirmTenant(seq, hit.tenant_id, hit.display_name, items).catch(() => {
      if (seq !== currentTenantOp()) return;
      writeTenantMemory("reject", hit.tenant_id, window.localStorage);
      setError("切换后没有读到身份。没有沿用上一户的数据。");
      setRole("");
      setTenantId("");
      setPhase("error");
    });
  }, [confirmTenant, options]);

  return useMemo(() => ({
    tenantId,
    role,
    email,
    tenantName,
    phase,
    error,
    options,
    login,
    logout,
    switchTenant,
    api,
    retryConnection,
  }), [api, email, error, login, logout, options, phase, retryConnection, role, switchTenant, tenantId, tenantName]);
}

export { failureText };
