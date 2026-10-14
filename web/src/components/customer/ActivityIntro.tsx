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
  /** 1671 公开 summary 的余量;unknown 传 undefined(不显示编造数字)。 */
  availableQty?: number;
  /** 自己参与的预留状态(未开始时缺省)。 */
  benefitReservationState?: string;
  /** participation 门控 off(或活动不可参与)时 false。 */
  canStart: boolean;
  /** 库存确认用尽(0)→按钮停用并给恢复动作。 */
  stockExhausted: boolean;
  onParticipationStart?: () => void;
}

export function ActivityIntro({
  evidenceKinds,
  availableQty,
  benefitReservationState,
  canStart,
  stockExhausted,
  onParticipationStart,
}: ActivityIntroProps) {
  const stock = stockLine(availableQty);
  const benefit = benefitCopy(benefitReservationState);
  return (
    <section className="tk-section" data-testid="activity-intro">
      <h2 className="tk-section-title">这次活动的优惠怎么拿</h2>

      <h3 className="tk-note">要完成的动作</h3>
      <ol className="tk-list">
        <li>从门店准备好的内容里选一条（视频和文案都由门店做好）。</li>
        <li>用你自己的账号，在已放行的平台发布。</li>
        <li>发布后请店员确认，确认通过才有优惠。</li>
      </ol>

      <h3 className="tk-note">店员怎么确认</h3>
      <ul className="tk-list">
        {evidenceKinds.map((kind) => (
          <li key={kind}>{evidenceStepCopy(kind)}</li>
        ))}
        {evidenceKinds.length === 0 && <li>{evidenceStepCopy("")}</li>}
      </ul>

      {stock && <p className="tk-note" data-testid="activity-stock">{stock}</p>}
      {benefit && <p className="tk-note" data-testid="activity-benefit">{benefit}</p>}
      <p className="tk-note">领取条件:店员确认完成后由门店发放。发放前不会先展示券码。</p>

      {stockExhausted ? (
        <p className="tk-warn" data-testid="activity-stock-exhausted">{exceptionCopy("reward_exhausted")}</p>
      ) : canStart && onParticipationStart ? (
        <button type="button" className="tk-primary" data-testid="activity-start" onClick={onParticipationStart}>
          选择内容并参与
        </button>
      ) : (
        <p className="tk-note" data-testid="activity-start-closed">
          这家店暂时不能在线开始参与。可以按门店现场说明参与，或请店员协助。
        </p>
      )}
    </section>
  );
}
