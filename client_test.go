package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

// A cross-board move carries board names in fromName/toName and column
// titles in fromDetail/toDetail; a board the caller can't see has neither.
func TestGetCardDetailDecodesBoardMove(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"card":     map[string]any{"id": "c1", "title": "Hello"},
			"comments": []any{},
			"events": []any{
				map[string]any{
					"id": "e1", "type": "moved_board", "fromValue": "p1:todo", "toValue": "p2:done",
					"fromName": "Product Launch", "toName": "Website Redesign",
					"fromDetail": "To Do", "toDetail": "Done", "createdAt": "2026-09-28T10:00:00Z",
				},
				map[string]any{"id": "e2", "type": "moved_board", "fromValue": "p9:todo", "toValue": "p2:todo", "createdAt": "2026-09-28T11:00:00Z"},
			},
			"checklist":   []any{},
			"attachments": []any{},
		})
	}))
	t.Cleanup(srv.Close)
	d, err := NewClient(srv.URL, "cra_testtoken").GetCardDetail(context.Background(), "c1")
	if err != nil {
		t.Fatalf("GetCardDetail: %v", err)
	}
	e := d.Events[0]
	if e.Type != "moved_board" || e.FromDetail == nil || *e.FromDetail != "To Do" || e.ToDetail == nil || *e.ToDetail != "Done" {
		t.Errorf("board move = %+v", e)
	}
	if d.Events[1].FromName != nil || d.Events[1].FromDetail != nil {
		t.Errorf("hidden board leaked names: %+v", d.Events[1])
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

// POST /invitations answers a wrapper, {"invitation": {...}, "consumed":
// bool} - not a bare Invitation. Decoding the wrapper directly as an
// Invitation used to silently produce an empty id/email/etc; this was a
// real, reproduced bug in every SDK built against the old spec.
func TestInviteMemberUnwrapsEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"invitation": map[string]any{
				"id":            "inv-1",
				"email":         "sam@example.com",
				"role":          "member",
				"invitedBy":     "u1",
				"invitedByName": "Owner",
				"createdAt":     "2026-09-28T00:00:00Z",
				"expiresAt":     "2026-10-05T00:00:00Z",
			},
			"consumed": true,
		})
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "cra_testtoken")

	inv, err := c.InviteMember(context.Background(), "sam@example.com", "member", nil)
	if err != nil {
		t.Fatalf("InviteMember: %v", err)
	}
	if inv.ID != "inv-1" || inv.Email != "sam@example.com" {
		t.Errorf("InviteMember did not unwrap the envelope: %+v", inv)
	}
	if !inv.Consumed {
		t.Error("Consumed should be true when the wrapper says so")
	}
}

// A bare Invitation object (no wrapper) must still decode, in case the
// server ever stops wrapping this response.
func TestInviteMemberToleratesBareInvitation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":    "inv-2",
			"email": "sam@example.com",
			"role":  "member",
		})
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "cra_testtoken")

	inv, err := c.InviteMember(context.Background(), "sam@example.com", "member", nil)
	if err != nil {
		t.Fatalf("InviteMember: %v", err)
	}
	if inv.ID != "inv-2" {
		t.Errorf("InviteMember did not fall back to a bare object: %+v", inv)
	}
	if inv.Consumed {
		t.Error("Consumed should default to false when the server sends no wrapper")
	}
}

// The platform's POST /columns/{id}/archive response grew an "ids" field
// alongside "archived" so callers can undo the sweep via RestoreCard.
func TestArchiveColumnReturnsIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"archived": 2, "ids": []string{"c1", "c2"}})
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "cra_testtoken")

	n, ids, err := c.ArchiveColumn(context.Background(), "col-1")
	if err != nil {
		t.Fatalf("ArchiveColumn: %v", err)
	}
	if n != 2 || len(ids) != 2 || ids[0] != "c1" || ids[1] != "c2" {
		t.Errorf("ArchiveColumn = (%d, %v), want (2, [c1 c2])", n, ids)
	}
}

// An empty template must be omitted (the server treats an absent key as
// "use the default kanban layout"); a non-empty one must ride along.
func TestCreateProjectOmitsEmptyTemplate(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "p1", "name": "x"})
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "cra_testtoken")

	if _, err := c.CreateProject(context.Background(), "x", "", ""); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, ok := body["template"]; ok {
		t.Errorf("empty template should be omitted from the request body, got %v", body)
	}

	if _, err := c.CreateProject(context.Background(), "x", "", "sprint"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if body["template"] != "sprint" {
		t.Errorf("template = %v, want %q", body["template"], "sprint")
	}
}

