import { describe, expect, it } from "vitest";
import {
  SERVICE_DESK_HREF,
  SERVICE_DESK_LABEL,
  serviceDeskEntryVisible,
} from "@/lib/work/service-desk";

// HUI-2222：「确认与核销」入口可见性是纯判定。可见集对齐服务端 authz
// ActionUpdate 矩阵（org_owner / store_manager / staff；agent 只读不进），
// 开关缺省或 off 一律不渲染（HUI-3562 页面本体，未开通不冒充可用）。

describe("service desk entry visibility", () => {
  it("label and href are merchant language, pointing at the future page", () => {
    expect(SERVICE_DESK_LABEL).toBe("确认与核销");
    expect(SERVICE_DESK_HREF).toBe("/work/service");
  });

  it("feature off or missing hides the entry for every role", () => {
    for (const features of [undefined, { serviceDesk: false }] as const) {
      for (const role of ["org_owner", "store_manager", "staff", "agent", "", "unknown"]) {
        expect(serviceDeskEntryVisible(features, role)).toBe(false);
      }
    }
  });

  it("feature on shows the entry only for service-capable roles", () => {
    expect(serviceDeskEntryVisible({ serviceDesk: true }, "org_owner")).toBe(true);
    expect(serviceDeskEntryVisible({ serviceDesk: true }, "store_manager")).toBe(true);
    expect(serviceDeskEntryVisible({ serviceDesk: true }, "staff")).toBe(true);
  });

  it("feature on still hides read-only agent and unknown roles", () => {
    for (const role of ["agent", "", "unknown", "owner"]) {
      expect(serviceDeskEntryVisible({ serviceDesk: true }, role)).toBe(false);
    }
  });

  it("does not mutate its inputs", () => {
    const features = { serviceDesk: true };
    serviceDeskEntryVisible(features, "staff");
    expect(features).toEqual({ serviceDesk: true });
  });
});
