package main

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func gatewayRuntimeEnvironment(local localRuntime) map[string]string {
	return map[string]string{"DATABASE_URL": local.URLs["gateway"], "BROWSER_GATEWAY_HTTP_ADDR": "127.0.0.1:18094", "BROWSER_GATEWAY_ALLOWED_ORIGINS": "http://127.0.0.1:18073,http://127.0.0.1:18094", "BROWSER_GATEWAY_ALLOW_LOOPBACK_HTTP": "true", "BROWSER_GATEWAY_COORDINATOR_URL": "http://127.0.0.1:18090", "BROWSER_GATEWAY_COORDINATOR_READ_SECRET": local.Secrets["coordinator-reader"], "BROWSER_GATEWAY_SERVICING_URL": "http://127.0.0.1:18091", "BROWSER_GATEWAY_SERVICING_READ_SECRET": local.Secrets["servicing-reader"], "BROWSER_GATEWAY_FINANCIAL_WRITE_MODE": "SANDBOX", "BROWSER_GATEWAY_SERVICING_OPERATOR_SECRET": local.Secrets["operator"], "BROWSER_GATEWAY_SERVICING_CUSTOMER_SECRET": local.Secrets["customer"]}
}

func bindLocalUsers(ctx context.Context, manifest, runID, adminFile, databaseName string) error {
	var local localRuntime
	if err := readPrivateJSON(localRuntimePath(manifest), &local, 65536); err != nil {
		return err
	}
	if err := validateLocalProfile(local); err != nil {
		return err
	}
	if local.RunID != runID || local.DatabaseName != databaseName {
		return errors.New("sandbox_binding_target_mismatch")
	}
	root, err := findRepositoryRoot()
	if err != nil {
		return err
	}
	target, err := localAdminURL(adminFile)
	if err != nil {
		return err
	}
	target.Path = "/" + databaseName
	admin, err := pgxpool.New(ctx, target.String())
	if err != nil {
		return errors.New("sandbox_binding_admin_unavailable")
	}
	defer admin.Close()
	var actual, marker string
	if admin.QueryRow(ctx, `SELECT current_database(),COALESCE(shobj_description(oid,'pg_database'),'') FROM pg_database WHERE datname=current_database()`).Scan(&actual, &marker) != nil || actual != databaseName || marker != local.Marker {
		return errors.New("sandbox_binding_database_not_owned")
	}
	environment := gatewayRuntimeEnvironment(local)
	environment["BROWSER_GATEWAY_ADMIN_DATABASE_URL"] = target.String()
	for _, role := range []string{"operator", "customer"} {
		user := local.Users[role]
		if user.ID == "" {
			return errors.New("sandbox_invited_user_unverified")
		}
		var mode, actor, subject, source, facility, binding, actorType string
		var active bool
		err := admin.QueryRow(ctx, `SELECT mode,actor_id,subject,source_origin,facility_id,source_binding_id,actor_type,active FROM browser_gateway.servicing_bindings WHERE user_id=$1`, user.ID).Scan(&mode, &actor, &subject, &source, &facility, &binding, &actorType, &active)
		if err == nil {
			if mode != "SANDBOX" || actor != requestIDFor(runID, "actor-"+role) || subject != user.ID || source != "http://127.0.0.1:18091" || facility != local.FacilityID || binding != local.SourceBindingID || actorType != strings.ToUpper(role) || !active {
				return errors.New("sandbox_existing_binding_mismatch")
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return errors.New("sandbox_binding_lookup_failed")
		}
		if err := runLocalCommand(ctx, root, "browser-gateway", []string{"./cmd/bindings", "create", "--username", user.Username, "--subject", user.ID, "--database-name", databaseName}, environment, ""); err != nil {
			return err
		}
	}
	return nil
}
