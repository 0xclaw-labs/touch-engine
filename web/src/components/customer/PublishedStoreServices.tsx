"use client";

import { useEffect, useState } from "react";
import { detectEnvironment } from "@/lib/env-capability";
import { parseServices, type ServiceItemView } from "@/lib/storefront-view";
import { panelKind } from "@/lib/store-service-panels";
import { StoreServicePanel } from "@/components/customer/StoreServicePanels";

// Store services use their published snapshot. Reading the homepage never
// starts a participation, creates a contact, or claims a connection succeeded.
export function PublishedStoreServices({ storeCode, listedActionIds }: { storeCode: string; listedActionIds: string[] }) {
  const [items, setItems] = useState<ServiceItemView[]>([]);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [retry, setRetry] = useState(0);
  const environment = detectEnvironment(typeof navigator === "undefined" ? "" : navigator.userAgent);
  useEffect(() => {
    let alive = true;
    setItems([]); setState("loading");
    void fetch(`/api/public/storefronts/${encodeURIComponent(storeCode)}/services?environment=${encodeURIComponent(environment)}`)
      .then(async (response) => {
        const parsed = response.ok ? parseServices(await response.json().catch(() => null)) : null;
        if (!alive) return;
        if (!parsed) { setState(response.status === 404 ? "ready" : "error"); return; }
        setItems([...parsed.values()]); setState("ready");
      }).catch(() => { if (alive) setState("error"); });
    return () => { alive = false; };
  }, [storeCode, environment, retry]);
  const visible = items.filter((item) => item.entry && panelKind(item.entry) && !listedActionIds.includes(item.actionId));
  if (state === "loading") return <p className="tk-note">正在读取门店服务…</p>;
  if (state === "error") return <section className="tk-section"><p className="tk-note">门店服务暂未读到，请重试，或向店员询问。</p><button type="button" className="tk-quiet" onClick={() => setRetry((value) => value + 1)}>重试门店服务</button></section>;
  if (!visible.length) return null;
  return <section className="tk-section" data-testid="published-store-services">
    <h2 className="tk-section-title">门店服务</h2>
    {visible.map((item) => <details key={item.actionId} className="tk-section" data-service-id={item.actionId}>
      <summary>{item.title || "查看门店服务"}</summary>
      <StoreServicePanel entry={item.entry!} environment={environment} storefrontCode={storeCode} />
    </details>)}
  </section>;
}
