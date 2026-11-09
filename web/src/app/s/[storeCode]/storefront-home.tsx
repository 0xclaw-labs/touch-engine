"use client";

// HUI-1896 C1 门店首页(/s/{storeCode}):先进入门店,按意图使用服务。
//
// 顺序:门店身份 → 现在能得到什么 → 可做的动作(意图分组) → 可选活动卡 →
// 团购套餐区(端点在基线尚未提供,读不到整区不渲染) → 我的权益/隐私入口。
//
// 纪律(裁定书/简报硬红线):
//   - 公共面无 EcoTopNav、无生态导航、不推销派诺工具、不强制注册/留资/下载;
//   - 未配置动作不渲染(服务端白名单面 + 空组不出现);disabled 门店动作区
//     收起,只给门店提示;
//   - 活动卡可选:无合法活动码不给深链,基础服务不依赖活动;活动深链保留
//     既有 /c/{code} 兼容(C4);
//   - 浏览不建参与、不占库存、不触发任何收费;
//   - 弱网/断网给重试;未知状态按未知(不冒充可用)。

import { Suspense, useCallback, useEffect, useState } from "react";
import { useParams, useSearchParams } from "next/navigation";
import { classifyEntry, failureState } from "@/lib/public-state";
import { publicVisitorCopy } from "@/lib/account-separation";
import { publicVisitorColumns } from "@/lib/visitor-experience";
import {
  activityCards,
  groupActions,
  offerEntry,
  offersView,
  parseStorefront,
  storeStateFace,
  type OfferItem,
  type StorefrontView,
} from "@/lib/storefront-view";

type HomeState = "loading" | "available" | "not_found" | "network_error";

interface OfferPayload {
  items?: unknown;
}

export function StorefrontHome() {
  return (
    <Suspense fallback={<main className="tk-public" data-state="loading"><p className="tk-note">正在读取门店</p></main>}>
      <StorefrontHomeInner />
    </Suspense>
  );
}

