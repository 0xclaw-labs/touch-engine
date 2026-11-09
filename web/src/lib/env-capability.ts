// HUI-1896 环境差异化与 3535 能力路线选择(纯函数,零 React,零 fetch)。
//
// 消费 GET /api/v1/public/actions/{id}/capabilities(FEATURE_CHANNEL_ADMISSION,
// sa_* 实例 id 直查,tenant 由服务端反查)。纪律(3535 合同 §6/裁定书):
//   - 六环境枚举与 Go channeladmission.Environments 一字不差;target_app 不由
//     UA 判定(应用内 webview 无可靠指纹,不猜)——只在调用方确知身处目标 App
//     时显式传入;
//   - pending_external=true 永不自动降级为 manual 许可(unknown 不当许可);
//   - allowed+promised 才是「可以走」的路线;refused 给 reason_text;
//     emergency_disabled 是独立否决;
//   - reward 判定不进公开面:客户端永不传 reward 偏好,奖励语义由消费方
//     服务端按动作性质判定(本模块只做展示选择)。

export type Environment =
  | "desktop_browser"
  | "ios_safari"
  | "android_browser"
  | "ios_wechat"
  | "android_wechat"
  | "target_app";

export interface CapabilityRoute {
  platform: string;
  method: string;
  environment: string;
  scenario: string;
  app_binding: string;
  status: string;
  allowed: boolean;
  promised: boolean;
  pending_external: boolean;
  reason: string;
  reason_text: string;
  navigation: string;
  media_handoff: string;
  platform_receipt: string;
  reward_eligibility: string;
  field_support: Record<string, string>;
  required_inputs: string[];
  missing: string[];
  official_sources: string[];
  evidence_grade: string;
  checked_at: string;
  valid_until: string;
  emergency_disabled: boolean;
  close_reason: string;
}

export interface ActionCapabilitiesView {
  actionId: string;
  catalogCode: string;
  actionKind: string;
  label: string;
  routes: CapabilityRoute[];
}

