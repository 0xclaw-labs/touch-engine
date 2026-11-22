import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const root = path.resolve(process.cwd(), "src");

function read(rel: string): string {
  return readFileSync(path.join(root, rel), "utf8");
}

// HUI-4884 原语落地证据：Command 面板为零依赖受控结构（不引入 cmdk/popover），
// 组织切换候选只来自 memberships；侧栏分组与待办 Badge 在 AdminSidebar 落地。
describe("HUI-4884 shadcn-admin 壳层（Command / 分组侧栏 / 待办 Badge）", () => {
  it("Command 原语导出输入、列表、分组、条目与空态", () => {
    const src = read("components/ui/command.tsx");
    expect(src).toContain("function CommandInput");
    expect(src).toContain("function CommandList");
    expect(src).toContain("function CommandGroup");
    expect(src).toContain("function CommandItem");
    expect(src).toContain("function CommandEmpty");
    expect(src).toContain('role="listbox"');
    expect(src).toContain('role="option"');
  });

  it("组织切换 Command 只在受控成员列表内搜索并回传选中租户", () => {
    const src = read("components/admin/OrgSwitchCommand.tsx");
    expect(src).toContain('from "@/components/ui/command"');
    expect(src).toContain("usable(scopes)");
    expect(src).toContain("onSelect(item.tenant_id)");
    expect(src).toContain("Escape");
    expect(src).toContain("inputRef.current?.focus()");
  });

  it("侧栏按分组渲染导航，待办以 Badge 呈现，组织切换接 Command", () => {
    const src = read("components/admin/AdminSidebar.tsx");
    expect(src).toContain('from "./OrgSwitchCommand"');
    expect(src).toContain('from "@/components/ui/badge"');
    expect(src).toContain('label: "概览"');
    expect(src).toContain('label: "经营"');
    expect(src).toContain('label: "分析与服务"');
    expect(src).toContain("badge: todoCount");
  });

  it("AdminShell 把成员列表与待办计数交给侧栏，页面回传待办数", () => {
    const shell = read("components/admin/AdminShell.tsx");
    expect(shell).toContain('from "./AdminSidebar"');
    expect(shell).toContain("scopes={scopes}");
    expect(shell).toContain("todoCount={todoCount}");
    const page = read("app/admin/page.tsx");
    expect(page).toContain("onTodoCount={setTodoCount}");
  });
});
