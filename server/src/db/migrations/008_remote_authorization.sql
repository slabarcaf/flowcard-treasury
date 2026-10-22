CREATE TABLE IF NOT EXISTS issuing_card_bindings (
  card_id text PRIMARY KEY CHECK (length(btrim(card_id)) > 0),
  customer_id text NOT NULL REFERENCES customer_roster(customer_id),
  airwallex_account_id text NOT NULL CHECK (length(btrim(airwallex_account_id)) > 0),
  bound_by text NOT NULL CHECK (length(btrim(bound_by)) > 0),
  binding_reason text NOT NULL CHECK (length(btrim(binding_reason)) > 0),
  bound_at timestamptz NOT NULL DEFAULT now(),
  is_active boolean NOT NULL DEFAULT true
);

CREATE TABLE IF NOT EXISTS issuing_card_binding_audit (
  id bigserial PRIMARY KEY,
  card_id text NOT NULL,
  customer_id text NOT NULL REFERENCES customer_roster(customer_id),
  airwallex_account_id text NOT NULL,
  action text NOT NULL CHECK (action IN ('bound', 'deactivated')),
  actor text NOT NULL CHECK (length(btrim(actor)) > 0),
  reason text NOT NULL CHECK (length(btrim(reason)) > 0),
  occurred_at timestamptz NOT NULL DEFAULT now()
);

CREATE OR REPLACE FUNCTION reject_issuing_card_binding_audit_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'issuing_card_binding_audit is immutable';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS issuing_card_binding_audit_immutable ON issuing_card_binding_audit;
CREATE TRIGGER issuing_card_binding_audit_immutable
  BEFORE UPDATE OR DELETE ON issuing_card_binding_audit
  FOR EACH ROW EXECUTE FUNCTION reject_issuing_card_binding_audit_mutation();

CREATE TABLE IF NOT EXISTS remote_authorization_requests (
  card_transaction_event_id text PRIMARY KEY CHECK (length(btrim(card_transaction_event_id)) > 0),
  nonce text NOT NULL UNIQUE CHECK (length(btrim(nonce)) > 0),
  card_transaction_id text NOT NULL CHECK (length(btrim(card_transaction_id)) > 0),
  card_transaction_lifecycle_id text NOT NULL CHECK (length(btrim(card_transaction_lifecycle_id)) > 0),
  account_id text NOT NULL CHECK (length(btrim(account_id)) > 0),
  card_id text NOT NULL CHECK (length(btrim(card_id)) > 0),
  customer_id text NOT NULL REFERENCES customer_roster(customer_id),
  amount_cents bigint NOT NULL CHECK (amount_cents > 0),
  currency text NOT NULL,
  transaction_type text NOT NULL,
  transaction_category text NOT NULL,
  merchant_category_code text NOT NULL,
  request_received_at timestamptz NOT NULL,
  decision_id uuid,
  response_status text CHECK (response_status IN ('AUTHORIZED', 'DECLINED')),
  status_reason text,
  CHECK (length(currency) <= 3),
  CHECK (merchant_category_code ~ '^[0-9]{4}$')
);

CREATE INDEX IF NOT EXISTS remote_authorization_customer_time
  ON remote_authorization_requests (customer_id, request_received_at DESC);
