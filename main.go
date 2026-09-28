package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
)

func main() {
	baseURL := os.Getenv("CRAAFT_BASE_URL")
	if baseURL == "" {
		baseURL = "https://craaft.io"
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	getServer := func(r *http.Request) *mcp.Server {
		token := extractToken(r)
		if token == "" {
			return nil
		}
		client := NewClient(baseURL, token)
		s := mcp.NewServer(&mcp.Implementation{
			Name:    "craaft-mcp",
			Version: version,
		}, nil)
		registerTools(s, client)
		return s
	}

	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(getServer, nil))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"status":"ok"}`)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintln(w, `{"error":"not found"}`)
	})

	addr := ":" + port
	log.Printf("craaft-mcp %s (%s) listening on %s (upstream %s)", version, commit, addr, baseURL)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func extractToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		token := strings.TrimPrefix(h, "Bearer ")
		if strings.HasPrefix(token, "cra_") {
			return token
		}
	}
	return ""
}

func registerTools(s *mcp.Server, c *Client) {
	// ── list_projects ──────────────────────────────────────────────
	type listProjectsResult struct {
		Projects []Project `json:"projects"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_projects",
		Description: "List all Kanban projects accessible to the authenticated user. Returns project names, IDs, column counts, and card totals.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listProjectsResult, error) {
		projects, err := c.ListProjects(ctx)
		if err != nil {
			return nil, listProjectsResult{}, err
		}
		return nil, listProjectsResult{Projects: projects}, nil
	})

	// ── get_board ──────────────────────────────────────────────────
	type getBoardArgs struct {
		ProjectID string `json:"project_id" jsonschema:"the project UUID to load"`
	}
	type getBoardResult struct {
		Project Project `json:"project"`
		Cards   []Card  `json:"cards"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "get_board",
		Description: "Get a Kanban board: its columns and every card with metadata (title, due date, " +
			"assignee, priority, size, tags, checklist progress). Card descriptions are omitted — use " +
			"get_card for the body, or get_card_detail for the body plus comments, events, checklist " +
			"and attachments in one call.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args getBoardArgs) (*mcp.CallToolResult, getBoardResult, error) {
		project, cards, err := c.GetBoard(ctx, args.ProjectID)
		if err != nil {
			return nil, getBoardResult{}, err
		}
		return nil, getBoardResult{Project: *project, Cards: cards}, nil
	})

	// ── search_cards ──────────────────────────────────────────────
	type searchCardsArgs struct {
		Query string `json:"query" jsonschema:"search text to match against card titles and descriptions"`
		Limit int    `json:"limit,omitempty" jsonschema:"max results to return (default 20, max 50)"`
	}
	type searchCardsResult struct {
		Results []SearchResult `json:"results"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "search_cards",
		Description: "Search for cards across all accessible projects by title and description text. " +
			"Each hit's description is a ≤180-character snippet, not the full body — use get_card or " +
			"get_card_detail when you need the rest.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args searchCardsArgs) (*mcp.CallToolResult, searchCardsResult, error) {
		limit := args.Limit
		if limit <= 0 {
			limit = 20
		}
		results, err := c.SearchCards(ctx, args.Query, limit)
		if err != nil {
			return nil, searchCardsResult{}, err
		}
		return nil, searchCardsResult{Results: results}, nil
	})

	// ── get_card ──────────────────────────────────────────────────
	type getCardArgs struct {
		CardID string `json:"card_id" jsonschema:"card UUID"`
	}
	type getCardResult struct {
		Card Card `json:"card"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "get_card",
		Description: "Get a single card by its ID, with full metadata including description. Prefer this " +
			"over get_board when you already know the card ID — get_board omits description and returns " +
			"every card on the board. Use get_card_detail instead when you also need comments, events, " +
			"checklist or attachments.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args getCardArgs) (*mcp.CallToolResult, getCardResult, error) {
		card, err := c.GetCard(ctx, args.CardID)
		if err != nil {
			return nil, getCardResult{}, err
		}
		return nil, getCardResult{Card: *card}, nil
	})

	// ── get_card_detail ───────────────────────────────────────────
	type getCardDetailResult struct {
		Card        Card            `json:"card"`
		Comments    []Comment       `json:"comments"`
		Events      []CardEvent     `json:"events"`
		Checklist   []ChecklistItem `json:"checklist"`
		Attachments []Attachment    `json:"attachments"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "get_card_detail",
		Description: "Get a card plus its comments, activity events (newest 100, oldest-first), " +
			"checklist and attachments in one call. Prefer this over get_card + get_comments + " +
			"get_card_activity + get_checklist + list_attachments — one authorised round-trip instead of five.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args getCardArgs) (*mcp.CallToolResult, getCardDetailResult, error) {
		d, err := c.GetCardDetail(ctx, args.CardID)
		if err != nil {
			return nil, getCardDetailResult{}, err
		}
		return nil, getCardDetailResult{
			Card: d.Card, Comments: d.Comments, Events: d.Events,
			Checklist: d.Checklist, Attachments: d.Attachments,
		}, nil
	})

	// ── create_card ───────────────────────────────────────────────
	type createCardArgs struct {
		ProjectID    string   `json:"project_id" jsonschema:"project UUID"`
		Title        string   `json:"title" jsonschema:"card title"`
		Column       string   `json:"column" jsonschema:"column key (e.g. 'todo', 'doing', 'done')"`
		Description  string   `json:"description,omitempty" jsonschema:"optional markdown description"`
		DueDate      string   `json:"due_date,omitempty" jsonschema:"optional ISO 8601 date (e.g. '2026-01-15T00:00:00Z')"`
		AssignedUser string   `json:"assigned_user_id,omitempty" jsonschema:"optional user UUID to assign"`
		Size         int      `json:"size,omitempty" jsonschema:"optional size estimate (integer)"`
		Priority     string   `json:"priority,omitempty" jsonschema:"optional priority: low, medium, high, urgent"`
		Tags         []string `json:"tags,omitempty" jsonschema:"optional list of tag strings"`
	}
	type createCardResult struct {
		Card Card `json:"card"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "create_card",
		Description: "Create a new card on a Kanban board. The card appears in the specified column.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args createCardArgs) (*mcp.CallToolResult, createCardResult, error) {
		card := Card{
			Title:  args.Title,
			Column: args.Column,
			Tags:   args.Tags,
		}
		if args.Description != "" {
			card.Description = args.Description
		}
		if args.DueDate != "" {
			card.DueDate = &args.DueDate
		}
		if args.AssignedUser != "" {
			card.AssignedUserID = &args.AssignedUser
		}
		if args.Size > 0 {
			card.Size = &args.Size
		}
		if args.Priority != "" {
			card.Priority = &args.Priority
		}

		created, err := c.CreateCard(ctx, args.ProjectID, card)
		if err != nil {
			return nil, createCardResult{}, err
		}
		return nil, createCardResult{Card: *created}, nil
	})

	// ── update_card ───────────────────────────────────────────────
	type updateCardArgs struct {
		CardID        string `json:"card_id" jsonschema:"card UUID"`
		Title         string `json:"title,omitempty" jsonschema:"new title (omit to leave unchanged)"`
		Description   string `json:"description,omitempty" jsonschema:"new description (omit to leave unchanged)"`
		Column        string `json:"column,omitempty" jsonschema:"move to this column key (omit to leave unchanged)"`
		DueDate       string `json:"due_date,omitempty" jsonschema:"set due date (omit to leave unchanged)"`
		ClearDueDate  bool   `json:"clear_due_date,omitempty" jsonschema:"set true to clear the due date"`
		AssignedUser  string `json:"assigned_user_id,omitempty" jsonschema:"assign to this user UUID (omit to leave unchanged)"`
		ClearAssignee bool   `json:"clear_assignee,omitempty" jsonschema:"set true to unassign"`
		Size          int    `json:"size,omitempty" jsonschema:"set size estimate (omit to leave unchanged)"`
		ClearSize     bool   `json:"clear_size,omitempty" jsonschema:"set true to clear size"`
		Priority      string `json:"priority,omitempty" jsonschema:"set priority: low, medium, high, urgent (omit to leave unchanged)"`
		ClearPriority bool   `json:"clear_priority,omitempty" jsonschema:"set true to clear priority"`
		Tags          string `json:"tags,omitempty" jsonschema:"comma-separated tags to set (omit to leave unchanged)"`
	}
	type updateCardResult struct {
		Card Card `json:"card"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "update_card",
		Description: "Update a card's title, description, column, metadata, or move it to another column. Omit a field to leave it unchanged. Set a clear_* flag to null out a nullable field.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args updateCardArgs) (*mcp.CallToolResult, updateCardResult, error) {
		patch := map[string]any{}
		if args.Title != "" {
			patch["title"] = args.Title
		}
		if args.Description != "" {
			patch["description"] = args.Description
		}
		if args.Column != "" {
			patch["column"] = args.Column
		}
		if args.DueDate != "" {
			patch["dueDate"] = args.DueDate
		} else if args.ClearDueDate {
			patch["dueDate"] = nil
		}
		if args.AssignedUser != "" {
			patch["assignedUserId"] = args.AssignedUser
		} else if args.ClearAssignee {
			patch["assignedUserId"] = nil
		}
		if args.Size > 0 {
			patch["size"] = args.Size
		} else if args.ClearSize {
			patch["size"] = nil
		}
		if args.Priority != "" {
			patch["priority"] = args.Priority
		} else if args.ClearPriority {
			patch["priority"] = nil
		}
		if args.Tags != "" {
			patch["tags"] = strings.Split(args.Tags, ",")
		}

		if len(patch) == 0 {
			return nil, updateCardResult{}, fmt.Errorf("no fields to update")
		}

		updated, err := c.UpdateCard(ctx, args.CardID, patch)
		if err != nil {
			return nil, updateCardResult{}, err
		}
		return nil, updateCardResult{Card: *updated}, nil
	})

	// ── move_card ─────────────────────────────────────────────────
	type moveCardArgs struct {
		CardID          string `json:"card_id" jsonschema:"card UUID"`
		TargetProjectID string `json:"target_project_id,omitempty" jsonschema:"project UUID to move to (omit to move within the same board)"`
		Column          string `json:"column" jsonschema:"destination column key"`
	}
	type moveCardResult struct {
		Card Card `json:"card"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "move_card",
		Description: "Move a card to a column. Optionally move it to a different board (must be in the same workspace).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args moveCardArgs) (*mcp.CallToolResult, moveCardResult, error) {
		target := args.TargetProjectID
		if target == "" {
			updated, err := c.UpdateCard(ctx, args.CardID, map[string]any{"column": args.Column})
			if err != nil {
				return nil, moveCardResult{}, err
			}
			return nil, moveCardResult{Card: *updated}, nil
		}
		moved, err := c.MoveCard(ctx, args.CardID, target, args.Column)
		if err != nil {
			return nil, moveCardResult{}, err
		}
		return nil, moveCardResult{Card: *moved}, nil
	})

	// ── delete_card ───────────────────────────────────────────────
	type deleteCardArgs struct {
		CardID string `json:"card_id" jsonschema:"card UUID to delete"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_card",
		Description: "Permanently delete a card. This cannot be undone.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args deleteCardArgs) (*mcp.CallToolResult, struct{}, error) {
		if err := c.DeleteCard(ctx, args.CardID); err != nil {
			return nil, struct{}{}, err
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "Card deleted"}},
		}, struct{}{}, nil
	})

	// ── add_comment ───────────────────────────────────────────────
	type addCommentArgs struct {
		CardID string `json:"card_id" jsonschema:"card UUID"`
		Body   string `json:"body" jsonschema:"comment body (markdown supported)"`
	}
	type addCommentResult struct {
		Comment Comment `json:"comment"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "add_comment",
		Description: "Add a comment to a card. Supports markdown and @username mentions.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args addCommentArgs) (*mcp.CallToolResult, addCommentResult, error) {
		comment, err := c.AddComment(ctx, args.CardID, args.Body)
		if err != nil {
			return nil, addCommentResult{}, err
		}
		return nil, addCommentResult{Comment: *comment}, nil
	})

	// ── get_comments ──────────────────────────────────────────────
	type getCommentsArgs struct {
		CardID string `json:"card_id" jsonschema:"card UUID"`
	}
	type getCommentsResult struct {
		Comments []Comment `json:"comments"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_comments",
		Description: "List all comments on a card, oldest first.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args getCommentsArgs) (*mcp.CallToolResult, getCommentsResult, error) {
		comments, err := c.GetComments(ctx, args.CardID)
		if err != nil {
			return nil, getCommentsResult{}, err
		}
		return nil, getCommentsResult{Comments: comments}, nil
	})

	// ── get_upcoming ──────────────────────────────────────────────
	type getUpcomingResult struct {
		Cards []FocusDue `json:"cards"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_upcoming",
		Description: "List all due-dated cards across every accessible project, ordered by due date. Useful for seeing what's coming up.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, getUpcomingResult, error) {
		cards, err := c.GetUpcoming(ctx)
		if err != nil {
			return nil, getUpcomingResult{}, err
		}
		return nil, getUpcomingResult{Cards: cards}, nil
	})

	// ── get_focus ─────────────────────────────────────────────────
	type getFocusResult struct {
		Focus FocusResponse `json:"focus"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_focus",
		Description: "Get a focus overview: cards that are due, cards needing attention (stale in progress, high priority, idle), and hygiene counters (ghost cards, stuck cards, assigned without due date).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, getFocusResult, error) {
		focus, err := c.GetFocus(ctx)
		if err != nil {
			return nil, getFocusResult{}, err
		}
		return nil, getFocusResult{Focus: *focus}, nil
	})

	// ── bulk_create_cards ─────────────────────────────────────────
	type bulkCreateArgs struct {
		ProjectID string           `json:"project_id" jsonschema:"project UUID"`
		Cards     []map[string]any `json:"cards" jsonschema:"up to 100 card objects. Each needs title and column (the column KEY, e.g. 'todo'); may also set description, position, dueDate, assignedUserId, size, priority, tags"`
	}
	type bulkCardsResult struct {
		Cards []Card `json:"cards"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "bulk_create_cards",
		Description: "Create up to 100 cards on one board in a single transaction. Strongly prefer this " +
			"over looping create_card: it costs one rate-limit token instead of one per card, and it is " +
			"all-or-nothing, so a failure leaves no half-imported board. Unlike create_card it does NOT " +
			"default the assignee to you, and it sends no notification emails.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args bulkCreateArgs) (*mcp.CallToolResult, bulkCardsResult, error) {
		cards, err := c.BulkCreateCards(ctx, args.ProjectID, args.Cards)
		if err != nil {
			return nil, bulkCardsResult{}, err
		}
		return nil, bulkCardsResult{Cards: cards}, nil
	})

	// ── bulk_update_cards ─────────────────────────────────────────
	type bulkUpdateArgs struct {
		Cards []map[string]any `json:"cards" jsonschema:"up to 100 patch objects. Each needs an id plus the fields to change; omit a field to leave it alone, send null to clear a nullable one (dueDate, assignedUserId, size, priority)"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "bulk_update_cards",
		Description: "Apply up to 100 partial card updates in one transaction. Same field semantics as " +
			"update_card. All-or-nothing: one bad item rolls the whole batch back and the error names " +
			"the offending index, so never assume a failed batch partially applied.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args bulkUpdateArgs) (*mcp.CallToolResult, bulkCardsResult, error) {
		cards, err := c.BulkUpdateCards(ctx, args.Cards)
		if err != nil {
			return nil, bulkCardsResult{}, err
		}
		return nil, bulkCardsResult{Cards: cards}, nil
	})

	// ── bulk_move_cards ───────────────────────────────────────────
	type bulkMoveArgs struct {
		IDs             []string `json:"ids" jsonschema:"up to 100 card UUIDs"`
		Column          string   `json:"column" jsonschema:"destination column key"`
		TargetProjectID string   `json:"target_project_id,omitempty" jsonschema:"optional destination board UUID (same workspace). Omit to move within the cards' current board"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "bulk_move_cards",
		Description: "Sweep up to 100 cards into a column in one transaction. Without target_project_id " +
			"every id must already be on the SAME board (mixed boards return 400). With it, the batch " +
			"moves to that board and appends to the end of the target column.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args bulkMoveArgs) (*mcp.CallToolResult, bulkCardsResult, error) {
		cards, err := c.BulkMoveCards(ctx, args.IDs, args.Column, args.TargetProjectID)
		if err != nil {
			return nil, bulkCardsResult{}, err
		}
		return nil, bulkCardsResult{Cards: cards}, nil
	})

	// ── get_checklist ─────────────────────────────────────────────
	type cardIDArgs struct {
		CardID string `json:"card_id" jsonschema:"card UUID"`
	}
	type checklistResult struct {
		Items []ChecklistItem `json:"items"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_checklist",
		Description: "List a card's checklist items, in position order.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args cardIDArgs) (*mcp.CallToolResult, checklistResult, error) {
		items, err := c.GetChecklist(ctx, args.CardID)
		if err != nil {
			return nil, checklistResult{}, err
		}
		return nil, checklistResult{Items: items}, nil
	})

	// ── add_checklist_item ────────────────────────────────────────
	type addChecklistArgs struct {
		CardID string `json:"card_id" jsonschema:"card UUID"`
		Text   string `json:"text" jsonschema:"item text, max 1000 characters"`
	}
	type checklistItemResult struct {
		Item ChecklistItem `json:"item"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "add_checklist_item",
		Description: "Append an item to a card's checklist.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args addChecklistArgs) (*mcp.CallToolResult, checklistItemResult, error) {
		item, err := c.AddChecklistItem(ctx, args.CardID, args.Text)
		if err != nil {
			return nil, checklistItemResult{}, err
		}
		return nil, checklistItemResult{Item: *item}, nil
	})

	// ── update_checklist_item ─────────────────────────────────────
	type updateChecklistArgs struct {
		ItemID string `json:"item_id" jsonschema:"checklist item UUID (not the card UUID)"`
		Text   string `json:"text,omitempty" jsonschema:"new text, max 1000 characters"`
		Done   *bool  `json:"done,omitempty" jsonschema:"tick or untick the item"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "update_checklist_item",
		Description: "Tick, untick or retitle a checklist item. Omitted fields are left unchanged.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args updateChecklistArgs) (*mcp.CallToolResult, checklistItemResult, error) {
		patch := map[string]any{}
		if args.Text != "" {
			patch["text"] = args.Text
		}
		if args.Done != nil {
			patch["done"] = *args.Done
		}
		if len(patch) == 0 {
			return nil, checklistItemResult{}, fmt.Errorf("nothing to update: pass text and/or done")
		}
		item, err := c.UpdateChecklistItem(ctx, args.ItemID, patch)
		if err != nil {
			return nil, checklistItemResult{}, err
		}
		return nil, checklistItemResult{Item: *item}, nil
	})

	// ── delete_checklist_item ─────────────────────────────────────
	type deleteChecklistArgs struct {
		ItemID string `json:"item_id" jsonschema:"checklist item UUID (not the card UUID)"`
	}
	type deleteChecklistResult struct {
		Deleted bool `json:"deleted"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_checklist_item",
		Description: "Permanently delete a checklist item.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args deleteChecklistArgs) (*mcp.CallToolResult, deleteChecklistResult, error) {
		if err := c.DeleteChecklistItem(ctx, args.ItemID); err != nil {
			return nil, deleteChecklistResult{}, err
		}
		return nil, deleteChecklistResult{Deleted: true}, nil
	})

	// ── get_card_activity ─────────────────────────────────────────
	type activityResult struct {
		Events []CardEvent `json:"events"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "get_card_activity",
		Description: "Get a card's activity log (column moves, priority changes, assignee changes), " +
			"oldest first. Useful for answering when something moved or who changed it.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args cardIDArgs) (*mcp.CallToolResult, activityResult, error) {
		events, err := c.GetCardEvents(ctx, args.CardID)
		if err != nil {
			return nil, activityResult{}, err
		}
		return nil, activityResult{Events: events}, nil
	})

	// ── get_hygiene ───────────────────────────────────────────────
	type hygieneArgs struct {
		Category string `json:"category" jsonschema:"which counter to expand: ghosts (no due date, priority or assignee), stuck (untouched 14+ days), or mine_no_date (assigned to you with no due date)"`
	}
	type hygieneResult struct {
		Cards []AttentionCard `json:"cards"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "get_hygiene",
		Description: "List the actual cards behind one of get_focus's hygiene counters. get_focus tells " +
			"you how many need attention; this tells you which ones.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args hygieneArgs) (*mcp.CallToolResult, hygieneResult, error) {
		switch args.Category {
		case "ghosts", "stuck", "mine_no_date":
		default:
			return nil, hygieneResult{}, fmt.Errorf("category must be ghosts, stuck or mine_no_date, got %q", args.Category)
		}
		cards, err := c.GetHygiene(ctx, args.Category)
		if err != nil {
			return nil, hygieneResult{}, err
		}
		return nil, hygieneResult{Cards: cards}, nil
	})

	// ── list_attachments ──────────────────────────────────────────
	type attachmentsResult struct {
		Attachments []Attachment `json:"attachments"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_attachments",
		Description: "List the files attached to a card (name, size, content type, who uploaded it).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args cardIDArgs) (*mcp.CallToolResult, attachmentsResult, error) {
		atts, err := c.ListAttachments(ctx, args.CardID)
		if err != nil {
			return nil, attachmentsResult{}, err
		}
		return nil, attachmentsResult{Attachments: atts}, nil
	})

	// ── download_attachment ───────────────────────────────────────
	type downloadArgs struct {
		AttachmentID string `json:"attachment_id" jsonschema:"attachment UUID (from list_attachments)"`
		MaxBytes     int    `json:"max_bytes,omitempty" jsonschema:"refuse to inline anything larger than this (default 262144, hard ceiling 5242880)"`
	}
	type downloadResult struct {
		ContentType string `json:"contentType"`
		Size        int    `json:"size"`
		Text        string `json:"text,omitempty"`
		Base64      string `json:"base64,omitempty"`
		Truncated   bool   `json:"truncated,omitempty"`
		Note        string `json:"note,omitempty"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "download_attachment",
		Description: "Fetch an attachment's bytes. Text-ish files come back as readable text; anything " +
			"else comes back base64-encoded. Attachments can be up to 25 MiB, which no conversation " +
			"wants inline, so this refuses to return anything over max_bytes and tells you the real " +
			"size instead - raise max_bytes deliberately if you need more.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args downloadArgs) (*mcp.CallToolResult, downloadResult, error) {
		const defaultMax, hardMax = 256 * 1024, 5 * 1024 * 1024
		limit := args.MaxBytes
		if limit <= 0 {
			limit = defaultMax
		}
		if limit > hardMax {
			limit = hardMax
		}
		data, ctype, err := c.DownloadAttachment(ctx, args.AttachmentID)
		if err != nil {
			return nil, downloadResult{}, err
		}
		res := downloadResult{ContentType: ctype, Size: len(data)}
		if isTextual(ctype) {
			// Truncating text still leaves something readable, so do it.
			if len(data) > limit {
				res.Text, res.Truncated = string(data[:limit]), true
				res.Note = fmt.Sprintf("truncated to %d of %d bytes", limit, len(data))
			} else {
				res.Text = string(data)
			}
			return nil, res, nil
		}
		// Truncated base64 is useless, so refuse rather than mislead.
		if len(data) > limit {
			res.Note = fmt.Sprintf("binary file is %d bytes, over the %d byte limit - not inlined. "+
				"Raise max_bytes if you really need it.", len(data), limit)
			return nil, res, nil
		}
		res.Base64 = base64.StdEncoding.EncodeToString(data)
		return nil, res, nil
	})

	// ── upload_attachment ─────────────────────────────────────────
	type uploadArgs struct {
		CardID      string `json:"card_id" jsonschema:"card UUID"`
		Filename    string `json:"filename" jsonschema:"filename to store, e.g. 'notes.md'"`
		ContentB64  string `json:"content_base64,omitempty" jsonschema:"file bytes, base64-encoded. Provide this OR content_text"`
		ContentText string `json:"content_text,omitempty" jsonschema:"file contents as plain text, for text files. Provide this OR content_base64"`
		ContentType string `json:"content_type,omitempty" jsonschema:"optional MIME type, e.g. 'text/markdown'"`
	}
	type uploadResult struct {
		Attachment Attachment `json:"attachment"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "upload_attachment",
		Description: "Attach a file to a card. This server runs remotely and cannot read your filesystem, " +
			"so send the contents inline: content_text for text, content_base64 for binary. Max 25 MiB. " +
			"Attachments are a paid feature - a Free workspace gets a 402 naming the current plan.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args uploadArgs) (*mcp.CallToolResult, uploadResult, error) {
		if (args.ContentB64 == "") == (args.ContentText == "") {
			return nil, uploadResult{}, fmt.Errorf("provide exactly one of content_base64 or content_text")
		}
		if args.Filename == "" {
			return nil, uploadResult{}, fmt.Errorf("filename is required")
		}
		var data []byte
		if args.ContentText != "" {
			data = []byte(args.ContentText)
		} else {
			decoded, err := base64.StdEncoding.DecodeString(args.ContentB64)
			if err != nil {
				return nil, uploadResult{}, fmt.Errorf("content_base64 is not valid base64: %w", err)
			}
			data = decoded
		}
		if len(data) == 0 {
			return nil, uploadResult{}, fmt.Errorf("file is empty")
		}
		if len(data) > 25*1024*1024 {
			return nil, uploadResult{}, fmt.Errorf("file is %d bytes, over the 25 MiB limit", len(data))
		}
		att, err := c.UploadAttachment(ctx, args.CardID, args.Filename, args.ContentType, data)
		if err != nil {
			return nil, uploadResult{}, err
		}
		return nil, uploadResult{Attachment: *att}, nil
	})

	// ── projects: get / create / update / delete ──────────────────
	type projectIDArgs struct {
		ProjectID string `json:"project_id" jsonschema:"project UUID"`
	}
	type projectResult struct {
		Project Project `json:"project"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_project",
		Description: "Get one project with its columns, counts and your role on it, without loading every card.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args projectIDArgs) (*mcp.CallToolResult, projectResult, error) {
		p, err := c.GetProject(ctx, args.ProjectID)
		if err != nil {
			return nil, projectResult{}, err
		}
		return nil, projectResult{Project: *p}, nil
	})

	type createProjectArgs struct {
		Name        string `json:"name" jsonschema:"board name, max 200 characters"`
		Description string `json:"description,omitempty" jsonschema:"optional description"`
		Template    string `json:"template,omitempty" jsonschema:"optional board template key from list_board_templates (e.g. 'sprint', 'bug-tracker'); omit for the default 'kanban' layout. An unknown key is rejected"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "create_project",
		Description: "Create a board. Without a template it starts with the default To Do / In Progress / " +
			"Done columns; pass template (a key from list_board_templates) to start from a different " +
			"column layout instead. IMPORTANT: the response from this call never includes columns (that " +
			"field is only populated by get_project/get_board, not by create) - and only the default " +
			"'kanban' template uses the todo/doing/done keys anyway, every other template generates its " +
			"own - so call get_project on the new board's id to read the real columns[].key before " +
			"creating cards on it. A Free workspace is capped at 3 projects and returns 402 beyond that.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args createProjectArgs) (*mcp.CallToolResult, projectResult, error) {
		if args.Name == "" {
			return nil, projectResult{}, fmt.Errorf("name is required")
		}
		p, err := c.CreateProject(ctx, args.Name, args.Description, args.Template)
		if err != nil {
			return nil, projectResult{}, err
		}
		return nil, projectResult{Project: *p}, nil
	})

	type updateProjectArgs struct {
		ProjectID   string `json:"project_id" jsonschema:"project UUID"`
		Name        string `json:"name,omitempty" jsonschema:"new name"`
		Description string `json:"description,omitempty" jsonschema:"new description"`
		IsFavorite  *bool  `json:"is_favorite,omitempty" jsonschema:"pin or unpin the board"`
		Visibility  string `json:"visibility,omitempty" jsonschema:"'private' (explicit grants only) or 'workspace' (every workspace member is a contributor)"`
		ColorScheme string `json:"color_scheme,omitempty" jsonschema:"board colour scheme, e.g. default, midnight, ocean, forest, sunset, graphite"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "update_project",
		Description: "Partially update a board. Omitted fields are left unchanged.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args updateProjectArgs) (*mcp.CallToolResult, projectResult, error) {
		patch := map[string]any{}
		if args.Name != "" {
			patch["name"] = args.Name
		}
		if args.Description != "" {
			patch["description"] = args.Description
		}
		if args.IsFavorite != nil {
			patch["isFavorite"] = *args.IsFavorite
		}
		if args.Visibility != "" {
			patch["visibility"] = args.Visibility
		}
		if args.ColorScheme != "" {
			patch["colorScheme"] = args.ColorScheme
		}
		if len(patch) == 0 {
			return nil, projectResult{}, fmt.Errorf("nothing to update")
		}
		p, err := c.UpdateProject(ctx, args.ProjectID, patch)
		if err != nil {
			return nil, projectResult{}, err
		}
		return nil, projectResult{Project: *p}, nil
	})

	type okResult struct {
		OK bool `json:"ok"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "delete_project",
		Description: "Permanently delete a board AND everything inside it - columns, cards, comments, " +
			"checklists and attachments all cascade. There is no undo.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args projectIDArgs) (*mcp.CallToolResult, okResult, error) {
		if err := c.DeleteProject(ctx, args.ProjectID); err != nil {
			return nil, okResult{}, err
		}
		return nil, okResult{OK: true}, nil
	})

	// ── export ────────────────────────────────────────────────────
	type exportArgs struct {
		ProjectID string `json:"project_id" jsonschema:"project UUID"`
		Format    string `json:"format,omitempty" jsonschema:"'json' (default) for a full snapshot including comments, or 'csv' for a flat one-row-per-card sheet"`
	}
	type exportResult struct {
		Format  string `json:"format"`
		Size    int    `json:"size"`
		Content string `json:"content"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "export_project",
		Description: "Export a board. JSON gives a versioned snapshot (project, columns, cards with " +
			"metadata, comments, attachment metadata); CSV gives one row per card. Returned as text.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args exportArgs) (*mcp.CallToolResult, exportResult, error) {
		format := args.Format
		if format == "" {
			format = "json"
		}
		if format != "json" && format != "csv" {
			return nil, exportResult{}, fmt.Errorf("format must be json or csv, got %q", format)
		}
		data, err := c.ExportProject(ctx, args.ProjectID, format)
		if err != nil {
			return nil, exportResult{}, err
		}
		return nil, exportResult{Format: format, Size: len(data), Content: string(data)}, nil
	})

	// ── columns ───────────────────────────────────────────────────
	type columnResult struct {
		Column Column `json:"column"`
	}
	type addColumnArgs struct {
		ProjectID string `json:"project_id" jsonschema:"project UUID"`
		Title     string `json:"title" jsonschema:"column title, max 80 characters"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "add_column",
		Description: "Add a column to a board. The server assigns its stable key, which is what card operations use as 'column'.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args addColumnArgs) (*mcp.CallToolResult, columnResult, error) {
		if args.Title == "" {
			return nil, columnResult{}, fmt.Errorf("title is required")
		}
		col, err := c.AddColumn(ctx, args.ProjectID, args.Title)
		if err != nil {
			return nil, columnResult{}, err
		}
		return nil, columnResult{Column: *col}, nil
	})

	type updateColumnArgs struct {
		ColumnID  string   `json:"column_id" jsonschema:"column UUID (the id, not the key)"`
		Title     string   `json:"title,omitempty" jsonschema:"new title, max 80 characters"`
		Color     string   `json:"color,omitempty" jsonschema:"palette token, or empty string to clear"`
		Position  *float64 `json:"position,omitempty" jsonschema:"new position; use a midpoint to slot between two columns"`
		IsDone    *bool    `json:"is_done,omitempty" jsonschema:"whether cards here count as done"`
		CardLimit *int     `json:"card_limit,omitempty" jsonschema:"WIP limit; 0 clears it"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "update_column",
		Description: "Rename, recolour, reorder or set the WIP limit on a column. Omitted fields are left unchanged.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args updateColumnArgs) (*mcp.CallToolResult, columnResult, error) {
		patch := map[string]any{}
		if args.Title != "" {
			patch["title"] = args.Title
		}
		if args.Color != "" {
			patch["color"] = args.Color
		}
		if args.Position != nil {
			patch["position"] = *args.Position
		}
		if args.IsDone != nil {
			patch["isDone"] = *args.IsDone
		}
		if args.CardLimit != nil {
			if *args.CardLimit == 0 {
				patch["cardLimit"] = nil
			} else {
				patch["cardLimit"] = *args.CardLimit
			}
		}
		if len(patch) == 0 {
			return nil, columnResult{}, fmt.Errorf("nothing to update")
		}
		col, err := c.UpdateColumn(ctx, args.ColumnID, patch)
		if err != nil {
			return nil, columnResult{}, err
		}
		return nil, columnResult{Column: *col}, nil
	})

	type columnIDArgs struct {
		ColumnID string `json:"column_id" jsonschema:"column UUID (the id, not the key)"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "delete_column",
		Description: "Delete an empty column. Returns 409 if it still holds cards - move them with " +
			"bulk_move_cards or clear it with archive_column first.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args columnIDArgs) (*mcp.CallToolResult, okResult, error) {
		if err := c.DeleteColumn(ctx, args.ColumnID); err != nil {
			return nil, okResult{}, err
		}
		return nil, okResult{OK: true}, nil
	})

	type archiveResult struct {
		Archived int      `json:"archived"`
		IDs      []string `json:"ids"`
		Note     string   `json:"note,omitempty"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "archive_column",
		Description: "Archive every card currently in a column. The cards leave the board but are not " +
			"deleted, and the column itself stays. IMPORTANT: the server only archives cards in a " +
			"column marked as a done column (is_done), and returns 200 with a count of 0 otherwise - " +
			"it does not error. Check the returned count rather than assuming it worked. The returned " +
			"ids let you undo the sweep by calling restore_card on each one.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args columnIDArgs) (*mcp.CallToolResult, archiveResult, error) {
		n, ids, err := c.ArchiveColumn(ctx, args.ColumnID)
		if err != nil {
			return nil, archiveResult{}, err
		}
		res := archiveResult{Archived: n, IDs: ids}
		if n == 0 {
			res.Note = "nothing archived - the column was already empty, or it is not marked as a " +
				"done column (set is_done with update_column if that is what you intended)"
		}
		return nil, res, nil
	})

	// ── card archive / restore ──────────────────────────────────────
	type archiveCardResult struct {
		Archived bool   `json:"archived"`
		ID       string `json:"id"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "archive_card",
		Description: "Archive a card: take it off its board without deleting it. It stays searchable and " +
			"shows up in list_archived_cards until restored. Archiving a card that is already archived, " +
			"or one you can't reach, returns an error.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args cardIDArgs) (*mcp.CallToolResult, archiveCardResult, error) {
		if err := c.ArchiveCard(ctx, args.CardID); err != nil {
			return nil, archiveCardResult{}, err
		}
		return nil, archiveCardResult{Archived: true, ID: args.CardID}, nil
	})

	type restoreCardResult struct {
		Card Card `json:"card"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "restore_card",
		Description: "Restore an archived card. It goes back to the exact column and position it was " +
			"archived from, so don't follow this with a move_card or update_card to 'put it back' - " +
			"restore already does that. Restoring a card that isn't archived returns an error.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args cardIDArgs) (*mcp.CallToolResult, restoreCardResult, error) {
		card, err := c.RestoreCard(ctx, args.CardID)
		if err != nil {
			return nil, restoreCardResult{}, err
		}
		return nil, restoreCardResult{Card: *card}, nil
	})

	type archivedCardsResult struct {
		Cards []ArchivedCard `json:"cards"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "list_archived_cards",
		Description: "List a board's archived cards, most recently archived first, capped at 200. Same " +
			"lean shape as get_board's cards (description omitted) plus archivedAt. Older archived cards " +
			"beyond the cap stay reachable through search_cards. An inaccessible board returns an empty " +
			"list rather than an error.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args projectIDArgs) (*mcp.CallToolResult, archivedCardsResult, error) {
		cards, err := c.ListArchivedCards(ctx, args.ProjectID)
		if err != nil {
			return nil, archivedCardsResult{}, err
		}
		return nil, archivedCardsResult{Cards: cards}, nil
	})

	// ── board templates ──────────────────────────────────────────────
	type boardTemplatesResult struct {
		Templates []BoardTemplate `json:"templates"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "list_board_templates",
		Description: "List the fixed catalogue of board templates (kanban, sprint, bug tracker, content " +
			"calendar, sales pipeline, ...) - the same for every caller, any authenticated user, no plan " +
			"gate. Pass a template's key as the template argument on create_project to start a board with " +
			"that column layout instead of the default To Do / In Progress / Done. Only the default " +
			"'kanban' template seeds todo/doing/done column keys - every other template generates its " +
			"own, and this list doesn't expose them, so read columns[].key from the created project.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, boardTemplatesResult, error) {
		templates, err := c.ListBoardTemplates(ctx)
		if err != nil {
			return nil, boardTemplatesResult{}, err
		}
		return nil, boardTemplatesResult{Templates: templates}, nil
	})

	// ── webhooks (outbound) ──────────────────────────────────────────
	type webhookResult struct {
		Webhook WebhookSubscription `json:"webhook"`
	}
	type webhooksResult struct {
		Webhooks       []WebhookSubscription `json:"webhooks"`
		EventCatalogue []string              `json:"eventCatalogue"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "list_webhooks",
		Description: "List a board's outbound webhook subscriptions plus the catalogue of broadcast " +
			"event names available to filter on. Board admins only - a Free workspace gets 402, a board " +
			"member who isn't an admin gets 403. Each subscription's signing secret is included in full " +
			"on every read - handle it as sensitive, never print or log it whole.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args projectIDArgs) (*mcp.CallToolResult, webhooksResult, error) {
		whs, cat, err := c.ListWebhooks(ctx, args.ProjectID)
		if err != nil {
			return nil, webhooksResult{}, err
		}
		return nil, webhooksResult{Webhooks: whs, EventCatalogue: cat}, nil
	})

	type createWebhookArgs struct {
		ProjectID   string   `json:"project_id" jsonschema:"project UUID"`
		URL         string   `json:"url" jsonschema:"absolute http(s) destination URL; a private, loopback or link-local address is rejected"`
		Description string   `json:"description,omitempty" jsonschema:"optional label"`
		Format      string   `json:"format,omitempty" jsonschema:"'craaft' (default, signed JSON envelope), 'slack', or 'discord' (message posted to those services' incoming-webhook URLs, unsigned)"`
		Events      []string `json:"events,omitempty" jsonschema:"broadcast event names to deliver, from list_webhooks' event_catalogue (e.g. 'card.created', 'card.updated'); omit or empty to subscribe to every event"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "create_webhook",
		Description: "Register an outbound webhook on a board: Craaft POSTs matching board events to the " +
			"given URL as they happen. Board admins on Pro boards only - a Free workspace gets 402. The " +
			"response carries a signing secret for 'craaft'-format deliveries (X-Craaft-Signature header); " +
			"treat it as sensitive, it is returned again in full on every list/update read, never " +
			"redacted. 'slack' and 'discord' deliveries are unsigned chat messages instead, authenticated " +
			"only by the secret URL itself.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args createWebhookArgs) (*mcp.CallToolResult, webhookResult, error) {
		if args.URL == "" {
			return nil, webhookResult{}, fmt.Errorf("url is required")
		}
		wh, err := c.CreateWebhook(ctx, args.ProjectID, args.URL, args.Description, args.Format, args.Events)
		if err != nil {
			return nil, webhookResult{}, err
		}
		return nil, webhookResult{Webhook: *wh}, nil
	})

	type updateWebhookArgs struct {
		SubscriptionID string    `json:"subscription_id" jsonschema:"webhook subscription UUID - the 'id' field from list_webhooks/create_webhook, NOT endpointId"`
		URL            string    `json:"url,omitempty" jsonschema:"new destination URL (omit to leave unchanged)"`
		Description    string    `json:"description,omitempty" jsonschema:"new label (omit to leave unchanged)"`
		Format         string    `json:"format,omitempty" jsonschema:"'craaft', 'slack' or 'discord' (omit to leave unchanged)"`
		Events         *[]string `json:"events,omitempty" jsonschema:"replace the event filter (empty array = subscribe to every event); omit to leave unchanged"`
		Active         *bool     `json:"active,omitempty" jsonschema:"pause (false) or resume (true) delivery"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "update_webhook",
		Description: "Update a webhook subscription: pause/resume it, or change its URL, format or event " +
			"filter. Omitted fields are left unchanged. Needs Pro even though the webhook already exists " +
			"- on a downgraded (Free) board this returns 402; delete_webhook stays open so a downgraded " +
			"workspace can still clean up.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args updateWebhookArgs) (*mcp.CallToolResult, webhookResult, error) {
		patch := map[string]any{}
		if args.URL != "" {
			patch["url"] = args.URL
		}
		if args.Description != "" {
			patch["description"] = args.Description
		}
		if args.Format != "" {
			patch["format"] = args.Format
		}
		if args.Events != nil {
			patch["events"] = *args.Events
		}
		if args.Active != nil {
			patch["active"] = *args.Active
		}
		if len(patch) == 0 {
			return nil, webhookResult{}, fmt.Errorf("nothing to update")
		}
		wh, err := c.UpdateWebhook(ctx, args.SubscriptionID, patch)
		if err != nil {
			return nil, webhookResult{}, err
		}
		return nil, webhookResult{Webhook: *wh}, nil
	})

	type webhookIDArgs struct {
		SubscriptionID string `json:"subscription_id" jsonschema:"webhook subscription UUID - the 'id' field, NOT endpointId"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_webhook",
		Description: "Delete a webhook subscription. Board admin only. Not plan-gated, so a downgraded workspace can still remove one.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args webhookIDArgs) (*mcp.CallToolResult, okResult, error) {
		if err := c.DeleteWebhook(ctx, args.SubscriptionID); err != nil {
			return nil, okResult{}, err
		}
		return nil, okResult{OK: true}, nil
	})

	// ── email-to-card (inbound email) ─────────────────────────────────
	type inboundEmailResult struct {
		Enabled bool            `json:"enabled"`
		Address *InboundAddress `json:"address,omitempty"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "get_inbound_email",
		Description: "Get a board's email-to-card intake address. Board admins on Pro boards only. " +
			"Returns enabled=false with no address when it has never been set up - that is not an error. " +
			"Mail to the address becomes a card (subject -> title, body -> description) only when the " +
			"sender is a workspace member and SPF/DKIM pass; everything else is dropped silently.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args projectIDArgs) (*mcp.CallToolResult, inboundEmailResult, error) {
		enabled, addr, err := c.GetInboundEmail(ctx, args.ProjectID)
		if err != nil {
			return nil, inboundEmailResult{}, err
		}
		return nil, inboundEmailResult{Enabled: enabled, Address: addr}, nil
	})

	type addressResult struct {
		Address InboundAddress `json:"address"`
	}
	type enableInboundEmailArgs struct {
		ProjectID    string `json:"project_id" jsonschema:"project UUID"`
		TargetColumn string `json:"target_column,omitempty" jsonschema:"column key to place inbound cards in; omit for the board's first column"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "enable_inbound_email",
		Description: "Turn on email-to-card for a board and mint its intake address. Board admins on Pro " +
			"boards only. Returns an error (409 from the API) if the board already has an address - use " +
			"update_inbound_email or disable_inbound_email first.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args enableInboundEmailArgs) (*mcp.CallToolResult, addressResult, error) {
		var target *string
		if args.TargetColumn != "" {
			target = &args.TargetColumn
		}
		addr, err := c.EnableInboundEmail(ctx, args.ProjectID, target)
		if err != nil {
			return nil, addressResult{}, err
		}
		return nil, addressResult{Address: *addr}, nil
	})

	type updateInboundEmailArgs struct {
		ProjectID         string `json:"project_id" jsonschema:"project UUID"`
		Active            *bool  `json:"active,omitempty" jsonschema:"turn intake on or off without losing the address"`
		TargetColumn      string `json:"target_column,omitempty" jsonschema:"new column key for inbound cards"`
		ClearTargetColumn bool   `json:"clear_target_column,omitempty" jsonschema:"set true to reset to the board's first column"`
		Rotate            bool   `json:"rotate,omitempty" jsonschema:"set true to mint a new token; the old address stops accepting mail immediately"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "update_inbound_email",
		Description: "Partially update a board's email-to-card configuration. Board admins on Pro boards " +
			"only - a downgraded (Free) board returns 402 here (disable_inbound_email stays open). " +
			"Omitted fields are left unchanged.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args updateInboundEmailArgs) (*mcp.CallToolResult, addressResult, error) {
		patch := map[string]any{}
		if args.Active != nil {
			patch["active"] = *args.Active
		}
		if args.ClearTargetColumn {
			patch["targetColumn"] = ""
		} else if args.TargetColumn != "" {
			patch["targetColumn"] = args.TargetColumn
		}
		if args.Rotate {
			patch["rotate"] = true
		}
		if len(patch) == 0 {
			return nil, addressResult{}, fmt.Errorf("nothing to update: pass active, target_column, clear_target_column and/or rotate")
		}
		addr, err := c.UpdateInboundEmail(ctx, args.ProjectID, patch)
		if err != nil {
			return nil, addressResult{}, err
		}
		return nil, addressResult{Address: *addr}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "disable_inbound_email",
		Description: "Turn off email-to-card for a board. Board admins only. Not plan-gated, so a downgraded workspace can still disable it.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args projectIDArgs) (*mcp.CallToolResult, okResult, error) {
		if err := c.DisableInboundEmail(ctx, args.ProjectID); err != nil {
			return nil, okResult{}, err
		}
		return nil, okResult{OK: true}, nil
	})

	// ── members + invitations ─────────────────────────────────────
	type membersResult struct {
		Members []Member `json:"members"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_members",
		Description: "List workspace members with their roles and, where visible, which boards they can see.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, membersResult, error) {
		m, err := c.ListMembers(ctx)
		if err != nil {
			return nil, membersResult{}, err
		}
		return nil, membersResult{Members: m}, nil
	})

	type memberRoleArgs struct {
		UserID string `json:"user_id" jsonschema:"the member's user UUID"`
		Role   string `json:"role" jsonschema:"'admin' or 'member'"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "update_member_role",
		Description: "Change a workspace member's role. Promoting to admin grants visibility of every " +
			"board in the workspace.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args memberRoleArgs) (*mcp.CallToolResult, okResult, error) {
		if args.Role != "admin" && args.Role != "member" {
			return nil, okResult{}, fmt.Errorf("role must be admin or member, got %q", args.Role)
		}
		if err := c.UpdateMemberRole(ctx, args.UserID, args.Role); err != nil {
			return nil, okResult{}, err
		}
		return nil, okResult{OK: true}, nil
	})

	type userIDArgs struct {
		UserID string `json:"user_id" jsonschema:"the member's user UUID"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "remove_member",
		Description: "Remove someone from the workspace. They lose access to every board in it immediately.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args userIDArgs) (*mcp.CallToolResult, okResult, error) {
		if err := c.RemoveMember(ctx, args.UserID); err != nil {
			return nil, okResult{}, err
		}
		return nil, okResult{OK: true}, nil
	})

	type invitationsResult struct {
		Invitations []Invitation `json:"invitations"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_invitations",
		Description: "List pending workspace invitations that haven't been accepted yet.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, invitationsResult, error) {
		inv, err := c.ListInvitations(ctx)
		if err != nil {
			return nil, invitationsResult{}, err
		}
		return nil, invitationsResult{Invitations: inv}, nil
	})

	type inviteArgs struct {
		Email       string           `json:"email" jsonschema:"email address to invite"`
		Role        string           `json:"role" jsonschema:"'admin' or 'member'"`
		BoardGrants []map[string]any `json:"board_grants,omitempty" jsonschema:"optional [{projectId, role}] board grants. Only honoured for role 'member' - admins see every board automatically. Board role is 'admin' or 'contributor'"`
	}
	type invitationResult struct {
		Invitation Invitation `json:"invitation"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "invite_member",
		Description: "Invite someone to the workspace by email. If they already have an account the " +
			"invitation is consumed immediately; otherwise it applies when they register. Board grants " +
			"are applied atomically on accept.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args inviteArgs) (*mcp.CallToolResult, invitationResult, error) {
		if args.Role != "admin" && args.Role != "member" {
			return nil, invitationResult{}, fmt.Errorf("role must be admin or member, got %q", args.Role)
		}
		inv, err := c.InviteMember(ctx, args.Email, args.Role, args.BoardGrants)
		if err != nil {
			return nil, invitationResult{}, err
		}
		return nil, invitationResult{Invitation: *inv}, nil
	})

	type invitationIDArgs struct {
		InvitationID string `json:"invitation_id" jsonschema:"invitation UUID"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "revoke_invitation",
		Description: "Revoke a pending invitation. The link in their email stops working; you can invite them again later.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args invitationIDArgs) (*mcp.CallToolResult, okResult, error) {
		if err := c.RevokeInvitation(ctx, args.InvitationID); err != nil {
			return nil, okResult{}, err
		}
		return nil, okResult{OK: true}, nil
	})

	type boardMembersResult struct {
		Members []BoardMember `json:"members"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "list_board_members",
		Description: "List who can see a specific board. Each row's 'source' says whether the access is " +
			"an explicit grant or implied by their workspace role.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args projectIDArgs) (*mcp.CallToolResult, boardMembersResult, error) {
		m, err := c.ListBoardMembers(ctx, args.ProjectID)
		if err != nil {
			return nil, boardMembersResult{}, err
		}
		return nil, boardMembersResult{Members: m}, nil
	})

	type grantArgs struct {
		ProjectID string `json:"project_id" jsonschema:"project UUID"`
		UserID    string `json:"user_id" jsonschema:"user UUID to grant"`
		Role      string `json:"role" jsonschema:"'admin' (can manage the board) or 'contributor'"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "grant_board_access",
		Description: "Give a workspace member access to a specific board. Board admins only. Only " +
			"meaningful for workspace 'member' role users - owners and admins already see every board.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args grantArgs) (*mcp.CallToolResult, okResult, error) {
		if args.Role != "admin" && args.Role != "contributor" {
			return nil, okResult{}, fmt.Errorf("role must be admin or contributor, got %q", args.Role)
		}
		if err := c.GrantBoardAccess(ctx, args.ProjectID, args.UserID, args.Role); err != nil {
			return nil, okResult{}, err
		}
		return nil, okResult{OK: true}, nil
	})

	type revokeGrantArgs struct {
		ProjectID string `json:"project_id" jsonschema:"project UUID"`
		UserID    string `json:"user_id" jsonschema:"user UUID whose grant to remove"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "revoke_board_access",
		Description: "Remove someone's explicit grant on a board. Board admins, or the person removing their own access.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args revokeGrantArgs) (*mcp.CallToolResult, okResult, error) {
		if err := c.RevokeBoardAccess(ctx, args.ProjectID, args.UserID); err != nil {
			return nil, okResult{}, err
		}
		return nil, okResult{OK: true}, nil
	})

	// ── comment edit / delete ─────────────────────────────────────
	type updateCommentArgs struct {
		CommentID string `json:"comment_id" jsonschema:"comment UUID"`
		Body      string `json:"body" jsonschema:"replacement text, max 5000 characters"`
	}
	type commentResult struct {
		Comment Comment `json:"comment"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "update_comment",
		Description: "Edit a comment. Author only - editing someone else's returns 404.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args updateCommentArgs) (*mcp.CallToolResult, commentResult, error) {
		if args.Body == "" {
			return nil, commentResult{}, fmt.Errorf("body is required")
		}
		cm, err := c.UpdateComment(ctx, args.CommentID, args.Body)
		if err != nil {
			return nil, commentResult{}, err
		}
		return nil, commentResult{Comment: *cm}, nil
	})

	type commentIDArgs struct {
		CommentID string `json:"comment_id" jsonschema:"comment UUID"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_comment",
		Description: "Delete a comment. Allowed for its author, or a workspace owner/admin.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args commentIDArgs) (*mcp.CallToolResult, okResult, error) {
		if err := c.DeleteComment(ctx, args.CommentID); err != nil {
			return nil, okResult{}, err
		}
		return nil, okResult{OK: true}, nil
	})

	// ── attachment delete ─────────────────────────────────────────
	type attachmentIDArgs struct {
		AttachmentID string `json:"attachment_id" jsonschema:"attachment UUID"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_attachment",
		Description: "Delete an attachment - both the metadata row and the stored bytes. There is no undo.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args attachmentIDArgs) (*mcp.CallToolResult, okResult, error) {
		if err := c.DeleteAttachment(ctx, args.AttachmentID); err != nil {
			return nil, okResult{}, err
		}
		return nil, okResult{OK: true}, nil
	})

	// ── card follow / unfollow ────────────────────────────────────
	type followArgs struct {
		CardID string `json:"card_id" jsonschema:"card UUID"`
		Follow *bool  `json:"follow" jsonschema:"true to follow, false to unfollow"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "set_card_follow",
		Description: "Follow or unfollow a card. Followers are notified when it moves or changes. " +
			"Assigning a card follows it automatically, so this is mainly for watching someone else's work.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args followArgs) (*mcp.CallToolResult, okResult, error) {
		if args.Follow == nil {
			return nil, okResult{}, fmt.Errorf("follow is required: true to follow, false to unfollow")
		}
		if err := c.SetCardFollow(ctx, args.CardID, *args.Follow); err != nil {
			return nil, okResult{}, err
		}
		return nil, okResult{OK: true}, nil
	})

	// ── list_tags ─────────────────────────────────────────────────
	type tagsResult struct {
		Tags []string `json:"tags"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "list_tags",
		Description: "List the distinct tags used on a board's cards, sorted, covering archived cards " +
			"as well as live ones. Useful for picking an existing tag rather than inventing a near-" +
			"duplicate. Note: a caller without access to the board gets an empty list rather than an " +
			"error, so an empty result means either no tags or no access.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args projectIDArgs) (*mcp.CallToolResult, tagsResult, error) {
		tags, err := c.ListTags(ctx, args.ProjectID)
		if err != nil {
			return nil, tagsResult{}, err
		}
		return nil, tagsResult{Tags: tags}, nil
	})

	// ── milestones ────────────────────────────────────────────────
	type milestonesResult struct {
		Milestones []Milestone `json:"milestones"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_milestones",
		Description: "List a board's milestones, earliest due date first.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args projectIDArgs) (*mcp.CallToolResult, milestonesResult, error) {
		ms, err := c.ListMilestones(ctx, args.ProjectID)
		if err != nil {
			return nil, milestonesResult{}, err
		}
		return nil, milestonesResult{Milestones: ms}, nil
	})

	type addMilestoneArgs struct {
		ProjectID string `json:"project_id" jsonschema:"project UUID"`
		Name      string `json:"name" jsonschema:"milestone name, max 200 characters"`
		DueOn     string `json:"due_on" jsonschema:"due date as YYYY-MM-DD - a calendar date, not a timestamp"`
	}
	type milestoneResult struct {
		Milestone Milestone `json:"milestone"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "add_milestone",
		Description: "Add a milestone to a board. Board admins only (workspace owners/admins, or explicit board admins).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args addMilestoneArgs) (*mcp.CallToolResult, milestoneResult, error) {
		if args.Name == "" || args.DueOn == "" {
			return nil, milestoneResult{}, fmt.Errorf("name and due_on are both required")
		}
		m, err := c.AddMilestone(ctx, args.ProjectID, args.Name, args.DueOn)
		if err != nil {
			return nil, milestoneResult{}, err
		}
		return nil, milestoneResult{Milestone: *m}, nil
	})

	type updateMilestoneArgs struct {
		MilestoneID string `json:"milestone_id" jsonschema:"milestone UUID"`
		Name        string `json:"name,omitempty" jsonschema:"new name"`
		DueOn       string `json:"due_on,omitempty" jsonschema:"new due date as YYYY-MM-DD"`
		Achieved    *bool  `json:"achieved,omitempty" jsonschema:"mark achieved or not. Setting true stamps achievedAt once; re-sending true keeps the original stamp"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "update_milestone",
		Description: "Rename, re-date or mark a milestone achieved. Board admins only. Omitted fields are left unchanged.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args updateMilestoneArgs) (*mcp.CallToolResult, milestoneResult, error) {
		patch := map[string]any{}
		if args.Name != "" {
			patch["name"] = args.Name
		}
		if args.DueOn != "" {
			patch["dueOn"] = args.DueOn
		}
		if args.Achieved != nil {
			patch["achieved"] = *args.Achieved
		}
		if len(patch) == 0 {
			return nil, milestoneResult{}, fmt.Errorf("nothing to update: pass name, due_on and/or achieved")
		}
		m, err := c.UpdateMilestone(ctx, args.MilestoneID, patch)
		if err != nil {
			return nil, milestoneResult{}, err
		}
		return nil, milestoneResult{Milestone: *m}, nil
	})

	type milestoneIDArgs struct {
		MilestoneID string `json:"milestone_id" jsonschema:"milestone UUID"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_milestone",
		Description: "Delete a milestone. Board admins only.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args milestoneIDArgs) (*mcp.CallToolResult, okResult, error) {
		if err := c.DeleteMilestone(ctx, args.MilestoneID); err != nil {
			return nil, okResult{}, err
		}
		return nil, okResult{OK: true}, nil
	})

	// ── public sharing ────────────────────────────────────────────
	type shareResult struct {
		PublicToken string `json:"publicToken"`
		URL         string `json:"url"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "enable_sharing",
		Description: "Turn on read-only public sharing for a board and return the share token and URL. " +
			"Anyone with the link can view the board without signing in; they cannot add, edit or move " +
			"anything. Idempotent - an already-shared board returns its existing token, so the URL stays stable.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args projectIDArgs) (*mcp.CallToolResult, shareResult, error) {
		token, err := c.EnableShare(ctx, args.ProjectID)
		if err != nil {
			return nil, shareResult{}, err
		}
		return nil, shareResult{PublicToken: token, URL: c.baseURL + "/share/" + token}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "disable_sharing",
		Description: "Revoke a board's public link. Existing links stop working immediately, and " +
			"re-enabling later mints a NEW token rather than restoring the old one.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args projectIDArgs) (*mcp.CallToolResult, okResult, error) {
		if err := c.DisableShare(ctx, args.ProjectID); err != nil {
			return nil, okResult{}, err
		}
		return nil, okResult{OK: true}, nil
	})

	// ── avatar ────────────────────────────────────────────────────
	type avatarArgs struct {
		UserID string `json:"user_id" jsonschema:"user UUID"`
	}
	type avatarResult struct {
		HasAvatar   bool   `json:"hasAvatar"`
		URL         string `json:"url"`
		ContentType string `json:"contentType,omitempty"`
		Size        int    `json:"size,omitempty"`
		Base64      string `json:"base64,omitempty"`
		Note        string `json:"note,omitempty"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "get_avatar",
		Description: "Fetch a user's avatar. Returns the URL plus the bytes base64-encoded (avatars cap " +
			"at 5 MiB). When the user has never uploaded one this is NOT an error - it returns " +
			"hasAvatar false, which is the cue to fall back to their initial and colour.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args avatarArgs) (*mcp.CallToolResult, avatarResult, error) {
		u := c.baseURL + "/api/v1/users/" + url.PathEscape(args.UserID) + "/avatar"
		data, ctype, err := c.GetAvatar(ctx, args.UserID)
		if errors.Is(err, ErrNoAvatar) {
			return nil, avatarResult{
				HasAvatar: false, URL: u,
				Note: "no uploaded avatar - render the initial-and-colour placeholder",
			}, nil
		}
		if err != nil {
			return nil, avatarResult{}, err
		}
		return nil, avatarResult{
			HasAvatar: true, URL: u, ContentType: ctype,
			Size: len(data), Base64: base64.StdEncoding.EncodeToString(data),
		}, nil
	})

	// ── me ────────────────────────────────────────────────────────
	type meResult struct {
		User json.RawMessage `json:"user"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_me",
		Description: "Get the current authenticated user's profile (name, email, username).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, meResult, error) {
		data, _, err := c.do(ctx, "GET", "/api/v1/me", nil)
		if err != nil {
			return nil, meResult{}, err
		}
		return nil, meResult{User: data}, nil
	})
}

// isTextual reports whether an attachment's content type is safe to return
// as readable text rather than base64.
func isTextual(contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	if strings.HasPrefix(ct, "text/") {
		return true
	}
	switch ct {
	case "application/json", "application/xml", "application/yaml",
		"application/x-yaml", "application/javascript", "application/sql",
		"application/toml", "image/svg+xml":
		return true
	}
	return strings.HasSuffix(ct, "+json") || strings.HasSuffix(ct, "+xml")
}
