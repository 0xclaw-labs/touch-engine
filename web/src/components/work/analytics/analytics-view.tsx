"use client";

// HUI-1677 经营看板(/work/analytics)。
//
// 消费 GET /api/v1/dashboard/operations(经 BFF catch-all 零改动透传;
// FEATURE_DASHBOARD 默认 off,off 时端点答统一 404 → 显示未开通文案,
// 缺失保持未知,不显示 0)。纪律:
//   - 五区块:漏斗七级 / 三类核验证据 / 库存 / 成本三分 / 异常待处理;
//   - 三类证据单列,绝不相加成「发布总人数」;
//   - 每级漏斗带口径披露(definition);请求了但本级没有该维度事实的筛选,
//     照实披露「本级未生效」,绝不假装过滤;
//   - 成本三分:内容实际生成费用(未录入就写未录入)/ 奖励实际兑现成本 /
//     券面额(面额是营销面值,不是支出);
//   - nfc 只是入口标记、匿名参与不冒充自然人数:两条注记来自响应常量,
//     必渲染;
//   - 空窗口是真实 0,不是未知,不是失败;
//   - 移动优先(tk- 体系,无固定像素宽);本组件只渲染,不发裸 fetch。

import { useEffect, useState } from "react";
import { failureText, useSession } from "@/components/work/merchant-session";
import {
  ANALYTICS_NOT_ENABLED_COPY,
  FILTER_LABELS,
  buildAnalyticsQuery,
  emptyWindowCopy,
  evidenceRowList,
  formatMinor,
  localDayWindow,
  stageNotAppliedLabels,
  type AnalyticsFilters,
  type FunnelStage,
  type OperationsResponse,
} from "@/lib/analytics";

const TOUCHPOINT_OPTIONS = ["nfc", "qr", "shorturl", "manual"] as const;
const HANDOFF_METHOD_OPTIONS = ["h5", "platform_miniprogram", "staff_view"] as const;

const EMPTY_FILTERS: AnalyticsFilters = {};

