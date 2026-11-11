import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { pageLedger } from "../src/lib/page-map";
import { pageCensus } from "../src/lib/page-census";
import { OPS_ACTION_LABELS, lifecycleText, ticketStatusText } from "../src/lib/ops-console";

const webRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

function read(rel: string): string {
  return readFileSync(path.join(webRoot, rel), "utf8");
}

function readOps(rel: string): string {
  return read(`src/components/ops/${rel}`);
}

describe("HUI-4592 平台运营控制台(账本与普查)", () => {
  it("六视图恰好记账,薄壳零标记", () => {
    const routes = pageLedger().map((p) => p.route);
    for (const r of ["/ops/tenants", "/ops/plans", "/ops/orders", "/ops/channels", "/ops/templates", "/ops/operations"]) {
      expect(routes).toContain(r);
    }
    const census = pageCensus();
    const opsPages = census.filter((p) => p.route.startsWith("/ops/"));
    expect(opsPages).toHaveLength(6);
    // 页面本体是薄壳:交互在 components/ops,页面源零标记。
    for (const page of opsPages) {
      expect(page.markers).toEqual([]);
    }
  });

  it("页面是薄壳:只挂 desk,不内联交互", () => {
    const shells: Record<string, string> = {
      "src/app/ops/tenants/page.tsx": "TenantsDesk",
      "src/app/ops/plans/page.tsx": "PlansDesk",
      "src/app/ops/orders/page.tsx": "OrdersDesk",
      "src/app/ops/channels/page.tsx": "ChannelsDesk",
      "src/app/ops/templates/page.tsx": "TemplatesDesk",
      "src/app/ops/operations/page.tsx": "OperationsDesk",
    };
    for (const [file, desk] of Object.entries(shells)) {
      const source = read(file);
      expect(source).toContain(desk);
      expect(source).not.toMatch(/<button/);
      expect(source).not.toMatch(/<input/);
    }
  });
});

const gate = () => readOps("ops-shell.tsx");

