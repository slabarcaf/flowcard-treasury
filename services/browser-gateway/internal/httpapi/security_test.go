package httpapi

import (
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/browser-gateway/internal/config"
)

func TestOriginAndBodyDenials(t *testing.T) {
	a := Server{config: config.Config{Origins: map[string]bool{"https://app.example": true}}}
	for _, tt := range []struct {
		method         string
		origin         []string
		fetch, referer string
		want           bool
	}{
		{"POST", []string{"https://app.example"}, "", "", true},
		{"POST", nil, "same-origin", "https://app.example/path", false},
		{"GET", nil, "same-origin", "https://app.example/path", true},
		{"HEAD", nil, "same-origin", "https://app.example/path", true},
		{"GET", []string{""}, "same-origin", "https://app.example/path", false},
		{"GET", []string{"null"}, "same-origin", "https://app.example/path", false},
		{"GET", []string{"https://evil.example"}, "same-origin", "https://app.example/path", false},
		{"GET", nil, "same-origin", "https://user:pass@app.example/path", false},
		{"GET", nil, "same-site", "https://app.example/path", false},
		{"GET", []string{"https://app.example", "https://app.example"}, "", "", false},
	} {
		r := httptest.NewRequest(tt.method, "https://app.example/api/session/me", nil)
		if tt.origin != nil {
			r.Header["Origin"] = tt.origin
		}
		r.Header.Set("Sec-Fetch-Site", tt.fetch)
		r.Header.Set("Referer", tt.referer)
		if a.allowed(r) != tt.want {
			t.Errorf("origin boundary accepted=%v for %+v", !tt.want, tt)
		}
	}
	for _, body := range []string{
		`{"username":"alice","password":"long enough password","role":"OPERATOR"}`,
		`{"username":"alice","username":"bob","password":"long enough password"}`,
		`{"username":"alice","password":"long enough password"} {}`,
		`{"username":"alice","password":null}`,
		`{"username":"alice","password":"unterminated`,
		`{"username":"alice","password":"` + strings.Repeat("x", 9000) + `"}`,
	} {
		r := httptest.NewRequest("POST", "/api/session/login", strings.NewReader(body))
		if _, _, ok := credentials(httptest.NewRecorder(), r); ok {
			t.Error("invalid credential body accepted")
		}
	}
}

func TestLimiterBounds(t *testing.T) {
	now := time.Now()
	var l limiter
	for i := 0; i < 5; i++ {
		if !l.allow("alice", "peer", now) {
			t.Fatal("early user limit")
		}
	}
	if l.allow("alice", "different", now) {
		t.Fatal("user limit bypassed by peer change")
	}
	for i := 0; i < 15; i++ {
		if !l.allow(string(rune('b'+i)), "peer", now) {
			t.Fatal("early peer limit")
		}
	}
	if l.allow("newuser", "peer", now) {
		t.Fatal("peer limit bypassed by username change")
	}
	if !l.allow("alice", "peer", now.Add(time.Minute)) {
		t.Fatal("TTL did not release bucket")
	}
	var full limiter
	for i := 0; i < 500; i++ {
		if !full.allow(string(rune(i)), string(rune(i)), now) {
			t.Fatal("early capacity limit")
		}
	}
	if full.allow("extra", "extra", now) || len(full.entries) != 1000 {
		t.Fatal("capacity bound exceeded")
	}
	if !full.allow("extra", "extra", now.Add(time.Minute)) {
		t.Fatal("expired capacity did not recover")
	}
	var concurrent limiter
	var admitted atomic.Int32
	var workers sync.WaitGroup
	for range 64 {
		workers.Go(func() {
			if concurrent.allow("same.user", "same.peer", now) {
				admitted.Add(1)
			}
		})
	}
	workers.Wait()
	if got := admitted.Load(); got != 5 {
		t.Errorf("concurrent admitted=%d, want 5", got)
	}
}

func TestFinancialCommandBodyBoundary(t *testing.T) {
	hash := strings.Repeat("a", 64)
	for _, body := range []string{
		`{"terms_hash":"` + hash + `","expected_version":1,"actor_id":"forged"}`,
		`{"terms_hash":"` + hash + `","expected_version":1,"Expected_Version":2}`,
		`{"terms_hash":"` + hash + `","expected_version":1,"expected_version":2}`,
		`{"terms_hash":"` + hash + `","expected_version":1.5}`,
		`{"terms_hash":"` + hash + `","expected_version":9007199254740992}`,
		`{"terms_hash":"` + hash + `","expected_version":1} {}`,
	} {
		request := httptest.NewRequest("POST", "/api/offers/22222222-2222-4222-8222-222222222222/acceptances", strings.NewReader(body))
		request.Pattern = "POST /api/offers/{id}/acceptances"
		if _, err := commandBody(httptest.NewRecorder(), request); err == nil {
			t.Fatal("ambiguous/authority-supplying financial body accepted")
		}
	}
	request := httptest.NewRequest("POST", "/api/offers/id/acceptances", strings.NewReader(`{"expected_version":7,"terms_hash":"`+hash+`"}`))
	request.Pattern = "POST /api/offers/{id}/acceptances"
	body, err := commandBody(httptest.NewRecorder(), request)
	if err != nil || string(body) != `{"terms_hash":"`+hash+`","expected_version":7}` {
		t.Fatal("valid command not canonicalized exactly")
	}
}
