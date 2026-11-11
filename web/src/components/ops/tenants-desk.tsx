"use client";

// HUI-4592 O1 商户与开通:搜索/筛选、平台层事实、受控暂停/恢复、收费保持、
// 受控支持访问(限时+原因+审计)。
//
// 纪律:
//   - 深层数据(门店/成员清单)只在活跃支持访问内可见;无授权时显示锁定态
//     与聚合计数,不泄露租户明细;
//   - 生命周期/收费保持动作带 expected_revision(读面下发 ops_revision),
//     stale → 服务端 409 → 页面提示重载;
//   - 不伪造付费开通:服务期只复述 4591 订阅投影事实。

import { useCallback, useEffect, useState } from "react";
import { useMerchantSession, type MerchantSession } from "@/components/work/merchant-session";
import {
  OperationFeedback,
  OpsReasonForm,
  oneIdempotencyKey,
  postOperation,
  type EnvelopeOutcome,
} from "@/components/ops/ops-shell";
import { lifecycleText, type OpsSupportAccess, type OpsTenantDetail, type OpsTenantRow } from "@/lib/ops-console";

type Phase = "loading" | "error" | "ready";

export function TenantsDesk() {
  const session = useMerchantSession();
  const [phase, setPhase] = useState<Phase>("loading");
  const [error, setError] = useState("");
  const [query, setQuery] = useState("");
  const [lifecycle, setLifecycle] = useState("");
  const [rows, setRows] = useState<OpsTenantRow[]>([]);
  const [detail, setDetail] = useState<OpsTenantDetail | null>(null);
  const [accessList, setAccessList] = useState<OpsSupportAccess[]>([]);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState("");
  const [outcome, setOutcome] = useState<EnvelopeOutcome | null>(null);
  const [opError, setOpError] = useState("");
  const [retryKey, setRetryKey] = useState("");

  const load = useCallback(
    async (ses: MerchantSession, q: string, lc: string) => {
      setPhase("loading");
      setError("");
      const params = new URLSearchParams();
      if (q) params.set("q", q);
      if (lc) params.set("lifecycle", lc);
      const suffix = params.toString() ? `?${params.toString()}` : "";
      const [listRes, accRes] = await Promise.all([
        ses.api("GET", `ops/tenants${suffix}`),
        ses.api("GET", "ops/support-access"),
      ]);
      if (!listRes.ok) {
        setError(`商户列表读取失败(${listRes.status})`);
        setPhase("error");
        return;
      }
      setRows(Array.isArray(listRes.data.tenants) ? (listRes.data.tenants as OpsTenantRow[]) : []);
      if (accRes.ok && Array.isArray(accRes.data.support_access)) {
        setAccessList(accRes.data.support_access as OpsSupportAccess[]);
      }
      setPhase("ready");
    },
    [],
  );

  useEffect(() => {
    void load(session, query, lifecycle);
  }, [load, session, query, lifecycle]);

  const openDetail = useCallback(
    async (ses: MerchantSession, id: string) => {
      setNote("");
      const res = await ses.api("GET", `ops/tenants/${id}`);
      if (!res.ok) {
        setNote(`商户详情读取失败(${res.status})`);
        return;
      }
      setDetail(res.data as unknown as OpsTenantDetail);
    },
    [],
  );

  const runOp = useCallback(
    async (ses: MerchantSession, body: Record<string, unknown>, key: string) => {
      setBusy(true);
      setOpError("");
      const res = await postOperation(ses, { ...body, idempotency_key: key });
      setBusy(false);
      if (!res.ok || !res.outcome) {
        // 幂等键按意图生成;失败时保留键供「按同一意图重试」。
        setRetryKey(key);
        setOpError(res.message);
        return;
      }
      setOutcome(res.outcome);
      if (typeof body.tenant_id === "string" && body.tenant_id) {
        await openDetail(ses, body.tenant_id as string);
      }
      await load(ses, query, lifecycle);
    },
    [load, openDetail, query, lifecycle],
  );

  return (
    <section className="tk-section" data-view="ops-tenants">
      <h2 className="tk-section-title">商户与开通</h2>
      <p className="tk-note">
        深层数据(门店/成员/明细)需要限时支持访问;平台层事实任何持 ops:tenants.read
        的运营可读。生命周期操作走操作信封,前后值进审计。
      </p>
      <form
        className="tk-form"
        data-section="tenant-filter"
        onSubmit={(e) => {
          e.preventDefault();
          void load(session, query, lifecycle);
        }}
      >
        <input
          className="tk-input"
          data-field="tenant-query"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="按名称或 ID 搜索商户"
          aria-label="搜索商户"
        />
        <select
          className="tk-select"
          data-field="tenant-lifecycle"
          value={lifecycle}
          onChange={(e) => setLifecycle(e.target.value)}
          aria-label="按生命周期筛选"
        >
          <option value="">全部状态</option>
          <option value="active">正常</option>
          <option value="suspended">已暂停</option>
          <option value="security_freeze">安全冻结</option>
          <option value="offboarding">退出中</option>
          <option value="retired">已退役</option>
        </select>
        <button className="tk-button" type="submit" data-action="search-tenants">搜索</button>
      </form>

      {phase === "loading" ? <p className="tk-state" data-state="loading">正在读取商户…</p> : null}
      {phase === "error" ? <p className="tk-state tk-danger" data-state="error">{error}</p> : null}
      {phase === "ready" && rows.length === 0 ? (
        <p className="tk-state" data-state="empty">没有符合条件的商户。</p>
      ) : null}

      <ul className="tk-list" data-section="tenant-list">
        {rows.map((row) => (
          <li key={row.id} className="tk-row" data-tenant-id={row.id} data-lifecycle={row.lifecycle || "unknown"}>
            <button
              className="tk-button"
              data-action="open-tenant"
              onClick={() => void openDetail(session, row.id)}
            >
              {row.name}
            </button>
            <span data-field="lifecycle">{lifecycleText(row.lifecycle)}</span>
            {row.charge_hold ? <span className="tk-warn" data-field="charge-hold">收费保持</span> : null}
            <span className="tk-quiet" data-field="stores">
              门店 {row.store_count} · 成员 {row.member_count}
            </span>
            <span className="tk-quiet" data-field="service-period">
              {row.subscription_status
                ? `服务期至 ${row.paid_until || "未知"}`
                : "无订阅投影(未开通/未知)"}
            </span>
          </li>
        ))}
      </ul>

      {detail ? (
        <div className="tk-section" data-section="tenant-detail" data-detail-tenant={detail.tenant.id}>
          <h3 className="tk-section-title">{detail.tenant.name} 详情</h3>
          <p data-field="platform-facts">
            生命周期:{lifecycleText(detail.tenant.lifecycle)} · 收费保持:
            {detail.tenant.charge_hold ? "是" : "否"} · 版本号 {detail.ops_revision} · 品牌
            {detail.tenant.brand_id || "未绑定"}
          </p>
          <p data-field="subscription-facts">
            {detail.subscription.available
              ? `订阅投影:${detail.subscription.status} · 生效判定 ${detail.subscription.effective_status} · 服务期至 ${detail.subscription.paid_until}`
              : "订阅投影:无投影行(未开通,不代表已付费或已扣款)"}
          </p>
          <div data-section="business-data" data-state={detail.business_data.state}>
            {detail.business_data.state === "open" ? (
              <>
                <p className="tk-note">
                  支持访问生效(引用 {detail.business_data.support_access_ref})。以下明细按授权作用域读取,全程审计。
                </p>
                <ul className="tk-list" data-section="business-stores">
                  {(detail.business_data.stores ?? []).map((s) => (
                    <li key={s.id}>{s.name}</li>
                  ))}
                </ul>
                <ul className="tk-list" data-section="business-members">
                  {(detail.business_data.members ?? []).map((m) => (
                    <li key={m.id}>
                      {m.display_name}({m.role}
                      {m.enabled ? "" : ",已停用"})
                    </li>
                  ))}
                </ul>
              </>
            ) : (
              <p className="tk-quiet" data-field="locked-note">{detail.business_data.message}</p>
            )}
          </div>

          <OpsReasonForm
            label={detail.tenant.lifecycle === "suspended" ? "恢复商户" : "暂停商户"}
            busy={busy}
            onSubmit={(reason) => {
              const action =
                detail.tenant.lifecycle === "suspended" ? "tenant.resume" : "tenant.suspend";
              void runOp(
                session,
                {
                  action,
                  resource_ref: detail.tenant.id,
                  tenant_id: detail.tenant.id,
                  expected_revision: String(detail.ops_revision),
                  reason,
                },
                retryKey || oneIdempotencyKey(),
              );
              setRetryKey("");
            }}
          />
          <OpsReasonForm
            label={detail.tenant.charge_hold ? "解除收费保持" : "设置收费保持"}
            busy={busy}
            onSubmit={(reason) => {
              const action = detail.tenant.charge_hold
                ? "tenant.charge_hold.clear"
                : "tenant.charge_hold.set";
              void runOp(
                session,
                {
                  action,
                  resource_ref: detail.tenant.id,
                  tenant_id: detail.tenant.id,
                  expected_revision: String(detail.ops_revision),
                  reason,
                },
                retryKey || oneIdempotencyKey(),
              );
              setRetryKey("");
            }}
          />
        </div>
      ) : null}

      <div className="tk-section" data-section="support-access">
        <h3 className="tk-section-title">受控支持访问</h3>
        <p className="tk-note">
          限时(最长 4 小时)、指定目标商户、原因必填;到期或关闭后立即失效,全程审计。
          {detail ? " 目标商户来自上方详情,避免自由输入跨户目标。" : " 先在上方打开商户详情以确定目标。"}
        </p>
        <SupportAccessForm session={session} tenantId={detail ? detail.tenant.id : ""} busy={busy}
          onDone={() => {
            void load(session, query, lifecycle);
            if (detail) void openDetail(session, detail.tenant.id);
          }}
          onError={(m) => setOpError(m)}
        />
        <ul className="tk-list" data-section="support-access-list">
          {accessList.map((a) => (
            <li key={a.id} data-support-id={a.id} data-closed={a.closed_at ? "true" : "false"}>
              <span data-field="support-target">{a.tenant_id}</span>
              <span className="tk-quiet">{a.reason}</span>
              <span className="tk-quiet">至 {a.expires_at}</span>
              {a.closed_at ? <span className="tk-quiet">已关闭:{a.close_reason}</span> : null}
            </li>
          ))}
        </ul>
      </div>

      {note ? <p className="tk-state tk-warn">{note}</p> : null}
      <OperationFeedback outcome={outcome} error={opError} />
    </section>
  );
}

