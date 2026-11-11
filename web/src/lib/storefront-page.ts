// HUI-1676:门店首页版本(M3)的产品语义映射与纯函数。
// 纪律(与 campaign-config.ts 同款):服务端机器原因码不出现在用户界面,每个
// 原因码在这里翻成「哪里错了 + 下一步怎么办」;发布确认的凭据是服务端指纹
// (preflight 给出、publish 回带),勾选框只是 UX;预览数据面是真实预览端点
// (会话约束、白名单同形),叠加明显预览标识,不另造假壳、不产生真实参与。

export interface StorefrontSection {
  id: string;
  type: "actions" | "offers";
  title: string;
  action_ids?: string[];
  offer_refs?: string[];
}

export interface StorefrontActionSnapshot {
  action_id: string;
  action_revision: number;
  catalog_code: string;
  platform: string;
  kind: string;
  variant: string;
  label: string;
  presentation: string;
  capability_ref: string;
  section_id: string;
}

export interface StorefrontDraftDoc {
  store_id: string;
  store_name?: string;
  store_code: string;
  revision: number;
  status: string;
  sections: StorefrontSection[];
  actions?: StorefrontActionSnapshot[];
  action_revision_refs?: Record<string, number>;
  offer_refs?: string[];
  campaign_refs?: string[];
  brand_ref?: string;
  published_revision: number;
}

export interface PreflightVerdict {
  action_id: string;
  section_id: string;
  state: "ready" | "restricted" | "missing_config";
  reason_codes: string[];
}

export interface PreflightResult {
  draft_revision: number;
  published_revision: number;
  fatal: string[];
  actions: PreflightVerdict[];
  change_summary: string[];
  summary_fingerprint: string;
  publishable: boolean;
}

export interface StorefrontPublishEvent {
  id: string;
  action: string;
  revision: number;
  previous_revision: number;
  actor: string;
  summary: string[];
  created_at: string;
}

export interface RevisionsDoc {
  published_revision: number;
  items: { id: string; revision: number; status: string; published_at: string }[];
  events: StorefrontPublishEvent[];
}

// 渠道动作页的行(来自 /stores/{id}/actions)——模板与常用按钮选择的数据源。
export interface ChannelItem {
  action_id: string;
  catalog_code: string;
  catalog_label?: string;
  label: string;
  enabled: boolean;
  order: number;
}

// 套餐(来自 /stores/{id}/deal-offers,1671 目录只读)。
export interface DealOfferItem {
  id: string;
  title: string;
  kind: string;
  issuer: string;
  face_value_minor?: number;
}

// ---- 行业模板(段落骨架;内容引用只挂本店已配置动作,绝不虚构入口)------------

export interface TemplatePreset {
  id: string;
  title: string;
  description: string;
  // 每段给一组目录码:模板应用 = 把本店已配置的对应动作按目录码归组。
  groups: { id: string; type: StorefrontSection["type"]; title: string; codes: string[] }[];
}

export const TEMPLATE_PRESETS: TemplatePreset[] = [
  {
    id: "basic",
    title: "基础到店",
    description: "常用服务一段 + 到店优惠一段。适合先用起来的大多数门店。",
    groups: [
      { id: "s1", type: "actions", title: "常用服务", codes: ["A08", "A09", "A10", "A11", "A12", "A13", "A14", "A15", "A16"] },
      { id: "s2", type: "actions", title: "关注我们", codes: ["A13", "A14", "A15", "A16", "A17", "A18"] },
      { id: "s3", type: "offers", title: "到店优惠", codes: [] },
    ],
  },
  {
    id: "dining",
    title: "餐饮到店",
    description: "点评与关注在前,团购套餐单独一段。适合堂食/到店核销为主的门店。",
    groups: [
      { id: "s1", type: "actions", title: "留下好评", codes: ["A08", "A09", "A10", "A11", "A12"] },
      { id: "s2", type: "actions", title: "关注我们", codes: ["A13", "A14", "A15", "A16"] },
      { id: "s3", type: "offers", title: "团购套餐", codes: ["A19", "A20", "A21"] },
    ],
  },
  {
    id: "retail",
    title: "零售货架",
    description: "服务入口与团购并列,内容发布单独一段。适合零售/货架为主的门店。",
    groups: [
      { id: "s1", type: "actions", title: "门店服务", codes: ["A08", "A13", "A14", "A15", "A16", "A22", "A23"] },
      { id: "s2", type: "actions", title: "晒作品", codes: ["A01", "A02", "A03", "A04", "A05", "A06", "A07"] },
      { id: "s3", type: "offers", title: "商品与团购", codes: ["A19", "A20", "A21"] },
    ],
  },
];

