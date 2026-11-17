package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrChangeCheckpointAhead = errors.New("change_checkpoint_ahead")

// BeginReadSnapshot starts a read-only snapshot for a composed servicing view.
func BeginReadSnapshot(ctx context.Context, pool *pgxpool.Pool) (pgx.Tx, error) {
	return pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
}

// ChangeRecord preserves the event's committed resource and parent versions.
type ChangeRecord struct {
	Sequence                                             int64
	ResourceType, ResourceID                             string
	ResourceVersion                                      int64
	ChangeType                                           string
	RecordedAt                                           time.Time
	Summary                                              []byte
	ParentBridgeID                                       *string
	ParentBridgeVersion                                  *int64
	CustomerID, EvaluationID, OriginatingCaseID, OfferID *string
}

// ReadChangeWindow reads a fixed ascending page bounded by the committed
// transactional per-mode counter, never by a sequence-generator watermark.
func ReadChangeWindow(ctx context.Context, tx pgx.Tx, mode string, after int64) (int64, []ChangeRecord, error) {
	var watermark int64
	if err := tx.QueryRow(ctx, `SELECT last_sequence FROM servicing.change_counters WHERE mode=$1`, mode).Scan(&watermark); err != nil {
		return 0, nil, errors.New("change_counter_read_failed")
	}
	if after > watermark {
		return watermark, nil, ErrChangeCheckpointAhead
	}
	rows, err := tx.Query(ctx, `SELECT c.sequence,c.resource_type,c.resource_id,c.resource_version,c.change_type,c.recorded_at,c.summary,c.parent_bridge_id::text,c.parent_bridge_version,
		COALESCE(o.customer_id,b.customer_id,ob.customer_id,op_bridge.customer_id),COALESCE(o.evaluation_id,b.evaluation_id,ob.evaluation_id,op_bridge.evaluation_id),COALESCE(NULLIF(o.originating_case_id,''),NULLIF(b.originating_case_id,''),NULLIF(ob.originating_case_id,''),NULLIF(op_bridge.originating_case_id,'')),
		COALESCE(CASE WHEN c.resource_type='OFFER' THEN c.resource_id WHEN c.resource_type='BRIDGE' THEN b.offer_id::text ELSE op_bridge.offer_id::text END,CASE WHEN c.resource_type='OPERATION' THEN op_bridge.offer_id::text END)
		FROM servicing.changes c
		LEFT JOIN servicing.offers o ON c.resource_type='OFFER' AND o.mode=c.mode AND o.id::text=c.resource_id
		LEFT JOIN servicing.bridges b ON c.resource_type='BRIDGE' AND b.mode=c.mode AND b.id::text=c.resource_id
		LEFT JOIN servicing.operations op ON c.resource_type='OPERATION' AND op.mode=c.mode AND op.id::text=c.resource_id
		LEFT JOIN servicing.bridges op_bridge ON op_bridge.mode=op.mode AND op_bridge.id=op.bridge_id
		LEFT JOIN servicing.offers ob ON c.resource_type='BRIDGE' AND ob.mode=b.mode AND ob.id=b.offer_id
		WHERE c.mode=$1 AND c.sequence>$2 AND c.sequence<=$3 ORDER BY c.sequence LIMIT 26`, mode, after, watermark)
	if err != nil {
		return 0, nil, errors.New("change_page_read_failed")
	}
	defer rows.Close()
	items := []ChangeRecord{}
	expected := after + 1
	for rows.Next() {
		var row ChangeRecord
		if err = rows.Scan(&row.Sequence, &row.ResourceType, &row.ResourceID, &row.ResourceVersion, &row.ChangeType, &row.RecordedAt, &row.Summary, &row.ParentBridgeID, &row.ParentBridgeVersion, &row.CustomerID, &row.EvaluationID, &row.OriginatingCaseID, &row.OfferID); err != nil {
			return 0, nil, errors.New("change_page_read_failed")
		}
		if row.Sequence != expected {
			return 0, nil, errors.New("change_sequence_gap")
		}
		expected++
		items = append(items, row)
	}
	if rows.Err() != nil {
		return 0, nil, errors.New("change_page_read_failed")
	}
	return watermark, items, nil
}