func TestArchiveCardAndRestoreCard(t *testing.T) {
	var lastPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/archive"):
			_ = json.NewEncoder(w).Encode(map[string]any{"archived": true, "id": "c1"})
		case strings.HasSuffix(r.URL.Path, "/restore"):
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "c1", "title": "T", "column": "todo"})
		}
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "cra_testtoken")

	if err := c.ArchiveCard(context.Background(), "c1"); err != nil {
		t.Fatalf("ArchiveCard: %v", err)
	}
	if lastPath != "/api/v1/cards/c1/archive" {
		t.Errorf("path = %q, want .../cards/c1/archive", lastPath)
	}

	card, err := c.RestoreCard(context.Background(), "c1")
	if err != nil {
		t.Fatalf("RestoreCard: %v", err)
	}
	if card.ID != "c1" || card.Column != "todo" {
		t.Errorf("card = %+v", card)
	}
	if lastPath != "/api/v1/cards/c1/restore" {
		t.Errorf("path = %q, want .../cards/c1/restore", lastPath)
	}
}

// GET /projects/{id}/cards/archived is the same lean board projection as
// GetBoard (description omitted) plus archivedAt.
func TestListArchivedCardsDecodesBoardProjection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": "c1", "title": "Old card", "column": "done", "archivedAt": "2026-09-01T00:00:00Z"},
		})
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "cra_testtoken")

	cards, err := c.ListArchivedCards(context.Background(), "p1")
	if err != nil {
		t.Fatalf("ListArchivedCards: %v", err)
	}
	if len(cards) != 1 {
		t.Fatalf("got %d cards, want 1", len(cards))
	}
	if cards[0].ID != "c1" || cards[0].ArchivedAt != "2026-09-01T00:00:00Z" {
		t.Errorf("card = %+v", cards[0])
	}
	if cards[0].Description != "" {
		t.Errorf("archived list is the lean board projection; got a description: %q", cards[0].Description)
	}
}

func TestListBoardTemplatesDecodesColumns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"key": "sprint", "name": "Sprint", "description": "Backlog through review", "columns": []map[string]any{
				{"title": "Backlog", "color": "", "isDone": false},
				{"title": "Done", "color": "green", "isDone": true},
			}},
		})
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "cra_testtoken")

	templates, err := c.ListBoardTemplates(context.Background())
	if err != nil {
		t.Fatalf("ListBoardTemplates: %v", err)
	}
	if len(templates) != 1 || templates[0].Key != "sprint" || len(templates[0].Columns) != 2 {
		t.Fatalf("templates = %+v", templates)
	}
	if !templates[0].Columns[1].IsDone || templates[0].Columns[1].Color != "green" {
		t.Errorf("last column = %+v", templates[0].Columns[1])
	}
}

// CreateWebhook must send format/events only when non-empty, and the
// response's secret must decode - it is returned in full, never redacted.
func TestCreateWebhookBuildsExpectedBody(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "wh1", "endpointId": "ep1", "url": "https://example.com/hook",
			"secret": "shh", "format": "slack", "events": []string{"card.created"},
			"active": true, "createdAt": "2026-09-28T00:00:00Z", "recentDeliveries": []any{},
		})
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "cra_testtoken")

	wh, err := c.CreateWebhook(context.Background(), "p1", "https://example.com/hook", "", "slack", []string{"card.created"})
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}
	if body["format"] != "slack" {
		t.Errorf("format = %v, want slack", body["format"])
	}
	if _, ok := body["description"]; ok {
		t.Errorf("empty description should be omitted from the request body, got %v", body)
	}
	if wh.Secret != "shh" || wh.ID != "wh1" {
		t.Errorf("webhook = %+v", wh)
	}
}

func TestListWebhooksDecodesEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"webhooks": []map[string]any{
				{"id": "wh1", "endpointId": "ep1", "url": "https://x", "secret": "s", "format": "craaft",
					"events": []string{}, "active": true, "createdAt": "2026-09-28T00:00:00Z", "recentDeliveries": []any{}},
			},
			"eventCatalogue": []string{"card.created", "card.updated"},
		})
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "cra_testtoken")

	whs, cat, err := c.ListWebhooks(context.Background(), "p1")
	if err != nil {
		t.Fatalf("ListWebhooks: %v", err)
	}
	if len(whs) != 1 || whs[0].ID != "wh1" {
		t.Errorf("webhooks = %+v", whs)
	}
	if len(cat) != 2 {
		t.Errorf("eventCatalogue = %v, want 2 entries", cat)
	}
}

func TestGetInboundEmailDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"enabled": false, "address": nil, "aiAvailable": true})
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "cra_testtoken")

	enabled, aiAvailable, addr, err := c.GetInboundEmail(context.Background(), "p1")
	if err != nil {
		t.Fatalf("GetInboundEmail: %v", err)
	}
	if enabled || addr != nil {
		t.Errorf("GetInboundEmail = (%v, %+v), want (false, nil)", enabled, addr)
	}
	if !aiAvailable {
		t.Errorf("aiAvailable = false, want true")
	}
}

// A nil targetColumn must be omitted from the request body entirely (the
// server defaults to the board's first column); a non-nil one must ride
// along even when it points at an empty string.
func TestEnableInboundEmailOmitsNilTargetColumn(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"email": "abc@mail.craaft.io", "token": "abc", "targetColumn": nil,
			"active": true, "createdAt": "2026-09-28T00:00:00Z",
		})
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "cra_testtoken")

	addr, err := c.EnableInboundEmail(context.Background(), "p1", nil, nil)
	if err != nil {
		t.Fatalf("EnableInboundEmail: %v", err)
	}
	if _, ok := body["targetColumn"]; ok {
		t.Errorf("nil targetColumn should be omitted from the request body, got %v", body)
	}
	if _, ok := body["aiEnrich"]; ok {
		t.Errorf("nil aiEnrich should be omitted from the request body, got %v", body)
	}
	if addr.TargetColumn != nil {
		t.Errorf("TargetColumn = %v, want nil", addr.TargetColumn)
	}

	col := "todo"
	if _, err := c.EnableInboundEmail(context.Background(), "p1", &col, nil); err != nil {
		t.Fatalf("EnableInboundEmail(with column): %v", err)
	}
	if body["targetColumn"] != "todo" {
		t.Errorf("targetColumn = %v, want todo", body["targetColumn"])
	}

	on := true
	if _, err := c.EnableInboundEmail(context.Background(), "p1", nil, &on); err != nil {
		t.Fatalf("EnableInboundEmail(with aiEnrich): %v", err)
	}
	if body["aiEnrich"] != true {
		t.Errorf("aiEnrich = %v, want true", body["aiEnrich"])
	}
}

// Regression test: GetFocus's hygiene counters are keyed "longInProgress" on
// the wire, not "stuck" - the query-string enum value for GetHygiene and the
// JSON field name here are different spellings the server itself uses.
func TestGetFocusDecodesHygieneCounters(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"due": []any{}, "attention": []any{},
			"hygiene": map[string]any{"ghosts": 1, "longInProgress": 2, "mineNoDate": 3},
		})
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "cra_testtoken")

	focus, err := c.GetFocus(context.Background())
	if err != nil {
		t.Fatalf("GetFocus: %v", err)
	}
	if focus.Hygiene.Ghosts != 1 || focus.Hygiene.LongInProgress != 2 || focus.Hygiene.MineNoDate != 3 {
		t.Errorf("hygiene = %+v", focus.Hygiene)
	}
}

// Regression test: FocusDue used to have no AssignedUserID field at all, so
// it was silently dropped even though both /cards/upcoming and /cards/focus
// send it.
func TestGetUpcomingDecodesAssignedUserID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": "c1", "projectId": "p1", "projectName": "P", "columnKey": "todo", "columnTitle": "To Do",
				"title": "T", "dueDate": "2026-10-01T00:00:00Z", "assignedUserId": "u1", "assignedUserName": "Sam"},
		})
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "cra_testtoken")

	cards, err := c.GetUpcoming(context.Background())
	if err != nil {
		t.Fatalf("GetUpcoming: %v", err)
	}
	if len(cards) != 1 || cards[0].AssignedUserID == nil || *cards[0].AssignedUserID != "u1" {
		t.Errorf("cards = %+v", cards)
	}
}
