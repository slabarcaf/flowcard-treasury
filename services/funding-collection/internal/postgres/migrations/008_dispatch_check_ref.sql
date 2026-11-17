ALTER TABLE servicing.operations
    ADD COLUMN last_funding_check_id uuid,
    ADD CONSTRAINT operations_last_funding_check_fk
        FOREIGN KEY (mode,last_funding_check_id)
        REFERENCES servicing.funding_checks(mode,id);

UPDATE servicing.operations o SET last_funding_check_id=(
    SELECT fc.id FROM servicing.funding_checks fc
    JOIN servicing.bridges b ON b.mode=fc.mode AND b.offer_id=fc.offer_id
    WHERE b.mode=o.mode AND b.id=o.bridge_id AND fc.passed
      AND fc.offer_revision=b.offer_revision AND fc.terms_hash=b.terms_hash
    ORDER BY fc.created_at DESC,fc.id DESC LIMIT 1
)
WHERE o.kind='FUND' AND o.last_funding_check_id IS NULL;

GRANT UPDATE (last_funding_check_id) ON servicing.operations TO servicing_runtime;
