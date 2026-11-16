package main

import (
	"bufio"
	"errors"
	"io"
	"os"
	"strings"
)

const sandboxHost = "https://api.sandbox.airwallex.com"
const maxCredentialFile = 1 << 20

type credentials struct {
	clientID      string
	apiKey        string
	platform      string
	webhookSecret string
	webhookID     string
}

func loadCredentials(path string) (credentials, error) {
	f, err := os.Open(path)
	if err != nil {
		return credentials{}, errors.New("sandbox_credentials_unavailable")
	}
	defer f.Close()
	return readCredentials(f)
}

// readCredentials accepts literal dotenv assignments, never shell execution or expansion.
// Unrelated root settings, including old cached tokens, are deliberately ignored.
func readCredentials(r io.Reader) (credentials, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxCredentialFile+1))
	if err != nil || len(raw) > maxCredentialFile {
		return credentials{}, errors.New("sandbox_credentials_invalid")
	}
	wanted := map[string]bool{"AIRWALLEX_CLIENT_ID": true, "AIRWALLEX_API_KEY": true, "AIRWALLEX_ACCOUNT_ID": true, "AIRWALLEX_BASE_URL": true, "AIRWALLEX_WEBHOOK_SECRET": true, "AIRWALLEX_WEBHOOK_ID": true}
	values := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	scanner.Buffer(make([]byte, 4096), maxCredentialFile)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		key, val, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || !wanted[key] {
			continue
		}
		if _, exists := values[key]; exists {
			return credentials{}, errors.New("sandbox_credentials_duplicate")
		}
		val = strings.TrimSpace(val)
		if len(val) > 0 && (val[0] == '\'' || val[0] == '"') {
			quote := val[0]
			end := strings.IndexByte(val[1:], quote)
			if end < 0 {
				return credentials{}, errors.New("sandbox_credentials_invalid")
			}
			end++
			suffix := strings.TrimSpace(val[end+1:])
			if suffix != "" && !strings.HasPrefix(suffix, "#") {
				return credentials{}, errors.New("sandbox_credentials_invalid")
			}
			val = val[1:end]
		} else {
			if i := strings.Index(val, " #"); i >= 0 {
				val = strings.TrimSpace(val[:i])
			}
		}
		if strings.ContainsAny(val, "\r\n\x00") || strings.Contains(val, "${") || strings.Contains(val, "$(") || strings.Contains(val, "`") {
			return credentials{}, errors.New("sandbox_credentials_not_literal")
		}
		values[key] = val
	}
	if scanner.Err() != nil {
		return credentials{}, errors.New("sandbox_credentials_invalid")
	}
	if base := values["AIRWALLEX_BASE_URL"]; base != "" && base != sandboxHost {
		return credentials{}, errors.New("sandbox_host_mismatch")
	}
	cfg := credentials{clientID: values["AIRWALLEX_CLIENT_ID"], apiKey: values["AIRWALLEX_API_KEY"], platform: values["AIRWALLEX_ACCOUNT_ID"], webhookSecret: values["AIRWALLEX_WEBHOOK_SECRET"], webhookID: values["AIRWALLEX_WEBHOOK_ID"]}
	if cfg.clientID == "" || cfg.apiKey == "" || cfg.platform == "" {
		return credentials{}, errors.New("sandbox_credentials_incomplete")
	}
	return cfg, nil
}