function StorefrontHomeInner() {
  const params = useParams<{ storeCode: string }>();
  const storeCode = params?.storeCode ?? "";
  const searchParams = useSearchParams();
  const entry = classifyEntry(searchParams.get("entry"));

  const [view, setView] = useState<StorefrontView | null>(null);
  const [offers, setOffers] = useState<OfferItem[] | null>(null);
  const [uiState, setUiState] = useState<HomeState>("loading");
  const [reloadTick, setReloadTick] = useState(0);

  useEffect(() => {
    if (!storeCode || entry === "unsupported") return;
    let alive = true;
    setUiState("loading");
    fetch(`/api/public/storefronts/${encodeURIComponent(storeCode)}`)
      .then(async (res) => {
        if (!alive) return;
        if (res.status === 502 || res.status === 503 || res.status === 504) {
          setUiState(failureState(res.status) === "network_error" ? "network_error" : "not_found");
          return;
        }
        const data = await res.json().catch(() => null);
        const parsed = res.ok ? parseStorefront(data) : null;
        if (parsed) {
          setView(parsed);
          setUiState("available");
        } else {
          setUiState("not_found");
        }
      })
      .catch(() => {
        if (alive) setUiState("network_error");
      });
    return () => {
      alive = false;
    };
  }, [storeCode, entry, reloadTick]);

  // 团购套餐区(4590;端点未在基线时 404 → offers=null → 整区不渲染,不伪建)。
  useEffect(() => {
    if (!storeCode || uiState !== "available") return;
    let alive = true;
    fetch(`/api/public/storefronts/${encodeURIComponent(storeCode)}/offers`)
      .then(async (res) => {
        if (!alive) return;
        const data = res.ok ? ((await res.json().catch(() => null)) as OfferPayload | null) : null;
        const parsed = data ? offersView(data) : null;
        if (alive) setOffers(parsed && parsed.length > 0 ? parsed : null);
      })
      .catch(() => {
        if (alive) setOffers(null);
      });
    return () => {
      alive = false;
    };
  }, [storeCode, uiState]);

  const retry = useCallback(() => {
    setUiState("loading");
    setReloadTick((t) => t + 1);
  }, []);

  const width = usePageWidth();
  const columns = publicVisitorColumns(width);
  const visitorNote = publicVisitorCopy({ action: "browse" });
  const cards = view ? activityCards((view as unknown as { activity_cards?: unknown }).activity_cards) : [];
  const groups = view ? groupActions(view.storeCode, view.actions) : [];
  const storeNotice = view ? storeStateFace(view.store.state) : "";
  const actionsOpen = view?.store.state === "active";

  return (
    <main
      data-testid="storefront-home"
      data-pattern="public_consumer"
      data-columns={columns}
      data-viewport={width}
      data-state={uiState}
      className="tk-public"
    >
      <div className="tk-public-card">
        {view && (
          <header data-testid="store-identity">
            <p className="tk-public-merchant">{view.store.name}</p>
            {view.store.address && <p className="tk-note">{view.store.address}</p>}
            {view.store.openingHours && <p className="tk-note">营业时间:{view.store.openingHours}</p>}
          </header>
        )}
        <p data-testid="visitor-note" className="tk-note">{visitorNote.text}</p>

        {uiState !== "available" && (
          <div data-testid="degraded-state">
            <h1 className="tk-public-title">{uiState === "loading" ? "正在读取门店…" : uiState === "not_found" ? "门店不存在或暂未开放" : "网络异常,门店加载失败"}</h1>
            <p className="tk-note">
              {uiState === "not_found"
                ? "请核对二维码或碰一碰标签,或联系门店工作人员。"
                : uiState === "network_error"
                  ? "请检查网络后重试。"
                  : ""}
            </p>
            {uiState === "network_error" && (
              <button type="button" onClick={retry} className="tk-quiet" data-testid="storefront-retry">
                重试
              </button>
            )}
          </div>
        )}

        {uiState === "available" && view && (
          <>
            {storeNotice && (
              <p className="tk-warn" data-testid="store-closed">{storeNotice}</p>
            )}

            {actionsOpen && groups.length > 0 && (
              <div data-testid="storefront-actions" className="tk-gap">
                {groups.map((group) => (
                  <section key={group.intent} className="tk-section" data-testid={`storefront-group-${group.intent}`}>
                    <h2 className="tk-section-title">{group.title}</h2>
                    <div className="tk-row">
                      {group.items.map(({ action, href }) => (
                        href ? (
                          <a key={action.actionId} className="tk-quiet" data-testid={`store-action-${action.actionId}`} href={href}>
                            {action.label || group.title}
                          </a>
                        ) : (
                          <span key={action.actionId} className="tk-note">{action.label}</span>
                        )
                      ))}
                    </div>
                  </section>
                ))}
              </div>
            )}

            {cards.length > 0 && (
              <section className="tk-section" data-testid="activity-cards">
                <h2 className="tk-section-title">门店活动</h2>
                <div className="tk-row tk-gap">
                  {cards.map((card) => (
                    <div key={card.cardId} className="tk-gap" data-testid={`activity-card-${card.cardId}`}>
                      <p className="tk-lead">{card.title}</p>
                      <p className="tk-note">{card.stateFace}</p>
                      {card.link ? (
                        <a className="tk-primary" href={card.link}>看这次活动</a>
                      ) : (
                        <p className="tk-note">这次活动暂不支持在线参与,详情请到店咨询。</p>
                      )}
                    </div>
                  ))}
                </div>
              </section>
            )}

            {offers && (
              <section className="tk-section" data-testid="store-offers">
                <h2 className="tk-section-title">团购套餐</h2>
                <div className="tk-gap">
                  {offers.map((offer) => {
                    const entryInfo = offerEntry(offer);
                    return (
                      <div key={offer.offerId} data-testid={`offer-${offer.offerId}`} className="tk-gap">
                        <p className="tk-lead">{offer.title}</p>
                        <p className="tk-note">
                          {offer.platform ? `平台:${offer.platform}。` : ""}
                          {offer.priceNote ? `价格参考:${offer.priceNote}。` : ""}
                          {entryInfo.kind !== "platform" ? "以平台页面实际价格为准。" : ""}
                        </p>
                        {entryInfo.href ? (
                          <a className="tk-quiet" href={entryInfo.href} target="_blank" rel="noopener noreferrer">{entryInfo.text}</a>
                        ) : (
                          <p className="tk-note">{entryInfo.text}</p>
                        )}
                      </div>
                    );
                  })}
                </div>
              </section>
            )}

            <footer className="tk-row tk-gap" data-testid="storefront-footer">
              <a className="tk-quiet" data-testid="benefits-link" href={`/s/${encodeURIComponent(view.storeCode)}/benefits`}>我的权益与凭证</a>
              <a className="tk-quiet" data-testid="privacy-link" href={`/s/${encodeURIComponent(view.storeCode)}/privacy`}>隐私与授权</a>
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
