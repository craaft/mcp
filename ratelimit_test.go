package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// limitedServer answers 429 (with the given Retry-After, omitted if empty)
// for the first n requests, recording every body, then 200 with {}.
func limitedServer(t *testing.T, n int, retryAfter string, bodies *[]string) (*Client, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*bodies = append(*bodies, string(b))
		if int(atomic.AddInt32(&hits, 1)) <= n {
			if retryAfter != "" {
				w.Header().Set("Retry-After", retryAfter)
			}
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"rate limit exceeded for this api key"}`))
			return
		}
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, "cra_testtoken"), &hits
}

func recordSleeps(c *Client) *[]time.Duration {
	var waits []time.Duration
	c.sleep = func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}
	return &waits
}

func TestRateLimitRetriesAndResendsBody(t *testing.T) {
	var bodies []string
	c, hits := limitedServer(t, 1, "2", &bodies)
	waits := recordSleeps(c)

	if _, _, err := c.do(context.Background(), http.MethodPost, "/x", map[string]string{"a": "b"}); err != nil {
		t.Fatalf("expected success after retry, got %v", err)
	}
	if *hits != 2 || len(bodies) != 2 || bodies[0] != bodies[1] || bodies[0] == "" {
		t.Fatalf("hits=%d bodies=%q, want 2 identical non-empty bodies", *hits, bodies)
	}
	if len(*waits) != 1 || (*waits)[0] != 2*time.Second {
		t.Fatalf("waits=%v, want [2s]", *waits)
	}
}

func TestRateLimitUploadResendsMultipartBody(t *testing.T) {
	var bodies []string
	c, hits := limitedServer(t, 2, "0", &bodies)
	recordSleeps(c)
	if _, err := c.UploadAttachment(context.Background(), "c1", "a.txt", "text/plain", []byte("hello")); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if *hits != 3 || bodies[0] != bodies[2] || bodies[0] == "" {
		t.Fatalf("hits=%d, bodies not resent identically", *hits)
	}
}

func TestRateLimitDownloadRetries(t *testing.T) {
	var bodies []string
	c, hits := limitedServer(t, 1, "", &bodies)
	waits := recordSleeps(c)
	if _, _, err := c.DownloadAttachment(context.Background(), "a1"); err != nil {
		t.Fatalf("download: %v", err)
	}
	if *hits != 2 || (*waits)[0] != defaultRetryWait {
		t.Fatalf("hits=%d waits=%v, want default 1s wait", *hits, *waits)
	}
}

func TestRateLimitWaitClamped(t *testing.T) {
	var bodies []string
	c, _ := limitedServer(t, 1, "3600", &bodies)
	waits := recordSleeps(c)
	if _, _, err := c.do(context.Background(), http.MethodGet, "/x", nil); err != nil {
		t.Fatal(err)
	}
	if (*waits)[0] != maxRetryWait {
		t.Fatalf("waits=%v, want clamp to %v", *waits, maxRetryWait)
	}
}

func TestRateLimitGivesUpAfterCap(t *testing.T) {
	var bodies []string
	c, hits := limitedServer(t, 100, "0", &bodies)
	recordSleeps(c)
	_, status, err := c.do(context.Background(), http.MethodGet, "/x", nil)
	if err == nil || status != http.StatusTooManyRequests || err.Error() != "rate limit exceeded for this api key" {
		t.Fatalf("status=%d err=%v, want the 429 error", status, err)
	}
	if int(*hits) != maxRateLimitRetries+1 {
		t.Fatalf("hits=%d, want %d", *hits, maxRateLimitRetries+1)
	}
}

func TestRateLimitContextCancelDuringWait(t *testing.T) {
	var bodies []string
	c, hits := limitedServer(t, 100, "5", &bodies)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	start := time.Now()
	_, _, err := c.do(ctx, http.MethodGet, "/x", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want context.Canceled", err)
	}
	if time.Since(start) > 2*time.Second || *hits != 1 {
		t.Fatalf("took %v with %d hits, want prompt return after 1 hit", time.Since(start), *hits)
	}
}

func TestRateLimitSkipsWaitPastDeadline(t *testing.T) {
	var bodies []string
	c, hits := limitedServer(t, 100, "5", &bodies)
	waits := recordSleeps(c)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, status, err := c.do(ctx, http.MethodGet, "/x", nil)
	if err == nil || status != http.StatusTooManyRequests || *hits != 1 || len(*waits) != 0 {
		t.Fatalf("status=%d err=%v hits=%d waits=%v, want immediate 429", status, err, *hits, *waits)
	}
}
