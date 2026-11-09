// HUI-4584 门店渠道动作页（M4）纯逻辑。
//
// 纪律：
//   - 页面只做通用动作列表/启停/排序/新增/测试入口；平台专属目标表单归 4589。
//   - 普通（链接目标）与高级权限（平台授权/凭据）分区展示；本页绝不存储或
//     展示 AppSecret 类凭据。
//   - validate 摘要守三分证据口径：网页可达 ≠ 原生唤起 ≠ 平台动作；营销奖励
//     场景准入真值在 3535，本页如实显示「未判定」。
//   - 保存永远带 expected_revision（服务端 config_revision 乐观锁），旧版本
//     并发被 409 拒绝时提示刷新重试，不静默覆盖。

export interface CatalogEntry {
  code: string;
  label: string;
  platform: string;
  kind: string;
  variant: string;
}

export interface ChannelItem {
  action_id: string;
  action_revision: number;
  revision: number;
  catalog_code: string;
  catalog_label?: string;
  platform: string;
  kind: string;
  variant: string;
  label: string;
  target_ref: string;
  content_binding_ref: string;
  order: number;
  enabled: boolean;
  source?: string;
  legacy_campaign_id?: string;
  /** 页面内派生：保存前的逐行阻塞原因（空串 = 无阻塞）。 */
  blocker?: string;
}

export interface ValidateResult {
  action_id?: string;
  action_revision?: number;
  field_errors?: Array<{ field?: string; code?: string }>;
  method_states?: Array<{ method?: string; state?: string; reason?: string }>;
  reason_codes?: string[];
  reward_admission?: { evaluated?: boolean; capability_ref?: string; reason?: string };
}

interface PutItem {
  action_id: string;
  catalog_code: string;
  label: string;
  target_ref: string;
  content_binding_ref: string;
  order: number;
  enabled: boolean;
}

const FIELD_COPY: Record<string, string> = {
  target_not_official: "目标地址不是公网正式链接",
  target_missing: "目标地址未配置",
  catalog_unassigned: "目录项未选择（旧数据待映射）",
  revoked: "该动作已被撤销",
  expired: "该动作已过期",
};

const WEB_REASON_COPY: Record<string, string> = {
  address_not_official: "网页未开放（地址不是正式链接）",
  not_configured: "网页未开放（未配置目标）",
  revoked: "网页未开放（已撤销）",
  expired: "网页未开放（已过期）",
};

/** 目录项里尚无任何已配置实例的条目（M4 缺项提示；'' 待映射行不算已配置）。 */
export function catalogMissingEntries(catalog: CatalogEntry[], items: ChannelItem[]): CatalogEntry[] {
  const configured = new Set(items.filter((item) => item.catalog_code !== "").map((item) => item.catalog_code));
  return catalog.filter((entry) => !configured.has(entry.code));
}

/** 目标摘要：主机名 + 截断路径；非 https 正式地址不给详情（不展示内网地址）。 */
export function targetSummary(targetRef: string): string {
  const raw = (targetRef ?? "").trim();
  if (raw === "") return "未配置目标";
  let parsed: URL;
  try {
    parsed = new URL(raw);
  } catch {
    return "目标地址不是正式链接";
  }
  if (parsed.protocol !== "https:" || parsed.hostname === "" ||
      /^(localhost|127\.|0\.0\.0\.0|10\.|192\.168\.|169\.254\.)/.test(parsed.hostname) ||
      parsed.hostname.endsWith(".local") || parsed.hostname.endsWith(".invalid")) {
    return "目标地址不是正式链接";
  }
  const path = (parsed.pathname + parsed.search).replace(/\?.*$/, "").replace(/\/+$/, "");
  const combined = parsed.hostname + path;
  return combined.length > 48 ? combined.slice(0, 45) + "…" : combined || parsed.hostname;
}

/** 交换相邻 order；越界（首项上移/末项下移）原样返回。 */
export function reorder(items: ChannelItem[], actionId: string, delta: number): ChannelItem[] {
  const sorted = [...items].sort((a, b) => a.order - b.order);
  const index = sorted.findIndex((item) => item.action_id === actionId);
  if (index < 0) return items;
  const swapWith = index + delta;
  if (swapWith < 0 || swapWith >= sorted.length) return items;
  const a = sorted[index];
  const b = sorted[swapWith];
  const orderOf = (item: ChannelItem) => (item.action_id === a.action_id ? b.order : item.action_id === b.action_id ? a.order : item.order);
  return sorted.map((item) => ({ ...item, order: orderOf(item) }));
}

/** 保存前逐行阻塞：启用行必须有目录与目标；待映射行必须先补目录；绑定值必须 pack: 形态。 */
export function normalizeItems(items: ChannelItem[]): ChannelItem[] {
  return items.map((item) => {
    let blocker = "";
    if (item.catalog_code === "") blocker = "请先选择目录项";
    else if (item.enabled && item.target_ref.trim() === "") blocker = "启用前需要填写目标地址";
    const binding = bindingBlocker(item.content_binding_ref);
    if (blocker === "" && binding !== "") blocker = binding;
    return { ...item, blocker };
  });
}

/** PUT 负载：只带契约字段；order 规整为 1..n 连续；不带 expected_revision 之外的只读回显。 */
export function buildPutBody(items: ChannelItem[], expectedRevision: number): { expected_revision: number; items: PutItem[] } {
  const sorted = [...items].sort((a, b) => a.order - b.order);
  const out: PutItem[] = sorted.map((item, index) => ({
    action_id: item.action_id || "",
    catalog_code: item.catalog_code,
    label: (item.label || "").slice(0, 200),
    target_ref: (item.target_ref || "").trim(),
    content_binding_ref: (item.content_binding_ref || "").trim(),
    order: index + 1,
    enabled: item.enabled,
  }));
  return { expected_revision: expectedRevision, items: out };
}

