"use client";

// HUI-2628: the merchant handoff that actually exists is the matrix draft.
// It carries the exact campaign version, the selected asset ref/sha, the grant
// reference and the return token. The old campaign-image button posted only a
// campaign id to a route that required a full handoff document, so it could
// never succeed; it is removed from the normal path.

import { useState } from "react";
import { planTaskHandoff, type TaskKind } from "@/lib/eco-nav/touch-shell";

const LABEL: Record<TaskKind, string> = {
  make_campaign_image: "记下矩阵草稿",
  make_campaign_video: "制作视频（去素材页）",
  view_campaign_leads: "查看本活动线索",
};

const SECONDARY_NOTICE: Record<Exclude<TaskKind, "make_campaign_image">, string> = {
  make_campaign_video: "视频制作入口在素材页；这里不新建制作工程，也不扣费。",
  view_campaign_leads: "线索读取在统计页；普通应用切换不会带走活动、素材或付款主体。",
};

interface MatrixResponse {
  matrix_button?: { enabled?: boolean; reason?: string };
  draft_id?: string;
  draft_status?: string;
  activity_version?: number;
  copy?: string;
  outbound_complete?: boolean;
}

export function TaskHandoffActions({
  campaignId,
  onPlanned,
}: {
  campaignId: string;
  onPlanned: (message: string) => void;
}) {
  const [busy, setBusy] = useState(false);
  const kinds: TaskKind[] = ["make_campaign_image", "make_campaign_video", "view_campaign_leads"];

  async function recordMatrixDraft() {
    if (busy) return;
    setBusy(true);
    onPlanned("正在记下矩阵草稿…");
    try {
      const res = await fetch(`/api/campaigns/${campaignId}/matrix-draft`, { method: "POST" });
      const data = (await res.json().catch(() => ({}))) as MatrixResponse;
      const matrix = data.matrix_button;
      if (!res.ok) {
        onPlanned("矩阵草稿没有记下。活动仍保持原状态，也没有外发。");
        return;
      }
      if (!matrix?.enabled) {
        onPlanned(matrix?.reason ? `矩阵草稿现在不可用：${matrix.reason}` : "矩阵草稿现在不可用。活动没有外发。");
        return;
      }
      const version = typeof data.activity_version === "number" ? `（活动版本 ${data.activity_version}）` : "";
      const draft = data.draft_id ? `草稿 ${data.draft_id}` : "矩阵草稿";
      onPlanned(`已记下${draft}${version}。${data.copy || "活动仍开放，这里没有执行。"} 没有外发。`);
    } catch {
      onPlanned("没有连上服务，矩阵草稿没有记下，也没有外发。");
    } finally {
      setBusy(false);
    }
  }

  return (
    <span className="tk-row">
      {kinds.map((kind) => {
        const plan = planTaskHandoff(kind, campaignId);
        const primary = kind === "make_campaign_image";
        return (
          <button
            key={kind}
            type="button"
            data-testid={`task-${kind}`}
            data-primary-action={primary ? "true" : undefined}
            data-nav-intent={plan.nav_intent}
            data-creates-handoff="true"
            data-creates-project={plan.creates_or_restores_project ? "true" : "false"}
            data-campaign-id={plan.campaign_id}
            data-charges-customer="false"
            data-leads-fetch={plan.leads_fetch}
            disabled={primary && busy}
            onClick={() => {
              if (primary) {
                void recordMatrixDraft();
                return;
              }
              onPlanned(SECONDARY_NOTICE[kind as Exclude<TaskKind, "make_campaign_image">]);
            }}
            className={primary ? "tk-button" : "tk-quiet"}
          >
            {LABEL[kind]}
          </button>
        );
      })}
      {busy ? <span className="tk-note" role="status">正在记录，不会外发。</span> : null}
    </span>
  );
}
