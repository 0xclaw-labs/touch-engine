"use client";

// HUI-4593 A1 代理工作台(/agency)。
//
// 纪律(裁定书 + 票面开发步骤):
//   - 商户列表 = 激活代管关系(服务端按代理主体单点过滤,已撤销立即不可见)
//     × 名下成员行(名称来自 /api/session/memberships);不新建权限体系,
//     scope 全部读自 0064 关系列与 /api/v1/access/effective;
//   - 受控进入/返回:进入 = 既有 switchTenant 单缝(成员行+激活关系双门槛,
//     切换即清上一户数据),就绪后跳 M1(/);返回 = 回本控制台;
//   - 门店配置入口:先按该商户租户读门店(代理 read_list 既有门槛),
//     再带着租户上下文进入 /work/stores;配置权以服务端裁决为准,
//     未获授权的动作如实标注「需商家授予」;
//   - 开通/续费协助:只判资格(renewal grant)与展示诚实口径——发起与支付
//     由平台订阅/资金权威统一裁决,本部署未接入收费通道,不伪造开通成功;
//   - 硬件交付/结算:真实依赖未接入,显示诚实空态与归属说明,零伪造状态。

import { useCallback, useEffect, useState } from "react";
import { failureText, useMerchantSession, type MerchantSession } from "@/components/work/merchant-session";
import { AgentBanner, ACTION_LABELS, type AccessEffective, type AgencyRel } from "@/components/work/settings/settings-desk";
import { surfaceLabel } from "@/lib/product-finish";
import { SharedLogin, type SignInBridge } from "@/components/login/SharedLogin";

interface MembershipScope {
  tenant_id: string;
  display_name: string;
  role: string;
  enabled: boolean;
  source: string;
}

interface StoreRec {
  id: string;
  name: string;
}

// 修复轮 2(F1):/agency/relations/granted 的卡片 —— 主体视角跨租户全集,
// 含 grantor 租户名与代付状态(行集由服务端按调用主体收口)。
interface GrantedRelation extends AgencyRel {
  tenant_name: string;
  payer_granted: boolean;
}

export function AgencyConsole() {
  const session = useMerchantSession();
  if (session.phase === "loading") {
    return <main className="tk-page"><p className="tk-state" data-state="loading">{surfaceLabel("loading")}</p></main>;
  }
  if (session.phase === "error") {
    return (
      <main className="tk-page">
        <p className="tk-state tk-danger" data-state="error">
          {session.error}
          <button className="tk-quiet" type="button" data-action="retry" onClick={session.retryConnection}>重试</button>
        </p>
      </main>
    );
  }
  if (session.phase === "anonymous") return <AgencyLogin session={session} />;
  // choose / none / ready 都进控制台:商户列表本身就是代理的组织选择器。
  return <AgencyDesk session={session} />;
}

function AgencyLogin({ session }: { session: MerchantSession }) {
  const signIn: SignInBridge = (email, password) =>
    session.login(email, password).then(() => ({}));
  return (
    <main className="tk-page">
      <h1 className="tk-title">代理工作台</h1>
      <p className="tk-lead">登录后查看你代管的商家，受控进入他们的工作台。</p>
      <SharedLogin signIn={signIn} />
    </main>
  );
}

