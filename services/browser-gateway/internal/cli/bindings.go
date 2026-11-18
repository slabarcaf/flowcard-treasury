package cli

import (
	"context"
	"errors"

	"github.com/slabarcaf/flowcard-treasury/services/browser-gateway/internal/auth"
	"github.com/slabarcaf/flowcard-treasury/services/browser-gateway/internal/config"
	"github.com/slabarcaf/flowcard-treasury/services/browser-gateway/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/browser-gateway/internal/servicing"
)

// Bindings provisions/disables verified actor bindings. Expected
// database is checked before any write and no browser can call this operation.
func Bindings(ctx context.Context, args []string, adminURL string, cfg config.Config) error {
	if len(args) == 0 || (args[0] != "create" && args[0] != "disable") {
		return errors.New("expected create or disable")
	}
	operation := args[0]
	f := flags("bindings")
	database := f.String("database-name", "", "expected gateway database")
	username := f.String("username", "", "invited username")
	subject := f.String("subject", "", "existing servicing authentication subject")
	if err := f.Parse(args[1:]); err != nil || f.NArg() != 0 || !auth.ValidUsername(*username) {
		return errors.New("invalid binding arguments")
	}
	pool, err := postgres.Admin(ctx, adminURL, *database)
	if err != nil {
		return err
	}
	defer pool.Close()
	if operation == "disable" {
		result, err := pool.Exec(ctx, `UPDATE browser_gateway.servicing_bindings SET active=false WHERE user_id=(SELECT id FROM browser_gateway.users WHERE username=$1)`, *username)
		if err != nil || result.RowsAffected() != 1 {
			return postgres.ErrBinding
		}
		return nil
	}
	if (cfg.BindingMode() != "DEMO" && cfg.BindingMode() != "SANDBOX") || *subject == "" || len(*subject) > 128 {
		return postgres.ErrBinding
	}
	var u postgres.User
	if err := pool.QueryRow(ctx, `SELECT id::text,username,role FROM browser_gateway.users WHERE username=$1 AND active`, *username).Scan(&u.ID, &u.Username, &u.Role); err != nil {
		return postgres.ErrBinding
	}
	if cfg.WriteMode == "" && u.Role != "OPERATOR" {
		return postgres.ErrBinding
	}
	key := cfg.OperatorSecret
	if u.Role == "CUSTOMER" {
		key = cfg.CustomerSecret
	}
	actor, err := servicing.ReadActor(ctx, servicing.HTTPClient(), cfg.ServicingURL, key, *subject, nil)
	if err != nil || (actor.ActorType != u.Role || actor.Mode != cfg.BindingMode()) {
		return postgres.ErrBinding
	}
	return postgres.Bind(ctx, pool, u, cfg.ServicingURL.String(), *subject, actor)
}
