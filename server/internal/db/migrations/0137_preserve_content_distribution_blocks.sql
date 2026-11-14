-- Adopting a replacement previously overwrote blocked with retired. A retained
-- block reason is authoritative: no unblock operation exists. Repair only
-- those rows; content, adoption pointers and the event ledger remain intact.
UPDATE store_content_pack_versions
SET status = 'blocked'
WHERE trim(blocked_reason) <> '' AND status <> 'blocked';

UPDATE content_pack_versions
SET status = 'blocked'
WHERE trim(blocked_reason) <> '' AND status <> 'blocked';
