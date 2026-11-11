"use client";

// HUI-1676 M3 门店首页设置台（/work/stores/{id}/page 页面本体）。
//
// 纪律：
//   - 草稿持久化在服务端（storefront-draft），刷新/重登可恢复；本地未保存
//     修改有显式提示，保存/放弃两个动作明确分开；
//   - 行业模板只是段落骨架：只归组本店已配置且启用的动作实例，绝不虚构入口；
//   - 手机预览用真实预览数据面（GET storefront-preview，会话约束、白名单
//     同形投影），叠加明显预览标识；不另造假壳、不产生真实参与/发布/费用；
//   - 发布三闸在服务端单点：预检可发布 + 用户确认变更摘要 + 指纹回带；
//     前端 disabled 只是第一道 UX，不是授权；
//   - 逐动作预检失败只影响相关动作：一个动作缺配置不拖垮其他动作的 ready；
//   - 回滚仍受实时撤权否决：被拒时逐项给原因，不静默删项、不改写历史；
//   - 4593 模板 applied 回调缝：商家对分发的模板执行「应用模板」后，发布
//     成功即回调 PATCH /api/v1/template-assignments/{id}（status=applied,
//     applied_revision=发布版本, reason 必填）；回调失败如实提示不吞掉。

import { useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useParams } from "next/navigation";
import { failureText, useSession } from "@/components/work/merchant-session";
import { surfaceLabel } from "@/lib/product-finish";
import {
  TEMPLATE_PRESETS,
  applyTemplate,
  buildPublishBody,
  buildRollbackBody,
  canPublish,
  previewRows,
  publishFailureText,
  summaryLineCopy,
  verdictCopy,
  type DealOfferItem,
  type PreflightResult,
  type PreviewPayload,
  type RevisionsDoc,
  type StorefrontDraftDoc,
  type StorefrontSection,
} from "@/lib/storefront-page";

interface ChannelsDoc {
  store_id: string;
  store_name: string;
  store_code: string;
  config_revision: number;
  items: {
    action_id: string;
    catalog_code: string;
    catalog_label?: string;
    label: string;
    enabled: boolean;
    order: number;
  }[];
}

interface TemplateAssignmentRow {
  id: string;
  store_id?: string;
  template_ref?: string;
  source_version?: string;
  status?: string;
}

