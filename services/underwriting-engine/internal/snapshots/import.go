// Package snapshots validates and publishes immutable underwriting inputs.
package snapshots

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"time"
	"unicode/utf8"

	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/domain"
)

const maxSnapshotBytes = 2_000_000

var (
	errInvalidSnapshot  = errors.New("invalid_snapshot")
	errSnapshotTooLarge = errors.New("snapshot_too_large")
)

// Publisher is the append-only persistence boundary for validated snapshots.
type Publisher interface {
	PublishCustomer(context.Context, domain.CustomerSnapshot, [32]byte) (domain.ImportResult, error)
	PublishFacility(context.Context, domain.FacilitySnapshot, [32]byte) (domain.ImportResult, error)
}

// ImportCustomer strictly decodes, validates, hashes and publishes one customer file.
func ImportCustomer(ctx context.Context, publisher Publisher, data []byte, mode string, now time.Time) (domain.ImportResult, error) {
	snapshot, digest, err := DecodeCustomer(data, mode, now)
	if err != nil {
		return domain.ImportResult{}, err
	}
	return publisher.PublishCustomer(ctx, snapshot, digest)
}

// ImportFacility strictly decodes, validates, hashes and publishes one facility file.
func ImportFacility(ctx context.Context, publisher Publisher, data []byte, mode, facilityID string, now time.Time) (domain.ImportResult, error) {
	snapshot, digest, err := DecodeFacility(data, mode, facilityID, now)
	if err != nil {
		return domain.ImportResult{}, err
	}
	return publisher.PublishFacility(ctx, snapshot, digest)
}

// DecodeCustomer rejects ambiguous JSON and returns the canonical typed content hash.
func DecodeCustomer(data []byte, mode string, now time.Time) (domain.CustomerSnapshot, [32]byte, error) {
	var snapshot domain.CustomerSnapshot
	if len(data) > maxSnapshotBytes {
		return snapshot, [32]byte{}, errSnapshotTooLarge
	}
	if !domain.ValidMode(mode) || !utf8.Valid(data) || rejectDuplicateKeys(data) != nil ||
		checkCustomerFields(data) != nil || decodeStrict(data, &snapshot) != nil {
		return snapshot, [32]byte{}, errInvalidSnapshot
	}
	normalizeCustomer(&snapshot)
	if snapshot.SourceMode != mode || domain.ValidateCustomer(snapshot, now) != nil {
		return domain.CustomerSnapshot{}, [32]byte{}, errInvalidSnapshot
	}
	canonical, err := json.Marshal(snapshot)
	if err != nil {
		return domain.CustomerSnapshot{}, [32]byte{}, errInvalidSnapshot
	}
	return snapshot, sha256.Sum256(canonical), nil
}

// DecodeFacility rejects ambiguous JSON and returns the canonical typed content hash.
func DecodeFacility(data []byte, mode, facilityID string, now time.Time) (domain.FacilitySnapshot, [32]byte, error) {
	var snapshot domain.FacilitySnapshot
	if len(data) > maxSnapshotBytes {
		return snapshot, [32]byte{}, errSnapshotTooLarge
	}
	if !domain.ValidMode(mode) || !utf8.Valid(data) || rejectDuplicateKeys(data) != nil ||
		checkFacilityFields(data) != nil || decodeStrict(data, &snapshot) != nil {
		return snapshot, [32]byte{}, errInvalidSnapshot
	}
	snapshot.CapturedAt = snapshot.CapturedAt.UTC()
	for i := range snapshot.SourceRefs {
		snapshot.SourceRefs[i].EffectiveAt = snapshot.SourceRefs[i].EffectiveAt.UTC()
	}
	if snapshot.SourceMode != mode || domain.ValidateFacility(snapshot, facilityID, now) != nil {
		return domain.FacilitySnapshot{}, [32]byte{}, errInvalidSnapshot
	}
	canonical, err := json.Marshal(snapshot)
	if err != nil {
		return domain.FacilitySnapshot{}, [32]byte{}, errInvalidSnapshot
	}
	return snapshot, sha256.Sum256(canonical), nil
}

