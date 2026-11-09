"use client";

// HUI-4593 M13 成员与权限工作台。
//
// 纪律(裁定书):
//   - 四区:成员与权限 / 代理协作 / 模板下发 / 白标授权;agent 角色时顶部
//     「正在为谁工作」横幅(组织/权限/付款主体),切换组织即清空上一户数据
//     (merchant-session 既有单缝保证)。
//   - 授权/撤回/代管/下发全部操作必填原因(服务端审计;前端先挡一道)。
//   - 特权操作只对 org_owner 显示;权限不足按产品语句说明,不崩、不装成功。
//   - FEATURE_AUTHZ off(access/effective 404)时诚实空态,不伪造能力。

import { useCallback, useEffect, useState } from "react";
import { failureText, useSession } from "@/components/work/merchant-session";
import { surfaceLabel } from "@/lib/product-finish";

// export:A1 /agency 控制台复用同一裁决读面形状,不另造权限类型。
export interface AccessEffective {
  role: string;
  permissions: string[];
  allowed_store_ids: string[];
  relation: {
    ref: string;
    grant_revision: number;
    expires_at: string;
    payer_granted: boolean;
    allowed_actions: string[];
  } | null;
  ops_permissions: { command: string; store_scope_set: string[] }[];
}

interface MemberRec {
  id: string;
  principal_ref: string;
  role: string;
  store_scope: string;
  enabled: boolean;
  display_name: string;
}

// export:A1 /agency 控制台复用同一代管关系行形状(0064 scope 列)。
export interface AgencyRel {
  id: string;
  tenant_id: string;
  agent_principal: string;
  note: string;
  revoked_at: string;
  allowed_actions: string[];
  store_scope_set: string[];
  valid_until: string;
  grant_revision: number;
  payer_grant_ref: string;
}

interface StoreRec {
  id: string;
  name: string;
}

interface TemplateRec {
  id: string;
  store_id: string;
  template_ref: string;
  source_version: string;
  status: string;
  allowed_override_fields: string[];
}

// export:A1 /agency 控制台复用同一授权动作文案,不另造一套术语。
export const ACTION_LABELS: Record<string, string> = {
  storefront: "首页配置",
  action: "动作配置",
  channel_target: "渠道目标",
  renewal: "续费协助",
};

const ROLE_LABELS: Record<string, string> = {
  org_owner: "组织负责人",
  store_manager: "门店经理",
  staff: "店员",
  agent: "代理",
};

// newTemplateDraft starts ONE logical distribution request: its idempotency
// key stays fixed across failure retries and is only rotated after success.
function newTemplateDraft() {
  return { ref: "", version: "", picked: [] as string[], fields: "", reason: "", key: `dist-${Date.now()}` };
}

