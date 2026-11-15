CREATE TABLE browser_gateway.users (
    id uuid PRIMARY KEY,
    username text COLLATE "C" NOT NULL UNIQUE
        CHECK (username ~ '^[a-z0-9][a-z0-9._-]{2,63}$'),
    role text NOT NULL CHECK (role IN ('OPERATOR', 'CUSTOMER')),
    active boolean NOT NULL DEFAULT true,
    password_algorithm text NOT NULL CHECK (password_algorithm = 'pbkdf2-sha256'),
    password_version integer NOT NULL CHECK (password_version = 1),
    password_iterations integer NOT NULL CHECK (password_iterations = 600000),
    password_salt bytea NOT NULL CHECK (octet_length(password_salt) = 16),
    password_verifier bytea NOT NULL CHECK (octet_length(password_verifier) = 32),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE FUNCTION browser_gateway.keep_user_role() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.role IS DISTINCT FROM OLD.role THEN
        RAISE EXCEPTION 'user role is immutable';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER immutable_user_role BEFORE UPDATE ON browser_gateway.users
FOR EACH ROW EXECUTE FUNCTION browser_gateway.keep_user_role();
REVOKE ALL ON FUNCTION browser_gateway.keep_user_role() FROM PUBLIC;
CREATE TABLE browser_gateway.sessions (
    digest bytea PRIMARY KEY CHECK (octet_length(digest) = 32),
    user_id uuid NOT NULL REFERENCES browser_gateway.users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    CHECK (expires_at = created_at + interval '8 hours')
);
CREATE INDEX sessions_expiry ON browser_gateway.sessions(expires_at);
REVOKE ALL ON ALL TABLES IN SCHEMA browser_gateway FROM PUBLIC;
