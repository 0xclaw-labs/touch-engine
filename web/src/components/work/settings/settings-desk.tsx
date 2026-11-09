"use client";

// HUI-4593 M13 成员与权限工作台。
//
// 纪律(裁定书 + 走查 Walk B 修复轮 2):
//   - 四区按角色维度呈现:组织负责人 / 门店经理 / 店员动作授权 / 代理协作
//     (外加模板下发与白标授权两区;走查验收按四区对照);
//   - 店员动作授权:对现有店员授予/撤回 storefront/action 配置权限
//     (授权→访问→撤回→同会话 403 旅程;PUT admin/members/{id}/permissions,
//      撤回即时生效由 access/effective 每请求实时读取保证);
//   - agent 角色时顶部「正在为谁工作」横幅(组织/权限/付款主体),切换组织
//     即清空上一户数据(merchant-session 既有单缝保证);
//   - 授权/撤回/代管/下发全部操作必填原因(服务端审计;前端先挡一道);
//   - 特权操作只对 org_owner 显示;权限不足按产品语句说明,不崩、不装成功;
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

interface MemberRec {
  id: string;
  principal_ref: string;
  role: string;
  store_scope: string;
  enabled: boolean;
  display_name: string;
  granted_permissions?: string[];
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

// 店员可被授予的动作权限(与后端白名单一一对应;billing/ops 永不经此面)。
const STAFF_PERM_LABELS: Record<string, string> = {
  "storefront:configure": "首页配置",
  "action:configure": "动作配置",
  "action:channel_target": "渠道目标",
  "action:manage_deal": "商务动作（deal）",
  "action:manage_voucher": "商务动作（voucher）",
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
  // 修复轮 3(F2①):页面级 access 门控 —— 店员没有任何配置权限时,本页呈现
  // 明确的受限视图(403 语义),不再渲染任何管理区;撤权后同会话下一请求即受限
  // (access/effective 每请求实时读取)。
  const myPerms = access?.permissions ?? [];
  if (session.role === "staff" && myPerms.length === 0) {
    return (
      <>
        <h1 className="tk-title">成员与权限</h1>
        <OrgSwitchSection options={session.options} currentTenantId={session.tenantId} switchTenant={session.switchTenant} />
        <section className="tk-section" data-state="restricted">
          <p className="tk-state tk-danger" data-field="restricted-403">
            当前账号没有成员与权限的管理能力（403）。这个页面只对有配置权限的账号开放；
            如需授权，请联系组织负责人在「店员动作授权」里授予。
          </p>
          <MyAccessSection role={session.role} access={access} tenantName={session.tenantName} />
        </section>
      </>
    );
  }
  return (
    <>
      <h1 className="tk-title">成员与权限</h1>
      <p className="tk-lead">谁进来、能做什么、代理替谁做——每一次授权和撤回都留痕可查。</p>
      {notice && phase === "ready" ? <p className="tk-warn" data-role="notice">{notice}</p> : null}
      <OrgSwitchSection options={session.options} currentTenantId={session.tenantId} switchTenant={session.switchTenant} />
      <MyAccessSection role={session.role} access={access} tenantName={session.tenantName} />
      {agent ? <AgentBanner tenantName={session.tenantName} access={access} backHref="/agency" /> : null}
      {owner ? <OwnerSection members={members} /> : null}
      {owner ? <StoreManagerSection members={members} stores={stores} reload={load} setNotice={setNotice} sessionApi={session.api} /> : null}
      {owner ? <StaffActionSection members={members} stores={stores} reload={load} setNotice={setNotice} sessionApi={session.api} /> : null}
      {owner ? <AgencySection relations={relations} stores={stores} reload={load} setNotice={setNotice} sessionApi={session.api} /> : null}
      {owner || session.role === "store_manager" ? (
        <TemplatesSection owner={owner} stores={stores} items={templates} reload={load} setNotice={setNotice} sessionApi={session.api} />
      ) : null}
      <BrandSection owner={owner} access={access} tenantName={session.tenantName} />
    </>
  );
}

// 我的有效授权(所有角色):授权→访问→撤回旅程的「访问」一侧,当场可核对。
function MyAccessSection({ role, access, tenantName }: { role: string; access: AccessEffective | null; tenantName: string }) {
  const perms = access?.permissions ?? [];
  const storeIds = access?.allowed_store_ids ?? [];
  return (
    <section className="tk-section" data-section="my-access">
      <h2 className="tk-section-title">我的有效授权</h2>
      <p data-field="my-role">当前身份:{ROLE_LABELS[role] ?? (role || "—")}（{tenantName || "—"}）</p>
      <p data-field="my-perms">
        {perms.length > 0 ? `已获配置权限：${perms.join("、")}` : "未获任何配置权限（业务读写按既有角色语义）"}
      </p>
      <p data-field="my-scope">
        {storeIds.length === 0 ? "作用范围：全部门店 / 租户级" : `作用范围：仅 ${storeIds.length} 家门店`}
      </p>
      {perms.includes("storefront:configure") ? (
        <p data-field="page-config-entry">
          <a className="tk-button" href="/work/stores" data-action="goto-page-config">前往门店首页配置</a>
          <span className="tk-note"> 在门店里完成首页/动作的配置；独立首页配置面（1676）落地后此入口指向它。</span>
        </p>
      ) : null}
    </section>
  );
}

// 代理视角:正在为谁工作 + 权限与付款主体(A1 受控进入的可见性纪律)。
// export:HUI-4593 A1 /agency 控制台复用同一横幅,不另造权限展示。
// backHref(修复轮 3/F1):代理正在为别的租户工作时,显式提供「返回代理台」入口
// (横幅内自足,不动 2628 共享壳的导航结构)。
export function AgentBanner({ tenantName, access, backHref }: { tenantName: string; access: AccessEffective | null; backHref?: string }) {
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
      {backHref ? (
        <p data-field="banner-back">
          <a className="tk-quiet" href={backHref} data-action="back-agency">返回代理台</a>
        </p>
      ) : null}
    </section>
  );
}

