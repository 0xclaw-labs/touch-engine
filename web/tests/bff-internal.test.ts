import { describe, expect, it, vi } from "vitest";
import { bffPathRefusesInternal, proxyToServer, type BffEnv } from "../src/lib/bff";

// HUI-4595: 公共 BFF 不得透传 /api/v1/internal/*(受信成片回流缝,仅限
// content_return_producer 服务 token,Go 直连 loopback)。浏览器无论用经典
// BFF 形(/api/internal/...)还是 Go 绝对直通形(/api/v1/internal/...)都拿到
// 统一 404,请求绝不转发;merchant 会话也不能借 BFF 伪造回流。

const ENV: BffEnv = { serverUrl: "http://127.0.0.1:18240", internalToken: "bff-internal-secret" };

describe("BFF internal 段拒透传(HUI-4595)", () => {
  it.each([
    [["internal", "content-production-handoffs", "cph_1", "results"]],
    [["v1", "internal", "content-production-handoffs", "cph_1", "results"]],
  ])("识别 internal 段(含 v1 直通形): %j", (path) => {
    expect(bffPathRefusesInternal(path)).toBe(true);
  });

  it.each([
    [["stores", "st_1", "content-production-handoffs"]],
    [["v1", "public", "content", "media", "tok"]],
    [["content-handoffs", "cho_1", "queue-view"]],
  ])("非 internal 路径不受影响: %j", (path) => {
    expect(bffPathRefusesInternal(path)).toBe(false);
  });

  it("proxyToServer 对 internal 路径统一 404 且绝不发起上游请求", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    for (const path of [
      ["internal", "content-production-handoffs", "cph_1", "results"],
      ["v1", "internal", "content-production-handoffs", "cph_1", "results"],
    ]) {
      const req = new Request("http://web.local/api/" + path.join("/"), {
        method: "POST",
        headers: { cookie: "touch_session=sess-owner-a" },
        body: JSON.stringify({ task_ref: "t" }),
      });
      const res = await proxyToServer(req, ENV, path);
      expect(res.status).toBe(404);
      const body = (await res.json()) as { error?: string };
      expect(body.error).toBe("not_found");
    }
    expect(fetchSpy).not.toHaveBeenCalled();
    fetchSpy.mockRestore();
  });

  it("拒绝与配置无关:未配置(503 config gate)也绝不触发上游 fetch", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const res = await proxyToServer(
      new Request("http://web.local/api/internal/x", { method: "POST" }),
      { serverUrl: "", internalToken: "" },
      ["internal", "x"],
    );
    // internal 拒绝优先级说明:配置缺失答 503 config gate(同样零转发);
    // 配置齐全时 internal 段答统一 404。两者都绝不 fetch。
    expect([404, 503]).toContain(res.status);
    expect(fetchSpy).not.toHaveBeenCalled();
    fetchSpy.mockRestore();
  });
});
