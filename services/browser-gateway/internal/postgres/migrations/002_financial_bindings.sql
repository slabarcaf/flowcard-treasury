CREATE TABLE browser_gateway.servicing_bindings (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL UNIQUE REFERENCES browser_gateway.users(id),
    source_origin text NOT NULL,
    mode text NOT NULL CHECK (mode = 'DEMO'),
    actor_id text NOT NULL,
    actor_type text NOT NULL CHECK (actor_type IN ('OPERATOR','CUSTOMER')),
    customer_id text NOT NULL,
    subject text NOT NULL,
    facility_id text NOT NULL,
    source_binding_id text NOT NULL,
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(source_origin,mode,subject),
    UNIQUE(source_origin,mode,actor_id),
    CHECK ((actor_type='CUSTOMER')=(customer_id<>''))
);
CREATE FUNCTION browser_gateway.keep_servicing_binding() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND
       ROW(NEW.id,NEW.user_id,NEW.source_origin,NEW.mode,NEW.actor_id,NEW.actor_type,
           NEW.customer_id,NEW.subject,NEW.facility_id,NEW.source_binding_id,NEW.created_at)
       IS DISTINCT FROM
       ROW(OLD.id,OLD.user_id,OLD.source_origin,OLD.mode,OLD.actor_id,OLD.actor_type,
           OLD.customer_id,OLD.subject,OLD.facility_id,OLD.source_binding_id,OLD.created_at) THEN
        RAISE EXCEPTION 'servicing binding is immutable';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM browser_gateway.users WHERE id=NEW.user_id AND role=NEW.actor_type) THEN
        RAISE EXCEPTION 'binding role does not match user';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER immutable_servicing_binding BEFORE INSERT OR UPDATE
ON browser_gateway.servicing_bindings FOR EACH ROW
EXECUTE FUNCTION browser_gateway.keep_servicing_binding();
REVOKE ALL ON FUNCTION browser_gateway.keep_servicing_binding() FROM PUBLIC;

CREATE TABLE browser_gateway.financial_commands (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES browser_gateway.users(id),
    binding_id uuid NOT NULL REFERENCES browser_gateway.servicing_bindings(id),
    resource_path text NOT NULL,
    idempotency_key text NOT NULL CHECK (idempotency_key ~ '^[A-Za-z0-9._:-]{1,128}$'),
    request_body bytea NOT NULL CHECK (octet_length(request_body)<=16384),
    request_hash bytea NOT NULL CHECK (octet_length(request_hash)=32),
    customer_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(user_id,binding_id,resource_path,idempotency_key)
);
CREATE TABLE browser_gateway.financial_receipts (
    command_id uuid PRIMARY KEY REFERENCES browser_gateway.financial_commands(id),
    status integer NOT NULL CHECK (status BETWEEN 200 AND 499),
    response_body bytea NOT NULL CHECK (octet_length(response_body)<=65536),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE browser_gateway.customer_offers (
    source_origin text NOT NULL,
    mode text NOT NULL CHECK (mode='DEMO'),
    offer_id uuid NOT NULL,
    customer_id text NOT NULL,
    command_id uuid NOT NULL UNIQUE REFERENCES browser_gateway.financial_commands(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(source_origin,mode,offer_id)
);
CREATE INDEX customer_offers_scope ON browser_gateway.customer_offers(source_origin,mode,customer_id,created_at DESC,offer_id DESC);
REVOKE ALL ON ALL TABLES IN SCHEMA browser_gateway FROM PUBLIC;
