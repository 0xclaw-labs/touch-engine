"use client";
import { useParams, useRouter } from "next/navigation";
import { useEffect, useRef, useState } from "react";
import { createHandoffRequest, continuationDestination } from "@/lib/follow-up-continuation";
import { observeSsoAccountChanges, resetWorkspaceAfterSso } from "@/lib/sso-workspace-reset";
import { currentTenantOp, TENANT_STORAGE_KEY } from "@/lib/workspace-tenant";
export default function FollowUpReturn() {
    const { h } = useParams<{
        h: string;
    }>();
    const router = useRouter();
    const [error, setError] = useState("");
    const pending = useRef(createHandoffRequest((handoff: string) => fetch(`/api/lead-follow-up-returns/${encodeURIComponent(handoff)}/resume`, { method: "POST" })));
    useEffect(() => { let alive = true; setError(""); resetWorkspaceAfterSso(); const epoch = currentTenantOp(); const stop = observeSsoAccountChanges(() => { alive = false; setError("账号已切换，请从原线索详情重新返回门店。"); }); pending.current(h).then(res => res.clone().json().then(data => ({ res, data }))).then(({ res, data }) => { if (!alive || currentTenantOp() !== epoch)
        return; if (!res.ok) {
        setError(data.message || "返回上下文已失效，请从原详情重试。");
        return;
    } if (!data.known) {
        router.replace(continuationDestination(data));
        return;
    } window.localStorage.setItem(TENANT_STORAGE_KEY, data.tenant_id); router.replace(continuationDestination(data)); }).catch(() => { if (alive)
        setError("没有连上返回服务，请从原详情重试。"); }); return () => { alive = false; stop(); }; }, [h, router]);
    return <main><h1>正在返回原门店</h1><p role="status">{error || "正在核验当前成员与原店权限…"}</p>{error ? <a href="/">返回碰一碰工作台</a> : null}</main>;
}
