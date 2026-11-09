// HUI-4589 门店服务配置页（/work/stores/{id}/services）纯逻辑。
//
// 纪律：
//   - B07 连接域三类：Wi-Fi（SSID/密码/加密方式——密码是商家主动公示的连接
//     凭据，顾客侧仅展示+主动复制，不强制留资）、加微信（个人 wechat_id/二维码
//     vs 企微官方「联系我」链接）、社群（群号/邀请链接/二维码+审批备注如实透传）。
//   - 保存永远带 expected_revision（服务端 config_revision 乐观锁），409 冲突
//     是独立剧本（刷新重试，不静默覆盖）——同 4584 channels 页。
//   - 企微回调资质状态诚实展示：pending_qualification 绝不显示成已配置。
//   - 本页绝不存储或展示 AppSecret 类凭据；回调凭据由部署侧 env 持有。

export interface ServiceItem {
  service_id: string;
  service_revision?: number;
  revision?: number;
  catalog_code: string;
  kind: "wifi" | "contact" | "community" | "";
  variant: string;
  label: string;
  config: Record<string, string>;
  order: number;
  enabled: boolean;
  /** 页面内派生：保存前的逐行阻塞原因（空串 = 无阻塞）。 */
  blocker?: string;
}

export interface WecomCallbackState {
  state?: string;
  note?: string;
}

export interface ServicesDoc {
  store_id?: string;
  store_name?: string;
  store_code?: string;
  config_revision?: number;
  wecom_callback?: WecomCallbackState;
  items?: unknown;
}

export const SERVICE_KINDS = [
  { value: "wifi", label: "门店 Wi-Fi（A17）" },
  { value: "contact", label: "加微信（A18）" },
  { value: "community", label: "社群/公众号（A16/A29）" },
] as const;

export const WIFI_ENCRYPTIONS = [
  { value: "open", label: "开放（无密码）" },
  { value: "wpa", label: "WPA" },
  { value: "wpa2", label: "WPA2" },
  { value: "wpa2_wpa3", label: "WPA2/WPA3 混合" },
  { value: "wpa3", label: "WPA3" },
] as const;

/** 行编辑的 config 草稿形态：全部字符串键值（页面输入天然字符串）。 */
export function emptyConfig(kind: string): Record<string, string> {
  switch (kind) {
    case "wifi":
      return { ssid: "", password: "", encryption: "wpa2" };
    case "contact":
      return { url: "", wechat_id: "", qr_ref: "", source_note: "" };
    case "community":
      return { group_no: "", invite_url: "", qr_ref: "", approval_note: "" };
    default:
      return {};
  }
}

/** 服务行摘要（列表展示）：不展示完整密码，只给「已设置」状态与关键标识。 */
export function serviceSummary(item: ServiceItem): string {
  switch (item.kind) {
    case "wifi": {
      const ssid = (item.config.ssid ?? "").trim();
      return ssid !== "" ? `Wi-Fi 名称：${ssid}（密码已设置）` : "Wi-Fi 名称未填";
    }
    case "contact": {
      if (item.variant === "wecom") {
        const url = (item.config.url ?? "").trim();
        return url !== "" ? `企微「联系我」链接：${hostSummary(url)}` : "企微链接未填";
      }
      const id = (item.config.wechat_id ?? "").trim();
      const qr = (item.config.qr_ref ?? "").trim();
      if (id !== "" && qr !== "") return `微信号：${id} + 二维码`;
      if (id !== "") return `微信号：${id}`;
      if (qr !== "") return `二维码：${hostSummary(qr)}`;
      return "微信号/二维码至少填一项";
    }
    case "community": {
      const parts: string[] = [];
      const group = (item.config.group_no ?? "").trim();
      if (group !== "") parts.push(`群号 ${group}`);
      const invite = (item.config.invite_url ?? "").trim();
      if (invite !== "") parts.push(`邀请链接 ${hostSummary(invite)}`);
      const note = (item.config.approval_note ?? "").trim();
      if (note !== "") parts.push(`状态：${note}`);
      return parts.length > 0 ? parts.join(" · ") : "群号/邀请链接/二维码至少填一项";
    }
    default:
      return "未选择类型";
  }
}

function hostSummary(raw: string): string {
  try {
    const parsed = new URL(raw);
    return parsed.host;
  } catch {
    return "（地址不完整）";
  }
}

