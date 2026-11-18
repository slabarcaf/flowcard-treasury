// Command bindings provisions or disables verified, immutable source delegations.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/browser-gateway/internal/cli"
	"github.com/slabarcaf/flowcard-treasury/services/browser-gateway/internal/config"
)

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if len(os.Args) > 1 && os.Args[1] == "disable" {
		return cli.Bindings(ctx, os.Args[1:], os.Getenv("BROWSER_GATEWAY_ADMIN_DATABASE_URL"), config.Config{})
	}
	cfg, err := (config.Config{}).WithFinancial("", "", os.Getenv("BROWSER_GATEWAY_SERVICING_URL"), os.Getenv("BROWSER_GATEWAY_SERVICING_READ_SECRET"))
	if err != nil {
		return err
	}
	cfg, err = cfg.WithFinancialAccessFromEnv()
	if err != nil {
		return err
	}
	return cli.Bindings(ctx, os.Args[1:], os.Getenv("BROWSER_GATEWAY_ADMIN_DATABASE_URL"), cfg)
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "binding command failed (check target, verified actor and explicit source configuration)")
		os.Exit(1)
	}
	fmt.Println("binding command completed")
}
