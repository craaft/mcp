package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

// protectedResourceHandler serves RFC 9728 metadata naming Craaft as the
// authorization server for this MCP endpoint.
func protectedResourceHandler(publicURL, authServer string) http.HandlerFunc {
	body, _ := json.Marshal(map[string]any{
		"resource":                 publicURL,
		"authorization_servers":    []string{strings.TrimRight(authServer, "/")},
		"scopes_supported":         []string{"craaft", "craaft:read"},
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
	userID     string
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
	r := v.lookup(ctx, token)
	return r.status, r.retryAfter
}

// lookup is check plus the user id /me reported, which verify turns into the
// TokenInfo the SDK binds a session to.
func (v *tokenValidator) lookup(ctx context.Context, token string) validation {
	sum := sha256.Sum256([]byte(token))
	key := hex.EncodeToString(sum[:])
	v.mu.Lock()
	if c, ok := v.cache[key]; ok && time.Since(c.at) < validationTTL {
		v.mu.Unlock()
		return c
	}
	v.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.baseURL+"/api/v1/me", nil)
	if err != nil {
		return validation{status: http.StatusBadGateway}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := v.client.Do(req)
	if err != nil {
		return validation{status: http.StatusBadGateway}
	}
	defer resp.Body.Close()
	res := validation{at: time.Now()}
	switch {
	case resp.StatusCode == http.StatusOK:
		res.status = http.StatusOK
		var me struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&me)
		res.userID = me.ID
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		res.status = http.StatusUnauthorized
	case resp.StatusCode == http.StatusTooManyRequests:
		return validation{status: http.StatusTooManyRequests, retryAfter: resp.Header.Get("Retry-After")} // not cached
	default:
		return validation{status: http.StatusBadGateway} // not cached
	}
	v.mu.Lock()
	if len(v.cache) >= maxCached {
		v.cache = map[string]validation{}
	}
	v.cache[key] = res
	v.mu.Unlock()
	return res
}

// tokenExtraKey is where verify keeps the raw bearer in TokenInfo.Extra, so a
// tool call can read the token of the request it arrived on.
const tokenExtraKey = "craaft_token"

// verify is the auth.TokenVerifier for /mcp. It answers from the same cache
// as check, so it adds no upstream call after requireBearer has run. The
// UserID comes from /me and lets the SDK refuse a session id used by anyone
// else. A token whose user id is unknown is refused rather than left unbound.
func (v *tokenValidator) verify(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	r := v.lookup(ctx, token)
	switch {
	case r.status == http.StatusUnauthorized:
		return nil, auth.ErrInvalidToken
	case r.status != http.StatusOK:
		return nil, fmt.Errorf("token check failed with status %d", r.status)
	case r.userID == "":
		return nil, fmt.Errorf("%w: /me returned no user id", auth.ErrInvalidToken)
	}
	// The platform does not report an expiry, so none is set. requireBearer's
	// 30 s revalidation is what ends a revoked or expired token.
	return &auth.TokenInfo{UserID: r.userID, Extra: map[string]any{tokenExtraKey: token}}, nil
}

// requireBearer answers missing / unknown / invalid tokens with 401 and the
// RFC 9728 challenge, so MCP clients start (or restart) OAuth. When a token
// was presented but rejected, the challenge also carries
// error="invalid_token" (RFC 6750 section 3.1), which tells a client to
// refresh or re-authorize rather than treat it as a first contact.
func requireBearer(v *tokenValidator, challenge string, next http.Handler) http.Handler {
	// Runs after the status handling below has accepted the token; it puts
	// the TokenInfo on the request context.
	bind := auth.RequireBearerToken(v.verify, &auth.RequireBearerTokenOptions{AllowMissingExpiration: true})(next)
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
			bind.ServeHTTP(w, r)
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
