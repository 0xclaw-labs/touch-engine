import { BenefitsView } from "@/app/s/[storeCode]/benefits/benefits-view";

// The original activity can show the visitor's existing benefits without
// inventing a storefront code or requiring a newly configured storefront.
export default async function CampaignBenefitsPage({ params }: { params: Promise<{ code: string }> }) {
  const { code } = await params;
  return <BenefitsView returnHref={`/c/${encodeURIComponent(code)}`} returnLabel="返回活动" />;
}
