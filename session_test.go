package main

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	c := NewClient("http://localhost:0", "cra_testtoken")
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		s := mcp.NewServer(&mcp.Implementation{Name: "craaft-mcp-test", Version: "test"}, nil)
		registerTools(s, c)
		return s
	}, opts)
	srv := httptest.NewServer(h)
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
