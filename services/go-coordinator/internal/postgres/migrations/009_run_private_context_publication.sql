-- Separate private pinned linkage from the model-visible packet and publication.
ALTER TABLE coordinator.runs
    ADD COLUMN private_context_refs jsonb,
    ADD COLUMN publication jsonb,
    ADD CONSTRAINT runs_private_context_object CHECK (
        private_context_refs IS NULL OR jsonb_typeof(private_context_refs) = 'object'
    ),
    ADD CONSTRAINT runs_publication_object CHECK (
        publication IS NULL OR jsonb_typeof(publication) = 'object'
    );

UPDATE coordinator.runs
SET publication = disposition::jsonb, disposition = NULL
WHERE disposition IS NOT NULL
  AND (disposition::jsonb ? 'explanation_mode'
       OR disposition::jsonb ? 'assessment_id'
       OR disposition::jsonb->>'kind' = 'SUPERSEDED');

ALTER TABLE coordinator.assessments
    ADD COLUMN source_transaction_id text,
    ADD COLUMN source_card_id text,
    ADD CONSTRAINT assessments_source_transaction_nonempty CHECK (
        source_transaction_id IS NULL OR length(btrim(source_transaction_id)) > 0
    ),
    ADD CONSTRAINT assessments_source_card_nonempty CHECK (
        source_card_id IS NULL OR length(btrim(source_card_id)) > 0
    );
