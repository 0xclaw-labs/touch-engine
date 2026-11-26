"use client";

import { useEffect, useState } from "react";
import { MerchantGate, useSession } from "@/components/work/merchant-session";
import { WorkFrame } from "@/components/work/work-frame";
import { serviceDeskEntryVisible, SERVICE_DESK_HREF, SERVICE_DESK_LABEL } from "@/lib/work/service-desk";
import {
  buildTodaySummaryView,
  todayObligations,
  type OperationsSummary,
} from "@/lib/work/today-obligations";
import {
  canViewDiagnostics,
  isCurrentTenantPayload,
  resolveStoreEntry,
  roleDisplayLabel,
  storeWorkspaceEntryHref,
  type StoreRef,
} from "@/lib/work/workbench-shell";
import { surfaceLabel, type TodayItem } from "@/lib/product-finish";
import { Card, CardContent } from "@/components/ui/card";

export function TodayDesk() {
  return (
    <MerchantGate>
      <TodayHost />
    </MerchantGate>
  );
}

function TodayHost() {
  const session = useSession();
  return <TodayBody key={session.tenantId} />;
}

interface OpsFetchState {
  loading: boolean;
  ok: boolean;
  status: number | null;
  data: OperationsSummary | null;
  errorCode: string;
}

interface StoresFetchState {
  loading: boolean;
  ok: boolean;
  status: number | null;
  items: StoreRef[];
}