function AgencyDesk({ session }: { session: MerchantSession }) {
  const [phase, setPhase] = useState<"loading" | "error" | "empty" | "ready">("loading");
  const [error, setError] = useState("");
  const [merchants, setMerchants] = useState<{ rel: GrantedRelation; name: string }[]>([]);
  const [enterError, setEnterError] = useState("");
  const [insideName, setInsideName] = useState("");
  const [access, setAccess] = useState<AccessEffective | null>(null);
  const [drawer, setDrawer] = useState<{ tenantId: string; name: string; stores: StoreRec[]; loading: boolean } | null>(null);

  const load = useCallback(async () => {
    setPhase("loading");
    // 成员行只用来选会话租户上下文(任何自己所属租户皆可);
    // 商户全集来自 /agency/relations/granted —— 服务端按【调用主体】收口的
    // 跨租户激活代管关系(修复轮 2/F1:当前租户上下文≠授权商户时同样显示)。
    const memRes = await fetch("/api/session/memberships");
    if (!memRes.ok) {
      setError(failureText(memRes.status, {}));
      setPhase("error");
      return;
    }
    const memData = (await memRes.json().catch(() => ({}))) as { items?: MembershipScope[] };
    const scopes = (memData.items ?? []).filter((m) => m.enabled && m.source === "membership");
    if (scopes.length === 0) {
      setPhase("empty");
      return;
    }
    const relRes = await fetch("/api/agency/relations/granted", { headers: { "x-tenant-id": scopes[0].tenant_id } });
    if (!relRes.ok) {
      setError(failureText(relRes.status, {}));
      setPhase("error");
      return;
    }
    const relData = (await relRes.json().catch(() => ({}))) as { items?: GrantedRelation[] };
    const granted = (relData.items ?? []).filter((r) => !r.revoked_at);
    setMerchants(granted.map((rel) => ({ rel, name: rel.tenant_name || rel.tenant_id })));
    setPhase(granted.length === 0 ? "empty" : "ready");
  }, []);

  useEffect(() => {
    void load().catch(() => {
      setError("没有连上服务。请稍后重试。");
      setPhase("error");
    });
  }, [load]);

  // 已进入某商户(会话就绪且有工作区租户):拉裁决读面并显示横幅与返回。
  useEffect(() => {
    if (session.phase !== "ready" || !session.tenantId) return;
    let cancelled = false;
    setInsideName(session.tenantName);
    void session.api("GET", "access/effective").then((res) => {
      if (!cancelled && res.ok) setAccess(res.data as unknown as AccessEffective);
    });
    return () => {
      cancelled = true;
    };
  }, [session]);

  // 受控进入:switchTenant 单缝(成员行+激活关系双门槛),就绪后跳 M1。
  async function enter(tenantId: string) {
    if (!merchants.some((m) => m.rel.tenant_id === tenantId)) {
      setEnterError("只能进入已授权商户。");
      return;
    }
    setEnterError("");
    session.switchTenant(tenantId);
    window.location.href = "/"; // M1:今天要做的事
  }

  // 门店配置入口:按该商户租户读门店(代理只读门槛),展开抽屉。
  async function openStores(tenantId: string, name: string) {
    setDrawer({ tenantId, name, stores: [], loading: true });
    const res = await fetch("/api/stores", { headers: { "x-tenant-id": tenantId } });
    if (!res.ok) {
      setDrawer({ tenantId, name, stores: [], loading: false });
      setEnterError(failureText(res.status, {}));
      return;
    }
    const data = (await res.json().catch(() => ({}))) as { items?: StoreRec[] };
    setDrawer({ tenantId, name, stores: data.items ?? [], loading: false });
  }

  function scopeHints(rel: AgencyRel): string[] {
    const held = rel.allowed_actions ?? [];
    const missing = Object.keys(ACTION_LABELS).filter((a) => !held.includes(a));
    if (held.length === 0) {
      return ["未获配置授权：只能只读与开子账号（商家可在「成员与权限」里授予具体动作）"];
    }
    return missing.map((a) => `未获${ACTION_LABELS[a]}授权（需商家授予）`);
  }

  const inside = session.phase === "ready" && Boolean(session.tenantId);
  return (
    <main className="tk-page">
      <h1 className="tk-title">代理工作台</h1>
      <p className="tk-lead">这里只列已授权的商家。进入后你的一切动作仍以商家授予的范围为准。</p>
      {enterError ? <p className="tk-warn" data-role="notice">{enterError}</p> : null}

      {inside ? (
        <section className="tk-section" data-section="inside-banner">
          <p data-field="back-row">
            当前位于 <strong>{insideName || "商户工作台"}</strong>。
            <a className="tk-quiet" href="/agency" data-action="back-console">返回商户列表</a>
          </p>
          <AgentBanner tenantName={insideName} access={access} />
        </section>
      ) : null}

      {phase === "loading" ? <p className="tk-state" data-state="loading">{surfaceLabel("loading")}</p> : null}
      {phase === "error" ? (
        <p className="tk-state tk-danger" data-state="error">
          {error}
          <button className="tk-quiet" type="button" data-action="retry" onClick={() => void load()}>重试</button>
        </p>
      ) : null}
      {phase === "empty" ? <p className="tk-state" data-state="empty">还没有商家授权你代管。授权建立后这里会列出商家。</p> : null}

      {phase === "ready" ? (
        <section className="tk-section" data-section="merchants">
          <h2 className="tk-section-title">授权商户</h2>
          <ul className="tk-list">
            {merchants.map(({ rel, name }) => (
              <li key={rel.id} data-role="merchant-row">
                <strong>{name}</strong>
                <span className="tk-note"> {describeScope(rel)}</span>
                <span className="tk-note"> {rel.valid_until ? `授权至 ${rel.valid_until.slice(0, 10)}` : "长期授权"}</span>
                <span className="tk-note" data-field="payer">{rel.payer_grant_ref ? "已获代付授权" : "无代付授权"}</span>
                <div className="tk-row tk-gap">
                  <button className="tk-button" type="button" data-action="enter-merchant" onClick={() => void enter(rel.tenant_id)}>
                    进入工作台
                  </button>
                  <button className="tk-quiet" type="button" data-action="open-stores" onClick={() => void openStores(rel.tenant_id, name)}>
                    门店配置入口
                  </button>
                </div>
                {scopeHints(rel).length > 0 ? (
                  <p className="tk-note" data-field="scope-hints">{scopeHints(rel).join("；")}</p>
                ) : null}
              </li>
            ))}
          </ul>
        </section>
      ) : null}

      {drawer ? (
        <section className="tk-section" data-section="store-config">
          <h2 className="tk-section-title">门店配置入口 · {drawer.name}</h2>
          {drawer.loading ? <p className="tk-state" data-state="loading">{surfaceLabel("loading")}</p> : null}
          {!drawer.loading && drawer.stores.length === 0 ? (
            <p className="tk-state" data-state="empty">这家商户还没有门店。</p>
          ) : null}
          <ul className="tk-list">
            {drawer.stores.map((s) => (
              <li key={s.id} data-role="config-store-row">
                <strong>{s.name}</strong>
                <button
                  className="tk-quiet"
                  type="button"
                  data-action="go-store-config"
                  onClick={() => {
                    session.switchTenant(drawer.tenantId);
                    window.location.href = "/work/stores";
                  }}
                >
                  去配置
                </button>
              </li>
            ))}
          </ul>
          <p className="tk-note">配置权限以商家授予为准；未获授权的动作在工作台内会被明确拒绝并说明。</p>
          <button className="tk-quiet" type="button" data-action="close-stores" onClick={() => setDrawer(null)}>收起</button>
        </section>
      ) : null}

      <section className="tk-section" data-section="renewal">
        <h2 className="tk-section-title">开通与续费协助</h2>
        {merchants.length === 0 ? (
          <p className="tk-state" data-state="empty">授权商家后，这里按商户显示你可以协助的事项。</p>
        ) : (
          <ul className="tk-list">
            {merchants.map(({ rel, name }) => (
              <li key={rel.id} data-role="renewal-row">
                <strong>{name}</strong>
                <span className="tk-note">
                  {(rel.allowed_actions ?? []).includes("renewal")
                    ? "已获续费协助授权：可代发起开通/续费申请（资格已具备）"
                    : "未获续费协助授权（需商家授予「续费协助」）"}
                </span>
                <span className="tk-note" data-field="renewal-payer">
                  {rel.payer_grant_ref ? "付款授权：已授予（真实扣费仍由平台按付款授权裁决）" : "付款授权：未授予，收费动作不可代付"}
                </span>
              </li>
            ))}
          </ul>
        )}
        <p className="tk-note" data-field="renewal-authority">
          开通与续费的发起、支付与生效由平台订阅与资金权威统一裁决：真实服务端支付状态才开通。
          本部署尚未接入收费通道，这里不做任何代收代付。
        </p>
      </section>

      <section className="tk-section" data-section="hardware">
        <h2 className="tk-section-title">硬件交付状态</h2>
        <p className="tk-state" data-state="empty" data-field="hardware-empty">
          硬件交付状态归平台硬件交付域，本部署尚未接入。这里不做任何伪造状态；
          交付事实以平台交付记录为准。
        </p>
      </section>

      <section className="tk-section" data-section="settlement">
        <h2 className="tk-section-title">结算（只读）</h2>
        <p className="tk-state" data-state="empty" data-field="settlement-empty">
          结算账目归平台资金权威，本部署尚未接入只读查询。代理不产生多级返佣账，
          也不会看到商家钱包余额。
        </p>
      </section>
    </main>
  );
}

function describeScope(rel: AgencyRel): string {
  const held = rel.allowed_actions ?? [];
  if (held.length === 0) return "只读 + 开子账号";
  const labels = held.map((a) => ACTION_LABELS[a] ?? a);
  const scope = (rel.store_scope_set ?? []).length > 0 ? `限 ${(rel.store_scope_set ?? []).length} 家门店` : "全部门店";
  return `获准：${labels.join("、")}（${scope}）`;
}
