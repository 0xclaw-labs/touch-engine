import React from "react";
import {
  presentAccountSeparation,
  type SeparationView,
} from "@/lib/account-separation";
import { Card, CardContent } from "@/components/ui/card";

export function AccountSeparation({ view }: { view: SeparationView }) {
  const presented = presentAccountSeparation(view);
  return (
    <div data-testid="account-separation" className="tk-admin-grid-12">
      <article data-testid="account-package">
        <Card>
          <CardContent className="tk-gap pt-5">
            <h3 className="tk-admin-mt-0">套餐权限</h3>
            <p className="tk-admin-mb-0">{presented.packageText}</p>
          </CardContent>
        </Card>
      </article>
      <article data-testid="account-ai-fees">
        <Card>
          <CardContent className="tk-gap pt-5">
            <h3 className="tk-admin-mt-0">AI 工具费用</h3>
            <p className="tk-admin-mb-0">{presented.feeText}</p>
          </CardContent>
        </Card>
      </article>
      <article data-testid="account-rewards">
        <Card>
          <CardContent className="tk-gap pt-5">
            <h3 className="tk-admin-mt-0">营销奖励</h3>
            <p className="tk-admin-mb-0">{presented.rewardText}</p>
          </CardContent>
        </Card>
      </article>
    </div>
  );
}
