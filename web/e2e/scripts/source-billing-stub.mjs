// Engineering fixture for public Billing source_reservation_v1.
// Never imported by product code. Quotes, reservations and charges stay distinct;
// this fixture does not perform real payments or claim production delivery.
import { createHash } from "node:crypto";

export function createSourceBillingFixture() {
  const usages = new Map();
  const keys = new Map();
  const counters = { usage: 0, quote: 0, hold: 0, charge: 0 };
  const digest = (value) => createHash("sha256").update(value).digest("hex").slice(0, 18);
  const scope = (body) => [body.app_id, body.payer_account_id, body.payer_user_id].join("|");
  return function handle(req, body, send) {
    const url = new URL(req.url, "http://fixture.invalid");
    const prefix = "/internal/v1/billing/unified";
    if (url.pathname === "/__e2e/billing-state") {
      send(200, { fixture: true, counters, usage_count: usages.size });
      return true;
    }
    if (!url.pathname.startsWith(prefix) || (url.pathname === prefix + "/quote" && !body.usage_id)) return false;
    if (req.headers["x-pilotseaview-internal-token"] !== "e2e-billing-token") {
      send(403, { error: "forbidden" }); return true;
    }
    if (req.method === "POST" && url.pathname === prefix + "/usage") {
      if (body.funding_version !== "source_reservation_v1" || !body.idempotency_key || !body.payer_account_id || !body.payer_user_id || !body.pricing_version) {
        send(400, { error: "invalid_source_usage" }); return true;
      }
      const key = scope(body) + "|" + body.idempotency_key;
      const payload = JSON.stringify(body);
      const previous = keys.get(key);
      if (previous && previous.payload !== payload) { send(409, { error: "idempotency_conflict" }); return true; }
      if (!previous) {
        const usage_id = "usage_fixture_" + digest(key);
        const row = { payload, scope: scope(body), usage_id, pricing_version: body.pricing_version };
        keys.set(key, row); usages.set(usage_id, row); counters.usage++;
      }
      send(200, { fact: { usage_id: keys.get(key).usage_id, status: "ingested" } }); return true;
    }
    const usageID = req.method === "GET" ? decodeURIComponent(url.pathname.slice((prefix + "/usage/").length)) : body.usage_id;
    const row = usages.get(usageID);
    const requestScope = req.method === "GET" ? scope(Object.fromEntries(url.searchParams)) : scope(body);
    if (!row || row.scope !== requestScope) { send(404, { error: "not_found" }); return true; }
    if (req.method === "POST" && url.pathname === prefix + "/quote") {
      if (!row.quote) {
        row.quote = { quote_id: "quote_fixture_" + digest(row.usage_id), usage_id: row.usage_id,
          amount_minor: 9900, expires_at_unix: Math.floor(Date.now() / 1000) + 600, pricing_version: row.pricing_version };
        counters.quote++;
      }
      send(200, { quote: row.quote }); return true;
    }
    if (req.method === "GET" && url.pathname.startsWith(prefix + "/usage/")) {
      send(200, { facts: { quote: row.quote ?? null, hold: row.hold ?? null } }); return true;
    }
    if (req.method === "POST" && url.pathname === prefix + "/hold") {
      if (!row.quote || body.quote_id !== row.quote.quote_id || body.expected_amount_minor !== row.quote.amount_minor) {
        send(409, { error: "billing_source_quote_changed" }); return true;
      }
      if (!row.hold && row.quote.expires_at_unix <= Math.floor(Date.now() / 1000)) {
        send(409, { error: "billing_source_quote_expired" }); return true;
      }
      if (!row.hold) {
        row.hold = { hold_id: "hold_fixture_" + digest(row.usage_id), usage_id: row.usage_id, amount_minor: row.quote.amount_minor, status: "open" };
        counters.hold++;
      }
      send(200, { hold: row.hold }); return true;
    }
    send(404, { error: "not_found" }); return true;
  };
}
