// Command migrate applies browser gateway migrations to an explicitly named DB.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/browser-gateway/internal/cli"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := cli.Migrate(ctx, os.Args[1:], os.Getenv("BROWSER_GATEWAY_ADMIN_DATABASE_URL")); err != nil {
		fmt.Fprintln(os.Stderr, "browser gateway migration failed (check arguments, target and administrative access)")
		os.Exit(1)
	}
	fmt.Println("browser gateway migrations applied")
}
