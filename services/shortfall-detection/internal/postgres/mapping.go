package postgres

import (
	"context"

	"github.com/slabarcaf/flowcard-treasury/services/shortfall-detection/internal/domain"
)

// ResolveMapping reads existing bindings by card, detecting account disagreement
// instead of hiding it behind an account filter. It never enrolls or changes data.
func (r *Repository) ResolveMapping(ctx context.Context, accountID, cardID string) (domain.Mapping, error) {
	if accountID == "" || cardID == "" {
		return domain.Mapping{}, &domain.Failure{Reason: "MAPPING_UNAVAILABLE"}
	}
	rows, err := r.pool.Query(ctx, `SELECT b.customer_id, b.airwallex_account_id,
		b.is_active, COALESCE(r.airwallex_connected_account_id, ''),
		COALESCE(r.is_active, false)
		FROM public.issuing_card_bindings b
		LEFT JOIN public.customer_roster r ON r.customer_id = b.customer_id
		WHERE b.card_id = $1 LIMIT 2`, cardID)
	if err != nil {
		return domain.Mapping{}, &domain.Failure{Reason: "MAPPING_DATABASE_UNAVAILABLE", Retryable: true}
	}
	defer rows.Close()
	var mapping domain.Mapping
	var bindingActive, rosterActive bool
	var rosterAccount string
	count := 0
	for rows.Next() {
		count++
		if err := rows.Scan(&mapping.CustomerID, &mapping.AccountID, &bindingActive,
			&rosterAccount, &rosterActive); err != nil {
			return domain.Mapping{}, &domain.Failure{Reason: "MAPPING_DATABASE_UNAVAILABLE", Retryable: true}
		}
	}
	if rows.Err() != nil {
		return domain.Mapping{}, &domain.Failure{Reason: "MAPPING_DATABASE_UNAVAILABLE", Retryable: true}
	}
	if count > 1 {
		return domain.Mapping{}, &domain.Failure{Reason: "MAPPING_CONFLICT"}
	}
	if count == 0 || !bindingActive || !rosterActive || mapping.CustomerID == "" {
		return domain.Mapping{}, &domain.Failure{Reason: "MAPPING_UNAVAILABLE"}
	}
	if mapping.AccountID != accountID || rosterAccount != accountID {
		return domain.Mapping{}, &domain.Failure{Reason: "MAPPING_CONFLICT"}
	}
	mapping.CardID = cardID
	return mapping, nil
}
