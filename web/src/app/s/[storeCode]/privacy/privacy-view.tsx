"use client";

// HUI-1896 C6 隐私与授权管理(/s/{storeCode}/privacy;页内独立可链接面板)。
//
// 复用既有 /contact 提交与撤销链(1747/B14;4594 将补门店级留资,本页消费
// 面不变):lead-form 告知 → lead-submissions 提交 → lead-status 查询 →
// lead-revocations 撤销。URL ?code={linkCode} 提供活动/门店链接上下文;
// 没有 code 或留资未开通时给诚实待开态,不伪建表单。
//
// 纪律(票面/1747 基线):
//   - 告知四要素:接收商家/用途/必填字段/告知版本;营销许可独立勾选,
//     拒绝不阻断浏览与参与;
//   - 提交后状态诚实:本地提交成功、CRM 已接收、负责人跟进是三个不同事件;
//     待同步不说「销售已收到」,不提前宣告;
//   - 撤销即撤回授权(成功=已撤销);失败给核对手机号引导;
//   - 无生态导航、不强制留资、不创建平台账户。

import { Suspense, useCallback, useEffect, useState, type ChangeEvent } from "react";
import { useParams, useSearchParams } from "next/navigation";
import { publicVisitorColumns, leadOutcomeCopy } from "@/lib/visitor-experience";
import { failureText } from "@/lib/failure-copy";
import { LEAD_REVOKED_COPY, leadOutcomeState } from "@/lib/lead-outcome";

type PrivacyState = "loading" | "form" | "submitted" | "not_open" | "network_error";

interface LeadFormView {
  enabled: boolean;
  required_fields?: string[];
  purpose?: string;
  recipient?: { name?: string };
  notice?: { version: string; text: string };
  marketing_optin_enabled?: boolean;
  marketing_blocks_browse?: boolean;
}

interface LeadSubmitResult {
  submission_ref?: string;
  state?: string;
  duplicate?: boolean;
  crm_received?: boolean;
  submitted_to?: { name?: string };
}

interface LeadStatusResult {
  submission_ref?: string;
  state?: string;
  crm_received?: boolean;
  sales_received?: string;
  owner_followed_up?: string;
  creates_platform_account?: boolean;
  receiver_state?: string;
  revocation_pending?: boolean;
}

export function PrivacyView() {
  return (
    <Suspense fallback={<main className="tk-public" data-state="loading"><p className="tk-note">正在读取</p></main>}>
      <PrivacyViewInner />
    </Suspense>
  );
}

