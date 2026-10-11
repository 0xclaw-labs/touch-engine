"use client";

import { useState } from "react";

type ButtonState = { enabled?: boolean; reason?: string };

const blockedCopy = ["发布成功", "外发完成"];

// 记下矩阵草稿。供给缺失只停这个按钮。草稿成功不是外发完成。
export function MatrixDraftButton({ campaignId, tenantId }: { campaignId: string; tenantId: string }) {
  const [disabled, setDisabled] = useState(false);
  const [note, setNote] = useState("");

  async function recordDraft() {
    const res = await fetch(`/api/campaigns/${campaignId}/matrix-draft`, {
      method: "POST",
      headers: { "x-tenant-id": tenantId },
    });
    const data = (await res.json().catch(() => ({}))) as {
      matrix_button?: ButtonState;
      outbound_complete?: boolean;
      copy?: string;
    };
    const matrix = data.matrix_button;
    if (!matrix?.enabled) setDisabled(true);
    let text = data.copy ?? "";
    if (data.outbound_complete === true || blockedCopy.some((phrase) => text.includes(phrase))) {
      text = "草稿已记下，没有外发。";
    } else if (!text && !matrix?.enabled) {
      text = "矩阵草稿现在不可用。";
    }
    setNote(text);
  }

  return (
    <span className="tk-admin-inline-flex tk-admin-gap-4">
      <button type="button" className="tk-admin-btn" disabled={disabled} onClick={() => void recordDraft()}>
        记下矩阵草稿
      </button>
      {note ? <span data-testid="matrix-draft-note">{note}</span> : null}
    </span>
  );
}
