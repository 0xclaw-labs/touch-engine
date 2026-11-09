import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const root = path.resolve(process.cwd(), "src");

function read(rel: string): string {
  return readFileSync(path.join(root, rel), "utf8");
}

const drawerRel = "components/work/store-content-drawer.tsx";
const materialsRel = "app/work/materials/page.tsx";

describe("门店内容包抽屉：真实上传、候选、采用、换版与受控媒体", () => {
  const drawer = read(drawerRel);

  it("真实上传走 multipart,商家不填 asset_id/版本号等技术字段", () => {
    expect(drawer).toContain('type="file"');
    expect(drawer).toMatch(/FormData\(\)/);
    expect(drawer).toMatch(/content-media/);
    // 技术字段只由选择器内部携带,商家没有输入框。
    expect(drawer).not.toMatch(/name="asset_id"/);
    expect(drawer).not.toMatch(/name="version"/);
    expect(drawer).toMatch(/value=\{m\.id\}/);
  });

  it("上传完成以服务器实测元数据为准(MIME/大小/时长/内容指纹),不伪装成功", () => {
    expect(drawer).toMatch(/data-field="upload-result"/);
    expect(drawer).toMatch(/mime_type/);
    expect(drawer).toMatch(/byte_size/);
    expect(drawer).toMatch(/sha256/);
    expect(drawer).toMatch(/按真实内容核验/);
    // 失败如实说:没上传成功不说上传了。
    expect(drawer).toMatch(/文件还没有上传/);
  });

  it("读取门店包/版本/采用指针(内容包脱离活动,门店根)", () => {
    expect(drawer).toMatch(/stores\/\$\{storeId\}\/content-packs/);
    expect(drawer).toContain("versions");
    expect(drawer).toContain("adoptions");
    expect(drawer).toMatch(/未挂活动|门店根/);
  });

  it("采用必须显式;换版 = 同包新候选再采用,旧版保留可追溯", () => {
    expect(drawer).toMatch(/content-packs\/\$\{packId\}\/versions\/\$\{versionId\}\/adopt/);
    expect(drawer).toMatch(/data-action="adopt"/);
    expect(drawer).toContain("采用这一版");
    expect(drawer).toContain("换版:同一包新开一版");
    expect(drawer).toMatch(/data-field="adopted-note"/);
  });

  it("blocked 显示商家语言原因,不显示机器码", () => {
    expect(drawer).toMatch(/data-field="blocked-reason"/);
    expect(drawer).toContain("已停止分发");
  });

  it("预览/下载只取已采用版本,走 media-access 短 TTL 受控 URL", () => {
    expect(drawer).toMatch(/public\/content\/\$\{version\.id\}\/media-access/);
    expect(drawer).toMatch(/purpose/);
    expect(drawer).toMatch(/"preview"/);
    expect(drawer).toMatch(/"download"/);
    // 预览/下载按钮只在已采用版本出现。
    expect(drawer).toMatch(/isAdopted \?[\s\S]*data-action="preview"/);
  });

  it("下载按真实结果表述:已开始下载,不以「已保存」冒充结果", () => {
    expect(drawer).toContain("已开始下载");
    expect(drawer).toMatch(/以浏览器实际结果为准/);
    expect(drawer).not.toContain("已保存相册");
  });

  it("到期续签诚实:重试不触发制作/扣费", () => {
    expect(drawer).toMatch(/不产生任何费用|不会触发任何制作/);
  });

  it("弱网失败给重试,不把失败当成功", () => {
    expect(drawer).toMatch(/data-action="retry"/);
    expect(drawer).toMatch(/data-state="error"/);
  });

  it("390/430 与桌面可操作:无固定像素宽,块级堆叠", () => {
    expect(drawer).toMatch(/className="tk-section|className="tk-list|className="tk-row/);
    expect(drawer).not.toMatch(/width:\s*[34]\d\dpx/);
    expect(drawer).not.toMatch(/style=\{\{[^}]*width:\s*\d/);
  });

  it("浏览/换稿/下载零生成零收费:请求只落内容包与受控媒体白名单", () => {
    expect(drawer).not.toMatch(/billing/i);
    expect(drawer).not.toMatch(/copy-drafts/);
    expect(drawer).not.toMatch(/content-handoffs/);
    // 抽屉发起的请求就是这四类(内容包列取/媒体列取/版本与采用/受控媒体)。
    expect(drawer).toMatch(/stores\/\$\{storeId\}\/content-packs/);
    expect(drawer).toMatch(/stores\/\$\{storeId\}\/content-media/);
    expect(drawer).toMatch(/content-packs\/\$\{packId\}\/versions/);
    expect(drawer).toMatch(/public\/content\/\$\{version\.id\}\/media-access/);
  });

  it("同控件可连续多次选择文件(每次上传互不影响,可重选同一文件)", () => {
    // Walk A F 场景:第一件上传后控件必须复位且不影响第二件。
    expect(drawer).toMatch(/event\.target\.value = ""/);
    expect(drawer).toMatch(/disabled=\{uploading\}/);
    // 每件文件独立出结果,前一件成功不掩盖后一件失败(反之亦然)。
    expect(drawer).toMatch(/data-field="upload-result"/);
    expect(drawer).not.toMatch(/uploadCount|alreadyUploaded/);
  });
});

describe("素材页挂载门店内容包抽屉", () => {
  const materials = read(materialsRel);

  it("门店选择器 + 抽屉一处挂载,不新建路由", () => {
    expect(materials).toContain('from "@/components/work/store-content-drawer"');
    const mounts = materials.match(/<StoreContentDrawer/g) ?? [];
    expect(mounts.length).toBe(1);
    expect(materials).toMatch(/<StoreContentDrawer storeId=\{storeID\} \/>/);
    expect(materials).toMatch(/aria-label="选择门店"/);
  });

  it("门店内容包不需要先建活动(文案明示)", () => {
    expect(materials).toContain("门店内容包(不需要先建活动)");
  });

  it("无门店时如实提示,不渲染空抽屉", () => {
    expect(materials).toMatch(/还没有门店/);
  });

  it("既有三入口(上传/素材库/帮我制作)保持不回退", () => {
    expect(materials).toMatch(/entry="upload"/);
    expect(materials).toMatch(/entry="library"/);
    expect(materials).toMatch(/data-entry="produce"/);
  });
});
