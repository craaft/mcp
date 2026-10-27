package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerTools builds a JSON schema for every tool's argument struct via
// reflection (github.com/google/jsonschema-go, through the mcp SDK). A type
// that reflection can't handle - notably the *[]string used by
// update_webhook's Events field, an unusual shape versus the rest of this
// file's plain slices and pointer-to-scalar fields - would panic here rather
// than fail a normal compile. This calls registerTools against a client
// pointed at nothing (no network call is made; that would require an actual
// tool invocation) purely to exercise schema generation for every tool this
// file registers.
func TestRegisterToolsDoesNotPanic(t *testing.T) {
	c := NewClient("http://localhost:0", "cra_testtoken")
	s := mcp.NewServer(&mcp.Implementation{Name: "craaft-mcp-test", Version: "test"}, nil)
	registerTools(s, c)
}

// apiCall is one request the stub platform saw.
type apiCall struct {
	Method string
	URI    string
	Body   map[string]any
	// HasBody is false when the request carried no body at all, as opposed
	// to an empty JSON object.
	HasBody bool
}

// stubAPI answers by "METHOD /path" (query string ignored) and records
// every request, so a test can assert on exactly what a tool put on the
// wire. A route that isn't listed answers 404.
func stubAPI(t *testing.T, routes map[string]any) (*Client, *[]apiCall) {
	t.Helper()
	var calls []apiCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := apiCall{Method: r.Method, URI: r.URL.RequestURI()}
		var raw json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); err == nil {
			call.HasBody = true
			_ = json.Unmarshal(raw, &call.Body)
		}
		calls = append(calls, call)
		w.Header().Set("Content-Type", "application/json")
		resp, ok := routes[r.Method+" "+r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "not found"})
			return
		}
		if fn, ok := resp.(func(w http.ResponseWriter)); ok {
			fn(w)
			return
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, "cra_testtoken"), &calls
}

// toolSession connects an in-memory MCP client to the real tool set.
func toolSession(t *testing.T, c *Client) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	s := mcp.NewServer(&mcp.Implementation{Name: "craaft-mcp-test", Version: "test"}, nil)
	registerTools(s, c)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cl := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	cs, err := cl.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// callTool runs one tool and fails the test if it errored.
func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s returned a tool error: %+v", name, res.Content)
	}
	return res
}

func onlyCall(t *testing.T, calls []apiCall, method string) apiCall {
	t.Helper()
	var found []apiCall
	for _, c := range calls {
		if c.Method == method {
			found = append(found, c)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one %s, got %+v", method, calls)
	}
	return found[0]
}

func toolNames(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}

// GET /cards/upcoming is gone from the platform, so the tool must be too;
// get_focus carries the due cards now.
func TestGetUpcomingIsGone(t *testing.T) {
	cs := toolSession(t, NewClient("http://localhost:0", "cra_testtoken"))
	for _, name := range toolNames(t, cs) {
		if name == "get_upcoming" {
			t.Fatal("get_upcoming is still registered")
		}
	}
}

// The README's tool table is the public list of what this server does; it
// must name exactly the registered tools, no more and no fewer.
func TestReadmeListsEveryTool(t *testing.T) {
	data, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	var readme []string
	for _, m := range regexp.MustCompile("(?m)^\\| `([a-z_]+)` \\|").FindAllStringSubmatch(string(data), -1) {
		readme = append(readme, m[1])
	}
	sort.Strings(readme)
	registered := toolNames(t, toolSession(t, NewClient("http://localhost:0", "cra_testtoken")))
	if strings.Join(readme, ",") != strings.Join(registered, ",") {
		t.Errorf("README tools (%d) != registered tools (%d)\nreadme:     %v\nregistered: %v",
			len(readme), len(registered), readme, registered)
	}
}

// The single create reads only title/column/position/description, so
// create_card appends after the column's last card and PATCHes the rest on.
func TestCreateCardAppendsAndPatchesMetadata(t *testing.T) {
	c, calls := stubAPI(t, map[string]any{
		"GET /api/v1/projects/p1/cards": []map[string]any{
			{"id": "a", "column": "todo", "position": 3},
			{"id": "b", "column": "todo", "position": 7.5},
			{"id": "c", "column": "doing", "position": 40},
		},
		"POST /api/v1/projects/p1/cards": map[string]any{"id": "new", "column": "todo", "position": 8.5},
		"PATCH /api/v1/cards/new":        map[string]any{"id": "new", "column": "todo", "priority": "high"},
	})
	cs := toolSession(t, c)
	res := callTool(t, cs, "create_card", map[string]any{
		"project_id": "p1", "title": "Ship it", "column": "todo", "description": "body",
		"due_date": "2026-11-01T00:00:00Z", "assigned_user_id": "u2", "size": 0,
		"priority": "high", "tags": []string{"api"},
	})

	post := onlyCall(t, *calls, http.MethodPost)
	if post.Body["position"] != 8.5 {
		t.Errorf("position = %v, want 8.5 (last todo card + 1)", post.Body["position"])
	}
	for _, k := range []string{"dueDate", "assignedUserId", "size", "priority", "tags"} {
		if _, ok := post.Body[k]; ok {
			t.Errorf("create body carries %s, which the endpoint ignores: %v", k, post.Body)
		}
	}
	patch := onlyCall(t, *calls, http.MethodPatch)
	want := map[string]any{
		"dueDate": "2026-11-01T00:00:00Z", "assignedUserId": "u2", "size": 0.0,
		"priority": "high", "tags": []any{"api"},
	}
	got, _ := json.Marshal(patch.Body)
	exp, _ := json.Marshal(want)
	if string(got) != string(exp) {
		t.Errorf("patch body = %s, want %s", got, exp)
	}
	out, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(out), `"priority":"high"`) {
		t.Errorf("result should be the patched card, got %s", out)
	}
}

