"use client";

// HUI-4589 门店服务配置页（/work/stores/{id}/services）。
//
// 纪律：
//   - B07 连接域三类服务的 kind 专属表单：Wi-Fi（SSID/密码/加密方式）、加微信
//     （个人微信号/二维码 vs 企微官方「联系我」链接）、社群（群号/邀请链接/
//     二维码引用+审批备注如实透传）。4584 channels 页是通用动作面；平台专属
//     表单归本页（walkA 走查裁定）。
//   - 保存带 expected_revision 乐观锁；409 是独立冲突剧本（刷新一次点击恢复，
//     同 channels 页 D1 修复口径）。
//   - 企微回调资质诚实徽章：pending_qualification 绝不显示为已配置。
//   - 本页绝不存储或展示 AppSecret 类凭据；回调凭据由部署侧 env 持有。
//   - tenant_id 由服务端从会话解析；不手填、不展示内部 id。

import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { useParams } from "next/navigation";
import { failureText, useSession } from "@/components/work/merchant-session";
import { surfaceLabel } from "@/lib/product-finish";
import {
  buildServicesPutBody,
  canSaveServices,
  emptyConfig,
  normalizeServiceItems,
  saveServicesFailure,
  serviceSummary,
  wecomCallbackBadge,
  WIFI_ENCRYPTIONS,
  type ServiceItem,
  type ServicesDoc,
  type WecomCallbackState,
} from "@/lib/store-services";

interface DraftItem extends ServiceItem {
  isNew: boolean;
}

function toDrafts(items: unknown): DraftItem[] {
  const rows = Array.isArray(items) ? items : [];
  return normalizeServiceItems(
    rows
      .filter((row): row is Record<string, unknown> => !!row && typeof row === "object")
      .map((row) => {
        const config: Record<string, string> = {};
        const rawConfig = row.config;
        if (rawConfig && typeof rawConfig === "object") {
          for (const [key, value] of Object.entries(rawConfig as Record<string, unknown>)) {
            config[key] = typeof value === "string" ? value : "";
          }
        }
        return {
          service_id: typeof row.service_id === "string" ? row.service_id : "",
          catalog_code: typeof row.catalog_code === "string" ? row.catalog_code : "",
          kind: (typeof row.kind === "string" ? row.kind : "") as ServiceItem["kind"],
          variant: typeof row.variant === "string" ? row.variant : "",
          label: typeof row.label === "string" ? row.label : "",
          config,
          order: typeof row.order === "number" ? row.order : 0,
          enabled: row.enabled === true,
          blocker: "",
        } satisfies ServiceItem;
      }),
  )
    .slice()
    .sort((a, b) => a.order - b.order)
    .map((item) => ({ ...item, isNew: false }));
}

