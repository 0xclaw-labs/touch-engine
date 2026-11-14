export interface ProductionQuote {
  quote_ref: string;
  offer_token?: string;
  amount_minor: number;
  currency: string;
  expires_at: string;
  payer: string;
  payer_context?: string;
  deliverables: string;
}
export type ContentAPI = (method: "GET" | "POST", path: string, body?: Record<string, unknown>) => Promise<{ ok: boolean; status: number; data: Record<string, unknown> }>;

export async function ensureContentPack(api: ContentAPI, storeId: string, packs: { id: string; title: string; legacy?: boolean; campaign_id?: string }[], title: string, campaignId?: string): Promise<string> {
  const samePurpose = (pack: typeof packs[number]) => !pack.legacy && (pack.campaign_id || "") === (campaignId || "");
  // The existing store contract keeps titles unique across all optional campaigns.
  // A readable numeric suffix avoids silently reusing another campaign's bundle.
  const belongsToTitle = (value: string) => value === title || (value.startsWith(`${title}（`) && /^\d+）$/.test(value.slice(title.length + 1)));
  const existing = packs.find((pack) => samePurpose(pack) && pack.title === title) || packs.find((pack) => samePurpose(pack) && belongsToTitle(pack.title));
  if (existing) return existing.id;
  const titles = new Set(packs.filter((pack) => !pack.legacy).map((pack) => pack.title));
  let packTitle = title;
  for (let index = 2; titles.has(packTitle); index++) packTitle = `${title}（${index}）`;
  const result = await api("POST", `stores/${storeId}/content-packs`, { title: packTitle, ...(campaignId ? { campaign_id: campaignId } : {}) });
  if (!result.ok || !result.data.id) throw new Error(String(result.data.message || "内容准备未完成，请重试；不会开始制作或收费。"));
  return String(result.data.id);
}

export function quoteIsUsable(q: ProductionQuote | null, now = Date.now()): q is ProductionQuote {
  return Boolean(q && q.quote_ref && Number.isSafeInteger(q.amount_minor) && q.amount_minor >= 0 && q.currency && q.payer && q.deliverables && Date.parse(q.expires_at) > now);
}

export function productionPayload(packId: string, quote: ProductionQuote, key: string, note: string) {
  return { content_pack_ref: `pack:${packId}`, quote_ref: quote.quote_ref, idempotency_key: key, note,
    offer_token: quote.offer_token, accepted_amount_minor: quote.amount_minor, accepted_currency: quote.currency, accepted_expires_at: quote.expires_at };
}
export type ProductionPayload = ReturnType<typeof productionPayload>;
