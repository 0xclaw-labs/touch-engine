import { beforeEach, expect, it, vi } from "vitest";
import { NextRequest } from "next/server";
const exchange = vi.hoisted(() => vi.fn());
vi.mock("../src/lib/sso-bridge", async (orig) => ({ ...await orig<typeof import("../src/lib/sso-bridge")>(), exchangeWithIdentity: exchange }));
import { GET } from "../src/app/api/auth/sso/landing/route";
import { POST } from "../src/app/api/auth/sso/exchange/route";
import { middleware } from "../src/middleware";
beforeEach(() => { process.env.SSO_APP_HOST = "touch-followup.modelxing.com"; exchange.mockReset(); });
it("keeps original code+h through registered root into normal SSO", async () => { const h = "hnd_" + "b".repeat(32); const req = new NextRequest(`https://touch-followup.modelxing.com/?code=original&h=${h}`, { headers: { host: "touch-followup.modelxing.com" } }); const redirect = middleware(req); expect(redirect.headers.get("location")).toBe(`https://touch-followup.modelxing.com/api/auth/sso/landing?code=original&h=${h}`); const res = await GET(new NextRequest(redirect.headers.get("location")!, { headers: { host: "touch-followup.modelxing.com" } })); const html = await res.text(); expect(html).toContain('name="code" value="original"'); expect(html).toContain(`/lead-follow-up-return/${h}`); });
it("fails closed without configured canonical host", async () => { delete process.env.SSO_APP_HOST; expect((await GET(new NextRequest("https://unknown.example/api/auth/sso/landing?code=x", { headers: { host: "unknown.example" } }))).status).toBe(400); });
it("uses the configured canonical HTTPS host behind an internal reverse proxy URL", () => {
  const h = "hnd_" + "b".repeat(32);
  const response = middleware(new NextRequest(`http://localhost:19961/?code=original&h=${h}`, { headers: { host: "touch-followup.modelxing.com", "x-forwarded-host": "touch-followup.modelxing.com" } }));
  expect(response.headers.get("location")).toBe(`https://touch-followup.modelxing.com/api/auth/sso/landing?code=original&h=${h}`);
  expect(response.headers.get("cache-control")).toBe("no-store");
});
it("does not relay the code to an untrusted forwarded host", () => {
  const response = middleware(new NextRequest("http://localhost:19961/?code=original", { headers: { host: "touch-followup.modelxing.com", "x-forwarded-host": "evil.example" } }));
  expect(response.status).toBe(400);
  expect(response.headers.get("location")).toBeNull();
});
it("forwards only Identity-issued canonical cookies after same-origin exchange", async () => { exchange.mockResolvedValue({ status: 200, setCookie: ["touch-engine_session=identity-cookie; Secure; HttpOnly; Path=/"] }); const res = await POST(new NextRequest("https://touch-followup.modelxing.com/api/auth/sso/exchange", { method: "POST", headers: { host: "touch-followup.modelxing.com", origin: "https://touch-followup.modelxing.com", "content-type": "application/x-www-form-urlencoded" }, body: "code=original&return_target=/lead-follow-up-return/hnd_" + "b".repeat(32) })); expect(res.status).toBe(303); expect(res.headers.get("set-cookie")).toContain("touch-engine_session=identity-cookie"); expect(res.headers.get("location")).toContain("/lead-follow-up-return/hnd_"); });
it("rejects cross-origin or missing Identity session cookie", async () => { const make = (origin: string) => new NextRequest("https://touch-followup.modelxing.com/api/auth/sso/exchange", { method: "POST", headers: { host: "touch-followup.modelxing.com", origin, "content-type": "application/x-www-form-urlencoded" }, body: "code=original" }); expect((await POST(make("https://evil.example"))).status).toBe(400); expect(exchange).not.toHaveBeenCalled(); exchange.mockResolvedValue({ status: 200, setCookie: [] }); expect((await POST(make("https://touch-followup.modelxing.com"))).status).toBe(400); });
