package http

import (
	"net"
	"net/http"
	"net/url"
)

// readActor supplies only the authenticated principal's registry/installation
// metadata. No provider identifiers, credentials or customer financial data.
func (h *handler) readActor(w http.ResponseWriter, r *http.Request) {
	id, ok := h.auth(w, r, false)
	if !ok {
		return
	}
	if id.Role != "ACTOR" {
		writeError(w, 403, "FORBIDDEN", "Actor credentials required")
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, 400, "INVALID_QUERY", "Query parameters are not accepted")
		return
	}
	if id.ActorType == "CUSTOMER" {
		var mapped bool
		err := h.pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM servicing.customers c JOIN public.customer_roster r ON r.customer_id=c.id AND r.is_active WHERE c.mode=$1 AND c.id=$2)`, h.cfg.Mode, id.CustomerID).Scan(&mapped)
		if err != nil {
			writeError(w, 503, "STORAGE_UNAVAILABLE", "Actor verification unavailable")
			return
		}
		if !mapped {
			writeError(w, 403, "BINDING_CHANGED", "Customer mapping unavailable")
			return
		}
	}
	local := false
	if target, err := url.Parse(h.cfg.ProviderBaseURL); err == nil {
		host := target.Hostname()
		ip := net.ParseIP(host)
		local = target.User == nil && (target.Scheme == "http" || target.Scheme == "https") && (host == "localhost" || ip != nil && ip.IsLoopback())
	}
	writeJSON(w, 200, struct {
		ActorID         string `json:"actor_id"`
		ActorType       string `json:"actor_type"`
		CustomerID      string `json:"customer_id"`
		Mode            string `json:"mode"`
		FacilityID      string `json:"facility_id"`
		SourceBindingID string `json:"source_binding_id"`
		FixtureOnly     bool   `json:"fixture_only"`
	}{id.ActorID, id.ActorType, id.CustomerID, h.cfg.Mode, h.cfg.FacilityID, h.cfg.SourceBindingID, h.cfg.Mode == "DEMO" && h.cfg.ProviderMode == "MOCK_HTTP" && local})
}
