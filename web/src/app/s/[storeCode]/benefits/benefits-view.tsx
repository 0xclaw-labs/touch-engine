"use client";

// HUI-1896 C5 我的权益与凭证(/s/{storeCode}/benefits)。
//
// 只读 1671 访客本人权益面(同旅程 benefits 视图的地址化入口):
//   - 活动结束/订阅到期不拦读(T16):读路径不经新参与 Gate,页面不给
//     「活动结束看不到券」类错误;已发未过期权益按原承诺可查;
//   - 展示码重签:旧展示码过期不续,持有效访客会话可重签短时店员码
//     (POST display-code);明文只显示一次,不落盘;customer_resume 不进本页;
//   - 无访客会话(401)给诚实引导,不自动创建身份;REWARD_OPS off(404)
//     诚实「暂未开通」;网络失败给重试;
//   - 无生态导航、不强制注册/留资/下载。

import { Suspense, useCallback, useEffect, useState } from "react";
import { useParams } from "next/navigation";
import { publicVisitorColumns } from "@/lib/visitor-experience";
import {
  benefitRowFace,
  expiryReasonFace,
  parseVisitorBenefits,
  type VisitorBenefitRow,
} from "@/lib/visitor-benefits";

type BenefitsState = "loading" | "ready" | "visitor_required" | "unavailable" | "network_error";

interface DisplayCode {
  token: string;
  expiresAt: string;
  benefitId: string;
}

export function BenefitsView() {
  return (
    <Suspense fallback={<main className="tk-public" data-state="loading"><p className="tk-note">正在读取权益</p></main>}>
      <BenefitsViewInner />
    </Suspense>
  );
}

function BenefitsViewInner() {
  const params = useParams<{ storeCode: string }>();
  const storeCode = params?.storeCode ?? "";

  const [rows, setRows] = useState<VisitorBenefitRow[] | null>(null);
  const [state, setState] = useState<BenefitsState>("loading");
  const [displayCode, setDisplayCode] = useState<DisplayCode | null>(null);
  const [codeNote, setCodeNote] = useState("");
  const [busyId, setBusyId] = useState("");
  const [reloadTick, setReloadTick] = useState(0);

  const width = usePageWidth();
  const columns = publicVisitorColumns(width);

  const load = useCallback(() => {
    let alive = true;
    setState("loading");
    fetch("/api/visitor/reward-benefits")
      .then(async (res) => {
        if (!alive) return;
        if (res.status === 401) {
          setState("visitor_required");
          return;
        }
        if (res.status === 404) {
          setState("unavailable");
          return;
        }
        if (res.status === 502 || res.status === 503 || res.status === 504) {
          setState("network_error");
          return;
        }
        const data = await res.json().catch(() => null);
        const parsed = res.ok ? parseVisitorBenefits(data) : null;
        if (parsed) {
          setRows(parsed);
          setState("ready");
        } else {
          setState("network_error");
        }
      })
      .catch(() => {
        if (alive) setState("network_error");
      });
    return () => {
      alive = false;
    };
  }, []);

  useEffect(() => {
    return load();
  }, [load, reloadTick]);

  // 展示码重签:短时店员定位码;明文只在本次响应出现一次。
  const resignDisplayCode = useCallback(async (benefitId: string) => {
    if (busyId !== "") return;
    setBusyId(benefitId);
    setCodeNote("");
    try {
      const res = await fetch(`/api/visitor/reward-benefits/${encodeURIComponent(benefitId)}/display-code`, { method: "POST" });
      const data = await res.json().catch(() => null);
      if (res.status === 201 && data && typeof data.token === "string") {
        setDisplayCode({
          token: data.token,
          expiresAt: typeof data.expires_at === "string" ? data.expires_at : "",
          benefitId,
        });
        setCodeNote("展示码已重签,旧的作废,以这个新码为准。只显示这一次,请直接给店员在确认与核销台输入。");
      } else {
        setCodeNote("展示码暂时签发不出来。可以直接把权益页给店员看,或请店员协助。");
      }
    } catch {
      setCodeNote("网络没有给出明确结果。稍后再试,或直接把权益页给店员看。");
    } finally {
      setBusyId("");
    }
  }, [busyId]);

  const retry = useCallback(() => {
    setReloadTick((t) => t + 1);
  }, []);

  return (
    <main
      data-testid="benefits-view"
      data-pattern="public_consumer"
      data-columns={columns}
      data-viewport={width}
      data-state={state}
      className="tk-public"
    >
      <div className="tk-public-card">
        <header>
          <p className="tk-public-merchant">我的权益与凭证</p>
          <h1 className="tk-public-title">已发放的奖励</h1>
        </header>
        <p className="tk-note">活动结束或订阅到期,已发放、还没有过有效期的权益仍按原期限可以查看和使用。</p>

        {state === "loading" && <p className="tk-note">正在读取…</p>}
        {state === "visitor_required" && (
          <div data-testid="benefits-visitor-required">
            <p className="tk-note">需要访客会话才能查看自己的权益。请从门店的碰一碰标签或二维码重新进入,不能凭短码查看别人的权益。</p>
          </div>
        )}
        {state === "unavailable" && (
          <div data-testid="benefits-unavailable">
            <p className="tk-note">权益查询暂未开通。已发放的奖励不会因此丢失,可请店员在店内协助查询。</p>
          </div>
        )}
        {state === "network_error" && (
          <div>
            <p className="tk-note">网络异常,权益加载失败。请检查网络后重试。</p>
            <button type="button" className="tk-quiet" onClick={retry}>重试</button>
          </div>
        )}

        {state === "ready" && (
          <>
            {(rows ?? []).length === 0 ? (
              <p className="tk-note" data-testid="benefits-empty">还没有记录到可用的权益。完成店员确认后由门店发放,发放前这里不会有券码。</p>
            ) : (
              <div className="tk-gap" data-testid="benefits-list">
                {rows?.map((row) => {
                  const face = benefitRowFace(row);
                  const issued = face.status === "issued";
                  return (
                    <section key={row.benefit.id} className="tk-section" data-testid={`benefit-${row.benefit.id}`}>
                      <div data-testid="benefit-card">
                        {face.lines.map((line, index) => (
                          <p key={line.slice(0, 24) + String(index)} className={line.includes("已发放") || line.includes("已兑现") ? "tk-ok" : "tk-note"}>{line}</p>
                        ))}
                        {(row.expiry_reasons ?? []).map((reason) => (
                          <p key={reason} className="tk-note">注意:{expiryReasonFace(reason)}(已有权益按原记录保留)。</p>
                        ))}
                      </div>
                      {issued && row.benefit.id && (
                        <button
                          type="button"
                          className="tk-quiet"
                          disabled={busyId !== ""}
                          data-testid={`benefit-resign-${row.benefit.id}`}
                          onClick={() => resignDisplayCode(row.benefit.id ?? "")}
                        >
                          给店员的展示码(重签)
                        </button>
                      )}
                    </section>
                  );
                })}
              </div>
            )}
            {displayCode && (
              <p className="tk-note" data-testid="benefit-display-code">
                展示码(只显示这一次):{" "}
                <code>{displayCode.token}</code>
                {displayCode.expiresAt ? `。有效期至 ${displayCode.expiresAt}` : ""}
              </p>
            )}
            {codeNote && <p className="tk-note" data-testid="benefit-code-note">{codeNote}</p>}
          </>
        )}

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
