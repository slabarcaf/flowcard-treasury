// Command users provisions or disables invited browser gateway accounts.
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
	if err := cli.Users(ctx, os.Args[1:], os.Getenv("BROWSER_GATEWAY_ADMIN_DATABASE_URL"), os.Stdin); err != nil {
		fmt.Fprintln(os.Stderr, "browser gateway user command failed (check arguments, target, password bounds and administrative access)")
		os.Exit(1)
	}
	fmt.Println("browser gateway user command completed")
}
