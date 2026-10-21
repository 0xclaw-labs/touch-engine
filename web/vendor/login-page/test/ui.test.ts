import test from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import { componentHarness, elements } from "./harness.ts";

test("Segmented 渲染 tablist 且点击回调 onChange", () => {
  const harness = componentHarness(
    fileURLToPath(new URL("../src/internal/Segmented.tsx", import.meta.url)),
    {},
  );
  const tree = harness.render("Segmented", {
    items: [{ key: "otp", label: "验证码" }, { key: "password", label: "密码" }],
    value: "otp",
    onChange: (key: string) => recorded.push(key),
    ariaLabel: "登录方式",
  });
  const recorded: string[] = [];
  const tabs = elements(tree).filter((el) => el.props?.role === "tab");
  assert.equal(tabs.length, 2);
  tabs[1].props.onClick();
  assert.deepEqual(recorded, ["password"]);
});

test("Button loading 态渲染三点且禁用点击", () => {
  const harness = componentHarness(
    fileURLToPath(new URL("../src/internal/Button.tsx", import.meta.url)),
    {},
  );
  const tree = harness.render("Button", { variant: "primary", loading: true, type: "submit", children: "登录" });
  const dots = elements(tree).filter((el) => el.props?.className?.includes?.("dots") || el.type === "i");
  assert.ok(dots.length >= 1);
  const button = elements(tree).find((el) => el.type === "button");
  assert.equal(button?.props.type, "submit");
  assert.equal(button?.props.disabled, true);
});

test("TextField label htmlFor 配对、invalid 走 aria-invalid 且 error 文案行 role=alert", () => {
  const harness = componentHarness(
    fileURLToPath(new URL("../src/internal/TextField.tsx", import.meta.url)),
    {},
  );
  const recorded: string[] = [];
  const tree = harness.render("TextField", {
    label: "邮箱",
    value: "a@b.c",
    onChange: (value: string) => recorded.push(value),
    invalid: true,
    error: "邮箱格式不正确",
  });
  const label = elements(tree).find((el) => el.type === "label");
  const input = elements(tree).find((el) => el.type === "input");
  assert.ok(label && input);
  assert.equal(label.props.htmlFor, input.props.id);
  assert.equal(input.props["aria-invalid"], true);
  assert.equal(String(input.props.className).includes("invalid"), true);
  assert.equal(input.props.value, "a@b.c");
  const alertLine = elements(tree).find((el) => el.props?.role === "alert");
  assert.equal(alertLine?.props.children, "邮箱格式不正确");
  input.props.onChange({ currentTarget: { value: "x@y.z" } });
  assert.deepEqual(recorded, ["x@y.z"]);
});
