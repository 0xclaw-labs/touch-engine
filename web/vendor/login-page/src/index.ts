/** @painuo/login-page 公共交付面。 */
export { default, LoginPage } from "./LoginPage.tsx";
export type {
  AccentConfig,
  AuthAdapter,
  AuthResult,
  BrandConfig,
  LoginMessages,
  LoginPageProps,
  ModesConfig,
  SsoConfig,
} from "./types.ts";
export { ApiFailure } from "./apiError.ts";
export { createBuiltInAuth } from "./builtInAuth.ts";
