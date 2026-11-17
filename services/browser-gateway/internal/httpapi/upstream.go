package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
)

// forward copies only selected content headers, never browser identity. A zero
// response limit is reserved for streamed onboarding document responses.
func (a *Server) forward(ctx context.Context, w http.ResponseWriter, r *http.Request, base *url.URL, token string, requestLimit, responseLimit int64) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, requestLimit))
	if err != nil {
		fail(w, 413, "request too large")
		return
	}
	target := *base
	target.Path, target.RawQuery = r.URL.Path, r.URL.RawQuery
	upstream, err := http.NewRequestWithContext(ctx, r.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		fail(w, 502, "upstream unavailable")
		return
	}
	for _, name := range []string{"Accept", "Content-Type", "Idempotency-Key", "If-Match", "If-None-Match", "Range"} {
		if values := r.Header.Values(name); len(values) > 0 {
			upstream.Header[name] = append([]string(nil), values...)
		}
	}
	upstream.Header.Set("Authorization", "Bearer "+token)
	response, err := a.upstream.Do(upstream)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			fail(w, 504, "upstream timeout")
		} else {
			fail(w, 502, "upstream unavailable")
		}
		return
	}
	defer response.Body.Close()
	if responseLimit > 0 && (response.StatusCode == 401 || response.StatusCode == 403) {
		// Browser identity was already verified. A reader credential failure is
		// a service configuration outage, not a reason to discard that session.
		fail(w, 503, "financial source unavailable")
		return
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		fail(w, 502, "upstream redirect refused")
		return
	}
	var bounded []byte
	if responseLimit > 0 {
		bounded, err = io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
		if err != nil || int64(len(bounded)) > responseLimit {
			fail(w, 502, "upstream response unavailable")
			return
		}
	}
	for _, name := range []string{"Content-Type", "Content-Disposition", "Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Retry-After"} {
		if values := response.Header.Values(name); len(values) > 0 {
			w.Header()[name] = append([]string(nil), values...)
		}
	}
	// Location, Set-Cookie, WWW-Authenticate and identity headers remain private.
	w.WriteHeader(response.StatusCode)
	if responseLimit > 0 {
		_, _ = w.Write(bounded) // The client may disconnect after headers are sent.
		return
	}
	if _, err := io.Copy(w, response.Body); err != nil {
		panic(http.ErrAbortHandler)
	}
}
