package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestClient spins a stub API that records the raw request URI it was
// asked for, so assertions can be made about what actually went on the wire
// rather than about what the client meant to send.
func newTestClient(t *testing.T, seen *string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"cards": []any{}})
	}))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, "cra_testtoken")
}

// Each of these was a real, reproduced failure before the query was escaped:
//
//   - "#design"  the fragment marker truncated the query, so the server saw
//     q="" and returned zero results with no error. Worst case: a silently
//     wrong answer to a query people genuinely type, since the app renders
//     tags as "#design".
//   - "50% off"  produced an invalid percent-escape; Cloud Run's proxy
//     rejected the request with a 400 HTML page before it reached the app.
//   - "a&b"      injected a stray query parameter, so the server searched
//     for "a" and silently returned the wrong result set.
func TestSearchCardsEscapesQuery(t *testing.T) {
	for _, tc := range []struct{ name, query, wantURI string }{
		{"fragment marker", "#design", "/api/v1/search?limit=20&q=%23design"},
		{"percent", "50% off", "/api/v1/search?limit=20&q=50%25+off"},
		{"ampersand", "a&b", "/api/v1/search?limit=20&q=a%26b"},
		{"space", "new card", "/api/v1/search?limit=20&q=new+card"},
		{"plus", "a+b", "/api/v1/search?limit=20&q=a%2Bb"},
		{"equals", "size=5", "/api/v1/search?limit=20&q=size%3D5"},
		{"unicode", "café", "/api/v1/search?limit=20&q=caf%C3%A9"},
		{"plain", "onboarding", "/api/v1/search?limit=20&q=onboarding"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var seen string
			c := newTestClient(t, &seen)
			if _, err := c.SearchCards(context.Background(), tc.query, 20); err != nil {
				t.Fatalf("SearchCards(%q) returned an error: %v", tc.query, err)
			}
			if seen != tc.wantURI {
				t.Errorf("SearchCards(%q)\n sent %q\n want %q", tc.query, seen, tc.wantURI)
			}
		})
	}
}

// Omitting the limit must omit the parameter, not send limit=0 - the server
// treats an out-of-range limit as "use the default", so limit=0 would work
// by accident rather than by intent.
func TestSearchCardsOmitsZeroLimit(t *testing.T) {
	var seen string
	c := newTestClient(t, &seen)
	if _, err := c.SearchCards(context.Background(), "x", 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seen != "/api/v1/search?q=x" {
		t.Errorf("sent %q, want %q", seen, "/api/v1/search?q=x")
	}
}

// Ids arrive as tool arguments and nothing validates that they are uuids. An
// id carrying a slash or a question mark would rewrite the request path.
func TestPathSegmentsAreEscaped(t *testing.T) {
	var seen string
	c := newTestClient(t, &seen)
	// GetBoard issues two requests; the second overwrites `seen`, so assert
	// on the cards call it ends with.
	_, _, _ = c.GetBoard(context.Background(), "../../admin/users")
	const want = "/api/v1/projects/..%2F..%2Fadmin%2Fusers/cards"
	if seen != want {
		t.Errorf("sent %q, want %q", seen, want)
	}
}

func TestGetCardDetailEscapesPathAndDecodes(t *testing.T) {
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"card":        map[string]any{"id": "c1", "title": "Hello", "description": "full body"},
			"comments":    []any{},
			"events":      []any{},
			"checklist":   []any{},
			"attachments": []any{},
		})
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "cra_testtoken")

	d, err := c.GetCardDetail(context.Background(), "../../admin/users")
	if err != nil {
		t.Fatalf("GetCardDetail: %v", err)
	}
	if seen != "/api/v1/cards/..%2F..%2Fadmin%2Fusers/detail" {
		t.Errorf("sent %q, want escaped /detail path", seen)
	}
	if d.Card.Title != "Hello" || d.Card.Description != "full body" {
		t.Errorf("card = %+v", d.Card)
	}
	if d.Comments == nil || d.Events == nil || d.Checklist == nil || d.Attachments == nil {
		t.Errorf("nil slices in detail: comments=%v events=%v checklist=%v atts=%v",
			d.Comments, d.Events, d.Checklist, d.Attachments)
	}
}

// Board-list cards omit description. Re-encoding them must not invent an
// empty body, or an agent would think every card is blank.
func TestBoardCardOmitsEmptyDescription(t *testing.T) {
	var card Card
	if err := json.Unmarshal([]byte(`{"id":"1","title":"A","column":"todo"}`), &card); err != nil {
		t.Fatal(err)
	}
	if card.Description != "" {
		t.Fatalf("unmarshalled description = %q, want empty", card.Description)
	}
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["description"]; ok {
		t.Errorf("re-encoded board card includes description: %s", raw)
	}
}
