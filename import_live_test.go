package main

// Previews then imports a small CSV against a real server, and deletes the
// board it made. Skipped unless CRAAFT_E2E is set.
import (
	"context"
	"os"
	"testing"
)

func TestLiveImport(t *testing.T) {
	base, tok := os.Getenv("CRAAFT_BASE_URL"), os.Getenv("CRAAFT_API_TOKEN")
	if os.Getenv("CRAAFT_E2E") == "" || base == "" || tok == "" {
		t.Skip("set CRAAFT_E2E + CRAAFT_BASE_URL + CRAAFT_API_TOKEN")
	}
	c, ctx := NewClient(base, tok), context.Background()

	csv := "Name,Stage,Notes\nWrite the brief,To Do,first\nBook the room,Doing,\nShip it,Done,last\n"
	in := ImportRequest{Filename: "mcp-import-scratch.csv", Data: []byte(csv)}

	// With no mapping, the preview returns the headers and the server's
	// suggested mapping alongside the board it guessed.
	p, err := c.PreviewImport(ctx, in)
	if err != nil {
		t.Fatalf("PreviewImport (unmapped): %v", err)
	}
	if p.CSV == nil || len(p.CSV.Headers) != 3 {
		t.Fatalf("unmapped preview: want 3 CSV headers, got %+v", p.CSV)
	}
	if p.CSV.Mapping["title"] == "" {
		t.Errorf("unmapped preview: want a suggested title column, got %v", p.CSV.Mapping)
	}
	t.Logf("unmapped preview -> source %q, %d columns, headers %v", p.Source, len(p.Columns), p.CSV.Headers)

	in.Mapping = map[string]string{"title": "Name", "column": "Stage", "description": "Notes"}
	p, err = c.PreviewImport(ctx, in)
	if err != nil {
		t.Fatalf("PreviewImport (mapped): %v", err)
	}
	if p.Totals.Cards != 3 || len(p.Columns) != 3 {
		t.Fatalf("mapped preview: want 3 cards in 3 columns, got %d cards, %+v", p.Totals.Cards, p.Columns)
	}
	t.Logf("mapped preview   -> %q, %d cards, columns %+v", p.Name, p.Totals.Cards, p.Columns)

	in.Name = "mcp-import-scratch"
	proj, err := c.ImportBoard(ctx, in)
	if err != nil {
		t.Fatalf("ImportBoard: %v", err)
	}
	t.Cleanup(func() {
		if _, _, err := c.do(ctx, "DELETE", "/api/v1/projects/"+proj.ID, nil); err != nil {
			t.Errorf("CLEANUP FAILED, board %s left behind: %v", proj.ID, err)
		} else {
			t.Logf("scratch board %s deleted", proj.ID)
		}
	})
	if proj.ID == "" || proj.Name != "mcp-import-scratch" {
		t.Fatalf("imported board = %+v", proj)
	}
	_, cards, err := c.GetBoard(ctx, proj.ID)
	if err != nil {
		t.Fatalf("GetBoard: %v", err)
	}
	if len(cards) != 3 {
		t.Errorf("imported board has %d cards, want 3", len(cards))
	}
	t.Logf("import           -> board %s with %d cards", proj.ID, len(cards))
}
