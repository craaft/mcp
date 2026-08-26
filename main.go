package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
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
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"status":"ok"}`)
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
		Name:        "get_board",
		Description: "Get a Kanban board: its columns and every card with metadata (title, description, due date, assignee, priority, size, tags, checklist progress).",
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
		Name:        "search_cards",
		Description: "Search for cards across all accessible projects by title and description text.",
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