export function AnalyticsView() {
  const session = useSession();
  const [phase, setPhase] = useState<"loading" | "error" | "disabled" | "ready">("loading");
  const [loadError, setLoadError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const [data, setData] = useState<OperationsResponse | null>(null);

  const [draft, setDraft] = useState<AnalyticsFilters>(EMPTY_FILTERS);
  const [applied, setApplied] = useState<AnalyticsFilters>(EMPTY_FILTERS);

  useEffect(() => {
    let alive = true;
    setPhase("loading");
    const query = buildAnalyticsQuery(localDayWindow(new Date()), applied);
    void session
      .api("GET", `dashboard/operations?${query}`)
      .then((res) => {
        if (!alive) return;
        // 未开通(路由未注册 + gate 统一 404)不是空数据:缺失保持未知。
        if (res.status === 404) {
          setPhase("disabled");
          return;
        }
        if (!res.ok) {
          setLoadError(failureText(res.status, res.data));
          setPhase("error");
          return;
        }
        setData(res.data as unknown as OperationsResponse);
        setPhase("ready");
      })
      .catch(() => {
        if (!alive) return;
        setLoadError("没有连上服务。没有读到任何统计，请检查网络后重试。");
        setPhase("error");
      });
    return () => {
      alive = false;
    };
  }, [session, applied, attempt]);

  if (phase === "loading") {
    return (
      <main>
        <h1 className="tk-title">经营看板</h1>
        <p className="tk-state" data-state="loading">正在读取…</p>
      </main>
    );
  }

  if (phase === "error") {
    return (
      <main>
        <h1 className="tk-title">经营看板</h1>
        <p className="tk-state tk-danger" data-state="error">
          {loadError || "没有读到统计数据。"}
          <button className="tk-quiet" type="button" data-action="retry" onClick={() => setAttempt((a) => a + 1)}>
            重试
          </button>
        </p>
      </main>
    );
  }

  if (phase === "disabled") {
    return (
      <main>
        <h1 className="tk-title">经营看板</h1>
        <p className="tk-state tk-unknown" data-state="disabled">{ANALYTICS_NOT_ENABLED_COPY}</p>
      </main>
    );
  }

  if (!data) return null;

  const windowAllZero = data.funnel.length > 0 && data.funnel.every((stage) => stage.value === 0);
  const campaigns = data.dimensions.campaigns ?? [];
  const stores = data.dimensions.stores ?? [];

  const setField = (field: keyof AnalyticsFilters) => (value: string) =>
    setDraft((prev) => ({ ...prev, [field]: value }));

  return (
    <main>
      <h1 className="tk-title">经营看板</h1>
      <p className="tk-lead">
        {data.scope.store_scope ? `统计范围:本店(${data.scope.store_scope})。` : "统计范围:全部门店。"}
        只统计已经发生的真实事实;拿不到的就是未知,未知不写成 0。
      </p>
      <p className="tk-note" data-field="window-line">
        窗口 {data.window.start} 到 {data.window.end}。
      </p>
      {windowAllZero ? (
        <p className="tk-note" data-field="window-empty">{emptyWindowCopy()}</p>
      ) : null}

      <section aria-label="筛选" data-section="filters">
        <h2 className="tk-subtitle">筛选</h2>
        <label className="tk-label">
          活动
          <select
            className="tk-select"
            data-filter="campaign"
            value={draft.campaignId ?? ""}
            onChange={(event) => setField("campaignId")(event.target.value)}
          >
            <option value="">全部活动</option>
            {campaigns.map((item) => (
              <option key={item.id} value={item.id}>{item.title || item.id}</option>
            ))}
          </select>
        </label>
        <label className="tk-label">
          门店
          <select
            className="tk-select"
            data-filter="store"
            value={draft.storeId ?? ""}
            onChange={(event) => setField("storeId")(event.target.value)}
          >
            <option value="">全部门店</option>
            {stores.map((item) => (
              <option key={item.id} value={item.id}>{item.name || item.id}</option>
            ))}
          </select>
        </label>
        <label className="tk-label">
          内容包版本
          <input
            className="tk-input"
            data-filter="version"
            value={draft.contentPackVersion ?? ""}
            onChange={(event) => setField("contentPackVersion")(event.target.value)}
            placeholder="如 v3"
            autoComplete="off"
          />
        </label>
        <label className="tk-label">
          触点
          <select
            className="tk-select"
            data-filter="touchpoint"
            value={draft.touchpoint ?? ""}
            onChange={(event) => setField("touchpoint")(event.target.value)}
          >
            <option value="">全部触点</option>
            {TOUCHPOINT_OPTIONS.map((item) => (
              <option key={item} value={item}>{item}</option>
            ))}
          </select>
        </label>
        <label className="tk-label">
          平台
          <input
            className="tk-input"
            data-filter="platform"
            value={draft.platform ?? ""}
            onChange={(event) => setField("platform")(event.target.value)}
            placeholder="如 douyin"
            autoComplete="off"
          />
        </label>
        <label className="tk-label">
          承接方式
          <select
            className="tk-select"
            data-filter="method"
            value={draft.handoffMethod ?? ""}
            onChange={(event) => setField("handoffMethod")(event.target.value)}
          >
            <option value="">全部承接方式</option>
            {HANDOFF_METHOD_OPTIONS.map((item) => (
              <option key={item} value={item}>{item}</option>
            ))}
          </select>
        </label>
        <button
          className="tk-button"
          type="button"
          data-action="apply-filters"
          onClick={() => setApplied({ ...draft })}
        >
          应用筛选
        </button>
        <button
          className="tk-quiet"
          type="button"
          data-action="reset-filters"
          onClick={() => {
            setDraft(EMPTY_FILTERS);
            setApplied(EMPTY_FILTERS);
          }}
        >
          重置
        </button>
      </section>

      <section aria-label="漏斗" data-section="funnel">
        <h2 className="tk-subtitle">漏斗七级</h2>
        {data.funnel.length === 0 ? (
          <p className="tk-note">没有读到漏斗事实。缺失保持未知,不显示 0。</p>
        ) : (
          <ol className="tk-list" data-field="funnel-list">
            {data.funnel.map((stage) => {
              const notApplied = stageNotAppliedLabels(stage, data.filter_echo);
              return (
                <li key={stage.key} data-stage={stage.key}>
                  <span data-field="stage-title">{stage.title}</span>
                  <strong data-field="stage-value">{stage.available ? stage.value : "未知"}</strong>
                  {(stage.applied_filters ?? []).length > 0 ? (
                    <span className="tk-note" data-field="stage-applied">
                      本级按 {(stage.applied_filters ?? []).map((key) => FILTER_LABELS[key] ?? key).join("、")} 收窄
                    </span>
                  ) : null}
                  {notApplied.length > 0 ? (
                    <span className="tk-note tk-danger" data-field="stage-not-applied">
                      本级未生效:{notApplied.join("、")}(这一级的事实没有这些维度,不假装过滤)
                    </span>
                  ) : null}
                  {stage.note ? <span className="tk-note" data-field="stage-note">{stage.note}</span> : null}
                  <details className="tk-note" data-field="stage-definition">
                    <summary>口径</summary>
                    <p>来源:{stage.definition.source}</p>
                    <p>去重:{stage.definition.dedup_key}</p>
                    <p>分母:{stage.definition.denominator}</p>
                    <p>事件时间:{stage.definition.event_time}</p>
                    <p>窗口:{stage.definition.window}</p>
                  </details>
                </li>
              );
            })}
          </ol>
        )}
      </section>

      <section aria-label="核验证据" data-section="evidence">
        <h2 className="tk-subtitle">发布核验证据</h2>
        <ul className="tk-list" data-field="evidence-list">
          {evidenceRowList(data.evidence).map((row) => (
            <li key={row.key} data-evidence={row.key}>
              {row.label}:<strong data-field="evidence-count">{row.count}</strong>
            </li>
          ))}
        </ul>
        <p>
          总体覆盖(distinct 参与数):
          <strong data-field="evidence-distinct">{data.evidence.distinct_participations}</strong>
        </p>
        <p>
          官方确认发布(按可信尝试):
          <strong data-field="evidence-official">{data.evidence.official_attempts}</strong>
        </p>
        <p className="tk-note" data-field="evidence-note">{data.evidence.note}</p>
      </section>

      <section aria-label="奖励库存" data-section="inventory">
        <h2 className="tk-subtitle">奖励库存(账本口径)</h2>
        {data.inventory.length === 0 ? (
          <p className="tk-note" data-field="inventory-empty">统计范围内没有库存批次。没有事实不写 0。</p>
        ) : (
          <ul className="tk-list" data-field="inventory-list">
            {data.inventory.map((line) => (
              <li key={line.inventory_id} data-inventory={line.inventory_id}>
                <span data-field="inventory-name">{line.name} · {line.benefit_slot} · 规则 v{line.rule_version}</span>
                <span data-field="inventory-counts">
                  可用 {line.available} · 预留 {line.reserved} · 已发 {line.issued} · 已核销 {line.redeemed}
                  {line.reversed > 0 ? ` · 撤销 ${line.reversed}` : ""}
                  {line.expired > 0 ? ` · 过期 ${line.expired}` : ""}
                </span>
                <span className="tk-note" data-field="inventory-face">
                  面额 {formatMinor(line.face_value_minor)}{line.currency ? ` ${line.currency}` : ""}
                  {line.store_id ? "" : " · 租户级批次(全店共享)"}
                </span>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section aria-label="成本" data-section="cost">
        <h2 className="tk-subtitle">成本三分</h2>
        <p data-field="cost-reward">
          奖励实际兑现成本(已发):{formatMinor(data.cost.reward_cost_minor)};其中已核销:
          {formatMinor(data.cost.reward_redeemed_cost_minor)}
        </p>
        <p data-field="cost-face-total">
          券面额合计(营销面值,不是支出):{formatMinor(data.cost.reward_face_minor)}
        </p>
        {data.cost.content_generation_available && data.cost.content_generation_amount_minor != null ? (
          <p data-field="cost-content">
            内容实际生成费用:{formatMinor(data.cost.content_generation_amount_minor)}
            ({data.cost.content_generation_runs} 次已结算)
          </p>
        ) : (
          <p className="tk-unknown" data-field="cost-content-unknown">
            内容实际生成费用:未录入{data.cost.content_generation_note ? `。${data.cost.content_generation_note}` : ""}
          </p>
        )}
        {data.cost.unrecorded_batches > 0 ? (
          <p className="tk-note" data-field="cost-unrecorded">
            未录成本的批次:{data.cost.unrecorded_batches} 个,按未录入展示,不冒充 0。
          </p>
        ) : null}
        <p className="tk-note" data-field="cost-face">{data.cost.face_note}</p>
      </section>

      <section aria-label="异常待处理" data-section="anomalies">
        <h2 className="tk-subtitle">异常待处理</h2>
        <ul className="tk-list" data-field="anomaly-list">
          <li data-anomaly="pending_review">待人工确认(顾客自报):{data.anomalies.pending_review}</li>
          <li data-anomaly="reservation_failed">预留失败:{data.anomalies.reservation_failed}</li>
          <li data-anomaly="reversed_in_window">窗口内撤销:{data.anomalies.reversed_in_window}</li>
          <li data-anomaly="expired_in_window">窗口内过期:{data.anomalies.expired_in_window}</li>
        </ul>
      </section>

      <p className="tk-note" data-field="note-entry">{data.notes.entry_marker}</p>
      <p className="tk-note" data-field="note-visitor">{data.notes.visitor}</p>
      <p className="tk-gap">
        <a href="/work/service">去店员台确认与核销</a>
        <a href="/work/rewards">去看奖励与核销记录</a>
      </p>
    </main>
  );
}