export function templateById(id: string): TemplatePreset | undefined {
  return TEMPLATE_PRESETS.find((preset) => preset.id === id);
}

// applyTemplate 把模板段落骨架落到本店真实动作实例上:目录码匹配本店已配置
// 且启用的动作;没有匹配实例的段落整段省略(不虚构入口)。返回省略说明。
export function applyTemplate(
  preset: TemplatePreset,
  items: ChannelItem[],
): { sections: StorefrontSection[]; skipped: string[] } {
  const byCode = new Map<string, ChannelItem[]>();
  for (const item of items) {
    if (!item.enabled) continue;
    const list = byCode.get(item.catalog_code) ?? [];
    list.push(item);
    byCode.set(item.catalog_code, list);
  }
  const sections: StorefrontSection[] = [];
  const skipped: string[] = [];
  for (const group of preset.groups) {
    if (group.type === "offers") {
      sections.push({ id: group.id, type: "offers", title: group.title, offer_refs: [] });
      continue;
    }
    const ids: string[] = [];
    for (const code of group.codes) {
      for (const item of byCode.get(code) ?? []) {
        if (!ids.includes(item.action_id)) ids.push(item.action_id);
      }
    }
    if (ids.length === 0) {
      skipped.push(group.title);
      continue;
    }
    sections.push({ id: group.id, type: "actions", title: group.title, action_ids: ids });
  }
  return { sections, skipped };
}

// ---- 预检结果与发布确认的产品语言 -------------------------------------------

export interface VerdictCopy {
  stateText: string;
  lines: string[];
  fixEntry: string;
}

// state + reason_codes → 商家语言 + 修复入口文案。一个动作的问题绝不传染
// 邻居(预检逐动作隔离,缺配置不拖垮别的动作的 ready);受限/缺配置项必须
// 明确排除或修复,不能标可用。
export function verdictCopy(v: PreflightVerdict): VerdictCopy {
  const stateText =
    v.state === "ready" ? "可以发布" : v.state === "restricted" ? "受限:现在不能发布" : "缺配置:先去补";
  const lines = v.reason_codes.map(reasonCopy);
  return {
    stateText,
    lines,
    fixEntry: v.state === "ready" ? "" : "去「渠道动作」页修复这一项,或在本页把它从段落里移除。",
  };
}

export function reasonCopy(code: string): string {
  const byCode: Record<string, string> = {
    action_not_found: "这个动作已经被删除了。",
    action_disabled: "动作在渠道动作页被停用了。",
    action_revoked: "平台授权已被撤销,这项入口暂时不能上架。",
    action_expired: "这项动作已过有效期。",
    target_missing: "还没有填目标地址。",
    target_not_official: "目标地址不是平台官方页面,不能对外展示。",
    content_not_adopted: "绑定的内容包还没有采用版本。",
    content_blocked: "绑定的内容已停止分发。",
    content_grant_expired: "绑定内容的授权已过期。",
  };
  return byCode[code] ?? code;
}