export function SettingsDesk() {
  const session = useSession();
  const owner = session.role === "org_owner";
  const agent = session.role === "agent";
  const [phase, setPhase] = useState<"loading" | "off" | "error" | "ready">("loading");
  const [loadError, setLoadError] = useState("");
  const [access, setAccess] = useState<AccessEffective | null>(null);
  const [stores, setStores] = useState<StoreRec[]>([]);
  const [members, setMembers] = useState<MemberRec[]>([]);
  const [relations, setRelations] = useState<AgencyRel[]>([]);
  const [templates, setTemplates] = useState<TemplateRec[]>([]);
  const [notice, setNotice] = useState("");

  const load = useCallback(async () => {
    setPhase("loading");
    const acc = await session.api("GET", "access/effective");
    if (acc.status === 404) {
      setPhase("off");
      return;
    }
    if (!acc.ok) {
      setLoadError(failureText(acc.status, acc.data));
      setPhase("error");
      return;
    }
    setAccess(acc.data as unknown as AccessEffective);
    const storesRes = await session.api("GET", "stores");
    if (storesRes.ok) {
      setStores((storesRes.data.items as StoreRec[]) ?? []);
    }
    if (owner) {
      const m = await session.api("GET", "admin/members");
      if (m.ok) setMembers((m.data.items as MemberRec[]) ?? []);
    }
    const rel = await session.api("GET", "agency/relations");
    if (rel.ok) setRelations((rel.data.items as AgencyRel[]) ?? []);
    const tas = await session.api("GET", "template-assignments");
    if (tas.ok) setTemplates((tas.data.items as TemplateRec[]) ?? []);
    setPhase("ready");
  }, [session, owner]);

  useEffect(() => {
    void load().catch(() => {
      setLoadError("没有连上服务。请稍后重试。");
      setPhase("error");
    });
  }, [load]);

  if (phase === "loading") return <p className="tk-state" data-state="loading">{surfaceLabel("loading")}</p>;
  if (phase === "error") {
    return (
      <p className="tk-state tk-danger" data-state="error">
        {loadError}
        <button className="tk-quiet" type="button" data-action="retry" onClick={() => void load()}>重试</button>
      </p>
    );
  }
  if (phase === "off") {
    return (
      <p className="tk-state" data-state="empty">
        成员与权限能力还没有对本组织开启。授权、代理协作和模板下发暂不可用。
      </p>
    );
  }
  return (
    <>
      <h1 className="tk-title">成员与权限</h1>
      <p className="tk-lead">谁进来、能做什么、代理替谁做——每一次授权和撤回都留痕可查。</p>
      {notice && phase === "ready" ? <p className="tk-warn" data-role="notice">{notice}</p> : null}
      {agent ? <AgentBanner tenantName={session.tenantName} access={access} /> : null}
      {owner ? <MembersSection members={members} stores={stores} reload={load} setNotice={setNotice} sessionApi={session.api} /> : null}
      {owner ? <AgencySection relations={relations} stores={stores} reload={load} setNotice={setNotice} sessionApi={session.api} /> : null}
      {owner || session.role === "store_manager" ? (
        <TemplatesSection owner={owner} stores={stores} items={templates} reload={load} setNotice={setNotice} sessionApi={session.api} />
      ) : null}
      <BrandSection owner={owner} access={access} tenantName={session.tenantName} />
    </>
  );
}

// 代理视角:正在为谁工作 + 权限与付款主体(A1 受控进入的可见性纪律)。
// export:HUI-4593 A1 /agency 控制台复用同一横幅,不另造权限展示。
export function AgentBanner({ tenantName, access }: { tenantName: string; access: AccessEffective | null }) {
  const actions = (access?.relation?.allowed_actions ?? []).map((a) => ACTION_LABELS[a] ?? a);
  return (
    <section className="tk-section" data-section="agent-banner">
      <h2 className="tk-section-title">工作身份</h2>
      <p data-field="acting-for">正在为 {tenantName || "—"} 工作。你看到的数据都属于这家组织。</p>
      <p data-field="agent-perms">获准动作:{actions.length > 0 ? actions.join("、") : "只读与开子账号（未获配置授权）"}</p>
      <p data-field="payer-status">
        {access?.relation?.payer_granted
          ? "付款主体：已获代付授权（真实扣费仍由平台按付款授权裁决）"
          : "付款主体：无代付授权。收费动作需要商家先授予付款授权。"}
      </p>
    </section>
  );
}

type SessionApi = (method: string, path: string, body?: unknown) => Promise<{ ok: boolean; status: number; data: Record<string, unknown> }>;

// ---- 成员与权限(授权/改角色/停用,org_owner)----------------------------------

