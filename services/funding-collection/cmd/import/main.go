// Command import publishes validated DEMO fixture facts into servicing-owned tables.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/config"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/postgres"
)

const maxImportBytes int64 = 2_000_000

func main() {
	kind := flag.String("kind", "", "facility|receivables|authority|actors")
	file := flag.String("file", "", "JSON import file")
	databaseName := flag.String("database-name", "", "expected owned funding database name")
	flag.Parse()
	if *kind == "" || *file == "" || flag.NArg() != 0 ||
		!config.ValidOwnedDatabaseName(*databaseName, "flowcard_funding_demo") {
		log.Print("import requires --kind, --file and a safe --database-name")
		os.Exit(2)
	}
	if err := run(*kind, *file, *databaseName); err != nil {
		log.Print("funding import rejected: " + err.Error())
		os.Exit(1)
	}
}

func run(kind, path, databaseName string) error {
	mode := strings.TrimSpace(os.Getenv("SERVICING_MODE"))
	facilityID := strings.TrimSpace(os.Getenv("SERVICING_FACILITY_ID"))
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if mode != "DEMO" {
		return errors.New("imports_demo_only")
	}
	if databaseURL == "" || facilityID == "" {
		return errors.New("import_configuration_incomplete")
	}
	input, err := os.Open(path)
	if err != nil {
		return errors.New("import_file_unavailable")
	}
	defer input.Close()
	raw, err := io.ReadAll(io.LimitReader(input, maxImportBytes+1))
	if err != nil || int64(len(raw)) > maxImportBytes {
		return errors.New("import_file_invalid_or_too_large")
	}
	if err := rejectDuplicateKeys(raw); err != nil {
		return err
	}
	if err := validateRequiredNumbers(raw, kind); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return errors.New("import_database_unavailable")
	}
	defer pool.Close()
	if err := postgres.VerifyDatabaseName(ctx, pool, databaseName); err != nil {
		return err
	}
	repo := postgres.NewRepository(pool)
	if err := repo.Ready(ctx); err != nil {
		return err
	}
	now := time.Now().UTC()
	var result postgres.ImportResult
	switch kind {
	case "facility":
		var value domain.FacilityImport
		if err := decodeStrict(raw, &value); err != nil {
			return err
		}
		result, err = repo.ImportFacility(ctx, value, facilityID, now)
	case "receivables":
		var value domain.ReceivablesImport
		if err := decodeStrict(raw, &value); err != nil {
			return err
		}
		result, err = repo.ImportReceivables(ctx, value, facilityID, now)
	case "actors":
		var value domain.ActorsImport
		if err := decodeStrict(raw, &value); err != nil {
			return err
		}
		result, err = repo.ImportActors(ctx, value, facilityID, now)
	case "authority":
		var value domain.AuthoritiesImport
		if err := decodeStrict(raw, &value); err != nil {
			return err
		}
		result, err = repo.ImportAuthorities(ctx, value, facilityID, now)
	default:
		return errors.New("import_kind_invalid")
	}
	if err != nil {
		return err
	}
	log.Printf("funding import accepted kind=%s records=%d replayed=%t", kind, result.Records, result.Replayed)
	return nil
}

func decodeStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("import_json_invalid")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("import_trailing_json")
	}
	return nil
}

func rejectDuplicateKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var walk func() error
	walk = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok || seen[key] {
					return errors.New("import_duplicate_json_key")
				}
				for i := 0; i < len(key); i++ {
					if key[i] >= 'A' && key[i] <= 'Z' {
						return errors.New("import_json_key_case_invalid")
					}
				}
				seen[key] = true
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return errors.New("import_json_invalid")
		}
	}
	if err := walk(); err != nil {
		return errors.New("import_json_invalid")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("import_trailing_json")
	}
	return nil
}

func validateRequiredNumbers(raw []byte, kind string) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return errors.New("import_json_invalid")
	}
	required := []string(nil)
	switch kind {
	case "facility":
		required = []string{"facility_total_cents", "reserve_floor_bps", "other_commitments_cents"}
	case "receivables":
		required = nil
	default:
		return nil
	}
	for _, key := range required {
		value, ok := root[key]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errors.New("import_required_number_missing")
		}
	}
	if kind == "receivables" {
		var rows []map[string]json.RawMessage
		if err := json.Unmarshal(root["receivables"], &rows); err != nil {
			return errors.New("import_json_invalid")
		}
		for _, row := range rows {
			value, ok := row["eligible_unpaid_cents"]
			if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return errors.New("import_required_number_missing")
			}
		}
	}
	return nil
}
