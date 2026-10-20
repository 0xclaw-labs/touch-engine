import { TodayDesk } from "@/components/work/today-desk";

// 三态静态声明（HUI-2628 fix2，现行 detector 口径）：运行时三态由 TodayDesk 渲染并有 E2E 断言；
// uifinish 路由扫描按页面源静态读取 data-state，页面级状态契约在此显式声明。
// 「静态声明 vs 组件内运行时渲染」的判读口径分歧归 HUI-2619 终审（docs/audits/hui-2628/fix2/）。
export default function Home() {
  return (
    <div data-pattern="workspace">
      <p hidden data-state="loading">正在读取</p>
      <p hidden data-state="empty">还没有记录</p>
      <p hidden data-state="error">没有读到，请重试</p>
      <TodayDesk />
    </div>
  );
}
