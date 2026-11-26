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

import { SharedLogin } from "@/components/login/SharedLogin";
import { MerchantGate } from "@/components/work/merchant-session";
import {
  AUTH_PORTAL_LOGIN_URL,
  buildPortalLoginUrl,
  canUsePortalSso,
  isPortalSsoHost,
  startPortalSso,
} from "@/lib/portal-sso";

let root: Root;
let container: HTMLDivElement;

beforeEach(() => {
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
  await act(async () => root.unmount());
  container.remove();
  vi.restoreAllMocks();
});

afterAll(() => vi.unstubAllGlobals());

describe("portal-sso 品牌域与安全跳转边界", () => {
  it("只在官方生态域 https://*.modelxing.com 自动启用 SSO，白标自定义品牌域与普通 loopback 不越界启用", () => {
    expect(isPortalSsoHost("touch.modelxing.com")).toBe(true);
    expect(isPortalSsoHost("modelxing.com")).toBe(true);
    expect(isPortalSsoHost("evil-modelxing.com")).toBe(false);
    expect(isPortalSsoHost("brand.partner.example")).toBe(false);

    expect(canUsePortalSso({ protocol: "https:", hostname: "touch.modelxing.com" })).toBe(true);
    expect(canUsePortalSso({ protocol: "https:", hostname: "touch-followup.modelxing.com" })).toBe(true);
    expect(canUsePortalSso({ protocol: "http:", hostname: "touch.modelxing.com" })).toBe(false);
    expect(canUsePortalSso({ protocol: "https:", hostname: "brand.partner.example" })).toBe(false);
    expect(canUsePortalSso({ protocol: "http:", hostname: "127.0.0.1" })).toBe(false);
    // 显式开关允许 loopback 联调，但仍拒绝第三方白标品牌域
    expect(canUsePortalSso({ protocol: "http:", hostname: "127.0.0.1" }, "1")).toBe(true);
    expect(canUsePortalSso({ protocol: "https:", hostname: "brand.partner.example" }, "1")).toBe(false);
    // 显式关闭优先
    expect(canUsePortalSso({ protocol: "https:", hostname: "touch.modelxing.com" }, "0")).toBe(false);
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

  it("默认本地/非生态域环境下不越界显示生态账号 SSO 按钮", async () => {
    await act(async () => {
      root.render(createElement(SharedLogin, { signIn: async () => ({}) }));
    });
    const ssoBtn = Array.from(container.querySelectorAll("button")).find(
      (btn) => btn.textContent?.trim() === "使用生态账号登录",
    );
    expect(ssoBtn).toBeUndefined();
  });
});

describe("MerchantGate 无团队席位（phase=none）切换账号引导", () => {
  it("无组织席位时展示带 prompt=login 的切换其他账号链接与退出按钮，不困死用户", async () => {
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
    const switchLink = container.querySelector<HTMLAnchorElement>('a[data-action="switch-account"]');
    expect(switchLink).not.toBeNull();
    expect(switchLink?.textContent?.trim()).toBe("切换其他账号");
    expect(switchLink?.getAttribute("href")).toContain("https://auth.modelxing.com/login?app=touch-engine&prompt=login");

    const logoutBtn = container.querySelector<HTMLButtonElement>('button[data-action="logout"]');
    expect(logoutBtn).not.toBeNull();
    await act(async () => {
      logoutBtn!.click();
    });
    // 退出后回到登录表单
    expect(container.querySelector('input[type="password"]')).not.toBeNull();
  });
});
