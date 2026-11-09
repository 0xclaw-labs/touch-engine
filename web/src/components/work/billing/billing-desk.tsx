"use client";

// HUI-4591 M12 订购收费账单工作台(页签复用纪律:一页四签,不新建路由)。
//
// 纪律(裁定书):
//   - 套餐/门店额度/续费扩店/支付状态/AI 用量/发票退款六要素落页;
//   - 支付走生态收银台跳转(复用 eco-nav switch;未接入时诚实说明,不自造
//     跳转地址),订单状态用「回查」按钮只读刷新 —— 浏览器返回永不当支付证明;
//   - 订阅/订单/退款是公共订单权威的投影:文案只复述事实,不自称已扣款;
//   - 下单按钮只对持有 billing:manage_payer 的成员显示(服务端再拦一道);
//   - 订阅到期:新增收费能力受限文案 + 旧权益不受锁说明(F18/T16)。

import { useCallback, useEffect, useState } from "react";
import { failureText, useSession } from "@/components/work/merchant-session";
import {
  AIUsageView,
  BillingOrderView,
  CheckoutRef,
  HARDWARE_STEPS,
  HardwareFulfillmentView,
  HardwareItemView,
  RefundView,
  SubscriptionView,
  aiUsageStateText,
  canManageBilling,
  formatAmount,
  invoiceStatusText,
  orderActionText,
  orderStatusText,
  planCodeText,
  stepText,
  storeQuotaText,
  subscriptionStatusText,
} from "@/lib/billing";

type LoadPhase = "loading" | "error" | "ready";
type BillingTab = "plan" | "orders" | "ai" | "refunds";

interface CatalogResolved {
  [plan: string]: {
    price_minor: number;
    currency: string;
    service_days: number;
    included_store_count: number;
    extra_store_price_minor: number;
    pricing_version: string;
    grace_days: number;
  };
}

interface OrderDetail {
  order: BillingOrderView;
  items?: HardwareItemView[];
  fulfillment?: HardwareFulfillmentView;
}

const TABS: { key: BillingTab; label: string }[] = [
  { key: "plan", label: "套餐与门店额度" },
  { key: "orders", label: "订单与支付" },
  { key: "ai", label: "AI 用量" },
  { key: "refunds", label: "发票与退款" },
];