function TodayBody() {
  const session = useSession();
  const desk = serviceDeskEntryVisible(session.features, session.role);
  const diagnosticsAllowed = canViewDiagnostics(session.role);
  const [opsState, setOpsState] = useState<OpsFetchState>({
    loading: true,
    ok: false,
    status: null,
    data: null,
    errorCode: "",
  });
  const [storesState, setStoresState] = useState<StoresFetchState>({
    loading: true,
    ok: false,
    status: null,
    items: [],
  });
  const [pickedStoreId, setPickedStoreId] = useState("");
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    const requestTenant = session.tenantId;
    let alive = true;
    setOpsState({ loading: true, ok: false, status: null, data: null, errorCode: "" });
    setStoresState({ loading: true, ok: false, status: null, items: [] });
    setPickedStoreId("");

    void Promise.allSettled([
      session.api("GET", "dashboard/operations-summary"),
      session.api("GET", "stores"),
    ]).then(([ops, stores]) => {
      if (!alive || !isCurrentTenantPayload(requestTenant, session.tenantId)) return;

      if (stores.status === "fulfilled" && stores.value.ok && Array.isArray(stores.value.data.items)) {
        const validStores = (stores.value.data.items as unknown[]).filter(
          (row): row is StoreRef =>
            typeof row === "object" &&
            row !== null &&
            typeof (row as StoreRef).id === "string" &&
            (row as StoreRef).id !== "",
        );
        setStoresState({ loading: false, ok: true, status: stores.value.status, items: validStores });
        if (validStores.length > 0) setPickedStoreId(validStores[0].id);
      } else {
        const status = stores.status === "fulfilled" ? stores.value.status : null;
        setStoresState({ loading: false, ok: false, status, items: [] });
      }

      if (ops.status === "fulfilled" && ops.value.ok) {
        setOpsState({
          loading: false,
          ok: true,
          status: ops.value.status,
          data: ops.value.data as OperationsSummary,
          errorCode: "",
        });
      } else {
        const status = ops.status === "fulfilled" ? ops.value.status : null;
        const errorCode =
          ops.status === "fulfilled" && typeof ops.value.data.error === "string" ? ops.value.data.error : "";
        setOpsState({ loading: false, ok: false, status, data: null, errorCode });
      }
    });

    return () => {
      alive = false;
    };
  }, [session, attempt]);

  const summaryView = buildTodaySummaryView({
    loading: opsState.loading,
    ok: opsState.ok,
    status: opsState.status,
    data: opsState.data,
    errorCode: opsState.errorCode,
    storeLoadFailed: !storesState.loading && !storesState.ok,
  });

  const items = todayObligations(opsState.ok ? opsState.data : null, {
    serviceDesk: desk,
    owner: session.role === "org_owner",
  });

  const storeEntry = storesState.ok ? resolveStoreEntry(storesState.items, { returnOrigin: "/" }) : null;
  const activePickedStore =
    storeEntry?.mode === "multi_store"
      ? storeEntry.stores.find((s) => s.id === pickedStoreId) ?? storeEntry.stores[0]
      : null;

  return (
    <WorkFrame
      tenantName={session.tenantName || "当前组织"}
      role={session.role}
      tenantId={session.tenantId}
      options={session.options}
      onSwitchTenant={session.switchTenant}
      serviceDesk={desk}
    >
      <main>
        <h1 className="tk-title">今天</h1>
        <p className="tk-lead">
          处理已经发生、需要你继续办理的顾客事项。无需先建活动，可直接进入门店工作区配置基础服务与顾客页。
        </p>

        <section className="tk-section" data-section="store-entry" aria-label="门店任务入口">
          <h2 className="tk-section-title">门店工作入口</h2>
          {storesState.loading ? (
            <p className="tk-state" data-store-entry-state="loading">
              正在读取门店入口…
            </p>
          ) : !storesState.ok ? (
            <p className="tk-warn" data-store-entry-state="error" role="alert">
              门店列表暂时没有读到。
              <button className="tk-quiet" type="button" onClick={() => setAttempt((x) => x + 1)}>
                重试门店列表
              </button>{" "}
              仍可<a href="/work/stores">打开门店管理</a>。
            </p>
          ) : storeEntry?.mode === "empty" ? (
            <Card data-store-entry-state="empty">
              <CardContent className="tk-gap">
                <p className="tk-note">还没有门店。先登记第一家门店即可配置基础服务与顾客页，无需先建推广活动。</p>
                <a className="tk-button" href="/work/stores">
                  登记第一家门店
                </a>
              </CardContent>
            </Card>
          ) : storeEntry?.mode === "auto_single" ? (
            <Card data-store-entry-state="auto_single">
              <CardContent className="tk-gap">
                <p className="tk-note">当前组织仅有一家门店，已为你直达本店：<strong>{storeEntry.store.name}</strong></p>
                <div className="tk-row tk-gap">
                  <a className="tk-button" href={storeEntry.href}>
                    {storeEntry.label}
                  </a>
                  <a className="tk-quiet" href={storeEntry.channelsHref}>
                    配置门店服务
                  </a>
                  <a className="tk-quiet" href={storeEntry.entrancesHref}>
                    管理桌卡与二维码
                  </a>
                  <a className="tk-quiet" href={storeEntry.contentHref}>
                    准备门店内容
                  </a>
                  {desk ? (
                    <a className="tk-quiet" href={`${SERVICE_DESK_HREF}?return=/`}>
                      {SERVICE_DESK_LABEL}
                    </a>
                  ) : null}
                </div>
              </CardContent>
            </Card>
          ) : storeEntry?.mode === "multi_store" && activePickedStore ? (
            <Card data-store-entry-state="multi_store">
              <CardContent className="tk-gap">
                <label className="tk-label">
                  选择当前处理的门店
                  <select
                    className="tk-select"
                    aria-label="选择门店进入工作区"
                    value={activePickedStore.id}
                    onChange={(event) => setPickedStoreId(event.target.value)}
                  >
                    {storeEntry.stores.map((store) => (
                      <option key={store.id} value={store.id}>
                        {store.name}
                      </option>
                    ))}
                  </select>
                </label>
                <div className="tk-row tk-gap">
                  <a
                    className="tk-button"
                    href={storeWorkspaceEntryHref(activePickedStore.id, { panel: "page", returnOrigin: "/" })}
                  >
                    进入「{activePickedStore.name}」工作区
                  </a>
                  <a className="tk-quiet" href={storeEntry.manageHref}>
                    管理全部门店
                  </a>
                  {desk ? (
                    <a className="tk-quiet" href={`${SERVICE_DESK_HREF}?return=/`}>
                      {SERVICE_DESK_LABEL}
                    </a>
                  ) : null}
                </div>
              </CardContent>
            </Card>
          ) : null}
        </section>

        <section className="tk-section" data-section="today-obligations" aria-label="需要办理">
          <h2 className="tk-section-title">需要办理</h2>
          {summaryView.state === "loading" ? (
            <p className="tk-state" data-state="loading" data-summary-state="loading">
              {surfaceLabel("loading")}
            </p>
          ) : null}

          {summaryView.state === "not_connected" ? (
            <p className="tk-note" data-summary-state="not_connected">
              {summaryView.headline}。{summaryView.detail} 仍可<a href="/work/stores">进入门店工作区</a>
              {desk ? (
                <>
                  或<a href={`${SERVICE_DESK_HREF}?return=/`}>{SERVICE_DESK_LABEL}</a>
                </>
              ) : null}
              。
            </p>
          ) : null}

          {summaryView.state === "partial_failure" && !opsState.ok ? (
            <p className="tk-warn" data-state="error" data-summary-state="partial_failure" role="alert">
              {summaryView.headline}，待处理数量和今日结果保持未知（不显示 0）。
              <button
                className="tk-quiet"
                type="button"
                data-action="retry"
                onClick={() => setAttempt((x) => x + 1)}
              >
                重试摘要
              </button>{" "}
              仍可<a href="/work/stores">进入门店工作区</a>
              {desk ? (
                <>
                  或<a href={`${SERVICE_DESK_HREF}?return=/`}>{SERVICE_DESK_LABEL}</a>
                </>
              ) : null}
              。
            </p>
          ) : null}

          {opsState.ok ? (
            <>
              {summaryView.state === "partial_failure" ? (
                <p className="tk-warn" data-summary-state="partial_failure" role="alert">
                  {summaryView.headline}。{summaryView.detail}
                  <button
                    className="tk-quiet"
                    type="button"
                    data-action="retry"
                    onClick={() => setAttempt((x) => x + 1)}
                  >
                    重试摘要
                  </button>
                </p>
              ) : null}
              {items.length === 0 ? (
                <p className="tk-state" data-state="empty">
                  当前没有已知的待处理事项。正常暂停、结束或过期的活动，以及未选择的服务，都不会列为异常待办。
                </p>
              ) : (
                <div className="tk-stack">
                  {items.map((item: TodayItem) => (
                    <Card key={item.id} data-obligation-id={item.id}>
                      <CardContent className="tk-gap">
                        {item.object ? <p className="tk-kicker">{item.object}</p> : null}
                        <strong>{item.title}</strong>
                        <p className="tk-note">
                          {item.status} · {item.impact}
                        </p>
                        <a className="tk-button" href={item.href}>
                          {item.action}
                        </a>
                      </CardContent>
                    </Card>
                  ))}
                </div>
              )}
            </>
          ) : null}
        </section>

        <Card className="tk-section" data-section="today-summary" data-summary-state={summaryView.state}>
          <CardContent className="tk-gap">
            <h2 className="tk-section-title">今天的实际结果</h2>
            {summaryView.state === "loading" ? (
              <p className="tk-note">{summaryView.detail}</p>
            ) : summaryView.state === "not_connected" ? (
              <p className="tk-note">{summaryView.detail}</p>
            ) : !opsState.ok ? (
              <p className="tk-note">{summaryView.detail}</p>
            ) : (
              <>
                <p className="tk-note">
                  {summaryView.metrics
                    .map((metric) =>
                      metric.state === "known" && metric.value !== null
                        ? `${metric.label} ${metric.value} ${metric.unit}`
                        : `${metric.label}暂未读到（不显示 0）`,
                    )
                    .join(" · ")}
                </p>
                <p className="tk-note">{summaryView.detail}</p>
              </>
            )}
            <a href="/work/analytics">查看经营结果与统计方式</a>
          </CardContent>
        </Card>

        {diagnosticsAllowed ? (
          <details className="tk-section" data-testid="authorized-diagnostics" data-section="authorized-diagnostics">
            <summary>受权诊断（排障用）</summary>
            <p className="tk-note">
              当前身份：{roleDisplayLabel(session.role) || "未确认"} · 经营摘要状态：{summaryView.state}
              {summaryView.httpStatus !== null ? `（接口状态 ${summaryView.httpStatus}）` : ""}
              {summaryView.asOf ? ` · 统计截止 ${summaryView.asOf}` : ""}
            </p>
            {summaryView.missingFields.length > 0 ? (
              <p className="tk-note">未返回项：{summaryView.missingFields.join("、")}</p>
            ) : null}
          </details>
        ) : null}
      </main>
    </WorkFrame>
  );
}
