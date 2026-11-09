import { afterEach, describe, expect, it, vi } from "vitest";

import {
  exchangeResumeCredential,
  issueCustomerResume,
  mintMediaAccess,
  sessionStatus,
  startOrResumeSession,
  sessionStatusLabel,
  switchSessionContent,
  type ActionSessionView,
} from "../src/lib/store-session";
import { parseHandoffPlan, parseHandoffStatus, planCopy, statusFace } from "../src/lib/store-handoff";

// HUI-1896 C2 顾客消费面:3546 匿名会话(进门幂等=同浏览器恢复;凭据只在
// 内存返回值,永不落 localStorage) + 1670 handoff v2 契约(fixture 只进本
// 测试做本地契约验证;提供者合流前生产面给诚实待办态,待 1670 联验)。

const ok = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("startOrResumeSession:进门幂等恢复", () => {
  it("POST /api/public/actions/{id}/sessions 带 source 与幂等键", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      expect(String(input)).toBe("/api/public/actions/sa_a1/sessions");
      return ok({ action_session_id: "as_1", status: "active", created_at: "t0" }, 201);
    });
    vi.stubGlobal("fetch", fetchMock);
    const res = await startOrResumeSession("sa_a1", { source: "qr" });
    if (!("session" in res) || !res.session) throw new Error("expected session");
    expect((res.session as ActionSessionView).action_session_id).toBe("as_1");
    const init = (fetchMock.mock.calls[0] as unknown[])[1] as RequestInit;
    expect(init.method).toBe("POST");
    const body = JSON.parse(String(init.body));
    expect(body.touchpoint_source).toBe("qr");
    expect(body.idempotency_key).toBeTruthy();
  });

  it("同 actionId 重放:同会话(幂等键稳定),不重开", async () => {
    const fetchMock = vi.fn(async () => ok({ action_session_id: "as_1", status: "active" }, 200));
    vi.stubGlobal("fetch", fetchMock);
    const first = await startOrResumeSession("sa_a1", {});
    const second = await startOrResumeSession("sa_a1", {});
    const key1 = JSON.parse(String(((fetchMock.mock.calls[0] as unknown[])[1] as RequestInit).body)).idempotency_key;
    const key2 = JSON.parse(String(((fetchMock.mock.calls[1] as unknown[])[1] as RequestInit).body)).idempotency_key;
    expect(key1).toBe(key2);
    expect(first).toEqual(second);
  });

  it("action_unavailable(409) → {error:'action_unavailable'} 诚实态", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => ok({ error: "action_unavailable" }, 409)));
    const res = await startOrResumeSession("sa_a1", {});
    expect(res).toEqual({ error: "action_unavailable" });
  });

  it("网络异常 → {error:'network'}(不判成功/失败)", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => { throw new Error("down"); }));
    const res = await startOrResumeSession("sa_a1", {});
    expect(res).toEqual({ error: "network" });
  });
});

describe("sessionStatus / switchSessionContent:恢复与换选", () => {
  it("status GET 只读;解析 selected_content_pack_version 与实时位", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      expect(String(input)).toBe("/api/public/action-sessions/as_1/status");
      return ok({
        action_session_id: "as_1", status: "active", resume_available: true,
        action_available: true, selected_content_pack_version: "v7",
        participation: { id: "pt_1", status: "pending", benefit_reservation_state: "confirmed" },
      });
    }));
    const res = await sessionStatus("as_1");
    if (!("session" in res) || !res.session) throw new Error("expected session");
    expect(res.session.selected_content_pack_version).toBe("v7");
    expect(res.session.participation?.id).toBe("pt_1");
  });

  it("换选 POST content;冻结(409 frozen) → {error:'frozen'} 不动已交接版本", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      expect(String(input)).toBe("/api/public/action-sessions/as_1/content");
      expect(init?.method).toBe("POST");
      return ok({ error: "frozen", message: "this draft is frozen" }, 409);
    });
    vi.stubGlobal("fetch", fetchMock);
    const res = await switchSessionContent("as_1", "v9");
    expect(res).toEqual({ error: "frozen" });
  });

  it("未知会话(404) → {error:'not_found'}(非网络错误)", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => ok({ error: "not_found" }, 404)));
    const res = await sessionStatus("as_x");
    expect(res).toEqual({ error: "not_found" });
  });
});

describe("凭据:customer_resume 只经内存返回值", () => {
  it("签发 POST,201 返回 token;永不写 localStorage", async () => {
    const setItem = vi.fn();
    vi.stubGlobal("localStorage", { setItem });
    vi.stubGlobal("fetch", vi.fn(async () => ok({ credential_id: "rc_1", purpose: "customer_resume", token: "rsk_abc", expires_at: "t1" }, 201)));
    const cred = await issueCustomerResume("as_1");
    expect(cred?.token).toBe("rsk_abc");
    expect(cred?.purpose).toBe("customer_resume");
    expect(setItem).not.toHaveBeenCalled();
  });

  it("兑换 POST /resume/exchange;token 走 POST 体不进 URL", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      expect(String(input)).not.toContain("rsk_");
      expect(String(input)).toBe("/api/public/resume/exchange");
      expect(init?.method).toBe("POST");
      expect(JSON.parse(String(init?.body)).token).toBe("rsk_abc");
      return ok({ action_session_id: "as_9", status: "active", resume_available: true });
    });
    vi.stubGlobal("fetch", fetchMock);
    const res = await exchangeResumeCredential("rsk_abc");
    if (!("session" in res) || !res.session) throw new Error("expected session");
    expect(res.session.action_session_id).toBe("as_9");
  });

  it("兑换失败:expired(410)/used(409)/target_mismatch(403) 各自真实错误", async () => {
    for (const [status, code, expected] of [
      [410, "resume_expired", "resume_expired"],
      [409, "resume_used", "resume_used"],
      [403, "target_mismatch", "target_mismatch"],
    ] as const) {
      vi.stubGlobal("fetch", vi.fn(async () => ok({ error: code }, status)));
      const res = await exchangeResumeCredential("rsk_x");
      expect(res).toEqual({ error: expected });
      vi.unstubAllGlobals();
    }
  });
});

