// HUI-1665 触点稳定入口:纯函数层(零 DOM、零 fetch)。
// 服务端契约:server/internal/store/store_touchpoints.go(五态 stageOrder、
// TouchpointOutcome、DecideLink 状态串、B02 TargetStore/TargetLegacyCampaign)
// 与 internal/qrentry(码形闸)、store_storefronts.store_code(sf_+16hex)。
// 纪律:未验证不冒充已部署;白名单之外一律未知语气;跳转只认合法码形;
// B02 门店根跳 /s(解析零活动门控),legacy 卡保持 /c 契约零破坏。

// ---- FEATURE 开关(与 Go config.isTruthy 同纪律;默认 off) ----

export function featureEnabled(raw: unknown): boolean {
  const v = typeof raw === "string" ? raw.trim().toLowerCase() : "";
  return v === "1" || v === "true" || v === "yes" || v === "on";
}

// ---- 码形(与 Go campaign.NewShortcode/ValidShortcode 同源:Crockford base32,12 位) ----

const CODE_ALPHABET = "0123456789ABCDEFGHJKMNPQRSTVWXYZ";
const CODE_LENGTH = 12;

export function isValidEntryCode(code: unknown): code is string {
  if (typeof code !== "string" || code.length !== CODE_LENGTH) return false;
  for (const ch of code) {
    if (!CODE_ALPHABET.includes(ch)) return false;
  }
  return true;
}

// ---- /t 公共解析:白名单跳转分支 ----

// 活动级状态:公共端点对这些状态返回 200 {state, code},交既有 /c 页渲染。
// 别名级状态(touchpoint_disabled/not_found)是 404,绝不跳转。
export const campaignStateList = [
  "available",
  "not_started",
  "expired",
  "paused",
  "draft",
  "ended",
] as const;

export type CampaignState = (typeof campaignStateList)[number];

// B02 目标类型(与 Go 端 store.TargetStore/TargetLegacyCampaign 同源白名单)。
export const touchpointTargetList = ["store", "legacy_campaign"] as const;
export type TouchpointTargetType = (typeof touchpointTargetList)[number];

export interface TouchEntryView {
  state?: unknown;
  code?: unknown;
  campaign_code?: unknown;
  target_type?: unknown;
  store_code?: unknown;
  schema_version?: unknown;
  binding_revision?: unknown;
}

// 门店公共码形状闸(与 Go 端 store_storefronts.store_code 同源:sf_ + 16 hex,
// randHex(8))。跳转只认合法形状,防注入式路径。
export function isValidStoreCode(code: unknown): code is string {
  if (typeof code !== "string") return false;
  if (!code.startsWith("sf_")) return false;
  const hex = code.slice(3);
  if (hex.length !== 16) return false;
  for (const ch of hex) {
    const c = ch.charCodeAt(0);
    const isDigit = c >= 48 && c <= 57;
    const isLowerHex = c >= 97 && c <= 102;
    if (!isDigit && !isLowerHex) return false;
  }
  return true;
}

// 返回要去的同站路径;白名单之外一律 null(调用方不得自行拼路径):
//   - target_type=store + available + 合法门店码 → /s/{store_code}(B02 门店根)
//   - legacy(显式 legacy_campaign 或旧载荷缺失/未知 target_type)→ /c/{code}
//     (旧响应 code 兼容约定保留,旧前端行为零破坏)
export function touchEntryRedirect(view: TouchEntryView | null | undefined): string | null {
  if (view === null || typeof view !== "object") return null;
  const { state, code, target_type, store_code } = view;
  if (target_type === "store") {
    if (state !== "available") return null;
    if (!isValidStoreCode(store_code)) return null;
    return `/s/${store_code}`;
  }
  if (!(campaignStateList as readonly unknown[]).includes(state)) return null;
  if (!isValidEntryCode(code)) return null;
  return `/c/${code}`;
}

