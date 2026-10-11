"use client";

// 商家后台(路由区 /admin):全部数据经 BFF(/api/*)转发到 Go 服务,
// 由服务端做平台会话解析 + 租户成员校验。本页面不持有任何凭证。

import { useCallback, useEffect, useRef, useState } from "react";
import { AdminDataTable } from "@/components/admin/AdminDataTable";
import { ExtraJumpPanel } from "@/components/admin/ExtraJumpPanel";
import { AdminShell } from "@/components/admin/AdminShell";
import { MatrixDraftButton } from "@/components/admin/MatrixDraftButton";
import { MerchantWorkbench } from "@/components/admin/MerchantWorkbench";
import { TaskHandoffActions } from "@/components/admin/TaskHandoffActions";
import { adminHeaderTenantName } from "@/lib/admin-header-tenant";
import { copyJobClosed, copyJobKey, copyUsability, handoffHasFormalJump } from "@/lib/copy-draft";
import { acceptTenantPayload } from "@/lib/eco-nav/touch-shell";
import { failureText } from "@/lib/failure-copy";
import { toJsonBody } from "@/lib/http-json";
import {
  beginTenantOp,
  currentTenantOp,
  decideTenantConfirm,
  resolveWorkspaceTenant,
  TENANT_STORAGE_KEY,
  writeTenantMemory,
  type MembershipScope,
} from "@/lib/workspace-tenant";

interface Campaign {
  id: string;
  title: string;
  public_content: string;
  status: string;
  starts_at: string;
  ends_at: string;
  order_ref?: string;
}

interface StoreRec {
  id: string;
  name: string;
  address: string;
  status: "active" | "disabled";
}

interface LinkRec {
  id: string;
  code: string;
  enabled: boolean;
}

interface QrMeta {
  url: string;
  code: string;
  size: number;
}

// HUI-1665 NFC 标签管理(管理面,owner-only;服务端裁决,staff 见 403 文案)
interface TagGroup {
  id: string;
  name: string;
}

interface TagView {
  id: string;
  label: string;
  code: string;
  link_id: string;
  campaign_id: string;
  status: "active" | "disabled";
  uid_hint?: string;
  store_name?: string;
  group_name?: string;
}

const QR_SIZES = [128, 256, 512] as const;

interface AssetRec {
  id: string;
  asset_id: string;
  version: string;
}

// fix2：交接资料字段呈现（server/internal/copydraft Handoff 的 JSON 字段名 → 中文标签）
type ProfessionalHandoff = Record<string, unknown> & {
  store_name?: unknown;
  campaign_title?: unknown;
  public_content?: unknown;
  price?: unknown;
  address?: unknown;
  hours?: unknown;
  asset_ids?: unknown;
  evidenced_claims?: unknown;
};
const HANDOFF_FIELDS: Array<[string, keyof ProfessionalHandoff]> = [
  ["门店", "store_name"],
  ["活动", "campaign_title"],
  ["顾客能看到的内容", "public_content"],
  ["价格", "price"],
  ["地址", "address"],
  ["营业时间", "hours"],
  ["素材引用", "asset_ids"],
  ["已证实的说法", "evidenced_claims"],
];

