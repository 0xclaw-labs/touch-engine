import test from "node:test";
import assert from "node:assert/strict";
import { createBuiltInAuth } from "../src/builtInAuth.ts";
import { ApiFailure } from "../src/apiError.ts";

function fetchMock(routes: Record<string, { status: number; body?: string }>, calls: { url: string; init?: RequestInit }[]) {
  return (async (url: string, init?: RequestInit) => {
    calls.push({ url, init });
    const hit = Object.entries(routes).find(([pattern]) => url.includes(pattern));
    if (!hit) throw new Error("unexpected url " + url);
    return new Response(hit[1].body ?? null, { status: hit[1].status });
  }) as typeof fetch;
}

test("start 发 POST /api/auth/email/code/start 且 body 含 email", async () => {
  const calls: { url: string; init?: RequestInit }[] = [];
  const auth = createBuiltInAuth(fetchMock({ "/code/start": { status: 200 } }, calls));
  await auth.startEmailCode("a@b.c");
  assert.equal(calls[0].url, "/api/auth/email/code/start");
  assert.equal(calls[0].init?.method, "POST");
  assert.equal(String(calls[0].init?.body), JSON.stringify({ email: "a@b.c" }));
});

test("verify 2xx 返回解析后的 JSON 会话数据", async () => {
  const auth = createBuiltInAuth(fetchMock({ "/code/verify": { status: 200, body: '{"token":"t1"}' } }, []));
  const result = await auth.verifyEmailCode("a@b.c", "123456");
  assert.deepEqual(result, { token: "t1" });
});

test("verify 401 归一为 invalid_otp 的 ApiFailure", async () => {
  const auth = createBuiltInAuth(fetchMock({ "/code/verify": { status: 401, body: "unauthorized" } }, []));
  await assert.rejects(auth.verifyEmailCode("a@b.c", "000000"), (e: ApiFailure) => e.code === "invalid_otp" && e.status === 401);
});

test("password 失败透传响应体 error 字段为 code", async () => {
  const auth = createBuiltInAuth(fetchMock({ "/auth/password": { status: 403, body: '{"error":"invalid_credentials"}' } }, []));
  await assert.rejects(auth.signIn("u", "p"), (e: ApiFailure) => e.code === "invalid_credentials");
});
