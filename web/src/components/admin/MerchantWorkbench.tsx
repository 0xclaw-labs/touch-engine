"use client";

import { useEffect, useRef, useState } from "react";
import { AccountSeparation } from "@/components/admin/AccountSeparation";
import { Card, CardContent } from "@/components/ui/card";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { SeparationView } from "@/lib/account-separation";
import type { EcoNavModel } from "@/lib/eco-nav/model";
import { planTaskHandoff } from "@/lib/eco-nav/touch-shell";
import {
  activityGroupLabel,
  assembleTodos,
  contentGap,
  contentHonestyLine,
  customerCountLine,
  enterLeadsPlan,
  followUpLine,
  leadLink,
  offerContentTools,
  resolveWorkingFor,
  salesReceptionLine,
  visibleSections,
  workbenchBilling,
  workbenchColumns,
  type ContentToolInput,
} from "@/lib/workbench/compose";

interface WorkbenchSeat {
  role?: string;
  source?: "membership" | "delegation" | "agency" | "";
  relation_id?: string;
  delegation_type?: "maker_service" | "ops_collab";
  merchant_name?: string;
}

interface WorkbenchTask {
  kind: string;
  campaign_id?: string;
  title: string;
  object_id: string;
  state: string;
}

interface WorkbenchPayload {
  working_for?: string;
  seat?: WorkbenchSeat;
  my_tasks?: WorkbenchTask[];
  content?: {
    gap?: string;
    next_step?: string;
    upgrade_note?: string;
    locks_existing?: boolean;
    materials_known?: boolean;
    tasks_known?: boolean;
    materials?: { id: string; media_type?: string; purpose?: string }[];
    selections?: { campaign_id: string; asset_id: string }[];
    actions?: { id: string; mode: string; shown: boolean }[];
  };
  activities?: {
    draft?: ActivityCard[];
    in_progress?: ActivityCard[];
    ended?: ActivityCard[];
    unclassified?: ActivityCard[];
    next_step?: string;
  };
  customers?: {
    available?: boolean;
    reason?: string;
    cards?: CustomerCard[];
  };
  account_separation?: SeparationView;
}

interface ActivityCard {
  id: string;
  title: string;
  status: string;
  object_id: string;
  copy_inline?: boolean;
}

interface CustomerCard {
  campaign_id: string;
  title: string;
  authorized?: number | null;
  pending_sync?: number | null;
  sales_received?: { available?: boolean; value?: number };
  follow_up?: { available?: boolean; value?: number; reason?: string };
  open_lead_ids?: string[];
}

export interface WorkbenchCampaign {
  id: string;
  title: string;
  public_content: string;
}

