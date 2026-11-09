// HUI-1896 C2 匿名动作会话消费面(3546;fetch 薄面,与 customer-journey 同纪律)。
//
// 纪律:
//   - 进门 POST /api/public/actions/{id}/sessions 是幂等恢复(同浏览器同
//     actionId 重放回原会话);浏览路径(C1/C3)绝不调用——只有内容视图进门用;
//   - 幂等键按 actionId 在组件会话内稳定(防双击/重试双开;刷新即新会话,
//     服务端 visitor cookie 兜底同浏览器恢复);
//   - 恢复凭据明文只在签发响应出现一次,只经返回值进内存,永不写
//     localStorage/sessionStorage;兑换 token 走 POST 体,永不进 URL;
//   - 错误映射:404=not_found(未知/他人会话同形);409 action_unavailable/
//     frozen/session_ended/stale_version;410 resume_expired;403 forbidden/
//     target_mismatch;网络=network(不判成功失败);
//   - media-access purpose 白名单 preview|download|platform_fetch(与 3553
//     服务端同源),非法 purpose 本地拒绝,不发请求。

export interface ActionSessionView {
  action_session_id?: string;
  status?: string;
  storefront_revision?: number;
  action_revision?: number;
  rule_version?: number;
  selected_content_pack_version?: string;
  participation_id?: string;
  action_available?: boolean;
  resume_available?: boolean;
  created_at?: string;
  participation?: { id?: string; status?: string; benefit_reservation_state?: string };
}

export interface ResumeTokenView {
  credential_id?: string;
  purpose?: string;
  token?: string;
  expires_at?: string;
}

export interface MediaAccessItem {
  media_id: string;
  url: string;
  expires_at: string;
  mime_type: string;
  byte_size: number;
  sha256: string;
  filename: string;
}

export type SessionResult =
  | { session: ActionSessionView }
  | { error: "network" }
  | { error: "not_found" }
  | { error: "action_unavailable" }
  | { error: "frozen" }
  | { error: "session_ended" }
  | { error: "stale_version" }
  | { error: "resume_expired" }
  | { error: "resume_used" }
  | { error: "target_mismatch" };

async function postJson(url: string, body: unknown): Promise<{ status: number; data: Record<string, unknown> | null }> {
  try {
    const res = await fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    const data = (await res.json().catch(() => null)) as Record<string, unknown> | null;
    return { status: res.status, data };
  } catch {
    return { status: 0, data: null };
  }
}

async function getJson(url: string): Promise<{ status: number; data: Record<string, unknown> | null }> {
  try {
    const res = await fetch(url);
    const data = (await res.json().catch(() => null)) as Record<string, unknown> | null;
    return { status: res.status, data };
  } catch {
    return { status: 0, data: null };
  }
}

function asSession(data: Record<string, unknown> | null): ActionSessionView | null {
  if (data && typeof data.action_session_id === "string" && data.action_session_id !== "") {
    return data as ActionSessionView;
  }
  return null;
}

/** 会话域错误 → 稳定错误名(与服务端 writeActionSessionError 码形对齐)。 */
function sessionErrorOf(status: number, data: Record<string, unknown> | null): SessionResult {
  const code = data && typeof data.error === "string" ? data.error : "";
  switch (code || String(status)) {
    case "404":
    case "not_found":
      return { error: "not_found" };
    case "action_unavailable":
      return { error: "action_unavailable" };
    case "frozen":
      return { error: "frozen" };
    case "session_ended":
      return { error: "session_ended" };
    case "stale_version":
      return { error: "stale_version" };
    case "resume_expired":
      return { error: "resume_expired" };
    case "resume_used":
      return { error: "resume_used" };
    case "target_mismatch":
      return { error: "target_mismatch" };
    case "forbidden":
      return { error: "target_mismatch" };
    default:
      return { error: "network" };
  }
}

// 会话级幂等键:Map<actionId, key>(内存,不落盘;刷新即新会话)。
const idempotencyKeys = new Map<string, string>();

function idempotencyKeyFor(actionId: string): string {
  let key = idempotencyKeys.get(actionId);
  if (!key) {
    key = "cs-web-" + actionId + "-" + Math.random().toString(36).slice(2, 12) + Date.now().toString(36);
    idempotencyKeys.set(actionId, key);
  }
  return key;
}

/**
 * 内容视图进门:显式动作会话创建或同浏览器恢复(3546 幂等语义)。
 * 这一步不建参与、不占库存——参与升级是单独的显式 POST participations。
 */
export async function startOrResumeSession(
  actionId: string,
  opts: { source?: string; contentPackVersion?: string; expectedActionRevision?: number } = {},
): Promise<SessionResult> {
  const { status, data } = await postJson(
    `/api/public/actions/${encodeURIComponent(actionId)}/sessions`,
    {
      touchpoint_source: opts.source ?? "web",
      content_pack_version: opts.contentPackVersion ?? "",
      expected_action_revision: opts.expectedActionRevision ?? 0,
      idempotency_key: idempotencyKeyFor(actionId),
    },
  );
  if (status === 0) return { error: "network" };
  if (status === 200 || status === 201) {
    const session = asSession(data);
    return session ? { session } : { error: "network" };
  }
  return sessionErrorOf(status, data);
}

