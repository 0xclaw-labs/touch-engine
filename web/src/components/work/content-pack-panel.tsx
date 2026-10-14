"use client";

import { useCallback, useEffect, useState } from "react";
import { failureText, useSession } from "@/components/work/merchant-session";
import { surfaceLabel } from "@/lib/product-finish";

// HUI-3553 活动内容面板:候选 → 商家预览/编辑 → 明确采用。
// 纪律:采用只经显式按钮(活动×平台指针只在这里移动);采用后顾客侧读同一
// 版本(pack_version_id 快照),不随机刷新换片;blocked/撤权用商家语言显示
// 原因,不显示内部机器码;弱网失败给重试,不把失败当成功。

interface AssetSnapshot {
  asset_ref?: string;
  sha256?: string;
}

interface PlatformVersion {
  platform: string;
  note?: string;
}

interface PackVersion {
  id: string;
  version_no: number;
  content_kind: string;
  status: string;
  title: string;
  body: string;
  topics?: string[];
  store_name: string;
  price_note: string;
  valid_note: string;
  cover?: AssetSnapshot | null;
  platform_versions?: PlatformVersion[];
  blocked_reason?: string;
}

interface PackAdoption {
  platform: string;
  version_id: string;
  adopted_by?: string;
  updated_at?: string;
}

interface ContentPack {
  id: string;
  title: string;
  versions: PackVersion[];
  adoptions: PackAdoption[];
}

function statusCopy(status: string): string {
  switch (status) {
    case "draft":
      return "编辑中";
    case "candidate":
      return "待采用";
    case "adopted":
      return "已采用";
    case "retired":
      return "已被新版本取代";
    case "blocked":
      return "已停止分发";
    default:
      return status;
  }
}

function coverText(cover?: AssetSnapshot | null): string {
  return cover && cover.asset_ref ? "封面已就绪" : "无封面";
}

function platformLabel(platform: string): string {
  switch (platform) {
    case "douyin":
      return "抖音";
    case "shipinhao":
      return "视频号";
    case "xiaohongshu":
      return "小红书";
    case "dianping":
      return "大众点评";
    default:
      return platform;
  }
}

export function ContentPackPanel({ campaignId }: { campaignId: string }) {
  const session = useSession();
  const [phase, setPhase] = useState<"loading" | "empty" | "ready" | "error">("loading");
  const [packs, setPacks] = useState<ContentPack[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");

  const load = useCallback(async () => {
    setPhase("loading");
    const res = await session.api("GET", `campaigns/${campaignId}/content-packs`);
    if (!res.ok) {
      setError(failureText(res.status, res.data));
      setPhase("error");
      return;
    }
    const items = Array.isArray(res.data.items) ? (res.data.items as ContentPack[]) : [];
    setPacks(items);
    setError("");
    setPhase(items.length === 0 ? "empty" : "ready");
  }, [campaignId, session]);

  useEffect(() => {
    void load().catch(() => {
      setError("没有连上服务。内容包没有读到,不把它当成空的。");
      setPhase("error");
    });
  }, [load]);

  async function adopt(packId: string, versionId: string, platform: string) {
    setBusy(versionId + platform);
    try {
      const res = await session.api("POST", `content-packs/${packId}/versions/${versionId}/adopt`, { platform });
      if (!res.ok) {
        setError(failureText(res.status, res.data));
        return;
      }
      setError("");
      await load();
    } catch {
      setError("没有完成(没有连上服务)。这一版还没有被采用。");
    } finally {
      setBusy("");
    }
  }

  async function block(packId: string, versionId: string) {
    const reason = window.prompt("停止分发的原因(会记进处理记录):");
    if (!reason || !reason.trim()) return;
    setBusy(versionId + ":block");
    try {
      const res = await session.api("POST", `content-packs/${packId}/versions/${versionId}/block`, { reason });
      if (!res.ok) {
        setError(failureText(res.status, res.data));
        return;
      }
      setError("");
      await load();
    } catch {
      setError("没有完成(没有连上服务)。这一版还没有被停止。");
    } finally {
      setBusy("");
    }
  }

  return (
    <section id="content-packs" className="tk-section">
      <h2 className="tk-section-title">活动内容</h2>
      <p className="tk-note">候选内容先预览,再点「采用这一版」。采用后顾客碰一碰拿到的就是这一版,不会自己换片。</p>
      {phase === "loading" ? <p className="tk-state" data-state="loading">{surfaceLabel("loading")}</p> : null}
      {phase === "empty" ? (
        <p className="tk-state" data-state="empty">
          这个活动还没有内容包。去<a href="/work/materials">素材页</a>上传已有内容或从素材库选择。
        </p>
      ) : null}
      {phase === "error" ? (
        <p className="tk-state tk-danger" data-state="error">
          {error || surfaceLabel("error")}
          <button className="tk-quiet" type="button" data-action="retry" onClick={() => void load()}>重试</button>
        </p>
      ) : null}
      {error && phase === "ready" ? <p className="tk-danger" data-state="error">{error}</p> : null}
      {phase === "ready" ? (
        <ul className="tk-list">
          {packs.map((pack) => {
            const adoptedByVersion = new Map(pack.adoptions.map((a) => [a.version_id, a]));
            return (
              <li key={pack.id} data-pack-id={pack.id}>
                <p className="tk-note">{pack.title}</p>
                <ul className="tk-list">
                  {pack.versions.map((version) => {
                    const adoption = adoptedByVersion.get(version.id);
                    const isBlocked = version.status === "blocked";
                    const platforms = (version.platform_versions ?? []).map((p) => p.platform);
                    const adoptTargets = platforms.length > 0 ? platforms : ["douyin"];
                    return (
                      <li key={version.id} data-version-id={version.id}>
                        <p className="tk-note">
                          第 {version.version_no} 版 · {statusCopy(version.status)} · {coverText(version.cover)}
                        </p>
                        <p className="tk-note" data-field="preview-title">{version.title}</p>
                        <p className="tk-note">{version.body}</p>
                        <p className="tk-note">门店 {version.store_name} · 价格 {version.price_note} · 有效期 {version.valid_note}</p>
                        {isBlocked ? (
                          <p className="tk-note tk-unknown" data-field="blocked-reason">
                            已停止分发:{version.blocked_reason || "素材或授权有问题"}。已发出去的内容保留原版本。
                          </p>
                        ) : null}
                        {adoption ? (
                          <p className="tk-note tk-ok" data-field="adopted-note">
                            当前采用({adoption.platform}):第 {version.version_no} 版,顾客看到的就是这一版。
                          </p>
                        ) : null}
                        <div className="tk-row">
                          {adoptTargets.map((platform) =>
                            adoption && adoption.platform === platform ? null : (
                              <button
                                key={platform}
                                className="tk-button"
                                type="button"
                                data-action="adopt"
                                disabled={busy === version.id + platform}
                                onClick={() => void adopt(pack.id, version.id, platform)}
                              >
                                采用这一版({platformLabel(platform)})
                              </button>
                            ),
                          )}
                          {!isBlocked && version.status !== "retired" ? (
                            <button
                              className="tk-quiet"
                              type="button"
                              data-action="block"
                              disabled={busy === version.id + ":block"}
                              onClick={() => void block(pack.id, version.id)}
                            >
                              停止分发
                            </button>
                          ) : null}
                        </div>
                      </li>
                    );
                  })}
                </ul>
              </li>
            );
          })}
        </ul>
      ) : null}
    </section>
  );
}
