"use client";

// HUI-4589 顾客承接面板（C3 服务模板）组件，按 actionId 复用。
// 消费方：HUI-1896 顾客六视图（/s/{storeCode} 与服务详情页）。
//
// 纪律：
//   - 点评面板（ReviewPanel）：无任何输入框/生成按钮/截图上传/奖励提示——
//     「零模板零AIGC零截图核验零奖励触发」的 UI 侧第二道拦（后端投影零内容
//     字段是第一道）。
//   - 一切面板零成功话术：不存在「已关注/已连接/已添加/已点评」状态。
//   - Wi-Fi（WifiPanel）：SSID/密码仅展示 + 主动复制按钮；「连不上？」展开
//     手动回退指引（T12）；无任何留资前置。
//   - 微信（ContactPanel）：二维码（公网 https 才渲染）+ 复制微信号 + 按环境
//     识别步骤（T13）；企微官方「联系我」链接原样打开。
//   - open/jump_attempt/copy 事件 fire-and-forget 上报（POST events）；上报
//     失败静默（不阻断使用）；回执文案永远带「不代表」否定。

import { useCallback, useState } from "react";
import {
  admissionLine,
  contactSteps,
  eventNotice,
  followSteps,
  panelKind,
  reviewNotices,
  safeQrURL,
  wifiFallbackSteps,
  type ServiceEntry,
} from "@/lib/store-service-panels";

export interface StoreServicePanelProps {
  entry: ServiceEntry;
  /** 当前环境（3535 六环境口径；未知给通用步骤）。 */
  environment?: string;
  /** storefrontCode：事件上报的目标门店（公共码，非内部 id）。 */
  storefrontCode?: string;
  /** reportEvent 注入点（默认 fetch POST events；测试可替换）。 */
  reportEvent?: (payload: { service_id: string; event: "open" | "jump_attempt" | "copy" }) => void;
}

function useReporter(props: StoreServicePanelProps) {
  const { storefrontCode, reportEvent, entry, environment } = props;
  return useCallback(
    (event: "open" | "jump_attempt" | "copy") => {
      if (!entry.service_id) return;
      if (reportEvent) {
        reportEvent({ service_id: entry.service_id, event });
        return;
      }
      if (!storefrontCode) return;
      void fetch(`/api/public/storefronts/${encodeURIComponent(storefrontCode)}/services/events`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ service_id: entry.service_id, event, environment }),
      }).catch(() => {
        /* 上报失败静默：事件是证据，不是功能门槛。 */
      });
    },
    [entry.service_id, reportEvent, storefrontCode, environment],
  );
}

function NoticeList({ lines }: { lines: string[] }) {
  return (
    <ul className="tk-note">
      {lines.map((line, index) => (
        <li key={index}>{line}</li>
      ))}
    </ul>
  );
}

/** 点评面板：只帮顾客找到正确平台门店，自主评价。零模板零AIGC零截图零奖励。 */
export function ReviewPanel(props: StoreServicePanelProps) {
  const entry = props.entry;
  const report = useReporter(props);
  const steps = entry.guidance?.steps ?? ["打开平台内本店门店主页，按平台流程自主撰写并发表评价"];
  return (
    <section data-testid="store-service-panel" data-kind="review" className="tk-section">
      <h3>{entry.label || "点评本店"}</h3>
      <p className="tk-note">{admissionLine(entry)}</p>
      <ol className="tk-note">
        {steps.map((step, index) => (
          <li key={index}>{step}</li>
        ))}
      </ol>
      <NoticeList lines={reviewNotices()} />
      {typeof entry.target_url === "string" && entry.target_url.startsWith("https://") ? (
        <p>
          <a
            className="tk-button"
            data-action="open-platform-store"
            href={entry.target_url}
            target="_blank"
            rel="noreferrer"
            onClick={() => report("open")}
          >
            打开平台门店页（评价在平台内由你自己完成）
          </a>
        </p>
      ) : (
        <p className="tk-note">该点评入口暂无可打开的页面，可先在平台内搜索本店。</p>
      )}
    </section>
  );
}

