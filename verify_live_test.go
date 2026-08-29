package main

// End-to-end exercise of the newly added surfaces against a real server.
// Skipped unless CRAAFT_E2E is set. Creates its own scratch board and
// deletes it, so it never touches existing data.
import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestLiveNewSurfaces(t *testing.T) {
	base, tok := os.Getenv("CRAAFT_BASE_URL"), os.Getenv("CRAAFT_API_TOKEN")
	if os.Getenv("CRAAFT_E2E") == "" || base == "" || tok == "" {
		t.Skip("set CRAAFT_E2E + CRAAFT_BASE_URL + CRAAFT_API_TOKEN")
	}
	c, ctx := NewClient(base, tok), context.Background()

	// Scratch board so nothing here mutates a real one.
	var proj struct {
		ID string `json:"id"`
	}
	data, _, err := c.do(ctx, "POST", "/api/v1/projects", map[string]any{"name": "mcp-verify-scratch"})
	if err != nil {
		t.Fatalf("create scratch project: %v", err)
	}
	if err := json.Unmarshal(data, &proj); err != nil {
		t.Fatalf("decode project: %v", err)
	}
	t.Cleanup(func() {
		if _, _, err := c.do(ctx, "DELETE", "/api/v1/projects/"+proj.ID, nil); err != nil {
			t.Errorf("CLEANUP FAILED, scratch board %s left behind: %v", proj.ID, err)
		} else {
			t.Logf("scratch board %s deleted", proj.ID)
		}
	})

	// bulk_create_cards
	made, err := c.BulkCreateCards(ctx, proj.ID, []map[string]any{
		{"title": "bulk one", "column": "todo", "priority": "high", "tags": []string{"api"}},
		{"title": "bulk two", "column": "todo", "size": 3},
		{"title": "bulk three", "column": "todo"},
	})
	if err != nil {
		t.Fatalf("BulkCreateCards: %v", err)
	}
	if len(made) != 3 {
		t.Fatalf("BulkCreateCards returned %d cards, want 3", len(made))
	}
	t.Logf("bulk_create_cards  -> %d cards, first=%q priority=%v", len(made), made[0].Title, deref(made[0].Priority))

	// bulk_update_cards
	upd, err := c.BulkUpdateCards(ctx, []map[string]any{
		{"id": made[0].ID, "title": "bulk one renamed"},
		{"id": made[1].ID, "priority": "urgent"},
	})
	if err != nil {
		t.Fatalf("BulkUpdateCards: %v", err)
	}
	t.Logf("bulk_update_cards  -> %d cards, [0]=%q [1] priority=%v", len(upd), upd[0].Title, deref(upd[1].Priority))

	// bulk_move_cards
	moved, err := c.BulkMoveCards(ctx, []string{made[0].ID, made[1].ID, made[2].ID}, "done", "")
	if err != nil {
		t.Fatalf("BulkMoveCards: %v", err)
	}
	t.Logf("bulk_move_cards    -> %d cards now in %q", len(moved), moved[0].Column)

	// checklist: add, list, tick, delete
	item, err := c.AddChecklistItem(ctx, made[0].ID, "first step")
	if err != nil {
		t.Fatalf("AddChecklistItem: %v", err)
	}
	if _, err := c.AddChecklistItem(ctx, made[0].ID, "second step"); err != nil {
		t.Fatalf("AddChecklistItem 2: %v", err)
	}
	items, err := c.GetChecklist(ctx, made[0].ID)
	if err != nil {
		t.Fatalf("GetChecklist: %v", err)
	}
	t.Logf("get_checklist      -> %d items", len(items))

	ticked, err := c.UpdateChecklistItem(ctx, item.ID, map[string]any{"done": true})
	if err != nil {
		t.Fatalf("UpdateChecklistItem: %v", err)
	}
	if !ticked.Done {
		t.Errorf("UpdateChecklistItem did not set done")
	}
	t.Logf("update_checklist   -> %q done=%v", ticked.Text, ticked.Done)

	if err := c.DeleteChecklistItem(ctx, item.ID); err != nil {
		t.Fatalf("DeleteChecklistItem: %v", err)
	}
	after, _ := c.GetChecklist(ctx, made[0].ID)
	t.Logf("delete_checklist   -> %d items remain (was %d)", len(after), len(items))
	if len(after) != len(items)-1 {
		t.Errorf("delete left %d items, want %d", len(after), len(items)-1)
	}

	// card activity - the bulk move above should have produced a moved event
	events, err := c.GetCardEvents(ctx, made[0].ID)
	if err != nil {
		t.Fatalf("GetCardEvents: %v", err)
	}
	t.Logf("get_card_activity  -> %d events", len(events))
	for _, e := range events {
		t.Logf("      %s: %v -> %v", e.Type, deref(e.FromValue), deref(e.ToValue))
	}

	// hygiene - all three categories must at least answer cleanly
	for _, cat := range []string{"ghosts", "stuck", "mine_no_date"} {
		cards, err := c.GetHygiene(ctx, cat)
		if err != nil {
			t.Errorf("GetHygiene(%s): %v", cat, err)
			continue
		}
		t.Logf("get_hygiene %-12s -> %d cards", cat, len(cards))
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
