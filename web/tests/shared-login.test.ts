// @vitest-environment jsdom

import * as React from "react";
import { act, createElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterAll, afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { fetchMock, router } = vi.hoisted(() => {
  const fetchMock = vi.fn();
  vi.stubGlobal("fetch", fetchMock);
  return { fetchMock, router: { replace: vi.fn() } };
});
vi.mock("next/navigation", () => ({
  useRouter: () => router,
  usePathname: () => "/",
}));

import AdminPage from "@/app/admin/page";
import { SharedLogin } from "@/components/login/SharedLogin";
import { MerchantGate } from "@/components/work/merchant-session";
import {
  AUTH_PORTAL_LOGIN_URL,
  buildPortalLoginUrl,
  canUsePortalSso,
  isPortalSsoHost,
  portalBrowser,
  startPortalSso,
} from "@/lib/portal-sso";

let root: Root;
let container: HTMLDivElement;
let savedSsoEnv: string | undefined;

beforeEach(() => {
  savedSsoEnv = process.env.NEXT_PUBLIC_TOUCH_SSO_ENABLED;
  delete process.env.NEXT_PUBLIC_TOUCH_SSO_ENABLED;
  vi.stubGlobal("React", React);
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  window.localStorage.clear();
  fetchMock.mockReset();
  router.replace.mockClear();
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(async () => {
  if (savedSsoEnv === undefined) {
    delete process.env.NEXT_PUBLIC_TOUCH_SSO_ENABLED;
  } else {
    process.env.NEXT_PUBLIC_TOUCH_SSO_ENABLED = savedSsoEnv;
  }
  await act(async () => root.unmount());
  container.remove();
  vi.restoreAllMocks();
});

afterAll(() => vi.unstubAllGlobals());

describe("portal-sso 品牌域与安全跳转边界", () => {
  it("未显式配置 NEXT_PUBLIC_TOUCH_SSO_ENABLED=1 时默认关闭；仅显式开启且处于官方生态域才返回 true", () => {
    expect(isPortalSsoHost("touch.modelxing.com")).toBe(true);
    expect(isPortalSsoHost("modelxing.com")).toBe(true);
    expect(isPortalSsoHost("evil-modelxing.com")).toBe(false);
    expect(isPortalSsoHost("brand.partner.example")).toBe(false);

    // 门户未打通前默认关闭，哪怕是官方生态域也不自动弹出 SSO 入口（防死循环）
    expect(canUsePortalSso({ protocol: "https:", hostname: "touch.modelxing.com" })).toBe(false);
    expect(canUsePortalSso({ protocol: "https:", hostname: "touch-followup.modelxing.com" })).toBe(false);
    expect(canUsePortalSso({ protocol: "https:", hostname: "touch.modelxing.com" }, "")).toBe(false);
    expect(canUsePortalSso({ protocol: "https:", hostname: "touch.modelxing.com" }, "0")).toBe(false);
    expect(canUsePortalSso({ protocol: "https:", hostname: "touch.modelxing.com" }, "false")).toBe(false);
    expect(canUsePortalSso({ protocol: "https:", hostname: "touch.modelxing.com" }, "true")).toBe(false);

    // 仅显式配置 "1" 且处于 https 官方生态域才开启
    expect(canUsePortalSso({ protocol: "https:", hostname: "touch.modelxing.com" }, "1")).toBe(true);
    expect(canUsePortalSso({ protocol: "https:", hostname: "touch-followup.modelxing.com" }, "1")).toBe(true);
    expect(canUsePortalSso({ protocol: "https:", hostname: "modelxing.com" }, "1")).toBe(true);

    // 非 HTTPS、第三方白标品牌域、loopback 一律拒绝
    expect(canUsePortalSso({ protocol: "http:", hostname: "touch.modelxing.com" }, "1")).toBe(false);
    expect(canUsePortalSso({ protocol: "https:", hostname: "brand.partner.example" }, "1")).toBe(false);
    expect(canUsePortalSso({ protocol: "http:", hostname: "127.0.0.1" }, "1")).toBe(false);
    expect(canUsePortalSso({ protocol: "https:", hostname: "evil-modelxing.com" }, "1")).toBe(false);
  });

  it("buildPortalLoginUrl 正确拼接 app=touch-engine、安全 next 与 prompt=login，拒收开放重定向与非生态域", () => {
    expect(buildPortalLoginUrl()).toBe(`${AUTH_PORTAL_LOGIN_URL}?app=touch-engine`);
    expect(
      buildPortalLoginUrl({
        currentOrigin: "https://touch.modelxing.com",
        currentPathname: "/work/stores",
      }),
    ).toBe(
      "https://auth.modelxing.com/login?app=touch-engine&next=https%3A%2F%2Ftouch.modelxing.com%2Fwork%2Fstores",
    );
    expect(
      buildPortalLoginUrl({
        promptLogin: true,
        currentOrigin: "https://touch.modelxing.com",
        next: "/admin",
      }),
    ).toBe(
      "https://auth.modelxing.com/login?app=touch-engine&prompt=login&next=https%3A%2F%2Ftouch.modelxing.com%2Fadmin",
    );
    // 第三方白标域名与开放重定向 next 绝不透传
    expect(
      buildPortalLoginUrl({
        promptLogin: true,
        currentOrigin: "https://brand.partner.example",
        next: "//evil.example",
      }),
    ).toBe("https://auth.modelxing.com/login?app=touch-engine&prompt=login");
    expect(
      buildPortalLoginUrl({
        currentOrigin: "https://touch.modelxing.com",
        next: "/api/v1/whoami",
        currentPathname: "//evil.example",
      }),
    ).toBe("https://auth.modelxing.com/login?app=touch-engine&next=https%3A%2F%2Ftouch.modelxing.com%2F");
  });

  it("startPortalSso 在无 window（SSR）下安全无异常", () => {
    const originalWindow = globalThis.window;
    // @ts-expect-error simulating SSR environment without window
    delete globalThis.window;
    try {
      expect(() => startPortalSso()).not.toThrow();
    } finally {
      globalThis.window = originalWindow;
    }
  });
});

describe("SharedLogin 标签与 SSO 按钮行为", () => {
  it("密码标识输入框使用「邮箱」标签（不再是「用户名」）", async () => {
    await act(async () => {
      root.render(createElement(SharedLogin, { signIn: async () => ({}) }));
    });
    const labels = Array.from(container.querySelectorAll("label")).map((el) => el.textContent?.trim());
    expect(labels).toContain("邮箱");
    expect(labels).not.toContain("用户名");
  });

  it("显式挂载 sso.available=true 时渲染「使用生态账号登录」次级按钮并触发 start", async () => {
    const start = vi.fn();
    await act(async () => {
      root.render(
        createElement(SharedLogin, {
          signIn: async () => ({}),
          sso: { available: true, start },
        }),
      );
    });
    const ssoBtn = Array.from(container.querySelectorAll("button")).find(
      (btn) => btn.textContent?.trim() === "使用生态账号登录",
    );
    expect(ssoBtn).toBeDefined();
    await act(async () => {
      ssoBtn!.click();
    });
    expect(start).toHaveBeenCalledTimes(1);
  });

  it("未显式开启 NEXT_PUBLIC_TOUCH_SSO_ENABLED=1 时，生态域下也不显示 SSO 按钮", async () => {
    vi.spyOn(portalBrowser, "location").mockReturnValue({
      protocol: "https:",
      hostname: "touch.modelxing.com",
      origin: "https://touch.modelxing.com",
      pathname: "/",
    });
    await act(async () => {
      root.render(createElement(SharedLogin, { signIn: async () => ({}) }));
    });
    const ssoBtn = Array.from(container.querySelectorAll("button")).find(
      (btn) => btn.textContent?.trim() === "使用生态账号登录",
    );
    expect(ssoBtn).toBeUndefined();
  });

  it("显式开启 NEXT_PUBLIC_TOUCH_SSO_ENABLED=1 且处于生态域时自动展示 SSO 按钮，白标域仍不展示", async () => {
    process.env.NEXT_PUBLIC_TOUCH_SSO_ENABLED = "1";
    const locSpy = vi.spyOn(portalBrowser, "location").mockReturnValue({
      protocol: "https:",
      hostname: "brand.partner.example",
      origin: "https://brand.partner.example",
      pathname: "/",
    });
    await act(async () => {
      root.render(createElement(SharedLogin, { signIn: async () => ({}) }));
    });
    expect(
      Array.from(container.querySelectorAll("button")).find((btn) => btn.textContent?.trim() === "使用生态账号登录"),
    ).toBeUndefined();

    await act(async () => root.unmount());
    root = createRoot(container);
    locSpy.mockReturnValue({
      protocol: "https:",
      hostname: "touch.modelxing.com",
      origin: "https://touch.modelxing.com",
      pathname: "/",
    });
    await act(async () => {
      root.render(createElement(SharedLogin, { signIn: async () => ({}) }));
    });
    expect(
      Array.from(container.querySelectorAll("button")).find((btn) => btn.textContent?.trim() === "使用生态账号登录"),
    ).toBeDefined();
  });
});

describe("MerchantGate 无团队席位（phase=none）白标隔离与先退出再切号", () => {
  it("白标品牌域或未开启 SSO 开关时不露出 auth.modelxing.com 与切换门户按钮，仅保留本地退出", async () => {
    process.env.NEXT_PUBLIC_TOUCH_SSO_ENABLED = "1";
    vi.spyOn(portalBrowser, "location").mockReturnValue({
      protocol: "https:",
      hostname: "brand.partner.example",
      origin: "https://brand.partner.example",
      pathname: "/work",
    });
    fetchMock.mockImplementation(async (input: string) => {
      const path = new URL(input, window.location.href).pathname;
      if (path === "/api/session/memberships") {
        return new Response(JSON.stringify({ items: [] }), {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      }
      if (path === "/api/auth/logout") {
        return new Response(JSON.stringify({ ok: true }), {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      }
      throw new Error(`Unexpected request: ${path}`);
    });

    await act(async () => {
      root.render(createElement(MerchantGate, null, createElement("div", null, "workspace")));
    });

    expect(container.textContent).toContain("当前账号没有可进入的组织。");
    expect(container.querySelector('[data-action="switch-account"]')).toBeNull();
    expect(container.innerHTML).not.toContain("auth.modelxing.com");

    const logoutBtn = container.querySelector<HTMLButtonElement>('button[data-action="logout"]');
    expect(logoutBtn).not.toBeNull();
    await act(async () => {
      logoutBtn!.click();
    });
    expect(container.querySelector('input[type="password"]')).not.toBeNull();
  });

  it("生态域且开启 SSO 开关时，点击切换其他账号先 await 本地退出再跳门户 prompt=login", async () => {
    process.env.NEXT_PUBLIC_TOUCH_SSO_ENABLED = "1";
    vi.spyOn(portalBrowser, "location").mockReturnValue({
      protocol: "https:",
      hostname: "touch.modelxing.com",
      origin: "https://touch.modelxing.com",
      pathname: "/work/stores",
    });
    const events: string[] = [];
    const assignSpy = vi.spyOn(portalBrowser, "assign").mockImplementation((url) => {
      events.push(`navigate:${url}`);
    });
    let resolveLogout!: (res: Response) => void;
    fetchMock.mockImplementation(async (input: string) => {
      const path = new URL(input, window.location.href).pathname;
      if (path === "/api/session/memberships") {
        return new Response(JSON.stringify({ items: [] }), {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      }
      if (path === "/api/auth/logout") {
        events.push("logout:start");
        return new Promise<Response>((resolve) => {
          resolveLogout = (res) => {
            events.push("logout:done");
            resolve(res);
          };
        });
      }
      throw new Error(`Unexpected request: ${path}`);
    });

    await act(async () => {
      root.render(createElement(MerchantGate, null, createElement("div", null, "workspace")));
    });

    const switchBtn = container.querySelector<HTMLButtonElement>('button[data-action="switch-account"]');
    expect(switchBtn).not.toBeNull();
    expect(switchBtn?.textContent?.trim()).toBe("切换其他账号");

    await act(async () => {
      switchBtn!.click();
    });
    // 本地退出尚未完成前绝不提前跳转门户
    expect(events).toEqual(["logout:start"]);
    expect(assignSpy).not.toHaveBeenCalled();

    await act(async () => {
      resolveLogout(new Response(JSON.stringify({ ok: true }), { status: 200 }));
    });
    expect(events).toEqual([
      "logout:start",
      "logout:done",
      "navigate:https://auth.modelxing.com/login?app=touch-engine&prompt=login&next=https%3A%2F%2Ftouch.modelxing.com%2Fwork%2Fstores",
    ]);
  });

  it("点击切换其他账号时若本地退出失败，不跳转门户并展示错误提示", async () => {
    process.env.NEXT_PUBLIC_TOUCH_SSO_ENABLED = "1";
    vi.spyOn(portalBrowser, "location").mockReturnValue({
      protocol: "https:",
      hostname: "touch.modelxing.com",
      origin: "https://touch.modelxing.com",
      pathname: "/work",
    });
    const assignSpy = vi.spyOn(portalBrowser, "assign").mockImplementation(() => {});
    fetchMock.mockImplementation(async (input: string) => {
      const path = new URL(input, window.location.href).pathname;
      if (path === "/api/session/memberships") {
        return new Response(JSON.stringify({ items: [] }), {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      }
      if (path === "/api/auth/logout") {
        return new Response(JSON.stringify({ error: "unavailable" }), { status: 500 });
      }
      throw new Error(`Unexpected request: ${path}`);
    });

    await act(async () => {
      root.render(createElement(MerchantGate, null, createElement("div", null, "workspace")));
    });

    const switchBtn = container.querySelector<HTMLButtonElement>('button[data-action="switch-account"]');
    expect(switchBtn).not.toBeNull();
    await act(async () => {
      switchBtn!.click();
    });
    expect(assignSpy).not.toHaveBeenCalled();
    expect(container.querySelector('[data-state="error"]')?.textContent).toContain("退出没有完成");
  });
});

describe("AdminPage 无团队席位（orgPhase=none）白标隔离与先退出再切号", () => {
  it("白标品牌域下不展示切换门户按钮且 DOM 不含 auth.modelxing.com", async () => {
    process.env.NEXT_PUBLIC_TOUCH_SSO_ENABLED = "1";
    vi.spyOn(portalBrowser, "location").mockReturnValue({
      protocol: "https:",
      hostname: "brand.partner.example",
      origin: "https://brand.partner.example",
      pathname: "/admin",
    });
    fetchMock.mockImplementation(async (input: string) => {
      const path = new URL(input, window.location.href).pathname;
      if (path === "/api/session/memberships") {
        return new Response(JSON.stringify({ items: [] }), {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      }
      throw new Error(`Unexpected request: ${path}`);
    });

    await act(async () => {
      root.render(createElement(AdminPage));
    });

    expect(container.textContent).toContain("当前账号没有可进入的组织。");
    expect(container.querySelector('[data-action="switch-account"]')).toBeNull();
    expect(container.innerHTML).not.toContain("auth.modelxing.com");
    expect(container.querySelector('button[data-action="logout"]')).not.toBeNull();
  });

  it("生态域且开启 SSO 开关时，点击切换其他账号先 await 本地退出再跳门户 /admin", async () => {
    process.env.NEXT_PUBLIC_TOUCH_SSO_ENABLED = "1";
    vi.spyOn(portalBrowser, "location").mockReturnValue({
      protocol: "https:",
      hostname: "touch.modelxing.com",
      origin: "https://touch.modelxing.com",
      pathname: "/admin",
    });
    const events: string[] = [];
    vi.spyOn(portalBrowser, "assign").mockImplementation((url) => {
      events.push(`navigate:${url}`);
    });
    fetchMock.mockImplementation(async (input: string) => {
      const path = new URL(input, window.location.href).pathname;
      if (path === "/api/session/memberships") {
        return new Response(JSON.stringify({ items: [] }), {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      }
      if (path === "/api/auth/logout") {
        events.push("logout");
        return new Response(JSON.stringify({ ok: true }), { status: 200 });
      }
      throw new Error(`Unexpected request: ${path}`);
    });

    await act(async () => {
      root.render(createElement(AdminPage));
    });

    const switchBtn = container.querySelector<HTMLButtonElement>('button[data-action="switch-account"]');
    expect(switchBtn).not.toBeNull();
    await act(async () => {
      switchBtn!.click();
    });
    expect(events).toEqual([
      "logout",
      "navigate:https://auth.modelxing.com/login?app=touch-engine&prompt=login&next=https%3A%2F%2Ftouch.modelxing.com%2Fadmin",
    ]);
  });
});
