// douyin.ts: HUI-4585 抖音视频适配的顾客面模型。
//
// 票面:官方 H5 与获准专项两条路线;当前商铺宣传通用投稿未准入=服务端闸门
// refused(无入口/无指引/零落行),本模块只在获准路线的 client_action 到达时
// 渲染类型化操作;用户主动唤起,系统绝不代点;签名/参数以服务端下发的官方
// 形状为准,前端不拼参、不造协议。
//
// 反例红线(本模块自检):
//   - 通用 media/title/body query 不渲染成可执行入口;
//   - store_promo 拒绝面(refused notice)透传,不提供"手动发完领券"替代入口。

import { isHttpsUrl, parseClientAction, type ClientActionView, type HandoffEnvironment } from "./client-action";

export interface DouyinInvokeModel {
  kind: "js_invoke";
  sdk: string;
  shareId: string;
  clientKey: string;
  mediaUrl: string;
  expiresAt: string;
  steps: string[];
  note: string;
  signingGap: string[];
}

export interface DouyinMiniappModel {
  kind: "miniapp_task";
  miniappPath: string;
  shareId: string;
  note: string;
}

export interface DouyinUnavailable {
  kind: "unavailable";
  reason: string;
}

export type DouyinHandoffModel = DouyinInvokeModel | DouyinMiniappModel | DouyinUnavailable;

// buildDouyinHandoff turns the prepared client_action into a typed render
// model. Refused/pending_external 路线不会到达本函数(服务端零落行),但
// 防御性校验仍在:形状不过=unavailable(诚实),绝不伪造入口。
export function buildDouyinHandoff(raw: unknown, _environment: HandoffEnvironment | null): DouyinHandoffModel {
  const action: ClientActionView | null = parseClientAction(raw);
  if (!action) return { kind: "unavailable", reason: "交接参数形状校验未通过；请回到交接页重试" };
  switch (action.type) {
    case "js_media_handoff": {
      if (action.params["sdk"] !== "douyin_h5") {
        return { kind: "unavailable", reason: "抖音官方 H5 参数缺失（sdk=douyin_h5）；本页不拼通用参数" };
      }
      const shareId = action.params["share_id"] ?? "";
      if (!shareId) return { kind: "unavailable", reason: "官方 share_id 关联缺失；请回到交接页重新获取" };
      const mediaUrl = action.params["media_url"] ?? "";
      if (mediaUrl !== "" && !isHttpsUrl(mediaUrl)) {
        return { kind: "unavailable", reason: "素材引用形状不合法；请回到交接页重试" };
      }
      const gaps = (raw as { unsupported_fields?: unknown });
      const gapLines = Array.isArray(gaps?.unsupported_fields)
        ? (gaps.unsupported_fields as unknown[]).filter((v): v is string => typeof v === "string")
        : [];
      return {
        kind: "js_invoke",
        sdk: "douyin_h5",
        shareId,
        clientKey: action.params["client_key"] ?? "",
        mediaUrl,
        expiresAt: action.params["share_expires_at"] ?? "",
        steps: action.steps,
        note: action.note,
        signingGap: gapLines.filter((g) => g.includes("签名")),
      };
    }
    case "open_miniapp": {
      if (action.params["sdk"] !== "douyin_miniapp_task") {
        return { kind: "unavailable", reason: "抖音专项任务路线参数缺失（sdk=douyin_miniapp_task）" };
      }
      if (!action.miniappPath) return { kind: "unavailable", reason: "专项任务小程序路由缺失" };
      return {
        kind: "miniapp_task",
        miniappPath: action.miniappPath,
        shareId: action.params["share_id"] ?? "",
        note: action.note,
      };
    }
    default:
      return { kind: "unavailable", reason: "当前路线不是抖音官方交接形态；请按页面说明操作" };
  }
}

// douyinRefusalCopy passes the server's refused notice through verbatim
// (不做任何「手动发完领券」式的替代入口——拒绝面没有可执行入口)。
export function douyinRefusalCopy(notice: string): string {
  return notice || "官方规范明确排除该场景，本动作不提供此平台的发布交接，也没有可替代的手动发布入口。";
}
