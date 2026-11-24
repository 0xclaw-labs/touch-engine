// dev-only fixture upstream for touch-engine web shell verification.
//
// 用途：在没有真实 Go/identity 后端的本机环境下，给 Next BFF
// (TOUCH_SERVER_URL 指到这里) 提供一份合法会话与只读演示数据，
// 让 /work/* 页面在真实守卫代码下走到 ready 主体渲染。
//
// 纪律（与任务口径一致）：
//   - 不改动产品代码里的任何 gate/守卫；MerchantGate、作用域解析、
//     角色判断、失败/空态分支全部按 main 既有逻辑正常执行。
//   - 本服务只模拟上游 BFF 契约：memberships/whoami 给出一个真实、
//     启用的 membership，守卫据此正常通过；它不注释、不绕过、不伪造
//     前端登录态。
//   - 不进入产品构建：web/src 不 import 本文件；仅 dev/冒烟时由
//     人工 `node scripts/dev-fixture-server.mjs` 启动。
//   - 未覆盖的写端点统一回 200 `{ok:true}` 仅供交互冒烟；未覆盖的
//     只读端点诚实 404，页面按既有降级分支展示未知/空态，不假装成功。
//
// 启动：node scripts/dev-fixture-server.mjs [port]  (默认 4135)

import { createServer } from "node:http";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const PORT = Number(process.argv[2] || process.env.FIXTURE_PORT || 4135);
const HOST = "127.0.0.1";

const TENANT_ID = "tnt_demo";

const stores = [
  { id: "st_1", name: "演示门店", address: "贵阳市观山湖示范路 1 号", status: "active" },
  { id: "st_2", name: "第二门店", address: "花溪大道 28 号", status: "active" },
];

const campaigns = [
  {
    id: "cmp_1",
    title: "秋季到店礼遇",
    public_content: "到店出示活动页，即可领取一份秋季伴手礼，数量有限。",
    status: "active",
    starts_at: "2026-11-01T00:00:00Z",
    ends_at: "2026-12-31T23:59:59Z",
    store_id: "st_1",
    availability: { state: "active", label: "当前可参与", can_start: true },
  },
  {
    id: "cmp_2",
    title: "草稿：会员日预热",
    public_content: "",
    status: "draft",
    starts_at: "2026-12-10T00:00:00Z",
    ends_at: "2026-12-31T23:59:59Z",
    store_id: "st_1",
    availability: { state: "draft", label: "草稿", can_start: false },
  },
];

const touchpoints = [
  {
    id: "tp_1",
    code: "TP-0001",
    label: "门店主入口",
    position: "前台",
    status: "active",
    deployment_stage: "verified",
    store_id: "st_1",
    store_name: "演示门店",
    store_code: "ST-001",
    campaign_id: "cmp_1",
    campaign_title: "秋季到店礼遇",
    target_type: "store",
    entry_state: "ok",
    binding_revision: 1,
    url: "https://demo.local/t/TP-0001",
  },
];

const tags = [
  { id: "tag_1", label: "门店主标签", code: "NFC-0001", status: "active", link_id: "lnk_1", store_id: "st_1", campaign_id: "cmp_1", campaign_title: "秋季到店礼遇" },
];

const contentPacks = [
  {
    id: "pack_1",
    title: "秋季门店图文",
    campaign_id: "cmp_1",
    versions: [
      {
        pack_id: "pack_1",
        id: "ver_1",
        version_no: 1,
        content_kind: "imagetext",
        status: "adopted",
        source: "upload",
        title: "秋季礼遇图文",
        body: "到店出示活动页即可领取一份伴手礼。",
        topics: ["秋季"],
        store_name: "演示门店",
        price_note: "",
        valid_note: "长期有效",
        video: null,
        cover: null,
        image_text: [],
        platform_versions: [{ platform: "meituan" }],
        marking: { ad_required: false, ai_generated: false },
      },
      {
        pack_id: "pack_1",
        id: "ver_2",
        version_no: 2,
        content_kind: "video",
        status: "candidate",
        source: "produce",
        title: "门店介绍短片",
        body: "",
        topics: [],
        store_name: "演示门店",
        price_note: "",
        valid_note: "本月有效",
        video: null,
        cover: null,
        image_text: [],
        platform_versions: [],
        marking: { ad_required: false, ai_generated: false },
      },
    ],
    adoptions: [{ platform: "meituan", pack_id: "pack_1", version_id: "ver_1", adopted_by: "owner@demo.local", updated_at: "2026-11-20T10:00:00Z" }],
  },
];

