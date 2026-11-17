// Package httpapi exposes only the browser session and health endpoints.
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/slabarcaf/flowcard-treasury/services/browser-gateway/internal/auth"
	"github.com/slabarcaf/flowcard-treasury/services/browser-gateway/internal/config"
	"github.com/slabarcaf/flowcard-treasury/services/browser-gateway/internal/postgres"
)

const cookieName = "flowcard_session"

// Server owns bounded login work and the restricted database adapter.
type Server struct {
	store    *postgres.Store
	config   config.Config
	limiter  limiter
	work     chan struct{}
	upstream *http.Client
}

// New builds a handler without starting a listener.
func New(c config.Config, s *postgres.Store) http.Handler {
	a := &Server{config: c, store: s, work: make(chan struct{}, 4), upstream: upstreamClient()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	// Retain the existing private ALB health path during the Go gateway cutover.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := s.Ready(ctx); err != nil {
			fail(w, 503, "unavailable")
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /api/session/login", a.login)
	mux.HandleFunc("GET /api/session/me", a.me)
	mux.HandleFunc("POST /api/session/logout", a.logout)
	mux.HandleFunc("GET /api/servicing/session", a.servicingSession)
	if c.WriteMode == "DEMO" || c.WriteMode == "SANDBOX" {
		for _, route := range []string{"POST /api/offers", "POST /api/offers/{id}/acceptances", "POST /api/offers/{id}/approvals", "POST /api/offers/{id}/fund", "POST /api/bridges/{id}/collection-reviews", "POST /api/operations/{id}/reconcile"} {
			mux.HandleFunc(route, a.financialCommand)
		}
		mux.HandleFunc("GET /api/financial/commands", a.financialCommands)
		mux.HandleFunc("GET /api/customer/offers", a.customerOffers)
	}
	for _, route := range onboardingRoutes {
		mux.HandleFunc(route, a.onboarding)
	}
	for _, route := range productReadRoutes {
		mux.HandleFunc(route, a.productRead)
	}
	for _, route := range financialRoutes {
		mux.HandleFunc(route, a.financial)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawPath != "" || strings.ContainsAny(r.URL.Path, "%\\") || path.Clean(r.URL.Path) != r.URL.Path {
			w.Header().Set("Cache-Control", "no-store")
			fail(w, 400, "invalid path")
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Add("Vary", "Origin")
			w.Header().Add("Vary", "Sec-Fetch-Site")
			w.Header().Add("Vary", "Referer")
			if !a.allowed(r) {
				fail(w, 403, "forbidden")
				return
			}
			// Exact origins permit credentialed local previews. No preflight or wildcard.
			if origin := r.Header.Get("Origin"); origin != "" {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func (a *Server) allowed(r *http.Request) bool {
	values, present := r.Header["Origin"]
	if present {
		return len(values) == 1 && a.config.Origins[values[0]]
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if r.Header.Get("Sec-Fetch-Site") != "same-origin" || len(r.Header.Values("Referer")) != 1 {
		return false
	}
	u, err := url.Parse(r.Header.Get("Referer"))
	if err != nil || u.User != nil || u.Opaque != "" || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return false
	}
	return a.config.Origins[u.Scheme+"://"+u.Host]
}

func fail(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message}) // Network write errors cannot be recovered here.
}

func respond(w http.ResponseWriter, session postgres.Session) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(session) // The connection may close after headers are sent.
}

// credentials parses only the two unique string keys and exactly one object.
func credentials(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 8*1024)
	data, err := io.ReadAll(r.Body)
	if err != nil || !utf8.Valid(data) {
		return "", "", false
	}
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return "", "", false
	}
	fields := map[string]string{}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return "", "", false
		}
		key, ok := token.(string)
		if !ok || (key != "username" && key != "password") {
			return "", "", false
		}
		if _, exists := fields[key]; exists {
			return "", "", false
		}
		// Decode into a token so JSON null cannot silently become an empty string.
		value, err := d.Token()
		if err != nil {
			return "", "", false
		}
		s, ok := value.(string)
		if !ok {
			return "", "", false
		}
		fields[key] = s
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') {
		return "", "", false
	}
	if _, err = d.Token(); err != io.EOF {
		return "", "", false
	}
	if len(fields) != 2 {
		return "", "", false
	}
	return fields["username"], fields["password"], true
}

func (a *Server) login(w http.ResponseWriter, r *http.Request) {
	username, password, ok := credentials(w, r)
	if !ok {
		fail(w, 400, "invalid request")
		return
	}
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		fail(w, 400, "invalid request")
		return
	}
	if !a.limiter.allow(username, peer, time.Now()) {
		limited(w)
		return
	}
	if !auth.ValidUsername(username) || !auth.ValidPassword(password) {
		fail(w, 401, "invalid credentials")
		return
	}
	select {
	case a.work <- struct{}{}:
		defer func() { <-a.work }()
	default:
		limited(w)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	u, err := a.store.Authenticate(ctx, username, password)
	if err != nil {
		authFailure(w, err, "invalid credentials")
		return
	}
	token, session, err := a.store.CreateSession(ctx, u)
	if err != nil {
		authFailure(w, err, "invalid credentials")
		return
	}
	a.cookie(w, token, session.ExpiresAt, 8*60*60)
	respond(w, session)
}

func limited(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "60")
	fail(w, 429, "too many attempts")
}

func authFailure(w http.ResponseWriter, err error, message string) {
	if errors.Is(err, auth.ErrCredentials) {
		fail(w, 401, message)
		return
	}
	fail(w, 503, "unavailable")
}

func sessionToken(r *http.Request) string {
	var token string
	count := 0
	for _, c := range r.Cookies() {
		if c.Name == cookieName {
			token = c.Value
			count++
		}
	}
	if count != 1 {
		return ""
	}
	return token
}

func (a *Server) me(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	session, err := a.store.Session(ctx, sessionToken(r))
	if err != nil {
		authFailure(w, err, "unauthorized")
		return
	}
	respond(w, session)
}

func (a *Server) logout(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8*1024))
	if err != nil || len(bytes.TrimSpace(data)) != 0 {
		fail(w, 400, "invalid request")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	token := sessionToken(r)
	if _, err := a.store.Session(ctx, token); err != nil {
		authFailure(w, err, "unauthorized")
		return
	}
	if err := a.store.Logout(ctx, token); err != nil {
		fail(w, 503, "unavailable")
		return
	}
	a.cookie(w, "", time.Unix(1, 0), -1)
	w.WriteHeader(http.StatusNoContent)
}

func (a *Server) cookie(w http.ResponseWriter, token string, expires time.Time, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/api", HttpOnly: true, Secure: a.config.SecureCookie, SameSite: http.SameSiteLaxMode, Expires: expires, MaxAge: maxAge})
}