func TestCreateCardWithoutMetadataSkipsPatch(t *testing.T) {
	c, calls := stubAPI(t, map[string]any{
		"GET /api/v1/projects/p1/cards":  []map[string]any{{"id": "c", "column": "doing", "position": 40}},
		"POST /api/v1/projects/p1/cards": map[string]any{"id": "new", "column": "todo"},
	})
	callTool(t, toolSession(t, c), "create_card", map[string]any{"project_id": "p1", "title": "T", "column": "todo"})
	post := onlyCall(t, *calls, http.MethodPost)
	if post.Body["position"] != 1.0 {
		t.Errorf("position on an empty column = %v, want 1", post.Body["position"])
	}
	for _, call := range *calls {
		if call.Method == http.MethodPatch {
			t.Errorf("unexpected PATCH with nothing to set: %+v", call)
		}
	}
}

// Every clear must reach the platform as the value it treats as "clear":
// null for tags, "" for text and colours. size 0 is a real size.
func TestClearFlagsSendClearingValues(t *testing.T) {
	for _, tc := range []struct {
		name, tool, route string
		args              map[string]any
		want              map[string]any
	}{
		{"card tags", "update_card", "PATCH /api/v1/cards/c1",
			map[string]any{"card_id": "c1", "clear_tags": true}, map[string]any{"tags": nil}},
		{"card description", "update_card", "PATCH /api/v1/cards/c1",
			map[string]any{"card_id": "c1", "clear_description": true}, map[string]any{"description": ""}},
		{"card size zero", "update_card", "PATCH /api/v1/cards/c1",
			map[string]any{"card_id": "c1", "size": 0}, map[string]any{"size": 0.0}},
		{"card position", "update_card", "PATCH /api/v1/cards/c1",
			map[string]any{"card_id": "c1", "position": 2.5}, map[string]any{"position": 2.5}},
		{"card tags trimmed", "update_card", "PATCH /api/v1/cards/c1",
			map[string]any{"card_id": "c1", "tags": " a, b ,,"}, map[string]any{"tags": []any{"a", "b"}}},
		{"column colour", "update_column", "PATCH /api/v1/columns/col1",
			map[string]any{"column_id": "col1", "clear_color": true}, map[string]any{"color": ""}},
		{"project description", "update_project", "PATCH /api/v1/projects/p1",
			map[string]any{"project_id": "p1", "clear_description": true}, map[string]any{"description": ""}},
		{"project background", "update_project", "PATCH /api/v1/projects/p1",
			map[string]any{"project_id": "p1", "clear_background": true}, map[string]any{"backgroundImage": "", "backgroundColor": ""}},
		{"webhook description", "update_webhook", "PATCH /api/v1/webhooks/w1",
			map[string]any{"subscription_id": "w1", "clear_description": true}, map[string]any{"description": ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// columnCounts is always an object on the platform; the output
			// schema rejects null there.
			c, calls := stubAPI(t, map[string]any{tc.route: map[string]any{"id": "x", "columnCounts": map[string]int{}}})
			callTool(t, toolSession(t, c), tc.tool, tc.args)
			got, _ := json.Marshal(onlyCall(t, *calls, http.MethodPatch).Body)
			exp, _ := json.Marshal(tc.want)
			if string(got) != string(exp) {
				t.Errorf("body = %s, want %s", got, exp)
			}
		})
	}
}

