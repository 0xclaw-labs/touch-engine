import * as React from "react";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, expect, it, vi } from "vitest";
import { CustomerPublish } from "../src/app/c/[code]/customer-publish";

afterEach(() => vi.unstubAllGlobals());
it("reference content is readable before legacy execution is actually authorized", () => {
  vi.stubGlobal("React", React);
  const html = renderToStaticMarkup(createElement(CustomerPublish, { code: "original-campaign", defaultCopy: "原活动参考内容" }));
  expect(html).toContain("原活动参考内容");
  expect(html).not.toContain('type="checkbox"');
  expect(html).not.toContain("我已在平台操作");
  expect(html).not.toContain("导出参考文案与步骤");
});
