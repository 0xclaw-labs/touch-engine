import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const webRoot = path.resolve(__dirname, "..");

function read(rel: string): string {
  return readFileSync(path.join(webRoot, rel), "utf8");
}

describe("HUI-2628 后台两张表与手填租户", () => {
  it("活动页不再直接写 table，表格只留在 AdminDataTable", () => {
    const admin = read("src/app/admin/page.tsx");
    const table = read("src/components/admin/AdminDataTable.tsx");
    expect(admin.includes("<table")).toBe(false);
    expect(table.match(/<table/g)).toHaveLength(1);
  });

  it("登录不再要求填写租户编号，页头不展示原始租户编号", () => {
    const admin = read("src/app/admin/page.tsx");
    const session = read("src/components/work/merchant-session.tsx");
    expect(admin).not.toContain("租户 ID");
    expect(admin).not.toContain("请填写租户");
    expect(admin).not.toContain("tenantName || tenantId");
    expect(session).not.toContain("租户编号");
    expect(session).not.toContain("login(email, password, tenantId)");
  });
});
