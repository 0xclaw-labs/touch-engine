import path from "node:path";
import { fileURLToPath } from "node:url";
import { defineConfig } from "vitest/config";

const root = path.dirname(fileURLToPath(import.meta.url));

export default defineConfig({
  resolve: {
    alias: { "@": path.resolve(root, "src"), "@painuo/login-page": path.resolve(root, "vendor/login-page/src/index.ts") },
  },
  test: {
    environment: "node",
    include: ["tests/**/*.test.ts"],
  },
});
