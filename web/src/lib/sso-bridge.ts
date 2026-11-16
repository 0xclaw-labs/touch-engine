/**
 * 碰一碰入站 SSO 桥：门户签发的一次性 code 由 landing 表单自动 POST，
 * exchange 在服务端用本应用 canonical Host 调 identity，identity 下发
 * touch-engine_* cookie（本应用不写、不解析凭据）。
 */
import { request as httpRequest } from "node:http";
import { readSsoAppHost } from "@/lib/sso-host";
export { TOUCH_APP_HOST, hostAllowed } from "@/lib/sso-host";

export const IDENTITY_SSO_EXCHANGE_DEFAULT = "http://127.0.0.1:18101/api/auth/sso/exchange";

export type SsoBridgeConfig = { exchangeURL: string; appHost: string };

export function readSsoBridgeConfig(env: Record<string, string | undefined> = process.env): SsoBridgeConfig {
  return {
    exchangeURL: (env.IDENTITY_SSO_EXCHANGE_URL ?? "").trim() || IDENTITY_SSO_EXCHANGE_DEFAULT,
    appHost: readSsoAppHost(env),
  };
}

/** 一次性码只做形状校验：非空、≤512、可见 ASCII；不解码、不落日志。 */
export function opaqueExchangeCode(raw: unknown): string | null {
  if (typeof raw !== "string") return null;
  const code = raw.trim();
  if (code.length === 0 || code.length > 512) return null;
  if (!/^[A-Za-z0-9._~-]+$/.test(code)) return null;
  return code;
}

/** 只接受站内落点（回 www 或非本站一律回 /）。 */
export function safeReturnPath(raw: unknown): string {
  return typeof raw === "string" && /^\/lead-follow-up-return\/hnd_[a-f0-9]{32}$/.test(raw) ? raw : "/";
}

export function sameOriginPost(
  headers: { origin: string | null; secFetchSite: string | null },
  appHost: string,
): boolean {
  const origin = (headers.origin ?? "").trim();
  if (origin === "") return headers.secFetchSite === "same-origin";
  return origin === `https://${appHost}` || origin === `https://www.${appHost}`;
}

export type ExchangeResult = { status: number; setCookie: string[] };

export function exchangeWithIdentity(
  cfg: SsoBridgeConfig,
  code: string,
  clientIP?: string | null,
): Promise<ExchangeResult> {
  const url = new URL(cfg.exchangeURL);
  const body = JSON.stringify({ code });
  return new Promise((resolve, reject) => {
    const req = httpRequest(
      {
        protocol: url.protocol,
        hostname: url.hostname,
        port: url.port === "" ? undefined : Number(url.port),
        path: url.pathname + url.search,
        method: "POST",
        headers: {
          "content-type": "application/json",
          "content-length": Buffer.byteLength(body),
          host: cfg.appHost,
          origin: `https://${cfg.appHost}`,
          accept: "application/json",
          ...(clientIP ? { "x-forwarded-for": clientIP } : {}),
        },
      },
      (res) => {
        const chunks: Buffer[] = [];
        res.on("data", (c: Buffer) => chunks.push(c));
        res.on("end", () => {
          const raw = res.headers["set-cookie"];
          resolve({ status: res.statusCode ?? 502, setCookie: Array.isArray(raw) ? raw : raw ? [raw] : [] });
        });
      },
    );
    req.on("error", reject);
    req.setTimeout(8000, () => req.destroy(new Error("identity timeout")));
    req.write(body);
    req.end();
  });
}
