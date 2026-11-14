// HUI-1992: Touch package rights, AI tool fees, and marketing rewards stay
// in three panes. A coupon face value is not a platform cash expense, and a
// public visitor never sees an enterprise balance.

export interface RewardItemView {
  kind: string;
  face_minor: number;
  ledger?: string;
}

export interface SeparationView {
  package: {
    kind: "touch_subscription";
    status: string;
    restricts_new_premium: boolean;
    locks_existing: boolean;
  };
  ai_fees: {
    kind: "ai_tool_fee";
    quote_created: boolean;
    usage_created: boolean;
    includes_marketing_face: boolean;
    balance_mutated: boolean;
    personal_wallet_debited: boolean;
    reason?: string;
  };
  rewards: {
    kind: "marketing_reward";
    ledger: "activity";
    appears_in_platform_wallet: boolean;
    face_as_cash_expense: boolean;
    items: RewardItemView[];
  };
}

export interface PresentedSeparation {
  packageText: string;
  feeText: string;
  rewardText: string;
  showsEnterpriseBalance: false;
}

const benefitNames: Record<string, string> = {
  coupon: "优惠券",
  points: "积分",
  lottery: "抽奖",
  group_buy_voucher: "团购券",
};

function yuan(faceMinor: number): string {
  return (faceMinor / 100).toFixed(2);
}

export function presentAccountSeparation(view: SeparationView): PresentedSeparation {
  const status = view.package.status;
  let packageText = "套餐状态未确认。不会为了统一计费扣费。已有活动、留资和营销记录仍可查看。";
  if (status === "expired") {
    packageText = "套餐已过期，不能新增高级能力。已有活动、留资和营销记录仍可查看，不靠充值解锁。";
  } else if (status === "active" && !view.package.restricts_new_premium) {
    packageText = "套餐有效。新增高级能力按套餐权限单独判断，不和营销奖励混在一起。";
  }
  if (view.package.locks_existing) {
    packageText = "套餐已过期，不能新增高级能力。已有活动、留资和营销记录仍可查看，不靠充值解锁。";
  }

  let feeText = "这次没有实际收费调用，不产生 AI 工具费用。";
  if (view.ai_fees.reason === "payer_authorization_required" || view.ai_fees.reason === "personal_wallet_not_payer") {
    feeText = "没有商家付款授权时，不扣代理个人钱包，也不产生 AI 工具费用。";
  } else if (view.ai_fees.quote_created && !view.ai_fees.balance_mutated) {
    feeText = "已记录 AI 工具报价上下文。碰一碰不改钱包余额。";
  }

  const lines = view.rewards.items.map((item) => {
    const name = benefitNames[item.kind] ?? "营销权益";
    return `${name} ${yuan(item.face_minor)} 元在活动里，不是平台现金。`;
  });
  const rewardText = lines.length > 0 ? lines.join("") : "当前财务投影没有营销权益汇总。各活动的实际库存、待发放和已发权益以上方活动账本为准。";

  return {
    packageText,
    feeText,
    rewardText,
    showsEnterpriseBalance: false,
  };
}

export function publicVisitorCopy(payload: { action?: string } | null): {
  text: string;
  showsEnterpriseBalance: false;
  createsPlatformUser: false;
} {
  const action = payload?.action;
  const verb = action === "lead" ? "留资" : action === "claim" ? "领券" : "浏览";
  return {
    text: `${verb}不用登录，不创建平台账户，这里不显示企业钱包。`,
    showsEnterpriseBalance: false,
    createsPlatformUser: false,
  };
}
