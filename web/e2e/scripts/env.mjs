#!/usr/bin/env node
// HUI-2628 r2 E2E 环境编排（抄 guanlan-order/web/harness run.sh 模式的 node 版）。
// up: identity stub(18461) -> go server(18460, 临时库) -> provision -> HTTP seed -> next start(18462)
// down: 按 spawn 记录逆序回收并删临时库。UNKNOWN 不算绿由 run.sh 判定。
import { execFileSync, spawn } from "node:child_process";
import { mkdirSync, mkdtempSync, openSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const webDir = path.resolve(here, "..", "..");
const repoDir = path.resolve(webDir, "..");
const serverDir = path.join(repoDir, "server");
const evidenceDir = path.join(webDir, "e2e", "evidence");
const artDir = path.join(webDir, "e2e", "_art");

const PORTS = { go: 18460, identity: 18461, web: 18462 };
const GO_URL = `http://127.0.0.1:${PORTS.go}`;
const INTERNAL_TOKEN = "e2e-internal-token";
const EMAIL = "owner@e2e.test";
const PASSWORD = "e2e-pass-123";
const PRINCIPAL = "usr_e2e_owner";
const TENANT_NAME = "磁石科技";
// HUI-2222 多组织/无权限正向链：双成员 principal（A=staff、B=org_owner）与
// 零成员 principal。principal_id 必须与 identity-stub.mjs 的 email 派生规则一致。
const MULTI_EMAIL = "multi@e2e.test";
const MULTI_PASSWORD = "e2e-pass-multi";
const MULTI_PRINCIPAL = "usr_e2e_multi";
const TENANT_B_NAME = "连锁二号店";

const procs = [];
const state = { db: "", session: "", tenantId: "", urls: {} };

function spawnProc(name, cmd, args, opts = {}) {
  // 子进程输出落 _art/logs/（不挂管道）：up 进程退出后子进程仍在跑，
  // 文件日志是观测它们（尤其 go server）存活的唯一窗口。
  const logDir = path.join(artDir, "logs");
  mkdirSync(logDir, { recursive: true });
  const logFd = openSync(path.join(logDir, `${name}.log`), "a");
  const child = spawn(cmd, args, { stdio: ["ignore", logFd, logFd], ...opts });
  procs.push({ name, child });
  return child;
}

async function waitReady(url, label, tries = 120) {
  for (let i = 0; i < tries; i++) {
    try {
      const res = await fetch(url);
      if (res.ok || res.status === 404) return;
    } catch {
      /* not up yet */
    }
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error(`[env] ${label} not ready at ${url}`);
}

async function goApi(method, path, body, extra = {}) {
  const res = await fetch(GO_URL + path, {
    method,
    headers: {
      "x-internal-token": INTERNAL_TOKEN,
      ...(state.session ? { cookie: state.session } : {}),
      ...(state.tenantId ? { "x-tenant-id": state.tenantId } : {}),
      ...(body ? { "content-type": "application/json" } : {}),
      ...extra,
    },
    body: body ? JSON.stringify(body) : undefined,
  });
  const setCookie = res.headers.get("set-cookie");
  if (setCookie) state.session = setCookie.split(";")[0];
  let data = null;
  try {
    data = await res.json();
  } catch {
    /* empty body */
  }
  if (!res.ok) throw new Error(`[env] ${method} ${path} -> ${res.status} ${JSON.stringify(data)}`);
  return data;
}

function relDays(days) {
  return new Date(Date.now() + days * 86400_000).toISOString();
}

async function seed() {
  // 登录拿 session cookie（requireInternal + handleLogin）
  await fetch(`${GO_URL}/api/v1/auth/login`, {
    method: "POST",
    headers: { "x-internal-token": INTERNAL_TOKEN, "content-type": "application/json" },
    body: JSON.stringify({ email: EMAIL, password: PASSWORD }),
  }).then(async (res) => {
    if (!res.ok) throw new Error(`[env] seed login failed: ${res.status}`);
    const setCookie = res.headers.get("set-cookie");
    if (setCookie) state.session = setCookie.split(";")[0];
  });
  const who = await goApi("GET", "/api/v1/whoami");
  state.tenantId = who.tenant_id;
  if (!state.tenantId) throw new Error(`[env] whoami missing tenant_id: ${JSON.stringify(who)}`);

  // 门店 1 家
  const store = await goApi("POST", "/api/v1/stores", { name: "旗舰旗舰店", address: "北京市东城区" });

  // 活动 4 场：active 未过期 / expired / paused / ended，各配 1 条短码
  const mkCampaign = async (title, startsAt, endsAt) => {
    const c = await goApi("POST", "/api/v1/campaigns", {
      title,
      public_content: "到店有礼",
      starts_at: startsAt,
      ends_at: endsAt,
      store_id: store.id,
    });
    const link = await goApi("POST", `/api/v1/campaigns/${c.id}/links`, {});
    return { id: c.id, code: link.code };
  };
  const active = await mkCampaign("E2E 进行中活动", relDays(-1), relDays(2));
  const expired = await mkCampaign("E2E 过期活动", relDays(-10), relDays(-1));
  const paused = await mkCampaign("E2E 暂停活动", relDays(-1), relDays(2));
  const ended = await mkCampaign("E2E 结束活动", relDays(-10), relDays(-5));
  // 状态迁移表（server/internal/campaign/campaign.go）：draft→active|ended、active→paused|ended。
  // 新建活动是 draft，所以 paused/ended 也必须先转 active 再到目标态。
  await goApi("POST", `/api/v1/campaigns/${active.id}/status`, { status: "active" });
  await goApi("POST", `/api/v1/campaigns/${expired.id}/status`, { status: "active" });
  await goApi("POST", `/api/v1/campaigns/${paused.id}/status`, { status: "active" });
  await goApi("POST", `/api/v1/campaigns/${paused.id}/status`, { status: "paused" });
  await goApi("POST", `/api/v1/campaigns/${ended.id}/status`, { status: "ended" });

  // HUI-2628：用真实 lib_assets 行 + 真实 content-pack API 种一个已采用版本。
  // 平台 upload 在本机未开，登记端点不可用；直接写引用行等价于素材库已登记
  // 的 DB 事实（不是伪造接口结果），后续建包/采用仍走真实 HTTP 路由。
  const libAssetId = "ast_e2e_seed_0001";
  const now = new Date().toISOString();
  const sql = `INSERT INTO lib_assets
    (id,tenant_id,asset_ref,sha256,media_type,source,purpose,grant_ref,store_id,tags,candidate,created_by,created_at,updated_at)
    VALUES ('${libAssetId}','${state.tenantId}','e2e-media-1','${"c".repeat(64)}','image','merchant_upload','campaign_publish','grant-e2e','${store.id}','',0,'e2e','${now}','${now}');`;
  execFileSync("sqlite3", [state.dbPath, sql], { encoding: "utf8" });
  const pack = await goApi("POST", `/api/v1/campaigns/${active.id}/content-packs`, { title: "E2E 内容包" });
  const version = await goApi("POST", `/api/v1/content-packs/${pack.id}/versions`, {
    content_kind: "imagetext",
    image_text_asset_ids: [libAssetId],
    title: "E2E 已采用内容",
    body: "这是一条门店已采用、带 AI 标识的图文内容。",
    topics: ["e2e"],
    store_name: "旗舰旗舰店",
    price_note: "到店有礼",
    valid_note: "2026-12-31 前",
    platform_versions: [{ platform: "douyin", note: "竖版", source_version: "src-e2e" }],
    grant_refs: ["grant-e2e"],
    marking: { ad_required: true, incentive_disclosed: false, ai_generated: true },
    source: "merchant_upload",
    status: "candidate",
  });
  await goApi("POST", `/api/v1/content-packs/${pack.id}/versions/${version.id}/adopt`, { platform: "douyin" });

  // 留资表单（FEATURE_LEADS_CAPTURE=on 时可配；notice v1 与 leads.CurrentNotice 同源）
  await goApi("POST", `/api/v1/campaigns/${active.id}/lead-form`, { notice_version: "v1", marketing_optin_enabled: true });

  // tenantId：/admin 登录表单要求手填租户 ID（helpers.login 消费）；
  // activeId：E2E 06 spec 的 /work/campaigns/[id] 代表页需要（Task 12 配套）。
  // email/password/tenantName：owner 凭据唯一真实来源（helpers.login 只读这里，不再各自硬编码）。
  state.urls = {
    activeCode: active.code,
    expiredCode: expired.code,
    pausedCode: paused.code,
    endedCode: ended.code,
    storeId: store.id,
    activeId: active.id,
    tenantId: state.tenantId,
    email: EMAIL,
    password: PASSWORD,
    tenantName: TENANT_NAME,
    // HUI-2222：多组织/无权限场景凭据与第二租户（唯一真实来源，spec 只读这里）。
    multiEmail: MULTI_EMAIL,
    multiPassword: MULTI_PASSWORD,
    tenantBId: state.tenantBId ?? "",
    tenantBName: TENANT_B_NAME,
    contentPackId: pack.id,
    contentVersionId: version.id,
    libAssetId,
  };
  writeFileSync(path.join(evidenceDir, "seed.json"), JSON.stringify(state.urls, null, 2));
}

function provision(goBin, dbPath, goEnv) {
  // provision-tenant 把租户 id 打到 stdout（cmdProvisionTenant -> fmt.Println(id)）。
  // provision-member 必须拿真实 tenant id，所以先捕获解析，再建 member。
  const out = execFileSync(goBin, ["provision-tenant", "-db", dbPath, "-name", TENANT_NAME], {
    env: goEnv,
    encoding: "utf8",
  });
  const tenantId = out.split("\n").map((l) => l.trim()).find((l) => l.startsWith("tnt_"));
  if (!tenantId) {
    throw new Error(`[env] provision-tenant stdout 未解析出 tnt_* id: ${JSON.stringify(out)}`);
  }
  state.tenantId = tenantId;
  console.log(`[env] provisioned tenant ${tenantId}`);
  execFileSync(
    goBin,
    ["provision-member", "-db", dbPath, "-tenant", tenantId, "-principal", PRINCIPAL, "-role", "owner", "-name", "E2E 店长"],
    { env: goEnv },
  );

  // HUI-2222：第二租户 + 双成员 principal（A=staff、B=org_owner）。
  // B 不种任何活动/门店——切组织后「上一户数据不残留」才有真断言面。
  const outB = execFileSync(goBin, ["provision-tenant", "-db", dbPath, "-name", TENANT_B_NAME], {
    env: goEnv,
    encoding: "utf8",
  });
  const tenantBId = outB.split("\n").map((l) => l.trim()).find((l) => l.startsWith("tnt_"));
  if (!tenantBId) {
    throw new Error(`[env] provision second tenant stdout 未解析出 tnt_* id: ${JSON.stringify(outB)}`);
  }
  state.tenantBId = tenantBId;
  execFileSync(
    goBin,
    ["provision-member", "-db", dbPath, "-tenant", tenantId, "-principal", MULTI_PRINCIPAL, "-role", "staff", "-name", "E2E 店员"],
    { env: goEnv },
  );
  execFileSync(
    goBin,
    ["provision-member", "-db", dbPath, "-tenant", tenantBId, "-principal", MULTI_PRINCIPAL, "-role", "owner", "-name", "E2E 二号店主"],
    { env: goEnv },
  );
  console.log(`[env] provisioned second tenant ${tenantBId} + multi-org principal ${MULTI_PRINCIPAL}`);
}

async function up() {
  rmSync(artDir, { recursive: true, force: true });
  rmSync(evidenceDir, { recursive: true, force: true });
  mkdirSync(evidenceDir, { recursive: true });
  mkdirSync(artDir, { recursive: true });

  spawnProc("identity", process.execPath, [path.join(here, "identity-stub.mjs"), String(PORTS.identity)]);
  await waitReady(`http://127.0.0.1:${PORTS.identity}/healthz`, "identity stub");

  state.db = mkdtempSync(path.join(tmpdir(), "touch-e2e-"));
  const dbPath = path.join(state.db, "touch.db");
  state.dbPath = dbPath;
  const go = "go";
  const goEnv = {
    ...process.env,
    GOWORK: "off",
    TOUCH_HTTP_ADDR: `127.0.0.1:${PORTS.go}`,
    TOUCH_DB_PATH: dbPath,
    TOUCH_INTERNAL_TOKEN: INTERNAL_TOKEN,
    PLATFORM_IDENTITY_BASE_URL: `http://127.0.0.1:${PORTS.identity}`,
    PLATFORM_IDENTITY_TOKEN: "e2e-identity-token",
    FEATURE_LEADS_CAPTURE: "on",
    // FEATURE_LEADS_CAPTURE=on 的 Gate 要求（哑值即可：forwarder 是后台轮询，不阻塞请求）
    PLATFORM_NOTIFY_BASE_URL: `http://127.0.0.1:${PORTS.identity}`,
    PLATFORM_NOTIFY_TOKEN: "e2e-notify-token",
    LEADS_TARGET_APP_ID: "e2e-crm-app",
    LEADS_PHONE_PEPPER: "e2e-phone-pepper",
    FEATURE_DASHBOARD: "on",
    // HUI-1676：规则编辑器与发布兑奖配置（默认 off；本地测试部署开,启用裁决归 HUI-3531）
    FEATURE_CAMPAIGN_RULES: "on",
    // HUI-2222：本地测试部署开确认与核销入口（生产默认 off，启用裁决归 HUI-3531）
    FEATURE_SERVICE_DESK: "on",
    // HUI-2628：公共页能力门的真实开关。开着才跑参与/内容包/奖励摘要；
    // 关闭态另由单测覆盖，不在同一轮里混用假结果。
    FEATURE_CONTENT_PACKS: "on",
    // The content-pack seed needs the lib_assets table to exist. Registration
    // HTTP still requires platform upload (off here), so this does not fake an
    // upload result; the seed writes the same reference row the library owns.
    FEATURE_ASSET_LIB: "on",
    FEATURE_PARTICIPATION: "on",
    FEATURE_REWARD_OPS: "on",
  };

  execFileSync(go, ["build", "-o", path.join(artDir, "touch-server"), "./cmd/touch-server"], { cwd: serverDir, env: goEnv });
  provision(path.join(artDir, "touch-server"), dbPath, goEnv);
  spawnProc("go", path.join(artDir, "touch-server"), [], { env: goEnv });
  await waitReady(`${GO_URL}/healthz`, "go server");

  await seed();

  spawnProc("web", process.execPath, [path.join(webDir, "node_modules", "next", "dist", "bin", "next"), "start", "-p", String(PORTS.web)], {
    cwd: webDir,
    env: { ...process.env, TOUCH_SERVER_URL: GO_URL, TOUCH_INTERNAL_TOKEN: INTERNAL_TOKEN },
  });
  await waitReady(`http://127.0.0.1:${PORTS.web}/login`, "next start");

  writeFileSync(path.join(evidenceDir, "env.json"), JSON.stringify({ ports: PORTS, email: EMAIL, ...state.urls }, null, 2));
  console.log("[env] up complete");
}

function killPort(label, port) {
  // up/down 是两次独立进程调用：run.sh 的 down 是新进程，procs 记录为空，
  // 所以跨进程一律按端口回收（lsof 找监听 PID）。
  try {
    const out = execFileSync("lsof", ["-ti", `tcp:${port}`], { encoding: "utf8" });
    for (const pid of out.split("\n").map((l) => l.trim()).filter(Boolean)) {
      try {
        process.kill(Number(pid), "SIGTERM");
        console.log(`[env] stopped ${label} pid ${pid} (port ${port})`);
      } catch {
        /* already gone */
      }
    }
  } catch {
    /* nothing listening on this port */
  }
}

async function down() {
  for (const { name, child } of procs.reverse()) {
    try {
      child.kill("SIGTERM");
      console.log(`[env] stopped ${name}`);
    } catch {
      /* already gone */
    }
  }
  killPort("identity", PORTS.identity);
  killPort("go", PORTS.go);
  killPort("web", PORTS.web);
  // 给内核一点时间释放 TIME_WAIT，下一轮 bind 才不会 EADDRINUSE
  await new Promise((r) => setTimeout(r, 1500));
  if (state.db) {
    try {
      rmSync(state.db, { recursive: true, force: true });
    } catch {
      /* best effort */
    }
  }
}

const cmd = process.argv[2] || "";
if (cmd === "up") {
  up()
    .then(() => process.exit(0))
    .catch((err) => {
      console.error(String(err));
      down().finally(() => process.exit(1));
    });
} else if (cmd === "down") {
  down().finally(() => process.exit(0));
} else {
  console.error("usage: node env.mjs up|down");
  process.exit(2);
}
