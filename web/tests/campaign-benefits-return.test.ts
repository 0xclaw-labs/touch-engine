import * as React from "react";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { BenefitsView } from "../src/app/s/[storeCode]/benefits/benefits-view";

vi.mock("next/navigation", () => ({ useParams: () => ({ storeCode: "sf_original_store" }) }));
beforeEach(() => vi.stubGlobal("React", React));
afterEach(() => vi.unstubAllGlobals());

it("the shared personal-benefits view returns to its original activity when opened there", () => {
  const html = renderToStaticMarkup(createElement(BenefitsView, {
    returnHref: "/c/HDR2727ZSH71", returnLabel: "返回活动",
  }));
  expect(html).toContain('href="/c/HDR2727ZSH71"');
  expect(html).toContain("返回活动");
  expect(html).not.toContain('href="/s/HDR2727ZSH71"');
});

it("the original storefront entry keeps its own store return", () => {
  const html = renderToStaticMarkup(createElement(BenefitsView));
  expect(html).toContain('href="/s/sf_original_store"');
  expect(html).toContain("返回门店");
});
