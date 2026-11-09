// HUI-1896 C1 门店首页数据面(纯函数,零 React,便于 vitest)。
//
// 消费 1676/4584 的 GET /api/v1/public/storefronts/{storeCode}(基线已有
// 数据面:storefront.v1,revision=0,默认单 section;1676 合流后补 published
// 版本/多分组/活动卡,载荷形状兼容)。纪律:
//   - payload 白名单解析:tenant_id/target_ref/legacy_* 等内部字段绝不进视图;
//   - 未配置动作/空分组不渲染(不死按钮);disabled 门店动作区收起只给门店提示;
//   - activity_cards 容忍解析:无合法活动码不伪造深链——活动卡可选,基础
//     服务不依赖活动;
//   - offers(4590)/services(4589)端点尚未在基线:解析器按文档草案契约写,
//     fixture 只进 tests/ 做本地契约验证;生产路径读不到端点时页面整区
//     不渲染,不伪建素材/价格/入口。

export interface StorefrontStoreView {
  name: string;
  address: string;
  openingHours: string;
  state: string;
}

export interface StorefrontActionView {
  actionId: string;
  actionRevision: number;
  catalogCode: string;
  platform: string;
  kind: string;
  variant: string;
  label: string;
  presentation: string;
  capabilityRef: string;
}

export interface StorefrontSectionView {
  sectionId: string;
  title: string;
  actionIds: string[];
}

export interface StorefrontView {
  storeCode: string;
  storefrontRevision: number;
  store: StorefrontStoreView;
  sections: StorefrontSectionView[];
  actions: StorefrontActionView[];
}

// 4584 服务端动作 kind 白名单(store.ListPublicStoreActions 只放行已映射实例)。
const KINDS: ReadonlySet<string> = new Set([
  "publish", "review", "follow", "wifi", "contact", "groupbuy", "share", "join_group",
]);

