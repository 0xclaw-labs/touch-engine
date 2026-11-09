// HUI-4589 顾客承接面板（C3 服务模板）纯逻辑。
//
// 消费方：1896 顾客六视图 C3（按 actionId 复用面板组件，可地址化）。
// 数据源：GET /api/v1/public/storefronts/{code}/services。
//
// 纪律（裁定书 + SHARED-BRIEF 第 6/7 条）：
//   - 点评面板：零模板零AIGC零截图核验零奖励触发（UI 侧第二道拦）——面板无
//     输入框、无生成按钮、无截图上传；文案明示四不。
//   - 一切面板零成功话术：不存在「已关注/已连接/已添加/已点评」状态显示；
//     点击/复制只作为行为证据上报，回执文案永远带否定。
//   - Wi-Fi 密码仅展示+主动复制（无留资门）；连接失败→手动指引回退（T12）。
//   - 微信面板按环境给识别步骤（T13）；二维码只渲染公网 https 引用。

export interface ServiceRoute {
  method?: string;
  environment?: string;
  allowed?: boolean;
  promised?: boolean;
  pending_external?: boolean;
  reason?: string;
  navigation?: string;
  platform_receipt?: string;
  reward_eligibility?: string;
}

export interface ServiceGuidance {
  title?: string;
  steps?: string[];
  notices?: string[];
}

export interface ServiceEntry {
  service_id?: string;
  kind?: string; // review | follow | group | wifi | contact
  catalog_code?: string;
  platform?: string;
  variant?: string;
  label?: string;
  target_url?: string;
  admission?: string; // promised | pending_external | blocked | unknown
  routes?: ServiceRoute[];
  guidance?: ServiceGuidance;
  wifi?: { ssid?: string; password?: string; encryption?: string };
  qr_url?: string;
  wechat_id?: string;
  wecom_url?: string;
  source_note?: string;
  group_no?: string;
  invite_url?: string;
  approval_note?: string;
}

export type PanelKind = "review" | "follow" | "group" | "wifi" | "contact" | "";

/** 面板可渲染判定：缺 service_id/未知 kind 的行不渲染（不出现死面板）。 */
export function panelKind(entry: ServiceEntry): PanelKind {
  if (!entry || typeof entry.service_id !== "string" || entry.service_id === "") return "";
  switch (entry.kind) {
    case "review":
    case "follow":
    case "group":
    case "wifi":
    case "contact":
      return entry.kind;
    default:
      return "";
  }
}

/**
 * 环境感知提示（T13）：微信内/浏览器/其他给不同的二维码识别步骤。
 * env 是 3535 六环境口径；未知环境给通用步骤（不猜）。
 */
export function contactSteps(environment: string): string[] {
  if (environment === "ios_wechat" || environment === "android_wechat") {
    return ["长按二维码识别，或截图后在微信扫一扫中打开", "也可以复制微信号后在微信搜索添加"];
  }
  if (environment === "ios_safari" || environment === "android_browser" || environment === "desktop_browser") {
    return ["截图保存二维码，打开微信扫一扫识别", "或复制微信号后打开微信搜索添加"];
  }
  return ["用微信扫描或识别二维码", "或复制微信号后在微信搜索添加"];
}

/**
 * T12 Wi-Fi 手动回退：连接失败时的手动指引步骤（含展示字段核对与店员兜底）。
 * 密码已在面板展示；回退指引不索要任何留资。
 */
export function wifiFallbackSteps(): string[] {
  return [
    "核对 Wi-Fi 名称与密码是否完整复制（注意大小写与结尾字符）",
    "在系统 Wi-Fi 设置中删除该网络后重新连接一次",
    "仍然连不上时，请直接询问店员获取当前正确的连接信息",
  ];
}

/** 点评面板的四不声明（UI 侧第二道拦的文案锚点）。 */
export function reviewNotices(): string[] {
  return [
    "本入口不提供评价内容，没有模板，也不会自动生成任何评价文字。",
    "不需要提交评价截图，评价不以截图核验为准。",
    "评价与奖励完全无关：点不点评、怎么点评，都不会影响你在这里的任何权益。",
  ];
}

/** 关注面板的未安装手动步骤。 */
export function followSteps(entry: ServiceEntry): string[] {
  const platform = (entry.platform ?? "").trim();
  const head = platform !== "" ? `打开${platformLabel(platform)}内本店账号的主页` : "打开账号主页链接";
  return [head, "在平台内自行点击关注；未安装该平台时，到应用商店安装后搜索同名账号"];
}

function platformLabel(platform: string): string {
  const labels: Record<string, string> = {
    douyin: "抖音", kuaishou: "快手", xiaohongshu: "小红书", bilibili: "B站",
    weibo: "微博", wechat: "微信", qq: "QQ", dianping: "大众点评",
    amap: "高德", baidu: "百度", meituan: "美团",
  };
  return labels[platform] ?? platform;
}

/**
 * 面板顶部能力提示行：admission + routes 的诚实翻译。
 * 绝不出现成功话术；pending_external/blocked/unknown 都如实说。
 */
export function admissionLine(entry: ServiceEntry): string {
  switch (entry.admission) {
    case "promised":
      return "这个入口可以打开对应平台的页面，后续操作在平台内由你自己完成。";
    case "pending_external":
      return "该入口的平台能力还没核实完成，暂时不能承诺可用；可以先在平台内自行搜索。";
    case "blocked":
      return "该入口当前未开放；可以先在平台内自行搜索本店。";
    default:
      return "该入口的平台能力暂无判定依据；可以先在平台内自行搜索本店。";
  }
}

/** 点击证据上报负载（open/jump_attempt/copy；只证明行为，不证明结果）。 */
export interface ServiceEventPayload {
  service_id: string;
  event: "open" | "jump_attempt" | "copy";
  environment?: string;
}

/** 事件回执的诚实文案（与服务端 note 同口径）。 */
export function eventNotice(event: string): string {
  if (event === "copy") {
    return "已复制。复制只记录为一次点击，不代表已连接、已关注或已添加。";
  }
  if (event === "jump_attempt") {
    return "已尝试打开平台页面。打开不代表已关注、已添加或已点评。";
  }
  return "已打开平台页面。后续操作在平台内由你自己完成；点击不代表已关注、已添加或已点评。";
}

/** 二维码引用只渲染公网 https（形状校验，非 https 引用不渲染 img）。 */
export function safeQrURL(raw: string): string {
  const v = (raw ?? "").trim();
  if (!v.startsWith("https://")) return "";
  try {
    const parsed = new URL(v);
    if (parsed.hostname === "" || /^(localhost|127\.|0\.0\.0\.0|10\.|192\.168\.|169\.254\.)/.test(parsed.hostname)) return "";
    return v;
  } catch {
    return "";
  }
}
