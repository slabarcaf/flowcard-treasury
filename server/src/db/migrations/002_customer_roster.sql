CREATE TABLE IF NOT EXISTS customer_roster (
  customer_id text PRIMARY KEY CHECK (length(btrim(customer_id)) > 0),
  salesforce_account_id text NOT NULL UNIQUE CHECK (length(btrim(salesforce_account_id)) > 0),
  airwallex_connected_account_id text NOT NULL UNIQUE CHECK (length(btrim(airwallex_connected_account_id)) > 0),
  is_active boolean NOT NULL DEFAULT true,
  enrolled_at timestamptz NOT NULL DEFAULT now(),
  enrolled_by text NOT NULL CHECK (length(btrim(enrolled_by)) > 0),
  enrollment_reason text NOT NULL CHECK (length(btrim(enrollment_reason)) > 0),
  deactivated_at timestamptz,
  deactivated_by text,
  deactivation_reason text,
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (
    (is_active AND deactivated_at IS NULL AND deactivated_by IS NULL AND deactivation_reason IS NULL)
    OR
    (NOT is_active AND deactivated_at IS NOT NULL AND deactivated_by IS NOT NULL
      AND deactivation_reason IS NOT NULL AND length(btrim(deactivated_by)) > 0
      AND length(btrim(deactivation_reason)) > 0)
  )
);

CREATE TABLE IF NOT EXISTS customer_roster_audit (
  id bigserial PRIMARY KEY,
  customer_id text NOT NULL REFERENCES customer_roster(customer_id),
  action text NOT NULL CHECK (action IN ('enrolled', 'deactivated')),
  actor text NOT NULL CHECK (length(btrim(actor)) > 0),
  reason text NOT NULL CHECK (length(btrim(reason)) > 0),
  occurred_at timestamptz NOT NULL DEFAULT now()
);

CREATE OR REPLACE FUNCTION reject_customer_roster_audit_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'customer_roster_audit is immutable';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS customer_roster_audit_immutable ON customer_roster_audit;
CREATE TRIGGER customer_roster_audit_immutable
  BEFORE UPDATE OR DELETE ON customer_roster_audit
  FOR EACH ROW EXECUTE FUNCTION reject_customer_roster_audit_mutation();

INSERT INTO customer_roster (
  customer_id, salesforce_account_id, airwallex_connected_account_id,
  enrolled_by, enrollment_reason
) VALUES
  ('acct_bloom001', 'demo-sf-bloom001', 'acct_bloom001', 'demo-seed', 'Synthetic Bloom demo customer'),
  ('acct_atlas001', 'demo-sf-atlas001', 'acct_atlas001', 'demo-seed', 'Synthetic Atlas demo customer'),
  ('acct_nova001', 'demo-sf-nova001', 'acct_nova001', 'demo-seed', 'Synthetic Nova demo customer'),
  ('acct_pixel001', 'demo-sf-pixel001', 'acct_pixel001', 'demo-seed', 'Synthetic Pixel demo customer')
ON CONFLICT (customer_id) DO NOTHING;

INSERT INTO customer_roster_audit (customer_id, action, actor, reason)
SELECT customer_id, 'enrolled', 'demo-seed', enrollment_reason
  FROM customer_roster
 WHERE customer_id IN ('acct_bloom001', 'acct_atlas001', 'acct_nova001', 'acct_pixel001')
   AND NOT EXISTS (
     SELECT 1 FROM customer_roster_audit AS audit
      WHERE audit.customer_id = customer_roster.customer_id AND audit.action = 'enrolled'
   );
