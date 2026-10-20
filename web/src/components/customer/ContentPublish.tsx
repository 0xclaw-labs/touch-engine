"use client";

// HUI-1896 屏2「内容与发布」:顾客三屏第二屏的内容面。
// HUI-2628: the snapshot is pinned to the version the participation selected.
// A merchant adoption change never silently swaps the guest's content.

import { useEffect, useRef, useState } from "react";
import { admissionLines, type AdmissionView } from "@/lib/customer-journey";

export interface ContentChoice {
  pack_id: string;
  title: string;
  version_id: string;
  platform: string;
  content_kind?: string;
}

export interface ContentMarking {
  ad_required?: boolean;
  incentive_disclosed?: boolean;
  ai_generated?: boolean;
}

export interface ContentSnapshotView {
  pack_version_id?: string;
  title?: string;
  body?: string;
  topics?: string[];
  store_name?: string;
  price_note?: string;
  valid_note?: string;
  marking?: ContentMarking;
}

export interface ContentPublishProps {
  code: string;
  /** 自身参与的内容版本(回显当前选中;null=还没开始参与)。 */
  selectedVersion?: string;
  /** 未交接草稿才可换(canSwitchDraft 判定在页面)。 */
  canSwitch: boolean;
  onSwitchVersion?: (versionId: string) => void;
}

