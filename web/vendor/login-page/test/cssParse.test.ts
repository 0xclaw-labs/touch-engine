// CSS 语法哨兵：逐个解析 src 下全部 .module.css，语法错即 fail。
//
// 背景：ui.module.css 头注释曾因「--dur-*/--ease*」里的 `*/` 过早终结块注释，
// 注释残文被吞进后续规则（.tab 整条蒸发）而包内测试/CI 全绿。本测试防该类
// bug 复发，CI 跑 npm test 即自动覆盖。
//
// 注：postcss 8 没有 safeParse 导出（严格 parse 遇语法错直接抛 CssSyntaxError，
// 覆盖面只多不少）。但历史上的早终结案例 parse 本身不抛错——残文被吸收成
// 「合法」节点，故再加一层 AST 哨兵：选择器 / 声明名 / @规则参数不含非 ASCII
// （本包合法值全 ASCII，注释残文是中文）。
import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import test from "node:test";
import assert from "node:assert/strict";
import postcss from "postcss";

function listCss(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = `${dir}${entry.name}`;
    if (entry.isDirectory()) return listCss(`${path}/`);
    return entry.name.endsWith(".css") ? [path] : [];
  });
}

const srcDir = fileURLToPath(new URL("../src/", import.meta.url));
const files = listCss(srcDir);

test("cssParse 清单非空：src 下至少发现 2 个 .css", () => {
  assert.ok(files.length >= 2, `应发现 src 下的 css 文件，实际只找到 ${files.length}`);
});

for (const file of files) {
  test(`CSS 语法解析：${file.slice(srcDir.length)}`, () => {
    const css = readFileSync(file, "utf8");
    // 语法错（未闭合块/坏字符等）→ CssSyntaxError 抛出即 fail
    const root = postcss.parse(css, { from: file });
    const asciiOnly = (value: string, where: string) =>
      assert.match(
        value,
        /^[\x00-\x7F]*$/,
        `${where} 含非 ASCII 字符（疑似注释早终结的残文）: ${JSON.stringify(value.slice(0, 60))}`,
      );
    root.walk((node) => {
      if (node.type === "rule") asciiOnly(node.selector, `${file.slice(srcDir.length)} 选择器`);
      else if (node.type === "decl") asciiOnly(node.prop, `${file.slice(srcDir.length)} 声明名`);
      else if (node.type === "atrule")
        asciiOnly(node.params, `${file.slice(srcDir.length)} @${node.name} 参数`);
    });
  });
}
