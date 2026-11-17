package main

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/config"
)

const providerRequestLimit = 64 * 1024

type providerDouble struct {
	mu          sync.Mutex
	state       providerState
	statePath   string
	controlKey  []byte
	token       string
	tokenExpiry time.Time
}

type transferCreateRequest struct {
	RequestID   string `json:"request_id"`
	Destination string `json:"destination"`
	Amount      string `json:"amount"`
	Currency    string `json:"currency"`
	Reason      string `json:"reason"`
	Reference   string `json:"reference"`
}

type transferResponse struct {
	ID               string          `json:"id"`
	SourceAccountID  string          `json:"source_account_id"`
	RequestID        string          `json:"request_id"`
	Status           string          `json:"status"`
	Destination      string          `json:"destination"`
	Amount           json.RawMessage `json:"amount"`
	Currency         string          `json:"currency"`
	FeeAmount        json.RawMessage `json:"fee_amount"`
	FeeCurrency      string          `json:"fee_currency"`
	SettledAt        *time.Time      `json:"settled_at,omitempty"`
	CreatedAt        *time.Time      `json:"created_at,omitempty"`
	ReversalID       string          `json:"reversal_id,omitempty"`
	ReversalAmount   json.RawMessage `json:"reversed_amount,omitempty"`
	ReversalCurrency string          `json:"reversal_currency,omitempty"`
	ReversedAt       *time.Time      `json:"reversed_at,omitempty"`
}

func runServeProvider(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("serve-provider", flag.ContinueOnError)
	manifestPath := flags.String("manifest", "", "prepared DEMO manifest path")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *manifestPath == "" {
		return errors.New("usage: demo serve-provider --manifest PATH")
	}
	manifest, statePath, err := loadFundingManifest(*manifestPath)
	if err != nil {
		return err
	}
	if manifest.SourceMode != "DEMO" || manifest.ProviderAddress != providerAddress || manifest.FacilityID != fixtureFacilityID {
		return errors.New("provider double requires prepared DEMO manifest")
	}
	now := time.Now().UTC()
	if manifest.RunAnchor.After(now.Add(5*time.Second)) || now.Sub(manifest.RunAnchor) > 4*time.Minute {
		return errors.New("prepared DEMO manifest expired; prepare a fresh run")
	}
	if os.Getenv("SERVICING_MODE") != "DEMO" || os.Getenv("SERVICING_PROVIDER_MODE") != "MOCK_HTTP" ||
		os.Getenv("SERVICING_PROVIDER_BASE_URL") != "http://"+manifest.ProviderAddress ||
		os.Getenv("AIRWALLEX_CLIENT_ID") != config.DemoClientID || os.Getenv("AIRWALLEX_API_KEY") != config.DemoAPIKey {
		return errors.New("provider double environment does not match synthetic DEMO profile")
	}
	controlKey := os.Getenv("DEMO_PROVIDER_CONTROL_KEY")
	if len(controlKey) < 32 {
		return errors.New("provider double control key unavailable")
	}
	data, err := readDemoFile(statePath, maxDemoArtifactBytes)
	if err != nil {
		return err
	}
	var state providerState
	if decodeStrict(data, &state) != nil || state.SchemaVersion != fixtureVersion || state.RunID != manifest.RunID ||
		state.FundingAccountRef != manifest.FundingAccountRef || state.Accounts[manifest.FundingAccountRef] <= 0 ||
		state.Payments == nil || state.Deposits == nil || state.Transfers == nil || state.RequestIndex == nil {
		return errors.New("provider fixture state invalid")
	}
	double := &providerDouble{state: state, statePath: statePath, controlKey: []byte(controlKey)}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/authentication/login", double.login)
	mux.HandleFunc("POST /api/v1/connected_account_transfers/create", double.createTransfer)
	mux.HandleFunc("GET /api/v1/connected_account_transfers", double.listTransfers)
	mux.HandleFunc("GET /api/v1/connected_account_transfers/{id}", double.retrieveTransfer)
	mux.HandleFunc("GET /api/v1/balances/current", double.readBalance)
	mux.HandleFunc("GET /__fixture/payments/{id}", double.readPayment)
	mux.HandleFunc("GET /__fixture/deposits/{id}", double.readDeposit)
	mux.HandleFunc("POST /__control/deposits/{id}/configure", double.configureDepositControl)
	mux.HandleFunc("POST /__control/deposits/{id}/settle", double.settleDepositControl)
	mux.HandleFunc("POST /__control/transfers/{id}/reverse", double.reverseTransferControl)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { writeProviderError(w, http.StatusNotFound, "not_found") })
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp4", manifest.ProviderAddress)
	if err != nil {
		return errors.New("provider double address unavailable")
	}
	defer listener.Close()
	server := &http.Server{Handler: providerJSONHandler(mux), ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second,
		MaxHeaderBytes: 16 * 1024, BaseContext: func(net.Listener) context.Context { return ctx }}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
		}
		<-done
		return nil
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("provider double stopped")
	}
}

