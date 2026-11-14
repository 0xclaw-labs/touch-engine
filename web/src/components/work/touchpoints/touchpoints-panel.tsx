"use client";

// Store-root stable entries are independent of optional physical cards.
// Creation retries persist one frozen request. Legacy tags, rebind concurrency,
// downloads and append-only delivery facts continue through the original APIs.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { LoadErrorState } from "@/components/work/load-error-state";
import { failureText, useSession } from "@/components/work/merchant-session";
import {
  bindingStaleFace,
  deliveryEventFace,
  deliveryNextStep,
  deliveryStatusFace,
  isDeliveryStatus,
  isReplaceReason,
  readbackFace,
  replaceReasonFace,
  replaceReasonList,
} from "@/lib/touchpoint-deliveries";
import { entryIntentStorageKey, freezeEntryIntent, readEntryIntent, type EntryIntent } from "@/lib/touchpoint-creation";
import {
  entryFace,
  isStage,
  nextStage,
  rebindImpactFace,
  sharedStopFace,
  stageFace,
  targetFace,
} from "@/lib/touchpoints";

interface TouchpointItem {
  id: string;
  code: string;
  label?: string;
  position?: string;
  status: string;
  deployment_stage?: string;
  tag_id?: string;
  tag_label?: string;
  store_id?: string;
  store_name?: string;
  store_code?: string;
  campaign_id?: string;
  campaign_title?: string;
  campaign_status?: string;
  target_type: string;
  binding_revision?: number;
  url?: string;
  entry_code?: string;
  entry_state?: string;
  created_at?: string;
}

interface CampaignItem {
  id: string;
  title?: string;
  store_id?: string;
}

interface StoreItem {
  id: string;
  name?: string;
}

interface TagItem {
  id: string;
  label: string;
  status: string;
  link_id: string;
  store_id?: string;
  campaign_id?: string;
  campaign_title?: string;
}

interface TargetFacts {
  target_type?: string;
  id?: string;
  title?: string;
  status?: string;
  store_code?: string;
}

interface RebindPreview {
  current: TargetFacts;
  next: TargetFacts;
  started_participations: number;
  binding_revision?: number;
}

// HUI-1663 交付视图(服务端 TouchpointDeliveryView 的展示子集)。
interface DeliveryStepFact {
  event_type?: string;
  operator_ref?: string;
  device_ref?: string;
  evidence_ref?: string;
  observed_binding_revision?: number;
  occurred_at?: string;
}

interface DeliveryView {
  id: string;
  touchpoint_id: string;
  status: string;
  hardware_order_id?: string;
  order_ref?: string;
  physical_unit_ref?: string;
  replacement_of?: string;
  bound_binding_revision?: number;
  verified_binding_revision?: number;
  current_binding_revision?: number;
  binding_stale?: boolean;
  payment?: { state?: string; operator?: string; occurred_at?: string; device_ref?: string };
  steps?: Record<string, DeliveryStepFact>;
  last_readback?: DeliveryStepFact | null;
  last_readback_matches?: boolean;
  last_readback_reason?: string;
}

interface DeliveryEventRow {
  id?: string;
  event_type?: string;
  operator_ref?: string;
  device_ref?: string;
  occurred_at?: string;
}

interface DeliveryDetail extends DeliveryView {
  events?: DeliveryEventRow[];
  replacements?: Array<{ reason_category?: string; reason_detail?: string; old_entry_revoked?: number }>;
}

function queryOf(params: Record<string, string>): string {
  const qs = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value) qs.set(key, value);
  }
  const text = qs.toString();
  return text ? `?${text}` : "";
}

// 用商家可核对的名称说明换绑对象；可复制的完整链接另行显示。
function targetLine(facts: TargetFacts | undefined): string {
  if (!facts || !facts.target_type) return "目标未知";
  return `${targetFace(facts.target_type)} · ${facts.title || "名称待确认"}`;
}

