package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/slabarcaf/flowcard-treasury/services/browser-gateway/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/browser-gateway/internal/servicing"
)

var commandKey = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
var resourceID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var reasonCode = regexp.MustCompile(`^[A-Z0-9_:-]{1,96}$`)
var termsHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (a *Server) bound(ctx context.Context, u postgres.User) (postgres.Binding, string, error) {
	if a.config.WriteMode != "DEMO" && a.config.WriteMode != "SANDBOX" {
		return postgres.Binding{}, "", postgres.ErrBinding
	}
	binding, err := a.store.Binding(ctx, u)
	if err != nil {
		return binding, "", err
	}
	if binding.Mode != a.config.WriteMode || binding.Origin != a.config.ServicingURL.String() || binding.ActorType != u.Role {
		return binding, "", postgres.ErrBinding
	}
	key := a.config.OperatorSecret
	if u.Role == "CUSTOMER" {
		key = a.config.CustomerSecret
	}
	actor, err := servicing.ReadActor(ctx, a.upstream, a.config.ServicingURL, key, binding.Subject, &binding.Restriction)
	if err != nil {
		return binding, "", err
	}
	if actor.Restriction != binding.Restriction {
		return binding, "", postgres.ErrBinding
	}
	return binding, key, nil
}

func bindingFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, postgres.ErrBinding) || errors.Is(err, servicing.ErrBinding) {
		fail(w, 403, "servicing binding unavailable")
		return
	}
	fail(w, 503, "servicing unavailable")
}

func (a *Server) servicingSession(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	session, err := a.store.Session(ctx, sessionToken(r))
	if err != nil {
		authFailure(w, err, "unauthorized")
		return
	}
	if a.config.WriteMode != "DEMO" && a.config.WriteMode != "SANDBOX" {
		writePublic(w, map[string]any{"enabled": false, "linked": false})
		return
	}
	binding, _, err := a.bound(ctx, session.User)
	if errors.Is(err, postgres.ErrBinding) || errors.Is(err, servicing.ErrBinding) {
		writePublic(w, map[string]any{"enabled": true, "linked": false})
		return
	}
	if err != nil {
		bindingFailure(w, err)
		return
	}
	writePublic(w, map[string]any{"enabled": true, "linked": true, "mode": binding.Mode, "role": binding.ActorType, "customer_id": binding.CustomerID})
}
func writePublic(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func strictCommand(w http.ResponseWriter, r *http.Request, destination any) ([]byte, error) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16384))
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(raw) {
		return nil, errors.New("invalid JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 16 {
			return errors.New("invalid JSON")
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, compound := token.(json.Delim)
		if !compound {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				token, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := token.(string)
				if !ok || seen[key] {
					return errors.New("duplicate JSON key")
				}
				for _, character := range key {
					if character >= 'A' && character <= 'Z' {
						return errors.New("invalid JSON field")
					}
				}
				seen[key] = true
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return errors.New("invalid JSON")
		}
	}
	if err := walk(0); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("trailing JSON")
	}
	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("trailing JSON")
	}
	return json.Marshal(destination)
}

// commandBody accepts only the owning service's narrow contracts, never money,
// policy, actor, customer or provider instructions from the browser.
func commandBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	var body []byte
	var err error
	if r.Pattern == "POST /api/offers" {
		var request struct {
			EvaluationID string `json:"evaluation_id"`
			CaseID       string `json:"originating_case_id"`
		}
		body, err = strictCommand(w, r, &request)
		if err == nil && (request.EvaluationID == "" || len(request.EvaluationID) > 128 || !resourceID.MatchString(request.CaseID)) {
			err = errors.New("invalid offer source")
		}
	} else if strings.HasSuffix(r.Pattern, "/acceptances") {
		var request struct {
			Hash    string `json:"terms_hash"`
			Version int64  `json:"expected_version"`
		}
		body, err = strictCommand(w, r, &request)
		if err == nil && (!termsHash.MatchString(request.Hash) || request.Version < 1 || request.Version > 9007199254740991) {
			err = errors.New("invalid terms")
		}
	} else if strings.HasSuffix(r.Pattern, "/approvals") {
		var request struct {
			Hash    string `json:"terms_hash"`
			Version int64  `json:"expected_version"`
			Reason  string `json:"reason_code"`
		}
		body, err = strictCommand(w, r, &request)
		if err == nil && (!termsHash.MatchString(request.Hash) || request.Version < 1 || request.Version > 9007199254740991 || !reasonCode.MatchString(request.Reason)) {
			err = errors.New("invalid approval")
		}
	} else if strings.HasSuffix(r.Pattern, "/fund") {
		var request struct {
			Version int64 `json:"expected_version"`
		}
		body, err = strictCommand(w, r, &request)
		if err == nil && (request.Version < 1 || request.Version > 9007199254740991) {
			err = errors.New("invalid version")
		}
	} else {
		var request struct {
			Version int64  `json:"expected_version"`
			Reason  string `json:"reason_code"`
		}
		body, err = strictCommand(w, r, &request)
		if err == nil && (request.Version < 1 || request.Version > 9007199254740991 || !reasonCode.MatchString(request.Reason)) {
			err = errors.New("invalid review")
		}
	}
	return body, err
}

