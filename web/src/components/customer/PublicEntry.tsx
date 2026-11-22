"use client";

// 顾客公共入口（`/` 匿名/未就绪分支的入口壳）：首屏承诺卡 + 门店/产品卡 + 固定底部 CTA。
// 纪律：不 fetch、不构造跳转地址、不建商家账号；数据与商家工作台路径分开。
// 已登录商家路径仍由 TodayDesk/TodayBody 渲染，本壳只挂在 gate 未就绪分支之上。

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";

interface EntryCard {
  id: string;
  badge: "success" | "secondary" | "muted";
  badgeText: string;
  title: string;
  desc: string;
}

const ENTRY_CARDS: EntryCard[] = [
  {
    id: "activity",
    badge: "success",
    badgeText: "进行中",
    title: "活动与优惠",
    desc: "门店准备好的视频、文案和到店权益，扫码后一屏看清。",
  },
  {
    id: "how",
    badge: "secondary",
    badgeText: "三步",
    title: "参与方式",
    desc: "选一条内容 → 用自己的账号发布 → 店员确认后由门店发放优惠。",
  },
  {
    id: "contact",
    badge: "muted",
    badgeText: "可留资",
    title: "联系门店",
    desc: "没找到活动或有疑问时，直接联系门店询问。",
  },
];

export function PublicEntry() {
  return (
    <div className="tk-public" data-pattern="public_entry">
      <Card className="tk-public-card" data-testid="entry-promise">
        <CardContent className="tk-gap pt-6">
          <p className="tk-kicker">碰一碰</p>
          <h1 className="tk-public-title">到店扫码，即看这家店的活动与优惠</h1>
          <p className="tk-note">
            不用下载应用；店员准备好内容，你选一条发布，店员确认后由门店发放优惠。
          </p>
        </CardContent>
      </Card>

      <div className="tk-stack">
        {ENTRY_CARDS.map((card) => (
          <Card key={card.id} data-testid={`entry-card-${card.id}`}>
            <CardContent className="tk-gap pt-5">
              <div className="tk-row tk-admin-between">
                <strong>{card.title}</strong>
                <Badge variant={card.badge}>{card.badgeText}</Badge>
              </div>
              <p className="tk-note">{card.desc}</p>
            </CardContent>
          </Card>
        ))}
      </div>

      <div className="tk-public-foot-cta" data-testid="entry-cta-bar">
        <Button
          className="min-h-11 w-full"
          size="lg"
          asChild
          data-action="entry-cta"
        >
          <a href="#gate-entry">查看门店活动</a>
        </Button>
      </div>
    </div>
  );
}
