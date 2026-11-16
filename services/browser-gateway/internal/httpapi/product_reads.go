package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/browser-gateway/internal/servicing"
)

var productReadRoutes = []string{
	"GET /api/operator/portfolio",
	"GET /api/operator/customers",
	"GET /api/operator/customers/{id}",
	"GET /api/operator/customers/{id}/bridges",
	"GET /api/customer/bridges",
}

func (a *Server) productRead(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	session, err := a.store.Session(ctx, sessionToken(r))
	if err != nil {
		authFailure(w, err, "unauthorized")
		return
	}
	customerRoute := r.Pattern == "GET /api/customer/bridges"
	if (customerRoute && session.User.Role != "CUSTOMER") || (!customerRoute && session.User.Role != "OPERATOR") {
		fail(w, 403, "forbidden")
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		fail(w, 400, "invalid query")
		return
	}
	allowsCursor := strings.HasSuffix(r.Pattern, "/bridges") || r.Pattern == "GET /api/operator/customers"
	for k, v := range q {
		if !allowsCursor || k != "cursor" || len(v) != 1 || v[0] == "" || len(v[0]) > 2048 {
			fail(w, 400, "invalid query")
			return
		}
	}
	binding, key, err := a.bound(ctx, session.User)
	if err != nil {
		bindingFailure(w, err)
		return
	}
	target := *a.config.ServicingURL
	switch r.Pattern {
	case "GET /api/operator/portfolio":
		target.Path = "/api/servicing/portfolio"
	case "GET /api/operator/customers":
		target.Path = "/api/servicing/customers"
	case "GET /api/customer/bridges":
		target.Path = "/api/bridges"
	default:
		id := r.PathValue("id")
		if id == "" || len(id) > 128 || strings.TrimSpace(id) != id || strings.ContainsAny(id, "/\\\x00") {
			fail(w, 400, "invalid customer")
			return
		}
		if strings.HasSuffix(r.Pattern, "/bridges") {
			target.Path = "/api/bridges"
			q.Set("customer_id", id)
		} else {
			target.Path = "/api/servicing/customers/" + id
		}
	}
	target.RawPath = ""
	target.RawQuery = q.Encode()
	token, err := servicing.Assertion(key, binding.Subject, &binding.Restriction)
	if err != nil {
		fail(w, 503, "servicing unavailable")
		return
	}
	req, err := http.NewRequestWithContext(ctx, "GET", target.String(), nil)
	if err != nil {
		fail(w, 503, "servicing unavailable")
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	response, err := a.upstream.Do(req)
	if err != nil {
		fail(w, 503, "servicing unavailable")
		return
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024+1))
	if err != nil || len(data) > 2*1024*1024 {
		fail(w, 502, "invalid servicing response")
		return
	}
	if response.StatusCode == 401 || response.StatusCode == 403 {
		fail(w, 503, "servicing unavailable")
		return
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		fail(w, 502, "servicing redirect refused")
		return
	}
	if response.StatusCode != 200 {
		if response.StatusCode == 400 || response.StatusCode == 404 || response.StatusCode == 409 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(response.StatusCode)
			_, _ = w.Write(data)
		} else {
			fail(w, 503, "servicing unavailable")
		}
		return
	}
	var body map[string]any
	if json.Unmarshal(data, &body) != nil || body["source_mode"] != binding.Mode || body["source_binding_id"] != binding.SourceBindingID || body["facility_id"] != binding.FacilityID || !safeProductJSON(body) {
		fail(w, 502, "invalid servicing response")
		return
	}
	if _, err := time.Parse(time.RFC3339Nano, asString(body["as_of"])); err != nil {
		fail(w, 502, "invalid servicing response")
		return
	}
	if customerRoute {
		items, ok := body["items"].([]any)
		if !ok || len(items) > 25 {
			fail(w, 502, "invalid servicing response")
			return
		}
		for _, item := range items {
			row, ok := item.(map[string]any)
			if !ok || row["customer_id"] != binding.CustomerID {
				fail(w, 502, "invalid servicing scope")
				return
			}
		}
	}
	writePublic(w, body)
}
func asString(v any) string { s, _ := v.(string); return s }
func safeProductJSON(v any) bool {
	switch value := v.(type) {
	case float64:
		return !math.IsNaN(value) && !math.IsInf(value, 0) && value == math.Trunc(value) && math.Abs(value) <= 9007199254740991
	case map[string]any:
		for _, item := range value {
			if !safeProductJSON(item) {
				return false
			}
		}
	case []any:
		for _, item := range value {
			if !safeProductJSON(item) {
				return false
			}
		}
	}
	return true
}
