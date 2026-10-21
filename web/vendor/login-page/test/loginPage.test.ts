import test from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import { componentHarness, elements } from "./harness.ts";
import { ApiFailure } from "../src/apiError.ts";
import { createBuiltInAuth } from "../src/builtInAuth.ts";
import type { AuthAdapter, LoginPageProps } from "../src/types.ts";

const pagePath = fileURLToPath(new URL("../src/LoginPage.tsx", import.meta.url));

const baseProps = {
  brand: { name: "数海观澜AIGC" },
  onAuthenticated: () => {},
  navigate: () => {},
};

/** 让异步回调链（提交/自动提交）在断言前跑完。 */
function flush() {
  return new Promise<void>((resolve) => setTimeout(resolve, 0));
}

/** 内建 auth 模块桩：按 require 说明符挂载（localRequire 按 specifier 精确匹配）。 */
function authMock(overrides: Partial<AuthAdapter> = {}) {
  return {
    "./builtInAuth.ts": {
      createBuiltInAuth: () => ({
        startEmailCode: async () => {},
        verifyEmailCode: async () => ({}),
        signIn: async () => ({}),
        ...overrides,
      }),
    },
  };
}

/**
 * LoginPage 测试基座。桥接 "./apiError.ts" 为测试侧同一类实例：
 * vm 内 require 出的 CJS 编译实例与测试 ESM 实例在 tsx 下是两个类，
 * 不桥接则组件内 `err instanceof ApiFailure` 断裂，错误映射全落兜底文案。
 */
function pageHarness(extraMocks: Record<string, unknown> = {}) {
  return componentHarness(pagePath, { "./apiError.ts": { ApiFailure }, ...extraMocks });
}

/** 驱动到 verify 态：填邮箱 → 提交发码表单。 */
async function driveToVerify(
  harness: ReturnType<typeof componentHarness>,
  props: LoginPageProps,
) {
  const t0 = harness.render("LoginPage", props);
  const emailField = elements(t0).find((el) => el.props?.type === "email");
  assert.ok(emailField, "request 态应有邮箱输入");
  emailField.props.onChange("a@b.c");
  const t1 = harness.render("LoginPage", props);
  const form = elements(t1).find((el) => el.type === "form");
  assert.ok(form, "request 态应有发码表单");
  await form.props.onSubmit({ preventDefault() {} });
  return harness.render("LoginPage", props);
}

/** 驱动到认证完成：verify 态输入 6 位（触发自动提交）并等异步链落定。 */
async function driveVerified(
  harness: ReturnType<typeof componentHarness>,
  props: LoginPageProps,
) {
  const t2 = await driveToVerify(harness, props);
  const codeField = elements(t2).find((el) => el.props?.inputMode === "numeric");
  assert.ok(codeField, "verify 态应有验证码输入");
  codeField.props.onChange("123456");
  await flush();
  return harness.render("LoginPage", props);
}

function alertText(tree: unknown): string | undefined {
  const alert = elements(tree).find((el) => el.props?.role === "alert");
  return alert ? String(alert.props.children) : undefined;
}

