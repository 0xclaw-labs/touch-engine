"use client";

// HUI-4584 M4 门店渠道动作页（/work/stores/{id}/channels）。
//
// 纪律：
//   - 通用动作列表/启停/排序/新增/内容包绑定/目标摘要/测试入口；平台专属目标
//     表单与校验归 HUI-4589；tenant_id 由服务端从会话解析，本页不要求也不允许手填。
//   - 普通（链接目标+内容包绑定）与高级权限（平台授权/凭据）分区展示；本页
//     绝不存储或展示 AppSecret 类凭据。
//   - validate 是只读测试入口：三分证据 + 准入未判定，绝不说成功。
//   - 冲突剧本（D1 修复）：保存 409 → 独立冲突提示 + 停用保存；「刷新最新配置」
//     一次点击完成重取+表单重渲染（横幅内联报错，不整页替换、不丢未保存上下文）；
//     刷新成功后 revision 与服务端一致，保存才重新放行——expected_revision
//     永远等于页面当前显示的服务端版本。
//   - 内容包绑定（D2 修复）：下拉只列本店「已采用」内容包（GET
//     /api/v1/stores/{id}/content-packs 的 adoptions 非空项）；值 pack:<id>，
//     可清空；非法形态前端先拦 + 后端 400 单独文案。

import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { useParams } from "next/navigation";
import { failureText, useSession } from "@/components/work/merchant-session";
import { surfaceLabel } from "@/lib/product-finish";
import {
  adoptedPackOptions,
  bindingBlocker,
  buildPutBody,
  canSave,
  catalogMissingEntries,
  channelsSaveError,
  nextOrder,
  normalizeItems,
  refreshNotice,
  reorder,
  saveFailure,
  targetSummary,
  validateSummary,
  type CatalogEntry,
  type ChannelItem,
  type ContentPackOption,
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

function blockerOf(row: Pick<DraftItem, "catalog_code" | "enabled" | "target_ref" | "content_binding_ref">): string {
  if (row.catalog_code === "") return "请先选择目录项";
  if (row.enabled && row.target_ref.trim() === "") return "启用前需要填写目标地址";
  return bindingBlocker(row.content_binding_ref);
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
  const [reloading, setReloading] = useState(false);
  const [refreshError, setRefreshError] = useState("");
  const [dirty, setDirty] = useState(false);
  const [newCode, setNewCode] = useState("");
  const [packs, setPacks] = useState<ContentPackOption[]>([]);
  const [packsState, setPacksState] = useState<"loading" | "ready" | "unavailable">("loading");

  const loadPacks = useCallback(async () => {
    setPacksState("loading");
    const res = await session.api("GET", `stores/${storeId}/content-packs`);
    if (res.ok) {
      setPacks(adoptedPackOptions(res.data));
      setPacksState("ready");
      return;
    }
    // 内容包列表不可用（未合流/无权限/网络）：绑定下拉降级为提示，不阻断动作配置。
    setPacks([]);
    setPacksState("unavailable");
  }, [session, storeId]);

  const applyConfig = useCallback((data: ChannelsDoc) => {
    setDoc(data);
    setDrafts(toDraft(Array.isArray(data.items) ? data.items : []));
    setRevision(data.config_revision);
    setDirty(false);
    setStale(false);
    setSaveError("");
  }, []);

  const load = useCallback(async () => {
    setPhase("loading");
    setSaveError("");
    setNotice("");
    setRefreshError("");
    const res = await session.api("GET", `stores/${storeId}/actions`);
    if (!res.ok) {
      setLoadError(failureText(res.status, res.data));
      setPhase("error");
      return;
    }
    applyConfig(res.data as unknown as ChannelsDoc);
    setNotice("");
    setPhase("ready");
    void loadPacks();
  }, [session, storeId, applyConfig, loadPacks]);

  // 冲突后的唯一恢复路径：一次点击 = 重取 + 表单重渲染。已有配置在屏时失败
  // 只内联报错（保留页面与未保存状态），不再整页替换、不再需要第二跳。
  const refreshConfig = useCallback(async () => {
    setReloading(true);
    setRefreshError("");
    setNotice("");
    const res = await session.api("GET", `stores/${storeId}/actions`);
    setReloading(false);
    if (!res.ok) {
      setRefreshError(`没有读到最新配置：${failureText(res.status, res.data)} 页面仍是上次读到的内容，可再点一次刷新。`);
      return;
    }
    const data = res.data as unknown as ChannelsDoc;
    applyConfig(data);
    setNotice(refreshNotice(data.config_revision));
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
      blocker: blockerOf({ catalog_code: entry.code, enabled: false, target_ref: "", content_binding_ref: "" }),
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
      // 409 是独立剧本：冲突提示 + 停用保存 + 只能走刷新；绝不吞成泛化失败。
      const failure = saveFailure(res.status, res.data);
      if (failure.kind === "conflict") {
        setStale(true);
        setSaveError(failure.text);
      } else {
        setSaveError(failure.text);
      }
      return;
    }
    const data = res.data as unknown as ChannelsDoc;
    applyConfig(data);
    setStale(false);
    setNotice(`渠道动作已保存，当前是配置第 ${data.config_revision} 版。顾客门店页使用的是已启用的动作。`);
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
  const blockers = drafts.filter((row) => row.blocker !== "").length;
  const saveAllowed = canSave({ stale, reloading, dirty, blockers });

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
            <label className="tk-note">
              绑定内容包（已采用的内容包，供顾客「换一个」等场景引用）
              <select
                className="tk-input"
                value={row.content_binding_ref}
                disabled={!canEdit}
                onChange={(event) => patchDraft(index, { content_binding_ref: event.target.value })}
              >
                <option value="">不绑定内容包</option>
                {row.content_binding_ref !== "" && !packs.some((p) => `pack:${p.packId}` === row.content_binding_ref) ? (
                  <option value={row.content_binding_ref}>{row.content_binding_ref}（不在已采用列表）</option>
                ) : null}
                {packs.map((p) => (
                  <option key={p.packId} value={`pack:${p.packId}`}>
                    {p.title}{p.platforms.length > 0 ? `（${p.platforms.join("/")}）` : ""}
                  </option>
                ))}
              </select>
            </label>
            {packsState === "unavailable" ? (
              <p className="tk-note">内容包列表当前不可用，暂不能调整绑定；已有绑定保持不变。</p>
            ) : null}
            {packsState === "ready" && packs.length === 0 ? (
              <p className="tk-note">这家店还没有已采用的内容包。先在内容包面板采用，再回到这里绑定。</p>
            ) : null}
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
          <button className="tk-button" type="button" disabled={!saveAllowed} onClick={() => void save()}>保存全部修改</button>
          <button className="tk-quiet" type="button" disabled={!dirty || reloading || stale} onClick={() => void load()}>放弃修改</button>
          {stale ? <span className="tk-note">配置已有更新版本，先点上面的「刷新最新配置」。</span> : null}
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
          平台授权、AppSecret 等凭据不在这页存储或展示。普通链接目标与内容包绑定在上面配置；
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