func loadFundingManifest(path string) (fundingManifest, string, error) {
	var manifest fundingManifest
	absPath, err := filepath.Abs(path)
	if err != nil {
		return manifest, "", errors.New("funding demo manifest path invalid")
	}
	repoRoot, err := demoRepositoryRoot()
	if err != nil {
		return manifest, "", err
	}
	devRoot := filepath.Join(repoRoot, ".dev")
	relativeManifest, relErr := filepath.Rel(devRoot, absPath)
	if relErr != nil || relativeManifest == ".." || strings.HasPrefix(relativeManifest, ".."+string(filepath.Separator)) || ensureNoSymlinkPath(repoRoot, absPath) != nil {
		return manifest, "", errors.New("funding demo manifest must be under .dev without symlinks")
	}
	info, err := os.Lstat(absPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return manifest, "", errors.New("funding demo manifest unavailable")
	}
	data, err := readDemoFile(absPath, maxDemoArtifactBytes)
	if err != nil || decodeStrict(data, &manifest) != nil || manifest.SchemaVersion != fixtureVersion ||
		manifest.SourceMode != "DEMO" || !demoUUID(manifest.RunID) || manifest.SourceBindingID != fixtureSourceBindingID ||
		manifest.FacilityID != fixtureFacilityID || !config.ValidOwnedDatabaseName(manifest.FundingDatabaseName, "flowcard_funding_demo") ||
		!config.ValidOwnedDatabaseName(manifest.UnderwritingDatabaseName, "flowcard_underwriting_demo") ||
		manifest.ProviderAddress != providerAddress || manifest.APIAddress != apiAddress ||
		manifest.FundingOpeningCents <= 0 ||
		manifest.PolicyID == "" || !demoSHA256(manifest.PolicyHash) ||
		(manifest.UpstreamInputKind != upstreamInputComponent && manifest.UpstreamInputKind != upstreamInputOwnedBundle) {
		return fundingManifest{}, "", errors.New("funding demo manifest invalid")
	}
	root := filepath.Dir(absPath)
	if manifest.ArtifactRoot != "." {
		artifactRoot := filepath.FromSlash(manifest.ArtifactRoot)
		if filepath.IsAbs(artifactRoot) || !filepath.IsLocal(artifactRoot) {
			return fundingManifest{}, "", errors.New("funding demo artifact root invalid")
		}
		root = filepath.Join(root, artifactRoot)
	}
	if ensureNoSymlinkPath(filepath.Dir(absPath), root) != nil {
		return fundingManifest{}, "", errors.New("funding demo artifact root invalid")
	}
	statePath, err := demoArtifactPath(root, manifest.ProviderStateFile)
	if err != nil {
		return fundingManifest{}, "", err
	}
	privatePath, err := demoArtifactPath(root, manifest.PrivateEnvFile)
	if err != nil {
		return fundingManifest{}, "", err
	}
	privateInfo, err := os.Lstat(privatePath)
	if err != nil || !privateInfo.Mode().IsRegular() || privateInfo.Mode().Perm() != 0o600 {
		return fundingManifest{}, "", errors.New("private DEMO environment file must be mode 0600")
	}
	stateInfo, err := os.Lstat(statePath)
	if err != nil || !stateInfo.Mode().IsRegular() || stateInfo.Mode().Perm() != 0o600 {
		return fundingManifest{}, "", errors.New("provider fixture state must be mode 0600")
	}
	return manifest, statePath, nil
}

