"use client";

// HUI-1896 屏1「活动与优惠」结构化面板:顾客三屏第一屏的增量部分。
//
// 职责:把「要完成的动作、店员怎么确认、限额、领取条件」先说清楚,再给
// 主动作「选择内容并参与」。商家/门店、活动说明、时间、门店提示由基线
// 区块(门店头 + 活动说明区)承载,这里不复制,避免同文重复渲染。
//
// 纪律:
//   - 本组件不 fetch(数据由页面装配传入);建参与只经 onParticipationStart
//     回调交给页面走 T2 门面——浏览路径绝不建参与;
//   - 限额文案唯一出口 stockLine;预留文案唯一出口 benefitCopy(3546:
//     未确认不说「已保留」);异常文案唯一出口 exceptionCopy;
//   - 公共页红线:无生态导航、不推销派诺工具、不要求安装随身搭子、
//     不建商家账号。

import { benefitCopy } from "@/lib/participation";
import { evidenceStepCopy, exceptionCopy, stockLine } from "@/lib/customer-journey";

export interface ActivityIntroProps {
  /** 3535 放行的核验方式集合(店员见提交/店员核对作品/官方确认)。 */
  evidenceKinds: string[];
  promise?: {description?:string; benefit_name?:string; stores?:string[]; claim_from?:string; claim_until?:string; redeem_from?:string; redeem_until?:string; valid_from?:string; valid_until?:string; dedup_basis?:string; per_order_limit?:boolean; onsite_redemption?:boolean};
  /** 1671 公开 summary 的余量;unknown 传 undefined(不显示编造数字)。 */
  availableQty?: number;
  /** 自己参与的预留状态(未开始时缺省)。 */
  benefitReservationState?: string;
  /** participation 门控 off(或活动不可参与)时 false。 */
  canStart: boolean;
  existingParticipation?:boolean;
  reservationReady?: boolean;
  conditionsConfirmed?: boolean;
  /** 库存确认用尽(0)→按钮停用并给恢复动作。 */
  stockExhausted: boolean;
  /** 在线参与未开通时的真实原因;空=不展示。 */
  closedReason?: string;
  onParticipationStart?: () => void;
}

export function ActivityIntro({
  evidenceKinds,
  promise,
  availableQty,
  benefitReservationState,
  canStart,
  existingParticipation,
  reservationReady = false,
  conditionsConfirmed = false,
  stockExhausted,
  closedReason,
  onParticipationStart,
}: ActivityIntroProps) {
  const stock = stockLine(availableQty);
  const benefit = benefitCopy(benefitReservationState);
  if(existingParticipation)return <section className="tk-section" data-testid="activity-intro"><h2 className="tk-section-title">继续这次参与</h2><p className="tk-note">{reservationReady ? "原参与、内容和已取得权益保留。请在下方继续发布交接，领取与使用以原记录中的冻结规则为准。" : "原参与编号保留。请先在下方回查或恢复本次名额，确认前不要先完成有奖励任务。"}</p>{benefit&&<p className="tk-note">{benefit}</p>}<a href="#my-activity-benefits" className="tk-quiet">查看本次权益与凭证</a></section>;
  return (
    <section className="tk-section" data-testid="activity-intro">
      <h2 className="tk-section-title">这次活动的优惠怎么拿</h2>

      {promise?.benefit_name && <p className="tk-lead">本次权益：{promise.benefit_name}</p>}
      {promise?.description && <p className="tk-note">{promise.description}</p>}
      {promise?.stores?.length ? <p className="tk-note">适用门店：{promise.stores.join("、")}</p> : <p className="tk-note">适用门店尚未确认，请先向门店核对。</p>}
      {promise?.claim_until && <p className="tk-note">领取时间：{promise.claim_from || "以活动开始时间为准"} 至 {promise.claim_until}</p>}
      {(promise?.redeem_until || promise?.valid_until) && <p className="tk-note">使用时间：{promise.redeem_from || promise.valid_from || "按券面说明"} 至 {promise.redeem_until || promise.valid_until}</p>}
      {promise?.per_order_limit && <p className="tk-note">每笔订单限领一次。</p>}
      {promise?.dedup_basis && <p className="tk-note">领取资格与去重条件：{{verifiable_receipt:"按本次消费凭据核对领取资格",platform_authorized_subject:"按平台授权账号核对领取资格",onsite_manual:"由门店现场核对领取资格"}[promise.dedup_basis as "verifiable_receipt"|"platform_authorized_subject"|"onsite_manual"] || "领取资格由门店按活动原规则核对"}</p>}
      {conditionsConfirmed && <><h3 className="tk-note">要完成的动作</h3>
      <ol className="tk-list">
        <li>从门店准备好的内容里选一条（视频和文案都由门店做好）。</li>
        <li>用你自己的账号，在已放行的平台发布。</li>
        <li>按下方本次认可的方式核验，符合条件后由门店发放。</li>
      </ol>

      <h3 className="tk-note">店员怎么确认</h3>
      <ul className="tk-list">
        {evidenceKinds.map((kind) => (
          <li key={kind}>{evidenceStepCopy(kind)}</li>
        ))}
        {evidenceKinds.length === 0 && <li>{evidenceStepCopy("")}</li>}
      </ul>
      </>}

      {stock && <p className="tk-note" data-testid="activity-stock">{stock}</p>}
      {benefit && <p className="tk-note" data-testid="activity-benefit">{benefit}</p>}
      {conditionsConfirmed && <p className="tk-note">领取条件：仅按本次认可的核验方式确认；库存和资格以办理时实际核对为准。发放前不展示券码。</p>}

      {stockExhausted ? (
        <p className="tk-warn" data-testid="activity-stock-exhausted" data-state="limit_reached">{exceptionCopy("reward_exhausted")}</p>
      ) : canStart && onParticipationStart ? (
        <button type="button" className="tk-primary" data-testid="activity-start" onClick={onParticipationStart}>
          选择内容并参与
        </button>
      ) : (
        <p className="tk-note" data-testid="activity-start-closed">
          {closedReason || "这家店暂时不能在线开始参与。可以按门店现场说明参与，或请店员协助。"}
        </p>
      )}
    </section>
  );
}
