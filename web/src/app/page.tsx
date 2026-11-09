import { AgentWorkBanner } from "@/components/work/agent-work-banner";
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
      {/* HUI-4593 走查收尾(r8):代理受控进入落地页的「返回代理台」横幅。
          最小挂载(2628 重构时收编);非代理渲染 null,零打扰。 */}
      <AgentWorkBanner />
      <TodayDesk />
    </div>
  );
}