// 404 面:顾客可读文案,不泄漏后台细节(uid_hint/角色/租户)。
export function touchEntryFace(state: unknown): string {
  if (state === "not_found") return "这个入口不存在，或已被商家收回。";
  if (state === "touchpoint_disabled") return "这个入口已被商家停用。请联系门店工作人员。";
  return "入口状态未知，请稍后再试。";
}

// ---- 部署五态(与 Go store.StageRegistered~StageDeviceVerified 对齐) ----

export const stageList = [
  "registered",
  "awaiting_write",
  "written_awaiting_readback",
  "readback_done",
  "device_verified",
] as const;

export type TouchpointStage = (typeof stageList)[number];

export function isStage(value: unknown): value is TouchpointStage {
  return (stageList as readonly unknown[]).includes(value);
}

// 单步推进;终态与未知态返回 null(推进与否由服务端裁决,这里只做展示/预校验)。
export function nextStage(current: unknown): TouchpointStage | null {
  const index = stageList.indexOf(current as TouchpointStage);
  if (index < 0 || index === stageList.length - 1) return null;
  return stageList[index + 1];
}

// 部署面文案:只有 device_verified 才允许「已验证」;其余态如实说还没到。
export function stageFace(stage: unknown): string {
  switch (stage) {
    case "registered":
      return "已登记，还没写卡";
    case "awaiting_write":
      return "待写入卡";
    case "written_awaiting_readback":
      return "卡已写入，等待回读确认";
    case "readback_done":
      return "已回读确认，还没在真机打开验证";
    case "device_verified":
      return "已验证：真机打开成功";
    default:
      return "部署状态未知";
  }
}

// ---- 检查态文案(管理面「查看目标」) ----

export function entryStateFace(state: unknown): string {
  switch (state) {
    case "available":
      return "顾客打开会进入活动";
    case "not_started":
      return "活动未开始";
    case "paused":
      return "活动已暂停";
    case "expired":
      return "活动已过期";
    case "draft":
      return "活动还是草稿，未发布";
    case "ended":
      return "活动已结束";
    case "touchpoint_disabled":
      return "稳定入口已停用";
    case "not_found":
      return "入口不存在";
    default:
      return "状态未知";
  }
}

// ---- B02 目标类型文案(M8 目标列 + 检查态按目标类型措辞) ----

// 目标列:store 根是门店首页,legacy 是活动页;未知不冒充。
export function targetFace(targetType: unknown): string {
  if (targetType === "store") return "门店首页";
  if (targetType === "legacy_campaign") return "活动页";
  return "目标未知";
}

// 检查态文案按目标类型区分:store 根的 available 是进门店首页(不是进活动);
// 其余状态沿用活动态措辞(停用/不存在/未知不冒充可用)。
export function entryFace(targetType: unknown, state: unknown): string {
  if (targetType === "store" && state === "available") {
    return "顾客打开会进入门店首页";
  }
  return entryStateFace(state);
}

// ---- 换绑影响文案(含 3546「已开始参与数」只读统计) ----

export function rebindImpactFace(startedParticipations: number): string {
  const started = Number.isFinite(startedParticipations) && startedParticipations > 0
    ? Math.floor(startedParticipations)
    : 0;
  if (started === 0) {
    return "目前没有已开始参与的顾客。换绑只改这张卡的指向，链接不变。";
  }
  return `已有 ${started} 位顾客开始参与，他们的参与和奖励不受影响；换绑只改这张卡之后的新访客指向，链接不变。`;
}

// ---- 旧路径共享停用确认文案(tag 无别名时走旧端点,传播必须先说清) ----

export function sharedStopFace(sharedCount: number): string {
  const count = Number.isFinite(sharedCount) && sharedCount > 0 ? Math.floor(sharedCount) : 1;
  return `这个短码被 ${count} 张卡共享：停用标签会把这些卡上的入口一起停掉。已启用稳定入口的卡不受此操作影响，建议改用稳定入口逐卡控制。`;
}
