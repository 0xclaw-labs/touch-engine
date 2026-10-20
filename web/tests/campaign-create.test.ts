import { describe, expect, it } from "vitest";
import {
  EMPTY_DRAFT,
  draftErrors,
  draftStorageKey,
  formatLocalInput,
  parseDraft,
  serializeDraft,
  toRFC3339Local,
} from "../src/lib/campaign-create";

describe("campaign draft dates", () => {
  it("converts a local datetime input to RFC3339 UTC", () => {
    const iso = toRFC3339Local("2026-10-21T10:30");
    expect(iso).not.toBeNull();
    expect(new Date(iso as string).toISOString()).toBe(iso);
    expect(iso).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/);
  });

  it("keeps an empty end as empty and rejects malformed input", () => {
    expect(toRFC3339Local("")).toBe("");
    expect(toRFC3339Local("2026-10-21")).toBeNull();
    expect(toRFC3339Local("not-a-date")).toBeNull();
  });

  it("round-trips a server timestamp into a local input value", () => {
    const iso = "2026-10-21T02:30:00.000Z";
    const local = formatLocalInput(iso);
    expect(local).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/);
    expect(toRFC3339Local(local as string)).toBe(iso);
    expect(formatLocalInput("")).toBe("");
  });
});

describe("campaign draft validation", () => {
  it("requires title, public content and a store", () => {
    const errors = draftErrors({ ...EMPTY_DRAFT });
    expect(errors.title).toContain("标题");
    expect(errors.public_content).toContain("顾客");
    expect(errors.store_id).toContain("门店");
  });

  it("rejects an end before the start or in the past", () => {
    const errors = draftErrors({
      ...EMPTY_DRAFT,
      store_id: "sto_1",
      title: "活动",
      public_content: "内容",
      starts_at_local: "2026-10-21T10:00",
      ends_at_local: "2026-10-20T10:00",
    });
    expect(errors.ends_at).toContain("结束");
    const past = draftErrors(
      {
        ...EMPTY_DRAFT,
        store_id: "sto_1",
        title: "活动",
        public_content: "内容",
        starts_at_local: "2026-10-01T10:00",
        ends_at_local: "2026-10-02T10:00",
      },
      new Date("2026-10-21T00:00:00Z"),
    );
    expect(past.ends_at).toContain("已经过去");
  });

  it("accepts a coherent draft", () => {
    expect(
      draftErrors(
        {
          store_id: "sto_1",
          title: "活动",
          public_content: "内容",
          starts_at_local: "2026-10-21T10:00",
          ends_at_local: "2026-10-22T10:00",
        },
        new Date("2026-10-21T00:00:00Z"),
      ),
    ).toEqual({});
  });
});

describe("campaign draft persistence", () => {
  it("round-trips a versioned draft and rejects foreign/malformed payloads", () => {
    const draft = {
      ...EMPTY_DRAFT,
      store_id: "sto_1",
      title: "活动",
      public_content: "内容",
      starts_at_local: "2026-10-21T10:00",
      ends_at_local: "2026-10-22T10:00",
    };
    expect(parseDraft(serializeDraft(draft))).toEqual(draft);
    expect(parseDraft(null)).toBeNull();
    expect(parseDraft("{}")).toBeNull();
    expect(parseDraft(JSON.stringify({ version: 99, draft }))).toBeNull();
    expect(parseDraft(JSON.stringify({ version: 1, draft: { title: 1 } }))).toBeNull();
  });

  it("keys drafts by tenant so switching org cannot resume another org's draft", () => {
    expect(draftStorageKey("tnt_a")).not.toBe(draftStorageKey("tnt_b"));
    expect(draftStorageKey("tnt_a")).toContain("tnt_a");
  });
});
