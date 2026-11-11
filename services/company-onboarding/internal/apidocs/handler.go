package apidocs

import (
	"embed"
	"net/http"
	"path"
	"path/filepath"
	"strings"
)

//go:embed specs/openapi.json
var embeddedOperatorSpec []byte

//go:embed specs/webhooks.json
var embeddedWebhookSpec []byte

//go:embed assets/*
var embeddedAssets embed.FS

// Handler returns an http.Handler that serves the offline read-only Swagger UI,
// embedded OpenAPI specifications, and bundled assets under /docs/api.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Only allow GET and HEAD methods; reject mutating methods with 405 Method Not Allowed
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusMethodNotAllowed)
			if r.Method != http.MethodHead {
				_, _ = w.Write([]byte("405 Method Not Allowed\n"))
			}
			return
		}

		// 2. Standard security headers for all documentation responses
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; font-src 'self' data:; frame-ancestors 'none'; object-src 'none'; base-uri 'self'")

		// 3. Clean and normalize request path
		reqPath := r.URL.Path

		// Exact prefix guard: direct out-of-prefix requests must return 404
		if reqPath != "/docs/api" && !strings.HasPrefix(reqPath, "/docs/api/") {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			if r.Method != http.MethodHead {
				_, _ = w.Write([]byte("404 page not found\n"))
			}
			return
		}

		// Redirect /docs/api (without trailing slash) to /docs/api/
		if reqPath == "/docs/api" {
			target := "/docs/api/"
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, target, http.StatusMovedPermanently)
			return
		}

		// 4. Strict path traversal and encoding guard
		if strings.Contains(reqPath, "..") || strings.Contains(r.URL.RawPath, "..") {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			if r.Method != http.MethodHead {
				_, _ = w.Write([]byte("400 bad request\n"))
			}
			return
		}

		// 5. Extract relative sub-path under /docs/api/
		subPath := strings.TrimPrefix(reqPath, "/docs/api/")
		subPath = strings.TrimPrefix(subPath, "assets/")
		subPath = path.Clean(subPath)

		// 6. Route dispatch
		switch subPath {
		case ".", "", "index.html":
			data, err := embeddedAssets.ReadFile("assets/index.html")
			if err != nil {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusInternalServerError)
				if r.Method != http.MethodHead {
					_, _ = w.Write([]byte("500 internal server error\n"))
				}
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache, must-revalidate")
			if r.Method == http.MethodHead {
				return
			}
			_, _ = w.Write(data)
			return

		case "openapi.json":
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache, must-revalidate")
			if r.Method == http.MethodHead {
				return
			}
			_, _ = w.Write(embeddedOperatorSpec)
			return

		case "webhooks.json":
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache, must-revalidate")
			if r.Method == http.MethodHead {
				return
			}
			_, _ = w.Write(embeddedWebhookSpec)
			return
		}

		// 7. Lookup asset strictly from embeddedAssets
		data, err := embeddedAssets.ReadFile("assets/" + subPath)
		if err != nil {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			if r.Method != http.MethodHead {
				_, _ = w.Write([]byte("404 page not found\n"))
			}
			return
		}

		var contentType string
		switch filepath.Ext(subPath) {
		case ".css":
			contentType = "text/css; charset=utf-8"
		case ".js":
			contentType = "application/javascript; charset=utf-8"
		case ".png":
			contentType = "image/png"
		case ".json":
			contentType = "application/json; charset=utf-8"
		default:
			contentType = "text/plain; charset=utf-8"
		}

		w.Header().Set("Content-Type", contentType)
		// Unversioned assets use no-cache, must-revalidate to avoid stale client caching
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(data)
	})
}