function str(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function num(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

function parseActions(raw: unknown): StorefrontActionView[] {
  const items = Array.isArray(raw) ? raw : [];
  const out: StorefrontActionView[] = [];
  for (const item of items) {
    if (item === null || typeof item !== "object") continue;
    const rec = item as Record<string, unknown>;
    const actionId = str(rec.action_id);
    const kind = str(rec.kind);
    if (actionId === "" || !KINDS.has(kind)) continue;
    out.push({
      actionId,
      actionRevision: num(rec.action_revision),
      catalogCode: str(rec.catalog_code),
      platform: str(rec.platform),
      kind,
      variant: str(rec.variant),
      label: str(rec.label),
      presentation: str(rec.presentation),
      capabilityRef: str(rec.capability_ref),
    });
  }
  return out;
}

function parseSections(raw: unknown): StorefrontSectionView[] {
  const items = Array.isArray(raw) ? raw : [];
  const out: StorefrontSectionView[] = [];
  for (const item of items) {
    if (item === null || typeof item !== "object") continue;
    const rec = item as Record<string, unknown>;
    const sectionId = str(rec.section_id);
    if (sectionId === "") continue;
    const ids = Array.isArray(rec.action_ids)
      ? rec.action_ids.filter((id): id is string => typeof id === "string" && id.trim() !== "").map((id) => id.trim())
      : [];
    out.push({ sectionId, title: str(rec.title), actionIds: ids });
  }
  return out;
}

/** 解析 storefronts 公共读载荷;结构不完整返回 null(页面按不可用处理)。 */
export function parseStorefront(payload: unknown): StorefrontView | null {
  if (payload === null || typeof payload !== "object") return null;
  const rec = payload as Record<string, unknown>;
  if (str(rec.schema_version) === "") return null;
  const storeRec = (rec.store ?? null);
  if (storeRec === null || typeof storeRec !== "object") return null;
  const store = storeRec as Record<string, unknown>;
  const name = str(store.name);
  if (name === "") return null;
  return {
    storeCode: str(rec.store_code),
    storefrontRevision: num(rec.storefront_revision),
    store: {
      name,
      address: str(store.address),
      openingHours: str(store.opening_hours),
      state: str(store.state) || "active",
    },
    sections: parseSections(rec.sections),
    actions: parseActions(rec.actions),
  };
}

// ---- 意图分组 ------------------------------------------------------------------

export type ActionIntent = "publish" | "review" | "follow" | "wifi" | "contact" | "groupbuy" | "share" | "other";

export const INTENT_TITLES: Record<ActionIntent, string> = {
  publish: "内容发布",
  review: "点评我们",
  follow: "关注我们",
  wifi: "门店 Wi-Fi",
  contact: "加微信",
  groupbuy: "团购套餐",
  share: "分享与社群",
  other: "门店服务",
};

/** kind → 意图分组(join_group 归入分享与社群;未知 kind 不丢动作,归 other)。 */
export function actionIntent(kind: string): ActionIntent {
  switch ((kind ?? "").trim()) {
    case "publish": return "publish";
    case "review": return "review";
    case "follow": return "follow";
    case "wifi": return "wifi";
    case "contact": return "contact";
    case "groupbuy": return "groupbuy";
    case "share":
    case "join_group": return "share";
    default: return "other";
  }
}

const INTENT_ORDER: readonly ActionIntent[] = [
  "publish", "review", "follow", "wifi", "contact", "groupbuy", "share", "other",
];

// 安全路径段:动作 id 由服务端签发(sa_*),这里只防御性放行 URL 安全字符。
function safeSegment(raw: string): string {
  return /^[A-Za-z0-9_-]+$/.test(raw) ? raw : "";
}

/**
 * 动作卡地址:publish → 内容视图(C2),其余 → 服务视图(C3,可地址化宿主)。
 * 非法 id 返回 null(绝不渲染死按钮)。
 */
export function actionHref(storeCode: string, action: StorefrontActionView): string | null {
  const code = safeSegment(storeCode);
  const id = safeSegment(action.actionId);
  if (code === "" || id === "") return null;
  const base = `/s/${code}`;
  return actionIntent(action.kind) === "publish" ? `${base}/content/${id}` : `${base}/service/${id}`;
}

export interface ActionGroup {
  intent: ActionIntent;
  title: string;
  items: { action: StorefrontActionView; href: string | null }[];
}

/** 按意图分组;空组不出现(未配置动作不渲染);组序稳定(publish 在前)。 */
export function groupActions(storeCode: string, actions: StorefrontActionView[]): ActionGroup[] {
  const byIntent = new Map<ActionIntent, ActionGroup>();
  for (const action of actions) {
    const intent = actionIntent(action.kind);
    let group = byIntent.get(intent);
    if (!group) {
      group = { intent, title: INTENT_TITLES[intent], items: [] };
      byIntent.set(intent, group);
    }
    group.items.push({ action, href: actionHref(storeCode, action) });
  }
  return INTENT_ORDER.filter((intent) => byIntent.has(intent)).map((intent) => byIntent.get(intent)!);
}

// ---- 门店状态 ------------------------------------------------------------------

/** active 无提示;disabled 门店暂停接待提示;未知状态按未知(不冒充可用)。 */
export function storeStateFace(state: string): string {
  switch ((state ?? "").trim()) {
    case "active":
      return "";
    case "disabled":
      return "门店暂停接待:以下服务可能不可用,请以门店现场公告为准。";
    default:
      return "门店状态未知,请以门店现场公告为准。";
  }
}

// ---- 活动卡(C4 深链;可选) -----------------------------------------------------

export interface ActivityCardRaw {
  card_id?: unknown;
  title?: unknown;
  state?: unknown;
  campaign_code?: unknown;
}

export interface ActivityCardView {
  cardId: string;
  title: string;
  state: string;
  stateFace: string;
  /** 合法活动码才有深链;无码/非法码 = null(不伪造活动入口)。 */
  link: string | null;
}

const CARD_STATE_FACE: Record<string, string> = {
  available: "进行中",
  not_started: "未开始",
  paused: "已暂停",
  expired: "已结束",
  ended: "已结束",
  draft: "未发布",
};

function safeCampaignCode(raw: unknown): string {
  return typeof raw === "string" && /^[A-Za-z0-9]{6,32}$/.test(raw.trim()) ? raw.trim() : "";
}

/** 活动卡容忍解析:缺 card_id 丢弃;活动码非法不给深链。 */
export function activityCards(raw: unknown): ActivityCardView[] {
  const items = Array.isArray(raw) ? raw : [];
  const out: ActivityCardView[] = [];
  for (const item of items) {
    if (item === null || typeof item !== "object") continue;
    const rec = item as ActivityCardRaw;
    const cardId = str(rec.card_id);
    if (cardId === "") continue;
    const state = str(rec.state) || "unknown";
    const code = safeCampaignCode(rec.campaign_code);
    out.push({
      cardId,
      title: str(rec.title) || "门店活动",
      state,
      stateFace: CARD_STATE_FACE[state] ?? "状态未知",
      link: code !== "" ? `/c/${code}?entry=web` : null,
    });
  }
  return out;
}

// ---- 团购套餐(4590 草案契约;提供者合流前生产面读不到 → 整区不渲染) ------------

export interface OfferItem {
  offerId: string;
  platform: string;
  title: string;
  priceNote: string;
  state: string;
  productUrl: string;
}

/** 4590 offers 读取草案契约;结构不完整返回 null(页面整区不渲染)。 */
export function offersView(payload: unknown): OfferItem[] | null {
  if (payload === null || typeof payload !== "object") return null;
  const rec = payload as Record<string, unknown>;
  if (!Array.isArray(rec.items)) return null;
  const out: OfferItem[] = [];
  for (const item of rec.items) {
    if (item === null || typeof item !== "object") continue;
    const row = item as Record<string, unknown>;
    const offerId = str(row.offer_id);
    if (offerId === "") continue;
    out.push({
      offerId,
      platform: str(row.platform),
      title: str(row.title) || "团购套餐",
      priceNote: str(row.price_note),
      state: str(row.state) || "unknown",
      productUrl: str(row.product_url),
    });
  }
  return out;
}

export interface OfferEntry {
  kind: "platform" | "view" | "stopped";
  text: string;
  /** 只有 https 官方商品地址才渲染为链接;其余 null(文案引导到平台)。 */
  href: string | null;
}

/** 套餐入口:available→平台购买(https 才有 href);unverified→到平台查看;售罄/失效→停入口给替代文案。 */
export function offerEntry(offer: OfferItem): OfferEntry {
  switch (offer.state) {
    case "available": {
      const href = /^https:\/\//.test(offer.productUrl) ? offer.productUrl : null;
      return { kind: "platform", text: "到平台购买", href };
    }
    case "unverified":
      return { kind: "view", text: "到平台查看", href: null };
    case "sold_out":
      return { kind: "stopped", text: "已售罄:可看看其他套餐,或到平台查看门店商品。" };
    case "expired":
      return { kind: "stopped", text: "这个套餐已过期:可看看其他套餐,或到平台查看门店商品。" };
    default:
      return { kind: "stopped", text: "套餐状态未知:价格与库存以平台页面为准。" };
  }
}
