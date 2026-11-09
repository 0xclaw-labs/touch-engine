import path from "node:path";
import { fileURLToPath } from "node:url";
import { defineConfig } from "vitest/config";

const root = path.dirname(fileURLToPath(import.meta.url));

export default defineConfig({
  resolve: {
    alias: {
      "@": path.resolve(root, "src"),
      // HUI-4593 窄补丁:@painuo/login-page 在 tsconfig paths 里映射到 vendor
      // 单缝,vitest 不读 tsconfig paths,这里补同一映射(此前加载
      // merchant-session 的测试因此无法启动,与本票写面无关)。
      "@painuo/login-page": path.resolve(root, "vendor/login-page/src/index.ts"),
    },
  },
  test: {
    environment: "node",
    include: ["tests/**/*.test.ts"],
  },
});
