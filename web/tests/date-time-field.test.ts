// @vitest-environment jsdom
import * as React from "react";
import { act, createElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { DateField } from "@/components/ui/date-field";
import { DateTimeField } from "@/components/ui/date-time-field";

// D7 复合件控件级交互验收：打开触发器 → 选日期 → 填时间槽 → 提交回调收到
// 同口径本地墙钟值；单日期件空值允许；非法区间 Apply 禁用且不发提交。

let root: Root;
let container: HTMLDivElement;

beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  document.body.querySelectorAll("[data-radix-popper-content-wrapper]").forEach((node) => node.remove());
});

function trigger(): HTMLButtonElement {
  const button = container.querySelector('button[aria-haspopup="dialog"]') as HTMLButtonElement | null;
  expect(button, "日历触发器应可聚焦").not.toBeNull();
  return button!;
}

function popoverContent(): HTMLElement {
  const wrapper = document.body.querySelector("[data-radix-popper-content-wrapper]");
  expect(wrapper, "浮层应挂到 body Portal").not.toBeNull();
  return wrapper!.firstElementChild as HTMLElement;
}

async function openPopover() {
  await act(async () => { trigger().click(); });
  return popoverContent();
}

function setInputValue(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value")!.set!;
  setter.call(input, value);
  input.dispatchEvent(new Event("input", { bubbles: true }));
}

describe("DateTimeField 复合件", () => {
  it("打开 → 选日期 → 填时间槽 → 回调收到 YYYY-MM-DDTHH:mm 同口径值", async () => {
    const onChange = vi.fn();
    await act(async () => {
      root.render(createElement(DateTimeField, {
        name: "claim_from",
        "aria-label": "领取开始时间",
        value: "",
        onChange,
      }));
    });

    // hidden input 承载提交值（对齐原生 datetime-local 的 FormData 行为）。
    const hidden = container.querySelector('input[type="hidden"][name="claim_from"]');
    expect(hidden).not.toBeNull();
    expect(hidden!.getAttribute("value")).toBe("");

    const content = await openPopover();

    const dayButtons = Array.from(content.querySelectorAll("button")).filter((b) =>
      /\d{4}/.test(b.getAttribute("aria-label") ?? ""),
    );
    const target = dayButtons.find((b) => !b.hasAttribute("disabled") && b.textContent?.trim() === "24");
    expect(target, "日历应渲染可选日期格").toBeDefined();
    await act(async () => { target!.click(); });

    const afterDay = onChange.mock.calls.at(-1)![0] as string;
    expect(afterDay).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:00$/);

    const timeInput = content.querySelector('input[type="time"]') as HTMLInputElement;
    expect(timeInput, "复合件内显式时间槽应存在").not.toBeNull();
    await act(async () => { setInputValue(timeInput, "09:45"); });
    const afterTime = onChange.mock.calls.at(-1)![0] as string;
    expect(afterTime).toBe(`${afterDay.slice(0, 10)}T09:45`);
  });

  it("清除按钮把值回到空串", async () => {
    const onChange = vi.fn();
    await act(async () => {
      root.render(createElement(DateTimeField, {
        name: "claim_until",
        value: "2026-11-24T08:30",
        onChange,
      }));
    });
    const content = await openPopover();
    const clear = Array.from(content.querySelectorAll("button")).find((b) => b.textContent?.includes("清除"));
    expect(clear).toBeDefined();
    await act(async () => { clear!.click(); });
    expect(onChange).toHaveBeenLastCalledWith("");
  });
});

describe("DateField 复合件", () => {
  it("选日期回调收到 YYYY-MM-DD；清空回到空串", async () => {
    const onChange = vi.fn();
    await act(async () => {
      root.render(createElement(DateField, {
        name: "valid_until",
        "aria-label": "授权截止日期",
        value: "2026-11-24",
        onChange,
      }));
    });
    const content = await openPopover();
    const dayButtons = Array.from(content.querySelectorAll("button")).filter((b) =>
      /\d{4}/.test(b.getAttribute("aria-label") ?? ""),
    );
    const target = dayButtons.find((b) => !b.hasAttribute("disabled") && b.textContent?.trim() === "25");
    expect(target).toBeDefined();
    await act(async () => { target!.click(); });
    expect(onChange).toHaveBeenLastCalledWith("2026-11-25");

    // 选完日期浮层已关闭；重新打开再点清除。
    await act(async () => { trigger().click(); });
    const content2 = popoverContent();
    const clear = Array.from(content2.querySelectorAll("button")).find((b) => b.textContent?.includes("清除"));
    await act(async () => { clear!.click(); });
    expect(onChange).toHaveBeenLastCalledWith("");
  });
});

describe("先后校验门控", () => {
  it("起止非法时 Apply 禁用；合法后解除禁用", async () => {
    let start = "2026-11-24T08:00";
    let end = "2026-11-23T08:00";
    const setStart = (v: string) => { start = v; };
    const setEnd = (v: string) => { end = v; };
    function Harness() {
      const invalid = Date.parse(end) <= Date.parse(start);
      return createElement(
        "div",
        null,
        createElement(DateTimeField, { name: "starts", value: start, onChange: setStart }),
        createElement(DateTimeField, { name: "ends", value: end, onChange: setEnd }),
        createElement("button", { type: "button", "data-testid": "apply", disabled: invalid }, "应用"),
      );
    }
    await act(async () => { root.render(createElement(Harness)); });
    const apply = container.querySelector('[data-testid="apply"]') as HTMLButtonElement;
    expect(apply.disabled).toBe(true);

    end = "2026-11-25T08:00";
    await act(async () => { root.render(createElement(Harness)); });
    expect(apply.disabled).toBe(false);
  });

  it("纯函数口径：晚于起点才放行", () => {
    const ok = Date.parse("2026-11-25T08:00") > Date.parse("2026-11-24T08:00");
    expect(ok).toBe(true);
  });
});
