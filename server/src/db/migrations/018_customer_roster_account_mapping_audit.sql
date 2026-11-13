CREATE TABLE IF NOT EXISTS customer_roster_account_mapping_audit (
  id bigserial PRIMARY KEY,
  customer_id text NOT NULL REFERENCES customer_roster(customer_id),
  old_connected_account_id text NOT NULL CHECK (length(btrim(old_connected_account_id)) > 0),
  new_connected_account_id text NOT NULL CHECK (length(btrim(new_connected_account_id)) > 0),
  actor text NOT NULL CHECK (length(btrim(actor)) > 0),
  reason text NOT NULL CHECK (length(btrim(reason)) > 0),
  changed_at timestamptz NOT NULL DEFAULT now(),
  CHECK (old_connected_account_id <> new_connected_account_id),
  CHECK (new_connected_account_id <> customer_id)
);

CREATE OR REPLACE FUNCTION reject_customer_roster_account_mapping_audit_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'customer_roster_account_mapping_audit is immutable';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS customer_roster_account_mapping_audit_immutable
  ON customer_roster_account_mapping_audit;
CREATE TRIGGER customer_roster_account_mapping_audit_immutable
  BEFORE UPDATE OR DELETE ON customer_roster_account_mapping_audit
  FOR EACH ROW EXECUTE FUNCTION reject_customer_roster_account_mapping_audit_mutation();