describe("mintMediaAccess:预览/下载短 TTL 凭据", () => {
  it("POST media-access 带 action_session_id 与 purpose=preview", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      expect(String(input)).toBe("/api/public/content/v7/media-access");
      const body = JSON.parse(String(init?.body));
      expect(body.action_session_id).toBe("as_1");
      expect(body.purpose).toBe("preview");
      expect(body.media_ids).toEqual(["md_1"]);
      return ok({ version_id: "v7", purpose: "preview", items: [{ media_id: "md_1", url: "/api/public/content/media/tok", expires_at: "t2", mime_type: "video/mp4", byte_size: 10, sha256: "ab", filename: "a.mp4" }] });
    });
    vi.stubGlobal("fetch", fetchMock);
    const res = await mintMediaAccess("v7", { actionSessionId: "as_1", mediaIds: ["md_1"], purpose: "preview" });
    if (!("items" in res) || !res.items) throw new Error("expected items");
    expect(res.items[0].url).toBe("/api/public/content/media/tok");
  });

  it("purpose 白名单:非法 purpose 本地拒绝(不发请求)", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    const res = await mintMediaAccess("v7", { actionSessionId: "as_1", mediaIds: [], purpose: "hack" });
    expect(res).toEqual({ error: "bad_purpose" });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("版本不可分发(409) → {error:'undistributable'}(素材待办态)", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => ok({ error: "content_blocked" }, 409)));
    const res = await mintMediaAccess("v7", { actionSessionId: "as_1", mediaIds: ["md_1"], purpose: "preview" });
    expect(res).toEqual({ error: "undistributable" });
  });
});

describe("sessionStatusLabel:未知状态按未知", () => {
  it("active/ended/未知", () => {
    expect(sessionStatusLabel("active")).toContain("进行中");
    expect(sessionStatusLabel("ended")).toContain("结束");
    expect(sessionStatusLabel("weird")).toContain("未知");
  });
});

// ---- 1670 handoff v2 契约(草案 fixture;待 1670 真实提供者联验) ------------------

describe("parseHandoffPlan:1670 契约本地验证", () => {
  it("official_handoff plan:kind/client_action/unsupported_fields/evidence_level 解析", () => {
    const plan = parseHandoffPlan({
      handoff_id: "ho_1",
      plan: {
        kind: "official_handoff",
        client_action: { type: "open_platform_page", url: "https://www.douyin.com/creator", note: "在新页完成发布" },
        unsupported_fields: [],
        evidence_level: "platform_receipt",
      },
    });
    expect(plan?.handoffId).toBe("ho_1");
    expect(plan?.kind).toBe("official_handoff");
    expect(plan?.clientAction?.url).toBe("https://www.douyin.com/creator");
    expect(plan?.evidenceLevel).toBe("platform_receipt");
    expect(plan?.unsupportedFields).toEqual([]);
  });

  it("refused/manual_only/oauth_publish 三态解析", () => {
    expect(parseHandoffPlan({ plan: { kind: "refused", reason: "channel_not_admitted", reason_text: "未准入" } })?.kind).toBe("refused");
    expect(parseHandoffPlan({ plan: { kind: "manual_only", instructions: "保存素材后手动发布" } })?.kind).toBe("manual_only");
    expect(parseHandoffPlan({ plan: { kind: "oauth_publish" } })?.kind).toBe("oauth_publish");
  });

  it("未知 kind → 'unknown'(绝不渲染成功绿态)", () => {
    const plan = parseHandoffPlan({ plan: { kind: "mystery_kind" } });
    expect(plan?.kind).toBe("unknown");
  });

  it("非对象/缺 plan → null", () => {
    expect(parseHandoffPlan(null)).toBeNull();
    expect(parseHandoffPlan({})).toBeNull();
  });
});

describe("planCopy:plan.kind 四态文案(unknown 不绿态)", () => {
  it("official/oauth/manual/refused 各有真实文案;unknown 给未知语气", () => {
    expect(planCopy("official_handoff")).toContain("平台");
    expect(planCopy("oauth_publish")).toContain("授权");
    expect(planCopy("manual_only")).toContain("手动");
    expect(planCopy("refused")).toContain("未放行");
    const unknown = planCopy("unknown");
    expect(unknown).toContain("未知");
    expect(unknown).not.toMatch(/成功|完成|已发布/);
  });
});

describe("parseHandoffStatus / statusFace:交接状态诚实", () => {
  it("解析 handoff/attempt 状态", () => {
    const st = parseHandoffStatus({ handoff_id: "ho_1", status: "pending_external", attempt: { status: "submitted" } });
    expect(st?.status).toBe("pending_external");
    expect(st?.attemptStatus).toBe("submitted");
    expect(parseHandoffStatus(null)).toBeNull();
  });

  it("状态文案:等待/完成/失败/未知各有独立真实说法", () => {
    expect(statusFace("pending_external")).toContain("等待");
    expect(statusFace("platform_confirmed")).toContain("平台已确认");
    expect(statusFace("failed")).toContain("没有完成");
    const unknown = statusFace("weird");
    expect(unknown).toContain("未知");
    // 未知态必须明确否认成功(否定句允许出现「成功」字样)。
    expect(unknown).toContain("不算发布成功");
  });
});