test("request 态渲染邮箱输入与发码钮；提交后进入 verify 态并显示倒计时", async () => {
  const seen: string[] = [];
  let created = 0;
  const harness = pageHarness({
    "./builtInAuth.ts": {
      createBuiltInAuth: (() => {
        created += 1;
        return {
          startEmailCode: async (email: string) => void seen.push(email),
          verifyEmailCode: async () => ({}),
          signIn: async () => ({}),
        };
      }) as typeof createBuiltInAuth,
    },
  });
  const props = { ...baseProps, layout: "split" } as LoginPageProps;
  const t0 = harness.render("LoginPage", props);
  const els0 = elements(t0);
  const emailField = els0.find((el) => el.props?.type === "email");
  assert.ok(emailField, "request 态应有邮箱输入");
  const sendBtn = els0.find((el) => el.props?.type === "submit");
  assert.ok(sendBtn, "request 态应有发码钮");

  emailField.props.onChange("a@b.c");
  const t1 = harness.render("LoginPage", props);
  const form = elements(t1).find((el) => el.type === "form");
  assert.ok(form);
  await form.props.onSubmit({ preventDefault() {} });
  assert.deepEqual(seen, ["a@b.c"]);
  assert.ok(created >= 1, "不传 auth 时应零参构造内建客户端");

  const t2 = harness.render("LoginPage", props);
  const els2 = elements(t2);
  assert.ok(els2.some((el) => el.props?.inputMode === "numeric"), "verify 态应有验证码输入");
  assert.ok(JSON.stringify(t2).includes("a@b.c"), "verify 态应回显邮箱");
  const resendLine = els2.find((el) => {
    const kids = el.props?.children;
    const text = Array.isArray(kids) ? kids.map(String).join("") : typeof kids === "string" ? kids : "";
    return text.includes("秒后可重发");
  });
  assert.ok(resendLine, "verify 态应显示 60s 重发倒计时");
  assert.ok(
    !els2.some((el) => el.props?.children === "重发验证码"),
    "remain>0 时重发文字链应被抑制",
  );
});

test("compact 布局不渲染右栏媒体", () => {
  const harness = pageHarness({});
  const tree = harness.render("LoginPage", {
    ...baseProps,
    layout: "compact",
    brand: { name: "X", media: { src: "/media/login.mp4", poster: "/media/login-poster.jpg" } },
  } as LoginPageProps);
  assert.ok(!elements(tree).some((el) => el.type === "video"));
  assert.ok(!elements(tree).some((el) => el.type === "img"));
});

test("split 缺省渲染右栏 video；reduced-motion 退为 img 海报", () => {
  const media = { src: "/media/login.mp4", poster: "/media/login-poster.jpg" };
  const props = {
    ...baseProps,
    layout: "split",
    brand: { name: "X", media },
  } as LoginPageProps;

  const harness = pageHarness({});
  const tree = harness.render("LoginPage", props);
  const video = elements(tree).find((el) => el.type === "video");
  assert.ok(video, "split + media 应渲染 video");
  assert.equal(video?.props.src, media.src);
  assert.equal(video?.props.poster, media.poster);
  assert.ok(!elements(tree).some((el) => el.type === "img"));

  const rm = pageHarness({
    __globals: { window: { matchMedia: () => ({ matches: true }) } },
  });
  const t1 = rm.render("LoginPage", props);
  assert.ok(elements(t1).some((el) => el.type === "video"), "effect 前的首帧仍为 video");
  const t2 = rm.render("LoginPage", props);
  assert.ok(!elements(t2).some((el) => el.type === "video"), "reduced-motion 下不渲染 video");
  const img = elements(t2).find((el) => el.type === "img");
  assert.ok(img, "reduced-motion 下媒体退为 img");
  assert.equal(img?.props.src, media.poster);
});

test("输入 6 位验证码自动提交；成功后 onAuthenticated 并 navigate 缺省落点 /", async () => {
  let verified: { email: string; code: string } | null = null;
  const authed: unknown[] = [];
  const goneTo: string[] = [];
  const harness = pageHarness(
    authMock({
      verifyEmailCode: async (email: string, code: string) => {
        verified = { email, code };
        return { token: "t1" };
      },
    }),
  );
  const props = {
    ...baseProps,
    layout: "split",
    onAuthenticated: (r: unknown) => authed.push(r),
    navigate: (p: string) => goneTo.push(p),
  } as LoginPageProps;
  const final = await driveVerified(harness, props);
  assert.deepEqual(verified, { email: "a@b.c", code: "123456" });
  assert.deepEqual(authed, [{ token: "t1" }]);
  assert.deepEqual(goneTo, ["/"], "无 ?next 且无 override 时落 /");
  assert.equal(alertText(final), undefined, "成功路径不应留错误文案");
});

