"use client";

// HUI-1896 屏2「内容与发布」:顾客三屏第二屏的内容面。
//
// 职责:默认推荐一个 3553 已采用内容包(先看文案与说明),少量换选;
// 平台只按 3535 放行面提示(查询 channel-admission,off/unknown 诚实显示
// 不可用原因)。真正的交接动作收敛在页面挂载的 1670 PublishHandoff 组件
//(自足:自己拉 routes/start;媒体准备与失败恢复都内联在那里)。
//
// 纪律:
//   - 只读公共白名单端点(content-choices/content-snapshot/channel-admission);
//     不建参与(开始是屏1唯一主动作)、不触碰奖励面;
//   - 未放行/未知路线不出「先手动发完再领」类后门指引;
//   - 发布主体声明:顾客自己的账号,可取消,不会替顾客发布;
//   - 快照媒体只有 asset_ref/sha256 锚(3553 公共白名单),这里只展示文案;
//     视频文件在开始交接后由 1670 受控媒体流程给出。

import { useEffect, useState } from "react";
import { admissionLines, type AdmissionView } from "@/lib/customer-journey";

export interface ContentChoice {
  pack_id: string;
  title: string;
  version_id: string;
  platform: string;
  content_kind?: string;
}

export interface ContentSnapshotView {
  pack_version_id?: string;
  title?: string;
  body?: string;
  topics?: string[];
  store_name?: string;
  price_note?: string;
  valid_note?: string;
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
  const [chosenPack, setChosenPack] = useState("");
  const [snapshot, setSnapshot] = useState<ContentSnapshotView | null>(null);
  const [admission, setAdmission] = useState<AdmissionView | null>(null);

  // 已采用内容包:404/门控 off = 诚实降级(不编造内容)。
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

  // 默认推荐第一个;顾客已选定(参与里冻结/草稿)则回显其版本。
  useEffect(() => {
    if (!choices || choices.length === 0) return;
    const mine = selectedVersion ? choices.find((c) => c.version_id === selectedVersion) : undefined;
    const chosen = mine ?? choices[0];
    setChosenPack(chosen.pack_id);
    let alive = true;
    fetch(
      `/api/public/links/${encodeURIComponent(code)}/content-snapshot?pack_id=${encodeURIComponent(chosen.pack_id)}&platform=${encodeURIComponent(chosen.platform)}`,
    )
      .then(async (res) => {
        if (!alive) return;
        const data = await res.json().catch(() => null);
        if (alive && res.ok && data) setSnapshot(data as ContentSnapshotView);
        else if (alive) setSnapshot(null);
      })
      .catch(() => {
        if (alive) setSnapshot(null);
      });
    // 3535 放行面:与 1670 web 流同参(h5 + store_promo)。
    fetch(
      `/api/public/links/${encodeURIComponent(code)}/channel-admission?platform=${encodeURIComponent(chosen.platform)}&entry_mode=h5&scenario=store_promo`,
    )
      .then(async (res) => {
        if (!alive) return;
        const data = await res.json().catch(() => null);
        if (alive && res.ok && data) setAdmission(data as AdmissionView);
        else if (alive) setAdmission(null);
      })
      .catch(() => {
        if (alive) setAdmission(null);
      });
    return () => {
      alive = false;
    };
  }, [code, choices, selectedVersion]);

  const snapshotBody = snapshot?.body || "文案内容在开始交接时一并给出。";
  const lines = admissionLines(admission);

  return (
    <section className="tk-section" data-testid="content-publish">
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
          {snapshot && (
            <div data-testid="content-snapshot">
              <p className="tk-lead">〈{snapshot.title || "未命名"}〉</p>
              <p className="tk-public-value">{snapshotBody}</p>
              {snapshot.price_note && <p className="tk-note">价格说明:{snapshot.price_note}</p>}
              {snapshot.valid_note && <p className="tk-note">有效期说明:{snapshot.valid_note}</p>}
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
      {chosenPack && <p className="tk-note">当前内容包 {chosenPack}。</p>}
    </section>
  );
}
