"use client";

// HUI-4584 M4 门店渠道动作页（/work/stores/{id}/channels）。
//
// 纪律：
//   - 通用动作列表/启停/排序/新增/目标摘要/测试入口；平台专属目标表单与
//     校验归 HUI-4589，本页不重复实现；tenant_id 由服务端从会话解析，本页
//     不要求也不允许手填。
//   - 普通（链接目标）与高级权限（平台授权/凭据）分区展示；本页绝不存储或
//     展示 AppSecret 类凭据——那由 4589/4593 的授权面负责。
//   - validate 是只读测试入口：三分证据 + 准入未判定，绝不说成功。
//   - 保存带 config_revision 乐观锁；409 提示刷新重试，不静默覆盖。

import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { useParams } from "next/navigation";
import { failureText, useSession } from "@/components/work/merchant-session";
import { surfaceLabel } from "@/lib/product-finish";
import {
  buildPutBody,
  catalogMissingEntries,
  channelsSaveError,
  nextOrder,
  normalizeItems,
  reorder,
  targetSummary,
  validateSummary,
  type CatalogEntry,
  type ChannelItem,
  type ValidateResult,
} from "@/lib/store-channels";

interface ChannelsDoc {
  store_id: string;
  store_name: string;
  store_code: string;
  config_revision: number;
  items: ChannelItem[];
  catalog: CatalogEntry[];
}

interface DraftItem {
  /** 服务端 action_id；未保存的新行为空串（行操作按数组下标定位）。 */
  action_id: string;
  catalog_code: string;
  catalog_label: string;
  label: string;
  target_ref: string;
  content_binding_ref: string;
  order: number;
  enabled: boolean;
  blocker: string;
  validating: boolean;
  validateText: string[];
  validateProblem: string;
}

function blockerOf(row: Pick<DraftItem, "catalog_code" | "enabled" | "target_ref">): string {
  if (row.catalog_code === "") return "请先选择目录项";
  if (row.enabled && row.target_ref.trim() === "") return "启用前需要填写目标地址";
  return "";
}

function toDraft(items: ChannelItem[]): DraftItem[] {
  return normalizeItems(items)
    .slice()
    .sort((a, b) => a.order - b.order)
    .map((item) => ({
      action_id: item.action_id,
      catalog_code: item.catalog_code,
      catalog_label: item.catalog_label || "",
      label: item.label || item.catalog_label || "",
      target_ref: item.target_ref || "",
      content_binding_ref: item.content_binding_ref || "",
      order: item.order,
      enabled: item.enabled,
      blocker: item.blocker || "",
      validating: false,
      validateText: [],
      validateProblem: "",
    }));
}

function draftsToChannelItems(rows: DraftItem[]): ChannelItem[] {
  return rows.map((row) => ({
    action_id: row.action_id,
    action_revision: 0,
    revision: 0,
    catalog_code: row.catalog_code,
    platform: "",
    kind: "",
    variant: "",
    label: row.label,
    target_ref: row.target_ref,
    content_binding_ref: row.content_binding_ref,
    order: row.order,
    enabled: row.enabled,
  }));
}

