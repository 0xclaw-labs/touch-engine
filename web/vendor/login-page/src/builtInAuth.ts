import { ApiFailure } from "./apiError.ts";
import type { AuthAdapter, AuthResult } from "./types.ts";

async function call(fetchImpl: typeof fetch, url: string, body: unknown): Promise<Response> {
  return fetchImpl(url, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(body),
  });
}

function toFailure(res: Response, text: string, fallback: string): ApiFailure {
  let code = fallback;
  let retryAt: string | undefined;
  try {
    const parsed = JSON.parse(text) as { error?: string; retryAt?: string };
    if (parsed.error) code = parsed.error;
    if (typeof parsed.retryAt === "string") retryAt = parsed.retryAt;
  } catch {
    /* 非 JSON 体保底码 */
  }
  return new ApiFailure(text || res.statusText, res.status, code, retryAt);
}

export function createBuiltInAuth(fetchImpl: typeof fetch = fetch): AuthAdapter {
  return {
    async startEmailCode(email: string): Promise<void> {
      const res = await call(fetchImpl, "/api/auth/email/code/start", { email });
      if (!res.ok) throw toFailure(res, await res.text().catch(() => ""), "unknown");
    },
    async verifyEmailCode(email: string, code: string): Promise<AuthResult> {
      const res = await call(fetchImpl, "/api/auth/email/code/verify", { email, code });
      const text = await res.text();
      if (!res.ok) throw toFailure(res, text, res.status === 401 ? "invalid_otp" : "unknown");
      return (text ? JSON.parse(text) : {}) as AuthResult;
    },
    async signIn(identifier: string, password: string): Promise<AuthResult> {
      const res = await call(fetchImpl, "/api/auth/password", { email: identifier, password });
      const text = await res.text();
      if (!res.ok) throw toFailure(res, text, "unknown");
      return (text ? JSON.parse(text) : {}) as AuthResult;
    },
  };
}
