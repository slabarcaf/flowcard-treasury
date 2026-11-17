package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Ready verifies migration identity and the privileges of both the effective
// and session roles before a runtime repository is used. It performs reads only.
func (r *Repository) Ready(ctx context.Context) error {
	if err := VerifyMigration(ctx, r.pool); err != nil {
		return err
	}
	var safe bool
	err := r.pool.QueryRow(ctx, `SELECT
		pg_has_role(current_user, 'servicing_runtime', 'MEMBER')
		AND NOT EXISTS (
			SELECT 1 FROM pg_roles
			WHERE rolname IN (current_user, session_user)
			AND (rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls)
		)
		AND NOT has_schema_privilege(current_user, 'servicing', 'CREATE')
		AND NOT has_schema_privilege(session_user, 'servicing', 'CREATE')
		AND NOT has_schema_privilege(current_user, 'public', 'CREATE')
		AND NOT has_schema_privilege(session_user, 'public', 'CREATE')
		AND NOT has_database_privilege(current_user, current_database(), 'CREATE')
		AND NOT has_database_privilege(session_user, current_database(), 'CREATE')
		AND NOT has_table_privilege(current_user, 'public.customer_roster',
			'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
		AND NOT has_table_privilege(current_user, 'public.issuing_card_bindings',
			'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
		AND NOT has_table_privilege(session_user, 'public.customer_roster',
			'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
		AND NOT has_table_privilege(session_user, 'public.issuing_card_bindings',
			'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
		AND NOT has_table_privilege(current_user, 'servicing.ledger_transactions', 'UPDATE,DELETE,TRUNCATE')
		AND NOT has_table_privilege(current_user, 'servicing.ledger_lines', 'UPDATE,DELETE,TRUNCATE')
		AND NOT has_table_privilege(current_user, 'servicing.provider_observations', 'UPDATE,DELETE,TRUNCATE')
		AND NOT has_table_privilege(current_user, 'servicing.accrual_days', 'UPDATE,DELETE,TRUNCATE')
		AND NOT has_table_privilege(current_user, 'servicing.actor_decisions', 'UPDATE,DELETE,TRUNCATE')
		AND NOT has_table_privilege(current_user, 'servicing.funding_checks', 'UPDATE,DELETE,TRUNCATE')
		AND NOT has_table_privilege(current_user, 'servicing.operation_journal', 'UPDATE,DELETE,TRUNCATE')
		AND NOT has_table_privilege(current_user, 'servicing.webhook_inbox', 'UPDATE,DELETE,TRUNCATE')
		AND NOT has_table_privilege(current_user, 'servicing.changes', 'UPDATE,DELETE,TRUNCATE')
		AND NOT has_table_privilege(session_user, 'servicing.ledger_transactions', 'UPDATE,DELETE,TRUNCATE')
		AND NOT has_table_privilege(session_user, 'servicing.ledger_lines', 'UPDATE,DELETE,TRUNCATE')
		AND NOT has_table_privilege(session_user, 'servicing.provider_observations', 'UPDATE,DELETE,TRUNCATE')
		AND NOT has_table_privilege(session_user, 'servicing.accrual_days', 'UPDATE,DELETE,TRUNCATE')
		AND NOT has_table_privilege(session_user, 'servicing.actor_decisions', 'UPDATE,DELETE,TRUNCATE')
		AND NOT has_table_privilege(session_user, 'servicing.funding_checks', 'UPDATE,DELETE,TRUNCATE')
		AND NOT has_table_privilege(session_user, 'servicing.operation_journal', 'UPDATE,DELETE,TRUNCATE')
		AND NOT has_table_privilege(session_user, 'servicing.webhook_inbox', 'UPDATE,DELETE,TRUNCATE')
		AND NOT has_table_privilege(session_user, 'servicing.changes', 'UPDATE,DELETE,TRUNCATE')
		AND has_table_privilege(current_user, 'servicing.facilities', 'SELECT')
		AND has_table_privilege(current_user, 'servicing.facilities', 'INSERT')
		AND has_table_privilege(current_user, 'servicing.customers', 'SELECT')
		AND has_table_privilege(current_user, 'servicing.customers', 'INSERT')
		AND has_table_privilege(current_user, 'servicing.funding_checks', 'SELECT')
		AND has_table_privilege(current_user, 'servicing.funding_checks', 'INSERT')
		AND has_table_privilege(session_user, 'servicing.funding_checks', 'SELECT')
		AND has_table_privilege(session_user, 'servicing.funding_checks', 'INSERT')
		AND has_table_privilege(current_user, 'servicing.operation_journal', 'SELECT')
		AND has_table_privilege(current_user, 'servicing.operation_journal', 'INSERT')
		AND has_table_privilege(session_user, 'servicing.operation_journal', 'SELECT')
		AND has_table_privilege(session_user, 'servicing.operation_journal', 'INSERT')
		AND has_table_privilege(current_user, 'servicing.webhook_inbox', 'SELECT')
		AND has_table_privilege(current_user, 'servicing.webhook_inbox', 'INSERT')
		AND has_table_privilege(session_user, 'servicing.webhook_inbox', 'SELECT')
		AND has_table_privilege(session_user, 'servicing.webhook_inbox', 'INSERT')
		AND has_table_privilege(current_user, 'servicing.webhook_jobs', 'SELECT')
		AND has_table_privilege(current_user, 'servicing.webhook_jobs', 'INSERT')
		AND has_table_privilege(session_user, 'servicing.webhook_jobs', 'SELECT')
		AND has_table_privilege(session_user, 'servicing.webhook_jobs', 'INSERT')
		AND has_column_privilege(current_user, 'servicing.webhook_jobs', 'state', 'UPDATE')
		AND has_column_privilege(session_user, 'servicing.webhook_jobs', 'state', 'UPDATE')
		AND has_column_privilege(current_user, 'servicing.deposits', 'status', 'UPDATE')
		AND has_column_privilege(session_user, 'servicing.deposits', 'status', 'UPDATE')
		AND has_column_privilege(current_user, 'servicing.bridges', 'collection_debt_version', 'UPDATE')
		AND has_column_privilege(session_user, 'servicing.bridges', 'collection_debt_version', 'UPDATE')
		AND has_column_privilege(current_user, 'servicing.collection_refund_payables', 'remaining_cents', 'UPDATE')
		AND has_column_privilege(current_user, 'servicing.collection_refund_payables', 'state', 'UPDATE')
		AND has_column_privilege(current_user, 'servicing.collection_refund_payables', 'resolved_at', 'UPDATE')
		AND has_column_privilege(session_user, 'servicing.collection_refund_payables', 'remaining_cents', 'UPDATE')
		AND has_column_privilege(session_user, 'servicing.collection_refund_payables', 'state', 'UPDATE')
		AND has_column_privilege(session_user, 'servicing.collection_refund_payables', 'resolved_at', 'UPDATE')
		AND has_column_privilege(current_user, 'servicing.operations', 'last_funding_check_id', 'UPDATE')
		AND has_column_privilege(session_user, 'servicing.operations', 'last_funding_check_id', 'UPDATE')
		AND has_function_privilege(current_user,
			'servicing.lock_verified_mapping(text,text,text)', 'EXECUTE')
		AND has_function_privilege(current_user,
			'servicing.append_change(text,text,text,bigint,uuid,bigint,text,jsonb)', 'EXECUTE')`).Scan(&safe)
	if err != nil || !safe {
		return errors.New("database_runtime_privileges_invalid")
	}
	var tables bool
	if err := r.pool.QueryRow(ctx, `SELECT
		to_regclass('servicing.facilities') IS NOT NULL
		AND to_regclass('servicing.customers') IS NOT NULL
		AND to_regclass('servicing.ledger_transactions') IS NOT NULL
		AND to_regclass('servicing.ledger_lines') IS NOT NULL
		AND to_regclass('servicing.changes') IS NOT NULL
		AND to_regclass('servicing.funding_checks') IS NOT NULL
		AND to_regclass('servicing.operation_journal') IS NOT NULL
		AND to_regclass('servicing.webhook_inbox') IS NOT NULL
		AND to_regclass('servicing.webhook_jobs') IS NOT NULL
		AND to_regclass('servicing.collection_plan_checks') IS NOT NULL
		AND to_regclass('servicing.collection_refund_payables') IS NOT NULL
		AND to_regclass('servicing.collection_reversals') IS NOT NULL
		AND to_regclass('servicing.collection_reversal_allocations') IS NOT NULL
		AND to_regclass('public.customer_roster') IS NOT NULL
		AND to_regclass('public.issuing_card_bindings') IS NOT NULL`).Scan(&tables); err != nil || !tables {
		return errors.New("database_tables_unavailable")
	}
	return nil
}

// ReadyRuntime verifies readiness using a pool created with the restricted URL.
func ReadyRuntime(ctx context.Context, pool *pgxpool.Pool) error {
	return NewRepository(pool).Ready(ctx)
}
