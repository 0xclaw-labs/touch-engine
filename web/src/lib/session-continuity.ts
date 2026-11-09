// HUI-2222 平台身份复用:免重复登录的客户端续期缝。
//
// 语义:memberships 读到 401(access token 过期)时,先用 refresh cookie 静默
// 续期一次(POST /api/auth/refresh,BFF 直通 Go,Go 再走真实 identity
// /internal/v1/identity/refresh——权威仍是 public-ai Identity,零新身份路径),
// 然后重试一次 memberships。续期也被拒 → null(匿名,门闸落登录表单);
// transport 失败 → throw(error 态:登录状态未知,绝不当作已登录或已匿名)。
//
// 单飞:一个 reader 实例只允许一个 in-flight refresh;并发 401 共享同一次
// 续期,不放大对 identity 的续期流量。

const MEMBERSHIPS_PATH = "/api/session/memberships";
const REFRESH_PATH = "/api/auth/refresh";

export type MembershipScopeLike = Record<string, unknown>;

export type ScopeReader = () => Promise<MembershipScopeLike[] | null>;

async function readMemberships(fetchImpl: typeof fetch): Promise<MembershipScopeLike[] | null | "unauthenticated"> {
  const res = await fetchImpl(MEMBERSHIPS_PATH);
  if (res.status === 401) return "unauthenticated";
  if (!res.ok) throw new Error("没有读到组织");
  const data = (await res.json().catch(() => ({}))) as { items?: MembershipScopeLike[] };
  return data.items ?? [];
}

async function refreshSession(fetchImpl: typeof fetch): Promise<boolean> {
  const res = await fetchImpl(REFRESH_PATH, { method: "POST" });
  return res.ok;
}

/**
 * 创建带单飞续期的 memberships 读取器。null = 匿名(续期也被拒);
 * throw = 网络/服务故障(登录状态未知,诚实保持 error 态)。
 */
export function createScopeReader(fetchImpl: typeof fetch = fetch): ScopeReader {
  let refreshInFlight: Promise<boolean> | null = null;

  const refreshOnce = (): Promise<boolean> => {
    if (!refreshInFlight) {
      refreshInFlight = refreshSession(fetchImpl).finally(() => {
        refreshInFlight = null;
      });
    }
    return refreshInFlight;
  };

  return async (): Promise<MembershipScopeLike[] | null> => {
    const first = await readMemberships(fetchImpl);
    if (first !== "unauthenticated") return first;
    // access 过期 → 静默续期一次 → 重试一次。续期/重试仍 401 → 匿名。
    const refreshed = await refreshOnce();
    if (!refreshed) return null;
    const second = await readMemberships(fetchImpl);
    if (second === "unauthenticated") return null;
    return second;
  };
}
