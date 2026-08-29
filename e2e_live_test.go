package main

// Live check against the local dev server. Skipped unless CRAAFT_E2E is set.
import (
	"context"
	"os"
	"testing"
)

func TestLiveGetCardAndSearch(t *testing.T) {
	base, tok := os.Getenv("CRAAFT_BASE_URL"), os.Getenv("CRAAFT_API_TOKEN")
	if os.Getenv("CRAAFT_E2E") == "" || base == "" || tok == "" {
		t.Skip("set CRAAFT_E2E + CRAAFT_BASE_URL + CRAAFT_API_TOKEN")
	}
	c := NewClient(base, tok)
	ctx := context.Background()

	projects, err := c.ListProjects(ctx)
	if err != nil || len(projects) == 0 {
		t.Fatalf("ListProjects: %v (n=%d)", err, len(projects))
	}
	_, cards, err := c.GetBoard(ctx, projects[0].ID)
	if err != nil || len(cards) == 0 {
		t.Fatalf("GetBoard: %v (n=%d)", err, len(cards))
	}
	want := cards[0]

	got, err := c.GetCard(ctx, want.ID)
	if err != nil {
		t.Fatalf("GetCard: %v", err)
	}
	if got.ID != want.ID || got.Title != want.Title {
		t.Errorf("GetCard = %q/%q, want %q/%q", got.ID, got.Title, want.ID, want.Title)
	}
	t.Logf("get_card OK: %q", got.Title)

	// The queries that used to break.
	for _, q := range []string{"#design", "50% off", "a&b", "new card", "design"} {
		res, err := c.SearchCards(ctx, q, 20)
		if err != nil {
			t.Errorf("SearchCards(%q): %v", q, err)
			continue
		}
		t.Logf("search %-10q -> %d results", q, len(res))
	}
}
