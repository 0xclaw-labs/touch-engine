import { isHttpsUrl, parseClientAction, parseOrderedMedia, type OrderedMediaEntry } from "./client-action";
import { buildDouyinSchema } from "./douyin";
import { buildXhsHandoff, invokeXhsJs, loadOfficialXhsSdk } from "./xiaohongshu";

/** Local handoff facts only. Never substitute these for an official receipt. */
export type LocalHandoffFact = { evidence: "handoff_only"; operation: "sdk_invoked" | "system_share" | "download_requested"; officialResult: "unknown" };
const fact = (operation: LocalHandoffFact["operation"]): LocalHandoffFact => ({ evidence: "handoff_only", operation, officialResult: "unknown" });

/** Fetch before enabling the user-action share button: awaiting fetch inside a
 * click handler can consume transient user activation on iOS. Preserve order. */
export async function loadMediaFiles(media: OrderedMediaEntry[], fetcher: typeof fetch = fetch): Promise<File[]> {
  if (!media.length) throw new Error("本次没有可交接的文件，请刷新原交接获取素材。");
  const files: File[] = [];
  let total = 0;
  for (const item of media) {
    if (!isHttpsUrl(item.url)) throw new Error("素材引用无效，请刷新原交接。");
    const response = await fetcher(item.url, { credentials: "omit", redirect: "error" });
    if (!response.ok) throw new Error(response.status === 401 || response.status === 403 ? "素材链接已失效，请刷新原交接重新获取。" : "素材下载失败，请在原交接重试。");
    const blob = await response.blob();
    const expected = item.kind === "video" ? "video/" : "image/";
    total += blob.size;
    if (!blob.size || !blob.type.startsWith(expected) || total > 512 * 1024 * 1024) throw new Error("素材为空、格式不符或过大，请重新获取素材。");
    const ext = blob.type.includes("png") ? "png" : blob.type.includes("quicktime") ? "mov" : item.kind === "video" ? "mp4" : "jpg";
    files.push(new File([blob], `${String(files.length + 1).padStart(2, "0")}-${item.kind}.${ext}`, { type: blob.type }));
  }
  return files;
}

/** Invoke directly from the user's click, with already prepared actual files. */
export function sharePreparedFiles(files: File[], nav: Pick<Navigator, "share" | "canShare"> = navigator): Promise<LocalHandoffFact> {
  if (!files.length || !nav.share || !nav.canShare?.({ files })) return Promise.reject(new Error("当前环境不支持这些文件的系统分享，请逐个保存素材后在目标应用选择。"));
  return nav.share({ files }).then(() => fact("system_share"));
}

export function sharePublicLink(url: string, title = "", nav: Pick<Navigator, "share"> = navigator): Promise<LocalHandoffFact> {
  if (!isHttpsUrl(url) || !nav.share) return Promise.reject(new Error("当前环境无法分享此链接，可主动复制链接。"));
  return nav.share({ url, title }).then(() => fact("system_share"));
}

/** Download request is not proof of a file reaching Photos or publication. */
export function savePreparedFile(file: File): LocalHandoffFact {
  if (!file.size) throw new Error("文件尚未准备好，请重新获取素材。");
  const url = URL.createObjectURL(file);
  const a = document.createElement("a");
  a.href = url; a.download = file.name; a.rel = "noopener";
  document.body.appendChild(a); a.click(); a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 60_000);
  return fact("download_requested");
}

/** Preload outside the user gesture; no invoking, no clipboard mutation. */
export async function preparePlatformHandoff(raw: unknown): Promise<{ files: File[] }> {
  const action = parseClientAction(raw);
  if (!action) throw new Error("交接参数无效，请刷新原交接。");
  if (action.params.sdk === "xhs_js") { await loadOfficialXhsSdk(); return { files: [] }; }
  if (action.type === "manual_export") return { files: await loadMediaFiles(parseOrderedMedia(action.params)) };
  return { files: [] };
}

/** Call only as a direct result of user intent. OAuth submission still uses
 * the canonical server submit endpoint after account/content confirmation. */
export async function executePlatformHandoff(raw: unknown, options?: { onFailure?: (message: string) => void }): Promise<LocalHandoffFact> {
  const action = parseClientAction(raw);
  if (!action) throw new Error("交接参数无效，请刷新原交接。");
  if (action.params.sdk === "xhs_js") {
    const model = buildXhsHandoff(raw);
    if (model.kind !== "xhs_handoff") throw new Error(model.reason);
    await invokeXhsJs(model.payload, model.verifyConfig, options?.onFailure);
    return fact("sdk_invoked");
  }
  if (action.params.sdk === "douyin_h5") {
    const schema = buildDouyinSchema(raw);
    if (!schema) throw new Error("签名或素材已失效，请刷新原交接重新获取。");
    window.location.assign(schema);
    return fact("sdk_invoked");
  }
  throw new Error("本方法需要按页面显示的授权、系统分享或保存步骤继续。");
}

export function handoffFailureMessage(error: unknown, payloadKind: "media" | "link_share" = "media"): string {
  const name = typeof error === "object" && error !== null && "name" in error ? String(error.name) : "";
  if (payloadKind === "link_share") {
    if (name === "AbortError") return "分享未继续（已取消或没有可用分享目标）；链接仍在，可重试或主动复制。";
    if (name === "NotAllowedError" || name === "SecurityError") return "当前浏览器或系统没有允许这次分享；请检查权限后主动重试，或复制链接。";
  }
  if (name === "AbortError") return "分享未继续（已取消或没有可用分享目标）；素材仍在，可重试或逐个保存。";
  if (name === "NotAllowedError" || name === "SecurityError") return "当前浏览器或系统没有允许这次分享；请检查权限后主动重试，或逐个保存素材。";
  if (name === "DataError" || name === "InvalidStateError") return "本页没有取得分享完成结果，请核对目标应用后回到原交接重试。";
  return error instanceof Error ? error.message : "交接暂未完成，请回到原交接重试。";
}
