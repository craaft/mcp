package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Without an idle timeout, sessions that clients abandon are never freed,
// and the service runs out of memory every day or two. This guards the
// setting and checks that an idle session really is closed.
func TestIdleSessionsAreClosed(t *testing.T) {
	opts := mcpHandlerOptions()
	if opts.SessionTimeout <= 0 {
		t.Fatal("SessionTimeout must be set, or abandoned sessions leak")
	}

	opts.SessionTimeout = 100 * time.Millisecond
	h := mcp.NewStreamableHTTPHandler(newServerGetter("http://localhost:0"), opts)
	srv := httptest.NewServer(withTestToken(h, "cra_testtoken"))
	t.Cleanup(srv.Close)

	ctx := context.Background()
	cl := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	cs, err := cl.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	if _, err := cs.ListTools(ctx, nil); err != nil {
		t.Fatalf("ListTools on a fresh session: %v", err)
	}

	time.Sleep(500 * time.Millisecond)
	if _, err := cs.ListTools(ctx, nil); err == nil {
		t.Fatal("an idle session was still open after its timeout")
	}
}

// withTestToken sets a bearer on requests that carry none, so the idle test
// can use the real server getter without the auth middleware.
func withTestToken(next http.Handler, token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
		next.ServeHTTP(w, r)
	})
}

// authedStack is the production chain (requireBearer, TokenInfo, the shared
// server) in front of a fake platform that records which token each
// /projects call arrived with.
type authedStack struct {
	url  string
	mu   sync.Mutex
	seen []string
}

func newAuthedStack(t *testing.T) *authedStack {
	t.Helper()
	st := &authedStack{}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		switch r.URL.Path {
		case "/api/v1/me":
			// cra_a1 and cra_a2 are two tokens of user a; cra_b is user b.
			switch tok {
			case "cra_a1", "cra_a2":
				w.Write([]byte(`{"id":"user-a"}`))
			case "cra_b":
				w.Write([]byte(`{"id":"user-b"}`))
			default:
				w.WriteHeader(http.StatusUnauthorized)
			}
		case "/api/v1/projects":
			st.mu.Lock()
			st.seen = append(st.seen, tok)
			st.mu.Unlock()
			w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected upstream path %s", r.URL.Path)
		}
	}))
	t.Cleanup(up.Close)
	h := mcp.NewStreamableHTTPHandler(newServerGetter(up.URL), mcpHandlerOptions())
	front := httptest.NewServer(requireBearer(newTokenValidator(up.URL), `Bearer realm="craaft"`, h))
	t.Cleanup(front.Close)
	st.url = front.URL
	return st
}

func (st *authedStack) tokens() []string {
	st.mu.Lock()
	defer st.mu.Unlock()
	return append([]string(nil), st.seen...)
}

// swapToken sends whatever token is currently in *cur, like a client that
// refreshes its OAuth token mid-session.
type swapToken struct {
	mu  sync.Mutex
	cur string
}

func (s *swapToken) set(t string) { s.mu.Lock(); s.cur = t; s.mu.Unlock() }
func (s *swapToken) RoundTrip(r *http.Request) (*http.Response, error) {
	s.mu.Lock()
	tok := s.cur
	s.mu.Unlock()
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+tok)
	return http.DefaultTransport.RoundTrip(r)
}

func connect(t *testing.T, url string, rt http.RoundTripper) *mcp.ClientSession {
	t.Helper()
	cl := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	cs, err := cl.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:   url,
		HTTPClient: &http.Client{Transport: rt},
		MaxRetries: -1,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func callList(t *testing.T, cs *mcp.ClientSession) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_projects"})
	if err != nil {
		t.Fatalf("call list_projects: %v", err)
	}
	if res.IsError {
		t.Fatalf("list_projects returned a tool error: %+v", res.Content)
	}
}

// After an OAuth refresh the client sends a new token on the same session.
// Tool calls must use that token, not the one the session started with.
func TestSessionUsesCurrentRequestToken(t *testing.T) {
	st := newAuthedStack(t)
	rt := &swapToken{cur: "cra_a1"}
	cs := connect(t, st.url, rt)

	callList(t, cs)
	rt.set("cra_a2")
	callList(t, cs)

	got := st.tokens()
	if len(got) != 2 || got[0] != "cra_a1" || got[1] != "cra_a2" {
		t.Fatalf("upstream tokens = %v, want [cra_a1 cra_a2]", got)
	}
}

// A valid token for another user must not drive someone else's session.
func TestSessionRefusesOtherUsersToken(t *testing.T) {
	st := newAuthedStack(t)
	rt := &swapToken{cur: "cra_a1"}
	cs := connect(t, st.url, rt)
	callList(t, cs)

	rt.set("cra_b")
	_, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_projects"})
	if err == nil {
		t.Fatal("user b's token was accepted on user a's session")
	}
	if got := st.tokens(); len(got) != 1 {
		t.Fatalf("upstream saw %v, want only the first call", got)
	}
}

// The streamable handler asks for a server on every HTTP request. It must be
// the same one each time, not a rebuild of all the tools.
func TestServerIsBuiltOnce(t *testing.T) {
	get := newServerGetter("http://localhost:0")
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set("Authorization", "Bearer cra_x")
	first := get(r)
	if first == nil {
		t.Fatal("no server for a request with a token")
	}
	if get(r) != first {
		t.Fatal("getServer built a new server for a second request")
	}
	if get(httptest.NewRequest("POST", "/mcp", nil)) != nil {
		t.Fatal("a request without a token got a server")
	}
}

func TestRunServerShutsDownOnCancel(t *testing.T) {
	srv := newHTTPServer("127.0.0.1:0", http.NewServeMux())
	if srv.ReadHeaderTimeout <= 0 || srv.IdleTimeout <= 0 {
		t.Fatal("ReadHeaderTimeout and IdleTimeout must be set")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runServer(ctx, srv, time.Second) }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runServer: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runServer did not return after cancel")
	}
}

func TestClientHasTimeouts(t *testing.T) {
	c := NewClient("http://x", "t")
	if c.http.Timeout <= 0 || c.httpLong.Timeout <= c.http.Timeout {
		t.Fatalf("timeouts: %v / %v", c.http.Timeout, c.httpLong.Timeout)
	}
}