export function ContentPublish({ code, selectedVersion, canSwitch, onSwitchVersion }: ContentPublishProps) {
  const [choices, setChoices] = useState<ContentChoice[] | null>(null);
  const [choicesUnavailable, setChoicesUnavailable] = useState(false);
  const [snapshot, setSnapshot] = useState<ContentSnapshotView | null>(null);
  const [admission, setAdmission] = useState<AdmissionView | null>(null);
  const [mismatch, setMismatch] = useState("");
  const requestSeq = useRef(0);

  useEffect(() => {
    if (!code) return;
    let alive = true;
    fetch(`/api/public/links/${encodeURIComponent(code)}/content-choices`)
      .then(async (res) => {
        if (!alive) return;
        if (!res.ok) {
          setChoicesUnavailable(true);
          return;
        }
        const data = await res.json().catch(() => null);
        const items = Array.isArray(data?.items) ? (data.items as ContentChoice[]) : [];
        if (items.length === 0) setChoicesUnavailable(true);
        else setChoices(items);
      })
      .catch(() => {
        if (alive) setChoicesUnavailable(true);
      });
    return () => {
      alive = false;
    };
  }, [code]);

  // Default recommendation is the first choice. Once the guest has a selected
  // version, only that version may be shown; missing/retired is a real mismatch.
  useEffect(() => {
    if (!choices || choices.length === 0) return;
    const mine = selectedVersion ? choices.find((c) => c.version_id === selectedVersion) : undefined;
    if (selectedVersion && !mine) {
      setMismatch("你参与时选的内容已经不是门店当前可用的版本。请换选一个当前版本，或请店员协助。");
      setSnapshot(null);
      setAdmission(null);
      return;
    }
    const chosen = mine ?? choices[0];
    const seq = ++requestSeq.current;
    setMismatch("");
    let alive = true;
    const versionQuery = selectedVersion ? `&version_id=${encodeURIComponent(selectedVersion)}` : "";
    fetch(
      `/api/public/links/${encodeURIComponent(code)}/content-snapshot?pack_id=${encodeURIComponent(chosen.pack_id)}&platform=${encodeURIComponent(chosen.platform)}${versionQuery}`,
    )
      .then(async (res) => {
        if (!alive || seq !== requestSeq.current) return;
        const data = await res.json().catch(() => null);
        if (res.status === 409 && data?.error === "version_mismatch") {
          setMismatch("门店已经换了采用版本，和你参与时选的内容不一致。为了不让你发错内容，这里先不展示新版本。");
          setSnapshot(null);
          return;
        }
        if (res.ok && data) {
          if (selectedVersion && data.pack_version_id && data.pack_version_id !== selectedVersion) {
            setMismatch("读到的是别的内容版本，和你参与时选的不一致。没有把它当成你的内容。");
            setSnapshot(null);
            return;
          }
          setSnapshot(data as ContentSnapshotView);
        } else {
          setSnapshot(null);
        }
      })
      .catch(() => {
        if (alive && seq === requestSeq.current) setSnapshot(null);
      });
    fetch(
      `/api/public/links/${encodeURIComponent(code)}/channel-admission?platform=${encodeURIComponent(chosen.platform)}&entry_mode=h5&scenario=store_promo`,
    )
      .then(async (res) => {
        if (!alive || seq !== requestSeq.current) return;
        const data = await res.json().catch(() => null);
        if (res.ok && data) setAdmission(data as AdmissionView);
        else setAdmission(null);
      })
      .catch(() => {
        if (alive && seq === requestSeq.current) setAdmission(null);
      });
    return () => {
      alive = false;
    };
  }, [code, choices, selectedVersion]);

  const snapshotBody = snapshot?.body || "文案内容在开始交接时一并给出。";
  const lines = admissionLines(admission);
  const currentChoice = choices?.find((c) => c.version_id === selectedVersion) ?? choices?.[0];

  return (
    <section className="tk-section" data-testid="content-publish" data-version={selectedVersion ?? ""}>
      <h2 className="tk-section-title">内容与发布</h2>
      <p className="tk-note">
        发布用的是你自己的账号。本服务不会替你发布，也不保存你的平台密码；每一步都可以取消。
      </p>

      {choicesUnavailable ? (
        <p className="tk-note" data-testid="content-unavailable">
          这家店还没有准备好在线内容。可以按门店现场说明参与，或请店员协助。
        </p>
      ) : (
        <>
          <p className="tk-note">门店已为这次活动准备好内容。先看推荐：</p>
          {choices && choices.length > 1 && (
            <div className="tk-row" data-testid="content-choices">
              {choices.map((c) => (
                <button
                  key={c.version_id}
                  type="button"
                  className={c.version_id === (selectedVersion ?? choices[0].version_id) ? "tk-primary" : "tk-quiet"}
                  disabled={!canSwitch}
                  data-testid={`content-choice-${c.version_id}`}
                  onClick={() => onSwitchVersion?.(c.version_id)}
                >
                  {c.title || "内容包"}
                </button>
              ))}
            </div>
          )}
          {choices && choices.length > 1 && !canSwitch && (
            <p className="tk-note" data-testid="content-switch-frozen">
              当前参与的版本已交接，不能再换选。
            </p>
          )}
          {mismatch ? (
            <div className="tk-warn" data-testid="content-version-mismatch">
              <p>{mismatch}</p>
              {canSwitch && currentChoice ? (
                <button type="button" className="tk-quiet" onClick={() => onSwitchVersion?.(currentChoice.version_id)}>
                  换选到当前版本
                </button>
              ) : (
                <p className="tk-note">当前版本不能换选，请把参与编号给店员，由门店协助处理。</p>
              )}
            </div>
          ) : null}
          {snapshot && !mismatch && (
            <div data-testid="content-snapshot">
              <p className="tk-lead">〈{snapshot.title || "未命名"}〉</p>
              <p className="tk-public-value">{snapshotBody}</p>
              {snapshot.price_note && <p className="tk-note">价格说明:{snapshot.price_note}</p>}
              {snapshot.valid_note && <p className="tk-note">有效期说明:{snapshot.valid_note}</p>}
              {snapshot.marking?.ai_generated ? (
                <p className="tk-unknown" data-testid="content-ai-marking">
                  含 AI 合成画面或配音。这是内容标识，不是发布成功，也不改变奖励条件。
                </p>
              ) : null}
              {snapshot.marking?.ad_required ? (
                <p className="tk-note" data-testid="content-ad-marking">按平台要求，这条内容属于广告或推广信息。</p>
              ) : null}
            </div>
          )}
        </>
      )}

      <div data-testid="content-admission">
        {lines.map((line) => (
          <p key={line} className="tk-warn">{line}</p>
        ))}
        {lines.length === 0 && (
          <p className="tk-note">这个平台的发布路线已放行。往下用「发布交接」开始，视频和文案会在那一步交给你。</p>
        )}
      </div>
    </section>
  );
}