export function MerchantWorkbench({
  tenantId,
  campaigns,
  onSaveCopy,
  onTodoCount,
}: {
  tenantId: string;
  campaigns: WorkbenchCampaign[];
  onSaveCopy: (id: string, title: string, copy: string) => Promise<void>;
  onTodoCount?: (count: number) => void;
}) {
  const [payload, setPayload] = useState<WorkbenchPayload | null>(null);
  const [model, setModel] = useState<EcoNavModel | null>(null);
  const [notice, setNotice] = useState("");
  const [intent, setIntent] = useState<ContentToolInput["intent"]>("operate");
  const [width, setWidth] = useState(960);
  const [copyDraft, setCopyDraft] = useState<{
    id: string;
    title: string;
    copy: string;
  } | null>(null);
  const todoRef = useRef(onTodoCount);
  todoRef.current = onTodoCount;

  useEffect(() => {
    const apply = () => setWidth(window.innerWidth);
    apply();
    window.addEventListener("resize", apply);
    return () => window.removeEventListener("resize", apply);
  }, []);

  useEffect(() => {
    if (!tenantId) return;
    let alive = true;
    fetch("/api/workbench", { headers: { "x-tenant-id": tenantId } })
      .then(async (res) =>
        res.ok ? ((await res.json()) as WorkbenchPayload) : null,
      )
      .then((next) => {
        if (!alive) return;
        setPayload(next);
        todoRef.current?.(next?.my_tasks?.length ?? 0);
      })
      .catch(() => {
        if (alive) setPayload(null);
      });
    return () => {
      alive = false;
    };
  }, [tenantId, campaigns]);

  useEffect(() => {
    let alive = true;
    fetch("/api/eco-nav")
      .then(async (res) =>
        res.ok ? ((await res.json()) as EcoNavModel) : null,
      )
      .then((next) => {
        if (alive) setModel(next);
      })
      .catch(() => {
        if (alive) setModel(null);
      });
    return () => {
      alive = false;
    };
  }, []);

  if (!payload) {
    return (
      <p data-testid="workbench-pending">
        工作台摘要还没读到。不会把缺失写成 0。
      </p>
    );
  }

  const seat = payload.seat ?? {};
  const serverBanner = resolveWorkingFor({
    seatSource:
      seat.source === "agency" ||
      seat.source === "delegation" ||
      seat.source === "membership"
        ? seat.source
        : "membership",
    role: seat.role ?? "",
    relationId: seat.relation_id,
    serverBanner: payload.working_for,
    delegations: [],
    merchantName: seat.merchant_name ?? "",
    now: new Date().toISOString(),
  });
  const ecoBanner = resolveWorkingFor({
    seatSource:
      model?.seat_source === "delegation" ? "delegation" : "membership",
    role: model?.role_label ?? "",
    relationId: model?.delegations.find((item) => item.type === "ops_collab")
      ?.delegation_id,
    delegations: model?.delegations ?? [],
    merchantName:
      model?.scopes.find((item) => item.tenant_id === model.active_tenant_id)
        ?.display_name ??
      seat.merchant_name ??
      "",
    now: new Date().toISOString(),
  });
  const workingFor = serverBanner || ecoBanner;
  const billing = workbenchBilling(model);
  const tasks = assembleTodos({
    tasks: payload.my_tasks ?? [],
    drafts: (payload.activities?.draft ?? []).map((item) => ({
      id: item.id,
      title: item.title,
      status: item.status,
    })),
    pending: (payload.customers?.cards ?? []).flatMap((card) => {
      if (typeof card.pending_sync !== "number") return [];
      return [
        { campaign_id: card.campaign_id, pending_sync: card.pending_sync },
      ];
    }),
  });
  const sections = visibleSections(tasks.length);
  const gap = contentGap(payload.content?.gap);
  const offers = offerContentTools({
    gap,
    intent,
    apps: model?.apps ?? [],
    returnProven: false,
  });
  const columns = workbenchColumns(width);
  const leadsApp = model?.apps.find((item) => item.app_id === "leads") ?? null;

  return (
    <section data-testid="merchant-workbench" className="tk-admin-grid-16">
      {workingFor ? (
        <p data-testid="ops-banner" className="tk-admin-notice">
          {workingFor}
        </p>
      ) : null}
      <div className="tk-admin-row tk-admin-between tk-admin-wrap tk-admin-gap-12">
        <h2 className="tk-admin-m-0">经营工作台</h2>
        <p data-testid="billing-strip" className="tk-admin-m-0">
          费用 {billing.label}
        </p>
      </div>
      {payload.account_separation ? (
        <AccountSeparation view={payload.account_separation} />
      ) : null}
      <div
        data-testid="workbench-grid"
        data-columns={columns}
        className={`tk-admin-grid-cols-${columns}`}
      >
        {sections.includes("my_tasks") ? (
          <Card data-testid="section-my-tasks">
            <CardContent className="tk-gap pt-5">
              <h3 className="tk-admin-mt-0">我的事情</h3>
              <ul>
                {tasks.map((task) => (
                  <li key={`${task.kind}-${task.object_id}`}>
                    <a href={`#campaign-${task.object_id}`}>{task.title}</a>
                    <span className="tk-admin-muted"> · {task.state}</span>
                  </li>
                ))}
              </ul>
            </CardContent>
          </Card>
        ) : null}
        <Card data-testid="section-content">
          <CardContent className="tk-gap pt-5">
            <h3 className="tk-admin-mt-0">内容</h3>
            {contentHonestyLine({
              gap,
              next_step: payload.content?.next_step,
            }) ? (
              <p
                data-testid={
                  gap === "unknown" ? "content-unknown" : "content-next"
                }
              >
                {contentHonestyLine({
                  gap,
                  next_step: payload.content?.next_step,
                })}
              </p>
            ) : null}
            {payload.content?.tasks_known === true ? null : (
              <p data-testid="content-tasks-unknown">
                内容任务摘要未知。这里不显示 0。
              </p>
            )}
            {(payload.content?.materials ?? []).length > 0 ? (
              <ul>
                {payload.content?.materials?.map((item) => (
                  <li key={item.id}>
                    {item.purpose || item.media_type || item.id}
                  </li>
                ))}
              </ul>
            ) : null}
            {(payload.content?.selections ?? []).length > 0 ? (
              <ul>
                {payload.content?.selections?.map((item) => (
                  <li key={`${item.campaign_id}-${item.asset_id}`}>
                    已选素材 {item.asset_id}
                  </li>
                ))}
              </ul>
            ) : null}
            {payload.content?.actions?.some(
              (action) => action.id === "edit_copy" && action.shown,
            ) ? (
              <CopyEditor
                campaigns={campaigns}
                draft={copyDraft}
                setDraft={setCopyDraft}
                onSaveCopy={onSaveCopy}
              />
            ) : null}
            <label className="tk-admin-block tk-admin-mt-8">
              当前内容任务
              <Select
                value={intent}
                onValueChange={(value) =>
                  setIntent(value as ContentToolInput["intent"])
                }
              >
                <SelectTrigger className="tk-admin-ml-8">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="operate">用已有素材做活动</SelectItem>
                  <SelectItem value="creative_plan">需要脚本和分镜</SelectItem>
                  <SelectItem value="deep_edit">需要深剪成片</SelectItem>
                  <SelectItem value="avatar">需要数字人口播</SelectItem>
                </SelectContent>
              </Select>
            </label>
            {offers.map((offer) =>
              offer.shown ? (
                <button
                  key={offer.id}
                  type="button"
                  data-testid={`offer-${offer.app_id}`}
                  onClick={() =>
                    prepareHandoff(
                      offer.app_id,
                      campaigns[0]?.id ?? "",
                      setNotice,
                    )
                  }
                  className="tk-admin-mt-8 tk-admin-mr-8"
                >
                  {offer.label}
                </button>
              ) : (
                <p key={offer.id} data-testid={`upgrade-${offer.app_id}`}>
                  {offer.upgrade}
                </p>
              ),
            )}
            {payload.content?.upgrade_note ? (
              <p>{payload.content.upgrade_note}</p>
            ) : null}
          </CardContent>
        </Card>
        <Card data-testid="section-activities">
          <CardContent className="tk-gap pt-5">
            <h3 className="tk-admin-mt-0">活动</h3>
            {payload.activities?.next_step ? (
              <p>
                <a href="#create-campaign">{payload.activities.next_step}</a>
              </p>
            ) : null}
            {(payload.activities?.unclassified ?? []).length > 0 ? (
              <p data-testid="activities-unclassified">
                状态未归类。不记成已结束，也不写成 0。
              </p>
            ) : null}
            <ActivityGroup
              label={activityGroupLabel("draft")}
              items={payload.activities?.draft ?? []}
            />
            <ActivityGroup
              label={activityGroupLabel("in_progress")}
              items={payload.activities?.in_progress ?? []}
            />
            <ActivityGroup
              label={activityGroupLabel("ended")}
              items={payload.activities?.ended ?? []}
            />
            <ActivityGroup
              label={activityGroupLabel("unclassified")}
              items={payload.activities?.unclassified ?? []}
            />
          </CardContent>
        </Card>
        <Card data-testid="section-customers">
          <CardContent className="tk-gap pt-5">
            <h3 className="tk-admin-mt-0">客户</h3>
            {payload.customers?.available === false ? (
              <p data-testid="customers-degraded">
                留资摘要未开启（{payload.customers.reason || "未知"}
                ）。活动仍可继续，这里不显示 0。
              </p>
            ) : (
              (payload.customers?.cards ?? []).map((card) => {
                const enter = enterLeadsPlan({
                  app: leadsApp,
                  campaignId: card.campaign_id,
                });
                return (
                  <div key={card.campaign_id} className="tk-admin-mb-10">
                    <a href={`#campaign-${card.campaign_id}`}>{card.title}</a>
                    <p className="tk-admin-m-4-0">
                      {customerCountLine("授权线索", card.authorized)} ·{" "}
                      {customerCountLine("待同步", card.pending_sync)}
                    </p>
                    <p
                      className="tk-admin-m-4-0"
                      data-testid={`sales-reception-${card.campaign_id}`}
                    >
                      {salesReceptionLine(card)}
                    </p>
                    <p
                      className="tk-admin-m-4-0"
                      data-testid={`follow-up-${card.campaign_id}`}
                    >
                      {followUpLine(card.follow_up)}
                    </p>
                    {(card.open_lead_ids ?? []).map((id) => {
                      const href = leadLink(
                        process.env.NEXT_PUBLIC_LEADS_ORIGIN ?? "",
                        id,
                      );
                      return href ? (
                        <a key={id} href={href} data-testid={`open-lead-${id}`}>
                          打开获客线索 {id}
                        </a>
                      ) : (
                        <p key={id} data-testid={`open-lead-${id}`}>
                          获客线索 {id}
                        </p>
                      );
                    })}
                    {enter.available ? (
                      <button
                        type="button"
                        data-testid={`enter-leads-${card.campaign_id}`}
                        onClick={() => {
                          const plan = planTaskHandoff(
                            "view_campaign_leads",
                            card.campaign_id,
                          );
                          setNotice(
                            `已准备进入获客查看本活动线索。碰一碰不读取联系人。交接 ${plan.kind}`,
                          );
                        }}
                      >
                        进入获客看本活动线索
                      </button>
                    ) : (
                      <p>获客工作台当前不可进入。活动主线没有被锁住。</p>
                    )}
                  </div>
                );
              })
            )}
          </CardContent>
        </Card>
      </div>
      {notice ? <p data-testid="workbench-notice">{notice}</p> : null}
    </section>
  );
}

