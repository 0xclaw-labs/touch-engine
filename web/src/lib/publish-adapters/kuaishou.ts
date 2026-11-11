// kuaishou.ts: HUI-4587 快手网站 OAuth 适配的顾客面模型。
//
// 票面:授权服务端投稿路线——OAuth 授权后回到同一 action_session,顾客再次
// 确认本次内容才提交;不弹出原生编辑器;异步接受≠最终公开(photo_id 回查)。
// secret 永不出服务器:授权 URL 只携带 client_id/scope/state/redirect_uri。
//
// 回跳:GET /api/v1/public/handoff-callbacks/kuaishou/oauth 处理 code+state
// 后 302 回本页(同源 https);本模块解析回跳旗标(ok/denied)并给出诚实文案。

import { isHttpsUrl, parseClientAction, type ClientActionView } from "./client-action";

export interface KuaishouAuthorizeModel {
  kind: "authorize";
  authorizeUrl: string;
  state: string;
  note: string;
}

export interface KuaishouConfirmModel {
  kind: "confirm_submit";
  accountRef: string;
  note: string;
}

export interface KuaishouUnavailable {
  kind: "unavailable";
  reason: string;
}

export type KuaishouModel = KuaishouAuthorizeModel | KuaishouConfirmModel | KuaishouUnavailable;

export interface KuaishouOAuthReturn {
  outcome: "ok" | "denied" | "none";
  handoffId: string;
  reason: string;
}

// buildKuaishouModel turns the prepared client_action into the typed model.
export function buildKuaishouModel(raw: unknown): KuaishouModel {
  const action: ClientActionView | null = parseClientAction(raw);
  if (!action) return { kind: "unavailable", reason: "交接参数形状校验未通过；请回到交接页重试" };
  if (action.type !== "oauth_consent") {
    return { kind: "unavailable", reason: "当前路线不是快手授权投稿形态" };
  }
  const phase = action.params["phase"];
  if (phase === "authorize") {
    const url = action.url;
    if (!isHttpsUrl(url)) return { kind: "unavailable", reason: "授权地址形状不合法；本页不跳转" };
    const authorize = new URL(url);
    // secret 永不出服务器:授权 URL 出现 secret 类参数=拒绝渲染(自检)。
    for (const banned of ["client_secret", "secret", "access_token"]) {
      if (authorize.searchParams.has(banned)) {
        return { kind: "unavailable", reason: "授权地址携带了不该出现的参数；本页拒绝跳转" };
      }
    }
    if (!authorize.searchParams.get("state") || !authorize.searchParams.get("client_id")) {
      return { kind: "unavailable", reason: "授权参数缺失（state/client_id）；请回到交接页重试" };
    }
    return {
      kind: "authorize",
      authorizeUrl: url,
      state: authorize.searchParams.get("state") ?? "",
      note: action.note,
    };
  }
  if (phase === "confirm_submit") {
    return { kind: "confirm_submit", accountRef: action.params["account_ref"] ?? "", note: action.note };
  }
  return { kind: "unavailable", reason: "授权阶段未知；请回到交接页重试" };
}

// parseKuaishouOAuthReturn reads the redirect-back flags on the customer page
// (oauth=ok|denied&reason=…&handoff_id=…).
export function parseKuaishouOAuthReturn(search: string): KuaishouOAuthReturn {
  let params: URLSearchParams;
  try {
    params = new URLSearchParams(search.startsWith("?") ? search.slice(1) : search);
  } catch {
    return { outcome: "none", handoffId: "", reason: "" };
  }
  const flag = params.get("oauth");
  if (flag === "ok") {
    return { outcome: "ok", handoffId: params.get("handoff_id") ?? "", reason: "" };
  }
  if (flag === "denied") {
    return { outcome: "denied", handoffId: params.get("handoff_id") ?? "", reason: params.get("reason") ?? "" };
  }
  return { outcome: "none", handoffId: "", reason: "" };
}

export function kuaishouDeniedCopy(): string {
  return "授权没有完成或已失效；请回到交接页重新发起授权。素材选择不会因此丢失。";
}

export function kuaishouAsyncCopy(): string {
  return "快手接受投稿后是异步处理：系统以 photo_id 回查官方状态；接受不等于最终公开，审核中/失败都会如实展示。";
}
