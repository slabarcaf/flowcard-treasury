package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/go-coordinator/internal/httpapi"
)

const maxResponseBytes = 1 << 20

func main() {
	caseID := flag.String("case", "", "coordinator case ID")
	interval := flag.Duration("interval", 2*time.Second, "poll interval")
	once := flag.Bool("once", false, "fetch one detail response")
	flag.Parse()
	if !validUUID(*caseID) || *interval < 250*time.Millisecond || *interval > time.Minute {
		fatal(errors.New("case_or_interval_invalid"))
	}
	secret := os.Getenv("COORDINATOR_READER_SECRET")
	if len(strings.TrimSpace(secret)) < 32 {
		fatal(errors.New("coordinator_reader_auth_configuration_invalid"))
	}
	address := strings.TrimSpace(os.Getenv("COORDINATOR_HTTP_ADDR"))
	if address == "" {
		address = "127.0.0.1:8084"
	}
	base, err := localBaseURL(address)
	if err != nil {
		fatal(err)
	}
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var lastHash string
	for {
		body, err := readCase(ctx, client, base, secret, *caseID)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			if *once {
				os.Exit(1)
			}
		} else {
			sum := sha256.Sum256(body)
			hash := hex.EncodeToString(sum[:])
			if hash != lastHash {
				var pretty any
				if json.Unmarshal(body, &pretty) != nil {
					fatal(errors.New("coordinator_response_invalid"))
				}
				encoder := json.NewEncoder(os.Stdout)
				encoder.SetIndent("", "  ")
				if err := encoder.Encode(pretty); err != nil {
					fatal(errors.New("coordinator_output_failed"))
				}
				lastHash = hash
			}
			if *once || terminal(body) {
				return
			}
		}
		if *once {
			return
		}
		timer := time.NewTimer(*interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func readCase(ctx context.Context, client *http.Client, base *url.URL, secret, caseID string) ([]byte, error) {
	token, err := httpapi.SignReaderAssertion(secret, time.Now())
	if err != nil {
		return nil, err
	}
	target := *base
	target.Path = strings.TrimRight(target.Path, "/") + "/api/coordinator/cases/" + url.PathEscape(caseID)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, errors.New("coordinator_request_invalid")
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("coordinator_unavailable")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		return nil, errors.New("coordinator_response_too_large")
	}
	if response.StatusCode != http.StatusOK {
		return nil, errors.New(safeHTTPCode(response.StatusCode, body))
	}
	return body, nil
}

func localBaseURL(address string) (*url.URL, error) {
	if strings.Contains(address, "://") {
		u, err := url.ParseRequestURI(address)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, errors.New("coordinator_endpoint_invalid")
		}
		host := u.Hostname()
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, errors.New("coordinator_endpoint_must_be_loopback")
		}
		return u, nil
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || port == "" {
		return nil, errors.New("coordinator_endpoint_invalid")
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, errors.New("coordinator_endpoint_must_be_loopback")
	}
	return url.Parse("http://" + net.JoinHostPort(host, port))
}

func safeHTTPCode(status int, body []byte) string {
	var value struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &value) == nil && value.Error.Code != "" && len(value.Error.Code) <= 80 {
		return value.Error.Code
	}
	return fmt.Sprintf("coordinator_http_%d", status)
}

func terminal(body []byte) bool {
	var value struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(body, &value) != nil {
		return false
	}
	return value.Status == "COMPLETED" || value.Status == "CLOSED" || value.Status == "HELD"
}

func validUUID(value string) bool {
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

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