func normalizeCustomer(snapshot *domain.CustomerSnapshot) {
	snapshot.CapturedAt = snapshot.CapturedAt.UTC()
	for i := range snapshot.SourceRefs {
		snapshot.SourceRefs[i].EffectiveAt = snapshot.SourceRefs[i].EffectiveAt.UTC()
	}
	for i := range snapshot.Receivables {
		snapshot.Receivables[i].SourceRef.EffectiveAt = snapshot.Receivables[i].SourceRef.EffectiveAt.UTC()
	}
	for i := range snapshot.Classifications {
		snapshot.Classifications[i].SourceRef.EffectiveAt = snapshot.Classifications[i].SourceRef.EffectiveAt.UTC()
	}
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errInvalidSnapshot
	}
	return nil
}

func rejectDuplicateKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := consumeValue(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errInvalidSnapshot
	}
	return nil
}

func consumeValue(decoder *json.Decoder, depth int) error {
	if depth > 64 {
		return errInvalidSnapshot
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	switch delimiter {
	case '{':
		keys := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errInvalidSnapshot
			}
			if _, exists := keys[key]; exists {
				return errInvalidSnapshot
			}
			keys[key] = struct{}{}
			if err := consumeValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errInvalidSnapshot
		}
	case '[':
		count := 0
		for decoder.More() {
			count++
			if count > domain.MaxSnapshotRecords {
				return errInvalidSnapshot
			}
			if err := consumeValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errInvalidSnapshot
		}
	default:
		return errInvalidSnapshot
	}
	return nil
}

func checkCustomerFields(data []byte) error {
	if requireObjectFields(data, "schema_version", "source_mode", "source_id", "source_version", "captured_at", "source_refs", "customer_id", "account_id", "card_id", "coverage", "standing", "history_window", "balance_history", "inflow_history", "bridges", "reservations", "receivables", "classifications") != nil ||
		requireNonNullObjectFields(data, "schema_version", "source_mode", "source_id", "source_version", "captured_at", "customer_id", "account_id", "card_id") != nil ||
		requireArrayFields(data, "source_refs", "system", "record_id", "version", "effective_at") != nil ||
		requireNonNullArrayFields(data, "source_refs", "system", "record_id", "version", "effective_at") != nil ||
		requireObjectPath(data, []string{"coverage"}, "standing", "balance_history", "inflow_history", "bridge_history", "lifetime_defaults", "reservations", "receivables", "classification") != nil ||
		requireNonNullObjectPath(data, []string{"coverage"}, "standing", "balance_history", "inflow_history", "bridge_history", "lifetime_defaults", "reservations", "receivables", "classification") != nil ||
		requireOptionalObjectFields(data, "standing", "relationship_started_on", "onboarding_complete", "active", "suspended", "provider_account_status") != nil ||
		requireStandingValues(data) != nil ||
		requireObjectPath(data, []string{"history_window"}, "window_start", "window_end") != nil ||
		requireNonNullObjectPath(data, []string{"history_window"}, "window_start", "window_end") != nil ||
		requireArrayFields(data, "balance_history", "date", "balance_cents") != nil ||
		requireNonNullArrayFields(data, "balance_history", "date", "balance_cents") != nil ||
		requireArrayFields(data, "inflow_history", "id", "date", "amount_cents", "kind", "payer_id") != nil ||
		requireNonNullArrayFields(data, "inflow_history", "id", "date", "amount_cents", "kind", "payer_id") != nil ||
		requireArrayFields(data, "bridges", "id", "transaction_id", "original_principal_cents", "principal_owed_cents", "fees_owed_cents", "status", "opened_on", "due_on", "closed_on", "defaulted_on", "repayment_outcome") != nil ||
		requireNonNullArrayFields(data, "bridges", "id", "transaction_id", "original_principal_cents", "principal_owed_cents", "fees_owed_cents", "status", "opened_on", "due_on") != nil ||
		requireArrayFields(data, "reservations", "id", "transaction_id", "principal_cents", "fee_cents", "status", "bridge_id") != nil ||
		requireNonNullArrayFields(data, "reservations", "id", "transaction_id", "principal_cents", "fee_cents", "status") != nil ||
		requireArrayFields(data, "receivables", "invoice_id", "payer_id", "currency", "eligible_unpaid_cents", "allocated_cents", "due_on", "due_date_verified", "allocation_complete", "source_ref") != nil ||
		requireNonNullArrayFields(data, "receivables", "invoice_id", "payer_id", "currency", "eligible_unpaid_cents", "allocated_cents", "due_date_verified", "allocation_complete", "source_ref") != nil ||
		requireArrayFields(data, "classifications", "transaction_id", "merchant_category", "source_ref") != nil ||
		requireNonNullArrayFields(data, "classifications", "transaction_id", "merchant_category", "source_ref") != nil {
		return errInvalidSnapshot
	}
	if requireNestedArrayObjects(data, "receivables", "source_ref", "system", "record_id", "version", "effective_at") != nil ||
		requireNonNullNestedArrayFields(data, "receivables", "source_ref", "system", "record_id", "version", "effective_at") != nil {
		return errInvalidSnapshot
	}
	if requireNestedArrayObjects(data, "classifications", "source_ref", "system", "record_id", "version", "effective_at") != nil ||
		requireNonNullNestedArrayFields(data, "classifications", "source_ref", "system", "record_id", "version", "effective_at") != nil {
		return errInvalidSnapshot
	}
	return nil
}