// diff 摘要行 → 人话(发布确认视图)。空摘要 = 与线上版本相同。
export function summaryLineCopy(line: string): string {
  const [kind, arg = ""] = line.split(":");
  const byKind: Record<string, string> = {
    section_added: `新增段落「${arg}」`,
    section_removed: `移除段落「${arg}」`,
    section_renamed: `段落「${arg}」改名了`,
    section_reordered: `段落顺序有调整`,
    action_added: `新增动作 ${arg}`,
    action_removed: `移除动作 ${arg}`,
    action_updated: `动作 ${arg} 的名称有修改`,
    offers_changed: `段落「${arg}」的套餐有变化`,
    offer_refs_changed: `套餐引用有变化`,
    campaign_refs_changed: `活动卡片有变化`,
    brand_changed: `品牌信息有变化`,
  };
  return byKind[kind] ?? line;
}

export function canPublish(pre: PreflightResult | null, confirmed: boolean, fingerprint: string): boolean {
  if (!pre) return false;
  if (!pre.publishable) return false;
  if (!confirmed) return false;
  return pre.summary_fingerprint !== "" && pre.summary_fingerprint === fingerprint;
}

// ---- 请求体组装 -------------------------------------------------------------

export function buildPublishBody(
  candidateRevision: number,
  expectedPublished: number,
  fingerprint: string,
): Record<string, unknown> {
  return {
    candidate_revision: candidateRevision,
    expected_published_revision: expectedPublished,
    summary_fingerprint: fingerprint,
    confirm_changes: true,
  };
}

export function buildRollbackBody(target: number, expectedPublished: number): Record<string, unknown> {
  return { target_revision: target, expected_published_revision: expectedPublished };
}

// 发布/回滚失败的产品文案(409/422/400 之外落通用失败)。服务端机器原因码
// 不出现在界面。
export function publishFailureText(status: number, data: Record<string, unknown>): string {
  const reason = typeof data.error === "string" ? data.error : "";
  if (status === 409 && reason === "stale_version") return "线上版本刚被别人更新过。请重新预检,确认新的变更摘要后再发布。";
  if (status === 409 && reason === "confirm_required") return "变更摘要在你确认后又变了。请重新预检,确认最新差异。";
  if (status === 409 && reason === "action_changed") return "有动作在保存草稿后又被改过。请重新保存草稿,再预检发布。";
  if (status === 422 && reason === "actions_not_ready") return "还有不能发布的动作。请在段落里移除它们(形成新版本),或先去渠道动作页修复。";
  if (status === 409 && reason === "not_published") return "只能回滚到已经发布过的版本。";
  if (status === 400) return "请求没有发完整。请刷新页面重试。";
  return "这次没有完成,请稍后重试。";
}

// ---- 手机预览投影 ------------------------------------------------------------

export interface PreviewPayload {
  schema_version?: string;
  store_code?: string;
  storefront_revision?: number;
  store?: { name?: string; address?: string; opening_hours?: string; state?: string };
  sections?: { section_id: string; type?: string; title: string; action_ids?: string[]; offer_refs?: string[] }[];
  actions?: StorefrontActionSnapshot[];
  activity_cards?: { campaign_code: string; state: string }[];
  preview?: boolean;
}

// 预览载荷 → 手机壳渲染行(纯投影;预览标识由壳上 data-state 承担)。
export function previewRows(payload: PreviewPayload): { title: string; lines: string[] }[] {
  const byId = new Map((payload.actions ?? []).map((a) => [a.action_id, a]));
  const rows: { title: string; lines: string[] }[] = [];
  for (const sec of payload.sections ?? []) {
    if (sec.type === "offers") {
      rows.push({ title: sec.title, lines: [(sec.offer_refs ?? []).length > 0 ? `${(sec.offer_refs ?? []).length} 个套餐` : "还没有套餐"] });
      continue;
    }
    const lines = (sec.action_ids ?? []).map((id) => byId.get(id)?.label ?? "已下线的入口");
    if (lines.length === 0) continue;
    rows.push({ title: sec.title, lines });
  }
  return rows;
}