/** 新增行的下一个 order。 */
export function nextOrder(items: ChannelItem[]): number {
  return items.reduce((max, item) => Math.max(max, item.order), 0) + 1;
}

/** validate 摘要：问题行 + 三分证据行。绝不把「可达」说成「已关注/点评成功」。 */
export function validateSummary(result: ValidateResult): { problem: string; lines: string[] } {
  const lines: string[] = [];
  let problem = "";
  for (const err of result.field_errors ?? []) {
  	const copy = FIELD_COPY[err.code ?? ""] ?? `${err.field ?? "字段"}校验未通过`;
  	if (problem === "") problem = copy;
  	else problem += "；" + copy;
  }
  for (const state of result.method_states ?? []) {
  	if (state.method === "web") {
  		lines.push(state.state === "configured" ? "网页配置可达。" : WEB_REASON_COPY[state.reason ?? ""] ?? "网页未开放。");
  	}
  	if (state.method === "client_launch") {
  		lines.push("原生唤起未验证——不能代开客户端。");
  	}
  	if (state.method === "platform_action") {
  		lines.push("平台动作未知——不是关注、点评或加群成功。");
  	}
  }
  const admission = result.reward_admission ?? {};
  if (admission.evaluated !== true) {
  	lines.push("营销奖励场景准入未判定（由渠道能力域单独判定，与本页无关）。");
  }
  return { problem, lines };
}

/** 内容包下拉选项：只收「已采用」的包（adoptions 非空），容忍脏数据行。 */
export interface ContentPackOption {
  packId: string;
  title: string;
  platforms: string[];
}

export function adoptedPackOptions(data: unknown): ContentPackOption[] {
  const items = Array.isArray((data as { items?: unknown })?.items) ? (data as { items: unknown[] }).items : [];
  const out: ContentPackOption[] = [];
  for (const item of items) {
  	if (!item || typeof item !== "object") continue;
  	const row = item as Record<string, unknown>;
  	const packId = typeof row.id === "string" ? row.id : "";
  	if (packId === "") continue;
  	const adoptions = Array.isArray(row.adoptions) ? row.adoptions : [];
  	if (adoptions.length === 0) continue; // 未采用的包不进绑定下拉
  	const platforms: string[] = [];
  	for (const a of adoptions) {
  		const platform = a && typeof a === "object" && typeof (a as Record<string, unknown>).platform === "string"
  			? ((a as Record<string, unknown>).platform as string)
  			: "";
  		if (platform !== "" && !platforms.includes(platform)) platforms.push(platform);
  	}
  	const title = typeof row.title === "string" && row.title !== "" ? row.title : packId;
  	out.push({ packId, title, platforms });
  }
  return out;
}

/** 行上内容绑定值只允许 '' 或 pack:<id>；其他值提交前先拦（后端 400 同口径）。 */
export function bindingBlocker(value: string): string {
  const v = (value ?? "").trim();
  if (v === "") return "";
  if (!v.startsWith("pack:") || v.slice("pack:".length).trim() === "") {
  	return "内容绑定必须是已采用的内容包（pack: 形态）。";
  }
  return "";
}

/** 保存闸：已知落后（stale）或刷新中一律不允许保存——先刷新对齐版本号。 */
export function canSave(state: { stale: boolean; reloading: boolean; dirty: boolean; blockers: number }): boolean {
  return !state.stale && !state.reloading && state.dirty && state.blockers === 0;
}

/** 保存失败分流：409 是独立剧本（冲突→刷新），其余才是普通保存失败。 */
export function saveFailure(status: number, data: Record<string, unknown>): { kind: "conflict" | "error"; text: string } {
  const code = typeof data.error === "string" ? data.error : "";
  if (status === 409 || code === "stale_version") {
  	return { kind: "conflict", text: "配置已被他人更新。请点「刷新最新配置」，在最新版本上继续修改（这次修改不会自动带上）。" };
  }
  return { kind: "error", text: channelsSaveError(status, data) };
}

/** 刷新成功提示：明确告诉商家当前版本号，保存会带这一版。 */
export function refreshNotice(revision: number): string {
  return `已读到最新配置（第 ${revision} 版）。页面内容已换成这份最新版本，继续修改后保存会带这一版号。`;
}

/** 保存失败的产品文案（不含状态码/机器码给顾客）。 */
export function channelsSaveError(status: number, data: Record<string, unknown>): string {
  const code = typeof data.error === "string" ? data.error : "";
  switch (code) {
  	case "stale_version":
  		return "配置已被他人更新。请刷新页面后重试，避免覆盖别人的修改。";
  	case "unknown_catalog_code":
  		return "目录项不在 30 项动作目录里，请重新选择。";
  	case "duplicate_action_id":
  		return "同一个动作出现了两次，请刷新后重试。";
  	case "unknown_action_id":
  		return "有动作刚被删除。请刷新页面后再保存。";
  	case "bad_target_ref":
  		return "目标地址必须是公网 https 链接。";
  	case "target_required":
  		return "启用前需要填写目标地址。";
  	case "bad_label":
  		return "名称太长了，请缩短后保存。";
  	case "bad_content_binding_ref":
  		return "内容绑定必须是已采用的内容包引用，这次没有保存。请重新选择内容包。";
  	case "out_of_scope":
  	case "forbidden":
  	case "agency_inactive":
  		return "你只能配置自己管辖的门店。";
  	case "not_found":
  		return "门店不存在或已被停用。";
  	default:
  		return status >= 500 ? "没有保存成功，请重试。" : "没有保存成功，请检查后重试。";
  }
}