func (a *Server) offerCustomer(ctx context.Context, body []byte) (string, error) {
	if a.config.CoordinatorURL == nil {
		return "", servicing.ErrUnavailable
	}
	var request struct {
		Evaluation string `json:"evaluation_id"`
		Case       string `json:"originating_case_id"`
	}
	if json.Unmarshal(body, &request) != nil {
		return "", servicing.ErrBinding
	}
	token, err := serviceAssertion(a.config.CoordinatorSecret, "flowcard-go", "flowcard-coordinator-service", "coordinator-reader")
	if err != nil {
		return "", err
	}
	target := *a.config.CoordinatorURL
	target.Path = "/api/coordinator/cases/" + request.Case
	call, err := http.NewRequestWithContext(ctx, "GET", target.String(), nil)
	if err != nil {
		return "", servicing.ErrUnavailable
	}
	call.Header.Set("Authorization", "Bearer "+token)
	response, err := a.upstream.Do(call)
	if err != nil {
		return "", servicing.ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return "", servicing.ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024+1))
	if err != nil || len(raw) > 2*1024*1024 {
		return "", servicing.ErrUnavailable
	}
	var record struct {
		ID         string `json:"case_id"`
		Mode       string `json:"mode"`
		Customer   string `json:"customer_id"`
		Assessment *struct {
			ID         string `json:"id"`
			Mode       string `json:"source_mode"`
			Customer   string `json:"customer_id"`
			Authorized bool   `json:"funding_authorized"`
			Recheck    bool   `json:"requires_recheck"`
		} `json:"assessment"`
	}
	if json.Unmarshal(raw, &record) != nil || record.ID != request.Case || record.Mode != a.config.WriteMode || record.Customer == "" || record.Assessment == nil || record.Assessment.ID != request.Evaluation || record.Assessment.Mode != record.Mode || record.Assessment.Customer != record.Customer || record.Assessment.Authorized || !record.Assessment.Recheck {
		return "", servicing.ErrBinding
	}
	return record.Customer, nil
}

