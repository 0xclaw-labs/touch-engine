import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const root = path.resolve(process.cwd(), "src");

function read(rel: string): string {
  return readFileSync(path.join(root, rel), "utf8");
}

// HUI-4885 原语落地证据：外壳接线后，Dialog/Sheet/Tabs 原语真实引入并渲染；
// 业务守卫、请求模型、恢复路径沿 main 既有实现保留，本用例只锁定外壳接线。
describe("HUI-4885 shadcn 原语落地（Sheet / Dialog / Tabs）", () => {
  it("stores 新增/编辑走 Dialog 原语，不再是内联 Card 表单", () => {
    const src = read("app/work/stores/page.tsx");
    expect(src).toContain('from "@/components/ui/dialog"');
    expect(src).toContain("<DialogTrigger");
    expect(src).toContain("<DialogContent");
    expect(src).toContain('data-action="open-add-store"');
    expect(src).toContain('data-action="open-edit-store"');
    // 内联 Card 标题「登记门店」不再作为表单壳存在；表单迁入 Dialog。
    expect(src).not.toContain("登记门店</h2>");
  });

  it("touchpoints 二维码走 Sheet 原语（二维码图 + 短码 + 下载动作）", () => {
    const src = read("components/work/touchpoints/touchpoints-panel.tsx");
    expect(src).toContain('from "@/components/ui/sheet"');
    expect(src).toContain("<SheetTrigger");
    expect(src).toContain("<SheetContent");
    expect(src).toContain('data-action="view-qr"');
    expect(src).toContain('data-field="qr-image"');
    expect(src).toContain('data-action="download-qr"');
  });

  it("rewards 发放/批次与核销记录走 Tabs 分区", () => {
    const src = read("app/work/rewards/page.tsx");
    expect(src).toContain('from "@/components/ui/tabs"');
    expect(src).toContain('<TabsTrigger value="issue">发放与批次</TabsTrigger>');
    expect(src).toContain('<TabsTrigger value="records">权益记录（核销）</TabsTrigger>');
  });

  it("campaign 详情 配置 / 分享触达 走 Tabs 分区", () => {
    const src = read("app/work/campaigns/[id]/page.tsx");
    expect(src).toContain('from "@/components/ui/tabs"');
    expect(src).toContain('<TabsTrigger value="config">配置</TabsTrigger>');
    expect(src).toContain('<TabsTrigger value="share">分享与触达</TabsTrigger>');
  });
});
