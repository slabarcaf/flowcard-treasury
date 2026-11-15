package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// No financial command or broad prefix is registered, even for operators.
var financialRoutes = []string{
	"GET /api/coordinator/cases", "GET /api/coordinator/cases/{id}",
	"GET /api/servicing/changes", "GET /api/offers/{id}",
	"GET /api/bridges/{id}", "GET /api/bridges/{id}/collections",
	"GET /api/operations/{id}",
}

func (a *Server) financial(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	session, err := a.store.Session(ctx, sessionToken(r))
	if err != nil {
		authFailure(w, err, "unauthorized")
		return
	}
	if session.User.Role != "OPERATOR" {
		fail(w, 403, "forbidden")
		return
	}
	base, secret, audience := a.config.ServicingURL, a.config.ServicingSecret, "flowcard-funding-collection"
	if strings.HasPrefix(r.Pattern, "GET /api/coordinator/") {
		base, secret, audience = a.config.CoordinatorURL, a.config.CoordinatorSecret, "flowcard-coordinator-service"
	}
	if base == nil {
		fail(w, 503, "unavailable")
		return
	}
	// Existing upstreams bind this dedicated, non-actor reader subject. Browser
	// UUIDs and customer/actor registry bindings are never synthesized here.
	token, err := serviceAssertion(secret, "flowcard-go", audience, "coordinator-reader")
	if err != nil {
		fail(w, 503, "unavailable")
		return
	}
	a.forward(ctx, w, r, base, token, 0, 2*1024*1024)
}
