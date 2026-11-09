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

describe("门店真实制作面板:发起/能力如实申报/幂等", () => {
  const panel = read(panelRel);

  it("挂在 /work/materials「帮我制作」区,按门店接线", () => {
    const materials = read(materialsRel);
    expect(materials).toContain("StoreProductionPanel");
    expect(materials).toMatch(/storeID \? \(?\s*<StoreProductionPanel storeId=\{storeID\}/);
  });

  it("能力如实申报:生产方未注册=发起按钮禁用+原因,不伪进度", () => {
    expect(panel).toMatch(/content-production-handoffs\/capabilities/);
    expect(panel).toMatch(/production_available/);
    expect(panel).toMatch(/disabled=\{busy \|\| !caps\?\.production_available\}/);
    expect(panel).toMatch(/data-field="produce-reason"/);
  });

  it("发起走门店公共端点,content_pack_ref 用 pack:<id> 形,quote_ref 必填", () => {
    expect(panel).toMatch(/stores\/\$\{storeId\}\/content-production-handoffs/);
    expect(panel).toContain("content_pack_ref: `pack:${packID}`");
    expect(panel).toContain("quote_ref");
    // 商家不填任何技术字段:pack 用选择器,quote_ref 是唯一语义输入。
    expect(panel).toMatch(/aria-label="选择内容包"/);
    expect(panel).toMatch(/aria-label="已确认报价引用"/);
  });

  it("幂等:自动生成凭据并在成功后换新;重放如实说明不重复建工程/计费", () => {
    expect(panel).toMatch(/idempotency_key: idempotencyKey/);
    expect(panel).toMatch(/setIdempotencyKey\(randomKey\(\)\)/);
    expect(panel).toContain("不重复建工程、不重复报价");
    expect(panel).toMatch(/重试走同一凭据/);
  });

  it("503 诚实降级如实呈现(发起已记下但没有送去做),不伪装成功", () => {
    expect(panel).toMatch(/res\.status === 503/);
    expect(panel).toContain("这次发起已记下但没有送去做");
    expect(panel).toMatch(/noteKind === "ok" \? "tk-note tk-ok" : "tk-note tk-unknown"/);
  });

  it("状态独立可辨:未提交/处理中/已产出/失败/已取消;task_ref 缺失如实说", () => {
    expect(panel).toContain('"recorded"');
    expect(panel).toContain('"submitted"');
    expect(panel).toContain('"produced"');
    expect(panel).toContain('"failed"');
    expect(panel).toContain('"canceled"');
    expect(panel).toContain("尚无平台任务(未接通不伪造)");
  });

  it("回流候选指向抽屉预览与显式采用,本区不自动采用/外发", () => {
    expect(panel).toContain("已登记候选(到上方门店内容包预览、采用)");
    expect(panel).not.toMatch(/autoAdopt|自动采用/);
  });

  it("账单权威未接入时如实标注报价按申报记录", () => {
    expect(panel).toContain("quote_authority_configured");
    expect(panel).toContain("费用状态以账单为准");
    expect(panel).toContain("报价已核验");
  });
});

describe("门店内容抽屉:回流候选预览与来源", () => {
  const drawer = read(drawerRel);

  it("候选(含制作回流)采用前可预览真实成片(media-access preview,零计费)", () => {
    expect(drawer).toMatch(/data-action="preview-candidate"/);
    expect(drawer).toMatch(/public\/content\/\$\{version\.id\}\/media-access/);
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