// 修复轮 5(F②):组织切换 = 身份级能力。principal 在 >1 租户有 membership 即可
// 切换,不受当前租户内权限门控 —— 受限视图(403 语义)也必须保留本入口。
// 切换走既有 switchTenant 单缝;切到目标租户后按那里的 membership+role 授权
// (跨租户 scope 检查照旧,本票既有语义)。
function OrgSwitchSection({
  options, currentTenantId, switchTenant,
}: {
  options: { tenant_id: string; display_name: string; role: string; enabled: boolean; source: string }[];
  currentTenantId: string;
  switchTenant: (tenantId: string) => void;
}) {
  const others = options.filter(
    (o) => o.enabled && o.source === "membership" && o.tenant_id !== currentTenantId,
  );
  if (others.length === 0) return null;
  return (
    <section className="tk-section" data-section="org-switch">
      <h2 className="tk-section-title">切换组织</h2>
      <p className="tk-note">你有多个组织的身份。切换与当前组织的权限无关；切过去之后按那边的角色授权。</p>
      <div className="tk-row tk-gap" data-field="org-options">
        {others.map((o) => (
          <button
            key={o.tenant_id}
            className="tk-button"
            type="button"
            data-action="switch-org"
            onClick={() => switchTenant(o.tenant_id)}
          >
            进入{ROLE_LABELS[o.role] ?? o.role}身份:{o.display_name}
          </button>
        ))}
      </div>
    </section>
  );
}

type SessionApi = (method: string, path: string, body?: unknown) => Promise<{ ok: boolean; status: number; data: Record<string, unknown> }>;

function memberRowLabel(m: MemberRec): string {
  return m.display_name || m.principal_ref;
}

// ---- 四区之一:组织负责人(治理面清单,只读展示)---------------------------------

function OwnerSection({ members }: { members: MemberRec[] }) {
  const owners = members.filter((m) => m.role === "org_owner" && m.enabled);
  return (
    <section className="tk-section" data-section="owner">
      <h2 className="tk-section-title">组织负责人</h2>
      <ul className="tk-list">
        {owners.map((m) => (
          <li key={m.id} data-role="owner-row">
            <strong>{memberRowLabel(m)}</strong>
            <span className="tk-note"> 总部 / 全部门店</span>
          </li>
        ))}
      </ul>
      <p className="tk-note" data-field="owner-scope">
        组织负责人独占:成员与门店管理、模板下发、代理授权、白标设置。以下三区的一切授权都由组织负责人操作并留痕。
      </p>
    </section>
  );
}

// ---- 四区之二:门店经理授权 ------------------------------------------------------

