import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const read = (rel: string) => readFileSync(path.join(process.cwd(), rel), "utf8");

describe("HUI-2222 public normal path guards", () => {
  it("does not render a dead participation CTA when the server says it is closed", () => {
    const page = read("src/app/c/[code]/public-campaign.tsx");
    expect(page).toContain("capabilities?.participation");
    expect(page).toContain("canParticipate && !participationOpen");
    expect(page).toContain("这家店还没有开放在线参与");
    expect(page).toContain("closedReason");
    expect(page).toContain("canReadContent");
    expect(page).toContain("content-choices");
    expect(page).toContain("let version = participation?.content_pack_version");
  });

  it("pins the snapshot to the guest's version and surfaces mismatch", () => {
    const content = read("src/components/customer/ContentPublish.tsx");
    expect(content).toContain("version_id=");
    expect(content).toContain("version_mismatch");
    expect(content).toContain("content-version-mismatch");
    expect(content).toContain("data.pack_version_id");
    expect(content).toContain("requestSeq");
  });

  it("shows AI marking instead of silently publishing AI content", () => {
    const content = read("src/components/customer/ContentPublish.tsx");
    expect(content).toContain("marking?.ai_generated");
    expect(content).toContain("含 AI 合成画面或配音");
    expect(content).toContain("marking?.ad_required");
    const panel = read("src/components/work/content-pack-panel.tsx");
    expect(panel).toContain("ai-marking");
    const materials = read("src/app/work/materials/page.tsx");
    expect(materials).toContain("ai_generated");
    expect(materials).toContain("marking: { ad_required: true");
  });

  it("does not show a contradictory code note after a successful issue", () => {
    const reward = read("src/components/customer/RewardStatus.tsx");
    expect(reward).toContain("credentialIssueNotice");
    expect(reward).not.toContain("reward-qr");
    expect(reward).not.toContain("setVerifyNote(qrPayload ?");
    expect(reward).toContain("if (!id || busy) return;");
  });

  it("localizes the campaign window and marks a reached limit", () => {
    const page = read("src/app/c/[code]/public-campaign.tsx");
    expect(page).toContain("formatCampaignWindow");
    expect(page).not.toContain('view?.starts_at || "即日起"');
    const intro = read("src/components/customer/ActivityIntro.tsx");
    expect(intro).toContain('data-state="limit_reached"');
  });
});
