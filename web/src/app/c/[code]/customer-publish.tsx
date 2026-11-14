"use client";

import { useCallback, useEffect, useState } from "react";
import {
  actionEnabled,
  authorizedPublishEnabled,
  draftMatchesAttempt,
  outcomeMessage,
  platformLabel,
  publishSucceeded,
  type CapabilityMatrix,
  type PlatformRow,
} from "@/lib/customer-publish";
import { rewardPresentation } from "@/lib/product-finish";
import { proofNotice, publishRewardOpen, rewardNotice, type ProofView, type RewardView } from "@/lib/publish-reward";

interface AttemptResponse {
  attempt_id?: string;
  status?: string;
  copy?: string;
  content_version?: string;
  account_label?: string;
  platform_post_id?: string;
  counts_as_published?: boolean;
  publish_success?: boolean;
  self_reported?: boolean;
  reward_triggered?: boolean;
  confirmation_current?: boolean;
  package?: { post_id?: string; caption?: string; steps?: string[] };
  error?: string;
  message?: string;
}

export function CustomerPublish({ code, defaultCopy }: { code: string; defaultCopy: string }) {
  const [matrix, setMatrix] = useState<CapabilityMatrix | null>(null);
  const [legacyState, setLegacyState] = useState<"loading" | "ready" | "original" | "error">("loading");
  const [platform, setPlatform] = useState("douyin");
  const [copy, setCopy] = useState(defaultCopy);
  const account = ""; // 手工路线不收集不能证明归属的平台账号声明。
  const [assetOk, setAssetOk] = useState(false);
  const [attempt, setAttempt] = useState<AttemptResponse | null>(null);
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const [reward, setReward] = useState<RewardView | null>(null);
  const [proof, setProof] = useState<ProofView | null>(null);

  useEffect(() => {
    let alive = true;
    fetch(`/api/public/links/${encodeURIComponent(code)}/publish-reward`)
      .then(async (res) => (res.ok ? res.json() : null))
      .then((data) => {
        if (alive && data) setReward(data as RewardView);
      })
      .catch(() => undefined);
    return () => {
      alive = false;
    };
  }, [code]);

  useEffect(() => {
    let alive = true;
    setMatrix(null);
    setAttempt(null);
    setLegacyState("loading");
    fetch(`/api/public/links/${encodeURIComponent(code)}/publish-capabilities`)
      .then(async (res) => {
        const data = await res.json().catch(() => null);
        if (!alive) return;
        if (res.ok && Array.isArray(data?.platforms)) {
          setMatrix(data as CapabilityMatrix);
          setLegacyState("ready");
        } else {
          setLegacyState(data?.error === "original_participation_required" ? "original" : "error");
        }
      })
      .catch(() => { if (alive) setLegacyState("error"); });
    return () => {
      alive = false;
    };
  }, [code]);

  const row: PlatformRow | undefined = matrix?.platforms.find((item) => item.platform === platform);

  const run = useCallback(async (path: string, body: unknown, method = "POST") => {
    setBusy(true);
    setNotice("");
    try {
      const res = await fetch(path, {
        method,
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      const data = (await res.json().catch(() => null)) as AttemptResponse | null;
      if (!res.ok || !data) {
        if (data?.error === "original_participation_required") {
          setMatrix(null);
          setLegacyState("original");
        }
        setNotice(data?.message || "没有发出去。这一步不是发布成功。");
        return;
      }
      setAttempt(data);
      setNotice(outcomeMessage(data));
    } catch {
      setNotice("网络异常。没有把它当成发布成功。");
    } finally {
      setBusy(false);
    }
  }, []);

  const preview = () =>
    run(`/api/public/links/${encodeURIComponent(code)}/publish-attempts`, {
      platform,
      publisher: "activity_customer",
      copy,
      account_label: account,
    });

  const exportPack = () => {
    if (!attempt?.attempt_id) return;
    run(`/api/public/links/${encodeURIComponent(code)}/publish-attempts/${attempt.attempt_id}/export`, {});
  };

  const confirm = () => {
    if (!attempt?.attempt_id || !attempt.content_version) return;
    if (!draftMatchesAttempt(attempt, copy, account)) return;
    run(`/api/public/links/${encodeURIComponent(code)}/publish-attempts/${attempt.attempt_id}/confirm`, {
      content_version: attempt.content_version,
      account_label: attempt.account_label,
      publisher: "activity_customer",
      asset_use_accepted: assetOk,
    });
  };

  const revise = (next: { copy?: string; account_label?: string }) => {
    if (!attempt?.attempt_id) return;
    run(`/api/public/links/${encodeURIComponent(code)}/publish-attempts/${attempt.attempt_id}`, next, "PATCH");
  };

  const submitProof = () => {
    if (!attempt?.attempt_id) return;
    setBusy(true);
    fetch(`/api/public/links/${encodeURIComponent(code)}/publish-attempts/${attempt.attempt_id}/reward-proof`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ note: "顾客提交的发布证明", post_id: "" }),
    })
      .then(async (res) => (res.ok ? res.json() : null))
      .then((data) => {
        if (data) setProof(data as ProofView);
      })
      .catch(() => undefined)
      .finally(() => setBusy(false));
  };

  const selfReport = () => {
    if (!attempt?.attempt_id) return;
    run(`/api/public/links/${encodeURIComponent(code)}/publish-attempts/${attempt.attempt_id}/self-report`, {
      post_id: "",
    });
  };

  const rewardOpen = publishRewardOpen(reward ?? {});
  const presented = rewardPresentation(reward ?? {});
  const succeeded = attempt ? publishSucceeded(attempt) : false;
  const draftMatches = draftMatchesAttempt(attempt, copy, account);
  const publishClosed = !row || !actionEnabled(row, "authorized_publish");
  const editorClosed = !row || !actionEnabled(row, "open_editor");

  if (legacyState !== "ready") return (
    <section className="tk-section" data-testid="customer-publish" data-state={legacyState}>
      <h2 className="tk-section-title">参考文案</h2>
      <p className="tk-note">{defaultCopy}</p>
      <p className="tk-note">{legacyState === "loading" ? "正在核对本次活动的办理方式…" : legacyState === "original" ? "请在上方原参与进度中确认名额并继续发布交接。名额未确认前不要先完成有奖励任务。" : "本次办理方式暂时读不到，请刷新原活动进度。"}</p>
    </section>
  );

  return (
    <section className="tk-section" data-testid="customer-publish">
      <h2 className="tk-section-title">预览并手动发布</h2>
      <p data-testid="publish-reward" data-reward-tone={presented.tone} data-success={presented.success ? "true" : "false"} className={presented.success ? "tk-ok" : "tk-unknown"}>
        {rewardNotice(reward ?? {})}
        {rewardOpen ? "" : " 预览、导出、唤起编辑器或点击「我已发布」都不能领券。"}
      </p>
      <p className="tk-note">
        发布账号是你自己的。商家账号不能代替你发，再算成你的作品。
        {matrix && !authorizedPublishEnabled(matrix) ? " 当前没有授权代你发布。" : ""}
      </p>
      <label className="tk-label">
        发布到
        <select className="tk-select" value={platform} onChange={(e) => setPlatform(e.target.value)}>
          {(matrix?.platforms ?? [{ platform: "douyin" }]).map((item) => (
            <option key={item.platform} value={item.platform}>
              {platformLabel(item.platform)}
            </option>
          ))}
        </select>
      </label>
      {row && (
        <p className="tk-note" data-testid="publish-summary">
          当前平台：预览{actionEnabled(row, "preview") ? "可用" : "关闭"} · 导出{actionEnabled(row, "export") ? "可用" : "关闭"} · 代发{actionEnabled(row, "authorized_publish") ? "可用" : "关闭"} · 唤起编辑器{actionEnabled(row, "open_editor") ? "可用" : "关闭"}。
          可用方式以本次路线说明为准，素材导出不代表符合活动领奖条件。
        </p>
      )}
      {row && (
        <details className="tk-gap" data-testid="publish-capability-details">
          <summary className="tk-note">发布能力说明</summary>
          <ul className="tk-list">
            {(["preview", "export", "open_editor", "authorized_publish", "confirm_publish"] as const).map((kind) => {
              const cell = row.capabilities[kind];
              const title =
                kind === "preview" ? "可预览" : kind === "export" ? "可导出" : kind === "open_editor" ? "可唤起编辑器" : kind === "authorized_publish" ? "可授权发布" : "可确认发布";
              return (
                <li key={kind}>
                  {title}：{cell?.enabled ? "可用" : "关闭"}。{cell?.reason}
                  {cell && !cell.enabled && cell.evidence_url ? (
                    <>
                      {" "}
                      <a href={cell.evidence_url} target="_blank" rel="noreferrer">官方说明</a>
                    </>
                  ) : null}
                </li>
              );
            })}
          </ul>
          {row.manual_guide && <p className="tk-note">{row.manual_guide}</p>}
        </details>
      )}
      <label className="tk-label">
        文案
        <textarea className="tk-input tk-input-tall" value={copy} onChange={(e) => setCopy(e.target.value)} />
      </label>
      <label className="tk-check">
        <input type="checkbox" checked={assetOk} onChange={(e) => setAssetOk(e.target.checked)} /> 我确认使用本次活动素材，并用我自己的账号发布
      </label>
      <div className="tk-row">
        {!attempt?.attempt_id ? <button type="button" disabled={busy || !actionEnabled(row,"preview")} onClick={preview} className="tk-primary">预览本次内容</button> : !attempt.confirmation_current || !draftMatches ? <button type="button" disabled={busy || !assetOk || !draftMatches} onClick={confirm} className="tk-primary">确认这次内容</button> : <button type="button" disabled={busy || !actionEnabled(row,"export")} onClick={exportPack} className="tk-primary">导出参考文案与步骤</button>}
      </div>
      <details className="tk-gap"><summary>其他操作与核验</summary>
        <p className="tk-note">这里仅准备参考文案。平台支持的实际交接请使用上方“发布交接”。</p>
        <button type="button" disabled={busy || !attempt?.attempt_id} onClick={selfReport} className="tk-quiet">我已在平台操作（仅自报）</button>
        <button type="button" disabled={busy || !attempt?.attempt_id || !rewardOpen} onClick={submitProof} className="tk-quiet">提交发布证明</button>
        <p className="tk-note">自报与证明提交不表示官方发布成功，只有活动认可的核验才会进入权益判断。</p>
      </details>
      {attempt?.attempt_id && (
        <div className="tk-row tk-gap">
          <button type="button" disabled={busy} onClick={() => revise({ copy })} className="tk-quiet">
            更新文案（需重新确认）
          </button>

        </div>
      )}
      {editorClosed && <p className="tk-note">{row?.capabilities.open_editor?.reason}</p>}
      {publishClosed && <p className="tk-note">{row?.capabilities.authorized_publish?.reason}</p>}
      {notice && (
        <p className={succeeded ? "tk-ok" : "tk-note"} data-testid="publish-outcome" data-success={succeeded ? "true" : "false"}>
          {notice}
        </p>
      )}
      {attempt?.attempt_id && !draftMatches && (
        <p className="tk-unknown">文案已改，需要先更新，再重新确认。现在的输入还没有确认。</p>
      )}
      {attempt?.confirmation_current && draftMatches && !succeeded && (
        <p className="tk-note">你已确认这次参考文案。发布账号由你在平台内核对，确认文案不是发布成功。</p>
      )}
      {attempt?.package?.steps && attempt.package.steps.length > 0 && (
        <ol className="tk-list">
          {attempt.package.steps.map((step) => (
            <li key={step}>{step}</li>
          ))}
        </ol>
      )}
      {attempt && attempt.platform_post_id === "" && attempt.self_reported && (
        <p className="tk-note">没有回执，不计入已发布视频，也不发奖励。</p>
      )}
      {proof && (
        <p data-testid="reward-proof" className="tk-unknown">
          {proofNotice(proof)}
        </p>
      )}
    </section>
  );
}
