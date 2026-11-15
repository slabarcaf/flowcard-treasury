-- Separate private upstream account identity from the funding-owned alias.
ALTER TABLE coordinator.cases ADD COLUMN raw_account_id text;

UPDATE coordinator.cases
SET raw_account_id = account_ref
WHERE kind = 'SHORTFALL';

UPDATE coordinator.cases
SET account_ref = 'customer:' || customer_id
WHERE kind = 'SHORTFALL' AND customer_id IS NOT NULL AND raw_account_id IS NOT NULL;

UPDATE coordinator.cases
SET account_ref = NULL
WHERE kind = 'SHORTFALL' AND customer_id IS NULL;

UPDATE coordinator.cases
SET status = 'HELD', next_stage = 'HELD', retry_at = NULL,
    held_reason = 'ACCOUNT_SCOPE_UNRESOLVED', lease_token = NULL,
    lease_expires_at = NULL, lease_stage = NULL
WHERE kind = 'SHORTFALL'
  AND (customer_id IS NULL OR raw_account_id IS NULL OR btrim(raw_account_id) = ''
       OR account_ref IS DISTINCT FROM ('customer:' || customer_id));

ALTER TABLE coordinator.cases ADD CONSTRAINT cases_shortfall_account_scope_check
    CHECK (kind <> 'SHORTFALL' OR status = 'HELD' OR
        (customer_id IS NOT NULL AND raw_account_id IS NOT NULL AND btrim(raw_account_id) <> ''
         AND account_ref IS NOT NULL AND account_ref = ('customer:' || customer_id)));