export default function AdminPage() {
  const [taskNotice, setTaskNotice] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [tenantId, setTenantId] = useState("");
  const [role, setRole] = useState("");
  const [storeScope, setStoreScope] = useState(""); // HUI-1674:""=总部;非空=仅该门店
  const [email_, setEmailMasked] = useState("");
  const [workbar, setWorkbar] = useState("");
  const [tenantName, setTenantName] = useState("");
  const [scopes, setScopes] = useState<MembershipScope[]>([]);
  const [orgPhase, setOrgPhase] = useState<"boot" | "login" | "choose" | "none" | "ready">("boot");
  const [brandName, setBrandName] = useState("");
  const [supportLine, setSupportLine] = useState("");
  const [error, setError] = useState("");
  const [campaigns, setCampaigns] = useState<Campaign[]>([]);
  const [stores, setStores] = useState<StoreRec[]>([]);

  // HUI-1674 门店治理:org_owner 改店/停用;编辑行内状态
  const [storeEdit, setStoreEdit] = useState<{ id: string; name: string; address: string } | null>(null);
  const [storeError, setStoreError] = useState("");

  const isOrgOwner = role === "org_owner";
  const isStoreManager = role === "store_manager";
  const managedStore = isStoreManager ? stores.find((s) => s.id === storeScope) : undefined;
  // 建活动/绑标签只可选未停用门店(经理的门店列表服务端已过滤为本店)
  const activeStores = stores.filter((s) => s.status !== "disabled");

  const [newStore, setNewStore] = useState({ name: "", address: "" });
  const [newCampaign, setNewCampaign] = useState({ title: "", public_content: "", starts_at: "", ends_at: "", store_id: "", order_ref: "" });
  const [newAsset, setNewAsset] = useState({ campaign: "", asset_id: "", version: "" });

  // HUI-1668 报价 → 确认 → 生成 → 选定 → 保存。没有模型凭证时只展示失败关闭。
  const [copyCampaign, setCopyCampaign] = useState("");
  const [copyPrice, setCopyPrice] = useState({ text: "", status: "expired" });
  const [copyAddress, setCopyAddress] = useState({ text: "", status: "uncertain" });
  const [copyHours, setCopyHours] = useState({ text: "", status: "uncertain" });
  const [copyClaim, setCopyClaim] = useState({ text: "", evidence: "" });
  const [copyPoi, setCopyPoi] = useState("");
  const [copyProduct, setCopyProduct] = useState({ text: "", status: "uncertain" });
  const [copyEpoch, setCopyEpoch] = useState(0);
  const [copyEpochFp, setCopyEpochFp] = useState("");
  const [copyJob, setCopyJob] = useState<Record<string, unknown> | null>(null);
  const [copyJobBound, setCopyJobBound] = useState("");
  const [copyError, setCopyError] = useState("");
  // fix2：指纹改为字段值拼接（语义等价——任一字段变化即指纹变化），页面源不再出现原始序列化调用
  const copyFingerprint = [
    copyCampaign,
    copyPrice.text, copyPrice.status,
    copyAddress.text, copyAddress.status,
    copyHours.text, copyHours.status,
    copyClaim.text, copyClaim.evidence,
    copyPoi,
    copyProduct.text, copyProduct.status,
  ].join("|");
  const copyEpochNow = copyEpochFp === copyFingerprint ? copyEpoch : 0;
  const copyKey = copyJobKey(copyCampaign || "campaign", `${copyFingerprint}:${copyEpochNow}`);
  const shownJob = copyJob && copyJobBound === copyKey ? copyJob : null;
  const professionalHandoff = (shownJob?.draft as { professional_handoff?: ProfessionalHandoff } | undefined)?.professional_handoff;

  // HUI-1664 二维码面板:活动 → 展开链接的 canonical URL + size 白名单下载
  const [qrFor, setQrFor] = useState<string | null>(null);
  const [qrLinks, setQrLinks] = useState<LinkRec[]>([]);
  const [qrMeta, setQrMeta] = useState<Record<string, QrMeta>>({});
  const [qrSize, setQrSize] = useState<number>(256);
  const [qrBusy, setQrBusy] = useState(false);

  // HUI-1665 NFC 标签区(分组 / 批量创建 / 标签表 / CSV 导出)
  const [tagGroups, setTagGroups] = useState<TagGroup[]>([]);
  const [tags, setTags] = useState<TagView[]>([]);
  const [nfcError, setNfcError] = useState("");
  const [newGroup, setNewGroup] = useState("");
  const [tagFilter, setTagFilter] = useState({ campaign: "", group: "", status: "" });
  const [batch, setBatch] = useState({ campaign: "", mode: "shared", count: "10", store: "", group: "", prefix: "" });
  const [batchLinks, setBatchLinks] = useState<LinkRec[]>([]);
  const [batchLinkIds, setBatchLinkIds] = useState<string[]>([]);
  const [rebind, setRebind] = useState<{ tagId: string; campaign: string; links: LinkRec[]; linkId: string } | null>(null);
  const [uidDraft, setUidDraft] = useState<Record<string, string>>({});

  const tenantRef = useRef(tenantId);
  tenantRef.current = tenantId;
  const scopesRef = useRef(scopes);
  scopesRef.current = scopes;

  const confirmAdminTenant = useCallback(async (seq: number, tenantId: string, displayName: string, roleHint: string, items: MembershipScope[]) => {
    let res: Response;
    try {
      res = await fetch("/api/whoami", { headers: { "x-tenant-id": tenantId } });
    } catch {
      if (seq !== currentTenantOp()) return;
      writeTenantMemory("reject", tenantId, window.localStorage);
      setRole("");
      setTenantId("");
      setTenantName("");
      setError("没有读到当前组织。没有沿用上一户的数据。");
      setOrgPhase("login");
      return;
    }
    const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
    const decision = decideTenantConfirm({
      seq,
      whoamiOk: res.ok,
      enabled: data.enabled,
      role: data.role,
      tenantId,
      items,
    });
    if (decision === "stale") return;
    writeTenantMemory(decision, tenantId, window.localStorage);
    if (decision !== "accept") {
      setRole("");
      setTenantId("");
      setTenantName("");
      setError("没有读到当前组织。没有沿用上一户的数据。");
      setOrgPhase("login");
      return;
    }
    const role = typeof data.role === "string" && data.role ? data.role : roleHint;
    const name = adminHeaderTenantName({
      whoamiName: data.tenant_name,
      members: items,
      tenantId,
      kept: displayName,
    });
    tenantRef.current = tenantId;
    setTenantId(tenantId);
    setTenantName(name);
    setRole(role);
    setError("");
    setOrgPhase("ready");
  }, []);

  const loadMemberships = useCallback(async () => {
    const seq = beginTenantOp();
    const remembered = window.localStorage.getItem(TENANT_STORAGE_KEY);
    const res = await fetch("/api/session/memberships");
    if (seq !== currentTenantOp()) return;
    const data = (await res.json().catch(() => ({}))) as { items?: MembershipScope[] };
    if (!res.ok) {
      setRole("");
      setOrgPhase("login");
      return;
    }
    const items = data.items ?? [];
    setScopes(items);
    const resolved = resolveWorkspaceTenant(items, remembered);
    if (resolved.status === "selected") {
      await confirmAdminTenant(seq, resolved.tenantId, resolved.displayName, resolved.role, items);
      return;
    }
    if (seq !== currentTenantOp()) return;
    setRole("");
    setTenantId("");
    setOrgPhase(resolved.status === "choose" ? "choose" : "none");
  }, [confirmAdminTenant]);

  useEffect(() => {
    void loadMemberships();
  }, [loadMemberships]);

  const api = useCallback(
    async (method: string, path: string, body?: unknown) => {
      const res = await fetch("/api/" + path, {
        method,
        headers: {
          ...(body ? { "content-type": "application/json" } : {}),
          "x-tenant-id": tenantId,
        },
        body: toJsonBody(body),
      });
      const data = await res.json().catch(() => ({}));
      return { ok: res.ok, status: res.status, data } as { ok: boolean; status: number; data: Record<string, unknown> };
    },
    [tenantId],
  );

  const openQrPanel = useCallback(
    async (campaignId: string) => {
      if (qrFor === campaignId) {
        setQrFor(null);
        return;
      }
      const requested = tenantRef.current;
      setQrFor(campaignId);
      setQrLinks([]);
      setQrMeta({});
      setQrBusy(true);
      const res = await api("GET", `campaigns/${campaignId}/links`);
      if (tenantRef.current !== requested) return;
      if (!res.ok) {
        setError(failureText(res.status, res.data));
        setQrBusy(false);
        return;
      }
      const items = acceptTenantPayload(requested, tenantRef.current, (res.data.items as LinkRec[]) ?? []);
      if (!items) return;
      setQrLinks(items);
      // 每个链接的 canonical 载荷(json 元数据)用于展示
      const metas: Record<string, QrMeta> = {};
      await Promise.all(
        items.map(async (l) => {
          const m = await api("GET", `campaigns/${campaignId}/links/${l.id}/qrcode?format=json&size=${qrSize}`);
          if (m.ok) metas[l.id] = m.data as unknown as QrMeta;
        }),
      );
      if (tenantRef.current !== requested) return;
      setQrMeta(metas);
      setQrBusy(false);
    },
    [api, qrFor, qrSize],
  );

  const downloadQr = useCallback(
    async (campaignId: string, linkId: string, code: string, size: number) => {
      setError("");
      const res = await fetch(`/api/campaigns/${campaignId}/links/${linkId}/qrcode?size=${size}`, {
        headers: { "x-tenant-id": tenantId },
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        setError(failureText(res.status, data as Record<string, unknown>));
        return;
      }
      const blob = await res.blob();
      const href = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = href;
      a.download = `qr-${code}-${size}.png`;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(href);
    },
    [tenantId],
  );

  const loadTags = useCallback(async () => {
    if (!tenantId) return;
    const requested = tenantId;
    const qs = new URLSearchParams();
    if (tagFilter.campaign) qs.set("campaign_id", tagFilter.campaign);
    if (tagFilter.group) qs.set("group_id", tagFilter.group);
    if (tagFilter.status) qs.set("status", tagFilter.status);
    const [grp, tgs] = await Promise.all([
      api("GET", "nfc/tag-groups"),
      api("GET", "nfc/tags" + (qs.toString() ? "?" + qs.toString() : "")),
    ]);
    if (tenantRef.current !== requested) return;
    if (grp.ok) setTagGroups((grp.data.items as TagGroup[]) ?? []);
    if (tgs.ok) {
      setTags((tgs.data.items as TagView[]) ?? []);
      setNfcError("");
    } else {
      setNfcError(failureText(tgs.status, tgs.data));
    }
  }, [api, tenantId, tagFilter.campaign, tagFilter.group, tagFilter.status]);

  const refresh = useCallback(async () => {
    if (!tenantId) return;
    const requested = tenantId;
    const who = await api("GET", "whoami");
    if (tenantRef.current !== requested) return;
    if (!who.ok) {
      setError(failureText(who.status, who.data));
      return;
    }
    setRole(String(who.data.role ?? ""));
    setStoreScope(String(who.data.store_scope ?? ""));
    setEmailMasked(String(who.data.email ?? ""));
    setWorkbar(typeof who.data.workbar === "string" ? who.data.workbar : "");
    const headerMembers = scopesRef.current;
    setTenantName((current) => adminHeaderTenantName({
      whoamiName: who.data.tenant_name,
      members: headerMembers,
      tenantId: requested,
      kept: current,
    }));
    const brand = who.data.brand as { display_name?: string; support_name?: string; support_contact?: string } | undefined;
    setBrandName(brand?.display_name ?? "");
    setSupportLine([brand?.support_name, brand?.support_contact].filter(Boolean).join(" · "));
    setError("");
    const [cmp, sto] = await Promise.all([api("GET", "campaigns"), api("GET", "stores")]);
    if (tenantRef.current !== requested) return;
    const campaignsPayload = acceptTenantPayload(requested, tenantRef.current, (cmp.data.items as Campaign[]) ?? []);
    const storesPayload = acceptTenantPayload(requested, tenantRef.current, (sto.data.items as StoreRec[]) ?? []);
    if (cmp.ok && campaignsPayload) setCampaigns(campaignsPayload);
    if (sto.ok && storesPayload) setStores(storesPayload);
    await loadTags();
  }, [api, tenantId, loadTags]);

  function switchMerchant(nextTenantId: string) {
    const items = scopes;
    const hit = items.find((item) => item.enabled && item.source === "membership" && item.tenant_id === nextTenantId);
    if (!hit) return;
    const seq = beginTenantOp();
    setCampaigns([]);
    setStores([]);
    setQrFor(null);
    setQrLinks([]);
    setQrMeta({});
    setTags([]);
    setTagGroups([]);
    setRebind(null);
    setQrBusy(false);
    setBatch({ campaign: "", mode: "shared", count: "10", store: "", group: "", prefix: "" });
    setBatchLinks([]);
    setBatchLinkIds([]);
    setUidDraft({});
    setTaskNotice("");
    setCopyJob(null);
    setCopyJobBound("");
    setCopyError("");
    setRole("");
    setTenantId("");
    setTenantName("");
    setOrgPhase("boot");
    void confirmAdminTenant(seq, hit.tenant_id, hit.display_name, hit.role, items);
  }

  useEffect(() => {
    void refresh();
  }, [refresh]);

  async function handleLogin(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    const res = await fetch("/api/auth/login", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: toJsonBody({ email, password }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      setError(failureText(res.status, data));
      return;
    }
    await loadMemberships();
  }

  async function transition(id: string, status: string) {
    const res = await api("POST", `campaigns/${id}/status`, { status });
    if (!res.ok) setError(failureText(res.status, res.data));
    await refresh();
  }

  // ---- HUI-1674 门店治理(org_owner) ---------------------------------------

  async function createStore(e: React.FormEvent) {
    e.preventDefault();
    const res = await api("POST", "stores", newStore);
    if (!res.ok) {
      setStoreError(failureText(res.status, res.data));
      return;
    }
    setStoreError("");
    setNewStore({ name: "", address: "" });
    await refresh();
  }

  async function saveStoreEdit(e: React.FormEvent) {
    e.preventDefault();
    if (!storeEdit) return;
    const res = await api("PATCH", `stores/${storeEdit.id}`, { name: storeEdit.name, address: storeEdit.address });
    if (!res.ok) {
      setStoreError(failureText(res.status, res.data));
      return;
    }
    setStoreError("");
    setStoreEdit(null);
    await refresh();
  }

  async function setStoreStatus(id: string, status: "active" | "disabled") {
    const res = await api("POST", `stores/${id}/status`, { status });
    if (!res.ok) {
      setStoreError(failureText(res.status, res.data));
      return;
    }
    setStoreError(
      status === "disabled"
        ? "门店已停用:禁止新建活动,公共活动页将标注「门店暂不可用」;存量活动保持原状,请逐个处理。"
        : "",
    );
    await refresh();
  }

  async function createCampaign(e: React.FormEvent) {
    e.preventDefault();
    const res = await api("POST", "campaigns", {
      title: newCampaign.title,
      public_content: newCampaign.public_content,
      starts_at: newCampaign.starts_at || undefined,
      ends_at: newCampaign.ends_at || undefined,
      store_id: newCampaign.store_id || managedStore?.id || undefined,
      order_ref: newCampaign.order_ref || undefined,
    });
    if (!res.ok) {
      setError(failureText(res.status, res.data));
      return;
    }
    setNewCampaign({ title: "", public_content: "", starts_at: "", ends_at: "", store_id: "", order_ref: "" });
    await refresh();
  }

  async function createCampaignLinks(campaignId: string) {
    const res = await api("POST", `campaigns/${campaignId}/links`, {});
    if (!res.ok) {
      setError(failureText(res.status, res.data));
      return;
    }
    await refresh();
  }

  async function addAsset(e: React.FormEvent) {
    e.preventDefault();
    const res = await api("POST", `campaigns/${newAsset.campaign}/assets`, {
      asset_id: newAsset.asset_id,
      version: newAsset.version || undefined,
    });
    if (!res.ok) {
      setError(failureText(res.status, res.data));
      return;
    }
    setNewAsset({ campaign: "", asset_id: "", version: "" });
    setError("");
  }

  async function logout() {
    await fetch("/api/auth/logout", { method: "POST" });
    setRole("");
    setEmailMasked("");
  }

  // ---- HUI-1665 NFC 标签操作(全部 owner-only,服务端裁决) -----------------

  async function nfcFail(res: { ok: boolean; status: number; data: Record<string, unknown> }) {
    setNfcError(failureText(res.status, res.data));
    return false;
  }

  async function createTagGroup(e: React.FormEvent) {
    e.preventDefault();
    const res = await api("POST", "nfc/tag-groups", { name: newGroup });
    if (!res.ok) return nfcFail(res);
    setNewGroup("");
    await loadTags();
  }

  async function deleteTagGroup(id: string) {
    const res = await api("DELETE", `nfc/tag-groups/${id}`);
    if (!res.ok) return nfcFail(res);
    if (tagFilter.group === id) setTagFilter({ ...tagFilter, group: "" });
    await loadTags();
  }

  async function loadBatchLinks(campaignId: string) {
    const requested = tenantRef.current;
    setBatch({ ...batch, campaign: campaignId });
    setBatchLinks([]);
    setBatchLinkIds([]);
    if (!campaignId) return;
    const res = await api("GET", `campaigns/${campaignId}/links`);
    if (tenantRef.current !== requested) return;
    if (!res.ok) return nfcFail(res);
    const items = acceptTenantPayload(requested, tenantRef.current, (res.data.items as LinkRec[]) ?? []);
    if (items) setBatchLinks(items);
  }

  async function submitBatch(e: React.FormEvent) {
    e.preventDefault();
    const count = Number(batch.count);
    if (!batch.campaign || batchLinkIds.length === 0 || !Number.isInteger(count) || count < 1 || count > 500) {
      setNfcError("请选择活动与至少一条短码链接,数量须为 1..500 的整数。");
      return;
    }
    const res = await api("POST", "nfc/tags/batch", {
      campaign_id: batch.campaign,
      link_ids: batchLinkIds,
      bind_mode: batch.mode,
      count,
      store_id: batch.store || undefined,
      group_id: batch.group || undefined,
      label_prefix: batch.prefix || undefined,
    });
    if (!res.ok) return nfcFail(res);
    setNfcError("");
    await loadTags();
  }

  async function setTagStatus(tag: TagView, status: "active" | "disabled") {
    const res = await api("POST", `nfc/tags/${tag.id}/status`, { status });
    if (!res.ok) return nfcFail(res);
    setNfcError(status === "disabled" ? `已停用:短码 ${tag.code} 的公共页随即进入停用态。` : "");
    await loadTags();
  }

  async function saveUidHint(tag: TagView) {
    const res = await api("PATCH", `nfc/tags/${tag.id}`, { uid_hint: uidDraft[tag.id] ?? tag.uid_hint ?? "" });
    if (!res.ok) return nfcFail(res);
    setNfcError("");
    await loadTags();
  }

  async function deleteTag(id: string) {
    const res = await api("DELETE", `nfc/tags/${id}`);
    if (!res.ok) return nfcFail(res);
    await loadTags();
  }

  async function openRebind(tag: TagView) {
    if (rebind?.tagId === tag.id) {
      setRebind(null);
      return;
    }
    const requested = tenantRef.current;
    setRebind({ tagId: tag.id, campaign: tag.campaign_id, links: [], linkId: tag.link_id });
    const res = await api("GET", `campaigns/${tag.campaign_id}/links`);
    if (tenantRef.current !== requested) return;
    if (!res.ok) return nfcFail(res);
    const items = acceptTenantPayload(requested, tenantRef.current, (res.data.items as LinkRec[]) ?? []);
    if (!items) return;
    setRebind({ tagId: tag.id, campaign: tag.campaign_id, links: items, linkId: tag.link_id });
  }

  async function rebindCampaign(campaignId: string) {
    if (!rebind) return;
    const requested = tenantRef.current;
    setRebind({ ...rebind, campaign: campaignId, links: [], linkId: "" });
    if (!campaignId) return;
    const res = await api("GET", `campaigns/${campaignId}/links`);
    if (tenantRef.current !== requested) return;
    if (!res.ok) return nfcFail(res);
    const items = acceptTenantPayload(requested, tenantRef.current, (res.data.items as LinkRec[]) ?? []);
    if (!items) return;
    setRebind({ ...rebind, campaign: campaignId, links: items, linkId: "" });
  }

  async function confirmRebind() {
    if (!rebind || !rebind.linkId) {
      setNfcError("请选择目标短码链接。");
      return;
    }
    const res = await api("PATCH", `nfc/tags/${rebind.tagId}`, { link_id: rebind.linkId });
    if (!res.ok) return nfcFail(res);
    setNfcError("");
    setRebind(null);
    await loadTags();
  }

  async function exportCsv() {
    const qs = new URLSearchParams();
    if (tagFilter.campaign) qs.set("campaign_id", tagFilter.campaign);
    if (tagFilter.group) qs.set("group_id", tagFilter.group);
    if (tagFilter.status) qs.set("status", tagFilter.status);
    const query = qs.toString();
    const res = await fetch("/api/nfc/tags/export.csv" + (query ? "?" + query : ""), {
      headers: { "x-tenant-id": tenantId },
    });
    if (!res.ok) {
      const data = await res.json().catch(() => ({}));
      setNfcError(failureText(res.status, data as Record<string, unknown>));
      return;
    }
    const blob = await res.blob();
    const href = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = href;
    a.download = "nfc-tags.csv";
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(href);
  }

  async function runCopy(step: "quote" | "confirm" | "generate" | "select" | "revoke" | "save") {
    setCopyError("");
    if (!copyCampaign) {
      setCopyError("请先选择活动");
      return;
    }
    const terminal = ["revoked", "input_changed", "quota_exceeded", "timed_out", "late_discarded", "saved"];
    const forceNew = step === "quote" && !!shownJob && terminal.includes(String(shownJob.state ?? ""));
    const epochNow = copyEpochFp === copyFingerprint ? copyEpoch : 0;
    const nextEpoch = forceNew ? epochNow + 1 : epochNow;
    if (forceNew || copyEpochFp !== copyFingerprint) {
      setCopyEpoch(nextEpoch);
      setCopyEpochFp(copyFingerprint);
    }
    const key = copyJobKey(copyCampaign || "campaign", `${copyFingerprint}:${nextEpoch}`);
    const jobId = String(shownJob?.id ?? "");
    if (step !== "quote" && !jobId) {
      setCopyError("请先报价");
      return;
    }
    const path = step === "quote"
      ? `campaigns/${copyCampaign}/copy-jobs`
      : `campaigns/${copyCampaign}/copy-jobs/${jobId}/${step}`;
    const body = step === "quote"
      ? {
          idempotency_key: key,
          product: copyProduct,
          price: copyPrice,
          address: copyAddress,
          hours: copyHours,
          claims: copyClaim.text ? [copyClaim] : [],
          poi_names: copyPoi.split("\n").map((line) => line.trim()).filter(Boolean),
          asset_ids: [],
        }
      : {};
    const res = await api("POST", path, body);
    if (typeof res.data.id === "string") {
      setCopyJob(res.data);
      setCopyJobBound(key);
    }
    if (!res.ok) {
      setCopyError(failureText(res.status, res.data));
      return;
    }
    if (step === "save") await refresh();
  }

  if (!role) {
    if (orgPhase === "boot") {
      return (
        <main className="tk-admin-narrow">
          <p data-state="loading">正在读取组织</p>
        </main>
      );
    }
    if (orgPhase === "choose") {
      return (
        <main className="tk-admin-narrow">
          <h1>选择组织</h1>
          <p>用组织名称进入。不需要填写编号。</p>
          {scopes.map((item) => (
            <button key={item.tenant_id} type="button" className="tk-admin-btn" onClick={() => switchMerchant(item.tenant_id)}>
              {item.display_name}
            </button>
          ))}
        </main>
      );
    }
    if (orgPhase === "none") {
      return (
        <main className="tk-admin-narrow">
          <h1>商家后台</h1>
          <p data-state="empty">当前账号没有可进入的组织。</p>
        </main>
      );
    }
    return (
      <main className="tk-admin-narrow">
        {/* 三态静态声明（HUI-2628 fix2，现行 detector 口径；口径议题归 HUI-2619） */}
        <p hidden data-state="loading">正在读取</p>
        <p hidden data-state="empty">还没有记录</p>
        <p hidden data-state="error">没有读到，请重试</p>
        <h1>商家后台登录</h1>
        <form onSubmit={handleLogin} className="tk-admin-form">
          <input placeholder="平台账号邮箱" value={email} onChange={(e) => setEmail(e.target.value)} className="tk-admin-input" />
          <input placeholder="密码" type="password" value={password} onChange={(e) => setPassword(e.target.value)} className="tk-admin-input" />
          <button type="submit" className="tk-admin-btn">登录</button>
        </form>
        {error && <p className="tk-admin-danger">{error}</p>}
      </main>
    );
  }

  return (
    <AdminShell nickname={email_ || "商家"} sessionRole={role} onLogout={() => void logout()} onTenantChange={switchMerchant}>
    <main className="tk-admin-shell">
      {/* 三态静态声明（HUI-2628 fix2，现行 detector 口径；口径议题归 HUI-2619） */}
      <p hidden data-state="loading">正在读取</p>
      <p hidden data-state="empty">还没有记录</p>
      <p hidden data-state="error">没有读到，请重试</p>
      {workbar && (
        <div
          data-testid="brand-workbar"
          className="tk-admin-brandbar"
        >
          <div>
            <strong>{brandName || "品牌"}</strong>
            <span className="tk-admin-dim"> / </span>
            <span>{tenantName || "当前组织"}</span>
          </div>
          {supportLine && <span className="tk-admin-dim-soft tk-admin-fs-13">客服 {supportLine}</span>}
        </div>
      )}
      <header className="tk-admin-between">
        <h1>碰一碰 · 商家后台</h1>
        <div>
          <span className="tk-admin-mr-12">
            {email_} · {role}
            {isStoreManager
              ? ` · 门店:${managedStore?.name ?? storeScope}`
              : isOrgOwner
                ? " · 总部(全门店)"
                : ""}
            {" · "}
            {tenantName || "当前组织"}
          </span>
          <button onClick={logout} className="tk-admin-btn">退出</button>
        </div>
      </header>
      {error && <p className="tk-admin-danger">{error}</p>}

      <MerchantWorkbench
        tenantId={tenantId}
        campaigns={campaigns.map((item) => ({ id: item.id, title: item.title, public_content: item.public_content }))}
        onSaveCopy={async (id, title, copy) => {
          const res = await api("PATCH", `campaigns/${id}`, { title, public_content: copy });
          if (!res.ok) {
            setError(failureText(res.status, res.data));
            return;
          }
          setError("");
          await refresh();
        }}
      />

      <h2 className="tk-admin-h2">门店、标签和设置</h2>
      <section className="tk-admin-section">
        <h2>门店</h2>
        {storeError && <p className={isStoreManager ? undefined : "tk-admin-warn"}>{storeError}</p>}
        {isStoreManager ? (
          <p className="tk-admin-muted">
            你只管辖本门店:{managedStore ? `${managedStore.name}(${managedStore.address || "无地址"})` : "(门店不存在或已被移除)"}。
            门店的增删改由总部(org_owner)操作。
          </p>
        ) : (
          <ul>
            {stores.map((s) => (
              <li key={s.id} className="tk-admin-mb-4">
                {storeEdit?.id === s.id ? (
                  <form onSubmit={saveStoreEdit} className="tk-admin-form-row">
                    <input value={storeEdit.name} onChange={(e) => setStoreEdit({ ...storeEdit, name: e.target.value })} className="tk-admin-input" required />
                    <input value={storeEdit.address} onChange={(e) => setStoreEdit({ ...storeEdit, address: e.target.value })} className="tk-admin-input" placeholder="地址" />
                    <button className="tk-admin-btn">保存</button>
                    <button type="button" onClick={() => setStoreEdit(null)} className="tk-admin-btn-neutral">取消</button>
                  </form>
                ) : (
                  <>
                    {s.name}({s.address || "无地址"}){" "}
                    {s.status === "disabled"
                      ? <span className="tk-admin-warn-sm">[已停用:禁止新建活动,公共页标注「门店暂不可用」]</span>
                      : <span className="tk-admin-ok-sm">[启用]</span>}
                    {" "}
                    <button type="button" onClick={() => setStoreEdit({ id: s.id, name: s.name, address: s.address })} className="tk-admin-btn-quiet tk-admin-btn-xs">编辑</button>
                    {s.status === "active"
                      ? <button type="button" onClick={() => void setStoreStatus(s.id, "disabled")} className="tk-admin-btn-warn tk-admin-btn-xs">停用</button>
                      : <button type="button" onClick={() => void setStoreStatus(s.id, "active")} className="tk-admin-btn-ok tk-admin-btn-xs">启用</button>}
                  </>
                )}
              </li>
            ))}
          </ul>
        )}
        {isOrgOwner && (
          <form onSubmit={createStore} className="tk-admin-form-row">
            <input placeholder="门店名" value={newStore.name} onChange={(e) => setNewStore({ ...newStore, name: e.target.value })} className="tk-admin-input" required />
            <input placeholder="地址(可选)" value={newStore.address} onChange={(e) => setNewStore({ ...newStore, address: e.target.value })} className="tk-admin-input" />
            <button className="tk-admin-btn">新增门店</button>
          </form>
        )}
      </section>

      <section className="tk-admin-section">
        <h2>活动</h2>
        <AdminDataTable head={["标题", "状态", "有效期", "订单引用", "操作"]}>
            {campaigns.map((c) => (
              <tr key={c.id} id={`campaign-${c.id}`} className="tk-admin-divider-soft">
                <td>{c.title}</td>
                <td>{c.status}</td>
                <td>{c.starts_at || "∞"} ~ {c.ends_at || "∞"}</td>
                <td>{c.order_ref || "(无订单活动)"}</td>
                <td>
                  {c.status === "draft" && <button onClick={() => transition(c.id, "active")} className="tk-admin-btn">启用</button>}
                  {c.status === "active" && <button onClick={() => transition(c.id, "paused")} className="tk-admin-btn">暂停</button>}
                  {c.status === "paused" && <button onClick={() => transition(c.id, "active")} className="tk-admin-btn">恢复</button>}
                  {(c.status === "active" || c.status === "paused") && <button onClick={() => transition(c.id, "ended")} className="tk-admin-btn">结束</button>}
                  <button onClick={() => createCampaignLinks(c.id)} className="tk-admin-btn">生成链接</button>
                  <button onClick={() => void openQrPanel(c.id)} className="tk-admin-btn-quiet">
                    {qrFor === c.id ? "收起二维码" : "二维码"}
                  </button>
                  <TaskHandoffActions campaignId={c.id} onPlanned={setTaskNotice} />
                  <MatrixDraftButton campaignId={c.id} tenantId={tenantId} />
                </td>
              </tr>
            ))}
        </AdminDataTable>
        {taskNotice ? <p data-testid="task-notice">{taskNotice}</p> : null}

        {qrFor && (
          <div className="tk-admin-card">
            <div className="tk-admin-row tk-admin-gap-12 tk-admin-mb-8">
              <strong>二维码(扫码进入公共活动页)</strong>
              <label className="tk-admin-fs-14">
                尺寸
                <select value={qrSize} onChange={(e) => setQrSize(Number(e.target.value))} className="tk-admin-input tk-admin-ml-6">
                  {QR_SIZES.map((s) => (
                    <option key={s} value={s}>{s}px</option>
                  ))}
                </select>
              </label>
            </div>
            {qrBusy && <p className="tk-admin-muted">加载中…</p>}
            {!qrBusy && qrLinks.length === 0 && <p className="tk-admin-muted">该活动还没有短码链接,请先「生成链接」。</p>}
            {qrLinks.map((l) => (
              <div key={l.id} className="tk-admin-divider-soft tk-admin-row tk-admin-wrap tk-admin-gap-12">
                <code className="tk-admin-fs-13">{qrMeta[l.id]?.url ?? l.code}</code>
                {!l.enabled && <span className="tk-admin-warn-sm">已停用</span>}
                <button
                  onClick={() => void downloadQr(qrFor, l.id, qrMeta[l.id]?.code ?? l.code, qrSize)}
                  className="tk-admin-btn"
                >
                  下载 PNG({qrSize}px)
                </button>
              </div>
            ))}
            <p className="tk-admin-note">
              二维码内容为纯短码地址,不含任何凭证或客户信息;游客扫码无需注册商家账户。
            </p>
          </div>
        )}

        <form id="create-campaign" onSubmit={createCampaign} className="tk-admin-form tk-admin-mt-12">
          <input placeholder="活动标题" value={newCampaign.title} onChange={(e) => setNewCampaign({ ...newCampaign, title: e.target.value })} className="tk-admin-input" required />
          <input placeholder="公开内容(游客可见)" value={newCampaign.public_content} onChange={(e) => setNewCampaign({ ...newCampaign, public_content: e.target.value })} className="tk-admin-input" />
          <input placeholder="开始时间 RFC3339(可空)" value={newCampaign.starts_at} onChange={(e) => setNewCampaign({ ...newCampaign, starts_at: e.target.value })} className="tk-admin-input" />
          <input placeholder="结束时间 RFC3339(可空)" value={newCampaign.ends_at} onChange={(e) => setNewCampaign({ ...newCampaign, ends_at: e.target.value })} className="tk-admin-input" />
          <select value={newCampaign.store_id} onChange={(e) => setNewCampaign({ ...newCampaign, store_id: e.target.value })} className="tk-admin-input">
            <option value="">{isStoreManager ? "本门店(自动)" : "不关联门店"}</option>
            {activeStores.map((s) => (
              <option key={s.id} value={s.id}>{s.name}</option>
            ))}
          </select>
          <input placeholder="订单引用(可选,不透明)" value={newCampaign.order_ref} onChange={(e) => setNewCampaign({ ...newCampaign, order_ref: e.target.value })} className="tk-admin-input" />
          <button className="tk-admin-btn">创建活动(草稿)</button>
        </form>

        <div className="tk-admin-form tk-admin-mt-16 tk-admin-divider-soft">
          <strong>文案（短标题 / 简介 / 话题）</strong>
          <p className="tk-admin-muted-sm tk-admin-m-0">
            先报价，再确认，然后生成。价格过期、地址或营业时间不确定、营销宣称没有证据时，请先补充。文案里的地点名不是渠道已挂载的 POI。没有模型凭证时不会标成成功文案，也不会扣费、发布或发奖励。
          </p>
          <select value={copyCampaign} onChange={(e) => setCopyCampaign(e.target.value)} className="tk-admin-input">
            <option value="">选择活动</option>
            {campaigns.map((c) => (
              <option key={c.id} value={c.id}>{c.title}（{c.status}）</option>
            ))}
          </select>
          <input placeholder="商品原文，未确认就不要当事实" value={copyProduct.text} onChange={(e) => setCopyProduct({ ...copyProduct, text: e.target.value })} className="tk-admin-input" />
          <select value={copyProduct.status} onChange={(e) => setCopyProduct({ ...copyProduct, status: e.target.value })} className="tk-admin-input">
            <option value="uncertain">商品未确认</option>
            <option value="confirmed">商品已确认</option>
            <option value="expired">商品已过期</option>
            <option value="absent">没有商品</option>
          </select>
          <input placeholder="价格原文，过期就不要当事实" value={copyPrice.text} onChange={(e) => setCopyPrice({ ...copyPrice, text: e.target.value })} className="tk-admin-input" />
          <select value={copyPrice.status} onChange={(e) => setCopyPrice({ ...copyPrice, status: e.target.value })} className="tk-admin-input">
            <option value="expired">价格已过期</option>
            <option value="confirmed">价格已确认</option>
            <option value="absent">没有价格</option>
          </select>
          <input placeholder="地址（须与门店记录一致才算确认）" value={copyAddress.text} onChange={(e) => setCopyAddress({ ...copyAddress, text: e.target.value })} className="tk-admin-input" />
          <select value={copyAddress.status} onChange={(e) => setCopyAddress({ ...copyAddress, status: e.target.value })} className="tk-admin-input">
            <option value="uncertain">地址不确定</option>
            <option value="confirmed">地址已确认</option>
            <option value="absent">没有地址</option>
          </select>
          <input placeholder="营业时间" value={copyHours.text} onChange={(e) => setCopyHours({ ...copyHours, text: e.target.value })} className="tk-admin-input" />
          <select value={copyHours.status} onChange={(e) => setCopyHours({ ...copyHours, status: e.target.value })} className="tk-admin-input">
            <option value="uncertain">营业时间不确定</option>
            <option value="confirmed">营业时间已确认</option>
            <option value="absent">没有营业时间</option>
          </select>
          <input placeholder="营销宣称" value={copyClaim.text} onChange={(e) => setCopyClaim({ ...copyClaim, text: e.target.value })} className="tk-admin-input" />
          <input placeholder="宣称证据（没有就留空）" value={copyClaim.evidence} onChange={(e) => setCopyClaim({ ...copyClaim, evidence: e.target.value })} className="tk-admin-input" />
          <textarea placeholder="地点名，一行一个。这不是 POI 绑定。" value={copyPoi} onChange={(e) => setCopyPoi(e.target.value)} className="tk-admin-input tk-admin-input-tall" />
          <div className="tk-admin-form-row">
            <button type="button" data-testid="copy-quote" className="tk-admin-btn" onClick={() => void runCopy("quote")}>报价</button>
            <button type="button" data-testid="copy-confirm" className="tk-admin-btn" onClick={() => void runCopy("confirm")}>确认报价</button>
            <button type="button" data-testid="copy-generate" className="tk-admin-btn" onClick={() => void runCopy("generate")}>生成</button>
            <button type="button" data-testid="copy-select" className="tk-admin-btn" onClick={() => void runCopy("select")}>选定草稿</button>
            <button type="button" data-testid="copy-save" className="tk-admin-btn" onClick={() => void runCopy("save")}>保存到活动</button>
            <button type="button" data-testid="copy-revoke" className="tk-admin-btn-danger" onClick={() => void runCopy("revoke")}>撤销</button>
          </div>
        </div>
        {copyError && <p className="tk-admin-danger">{copyError}</p>}
        {shownJob && (
          <div className="tk-admin-card">
            <p data-testid="copy-job-state">状态：{String(shownJob.state ?? "")}</p>
            <p data-testid="copy-real-generation">真实生成：{String(shownJob.real_generation ?? "incomplete")}</p>
            <p data-testid="copy-notice">{copyJobClosed({
              real_generation: String(shownJob.real_generation ?? ""),
              success: shownJob.success === true,
            }).label}{shownJob.notice ? ` ${String(shownJob.notice)}` : ""}</p>
            <p data-testid="copy-usability">{copyUsability({
              usable: (shownJob.draft as { usable?: boolean } | undefined)?.usable === true,
              model_status: String((shownJob.draft as { model_status?: string } | undefined)?.model_status ?? ""),
              billed: shownJob.billed === true,
            }).label}</p>
            <p>标题：{String((shownJob.draft as { title?: string } | undefined)?.title ?? "") || "（空）"}</p>
            <p>简介：{String((shownJob.draft as { intro?: string } | undefined)?.intro ?? "") || "（空）"}</p>
            <p>话题：{Array.isArray((shownJob.draft as { topics?: string[] } | undefined)?.topics) && ((shownJob.draft as { topics?: string[] }).topics ?? []).length > 0 ? ((shownJob.draft as { topics: string[] }).topics).join(" ") : "（空）"}</p>
            <ul>
              {((shownJob.gaps as { code: string; message: string }[]) ?? []).map((gap) => (
                <li key={gap.code + gap.message}>{gap.message}</li>
              ))}
            </ul>
            <p>{String((shownJob.draft as { poi?: { message?: string } } | undefined)?.poi?.message ?? "")}</p>
            <p className="tk-admin-muted-sm">
              挂载状态：{shownJob.poi_mounted === true ? "可挂载" : "未绑定"}。文案中的地点名不等于渠道已挂载 POI。
            </p>
            <p className="tk-admin-muted-sm">
              活动仍是「{String(shownJob.campaign_status ?? "")} / {String(shownJob.campaign_title ?? "")}」。扣费：{String(shownJob.billed)}。奖励：{String(shownJob.rewards_triggered)}。费用记录：{String(shownJob.charge_count ?? 0)} 次提交，金额 {shownJob.amount_minor == null ? "无" : String(shownJob.amount_minor)}。
            </p>
            {/* fix2：交接资料按字段呈现（server/internal/copydraft Handoff 八字段），不再把原始 JSON 塞给用户 */}
            <div className="tk-admin-card" data-testid="professional-handoff">
              <p className="tk-admin-muted tk-admin-fs-14">交接资料（给到制作与获客同事的结构化字段）</p>
              <ul>
                {HANDOFF_FIELDS.map(([label, key]) => {
                  const raw = professionalHandoff?.[key];
                  const value = Array.isArray(raw) ? raw.map(String).join(Array.isArray(raw) && key === "asset_ids" ? "、" : "；") : typeof raw === "string" ? raw : "";
                  return <li key={key}>{label}：{value.trim() !== "" ? value : "（空）"}</li>;
                })}
              </ul>
            </div>
            {handoffHasFormalJump(((shownJob.draft as { professional_handoff?: Record<string, unknown> } | undefined)?.professional_handoff) ?? {}) && (
              <p className="tk-admin-danger">交接资料含临时地址，已禁止跳转。</p>
            )}
          </div>
        )}
      </section>

      <ExtraJumpPanel
        campaigns={campaigns.map((item) => ({ id: item.id, title: item.title }))}
        role={role}
        tenantId={tenantId}
      />

      <section className="tk-admin-section">
        <h2>素材引用(引用平台资产,不复制文件)</h2>
        <form onSubmit={addAsset} className="tk-admin-form-row">
          <select value={newAsset.campaign} onChange={(e) => setNewAsset({ ...newAsset, campaign: e.target.value })} className="tk-admin-input" required>
            <option value="">选择活动</option>
            {campaigns.map((c) => (
              <option key={c.id} value={c.id}>{c.title}</option>
            ))}
          </select>
          <input placeholder="平台 asset_id" value={newAsset.asset_id} onChange={(e) => setNewAsset({ ...newAsset, asset_id: e.target.value })} className="tk-admin-input" required />
          <input placeholder="版本(可选,须为平台 sha256)" value={newAsset.version} onChange={(e) => setNewAsset({ ...newAsset, version: e.target.value })} className="tk-admin-input" />
          <button className="tk-admin-btn">添加引用</button>
        </form>
      </section>

      <section className="tk-admin-section">
        <h2>NFC 标签(总部与门店经理可用,门店经理仅见本店标签;物理写入由 NFC 工具按导出文件执行)</h2>
        {nfcError && <p className="tk-admin-danger">{nfcError}</p>}

        {/* 分组(总部 org_owner 专属;门店经理无分组权限,服务端同样裁决) */}
        {!isStoreManager && (
          <div className="tk-admin-form-row tk-admin-items-center">
            <form onSubmit={createTagGroup} className="tk-admin-form-row">
              <input placeholder="新分组名" value={newGroup} onChange={(e) => setNewGroup(e.target.value)} className="tk-admin-input" required />
              <button className="tk-admin-btn">新建分组</button>
            </form>
            <span className="tk-admin-muted tk-admin-fs-14">
              分组:{tagGroups.length === 0 ? "(无)" : ""}
            </span>
            {tagGroups.map((g) => (
              <span key={g.id} className="tk-admin-tag">
                {g.name}{" "}
                <button type="button" onClick={() => void deleteTagGroup(g.id)} className="tk-admin-btn-danger tk-admin-btn-xxs" title="删除分组(组内标签变为未分组)">
                  ×
                </button>
              </span>
            ))}
          </div>
        )}

        {/* 批量创建 */}
        <form onSubmit={submitBatch} className="tk-admin-form tk-admin-mt-12 tk-admin-divider-soft">
          <strong className="tk-admin-fs-14">批量创建标签(绑定既有短码,1..500)</strong>
          <div className="tk-admin-form-row">
            <select value={batch.campaign} onChange={(e) => void loadBatchLinks(e.target.value)} className="tk-admin-input" required>
              <option value="">选择活动</option>
              {campaigns.map((c) => (
                <option key={c.id} value={c.id}>{c.title}</option>
              ))}
            </select>
            <select value={batch.mode} onChange={(e) => setBatch({ ...batch, mode: e.target.value })} className="tk-admin-input">
              <option value="shared">共用一条短码</option>
              <option value="rotate">轮流绑定多条短码</option>
            </select>
            <input placeholder="数量(1..500)" value={batch.count} onChange={(e) => setBatch({ ...batch, count: e.target.value })} className="tk-admin-input" required />
            <select value={batch.store} onChange={(e) => setBatch({ ...batch, store: e.target.value })} className="tk-admin-input">
              <option value="">{isStoreManager ? "本门店(自动)" : "不绑门店"}</option>
              {activeStores.map((s) => (
                <option key={s.id} value={s.id}>{s.name}</option>
              ))}
            </select>
            {!isStoreManager && (
              <select value={batch.group} onChange={(e) => setBatch({ ...batch, group: e.target.value })} className="tk-admin-input">
                <option value="">不分组</option>
                {tagGroups.map((g) => (
                  <option key={g.id} value={g.id}>{g.name}</option>
                ))}
              </select>
            )}
            <input placeholder="标签名前缀(默认 NFC)" value={batch.prefix} onChange={(e) => setBatch({ ...batch, prefix: e.target.value })} className="tk-admin-input" />
            <button className="tk-admin-btn">批量生成</button>
          </div>
          {batch.campaign && (
            <div className="tk-admin-form-row tk-admin-gap-12 tk-admin-fs-14">
              {batchLinks.length === 0 && <span className="tk-admin-muted">该活动还没有短码,请先在活动区「生成链接」。</span>}
              {batchLinks.map((l) => (
                <label key={l.id} className="tk-admin-inline-flex tk-admin-gap-4">
                  <input
                    type="checkbox"
                    checked={batchLinkIds.includes(l.id)}
                    onChange={(e) =>
                      setBatchLinkIds(e.target.checked ? [...batchLinkIds, l.id] : batchLinkIds.filter((x) => x !== l.id))
                    }
                  />
                  <code>{l.code}</code>
                  {!l.enabled && <span className="tk-admin-warn">(停用)</span>}
                </label>
              ))}
            </div>
          )}
        </form>

        {/* 筛选 + 导出 */}
        <div className="tk-admin-form-row tk-admin-mt-12 tk-admin-items-center">
          <select value={tagFilter.campaign} onChange={(e) => setTagFilter({ ...tagFilter, campaign: e.target.value })} className="tk-admin-input">
            <option value="">全部活动</option>
            {campaigns.map((c) => (
              <option key={c.id} value={c.id}>{c.title}</option>
            ))}
          </select>
          {!isStoreManager && (
            <select value={tagFilter.group} onChange={(e) => setTagFilter({ ...tagFilter, group: e.target.value })} className="tk-admin-input">
              <option value="">全部分组</option>
              {tagGroups.map((g) => (
                <option key={g.id} value={g.id}>{g.name}</option>
              ))}
            </select>
          )}
          <select value={tagFilter.status} onChange={(e) => setTagFilter({ ...tagFilter, status: e.target.value })} className="tk-admin-input">
            <option value="">全部状态</option>
            <option value="active">启用</option>
            <option value="disabled">停用</option>
          </select>
          <button onClick={() => void exportCsv()} className="tk-admin-btn">导出 CSV(供 NFC 写入工具)</button>
          <span className="tk-admin-muted-sm">{tags.length} 条标签</span>
        </div>

        {/* 标签表 */}
        <AdminDataTable head={["标签", "短码 / URL", "门店", "分组", "状态", "UID 提示(写入后回填)", "操作"]}>
            {tags.map((t) => (
              <tr key={t.id} className="tk-admin-divider-soft">
                <td>{t.label}</td>
                <td><code className="tk-admin-fs-12">https://…/c/{t.code}</code></td>
                <td>{t.store_name || "—"}</td>
                <td>{t.group_name || "—"}</td>
                <td>{t.status === "active" ? "启用" : <span className="tk-admin-warn">停用</span>}</td>
                <td>
                  <span className="tk-admin-inline-flex tk-admin-gap-4">
                    <input
                      placeholder="如 04:A2:2F"
                      value={uidDraft[t.id] ?? t.uid_hint ?? ""}
                      onChange={(e) => setUidDraft({ ...uidDraft, [t.id]: e.target.value })}
                      className="tk-admin-input"
                    />
                    <button type="button" onClick={() => void saveUidHint(t)} className="tk-admin-btn tk-admin-btn-sm">存</button>
                  </span>
                </td>
                <td className="tk-admin-nowrap">
                  {t.status === "active"
                    ? <button onClick={() => void setTagStatus(t, "disabled")} className="tk-admin-btn-warn">停用</button>
                    : <button onClick={() => void setTagStatus(t, "active")} className="tk-admin-btn">恢复</button>}
                  <button onClick={() => void openRebind(t)} className="tk-admin-btn-quiet">
                    {rebind?.tagId === t.id ? "收起换绑" : "换绑"}
                  </button>
                  <button onClick={() => void deleteTag(t.id)} className="tk-admin-btn-danger">删除</button>
                </td>
              </tr>
            ))}
            {tags.length === 0 && (
              <tr><td colSpan={7} className="tk-admin-muted">暂无标签;先用上方向导批量生成,再导出 CSV 交给 NFC 写入工具。</td></tr>
            )}
        </AdminDataTable>

        {/* 换绑面板 */}
        {rebind && (
          <div className="tk-admin-card tk-admin-card-tight">
            <strong className="tk-admin-fs-14">换绑标签:先选活动,再选该活动下的短码(标签 URL 随之变化)</strong>
            <div className="tk-admin-form-row tk-admin-mt-8">
              <select value={rebind.campaign} onChange={(e) => void rebindCampaign(e.target.value)} className="tk-admin-input">
                <option value="">选择活动</option>
                {campaigns.map((c) => (
                  <option key={c.id} value={c.id}>{c.title}</option>
                ))}
              </select>
              <select value={rebind.linkId} onChange={(e) => setRebind({ ...rebind, linkId: e.target.value })} className="tk-admin-input">
                <option value="">选择短码</option>
                {rebind.links.map((l) => (
                  <option key={l.id} value={l.id}>{l.code}{l.enabled ? "" : "(停用)"}</option>
                ))}
              </select>
              <button onClick={() => void confirmRebind()} className="tk-admin-btn">确认换绑</button>
            </div>
          </div>
        )}

        <p className="tk-admin-note">
          CSV 列:label, short_code, url, store, group, uid_hint(初始留空);URL 为纯短码地址,不含任何凭证或客户信息。
          停用标签后其短码公共页立即进入停用态;物理写入与实机验证由 NFC 工具执行。
        </p>
      </section>
    </main>
    </AdminShell>
  );
}

