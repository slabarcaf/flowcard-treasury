ALTER TABLE ai_evidence_extractions
  ADD COLUMN IF NOT EXISTS decision_allowed boolean NOT NULL DEFAULT false;

ALTER TABLE ai_evidence_extractions
  ADD COLUMN IF NOT EXISTS decision_block_reason text;