test("nextOverride 嵌入语义：compact 卡无媒体无衬底，认证后 navigate(nextOverride)", async () => {
  const goneTo: string[] = [];
  const harness = pageHarness(authMock());
  const props = {
    ...baseProps,
    layout: "split",
    nextOverride: "/console",
    navigate: (p: string) => goneTo.push(p),
    brand: { name: "X", media: { src: "/m.mp4", poster: "/p.jpg" } },
  } as LoginPageProps;

  const t0 = harness.render("LoginPage", props);
  assert.equal(t0.type, "div", "嵌入根为 div 非 main");
  assert.ok(String(t0.props.className).includes("compact"), "嵌入走 compact 卡（无页面衬底）");
  assert.ok(!elements(t0).some((el) => el.type === "video" || el.type === "img"), "嵌入无右栏媒体");

  await driveVerified(harness, props);
  assert.deepEqual(goneTo, ["/console"], "认证后 navigate(nextOverride)");
});

test("nextOverride 非站内路径被拒绝回 /；自定义 nextResolver 透传", async () => {
  const goneTo: string[] = [];
  const harness = pageHarness(authMock());
  await driveVerified(harness, {
    ...baseProps,
    nextOverride: "//evil.com",
    navigate: (p: string) => goneTo.push(p),
  } as LoginPageProps);
  assert.deepEqual(goneTo, ["/"], "协议相对路径不算站内");

  const goneTo2: string[] = [];
  const harness2 = pageHarness(authMock());
  await driveVerified(harness2, {
    ...baseProps,
    nextResolver: () => "/abc?x=1",
    navigate: (p: string) => goneTo2.push(p),
  } as LoginPageProps);
  assert.deepEqual(goneTo2, ["/abc?x=1"], "自定义 nextResolver 的返回透传给 navigate");
});

test("modes 语义：otp=false 固定密码态、password=false 固定验证码态、双开或双关显分段", () => {
  const h1 = pageHarness({});
  const t1 = h1.render("LoginPage", { ...baseProps, modes: { otp: false } } as LoginPageProps);
  const e1 = elements(t1);
  assert.ok(!e1.some((el) => el.props?.ariaLabel === "登录方式"), "otp=false 无分段");
  assert.ok(e1.some((el) => el.props?.type === "password"), "固定密码态");
  assert.ok(!e1.some((el) => el.props?.type === "email"), "无验证码入口");
  assert.ok(!JSON.stringify(t1).includes("邮箱验证码登录"), "固定密码态不渲染切回邮箱验证码登录的链");

  const h2 = pageHarness({});
  const t2 = h2.render("LoginPage", { ...baseProps, modes: { password: false } } as LoginPageProps);
  const e2 = elements(t2);
  assert.ok(!e2.some((el) => el.props?.ariaLabel === "登录方式"), "password=false 无分段");
  assert.ok(e2.some((el) => el.props?.type === "email"), "固定验证码态");
  assert.ok(!e2.some((el) => el.props?.type === "password"), "无密码入口");

  const h3 = pageHarness({});
  const t3 = h3.render("LoginPage", { ...baseProps } as LoginPageProps);
  assert.ok(elements(t3).some((el) => el.props?.ariaLabel === "登录方式"), "缺省双开显分段");

  const h4 = pageHarness({});
  const t4 = h4.render(
    "LoginPage",
    { ...baseProps, modes: { otp: false, password: false } } as LoginPageProps,
  );
  const e4 = elements(t4);
  assert.ok(e4.some((el) => el.props?.ariaLabel === "登录方式"), "双 false 回退为双开显分段");
  assert.ok(e4.some((el) => el.props?.type === "email"), "回退缺省 otp 态");
});

test("错误映射：invalid_otp/401 → 「验证码不正确或已过期」，messages.otpInvalid 可覆写", async () => {
  const harness = pageHarness(
    authMock({
      verifyEmailCode: async () => {
        throw new ApiFailure("bad code", 401, "invalid_otp");
      },
    }),
  );
  const props = { ...baseProps, layout: "split" } as LoginPageProps;
  const final = await driveVerified(harness, props);
  assert.equal(alertText(final), "验证码不正确或已过期");

  const harness2 = pageHarness(
    authMock({
      verifyEmailCode: async () => {
        throw new ApiFailure("bad code", 401, "invalid_otp");
      },
    }),
  );
  const final2 = await driveVerified(harness2, {
    ...baseProps,
    messages: { otpInvalid: "口令不对，看看有没有多敲一位。" },
  } as LoginPageProps);
  assert.equal(alertText(final2), "口令不对，看看有没有多敲一位。");
});

