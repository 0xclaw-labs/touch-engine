-- NULL keeps existing legacy public behavior; a frozen [] starts an unpublished workspace.
ALTER TABLE store_action_configs ADD COLUMN public_baseline TEXT DEFAULT NULL;
-- A session's preview choices are immutable publication authority, not the latest adoption pool.
ALTER TABLE action_sessions ADD COLUMN content_candidates TEXT DEFAULT NULL;
