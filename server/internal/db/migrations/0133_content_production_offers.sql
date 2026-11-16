-- HUI-4657: recovery metadata for authoritative offers, not a billing ledger.
CREATE TABLE IF NOT EXISTS content_production_offers (
 tenant_id TEXT NOT NULL REFERENCES tenants(id),
 quote_ref TEXT NOT NULL,
 offer_token TEXT NOT NULL,
 hold_id TEXT NOT NULL DEFAULT '',
 PRIMARY KEY (tenant_id, quote_ref)
);
