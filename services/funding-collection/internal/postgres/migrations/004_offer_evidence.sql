ALTER TABLE servicing.offers ADD COLUMN candidate_evidence jsonb NOT NULL DEFAULT '{}'::jsonb
    CHECK (jsonb_typeof(candidate_evidence)='object');

CREATE UNIQUE INDEX offers_one_unterminated_payment ON servicing.offers
    (mode,canonical_account_id,transaction_id)
    WHERE state NOT IN ('SUPERSEDED','EXPIRED','TERMINATED');