func checkFacilityFields(data []byte) error {
	if requireObjectFields(data, "schema_version", "source_mode", "source_id", "source_version", "captured_at", "source_refs", "facility_id", "currency", "facility_total_cents", "reserve_floor_bps", "funded_principal_cents", "reserved_principal_cents", "ledger_version", "ledger_complete") != nil {
		return errInvalidSnapshot
	}
	if requireNonNullObjectFields(data, "schema_version", "source_mode", "source_id", "source_version", "captured_at", "facility_id", "currency", "facility_total_cents", "reserve_floor_bps", "funded_principal_cents", "reserved_principal_cents", "ledger_version", "ledger_complete") != nil ||
		requireArrayFields(data, "source_refs", "system", "record_id", "version", "effective_at") != nil ||
		requireNonNullArrayFields(data, "source_refs", "system", "record_id", "version", "effective_at") != nil {
		return errInvalidSnapshot
	}
	return nil
}

func requireObjectFields(data []byte, fields ...string) error {
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil || object == nil {
		return errInvalidSnapshot
	}
	for _, field := range fields {
		if _, exists := object[field]; !exists {
			return errInvalidSnapshot
		}
	}
	return nil
}

func requireNonNullObjectFields(data []byte, fields ...string) error {
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil || object == nil {
		return errInvalidSnapshot
	}
	for _, field := range fields {
		value, exists := object[field]
		if !exists || isJSONNull(value) {
			return errInvalidSnapshot
		}
	}
	return nil
}

func requireObjectPath(data []byte, path []string, fields ...string) error {
	var value any
	if json.Unmarshal(data, &value) != nil {
		return errInvalidSnapshot
	}
	for _, segment := range path {
		object, ok := value.(map[string]any)
		if !ok {
			return errInvalidSnapshot
		}
		value, ok = object[segment]
		if !ok {
			return errInvalidSnapshot
		}
	}
	object, ok := value.(map[string]any)
	if !ok {
		return errInvalidSnapshot
	}
	for _, field := range fields {
		if _, exists := object[field]; !exists {
			return errInvalidSnapshot
		}
	}
	return nil
}