/** 保存前逐行阻塞：启用行必须有 kind 与按 kind 的必填项（与服务端校验同口径）。 */
export function normalizeServiceItems(items: ServiceItem[]): ServiceItem[] {
  return items.map((item) => {
    let blocker = "";
    const cfg = item.config ?? {};
    const str = (key: string) => (cfg[key] ?? "").trim();
    if (item.kind === "") blocker = "请先选择服务类型";
    else if (item.kind === "wifi") {
      if (str("ssid") === "") blocker = "启用前需要填写 Wi-Fi 名称";
      else if (str("encryption") === "open") {
        if (str("password") !== "") blocker = "开放网络不留密码";
      } else if (str("password").length < 8) blocker = "非开放网络需要 8-64 位密码";
    } else if (item.kind === "contact") {
      if (item.variant === "wecom") {
        if (str("url") === "") blocker = "企微需要官方「联系我」链接";
        else if (!isOfficialWecomURL(str("url"))) blocker = "必须是 work.weixin.qq.com 官方链接";
      } else if (str("wechat_id") === "" && str("qr_ref") === "") blocker = "微信号/二维码至少填一项";
      else if (str("wechat_id") !== "" && !/^[A-Za-z][A-Za-z0-9_-]{5,19}$/.test(str("wechat_id"))) blocker = "微信号形状不正确（字母开头 6-20 位）";
    } else if (item.kind === "community") {
      if (str("group_no") === "" && str("invite_url") === "" && str("qr_ref") === "") blocker = "群号/邀请链接/二维码至少填一项";
    }
    return { ...item, blocker };
  });
}

export function isOfficialWecomURL(raw: string): boolean {
  try {
    const parsed = new URL(raw);
    if (parsed.protocol !== "https:") return false;
    const host = parsed.hostname.toLowerCase();
    return host === "work.weixin.qq.com" || host.endsWith(".work.weixin.qq.com");
  } catch {
    return false;
  }
}

/** PUT 负载：只带契约字段；order 规整为 1..n 连续。 */
export function buildServicesPutBody(items: ServiceItem[], expectedRevision: number): {
  expected_revision: number;
  items: Array<{
    service_id: string;
    catalog_code: string;
    kind: string;
    variant: string;
    label: string;
    config: Record<string, string>;
    order: number;
    enabled: boolean;
  }>;
} {
  const sorted = [...items].sort((a, b) => a.order - b.order);
  return {
    expected_revision: expectedRevision,
    items: sorted.map((item, index) => {
      const config: Record<string, string> = {};
      for (const [key, value] of Object.entries(item.config ?? {})) {
        config[key] = (value ?? "").trim();
      }
      // open 网络密码规范化为空串（与服务端同口径）。
      if (item.kind === "wifi" && (config.encryption ?? "") === "open") {
        config.password = "";
      }
      return {
        service_id: item.service_id || "",
        catalog_code: item.catalog_code || "",
        kind: item.kind,
        variant: item.variant || "",
        label: (item.label || "").slice(0, 200),
        config,
        order: index + 1,
        enabled: item.enabled,
      };
    }),
  };
}

/** 保存失败分流：409 是独立剧本（冲突→刷新），其余普通失败。 */
export function saveServicesFailure(status: number, data: Record<string, unknown>): { kind: "conflict" | "error"; text: string } {
  const code = typeof data.error === "string" ? data.error : "";
  if (status === 409 || code === "stale_version") {
    return { kind: "conflict", text: "配置已被他人更新。请点「刷新最新配置」，在最新版本上继续修改（这次修改不会自动带上）。" };
  }
  return { kind: "error", text: servicesSaveError(status, data) };
}

export function servicesSaveError(status: number, data: Record<string, unknown>): string {
  const code = typeof data.error === "string" ? data.error : "";
  switch (code) {
    case "stale_version":
      return "配置已被他人更新。请刷新页面后重试，避免覆盖别人的修改。";
    case "duplicate_service_id":
      return "同一个服务出现了两次，请刷新后重试。";
    case "unknown_service_id":
      return "有服务刚被删除。请刷新页面后再保存。";
    case "bad_service_config":
      return `服务信息没有通过校验：${typeof data.message === "string" ? data.message : "请检查必填项后重试。"}`;
    case "bad_label":
      return "名称太长了，请缩短后保存。";
    case "forbidden":
      return "你只能配置自己管辖的门店。";
    case "not_found":
      return "门店不存在或已被停用。";
    default:
      return status >= 500 ? "没有保存成功，请重试。" : "没有保存成功，请检查后重试。";
  }
}

/** 企微回调资质徽章文案：待资质诚实态绝不显示为已配置。 */
export function wecomCallbackBadge(cb: WecomCallbackState | undefined): { text: string; tone: "warn" | "ok" } {
  if (cb && cb.state === "configured") {
    return { text: "企微回调：已配置（回调仅作证据留存，添加结果以企业微信官方为准）", tone: "ok" };
  }
  return {
    text: "企微回调：待资质——凭据未配置，URL 验证无法完成；资质核实前不记录任何「客户已添加」。",
    tone: "warn",
  };
}

/** 保存闸：已知落后（stale）或刷新中一律不允许保存——先刷新对齐版本号。 */
export function canSaveServices(state: { stale: boolean; reloading: boolean; dirty: boolean; blockers: number }): boolean {
  return !state.stale && !state.reloading && state.dirty && state.blockers === 0;
}
