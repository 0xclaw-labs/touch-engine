import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

/**
 * 允许列表来自已合并的 HUI-2228；登记真源自 HUI-2227（残差②）改为 public-ai
 * production-manifest.json（生产实况，逐字节 vendored，见 registry/SOURCE.txt）。
 * 登记地址只有通过与 econavmanifest.emittable 相同的检查才返回。
 * .invalid、loopback、带端口的登记地址不返回；生产表没有的行诚实缺失（null）。
 * 测试品牌只能把 ti-product-image-web 指到本机产品图，不能换成任意公网地址。
 */

const PRODUCT_IMAGE_TARGET = "ti-product-image-web";
const BLOCKED_QUERY_KEYS = ["order_id", "handoff_id", "claim_code", "stage_id", "brief_version", "campaign_id"];

type LaunchRow = { target_id?: string; kind?: string; url?: string };
type RegistryApp = { launch_targets?: LaunchRow[] };
type RegistryFile = { apps?: RegistryApp[] };

export type AllowlistEnv = {
  TOUCH_ECO_NAV_TEST_BRAND?: string;
  TOUCH_ECO_NAV_TEST_PRODUCT_IMAGE_URL?: string;
};

const registryDir = join(dirname(fileURLToPath(import.meta.url)), "registry");

function readRegistry(name: string): RegistryFile {
  return JSON.parse(readFileSync(join(registryDir, name), "utf8")) as RegistryFile;
}

function indexLaunchTargets(files: RegistryFile[]): Map<string, string> {
  const out = new Map<string, string>();
  for (const file of files) {
    for (const app of file.apps ?? []) {
      for (const target of app.launch_targets ?? []) {
        if (target.kind !== "launch" || !target.target_id || !target.url) continue;
        if (out.has(target.target_id)) {
          out.set(target.target_id, "");
          continue;
        }
        out.set(target.target_id, target.url);
      }
    }
  }
  return out;
}

const registeredTargets = indexLaunchTargets([readRegistry("production-manifest.json")]);

export function emittable(raw: string): boolean {
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    return false;
  }
  // 与 public-ai internal/econavmanifest emittable 同口径：https、无凭据/端口/
  // query/fragment、非空路径（根路径 "/" 合法——生产注册表含根路径登记）、
  // host 非 loopback/invalid。
  if (url.protocol !== "https:" || url.username || url.password || url.port || url.search || url.hash || url.pathname === "") {
    return false;
  }
  const host = url.hostname.toLowerCase();
  if (host === "localhost" || host === "127.0.0.1" || host === "::1" || host === "invalid" || host.endsWith(".invalid")) {
    return false;
  }
  return true;
}

function blockedQuery(url: URL): boolean {
  return BLOCKED_QUERY_KEYS.some((key) => url.searchParams.has(key));
}

export function testProductImageUrl(raw: string | undefined): string | null {
  if (!raw) return null;
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    return null;
  }
  if (url.protocol !== "http:" && url.protocol !== "https:") return null;
  const host = url.hostname.toLowerCase();
  if (host !== "127.0.0.1" && host !== "localhost") return null;
  if (blockedQuery(url)) return null;
  return url.toString();
}

export function resolveRegisteredLaunch(targetId: string | null | undefined, env: AllowlistEnv = process.env as AllowlistEnv): string | null {
  if (!targetId) return null;
  if (env.TOUCH_ECO_NAV_TEST_BRAND === "1" && targetId === PRODUCT_IMAGE_TARGET) {
    return testProductImageUrl(env.TOUCH_ECO_NAV_TEST_PRODUCT_IMAGE_URL);
  }
  const raw = registeredTargets.get(targetId);
  if (!raw || !emittable(raw)) return null;
  return raw;
}

export function registeredLaunchMap(env: AllowlistEnv = process.env as AllowlistEnv): Record<string, string> {
  const out: Record<string, string> = {};
  for (const targetId of registeredTargets.keys()) {
    const href = resolveRegisteredLaunch(targetId, env);
    if (href) out[targetId] = href;
  }
  const testHref = resolveRegisteredLaunch(PRODUCT_IMAGE_TARGET, env);
  if (testHref) out[PRODUCT_IMAGE_TARGET] = testHref;
  return out;
}