test("错误映射：密码 401 → 「用户名或密码不正确。」、500 → 连不上服务器；messages.generic 整体覆写", async () => {
  async function passwordFlow(signIn: AuthAdapter["signIn"], extra?: Partial<LoginPageProps>) {
    const harness = pageHarness(authMock({ signIn }));
    const props = { ...baseProps, layout: "split", ...extra } as LoginPageProps;
    const t0 = harness.render("LoginPage", props);
    const seg = elements(t0).find((el) => el.props?.ariaLabel === "登录方式");
    assert.ok(seg, "缺省双开应有分段");
    seg.props.onChange("password");
    const t1 = harness.render("LoginPage", props);
    const fields = elements(t1).filter((el) => el.props?.label);
    const usernameField = fields.find((el) => el.props.label === "用户名");
    const passwordField = fields.find((el) => el.props.type === "password");
    assert.ok(usernameField && passwordField, "密码态应有用户名与密码输入");
    usernameField.props.onChange("op1");
    passwordField.props.onChange("pw");
    const t2 = harness.render("LoginPage", props);
    const form = elements(t2).find((el) => el.type === "form");
    assert.ok(form);
    await form.props.onSubmit({ preventDefault() {} });
    await flush();
    return harness.render("LoginPage", props);
  }

  const t1 = await passwordFlow(async () => {
    throw new ApiFailure("no", 401, "invalid_credentials");
  });
  assert.equal(alertText(t1), "用户名或密码不正确。");

  const t2 = await passwordFlow(async () => {
    throw new ApiFailure("boom", 500, "internal");
  });
  assert.equal(alertText(t2), "连不上服务器，确认后端已启动后重试。");

  const t3 = await passwordFlow(
    async () => {
      throw new Error("whatever");
    },
    { messages: { generic: () => "自定义错误文案" } },
  );
  assert.equal(alertText(t3), "自定义错误文案");
});

test("sso.available 渲染 secondary「使用生态账号登录」钮并触发 start；缺省/不可用不渲染", () => {
  const starts: number[] = [];
  const harness = pageHarness({});
  const withSso = harness.render("LoginPage", {
    ...baseProps,
    sso: { available: true, start: () => void starts.push(1) },
  } as LoginPageProps);
  // harness 不深展开子组件：Button 以组件元素（variant/children props）形态出现
  const ssoBtn = elements(withSso).find(
    (el) => el.props?.variant === "secondary" && el.props?.children === "使用生态账号登录",
  );
  assert.ok(ssoBtn, "sso.available 时表单下方应有 SSO 按钮");
  assert.equal(ssoBtn?.props.type, "button", "SSO 按钮非 submit");
  assert.equal(typeof ssoBtn?.props.onClick, "function", "SSO 按钮带 onClick");
  ssoBtn?.props.onClick();
  assert.equal(starts.length, 1, "点击触发 sso.start");

  const without = harness.render("LoginPage", baseProps as LoginPageProps);
  assert.ok(
    !elements(without).some((el) => el.props?.children === "使用生态账号登录"),
    "缺省（无 sso prop）不渲染 SSO 按钮",
  );

  const off = harness.render("LoginPage", {
    ...baseProps,
    sso: { available: false, start: () => {} },
  } as LoginPageProps);
  assert.ok(
    !elements(off).some((el) => el.props?.children === "使用生态账号登录"),
    "available=false 不渲染 SSO 按钮",
  );
});

