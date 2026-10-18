"use client";

import { useCallback, useEffect, useState } from "react";
import { failureText, useSession } from "@/components/work/merchant-session";
import { surfaceLabel } from "@/lib/product-finish";
import { presentProduction, type ProductionJob, type ProductionView } from "@/lib/work/production";

// HUI-3553 MX-OPS 增量:活动详情「云端制作」区。
// 纪律:
//   - 只展示权威队列投影;没有投影就写「未知」,不给伪进度;
//   - 夜间窗口与活动结束日分开,赶不上只提示,不自动升级付费/改活动;
//   - 商家工具费 / 活动预算 / 模型成本分开,只展示不建钱包;
//   - 恢复只回同一 handoff/job,不新建制作工程;
//   - 候选回流在「活动内容」区预览后显式采用;本区不自动采用/外发;
//   - 顾客触碰不会新开 GPU 或重新生成。

function toUnix(value: string | null | undefined): number | null {
  if (!value) return null;
  const millis = Date.parse(value);
  return Number.isFinite(millis) ? Math.floor(millis / 1000) : null;
}

function waitingKindLabel(kind: ProductionView["waitingKind"]): string {
  switch (kind) {
    case "cloud":
      return "等待云端产物";
    case "window":
      return "等待时间窗";
    case "human":
      return "等待商家决定";
    case "none":
      return "进行中";
    default:
      return "状态未知";
  }
}

export function ProductionPanel({
  campaignId,
  campaignEndsAt,
}: {
  campaignId: string;
  campaignEndsAt?: string | null;
}) {
  const session = useSession();
  const [phase, setPhase] = useState<"loading" | "empty" | "ready" | "error">("loading");
  const [jobs, setJobs] = useState<ProductionJob[]>([]);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    setPhase("loading");
    const res = await session.api("GET", `campaigns/${campaignId}/content-handoffs`);
    if (!res.ok) {
      setError(failureText(res.status, res.data));
      setPhase("error");
      return;
    }
    const items = Array.isArray(res.data.handoffs) ? (res.data.handoffs as ProductionJob[]) : [];
    setJobs(items);
    setError("");
    setPhase(items.length === 0 ? "empty" : "ready");
  }, [campaignId, session]);

  useEffect(() => {
    void load().catch(() => {
      setError("没有连上服务。制作状态没有读到，不把它当成没有制作。");
      setPhase("error");
    });
  }, [load]);

  const publishAt = toUnix(campaignEndsAt ?? null);

  return (
    <section id="production" className="tk-section">
      <h2 className="tk-section-title">云端制作</h2>
      <p className="tk-note">
        这里显示已交给专业工具的云端制作与排队。制作由该工具经星模完成，本页不新开 GPU；顾客触碰只读取已采用的预制内容，不会新开生成。
      </p>
      <p className="tk-note tk-unknown" data-field="visitor-guard">
        访客路径：只读已采用内容，零新生成。
      </p>
      {phase === "loading" ? <p className="tk-state" data-state="loading">{surfaceLabel("loading")}</p> : null}
      {phase === "empty" ? (
        <p className="tk-state" data-state="empty">
          这个活动还没有云端制作交接。去<a href="/work/materials">素材页</a>从「帮我制作」提交需求；制作引擎未接通时按钮会说明原因。
        </p>
      ) : null}
      {phase === "error" ? (
        <p className="tk-state tk-danger" data-state="error">
          {error || surfaceLabel("error")}
          <button className="tk-quiet" type="button" data-action="retry" onClick={() => void load()}>重试</button>
        </p>
      ) : null}
      {phase === "ready" ? (
        <ul className="tk-list" data-list="production">
          {jobs.map((job) => {
            const view = presentProduction(job, {
              campaignPublishAtUnix: publishAt,
              entitlementRefs: [],
              quoteConfirmed: false,
            });
            return (
              <li key={view.handoffId ?? view.jobRef ?? "handoff"} data-handoff-id={view.handoffId ?? ""}>
                <p className="tk-note" data-field="purpose">{job.purpose || "（未填写制作目的）"}</p>
                <p className="tk-note" data-field="preference">
                  执行偏好：{view.preferenceLabel}
                  {view.jobRef ? ` · 任务 ${view.jobRef}` : ""}
                </p>
                <p className="tk-note" data-field="waiting">
                  {waitingKindLabel(view.waitingKind)}：{view.waitingText}
                </p>
                {view.queue ? (
                  <>
                    <p className="tk-note" data-field="queue">
                      {view.queue.queueKindLabel} · {view.queue.phaseLabel} · 前面共 {view.queue.jobsAhead} 个任务（含运行中）
                      {" · "}等待 {view.queue.waitingJobsAheadText} · 运行中 {view.queue.runningJobsText}
                    </p>
                    <p className="tk-note" data-field="eta">
                      预估 {view.queue.etaText}
                      {view.queue.policyVersion === null ? "" : ` · 策略版本 v${view.queue.policyVersion}`}
                      {view.nextEligibleText ? ` · 下一可执行窗口 ${view.nextEligibleText}` : ""}
                    </p>
                  </>
                ) : (
                  <p className="tk-note tk-unknown" data-field="queue-unknown">
                    云端排队状态未知：没有拿到权威队列投影，不提供估算时间。
                  </p>
                )}
                {view.publishWarning ? (
                  <p className="tk-note tk-unknown" data-field="publish-warning">{view.publishWarning}</p>
                ) : null}
                <p className="tk-note" data-field="fees">
                  商家工具费 {view.feeLines.merchantToolFee} · 活动预算 {view.feeLines.campaignBudget} · 模型成本 {view.feeLines.modelCost}
                </p>
                <p className="tk-note" data-field="fee-note">{view.feeNote}</p>
                <p className="tk-note" data-field="recovery">{view.recovery.text}</p>
                <p className="tk-note" data-field="candidate">
                  回流产物只进候选，在<a href="#content-packs">活动内容</a>预览后显式采用；不会自动替换当前启用内容。
                </p>
                <p className="tk-note tk-unknown" data-field="notice">{view.notice}</p>
                <div className="tk-row">
                  <button
                    className="tk-button"
                    type="button"
                    data-action="arrange-night"
                    disabled
                    title="制作引擎尚未接通：产品图/GoBoost/AiCut/数字人的真实交接还没开通。"
                  >
                    安排夜间制作
                  </button>
                  <button className="tk-quiet" type="button" data-action="reload" onClick={() => void load()}>
                    刷新状态
                  </button>
                </div>
              </li>
            );
          })}
        </ul>
      ) : null}
    </section>
  );
}