func requireNonNullObjectPath(data []byte, path []string, fields ...string) error {
	value := json.RawMessage(data)
	for _, segment := range path {
		var object map[string]json.RawMessage
		if json.Unmarshal(value, &object) != nil || object == nil {
			return errInvalidSnapshot
		}
		next, exists := object[segment]
		if !exists || isJSONNull(next) {
			return errInvalidSnapshot
		}
		value = next
	}
	return requireNonNullObjectFields(value, fields...)
}

func requireStandingValues(data []byte) error {
	var root struct {
		Coverage struct {
			Standing *bool `json:"standing"`
		} `json:"coverage"`
		Standing json.RawMessage `json:"standing"`
	}
	if json.Unmarshal(data, &root) != nil || root.Coverage.Standing == nil {
		return errInvalidSnapshot
	}
	standing := bytes.TrimSpace(root.Standing)
	if isJSONNull(standing) {
		if *root.Coverage.Standing {
			return errInvalidSnapshot
		}
		return nil
	}
	if !*root.Coverage.Standing {
		return nil
	}
	return requireNonNullObjectFields(standing, "relationship_started_on", "onboarding_complete", "active", "suspended", "provider_account_status")
}

func requireOptionalObjectFields(data []byte, field string, fields ...string) error {
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil {
		return errInvalidSnapshot
	}
	value := bytes.TrimSpace(root[field])
	if bytes.Equal(value, []byte("null")) {
		return nil
	}
	return requireObjectFields(value, fields...)
}

func requireArrayFields(data []byte, field string, fields ...string) error {
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil {
		return errInvalidSnapshot
	}
	var rows []json.RawMessage
	if json.Unmarshal(root[field], &rows) != nil || rows == nil || len(rows) > domain.MaxSnapshotRecords {
		return errInvalidSnapshot
	}
	for _, row := range rows {
		if err := requireObjectFields(row, fields...); err != nil {
			return err
		}
	}
	return nil
}

func requireNonNullArrayFields(data []byte, field string, fields ...string) error {
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil {
		return errInvalidSnapshot
	}
	var rows []json.RawMessage
	if json.Unmarshal(root[field], &rows) != nil || rows == nil || len(rows) > domain.MaxSnapshotRecords {
		return errInvalidSnapshot
	}
	for _, row := range rows {
		if err := requireNonNullObjectFields(row, fields...); err != nil {
			return err
		}
	}
	return nil
}

func requireNestedArrayObjects(data []byte, field string, pathAndFields ...string) error {
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil {
		return errInvalidSnapshot
	}
	var rows []json.RawMessage
	if json.Unmarshal(root[field], &rows) != nil || rows == nil {
		return errInvalidSnapshot
	}
	for _, row := range rows {
		var object map[string]json.RawMessage
		if json.Unmarshal(row, &object) != nil {
			return errInvalidSnapshot
		}
		var nested map[string]json.RawMessage
		if json.Unmarshal(object[pathAndFields[0]], &nested) != nil || nested == nil {
			return errInvalidSnapshot
		}
		for _, field := range pathAndFields[1:] {
			if _, exists := nested[field]; !exists {
				return errInvalidSnapshot
			}
		}
	}
	return nil
}

func requireNonNullNestedArrayFields(data []byte, field, nestedField string, fields ...string) error {
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil {
		return errInvalidSnapshot
	}
	var rows []json.RawMessage
	if json.Unmarshal(root[field], &rows) != nil || rows == nil || len(rows) > domain.MaxSnapshotRecords {
		return errInvalidSnapshot
	}
	for _, row := range rows {
		var object map[string]json.RawMessage
		if json.Unmarshal(row, &object) != nil || object == nil {
			return errInvalidSnapshot
		}
		nested, exists := object[nestedField]
		if !exists || isJSONNull(nested) || requireNonNullObjectFields(nested, fields...) != nil {
			return errInvalidSnapshot
		}
	}
	return nil
}

func isJSONNull(value []byte) bool {
	return bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}
