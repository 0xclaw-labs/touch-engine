"use client";

// 碰一碰登录面对齐生态统一组件（vendor/login-page = @painuo/login-page，
// .source-ref 记录同步源）。会话不走 identity cookie：auth.signIn 打回自家
// BFF /api/auth/login，Go 后端验 identity 后落 touch_session——BFF/Go 零改动。
// 错误面统一收口 failure-copy（HUI-2628：状态码/机器码不出用户界面）。

import { LoginPage, ApiFailure } from "@painuo/login-page";

import { loginFailureText } from "@/lib/failure-copy";

import styles from "./shared-login.module.css";

/** 与 vendor AuthAdapter["signIn"] 同形；抛 ApiFailure 进组件错误面。 */
export type SignInBridge = (
  identifier: string,
  password: string,
) => Promise<Record<string, unknown>>;

/** 统一提交桥：POST /api/auth/login（BFF catch-all → Go /api/v1/auth/login）。 */
export function createBffSignIn(): SignInBridge {
  return async (identifier, password) => {
    const res = await fetch("/api/auth/login", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ email: identifier.trim().toLowerCase(), password }),
    });
    const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
    if (!res.ok) {
      throw new ApiFailure(loginFailureText(res.status, data), res.status, "login_rejected");
    }
    return data;
  };
}

/**
 * 亮色薄包装。modes 固定密码单式（后端只有 password 登录，没有验证码端点）；
 * brand.tagline 不覆写——包缺省「账号由主管理员开通，不能自助注册。」正是
 * 碰一碰的真实口径。
 */
export function SharedLogin({
  signIn,
  onAuthenticated,
}: {
  signIn: SignInBridge;
  onAuthenticated?: (result: Record<string, unknown>) => void;
}) {
  return (
    <div className={styles.light}>
      <LoginPage
        layout="compact"
        modes={{ otp: false, password: true }}
        brand={{ name: "碰一碰", media: null }}
        auth={{ signIn }}
        onAuthenticated={(result) => onAuthenticated?.(result)}
        navigate={() => {}}
        messages={{
          generic: (e: unknown) =>
            e instanceof ApiFailure
              ? loginFailureText(e.status, { error: e.code })
              : loginFailureText(0, {}),
        }}
      />
    </div>
  );
}
