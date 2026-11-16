"use client";

import { useEffect, useState } from "react";
import { startOrResumeSession, type ActionSessionView } from "@/lib/store-session";
import { ActionPublishFlow } from "@/components/publish/ActionPublishFlow";

/** A share service uses the same published-action session and admission gate as
 * other handoffs. It never invents a content pack or a reward participation. */
export function LinkShareFlow({ actionId, method, environment, storeCode }: {
  actionId: string; method: string; environment: string; storeCode: string;
}) {
  const [session, setSession] = useState<ActionSessionView | null>(null);
  const [notice, setNotice] = useState("");
  const [reload, setReload] = useState(0);

  useEffect(() => {
    let alive = true;
    setSession(null);
    setNotice("");
    startOrResumeSession(actionId).then((result) => {
      if (!alive) return;
      if ("session" in result) setSession(result.session);
      else setNotice(result.error === "network"
        ? "原分享进度暂时读不到，请重试。"
        : "这项分享服务当前不可用，请返回门店查看最新服务。");
    });
    return () => { alive = false; };
  }, [actionId, reload]);

  if (session?.action_session_id) return <ActionPublishFlow
    actionId={actionId}
    sessionId={session.action_session_id}
    actionRevision={session.action_revision ?? 0}
    versionId={`link-${session.action_revision ?? 0}`}
    method={method}
    environment={environment}
    storeCode={storeCode}
    payloadKind="link_share"
  />;

  return <section className="tk-section" data-testid="link-share-session">
    <p className="tk-note" role="status">{notice || "正在读取原分享进度…"}</p>
    {notice && <button type="button" className="tk-quiet" onClick={() => setReload((value) => value + 1)}>重试原分享</button>}
  </section>;
}
