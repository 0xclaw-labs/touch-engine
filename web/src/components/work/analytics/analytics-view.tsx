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

import React, { useEffect, useState } from "react";
import { useSession, type MerchantSession } from "@/components/work/merchant-session";
import {
  ANALYTICS_NOT_ENABLED_COPY,
  ENVIRONMENT_OPTIONS,
  FILTER_LABELS,
  LAYER_TITLES,
  buildAnalyticsQuery,
  countDisplay,
  readAnalytics,
  type AnalyticsRead,
  emptyWindowCopy,
  evidenceRowList,
  formatMinor,
  leadsPanelRows,
  localDayWindow,
  metricDisplay,
  stageNotAppliedLabels,
  type AnalyticsFilters,
  type CatalogMetric,
  type OperationsResponse,
  type EvidenceLayer,
} from "@/lib/analytics";

const TOUCHPOINT_OPTIONS = ["nfc", "qr", "shorturl", "manual"] as const;
const HANDOFF_METHOD_OPTIONS = ["h5", "platform_miniprogram", "staff_view"] as const;

const EMPTY_FILTERS: AnalyticsFilters = {};

export function AnalyticsView() {
  const session = useSession();
  // 切组织或角色时卸载旧读面，连未应用筛选和迟到响应都不能跨范围复用。
  return <ScopedAnalyticsView key={`${session.tenantId}:${session.role}`} session={session} />;
}

function ScopedAnalyticsView({ session }: { session: MerchantSession }) {
  const [attempt, setAttempt] = useState(0);
  const [snapshot, setSnapshot] = useState<{ key: string; result: AnalyticsRead } | null>(null);
  const [draft, setDraft] = useState<AnalyticsFilters>(EMPTY_FILTERS);
  const [applied, setApplied] = useState<AnalyticsFilters>(EMPTY_FILTERS);

  const requestKey = JSON.stringify([applied, attempt]);
  useEffect(() => {
    let alive = true;
    setSnapshot(null);
    const query = buildAnalyticsQuery(localDayWindow(new Date()), applied);
    void readAnalytics(session.api, session.tenantId, query).then((result) => {
      if (alive) setSnapshot({ key: requestKey, result });
    });
    return () => { alive = false; };
  }, [session.api, session.tenantId, applied, requestKey]);

  useEffect(() => {
    const refresh = () => setAttempt(value => value + 1);
    window.addEventListener("focus", refresh);
    return () => window.removeEventListener("focus", refresh);
  }, []);

  // 筛选变化的第一次 render 即隐藏旧结果；不等 effect 执行才清理。
  const result = snapshot?.key === requestKey ? snapshot.result : null;
  if (!result) {
    return <main><h1 className="tk-title">经营看板</h1><p className="tk-state" data-state="loading">正在读取当前范围…</p></main>;
  }
  if (result.phase !== "ready") {
    return (
      <main>
        <h1 className="tk-title">经营看板</h1>
        {result.phase === "error" ? (
          <p className="tk-state tk-danger" data-state="error">{result.message}</p>
        ) : (
          <p className="tk-state tk-unknown" data-state={result.phase}>
            {result.phase === "disabled" ? ANALYTICS_NOT_ENABLED_COPY : result.message}
          </p>
        )}
        <button className="tk-quiet" type="button" data-action="retry" onClick={() => setAttempt(value => value + 1)}>重试</button>
			<p><a href="/">返回工作台</a></p>
      </main>
    );
  }
  const { data, catalog, catalogNote } = result;

  return <AnalyticsReport data={data} catalog={catalog} catalogNote={catalogNote} draft={draft}
    onDraft={setDraft} onApply={() => setApplied({ ...draft })}
    onReset={() => { setDraft(EMPTY_FILTERS); setApplied(EMPTY_FILTERS); }}
    onRefresh={() => setAttempt(value => value + 1)} />;
}

