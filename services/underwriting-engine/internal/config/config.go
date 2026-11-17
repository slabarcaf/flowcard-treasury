// Package config loads server-owned underwriting configuration.
package config

import (
	"errors"
	"net"
	"net/url"
	"os"
	"strings"
)

// SnapshotImport contains the restricted connection and configured source scope.
type SnapshotImport struct {
	DatabaseURL string
	Mode        string
	FacilityID  string
}

// Runtime contains the API's server-owned settings and dedicated secrets.
type Runtime struct {
	SnapshotImport
	HTTPAddress            string
	ServiceSecret          string
	ShortfallBaseURL       string
	ShortfallServiceSecret string
}

// LoadSnapshotImport reads only settings needed by the local importer.
func LoadSnapshotImport() (SnapshotImport, error) {
	config := SnapshotImport{
		DatabaseURL: strings.TrimSpace(os.Getenv("DATABASE_URL")),
		Mode:        strings.TrimSpace(os.Getenv("UNDERWRITING_MODE")),
		FacilityID:  strings.TrimSpace(os.Getenv("UNDERWRITING_FACILITY_ID")),
	}
	if config.DatabaseURL == "" || (config.Mode != "DEMO" && config.Mode != "SANDBOX") ||
		config.FacilityID == "" {
		return SnapshotImport{}, errors.New("snapshot_import_configuration_invalid")
	}
	return config, nil
}

// LoadRuntime reads and validates API configuration without printing credentials.
func LoadRuntime() (Runtime, error) {
	importConfig, err := LoadSnapshotImport()
	if err != nil {
		return Runtime{}, errors.New("runtime_configuration_invalid")
	}
	runtime := Runtime{
		SnapshotImport:         importConfig,
		HTTPAddress:            strings.TrimSpace(os.Getenv("UNDERWRITING_HTTP_ADDR")),
		ServiceSecret:          os.Getenv("UNDERWRITING_SERVICE_SECRET"),
		ShortfallBaseURL:       strings.TrimSpace(os.Getenv("SHORTFALL_BASE_URL")),
		ShortfallServiceSecret: os.Getenv("SHORTFALL_SERVICE_SECRET"),
	}
	if runtime.HTTPAddress == "" {
		runtime.HTTPAddress = "127.0.0.1:8083"
	}
	if len(runtime.ServiceSecret) < 32 || len(runtime.ShortfallServiceSecret) < 32 ||
		!validDependencyURL(runtime.ShortfallBaseURL) {
		return Runtime{}, errors.New("runtime_configuration_invalid")
	}
	return runtime, nil
}

func validDependencyURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || !parsed.IsAbs() || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(value, "#") {
		return false
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "https" && scheme != "http" {
		return false
	}
	if scheme == "https" || strings.EqualFold(parsed.Hostname(), "localhost") {
		return true
	}
	ip := net.ParseIP(parsed.Hostname())
	return ip != nil && ip.IsLoopback()
}
