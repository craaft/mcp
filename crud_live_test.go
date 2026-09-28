package main

// Round-trips project/column CRUD, members, export, comment edit/delete,
// attachment delete and card follow against a real server.
// Skipped unless CRAAFT_E2E is set. Everything happens on a board this test
// creates and deletes.
import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestLiveCRUD(t *testing.T) {
	base, tok := os.Getenv("CRAAFT_BASE_URL"), os.Getenv("CRAAFT_API_TOKEN")
	if os.Getenv("CRAAFT_E2E") == "" || base == "" || tok == "" {
		t.Skip("set CRAAFT_E2E + CRAAFT_BASE_URL + CRAAFT_API_TOKEN")
	}
	c, ctx := NewClient(base, tok), context.Background()

	// create_project
	proj, err := c.CreateProject(ctx, "mcp-crud-scratch", "created by the MCP test", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	t.Cleanup(func() {
		if err := c.DeleteProject(ctx, proj.ID); err != nil {
			t.Errorf("CLEANUP FAILED, board %s left behind: %v", proj.ID, err)
		} else {
			t.Logf("delete_project      -> scratch board removed")
		}
	})
	t.Logf("create_project      -> %q with %d default columns", proj.Name, len(proj.Columns))

	// get_project
	got, err := c.GetProject(ctx, proj.ID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	t.Logf("get_project         -> %q myRole=%s visibility=%s", got.Name, got.MyRole, got.Visibility)

	// update_project
	upd, err := c.UpdateProject(ctx, proj.ID, map[string]any{"name": "mcp-crud-renamed", "isFavorite": true})
	if err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}
	if upd.Name != "mcp-crud-renamed" {
		t.Errorf("rename did not take: %q", upd.Name)
	}
	t.Logf("update_project      -> renamed to %q", upd.Name)

	// add_column
	col, err := c.AddColumn(ctx, proj.ID, "Review")
	if err != nil {
		t.Fatalf("AddColumn: %v", err)
	}
	t.Logf("add_column          -> %q key=%s", col.Title, col.Key)

	// update_column: rename + WIP limit
	limit := 5
	col2, err := c.UpdateColumn(ctx, col.ID, map[string]any{"title": "In Review", "cardLimit": limit})
	if err != nil {
		t.Fatalf("UpdateColumn: %v", err)
	}
	t.Logf("update_column       -> %q cardLimit=%v", col2.Title, col2.CardLimit)

	// delete_column on an EMPTY column works
	tmp, err := c.AddColumn(ctx, proj.ID, "Temp")
	if err != nil {
		t.Fatalf("AddColumn(temp): %v", err)
	}
	if err := c.DeleteColumn(ctx, tmp.ID); err != nil {
		t.Fatalf("DeleteColumn(empty): %v", err)
	}
	t.Logf("delete_column       -> empty column removed")

	// ...and is refused on a non-empty one
	cards, err := c.BulkCreateCards(ctx, proj.ID, []map[string]any{
		{"title": "card in review", "column": col.Key},
	})
	if err != nil {
		t.Fatalf("BulkCreateCards: %v", err)
	}
	card := cards[0]
	if err := c.DeleteColumn(ctx, col.ID); err == nil {
		t.Errorf("DeleteColumn on a non-empty column succeeded; expected 409")
	} else {
		t.Logf("delete_column       -> correctly refused non-empty: %v", err)
	}

	// archive_column only touches columns flagged isDone, and answers 200
	// with 0 otherwise rather than erroring - the silent no-op this asserts.
	n, ids, err := c.ArchiveColumn(ctx, col.ID)
	if err != nil {
		t.Fatalf("ArchiveColumn: %v", err)
	}
	if n != 0 {
		t.Errorf("archived %d cards from a non-done column, want 0", n)
	}
	if len(ids) != 0 {
		t.Errorf("got %d ids from a no-op archive, want 0", len(ids))
	}
	t.Logf("archive_column      -> %d archived (column is not isDone: silent no-op, as documented)", n)

	// Flag it done, then archiving works and the column can be deleted.
	if _, err := c.UpdateColumn(ctx, col.ID, map[string]any{"isDone": true}); err != nil {
		t.Fatalf("UpdateColumn(isDone): %v", err)
	}
	n, ids, err = c.ArchiveColumn(ctx, col.ID)
	if err != nil {
		t.Fatalf("ArchiveColumn after isDone: %v", err)
	}
	t.Logf("archive_column      -> %d archived once isDone=true, ids=%v", n, ids)
	if n != 1 {
		t.Errorf("archived %d cards, want 1", n)
	}
	if len(ids) != 1 || ids[0] != card.ID {
		t.Errorf("archive_column ids = %v, want [%s]", ids, card.ID)
	}

	// restore_card puts it right back where it came from.
	restored, err := c.RestoreCard(ctx, card.ID)
	if err != nil {
		t.Fatalf("RestoreCard: %v", err)
	}
	if restored.Column != col.Key {
		t.Errorf("RestoreCard column = %q, want %q", restored.Column, col.Key)
	}
	t.Logf("restore_card        -> back in column %q", restored.Column)

	// archive_card / list_archived_cards round-trip on the same card. Left
	// archived (not restored again) so the DeleteColumn call below still
	// exercises "archived cards don't block delete".
	if err := c.ArchiveCard(ctx, card.ID); err != nil {
		t.Fatalf("ArchiveCard: %v", err)
	}
	archived, err := c.ListArchivedCards(ctx, proj.ID)
	if err != nil {
		t.Fatalf("ListArchivedCards: %v", err)
	}
	t.Logf("list_archived_cards -> %d archived", len(archived))
	found := false
	for _, a := range archived {
		if a.ID == card.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("archived card %s not found in ListArchivedCards", card.ID)
	}
	// Archiving an already-archived card is a 404.
	if err := c.ArchiveCard(ctx, card.ID); err == nil {
		t.Error("ArchiveCard on an already-archived card succeeded; expected an error")
	}
	// Archived cards no longer block the delete: the platform reassigns them
	// to the board's first column first. Live cards still block it.
	if err := c.DeleteColumn(ctx, col.ID); err != nil {
		t.Errorf("DeleteColumn after archive: %v", err)
	} else {
		t.Logf("delete_column       -> archived cards reassigned, column deleted")
	}

	// card follow / unfollow
	if err := c.SetCardFollow(ctx, card.ID, true); err != nil {
		t.Fatalf("SetCardFollow(true): %v", err)
	}
	after, _ := c.GetCard(ctx, card.ID)
	t.Logf("set_card_follow     -> follow=true, card.following=%v", after.Following)
	if err := c.SetCardFollow(ctx, card.ID, false); err != nil {
		t.Fatalf("SetCardFollow(false): %v", err)
	}
	after2, _ := c.GetCard(ctx, card.ID)
	t.Logf("set_card_follow     -> follow=false, card.following=%v", after2.Following)
	if after2.Following {
		t.Errorf("unfollow did not take")
	}

	// comment add / edit / delete
	cm, err := c.AddComment(ctx, card.ID, "first version")
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	edited, err := c.UpdateComment(ctx, cm.ID, "edited version")
	if err != nil {
		t.Fatalf("UpdateComment: %v", err)
	}
	if edited.Body != "edited version" {
		t.Errorf("edit did not take: %q", edited.Body)
	}
	t.Logf("update_comment      -> %q", edited.Body)
	if err := c.DeleteComment(ctx, cm.ID); err != nil {
		t.Fatalf("DeleteComment: %v", err)
	}
	left, _ := c.GetComments(ctx, card.ID)
	t.Logf("delete_comment      -> %d comments remain", len(left))
	if len(left) != 0 {
		t.Errorf("delete left %d comments, want 0", len(left))
	}

	// attachment upload then delete
	att, err := c.UploadAttachment(ctx, card.ID, "temp.txt", "text/plain", []byte("scratch"))
	if err != nil {
		t.Fatalf("UploadAttachment: %v", err)
	}
	if err := c.DeleteAttachment(ctx, att.ID); err != nil {
		t.Fatalf("DeleteAttachment: %v", err)
	}
	remaining, _ := c.ListAttachments(ctx, card.ID)
	t.Logf("delete_attachment   -> %d attachments remain", len(remaining))
	if len(remaining) != 0 {
		t.Errorf("delete left %d attachments, want 0", len(remaining))
	}

	// export, both formats
	for _, f := range []string{"json", "csv"} {
		data, err := c.ExportProject(ctx, proj.ID, f)
		if err != nil {
			t.Errorf("ExportProject(%s): %v", f, err)
			continue
		}
		head := strings.SplitN(string(data), "\n", 2)[0]
		if len(head) > 70 {
			head = head[:70] + "..."
		}
		t.Logf("export_project %-4s -> %d bytes, starts %q", f, len(data), head)
	}

	// members + board members (read-only: no mutating anyone's real access)
	members, err := c.ListMembers(ctx)
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	t.Logf("list_members        -> %d members", len(members))
	bm, err := c.ListBoardMembers(ctx, proj.ID)
	if err != nil {
		t.Fatalf("ListBoardMembers: %v", err)
	}
	t.Logf("list_board_members  -> %d with access", len(bm))
	invs, err := c.ListInvitations(ctx)
	if err != nil {
		t.Fatalf("ListInvitations: %v", err)
	}
	t.Logf("list_invitations    -> %d pending", len(invs))
}
