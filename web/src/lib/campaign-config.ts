// HUI-1676 增量:发布兑奖配置的产品语义映射与纯函数。
// 纪律(与 failure-copy.ts 同款):服务端机器原因码不出现在用户界面,每个
// 原因码在这里翻成「哪里错了 + 下一步怎么办」;三核验模式/去重判定基础/
// 奖励口径的文案只收口在这里;窗口校验镜像服务端(campaignenable),但服务端
// 仍是唯一权威,客户端提示只是提前反馈。

export interface ChannelRef {
  platform: string;
  entry_mode: string;
  scenario: string;
}

export interface PublishReward {
  kind: string;
  slot: string;
  budget_mode: string;
  total_quantity?: number;
  cost_budget_minor?: number;
  currency: string;
  store_ids: string[];
  claim_from: string;
  claim_until: string;
  redeem_from: string;
  redeem_until: string;
  onsite_redemption: boolean;
  staff_scope: string;
  out_of_stock_rule: string;
}

export interface PublishSpec {
  verification_mode: string;
  customer_copy: string;
  reward: PublishReward;
  dedup_basis: string;
  per_order_limit: boolean;
  channels: ChannelRef[];
  content_version_id: string;
}

export interface PublishConfigRecord {
  id?: string;
  version?: number;
  status?: string;
  spec?: PublishSpec;
  enabled_version?: number;
  disclosure?: string;
}

export interface ChannelAdmissionRecord {
  platform: string;
  entry_mode: string;
  scenario: string;
  status: string;
  allowed: boolean;
  reason?: string;
  reason_text?: string;
  missing?: string[];
  reward_admitted?: boolean;
  close_reason?: string;
}

// 三种核验模式(与 rewardops 三个 Evidence 类逐字对齐)。
export const MODES = [
  "staff_observed_submission",
  "staff_verified_post",
  "official_publish_confirmed",
] as const;

export function verificationModeLabel(mode: string): string {
  switch (mode) {
    case "staff_observed_submission":
      return "店员目测提交";
    case "staff_verified_post":
      return "店员核对作品";
    case "official_publish_confirmed":
      return "官方确认";
    default:
      return mode;
  }
}

// 每种模式配置时写给商家看的说明:顾客要做完什么才算数。配置时写死顾客
// 文案,不在用户完成后临时加严。
export function modeExplanation(mode: string): string {
  switch (mode) {
    case "staff_observed_submission":
      return "顾客当场把发布成功的页面给店员看一眼,店员确认就算完成。最快,适合到店场景。";
    case "staff_verified_post":
      return "店员在现场看到顾客发出来的作品本身(不只提交页),核对后才算完成。";
    case "official_publish_confirmed":
      return "要等平台的官方发布回执确认后才算完成。最严,不是所有活动都需要等这一种。";
    default:
      return "";
  }
}

// 三种去重判定基础:cookie/IP 永远不是选项。
export const DEDUP_BASES = [
  "verifiable_receipt",
  "platform_authorized_subject",
  "onsite_manual",
] as const;

export function dedupBasisLabel(basis: string): string {
  switch (basis) {
    case "verifiable_receipt":
      return "按可验证消费凭据(订单)";
    case "platform_authorized_subject":
      return "按平台授权账号";
    case "onsite_manual":
      return "按现场人工核对";
    default:
      return basis;
  }
}

export function perOrderAllowed(basis: string): boolean {
  return basis === "verifiable_receipt";
}

export function benefitKindLabel(kind: string): string {
  switch (kind) {
    case "store_coupon":
      return "门店优惠券";
    case "imported_code":
      return "合法导入券码";
    case "onsite_gift":
      return "现场赠品";
    case "onsite_discount":
      return "现场折扣";
    default:
      return kind;
  }
}

export function budgetModeLabel(mode: string): string {
  switch (mode) {
    case "total_quantity":
      return "按数量";
    case "cost_budget":
      return "按成本预算";
    default:
      return mode;
  }
}

export function staffScopeLabel(scope: string): string {
  switch (scope) {
    case "store_staff":
      return "门店店员可核验";
    case "org_owner":
      return "仅老板可核验";
    default:
      return scope;
  }
}

export function outOfStockLabel(rule: string): string {
  switch (rule) {
    case "stop_new":
      return "缺货即停:不再发新参与";
    case "mark_sold_out":
      return "显示已售罄:页面保留";
    default:
      return rule;
  }
}

export function channelLabel(ref: ChannelRef): string {
  const platforms: Record<string, string> = {
    douyin: "抖音",
    shipinhao: "视频号",
    xiaohongshu: "小红书",
    dianping: "大众点评",
  };
  const modes: Record<string, string> = {
    h5: "H5",
    miniapp: "小程序",
    native: "应用内",
    manual: "现场引导",
  };
  return `${platforms[ref.platform] ?? ref.platform} · ${modes[ref.entry_mode] ?? ref.entry_mode} · ${ref.scenario}`;
}

export interface RefusalCopy {
  field: string;
  text: string;
}

