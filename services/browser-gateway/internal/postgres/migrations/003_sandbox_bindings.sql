-- Extend only the allowed source modes. All immutable identity, role matching,
-- runtime-grant and tenant/installation constraints remain in force.
ALTER TABLE browser_gateway.servicing_bindings
    DROP CONSTRAINT servicing_bindings_mode_check;
ALTER TABLE browser_gateway.servicing_bindings
    ADD CONSTRAINT servicing_bindings_mode_check
    CHECK (mode IN ('DEMO','SANDBOX'));
ALTER TABLE browser_gateway.customer_offers
    DROP CONSTRAINT customer_offers_mode_check;
ALTER TABLE browser_gateway.customer_offers
    ADD CONSTRAINT customer_offers_mode_check
    CHECK (mode IN ('DEMO','SANDBOX'));
