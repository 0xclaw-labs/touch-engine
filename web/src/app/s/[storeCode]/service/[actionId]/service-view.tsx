"use client";

// HUI-1896 C3 门店服务详情(/s/{storeCode}/service/{actionId}):按 actionId
// 复用面板的可地址化宿主。仅在顾客需要时打开(点评/关注/Wi-Fi/微信/团购
// 等承接面板);直接地址栏打开即可用,无 referer 依赖。
//
// 纪律(票面/裁定书/4589 归属):
//   - 宿主只管「找到动作实例 + 路线状态 + 分派面板」;平台专属服务内容归
//     4589(services 端点),团购商品归 4590——端点未在基线时给「暂未开通
//     在线展示」待办态,不伪建入口/素材/密码;
//   - 点评纪律:只帮顾客找到正确平台门店自主评价;零模板、零 AIGC、零截图
//     核验、无领奖暗示(评价不作奖励触发);
//   - Wi-Fi 密码只展示+主动复制,不强制留资后才看;连接失败回手动指引;
//   - 一切「已关注/已连接/已添加」无官方回执绝不记录为成功;
//   - 3535 能力渲染环境差异;无生态导航、不强制注册/留资/下载。

import { Suspense, useCallback, useEffect, useState } from "react";
import { useParams } from "next/navigation";
import { publicVisitorColumns } from "@/lib/visitor-experience";
import { detectEnvironment, methodLabel, parseActionCapabilities, pickRoute, type ActionCapabilitiesView } from "@/lib/env-capability";
import { parseServices, parseStorefront, type ServiceItemView, type StorefrontActionView, type StorefrontView } from "@/lib/storefront-view";

type FetchState = "loading" | "ready" | "not_found" | "network_error";

async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}

export function ServiceView() {
  return (
    <Suspense fallback={<main className="tk-public" data-state="loading"><p className="tk-note">正在读取服务</p></main>}>
      <ServiceViewInner />
    </Suspense>
  );
}

