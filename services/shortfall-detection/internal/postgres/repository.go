package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository accesses the dedicated schema through a restricted runtime login.
type Repository struct {
	pool *pgxpool.Pool
}

// Open connects and verifies readiness without running DDL.
func Open(ctx context.Context, databaseURL string) (*Repository, error) {
	r, err := Connect(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	if err := r.Ready(ctx); err != nil {
		r.Close()
		return nil, err
	}
	return r, nil
}

// Connect creates a pool without requiring readiness, allowing the API liveness
// endpoint to remain available through database outages. It executes no DDL.
func Connect(ctx context.Context, databaseURL string) (*Repository, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, errors.New("database_configuration_invalid")
	}
	return &Repository{pool: pool}, nil
}

// Close releases task-owned runtime connections.
func (r *Repository) Close() {
	r.pool.Close()
}

// Ready checks migration identity, table access, and runtime privilege boundaries.
func (r *Repository) Ready(ctx context.Context) error {
	_, checksum, err := migrationSource()
	if err != nil {
		return err
	}
	var name, recorded string
	if err := r.pool.QueryRow(ctx, `SELECT name, checksum
		FROM shortfall.schema_migrations WHERE version = 1`).Scan(&name, &recorded); err != nil {
		return errors.New("database_schema_unavailable")
	}
	if name != migrationName || recorded != checksum {
		return errors.New("database_schema_mismatch")
	}
	var restricted bool
	if err := r.pool.QueryRow(ctx, `SELECT
		pg_has_role(current_user, 'shortfall_runtime', 'MEMBER')
		AND NOT (rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls)
		AND NOT has_schema_privilege(current_user, 'shortfall', 'CREATE')
		AND NOT has_schema_privilege(current_user, 'public', 'CREATE')
		AND NOT has_database_privilege(current_user, current_database(), 'CREATE')
		AND NOT has_table_privilege(current_user, 'public.customer_roster',
			'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
		AND NOT has_table_privilege(current_user, 'public.issuing_card_bindings',
			'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
		AND NOT has_table_privilege(current_user, 'shortfall.detections', 'UPDATE,DELETE,TRUNCATE')
		AND NOT has_table_privilege(current_user, 'shortfall.inbox_conflicts', 'UPDATE,DELETE,TRUNCATE')
		AND NOT has_table_privilege(current_user, 'shortfall.inbox_detections', 'UPDATE,DELETE,TRUNCATE')
		AND NOT has_table_privilege(current_user, 'shortfall.webhook_inbox', 'DELETE,TRUNCATE')
		AND NOT has_table_privilege(current_user, 'shortfall.schema_migrations',
			'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
		FROM pg_roles WHERE rolname = current_user`).Scan(&restricted); err != nil || !restricted {
		return errors.New("database_runtime_privileges_invalid")
	}
	if _, err := r.pool.Exec(ctx, `SELECT 1 FROM shortfall.webhook_inbox,
		shortfall.inbox_conflicts, shortfall.detections, shortfall.inbox_detections,
		public.customer_roster, public.issuing_card_bindings LIMIT 0`); err != nil {
		return errors.New("database_tables_unavailable")
	}
	return nil
}
