CREATE INDEX ledger_transactions_bridge_effective
    ON servicing.ledger_transactions(mode,bridge_id,effective_at,id)
    WHERE bridge_id IS NOT NULL;
