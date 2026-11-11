// HUI-1663 实体交付:纯函数层(零 DOM、零 fetch)。
// 服务端契约:server/internal/delivery(七步状态机/回读核对)与
// store_touchpoint_deliveries.go(DeliveryView/status 白名单)。
// 纪律:未到交付绝不显示「已交付」;绑定版本≠实测版本如实警示 stale;
// 回读不一致(matches=false)只说事实,绝不冒充已验证;白名单之外一律未知语气。

// ---- 交付状态(与 Go delivery 包七步+superseded 同源白名单) ----

export const deliveryStatusList = [
  "awaiting_dispatch",
  "dispatched",
  "bound",
  "written",
  "readback",
  "accepted",
  "delivered",
] as const;

export type DeliveryStatus = (typeof deliveryStatusList)[number];

export function isDeliveryStatus(value: unknown): value is DeliveryStatus {
  return (deliveryStatusList as readonly unknown[]).includes(value);
}

// 单步推进;终态与未知态返回 null(推进与否由服务端裁决,这里只做展示/预校验)。
export function deliveryNextStep(status: unknown): DeliveryStatus | null {
  const index = deliveryStatusList.indexOf(status as DeliveryStatus);
  if (index < 0 || index === deliveryStatusList.length - 1) return null;
  return deliveryStatusList[index + 1];
}

// 步骤名(= 事件类型)文案:出库/绑定/写卡/回读/现场验收/交付。
export function deliveryEventFace(eventType: unknown): string {
  switch (eventType) {
    case "dispatched":
      return "出库";
    case "bound":
      return "绑定确认";
    case "written":
      return "写卡";
    case "readback":
      return "回读";
    case "accepted":
      return "现场验收";
    case "delivered":
      return "交付";
    case "replaced":
      return "补换";
    default:
      return "未知事件";
  }
}

// 状态文案:只有 delivered 才说「已交付」;superseded 如实说被补换取代;
// 其余态一律说清还没到哪一步,绝不冒充已验证。
export function deliveryStatusFace(status: unknown): string {
  switch (status) {
    case "awaiting_dispatch":
      return "付款已确认,等待出库登记";
    case "dispatched":
      return "已出库,等待绑定确认";
    case "bound":
      return "已绑定,等待写卡";
    case "written":
      return "卡已写入,等待回读核对";
    case "readback":
      return "回读一致,还没现场验收";
    case "accepted":
      return "现场验收完成,等待确认交付";
    case "delivered":
      return "已交付:七步事实齐备";
    case "superseded":
      return "已被补换新卡取代(历史记录)";
    default:
      return "交付状态未知";
  }
}

// ---- 绑定版本与 stale(换绑/换域后不得沿用旧「已验证」) ----

export interface BindingRevisionFace {
  current?: number;
  bound?: number;
  verified?: number;
}

// stale 判定与服务端同源:bound/verified 任一非零且 ≠ 当前 → stale。
export function isBindingStale(face: BindingRevisionFace): boolean {
  const { current, bound, verified } = face;
  if (typeof current !== "number") return false;
  if (typeof bound === "number" && bound > 0 && bound !== current) return true;
  if (typeof verified === "number" && verified > 0 && verified !== current) return true;
  return false;
}

// stale 展示:绑定已变更时明说「本次实测不适用于当前绑定」,不冒充已验证。
export function bindingStaleFace(face: BindingRevisionFace): string {
  if (!isBindingStale(face)) {
    return `当前绑定版本 ${face.current ?? 0} · 绑定时 ${face.bound ?? 0} · 最后实测 ${face.verified ?? 0}`;
  }
  return `触点绑定已变更(当前版本 ${face.current ?? 0},绑定确认时 ${face.bound ?? 0},最后实测 ${face.verified ?? 0}):本次实测不适用于当前绑定,不能当作已验证。`;
}

// ---- 回读一致性(错误域名/错误码不得当已验证) ----

export function readbackFace(matches: unknown, reason: unknown): string {
  if (matches === true) {
    return "回读一致:卡内 URL 与绑定触点逐字符相符。";
  }
  if (matches === false) {
    switch (reason) {
      case "host_mismatch":
        return "回读不一致:域名不对。请检查卡内写入的地址,重新写卡后再回读。";
      case "code_mismatch":
        return "回读不一致:码不对。这张卡写的内容和这个触点不一致,请重新写卡或换卡。";
      case "empty_read":
        return "回读内容为空:请把回读工具读到的 URL 填进来再核对。";
      default:
        return "回读不一致:请重新写卡后再回读。";
    }
  }
  return "还没有回读记录。";
}

// ---- 补换 ----

export const replaceReasonList = ["damaged_recoverable", "lost_uncontrolled"] as const;
export type ReplaceReason = (typeof replaceReasonList)[number];

export function isReplaceReason(value: unknown): value is ReplaceReason {
  return (replaceReasonList as readonly unknown[]).includes(value);
}

// 补换措辞:区分可回收损坏(保留逻辑入口)与遗失失控(撤销旧入口)。
export function replaceReasonFace(category: unknown): string {
  if (category === "damaged_recoverable") {
    return "损坏可回收:换一张新卡写同一个入口,链接不变。";
  }
  if (category === "lost_uncontrolled") {
    return "遗失失控:旧入口立即停用,新卡换新入口新码。";
  }
  return "补换原因未知";
}
