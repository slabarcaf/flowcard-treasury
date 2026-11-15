// Command import-snapshot validates and publishes one immutable snapshot file.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/config"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/snapshots"
)

const maxSnapshotBytes = 2_000_000

func main() {
	kind := flag.String("kind", "", "snapshot kind: customer or facility")
	path := flag.String("file", "", "path to one snapshot JSON file")
	flag.Parse()
	if flag.NArg() != 0 || (*kind != "customer" && *kind != "facility") || *path == "" {
		log.Print("usage: import-snapshot --kind customer|facility --file PATH")
		os.Exit(2)
	}
	config, err := config.LoadSnapshotImport()
	if err != nil {
		log.Print(err)
		os.Exit(1)
	}
	file, err := os.Open(*path)
	if err != nil {
		log.Print("snapshot file unavailable")
		os.Exit(1)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSnapshotBytes+1))
	if err != nil {
		log.Print("snapshot file unavailable")
		os.Exit(1)
	}
	if len(data) > maxSnapshotBytes {
		log.Print("snapshot_too_large")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	store, err := postgres.ConnectSnapshotStore(ctx, config.DatabaseURL)
	if err != nil {
		log.Print(err)
		os.Exit(1)
	}
	defer store.Close()
	if err := store.Ready(ctx); err != nil {
		log.Print(err)
		os.Exit(1)
	}
	var result domain.ImportResult
	switch *kind {
	case "customer":
		result, err = snapshots.ImportCustomer(ctx, store, data, config.Mode, time.Now().UTC())
	case "facility":
		result, err = snapshots.ImportFacility(ctx, store, data, config.Mode, config.FacilityID, time.Now().UTC())
	}
	if err != nil {
		log.Print(err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		log.Print("snapshot result unavailable")
		os.Exit(1)
	}
}
