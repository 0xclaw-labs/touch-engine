import path from "node:path";
import { fileURLToPath } from "node:url";
import { defineConfig } from "vitest/config";

const root = path.dirname(fileURLToPath(import.meta.url));

export default defineConfig({
  resolve: {
    alias: {
      "@": path.resolve(root, "src"),
      // HUI-4584/HUI-4593 同款窄补丁（wave1 集成合一只留一份）：tsconfig paths
      // 对 vitest 不生效，vendor 登录页组件需显式别名，否则引用 merchant-session
      // 的既有测试（service-desk-entry 等）在 vitest 下无法装载。
      "@painuo/login-page": path.resolve(root, "vendor/login-page/src/index.ts"),
    },
  },
  test: {
    environment: "node",
    include: ["tests/**/*.test.ts"],
  },
});
