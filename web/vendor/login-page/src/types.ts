/** 会话数据。形状归接入仓解释（goboost: AuthLoginResult）；组件只透传。 */
export type AuthResult = Record<string, unknown>;

/** 认证适配器。缺省实现见 builtInAuth.ts；覆写口与 goboost-fd lib/auth.ts 函数签名对齐。 */
export type AuthAdapter = {
  startEmailCode(email: string): Promise<void>;
  verifyEmailCode(email: string, code: string): Promise<AuthResult>;
  signIn(identifier: string, password: string): Promise<AuthResult>;
};

export type AccentConfig = { base: string; hover: string; weak: string; onAccent: string };

export type BrandConfig = {
  name: string;
  /** 缺省「账号由主管理员开通，不能自助注册。」；开放注册产品必须覆写（spec §7）。 */
  tagline?: string;
  /** 覆写时四值必须齐全；缺省 mint（#6cebdc 系）。 */
  accent?: AccentConfig;
  /** 右栏媒体（split 版式）。null/缺省 = 右栏以底色+字标垫。
   * kind="video"（缺省）播放 src、poster 起播前垫帧；kind="image" 只渲染静帧
   * src（poster 不用）——高闪烁素材退静帧用，reduced-motion 降级同走 img。 */
  media?: { src: string; poster: string; kind?: "video" | "image" } | null;
};

export type ModesConfig = { otp?: boolean; password?: boolean };
export type SsoConfig = { available: boolean; start: () => void };

/** 页脚链接组：组件只渲染文案并调 navigate，落点页归接入仓。 */
export type LinksConfig = {
  /** 密码 tab 页脚「忘记密码？」链接目标（如 /forgot-password）；缺省渲染旧版静态提示。 */
  forgotPassword?: string;
  /** 注册入口（如 /register）；缺省不渲染注册行且 tagline 走封闭缺省。 */
  register?: string;
};

/** 错误文案覆写口：缺省表与 goboost-fd lib/errors.ts 对齐。 */
export type LoginMessages = {
  /** 验证码错误（invalid_otp / 401）文案；缺省「验证码不正确或已过期」。 */
  otpInvalid?: string;
  /** 其余错误的整体覆写，回传原始错误；缺省走包内 loginErrorMessage 等价表。 */
  generic?: (e: unknown) => string;
};

export type LoginPageProps = {
  brand: BrandConfig;
  auth?: Partial<AuthAdapter>;
  /**
   * 密码登录标识变换：legacy 用户名 → identity 合成邮箱
   * （如 `<external_key 小写>@users.dhuman.modelxing.com`）。
   * 只作用于内建 auth 路径（覆写 auth 的消费方自带标识语义，不经过它）；
   * OTP 的 email 输入不经过它。
   */
  identifierTransform?: (identifier: string) => string;
  modes?: ModesConfig;
  /** 错误文案覆写（auth 之外的 props 可选项）。 */
  messages?: LoginMessages;
  /** 页脚链接组（忘记密码/注册入口）；缺省全不渲染，行为与旧版一致。 */
  links?: LinksConfig;
  sso?: SsoConfig;
  onAuthenticated: (result: AuthResult) => void;
  navigate: (path: string) => void;
  /** 站内链接跳转（links.register/forgotPassword 等落点），不走宿主登录落点闸；缺省用 navigate。 */
  linkNavigate?: (path: string) => void;
  /** 缺省实现：只接受站内以 / 开头且不以 // 开头的路径，否则回 "/"。 */
  nextResolver?: (search: string) => string;
  /** 嵌入场景（portal loginFold 等）直接给定落点，等价 embedded 语义。 */
  nextOverride?: string | null;
  layout?: "split" | "compact";
  /** 根元素附加类（宿主极少量定位用；禁止借此改版式）。 */
  className?: string;
};