function MembersSection({
  members, stores, reload, setNotice, sessionApi,
}: {
  members: MemberRec[];
  stores: StoreRec[];
  reload: () => Promise<void>;
  setNotice: (v: string) => void;
  sessionApi: SessionApi;
}) {
  const [draft, setDraft] = useState({ principal: "", role: "staff", scope: "", reason: "" });
  const [stopReason, setStopReason] = useState<Record<string, string>>({});
  const storeName = (id: string) => stores.find((s) => s.id === id)?.name ?? id ?? "";

  async function grant(event: React.FormEvent) {
    event.preventDefault();
    if (!draft.reason.trim()) {
      setNotice("授权必须填写原因（会留痕可查）。");
      return;
    }
    const res = await sessionApi("POST", "admin/members", {
      principal_ref: draft.principal.trim(),
      role: draft.role,
      store_scope: (draft.role === "store_manager" || draft.scope !== "") ? draft.scope : "",
      display_name: "",
      reason: draft.reason.trim(),
    });
    if (!res.ok) {
      setNotice(failureText(res.status, res.data));
      return;
    }
    setDraft({ principal: "", role: "staff", scope: "", reason: "" });
    setNotice("");
    await reload();
  }

  async function stop(member: MemberRec) {
    const reason = (stopReason[member.id] ?? "").trim();
    if (!reason) {
      setNotice("停用成员必须填写原因（会留痕可查）。");
      return;
    }
    const res = await sessionApi("PATCH", `admin/members/${member.id}`, { enabled: false, reason });
    if (!res.ok) {
      setNotice(failureText(res.status, res.data));
      return;
    }
    setStopReason({ ...stopReason, [member.id]: "" });
    setNotice("");
    await reload();
  }

  return (
    <section className="tk-section" data-section="members">
      <h2 className="tk-section-title">成员与权限</h2>
      <ul className="tk-list">
        {members.map((m) => (
          <li key={m.id} data-role="member-row">
            <strong>{m.display_name || m.principal_ref}</strong>
            <span className="tk-note"> {ROLE_LABELS[m.role] ?? m.role}</span>
            {m.store_scope ? <span className="tk-note">仅本店：{storeName(m.store_scope)}</span> : null}
            {m.enabled ? null : <span className="tk-unknown">已停用</span>}
            {m.enabled ? (
              <div className="tk-row tk-gap">
                <input
                  className="tk-input"
                  placeholder="停用原因（必填）"
                  value={stopReason[m.id] ?? ""}
                  onChange={(e) => setStopReason({ ...stopReason, [m.id]: e.target.value })}
                />
                <button className="tk-quiet" type="button" data-action="disable-member" onClick={() => void stop(m)}>停用</button>
              </div>
            ) : null}
          </li>
        ))}
      </ul>
      <form className="tk-form" onSubmit={grant} data-form="grant-member">
        <h3 className="tk-note">授权新成员</h3>
        <input className="tk-input" placeholder="成员账号 ID（usr_ 开头，向平台管理员索取）" value={draft.principal} onChange={(e) => setDraft({ ...draft, principal: e.target.value })} required />
        <select className="tk-input" value={draft.role} onChange={(e) => setDraft({ ...draft, role: e.target.value })}>
          <option value="staff">店员</option>
          <option value="store_manager">门店经理</option>
        </select>
        <select className="tk-input" value={draft.scope} onChange={(e) => setDraft({ ...draft, scope: e.target.value })}>
          <option value="">不限门店（店员默认全组织可见）</option>
          {stores.map((s) => (
            <option key={s.id} value={s.id}>仅本店：{s.name}</option>
          ))}
        </select>
        <input className="tk-input" placeholder="授权原因（必填，留痕可查）" value={draft.reason} onChange={(e) => setDraft({ ...draft, reason: e.target.value })} required />
        <button className="tk-button" type="submit" data-action="grant-member">授权</button>
      </form>
    </section>
  );
}

// ---- 代理协作(建立/scope 授权/撤回,org_owner)---------------------------------

