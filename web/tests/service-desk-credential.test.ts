import { describe, expect, it } from "vitest";
import { resolveServiceCredential, serviceReturnPath, stableServiceOperation, serviceMutationNeedsRecovery } from "../src/lib/service-desk";

describe("统一顾客凭证解析", () => {
  it("同一入口自动定位参与凭据，调用只读peek且不需要活动或门店输入", async () => {
    const calls: unknown[] = [];
    const result = await resolveServiceCredential(async (...args) => {
      calls.push(args);
      return { ok: true, status: 200, data: { id: "pt_1", campaign_id: "cmp_original", store_id: "sto_original" } };
    }, "https://example.com/p/ptx_aaaaaaaaaaaaaaaaaaaaaa.AB");
    expect(calls).toEqual([["POST", "participations/staff/peek", { token: "ptx_aaaaaaaaaaaaaaaaaaaaaa.AB" }]]);
    expect(result.kind).toBe("participation");
    expect(result.response.data.store_id).toBe("sto_original");
  });
  it("展示码走原短时展示码权威，过期不降级成别的凭证", async () => {
    const calls: string[] = [];
    const result = await resolveServiceCredential(async (_method, route) => {
      calls.push(route);
      return { ok: false, status: 410, data: { error: "expired" } };
    }, "a".repeat(32));
    expect(calls).toEqual(["reward-display-codes/lookup"]);
    expect(result.kind).toBe("benefit");
    expect(result.response.status).toBe(410);
  });
  it("旧券仍经原券码只读查询，禁止内部引用或陌生URL", async () => {
    const calls: unknown[] = [];
    const api = async (...args: unknown[]) => { calls.push(args); return { ok: true, status: 200, data: { id: "ben_1" } }; };
    expect((await resolveServiceCredential(api, "COUPON-OLD")).kind).toBe("benefit");
    expect(calls).toEqual([["GET", "reward-benefits/by-code?code=COUPON-OLD"]]);
    for (const raw of ["pt_internal", "rben_internal", "https://bad.example/other/ptx_aaaaaaaaaaaaaaaaaaaaaa.AB", ""]) {
      const result = await resolveServiceCredential(api, raw);
      expect(result.kind).toBe("invalid");
    }
    expect(calls).toHaveLength(1);
  });
  it("旧券原码即使是32位十六进制，仍可沿原券码权威定位", async () => {
    const calls: string[] = [];
    const result = await resolveServiceCredential(async (_method, route) => {
      calls.push(route);
      return route === "reward-display-codes/lookup" ? { ok: false, status: 404, data: {} } : { ok: true, status: 200, data: { id: "rben_legacy", status: "issued" } };
    }, "b".repeat(32));
    expect(result.response.data.id).toBe("rben_legacy");
    expect(calls).toEqual(["reward-display-codes/lookup", `reward-benefits/by-code?code=${"b".repeat(32)}`]);
  });
  it("同一顾客同一操作重试保留原事件，不把不同后果混成同一确认", () => {
    const cache = new Map<string, Record<string, unknown>>();
    const first = stableServiceOperation(cache, "pt_1:issue", () => ({ event_id: "first" }));
    expect(stableServiceOperation(cache, "pt_1:issue", () => ({ event_id: "second" }))).toBe(first);
    expect(stableServiceOperation(cache, "pt_1:fulfill", () => ({ event_id: "fulfill" }))).not.toBe(first);
  });
  it("网关错误或成功响应缺事实时继续回查，不能当作明确拒绝后另起确认", () => {
    expect(serviceMutationNeedsRecovery({ ok: false, status: 502, data: {} })).toBe(true);
    expect(serviceMutationNeedsRecovery({ ok: true, status: 200, data: {} })).toBe(true);
    expect(serviceMutationNeedsRecovery({ ok: false, status: 403, data: {} })).toBe(false);
    expect(serviceMutationNeedsRecovery({ ok: true, status: 200, data: { outcome: "issued" } })).toBe(false);
  });
  it("管理入口返回路径只允许工作台内路径", () => {
    expect(serviceReturnPath("/work/reward-inventory?campaign=cmp_a")).toBe("/work/reward-inventory?campaign=cmp_a");
    for (const raw of ["https://other.example", "//other.example/work", "/public", "/work/../api", "/work\\evil"]) expect(serviceReturnPath(raw)).toBeNull();
  });
});
