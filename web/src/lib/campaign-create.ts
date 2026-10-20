// HUI-2628: merchant creation draft rules. The same draft is persisted locally
// (so navigation/refresh cannot lose typing) and, after step 1, exists as a
// real server draft. No technical field is ever requested from the merchant.
export const CAMPAIGN_DRAFT_VERSION = 1;

export interface CampaignDraft {
  store_id: string;
  title: string;
  public_content: string;
  starts_at_local: string;
  ends_at_local: string;
}

export const EMPTY_DRAFT: CampaignDraft = {
  store_id: "",
  title: "",
  public_content: "",
  starts_at_local: "",
  ends_at_local: "",
};

const DATETIME_LOCAL = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/;

function pad(value: number): string {
  return String(value).padStart(2, "0");
}

/** Local `datetime-local` value → RFC3339 UTC. Empty stays empty; malformed is null. */
export function toRFC3339Local(value: string): string | null {
  const raw = value.trim();
  if (raw === "") return "";
  if (!DATETIME_LOCAL.test(raw)) return null;
  const time = new Date(raw);
  if (!Number.isFinite(time.getTime())) return null;
  return time.toISOString();
}

/** Server RFC3339 → local `datetime-local` value (for edit/restore). */
export function formatLocalInput(value: string | null | undefined): string {
  if (!value) return "";
  const time = new Date(value);
  if (!Number.isFinite(time.getTime())) return "";
  return `${time.getFullYear()}-${pad(time.getMonth() + 1)}-${pad(time.getDate())}T${pad(time.getHours())}:${pad(time.getMinutes())}`;
}

export function draftErrors(draft: CampaignDraft, now = new Date()): Record<string, string> {
  const errors: Record<string, string> = {};
  if (!draft.store_id.trim()) errors.store_id = "先选一家门店，顾客才知道活动在哪家店。";
  if (!draft.title.trim()) errors.title = "给活动起一个顾客看得懂的标题。";
  if (!draft.public_content.trim()) errors.public_content = "写一句顾客能看到的内容。";
  const start = toRFC3339Local(draft.starts_at_local);
  const end = toRFC3339Local(draft.ends_at_local);
  if (start === null) errors.starts_at = "开始时间格式不对，请重新选择。";
  if (end === null) errors.ends_at = "结束时间格式不对，请重新选择。";
  if (typeof start === "string" && typeof end === "string" && start && end && Date.parse(end) <= Date.parse(start)) {
    errors.ends_at = "结束时间要晚于开始时间。";
  } else if (typeof end === "string" && end && Date.parse(end) <= now.getTime()) {
    errors.ends_at = "结束时间已经过去了，顾客打开会看到活动已结束。";
  }
  return errors;
}

export function draftStorageKey(tenantId: string): string {
  return `touch.campaignDraft.v1:${tenantId || "anonymous"}`;
}

export function serializeDraft(draft: CampaignDraft): string {
  return JSON.stringify({ version: CAMPAIGN_DRAFT_VERSION, draft });
}

export function parseDraft(raw: string | null): CampaignDraft | null {
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as { version?: unknown; draft?: unknown };
    if (parsed.version !== CAMPAIGN_DRAFT_VERSION) return null;
    if (!parsed.draft || typeof parsed.draft !== "object") return null;
    const d = parsed.draft as Record<string, unknown>;
    const fields: (keyof CampaignDraft)[] = ["store_id", "title", "public_content", "starts_at_local", "ends_at_local"];
    for (const field of fields) {
      if (typeof d[field] !== "string") return null;
    }
    return {
      store_id: d.store_id as string,
      title: d.title as string,
      public_content: d.public_content as string,
      starts_at_local: d.starts_at_local as string,
      ends_at_local: d.ends_at_local as string,
    };
  } catch {
    return null;
  }
}