const opsTenantRow = {
  id: TENANT_ID,
  name: "演示门店",
  created_at: "2026-10-01T08:00:00Z",
  brand_id: "",
  lifecycle: "active",
  charge_hold: false,
  store_count: 2,
  member_count: 3,
  agency_count: 0,
  subscription_status: "active",
  paid_until: "2026-12-31T23:59:59Z",
  retain_until: "",
  ops_revision: 1,
};

// 1x1 透明 PNG（合法图片，供 <img onLoad> 触发 ready 态）。
const PNG_1X1 = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==",
  "base64",
);

function sendJson(res, status, body) {
  const payload = JSON.stringify(body);
  res.writeHead(status, { "content-type": "application/json; charset=utf-8", "content-length": Buffer.byteLength(payload) });
  res.end(payload);
}

function list(items) {
  return { items };
}

function analyticsOperations() {
  const now = new Date();
  const start = new Date(now.getFullYear(), now.getMonth(), now.getDate(), 0, 0, 0);
  const iso = (d) => d.toISOString();
  return {
    window: { start: iso(start), end: iso(now) },
    scope: { tenant_id: TENANT_ID, store_scope: "" },
    filter_echo: {},
    funnel: [],
    evidence: { distinct_participations: 3, official_attempts: 1, note: "三类核验证据单列，不相加成发布总人数。" },
    evidence_layers: { layers: [], note: "" },
    leads: { campaign_submitted: 2, store_submitted: 2, pending_sync: 0, receiver_confirmed: 2, definition: { source: "留资表单", dedup_key: "contact", denominator: "参与会话", event_time: "提交时刻", window: "当天" }, note: "" },
    inventory: [],
    cost: {
      reward_cost_minor: 1200,
      reward_redeemed_cost_minor: 800,
      reward_face_minor: 2000,
      reward_cost_unrecorded: 0,
      redeemed_cost_unrecorded: 0,
      unrecorded_batches: 0,
      content_generation_available: false,
      content_generation_note: "",
      face_note: "券面额是营销面值，不是实际支出。",
    },
    cost_reconciliation: { subscription_available: false, hardware_available: false, note: "" },
    anomalies: { pending_review: 1, reservation_failed: 0, reversed_in_window: 0, expired_in_window: 0 },
    notes: { entry_marker: "NFC 只是入口标记，不冒充自然人数。", visitor: "匿名参与不冒充自然人数。" },
    dimensions: {
      campaigns: campaigns.map((c) => ({ id: c.id, title: c.title, store_id: c.store_id })),
      stores: stores.map((s) => ({ id: s.id, name: s.name })),
      actions: [],
    },
  };
}

