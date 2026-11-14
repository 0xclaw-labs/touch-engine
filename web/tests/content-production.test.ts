import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const root = path.resolve(process.cwd(), "src");

function read(rel: string): string {
  return readFileSync(path.join(root, rel), "utf8");
}

const panelRel = "components/work/store-production-panel.tsx";
const drawerRel = "components/work/store-content-drawer.tsx";
const materialsRel = "app/work/materials/page.tsx";

describe("门店真实制作面板：权威报价、确认与恢复", () => {
  const panel = read(panelRel);
  it("素材页通过统一内容编辑器按门店挂载制作", () => {
    expect(read(materialsRel)).toContain("StoreContentDrawer");
    expect(read(drawerRel)).toContain("<StoreProductionPanel storeId={storeId}");
  });
  it("能力未读到或生产方不可用时关闭制作并显示原因", () => {
    expect(panel).toContain("content-production-handoffs/capabilities");
    expect(panel).toContain('data-field="produce-reason"');
    expect(panel).toMatch(/disabled=\{busy \|\| !caps\?\.production_available\}/);
  });
  it("正常路径请求权威报价，商家不抄quote_ref或包号", () => {
    expect(panel).toContain("content-production-handoffs/quote");
    expect(panel).toContain("ensureContentPack");
    expect(panel).not.toMatch(/name="quote_ref"|aria-label="已确认报价引用"/);
    expect(panel).toContain("quote.deliverables");
    expect(panel).toContain("quote.payer");
    expect(panel).toContain("quote.expires_at");
  });
  it("费用知情确认后保存完整请求，刷新/响应丢失恢复同一负载", () => {
    expect(panel).toContain("确认费用并提交制作");
    expect(panel).toContain("setPending(payload)");
    expect(panel.indexOf("setPending(payload)")).toBeLessThan(panel.indexOf('session.api("POST", `stores/${storeId}/content-production-handoffs`, payload)'));
    expect(panel).toContain("恢复同一请求");
    expect(panel).toContain("localStorage.getItem(storageKey)");
    expect(panel).toContain("session.tenantId");
  });
  it("有权威handoff才确认登记，网络不确定时保留恢复请求", () => {
    expect(panel).toContain("if (!h)");
    expect(panel).toContain("制作结果尚未确认");
    expect(panel).toContain("setPending(null)");
  });
  it("真实状态、回流候选与费用结果独立展示，诊断默认收起", () => {
    for (const status of ["recorded", "submitted", "produced", "failed", "canceled"]) expect(panel).toContain(status);
    expect(panel).toContain("候选已回流，请在下方预览并采用");
    expect(panel).toContain("费用释放或退款以公共账务事实为准");
    expect(panel).toContain("<details><summary>受控诊断</summary>");
  });
});

describe("门店内容抽屉:回流候选预览与来源", () => {
  const drawer = read(drawerRel);

  it("候选(含制作回流)采用前可预览真实成片(media-access preview,零计费)", () => {
    expect(drawer).toMatch(/data-action="preview-candidate"/);
    expect(drawer).toMatch(/stores\/\$\{storeId\}\/content-packs\/\$\{version\.pack_id\}\/versions\/\$\{version\.id\}\/media-access/);
    expect(drawer).toMatch(/回流候选采用前可预览真实成片/);
    expect(drawer).toContain("纯预览零计费");
  });

  it("回流来源如实展示「制作回流」,不冒充商家上传", () => {
    expect(drawer).toMatch(/product_photo_return/);
    expect(drawer).toContain("制作回流");
    expect(drawer).toContain("sourceCopy(version.source)");
  });

  it("采用仍须显式:候选的采用按钮保持显式动作", () => {
    expect(drawer).toMatch(/data-action="adopt"/);
    expect(drawer).toContain("采用这一版");
  });
});