function StoreManagerSection({
  members, stores, reload, setNotice, sessionApi,
}: {
  members: MemberRec[];
  stores: StoreRec[];
  reload: () => Promise<void>;
  setNotice: (v: string) => void;
  sessionApi: SessionApi;
}) {
  const [draft, setDraft] = useState({ principal: "", scope: "", reason: "" });
  const [stopReason, setStopReason] = useState<Record<string, string>>({});
  const managers = members.filter((m) => m.role === "store_manager");
  const storeName = (id: string) => stores.find((s) => s.id === id)?.name ?? id ?? "";

  async function grant(event: React.FormEvent) {
    event.preventDefault();
    if (!draft.reason.trim()) {
      setNotice("授权必须填写原因（会留痕可查）。");
      return;
    }
    if (!draft.scope) {
      setNotice("门店经理必须指定负责的门店。");
      return;
    }
    const res = await sessionApi("POST", "admin/members", {
      principal_ref: draft.principal.trim(),
      role: "store_manager",
      store_scope: draft.scope,
      display_name: "",
      reason: draft.reason.trim(),
    });
    if (!res.ok) {
      setNotice(failureText(res.status, res.data));
      return;
    }
    setDraft({ principal: "", scope: "", reason: "" });
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
    <section className="tk-section" data-section="store-manager">
      <h2 className="tk-section-title">门店经理授权</h2>
      <ul className="tk-list">
        {managers.map((m) => (
          <li key={m.id} data-role="manager-row">
            <strong>{memberRowLabel(m)}</strong>
            <span className="tk-note"> 仅本店:{storeName(m.store_scope)}</span>
            {m.enabled ? null : <span className="tk-unknown">已停用</span>}
            {m.enabled ? (
              <div className="tk-row tk-gap">
                <input
                  className="tk-input"
                  placeholder="停用原因（必填）"
                  value={stopReason[m.id] ?? ""}
                  onChange={(e) => setStopReason({ ...stopReason, [m.id]: e.target.value })}
                />
                <button className="tk-quiet" type="button" data-action="disable-manager" onClick={() => void stop(m)}>停用</button>
              </div>
            ) : null}
          </li>
        ))}
      </ul>
      <form className="tk-form" onSubmit={grant} data-form="grant-manager">
        <h3 className="tk-note">授权门店经理</h3>
        <input className="tk-input" placeholder="成员账号 ID（usr_ 开头，向平台管理员索取）" value={draft.principal} onChange={(e) => setDraft({ ...draft, principal: e.target.value })} required />
        <select className="tk-input" value={draft.scope} onChange={(e) => setDraft({ ...draft, scope: e.target.value })} required>
          <option value="">选择负责的门店</option>
          {stores.map((s) => (
            <option key={s.id} value={s.id}>{s.name}</option>
          ))}
        </select>
        <input className="tk-input" placeholder="授权原因（必填，留痕可查）" value={draft.reason} onChange={(e) => setDraft({ ...draft, reason: e.target.value })} required />
        <button className="tk-button" type="submit" data-action="grant-manager">授权</button>
      </form>
    </section>
  );
}

// ---- 四区之三:店员动作授权(授权→访问→撤回→同会话 403 旅程的写面)----------------

