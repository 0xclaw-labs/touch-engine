#!/usr/bin/env node
// HUI-3531 Walk E 受信测试桩：统一账单报价权威 + 公共任务权威的外部系统替身。
// 票面认可的工程联验模式（外部系统在本地测试部署里以替身承载，桩只进测试入口；
// 生产部署换真服务，不换代码）。行为按 quote_ref 语义键驱动，供走查正负例：
//   quote_ref 含 "pending"  → pending（422 路径）
//   含 "mismatch"           → capability_mismatch（422 路径）
//   含 "consumed"           → already_consumed（409 路径）
//   其余                    → confirmed + amount_minor=9900
import { createServer } from "node:http";

const port = Number(process.argv[2] || process.env.SEAMS_PORT || 18999);

const server = createServer((req, res) => {
  let raw = "";
  req.on("data", (c) => (raw += c));
  req.on("end", () => {
    let body = {};
    try {
      body = JSON.parse(raw || "{}");
    } catch {
      body = {};
    }
    const send = (code, obj) => {
      res.writeHead(code, { "content-type": "application/json" });
      res.end(JSON.stringify(obj));
    };
    if (req.method === "POST" && req.url === "/internal/v1/billing/unified/quote") {
      const ref = String(body.quote_ref || "");
      if (ref.includes("pending")) {
        return send(200, { quote: { quote_ref: ref, status: "pending", amount_minor: 9900, currency: "CNY" } });
      }
      if (ref.includes("mismatch")) {
        return send(200, { quote: { quote_ref: ref, status: "confirmed", amount_minor: 9900, currency: "CNY", capability: "other_capability" } });
      }
      if (ref.includes("consumed")) {
        return send(200, { quote: { quote_ref: ref, status: "confirmed", amount_minor: 9900, currency: "CNY", capability: "content_production", consumed: true } });
      }
      return send(200, {
        quote: { quote_ref: ref, status: "confirmed", amount_minor: 9900, currency: "CNY", capability: "content_production", consumed: false },
      });
    }
    if (req.method === "POST" && req.url === "/internal/v1/tasks") {
      // 客户端验收契约=task_id（production.go TaskSubmitter）。
      return send(200, { task_id: `task_stub_${Date.now().toString(36)}`, status: "submitted" });
    }
    if (req.url === "/healthz") {
      return send(200, { status: "ok" });
    }
    send(404, { error: "not_found" });
  });
});

server.listen(port, "127.0.0.1", () => console.log(`[seams-stub] listening on ${port}`));
