package postgres

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct{ pool *pgxpool.Pool }

func Connect(ctx context.Context, databaseURL string) (*Repository, error) {
	databaseURL = strings.TrimSpace(databaseURL)
	if databaseURL == "" {
		return nil, errors.New("database_configuration_invalid")
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, errors.New("database_configuration_invalid")
	}
	return &Repository{pool: pool}, nil
}

func (r *Repository) Close() { r.pool.Close() }

func (r *Repository) Ready(ctx context.Context) error {
	_, checksum, err := migrationSource(migrations[len(migrations)-1].name)
	if err != nil {
		return err
	}
	var name, recorded string
	if err := r.pool.QueryRow(ctx, `SELECT name, checksum FROM coordinator.schema_migrations WHERE version=$1`, migrations[len(migrations)-1].version).Scan(&name, &recorded); err != nil || name != migrations[len(migrations)-1].name || recorded != checksum {
		return errors.New("database_schema_unavailable")
	}
	var restricted bool
	if err := r.pool.QueryRow(ctx, `SELECT pg_has_role(current_user, 'coordinator_runtime', 'MEMBER')
		AND NOT (rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls)
		AND NOT has_schema_privilege(current_user, 'coordinator', 'CREATE')
		AND NOT has_database_privilege(current_user, current_database(), 'CREATE')
		AND has_table_privilege(current_user, 'coordinator.cases', 'SELECT,INSERT,UPDATE')
		AND NOT has_table_privilege(current_user, 'coordinator.cases', 'DELETE,TRUNCATE,REFERENCES,TRIGGER')
		FROM pg_roles WHERE rolname=current_user`).Scan(&restricted); err != nil || !restricted {
		return errors.New("database_runtime_privileges_invalid")
	}
	return nil
}
