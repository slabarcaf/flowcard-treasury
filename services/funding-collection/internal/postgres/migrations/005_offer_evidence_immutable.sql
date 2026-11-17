CREATE FUNCTION servicing.guard_offer_evidence_update() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    IF NEW.candidate_evidence IS DISTINCT FROM OLD.candidate_evidence
       OR NEW.source_refs IS DISTINCT FROM OLD.source_refs THEN
        RAISE EXCEPTION 'offer evidence is immutable';
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER offers_evidence_immutable BEFORE UPDATE ON servicing.offers
FOR EACH ROW EXECUTE FUNCTION servicing.guard_offer_evidence_update();
