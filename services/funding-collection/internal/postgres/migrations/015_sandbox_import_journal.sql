-- The privileged sandbox bootstrap records its immutable opening-book evidence
-- in the existing append-only journal. Runtime APIs still perform no DDL.
ALTER TABLE servicing.import_journal DROP CONSTRAINT import_journal_mode_check;
ALTER TABLE servicing.import_journal ADD CONSTRAINT import_journal_mode_check
    CHECK (mode IN ('DEMO','SANDBOX'));
