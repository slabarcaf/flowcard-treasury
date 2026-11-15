// Package postgres owns only the browser_gateway schema and its restricted store.
package postgres

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/slabarcaf/flowcard-treasury/services/browser-gateway/internal/auth"
)

// ErrUnavailable identifies storage failures without exposing driver details.
var ErrUnavailable = errors.New("storage unavailable")

// User is the public identity. It contains no customer or financial authority.
type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

// Session is safe for authenticated HTTP responses.
type Session struct {
	User      User      `json:"user"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Store holds a restricted runtime pool.
type Store struct{ Pool *pgxpool.Pool }

// Open bounds runtime connections and connection establishment.
func Open(ctx context.Context, databaseURL string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, errors.New("invalid database configuration")
	}
	cfg.MaxConns = 4
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, errors.New("database unavailable")
	}
	return &Store{Pool: pool}, nil
}

// Authenticate derives a verifier even when the user does not exist or is disabled.
func (s *Store) Authenticate(ctx context.Context, username, password string) (User, error) {
	var u User
	var p auth.Password
	var active bool
	err := s.Pool.QueryRow(ctx, `SELECT id::text,username,role,active,password_algorithm,password_version,password_iterations,password_salt,password_verifier FROM browser_gateway.users WHERE username=$1`, username).Scan(&u.ID, &u.Username, &u.Role, &active, &p.Algorithm, &p.Version, &p.Iterations, &p.Salt, &p.Verifier)
	if !active {
		p = auth.Password{}
	}
	verified := auth.Verify(password, p)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUnavailable
	}
	if err != nil || !active || !verified {
		return User{}, auth.ErrCredentials
	}
	return u, nil
}

// CreateSession persists only the token digest and an absolute expiry.
func (s *Store) CreateSession(ctx context.Context, u User) (string, Session, error) {
	token, digest, err := auth.NewSession()
	if err != nil {
		return "", Session{}, err
	}
	session := Session{User: u}
	err = s.Pool.QueryRow(ctx, `INSERT INTO browser_gateway.sessions(digest,user_id,created_at,expires_at) SELECT $1,id,now(),now()+interval '8 hours' FROM browser_gateway.users WHERE id=$2 AND active RETURNING expires_at`, digest, u.ID).Scan(&session.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", Session{}, auth.ErrCredentials
	}
	if err != nil {
		return "", Session{}, ErrUnavailable
	}
	return token, session, nil
}

// Session joins current user state on every lookup, including after restart.
func (s *Store) Session(ctx context.Context, token string) (Session, error) {
	digest, ok := auth.SessionDigest(token)
	if !ok {
		return Session{}, auth.ErrCredentials
	}
	var session Session
	err := s.Pool.QueryRow(ctx, `SELECT u.id::text,u.username,u.role,s.expires_at FROM browser_gateway.sessions s JOIN browser_gateway.users u ON u.id=s.user_id WHERE s.digest=$1 AND s.expires_at>now() AND u.active`, digest).Scan(&session.User.ID, &session.User.Username, &session.User.Role, &session.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, auth.ErrCredentials
	}
	if err != nil {
		return Session{}, ErrUnavailable
	}
	return session, nil
}

// Logout deletes only the presented session.
func (s *Store) Logout(ctx context.Context, token string) error {
	digest, ok := auth.SessionDigest(token)
	if !ok {
		return auth.ErrCredentials
	}
	_, err := s.Pool.Exec(ctx, `DELETE FROM browser_gateway.sessions WHERE digest=$1`, digest)
	if err != nil {
		return ErrUnavailable
	}
	return nil
}

// CreateUser provisions an invited identity through an administrative connection.
func CreateUser(ctx context.Context, pool *pgxpool.Pool, username, role, password string) error {
	if !auth.ValidUsername(username) || (role != "OPERATOR" && role != "CUSTOMER") {
		return errors.New("invalid user")
	}
	p, err := auth.NewPassword(password)
	if err != nil {
		return err
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return err
	}
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	uuid := fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
	_, err = pool.Exec(ctx, `INSERT INTO browser_gateway.users(id,username,role,password_algorithm,password_version,password_iterations,password_salt,password_verifier) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, uuid, username, role, p.Algorithm, p.Version, p.Iterations, p.Salt, p.Verifier)
	return err
}

// DisableUser takes effect on all subsequent session lookups.
func DisableUser(ctx context.Context, pool *pgxpool.Pool, username string) error {
	if !auth.ValidUsername(username) {
		return errors.New("invalid user")
	}
	tag, err := pool.Exec(ctx, `UPDATE browser_gateway.users SET active=false WHERE username=$1`, username)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("user unavailable")
	}
	return nil
}