test("links：忘记密码/注册入口渲染并 navigate；封闭缺省随开放退位", () => {
  const goneTo: string[] = [];
  const harness = pageHarness({});
  const props = {
    ...baseProps,
    layout: "split",
    links: { forgotPassword: "/forgot-password", register: "/register" },
    navigate: (p: string) => goneTo.push(p),
  } as LoginPageProps;

  // 缺省 otp 态：注册入口在；忘记密码链是密码 tab 页脚专属
  const t0 = harness.render("LoginPage", props);
  const els0 = elements(t0);
  const registerBtn = els0.find((el) => el.props?.children === "立即注册");
  assert.ok(registerBtn, "links.register 时应渲染注册入口");
  registerBtn?.props.onClick();
  assert.deepEqual(goneTo, ["/register"], "注册入口点击应 navigate");
  assert.ok(
    !elements(t0).some((el) => el.props?.children === "忘记密码？"),
    "otp 态无忘记密码链（密码 tab 页脚专属）",
  );
  assert.ok(
    !JSON.stringify(t0).includes("账号由主管理员开通"),
    "开放注册时封闭 tagline 缺省退位",
  );

  // 分段文案钉：otp=邮箱、password=账号密码
  const seg = els0.find((el) => el.props?.ariaLabel === "登录方式");
  assert.ok(seg, "双开应有分段");
  assert.equal(seg?.props.items?.[0]?.label, "邮箱", "otp 分段文案=邮箱");
  assert.equal(seg?.props.items?.[1]?.label, "账号密码", "password 分段文案=账号密码");

  // 密码 tab：忘记密码链出现，旧静态提示退位，切回链用新文案
  seg?.props.onChange("password");
  const t1 = harness.render("LoginPage", props);
  const els1 = elements(t1);
  const forgot = els1.find((el) => el.props?.children === "忘记密码？");
  assert.ok(forgot, "密码 tab 页脚应有忘记密码链");
  forgot?.props.onClick();
  assert.deepEqual(goneTo, ["/register", "/forgot-password"], "忘记密码链点击应 navigate");
  assert.ok(
    !JSON.stringify(t1).includes("只有用户名时找主管理员重置"),
    "开放忘记密码后旧静态提示退位",
  );
  assert.ok(
    els1.some((el) => el.props?.children === "邮箱验证码登录"),
    "密码 tab 切回链文案=邮箱验证码登录",
  );
});

test("links 缺省：旧版静态提示与封闭 tagline 维持，无注册行", () => {
  const harness = pageHarness({});
  const props = baseProps as LoginPageProps;
  const t0 = harness.render("LoginPage", props);
  assert.ok(!elements(t0).some((el) => el.props?.children === "立即注册"), "无 links 不渲染注册行");
  assert.ok(JSON.stringify(t0).includes("账号由主管理员开通"), "无 links 时 tagline 维持封闭缺省");

  const seg = elements(t0).find((el) => el.props?.ariaLabel === "登录方式");
  assert.ok(seg, "缺省双开应有分段");
  seg?.props.onChange("password");
  const t1 = harness.render("LoginPage", props);
  assert.ok(
    JSON.stringify(t1).includes("只有用户名时找主管理员重置"),
    "无 links 时密码 tab 维持静态提示",
  );
  assert.ok(
    !elements(t1).some((el) => el.props?.children === "忘记密码？"),
    "无 links.forgotPassword 不渲染忘记密码链",
  );
});