func TestUpdateProjectSendsToggles(t *testing.T) {
	c, calls := stubAPI(t, map[string]any{
		"PATCH /api/v1/projects/p1": map[string]any{
			"id": "p1", "showMilestones": true, "showArchived": false, "showFiles": true, "textColor": "light",
			"columnCounts": map[string]int{},
		},
	})
	res := callTool(t, toolSession(t, c), "update_project", map[string]any{
		"project_id": "p1", "show_milestones": true, "show_archived": false, "show_files": true, "show_standup": false,
		"show_activity": true, "text_color": "light", "background_color": "#112233",
	})
	got, _ := json.Marshal(onlyCall(t, *calls, http.MethodPatch).Body)
	const want = `{"backgroundColor":"#112233","showActivity":true,"showArchived":false,"showFiles":true,"showMilestones":true,"showStandup":false,"textColor":"light"}`
	if string(got) != want {
		t.Errorf("body = %s, want %s", got, want)
	}
	out, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(out), `"showMilestones":true`) || !strings.Contains(string(out), `"showFiles":true`) {
		t.Errorf("project result drops the toggles: %s", out)
	}
}

// Naming the card's own board is a same-board move: the cross-board route
// refuses it with 400, so move_card falls back to a column PATCH.
func TestMoveCardSameBoardFallsBackToPatch(t *testing.T) {
	c, calls := stubAPI(t, map[string]any{
		"POST /api/v1/cards/c1/move": func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "card is already on that board"})
		},
		"PATCH /api/v1/cards/c1": map[string]any{"id": "c1", "column": "done"},
	})
	callTool(t, toolSession(t, c), "move_card", map[string]any{"card_id": "c1", "target_project_id": "p1", "column": "done"})
	patch := onlyCall(t, *calls, http.MethodPatch)
	if patch.Body["column"] != "done" || len(patch.Body) != 1 {
		t.Errorf("fallback patch = %v, want {column: done}", patch.Body)
	}
}

func TestMarkNotificationsReadAll(t *testing.T) {
	c, calls := stubAPI(t, map[string]any{
		"POST /api/v1/me/notifications/read-all": map[string]any{"unseen": 0},
	})
	callTool(t, toolSession(t, c), "mark_notifications_read", map[string]any{"all": true})
	call := onlyCall(t, *calls, http.MethodPost)
	if call.URI != "/api/v1/me/notifications/read-all" || call.HasBody {
		t.Errorf("read-all call = %+v, want a bodiless POST to read-all", call)
	}
}

func TestListNotificationsIncludesUnseenCount(t *testing.T) {
	c, calls := stubAPI(t, map[string]any{
		"GET /api/v1/me/notifications": map[string]any{
			"items": []map[string]any{{"ids": []string{"n1", "n2"}, "kind": "group", "count": 2,
				"card": map[string]any{"id": "c1", "title": "T", "projectId": "p1", "projectName": "P"}}},
			"nextBefore": "2026-10-01T00:00:00.123Z",
		},
		"GET /api/v1/me/notifications/count": map[string]any{"unseen": 4},
	})
	res := callTool(t, toolSession(t, c), "list_notifications", map[string]any{"unread_only": true, "limit": 10})
	if (*calls)[0].URI != "/api/v1/me/notifications?limit=10&unread=1" {
		t.Errorf("list URI = %q", (*calls)[0].URI)
	}
	out, _ := json.Marshal(res.StructuredContent)
	for _, want := range []string{`"unseen":4`, `"nextBefore":"2026-10-01T00:00:00.123Z"`, `"ids":["n1","n2"]`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("result missing %s: %s", want, out)
		}
	}
}