export function BillingDesk() {
  const session = useSession();
  const [phase, setPhase] = useState<LoadPhase>("loading");
  const [error, setError] = useState("");
  const [tab, setTab] = useState<BillingTab>("plan");
  const [sub, setSub] = useState<SubscriptionView | null>(null);
  const [effective, setEffective] = useState<CatalogResolved>({});
  const [orders, setOrders] = useState<BillingOrderView[]>([]);
  const [aiUsage, setAiUsage] = useState<AIUsageView[]>([]);
  const [refunds, setRefunds] = useState<RefundView[]>([]);
  const [detail, setDetail] = useState<OrderDetail | null>(null);
  const [permissions, setPermissions] = useState<string[] | null>(null);
  const [note, setNote] = useState("");
  const [expandStores, setExpandStores] = useState(1);
  const [attempt, setAttempt] = useState(0);
  // 下单在飞闸:幂等键按一次明确的下单意图生成,双击不得变成两笔订单。
  const [ordering, setOrdering] = useState(false);

  const loadAll = useCallback(async () => {
    setPhase("loading");
    setError("");
    const [subRes, catRes, ordRes, aiRes, refRes, accRes] = await Promise.all([
      session.api("GET", "billing/subscription"),
      session.api("GET", "billing/price-catalog"),
      session.api("GET", "billing/orders"),
      session.api("GET", "billing/ai-usage"),
      session.api("GET", "billing/refunds"),
      session.api("GET", "access/effective"),
    ]);
    if (subRes.ok) {
      setSub(subRes.data as unknown as SubscriptionView);
    } else {
      setError(failureText(subRes.status, subRes.data));
    }
    if (catRes.ok && catRes.data && typeof catRes.data.effective === "object") {
      setEffective(catRes.data.effective as CatalogResolved);
    }
    if (ordRes.ok && Array.isArray(ordRes.data.items)) setOrders(ordRes.data.items as BillingOrderView[]);
    if (aiRes.ok && Array.isArray(aiRes.data.items)) setAiUsage(aiRes.data.items as AIUsageView[]);
    if (refRes.ok && Array.isArray(refRes.data.items)) setRefunds(refRes.data.items as RefundView[]);
    if (accRes.ok && Array.isArray(accRes.data.permissions)) {
      setPermissions(accRes.data.permissions as string[]);
    } else {
      // FEATURE_AUTHZ off(access/effective 404):回退角色判定(见 lib)。
      setPermissions(null);
    }
    setPhase(subRes.ok ? "ready" : "error");
  }, [session]);

  useEffect(() => {
    void loadAll();
  }, [loadAll, attempt]);

  const manage = canManageBilling(session.role, permissions);

  const placeOrder = useCallback(
    async (action: "new" | "renew" | "expand", planCode: string, additionalStores?: number) => {
      if (ordering) return;
      setOrdering(true);
      setNote("");
      const body: Record<string, unknown> = {
        action,
        plan_code: planCode,
        idempotency_key: `web-${planCode}-${action}-${Date.now()}`,
      };
      if (typeof additionalStores === "number") body.additional_stores = additionalStores;
      const res = await session.api("POST", "billing/subscription-orders", body);
      if (!res.ok) {
        setNote(failureText(res.status, res.data));
        return;
      }
      const checkout = (res.data as { checkout?: CheckoutRef }).checkout;
      const order = (res.data as { order?: BillingOrderView }).order;
      try {
        if (checkout && checkout.available && checkout.app_id) {
          // 生态收银台跳转:复用 eco-nav switch(shell launch 铸造一次性 URL)。
          const sw = await session.api("POST", "eco-nav/switch", {
            app_id: checkout.app_id,
            launch_target_id: checkout.launch_target_id,
          });
          const target = sw.data as { launch_url?: string };
          if (sw.ok && target.launch_url) {
            window.location.href = target.launch_url;
            return;
          }
          setNote("订单已登记，但生态收银台跳转暂不可用；请稍后在本页「回查」订单状态。");
        } else {
          setNote("生态收银台未接入：订单已登记为处理中，开通以服务端支付事实为准。可用「回查」刷新状态。");
        }
        if (order) setDetail(null);
        setAttempt((a) => a + 1);
      } finally {
        setOrdering(false);
      }
    },
    [session, ordering],
  );

  const lookupOrder = useCallback(
    async (id: string) => {
      setNote("");
      const res = await session.api("GET", `billing/orders/${id}`);
      if (!res.ok) {
        setNote(failureText(res.status, res.data));
        return;
      }
      setDetail(res.data as unknown as OrderDetail);
    },
    [session],
  );

  if (phase === "loading") {
    return (
      <section data-pn-page="billing">
        <p role="status">账单读取中…</p>
      </section>
    );
  }
  if (phase === "error") {
    return (
      <section data-pn-page="billing">
        <h2>订购与账单</h2>
        <p role="alert">{error || "账单读取失败。"}</p>
        <button className="tk-quiet" type="button" data-action="retry" onClick={() => setAttempt((a) => a + 1)}>
          重试
        </button>
      </section>
    );
  }

  const subPane = sub?.subscription;
  const usage = sub?.store_usage ?? { limit: 0, live: 0 };
  const monthly = effective["store.plan.month"];
  const yearly = effective["store.plan.year"];

  return (
    <section data-pn-page="billing">
      <h2>订购与账单</h2>
      <p className="tk-kicker">
        套餐、订单、AI 用量与发票退款（公共订单权威投影；本页不展示也不保存任何余额）
      </p>
      <div role="tablist" aria-label="账单页签">
        {TABS.map((t) => (
          <button key={t.key} role="tab" type="button" data-action={`tab-${t.key}`}
            aria-selected={tab === t.key} onClick={() => setTab(t.key)}>
            {t.label}
          </button>
        ))}
      </div>
      {note ? <p role="status" data-testid="billing-note">{note}</p> : null}

      {tab === "plan" && (
        <div data-testid="pane-plan">
          {!subPane || subPane.status === "unconfirmed" ? (
            <p>当前租户没有订阅事实。套餐开通以生态收银台的支付事实为准。</p>
          ) : (
            <ul data-testid="subscription-facts">
              <li>当前套餐：{planCodeText(subPane.plan_code)}</li>
              <li>状态：{subscriptionStatusText(subPane.status)}</li>
              {subPane.paid_until ? <li>服务期至：{subPane.paid_until}</li> : null}
              {subPane.retain_until ? <li>维护保留期至：{subPane.retain_until}（到期后数据只读保留，不删除）</li> : null}
              <li>{storeQuotaText(subPane.store_limit, usage.live)}</li>
              <li>到期说明：订阅到期只暂停新增收费能力；已发放的门店券仍可查询与核销。</li>
            </ul>
          )}
          <h3>价格目录（生效版本）</h3>
          {monthly || yearly ? (
            <ul data-testid="catalog-facts">
              {monthly ? (
                <li>
                  {planCodeText("store.plan.month")}：{formatAmount(monthly.price_minor, monthly.currency)} /
                  服务期 {monthly.service_days} 天 / 含 {monthly.included_store_count} 家门店，
                  加店 {formatAmount(monthly.extra_store_price_minor, monthly.currency)}·家（版本 {monthly.pricing_version}，
                  保留期 {monthly.grace_days} 天）
                </li>
              ) : null}
              {yearly ? (
                <li>
                  {planCodeText("store.plan.year")}：{formatAmount(yearly.price_minor, yearly.currency)} /
                  服务期 {yearly.service_days} 天 / 含 {yearly.included_store_count} 家门店，
                  加店 {formatAmount(yearly.extra_store_price_minor, yearly.currency)}·家（版本 {yearly.pricing_version}，
                  保留期 {yearly.grace_days} 天）
                </li>
              ) : null}
            </ul>
          ) : (
            <p>价格目录暂无生效行（价格与档位由平台运营发布；本页不发明价格）。</p>
          )}
          {manage ? (
            <div data-testid="plan-actions">
              <h3>续费 / 扩店</h3>
              <p>未到期续费从服务期届满起算（剩余天数不丢）；扩店按现行目录加店价计差价；降档在当前服务期届满后生效，已承诺权益不追溯削减。</p>
              {monthly ? (
                <button className="tk-quiet" type="button" data-action="order-renew-month" disabled={ordering}
                  onClick={() => void placeOrder("renew", "store.plan.month")}>
                  续费 {planCodeText("store.plan.month")}
                </button>
              ) : null}
              {yearly ? (
                <button className="tk-quiet" type="button" data-action="order-renew-year" disabled={ordering}
                  onClick={() => void placeOrder("renew", "store.plan.year")}>
                  续费 {planCodeText("store.plan.year")}
                </button>
              ) : null}
              <label>
                加店数
                <input
                  data-testid="expand-stores"
                  type="number"
                  min={1}
                  value={expandStores}
                  onChange={(e) => setExpandStores(Math.max(1, Number(e.target.value) || 1))}
                />
              </label>
              {monthly ? (
                <button className="tk-quiet" type="button" data-action="order-expand-month" disabled={ordering}
                  onClick={() => void placeOrder("expand", "store.plan.month", expandStores)}>
                  按月付扩店 {expandStores} 家
                </button>
              ) : null}
            </div>
          ) : (
            <p data-testid="plan-readonly">本账号为只读账单视图；续费/扩店由组织负责人操作（代理续费协助走代理工作台，代发起需付款授权）。</p>
          )}
        </div>
      )}

      {tab === "orders" && (
        <div data-testid="pane-orders">
          {orders.length === 0 ? (
            <p>还没有订单。新购/续费/扩店下单后，这里展示处理中→开通中→已开通（或失败）四态。</p>
          ) : (
            <ul data-testid="order-list">
              {orders.map((o) => (
                <li key={o.id} data-testid="order-row">
                  <span>{orderActionText(o.action)}</span>
                  <span> {o.kind === "hardware" ? "硬件订单" : planCodeText(o.plan_code)}</span>
                  <span> · {formatAmount(o.amount_minor, o.currency)}</span>
                  <span> · {orderStatusText(o.status)}</span>
                  {o.fail_reason ? <span>（原因：{o.fail_reason}）</span> : null}
                  {o.payer_grant_ref ? <span> · 代付授权 {o.payer_grant_ref}</span> : null}
                  <span> · 下单 {o.created_at}</span>
                  <button className="tk-quiet" type="button" data-action={`lookup-${o.id}`}
                    onClick={() => void lookupOrder(o.id)}>
                    回查
                  </button>
                </li>
              ))}
            </ul>
          )}
          {detail ? (
            <div data-testid="order-detail">
              <h3>订单详情（状态回查结果）</h3>
              <p>
                状态：{orderStatusText(detail.order.status)}
                {detail.order.payment_ref ? ` · 支付引用 ${detail.order.payment_ref}` : " · 暂无支付事实"}
                {detail.order.order_ref ? ` · 权威订单引用 ${detail.order.order_ref}` : ""}
              </p>
              {detail.items && detail.fulfillment ? (
                <ul data-testid="hardware-facts">
                  {detail.items.map((it) => (
                    <li key={it.id}>
                      {it.name}（{it.kind === "goods" ? "一次性商品" : "一次性服务"}）×{it.qty} ·
                      单价 {formatAmount(it.unit_amount_minor, detail.order.currency)}
                    </li>
                  ))}
                  {HARDWARE_STEPS.map((s) => (
                    <li key={s.key}>
                      {s.label}：{stepText(detail.fulfillment?.[s.key])}
                      {detail.fulfillment?.[s.key]?.device_ref
                        ? ` · 设备 ${detail.fulfillment[s.key].device_ref}`
                        : ""}
                    </li>
                  ))}
                </ul>
              ) : null}
            </div>
          ) : null}
        </div>
      )}

      {tab === "ai" && (
        <div data-testid="pane-ai">
          <p>AI 用量按实际合约扣费；商家浏览与复制内容不计费（不计费动作不进入本投影）。</p>
          {aiUsage.length === 0 ? (
            <p>还没有 AI 用量投影记录。</p>
          ) : (
            <ul data-testid="ai-usage-list">
              {aiUsage.map((u) => (
                <li key={u.id} data-testid="ai-usage-row">
                  <span>引用 {u.quote_ref}</span>
                  <span> · {u.capability || "能力未标注"}</span>
                  <span> · {formatAmount(u.amount_minor, u.currency)}</span>
                  <span> · {aiUsageStateText(u.state)}</span>
                  {u.consumed_by_ref ? <span> · 消费方 {u.consumed_by_ref}</span> : null}
                  {u.occurred_at ? <span> · {u.occurred_at}</span> : null}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}

      {tab === "refunds" && (
        <div data-testid="pane-refunds">
          <p>退款与发票对账（原订单、金额、原因、开票主体、审核人逐字段可查；重复回调不会重复退款）。</p>
          {refunds.length === 0 ? (
            <p>还没有退款记录。</p>
          ) : (
            <ul data-testid="refund-list">
              {refunds.map((rf) => (
                <li key={rf.id} data-testid="refund-row">
                  <span>退款 {formatAmount(rf.amount_minor, rf.currency)}</span>
                  <span> · 原因：{rf.reason}</span>
                  <span> · 原订单 {rf.order_id}</span>
                  <span> · 开票主体：{rf.invoice_title || "未填写"}</span>
                  <span> · 发票：{invoiceStatusText(rf.invoice_status)}{rf.invoice_ref ? `（${rf.invoice_ref}）` : ""}</span>
                  <span> · 审核人：{rf.auditor}</span>
                  <span> · {rf.occurred_at}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </section>
  );
}