test("linkNavigate：站内链接走 linkNavigate，登录落点仍走 navigate；缺省回退 navigate", () => {
  const linked: string[] = [];
  const landed: string[] = [];
  const harness = pageHarness({});
  const props = {
    ...baseProps,
    layout: "split",
    links: { forgotPassword: "/forgot-password", register: "/register" },
    linkNavigate: (p: string) => linked.push(p),
    navigate: (p: string) => landed.push(p),
  } as LoginPageProps;

  // otp 态：注册入口走 linkNavigate，navigate 不被链接借用
  const t0 = harness.render("LoginPage", props);
  const registerBtn = elements(t0).find((el) => el.props?.children === "立即注册");
  assert.ok(registerBtn, "links.register 时应渲染注册入口");
  registerBtn?.props.onClick();
  assert.deepEqual(linked, ["/register"], "注册入口点击应走 linkNavigate");
  assert.deepEqual(landed, [], "链接不应借用 navigate（登录落点闸语义）");

  // 密码 tab：忘记密码链同样走 linkNavigate
  const seg = elements(t0).find((el) => el.props?.ariaLabel === "登录方式");
  seg?.props.onChange("password");
  const t1 = harness.render("LoginPage", props);
  const forgot = elements(t1).find((el) => el.props?.children === "忘记密码？");
  assert.ok(forgot, "密码 tab 页脚应有忘记密码链");
  forgot?.props.onClick();
  assert.deepEqual(linked, ["/register", "/forgot-password"], "忘记密码链点击应走 linkNavigate");
  assert.deepEqual(landed, [], "navigate 全程未被链接调用");

  // 缺省（不传 linkNavigate）回退 navigate——既有行为回归保护
  const fallback: string[] = [];
  const t2 = harness.render("LoginPage", {
    ...baseProps,
    layout: "split",
    links: { register: "/register" },
    navigate: (p: string) => fallback.push(p),
  } as LoginPageProps);
  const registerBtn2 = elements(t2).find((el) => el.props?.children === "立即注册");
  registerBtn2?.props.onClick();
  assert.deepEqual(fallback, ["/register"], "缺省 linkNavigate 时链接回退 navigate");
});

test("media.kind=image 只渲染静帧 src；reduced-motion 同走 img；缺省行为不变", () => {
  const harness = pageHarness({});
  const image = { src: "/media/login.jpg", poster: "/media/login-poster.jpg", kind: "image" as const };
  const props = {
    ...baseProps,
    layout: "split",
    brand: { name: "X", media: image },
  } as LoginPageProps;

  const t0 = harness.render("LoginPage", props);
  assert.ok(!elements(t0).some((el) => el.type === "video"), "kind=image 不渲染 video");
  const img = elements(t0).find((el) => el.type === "img");
  assert.ok(img, "kind=image 渲染静帧 img");
  assert.equal(img?.props.src, image.src, "静帧取 media.src，poster 不用");

  const rm = pageHarness({
    __globals: { window: { matchMedia: () => ({ matches: true }) } },
  });
  const t1 = rm.render("LoginPage", props);
  const rmImg = elements(t1).find((el) => el.type === "img");
  assert.ok(rmImg, "reduced-motion 下仍退为 img");
  assert.equal(rmImg?.props.src, image.src, "reduced-motion 下静帧同样取 media.src");

  // 缺省（不传 kind）= video 行为不变
  const video = { src: "/media/login.mp4", poster: "/media/login-poster.jpg" };
  const t2 = harness.render("LoginPage", {
    ...baseProps,
    layout: "split",
    brand: { name: "X", media: video },
  } as LoginPageProps);
  const v = elements(t2).find((el) => el.type === "video");
  assert.ok(v, "缺省 kind 仍渲染 video");
  assert.equal(v?.props.src, video.src);
  assert.equal(v?.props.poster, video.poster);
});

test("brand.accent 注入根元素 --lpg-accent/-hover/-weak/-fg-on-accent", () => {
  const harness = pageHarness({});
  const accent = { base: "#ff8800", hover: "#ffaa33", weak: "rgba(255,136,0,0.14)", onAccent: "#331a00" };
  const tree = harness.render("LoginPage", {
    ...baseProps,
    layout: "split",
    brand: { name: "X", accent },
  } as LoginPageProps);
  const style = tree.props.style;
  assert.equal(style["--lpg-accent"], "#ff8800");
  assert.equal(style["--lpg-accent-hover"], "#ffaa33");
  assert.equal(style["--lpg-accent-weak"], "rgba(255,136,0,0.14)");
  assert.equal(style["--lpg-fg-on-accent"], "#331a00");

  const plain = harness.render("LoginPage", { ...baseProps, layout: "compact" } as LoginPageProps);
  assert.equal(plain.props.style, undefined, "无 accent 不注入 inline 变量");
});