function str(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function bool(value: unknown): boolean {
  return value === true;
}

function parseRoutes(raw: unknown): CapabilityRoute[] {
  const items = Array.isArray(raw) ? raw : [];
  const out: CapabilityRoute[] = [];
  for (const item of items) {
    if (item === null || typeof item !== "object") continue;
    const rec = item as Record<string, unknown>;
    if (str(rec.environment) === "" || str(rec.method) === "") continue;
    out.push({
      platform: str(rec.platform),
      method: str(rec.method),
      environment: str(rec.environment),
      scenario: str(rec.scenario),
      app_binding: str(rec.app_binding),
      status: str(rec.status),
      allowed: bool(rec.allowed),
      promised: bool(rec.promised),
      pending_external: bool(rec.pending_external),
      reason: str(rec.reason),
      reason_text: str(rec.reason_text),
      navigation: str(rec.navigation),
      media_handoff: str(rec.media_handoff),
      platform_receipt: str(rec.platform_receipt),
      reward_eligibility: str(rec.reward_eligibility),
      field_support: (rec.field_support && typeof rec.field_support === "object" ? rec.field_support : {}) as Record<string, string>,
      required_inputs: Array.isArray(rec.required_inputs) ? rec.required_inputs.filter((s): s is string => typeof s === "string") : [],
      missing: Array.isArray(rec.missing) ? rec.missing.filter((s): s is string => typeof s === "string") : [],
      official_sources: Array.isArray(rec.official_sources) ? rec.official_sources.filter((s): s is string => typeof s === "string") : [],
      evidence_grade: str(rec.evidence_grade),
      checked_at: str(rec.checked_at),
      valid_until: str(rec.valid_until),
      emergency_disabled: bool(rec.emergency_disabled),
      close_reason: str(rec.close_reason),
    });
  }
  return out;
}

/** 白名单解析 3535 公开投影;缺 routes / 非对象 → null。 */
export function parseActionCapabilities(payload: unknown): ActionCapabilitiesView | null {
  if (payload === null || typeof payload !== "object") return null;
  const rec = payload as Record<string, unknown>;
  const routes = parseRoutes(rec.routes);
  if (!Array.isArray(rec.routes)) return null;
  return {
    actionId: str(rec.action_id),
    catalogCode: str(rec.catalog_code),
    actionKind: str(rec.action_kind),
    label: str(rec.label),
    routes,
  };
}

// ---- 环境检测 ------------------------------------------------------------------

/** UA → 六环境。target_app 只能由调用方显式声明(inApp=true),UA 不猜。 */
export function detectEnvironment(ua: string, inApp = false): Environment {
  if (inApp) return "target_app";
  const agent = (ua ?? "").toLowerCase();
  if (agent === "") return "desktop_browser";
  const isMobile = /iphone|ipad|ipod|android|mobile/.test(agent);
  if (!isMobile) return "desktop_browser";
  const wechat = agent.includes("micromessenger");
  const ios = /iphone|ipad|ipod/.test(agent);
  if (wechat) return ios ? "ios_wechat" : "android_wechat";
  return ios ? "ios_safari" : "android_browser";
}

// ---- 路线选择 ------------------------------------------------------------------

export type RoutePickKind = "ok" | "pending" | "refused" | "disabled";

export interface RoutePick {
  kind: RoutePickKind;
  route: CapabilityRoute | null;
  /** 顾客可读的路线状态文案(唯一出口;不编造可完成态)。 */
  face: string;
}

const NO_ROUTE_FACE = "这个平台在当前设备上的路线暂未确认。可以先看门店内容,发布方式请听门店现场安排。";

/**
 * 从当前环境的 routes 里挑一条诚实路线:
 *   1. emergency_disabled 优先如实报告(独立否决);
 *   2. allowed && !pending_external(可走)里 promised 优先;
 *   3. pending_external=true → pending(平台确认中,不承诺);
 *   4. 全部 refused → refused(reason_text);
 *   5. 本环境无记录 → pending(未知语气,不冒充可用)。
 */
export function pickRoute(routes: CapabilityRoute[], environment: string): RoutePick | null {
  const env = (environment ?? "").trim();
  if (env === "") return null;
  const here = routes.filter((r) => r.environment === env);
  if (here.length === 0) return { kind: "pending", route: null, face: NO_ROUTE_FACE };

  for (const r of here) {
    if (r.emergency_disabled) {
      // 独立否决语义:只停本路线,不影响其他动作/环境(文案用「停用」而非全店暂停)。
      const why = r.close_reason !== "" ? `(${r.close_reason})` : "";
      return { kind: "disabled", route: r, face: `这一步暂时停用${why}。可以请店员协助,或稍后再试。` };
    }
  }
  const walkable = here.filter((r) => r.allowed && !r.pending_external);
  const promised = walkable.find((r) => r.promised);
  if (promised) return { kind: "ok", route: promised, face: `当前设备可以走「${methodLabel(promised.method)}」路线。` };
  const walkableAny = walkable[0];
  if (walkableAny) return { kind: "ok", route: walkableAny, face: `当前设备可以走「${methodLabel(walkableAny.method)}」路线(以平台实际页面为准)。` };
  const pending = here.find((r) => r.pending_external);
  if (pending) {
    const why = pending.reason_text !== "" ? pending.reason_text : "平台路线还在确认中";
    return { kind: "pending", route: pending, face: `${why}。这一步现在不能承诺完成,可以先看内容或请店员协助。` };
  }
  const refused = here[0];
  const why = refused.reason_text !== "" ? refused.reason_text : "该路线在这里未放行";
  return { kind: "refused", route: refused, face: `${why}。可以先看活动和内容,发布方式请听门店现场安排。` };
}

/** 交接方法 → 真实说明(八种方法逐一给文案;未知方法给保守现场文案)。 */
export function methodLabel(method: string): string {
  switch ((method ?? "").trim()) {
    case "h5_share":
      return "平台页面分享投稿";
    case "miniapp":
      return "小程序发布";
    case "js_media_handoff":
      return "平台官方页媒体交接";
    case "oauth_server_push":
      return "平台授权后由服务发布";
    case "native_sdk":
      return "应用内发布";
    case "link_share":
      return "纯链接分享";
    case "manual_material":
      return "保存素材后手动发布";
    case "direct_open":
      return "直接打开平台页面";
    default:
      return "按门店现场方式";
  }
}
