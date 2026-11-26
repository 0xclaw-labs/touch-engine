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

import { useCallback, useEffect, useRef, useState } from "react";
import { failureText, useSession } from "@/components/work/merchant-session";
import { useAppSwitch } from "@/components/eco-nav/use-app-switch";
import {
  blocksNewSubscription,
  canContinueCheckout,
  checkoutUnavailableText,
  lifecycleStageText,
  milestoneStatusText,
  nextOrderStep,
  AIUsageView,
  BillingContinuityView,
  BillingOrderView,
  CheckoutRef,
  HARDWARE_STEPS,
  HardwareFulfillmentView,
  HardwareItemView,
  OrderContinuityView,
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

import { deliveryStatusFace, deliveryEventFace, readbackFace } from "@/lib/touchpoint-deliveries";
import { CheckoutContinuationBlock, type CheckoutContinuationEntry } from "@/components/work/billing/checkout-continuation";

interface PhysicalDelivery { id: string; hardware_order_id: string; status: string; binding_stale: boolean; last_readback_matches: boolean; last_readback_reason: string; replacement_of?: string; steps: Record<string, { occurred_at: string }> }

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

interface SubscriptionQuote { amount_minor: number; currency: string; price_ref: string; pricing_version: string; service_days: number }

interface OrderDetail {
  order: BillingOrderView;
  checkout?: CheckoutRef & { reason?: string };
  items?: HardwareItemView[];
  fulfillment?: HardwareFulfillmentView;
  deliveries?: PhysicalDelivery[];
  refunds?: RefundView[];
  continuity?: OrderContinuityView;
}

const TABS: { key: BillingTab; label: string }[] = [
  { key: "plan", label: "套餐与门店额度" },
  { key: "orders", label: "订单与支付" },
  { key: "ai", label: "AI 用量" },
  { key: "refunds", label: "发票与退款" },
];

export function BillingDesk() {
  const session = useSession();
  const { switchApp } = useAppSwitch(session.tenantId);
  const [phase, setPhase] = useState<LoadPhase>("loading");
  const [error, setError] = useState("");
  const [tab, setTab] = useState<BillingTab>("plan");
  const [sub, setSub] = useState<SubscriptionView | null>(null);
  const [continuity, setContinuity] = useState<BillingContinuityView | null>(null);
  const [effective, setEffective] = useState<CatalogResolved>({});
  const [orders, setOrders] = useState<BillingOrderView[]>([]);
  const [aiUsage, setAiUsage] = useState<AIUsageView[]>([]);
  const [refunds, setRefunds] = useState<RefundView[]>([]);
  const [deliveries,setDeliveries] = useState<PhysicalDelivery[]>([]);
  const [deliveryError,setDeliveryError] = useState("");
  const [detail, setDetail] = useState<OrderDetail | null>(null);
  const [permissions, setPermissions] = useState<string[] | null>(null);
  const [note, setNote] = useState("");
  const [expandStores, setExpandStores] = useState(1);
  const [attempt, setAttempt] = useState(0);
  // 下单在飞闸:幂等键按一次明确的下单意图生成,双击不得变成两笔订单。
  const [ordering, setOrdering] = useState(false);
  const orderLock = useRef(false);
  const lookupVersion = useRef(0);
  const [ordersReady, setOrdersReady] = useState(false);
  const [catalogError, setCatalogError] = useState("");
  const [quote,setQuote] = useState<SubscriptionQuote | null>(null);
  const [quoteBusy,setQuoteBusy] = useState(false);
  const quoteVersion = useRef(0);
  const [purchase, setPurchase] = useState<{ action: "new" | "renew" | "expand"; plan: string; count?: number } | null>(null);
  // HUI-4664 G1 正显示面:制作接续(可续付)真值,来自 handoff checkout 投影
  // 的只读发现(stores → handoffs → checkout GET);失败/无引用=不出块。
  const [continuations, setContinuations] = useState<CheckoutContinuationEntry[]>([]);
  const generation = useRef(0);
  useEffect(() => () => { ++generation.current; }, [session.tenantId]);

  const loadAll = useCallback(async () => {
    setPhase("loading");
    setError("");
    const seq = ++generation.current;
    setSub(null); setContinuity(null); setOrders([]); setDetail(null); setEffective({}); setPermissions([]); setAiUsage([]); setRefunds([]); setOrdersReady(false); setPurchase(null); setQuote(null);
    setContinuations([]);
    const [subRes, catRes, ordRes, aiRes, refRes, accRes, deliveryRes, contRes] = await Promise.all([
      session.api("GET", "billing/subscription"),
      session.api("GET", "billing/price-catalog"),
      session.api("GET", "billing/orders"),
      session.api("GET", "billing/ai-usage"),
      session.api("GET", "billing/refunds"),
      session.api("GET", "access/effective"),
      session.api("GET", "touchpoint-deliveries"),
      session.api("GET", "billing/continuity"),
    ]);
    if (seq !== generation.current) return;
    if (contRes.ok) {
      setContinuity(contRes.data as unknown as BillingContinuityView);
    }
    setOrdersReady(ordRes.ok);
    setDeliveries(deliveryRes.ok && Array.isArray(deliveryRes.data.items) ? deliveryRes.data.items as PhysicalDelivery[] : []);
    setDeliveryError(deliveryRes.ok ? "" : "实卡记录暂不可查或当前账号无交付查询权限，请由商家负责人从「门店投放入口」核对交付记录。");
    setCatalogError(catRes.ok ? "" : "价格目录暂时读不到，请刷新重试；持续失败请联系平台运营恢复生效目录。");
    if (subRes.ok) {
      setSub(subRes.data as unknown as SubscriptionView);
    } else {
      setError(failureText(subRes.status, subRes.data));
    }
    if (catRes.ok && catRes.data && typeof catRes.data.effective === "object") {
      setEffective(catRes.data.effective as CatalogResolved);
    }
    if (ordRes.ok && Array.isArray(ordRes.data.items)) {
      const rows = ordRes.data.items as BillingOrderView[];
      setOrders(rows);
      for (let i = window.sessionStorage.length - 1; i >= 0; --i) {
        const slot = window.sessionStorage.key(i);
        if (slot?.startsWith(`touch-billing-intent:${session.tenantId}:`) && slot.endsWith(":order")) {
          const original = rows.find((o) => o.id === window.sessionStorage.getItem(slot));
          if (original && !blocksNewSubscription(original)) { window.sessionStorage.removeItem(slot); window.sessionStorage.removeItem(slot.slice(0,-6)); }
        }
      }
    }
    if (aiRes.ok && Array.isArray(aiRes.data.items)) setAiUsage(aiRes.data.items as AIUsageView[]);
    if (refRes.ok && Array.isArray(refRes.data.items)) setRefunds(refRes.data.items as RefundView[]);
    if (accRes.ok && Array.isArray(accRes.data.permissions)) {
      setPermissions(accRes.data.permissions as string[]);
    } else {
      // FEATURE_AUTHZ off(access/effective 404):回退角色判定(见 lib)。
      setPermissions(accRes.status === 404 ? null : []);
    }
    setPhase(subRes.ok ? "ready" : "error");
    // G1 正显示面发现链(非阻塞,页面先就绪):stores → 每店 handoffs 列表 →
    // 每 handoff checkout 只读投影;canContinueCheckout 真值门在展示组件内
    // 统一裁决。任一环失败 = 诚实不出块(不虚假承诺,既有未接通文案保持)。
    void (async () => {
      try {
        const storesRes = await session.api("GET", "stores");
        if (seq !== generation.current) return;
        const stores = storesRes.ok && Array.isArray(storesRes.data.items)
          ? storesRes.data.items as Array<{ id: string }>
          : [];
        const lists = await Promise.all(stores.map((store) =>
          session.api("GET", `stores/${store.id}/content-production-handoffs`)));
        if (seq !== generation.current) return;
        const pairs: Array<{ storeId: string; handoffId: string }> = [];
        stores.forEach((store, index) => {
          const res = lists[index];
          const rows = res.ok && Array.isArray(res.data.handoffs) ? res.data.handoffs as Array<{ id: string }> : [];
          for (const h of rows) pairs.push({ storeId: store.id, handoffId: h.id });
        });
        const projections = await Promise.all(pairs.map((pair) =>
          session.api("GET", `stores/${pair.storeId}/content-production-handoffs/${pair.handoffId}/checkout`)));
        if (seq !== generation.current) return;
        const found: CheckoutContinuationEntry[] = [];
        projections.forEach((res, index) => {
          if (!res.ok) return;
          const checkout = res.data.checkout as CheckoutRef | undefined;
          if (checkout) found.push({ storeId: pairs[index].storeId, handoffId: pairs[index].handoffId, checkout });
        });
        setContinuations(found);
      } catch {
        if (seq === generation.current) setContinuations([]); // 读不到=不出块(诚实)
      }
    })();
  }, [session]);

  useEffect(() => {
    void loadAll().catch(() => { setError("账单暂时没有连上，请刷新重试。"); setPhase("error"); });
  }, [loadAll, attempt]);

  const manage = canManageBilling(session.role, permissions);

  const openCheckout = useCallback(async (checkout: CheckoutRef, seq: number) => {
    if (!checkout.available || !checkout.app_id || !checkout.launch_target_id) return false;
    // Reuse the shell's existing mint -> redeem contract. A mint response
    // deliberately has no launch URL and is never payment evidence.
    const target = await switchApp(checkout.app_id, checkout.launch_target_id);
    if (seq !== generation.current) return false;
    if (target.ok) {
      window.location.href = target.launchUrl;
      return true;
    }
    setNote(`原订单仍保留。${target.message} 可回查原订单后再次前往收银台。`);
    return false;
  }, [switchApp]);

  const continuePayment = useCallback(async (id: string) => {
    if (orderLock.current) return;
    orderLock.current = true;
    setOrdering(true); setNote("");
    const seq = generation.current;
    try {
      // HUI-4664: 统一走公共 Mint/Inspect 消费者(POST /billing/orders/{id}/checkout)
      // 发起或恢复原单收银;已付/撤权/未接通时 fail-closed 返回诚实投影,绝不重造新单。
      const mintRes = await session.api("POST", `billing/orders/${id}/checkout`, { provider: "wechat" });
      if (seq !== generation.current) return;
      if (mintRes.ok) {
        const fresh = mintRes.data as unknown as OrderDetail;
        setDetail(fresh);
        const payTarget = fresh.checkout?.code_url || fresh.checkout?.checkout_url;
        if (canContinueCheckout(fresh.checkout) && payTarget) {
          setNote(`已获取权威收银单（引用 ${fresh.checkout?.checkout_ref}）。付款状态以服务端事实为准，完成后请点击「回查」。`);
          return;
        }
      }
      const res = await session.api("GET", `billing/orders/${id}`);
      if (seq !== generation.current) return;
      if (!res.ok) { setNote(failureText(res.status, res.data)); return; }
      const original = res.data as unknown as OrderDetail;
      setDetail(original);
      if (!original.checkout?.available) {
        setNote(checkoutUnavailableText(original.checkout));
        return;
      }
      if (canContinueCheckout(original.checkout)) {
        setNote(`原单已就绪（引用 ${original.checkout.checkout_ref}），请通过权威收银链接继续支付，付款后回查原单。`);
        return;
      }
      await openCheckout(original.checkout, seq);
    } catch {
      if (seq === generation.current) setNote("原订单状态未能确认，请先回查；此次没有重新下单或确认付款。");
    } finally { orderLock.current = false; setOrdering(false); }
  }, [session, openCheckout]);

  async function reviewPurchase(action: "new" | "renew" | "expand", plan: string, count?: number) {
    const seq = ++quoteVersion.current;
    const tenantSeq = generation.current;
    setQuote(null); setPurchase(null); setQuoteBusy(true); setNote("");
    try {
      const res = await session.api("POST","billing/subscription-quote",{action,plan_code:plan,additional_stores:count});
      if (seq !== quoteVersion.current || tenantSeq !== generation.current) return;
      if (!res.ok) {setNote(failureText(res.status,res.data));return;}
      setQuote(res.data as unknown as SubscriptionQuote); setPurchase({action,plan,count});
    } catch { if (seq === quoteVersion.current && tenantSeq === generation.current) setNote("报价暂不可用，请稍后重试；尚未创建订单。"); }
    finally {if(seq === quoteVersion.current) setQuoteBusy(false);}
  }

  const placeOrder = useCallback(
    async (action: "new" | "renew" | "expand", planCode: string, additionalStores?: number) => {
      if (orderLock.current || !quote || !ordersReady || orders.some(blocksNewSubscription) || !sub?.checkout?.available) return;
      orderLock.current = true;
      setOrdering(true);
      const seq = generation.current;
      setNote("");
      try {
      // Persist one intent through response loss, refresh and browser back.
      const intentSlot = `touch-billing-intent:${session.tenantId}:${action}:${planCode}:${additionalStores ?? 0}`;
      let intentKey = window.sessionStorage.getItem(intentSlot);
      if (!intentKey) { intentKey = crypto.randomUUID(); window.sessionStorage.setItem(intentSlot, intentKey); }
      const body: Record<string, unknown> = { action, plan_code: planCode, price_ref: quote.price_ref, idempotency_key: intentKey };
      if (typeof additionalStores === "number") body.additional_stores = additionalStores;
      const res = await session.api("POST", "billing/subscription-orders", body);
      if (seq !== generation.current) return;
      if (!res.ok) { setNote(failureText(res.status, res.data)); return; }
      const checkout = (res.data as { checkout?: CheckoutRef }).checkout;
      const order = (res.data as { order?: BillingOrderView }).order;
      if (order) window.sessionStorage.setItem(intentSlot + ":order",order.id);
        if (checkout && checkout.available && checkout.app_id) {
          const launched = await openCheckout(checkout, seq);
          if (seq !== generation.current) return;
          if (launched) return;
        } else {
          setNote("生态收银台未接入：订单已登记为处理中，开通以服务端支付事实为准。可用「回查」刷新状态。");
        }
        if (order) setDetail(null);
        setAttempt((a) => a + 1);
      } catch {
        if (seq === generation.current) setNote("订单结果暂未确认。请先回查订单；重试会沿用本次办理编号，避免重复下单。");
      } finally {
        orderLock.current = false;
        setOrdering(false);
        setPurchase(null);
      }
    },
    [session, ordersReady, orders, sub, quote, openCheckout],
  );

  const lookupOrder = useCallback(
    async (id: string) => {
      setNote("");
      const seq = generation.current;
      const lookupSeq = ++lookupVersion.current;
      try {
      const res = await session.api("GET", `billing/orders/${id}`);
      if (seq !== generation.current || lookupSeq !== lookupVersion.current) return;
      if (!res.ok) {
        setNote(failureText(res.status, res.data));
        return;
      }
      const fresh = res.data as unknown as OrderDetail;
      setDetail(fresh);
      if (fresh.order) setOrders((previous) => previous.map((o) => o.id === fresh.order.id ? fresh.order : o));
      } catch { if (seq === generation.current && lookupSeq === lookupVersion.current) setNote("原订单暂时查不到，请稍后重试，无需重复购买。"); }
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
        {session.tenantName} · 查看当前服务、接续订单与交付，再办理续费和扩店
      </p>
      <section className="tk-section" data-testid="billing-summary">
        <h3>当前服务与待办</h3>
        <p>{session.tenantName} · {subPane ? subscriptionStatusText(subPane.status) : "暂无已确认服务"} · {storeQuotaText(usage.limit, usage.live)}</p>
        <p>服务期至：{subPane?.paid_until || "尚无生效期限"}。付款主体：{session.role === "agent" ? "仅按商家已有的独立付款授权办理" : "当前组织，由有付款权限的人确认办理"}。</p>
        {!ordersReady ? <p role="alert">订单暂时读不到，请刷新重试，确认已有订单后再购买。</p> : null}
        {orders.filter((o) => blocksNewSubscription(o) || (o.kind === "hardware" && o.status !== "cancelled")).map((o) => <p key={o.id}>{planCodeText(o.plan_code)} · {formatAmount(o.amount_minor,o.currency)} · {nextOrderStep(o)} <button type="button" className="tk-quiet" onClick={() => { setTab("orders"); void lookupOrder(o.id); }}>回查原订单</button></p>)}
        <CheckoutContinuationBlock entries={continuations} />
        {continuity && (!continuity.catalog_readiness.ready || !continuity.payer_authority.can_pay || !continuity.checkout_channel.ready) ? (
          <ul data-testid="billing-readiness-gaps">
            {!continuity.catalog_readiness.ready && continuity.catalog_readiness.next_step ? (
              <li>缺生效目录：{continuity.catalog_readiness.next_step.summary}</li>
            ) : null}
            {!continuity.payer_authority.can_pay && continuity.payer_authority.next_step ? (
              <li>付款授权：{continuity.payer_authority.next_step.summary}</li>
            ) : null}
            {!continuity.checkout_channel.ready && continuity.checkout_channel.next_step ? (
              <li>收费通道：{continuity.checkout_channel.next_step.summary}</li>
            ) : null}
          </ul>
        ) : null}
        {!sub?.checkout?.available && !continuity?.checkout_channel?.ready ? <p>支付接续尚未开通，暂不能确认下单。已有订单会保留，可继续回查；请联系平台完成接入。</p> : null}
      </section>
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
          {catalogError ? <p role="alert">{catalogError}</p> : null}
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
          ) : catalogError ? null : (
            <p>价格目录暂无生效行，请联系平台运营发布有效档位后刷新。已有订单仍可回查。</p>
          )}
          {purchase && quote ? <section className="tk-section" aria-label="确认购买">
            <h3>确认{orderActionText(purchase.action)}</h3>
            <p>组织：{session.tenantName}。付款资格：{session.role === "agent" ? "商家独立代付授权" : "组织付款权限"}。</p>
            <p>商品：{planCodeText(purchase.plan)}；价格版本：{quote.pricing_version}；服务期 {quote.service_days} 天。</p>
            <p>预计金额：{formatAmount(quote.amount_minor,quote.currency)}。本次报价来自当前生效目录，价格引用随订单校验；生效日以支付后的服务事实为准；未到期续费接续原服务期。</p>
            {!sub?.checkout?.available ? <p>可先核对本次报价。支付接续尚未开通，当前没有创建订单。</p> : null}
            <button className="tk-button" type="button" disabled={ordering || !sub?.checkout?.available} onClick={() => void placeOrder(purchase.action,purchase.plan,purchase.count)}>确认并前往受控支付</button>
            <button className="tk-quiet" type="button" disabled={ordering} onClick={() => setPurchase(null)}>返回修改</button>
          </section> : null}
          {manage && ordersReady && !orders.some(blocksNewSubscription) && (monthly || yearly) ? (
            <div data-testid="plan-actions">
              <h3>续费 / 扩店</h3>
              <p>未到期续费从服务期届满起算（剩余天数不丢）；扩店按现行目录加店价计差价；降档在当前服务期届满后生效，已承诺权益不追溯削减。</p>
              {monthly ? (
                <button className="tk-quiet" type="button" data-action="order-renew-month" disabled={ordering || quoteBusy}
                  onClick={() => void reviewPurchase(subPane?.status === "unconfirmed" || !subPane ? "new" : "renew", "store.plan.month")}>
                  查看{planCodeText("store.plan.month")}{subPane?.status === "unconfirmed" || !subPane ? "开通" : "续费"}报价
                </button>
              ) : null}
              {yearly ? (
                <button className="tk-quiet" type="button" data-action="order-renew-year" disabled={ordering || quoteBusy}
                  onClick={() => void reviewPurchase(subPane?.status === "unconfirmed" || !subPane ? "new" : "renew", "store.plan.year")}>
                  查看{planCodeText("store.plan.year")}{subPane?.status === "unconfirmed" || !subPane ? "开通" : "续费"}报价
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
                <button className="tk-quiet" type="button" data-action="order-expand-month" disabled={ordering || quoteBusy}
                  onClick={() => void reviewPurchase("expand", subPane?.plan_code || "store.plan.month", expandStores)}>
                  查看扩店 {expandStores} 家的报价
                </button>
              ) : null}
            </div>
          ) : (
            <p data-testid="plan-readonly">{!manage ? "当前账号没有付款权限。请交由本组织负责人或已有独立付款授权的人办理；配置权限不包含付款权限。" : orders.some(blocksNewSubscription) ? "已有订单正在办理，请先回查原订单，不需要重复购买。" : "待目录与订单读取恢复后即可查看报价，请按上方缺项联系平台。"}</p>
          )}
        </div>
      )}

      {tab === "orders" && (
        <div data-testid="pane-orders">
          <section className="tk-section"><h3>实卡交付事实</h3>
            {deliveryError ? <p>{deliveryError}</p> : deliveries.length === 0 ? <p>暂无实卡交付记录。硬件付款不代表已绑定、写卡、回读或现场验收。</p> : deliveries.map((record) => <article key={record.id}>
              <p>{deliveryStatusFace(record.status)}{record.replacement_of ? " · 补换卡记录" : ""}</p>
              {record.binding_stale ? <p role="alert">门店入口绑定已变更，旧实测不能代表当前绑定，请重新核对。</p> : null}
              <p>{readbackFace(record.last_readback_reason ? record.last_readback_matches : undefined,record.last_readback_reason)}</p>
              <ul>{Object.entries(record.steps ?? {}).map(([event,fact]) => <li key={event}>{deliveryEventFace(event)}：{fact.occurred_at}</li>)}</ul>
              <a href="/work/touchpoints">进入门店投放入口查看交付与补换</a>
            </article>)}
          </section>
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
                {detail.continuity ? lifecycleStageText(detail.continuity.lifecycle_stage) : nextOrderStep(detail.order)} · 状态：{orderStatusText(detail.order.status)}
                {detail.order.payment_ref ? ` · 支付引用 ${detail.order.payment_ref}` : " · 暂无支付事实"}
                {detail.order.order_ref ? ` · 权威订单引用 ${detail.order.order_ref}` : ""}
              </p>
              {detail.continuity ? (
                <div data-testid="order-continuity">
                  <p>{detail.continuity.next_step.summary}</p>
                  <ul data-testid="order-milestones">
                    {detail.continuity.milestones.map((m) => (
                      <li key={m.key}>
                        {m.label}：{milestoneStatusText(m.status)}
                        {m.detail ? `（${m.detail}）` : ""}
                        {m.occurred_at ? ` · ${m.occurred_at}` : ""}
                      </li>
                    ))}
                  </ul>
                </div>
              ) : null}
              {detail.checkout?.code_url ? (
                <p data-testid="order-code-url">
                  权威收银单（引用 {detail.checkout.checkout_ref}
                  {detail.checkout.remote_checkout_ref ? ` / 远端 ${detail.checkout.remote_checkout_ref}` : ""}）：
                  <code>{detail.checkout.code_url}</code>
                </p>
              ) : null}
              {(detail.checkout?.available || detail.continuity?.next_step?.allow_retry_payment) ? (
                <button className="tk-button" type="button" disabled={ordering} onClick={() => void continuePayment(detail.order.id)}>
                  继续支付原订单
                </button>
              ) : null}
              {!detail.checkout?.available && detail.checkout?.reason ? (
                <p>{checkoutUnavailableText(detail.checkout)}</p>
              ) : null}
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
