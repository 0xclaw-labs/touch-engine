-- Keep request recovery on the original alias authority; old tag rows remain NULL.
ALTER TABLE touchpoint_aliases ADD COLUMN creation_request_key TEXT;
ALTER TABLE touchpoint_aliases ADD COLUMN creation_request_payload TEXT;
CREATE UNIQUE INDEX idx_touchpoint_creation_request ON touchpoint_aliases(tenant_id,creation_request_key) WHERE creation_request_key IS NOT NULL;
