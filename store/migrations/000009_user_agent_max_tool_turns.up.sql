ALTER TABLE Agents_UserAgents
    ADD COLUMN IF NOT EXISTS MaxToolTurns INT NOT NULL DEFAULT 30;

-- Backfill: pre-existing rows had the implicit cap of 10 baked into the runner.
-- The product now treats unset (0) as "use server default" (currently 30), so we
-- intentionally do NOT reassert 10 — existing agents pick up the higher default
-- automatically and admins can lower it per-agent from the UI.
