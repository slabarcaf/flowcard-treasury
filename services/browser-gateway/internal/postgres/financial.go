package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/slabarcaf/flowcard-treasury/services/browser-gateway/internal/servicing"
)

var ErrBinding = errors.New("binding unavailable")
var ErrConflict = errors.New("command conflict")

// Binding is immutable, administrator-verified delegation, never browser input.
type Binding struct {
	ID      string
	UserID  string
	Origin  string
	Subject string
	servicing.Restriction
}

// Binding reads only current active bindings matching the immutable user role.
func (s *Store) Binding(ctx context.Context, u User) (Binding, error) {
	var b Binding
	err := s.Pool.QueryRow(ctx, `SELECT b.id::text,b.user_id::text,b.source_origin,b.subject,b.actor_id,b.actor_type,b.customer_id,b.mode,b.facility_id,b.source_binding_id FROM browser_gateway.servicing_bindings b JOIN browser_gateway.users u ON u.id=b.user_id AND u.active AND u.role=b.actor_type WHERE b.user_id=$1 AND b.active`, u.ID).Scan(&b.ID, &b.UserID, &b.Origin, &b.Subject, &b.ActorID, &b.ActorType, &b.CustomerID, &b.Mode, &b.FacilityID, &b.SourceBindingID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Binding{}, ErrBinding
	}
	if err != nil {
		return Binding{}, ErrUnavailable
	}
	return b, nil
}

// Bind provisions a verified immutable binding using an administrative pool.
func Bind(ctx context.Context, pool *pgxpool.Pool, u User, origin, subject string, a servicing.Actor) error {
	if u.Role != a.ActorType || !a.DelegationAllowed() {
		return ErrBinding
	}
	_, err := pool.Exec(ctx, `INSERT INTO browser_gateway.servicing_bindings(user_id,source_origin,subject,actor_id,actor_type,customer_id,mode,facility_id,source_binding_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, u.ID, origin, subject, a.ActorID, a.ActorType, a.CustomerID, a.Mode, a.FacilityID, a.SourceBindingID)
	return err
}

// Command is a durable canonical request and optional definitive receipt.
type Command struct {
	ID        string    `json:"id"`
	Path      string    `json:"path"`
	Key       string    `json:"idempotency_key"`
	Body      []byte    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
	Status    *int      `json:"status"`
	Response  []byte    `json:"-"`
	Customer  string    `json:"-"`
}

// PrepareCommand commits intent before any upstream write. Conflicting keys fail.
func (s *Store) PrepareCommand(ctx context.Context, b Binding, path, key, customer string, body []byte) (Command, error) {
	hash := sha256.Sum256(body)
	var id string
	err := s.Pool.QueryRow(ctx, `INSERT INTO browser_gateway.financial_commands(user_id,binding_id,resource_path,idempotency_key,request_body,request_hash,customer_id) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING RETURNING id::text`, b.UserID, b.ID, path, key, body, hash[:], customer).Scan(&id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Command{}, ErrUnavailable
	}
	if errors.Is(err, pgx.ErrNoRows) {
		var matches bool
		err = s.Pool.QueryRow(ctx, `SELECT id::text,request_hash=$5 FROM browser_gateway.financial_commands WHERE user_id=$1 AND binding_id=$2 AND resource_path=$3 AND idempotency_key=$4`, b.UserID, b.ID, path, key, hash[:]).Scan(&id, &matches)
		if err != nil {
			return Command{}, ErrUnavailable
		}
		if !matches {
			return Command{}, ErrConflict
		}
	}
	return s.Command(ctx, b, id)
}

// Command returns only an intent belonging to the current user and binding.
func (s *Store) Command(ctx context.Context, b Binding, id string) (Command, error) {
	var c Command
	err := s.Pool.QueryRow(ctx, `SELECT c.id::text,c.resource_path,c.idempotency_key,c.request_body,c.created_at,r.status,r.response_body,c.customer_id FROM browser_gateway.financial_commands c LEFT JOIN browser_gateway.financial_receipts r ON r.command_id=c.id WHERE c.user_id=$1 AND c.binding_id=$2 AND c.id=$3`, b.UserID, b.ID, id).Scan(&c.ID, &c.Path, &c.Key, &c.Body, &c.CreatedAt, &c.Status, &c.Response, &c.Customer)
	if errors.Is(err, pgx.ErrNoRows) {
		return Command{}, ErrBinding
	}
	if err != nil {
		return Command{}, ErrUnavailable
	}
	return c, nil
}

// FindCommand recovers an exact earlier request before current source checks.
func (s *Store) FindCommand(ctx context.Context, b Binding, path, key string, body []byte) (Command, bool, error) {
	var id string
	var matches bool
	hash := sha256.Sum256(body)
	err := s.Pool.QueryRow(ctx, `SELECT id::text,request_hash=$5 FROM browser_gateway.financial_commands WHERE user_id=$1 AND binding_id=$2 AND resource_path=$3 AND idempotency_key=$4`, b.UserID, b.ID, path, key, hash[:]).Scan(&id, &matches)
	if errors.Is(err, pgx.ErrNoRows) {
		return Command{}, false, nil
	}
	if err != nil {
		return Command{}, false, ErrUnavailable
	}
	if !matches {
		return Command{}, true, ErrConflict
	}
	c, err := s.Command(ctx, b, id)
	return c, true, err
}

// Commands lists bounded recovery entries for the same principal/binding only.
func (s *Store) Commands(ctx context.Context, b Binding) ([]Command, error) {
	rows, err := s.Pool.Query(ctx, `SELECT c.id::text,c.resource_path,c.idempotency_key,c.request_body,c.created_at,r.status,r.response_body FROM browser_gateway.financial_commands c LEFT JOIN browser_gateway.financial_receipts r ON r.command_id=c.id WHERE c.user_id=$1 AND c.binding_id=$2 ORDER BY (r.command_id IS NULL) DESC,c.created_at DESC,c.id DESC LIMIT 50`, b.UserID, b.ID)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer rows.Close()
	result := []Command{}
	for rows.Next() {
		var c Command
		if err := rows.Scan(&c.ID, &c.Path, &c.Key, &c.Body, &c.CreatedAt, &c.Status, &c.Response); err != nil {
			return nil, ErrUnavailable
		}
		result = append(result, c)
	}
	if rows.Err() != nil {
		return nil, ErrUnavailable
	}
	return result, nil
}

// Receipt atomically appends a definitive result and verified customer offer index.
func (s *Store) Receipt(ctx context.Context, b Binding, c Command, status int, body []byte, offerID, customer string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO browser_gateway.financial_receipts(command_id,status,response_body) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, c.ID, status, body); err != nil {
		return ErrUnavailable
	}
	if offerID != "" {
		_, err = tx.Exec(ctx, `INSERT INTO browser_gateway.customer_offers(source_origin,mode,offer_id,customer_id,command_id) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, b.Origin, b.Mode, offerID, customer, c.ID)
		if err != nil {
			return ErrUnavailable
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return ErrUnavailable
	}
	return nil
}

// CustomerOffers lists only IDs indexed from this verified customer's receipts.
func (s *Store) CustomerOffers(ctx context.Context, b Binding) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT offer_id::text FROM browser_gateway.customer_offers WHERE source_origin=$1 AND mode=$2 AND customer_id=$3 ORDER BY created_at DESC,offer_id DESC LIMIT 50`, b.Origin, b.Mode, b.CustomerID)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) != nil {
			return nil, ErrUnavailable
		}
		ids = append(ids, id)
	}
	if rows.Err() != nil {
		return nil, ErrUnavailable
	}
	return ids, nil
}