function ServiceViewInner() {
  const params = useParams<{ storeCode: string; actionId: string }>();
  const storeCode = params?.storeCode ?? "";
  const actionId = params?.actionId ?? "";

  const [store, setStore] = useState<StorefrontView | null>(null);
  const [fetchState, setFetchState] = useState<FetchState>("loading");
  const [services, setServices] = useState<Map<string, ServiceItemView> | null>(null);
  const [caps, setCaps] = useState<ActionCapabilitiesView | null>(null);
  const [copyNote, setCopyNote] = useState("");
  const [reloadTick, setReloadTick] = useState(0);

  const environment = detectEnvironment(typeof navigator === "undefined" ? "" : navigator.userAgent);
  const width = usePageWidth();
  const columns = publicVisitorColumns(width);

  useEffect(() => {
    if (!storeCode || !actionId) return;
    let alive = true;
    setFetchState("loading");
    fetch(`/api/public/storefronts/${encodeURIComponent(storeCode)}`)
      .then(async (res) => {
        if (!alive) return;
        const data = res.ok ? await res.json().catch(() => null) : null;
        const parsed = data ? parseStorefront(data) : null;
        if (parsed) {
          setStore(parsed);
          setFetchState("ready");
        } else {
          setFetchState("not_found");
        }
      })
      .catch(() => {
        if (alive) setFetchState("network_error");
      });
    // 4589 服务面(基线未提供 → 404 → null → 待办态,不伪建)。
    fetch(`/api/public/storefronts/${encodeURIComponent(storeCode)}/services`)
      .then(async (res) => {
        if (!alive) return;
        const data = res.ok ? await res.json().catch(() => null) : null;
        if (alive) setServices(data ? parseServices(data) : null);
      })
      .catch(() => undefined);
    // 3535 路线(实例 id 直查;读不到按未知路线处理)。
    fetch(`/api/public/actions/${encodeURIComponent(actionId)}/capabilities?environment=${encodeURIComponent(environment)}`)
      .then(async (res) => {
        if (!alive) return;
        const data = res.ok ? await res.json().catch(() => null) : null;
        if (alive) setCaps(data ? parseActionCapabilities(data) : null);
      })
      .catch(() => undefined);
    return () => {
      alive = false;
    };
  }, [actionId, environment, storeCode, reloadTick]);

  const action: StorefrontActionView | null =
    store && fetchState === "ready" ? store.actions.find((a) => a.actionId === actionId) ?? null : null;
  const routePick = caps ? pickRoute(caps.routes, environment) : null;
  const service = services?.get(actionId) ?? null;

  const doCopy = useCallback(async (label: string, value: string) => {
    if (value === "") return;
    setCopyNote((await copyText(value)) ? `${label}已复制。` : "复制没有完成,请长按文字手动复制。");
  }, []);

  const retry = useCallback(() => setReloadTick((t) => t + 1), []);

  return (
    <main
      data-testid="service-view"
      data-pattern="public_consumer"
      data-columns={columns}
      data-viewport={width}
      data-state={action ? action.kind : fetchState}
      className="tk-public"
    >
      <div className="tk-public-card">
        {fetchState === "loading" && <p className="tk-note">正在读取…</p>}
        {fetchState === "network_error" && (
          <div>
            <p className="tk-note">网络异常,请检查网络后重试。</p>
            <button type="button" className="tk-quiet" onClick={retry}>重试</button>
          </div>
        )}
        {fetchState === "not_found" && (
          <div data-testid="service-missing">
            <h1 className="tk-public-title">这个入口不存在或暂未开放</h1>
            <p className="tk-note">请核对二维码或碰一碰标签,或联系门店工作人员。</p>
          </div>
        )}
        {fetchState === "ready" && !action && (
          <div data-testid="service-missing">
            <h1 className="tk-public-title">这个入口不存在或暂未开放</h1>
            <p className="tk-note">这家门店当前没有提供这一项在线服务。</p>
          </div>
        )}

        {action && (
          <>
            <header>
              <p className="tk-public-merchant">{store?.store.name}</p>
              <h1 className="tk-public-title">{action.label || "门店服务"}</h1>
            </header>

            {routePick && routePick.kind !== "ok" && (
              <p className="tk-warn" data-testid="service-route-note">{routePick.face}</p>
            )}
            {routePick?.kind === "ok" && (
              <p className="tk-note" data-testid="service-route-method">当前设备路线:{methodLabel(routePick.route.method)}。</p>
            )}

            {action.kind === "review" && (
              <section className="tk-section" data-testid="service-review">
                <h2 className="tk-section-title">给门店点评</h2>
                <p className="tk-note">
                  这里帮你找到这家店在平台上的正确页面;评价请你用自己的话自主完成,是否评价、怎么评价都由你决定。
                </p>
                {service ? (
                  <p className="tk-note">{service.note || "打开平台页面后搜索本店即可评价。"}</p>
                ) : (
                  <p className="tk-warn">平台页面入口暂未开通在线展示,请向店员索取,或到平台搜索本店名称。</p>
                )}
              </section>
            )}

            {action.kind === "follow" && (
              <section className="tk-section" data-testid="service-follow">
                <h2 className="tk-section-title">关注我们</h2>
                {service ? (
                  <p className="tk-note">{service.note || "打开平台主页后点关注即可。"}</p>
                ) : (
                  <p className="tk-warn">平台主页入口暂未开通在线展示,请向店员索取,或到平台搜索本店名称。</p>
                )}
                <p className="tk-note">关注与否由你决定;这里不会自动关注,也不会记录你已关注。</p>
              </section>
            )}

            {action.kind === "wifi" && (
              <section className="tk-section" data-testid="service-wifi">
                <h2 className="tk-section-title">门店 Wi-Fi</h2>
                {service ? (
                  <>
                    <p className="tk-note">网络名称:{service.field("ssid") || "请向店员索取"}</p>
                    <p className="tk-note">
                      密码:<code data-testid="wifi-password">{service.field("password") || "请向店员索取"}</code>
                    </p>
                    {service.field("password") !== "" && (
                      <button type="button" className="tk-quiet" data-testid="wifi-copy" onClick={() => doCopy("密码", service.field("password"))}>
                        复制密码
                      </button>
                    )}
                    <p className="tk-note">连不上时,请向店员索取最新密码,或按手机系统提示手动输入。</p>
                  </>
                ) : (
                  <p className="tk-warn">Wi-Fi 信息暂未开通在线展示,请向店员索取。</p>
                )}
              </section>
            )}

            {action.kind === "contact" && (
              <section className="tk-section" data-testid="service-contact">
                <h2 className="tk-section-title">加微信</h2>
                {service ? (
                  <>
                    <p className="tk-note">微信号:<code data-testid="contact-wechat">{service.field("wechat_id") || "请向店员索取"}</code></p>
                    {service.field("wechat_id") !== "" && (
                      <button type="button" className="tk-quiet" data-testid="contact-copy" onClick={() => doCopy("微信号", service.field("wechat_id"))}>
                        复制微信号
                      </button>
                    )}
                    <p className="tk-note">是否添加由你决定;这一步不会自动添加好友,也不会记录你已添加。</p>
                  </>
                ) : (
                  <p className="tk-warn">联系方式暂未开通在线展示,请向店员索取。</p>
                )}
              </section>
            )}

            {(action.kind === "groupbuy" || action.kind === "share" || action.kind === "join_group") && (
              <section className="tk-section" data-testid="service-groupbuy">
                <h2 className="tk-section-title">{action.kind === "groupbuy" ? "团购套餐" : "社群"}</h2>
                {action.kind === "groupbuy" ? (
                  <p className="tk-note">套餐与价格以平台页面为准;下单在平台完成,这里不下单、不收款。</p>
                ) : service ? (
                  <p className="tk-note">群号:{service.field("group_number") || "请向店员索取"}{service.note ? `。${service.note}` : ""}</p>
                ) : (
                  <p className="tk-warn">社群信息暂未开通在线展示,请向店员索取。</p>
                )}
              </section>
            )}

            {copyNote && <p className="tk-note" data-testid="copy-note">{copyNote}</p>}

            <footer className="tk-row tk-gap">
              <a className="tk-quiet" href={`/s/${encodeURIComponent(storeCode)}`} data-testid="back-to-store">返回门店</a>
            </footer>
          </>
        )}
      </div>
    </main>
  );
}

function usePageWidth(): number {
  const [width, setWidth] = useState(390);
  useEffect(() => {
    const apply = () => setWidth(window.innerWidth || 390);
    apply();
    window.addEventListener("resize", apply);
    return () => window.removeEventListener("resize", apply);
  }, []);
  return width;
}