function AgencySection({
  relations, stores, reload, setNotice, sessionApi,
}: {
  relations: AgencyRel[];
  stores: StoreRec[];
  reload: () => Promise<void>;
  setNotice: (v: string) => void;
  sessionApi: SessionApi;
}) {
  const [draft, setDraft] = useState({ principal: "", note: "" });
  const [scopes, setScopes] = useState<Record<string, { actions: string[]; stores: string[]; until: string; payer: boolean; revision: number; reason: string }>>({});
  const [stopReason, setStopReason] = useState<Record<string, string>>({});

  function scopeOf(rel: AgencyRel) {
    return (
      scopes[rel.id] ?? {
        actions: rel.allowed_actions ?? [],
        stores: rel.store_scope_set ?? [],
        until: rel.valid_until ?? "",
        payer: rel.payer_grant_ref !== "",
        revision: rel.grant_revision,
        reason: "",
      }
    );
  }

  async function establish(event: React.FormEvent) {
    event.preventDefault();
    const res = await sessionApi("POST", "agency/relations", { agent_principal: draft.principal.trim(), note: draft.note.trim() });
    if (!res.ok) {
      setNotice(failureText(res.status, res.data));
      return;
    }
    setDraft({ principal: "", note: "" });
    await reload();
  }

  async function toggleAction(rel: AgencyRel, action: string) {
    const cur = scopeOf(rel);
    const actions = cur.actions.includes(action) ? cur.actions.filter((a) => a !== action) : [...cur.actions, action];
    setScopes({ ...scopes, [rel.id]: { ...cur, actions } });
  }

  function toggleStore(rel: AgencyRel, storeId: string) {
    const cur = scopeOf(rel);
    const picked = cur.stores.includes(storeId) ? cur.stores.filter((s) => s !== storeId) : [...cur.stores, storeId];
    setScopes({ ...scopes, [rel.id]: { ...cur, stores: picked } });
  }

  async function saveScope(rel: AgencyRel) {
    const cur = scopeOf(rel);
    if (!cur.reason.trim()) {
      setNotice("代理授权变更必须填写原因（会留痕可查）。");
      return;
    }
    const res = await sessionApi("PUT", `agency/relations/${rel.id}/scope`, {
      allowed_actions: cur.actions,
      store_scope_set: cur.stores,
      valid_until: cur.until.trim(),
      payer_grant_ref: cur.payer ? "granted-by-owner" : "",
      grant_revision: cur.revision,
      reason: cur.reason.trim(),
    });
    if (!res.ok) {
      setNotice(failureText(res.status, res.data));
      return;
    }
    setScopes({ ...scopes, [rel.id]: { ...cur, reason: "", revision: cur.revision + 1 } });
    setNotice("");
    await reload();
  }

  async function revoke(rel: AgencyRel) {
    const reason = (stopReason[rel.id] ?? "").trim();
    if (!reason) {
      setNotice("撤销代管必须填写原因（会留痕可查）。撤销后代理立即失去访问。");
      return;
    }
    const res = await sessionApi("DELETE", `agency/relations/${rel.id}`, { reason });
    if (!res.ok) {
      setNotice(failureText(res.status, res.data));
      return;
    }
    setNotice("代理授权已撤销。对方的下一次访问会立即被拒绝。");
    await reload();
  }

  const active = relations.filter((r) => !r.revoked_at);
  return (
    <section className="tk-section" data-section="agency">
      <h2 className="tk-section-title">代理协作</h2>
      <ul className="tk-list">
        {active.map((rel) => {
          const cur = scopeOf(rel);
          return (
            <li key={rel.id} data-role="agency-row">
              <strong>{rel.agent_principal}</strong>
              <span className="tk-note"> 版本 {cur.revision}{cur.until ? ` · 至 ${cur.until}` : " · 长期"}</span>
              <div className="tk-row tk-gap" data-field="scope-actions">
                {Object.keys(ACTION_LABELS).map((a) => (
                  <label key={a}>
                    <input
                      type="checkbox"
                      checked={cur.actions.includes(a)}
                      onChange={() => toggleAction(rel, a)}
                    />
                    {ACTION_LABELS[a]}
                  </label>
                ))}
              </div>
              <div className="tk-row tk-gap" data-field="scope-stores">
                {stores.map((s) => (
                  <label key={s.id}>
                    <input type="checkbox" checked={cur.stores.includes(s.id)} onChange={() => toggleStore(rel, s.id)} />
                    {s.name}
                  </label>
                ))}
              </div>
              <div className="tk-row tk-gap">
                <input
                  className="tk-input"
                  type="date"
                  value={cur.until.slice(0, 10)}
                  onChange={(e) => setScopes({ ...scopes, [rel.id]: { ...cur, until: e.target.value ? new Date(e.target.value).toISOString() : "" } })}
                />
                <label>
                  <input
                    type="checkbox"
                    checked={cur.payer}
                    onChange={(e) => setScopes({ ...scopes, [rel.id]: { ...cur, payer: e.target.checked } })}
                  />
                  代付授权（默认关闭；真实扣费仍由平台裁决）
                </label>
              </div>
              <div className="tk-row tk-gap">
                <input
                  className="tk-input"
                  placeholder="授权原因（必填）"
                  value={cur.reason}
                  onChange={(e) => setScopes({ ...scopes, [rel.id]: { ...cur, reason: e.target.value } })}
                />
                <button className="tk-quiet" type="button" data-action="save-scope" onClick={() => void saveScope(rel)}>保存授权</button>
              </div>
              <div className="tk-row tk-gap">
                <input
                  className="tk-input"
                  placeholder="撤销原因（必填）"
                  value={stopReason[rel.id] ?? ""}
                  onChange={(e) => setStopReason({ ...stopReason, [rel.id]: e.target.value })}
                />
                <button className="tk-quiet" type="button" data-action="revoke-agency" onClick={() => void revoke(rel)}>撤销代管</button>
              </div>
            </li>
          );
        })}
      </ul>
      <form className="tk-form" onSubmit={establish} data-form="establish-agency">
        <h3 className="tk-note">建立代管（新代理默认只读 + 可开子账号，配置权需在此显式授予）</h3>
        <input className="tk-input" placeholder="代理账号 ID（usr_ 开头，向平台管理员索取）" value={draft.principal} onChange={(e) => setDraft({ ...draft, principal: e.target.value })} required />
        <input className="tk-input" placeholder="备注，可空" value={draft.note} onChange={(e) => setDraft({ ...draft, note: e.target.value })} />
        <button className="tk-button" type="submit" data-action="establish-agency">建立代管</button>
      </form>
    </section>
  );
}