// SupportAccessForm:打开/关闭支持访问(独立端点,带审计)。
function SupportAccessForm({
  session,
  tenantId,
  busy,
  onDone,
  onError,
}: {
  session: MerchantSession;
  tenantId: string;
  busy: boolean;
  onDone: () => void;
  onError: (message: string) => void;
}) {
  const [ttl, setTtl] = useState("60");
  const [reason, setReason] = useState("");
  const [working, setWorking] = useState(false);

  const open = useCallback(async () => {
    if (!tenantId || !reason.trim()) {
      onError("申请支持访问需要目标商户与业务原因。");
      return;
    }
    setWorking(true);
    const res = await session.api("POST", "ops/support-access", {
      tenant_id: tenantId,
      reason: reason.trim(),
      ttl_minutes: Number(ttl) || 60,
    });
    setWorking(false);
    if (!res.ok) {
      onError(typeof res.data.message === "string" ? res.data.message : "支持访问申请被拒绝");
      return;
    }
    setReason("");
    onDone();
  }, [session, tenantId, reason, ttl, onDone, onError]);

  const closeLast = useCallback(async () => {
    setWorking(true);
    // 关闭当前主体对该目标最近的活跃授权。
    const list = await session.api("GET", "ops/support-access");
    const items = Array.isArray(list.data.support_access) ? (list.data.support_access as OpsSupportAccess[]) : [];
    const active = items.find((a) => a.tenant_id === tenantId && !a.closed_at);
    if (!active) {
      setWorking(false);
      onError("没有可关闭的活跃支持访问。");
      return;
    }
    const res = await session.api("POST", `ops/support-access/${active.id}/close`, {
      reason: "支持访问结束(运营台操作)",
    });
    setWorking(false);
    if (!res.ok) {
      onError(typeof res.data.message === "string" ? res.data.message : "关闭失败");
      return;
    }
    onDone();
  }, [session, tenantId, onDone, onError]);

  return (
    <form
      className="tk-form"
      data-section="support-access-form"
      onSubmit={(e) => {
        e.preventDefault();
        void open();
      }}
    >
      <label className="tk-label" htmlFor="support-ttl">时长(分钟,1-240)</label>
      <input
        id="support-ttl"
        className="tk-input"
        data-field="support-ttl"
        value={ttl}
        onChange={(e) => setTtl(e.target.value)}
        inputMode="numeric"
      />
      <label className="tk-label" htmlFor="support-reason">支持原因(必填)</label>
      <input
        id="support-reason"
        className="tk-input"
        data-field="support-reason"
        value={reason}
        onChange={(e) => setReason(e.target.value)}
        placeholder="如:处理商户工单 tkt_9"
      />
      <div className="tk-row tk-gap">
        <button className="tk-button" type="submit" disabled={working || busy || !tenantId} data-action="open-support-access">
          打开支持访问
        </button>
        <button className="tk-button" type="button" disabled={working || busy || !tenantId} data-action="close-support-access" onClick={() => void closeLast()}>
          结束支持访问
        </button>
      </div>
    </form>
  );
}