export default function StorefrontPageDesk() {
  const params = useParams<{ id: string }>();
  const storeId = params?.id ?? "";
  const session = useSession();
  const canEdit = session.role === "org_owner" || session.role === "staff" || session.role === "store_manager";

  const [phase, setPhase] = useState<"loading" | "error" | "ready">("loading");
  const [loadError, setLoadError] = useState("");
  const [storeName, setStoreName] = useState("");
  const [storeCode, setStoreCode] = useState("");
  const [draft, setDraft] = useState<StorefrontDraftDoc | null>(null);
  const [sections, setSections] = useState<StorefrontSection[]>([]);
  const [channelItems, setChannelItems] = useState<ChannelsDoc["items"]>([]);
  const [offers, setOffers] = useState<DealOfferItem[]>([]);
  const [offersState, setOffersState] = useState<"loading" | "ready" | "unavailable">("loading");
  const [dirty, setDirty] = useState(false);
  const [notice, setNotice] = useState("");
  const [saveError, setSaveError] = useState("");

  const [pre, setPre] = useState<PreflightResult | null>(null);
  const [preflighting, setPreflighting] = useState(false);
  const [confirmed, setConfirmed] = useState(false);
  const [publishing, setPublishing] = useState(false);
  const [pendingApplied, setPendingApplied] = useState("");
  const [assignments, setAssignments] = useState<TemplateAssignmentRow[]>([]);
  const [assignmentsState, setAssignmentsState] = useState<"loading" | "ready" | "unavailable">("loading");

  const [revisions, setRevisions] = useState<RevisionsDoc | null>(null);
  const [rollingBack, setRollingBack] = useState(0);

  const [preview, setPreview] = useState<PreviewPayload | null>(null);
  const [previewSource, setPreviewSource] = useState<"draft" | "live">("draft");
  const [previewLoading, setPreviewLoading] = useState(false);

  const publishedRevision = draft?.published_revision ?? revisions?.published_revision ?? 0;
  const draftRevision = draft?.revision ?? 0;

  const loadDraft = useCallback(async () => {
    const res = await session.api("GET", `stores/${storeId}/storefront-draft`);
    if (!res.ok) {
      setLoadError(failureText(res.status, res.data));
      setPhase("error");
      return;
    }
    const data = res.data as unknown as StorefrontDraftDoc;
    setDraft(data);
    setSections(data.sections ?? []);
    setStoreCode(data.store_code ?? "");
    setDirty(false);
    setConfirmed(false);
    setPre(null);
    setPhase("ready");
  }, [session, storeId]);

  const loadChannels = useCallback(async () => {
    const res = await session.api("GET", `stores/${storeId}/actions`);
    if (!res.ok) return;
    const data = res.data as unknown as ChannelsDoc;
    setChannelItems(Array.isArray(data.items) ? data.items : []);
    setStoreName(data.store_name ?? "");
  }, [session, storeId]);

  const loadOffers = useCallback(async () => {
    setOffersState("loading");
    const res = await session.api("GET", `stores/${storeId}/deal-offers`);
    if (res.ok) {
      const items = (res.data as unknown as { items?: DealOfferItem[] }).items ?? [];
      setOffers(items);
      setOffersState("ready");
      return;
    }
    setOffers([]);
    setOffersState("unavailable");
  }, [session, storeId]);

  const loadAssignments = useCallback(async () => {
    setAssignmentsState("loading");
    const res = await session.api("GET", `template-assignments?store_id=${encodeURIComponent(storeId)}`);
    if (res.ok) {
      const items = (res.data as unknown as { items?: TemplateAssignmentRow[] }).items ?? [];
      setAssignments(items.filter((row) => row.status === "distributed" || row.status === ""));
      setAssignmentsState("ready");
      return;
    }
    setAssignments([]);
    setAssignmentsState("unavailable");
  }, [session, storeId]);

  const loadRevisions = useCallback(async () => {
    const res = await session.api("GET", `stores/${storeId}/storefront/revisions`);
    if (res.ok) setRevisions(res.data as unknown as RevisionsDoc);
  }, [session, storeId]);

  const loadPreview = useCallback(async () => {
    setPreviewLoading(true);
    const suffix = previewSource === "live" && publishedRevision > 0 ? `?revision=${publishedRevision}` : "";
    const res = await session.api("GET", `stores/${storeId}/storefront-preview${suffix}`);
    setPreviewLoading(false);
    setPreview(res.ok ? (res.data as unknown as PreviewPayload) : null);
  }, [session, storeId, previewSource, publishedRevision]);

  useEffect(() => {
    if (!storeId) return;
    void (async () => {
      await loadDraft();
      await loadChannels();
      await loadOffers();
      await loadRevisions();
      await loadAssignments();
    })().catch(() => {
      setLoadError("没有读到，请重试");
      setPhase("error");
    });
  }, [storeId, loadDraft, loadChannels, loadOffers, loadRevisions, loadAssignments]);

  useEffect(() => {
    void loadPreview().catch(() => setPreview(null));
  }, [loadPreview]);

  function patchSections(next: StorefrontSection[]) {
    setSections(next);
    setDirty(true);
    setConfirmed(false);
    setPre(null);
  }

  async function saveDraft() {
    const body = {
      expected_revision: draftRevision,
      sections,
      offer_refs: collectOfferRefs(sections),
      campaign_refs: draft?.campaign_refs ?? [],
      brand_ref: draft?.brand_ref ?? "",
    };
    const res = await session.api("PUT", `stores/${storeId}/storefront-draft`, body);
    if (!res.ok) {
      if (res.status === 409) {
        setSaveError("草稿刚被别人保存过。点「重新读取」对齐最新版本,再继续修改。");
        return;
      }
      setSaveError(failureText(res.status, res.data));
      return;
    }
    const data = res.data as unknown as StorefrontDraftDoc;
    setDraft(data);
    setSections(data.sections ?? []);
    setDirty(false);
    setSaveError("");
    setNotice(`草稿已保存(第 ${data.revision} 版)。线上还是第 ${data.published_revision} 版,发布前顾客看不到修改。`);
  }

  async function runPreflight() {
    setPreflighting(true);
    const res = await session.api("POST", `stores/${storeId}/storefront/preflight`, {});
    setPreflighting(false);
    if (!res.ok) {
      setSaveError(failureText(res.status, res.data));
      return;
    }
    const data = res.data as unknown as PreflightResult;
    setPre(data);
    setConfirmed(false);
    setNotice("");
  }

  async function publish() {
    if (!pre || !canPublish(pre, confirmed, pre.summary_fingerprint)) return;
    setPublishing(true);
    const res = await session.api(
      "POST",
      `stores/${storeId}/storefront/publish`,
      buildPublishBody(pre.draft_revision, pre.published_revision, pre.summary_fingerprint),
    );
    setPublishing(false);
    if (!res.ok) {
      setSaveError(publishFailureText(res.status, res.data));
      return;
    }
    const data = res.data as unknown as { published_revision?: number; result?: { revision?: number } };
    setConfirmed(false);
    setPre(null);
    setSaveError("");
    const rev = data.result?.revision ?? data.published_revision ?? 0;
    setNotice(`发布完成,门店首页线上版本是第 ${rev} 版。顾客现在看到的就是这一版。`);
    if (pendingApplied !== "") {
      await notifyTemplateApplied(pendingApplied, rev);
      setPendingApplied("");
    }
    await loadDraft();
    await loadRevisions();
  }

  // 4593 接线缝:模板应用闭环——发布成功后回调 PATCH applied。失败如实提示
  // (分发行可能已取消或已终态),不阻塞发布本身的结果。
  async function notifyTemplateApplied(assignmentId: string, revision: number) {
    const res = await session.api("PATCH", `template-assignments/${assignmentId}`, {
      status: "applied",
      applied_revision: String(revision),
      reason: "门店首页应用模板并发布",
    });
    if (!res.ok) {
      setNotice((prev) => `${prev}(模板分发状态回写没有完成:该分发可能已被取消或已处理,可到「设置→模板下发」查看。)`);
    }
  }

  async function rollback(target: number) {
    setRollingBack(target);
    const res = await session.api(
      "POST",
      `stores/${storeId}/storefront/rollback`,
      buildRollbackBody(target, publishedRevision),
    );
    setRollingBack(0);
    if (!res.ok) {
      setSaveError(publishFailureText(res.status, res.data));
      return;
    }
    setSaveError("");
    setNotice(`已回滚到第 ${target} 版。已撤销的动作不会被恢复,顾客页面现在显示这一版。`);
    await loadDraft();
    await loadRevisions();
    if (previewSource === "live") await loadPreview();
  }

  function applyPreset(presetId: string) {
    const preset = TEMPLATE_PRESETS.find((p) => p.id === presetId);
    if (!preset) return;
    const { sections: next, skipped } = applyTemplate(preset, channelItems);
    patchSections(next);
    setNotice(
      skipped.length > 0
        ? `已按「${preset.title}」生成段落;${skipped.join("、")}段先省略(这家店还没有对应动作)。保存草稿后可继续调整。`
        : `已按「${preset.title}」生成段落。保存草稿后可继续调整。`,
    );
  }

  const publishedRows = useMemo(
    () => (revisions?.items ?? []).filter((item) => item.status === "published"),
    [revisions],
  );
  const readyCount = (pre?.actions ?? []).filter((v) => v.state === "ready").length;
  const blockedCount = (pre?.actions ?? []).filter((v) => v.state !== "ready").length;
  const publishAllowed = canPublish(pre, confirmed, pre?.summary_fingerprint ?? "");

  if (phase === "loading") return <main><p className="tk-state" data-state="loading">{surfaceLabel("loading")}</p></main>;
  if (phase === "error") {
    return (
      <main>
        <h1 className="tk-title">门店首页</h1>
        <p className="tk-state tk-danger" data-state="error">
          {loadError || surfaceLabel("error")}
          <button className="tk-quiet" type="button" data-action="retry" onClick={() => void loadDraft()}>重试</button>
        </p>
      </main>
    );
  }

  return (
    <main>
      <h1 className="tk-title">门店首页 · {storeName}</h1>
      <p className="tk-lead">
        这里排布顾客碰一碰后看到的门店首页:栏目分组、常用按钮、到店套餐、活动卡片。
        保存草稿不影响线上;明确发布后顾客才看到新版本;回滚随时可退回已发布版本。
      </p>
      <p className="tk-note" data-field="store-code">
        顾客门店码 {storeCode}。线上第 {publishedRevision} 版{draftRevision > 0 ? ` · 草稿第 ${draftRevision} 版` : " · 还没有草稿"}。
        {dirty ? "(有未保存修改)" : ""}
      </p>
      {notice ? <p className="tk-note" data-state="notice">{notice}</p> : null}
      {saveError ? <p className="tk-warn" data-state="save-error">{saveError}</p> : null}

      {canEdit ? (
        <div className="tk-section" data-region="template">
          <h2 className="tk-section-title">行业模板</h2>
          <p className="tk-note">模板只做段落骨架,按这家店已配置且启用的动作归组;没有对应动作的段落先省略。</p>
          <div className="tk-row tk-gap">
            {TEMPLATE_PRESETS.map((preset) => (
              <button key={preset.id} className="tk-quiet" type="button" data-action={`apply-template-${preset.id}`}
                title={preset.description} onClick={() => applyPreset(preset.id)}>
                {preset.title}
              </button>
            ))}
          </div>
          {assignmentsState === "ready" && assignments.length > 0 ? (
            <div className="tk-section">
              <h3 className="tk-section-title">总部下发的模板</h3>
              <ul className="tk-list" data-field="template-assignments">
                {assignments.map((row) => (
                  <li key={row.id}>
                    <span className="tk-note">模板 {row.template_ref ?? row.id} · 版本 {row.source_version ?? "-"}</span>
                    <button className="tk-quiet" type="button" data-action="apply-assignment"
                      onClick={() => {
                        const preset = TEMPLATE_PRESETS[0];
                        const { sections: next } = applyTemplate(preset, channelItems);
                        patchSections(next);
                        setPendingApplied(row.id);
                        setNotice("已按基础模板生成段落。保存草稿并发布后,会自动回写这条模板分发的应用状态。");
                      }}>
                      应用(基础模板)
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
        </div>
      ) : null}

      <div className="tk-section" data-region="sections">
        <h2 className="tk-section-title">栏目分组与排序</h2>
        {sections.length === 0 ? (
          <p className="tk-state" data-state="empty">
            还没有任何栏目。先在上面挑一个行业模板,或自己在下面新增段落。
          </p>
        ) : (
          <ul className="tk-list" data-field="sections">
            {sections.map((sec, index) => (
              <li key={sec.id}>
                <input
                  className="tk-input"
                  value={sec.title}
                  placeholder="段落名称"
                  disabled={!canEdit}
                  onChange={(e) => patchSections(sections.map((s, i) => (i === index ? { ...s, title: e.target.value } : s)))}
                />
                <span className="tk-note">{sec.type === "offers" ? "套餐段" : `动作段 · ${(sec.action_ids ?? []).length} 个入口`}</span>
                {canEdit ? (
                  <div className="tk-row tk-gap">
                    <button className="tk-quiet" type="button" onClick={() => moveSection(index, -1)}>上移</button>
                    <button className="tk-quiet" type="button" onClick={() => moveSection(index, 1)}>下移</button>
                    <button className="tk-quiet" type="button" onClick={() => removeSection(index)}>移除段落</button>
                    {sec.type === "offers" ? (
                      <select
                        className="tk-input"
                        value=""
                        disabled={offersState !== "ready"}
                        onChange={(e) => addOffer(index, e.target.value)}
                      >
                        <option value="">添加套餐…</option>
                        {offers.map((offer) => (
                          <option key={offer.id} value={offer.id}>{offer.title}</option>
                        ))}
                      </select>
                    ) : (
                      <select
                        className="tk-input"
                        value=""
                        onChange={(e) => addAction(index, e.target.value)}
                      >
                        <option value="">添加动作入口…</option>
                        {channelItems.filter((item) => item.enabled).map((item) => (
                          <option key={item.action_id} value={item.action_id}>
                            {item.label || item.catalog_label || item.catalog_code}
                          </option>
                        ))}
                      </select>
                    )}
                  </div>
                ) : null}
                {sec.type === "offers" && (sec.offer_refs ?? []).length > 0 ? (
                  <p className="tk-note">已选套餐 {(sec.offer_refs ?? []).length} 个(失效套餐发布时会自动隐藏)。</p>
                ) : null}
                {sec.type === "actions" ? (
                  <p className="tk-note">
                    入口:{(sec.action_ids ?? []).map((id) => channelLabel(id, channelItems)).join("、") || "(空段落)"}
                  </p>
                ) : null}
              </li>
            ))}
          </ul>
        )}
        {canEdit ? (
          <div className="tk-row tk-gap">
            <button className="tk-quiet" type="button" data-action="add-actions-section"
              onClick={() => patchSections([...sections, { id: `s${Date.now()}`, type: "actions", title: "新栏目", action_ids: [] }])}>
              新增动作段
            </button>
            <button className="tk-quiet" type="button" data-action="add-offers-section"
              onClick={() => patchSections([...sections, { id: `s${Date.now() + 1}`, type: "offers", title: "到店优惠", offer_refs: [] }])}>
              新增套餐段
            </button>
          </div>
        ) : null}
        {offersState === "unavailable" ? (
          <p className="tk-note">套餐列表当前不可用,套餐段可以先留空,已有引用保持不变。</p>
        ) : null}
      </div>

      {canEdit ? (
        <div className="tk-row tk-gap tk-section" data-region="draft-actions">
          <button className="tk-button" type="button" data-action="save-draft" disabled={!dirty} onClick={() => void saveDraft()}>
            保存草稿
          </button>
          <button className="tk-quiet" type="button" data-action="reload-draft"
            disabled={!dirty} onClick={() => { void loadDraft(); setNotice(""); }}>
            放弃修改(重新读取)
          </button>
          {dirty ? <span className="tk-note">保存草稿不影响线上页面。</span> : null}
        </div>
      ) : null}

      <div className="tk-section" data-region="preview">
        <h2 className="tk-section-title">手机预览</h2>
        <div className="tk-row tk-gap">
          <label className="tk-note">
            <input type="radio" name="preview-source" checked={previewSource === "draft"}
              onChange={() => setPreviewSource("draft")} />
            草稿预览
          </label>
          <label className="tk-note">
            <input type="radio" name="preview-source" checked={previewSource === "live"}
              onChange={() => setPreviewSource("live")} />
            线上版本
          </label>
          {previewLoading ? <span className="tk-note">预览读取中…</span> : null}
        </div>
        <div className="tk-phone" data-state="preview" data-preview-source={previewSource}>
          <p className="tk-note" data-state="preview-badge">
            {previewSource === "draft" ? "预览(草稿,未发布)" : "预览(线上版本)"} — 顾客看到的样子
          </p>
          {preview && preview.store?.state === "disabled" ? (
            <p className="tk-warn">门店已停用,顾客现在看到「门店暂不可用」。</p>
          ) : null}
          {preview && previewRows(preview).length > 0 ? (
            previewRows(preview).map((row) => (
              <div key={row.title}>
                <strong>{row.title}</strong>
                {row.lines.map((line, i) => (
                  <p key={i} className="tk-note">{line}</p>
                ))}
              </div>
            ))
          ) : (
            <p className="tk-state" data-state="empty">
              {previewSource === "live" && publishedRevision === 0
                ? "还没有发布过版本。先发布,或切到「草稿预览」。"
                : "这个版本还没有可显示的栏目。"}
            </p>
          )}
          {(preview?.activity_cards ?? []).length > 0 ? (
            <p className="tk-note">活动卡片 {(preview?.activity_cards ?? []).length} 张</p>
          ) : null}
        </div>
        <p className="tk-note">
          预览是真实数据面的投影,不产生参与、发布或费用。顾客实际入口是 /s/{storeCode || "{门店码}"}。
        </p>
      </div>

      <div className="tk-section" data-region="preflight">
        <h2 className="tk-section-title">发布前预检</h2>
        <p className="tk-note">
          预检逐动作检查目标、内容与授权;一个动作的问题只影响它自己,其他动作照常可以发布。
        </p>
        {canEdit ? (
          <button className="tk-button" type="button" data-action="preflight" disabled={preflighting || dirty}
            onClick={() => void runPreflight()}>
            {preflighting ? "预检中…" : "运行预检"}
          </button>
        ) : null}
        {dirty ? <p className="tk-note">先保存草稿,再运行预检。</p> : null}
        {pre ? (
          <div>
            {pre.fatal.length > 0 ? (
              <p className="tk-warn" data-state="fatal">
                页面本身有问题,先修正:{pre.fatal.map((code) => fatalCopy(code)).join(";")}
              </p>
            ) : null}
            {(pre.actions ?? []).length > 0 ? (
              <ul className="tk-list" data-field="preflight-actions">
                {pre.actions.map((v) => {
                  const copy = verdictCopy(v);
                  return (
                    <li key={v.action_id} data-state={v.state}>
                      <strong>{channelLabel(v.action_id, channelItems)}</strong>
                      <span className="tk-note"> {copy.stateText}</span>
                      {copy.lines.map((line, i) => (
                        <p key={i} className={v.state === "ready" ? "tk-note" : "tk-warn"}>{line}</p>
                      ))}
                      {copy.fixEntry && v.state !== "ready" ? (
                        <p className="tk-note">{copy.fixEntry} <Link className="tk-quiet" href={`/work/stores/${storeId}/channels`}>打开渠道动作</Link></p>
                      ) : null}
                    </li>
                  );
                })}
              </ul>
            ) : null}
            <p className="tk-note">
              可以发布 {readyCount} 项{blockedCount > 0 ? `;暂不能发布 ${blockedCount} 项(发布前先在段落里移除它们)` : ""}。
            </p>
            <div data-region="change-summary">
              <h3 className="tk-section-title">这次发布改了什么</h3>
              {pre.change_summary.length === 0 ? (
                <p className="tk-note">与线上版本相同,发布不会改变顾客看到的页面。</p>
              ) : (
                <ul className="tk-list" data-field="change-summary">
                  {pre.change_summary.map((line) => (
                    <li key={line}>{summaryLineCopy(line)}</li>
                  ))}
                </ul>
              )}
              <label className="tk-label" data-field="confirm-changes">
                <input type="checkbox" checked={confirmed} disabled={!canEdit || pre.fatal.length > 0}
                  onChange={(e) => setConfirmed(e.target.checked)} />
                我已确认以上变更(发布以服务端校验为准)
              </label>
              <button className="tk-button" type="button" data-action="publish"
                disabled={!publishAllowed || publishing} onClick={() => void publish()}>
                {publishing ? "发布中…" : "发布到线上"}
              </button>
              {!publishAllowed && pre.publishable && !confirmed ? (
                <p className="tk-note">勾选确认后才能发布。</p>
              ) : null}
            </div>
          </div>
        ) : null}
      </div>

      <div className="tk-section" data-region="rollback">
        <h2 className="tk-section-title">已发布版本与回滚</h2>
        {(publishedRows ?? []).length === 0 ? (
          <p className="tk-state" data-state="empty">还没有发布过版本。</p>
        ) : (
          <ul className="tk-list" data-field="published-versions">
            {publishedRows.map((item) => (
              <li key={item.id}>
                <strong>第 {item.revision} 版</strong>
                {item.revision === publishedRevision ? <span className="tk-note"> (当前线上)</span> : null}
                {item.published_at ? <span className="tk-note"> · {item.published_at}</span> : null}
                {canEdit && item.revision !== publishedRevision ? (
                  <button className="tk-quiet" type="button" data-action="rollback"
                    disabled={rollingBack === item.revision}
                    onClick={() => void rollback(item.revision)}>
                    {rollingBack === item.revision ? "回滚中…" : `回滚到这一版`}
                  </button>
                ) : null}
              </li>
            ))}
          </ul>
        )}
        {revisions && revisions.events.length > 0 ? (
          <div>
            <h3 className="tk-section-title">发布记录</h3>
            <ul className="tk-list" data-field="publish-events">
              {revisions.events.slice(0, 5).map((ev) => (
                <li key={ev.id} className="tk-note">
                  {ev.action === "rollback" ? "回滚" : "发布"}到第 {ev.revision} 版 · {ev.created_at}
                </li>
              ))}
            </ul>
          </div>
        ) : null}
        <p className="tk-note">回滚后已撤销的平台授权不会被恢复;被拒时会逐项说明原因。</p>
      </div>

      <p className="tk-note">
        <Link href={`/work/stores/${storeId}/channels`} className="tk-quiet">去配置渠道动作</Link>
        {" · "}
        <Link href="/work/stores" className="tk-quiet">返回门店列表</Link>
      </p>
    </main>
  );

  function moveSection(index: number, delta: number) {
    const next = [...sections];
    const target = index + delta;
    if (target < 0 || target >= next.length) return;
    [next[index], next[target]] = [next[target], next[index]];
    patchSections(next);
  }

  function removeSection(index: number) {
    patchSections(sections.filter((_, i) => i !== index));
  }

  function addAction(sectionIndex: number, actionId: string) {
    if (!actionId) return;
    patchSections(sections.map((sec, i) => {
      if (i !== sectionIndex || sec.type !== "actions") return sec;
      const ids = sec.action_ids ?? [];
      if (ids.includes(actionId)) return sec;
      return { ...sec, action_ids: [...ids, actionId] };
    }));
  }

  function addOffer(sectionIndex: number, offerId: string) {
    if (!offerId) return;
    patchSections(sections.map((sec, i) => {
      if (i !== sectionIndex || sec.type !== "offers") return sec;
      const refs = sec.offer_refs ?? [];
      if (refs.includes(offerId)) return sec;
      return { ...sec, offer_refs: [...refs, offerId] };
    }));
  }
}

function collectOfferRefs(sections: StorefrontSection[]): string[] {
  const out: string[] = [];
  for (const sec of sections) {
    for (const ref of sec.offer_refs ?? []) {
      if (!out.includes(ref)) out.push(ref);
    }
  }
  return out;
}

function channelLabel(actionId: string, items: ChannelsDoc["items"]): string {
  const item = items.find((row) => row.action_id === actionId);
  return item ? item.label || item.catalog_label || item.catalog_code : actionId;
}

function fatalCopy(code: string): string {
  const byCode: Record<string, string> = {
    sections_empty: "首页还没有任何栏目。",
    section_id_duplicate: "有段落的编号重复了,请重新保存。",
    action_id_duplicate: "同一个动作出现在多个段落里,一个动作只放一处。",
    section_type_unknown: "有段落类型不认识,请移除后重新添加。",
    actions_too_many: "动作入口太多,请精简到 100 个以内。",
  };
  return byCode[code] ?? "页面结构有问题,请重新调整栏目。";
}