// 启用拒绝的服务端机器原因码 → 具体字段 + 下一步动作。服务端 422 形状:
// {error: <reason>, field: <config field>}。
export function refusalCopy(status: number, data: Record<string, unknown>): RefusalCopy {
  const field = typeof data.field === "string" ? data.field : "";
  const byReason: Record<string, string> = {
    customer_copy_missing: "顾客还没有被告知完成之后怎么领。请在「顾客文案」里写清楚。",
    content_not_ready: "选中的内容还不是本活动已采用的版本。先在「活动内容」里采用,再回来选它。",
    content_blocked: "这块内容已停止分发(授权或内容有问题)。请换一版已采用的内容。",
    content_grant_expired: "这块内容的授权已经过期。请换一版授权有效的内容。",
    channel_closed: "选中的发布渠道已关闭。请至少换一条还开着的渠道。",
    channel_not_admitted: "选中的发布渠道还没有准入,或准入已过期。请在「发布方式」里看缺什么,或换渠道。",
    no_runnable_channel: "还没有任何一条可用的发布渠道。至少选一条准入有效的渠道才能启用。",
    benefit_ref_missing: "奖励还没有对应批次。请先在奖励页建好这个活动的券批次,再回来启用。",
    benefit_version_mismatch: "奖励批次还绑在旧版本上。改过配置后要回奖励页把批次对到最新版本。",
    benefit_stock_missing: "奖励库存已经没了。补库存或调低名额之后再启用。",
    window_conflict: "领取时间没有落在活动期内,或兑换结束早于领取结束。请调整时间。",
    campaign_ended: "活动已经结束,不能启用。请新建活动或调整活动期限。",
    store_mismatch: "可用门店里有不存在或不属于本活动的门店。请重新勾选门店。",
  };
  const reason = typeof data.error === "string" ? data.error : "";
  const fallback = "这次启用没有完成。请检查上面标红的配置,再试一次。";
  if (status === 422 && reason && byReason[reason]) {
    return { field: field || "form", text: byReason[reason] };
  }
  return { field: "form", text: fallback };
}

// 响应形状收口:服务端 JSON → PublishConfigRecord 的唯一一次形状断言
// (组件层不再手写 as 断言,未知形状在这里集中处理)。
export function toPublishConfig(data: Record<string, unknown>): PublishConfigRecord {
  return data as unknown as PublishConfigRecord;
}

// 空草稿:没有保存过配置时的起点。顾客文案预填当前模式的模板句,商家可改。
export function defaultSpec(mode = MODES[0]): PublishSpec {
  return {
    verification_mode: mode,
    customer_copy: "",
    reward: {
      kind: "store_coupon",
      slot: "",
      budget_mode: "total_quantity",
      currency: "CNY",
      store_ids: [],
      claim_from: "",
      claim_until: "",
      redeem_from: "",
      redeem_until: "",
      onsite_redemption: false,
      staff_scope: "store_staff",
      out_of_stock_rule: "stop_new",
    },
    dedup_basis: "platform_authorized_subject",
    per_order_limit: false,
    channels: [],
    content_version_id: "",
  };
}

// 窗口镜像校验:领取窗要完整填写、内部有序、兑换不早于领取截止。
// 返回人话错误;空串 = 还没填到(草稿允许,启用时服务端仍会拦)。
export function windowProblem(spec: PublishSpec): string {
  const r = spec.reward;
  if (!r.claim_from || !r.claim_until || !r.redeem_from || !r.redeem_until) return "";
  const cf = Date.parse(r.claim_from);
  const cu = Date.parse(r.claim_until);
  const rf = Date.parse(r.redeem_from);
  const ru = Date.parse(r.redeem_until);
  if ([cf, cu, rf, ru].some((n) => Number.isNaN(n))) {
    return "时间格式没有读出来,请重新选一次时间。";
  }
  if (cf > cu) return "领取开始时间晚于领取结束时间。";
  if (rf > ru) return "兑换开始时间晚于兑换结束时间。";
  if (cu > ru) return "兑换结束不能早于领取结束,顾客领了券要有地方用。";
  return "";
}

// datetime-local 输入值 → RFC3339(服务端契约);空串原样返回。
export function toRFC3339(localValue: string): string {
  if (!localValue) return "";
  const d = new Date(localValue);
  if (Number.isNaN(d.getTime())) return "";
  return d.toISOString();
}

// RFC3339 → datetime-local 输入值(草稿回填)。
export function fromRFC3339(value: string): string {
  if (!value) return "";
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

// 选中的渠道记录里的准入展示:可用的给绿灯,不可用的给缺项原因(商家语言)。
export function admissionNote(rec: ChannelAdmissionRecord): string {
  if (rec.allowed) return "可用";
  if (rec.close_reason) return `渠道已关闭:${rec.close_reason}`;
  if (rec.reason_text) return `暂不可用:${rec.reason_text}`;
  if (rec.missing && rec.missing.length > 0) {
    return `还缺:${rec.missing.join("、")}`;
  }
  return "暂不可用:还没有准入记录。";
}

// 组装保存体:把 datetime-local 草稿转成服务端契约。
export function toServerSpec(spec: PublishSpec): PublishSpec {
  return {
    ...spec,
    reward: {
      ...spec.reward,
      claim_from: toRFC3339(spec.reward.claim_from),
      claim_until: toRFC3339(spec.reward.claim_until),
      redeem_from: toRFC3339(spec.reward.redeem_from),
      redeem_until: toRFC3339(spec.reward.redeem_until),
    },
  };
}
