import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

// HUI-1896 C2 内容预览与交接(/s/{storeCode}/content/{actionId}):源码级断言。
// 红线:预览真实媒体(media-access 受控凭据 + video);「换一个」只在已采用
// 候选池且零收费;已交接版本不被商家新版本/刷新悄悄替换(冻结语义);
// 返回恢复同版本同会话;跨浏览器凭据走 POST 体;交接 1670 契约 unknown 不绿态;
// 无 EcoTopNav/注册/留资/下载强制;素材缺失诚实待办态(不死按钮)。

const root = path.dirname(fileURLToPath(import.meta.url));
const src = (p: string) => readFileSync(path.join(root, "..", "src", p), "utf8");

function codeOf(source: string): string {
  return source
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .split("\n")
    .map((line) => line.replace(/(^|\s)\/\/.*$/, ""))
    .join("\n");
}

const view = codeOf(src("app/s/[storeCode]/content/[actionId]/content-view.tsx"));

describe("C2 数据面:3553 内容 + 3546 会话", () => {
  it("内容读面走 store-session(startOrResumeSession/sessionStatus/mintMediaAccess/switchSessionContent)", () => {
    expect(view.includes("startOrResumeSession")).toBe(true);
    expect(view.includes("sessionStatus")).toBe(true);
    expect(view.includes("mintMediaAccess")).toBe(true);
    expect(view.includes("switchSessionContent")).toBe(true);
  });

  it("进门恢复:会话的 selected_content_pack_version 驱动回显(返回恢复同版本)", () => {
    expect(view.includes("selected_content_pack_version")).toBe(true);
    expect(view.includes("resume_available")).toBe(true);
  });

  it("跨浏览器恢复码兑换走 exchangeResumeCredential(POST 体)", () => {
    expect(view.includes("exchangeResumeCredential")).toBe(true);
    expect(view.includes("localStorage")).toBe(false);
    expect(view.includes("sessionStorage")).toBe(false);
  });
});

describe("C2 预览:真实媒体与素材缺失待办态", () => {
  it("视频真实播放(<video> 受控凭据 url),purpose=preview", () => {
    expect(view.includes("<video")).toBe(true);
    expect(view.includes('"preview"')).toBe(true);
  });

  it("保存走 purpose=download(不给死链)", () => {
    expect(view.includes('"download"')).toBe(true);
  });

  it("availability=missing 的媒体不给播放入口只给待办文案", () => {
    expect(view.includes("missing")).toBe(true);
    expect(view.includes("素材还没有准备好")).toBe(true);
  });

  it("内容 404/409 诚实待办态:未配置/不可分发不伪装成功", () => {
    expect(view.includes("not_found")).toBe(true);
    expect(view.includes("undistributable")).toBe(true);
  });

  it("AI/广告标注如实转述(marking)", () => {
    expect(view.includes("ai_generated")).toBe(true);
    expect(view.includes("ad_required")).toBe(true);
  });
});

describe("C2 换选:已采用候选池 + 冻结语义", () => {
  it("换选按钮来自 candidates(3553 已采用池),不是随机生成", () => {
    expect(view.includes("candidates")).toBe(true);
    expect(view.includes("换一个")).toBe(true);
  });

  it("冻结(已交接)给「版本保持不变」文案,不悄悄替换", () => {
    expect(view.includes("frozen")).toBe(true);
    expect(view.includes("保持不变")).toBe(true);
  });

  it("换选零收费:无生成/收费请求(copy-jobs 不出现)", () => {
    expect(view.includes("copy-jobs")).toBe(false);
    expect(view.includes("生成")).toBe(false);
  });
});

describe("C2 交接:1670 契约诚实呈现", () => {
  it("plan 四态走 planCopy/parseHandoffPlan/statusFace 唯一出口", () => {
    expect(view.includes("parseHandoffPlan")).toBe(true);
    expect(view.includes("planCopy")).toBe(true);
    expect(view.includes("statusFace")).toBe(true);
  });

  it("交接端点读不到(基线无 1670)给「尚未开通」诚实态,不伪报成功", () => {
    expect(view.includes("尚未开通")).toBe(true);
  });

  it("unsupported_fields 与 evidenceFace 如实展示", () => {
    expect(view.includes("unsupportedFields")).toBe(true);
    expect(view.includes("evidenceFace")).toBe(true);
  });

  it("发布主体=顾客自己账号;每一步可取消的说明在场", () => {
    expect(view.includes("自己的账号")).toBe(true);
    expect(view.includes("取消")).toBe(true);
  });
});

describe("C2 布局与环境差异", () => {
  it("环境路线走 detectEnvironment/pickRoute(3535 能力渲染)", () => {
    expect(view.includes("detectEnvironment")).toBe(true);
    expect(view.includes("pickRoute")).toBe(true);
    expect(view.includes("methodLabel")).toBe(true);
  });

  it("无生态导航/无强制注册留资;返回门店入口在场", () => {
    expect(view.includes("EcoTopNav")).toBe(false);
    expect(view.includes("注册")).toBe(false);
    expect(view.includes("必填")).toBe(false);
    expect(view.includes("返回门店")).toBe(true);
  });

  it("390/430/桌面列模式", () => {
    expect(view.includes("data-columns")).toBe(true);
    expect(view.includes("data-viewport")).toBe(true);
  });
});
