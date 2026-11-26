"use client";
import { useEffect, useMemo, useState, type ComponentType } from "react";
import { useParams, useSearchParams, useRouter } from "next/navigation";
import { useSession, failureText } from "@/components/work/merchant-session";
import StoreChannelsPage from "@/app/work/stores/[id]/channels/page";
import StoreServicesPage from "@/app/work/stores/[id]/services/page";
import StorefrontPageDesk from "@/components/work/storefront/storefront-page-desk";
import { TouchpointsPanel } from "@/components/work/touchpoints/touchpoints-panel";
import { StoreContentDrawer } from "@/components/work/store-content-drawer";
import { StoreWorkspaceContext } from "@/components/work/store-workspace-context";
import { workspaceHref, workspacePanel, type WorkspacePanel } from "@/lib/work/store-workspace";
import { sanitizeWorkReturnPath } from "@/lib/work/workbench-shell";

// 兼容内容组现有名称；新增可选合同由统一编辑器消费，采用仍需选择服务绑定并发布。
const ContentEditor = StoreContentDrawer as ComponentType<{ storeId: string; storeName?: string; purpose?: string; returnHref?: string; onAdopted?: () => void }>;
export default function StoreWorkspace() {
  const { id: storeId } = useParams<{ id: string }>();
  const search = useSearchParams();
  const router = useRouter();
  const session = useSession();
  const [store, setStore] = useState<{id:string;name:string}|null>(null);
  const [error, setError] = useState("");
  const [refresh, setRefresh] = useState(0);
  const [visitedEntrances, setVisitedEntrances] = useState(false);
  const [visited, setVisited] = useState<WorkspacePanel[]>([]);
  const entrances = search.get("tab") === "entrances";
  const panel = workspacePanel(search.get("panel"));
  const returnOrigin = sanitizeWorkReturnPath(search.get("return"));
  const hrefWithReturn = (next: WorkspacePanel | "entrances" = "page") => {
    const base = workspaceHref(storeId, next);
    return returnOrigin ? `${base}&return=${encodeURIComponent(returnOrigin)}` : base;
  };
  useEffect(() => { if (entrances) setVisitedEntrances(true); if (!entrances) setVisited(old => old.includes(panel) ? old : [...old, panel]); }, [panel, entrances]);
  useEffect(() => {
    let alive = true;
    setStore(null); setError("");
    void session.api("GET", "stores").then(res => {
      if (!alive) return;
      if (!res.ok) { setError(failureText(res.status,res.data)); return; }
      const item = (Array.isArray(res.data.items) ? res.data.items : []).find((row: {id:string}) => row.id === storeId);
      if (!item) { setError("这家门店不可用，或你没有管理权限。请返回门店列表选择有权门店。"); return; }
      setStore(item);
    }).catch(() => { if (alive) setError("没有读到门店，请刷新后重试。"); });
    return () => { alive = false; };
  }, [session, storeId]);
  const context = useMemo(() => ({storeId,refresh,saved:()=>setRefresh(x=>x+1),open:(next:WorkspacePanel)=>router.replace(hrefWithReturn(next),{scroll:false})}), [storeId,refresh,router,returnOrigin]);
  if (error) return <main><h1 className="tk-title">门店工作区</h1><p className="tk-warn" role="alert">{error}</p><a href={returnOrigin || "/work/stores?manage=1"}>{returnOrigin ? "返回原位置" : "返回门店列表"}</a></main>;
  if (!store) return <p className="tk-state" data-state="loading">正在读取本店工作区</p>;
  return <StoreWorkspaceContext.Provider value={context}>
    <header><h1 className="tk-title">{store.name}</h1><p className="tk-lead">在本店配置服务、预览发布顾客页，再管理桌卡和二维码。无需先建推广活动。</p><div className="tk-row tk-gap">{returnOrigin ? <a className="tk-quiet" data-action="return-origin" href={returnOrigin}>返回原位置</a> : null}<a className="tk-quiet" href="/work/stores?manage=1">切换或管理门店</a></div></header>
    <nav className="tk-row tk-gap" aria-label="门店工作区">
      <button className="tk-button" aria-pressed={!entrances} onClick={()=>router.replace(hrefWithReturn("page"),{scroll:false})}>顾客页与服务</button>
      <button className="tk-button" aria-pressed={entrances} onClick={()=>router.replace(hrefWithReturn("entrances"),{scroll:false})}>投放入口</button>
    </nav>
    <section hidden={entrances}>
      <nav className="tk-row tk-gap tk-section" aria-label="顾客页配置步骤">
        {([["channels","添加服务"],["services","Wi-Fi / 微信 / 社群"],["content","添加内容"],["page","预览与发布"]] as [WorkspacePanel,string][]).map(([key,label])=><button key={key} className="tk-quiet" aria-pressed={panel===key} onClick={()=>context.open(key)}>{label}</button>)}
      </nav>
      {/* 保留已打开组件的挂载与本地草稿，面板切换不会丢掉修改。租户/门店变化由宿主key清理。 */}
      <section hidden={panel!=="channels"}>{visited.includes("channels") ? <StoreChannelsPage /> : null}</section>
      <section hidden={panel!=="services"}>{visited.includes("services") ? <StoreServicesPage /> : null}</section>
      <section hidden={panel!=="content"}>{visited.includes("content") ? <ContentEditor storeId={storeId} storeName={store.name} purpose="门店顾客页" returnHref={hrefWithReturn("channels")} onAdopted={() => {context.saved();context.open("channels");}} /> : null}</section>
      <section hidden={panel!=="page"}>{visited.includes("page") ? <StorefrontPageDesk /> : null}</section>
    </section>
    <section hidden={!entrances}>{visitedEntrances ? <TouchpointsPanel storeId={storeId} /> : null}</section>
  </StoreWorkspaceContext.Provider>;
}
