package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestProtectedResourceMetadata(t *testing.T) {
	rec := httptest.NewRecorder()
	protectedResourceHandler("https://ai.craaft.io/mcp", "https://craaft.io")(rec, httptest.NewRequest("GET", "/.well-known/oauth-protected-resource", nil))
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	as, _ := m["authorization_servers"].([]any)
	if rec.Code != 200 || m["resource"] != "https://ai.craaft.io/mcp" || len(as) != 1 || as[0] != "https://craaft.io" ||
		rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("metadata: %d %s", rec.Code, rec.Body)
	}
}

func TestExtractToken(t *testing.T) {
	for tok, want := range map[string]string{"Bearer cra_x": "cra_x", "Bearer cro_x": "cro_x", "Bearer crr_x": "", "Bearer abc": "", "": ""} {
		r := httptest.NewRequest("POST", "/mcp", nil)
		if tok != "" {
			r.Header.Set("Authorization", tok)
		}
		if got := extractToken(r); got != want {
			t.Errorf("%q: got %q, want %q", tok, got, want)
		}
	}
}

func upstream(t *testing.T, calls *int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		if r.URL.Path != "/api/v1/me" {
			t.Errorf("unexpected upstream path %s", r.URL.Path)
		}
		switch r.Header.Get("Authorization") {
		case "Bearer cro_good", "Bearer cra_good":
			w.Write([]byte(`{"id":"u1"}`))
		case "Bearer cro_slow":
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
}

func TestMCP_revokedTokenIs401(t *testing.T) {
	var calls int32
	up := upstream(t, &calls)
	defer up.Close()
	v := newTokenValidator(up.URL)
	challenge := `Bearer resource_metadata="https://ai.craaft.io/.well-known/oauth-protected-resource/mcp"`
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	h := requireBearer(v, challenge, next)
	do := func(auth string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/mcp", strings.NewReader("{}"))
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
	// No token: the bare challenge. A presented but rejected token: the
	// challenge plus error="invalid_token".
	if rec := do(""); rec.Code != 401 || rec.Header().Get("WWW-Authenticate") != challenge {
		t.Fatalf("no token: %d %q", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
	for _, auth := range []string{"Bearer nope", "Bearer cro_revoked"} {
		rec := do(auth)
		if want := challenge + `, error="invalid_token"`; rec.Code != 401 || rec.Header().Get("WWW-Authenticate") != want {
			t.Fatalf("%q: %d %q, want %q", auth, rec.Code, rec.Header().Get("WWW-Authenticate"), want)
		}
	}
	if rec := do("Bearer cro_good"); rec.Code != 200 || rec.Body.String() != "ok" {
		t.Fatalf("good cro_: %d %s", rec.Code, rec.Body)
	}
	if rec := do("Bearer cra_good"); rec.Code != 200 {
		t.Fatalf("good cra_: %d", rec.Code)
	}
	before := atomic.LoadInt32(&calls)
	do("Bearer cro_good")
	if atomic.LoadInt32(&calls) != before {
		t.Fatal("validation result was not cached")
	}
	if rec := do("Bearer cro_slow"); rec.Code != 429 || rec.Header().Get("Retry-After") != "7" {
		t.Fatalf("upstream 429: %d %q", rec.Code, rec.Header().Get("Retry-After"))
	}
}

func TestNormalizeResource(t *testing.T) {
	for _, in := range []string{"https://ai.craaft.io/mcp", "https://ai.craaft.io/mcp/", "HTTPS://AI.Craaft.io/mcp", " https://ai.craaft.io/mcp/ "} {
		if got := normalizeResource(in); got != "https://ai.craaft.io/mcp" {
			t.Errorf("normalizeResource(%q) = %q", in, got)
		}
	}
	if normalizeResource("") != "" {
		t.Error("empty MCP_PUBLIC_URL must stay empty (OAuth off)")
	}
}
