import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { adminHeaderTenantName } from "@/lib/admin-header-tenant";

const webRoot = path.resolve(__dirname, "..");
const admin = readFileSync(path.join(webRoot, "src/app/admin/page.tsx"), "utf8");

const members = [
  { tenant_id: "tnt_other", display_name: "别的组织" },
  { tenant_id: "tnt_magnet", display_name: "磁石科技" },
];

describe("adminHeaderTenantName", () => {
  it("whoami 的 tenant_name 有字就用它", () => {
    expect(adminHeaderTenantName({
      whoamiName: "磁石科技",
      members: [{ tenant_id: "tnt_magnet", display_name: "成员名" }],
      tenantId: "tnt_magnet",
      kept: "旧名称",
    })).toBe("磁石科技");
  });

  it("whoami 为空字符串或缺少该字段时，用本次成员列表里这个租户的 display_name", () => {
    expect(adminHeaderTenantName({
      whoamiName: "",
      members,
      tenantId: "tnt_magnet",
      kept: "",
    })).toBe("磁石科技");
    expect(adminHeaderTenantName({
      whoamiName: undefined,
      members,
      tenantId: "tnt_magnet",
    })).toBe("磁石科技");
    expect(adminHeaderTenantName({
      whoamiName: "   ",
      members,
      tenantId: " tnt_magnet ",
    })).toBe("磁石科技");
  });

  it("两边都空才是空名称，页眉再写成当前组织；不拿租户编号充名", () => {
    expect(adminHeaderTenantName({
      whoamiName: "",
      members: [{ tenant_id: "tnt_magnet", display_name: "  " }],
      tenantId: "tnt_magnet",
      kept: "",
    })).toBe("");
    expect(adminHeaderTenantName({
      whoamiName: undefined,
      members: [],
      tenantId: "tnt_magnet",
    })).toBe("");
    expect(adminHeaderTenantName({
      whoamiName: "",
      members: [{ tenant_id: "tnt_magnet", display_name: "" }],
      tenantId: "tnt_magnet",
      kept: "   ",
    })).not.toBe("tnt_magnet");
  });

  it("refresh 不得把已经得到的非空名称覆盖成空", () => {
    expect(adminHeaderTenantName({
      whoamiName: "",
      members: [{ tenant_id: "tnt_magnet", display_name: "" }],
      tenantId: "tnt_magnet",
      kept: "磁石科技",
    })).toBe("磁石科技");
    expect(adminHeaderTenantName({
      whoamiName: undefined,
      members: null,
      tenantId: "tnt_magnet",
      kept: "磁石科技",
    })).toBe("磁石科技");
  });

  it("成员列表有新的非空名称时改用它，不把旧名称一直留着", () => {
    expect(adminHeaderTenantName({
      whoamiName: "",
      members,
      tenantId: "tnt_magnet",
      kept: "当前组织",
    })).toBe("磁石科技");
  });
});

describe("admin 页眉接线", () => {
  it("确认组织和 refresh 都走同一条名称规则，页眉不画租户编号", () => {
    expect(admin).toContain('from "@/lib/admin-header-tenant"');
    expect(admin.match(/adminHeaderTenantName\(/g)).toHaveLength(2);
    expect(admin).not.toContain('setTenantName(typeof who.data.tenant_name === "string" ? who.data.tenant_name : "")');
    expect(admin).toContain('tenantName || "当前组织"');
    expect(admin).not.toContain("tenantName || tenantId");
    expect(admin).not.toContain("租户 ID");
    expect(admin).not.toContain("请填写租户");
  });
});