function ActivityGroup({
  label,
  items,
}: {
  label: string;
  items: ActivityCard[];
}) {
  if (items.length === 0) return null;
  return (
    <div>
      <strong>{label}</strong>
      <ul>
        {items.map((item) => (
          <li key={item.id}>
            <a href={`#campaign-${item.object_id}`}>{item.title}</a>
            <span className="tk-admin-muted"> · {item.status}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}

function CopyEditor({
  campaigns,
  draft,
  setDraft,
  onSaveCopy,
}: {
  campaigns: WorkbenchCampaign[];
  draft: { id: string; title: string; copy: string } | null;
  setDraft: (next: { id: string; title: string; copy: string } | null) => void;
  onSaveCopy: (id: string, title: string, copy: string) => Promise<void>;
}) {
  const selected =
    draft ??
    (campaigns[0]
      ? {
          id: campaigns[0].id,
          title: campaigns[0].title,
          copy: campaigns[0].public_content,
        }
      : null);
  if (!selected) return <p>还没有活动。先创建活动，再在这里写标题和简介。</p>;
  return (
    <form
      data-testid="inline-copy"
      onSubmit={(event) => {
        event.preventDefault();
        void onSaveCopy(selected.id, selected.title, selected.copy);
      }}
      className="tk-admin-grid-6 tk-admin-mt-8"
    >
      <Select
        value={selected.id}
        onValueChange={(value) => {
          const next = campaigns.find((item) => item.id === value);
          if (next)
            setDraft({
              id: next.id,
              title: next.title,
              copy: next.public_content,
            });
        }}
      >
        <SelectTrigger className="w-full">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {campaigns.map((item) => (
            <SelectItem key={item.id} value={item.id}>
              {item.title}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <input
        value={selected.title}
        onChange={(event) =>
          setDraft({ ...selected, title: event.target.value })
        }
        placeholder="标题"
      />
      <input
        value={selected.copy}
        onChange={(event) =>
          setDraft({ ...selected, copy: event.target.value })
        }
        placeholder="简介"
      />
      <button type="submit">在本页保存标题和简介</button>
    </form>
  );
}

function prepareHandoff(
  appId: string,
  campaignId: string,
  setNotice: (message: string) => void,
) {
  if (!campaignId) {
    setNotice("还没有活动，不能把制作交到别的应用。");
    return;
  }
  if (appId === "product-image") {
    const plan = planTaskHandoff("make_campaign_image", campaignId);
    void fetch("/api/eco-nav/campaign-image", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ campaign_id: campaignId }),
    })
      .then(async (res) => {
        const data = (await res.json()) as {
          message?: string;
          project_id?: string;
          resolution?: string;
        };
        if (!res.ok || !data.project_id) {
          setNotice(data.message || "产品图没有创建或恢复工程");
          return;
        }
        const verb = data.resolution === "restored" ? "恢复" : "创建";
        setNotice(
          `产品图已${verb}工程 ${data.project_id}。这不是发布成功。交接 ${plan.kind}`,
        );
      })
      .catch(() => setNotice("产品图没有创建或恢复工程"));
    return;
  }
  const kind =
    appId === "leads" ? "view_campaign_leads" : "make_campaign_video";
  const plan = planTaskHandoff(kind, campaignId);
  setNotice(
    `已准备${appId}交接（${plan.nav_intent}）。手工切换应用不会创建目标项目，也不会记成发布成功。`,
  );
}
