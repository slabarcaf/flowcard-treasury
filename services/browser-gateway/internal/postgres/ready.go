package postgres

import (
	"context"
	"errors"
)

// Ready refuses inherited administrative, ownership, DDL or excess table rights.
// This is deliberately stricter than merely proving that SELECT works.
func (s *Store) Ready(ctx context.Context) error {
	var safe bool
	err := s.Pool.QueryRow(ctx, `
SELECT current_user = session_user AND
 NOT EXISTS (
  SELECT 1 FROM pg_roles r WHERE pg_has_role(current_user,r.oid,'MEMBER')
  AND (r.rolsuper OR r.rolcreatedb OR r.rolcreaterole OR r.rolreplication OR r.rolbypassrls
   OR left(r.rolname,3)='pg_'))
 AND NOT EXISTS (
  SELECT 1 FROM pg_database d WHERE
  pg_has_role(current_user,d.datdba,'MEMBER')
   OR (d.datname=current_database() AND (
    has_database_privilege(current_user,d.oid,'CREATE')
    OR has_database_privilege(current_user,d.oid,'TEMP'))))
 AND NOT EXISTS (
  SELECT 1 FROM pg_namespace n WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema'
  AND (pg_has_role(current_user,n.nspowner,'MEMBER') OR has_schema_privilege(current_user,n.oid,'CREATE')))
 AND NOT EXISTS (
  SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
  WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema' AND c.relkind IN ('r','p','v','m','S','f')
  AND (pg_has_role(current_user,c.relowner,'MEMBER')
   OR (c.relkind='S' AND has_sequence_privilege(current_user,c.oid,'USAGE,SELECT,UPDATE'))
   OR (c.relkind<>'S' AND (
    has_table_privilege(current_user,c.oid,'UPDATE,TRUNCATE,REFERENCES,TRIGGER')
    OR has_any_column_privilege(current_user,c.oid,'UPDATE,REFERENCES')
    OR (CASE WHEN current_setting('server_version_num')::integer >= 170000
     THEN has_table_privilege(current_user,c.oid,'MAINTAIN') ELSE false END)
    OR (NOT(n.nspname='browser_gateway' AND c.relname='sessions') AND has_table_privilege(current_user,c.oid,'INSERT,DELETE'))
    OR (NOT(n.nspname='browser_gateway' AND c.relname='sessions') AND has_any_column_privilege(current_user,c.oid,'INSERT'))
    OR (NOT(n.nspname='browser_gateway' AND c.relname IN ('users','sessions')) AND has_any_column_privilege(current_user,c.oid,'SELECT'))))))
 AND has_schema_privilege(current_user,'browser_gateway','USAGE')
 AND has_table_privilege(current_user,'browser_gateway.users','SELECT')
 AND has_table_privilege(current_user,'browser_gateway.sessions','SELECT')
 AND has_table_privilege(current_user,'browser_gateway.sessions','INSERT')
 AND has_table_privilege(current_user,'browser_gateway.sessions','DELETE')
`).Scan(&safe)
	if err != nil || !safe {
		return errors.New("restricted database unavailable")
	}
	// Also verify that migrations supplied the expected runtime columns.
	rows, err := s.Pool.Query(ctx, `SELECT u.id,u.username,u.role,u.active,u.password_algorithm,u.password_version,u.password_iterations,u.password_salt,u.password_verifier,s.digest,s.user_id,s.created_at,s.expires_at FROM browser_gateway.users u LEFT JOIN browser_gateway.sessions s ON s.user_id=u.id LIMIT 0`)
	if err != nil {
		return errors.New("restricted database unavailable")
	}
	rows.Close()
	return nil
}
