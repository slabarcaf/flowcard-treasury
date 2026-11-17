package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	runtimeStatementTimeout       = "8s"
	runtimeLockTimeout            = "2s"
	runtimeIdleTransactionTimeout = "15s"
)

// NewRuntimePool applies bounded server-side SQL and lock waits to runtime connections.
func NewRuntimePool(ctx context.Context, connectionString string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(connectionString)
	if err != nil {
		return nil, errors.New("database_configuration_invalid")
	}
	if config.ConnConfig.RuntimeParams == nil {
		config.ConnConfig.RuntimeParams = make(map[string]string)
	}
	config.ConnConfig.RuntimeParams["statement_timeout"] = runtimeStatementTimeout
	config.ConnConfig.RuntimeParams["lock_timeout"] = runtimeLockTimeout
	config.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = runtimeIdleTransactionTimeout
	config.ConnConfig.RuntimeParams["application_name"] = "funding-collection"
	config.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, errors.New("database_configuration_invalid")
	}
	return pool, nil
}