func demoArtifactPath(root, relative string) (string, error) {
	path := filepath.FromSlash(relative)
	if relative == "" || filepath.IsAbs(path) || !filepath.IsLocal(path) {
		return "", errors.New("demo artifact path invalid")
	}
	resolved := filepath.Join(root, path)
	if ensureNoSymlinkPath(root, resolved) != nil {
		return "", errors.New("demo artifact path invalid")
	}
	return resolved, nil
}

func readDemoFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("demo artifact unavailable")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("demo artifact unavailable")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("demo artifact unavailable")
	}
	return data, nil
}

func providerJSONHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func (p *providerDouble) login(w http.ResponseWriter, r *http.Request) {
	if _, err := readProviderBody(r); err != nil {
		writeProviderError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	clientIDs, apiKeys := r.Header.Values("x-client-id"), r.Header.Values("x-api-key")
	if len(clientIDs) != 1 || len(apiKeys) != 1 ||
		!hmac.Equal([]byte(clientIDs[0]), []byte(config.DemoClientID)) ||
		!hmac.Equal([]byte(apiKeys[0]), []byte(config.DemoAPIKey)) {
		writeProviderError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	now := time.Now().UTC()
	p.mu.Lock()
	token, expires := p.token, p.tokenExpiry
	var tokenErr error
	if token == "" || !now.Before(expires) {
		token, tokenErr = randomSecret()
		if tokenErr == nil {
			expires = now.Add(30 * time.Minute)
			p.token, p.tokenExpiry = token, expires
		}
	}
	p.mu.Unlock()
	if tokenErr != nil {
		writeProviderError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	writeProviderJSON(w, http.StatusOK, map[string]string{"token": token, "expires_at": expires.Format(time.RFC3339)})
}

func (p *providerDouble) authorize(w http.ResponseWriter, r *http.Request) (string, bool) {
	authorization, scopes := r.Header.Values("Authorization"), r.Header.Values("x-on-behalf-of")
	p.mu.Lock()
	token, expiry := p.token, p.tokenExpiry
	account := ""
	if len(scopes) == 1 {
		account = scopes[0]
	}
	_, accountExists := p.state.Accounts[account]
	p.mu.Unlock()
	if len(authorization) != 1 || !strings.HasPrefix(authorization[0], "Bearer ") ||
		len(scopes) != 1 || !accountExists || token == "" || !time.Now().UTC().Before(expiry) ||
		!hmac.Equal([]byte(strings.TrimSpace(strings.TrimPrefix(authorization[0], "Bearer "))), []byte(token)) {
		writeProviderError(w, http.StatusUnauthorized, "unauthorized")
		return "", false
	}
	return account, true
}

func (p *providerDouble) readBalance(w http.ResponseWriter, r *http.Request) {
	account, ok := p.authorize(w, r)
	if !ok {
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query) != 1 || len(query["account_type"]) != 1 || query.Get("account_type") != "cash" {
		writeProviderError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	p.mu.Lock()
	balance := p.state.Accounts[account]
	p.mu.Unlock()
	writeProviderJSON(w, http.StatusOK, []map[string]any{{"account_type": "CASH", "currency": "USD", "available_amount": json.RawMessage(decimalJSON(balance))}})
}

func (p *providerDouble) readPayment(w http.ResponseWriter, r *http.Request) {
	account, ok := p.authorize(w, r)
	if !ok {
		return
	}
	parentID := r.PathValue("id")
	p.mu.Lock()
	payment, exists := p.state.Payments[parentID]
	available, knownAccount := p.state.Accounts[account]
	p.mu.Unlock()
	if r.URL.RawQuery != "" || !exists || !knownAccount || payment.AccountID != account {
		writeProviderError(w, http.StatusNotFound, "not_found")
		return
	}
	shortfall := int64(0)
	if payment.AttemptedCents > available {
		shortfall = payment.AttemptedCents - available
	}
	now := time.Now().UTC()
	writeProviderJSON(w, http.StatusOK, map[string]any{"source_mode": "DEMO", "customer_id": payment.CustomerID,
		"canonical_account_id": payment.AccountID, "parent_transaction_id": parentID,
		"authorization_event_id": payment.AuthorizationEventID, "attempted_amount_cents": payment.AttemptedCents,
		"available_cents": available, "shortfall_cents": shortfall, "currency": "USD",
		"observed_at": now, "balance_observed_at": now})
}

func (p *providerDouble) readDeposit(w http.ResponseWriter, r *http.Request) {
	account, ok := p.authorize(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	p.mu.Lock()
	deposit, exists := p.state.Deposits[id]
	p.mu.Unlock()
	if r.URL.RawQuery != "" || !exists || deposit.AccountRef != account || !deposit.Settled || deposit.SettledAt == nil {
		writeProviderError(w, http.StatusNotFound, "not_found")
		return
	}
	writeProviderJSON(w, http.StatusOK, map[string]any{"account_ref": deposit.AccountRef, "deposit_id": deposit.ID,
		"customer_id": deposit.CustomerID, "currency": "USD", "gross_cents": deposit.GrossCents,
		"net_cents": deposit.NetCents, "provider_fee_cents": deposit.FeeCents,
		"status": "SETTLED", "kind": "EXTERNAL_SETTLED", "settled_at": deposit.SettledAt})
}

func (p *providerDouble) createTransfer(w http.ResponseWriter, r *http.Request) {
	source, ok := p.authorize(w, r)
	if !ok {
		return
	}
	data, err := readProviderBody(r)
	if err != nil {
		writeProviderError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	var request transferCreateRequest
	if decodeStrict(data, &request) != nil {
		writeProviderError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	amount, err := parseProviderAmount(request.Amount)
	if err != nil || request.RequestID == "" || request.Destination == "" || request.Destination == source ||
		request.Currency != "USD" || request.Reference == "" || !validTransferReason(request.Reason) {
		writeProviderError(w, http.StatusUnprocessableEntity, "transfer_invalid")
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if oldID, exists := p.state.RequestIndex[request.RequestID]; exists {
		old := p.state.Transfers[oldID]
		if old.SourceAccountID != source || old.Destination != request.Destination || old.AmountCents != amount ||
			old.Reason != request.Reason || old.Reference != request.Reference {
			writeProviderError(w, http.StatusConflict, "request_conflict")
			return
		}
		writeProviderTransfer(w, http.StatusOK, old)
		return
	}
	sourceBalance, sourceExists := p.state.Accounts[source]
	destinationBalance, destinationExists := p.state.Accounts[request.Destination]
	if !sourceExists || !destinationExists || sourceBalance < amount || destinationBalance > math.MaxInt64-amount {
		writeProviderError(w, http.StatusUnprocessableEntity, "balance_insufficient")
		return
	}
	id, err := randomUUID()
	if err != nil {
		writeProviderError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	now := time.Now().UTC()
	transfer := providerTransfer{ID: id, RequestID: request.RequestID, SourceAccountID: source,
		Destination: request.Destination, AmountCents: amount, Reason: request.Reason,
		Reference: request.Reference, Status: "SETTLED", SettledAt: now, CreatedAt: now}
	previous, err := cloneProviderState(p.state)
	if err != nil {
		writeProviderError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	p.state.Accounts[source] = sourceBalance - amount
	p.state.Accounts[request.Destination] = destinationBalance + amount
	p.state.Transfers[id] = transfer
	p.state.RequestIndex[request.RequestID] = id
	dropReply := request.Reason == "professional_business_services" && p.state.DropFirstCollectionFeeReply
	if dropReply {
		p.state.DropFirstCollectionFeeReply = false
		p.state.DroppedReplyRequestID = request.RequestID
	}
	if err = p.persistLocked(); err != nil {
		p.state = previous
		writeProviderError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	if dropReply {
		if dropProviderReply(w) {
			return
		}
		p.mu.Lock()
		p.state.DropFirstCollectionFeeReply = true
		p.state.DroppedReplyRequestID = ""
		persistErr := p.persistLocked()
		p.mu.Unlock()
		if persistErr != nil {
			writeProviderError(w, http.StatusServiceUnavailable, "unavailable")
			return
		}
	}
	writeProviderTransfer(w, http.StatusCreated, transfer)
}

func (p *providerDouble) listTransfers(w http.ResponseWriter, r *http.Request) {
	source, ok := p.authorize(w, r)
	if !ok {
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeProviderError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	for key, values := range query {
		if key != "request_id" && key != "page_size" && key != "page" || len(values) != 1 {
			writeProviderError(w, http.StatusBadRequest, "invalid_query")
			return
		}
	}
	requestID := query.Get("request_id")
	if requestID == "" {
		writeProviderError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	pageSize := 100
	if value := query.Get("page_size"); value != "" {
		pageSize, err = strconv.Atoi(value)
		if err != nil || pageSize < 1 || pageSize > 100 {
			writeProviderError(w, http.StatusBadRequest, "invalid_query")
			return
		}
	}
	offset := 0
	if value := query.Get("page"); value != "" {
		offset, err = strconv.Atoi(value)
		if err != nil || offset < 0 || offset > 1000 {
			writeProviderError(w, http.StatusBadRequest, "invalid_query")
			return
		}
	}
	p.mu.Lock()
	matches := []providerTransfer{}
	for _, transfer := range p.state.Transfers {
		if transfer.RequestID == requestID && transfer.SourceAccountID == source {
			matches = append(matches, transfer)
		}
	}
	p.mu.Unlock()
	sort.Slice(matches, func(i, j int) bool { return matches[i].ID < matches[j].ID })
	if offset > len(matches) {
		offset = len(matches)
	}
	end := offset + pageSize
	if end > len(matches) {
		end = len(matches)
	}
	items := make([]json.RawMessage, 0, end-offset)
	for _, transfer := range matches[offset:end] {
		item, err := json.Marshal(providerTransferWire(transfer))
		if err != nil {
			writeProviderError(w, http.StatusInternalServerError, "response_invalid")
			return
		}
		items = append(items, item)
	}
	pageAfter := ""
	if end < len(matches) {
		pageAfter = strconv.Itoa(end)
	}
	writeProviderJSON(w, http.StatusOK, map[string]any{"items": items, "page_after": pageAfter})
}

func (p *providerDouble) retrieveTransfer(w http.ResponseWriter, r *http.Request) {
	source, ok := p.authorize(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeProviderError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	p.mu.Lock()
	transfer, exists := p.state.Transfers[r.PathValue("id")]
	p.mu.Unlock()
	if !exists || transfer.SourceAccountID != source {
		writeProviderError(w, http.StatusNotFound, "not_found")
		return
	}
	writeProviderTransfer(w, http.StatusOK, transfer)
}

func (p *providerDouble) settleDepositControl(w http.ResponseWriter, r *http.Request) {
	if !p.authorizeControl(w, r) {
		return
	}
	if _, err := readProviderBody(r); err != nil {
		writeProviderError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	id := r.PathValue("id")
	p.mu.Lock()
	defer p.mu.Unlock()
	deposit, exists := p.state.Deposits[id]
	if !exists {
		writeProviderError(w, http.StatusNotFound, "not_found")
		return
	}
	if !deposit.Settled {
		balance, ok := p.state.Accounts[deposit.AccountRef]
		if !ok || balance > math.MaxInt64-deposit.NetCents {
			writeProviderError(w, http.StatusUnprocessableEntity, "deposit_balance_invalid")
			return
		}
		previous, err := cloneProviderState(p.state)
		if err != nil {
			writeProviderError(w, http.StatusServiceUnavailable, "unavailable")
			return
		}
		now := time.Now().UTC()
		p.state.Accounts[deposit.AccountRef] = balance + deposit.NetCents
		deposit.Settled, deposit.SettledAt = true, &now
		p.state.Deposits[id] = deposit
		if err = p.persistLocked(); err != nil {
			p.state = previous
			writeProviderError(w, http.StatusServiceUnavailable, "unavailable")
			return
		}
	}
	writeProviderJSON(w, http.StatusOK, deposit)
}

func (p *providerDouble) configureDepositControl(w http.ResponseWriter, r *http.Request) {
	if !p.authorizeControl(w, r) {
		return
	}
	data, err := readProviderBody(r)
	if err != nil {
		writeProviderError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	var command struct {
		GrossCents int64 `json:"gross_cents"`
		NetCents   int64 `json:"net_cents"`
		FeeCents   int64 `json:"provider_fee_cents"`
	}
	if decodeStrict(data, &command) != nil || command.GrossCents <= 0 || command.NetCents <= 0 ||
		command.FeeCents < 0 || command.NetCents > math.MaxInt64-command.FeeCents ||
		command.GrossCents != command.NetCents+command.FeeCents {
		writeProviderError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	id := r.PathValue("id")
	deposit, exists := p.state.Deposits[id]
	if !exists {
		writeProviderError(w, http.StatusNotFound, "not_found")
		return
	}
	if deposit.Settled {
		writeProviderError(w, http.StatusConflict, "deposit_already_settled")
		return
	}
	previous, err := cloneProviderState(p.state)
	if err != nil {
		writeProviderError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	deposit.GrossCents, deposit.NetCents, deposit.FeeCents = command.GrossCents, command.NetCents, command.FeeCents
	p.state.Deposits[id] = deposit
	if err = p.persistLocked(); err != nil {
		p.state = previous
		writeProviderError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	writeProviderJSON(w, http.StatusOK, deposit)
}

func (p *providerDouble) reverseTransferControl(w http.ResponseWriter, r *http.Request) {
	if !p.authorizeControl(w, r) {
		return
	}
	data, err := readProviderBody(r)
	if err != nil {
		writeProviderError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	var command struct {
		ReversalID  string `json:"reversal_id"`
		AmountCents int64  `json:"amount_cents"`
	}
	if decodeStrict(data, &command) != nil || command.ReversalID == "" || command.AmountCents <= 0 {
		writeProviderError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	id := r.PathValue("id")
	p.mu.Lock()
	defer p.mu.Unlock()
	transfer, exists := p.state.Transfers[id]
	if !exists {
		writeProviderError(w, http.StatusNotFound, "not_found")
		return
	}
	if transfer.Reason != "professional_business_services" && transfer.Reason != "loan_credit_repayment" {
		writeProviderError(w, http.StatusUnprocessableEntity, "reversal_not_supported")
		return
	}
	if transfer.ReversalID != "" {
		if transfer.ReversalID != command.ReversalID || transfer.ReversedAmountCents != command.AmountCents {
			writeProviderError(w, http.StatusConflict, "reversal_conflict")
			return
		}
		writeProviderTransfer(w, http.StatusOK, transfer)
		return
	}
	if command.AmountCents > transfer.AmountCents {
		writeProviderError(w, http.StatusUnprocessableEntity, "reversal_amount_invalid")
		return
	}
	sourceBalance, destinationBalance := p.state.Accounts[transfer.SourceAccountID], p.state.Accounts[transfer.Destination]
	if destinationBalance < command.AmountCents || sourceBalance > math.MaxInt64-command.AmountCents {
		writeProviderError(w, http.StatusUnprocessableEntity, "reversal_balance_invalid")
		return
	}
	previous, err := cloneProviderState(p.state)
	if err != nil {
		writeProviderError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	now := time.Now().UTC()
	p.state.Accounts[transfer.Destination] = destinationBalance - command.AmountCents
	p.state.Accounts[transfer.SourceAccountID] = sourceBalance + command.AmountCents
	transfer.Status = "REVERSED"
	transfer.ReversalID, transfer.ReversedAmountCents, transfer.ReversalCurrency, transfer.ReversedAt = command.ReversalID, command.AmountCents, "USD", &now
	p.state.Transfers[id] = transfer
	if err = p.persistLocked(); err != nil {
		p.state = previous
		writeProviderError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	writeProviderTransfer(w, http.StatusOK, transfer)
}

func (p *providerDouble) authorizeControl(w http.ResponseWriter, r *http.Request) bool {
	values := r.Header.Values("X-Demo-Control-Key")
	if len(values) != 1 || !hmac.Equal([]byte(values[0]), p.controlKey) {
		writeProviderError(w, http.StatusForbidden, "control_denied")
		return false
	}
	return true
}

func (p *providerDouble) persistLocked() error {
	data, err := json.MarshalIndent(p.state, "", "  ")
	if err != nil {
		return errors.New("provider state encode failed")
	}
	temp, err := os.CreateTemp(filepath.Dir(p.statePath), ".provider-state-*.tmp")
	if err != nil {
		return errors.New("provider state write failed")
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err = temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return errors.New("provider state write failed")
	}
	if _, err = temp.Write(data); err != nil {
		_ = temp.Close()
		return errors.New("provider state write failed")
	}
	if err = temp.Sync(); err != nil {
		_ = temp.Close()
		return errors.New("provider state write failed")
	}
	if err = temp.Close(); err != nil {
		return errors.New("provider state write failed")
	}
	if err = os.Rename(tempPath, p.statePath); err != nil {
		return errors.New("provider state write failed")
	}
	return nil
}

func providerTransferWire(transfer providerTransfer) transferResponse {
	fee := json.RawMessage("0.00")
	return transferResponse{ID: transfer.ID, SourceAccountID: transfer.SourceAccountID,
		RequestID: transfer.RequestID, Status: transfer.Status, Destination: transfer.Destination,
		Amount: json.RawMessage(decimalJSON(transfer.AmountCents)), Currency: "USD", FeeAmount: fee,
		FeeCurrency: "USD", SettledAt: timePointer(transfer.SettledAt), CreatedAt: timePointer(transfer.CreatedAt),
		ReversalID: transfer.ReversalID, ReversalAmount: reversalJSON(transfer),
		ReversalCurrency: transfer.ReversalCurrency, ReversedAt: transfer.ReversedAt}
}

func writeProviderTransfer(w http.ResponseWriter, status int, transfer providerTransfer) {
	writeProviderJSON(w, status, providerTransferWire(transfer))
}

func reversalJSON(transfer providerTransfer) json.RawMessage {
	if transfer.ReversalID == "" || transfer.ReversedAmountCents <= 0 {
		return nil
	}
	return json.RawMessage(decimalJSON(transfer.ReversedAmountCents))
}

func decimalJSON(cents int64) string {
	return strconv.FormatInt(cents/100, 10) + "." + fmt.Sprintf("%02d", cents%100)
}

func parseProviderAmount(value string) (int64, error) {
	if value == "" || strings.ContainsAny(value, "eE+-") {
		return 0, errors.New("provider amount invalid")
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" || len(parts) == 2 && len(parts[1]) > 2 {
		return 0, errors.New("provider amount invalid")
	}
	fraction := "00"
	if len(parts) == 2 {
		fraction = parts[1]
		if len(fraction) == 0 {
			fraction = "00"
		} else if len(fraction) == 1 {
			fraction += "0"
		}
	}
	whole, ok := new(big.Int).SetString(parts[0]+fraction, 10)
	if !ok || !whole.IsInt64() || whole.Sign() <= 0 {
		return 0, errors.New("provider amount invalid")
	}
	return whole.Int64(), nil
}

func validTransferReason(reason string) bool {
	return reason == "loan_disbursement" || reason == "loan_credit_repayment" || reason == "professional_business_services"
}

func readProviderBody(r *http.Request) ([]byte, error) {
	if r.ContentLength > providerRequestLimit {
		return nil, errors.New("provider request too large")
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, providerRequestLimit+1))
	if err != nil || len(data) == 0 || len(data) > providerRequestLimit {
		return nil, errors.New("provider request invalid")
	}
	return data, nil
}

func writeProviderJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeProviderError(w http.ResponseWriter, status int, code string) {
	writeProviderJSON(w, status, map[string]string{"error": code})
}

func dropProviderReply(w http.ResponseWriter) bool {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return false
	}
	connection, _, err := hijacker.Hijack()
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}

func cloneProviderState(state providerState) (providerState, error) {
	body, err := json.Marshal(state)
	if err != nil {
		return providerState{}, err
	}
	var cloned providerState
	if json.Unmarshal(body, &cloned) != nil {
		return providerState{}, errors.New("provider state clone failed")
	}
	return cloned, nil
}

func demoSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return false
		}
	}
	return true
}

func demoUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, char := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if char != '-' {
				return false
			}
			continue
		}
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return false
		}
	}
	return true
}