export function AnalyticsReport({ data, catalog, catalogNote, draft, onDraft, onApply, onReset, onRefresh }: {
  data: OperationsResponse;
  catalog: CatalogMetric[] | null;
  catalogNote: string;
  draft: AnalyticsFilters;
  onDraft: (value: AnalyticsFilters) => void;
  onApply: () => void;
  onReset: () => void;
  onRefresh: () => void;
}) {
  const windowAllZero = data.funnel.length > 0 && data.funnel.every((stage) => stage.available && stage.value === 0);
  const campaigns = data.dimensions.campaigns ?? [];
  const stores = data.dimensions.stores ?? [];
  const actions = data.dimensions.actions ?? [];
  const layerByKey = new Map<string, EvidenceLayer>(
    (data.evidence_layers.layers ?? []).map((l) => [l.key, l]),
  );

  const serviceCount = (layerKey: string, countKey: string) => {
    const layer = layerByKey.get(layerKey);
    return countDisplay(layer?.available ? layer.counts.find(row => row.key === countKey)?.value : undefined);
  };
  const filterLimit = (supported: string[], field: string) => {
    const labels = stageNotAppliedLabels({ applied_filters: supported }, data.filter_echo);
    return labels.length > 0 ? <p className="tk-note tk-danger" data-field={field}>本区块未生效：{labels.join("、")}。此项事实没有这些维度，不能与已收窄项作转化比较。</p> : null;
  };
  const setField = (field: keyof AnalyticsFilters) => (value: string) =>
    onDraft({ ...draft, [field]: value });

  return (
    <main>
      <h1 className="tk-title">经营看板</h1>
      <p className="tk-lead">按办理结果分别看门店服务、内容交接、权益、授权线索与费用。各项不相加为“成功总数”。</p>
      <p><button className="tk-quiet" type="button" data-action="refresh" onClick={onRefresh}>刷新统计</button> <a href="/">返回工作台</a></p>
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
        <label className="tk-label">
          动作
          <select
            className="tk-select"
            data-filter="action"
            value={draft.actionId ?? ""}
            onChange={(event) => setField("actionId")(event.target.value)}
          >
            <option value="">全部动作</option>
            {actions.map((item) => (
              <option key={item.id} value={item.id}>
                {item.label || item.id}
                {item.enabled ? "" : "(已停用)"}
              </option>
            ))}
          </select>
        </label>
        <label className="tk-label">
          环境
          <select
            className="tk-select"
            data-filter="environment"
            value={draft.environment ?? ""}
            onChange={(event) => setField("environment")(event.target.value)}
          >
            <option value="">全部环境</option>
            {ENVIRONMENT_OPTIONS.map((item) => (
              <option key={item} value={item}>{item}</option>
            ))}
          </select>
        </label>
        <button
          className="tk-button"
          type="button"
          data-action="apply-filters"
          onClick={onApply}
        >
          应用筛选
        </button>
        <button
          className="tk-quiet"
          type="button"
          data-action="reset-filters"
          onClick={onReset}
        >
          重置
        </button>
      </section>

      <section aria-label="门店服务" data-section="store-service">
        <h2 className="tk-subtitle">门店服务记录</h2>
        {filterLimit(["store_id", "action_id", "environment"], "service-filter-limit")}
        <p>服务卡片打开：<strong data-field="service-opens">{serviceCount("action_clicked", "service_opens")}</strong></p>
        <p>服务跳转或唤起尝试：<strong data-field="service-jumps">{serviceCount("invoke_attempt", "service_jump_attempts")}</strong></p>
        <p className="tk-note">仅表示本地已记录的打开或尝试，不证明服务办理完成、到店或成交。来源、去重与筛选适用范围见下方对应证据层口径。</p>
        {stores.length === 0 ? <p><a href="/work/stores">查看或建立门店</a>，门店可独立运营，无需先建活动。</p> : null}
      </section>

      <section aria-label="漏斗" data-section="funnel">
        <h2 className="tk-subtitle">活动参与与权益事实（七项独立口径）</h2>
        {data.funnel.length === 0 ? (
          <p className="tk-note">没有读到漏斗事实。缺失保持未知,不显示 0。</p>
        ) : (
          <ol className="tk-list" data-field="funnel-list">
            {data.funnel.map((stage) => {
              const notApplied = stageNotAppliedLabels(stage, data.filter_echo);
              return (
                <li key={stage.key} data-stage={stage.key}>
                  <span data-field="stage-title">{stage.title}</span>
                  <strong data-field="stage-value">{stage.available ? countDisplay(stage.value) : "未知"}</strong>
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
        <h2 className="tk-subtitle">内容发布核验证据</h2>
        {filterLimit(["campaign_id", "store_id", "touchpoint"], "evidence-filter-limit")}
        <ul className="tk-list" data-field="evidence-list">
          {evidenceRowList(data.evidence).map((row) => (
            <li key={row.key} data-evidence={row.key}>
              {row.label}:<strong data-field="evidence-count">{row.count}</strong>
            </li>
          ))}
        </ul>
        <p>
          总体覆盖(distinct 参与数):
          <strong data-field="evidence-distinct">{countDisplay(data.evidence.distinct_participations)}</strong>
        </p>
        {filterLimit(["campaign_id", "store_id", "platform"], "official-filter-limit")}
        <p>
          本地已持久化的官方确认记录(按可信尝试):
          <strong data-field="evidence-official">{countDisplay(data.evidence.official_attempts)}</strong>
        </p>
        <details className="tk-note" data-field="evidence-definition"><summary>来源与口径</summary><p>三类记录来自 reward_benefits 的核验证据类别，按权益账本行分别计数；总体覆盖按 distinct participation_id 去重，同参与多份证据不重复算覆盖。官方确认尝试来自 reward_official_facts，按可信 attempt_id 去重，各项不可相加。权益证据以 issued_at、官方事实以 created_at 在上方窗口落窗。</p></details>
        <p className="tk-note" data-field="evidence-note">{data.evidence.note}</p>
      </section>

      <section aria-label="证据六层" data-section="evidence_layers">
        <h2 className="tk-subtitle">证据六层(逐层分列)</h2>
        {(data.evidence_layers.layers ?? []).length === 0 ? (
          <p className="tk-note" data-field="layers-empty">没有读到分层事实。缺失保持未知,不显示 0。</p>
        ) : (
          <ol className="tk-list" data-field="layers-list">
            {LAYER_TITLES.map(({ key, label }) => {
              const layer = layerByKey.get(key);
              if (!layer || !layer.available) {
                return (
                  <li key={key} data-layer={key}>
                    <span data-field="layer-title">{label}</span>
                    <strong className="tk-unknown" data-field="layer-unknown">未知</strong>
                  </li>
                );
              }
              return (
                <li key={key} data-layer={key}>
                  <span data-field="layer-title">{layer.title || label}</span>
                  <ul className="tk-list" data-field="layer-counts">
                    {layer.counts.map((c) => (
                      <li key={c.key} data-line={c.key}>
                        {c.label}:<strong data-field="line-value">{countDisplay(c.value)}</strong>
                        {c.applied_filters && c.applied_filters.length > 0 ? <p className="tk-note" data-field="line-applied">本项按 {c.applied_filters.map(key => FILTER_LABELS[key] ?? key).join("、")} 收窄</p> : null}
                        {stageNotAppliedLabels({ applied_filters: c.applied_filters ?? layer.applied_filters }, data.filter_echo).length > 0 ? <p className="tk-note tk-danger" data-field="line-not-applied">本项未生效：{stageNotAppliedLabels({ applied_filters: c.applied_filters ?? layer.applied_filters }, data.filter_echo).join("、")}。该来源不能证明此维度。</p> : null}
                      </li>
                    ))}
                  </ul>
                  {stageNotAppliedLabels(layer, data.filter_echo).length > 0 ? (
                    <p className="tk-note tk-danger" data-field="layer-not-applied">本层并非所有计数均按 {stageNotAppliedLabels(layer, data.filter_echo).join("、")} 收窄，适用范围见各项说明，不能与已收窄项作转化比较。</p>
                  ) : null}
                  {layer.note ? <span className="tk-note" data-field="layer-note">{layer.note}</span> : null}
                  <details className="tk-note" data-field="layer-definition">
                    <summary>口径</summary>
                    <p>来源:{layer.definition.source}</p>
                    <p>去重:{layer.definition.dedup_key}</p>
                    <p>分母:{layer.definition.denominator}</p>
                    <p>事件时间:{layer.definition.event_time}</p>
                    <p>窗口:{layer.definition.window}</p>
                  </details>
                </li>
              );
            })}
          </ol>
        )}
        <p className="tk-note" data-field="platform-availability">平台证实只展示本地已经接收的可信回执。0 条回执不证明平台已授权或实际零发布；未授权、未同步的外部结果不可计。</p>
        <p className="tk-note" data-field="layers-note">{data.evidence_layers.note}</p>
      </section>

      <section aria-label="真实线索口径" data-section="leads">
        <h2 className="tk-subtitle">真实线索(接收状态口径)</h2>
        {filterLimit(["campaign_id", "store_id"], "leads-filter-limit")}
        <ul className="tk-list" data-field="leads-list">
          {leadsPanelRows(data.leads).map((row) => (
            <li key={row.key} data-lead={row.key}>
              {row.label}:<strong data-field="lead-value">{row.value}</strong>
            </li>
          ))}
        </ul>
        <p className="tk-note" data-field="leads-note">{data.leads.note}</p>
        <details className="tk-note" data-field="leads-definition">
          <summary>口径</summary>
          <p>来源:{data.leads.definition.source}</p>
          <p>去重:{data.leads.definition.dedup_key}</p>
          <p>分母:{data.leads.definition.denominator}</p>
          <p>事件时间:{data.leads.definition.event_time}</p>
          <p>窗口:{data.leads.definition.window}</p>
        </details>
      </section>

      <section aria-label="奖励库存" data-section="inventory">
        <h2 className="tk-subtitle">奖励库存(账本口径)</h2>
        {filterLimit(["campaign_id", "store_id"], "inventory-filter-limit")}
        {data.inventory.length === 0 ? (
          <p className="tk-note" data-field="inventory-empty">统计范围内没有库存批次。该范围批次记录为 0，不代表其他未接入事实为 0。</p>
        ) : (
          <ul className="tk-list" data-field="inventory-list">
            {data.inventory.map((line) => (
              <li key={line.inventory_id} data-inventory={line.inventory_id}>
                <span data-field="inventory-name">{line.name} · {line.benefit_slot} · 规则 v{countDisplay(line.rule_version)}</span>
                <span data-field="inventory-counts">
                  可用 {countDisplay(line.available_unknown ? undefined : line.available)} · 预留 {countDisplay(line.reserved)} · 已发 {countDisplay(line.issued)} · 已核销 {countDisplay(line.redeemed)}
                  {(line.reversed ?? 0) > 0 ? ` · 撤销 ${line.reversed}` : ""}
                  {(line.expired ?? 0) > 0 ? ` · 过期 ${line.expired}` : ""}
                </span>
                {line.available_unknown ? <p className="tk-note" data-field="inventory-available-unknown">当前门店范围无法证明全批次占用，可用量未知；以上领取和核销仅统计本店事实，办理时以权益账本裁决为准。</p> : null}
                <span className="tk-note" data-field="inventory-face">
                  面额 {formatMinor(line.face_value_minor)}{line.currency ? ` ${line.currency}` : ""}
                  {line.store_id ? "" : " · 批次未显式绑定门店，按活动归属核定可见范围"}
                </span>
              </li>
            ))}
          </ul>
        )}
        <details className="tk-note" data-field="inventory-definition"><summary>来源与口径</summary><p>来源 reward_inventory 与 reward_benefits，按 inventory_id 去重；批次及占用为当前快照，不是上方窗口内新增。总部全量可用数量 = 总量 − 预留 − 已发 − 已核销。门店视图仅计本店权益，无法证明全批次占用时可用量保持未知。空门店字段不代表共享授权，批次须结合活动归属确定可见范围。</p></details>
      </section>

      <section aria-label="成本" data-section="cost">
        <h2 className="tk-subtitle">权益成本、内容费用与券面额</h2>
        {filterLimit(["campaign_id", "store_id", "touchpoint"], "reward-cost-filter-limit")}
        <p data-field="cost-reward">
          奖励实际兑现成本(已发):{data.cost.reward_cost_unrecorded === 0 ? formatMinor(data.cost.reward_cost_minor) : "未知（本期已发权益包含未录成本，或完整性尚未读到）"};其中已核销:
          {data.cost.redeemed_cost_unrecorded === 0 ? formatMinor(data.cost.reward_redeemed_cost_minor) : "未知（本期已核销权益包含未录成本，或完整性尚未读到）"}
        </p>
        <p data-field="cost-face-total">
          券面额合计(营销面值,不是支出):{formatMinor(data.cost.reward_face_minor)}
        </p>
        <p className="tk-note">内容生成费用按活动当前归属门店筛选，未归属门店的费用不作分摊，不代表门店完整总费用；其他上方筛选未生效。未录成本批次按当前批次快照披露。</p>
        {data.cost.content_generation_available && data.cost.content_generation_amount_minor != null ? (
          <p data-field="cost-content">
            内容实际生成费用:{formatMinor(data.cost.content_generation_amount_minor)}
            ({countDisplay(data.cost.content_generation_runs)} 次已结算)
          </p>
        ) : (
          <p className="tk-unknown" data-field="cost-content-unknown">
            内容实际生成费用:未知{data.cost.content_generation_note ? `。${data.cost.content_generation_note}` : ""}
          </p>
        )}
        {(data.cost.unrecorded_batches ?? 0) > 0 ? (
          <p className="tk-note" data-field="cost-unrecorded">
            未录成本的批次:{data.cost.unrecorded_batches} 个,按未录入展示,不冒充 0。
          </p>
        ) : null}
        <details className="tk-note" data-field="cost-definition"><summary>来源与口径</summary>
          <p>权益金额来自 reward_benefits 的冻结面额、成本；以权益账本唯一参与与权益槽去重。已发金额按 issued_at、已核销成本按 redeemed_at 落在上方窗口，撤销及过期权益不算当前有效兑现。</p>
          <p>内容实际生成费用来自 copy_jobs 的已结算 trace.amount_minor，按生成任务与 created_at 落窗。任一匹配记录缺失有效结算金额时，合计保持未知；此项与 AI 用量投影可能有关联，不叠加计为总支出。</p>
        </details>
        <p className="tk-note" data-field="cost-face">{data.cost.face_note}</p>
      </section>

      <section aria-label="费用对账" data-section="cost_reconciliation">
        <h2 className="tk-subtitle">费用对账(订阅 / AI / 硬件,只读投影)</h2>
        {data.cost_reconciliation.availability === "forbidden" ? (
          <p className="tk-unknown" data-field="recon-forbidden">{data.cost_reconciliation.note || "当前账号没有账单查看权限。请联系组织管理员查看费用。"}</p>
        ) : <>
        <p className="tk-note" data-field="recon-scope">组织范围账单投影，不能按门店或活动分摊；上方业务筛选对此区块未生效。0 仅表示本地投影无相应记录，不证明外部账已同步齐全。</p>
        {data.cost_reconciliation.subscription_available && data.cost_reconciliation.subscription_paid_minor != null ? (
          <p data-field="recon-subscription">
            订阅实付(支付事实订单投影):{formatMinor(data.cost_reconciliation.subscription_paid_minor)}
            ({countDisplay(data.cost_reconciliation.subscription_paid_orders)} 单)
          </p>
        ) : (
          <p className="tk-unknown" data-field="recon-subscription-unknown">
            订阅实付:没有读到可用的支付金额，未接入、未同步或缺字段均保持未知。请到账单页核对。
          </p>
        )}
        {(data.cost_reconciliation.subscription_failed_orders ?? 0) > 0 ? (
          <p className="tk-note" data-field="recon-subscription-failed">
            失败订阅单:{data.cost_reconciliation.subscription_failed_orders} 单(失败不是支出,是待处理)。
          </p>
        ) : null}
        <p data-field="recon-ai">
          AI 实际费用:已消费 {formatMinor(data.cost_reconciliation.ai_consumed_minor)}
          ({countDisplay(data.cost_reconciliation.ai_consumed_count)} 笔)·已确认未消费{" "}
          {formatMinor(data.cost_reconciliation.ai_confirmed_minor)}
          ({countDisplay(data.cost_reconciliation.ai_confirmed_count)} 笔)
        </p>
        {(data.cost_reconciliation.ai_refunded_minor ?? 0) > 0 || (data.cost_reconciliation.ai_failed_minor ?? 0) > 0 ? (
          <p className="tk-note" data-field="recon-ai-exceptions">
            AI 退款 {formatMinor(data.cost_reconciliation.ai_refunded_minor)}({data.cost_reconciliation.ai_refunded_count} 笔)·
            失败 {formatMinor(data.cost_reconciliation.ai_failed_minor)}({data.cost_reconciliation.ai_failed_count} 笔)
            ——单列披露,不计入支出。
          </p>
        ) : null}
        {data.cost_reconciliation.hardware_available && data.cost_reconciliation.hardware_paid_minor != null ? (
          <p data-field="recon-hardware">
            硬件支出(支付事实订单投影):{formatMinor(data.cost_reconciliation.hardware_paid_minor)}
            ({countDisplay(data.cost_reconciliation.hardware_paid_orders)} 单)
          </p>
        ) : (
          <p className="tk-unknown" data-field="recon-hardware-unknown">
            硬件支出:没有读到可用的支付金额，未接入、未同步或缺字段均保持未知。请到账单页核对。
          </p>
        )}
        {(data.cost_reconciliation.hardware_refunds ?? 0) > 0 ? (
          <p className="tk-note" data-field="recon-hardware-refunds">
            硬件退款:{formatMinor(data.cost_reconciliation.hardware_refund_minor)}
            ({data.cost_reconciliation.hardware_refunds} 笔)——分列对账,不冲销原订单金额。
          </p>
        ) : null}
        <details className="tk-note" data-field="recon-definition">
          <summary>口径</summary>
          <p>来源:{data.cost_reconciliation.definition.source}</p>
          <p>去重:{data.cost_reconciliation.definition.dedup_key}</p>
          <p>分母:{data.cost_reconciliation.definition.denominator}</p>
          <p>事件时间:{data.cost_reconciliation.definition.event_time}</p>
          <p>窗口:{data.cost_reconciliation.definition.window}</p>
        </details>
        <p className="tk-note" data-field="recon-face">{data.cost_reconciliation.face_note}</p>
        </>}
      </section>

      <section aria-label="异常待处理" data-section="anomalies">
        <h2 className="tk-subtitle">异常待处理</h2>
        {filterLimit(["campaign_id", "store_id"], "anomalies-filter-limit")}
        <ul className="tk-list" data-field="anomaly-list">
          <li data-anomaly="pending_review">待人工确认(顾客自报):{countDisplay(data.anomalies.pending_review)}</li>
          <li data-anomaly="reservation_failed">预留失败:{countDisplay(data.anomalies.reservation_failed)}</li>
          <li data-anomaly="reversed_in_window">窗口内撤销:{countDisplay(data.anomalies.reversed_in_window)}</li>
          <li data-anomaly="expired_in_window">窗口内过期:{countDisplay(data.anomalies.expired_in_window)}</li>
        </ul>
      </section>

      <p className="tk-note" data-field="anomaly-definition">待人工确认来自 customer_publish_attempts，按尝试唯一记录计算当前待办；预留失败来自 participations 当前状态。撤销与过期来自 reward_operations 的唯一流水，按事件时间落在上方窗口。不以异常条数推算失败人数。</p>

      <section aria-label="授权留资与其他汇总指标" data-section="catalog">
        <h2 className="tk-subtitle">授权留资与其他汇总指标(单列)</h2>
        <p className="tk-note" data-field="catalog-note">
          授权留资与 CRM 接收单独计数;碰触或领券不自动算成获客联系人。
        </p>
        <p className="tk-note" data-field="catalog-scope">此目录按当前账号受信门店范围和同一时间窗口读取。上方活动、门店等筛选未应用到此目录，不可与已筛选的经营事实作转化比较。</p>
        {catalog === null ? (
          <p className="tk-unknown" data-field="catalog-unknown">{catalogNote || "汇总指标没有读到,保持未知,不显示 0。"}</p>
        ) : (
          <ul className="tk-list" data-field="catalog-list">
            {catalog.map((metric) => {
              const known = metric.available && typeof metric.value === "number";
              return (
                <li key={metric.key} data-metric={metric.key}>
                  {metric.title}:<strong data-field="metric-value">{metricDisplay(metric)}</strong>
                  {known && typeof metric.unique_contacts === "number" ? (
                    <span className="tk-note"> · 唯一联系人 {metric.unique_contacts}</span>
                  ) : null}
                  {metric.definition ? <details className="tk-note" data-field="catalog-definition"><summary>来源与口径</summary>
                    <p>来源：{metric.definition.source}</p><p>去重：{metric.definition.dedup_key}</p>
                    <p>分母：{metric.definition.denominator}</p><p>事件时间：{metric.definition.event_time}</p><p>窗口：{metric.definition.window}</p>
                  </details> : null}
                  {!known ? (
                    <span className="tk-note" data-field="metric-unknown">
                      未知{metric.reason ? `。${metric.reason}` : "。没有接入的事实来源"}
                    </span>
                  ) : null}
                </li>
              );
            })}
          </ul>
        )}
      </section>

      <section aria-label="经营效果边界" data-section="outcomes">
        <h2 className="tk-subtitle">成交与经营回报</h2>
        <p className="tk-unknown">真实成交收入、完整费用和因果归因证据尚未接入，当前不能计算完整 ROI 或宣称增长效果。没有一致分母，不计算转化率。</p>
        <p>费用为既有订单及用量来源的只读投影，可前往<a href="/work/billing">账单与用量</a>核对；回执与核销可前往原业务记录查看。</p>
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