func (a *Server) financialCommand(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	session, err := a.store.Session(ctx, sessionToken(r))
	if err != nil {
		authFailure(w, err, "unauthorized")
		return
	}
	acceptance := strings.HasSuffix(r.Pattern, "/acceptances")
	if (acceptance && session.User.Role != "CUSTOMER") || (!acceptance && session.User.Role != "OPERATOR") {
		fail(w, 403, "forbidden")
		return
	}
	binding, key, err := a.bound(ctx, session.User)
	if err != nil {
		bindingFailure(w, err)
		return
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || !commandKey.MatchString(keys[0]) || r.URL.RawQuery != "" {
		fail(w, 400, "invalid command")
		return
	}
	if id := r.PathValue("id"); id != "" && !resourceID.MatchString(id) {
		fail(w, 400, "invalid record")
		return
	}
	r.URL.Path = strings.ToLower(r.URL.Path)
	body, err := commandBody(w, r)
	if err != nil {
		fail(w, 400, "invalid command")
		return
	}
	command, found, err := a.store.FindCommand(ctx, binding, r.URL.Path, keys[0], body)
	if errors.Is(err, postgres.ErrConflict) {
		fail(w, 409, "idempotency conflict")
		return
	}
	if err != nil {
		fail(w, 503, "command storage unavailable")
		return
	}
	customer := command.Customer
	if !found {
		customer = binding.CustomerID
		if r.Pattern == "POST /api/offers" {
			customer, err = a.offerCustomer(ctx, body)
			if err != nil {
				bindingFailure(w, err)
				return
			}
		}
		command, err = a.store.PrepareCommand(ctx, binding, r.URL.Path, keys[0], customer, body)
	}
	if errors.Is(err, postgres.ErrConflict) {
		fail(w, 409, "idempotency conflict")
		return
	}
	if err != nil {
		fail(w, 503, "command storage unavailable")
		return
	}
	w.Header().Set("X-FlowCard-Command-ID", command.ID)
	if command.Status != nil {
		sendReceipt(w, *command.Status, command.Response)
		return
	}
	token, err := servicing.Assertion(key, binding.Subject, &binding.Restriction)
	if err != nil {
		fail(w, 503, "command outcome unknown")
		return
	}
	target := *a.config.ServicingURL
	target.Path = r.URL.Path
	call, err := http.NewRequestWithContext(ctx, "POST", target.String(), bytes.NewReader(command.Body))
	if err != nil {
		fail(w, 503, "command outcome unknown")
		return
	}
	call.Header.Set("Authorization", "Bearer "+token)
	call.Header.Set("Content-Type", "application/json")
	call.Header.Set("Idempotency-Key", command.Key)
	response, err := a.upstream.Do(call)
	if err != nil {
		fail(w, 502, "command outcome unknown")
		return
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(raw) > 65536 || !json.Valid(raw) || response.StatusCode >= 500 || response.StatusCode < 200 || response.StatusCode >= 300 && response.StatusCode < 400 {
		fail(w, 502, "command outcome unknown")
		return
	}
	offerID := ""
	if r.Pattern == "POST /api/offers" && response.StatusCode < 300 {
		var receipt struct {
			ID       string `json:"id"`
			Mode     string `json:"source_mode"`
			Customer string `json:"customer_id"`
		}
		if json.Unmarshal(raw, &receipt) != nil || !resourceID.MatchString(receipt.ID) || receipt.Mode != binding.Mode || receipt.Customer != customer {
			fail(w, 502, "command outcome unknown")
			return
		}
		offerID = receipt.ID
	}
	// A known upstream result must survive a disconnected browser, within a
	// separate short database-only budget. Unknown outcomes never get fabricated.
	saveCtx, saveCancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer saveCancel()
	if err := a.store.Receipt(saveCtx, binding, command, response.StatusCode, raw, offerID, customer); err != nil {
		fail(w, 503, "command outcome unknown")
		return
	}
	sendReceipt(w, response.StatusCode, raw)
}
func sendReceipt(w http.ResponseWriter, status int, raw []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

func (a *Server) financialCommands(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	session, err := a.store.Session(ctx, sessionToken(r))
	if err != nil {
		authFailure(w, err, "unauthorized")
		return
	}
	b, err := a.store.Binding(ctx, session.User)
	if err != nil {
		bindingFailure(w, err)
		return
	}
	commands, err := a.store.Commands(ctx, b)
	if err != nil {
		fail(w, 503, "command storage unavailable")
		return
	}
	type view struct {
		ID      string          `json:"id"`
		Path    string          `json:"path"`
		Key     string          `json:"idempotency_key"`
		Payload json.RawMessage `json:"payload"`
		State   string          `json:"state"`
		Status  *int            `json:"status"`
		Receipt json.RawMessage `json:"receipt"`
		Created time.Time       `json:"created_at"`
	}
	result := []view{}
	for _, c := range commands {
		state := "UNKNOWN"
		if c.Status != nil {
			state = "RECEIVED"
			if *c.Status >= 400 {
				state = "REJECTED"
			}
		}
		result = append(result, view{c.ID, c.Path, c.Key, json.RawMessage(c.Body), state, c.Status, json.RawMessage(c.Response), c.CreatedAt})
	}
	writePublic(w, map[string]any{"commands": result, "limit": 50})
}
func (a *Server) customerOffers(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	session, err := a.store.Session(ctx, sessionToken(r))
	if err != nil {
		authFailure(w, err, "unauthorized")
		return
	}
	if session.User.Role != "CUSTOMER" {
		fail(w, 403, "forbidden")
		return
	}
	b, _, err := a.bound(ctx, session.User)
	if err != nil {
		bindingFailure(w, err)
		return
	}
	if r.URL.RawQuery != "" {
		fail(w, 400, "invalid query")
		return
	}
	ids, err := a.store.CustomerOffers(ctx, b)
	if err != nil {
		fail(w, 503, "offer index unavailable")
		return
	}
	writePublic(w, map[string]any{"offer_ids": ids, "limit": 50})
}