test("identifierTransform：密码登录内建路径提交前变换标识（fetch body email 为合成邮箱）", async () => {
  // 走真实内建客户端（不 mock builtInAuth）：经补丁 globalThis.fetch 观测 signIn 请求体
  const calls: { url: string; init?: RequestInit }[] = [];
  const originalFetch = globalThis.fetch;
  globalThis.fetch = (async (url: string, init?: RequestInit) => {
    calls.push({ url, init });
    return new Response("{}", { status: 200 });
  }) as typeof fetch;
  try {
    const harness = pageHarness({});
    const props: LoginPageProps = {
      ...baseProps,
      layout: "split",
      identifierTransform: (v) => v.toLowerCase() + "@users.dhuman.modelxing.com",
    };
    const t0 = harness.render("LoginPage", props);
    const seg = elements(t0).find((el) => el.props?.ariaLabel === "登录方式");
    assert.ok(seg, "缺省双开应有分段");
    seg.props.onChange("password");
    const t1 = harness.render("LoginPage", props);
    const fields = elements(t1).filter((el) => el.props?.label);
    const usernameField = fields.find((el) => el.props.label === "用户名");
    const passwordField = fields.find((el) => el.props.type === "password");
    assert.ok(usernameField && passwordField, "密码态应有用户名与密码输入");
    usernameField.props.onChange("Op1");
    passwordField.props.onChange("pw");
    const t2 = harness.render("LoginPage", props);
    const form = elements(t2).find((el) => el.type === "form");
    assert.ok(form);
    await form.props.onSubmit({ preventDefault() {} });
    await flush();
    assert.equal(calls.length, 1, "内建密码路径应恰好发一次请求");
    assert.equal(calls[0].url, "/api/auth/password");
    assert.equal(
      String(calls[0].init?.body),
      JSON.stringify({ email: "op1@users.dhuman.modelxing.com", password: "pw" }),
      "fetch body 的 email 应为 identifierTransform 变换后的值",
    );
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test("集成：auth 覆写口接真实内建客户端，经注入 fetch 发起发码请求", async () => {
  const calls: { url: string; init?: RequestInit }[] = [];
  const fetchMock = (async (url: string, init?: RequestInit) => {
    calls.push({ url, init });
    return new Response("{}", { status: 200 });
  }) as typeof fetch;
  // 真实 createBuiltInAuth（vm 的 __globals.fetch 够不着真实 require 语境，故经 auth prop 注入）
  const props = { ...baseProps, layout: "split", auth: createBuiltInAuth(fetchMock) } as LoginPageProps;
  const harness = pageHarness({});
  const t0 = harness.render("LoginPage", props);
  elements(t0)
    .find((el) => el.props?.type === "email")!
    .props.onChange("a@b.c");
  const t1 = harness.render("LoginPage", props);
  const form = elements(t1).find((el) => el.type === "form")!;
  await form.props.onSubmit({ preventDefault() {} });
  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, "/api/auth/email/code/start");
  assert.equal(calls[0].init?.method, "POST");
  assert.equal(String(calls[0].init?.body), JSON.stringify({ email: "a@b.c" }));
  const t2 = harness.render("LoginPage", props);
  assert.ok(elements(t2).some((el) => el.props?.inputMode === "numeric"), "发码成功进入 verify 态");
});

test("tagline 空串显式关闭副标：常态不渲染空 <p>，输码屏发码提示不受影响", () => {
  const harness = pageHarness({});
  const props = {
    ...baseProps,
    layout: "split",
    brand: { name: "数海观澜AIGC", tagline: "" },
  } as LoginPageProps;

  const t0 = harness.render("LoginPage", props);
  const sub = elements(t0).find((el) => el.props?.className?.includes?.("sub"));
  assert.ok(!sub, "tagline 空串时常态不渲染副标");
  assert.ok(
    JSON.stringify(t0).includes("账号由主管理员开通") === false,
    "空串是显式关闭，不落回缺省封闭口径",
  );
});
