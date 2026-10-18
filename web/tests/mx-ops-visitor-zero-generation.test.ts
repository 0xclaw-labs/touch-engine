import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { visitorContentGuard } from "@/lib/work/production";

const webRoot = process.cwd();
const serverRoot = path.resolve(webRoot, "..", "server");

function readWeb(rel: string): string {
  return readFileSync(path.join(webRoot, "src", rel), "utf8");
}

function readServer(rel: string): string {
  return readFileSync(path.join(serverRoot, rel), "utf8");
}

describe("顾客/访客路径零新生成（HUI-3553 硬约束）", () => {
  it("纯函数守卫恒为 false，只读已采用内容", () => {
    const guard = visitorContentGuard();
    expect(guard.generationAllowed).toBe(false);
    expect(guard.reason).toContain("已采用");
    expect(guard.reason).toContain("不会新开 GPU");
  });

  it("顾客页不引用制作交接/队列投影/生成入口", () => {
    const customerFiles = [
      "app/c/[code]/page.tsx",
      "app/t/[code]/page.tsx",
      "lib/customer-journey.ts",
      "lib/customer-publish.ts",
      "lib/visitor-experience.ts",
    ];
    for (const file of customerFiles) {
      const source = readWeb(file);
      for (const forbidden of ["content-handoffs", "queue-view", "showcase-videos", "text-images", "job_queue_view"]) {
        expect(source, `${file} must not reference ${forbidden}`).not.toContain(forbidden);
      }
    }
  });

  it("顾客公共快照只读已采用版本，不经制作队列", () => {
    const handler = readServer("internal/httpapi/handlers_content_packs.go");
    expect(handler).toContain("GetAdoptedVersion");
    // 公共快照读取路径中不泄漏内部 worker 计数（internal_debug 只允许出现在
    // 队列投影接缝的注释里，且同步时已被删除）。
    expect(handler).not.toContain("ready_workers");
    expect(handler).not.toContain("busy_workers");
  });

  it("顾客发布域不直接生成内容（custpublish 无模型/生成调用）", () => {
    const custpublish = readServer("internal/custpublish/custpublish.go");
    for (const forbidden of ["content_handoffs", "job_queue_view", "provider", "AutoDL", "runtime_token"]) {
      expect(custpublish, `custpublish must not reference ${forbidden}`).not.toContain(forbidden);
    }
  });
});