describe("HUI-4592 权限门(4593 受信 grant,org_owner ≠ 平台管理员)", () => {

  it("门读 access/effective 的 ops_permissions,无授权诚实受限视图", () => {
    const source = gate();
    expect(source).toContain("access/effective");
    expect(source).toContain("ops_permissions");
    expect(source).toContain('data-state="restricted"');
    expect(source).toContain("403");
    // FEATURE_AUTHZ off(404)与无 grant 同样受限,不渲染运营面。
    expect(source).toContain("404");
  });

  it("门不把商家会话当平台管理员:不查角色名放行", () => {
    const source = gate();
    // 判据只有 ops 命令(受信 grant),没有 role==="owner" 之类的旁门。
    expect(source).not.toMatch(/role\s*===\s*["']org_owner["']/);
    expect(source).not.toMatch(/role\s*===\s*["']owner["']/);
  });

  it("六视图导航齐备且每项绑定命令", () => {
    const source = gate();
    for (const r of ["tenants", "plans", "orders", "channels", "templates", "operations"]) {
      expect(source).toContain(`/ops/${r}`);
    }
    expect(source).toContain("data-ops-command");
  });
});

describe("HUI-4592 统一操作信封客户端", () => {
  it("幂等键按一次意图生成;重试读回既有操作;结果带 operation_id", () => {
    const source = gate();
    expect(source).toContain("oneIdempotencyKey");
    expect(source).toContain("crypto.randomUUID");
    expect(source).toContain("replayed");
    expect(source).toContain("operation_id");
    // 重试语义:读回时明示未重复执行。
    expect(source).toContain("未重复执行");
  });

  it("原因必填在前端先挡空值(服务端仍是最后一道闸)", () => {
    const source = gate();
    expect(source).toContain("操作原因(必填)");
    expect(source).toMatch(/!reason\.trim\(\)/);
  });

  it("冻结白名单文案与后端一致,不含白名单外动作", () => {
    expect(Object.keys(OPS_ACTION_LABELS)).toContain("tenant.suspend");
    expect(Object.keys(OPS_ACTION_LABELS)).toContain("channel.disable");
    expect(Object.keys(OPS_ACTION_LABELS)).not.toContain("wifi.turn_off");
  });
});

describe("HUI-4592 O1 商户视图", () => {
  const source = () => readOps("tenants-desk.tsx");

  it("深层数据锁定/开放两态;支持访问限时+原因", () => {
    const s = source();
    expect(s).toContain('data-state={detail.business_data.state}');
    expect(s).toContain("限时(最长 4 小时)");
    expect(s).toContain("ttl_minutes");
    expect(s).toContain("support-reason");
    expect(s).toContain("结束支持访问");
  });

  it("生命周期/收费保持动作带 expected_revision(读面下发版本号)", () => {
    const s = source();
    expect(s).toContain("expected_revision: String(detail.ops_revision)");
    expect(s).toContain("tenant.suspend");
    expect(s).toContain("tenant.resume");
    expect(s).toContain("tenant.charge_hold.set");
    expect(s).toContain("tenant.charge_hold.clear");
  });

  it("不泄露锁定商户的门店/成员清单", () => {
    const s = source();
    // 门店/成员清单只在 open 分支渲染。
    expect(s).toContain('detail.business_data.state === "open"');
  });
});

describe("HUI-4592 O2 套餐视图", () => {
  const source = () => readOps("plans-desk.tsx");

  it("只控生效窗口,不编辑金额;订单快照纪律在案", () => {
    const s = source();
    expect(s).toContain("plan.publish");
    expect(s).toContain("plan.retire");
    expect(s).toContain("不能修改金额与条款");
    expect(s).toContain("价格快照");
    // 页面没有金额编辑输入(只有原因与过滤输入)。
    expect(s).not.toContain('data-field="price-edit"');
  });
});

describe("HUI-4592 O3 订单视图", () => {
  const source = () => readOps("orders-desk.tsx");

  it("重查是只读操作信封,不改账;差异队列可见", () => {
    const s = source();
    expect(s).toContain('"order.recheck"');
    expect(s).toContain("discrepancy-queue");
    expect(s).toContain("tenant_id: order.tenant_id");
    expect(s).toContain("没有改账通道");
  });

  it("退款/发票/AI 用量三读面齐备,unavailable 与零记录区分", () => {
    const s = source();
    expect(s).toContain("data-section=\"refund-list\"");
    expect(s).toContain("data-section=\"ai-usage\"");
    expect(s).toContain("没有退款投影");
    expect(s).not.toMatch(/balance|钱包余额/);
  });
});

describe("HUI-4592 O4 渠道视图", () => {
  const source = () => readOps("channels-desk.tsx");

  it("紧急停用只停选中行;无一键全关;无关导航不可触达", () => {
    const s = source();
    expect(s).toContain("channel.disable");
    expect(s).toContain("channel.restore");
    expect(s).toContain("只停这一行");
    // 不存在批量关闭入口。
    expect(s).not.toContain("全部停用");
    expect(s).not.toContain("一键关闭");
  });

  it("证据档与复检窗如实展示;feature off 诚实空态", () => {
    const s = source();
    expect(s).toContain("evidenceGradeText");
    expect(s).toContain("复检至");
    expect(s).toContain('data-state="feature-off"');
    expect(s).toContain("待外部核实");
  });
});

describe("HUI-4592 O5 模板视图", () => {
  const source = () => readOps("templates-desk.tsx");

  it("撤回分发与风险下架走各自唯一写面;历史证据不被覆盖", () => {
    const s = source();
    expect(s).toContain("template.assignment.cancel");
    expect(s).toContain("content.retract");
    expect(s).toContain("撤回不覆盖历史证据");
    expect(s).toContain("已应用版本按冻结策略保留");
  });
});

describe("HUI-4592 O6 运行与交付视图(T20 面)", () => {
  const source = () => readOps("operations-desk.tsx");

  it("探针如实分级(degraded/未配置/不可达),不把错误填 0", () => {
    const s = source();
    expect(s).toContain('data-status={view.domain_health.status}');
    expect(s).toContain("降级(存在不可达/未配置目标)");
    expect(s).toContain("不可达");
    expect(s).toContain("未配置");
  });

  it("硬件履约只读投影,来源如实标注(1663 seam)", () => {
    const s = source();
    expect(s).toContain("hardware-source");
    expect(s).toContain("付款≠交付");
    expect(s).toContain("1663");
  });

  it("工单状态机动作齐备,关闭必带理由;恢复演练必带校验报告", () => {
    const s = source();
    expect(s).toContain("ticket.confirm");
    expect(s).toContain("ticket.resolve");
    expect(s).toContain("ticket.close");
    expect(s).toContain("关闭理由");
    expect(s).toContain("恢复演练必填");
    expect(s).toContain('value="failed"');
  });

  it("操作按 operation_id 追踪;审计回查四要素可见", () => {
    const s = source();
    expect(s).toContain("recent-operations");
    expect(s).toContain("audit-trail");
    expect(s).toContain("前后值/操作者/时间/原因");
    expect(s).toContain("authority_source");
  });
});

describe("HUI-4592 文案收敛", () => {
  it("生命周期/工单状态有产品文案,未知值原样透出不编造", () => {
    expect(lifecycleText("suspended")).toBe("已暂停");
    expect(lifecycleText("")).toBe("未知");
    expect(lifecycleText("future_state")).toBe("future_state");
    expect(ticketStatusText("open")).toBe("待认领");
    expect(ticketStatusText("resolved")).toBe("已修复待确认");
  });
});
