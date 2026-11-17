package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/config"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/internal/snapshots"
	"github.com/slabarcaf/flowcard-treasury/services/underwriting-engine/policy"
)

const maxDemoAPIResponse = 4_000_000

type evaluationSummary struct {
	ScenarioID      string         `json:"scenario_id"`
	EvaluationID    string         `json:"evaluation_id"`
	Status          string         `json:"status"`
	ReasonCodes     []string       `json:"reason_codes"`
	Rules           []expectedRule `json:"rules"`
	FirstHTTPStatus int            `json:"first_http_status"`
	ReplayStatus    int            `json:"same_key_replay_status"`
}

func runEvaluate(args []string) error {
	flags := flag.NewFlagSet("evaluate", flag.ContinueOnError)
	manifestPath := flags.String("manifest", "", "prepared DEMO manifest path")
	apiURL := flags.String("api-url", "", "loopback underwriting API URL")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *manifestPath == "" || *apiURL == "" {
		return errors.New("usage: demo evaluate --manifest PATH --api-url URL")
	}
	bundleRoot, manifest, err := loadGeneratedManifest(*manifestPath)
	if err != nil {
		return err
	}
	if manifest.SourceMode != "DEMO" {
		return errors.New("demo evaluator accepts DEMO fixtures only")
	}
	runtimeConfig, err := config.LoadRuntime()
	if err != nil || runtimeConfig.Mode != "DEMO" || runtimeConfig.FacilityID != manifest.FacilityID {
		return errors.New("demo runtime configuration does not match manifest")
	}
	apiEndpoint, err := loopbackAPIURL(*apiURL)
	if err != nil {
		return err
	}
	policyConfig, err := policy.LoadConfig()
	if err != nil || policyConfig.PolicyID() != manifest.PolicyID || policyConfig.PolicyHash() != manifest.PolicyHash {
		return errors.New("demo policy identity does not match manifest")
	}
	now := time.Now().UTC()
	if !freshPreparedRun(manifest.RunAnchor, now) {
		return errors.New("prepared DEMO run is stale; prepare a new run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	store, err := postgres.ConnectSnapshotStore(ctx, runtimeConfig.DatabaseURL)
	if err != nil {
		return errors.New("demo database unavailable")
	}
	defer store.Close()
	if err := store.Ready(ctx); err != nil {
		return errors.New("demo database not ready for restricted runtime identity")
	}
	databaseName, err := store.DatabaseName(ctx)
	if err != nil || databaseName != manifest.DatabaseName {
		return errors.New("demo database does not match manifest")
	}
	facilityFile, err := artifactPath(bundleRoot, manifest.FacilityFile)
	if err != nil {
		return err
	}
	facilityData, err := readLimitedFile(facilityFile, fixtureMaxBytes)
	if err != nil {
		return err
	}
	if _, err := snapshots.ImportFacility(ctx, store, facilityData, runtimeConfig.Mode,
		runtimeConfig.FacilityID, now); err != nil {
		return errors.New("DEMO facility snapshot import failed")
	}
	for _, scenario := range manifest.Scenarios {
		customerFile, err := artifactPath(bundleRoot, scenario.CustomerFile)
		if err != nil {
			return err
		}
		customerData, err := readLimitedFile(customerFile, fixtureMaxBytes)
		if err != nil {
			return err
		}
		if _, err := snapshots.ImportCustomer(ctx, store, customerData, runtimeConfig.Mode, now); err != nil {
			return errors.New("DEMO customer snapshot import failed")
		}
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	endpoint, err := url.JoinPath(apiEndpoint, "api", "underwriting", "evaluations")
	if err != nil {
		return errors.New("DEMO API URL invalid")
	}
	summaries := make([]evaluationSummary, 0, len(manifest.Scenarios))
	for _, scenario := range manifest.Scenarios {
		request := domain.EvaluationRequest{ShortfallID: scenario.ShortfallID, TermDays: scenario.TermDays}
		body, err := json.Marshal(request)
		if err != nil {
			return errors.New("DEMO evaluation request encoding failed")
		}
		firstBody, firstStatus, err := callEvaluationAPI(ctx, client, runtimeConfig.ServiceSecret,
			endpoint, http.MethodPost, body, scenario.IdempotencyKey)
		if err != nil || (firstStatus != http.StatusCreated && firstStatus != http.StatusOK) {
			return errors.New("DEMO evaluation POST failed")
		}
		first, err := decodeEvaluationResponse(firstBody)
		if err != nil || !matchesScenario(first, scenario, manifest) {
			return errors.New("DEMO evaluation did not match the expected rule trace")
		}
		detailURL, err := url.JoinPath(apiEndpoint, "api", "underwriting", "evaluations", first.ID)
		if err != nil {
			return errors.New("DEMO evaluation detail URL invalid")
		}
		detailBody, detailStatus, err := callEvaluationAPI(ctx, client, runtimeConfig.ServiceSecret,
			detailURL, http.MethodGet, nil, "")
		if err != nil || detailStatus != http.StatusOK || !sameJSON(firstBody, detailBody) {
			return errors.New("DEMO saved evaluation detail did not match POST")
		}
		replayBody, replayStatus, err := callEvaluationAPI(ctx, client, runtimeConfig.ServiceSecret,
			endpoint, http.MethodPost, body, scenario.IdempotencyKey)
		if err != nil || replayStatus != http.StatusOK || !sameJSON(detailBody, replayBody) {
			return errors.New("DEMO same-key replay did not return the saved response")
		}
		rules := make([]expectedRule, len(first.Rules))
		for i, rule := range first.Rules {
			rules[i] = expectedRule{RuleID: rule.RuleID, Status: rule.Status}
		}
		summaries = append(summaries, evaluationSummary{
			ScenarioID: scenario.ID, EvaluationID: first.ID, Status: first.Status,
			ReasonCodes: first.ReasonCodes, Rules: rules, FirstHTTPStatus: firstStatus,
			ReplayStatus: replayStatus,
		})
	}
	return writeJSONOutput(struct {
		SourceMode string              `json:"source_mode"`
		RunID      string              `json:"run_id"`
		Results    []evaluationSummary `json:"results"`
	}{SourceMode: "DEMO", RunID: manifest.RunID, Results: summaries})
}

func freshPreparedRun(anchor, now time.Time) bool {
	if anchor.IsZero() {
		return false
	}
	age := now.Sub(anchor.UTC())
	return age <= 5*time.Minute-5*time.Second && age >= -5*time.Second
}

func loopbackAPIURL(value string) (string, error) {
	parsed, err := url.Parse(value)
	if err != nil || !parsed.IsAbs() || parsed.User != nil || parsed.RawQuery != "" ||
		parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(value, "#") {
		return "", errors.New("DEMO API URL invalid")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "https" && scheme != "http" {
		return "", errors.New("DEMO API URL invalid")
	}
	host := strings.ToLower(parsed.Hostname())
	loopback := host == "localhost"
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		loopback = true
	}
	if !loopback {
		return "", errors.New("DEMO API URL must be loopback")
	}
	return parsed.String(), nil
}

func callEvaluationAPI(ctx context.Context, client *http.Client, secret string,
	endpoint, method string, body []byte, idempotencyKey string) ([]byte, int, error) {
	token, err := serviceAssertion(secret, currentUTC())
	if err != nil {
		return nil, 0, errors.New("DEMO API assertion unavailable")
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, errors.New("DEMO API request unavailable")
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, 0, errors.New("DEMO API unavailable")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxDemoAPIResponse+1))
	if err != nil || len(data) > maxDemoAPIResponse || !json.Valid(data) {
		return nil, response.StatusCode, errors.New("DEMO API returned invalid JSON")
	}
	return data, response.StatusCode, nil
}

func serviceAssertion(secret string, now time.Time) (string, error) {
	if len(secret) < 32 {
		return "", errors.New("secret invalid")
	}
	jti, err := randomUUID()
	if err != nil {
		return "", err
	}
	header, err := json.Marshal(struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
	}{Algorithm: "HS256", Type: "JWT"})
	if err != nil {
		return "", err
	}
	claims, err := json.Marshal(struct {
		Issuer    string `json:"iss"`
		Audience  string `json:"aud"`
		Subject   string `json:"sub"`
		IssuedAt  int64  `json:"iat"`
		ExpiresAt int64  `json:"exp"`
		TokenID   string `json:"jti"`
	}{
		Issuer: "flowcard-node-proxy", Audience: "flowcard-underwriting-service",
		Subject: "underwriting-evaluator", IssuedAt: now.Unix(),
		ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: jti,
	})
	if err != nil {
		return "", err
	}
	encoding := base64.RawURLEncoding
	unsigned := encoding.EncodeToString(header) + "." + encoding.EncodeToString(claims)
	mac := hmac.New(sha256.New, []byte(secret))
	if _, err := mac.Write([]byte(unsigned)); err != nil {
		return "", err
	}
	return unsigned + "." + encoding.EncodeToString(mac.Sum(nil)), nil
}

func decodeEvaluationResponse(body []byte) (domain.EvaluationResponse, error) {
	var response domain.EvaluationResponse
	if decodeStrictJSON(body, &response) != nil || response.ID == "" || response.ShortfallID == "" ||
		response.CustomerID == "" || response.AccountID == "" || response.TransactionID == "" ||
		response.SourceMode != "DEMO" || response.PolicyID == "" || response.PolicyHash == "" ||
		response.CreatedAt.IsZero() || response.EvaluatedAt.IsZero() || len(response.Rules) != 10 {
		return domain.EvaluationResponse{}, errors.New("invalid DEMO evaluation response")
	}
	return response, nil
}

func matchesScenario(response domain.EvaluationResponse, scenario generatedScenario, manifest generatedManifest) bool {
	if response.ShortfallID != scenario.ShortfallID || response.CustomerID != scenario.CustomerID ||
		response.AccountID != scenario.AccountID || response.TransactionID != scenario.TransactionID ||
		response.SourceMode != "DEMO" || response.Status != scenario.ExpectedStatus ||
		response.PolicyID != manifest.PolicyID || response.PolicyHash != manifest.PolicyHash ||
		response.FundingAuthorized || !response.RequiresRecheck ||
		response.PrincipalCents == nil || *response.PrincipalCents != scenario.PrincipalCents ||
		response.TermDays != scenario.TermDays || response.EstimatedFeeCents == nil ||
		*response.EstimatedFeeCents != scenario.EstimatedFeeCents || response.TotalDueCents == nil ||
		*response.TotalDueCents != scenario.TotalDueCents || response.CustomerLimitCents == nil ||
		*response.CustomerLimitCents != scenario.CustomerLimitCents || response.ExistingExposureCents == nil ||
		*response.ExistingExposureCents != scenario.ExistingExposureCents || response.PostFundingExposureCents == nil ||
		*response.PostFundingExposureCents != scenario.PostFundingExposureCents || response.AvailableCapitalCents == nil ||
		*response.AvailableCapitalCents != manifest.FacilityBooks.AvailableCapitalCents ||
		response.CustomerSnapshotID == nil || response.CustomerSnapshotVersion == nil ||
		response.FacilitySnapshotID == nil || response.FacilitySnapshotVersion == nil ||
		response.Tier == nil || *response.Tier != "B" || len(response.Rules) != len(scenario.ExpectedRules) {
		return false
	}
	for i, expected := range scenario.ExpectedRules {
		if response.Rules[i].RuleID != expected.RuleID || response.Rules[i].Status != expected.Status {
			return false
		}
	}
	if response.Rules[4].RuleID != string(policy.RuleCustomerCapacity) ||
		!actualCents(response.Rules[4].Actual, "post_funding_exposure_cents", scenario.PostFundingExposureCents) ||
		!actualCents(response.Rules[4].Actual, "customer_limit_cents", scenario.CustomerLimitCents) {
		return false
	}
	if scenario.ID != "missing_receivable_dates" &&
		!actualCents(response.Rules[6].Actual, "verified_free_receivables_cents", scenario.FreeReceivablesCents) {
		return false
	}
	return true
}

func actualCents(actual map[string]any, name string, expected int64) bool {
	value, exists := actual[name]
	if !exists {
		return false
	}
	switch number := value.(type) {
	case float64:
		return number == float64(expected)
	case json.Number:
		parsed, err := number.Int64()
		return err == nil && parsed == expected
	default:
		return false
	}
}

func sameJSON(left, right []byte) bool {
	var leftValue, rightValue any
	leftDecoder := json.NewDecoder(bytes.NewReader(left))
	leftDecoder.UseNumber()
	rightDecoder := json.NewDecoder(bytes.NewReader(right))
	rightDecoder.UseNumber()
	if leftDecoder.Decode(&leftValue) != nil || rightDecoder.Decode(&rightValue) != nil {
		return false
	}
	leftCanonical, err := json.Marshal(leftValue)
	if err != nil {
		return false
	}
	rightCanonical, err := json.Marshal(rightValue)
	return err == nil && bytes.Equal(leftCanonical, rightCanonical)
}