// kind mention is how an agent lists where the caller was mentioned: the
// filter has to reach the API, and each row's ids have to come back.
func TestListNotificationsFiltersByKind(t *testing.T) {
	c, calls := stubAPI(t, map[string]any{
		"GET /api/v1/me/notifications": map[string]any{
			"items": []map[string]any{{"ids": []string{"n1"}, "kind": "mention", "kinds": []string{"mention"},
				"details": []string{"comment"}, "count": 1, "commentId": "cm1",
				"card": map[string]any{"id": "c1", "title": "T", "projectId": "p1", "projectName": "P"}}},
			"nextBefore": nil,
		},
		"GET /api/v1/me/notifications/count": map[string]any{"unseen": 1},
	})
	res := callTool(t, toolSession(t, c), "list_notifications", map[string]any{"kind": "mention"})
	if (*calls)[0].URI != "/api/v1/me/notifications?kind=mention" {
		t.Errorf("list URI = %q", (*calls)[0].URI)
	}
	out, _ := json.Marshal(res.StructuredContent)
	for _, want := range []string{`"commentId":"cm1"`, `"projectId":"p1"`, `"id":"c1"`, `"details":["comment"]`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("result missing %s: %s", want, out)
		}
	}
}

func TestGetBoardActivityPassesFiltersAndCursor(t *testing.T) {
	c, calls := stubAPI(t, map[string]any{
		"GET /api/v1/projects/p1/activity": map[string]any{
			"items": []map[string]any{{"id": "e1", "type": "card_deleted", "actorName": "Priya",
				"cardTitle": "Launch page", "createdAt": "2026-10-12T09:00:00Z"}},
			"nextBefore": nil,
		},
	})
	res := callTool(t, toolSession(t, c), "get_board_activity", map[string]any{
		"project_id": "p1", "actor_id": "u1", "types": "card_deleted,moved", "limit": 20, "before": "2026-10-12T10:00:00Z_e9",
	})
	if want := "/api/v1/projects/p1/activity?actor=u1&before=2026-10-12T10%3A00%3A00Z_e9&limit=20&type=card_deleted%2Cmoved"; (*calls)[0].URI != want {
		t.Errorf("URI = %q, want %q", (*calls)[0].URI, want)
	}
	out, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(out), `"cardTitle":"Launch page"`) || strings.Contains(string(out), "nextBefore") {
		t.Errorf("result = %s, want the entry and no nextBefore on the last page", out)
	}
}

// Every registered tool must reach clients with a title and annotations (the
// connector directory flags tools without them), and toolAnnotations must not
// keep entries for tools that no longer exist.
func TestEveryToolIsAnnotated(t *testing.T) {
	ctx := context.Background()
	s := mcp.NewServer(&mcp.Implementation{Name: "craaft-mcp-test", Version: "test"}, nil)
	registerTools(s, NewClient("http://localhost:0", "cra_testtoken"))

	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, tool := range res.Tools {
		seen[tool.Name] = true
		a := tool.Annotations
		switch {
		case a == nil:
			t.Errorf("%s: no annotations", tool.Name)
		case tool.Title == "" || a.Title == "":
			t.Errorf("%s: no title", tool.Name)
		case !a.ReadOnlyHint && a.DestructiveHint == nil:
			t.Errorf("%s: writes but doesn't declare destructiveHint", tool.Name)
		case a.OpenWorldHint == nil:
			t.Errorf("%s: no openWorldHint", tool.Name)
		}
	}
	for name := range toolAnnotations {
		if !seen[name] {
			t.Errorf("toolAnnotations has %s, but no such tool is registered", name)
		}
	}
}