export default function StoreServicesPage() {
  const params = useParams<{ id: string }>();
  const storeId = params?.id ?? "";
  const session = useSession();
  const canEdit = session.role === "org_owner" || session.role === "staff" || session.role === "store_manager";

  const [phase, setPhase] = useState<"loading" | "error" | "ready">("loading");
  const [loadError, setLoadError] = useState("");
  const [doc, setDoc] = useState<ServicesDoc | null>(null);
  const [drafts, setDrafts] = useState<DraftItem[]>([]);
  const [callback, setCallback] = useState<WecomCallbackState | undefined>(undefined);
  const [revision, setRevision] = useState(0);
  const [notice, setNotice] = useState("");
  const [saveError, setSaveError] = useState("");
  const [stale, setStale] = useState(false);
  const [reloading, setReloading] = useState(false);
  const [refreshError, setRefreshError] = useState("");
  const [dirty, setDirty] = useState(false);
  const [newKind, setNewKind] = useState("");

  const applyConfig = useCallback((data: ServicesDoc) => {
    setDoc(data);
    setDrafts(toDrafts(data.items));
    setRevision(typeof data.config_revision === "number" ? data.config_revision : 0);
    setCallback(data.wecom_callback);
    setDirty(false);
    setStale(false);
    setSaveError("");
  }, []);

  const load = useCallback(async () => {
    setPhase("loading");
    setSaveError("");
    setNotice("");
    setRefreshError("");
    const res = await session.api("GET", `stores/${storeId}/services`);
    if (!res.ok) {
      setLoadError(failureText(res.status, res.data));
      setPhase("error");
      return;
    }
    applyConfig(res.data as unknown as ServicesDoc);
    setPhase("ready");
  }, [session, storeId, applyConfig]);

  // 冲突后的唯一恢复路径：一次点击 = 重取 + 表单重渲染（内联报错，不整页替换）。
  const refreshConfig = useCallback(async () => {
    setReloading(true);
    setRefreshError("");
    setNotice("");
    const res = await session.api("GET", `stores/${storeId}/services`);
    setReloading(false);
    if (!res.ok) {
      setRefreshError(`没有读到最新配置：${failureText(res.status, res.data)} 页面仍是上次读到的内容，可再点一次刷新。`);
      return;
    }
    const data = res.data as unknown as ServicesDoc;
    applyConfig(data);
    setNotice(`已读到最新配置（第 ${data.config_revision} 版）。继续修改后保存会带这一版号。`);
  }, [session, storeId, applyConfig]);

  useEffect(() => {
    if (!storeId) return;
    void load().catch(() => {
      setLoadError("没有读到，请重试");
      setPhase("error");
    });
  }, [load, storeId]);

  function patchDraft(index: number, patch: Partial<DraftItem>) {
    setDrafts((rows) =>
      rows
        .map((row, i) => {
          if (i !== index) return row;
          const merged = { ...row, ...patch };
          const [checked] = normalizeServiceItems([merged]);
          return { ...row, ...checked, isNew: row.isNew };
        })
        .map((row, i) => ({ ...row, order: i + 1 })),
    );
    setDirty(true);
  }

  function patchConfig(index: number, key: string, value: string) {
    const row = drafts[index];
    if (!row) return;
    patchDraft(index, { config: { ...row.config, [key]: value } });
  }

  function changeKind(index: number, kind: ServiceItem["kind"]) {
    const row = drafts[index];
    if (!row) return;
    const sameKind = row.kind === kind;
    patchDraft(index, {
      kind,
      config: sameKind ? row.config : emptyConfig(kind),
      variant: kind === "contact" && row.variant === "wecom" ? "wecom" : row.variant,
    });
  }

  function moveRow(index: number, delta: number) {
    setDrafts((rows) => {
      const sorted = [...rows].sort((a, b) => a.order - b.order);
      const target = index + delta;
      if (target < 0 || target >= sorted.length) return rows;
      const swapped = [...sorted];
      const tmp = swapped[index];
      swapped[index] = swapped[target];
      swapped[target] = tmp;
      return swapped.map((row, i) => ({ ...row, order: i + 1 }));
    });
    setDirty(true);
  }

  function addRow() {
    if (!newKind) return;
    setDrafts((rows) => [
      ...rows,
      {
        service_id: "",
        catalog_code: newKind === "wifi" ? "A17" : newKind === "contact" ? "A18" : "",
        kind: newKind as ServiceItem["kind"],
        variant: "",
        label: "",
        config: emptyConfig(newKind),
        order: rows.length + 1,
        enabled: false,
        blocker: "请先补全必填项",
        isNew: true,
      },
    ]);
    setNewKind("");
    setDirty(true);
    setNotice("新服务已加入列表。填好必填项并启用后保存。");
  }

  async function save() {
    if (!doc) return;
    const blocked = drafts.find((row) => row.blocker !== "");
    if (blocked) {
      setSaveError(blocked.blocker || "请先补全带提示的服务行。");
      return;
    }
    const body = buildServicesPutBody(drafts, revision);
    const res = await session.api("PUT", `stores/${storeId}/services`, body);
    if (!res.ok) {
      const failure = saveServicesFailure(res.status, res.data);
      if (failure.kind === "conflict") setStale(true);
      setSaveError(failure.text);
      return;
    }
    applyConfig(res.data as unknown as ServicesDoc);
    setStale(false);
    setNotice(`门店服务已保存，当前是配置第 ${(res.data as unknown as ServicesDoc).config_revision} 版。顾客门店页只展示已启用的服务。`);
  }

  const blockers = drafts.filter((row) => row.blocker !== "").length;
  const saveAllowed = canSaveServices({ stale, reloading, dirty, blockers });
  const badge = wecomCallbackBadge(callback);

  if (phase === "loading") return <main><p className="tk-state" data-state="loading">{surfaceLabel("loading")}</p></main>;
  if (phase === "error") {
    return (
      <main>
        <h1 className="tk-title">门店服务</h1>
        <p className="tk-state tk-danger" data-state="error">
          {loadError || surfaceLabel("error")}
          <button className="tk-quiet" type="button" data-action="retry" onClick={() => void load()}>重试</button>
        </p>
      </main>
    );
  }
  if (!doc) return <main><p className="tk-state" data-state="empty">{surfaceLabel("empty")}</p></main>;

  return (
    <main>
      <h1 className="tk-title">门店服务 · {doc.store_name}</h1>
      <p className="tk-lead">
        连接类服务在这里配置：门店 Wi-Fi、加微信（个人/企业微信）、社群与公众号二维码引用。
        点评、关注等平台动作在「渠道动作」页配置。
      </p>
      <p className="tk-note">
        顾客门店码 {doc.store_code}。当前是配置第 {revision} 版{dirty ? "（有未保存修改）" : ""}。
        Wi-Fi 密码会明文展示给到店顾客（仅展示+主动复制，不要求顾客留任何信息）。
      </p>
      <p className={badge.tone === "warn" ? "tk-warn" : "tk-note"} data-testid="wecom-callback-badge">{badge.text}</p>
      {notice ? <p className="tk-note" data-state="notice">{notice}</p> : null}
      {saveError ? (
        <p className="tk-warn" data-state={stale ? "conflict" : "save-error"}>{saveError}</p>
      ) : null}
      {stale || refreshError ? (
        <p className="tk-warn" data-state="stale">
          {refreshError ? `${refreshError}` : "配置已被他人更新。保存已暂停，先刷新对齐最新版本再继续修改。"}
          <button className="tk-quiet" type="button" data-action="refresh-config" disabled={reloading} onClick={() => void refreshConfig()}>
            {reloading ? "刷新中…" : "刷新最新配置"}
          </button>
        </p>
      ) : null}

      <ul className="tk-list">
        {drafts.map((row, index) => (
          <li key={row.service_id !== "" ? row.service_id : `new-${index}-${row.order}`}>
            <strong>{row.label || "未命名服务"}</strong>
            <span className="tk-note"> {serviceSummary(row)}</span>
            {row.enabled ? <span className="tk-note">已启用</span> : <span className="tk-unknown">已停用</span>}
            {row.blocker ? <span className="tk-warn">{row.blocker}</span> : null}
            <div className="tk-row tk-gap">
              <label className="tk-note">
                <input
                  type="checkbox"
                  checked={row.enabled}
                  disabled={!canEdit}
                  onChange={(event) => patchDraft(index, { enabled: event.target.checked })}
                />
                启用
              </label>
              <button className="tk-quiet" type="button" disabled={!canEdit} onClick={() => moveRow(index, -1)}>上移</button>
              <button className="tk-quiet" type="button" disabled={!canEdit} onClick={() => moveRow(index, 1)}>下移</button>
            </div>
            <div className="tk-row tk-gap">
              <select
                className="tk-input"
                value={row.kind}
                disabled={!canEdit}
                onChange={(event) => changeKind(index, event.target.value as ServiceItem["kind"])}
              >
                <option value="">选择类型…</option>
                <option value="wifi">门店 Wi-Fi</option>
                <option value="contact">加微信</option>
                <option value="community">社群/公众号</option>
              </select>
              {row.kind === "contact" ? (
                <select
                  className="tk-input"
                  value={row.variant}
                  disabled={!canEdit}
                  onChange={(event) => patchDraft(index, { variant: event.target.value })}
                >
                  <option value="">选择微信方式…</option>
                  <option value="personal">个人微信（二维码/微信号）</option>
                  <option value="wecom">企业微信「联系我」链接</option>
                </select>
              ) : null}
            </div>
            <input
              className="tk-input"
              value={row.label}
              placeholder="顾客看到的名称"
              disabled={!canEdit}
              onChange={(event) => patchDraft(index, { label: event.target.value })}
            />
            {row.kind === "wifi" ? (
              <div className="tk-section">
                <input
                  className="tk-input"
                  value={row.config.ssid ?? ""}
                  placeholder="Wi-Fi 名称（SSID）"
                  disabled={!canEdit}
                  onChange={(event) => patchConfig(index, "ssid", event.target.value)}
                />
                <select
                  className="tk-input"
                  value={row.config.encryption ?? "wpa2"}
                  disabled={!canEdit}
                  onChange={(event) => patchConfig(index, "encryption", event.target.value)}
                >
                  {WIFI_ENCRYPTIONS.map((option) => (
                    <option key={option.value} value={option.value}>{option.label}</option>
                  ))}
                </select>
                {(row.config.encryption ?? "") !== "open" ? (
                  <input
                    className="tk-input"
                    value={row.config.password ?? ""}
                    placeholder="Wi-Fi 密码（8-64 位；明文展示给顾客）"
                    disabled={!canEdit}
                    onChange={(event) => patchConfig(index, "password", event.target.value)}
                  />
                ) : (
                  <p className="tk-note">开放网络不留密码；顾客直接连接。</p>
                )}
              </div>
            ) : null}
            {row.kind === "contact" && row.variant === "wecom" ? (
              <div className="tk-section">
                <input
                  className="tk-input"
                  value={row.config.url ?? ""}
                  placeholder="企业微信「联系我」链接（https://work.weixin.qq.com/...）"
                  disabled={!canEdit}
                  onChange={(event) => patchConfig(index, "url", event.target.value)}
                />
                <input
                  className="tk-input"
                  value={row.config.source_note ?? ""}
                  placeholder="来源备注（可选，如：台卡 A）"
                  disabled={!canEdit}
                  onChange={(event) => patchConfig(index, "source_note", event.target.value)}
                />
              </div>
            ) : null}
            {row.kind === "contact" && row.variant === "personal" ? (
              <div className="tk-section">
                <input
                  className="tk-input"
                  value={row.config.wechat_id ?? ""}
                  placeholder="微信号（字母开头 6-20 位）"
                  disabled={!canEdit}
                  onChange={(event) => patchConfig(index, "wechat_id", event.target.value)}
                />
                <input
                  className="tk-input"
                  value={row.config.qr_ref ?? ""}
                  placeholder="二维码图片地址（公网 https）"
                  disabled={!canEdit}
                  onChange={(event) => patchConfig(index, "qr_ref", event.target.value)}
                />
              </div>
            ) : null}
            {row.kind === "community" ? (
              <div className="tk-section">
                <input
                  className="tk-input"
                  value={row.config.group_no ?? ""}
                  placeholder="群号（可选）"
                  disabled={!canEdit}
                  onChange={(event) => patchConfig(index, "group_no", event.target.value)}
                />
                <input
                  className="tk-input"
                  value={row.config.invite_url ?? ""}
                  placeholder="邀请链接（公网 https，可选）"
                  disabled={!canEdit}
                  onChange={(event) => patchConfig(index, "invite_url", event.target.value)}
                />
                <input
                  className="tk-input"
                  value={row.config.qr_ref ?? ""}
                  placeholder="二维码图片地址（公网 https，可选）"
                  disabled={!canEdit}
                  onChange={(event) => patchConfig(index, "qr_ref", event.target.value)}
                />
                <input
                  className="tk-input"
                  value={row.config.approval_note ?? ""}
                  placeholder="审批状态备注（可选，如：群主审核中——会原样展示给顾客）"
                  disabled={!canEdit}
                  onChange={(event) => patchConfig(index, "approval_note", event.target.value)}
                />
              </div>
            ) : null}
          </li>
        ))}
      </ul>
      {drafts.length === 0 ? (
        <p className="tk-state" data-state="empty">这家店还没有配置连接类服务。从下面选择类型开始。</p>
      ) : null}

      {canEdit ? (
        <div className="tk-form tk-section">
          <h2 className="tk-section-title">新增服务</h2>
          <select className="tk-input" value={newKind} onChange={(event) => setNewKind(event.target.value)}>
            <option value="">选择类型…</option>
            <option value="wifi">门店 Wi-Fi</option>
            <option value="contact">加微信</option>
            <option value="community">社群/公众号</option>
          </select>
          <button className="tk-button" type="button" disabled={!newKind} onClick={addRow}>加入列表</button>
        </div>
      ) : (
        <p className="tk-note">门店服务的修改由总部或本店经理操作，你现在是只读查看。</p>
      )}

      {canEdit ? (
        <div className="tk-row tk-gap tk-section">
          <button className="tk-button" type="button" disabled={!saveAllowed} onClick={() => void save()}>保存全部修改</button>
          <button className="tk-quiet" type="button" disabled={!dirty || reloading || stale} onClick={() => void load()}>放弃修改</button>
          {stale ? <span className="tk-note">配置已有更新版本，先点上面的「刷新最新配置」。</span> : null}
        </div>
      ) : null}

      <div className="tk-section">
        <h2 className="tk-section-title">诚实边界</h2>
        <p className="tk-note">
          本页配置只是「帮顾客找到入口」：Wi-Fi 密码展示不等于替顾客连网；微信号/二维码展示不等于已添加；
          社群审批状态原样展示。任何「已关注/已连接/已添加」都不会被本系统记录或显示。
          企微回调即使配置了凭据，也只作证据留存，添加结果以企业微信官方为准。
        </p>
      </div>

      <p className="tk-note">
        <Link href={`/work/stores/${storeId}/channels`} className="tk-quiet">前往渠道动作配置</Link>
        {" · "}
        <Link href="/work/stores" className="tk-quiet">返回门店列表</Link>
      </p>
    </main>
  );
}