const server = createServer((req, res) => {
  const url = new URL(req.url, `http://${HOST}`);
  const pathname = url.pathname.replace(/^\/api\/v1/, "");
  const method = req.method || "GET";

  const segments = pathname.split("/").filter(Boolean);
  const [root, a, b, c, d] = segments;

  // ---- 会话（守卫真实执行：memberships → whoami 正常通过）----
  if (method === "GET" && pathname === "/session/memberships") {
    return sendJson(res, 200, list([{ tenant_id: TENANT_ID, display_name: "演示门店", role: "org_owner", enabled: true, source: "membership" }]));
  }
  if (method === "GET" && pathname === "/whoami") {
    return sendJson(res, 200, {
      principal_ref: "usr_demo",
      email: "owner@demo.local",
      tenant_id: TENANT_ID,
      role: "org_owner",
      store_scope: "",
      enabled: true,
      tenant_name: "演示门店",
      features: { service_desk: true, authz: false, billing_cashier_app_id: "", billing_cashier_target_id: "" },
    });
  }
  if (method === "POST" && pathname === "/auth/refresh") return sendJson(res, 200, { refreshed: true });
  if (method === "POST" && pathname === "/auth/login") return sendJson(res, 200, { authenticated: true });
  if (method === "POST" && pathname === "/auth/logout") return sendJson(res, 200, { logged_out: true });

  // ---- 经营摘要 / 看板 ----
  if (method === "GET" && pathname === "/dashboard/operations-summary") {
    return sendJson(res, 200, { pending_confirm: 2, rewards_issued: 12, rewards_redeemed: 8 });
  }
  if (method === "GET" && root === "dashboard" && a === "operations") {
    return sendJson(res, 200, analyticsOperations());
  }
  if (method === "GET" && root === "dashboard") return sendJson(res, 404, { error: "not_enabled" });

  // ---- 门店 ----
  if (method === "GET" && pathname === "/stores") return sendJson(res, 200, list(stores));
  if (method === "GET" && root === "stores" && a && !b) {
    const store = stores.find((s) => s.id === a);
    return store ? sendJson(res, 200, store) : sendJson(res, 404, { error: "not_found" });
  }

  // ---- 活动 ----
  if (method === "GET" && pathname === "/campaigns") return sendJson(res, 200, list(campaigns));
  if (method === "GET" && root === "campaigns" && a && !b) {
    const camp = campaigns.find((x) => x.id === a);
    return camp ? sendJson(res, 200, camp) : sendJson(res, 404, { error: "not_found" });
  }
  if (method === "GET" && root === "campaigns" && a && b === "publish-config") return sendJson(res, 200, {
    enabled: true,
    version: 1,
    enabled_version: 1,
    published_status: "enabled",
    spec: {
      verification_mode: "staff_observed",
      customer_copy: "到店出示活动页领取伴手礼。",
      channels: [],
      content_version_id: "",
      dedup_basis: "contact",
      per_order_limit: false,
      reward: {
        kind: "coupon",
        slot: "first_publish",
        budget_mode: "fixed",
        currency: "CNY",
        store_ids: ["st_1"],
        claim_from: "2026-11-01T00:00:00Z",
        claim_until: "2026-12-31T23:59:59Z",
        redeem_from: "2026-11-01T00:00:00Z",
        redeem_until: "2027-01-31T23:59:59Z",
        onsite_redemption: false,
        staff_scope: "store",
        out_of_stock_rule: "stop",
        total_quantity: 100,
        cost_budget_minor: 0,
      },
    },
  });
  if (method === "GET" && root === "campaigns" && a && b === "rules") return sendJson(res, 200, { ruleset: {} });
  if (method === "GET" && root === "campaigns" && a && b === "content-packs") return sendJson(res, 200, list([]));
  if (method === "GET" && pathname === "/channel-admissions") return sendJson(res, 200, { records: [] });
  if (method === "GET" && root === "campaigns" && a && b === "links") return sendJson(res, 200, list([{ id: "lnk_1", code: "demo123", enabled: true }]));
  if (method === "GET" && root === "campaigns" && a && b === "lead-stats") return sendJson(res, 200, { submissions: 5, anonymous_views: 42 });

  // ---- 奖励台账（空事实：诚实空态，不冒充已发放）----
  if (root === "campaigns" && a && /^reward-(inventory|benefits|review-requests|external-issuances)$/.test(b || "")) {
    return sendJson(res, 200, list([]));
  }
  if (root === "campaigns" && a && b === "reward-summary") return sendJson(res, 200, { totals: { issued: 0, redeemed: 0, reserved: 0 } });
  if (method === "GET" && pathname === "/account-separation") return sendJson(res, 200, {
    package: { kind: "touch_subscription", status: "active", restricts_new_premium: false, locks_existing: false },
    ai_fees: { kind: "ai_tool_fee", quote_created: false, usage_created: false, includes_marketing_face: false, balance_mutated: false, personal_wallet_debited: false },
    rewards: { kind: "marketing_reward", ledger: "activity", appears_in_platform_wallet: false, face_as_cash_expense: false, items: [] },
  });
  if (root === "stores" && a && b === "deal-offers") return sendJson(res, 200, list([]));

  // ---- 触点 ----
  if (method === "GET" && pathname === "/touchpoints") return sendJson(res, 200, list(touchpoints));
  if (method === "GET" && pathname === "/touchpoint-deliveries") return sendJson(res, 200, list([]));
  if (method === "GET" && root === "touchpoints" && a && b === "qrcode") {
    res.writeHead(200, { "content-type": "image/png", "content-length": PNG_1X1.length });
    return res.end(PNG_1X1);
  }
  if (method === "GET" && pathname === "/touchpoints/export.csv") {
    res.writeHead(200, { "content-type": "text/csv; charset=utf-8" });
    return res.end("code,label,status\nTP-0001,门店主入口,active\n");
  }

  // ---- 账单 ----
  if (method === "GET" && pathname === "/billing/orders") return sendJson(res, 200, list([]));
  if (method === "GET" && root === "billing" && a === "orders" && b) return sendJson(res, 404, { error: "not_found" });
  if (method === "GET" && pathname === "/billing/subscription") return sendJson(res, 200, { state: "active" });

  // ---- 标签 ----
  if (method === "GET" && pathname === "/nfc/tags") return sendJson(res, 200, list(tags));

  // ---- 素材 ----
  if (root === "stores" && a && b === "content-packs") return sendJson(res, 200, list(contentPacks));
  if (root === "stores" && a && b === "content-media") return sendJson(res, 200, list([]));
  if (root === "stores" && a && b === "actions") return sendJson(res, 200, list([]));
  if (root === "stores" && a && b === "services") return sendJson(res, 200, list([]));
  if (method === "GET" && pathname === "/access/effective") {
    return sendJson(res, 200, {
      scopes: [],
      // 本地取证用 ops grant：仅 fixture 侧补发，不进入生产鉴权逻辑。
      ops_permissions: [
        { command: "ops:tenants.read" },
        { command: "ops:plans.read" },
        { command: "ops:orders.read" },
        { command: "ops:channels.read" },
        { command: "ops:templates.read" },
        { command: "ops:operations.read" },
      ],
    });
  }

  // ---- 平台运营控制台只读投影（本地取证；空事实不冒充已发放/已开通）----
  if (method === "GET" && root === "ops" && a === "orders") {
    return sendJson(res, 200, { orders: [], refunds: [], ai_usage: [], discrepancy_queue: [] });
  }
  if (method === "GET" && root === "ops" && a === "tenants" && !b) {
    return sendJson(res, 200, { tenants: [opsTenantRow] });
  }
  if (method === "GET" && root === "ops" && a === "tenants" && b) {
    return sendJson(res, 200, {
      tenant: opsTenantRow,
      ops_revision: opsTenantRow.ops_revision,
      subscription: {
        available: true,
        status: "active",
        paid_until: opsTenantRow.paid_until,
        retain_until: "",
        effective_status: "active",
        state: "active",
      },
      business_data: {
        available: true,
        state: "ok",
        message: "",
        store_count: opsTenantRow.store_count,
        member_count: opsTenantRow.member_count,
        stores: [],
        members: [],
      },
    });
  }
  if (method === "GET" && root === "ops" && a === "support-access") {
    return sendJson(res, 200, { support_access: [] });
  }
  if (method === "GET" && root === "ops" && a === "plans") {
    return sendJson(res, 200, { plans: [] });
  }
  if (method === "GET" && root === "ops" && a === "channels") {
    return sendJson(res, 200, { records: [] });
  }
  if (method === "GET" && root === "ops" && a === "templates") {
    return sendJson(res, 200, { templates: [], content_versions: [], assignments: [] });
  }
  if (method === "GET" && root === "ops" && a === "operations") {
    return sendJson(res, 200, {
      domain_health: { status: "degraded", targets: [] },
      hardware_fulfillment: { state: "unavailable", source: "billing", note: "本地取证：硬件履约投影不可用。", orders: [] },
      touchpoint_inventory: {},
      tickets: [],
      drills: [],
      recent_operations: [],
    });
  }
  if (method === "GET" && root === "ops" && a === "audit") {
    return sendJson(res, 200, { audit: [] });
  }

  // ---- 写端点：交互冒烟统一 200（非事实下发；页面按既有成功分支刷新）----
  if (method !== "GET" && method !== "HEAD") return sendJson(res, 200, { ok: true });

  // 未覆盖只读端点：诚实 404，不伪造成功。
  return sendJson(res, 404, { error: "not_found", message: `fixture: ${method} ${pathname} not covered` });
});

server.listen(PORT, HOST, () => {
  console.log(`dev-fixture-server listening on http://${HOST}:${PORT} (demo tenant ${TENANT_ID})`);
});
