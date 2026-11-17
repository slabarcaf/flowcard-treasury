// Command demo-actor performs explicitly synthetic DEMO offer decisions.
package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/funding-collection/internal/config"
)

type offer struct {
	ID        string `json:"id"`
	Version   int64  `json:"version"`
	TermsHash string `json:"terms_hash"`
}

func main() {
	action := flag.String("action", "", "create-offer|accept|approve")
	apiURL := flag.String("api-url", "http://127.0.0.1:8085", "loopback DEMO API")
	id := flag.String("id", "", "offer ID")
	evaluation := flag.String("evaluation-id", "", "saved evaluation ID")
	originatingCase := flag.String("originating-case-id", "", "real coordinator case UUID supplied by the host")
	subject := flag.String("subject", "", "registered actor subject")
	reason := flag.String("reason-code", "", "operator reason")
	key := flag.String("idempotency-key", "", "stable command key")
	flag.Parse()
	if err := run(*action, *apiURL, *id, *evaluation, *originatingCase, *subject, *reason, *key); err != nil {
		fmt.Fprintln(os.Stderr, "demo actor action failed:", err)
		os.Exit(1)
	}
}

func run(action, apiURL, offerID, evaluationID, originatingCaseID, subject, reason, key string) error {
	cfg, err := config.Load()
	if err != nil || cfg.Mode != config.ModeDemo {
		return errors.New("demo_actor_configuration_invalid")
	}
	base, err := url.Parse(apiURL)
	if err != nil || base.Scheme != "http" || !isLoopback(base.Hostname()) {
		return errors.New("demo_actor_requires_loopback_http")
	}
	if key == "" {
		return errors.New("idempotency_key_required")
	}
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if action == "create-offer" {
		if evaluationID == "" {
			return errors.New("evaluation_id_required")
		}
		if originatingCaseID != "" && !validUUID(originatingCaseID) {
			return errors.New("originating_case_id_invalid")
		}
		token, e := sign(cfg.CoordinatorSecret, "funding-coordinator")
		if e != nil {
			return e
		}
		var created offer
		request := map[string]any{"evaluation_id": evaluationID}
		if originatingCaseID != "" {
			request["originating_case_id"] = originatingCaseID
		}
		if e = call(ctx, client, base.String(), http.MethodPost, "/api/offers", token, key, request, &created); e != nil {
			return e
		}
		fmt.Printf("SYNTHETIC DEMO offer=%s version=%d terms_hash=%s\n", created.ID, created.Version, created.TermsHash)
		return nil
	}
	if offerID == "" || subject == "" {
		return errors.New("offer_and_actor_subject_required")
	}
	roleKey, role := cfg.CustomerSecret, "CUSTOMER"
	pathSuffix := "acceptances"
	payload := map[string]any{}
	if action == "accept" {
		var current offer
		if err = call(ctx, client, base.String(), http.MethodGet, "/api/offers/"+url.PathEscape(offerID), mustSign(cfg.CustomerSecret, subject), "", nil, &current); err != nil {
			return err
		}
		payload = map[string]any{"terms_hash": current.TermsHash, "expected_version": current.Version}
	}
	if action == "approve" {
		roleKey, role, pathSuffix = cfg.OperatorSecret, "OPERATOR", "approvals"
		if reason == "" {
			return errors.New("operator_reason_required")
		}
		var current offer
		if err = call(ctx, client, base.String(), http.MethodGet, "/api/offers/"+url.PathEscape(offerID), mustSign(roleKey, subject), "", nil, &current); err != nil {
			return err
		}
		payload = map[string]any{"terms_hash": current.TermsHash, "expected_version": current.Version, "reason_code": reason}
	}
	if (action != "accept" && action != "approve") || role == "CUSTOMER" && cfg.CustomerSecret == "" {
		return errors.New("demo_actor_action_invalid")
	}
	token, err := sign(roleKey, subject)
	if err != nil {
		return err
	}
	var result map[string]any
	if err = call(ctx, client, base.String(), http.MethodPost, "/api/offers/"+url.PathEscape(offerID)+"/"+pathSuffix, token, key, payload, &result); err != nil {
		return err
	}
	fmt.Printf("SYNTHETIC DEMO decision=%s offer=%s status=recorded\n", action, offerID)
	return nil
}

func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, char := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
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

func call(ctx context.Context, client *http.Client, base, method, path, token, key string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return errors.New("request_invalid")
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, reader)
	if err != nil {
		return errors.New("request_invalid")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	res, err := client.Do(req)
	if err != nil {
		return errors.New("api_unavailable")
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 16385))
	if err != nil || len(raw) > 16384 {
		return errors.New("response_invalid")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("api_rejected_status_%d", res.StatusCode)
	}
	if out != nil && json.Unmarshal(raw, out) != nil {
		return errors.New("response_invalid")
	}
	return nil
}

func sign(secret, subject string) (string, error) {
	if len(secret) < 32 || subject == "" {
		return "", errors.New("actor_assertion_unavailable")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", errors.New("actor_assertion_unavailable")
	}
	header, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	now := time.Now().UTC()
	claims, _ := json.Marshal(map[string]any{"iss": "flowcard-go", "aud": "flowcard-funding-collection", "sub": subject, "jti": base64.RawURLEncoding.EncodeToString(nonce[:]), "iat": now.Unix(), "exp": now.Add(20 * time.Second).Unix()})
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(input))
	return input + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
func mustSign(secret, subject string) string { value, _ := sign(secret, subject); return value }
func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
