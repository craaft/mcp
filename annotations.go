package main

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Tool annotations tell clients what a tool does before they call it: a
// read-only tool can run without asking, a destructive one should be
// confirmed. Every tool talks only to the craaft API, so openWorldHint is
// false throughout.
//
// destructiveHint follows the spec: true for anything that is not purely
// additive, so updates that overwrite or clear values count as destructive
// alongside deletes. Archiving and moving are reversible and lose nothing, so
// they don't.

func boolPtr(b bool) *bool { return &b }

// readTool annotates a tool that only reads.
func readTool(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, OpenWorldHint: boolPtr(false)}
}

// additiveTool annotates a tool that only adds or relocates data.
func additiveTool(title string, idempotent bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, DestructiveHint: boolPtr(false), IdempotentHint: idempotent, OpenWorldHint: boolPtr(false)}
}

// destructiveTool annotates a tool that overwrites, clears or removes data.
func destructiveTool(title string, idempotent bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, DestructiveHint: boolPtr(true), IdempotentHint: idempotent, OpenWorldHint: boolPtr(false)}
}

// toolAnnotations holds the annotations for every registered tool, keyed by
// tool name. registerTools panics on a tool missing from here, and a test
// checks nothing here is left unregistered.
var toolAnnotations = map[string]*mcp.ToolAnnotations{
	// Account and notifications
	"get_me":                  readTool("Get my account"),
	"get_avatar":              readTool("Get a user's avatar"),
	"get_focus":               readTool("Get my focus overview"),
	"get_hygiene":             readTool("Get board hygiene details"),
	"list_notifications":      readTool("List notifications"),
	"mark_notifications_read": additiveTool("Mark notifications read", true),

	// Projects
	"list_projects":        readTool("List boards"),
	"get_project":          readTool("Get a board's settings"),
	"get_board":            readTool("Get a board and its cards"),
	"list_board_templates": readTool("List board templates"),
	"create_project":       additiveTool("Create a board", false),
	"update_project":       destructiveTool("Update a board", true),
	"delete_project":       destructiveTool("Delete a board", true),
	"export_project":       readTool("Export a board"),
	"preview_import":       readTool("Preview a board import"),
	"import_board":         additiveTool("Import a board", false),
	"enable_sharing":       additiveTool("Turn on public sharing", true),
	"disable_sharing":      destructiveTool("Turn off public sharing", true),
	"list_tags":            readTool("List a board's tags"),

	// Columns
	"add_column":     additiveTool("Add a column", false),
	"update_column":  destructiveTool("Update a column", true),
	"delete_column":  destructiveTool("Delete a column", true),
	"archive_column": additiveTool("Archive a column's cards", true),

	// Cards
	"search_cards":        readTool("Search cards"),
	"get_card":            readTool("Get a card"),
	"get_card_detail":     readTool("Get a card with its comments and history"),
	"get_card_prompt":     readTool("Get a card as a prompt"),
	"get_card_activity":   readTool("Get a card's activity"),
	"get_board_activity":  readTool("Get a board's activity"),
	"create_card":         additiveTool("Create a card", false),
	"update_card":         destructiveTool("Update a card", true),
	"move_card":           additiveTool("Move a card", true),
	"delete_card":         destructiveTool("Delete a card", true),
	"archive_card":        additiveTool("Archive a card", true),
	"restore_card":        additiveTool("Restore an archived card", true),
	"list_archived_cards": readTool("List archived cards"),
	"bulk_create_cards":   additiveTool("Create cards in bulk", false),
	"bulk_update_cards":   destructiveTool("Update cards in bulk", true),
	"bulk_move_cards":     additiveTool("Move cards in bulk", true),
	"rebalance_cards":     additiveTool("Reorder a column", true),
	"set_card_follow":     additiveTool("Follow or unfollow a card", true),

	// Comments
	"get_comments":   readTool("List a card's comments"),
	"add_comment":    additiveTool("Add a comment", false),
	"update_comment": destructiveTool("Edit a comment", true),
	"delete_comment": destructiveTool("Delete a comment", true),

	// Checklists
	"get_checklist":         readTool("Get a card's checklist"),
	"add_checklist_item":    additiveTool("Add a checklist item", false),
	"update_checklist_item": destructiveTool("Update a checklist item", true),
	"delete_checklist_item": destructiveTool("Delete a checklist item", true),

	// Attachments
	"list_attachments":    readTool("List a card's attachments"),
	"list_board_files":    readTool("List a board's files"),
	"download_attachment": readTool("Download an attachment"),
	"upload_attachment":   additiveTool("Upload an attachment", false),
	"delete_attachment":   destructiveTool("Delete an attachment", true),

	// Milestones
	"list_milestones":  readTool("List milestones"),
	"add_milestone":    additiveTool("Add a milestone", false),
	"update_milestone": destructiveTool("Update a milestone", true),
	"delete_milestone": destructiveTool("Delete a milestone", true),

	// Webhooks and email to card
	"list_webhooks":         readTool("List webhooks"),
	"create_webhook":        additiveTool("Create a webhook", false),
	"update_webhook":        destructiveTool("Update a webhook", true),
	"delete_webhook":        destructiveTool("Delete a webhook", true),
	"get_inbound_email":     readTool("Get email-to-card settings"),
	"enable_inbound_email":  additiveTool("Turn on email to card", false),
	"update_inbound_email":  destructiveTool("Update email-to-card settings", false), // rotate mints a new address each call
	"disable_inbound_email": destructiveTool("Turn off email to card", true),

	// Members and access
	"list_members":        readTool("List workspace members"),
	"update_member_role":  destructiveTool("Change a member's role", true),
	"remove_member":       destructiveTool("Remove a workspace member", true),
	"list_invitations":    readTool("List pending invitations"),
	"invite_member":       additiveTool("Invite someone to the workspace", false),
	"revoke_invitation":   destructiveTool("Revoke an invitation", true),
	"list_board_members":  readTool("List board members"),
	"grant_board_access":  destructiveTool("Grant board access", true), // also changes an existing grant's role
	"revoke_board_access": destructiveTool("Revoke board access", true),
}

// addTool registers a tool with its annotations from toolAnnotations. Use it
// instead of mcp.AddTool so no tool ships unannotated.
func addTool[In, Out any](s *mcp.Server, t *mcp.Tool, h mcp.ToolHandlerFor[In, Out]) {
	a, ok := toolAnnotations[t.Name]
	if !ok {
		panic("craaft-mcp: no annotations for tool " + t.Name)
	}
	ann := *a
	t.Annotations = &ann
	t.Title = ann.Title
	mcp.AddTool(s, t, withRequestToken(h))
}

// withRequestToken runs h with the bearer token of the request that carried
// this tool call, taken from the SDK's per-request TokenInfo. Tools then call
// upstream as that caller, whatever token the session started with.
func withRequestToken[In, Out any](h mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		if req != nil && req.Extra != nil && req.Extra.TokenInfo != nil {
			if tok, _ := req.Extra.TokenInfo.Extra[tokenExtraKey].(string); tok != "" {
				ctx = withToken(ctx, tok)
			}
		}
		return h(ctx, req, in)
	}
}