/** 关注面板：主页链接 + 可识别账号信息 + 未安装手动步骤；点击≠已关注。 */
export function FollowPanel(props: StoreServicePanelProps) {
  const entry = props.entry;
  const report = useReporter(props);
  const steps = entry.guidance?.steps?.length ? entry.guidance.steps : followSteps(entry);
  return (
    <section data-testid="store-service-panel" data-kind="follow" className="tk-section">
      <h3>{entry.label || "关注我们"}</h3>
      <p className="tk-note">{admissionLine(entry)}</p>
      {typeof entry.target_url === "string" && entry.target_url.startsWith("https://") ? (
        <>
          <p className="tk-note">账号主页：{entry.target_url.replace(/^https:\/\//, "").slice(0, 60)}</p>
          <ol className="tk-note">
            {steps.map((step, index) => (
              <li key={index}>{step}</li>
            ))}
          </ol>
          <p>
            <a
              className="tk-button"
              data-action="open-follow-page"
              href={entry.target_url}
              target="_blank"
              rel="noreferrer"
              onClick={() => report("jump_attempt")}
            >
              打开账号主页
            </a>
          </p>
        </>
      ) : (
        <p className="tk-note">暂无可打开的主页链接，可在平台内搜索本店账号。</p>
      )}
      <p className="tk-note">打开页面只记录为一次点击；本系统不记录也不显示「已关注」。</p>
    </section>
  );
}

/** Wi-Fi 面板：展示 + 主动复制 + 失败回手动（T12）；无留资门。 */
export function WifiPanel(props: StoreServicePanelProps) {
  const entry = props.entry;
  const report = useReporter(props);
  const [showFallback, setShowFallback] = useState(false);
  const [copyNote, setCopyNote] = useState("");
  const wifi = entry.wifi ?? {};
  const ssid = (wifi.ssid ?? "").trim();
  const password = (wifi.password ?? "").trim();
  const copy = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(password);
    } catch {
      /* 剪贴板不可用（如非安全上下文）时顾客手动选择复制，不谎报已复制。 */
      setCopyNote("无法自动复制，请长按/选中密码手动复制。");
      return;
    }
    report("copy");
    setCopyNote(eventNotice("copy"));
  }, [password, report]);
  return (
    <section data-testid="store-service-panel" data-kind="wifi" className="tk-section">
      <h3>{entry.label || "门店 Wi-Fi"}</h3>
      {ssid !== "" ? (
        <dl className="tk-note">
          <dt>Wi-Fi 名称</dt>
          <dd data-field="wifi-ssid">{ssid}</dd>
          <dt>密码</dt>
          <dd data-field="wifi-password">{password !== "" ? password : "无（开放网络）"}</dd>
        </dl>
      ) : (
        <p className="tk-note">连接信息待门店补充，可直接询问店员。</p>
      )}
      {password !== "" ? (
        <p>
          <button className="tk-button" type="button" data-action="copy-wifi-password" onClick={() => void copy()}>
            复制密码
          </button>
          {copyNote ? <span className="tk-note"> {copyNote}</span> : null}
        </p>
      ) : null}
      <p>
        <button className="tk-quiet" type="button" data-action="wifi-fallback" onClick={() => setShowFallback((v) => !v)}>
          {showFallback ? "收起手动步骤" : "连不上？手动连接指引"}
        </button>
      </p>
      {showFallback ? (
        <ol className="tk-note" data-testid="wifi-fallback-steps">
          {(entry.guidance?.steps?.length ? entry.guidance.steps : wifiFallbackSteps()).map((step, index) => (
            <li key={index}>{step}</li>
          ))}
        </ol>
      ) : null}
      <p className="tk-note">本面板只展示连接信息，不能替你完成连接；是否连上以你手机的 Wi-Fi 设置为准。</p>
    </section>
  );
}

