-- Explicit sandbox browser delegation still requires server-side configuration
-- and current actor/binding verification. Existing DEMO identities are unchanged.
ALTER TABLE servicing.actors DROP CONSTRAINT actors_mode_check;
ALTER TABLE servicing.actors ADD CONSTRAINT actors_mode_check
    CHECK (mode IN ('DEMO','SANDBOX'));