export default function StoreChannelsPage() {
  const params = useParams<{ id: string }>();
  const storeId = params?.id ?? "";
  const session = useSession();
  const canEdit = session.role === "org_owner" || session.role === "staff" || session.role === "store_manager";

  const [phase, setPhase] = useState<"loading" | "error" | "ready">("loading");
  const [loadError, setLoadError] = useState("");
  const [doc, setDoc] = useState<ChannelsDoc | null>(null);
  const [drafts, setDrafts] = useState<DraftItem[]>([]);
  const [revision, setRevision] = useState(0);
  const [notice, setNotice] = useState("");
  const [saveError, setSaveError] = useState("");
  const [stale, setStale] = useState(false);
  const [dirty, setDirty] = useState(false);
  const [newCode, setNewCode] = useState("");

  const load = useCallback(async () => {
    setPhase("loading");
    setSaveError("");
    setNotice("");
    const res = await session.api("GET", `stores/${storeId}/actions`);
    if (!res.ok) {
      setLoadError(failureText(res.status, res.data));
      setPhase("error");
      return;
    }
    const data = res.data as unknown as ChannelsDoc;
    setDoc(data);
    setDrafts(toDraft(Array.isArray(data.items) ? data.items : []));
    setRevision(data.config_revision);
    setDirty(false);
    setStale(false);
    setPhase("ready");
  }, [session, storeId]);

  useEffect(() => {
    if (!storeId) return;
    void load().catch(() => {
      setLoadError("没有读到，请重试");
      setPhase("error");
    });
  }, [load, storeId]);

  function patchDraft(index: number, patch: Partial<DraftItem>) {
    setDrafts((rows) =>
      rows.map((row, i) => {
        if (i !== index) return row;
        const merged = { ...row, ...patch };
        return { ...merged, blocker: blockerOf(merged) };
      }),
    );
    setDirty(true);
  }

  function moveRow(index: number, delta: number) {
    setDrafts((rows) => {
      const items = draftsToChannelItems(rows);
      const anchor = rows[index];
      const moved = reorder(items, anchor.action_id, delta);
      const orderOf = new Map(moved.map((item) => [item.action_id, item.order]));
      return rows
        .map((row) => ({ ...row, order: orderOf.get(row.action_id) ?? row.order }))
        .slice()
        .sort((a, b) => a.order - b.order);
    });
    setDirty(true);
  }

  function addRow() {
    if (!doc || !newCode) return;
    const entry = doc.catalog.find((item) => item.code === newCode);
    if (!entry) return;
    const row: DraftItem = {
      action_id: "",
      catalog_code: entry.code,
      catalog_label: entry.label,
      label: entry.label,
      target_ref: "",
      content_binding_ref: "",
      order: nextOrder(draftsToChannelItems(drafts)),
      enabled: false,
      blocker: blockerOf({ catalog_code: entry.code, enabled: false, target_ref: "" }),
      validating: false,
      validateText: [],
      validateProblem: "",
    };
    setDrafts((rows) => [...rows, row]);
    setNewCode("");
    setDirty(true);
    setNotice("新动作已加入列表。填好目标地址并启用后保存。");
  }

  async function save() {
    if (!doc) return;
    const blocked = drafts.find((row) => row.blocker !== "");
    if (blocked) {
      setSaveError(blocked.blocker || "请先补全带提示的动作行。");
      return;
    }
    const body = buildPutBody(draftsToChannelItems(drafts), revision);
    const res = await session.api("PUT", `stores/${storeId}/actions`, body);
    if (!res.ok) {
      if (res.status === 409) setStale(true);
      setSaveError(channelsSaveError(res.status, res.data));
      return;
    }
    const data = res.data as unknown as ChannelsDoc;
    setDoc(data);
    setDrafts(toDraft(Array.isArray(data.items) ? data.items : []));
    setRevision(data.config_revision);
    setDirty(false);
    setSaveError("");
    setStale(false);
    setNotice("渠道动作已保存。顾客门店页使用的是已启用的动作。");
  }

  async function runValidate(index: number) {
    const row = drafts[index];
    if (!row) return;
    if (row.action_id === "") {
      setNotice("先保存这个新动作，再做测试。");
      return;
    }
    patchDraft(index, { validating: true, validateText: [], validateProblem: "" });
    const res = await session.api("POST", `actions/${row.action_id}/validate`, {});
    if (!res.ok) {
      patchDraft(index, { validating: false });
      setSaveError(failureText(res.status, res.data));
      return;
    }
    const summary = validateSummary(res.data as unknown as ValidateResult);
    patchDraft(index, {
      validating: false,
      validateProblem: summary.problem,
      validateText: summary.lines,
    });
  }

  const catalog = doc?.catalog ?? [];
  const missing = catalogMissingEntries(catalog, drafts.map((row) => ({ catalog_code: row.catalog_code } as ChannelItem)));
  const configuredCodes = new Set(drafts.map((row) => row.catalog_code));

  if (phase === "loading") return <main><p className="tk-state" data-state="loading">{surfaceLabel("loading")}</p></main>;
  if (phase === "error") {
    return (
      <main>
        <h1 className="tk-title">渠道动作</h1>
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
      <h1 className="tk-title">渠道动作 · {doc.store_name}</h1>
      <p className="tk-lead">
        门店的顾客入口在这里逐项配置：点评、关注、Wi-Fi、加微信、团购等。
        每个动作是独立实例，同一种类可以配多条，互不覆盖。
      </p>
      <p className="tk-note">
        顾客门店码 {doc.store_code}。当前是配置第 {revision} 版{dirty ? "（有未保存修改）" : ""}。
        顾客侧页面由门店首页发布版本另行控制。
      </p>
      {notice ? <p className="tk-note">{notice}</p> : null}
      {saveError ? <p className="tk-warn">{saveError}</p> : null}
      {stale ? (
        <p className="tk-warn">
          配置已被他人更新。
          <button className="tk-quiet" type="button" onClick={() => void load()}>刷新最新配置</button>
        </p>
      ) : null}

      <ul className="tk-list">
        {drafts.map((row, index) => (
          <li key={row.action_id !== "" ? row.action_id : `new-${index}-${row.order}`}>
            <strong>{row.label || row.catalog_label}</strong>
            <span className="tk-note"> {row.catalog_code} · {row.catalog_label} · 目标：{targetSummary(row.target_ref)}</span>
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
              <button className="tk-quiet" type="button" disabled={!canEdit || row.validating} onClick={() => void runValidate(index)}>
                {row.validating ? "测试中…" : "测试"}
              </button>
            </div>
            <input
              className="tk-input"
              value={row.label}
              placeholder="顾客看到的名称"
              disabled={!canEdit}
              onChange={(event) => patchDraft(index, { label: event.target.value })}
            />
            <input
              className="tk-input"
              value={row.target_ref}
              placeholder="目标地址（平台主页 / POI / 商品页，https）"
              disabled={!canEdit}
              onChange={(event) => patchDraft(index, { target_ref: event.target.value })}
            />
            {row.validateProblem || row.validateText.length > 0 ? (
              <div className="tk-note">
                {row.validateProblem ? <p className="tk-warn">{row.validateProblem}</p> : null}
                {row.validateText.map((line, lineIndex) => (
                  <p key={lineIndex}>{line}</p>
                ))}
              </div>
            ) : null}
          </li>
        ))}
      </ul>
      {drafts.length === 0 ? (
        <p className="tk-state" data-state="empty">这家店还没有配置动作。从下面选择目录项开始。</p>
      ) : null}

      {canEdit ? (
        <div className="tk-form tk-section">
          <h2 className="tk-section-title">新增动作</h2>
          <p className="tk-note">同一个目录项可以配置多条（例如五家平台的点评、多个关注账号）。</p>
          <select className="tk-input" value={newCode} onChange={(event) => setNewCode(event.target.value)}>
            <option value="">选择目录项…</option>
            {catalog.map((entry) => (
              <option key={entry.code} value={entry.code}>
                {entry.code} {entry.label}{configuredCodes.has(entry.code) ? "（已有，可再加）" : ""}
              </option>
            ))}
          </select>
          <button className="tk-button" type="button" disabled={!newCode} onClick={addRow}>加入列表</button>
        </div>
      ) : (
        <p className="tk-note">渠道动作的修改由总部或本店经理操作，你现在是只读查看。</p>
      )}

      {canEdit ? (
        <div className="tk-row tk-gap tk-section">
          <button className="tk-button" type="button" disabled={!dirty} onClick={() => void save()}>保存全部修改</button>
          <button className="tk-quiet" type="button" disabled={!dirty} onClick={() => void load()}>放弃修改</button>
        </div>
      ) : null}

      <div className="tk-section">
        <h2 className="tk-section-title">目录缺口</h2>
        {missing.length === 0 ? (
          <p className="tk-note">30 项目录里这家店配置的项目都已覆盖。</p>
        ) : (
          <p className="tk-note">
            还未配置：{missing.map((entry) => `${entry.code} ${entry.label}`).join("、")}。目录支持不等于平台侧全部可执行，具体以各渠道能力为准。
          </p>
        )}
      </div>

      <div className="tk-section">
        <h2 className="tk-section-title">高级权限与平台授权</h2>
        <p className="tk-note">
          平台授权、AppSecret 等凭据不在这页存储或展示。普通链接目标在上面配置；
          平台专属目标与授权由对应平台的接入配置负责（授权与凭据另有专门入口）。
          本页任何动作都不会代顾客发布、关注、加群或收费。
        </p>
      </div>

      <p className="tk-note">
        <Link href="/work/stores" className="tk-quiet">返回门店列表</Link>
      </p>
    </main>
  );
}
