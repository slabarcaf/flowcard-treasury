package http

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/domain"
)

const maxCursorBytes = 2048

func decodeCursor(encoded, mode, customerID string) (*domain.EvaluationCursor, error) {
	if encoded == "" || len(encoded) > maxCursorBytes {
		return nil, errors.New("invalid_cursor")
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || !validJSONObjects(data) {
		return nil, errors.New("invalid_cursor")
	}
	var cursor domain.EvaluationCursor
	if decodeOneJSON(data, &cursor) != nil || cursor.Mode != mode || cursor.CustomerID != customerID ||
		cursor.CreatedAt.IsZero() || !domain.ValidID(cursor.ID) {
		return nil, errors.New("invalid_cursor")
	}
	canonical, err := json.Marshal(cursor)
	if err != nil || !bytes.Equal(canonical, data) {
		return nil, errors.New("invalid_cursor")
	}
	return &cursor, nil
}

func encodeCursor(cursor domain.EvaluationCursor) (string, error) {
	cursor.CreatedAt = cursor.CreatedAt.UTC()
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", errors.New("cursor_encoding_failed")
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