/** 只读状态(实时位/冻结版本/权益引用);动作停用不拦读(旧权益保护)。 */
export async function sessionStatus(sessionId: string): Promise<SessionResult> {
  const { status, data } = await getJson(`/api/public/action-sessions/${encodeURIComponent(sessionId)}/status`);
  if (status === 0) return { error: "network" };
  if (status === 200) {
    const session = asSession(data);
    return session ? { session } : { error: "network" };
  }
  return sessionErrorOf(status, data);
}

/** 预览期换选;交接冻结后服务端 409 → {error:'frozen'}(已交接版本不动)。 */
export async function switchSessionContent(sessionId: string, contentPackVersion: string): Promise<SessionResult> {
  const { status, data } = await postJson(
    `/api/public/action-sessions/${encodeURIComponent(sessionId)}/content`,
    { content_pack_version: contentPackVersion },
  );
  if (status === 0) return { error: "network" };
  if (status === 200) {
    const session = asSession(data);
    return session ? { session } : { error: "network" };
  }
  return sessionErrorOf(status, data);
}

/** customer_resume 凭据签发(跨浏览器恢复用);明文只在本次返回值出现。 */
export async function issueCustomerResume(sessionId: string): Promise<ResumeTokenView | null> {
  const { status, data } = await postJson(
    `/api/public/action-sessions/${encodeURIComponent(sessionId)}/resume-credentials`,
    {},
  );
  if (status !== 201 || !data || typeof data.token !== "string") return null;
  return data as ResumeTokenView;
}

/** 跨浏览器恢复:兑换单用途凭据,绑定当前浏览器并消费。 */
export async function exchangeResumeCredential(token: string): Promise<SessionResult> {
  const { status, data } = await postJson("/api/public/resume/exchange", { token });
  if (status === 0) return { error: "network" };
  if (status === 200) {
    const session = asSession(data);
    return session ? { session } : { error: "network" };
  }
  return sessionErrorOf(status, data);
}

const MEDIA_PURPOSES: ReadonlySet<string> = new Set(["preview", "download", "platform_fetch"]);

export type MediaAccessResult =
  | { versionId: string; purpose: string; expiresAt: string; items: MediaAccessItem[] }
  | { error: "network" }
  | { error: "bad_purpose" }
  | { error: "undistributable" }
  | { error: "media_missing" }
  | { error: "session_mismatch" }
  | { error: "not_found" };

/** 短 TTL 受控媒体凭据(purpose 白名单;非法 purpose 本地拒绝)。 */
export async function mintMediaAccess(
  versionId: string,
  opts: { actionSessionId: string; mediaIds: string[]; purpose: string },
): Promise<MediaAccessResult> {
  if (!MEDIA_PURPOSES.has(opts.purpose)) return { error: "bad_purpose" };
  const { status, data } = await postJson(`/api/public/content/${encodeURIComponent(versionId)}/media-access`, {
    action_session_id: opts.actionSessionId,
    media_ids: opts.mediaIds,
    purpose: opts.purpose,
  });
  if (status === 0) return { error: "network" };
  if (status === 200 && data && Array.isArray(data.items)) {
    return {
      versionId: typeof data.version_id === "string" ? data.version_id : versionId,
      purpose: typeof data.purpose === "string" ? data.purpose : opts.purpose,
      expiresAt: typeof data.expires_at === "string" ? data.expires_at : "",
      items: (data.items as Record<string, unknown>[]).map((item) => ({
        media_id: String(item.media_id ?? ""),
        url: String(item.url ?? ""),
        expires_at: String(item.expires_at ?? ""),
        mime_type: String(item.mime_type ?? ""),
        byte_size: typeof item.byte_size === "number" ? item.byte_size : 0,
        sha256: String(item.sha256 ?? ""),
        filename: String(item.filename ?? ""),
      })).filter((item) => item.media_id !== "" && item.url !== ""),
    };
  }
  const code = data && typeof data.error === "string" ? data.error : "";
  if (status === 409) {
    if (code === "action_mismatch") return { error: "session_mismatch" };
    return { error: "undistributable" };
  }
  if (status === 410 || code === "media_missing") return { error: "media_missing" };
  if (status === 404) return { error: "not_found" };
  return { error: "network" };
}

/** 会话状态中文标签(未知就是未知)。 */
export function sessionStatusLabel(status: string | undefined): string {
  switch ((status ?? "").trim()) {
    case "active":
      return "进行中";
    case "ended":
      return "已结束";
    default:
      return "状态未知";
  }
}
