"use client";

import { useCallback, useEffect, useState } from "react";
import {
  draftsFromMerchant,
  emptyJumpDraft,
  merchantJumpKinds,
  putJumpActions,
  saveJumpError,
  unauthorizedConfigCopy,
  type JumpDraft,
} from "@/lib/jump-matrix";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, CardContent } from "@/components/ui/card";
import { Alert, AlertDescription } from "@/components/ui/alert";

const KIND_LABEL: Record<string, string> = {
  wifi: "门店 WiFi",
  navigate: "导航",
  review: "写点评",
  follow: "关注账号",
};

export function ExtraJumpPanel(props: {
  campaigns: Array<{ id: string; title: string }>;
  role: string;
  tenantId: string;
}) {
  const owner = props.role === "org_owner";
  const [campaignId, setCampaignId] = useState("");
  const [actions, setActions] = useState<Record<string, JumpDraft>>(
    () => draftsFromMerchant(null).actions,
  );
  const [returnDraft, setReturnDraft] = useState<JumpDraft>(emptyJumpDraft());
  const [error, setError] = useState("");
  const [note, setNote] = useState("");
  const [saving, setSaving] = useState(false);
  const [preserved, setPreserved] = useState<
    Array<{
      kind?: string;
      href?: string;
      enabled?: boolean;
      revoked?: boolean;
      expires_at?: string;
    }>
  >([]);

  const load = useCallback(
    async (id: string) => {
      if (!id || !owner) return;
      const res = await fetch(
        `/api/campaigns/${encodeURIComponent(id)}/extra-jumps`,
        {
          headers: { "x-tenant-id": props.tenantId },
        },
      );
      const data = await res.json().catch(() => ({}));
      if (!res.ok) {
        setError(
          res.status === 403
            ? unauthorizedConfigCopy()
            : "没有读到已保存的跳转",
        );
        return;
      }
      const drafts = draftsFromMerchant(data);
      setActions(drafts.actions);
      setReturnDraft(drafts.returnDraft);
      setPreserved(Array.isArray(data.configured) ? data.configured : []);
      setError("");
    },
    [owner, props.tenantId],
  );

  useEffect(() => {
    if (!campaignId) return;
    void load(campaignId);
  }, [campaignId, load]);

  const patchAction = (kind: string, patch: Partial<JumpDraft>) => {
    setActions((prev) => ({ ...prev, [kind]: { ...prev[kind], ...patch } }));
  };

  const save = async () => {
    if (!owner || !campaignId) return;
    setSaving(true);
    setError("");
    setNote("");
    const res = await fetch(
      `/api/campaigns/${encodeURIComponent(campaignId)}/extra-jumps`,
      {
        method: "PUT",
        headers: {
          "content-type": "application/json",
          "x-tenant-id": props.tenantId,
        },
        body: JSON.stringify({
          actions: putJumpActions(actions, preserved),
          return: returnDraft,
        }),
      },
    );
    const data = await res.json().catch(() => ({}));
    setSaving(false);
    if (!res.ok) {
      const code = typeof data.error === "string" ? data.error : "";
      setError(
        saveJumpError(
          code === "forbidden" || res.status === 403 ? "forbidden" : code,
        ),
      );
      return;
    }
    setNote("已保存。这只记下地址，不是关注、加群或付费成功。原生唤起未验证。");
    await load(campaignId);
  };

  return (
    <Card data-testid="extra-jump-panel">
      <CardContent className="tk-gap pt-5">
        <h2 className="tk-section-title">跳转</h2>
        <p className="tk-note">
          只保存店内和活动的正式地址，以及已登记的返回地址。加企微仍在私域入口配置。原生唤起未验证。
        </p>
        {!owner && (
          <Alert variant="warning" data-testid="extra-jump-unauthorized">
            <AlertDescription>{unauthorizedConfigCopy()}</AlertDescription>
          </Alert>
        )}
        <label className="tk-label">
          活动
          <select
            className="flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
            value={campaignId}
            onChange={(e) => setCampaignId(e.target.value)}
            disabled={!owner}
          >
            <option value="">选择活动</option>
            {props.campaigns.map((item) => (
              <option key={item.id} value={item.id}>
                {item.title}
              </option>
            ))}
          </select>
        </label>
        {merchantJumpKinds().map((kind) => (
          <div key={kind} className="tk-row">
            <strong className="tk-inline-label">{KIND_LABEL[kind]}</strong>
            <label>
              <input
                type="checkbox"
                checked={actions[kind]?.enabled === true}
                disabled={!owner}
                onChange={(e) =>
                  patchAction(kind, { enabled: e.target.checked })
                }
              />
              启用
            </label>
            <Input
              placeholder="https 正式地址"
              value={actions[kind]?.href ?? ""}
              disabled={!owner}
              data-testid={kind === "wifi" ? "extra-jump-wifi-href" : undefined}
              onChange={(e) => patchAction(kind, { href: e.target.value })}
            />
            <label>
              <input
                type="checkbox"
                checked={actions[kind]?.revoked === true}
                disabled={!owner}
                onChange={(e) =>
                  patchAction(kind, { revoked: e.target.checked })
                }
              />
              撤销
            </label>
            <Input
              placeholder="失效时间 RFC3339，可空"
              value={actions[kind]?.expires_at ?? ""}
              disabled={!owner}
              onChange={(e) =>
                patchAction(kind, { expires_at: e.target.value })
              }
            />
          </div>
        ))}
        <div className="tk-row">
          <strong className="tk-inline-label">返回</strong>
          <label>
            <input
              type="checkbox"
              checked={returnDraft.enabled}
              disabled={!owner}
              onChange={(e) =>
                setReturnDraft({ ...returnDraft, enabled: e.target.checked })
              }
            />
            启用
          </label>
          <Input
            placeholder="站内路径或已登记的 https 地址"
            value={returnDraft.href}
            disabled={!owner}
            data-testid="extra-jump-return-href"
            onChange={(e) =>
              setReturnDraft({ ...returnDraft, href: e.target.value })
            }
          />
          <label>
            <input
              type="checkbox"
              checked={returnDraft.revoked}
              disabled={!owner}
              onChange={(e) =>
                setReturnDraft({ ...returnDraft, revoked: e.target.checked })
              }
            />
            撤销
          </label>
          <Input
            placeholder="失效时间 RFC3339，可空"
            value={returnDraft.expires_at}
            disabled={!owner}
            onChange={(e) =>
              setReturnDraft({ ...returnDraft, expires_at: e.target.value })
            }
          />
        </div>
        {error && (
          <Alert variant="destructive" role="alert">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        {note && (
          <Alert variant="success" role="status">
            <AlertDescription>{note}</AlertDescription>
          </Alert>
        )}
        <Button
          type="button"
          data-testid="extra-jump-save"
          disabled={!owner || !campaignId || saving}
          onClick={() => void save()}
        >
          {saving ? "保存中…" : "保存跳转"}
        </Button>
      </CardContent>
    </Card>
  );
}
