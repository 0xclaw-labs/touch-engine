import type { Metadata } from "next";
import "./globals.css";
import "./touch.css";
import "../styles/generated/painuo/v1/tokens.css";
import "../styles/generated/painuo/v1/aliases.css";

export const metadata: Metadata = {
  title: "碰一碰",
  description: "看今天门店要处理的活动",
};

// 主题策略（HUI-2628 走查暗色适配）：跟随系统在 light/dark 间切换。
// Painuo tokens 四 surface 的 dark 调色板齐备（tokens.css 按
// [data-pn-surface][data-pn-theme] 双属性作用域），SSR 缺省 light、内联脚本
// 在首帧前按 matchMedia 翻转并监听 change 动态跟随——暗色用户零闪烁。
// html 上同步 data-pn-theme+color-scheme：WebView 层的强制暗化（IAB 实测
// 无视 CSS color-scheme 声明）在页面本就深底浅字后失去反色目标，文本
// 对比度不再劣化。
const themeSyncScript = `(function(){var m=window.matchMedia("(prefers-color-scheme: dark)");function sync(){var t=m.matches?"dark":"light";document.documentElement.setAttribute("data-pn-theme",t);document.documentElement.setAttribute("color-scheme",t);document.querySelectorAll("[data-pn-theme]").forEach(function(el){el.setAttribute("data-pn-theme",t)});}sync();document.addEventListener("DOMContentLoaded",sync);if(m.addEventListener)m.addEventListener("change",sync);else if(m.addListener)m.addListener(sync);})();`;

export default function RootLayout({ children }: { children: React.ReactNode }) {
  // suppressHydrationWarning：主题脚本在首帧前按系统翻 dark，hydration 时
  // SSR(light)≠DOM(dark) 是预期差——不加会被 React 19 清回 SSR 值。
  return (
    <html lang="zh-CN" data-pn-theme="light" suppressHydrationWarning>
      <head>
        <script dangerouslySetInnerHTML={{ __html: themeSyncScript }} />
      </head>
      <body data-pn-surface="work.light" data-pn-theme="light" suppressHydrationWarning>{children}</body>
    </html>
  );
}