/** 微信面板：二维码 + 复制微信号 + 按环境识别步骤（T13）；企微官方链接。 */
export function ContactPanel(props: StoreServicePanelProps) {
  const entry = props.entry;
  const report = useReporter(props);
  const environment = props.environment ?? "";
  const qr = safeQrURL(entry.qr_url ?? "");
  const wechatId = (entry.wechat_id ?? "").trim();
  const wecomURL = typeof entry.wecom_url === "string" && entry.wecom_url.startsWith("https://") ? entry.wecom_url : "";
  const steps = entry.guidance?.steps?.length ? entry.guidance.steps : contactSteps(environment);
  const [copyNote, setCopyNote] = useState("");
  const copyId = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(wechatId);
    } catch {
      setCopyNote("无法自动复制，请长按/选中微信号手动复制。");
      return;
    }
    report("copy");
    setCopyNote(eventNotice("copy"));
  }, [wechatId, report]);
  return (
    <section data-testid="store-service-panel" data-kind="contact" className="tk-section">
      <h3>{entry.label || "加微信"}</h3>
      <p className="tk-note">{admissionLine(entry)}</p>
      {qr !== "" ? (
        <p>
          {/* 二维码引用是商家配置的公网 https 图片；alt 说明用途。 */}
          <img src={qr} alt="门店微信二维码" width={160} height={160} />
        </p>
      ) : null}
      {wechatId !== "" ? (
        <p className="tk-note">
          微信号：<span data-field="wechat-id">{wechatId}</span>
          <button className="tk-quiet" type="button" data-action="copy-wechat-id" onClick={() => void copyId()}>
            复制微信号
          </button>
          {copyNote ? <span> {copyNote}</span> : null}
        </p>
      ) : null}
      <ol className="tk-note">
        {steps.map((step, index) => (
          <li key={index}>{step}</li>
        ))}
      </ol>
      {wecomURL !== "" ? (
        <p>
          <a
            className="tk-button"
            data-action="open-wecom-contact"
            href={wecomURL}
            target="_blank"
            rel="noreferrer"
            onClick={() => report("jump_attempt")}
          >
            打开企业微信「联系我」{entry.source_note ? `（来源：${entry.source_note}）` : ""}
          </a>
        </p>
      ) : null}
      <p className="tk-note">扫码或复制微信号不代表已添加；对方通过后才算添加成功，以微信官方为准。</p>
    </section>
  );
}

/** QQ 群/社群面板：群号/邀请链接 + 审批中如实显示；点击≠已加群。 */
export function GroupPanel(props: StoreServicePanelProps) {
  const entry = props.entry;
  const report = useReporter(props);
  const inviteURL = typeof entry.invite_url === "string" && entry.invite_url.startsWith("https://") ? entry.invite_url : "";
  const qr = safeQrURL(entry.qr_url ?? "");
  const groupNo = (entry.group_no ?? "").trim();
  return (
    <section data-testid="store-service-panel" data-kind="group" className="tk-section">
      <h3>{entry.label || "加入社群"}</h3>
      <p className="tk-note">{admissionLine(entry)}</p>
      {groupNo !== "" ? (
        <p className="tk-note">
          群号：<span data-field="group-no">{groupNo}</span>
        </p>
      ) : null}
      {entry.approval_note ? (
        <p className="tk-warn" data-field="approval-note">
          群状态：{entry.approval_note}（以平台内实际群状态为准）
        </p>
      ) : null}
      {inviteURL !== "" ? (
        <p>
          <a
            className="tk-button"
            data-action="open-group-invite"
            href={inviteURL}
            target="_blank"
            rel="noreferrer"
            onClick={() => report("jump_attempt")}
          >
            打开邀请链接
          </a>
        </p>
      ) : null}
      {qr !== "" ? (
        <p>
          <img src={qr} alt="社群二维码" width={160} height={160} />
        </p>
      ) : null}
      <p className="tk-note">点击不代表已加群；申请后按平台规则等待群主通过。</p>
    </section>
  );
}

/** 按 actionId 复用的面板入口（1896 C3 直接渲染本组件）。 */
export function StoreServicePanel(props: StoreServicePanelProps) {
  switch (panelKind(props.entry)) {
    case "review":
      return <ReviewPanel {...props} />;
    case "follow":
      return <FollowPanel {...props} />;
    case "wifi":
      return <WifiPanel {...props} />;
    case "contact":
      return <ContactPanel {...props} />;
    case "group":
      return <GroupPanel {...props} />;
    default:
      return null;
  }
}
