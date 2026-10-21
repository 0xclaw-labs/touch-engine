import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  cancelParticipation,
  lookupParticipation,
  resumeCredential,
  startParticipation,
  switchDraft,
} from "../src/lib/customer-journey";

// HUI-1896 T2 参与门面:浏览绝不建参与;lookup 404=全新开始(不是错误);
// 幂等键会话内稳定;凭据不落 localStorage。

const ok = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("lookupParticipation 恢复语义", () => {
  it("POST participations/lookup 携带 link_code;404 → null 表示全新开始", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      expect(String(input)).toBe("/api/participations/lookup");
      return ok({ state: "not_found" }, 404);
    });
    vi.stubGlobal("fetch", fetchMock);
    const result = await lookupParticipation("CODE1");
    expect(result).toBeNull();
    const init = (fetchMock.mock.calls[0] as unknown[])[1] as RequestInit;
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual({ link_code: "CODE1" });
  });

  it("有参与 → 返回参与视图", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => ok({ id: "pt_9", status: "pending", benefit_reservation_state: "confirmed" })),
    );
    const p = await lookupParticipation("CODE1");
    expect(p?.id).toBe("pt_9");
    expect(p?.status).toBe("pending");
  });

  it("网络异常 → {error:'network'},不伪报恢复成功", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => {
      throw new Error("down");
    }));
    const result = await lookupParticipation("CODE1");
    expect(result).toEqual({ error: "network" });
  });
});

describe("startParticipation 显式开始才建参与", () => {
  it("POST participations 带幂等键;同一会话重放同键", async () => {
    const fetchMock = vi.fn(async () => ok({ id: "pt_1", status: "pending", benefit_reservation_state: "confirmed" }, 201));
    vi.stubGlobal("fetch", fetchMock);
    const first = await startParticipation("CODE1", "v1");
    const second = await startParticipation("CODE1", "v1");
    expect(first?.id).toBe("pt_1");
    expect(second?.id).toBe("pt_1");
    const body1 = JSON.parse(String(((fetchMock.mock.calls[0] as unknown[])[1] as RequestInit).body));
    const body2 = JSON.parse(String(((fetchMock.mock.calls[1] as unknown[])[1] as RequestInit).body));
    expect(body1.idempotency_key).toBe(body2.idempotency_key);
    expect(body1.link_code).toBe("CODE1");
    expect(body1.content_pack_version).toBe("v1");
  });

  it("库存 422 → 错误文案(不引导发布)", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => ok({ message: "奖励库存已用完" }, 422)));
    const result = await startParticipation("CODE1", "v1");
    expect(result).toMatchObject({ error: "start_failed" });
    expect((result as { message?: string }).message).toContain("用完");
  });

  it("网络异常 → network 错误", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => {
      throw new Error("down");
    }));
    expect(await startParticipation("CODE1", "v1")).toEqual({ error: "network" });
  });
});

describe("switchDraft / cancelParticipation", () => {
  it("PATCH content 换选;冻结 422 → frozen 错误", async () => {
    const fetchMock = vi.fn(async () => ok({ id: "pt_1", status: "pending", content_pack_version: "v2" }));
    vi.stubGlobal("fetch", fetchMock);
    const p = await switchDraft("pt_1", "v2");
    expect(p?.content_pack_version).toBe("v2");
    const [url, init] = fetchMock.mock.calls[0] as unknown[];
    expect(String(url)).toBe("/api/participations/pt_1/content");
    expect((init as RequestInit).method).toBe("PATCH");
  });

  it("冻结换选 422 → {error:'frozen'}", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => ok({ message: "已交接,版本冻结" }, 422)));
    expect(await switchDraft("pt_1", "v2")).toMatchObject({ error: "frozen" });
  });

  it("cancel POST cancel 端点", async () => {
    const fetchMock = vi.fn(async () => ok({ id: "pt_1", status: "canceled" }));
    vi.stubGlobal("fetch", fetchMock);
    const p = await cancelParticipation("pt_1");
    expect(p?.status).toBe("canceled");
    const [url, init] = fetchMock.mock.calls[0] as unknown[];
    expect(String(url)).toBe("/api/participations/pt_1/cancel");
    expect((init as RequestInit).method).toBe("POST");
  });
});

describe("resumeCredential 参与码出示", () => {
  it("POST handoff(customer_resume) → qr_payload 一次性返回", async () => {
    const fetchMock = vi.fn(async () =>
      ok({ credential_id: "c1", purpose: "customer_resume", token: "ptx_x.y", qr_payload: "https://example.com/p/ptx_x", participation: { id: "pt_1" } }, 201));
    vi.stubGlobal("fetch", fetchMock);
    const cred = await resumeCredential("pt_1");
    expect(cred?.qr_payload).toBe("https://example.com/p/ptx_x");
    const [url, init] = fetchMock.mock.calls[0] as unknown[];
    expect(String(url)).toBe("/api/participations/pt_1/handoff");
    expect(JSON.parse(String((init as RequestInit).body))).toEqual({ purpose: "staff_verify" });
  });

  it("凭据与参与码不写 localStorage/sessionStorage(源码级断言)", async () => {
    const root = path.dirname(fileURLToPath(import.meta.url));
    const raw = readFileSync(path.join(root, "..", "src", "lib", "customer-journey.ts"), "utf8");
    // 去注释后断言可执行代码(纪律说明不算实现;沿 publish-handoff-mount 惯例)。
    const code = raw
      .replace(/\/\*[\s\S]*?\*\//g, "")
      .split("\n")
      .map((line) => line.replace(/(^|\s)\/\/.*$/, ""))
      .join("\n");
    expect(code.includes("localStorage")).toBe(false);
    expect(code.includes("sessionStorage")).toBe(false);
    // 行为面:返回值只经内存。
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => ok({ credential_id: "c1", token: "ptx_x.y", qr_payload: "https://e.com/p/ptx_x" }, 201)),
    );
    const cred = await resumeCredential("pt_1");
    expect(cred?.token).toBe("ptx_x.y");
  });
});
