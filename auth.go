package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

// protectedResourceHandler serves RFC 9728 metadata naming Craaft as the
// authorization server for this MCP endpoint.
func protectedResourceHandler(publicURL, authServer string) http.HandlerFunc {
	body, _ := json.Marshal(map[string]any{
		"resource":                 publicURL,
		"authorization_servers":    []string{strings.TrimRight(authServer, "/")},
		"scopes_supported":         []string{"craaft"},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "Craaft",
	})
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Write(body)
	}
}

// validation is a cached upstream check result.
type validation struct {
	status     int
	retryAfter string
	at         time.Time
}

// tokenValidator checks a bearer against GET /api/v1/me, caching results
// per sha256(token) for 30 s so a busy session costs one upstream call per
// half minute. The cache is capped; when full it is reset.
type tokenValidator struct {
	baseURL string
	client  *http.Client
	mu      sync.Mutex
	cache   map[string]validation
}

const (
	validationTTL = 30 * time.Second
	maxCached     = 10000
)

func newTokenValidator(baseURL string) *tokenValidator {
	return &tokenValidator{baseURL: strings.TrimRight(baseURL, "/"), client: &http.Client{Timeout: 10 * time.Second}, cache: map[string]validation{}}
}

// check returns 200 for a valid token, 401 for an invalid one, 429 (with
// Retry-After) when upstream is rate limiting, and 502 on upstream failure.
func (v *tokenValidator) check(ctx context.Context, token string) (int, string) {
	sum := sha256.Sum256([]byte(token))
	key := hex.EncodeToString(sum[:])
	v.mu.Lock()
	if c, ok := v.cache[key]; ok && time.Since(c.at) < validationTTL {
		v.mu.Unlock()
		return c.status, c.retryAfter
	}
	v.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.baseURL+"/api/v1/me", nil)
	if err != nil {
		return http.StatusBadGateway, ""
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := v.client.Do(req)
	if err != nil {
		return http.StatusBadGateway, ""
	}
	resp.Body.Close()
	res := validation{at: time.Now()}
	switch {
	case resp.StatusCode == http.StatusOK:
		res.status = http.StatusOK
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		res.status = http.StatusUnauthorized
	case resp.StatusCode == http.StatusTooManyRequests:
		return http.StatusTooManyRequests, resp.Header.Get("Retry-After") // not cached
	default:
		return http.StatusBadGateway, "" // not cached
	}
	v.mu.Lock()
	if len(v.cache) >= maxCached {
		v.cache = map[string]validation{}
	}
	v.cache[key] = res
	v.mu.Unlock()
	return res.status, ""
}

// requireBearer answers missing / unknown / invalid tokens with 401 and the
// RFC 9728 challenge, so MCP clients start (or restart) OAuth. When a token
// was presented but rejected, the challenge also carries
// error="invalid_token" (RFC 6750 section 3.1), which tells a client to
// refresh or re-authorize rather than treat it as a first contact.
func requireBearer(v *tokenValidator, challenge string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deny := func() {
			c := challenge
			if r.Header.Get("Authorization") != "" {
				c += `, error="invalid_token"`
			}
			w.Header().Set("WWW-Authenticate", c)
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		}
		tok := extractToken(r)
		if tok == "" {
			deny()
			return
		}
		switch status, retry := v.check(r.Context(), tok); status {
		case http.StatusOK:
			next.ServeHTTP(w, r)
		case http.StatusUnauthorized:
			deny()
		case http.StatusTooManyRequests:
			if retry != "" {
				w.Header().Set("Retry-After", retry)
			}
			http.Error(w, `{"error":"rate limited"}`, http.StatusTooManyRequests)
		default:
			http.Error(w, `{"error":"upstream unavailable"}`, http.StatusBadGateway)
		}
	})
}