export function TouchpointsPanel({ storeId = "" }: { storeId?: string } = {}) {
  const session = useSession();
  const [phase, setPhase] = useState<"loading" | "error" | "ready">("loading");
  const [loadError, setLoadError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const [notice, setNotice] = useState("");

  const [items, setItems] = useState<TouchpointItem[]>([]);
  const [campaigns, setCampaigns] = useState<CampaignItem[]>([]);
  const [stores, setStores] = useState<StoreItem[]>([]);
  const [tags, setTags] = useState<TagItem[]>([]);

  const [storeFilter, setStoreFilter] = useState(storeId);
  const [campaignFilter, setCampaignFilter] = useState("");
  const [statusFilter, setStatusFilter] = useState("");

  const [createStoreId, setCreateStoreId] = useState(storeId);
  const [createCampaignId, setCreateCampaignId] = useState("");
  const [createTagId, setCreateTagId] = useState("");
  const [createLabel, setCreateLabel] = useState("");
  const [createPosition, setCreatePosition] = useState("");
  const [legacyCreate, setLegacyCreate] = useState(false);
  const [creationIntent, setCreationIntent] = useState<EntryIntent | null>(null);
  const [creating, setCreating] = useState(false);
  const creationBusy = useRef(false);
  const intentKey = entryIntentStorageKey(session.tenantId, session.email, createStoreId);
  useEffect(() => {
    try {
      const saved = readEntryIntent(localStorage, intentKey);
      setCreationIntent(saved);
      setCreateLabel(saved?.request.label ?? "");
      setCreatePosition(saved?.request.position ?? "");
    } catch { setNotice("此前入口请求无法恢复，请先核对下方已有入口。"); }
  }, [intentKey]);
  const [hardwareOrders, setHardwareOrders] = useState<Array<{id: string; name: string}>>([]);
  const [ordersNote, setOrdersNote] = useState("");
  const [billingReadable, setBillingReadable] = useState(false);
  const [deliveryTarget, setDeliveryTarget] = useState("");
  const [deliveryOrder, setDeliveryOrder] = useState("");
  const [deliveryUnit, setDeliveryUnit] = useState("");

  const [rebindTarget, setRebindTarget] = useState<TouchpointItem | null>(null);
  const [rebindType, setRebindType] = useState<"store" | "legacy_campaign">("store");
  const [rebindStoreId, setRebindStoreId] = useState("");
  const [rebindCampaignId, setRebindCampaignId] = useState("");
  const [preview, setPreview] = useState<RebindPreview | null>(null);
  const [legacyPending, setLegacyPending] = useState<{ tag: TagItem; shared: number } | null>(null);

  // HUI-1663 交付插槽:交付记录(按门店过滤,服务端收口)/事实录入/详情展开/补换。
  const [deliveries, setDeliveries] = useState<DeliveryView[]>([]);
  const [deliveriesNote, setDeliveriesNote] = useState("");
  const [deliveryDetail, setDeliveryDetail] = useState<DeliveryDetail | null>(null);
  const [deliveryDevice, setDeliveryDevice] = useState("");
  const [deliveryReadUrl, setDeliveryReadUrl] = useState("");
  const [deliveryEvidence, setDeliveryEvidence] = useState("");
  const [replaceReason, setReplaceReason] = useState("");
  const [replaceUnit, setReplaceUnit] = useState("");
  const [replaceTouchpointId, setReplaceTouchpointId] = useState("");
  const [replaceTarget, setReplaceTarget] = useState<DeliveryView | null>(null);

  const loadDeliveries = useCallback(async () => {
    const res = await session.api("GET", "touchpoint-deliveries" + queryOf({ store_id: storeFilter }));
    if (!res.ok) {
      setDeliveries([]);
      setDeliveriesNote(failureText(res.status, res.data));
      return;
    }
    setDeliveriesNote("");
    setDeliveries(Array.isArray(res.data.items) ? (res.data.items as DeliveryView[]) : []);
  }, [session, storeFilter]);

  const load = useCallback(async () => {
    const [tpRes, campRes, storeRes, tagRes] = await Promise.all([
      session.api("GET", "touchpoints" + queryOf({ store_id: storeFilter, campaign_id: campaignFilter, status: statusFilter })),
      session.api("GET", "campaigns"),
      session.api("GET", "stores"),
      session.api("GET", "nfc/tags"),
    ]);
    if (!tpRes.ok) {
      setLoadError(failureText(tpRes.status, tpRes.data));
      setPhase("error");
      return;
    }
    setItems(Array.isArray(tpRes.data.items) ? (tpRes.data.items as TouchpointItem[]) : []);
    if (campRes.ok && Array.isArray(campRes.data.items)) setCampaigns(campRes.data.items as CampaignItem[]);
    if (storeRes.ok && Array.isArray(storeRes.data.items)) setStores(storeRes.data.items as StoreItem[]);
    if (tagRes.ok && Array.isArray(tagRes.data.items)) setTags(tagRes.data.items as TagItem[]);
    await loadDeliveries();
    const ordersRes = await session.api("GET", "billing/orders?kind=hardware").catch(() => ({ok: false, status: 0, data: {} as Record<string, unknown>}));
    setBillingReadable(ordersRes.ok);
    if (!ordersRes.ok) {
      setHardwareOrders([]);
      setOrdersNote(ordersRes.status === 403 ? "你没有账单查看权限。请有权限的负责人先关联已付款硬件订单，你可继续处理下方已关联交付。" : "订单付款状态未能读取，请重试或到本商家的账单核对原订单。");
    } else {
      const orders = Array.isArray(ordersRes.data.items) ? ordersRes.data.items as Array<{id: string; created_at?: string}> : [];
      const checked = await Promise.all(orders.map(async (order) => {
        const detail = await session.api("GET", `billing/orders/${order.id}`).catch(() => ({ok: false, status: 0, data: {} as Record<string, unknown>}));
        if (!detail.ok) return { unknown: true };
        const facts = detail.data.fulfillment as {payment?: {state?: string}} | undefined;
        const lines = detail.data.items as Array<{name?: string}> | undefined;
        return facts?.payment?.state === "done" ? {id: order.id, name: `${lines?.map(line => line.name).filter(Boolean).join("、") || "硬件订单"} · ${order.created_at || "已付款"}`} : null;
      }));
      setHardwareOrders(checked.filter((row): row is {id: string; name: string} => !!row && "id" in row));
      setOrdersNote(checked.some(row => row && "unknown" in row) ? "部分订单付款状态未知，请核对原订单；仅列出付款事实已确认的硬件订单。" : "");
    }
    setPhase("ready");
  }, [session, storeFilter, campaignFilter, statusFilter, loadDeliveries]);

  useEffect(() => {
    let alive = true;
    setPhase("loading");
    void load().then(() => {
      if (!alive) return;
    }).catch(() => {
      if (alive) {
        setLoadError("没有连上服务。请稍后重试。");
        setPhase("error");
      }
    });
    return () => {
      alive = false;
    };
  }, [load, attempt]);

  const aliasTagIds = useMemo(() => new Set(items.map((item) => item.tag_id).filter(Boolean)), [items]);
  // 创建:先选门店,再按门店(可再按活动)过滤待开卡;活动过滤只是表单筛选,
  // 服务端始终从标签绑定派生门店与活动注记(客户端输入不作为绑定来源)。
  const unprovisionedTags = useMemo(() =>
    tags.filter((tag) =>
      !aliasTagIds.has(tag.id) &&
      (!createStoreId || tag.store_id === createStoreId) &&
      (!createCampaignId || tag.campaign_id === createCampaignId),
    ), [tags, aliasTagIds, createStoreId, createCampaignId]);

  const run = useCallback(async (action: () => Promise<void>) => {
    setNotice("");
    try {
      await action();
    } catch {
      setNotice("操作没有完成。网络未知,已保守处理,请刷新核对后再试。");
    }
  }, []);

  const create = useCallback(() => run(async () => {
    if (creationBusy.current) return;
    if (!createStoreId) { setNotice("先选本次创建入口的门店。"); return; }
    if (legacyCreate && !createTagId) { setNotice("先选要接续的旧卡。"); return; }
    creationBusy.current = true; setCreating(true);
    try {
      const intent = legacyCreate ? null : freezeEntryIntent(localStorage, intentKey, {store_id: createStoreId, label: createLabel.trim(), position: createPosition.trim()}, () => crypto.randomUUID());
      if (intent) setCreationIntent(intent);
      const body = intent ? intent.request : {tag_id: createTagId, label: createLabel, position: createPosition};
      const res = await session.api("POST", "touchpoints", body);
      if (!res.ok) { setNotice(res.status === 409 ? "这次创建请求与已有记录冲突，请先核对下方入口；重试不会另生入口。" : failureText(res.status,res.data)); return; }
      if (intent) {
        const completed = {...intent, completed: true};
        localStorage.setItem(intentKey,JSON.stringify(completed)); setCreationIntent(completed);
      }
      setCreateTagId("");
      setNotice(typeof res.data.url === "string" && res.data.url.trim()
        ? "稳定入口已保存，可复制链接或下载二维码。实体卡尚需关联已付款硬件订单并逐步记录交付事实。"
        : "稳定入口已保存。发布地址尚未配置，请联系系统管理员；配置完成后可继续使用这个入口，无需重新创建。");
      await load();
    } finally { creationBusy.current = false; setCreating(false); }
  }), [run, session, createStoreId, createTagId, createLabel, createPosition, legacyCreate, intentKey, load]);

  const newEntry = () => {
    localStorage.removeItem(intentKey); setCreationIntent(null); setLegacyCreate(false); setCreateLabel(""); setCreatePosition("");
    setNotice("正在另建一个独立入口，现有稳定入口保持可用。");
  };

  const createDelivery = useCallback((tp: TouchpointItem) => run(async () => {
    if (!deliveryOrder) {setNotice("先选择付款事实已确认的硬件订单。");return;}
    const res = await session.api("POST", "touchpoint-deliveries", {touchpoint_id: tp.id, hardware_order_id: deliveryOrder, physical_unit_ref: deliveryUnit.trim()});
    if (!res.ok) {setNotice(res.status === 409 ? "该入口已有交付或原订单付款尚未确认，请核对原记录。" : failureText(res.status,res.data)); await loadDeliveries();return;}
    setDeliveryTarget(""); setDeliveryOrder(""); setDeliveryUnit("");
    setNotice("已关联原硬件订单，当前待出库；付款、服务开通、写卡、回读与真机验收各自独立。");
    await loadDeliveries();
  }),[run,session,deliveryOrder,deliveryUnit,loadDeliveries]);

  const toggleStatus = useCallback((tp: TouchpointItem) => run(async () => {
    const status = tp.status === "active" ? "disabled" : "active";
    const res = await session.api("POST", `touchpoints/${tp.id}/status`, { status });
    if (!res.ok) {
      setNotice(failureText(res.status, res.data));
      return;
    }
    setNotice(status === "disabled" ? "稳定入口已停用,只影响这一张卡。" : "稳定入口已恢复。");
    await load();
  }), [run, session, load]);

  const advanceStage = useCallback((tp: TouchpointItem) => run(async () => {
    if (!isStage(tp.deployment_stage)) {
      setNotice(stageFace(tp.deployment_stage));
      return;
    }
    const nxt = nextStage(tp.deployment_stage);
    if (!nxt) {
      setNotice("已经是最后一态:真机打开验证完成。");
      return;
    }
    const res = await session.api("POST", `touchpoints/${tp.id}/stage`, { stage: nxt });
    if (!res.ok) {
      setNotice(res.status === 409 ? "状态被别人先改了一步,已重查,请看最新状态。" : failureText(res.status, res.data));
      await load();
      return;
    }
    await load();
  }), [run, session, load]);

  const previewRebind = useCallback((tp: TouchpointItem) => run(async () => {
    const body = rebindType === "store"
      ? { target_type: "store", store_id: rebindStoreId }
      : { target_type: "legacy_campaign", campaign_id: rebindCampaignId };
    if (rebindType === "store" && !rebindStoreId) {
      setNotice("先选要换到的目标门店。");
      return;
    }
    if (rebindType === "legacy_campaign" && !rebindCampaignId) {
      setNotice("先选要换到的目标活动。");
      return;
    }
    const res = await session.api("POST", `touchpoints/${tp.id}/rebind-preview`, body);
    if (!res.ok) {
      setNotice(failureText(res.status, res.data));
      return;
    }
    setPreview({
      current: (res.data.current ?? {}) as TargetFacts,
      next: (res.data.next ?? {}) as TargetFacts,
      started_participations: typeof res.data.started_participations === "number" ? res.data.started_participations : 0,
      binding_revision: typeof res.data.binding_revision === "number" ? res.data.binding_revision : undefined,
    });
  }), [run, session, rebindType, rebindStoreId, rebindCampaignId]);

  const confirmRebind = useCallback((tp: TouchpointItem) => run(async () => {
    if (!preview || typeof preview.binding_revision !== "number") {
      setNotice("先重新预览影响再确认。");
      return;
    }
    const body = rebindType === "store"
      ? { target_type: "store", store_id: rebindStoreId, expected_binding_revision: preview.binding_revision }
      : { target_type: "legacy_campaign", campaign_id: rebindCampaignId, expected_binding_revision: preview.binding_revision };
    const res = await session.api("POST", `touchpoints/${tp.id}/rebind`, body);
    if (res.status === 409) {
      setNotice("目标刚被别人改过,没有覆盖。已重查,请重新预览再确认。");
      setPreview(null);
      await load();
      return;
    }
    if (!res.ok) {
      setNotice(failureText(res.status, res.data));
      return;
    }
    setRebindTarget(null);
    setPreview(null);
    setRebindStoreId("");
    setRebindCampaignId("");
    setNotice("换绑完成。稳定链接不变,顾客下次打开直达新目标;已有参与和奖励不受影响。");
    await load();
  }), [run, session, preview, rebindType, rebindStoreId, rebindCampaignId, load]);

  const copyUrl = useCallback((tp: TouchpointItem) => run(async () => {
    if (!tp.url) {
      setNotice("这个入口还没有可复制的稳定链接(服务端未配置对外地址)。");
      return;
    }
    await navigator.clipboard.writeText(tp.url);
    setNotice("稳定链接已复制。同一链接,换绑目标后仍然有效。");
  }), [run]);

  const downloadQr = useCallback((tp: TouchpointItem) => run(async () => {
    const res = await fetch(`/api/touchpoints/${tp.id}/qrcode?size=256`, {
      headers: { "x-tenant-id": session.tenantId },
    });
    if (!res.ok) {
      const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
      setNotice(res.status === 503 ? "服务端还没配置对外地址,出不了二维码。" : failureText(res.status, data));
      return;
    }
    const blob = await res.blob();
    const href = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = href;
    anchor.download = `qr-touchpoint-${tp.code}.png`;
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    URL.revokeObjectURL(href);
  }), [run, session.tenantId]);

  const downloadCsv = useCallback(() => run(async () => {
    const res = await fetch("/api/touchpoints/export.csv" + queryOf({ store_id: storeFilter, campaign_id: campaignFilter, status: statusFilter }), {
      headers: { "x-tenant-id": session.tenantId },
    });
    if (!res.ok) {
      const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
      setNotice(res.status === 503 ? "服务端还没配置对外地址,出不了写卡资料。" : failureText(res.status, data));
      return;
    }
    const blob = await res.blob();
    const href = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = href;
    anchor.download = "touchpoints-stable-links.csv";
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    URL.revokeObjectURL(href);
  }), [run, session.tenantId, storeFilter, campaignFilter, statusFilter]);

  // 旧链接停用是两步:先算共享影响并展示(sharedStopFace),用户显式确认才发请求。
  // 不用浏览器原生弹窗:文案要留在页面里可回看,也守 uifinish 文件级闸。
  const legacyStop = useCallback((tag: TagItem) => {
    const shared = tags.filter((other) => other.link_id === tag.link_id && other.status === "active").length;
    setLegacyPending({ tag, shared });
  }, [tags]);

  const confirmLegacyStop = useCallback(() => run(async () => {
    const pending = legacyPending;
    if (!pending) return;
    const res = await session.api("POST", `nfc/tags/${pending.tag.id}/status`, { status: "disabled" });
    if (!res.ok) {
      setNotice(failureText(res.status, res.data));
      setLegacyPending(null);
      return;
    }
    setLegacyPending(null);
    setNotice("旧链接标签已停用(共享影响已按页面提示确认)。");
    await load();
  }), [run, session, legacyPending, load]);

  const openRebind = useCallback((tp: TouchpointItem) => {
    setRebindTarget(tp);
    setPreview(null);
    setRebindType(tp.target_type === "legacy_campaign" ? "legacy_campaign" : "store");
    setRebindStoreId("");
    setRebindCampaignId("");
    setNotice("");
  }, []);

  // ---- HUI-1663 交付插槽动作 -------------------------------------------------
  // 事件幂等键由前端每次明确意图生成一次(crypto.randomUUID);重放由服务端
  // (source+event_id)兜底;操作者/门店由服务端会话解析,前端不传。

  const recordDeliveryEvent = useCallback((d: DeliveryView) => run(async () => {
    const step = deliveryNextStep(d.status);
    if (!step) {
      setNotice("这张卡没有可推进的交付步骤了。");
      return;
    }
    const body: Record<string, unknown> = {
      event_type: step,
      event_id: crypto.randomUUID(),
      ...(deliveryDevice.trim() ? { device_ref: deliveryDevice.trim() } : {}),
      ...(deliveryEvidence.trim() ? { evidence_ref: deliveryEvidence.trim() } : {}),
    };
    if (step === "readback") {
      if (!deliveryReadUrl.trim()) {
        setNotice("回读要把读到的 URL 原样填进来再核对。");
        return;
      }
      body.read_url = deliveryReadUrl.trim();
    }
    const res = await session.api("POST", `touchpoint-deliveries/${d.id}/events`, body);
    if (res.status === 409) {
      const data = res.data as Record<string, unknown>;
      setNotice(
        data?.error === "step_skipped"
          ? "交付状态被别人先推进了,已重查,请看最新状态。"
          : data?.error === "IDEMPOTENCY_CONFLICT"
            ? "这条事实的幂等键被别的负载用过,请刷新后重试。"
            : failureText(res.status, res.data),
      );
      await load();
      return;
    }
    if (!res.ok) {
      const data = res.data as Record<string, unknown>;
      if (data?.error === "payment_required") {
        setNotice("这笔硬件订单还没有付款事实,不能录出库。");
        return;
      }
      setNotice(failureText(res.status, res.data));
      return;
    }
    const out = res.data as { readback_matches?: boolean; readback_reason?: string };
    setDeliveryReadUrl("");
    setDeliveryEvidence("");
    if (step === "readback" && out.readback_matches === false) {
      setNotice(readbackFace(false, out.readback_reason) + " 已记录本次回读,状态未推进。");
    } else {
      setNotice(`已记录:${deliveryEventFace(step)}。`);
    }
    await load();
  }), [run, session, deliveryDevice, deliveryEvidence, deliveryReadUrl, load]);

  const openDeliveryDetail = useCallback((d: DeliveryView) => run(async () => {
    const res = await session.api("GET", `touchpoint-deliveries/${d.id}`);
    if (!res.ok) {
      setNotice(failureText(res.status, res.data));
      return;
    }
    setDeliveryDetail(res.data as unknown as DeliveryDetail);
  }), [run, session]);

  const openReplace = useCallback((d: DeliveryView) => {
    setReplaceTarget(d);
    setReplaceReason("");
    setReplaceUnit("");
    setReplaceTouchpointId("");
    setNotice("");
  }, []);

  const submitReplace = useCallback(() => run(async () => {
    if (!replaceTarget) return;
    if (!isReplaceReason(replaceReason)) {
      setNotice("先选补换原因。");
      return;
    }
    if (replaceReason === "lost_uncontrolled" && !replaceTouchpointId) {
      setNotice("遗失失控必须换新入口:先选新触点。");
      return;
    }
    const res = await session.api("POST", `touchpoint-deliveries/${replaceTarget.id}/replace`, {
      reason_category: replaceReason,
      new_touchpoint_id: replaceReason === "damaged_recoverable" ? replaceTarget.touchpoint_id : replaceTouchpointId,
      ...(replaceUnit.trim() ? { physical_unit_ref: replaceUnit.trim() } : {}),
    });
    if (!res.ok) {
      setNotice(failureText(res.status, res.data));
      return;
    }
    setReplaceTarget(null);
    setNotice(replaceReasonFace(replaceReason) + " 新卡从出库重新走全流程。");
    await load();
  }), [run, session, replaceTarget, replaceReason, replaceUnit, replaceTouchpointId, load]);

  if (phase === "loading") {
    return <p className="tk-state" data-state="loading">正在读取…</p>;
  }
  if (phase === "error") {
    return <LoadErrorState message={loadError} onRetry={() => setAttempt((a) => a + 1)} />;
  }

  return (
    <section>
      <h1 className="tk-title">稳定入口(触点)</h1>
      <p className="tk-lead">
        每个入口有独立稳定链接，入口以门店为根，换绑目标不改链接；停用只影响当前入口；
        写卡与回读推进只经下面显式操作,下载与检查不改变状态。
      </p>
      {notice ? <p className="tk-state" data-state="notice">{notice}</p> : null}
      {deliveriesNote ? <p className="tk-note" data-state="delivery-unavailable">交付记录:{deliveriesNote}</p> : null}

      <div className="tk-toolbar">
        <label className="tk-label">
          门店
          <select className="tk-select" data-field="store-filter" disabled={!!storeId} value={storeFilter} onChange={(event) => setStoreFilter(event.target.value)}>
            <option value="">全部门店</option>
            {stores.map((sto) => (
              <option key={sto.id} value={sto.id}>{sto.name || sto.id}</option>
            ))}
          </select>
        </label>
        <label className="tk-label">
          活动
          <select className="tk-select" data-field="campaign-filter" value={campaignFilter} onChange={(event) => setCampaignFilter(event.target.value)}>
            <option value="">全部活动</option>
            {campaigns.map((camp) => (
              <option key={camp.id} value={camp.id}>{camp.title || camp.id}</option>
            ))}
          </select>
        </label>
        <label className="tk-label">
          状态
          <select className="tk-select" data-field="status-filter" value={statusFilter} onChange={(event) => setStatusFilter(event.target.value)}>
            <option value="">全部</option>
            <option value="active">启用</option>
            <option value="disabled">停用</option>
          </select>
        </label>
        <button className="tk-quiet" type="button" data-action="download-csv" onClick={() => void downloadCsv()}>
          下载写卡资料(CSV)
        </button>
      </div>

      <div className="tk-card">
        <h2 className="tk-subtitle">新建本店稳定入口</h2>
        <p className="tk-note">无需先建活动或登记实体卡。入口用于二维码、立牌或后续写卡；下载资料不代表已写入、已回读或真机验收。</p>
        <label className="tk-label">门店
          <select className="tk-select" data-field="create-store" disabled={!!storeId || creating} value={createStoreId} onChange={event => {setCreateStoreId(event.target.value);setCreateTagId("");}}>
            <option value="">选一家有权门店</option>
            {stores.map(sto => <option key={sto.id} value={sto.id}>{sto.name || "门店"}</option>)}
          </select>
        </label>
        <label className="tk-label">入口名称
          <input className="tk-input" data-field="create-label" disabled={!!creationIntent || creating} value={createLabel} maxLength={80} onChange={event => setCreateLabel(event.target.value)} placeholder="如：前台立牌" />
        </label>
        <label className="tk-label">摆放位置（可选）
          <input className="tk-input" data-field="create-position" disabled={!!creationIntent || creating} value={createPosition} maxLength={80} onChange={event => setCreatePosition(event.target.value)} placeholder="如：收银台" />
        </label>
        {!creationIntent ? <details onToggle={event => setLegacyCreate(event.currentTarget.open)}>
          <summary>接续已登记的旧卡（可选）</summary>
          <label className="tk-label">活动筛选（可选）
            <select className="tk-select" data-field="create-campaign" value={createCampaignId} onChange={event => setCreateCampaignId(event.target.value)}>
              <option value="">不限活动</option>{campaigns.filter(camp => camp.store_id === createStoreId).map(camp => <option key={camp.id} value={camp.id}>{camp.title}</option>)}
            </select>
          </label>
          <label className="tk-label">已登记的卡
            <select className="tk-select" data-field="create-tag" value={createTagId} onChange={event => setCreateTagId(event.target.value)}>
              <option value="">选择旧卡</option>{unprovisionedTags.map(tag => <option key={tag.id} value={tag.id}>{tag.label}{tag.campaign_title ? ` · ${tag.campaign_title}` : ""}</option>)}
            </select>
          </label>
          {unprovisionedTags.length === 0 ? <p className="tk-note">本店没有待接续旧卡，收起此项即可直接创建门店入口。</p> : null}
          {createTagId ? <button className="tk-quiet" type="button" onClick={() => {const tag = tags.find(row => row.id === createTagId); if(tag) legacyStop(tag);}}>停用旧共享链接</button> : null}
          {legacyPending ? <div data-state="legacy-stop"><p>{sharedStopFace(legacyPending.shared)}</p><button type="button" data-action="confirm-legacy-stop" onClick={() => void confirmLegacyStop()}>确认停用旧共享链接</button><button type="button" onClick={() => setLegacyPending(null)}>取消</button></div> : null}
        </details> : <p className="tk-note">{creationIntent.completed ? "本次入口已创建。再次查询仍恢复同一入口。" : "本次请求已保存，重试或刷新继续使用相同名称、位置和入口请求。"}</p>}
        <button className="tk-quiet" type="button" data-action="create" disabled={creating || !createStoreId} onClick={() => void create()}>{creating ? "正在保存…" : creationIntent ? "恢复／查询本次入口" : "创建稳定入口"}</button>
        {creationIntent?.completed ? <button className="tk-quiet" type="button" data-action="new-entry" onClick={newEntry}>另建一个入口</button> : null}
      </div>

      {items.length === 0 ? (
        <p className="tk-state" data-state="empty">还没有稳定入口。在上面为本店创建一个即可。</p>
      ) : (
        items.map((tp) => {
          const nxt = isStage(tp.deployment_stage) ? nextStage(tp.deployment_stage) : null;
          // HUI-1663 交付插槽:该触点的活交付(封存行只在详情账本里回看)。
          const dlv = deliveries.find((d) => d.touchpoint_id === tp.id && d.status !== "superseded") ?? null;
          const dlvStep = dlv && isDeliveryStatus(dlv.status) ? deliveryNextStep(dlv.status) : null;
          return (
            <article key={tp.id} className="tk-card" data-touchpoint={tp.code}>
              <h2 className="tk-subtitle">{tp.label || tp.tag_label || "未命名入口"}</h2>
              <p className="tk-note">
                {tp.store_name ? `${tp.store_name}` : "归属门店未定"}
                {" · 目标:"}{targetFace(tp.target_type)}
                {tp.campaign_title ? ` · 注记活动:${tp.campaign_title}` : ""}
                {tp.position ? ` · ${tp.position}` : ""}
              </p>
              <p className="tk-note">
                状态:{tp.status === "active" ? "启用" : "停用"} ·
                部署:{stageFace(tp.deployment_stage)}
              </p>
              <p className="tk-note">检查:{entryFace(tp.target_type, tp.entry_state)}</p>
              {tp.url ? <p className="tk-note"><code>{tp.url}</code></p> : <p className="tk-note">入口已保存，发布地址尚未配置。请联系系统管理员，完成后继续使用此入口。</p>}
              <div className="tk-toolbar">
                {tp.url ? <a className="tk-quiet" href={tp.url} target="_blank" rel="noopener noreferrer" data-action="open-entry">打开顾客入口</a> : null}
                <button className="tk-quiet" type="button" data-action="copy-url" disabled={!tp.url} onClick={() => void copyUrl(tp)}>复制稳定链接</button>
                <button className="tk-quiet" type="button" data-action="download-qr" disabled={!tp.url} onClick={() => void downloadQr(tp)}>下载二维码</button>
                <button className="tk-quiet" type="button" data-action="toggle-status" onClick={() => void toggleStatus(tp)}>
                  {tp.status === "active" ? "停用稳定入口" : "恢复稳定入口"}
                </button>
                {nxt ? (
                  <button className="tk-quiet" type="button" data-action="advance-stage" onClick={() => void advanceStage(tp)}>
                    推进到:{stageFace(nxt)}
                  </button>
                ) : null}
                <button className="tk-quiet" type="button" data-action="open-rebind" onClick={() => openRebind(tp)}>换绑目标</button>
              </div>
              {rebindTarget?.id === tp.id ? (
                <div className="tk-card" data-state="rebind">
                  <label className="tk-label">
                    换绑到
                    <select className="tk-select" data-field="rebind-type" value={rebindType} onChange={(event) => { setRebindType(event.target.value === "store" ? "store" : "legacy_campaign"); setPreview(null); }}>
                      <option value="store">门店首页</option>
                      <option value="legacy_campaign">活动页</option>
                    </select>
                  </label>
                  {rebindType === "store" ? (
                    <label className="tk-label">
                      目标门店
                      <select className="tk-select" data-field="rebind-store" value={rebindStoreId} onChange={(event) => { setRebindStoreId(event.target.value); setPreview(null); }}>
                        <option value="">选目标门店</option>
                        {stores.filter((sto) => sto.id !== tp.store_id).map((sto) => (
                          <option key={sto.id} value={sto.id}>{sto.name || sto.id}</option>
                        ))}
                      </select>
                    </label>
                  ) : (
                    <label className="tk-label">
                      目标活动
                      <select className="tk-select" data-field="rebind-campaign" value={rebindCampaignId} onChange={(event) => { setRebindCampaignId(event.target.value); setPreview(null); }}>
                        <option value="">选目标活动</option>
                        {campaigns.filter((camp) => camp.id !== tp.campaign_id).map((camp) => (
                          <option key={camp.id} value={camp.id}>{camp.title || camp.id}</option>
                        ))}
                      </select>
                    </label>
                  )}
                  <button className="tk-quiet" type="button" data-action="preview-rebind" onClick={() => void previewRebind(tp)}>预览影响</button>
                  {preview ? (
                    <>
                      <p className="tk-note">
                        {targetLine(preview.current)} → {targetLine(preview.next)}
                      </p>
                      <p className="tk-note">{rebindImpactFace(preview.started_participations)}</p>
                      <p className="tk-note">绑定版本:{preview.binding_revision}(别人先改过会拒绝提交,不会静默覆盖)</p>
                      <button className="tk-quiet" type="button" data-action="confirm-rebind" onClick={() => void confirmRebind(tp)}>确认换绑</button>
                      <button className="tk-quiet" type="button" data-action="cancel-rebind" onClick={() => setRebindTarget(null)}>取消</button>
                    </>
                  ) : null}
                </div>
              ) : null}
              {dlv ? (
                <div className="tk-card" data-state="delivery" data-delivery={dlv.id}>
                  <p className="tk-note" data-field="delivery-status">实体交付:{deliveryStatusFace(dlv.status)}</p>
                  {dlv.payment?.state ? (
                    <p className="tk-note">
                      付款:{dlv.payment.state === "done" ? "已确认" : "未确认"}
                      {dlv.payment.occurred_at ? ` · ${dlv.payment.occurred_at}` : ""}
                    </p>
                  ) : (
                    <p className="tk-note">付款:还没有订单付款事实(不能录出库)。</p>
                  )}
                  <p className="tk-note" data-field="delivery-binding">
                    {bindingStaleFace({
                      current: dlv.current_binding_revision,
                      bound: dlv.bound_binding_revision,
                      verified: dlv.verified_binding_revision,
                    })}
                  </p>
                  {dlv.status === "written" || dlv.status === "readback" ? (
                    <p className="tk-note" data-field="delivery-readback">
                      {!dlv.last_readback
                        ? "还没有回读记录。"
                        : readbackFace(dlv.last_readback_matches, dlv.last_readback_reason)}
                    </p>
                  ) : null}
                  {dlvStep ? (
                    <>
                      <label className="tk-label">
                        设备标识(可选)
                        <input className="tk-input" data-field="delivery-device" value={deliveryDevice} onChange={(event) => setDeliveryDevice(event.target.value)} placeholder="如:写卡器编号" />
                      </label>
                      {dlvStep === "readback" ? (
                        <>
                          <label className="tk-label">
                            回读到的 URL(原样抄录)
                            <input className="tk-input" data-field="delivery-read-url" value={deliveryReadUrl} onChange={(event) => setDeliveryReadUrl(event.target.value)} placeholder={tp.url || "/t/…"} />
                          </label>
                          <p className="tk-note">域名或码不对都会如实记为不一致,不会当作已验证。</p>
                        </>
                      ) : null}
                      <label className="tk-label">
                        证据引用(可选)
                        <input className="tk-input" data-field="delivery-evidence" value={deliveryEvidence} onChange={(event) => setDeliveryEvidence(event.target.value)} placeholder="如:回读截图编号" />
                      </label>
                      <button className="tk-quiet" type="button" data-action="delivery-event" onClick={() => void recordDeliveryEvent(dlv)}>
                        记录:{deliveryEventFace(dlvStep)}
                      </button>
                    </>
                  ) : null}
                  <div className="tk-toolbar">
                    <button className="tk-quiet" type="button" data-action="delivery-detail" onClick={() => void openDeliveryDetail(dlv)}>看写卡回读记录</button>
                    {dlv.status !== "delivered" ? (
                      <button className="tk-quiet" type="button" data-action="delivery-replace" onClick={() => openReplace(dlv)}>补换新卡</button>
                    ) : null}
                  </div>
                  {deliveryDetail?.id === dlv.id ? (
                    <div data-state="delivery-detail">
                      {deliveryDetail.events?.length ? (
                        deliveryDetail.events.map((ev) => (
                          <p className="tk-note" key={ev.id}>
                            {deliveryEventFace(ev.event_type)}
                            {ev.operator_ref ? ` · ${ev.operator_ref}` : ""}
                            {ev.device_ref ? ` · 设备 ${ev.device_ref}` : ""}
                            {ev.occurred_at ? ` · ${ev.occurred_at}` : ""}
                          </p>
                        ))
                      ) : (
                        <p className="tk-note">还没有事实记录。</p>
                      )}
                      {deliveryDetail.replacements?.length ? (
                        deliveryDetail.replacements.map((rep, index) => (
                          <p className="tk-note" key={index}>{replaceReasonFace(rep.reason_category)}{rep.old_entry_revoked === 1 ? "(旧入口已停用)" : ""}</p>
                        ))
                      ) : null}
                      <button className="tk-quiet" type="button" data-action="close-delivery-detail" onClick={() => setDeliveryDetail(null)}>收起</button>
                    </div>
                  ) : null}
                  {replaceTarget?.id === dlv.id ? (
                    <div className="tk-card" data-state="delivery-replace">
                      <label className="tk-label">
                        补换原因
                        <select className="tk-select" data-field="replace-reason" value={replaceReason} onChange={(event) => setReplaceReason(event.target.value)}>
                          <option value="">选补换原因</option>
                          {replaceReasonList.map((reason) => (
                            <option key={reason} value={reason}>{replaceReasonFace(reason)}</option>
                          ))}
                        </select>
                      </label>
                      {replaceReason === "lost_uncontrolled" ? (
                        <label className="tk-label">
                          新入口(触点)
                          <select className="tk-select" data-field="replace-touchpoint" value={replaceTouchpointId} onChange={(event) => setReplaceTouchpointId(event.target.value)}>
                            <option value="">选新触点(旧入口会停用)</option>
                            {items.filter((other) => other.id !== dlv.touchpoint_id && other.status === "active").map((other) => (
                              <option key={other.id} value={other.id}>{other.label || other.tag_label || other.code}</option>
                            ))}
                          </select>
                        </label>
                      ) : null}
                      <label className="tk-label">
                        新物理卡编号(可选)
                        <input className="tk-input" data-field="replace-unit" value={replaceUnit} onChange={(event) => setReplaceUnit(event.target.value)} placeholder="如:unit-002" />
                      </label>
                      <button className="tk-quiet" type="button" data-action="confirm-replace" onClick={() => void submitReplace()}>确认补换</button>
                      <button className="tk-quiet" type="button" data-action="cancel-replace" onClick={() => setReplaceTarget(null)}>取消</button>
                    </div>
                  ) : null}
                </div>
              ) : (
                <div className="tk-card" data-state="delivery-empty">
                  <p className="tk-note">尚无实体交付记录。二维码可独立使用；实体卡需关联原已付款硬件订单，再记录出库、写卡、回读与验收。</p>
                  {ordersNote ? <p className="tk-note">{ordersNote}</p> : null}
                  {hardwareOrders.length > 0 && tp.status === "active" ? <>
                    <button className="tk-quiet" type="button" data-action="open-delivery" onClick={() => {setDeliveryTarget(tp.id);setDeliveryOrder("");setDeliveryUnit("");}}>关联硬件交付</button>
                    {deliveryTarget === tp.id ? <>
                      <label className="tk-label">已付款硬件订单<select className="tk-select" data-field="delivery-order" value={deliveryOrder} onChange={event => setDeliveryOrder(event.target.value)}><option value="">选择原订单</option>{hardwareOrders.map(order => <option key={order.id} value={order.id}>{order.name}</option>)}</select></label>
                      <label className="tk-label">实体卡编号（取得实物后填写，可选）<input className="tk-input" data-field="delivery-unit" value={deliveryUnit} onChange={event => setDeliveryUnit(event.target.value)} /></label>
                      <p className="tk-note">这里只关联订单，不表示服务已开通或实体卡已验证。后续设备与证据须由实际操作者提供。</p>
                      <button className="tk-quiet" type="button" data-action="create-delivery" disabled={!deliveryOrder} onClick={() => void createDelivery(tp)}>确认关联原订单</button>
                    </> : null}
                  </> : !ordersNote ? <p className="tk-note">暂无付款事实已确认的硬件订单。请有账单权限的负责人在本商家账单核对原订单，付款状态未知时不要重复购买。</p> : null}
                  {billingReadable ? <a className="tk-quiet" href="/work/billing">查看本商家账单</a> : null}
                </div>
              )}
            </article>
          );
        })
      )}
    </section>
  );
}
