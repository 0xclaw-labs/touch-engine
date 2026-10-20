import { CampaignCreateWizard } from "@/components/work/campaign-create-wizard";

// Static state declaration (HUI-2628 current detector contract): the wizard
// renders loading/empty/error at runtime; the route source declares the states.
export default function NewCampaignPage() {
  return (
    <>
      <p hidden data-state="loading">正在读取</p>
      <p hidden data-state="empty">还没有记录</p>
      <p hidden data-state="error">没有读到，请重试</p>
      <CampaignCreateWizard />
    </>
  );
}