function StaffActionSection({
  members, stores, reload, setNotice, sessionApi,
}: {
  members: MemberRec[];
  stores: StoreRec[];
  reload: () => Promise<void>;
  setNotice: (v: string) => void;
  sessionApi: SessionApi;
}) {
  const [draft, setDraft] = useState({ principal: "", scope: "", reason: "" });
  const [stopReason, setStopReason] = useState<Record<string, string>>({});
  const [permDraft, setPermDraft] = useState<Record<string, { picked: string[]; reason: string }>>({});
  const staff = members.filter((m) => m.role === "staff");
  const storeName = (id: string) => stores.find((s) => s.id === id)?.name ?? id ?? "";

  function pickedOf(m: MemberRec): string[] {
    return permDraft[m.id]?.picked ?? m.granted_permissions ?? [];
  }

  function togglePerm(m: MemberRec, perm: string) {
    const cur = pickedOf(m);
    const picked = cur.includes(perm) ? cur.filter((p) => p !== perm) : [...cur, perm];
    setPermDraft({ ...permDraft, [m.id]: { picked, reason: permDraft[m.id]?.reason ?? "" } });
  }

  async function savePerms(m: MemberRec) {
    const entry = permDraft[m.id];
    const reason = (entry?.reason ?? "").trim();
    if (!reason) {
      setNotice("动作授权变更必须填写原因（会留痕可查）。撤回也一样。");
      return;
    }
    const res = await sessionApi("PUT", `admin/members/${m.id}/permissions`, {
      permissions: entry?.picked ?? [],
      reason,
    });
    if (!res.ok) {
      setNotice(failureText(res.status, res.data));
      return;
    }
    setPermDraft({ ...permDraft, [m.id]: { picked: entry?.picked ?? [], reason: "" } });
    setNotice("已保存。该店员的下一次请求即按新权限执行（撤回即时生效）。");
    await reload();
  }

  async function grantStaff(event: React.FormEvent) {
    event.preventDefault();
    if (!draft.reason.trim()) {
      setNotice("授权必须填写原因（会留痕可查）。");
      return;
    }
    const res = await sessionApi("POST", "admin/members", {
      principal_ref: draft.principal.trim(),
      role: "staff",
      store_scope: draft.scope,
      display_name: "",
      reason: draft.reason.trim(),
    });
    if (!res.ok) {
      setNotice(failureText(res.status, res.data));
      return;
    }
    setDraft({ principal: "", scope: "", reason: "" });
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
    <section className="tk-section" data-section="staff-action">
      <h2 className="tk-section-title">店员动作授权</h2>
      <p className="tk-note">给店员授予首页/动作配置权限；撤回后,店员的下一次请求立即失去对应能力。</p>
      <ul className="tk-list">
        {staff.map((m) => {
          const current = m.granted_permissions ?? [];
          const picked = pickedOf(m);
          const entry = permDraft[m.id];
          return (
            <li key={m.id} data-role="staff-row">
              <strong>{memberRowLabel(m)}</strong>
              <span className="tk-note"> {m.store_scope ? `仅本店：${storeName(m.store_scope)}` : "全组织"}</span>
              {m.enabled ? null : <span className="tk-unknown">已停用</span>}
              {m.enabled ? (
                <>
                  <div className="tk-row tk-gap" data-field={`staff-perms-${m.id}`}>
                    {Object.entries(STAFF_PERM_LABELS).map(([perm, label]) => (
                      <label key={perm}>
                        <input
                          type="checkbox"
                          checked={picked.includes(perm)}
                          onChange={() => togglePerm(m, perm)}
                        />
                        {label}
                      </label>
                    ))}
                  </div>
                  <p className="tk-note" data-field="staff-current">
                    {current.length > 0 ? `现行:${current.join("、")}` : "现行:未获配置权限"}
                  </p>
                  <div className="tk-row tk-gap">
                    <input
                      className="tk-input"
                      placeholder={entry?.picked && entry.picked.join(",") !== current.join(",") ? "授权/撤回原因（必填）" : "变更原因（必填，留痕可查）"}
                      value={entry?.reason ?? ""}
                      onChange={(e) => setPermDraft({ ...permDraft, [m.id]: { picked: pickedOf(m), reason: e.target.value } })}
                    />
                    <button className="tk-quiet" type="button" data-action="save-staff-perms" onClick={() => void savePerms(m)}>保存授权</button>
                  </div>
                  <div className="tk-row tk-gap">
                    <input
                      className="tk-input"
                      placeholder="停用原因（必填）"
                      value={stopReason[m.id] ?? ""}
                      onChange={(e) => setStopReason({ ...stopReason, [m.id]: e.target.value })}
                    />
                    <button className="tk-quiet" type="button" data-action="disable-staff" onClick={() => void stop(m)}>停用</button>
                  </div>
                </>
              ) : null}
            </li>
          );
        })}
      </ul>
      <form className="tk-form" onSubmit={grantStaff} data-form="grant-staff">
        <h3 className="tk-note">新增店员</h3>
        <input className="tk-input" placeholder="成员账号 ID（usr_ 开头，向平台管理员索取）" value={draft.principal} onChange={(e) => setDraft({ ...draft, principal: e.target.value })} required />
        <select className="tk-input" value={draft.scope} onChange={(e) => setDraft({ ...draft, scope: e.target.value })}>
          <option value="">不限门店（全组织可见）</option>
          {stores.map((s) => (
            <option key={s.id} value={s.id}>仅本店：{s.name}</option>
          ))}
        </select>
        <input className="tk-input" placeholder="授权原因（必填，留痕可查）" value={draft.reason} onChange={(e) => setDraft({ ...draft, reason: e.target.value })} required />
        <button className="tk-button" type="submit" data-action="grant-staff">新增店员</button>
      </form>
    </section>
  );
}

// ---- 四区之四:代理协作(建立/scope 授权/撤回,org_owner)--------------------------

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
      <h2 className="tk-section-title">代理协作授权</h2>
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

// ---- 模板下发(总部选店下发;门店经理本店只读)-------------------------------------

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
      // 修复轮 2(F3):业务原因独立于幂等键,原样透传进审计。
      reason: draft.reason.trim(),
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

// ---- 白标授权(只读展示;授权开关=org_owner 专属判定)-------------------------------

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