function PrivacyViewInner() {
  const params = useParams<{ storeCode: string }>();
  const storeCode = params?.storeCode ?? "";
  const searchParams = useSearchParams();
  const linkCode = (searchParams.get("code") ?? "").trim();
  const leadBase = linkCode ? `/api/public/links/${encodeURIComponent(linkCode)}` : `/api/public/storefronts/${encodeURIComponent(storeCode)}`;

  const [form, setForm] = useState<LeadFormView | null>(null);
  const [state, setState] = useState<PrivacyState>("loading");
  const [consulting, setConsulting] = useState(false);
  const [existingRef, setExistingRef] = useState("");
  const [name, setName] = useState("");
  const [phone, setPhone] = useState("");
  const [wechat, setWechat] = useState("");
  const [consent, setConsent] = useState(false);
  const [marketing, setMarketing] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState("");
  const [submitted, setSubmitted] = useState<LeadSubmitResult | null>(null);
  const [statusResult, setStatusResult] = useState<LeadStatusResult | null>(null);
  const [statusNote, setStatusNote] = useState("");
  const [revoking, setRevoking] = useState(false);
  const [revokeDone, setRevokeDone] = useState(false);
  const [revokeError, setRevokeError] = useState("");
  const [reloadTick, setReloadTick] = useState(0);

  const width = usePageWidth();
  const columns = publicVisitorColumns(width);

  // 告知读取:?code 存在才读;无 code → not_open(不伪建表单)。
  useEffect(() => {

    let alive = true;
    setState("loading");
    fetch(`${leadBase}/lead-form`)
      .then(async (res) => {
        if (!alive) return;
        const data = await res.json().catch(() => null);
        if (res.ok && data && data.enabled === true) {
          setForm(data as LeadFormView);
          setState("form");
        } else {
          setState("not_open");
        }
      })
      .catch(() => {
        if (alive) setState("network_error");
      });
    return () => {
      alive = false;
    };
  }, [leadBase, reloadTick]);

  const submitLead = useCallback(() => {
    if (!form?.notice) return;
    setSubmitError("");
    if (!consent) {
      setSubmitError("请先阅读并同意告知内容。拒绝营销通知不会挡住你看活动。");
      return;
    }
    setSubmitting(true);
    fetch(`${leadBase}/lead-submissions`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        name,
        phone,
        wechat,
        consent: true,
        consent_version: form.notice.version,
        marketing_optin: marketing,
        channel: "web",
      }),
    })
      .then(async (res) => {
        const data = (await res.json().catch(() => null)) as LeadSubmitResult | null;
        if (res.ok && data?.submission_ref) {
          setSubmitted(data);
          setState("submitted");
        } else {
          setSubmitError(res.status >= 500 || res.status === 401 || res.status === 403 ? failureText(res.status, {}) : "提交没有完成,请稍后再试");
        }
      })
      .catch(() => setSubmitError("网络异常,请稍后再试"))
      .finally(() => setSubmitting(false));
  }, [consent, form, leadBase, marketing, name, phone, wechat]);

  // 状态查询:待同步/已接收/已撤销各自真实说法;销售是否收到观察不到,
  // 永远 unknown,不伪报「销售已收到」。
  const queryStatus = useCallback(() => {
    if (!(submitted?.submission_ref || existingRef.trim())) return;
    setStatusNote("");
    fetch(`${leadBase}/lead-status`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ submission_ref: submitted?.submission_ref || existingRef.trim(), phone }),
    })
      .then(async (res) => {
        const data = (await res.json().catch(() => null)) as LeadStatusResult | null;
        if (res.ok && data) {
          setStatusResult(current => current?.state === "revoked" && data.state !== "revoked" ? current : data);
          setRevokeDone(current => current || data.state === "revoked");
          setSubmitted({submission_ref:data.submission_ref,state:data.state,crm_received:data.crm_received});
          setState("submitted");
          setStatusNote(data.state === "revoked" ? "已撤销:这次留资已撤回。" : "");
        } else {
          setStatusNote("状态暂时读不到。可以稍后再查,或联系门店。");
        }
      })
      .catch(() => setStatusNote("网络异常,请稍后再试。"));
  }, [leadBase, phone, submitted, existingRef]);

  const revokeLead = useCallback(() => {
    if (!(submitted?.submission_ref || existingRef.trim())) return;
    setRevokeError("");
    setRevoking(true);
    fetch(`${leadBase}/lead-revocations`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ submission_ref: submitted?.submission_ref || existingRef.trim(), phone }),
    })
      .then(async (res) => {
        const data = await res.json().catch(() => null);
        if (res.ok && data?.state === "revoked") { setRevokeDone(true); setStatusResult(data); }
        else setRevokeError("撤销没有完成,请核对手机号后重试");
      })
      .catch(() => setRevokeError("网络异常,请稍后再试"))
      .finally(() => setRevoking(false));
  }, [leadBase, phone, submitted, existingRef]);

  const retry = useCallback(() => setReloadTick((t) => t + 1), []);

  const merchant = form?.recipient?.name || "这家店";
  const outcome =
    submitted && !revokeDone
      ? leadOutcomeCopy({
          merchant: submitted.submitted_to?.name || merchant,
          state: submitted.duplicate ? "duplicate" : (statusResult?.state as string) || submitted.state || "accepted",
          duplicate: submitted.duplicate === true,
          crmReceived: statusResult ? statusResult.crm_received === true : submitted.crm_received === true,
          salesReceived: "unknown",
          ownerFollowedUp: "unknown",
        })
      : "";
  const outcomeState = leadOutcomeState(revokeDone ? LEAD_REVOKED_COPY : outcome);
  const requiredFields = form?.required_fields ?? [];

  return (
    <main
      data-testid="privacy-view"
      data-pattern="public_consumer"
      data-columns={columns}
      data-viewport={width}
      data-state={state}
      className="tk-public"
    >
      <div className="tk-public-card">
        <header>
          <p className="tk-public-merchant">隐私与授权</p>
          <h1 className="tk-public-title">我的联系方式与许可</h1>
        </header>
        <p className="tk-note">这里管理你交给这家店的联系方式和许可。看活动、用服务都不需要留下联系方式。</p>

        {state === "loading" && <p className="tk-note">正在读取…</p>}
        {state === "network_error" && (
          <div>
            <p className="tk-note">网络异常,请检查网络后重试。</p>
            <button type="button" className="tk-quiet" onClick={retry}>重试</button>
          </div>
        )}
        {state === "not_open" && (
          <div data-testid="privacy-not-open">
            <p className="tk-note">这家店当前无法接收新的在线咨询。以前的提交可在下方按原编号和手机号查询；若线上无法处理，请到门店服务台向工作人员提出撤回联系方式与营销许可，并说明提交时使用的手机号。若已离店，可通过门店已公布的电话或联系渠道提出同一请求。</p>
          </div>
        )}

        {state === "form" && form && !consulting && <button type="button" className="tk-primary" onClick={() => setConsulting(true)} data-testid="consult-open">请门店联系我</button>}
        {state === "form" && form && consulting && (
          <section className="tk-gap" data-testid="lead-disclosure">
            <h2 className="tk-section-title">把联系方式交给{merchant}</h2>
            <p className="tk-note">用途:{form.purpose || "由门店联系你,用于本次到店服务"}</p>
            <p className="tk-note">
              接收方:{merchant}。必填:{fieldLabels(requiredFields)}。告知版本 {form.notice?.version ?? "未知"}。
            </p>
            <p className="tk-note">{form.notice?.text}</p>
            <label className="tk-label">
              姓名（选填，只用于称呼）
              <input className="tk-input" value={name} onChange={(e: ChangeEvent<HTMLInputElement>) => setName(e.target.value)} placeholder="怎么称呼你" />
            </label>
            <label className="tk-label">
              手机号（用于本次联系与核对撤回）
              <input className="tk-input" value={phone} onChange={(e: ChangeEvent<HTMLInputElement>) => setPhone(e.target.value)} placeholder="商家用来联系你" inputMode="numeric" />
            </label>
            <label className="tk-label">
              微信号(选填)
              <input className="tk-input" value={wechat} onChange={(e: ChangeEvent<HTMLInputElement>) => setWechat(e.target.value)} placeholder="可选" />
            </label>
            <label className="tk-check">
              <input type="checkbox" checked={consent} onChange={(e: ChangeEvent<HTMLInputElement>) => setConsent(e.target.checked)} data-testid="consent" />
              我同意把上面的联系方式交给{merchant},用于这次告知里的用途
            </label>
            {form.marketing_optin_enabled && (
              <label className="tk-check">
                <input type="checkbox" checked={marketing} onChange={(e: ChangeEvent<HTMLInputElement>) => setMarketing(e.target.checked)} data-testid="marketing-optin" />
                另外同意以后接收活动通知。不勾选也可以继续看活动,也可以只提交这次联系方式
              </label>
            )}
            {submitError && <p className="tk-danger" data-state="error">{submitError}</p>}
            <button type="button" onClick={submitLead} disabled={submitting || !consent} data-testid="lead-submit" className="tk-primary">
              {submitting ? "提交中…" : "提交给这家店"}
            </button>
            <p className="tk-note">提交后可以随时在本页撤销;提交不会创建平台账户。</p>
          </section>
        )}

        {state === "submitted" && submitted?.submission_ref && (
          <section data-testid="lead-outcome" data-state={outcomeState} className={outcomeState === "received" ? "tk-outcome tk-ok" : "tk-outcome"}>
            {revokeDone ? (
              <> <p className="tk-ok" data-state="revoked">{statusResult?.receiver_state === "revoked" ? "授权已撤回，接收方已确认处理。" : "本服务已停止使用并发起撤回；接收方处理尚待确认。可继续查询处理状态。"}</p>
              <button type="button" className="tk-quiet" onClick={queryStatus}>查询撤回处理</button> </>
            ) : (
              <>
                <p className="tk-lead">{outcome}</p>
                <p className="tk-note">留资编号 {submitted.submission_ref}。可以在本页随时查询状态或撤销。</p>
                {statusResult && statusResult.state !== "revoked" && (
                  <p className="tk-note" data-testid="lead-sync-state">
                    {statusResult.crm_received === true
                      ? "商家已接收。是否已有负责人跟进,这里观察不到,以商家实际联系为准。"
                      : "待同步:商家还没有确认收到。这里不会提前说「销售已收到」。"}
                  </p>
                )}
                {statusNote && <p className="tk-note">{statusNote}</p>}
                {revokeError && <p className="tk-danger">{revokeError}</p>}
                <div className="tk-row tk-gap">
                  <button type="button" className="tk-quiet" onClick={queryStatus} data-testid="lead-query">查询同步状态</button>
                  <button type="button" className="tk-quiet" onClick={revokeLead} disabled={revoking} data-testid="lead-revoke">
                    {revoking ? "撤销中…" : "撤销我的授权"}
                  </button>
                </div>
              </>
            )}
          </section>
        )}

        {state !== "loading" && !submitted && <section className="tk-section" data-testid="lead-restore">
          <h2 className="tk-section-title">查询或撤回以前的提交</h2>
          <p className="tk-note">使用原提交编号和手机号核对。资料只在本次请求中使用，不会加入网址。</p>
          <label className="tk-label">提交编号<input className="tk-input" value={existingRef} onChange={e=>setExistingRef(e.target.value)} autoComplete="off" /></label>
          <label className="tk-label">原手机号<input className="tk-input" value={phone} onChange={e=>setPhone(e.target.value)} inputMode="tel" /></label>
          <button type="button" className="tk-quiet" disabled={!existingRef.trim() || !phone.trim()} onClick={queryStatus}>读取原记录</button>
          {statusNote && <p className="tk-note">{statusNote}</p>}
        </section>}
        <footer className="tk-row tk-gap">
          <a className="tk-quiet" href={`/s/${encodeURIComponent(storeCode)}`} data-testid="back-to-store">返回门店</a>
        </footer>
      </div>
    </main>
  );
}

function usePageWidth(): number {
  const [width, setWidth] = useState(390);
  useEffect(() => {
    const apply = () => setWidth(window.innerWidth || 390);
    apply();
    window.addEventListener("resize", apply);
    return () => window.removeEventListener("resize", apply);
  }, []);
  return width;
}

function fieldLabels(fields: string[]): string {
  return fields.map((field) => (field === "name" ? "姓名" : field === "phone" ? "手机号" : field)).join("、");
}
