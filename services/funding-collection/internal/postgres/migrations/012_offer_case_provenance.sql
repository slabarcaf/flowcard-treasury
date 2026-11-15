CREATE OR REPLACE FUNCTION servicing.guard_offer_evidence_update() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    IF NEW.candidate_evidence IS DISTINCT FROM OLD.candidate_evidence
       OR NEW.source_refs IS DISTINCT FROM OLD.source_refs
       OR NEW.originating_case_id IS DISTINCT FROM OLD.originating_case_id THEN
        RAISE EXCEPTION 'offer evidence and case provenance are immutable';
    END IF;
    RETURN NEW;
END
$$;
