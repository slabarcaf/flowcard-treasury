package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/browser-gateway/internal/servicing"
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
		if session.User.Role == "CUSTOMER" && (a.config.WriteMode == "DEMO" || a.config.WriteMode == "SANDBOX") && !strings.HasPrefix(r.Pattern, "GET /api/coordinator/") && r.Pattern != "GET /api/servicing/changes" {
			binding, key, err := a.bound(ctx, session.User)
			if err != nil {
				bindingFailure(w, err)
				return
			}
			token, err := servicing.Assertion(key, binding.Subject, &binding.Restriction)
			if err != nil {
				fail(w, 503, "servicing unavailable")
				return
			}
			a.forward(ctx, w, r, a.config.ServicingURL, token, 0, 2*1024*1024)
			return
		}
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
