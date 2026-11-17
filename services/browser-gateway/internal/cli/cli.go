// Package cli implements bounded, quiet administrative commands.
package cli

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"strings"

	"github.com/slabarcaf/flowcard-treasury/services/browser-gateway/internal/auth"
	"github.com/slabarcaf/flowcard-treasury/services/browser-gateway/internal/postgres"
)

// ReadPassword strips one terminal LF and its optional preceding CR only.
// All other whitespace, including a second newline, is part of the password.
func ReadPassword(r io.Reader) (string, error) {
	// Refuse terminal devices rather than allowing the terminal driver to echo.
	if f, ok := r.(*os.File); ok {
		info, err := f.Stat()
		if err != nil || info.Mode()&os.ModeCharDevice != 0 {
			return "", errors.New("password requires a pipe or private input file")
		}
	}
	data, err := io.ReadAll(io.LimitReader(r, auth.MaxPasswordBytes+3))
	if err != nil || len(data) > auth.MaxPasswordBytes+2 {
		return "", errors.New("invalid password input")
	}
	s := string(data)
	if strings.HasSuffix(s, "\n") {
		s = strings.TrimSuffix(s, "\n")
		s = strings.TrimSuffix(s, "\r")
	}
	if !auth.ValidPassword(s) {
		return "", errors.New("invalid password input")
	}
	return s, nil
}

func flags(name string) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	return f
}

// Migrate applies numbered migrations only on the explicitly named database.
func Migrate(ctx context.Context, args []string, adminURL string) error {
	f := flags("migrate")
	database := f.String("database-name", "", "expected database name (required)")
	if err := f.Parse(args); err != nil || f.NArg() != 0 {
		return errors.New("invalid migrate arguments")
	}
	pool, err := postgres.Admin(ctx, adminURL, *database)
	if err != nil {
		return err
	}
	defer pool.Close()
	return postgres.Migrate(ctx, pool, *database)
}

// Users creates or disables invited users. It never accepts passwords in flags.
func Users(ctx context.Context, args []string, adminURL string, stdin io.Reader) error {
	if len(args) == 0 || (args[0] != "create" && args[0] != "disable") {
		return errors.New("expected create or disable")
	}
	operation := args[0]
	f := flags("users")
	database := f.String("database-name", "", "expected database name (required)")
	username := f.String("username", "", "canonical username")
	var role *string
	var passwordStdin *bool
	if operation == "create" {
		role = f.String("role", "", "OPERATOR or CUSTOMER")
		passwordStdin = f.Bool("password-stdin", false, "read password from stdin")
	}
	if err := f.Parse(args[1:]); err != nil || f.NArg() != 0 || !auth.ValidUsername(*username) {
		return errors.New("invalid user arguments")
	}
	if operation == "create" && (!*passwordStdin || (*role != "OPERATOR" && *role != "CUSTOMER")) {
		return errors.New("role and password-stdin required")
	}
	pool, err := postgres.Admin(ctx, adminURL, *database)
	if err != nil {
		return err
	}
	defer pool.Close()
	if operation == "disable" {
		return postgres.DisableUser(ctx, pool, *username)
	}
	password, err := ReadPassword(stdin)
	if err != nil {
		return err
	}
	return postgres.CreateUser(ctx, pool, *username, *role, password)
}