// ---- 模板下发(总部选店下发;门店经理本店只读)----------------------------------

function TemplatesSection({
  owner, stores, items, reload, setNotice, sessionApi,
}: {
  owner: boolean;
  stores: StoreRec[];
  items: TemplateRec[];
  reload: () => Promise<void>;
  setNotice: (v: string) => void;
  sessionApi: SessionApi;
}) {
  const [draft, setDraft] = useState(() => newTemplateDraft());

  async function distribute(event: React.FormEvent) {
    event.preventDefault();
    if (!draft.reason.trim()) {
      setNotice("模板下发必须填写原因（会留痕可查）。");
      return;
    }
    if (draft.picked.length === 0) {
      setNotice("请选择要下发的门店。");
      return;
    }
    const res = await sessionApi("POST", "template-assignments", {
      template_ref: draft.ref.trim(),
      source_version: draft.version.trim(),
      store_ids: draft.picked,
      allowed_override_fields: draft.fields.split(/[,，\s]+/).filter(Boolean),
      // 幂等键在一次逻辑请求的生命周期内稳定:失败重试沿用同一 key
      // (服务端按 key 重放,不重复创建);成功后才换新 key。
      request_key: draft.key,
    });
    if (!res.ok) {
      setNotice(failureText(res.status, res.data));
      return;
    }
    const failItems = ((res.data.items as { ok: boolean }[]) ?? []).filter((it) => !it.ok);
    setNotice(failItems.length > 0 ? `下发完成，但有 ${failItems.length} 家门店失败（可重试，不会重复创建）。` : "模板已下发给选中门店。各店的账号、二维码、Wi-Fi 密码不会被覆盖。");
    setDraft(newTemplateDraft());
    await reload();
  }

  async function cancel(id: string) {
    const res = await sessionApi("PATCH", `template-assignments/${id}`, { status: "cancelled", reason: "总部撤回模板下发" });
    if (!res.ok) {
      setNotice(failureText(res.status, res.data));
      return;
    }
    await reload();
  }

  const storeName = (id: string) => stores.find((s) => s.id === id)?.name ?? id;
  return (
    <section className="tk-section" data-section="templates">
      <h2 className="tk-section-title">模板下发</h2>
      <p className="tk-note">总部把首页模板授给门店；门店可在允许的字段里局部覆盖。已发布页面不会被自动覆盖。</p>
      <ul className="tk-list">
        {items.map((t) => (
          <li key={t.id} data-role="template-row">
            <strong>{t.template_ref} {t.source_version}</strong>
            <span className="tk-note"> {storeName(t.store_id)} · {t.status === "distributed" ? "已下发" : t.status === "applied" ? "门店已应用" : "已撤回"}</span>
            {owner && t.status === "distributed" ? (
              <button className="tk-quiet" type="button" data-action="cancel-template" onClick={() => void cancel(t.id)}>撤回</button>
            ) : null}
          </li>
        ))}
      </ul>
      {owner ? (
        <form className="tk-form" onSubmit={distribute} data-form="distribute-template">
          <h3 className="tk-note">下发新模板</h3>
          <input className="tk-input" placeholder="模板引用（如 tpl_hero）" value={draft.ref} onChange={(e) => setDraft({ ...draft, ref: e.target.value })} required />
          <input className="tk-input" placeholder="模板版本（如 v3）" value={draft.version} onChange={(e) => setDraft({ ...draft, version: e.target.value })} required />
          <div className="tk-row tk-gap" data-field="template-stores">
            {stores.map((s) => (
              <label key={s.id}>
                <input
                  type="checkbox"
                  checked={draft.picked.includes(s.id)}
                  onChange={() =>
                    setDraft({
                      ...draft,
                      picked: draft.picked.includes(s.id) ? draft.picked.filter((x) => x !== s.id) : [...draft.picked, s.id],
                    })
                  }
                />
                {s.name}
              </label>
            ))}
          </div>
          <input className="tk-input" placeholder="允许门店覆盖的字段（逗号分隔，如 hero_title,theme_color）" value={draft.fields} onChange={(e) => setDraft({ ...draft, fields: e.target.value })} />
          <input className="tk-input" placeholder="下发原因（必填，留痕可查）" value={draft.reason} onChange={(e) => setDraft({ ...draft, reason: e.target.value })} required />
          <button className="tk-button" type="submit" data-action="distribute-template">下发</button>
        </form>
      ) : (
        <p className="tk-note">模板下发由总部操作。你能看到自己门店收到的模板。</p>
      )}
    </section>
  );
}

// ---- 白标授权(只读展示;授权开关=org_owner 专属判定)---------------------------

function BrandSection({ owner, access, tenantName }: { owner: boolean; access: AccessEffective | null; tenantName: string }) {
  const canManage = owner && (access?.permissions ?? []).includes("storefront:configure");
  return (
    <section className="tk-section" data-section="brand">
      <h2 className="tk-section-title">白标与品牌</h2>
      <p data-field="brand-tenant">当前组织：{tenantName || "—"}</p>
      <p data-field="brand-manage">
        {canManage
          ? "品牌展示配置由组织负责人管理。域名与证书的审核归平台，不由商家填写生效。"
          : "品牌展示配置只有组织负责人能改。你的账号对这项配置只读。"}
      </p>
    </section>
  );
}
