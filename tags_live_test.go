package main

// Round-trips list_tags against a real server, including the two behaviours
// the endpoint promises: archived cards contribute, and results are sorted
// and de-duplicated. Skipped unless CRAAFT_E2E is set.
import (
	"context"
	"os"
	"sort"
	"testing"
)

func TestLiveListTags(t *testing.T) {
	base, tok := os.Getenv("CRAAFT_BASE_URL"), os.Getenv("CRAAFT_API_TOKEN")
	if os.Getenv("CRAAFT_E2E") == "" || base == "" || tok == "" {
		t.Skip("set CRAAFT_E2E + CRAAFT_BASE_URL + CRAAFT_API_TOKEN")
	}
	c, ctx := NewClient(base, tok), context.Background()

	proj, err := c.CreateProject(ctx, "mcp-tags-scratch", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	t.Cleanup(func() {
		if err := c.DeleteProject(ctx, proj.ID); err != nil {
			t.Errorf("CLEANUP FAILED, board %s left behind: %v", proj.ID, err)
		} else {
			t.Logf("cleanup        -> scratch board removed")
		}
	})

	// An empty board has no tags.
	if tags, err := c.ListTags(ctx, proj.ID); err != nil {
		t.Fatalf("ListTags(empty): %v", err)
	} else if len(tags) != 0 {
		t.Errorf("empty board returned %v, want none", tags)
	}
	t.Logf("list_tags      -> empty board: 0 tags")

	// Two cards, overlapping tags, so de-duplication is actually exercised.
	made, err := c.BulkCreateCards(ctx, proj.ID, []map[string]any{
		{"title": "one", "column": "todo", "tags": []string{"zeta", "alpha"}},
		{"title": "two", "column": "todo", "tags": []string{"alpha", "mid"}},
		{"title": "archive-me", "column": "done", "tags": []string{"only-on-archived"}},
	})
	if err != nil {
		t.Fatalf("BulkCreateCards: %v", err)
	}

	tags, err := c.ListTags(ctx, proj.ID)
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	t.Logf("list_tags      -> %v", tags)

	want := []string{"alpha", "mid", "only-on-archived", "zeta"}
	if len(tags) != len(want) {
		t.Errorf("got %d tags %v, want %d %v", len(tags), tags, len(want), want)
	}
	if !sort.StringsAreSorted(tags) {
		t.Errorf("tags are not sorted: %v", tags)
	}
	seen := map[string]int{}
	for _, x := range tags {
		seen[x]++
	}
	if seen["alpha"] != 1 {
		t.Errorf("'alpha' appears %d times; it should be de-duplicated across cards", seen["alpha"])
	}

	// Archiving a card must NOT drop its tags from the list - the endpoint
	// covers archived cards too, which is what makes it safe for autocomplete.
	if _, err := c.BulkMoveCards(ctx, []string{made[2].ID}, "done", ""); err != nil {
		t.Fatalf("BulkMoveCards: %v", err)
	}
	cols, err := c.GetProject(ctx, proj.ID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	var doneID string
	for _, col := range cols.Columns {
		if col.Key == "done" {
			doneID = col.ID
		}
	}
	n, err := c.ArchiveColumn(ctx, doneID)
	if err != nil {
		t.Fatalf("ArchiveColumn: %v", err)
	}
	t.Logf("archive        -> %d card(s) archived", n)

	after, err := c.ListTags(ctx, proj.ID)
	if err != nil {
		t.Fatalf("ListTags(after archive): %v", err)
	}
	t.Logf("list_tags      -> after archiving: %v", after)
	found := false
	for _, x := range after {
		if x == "only-on-archived" {
			found = true
		}
	}
	if !found {
		t.Error("a tag that now exists only on an ARCHIVED card should still be listed")
	}
}
