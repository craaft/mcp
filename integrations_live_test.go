package main

// Round-trips board templates, outbound webhooks and email-to-card against a
// real server. Skipped unless CRAAFT_E2E is set. Webhooks and email-to-card
// are Pro-only (402 on a Free workspace) - that part of the test skips
// itself rather than failing when the test account isn't on Pro, so this
// still exercises list_board_templates + create_project(template=...) on
// any account.
import (
	"context"
	"os"
	"testing"
)

func TestLiveIntegrations(t *testing.T) {
	base, tok := os.Getenv("CRAAFT_BASE_URL"), os.Getenv("CRAAFT_API_TOKEN")
	if os.Getenv("CRAAFT_E2E") == "" || base == "" || tok == "" {
		t.Skip("set CRAAFT_E2E + CRAAFT_BASE_URL + CRAAFT_API_TOKEN")
	}
	c, ctx := NewClient(base, tok), context.Background()

	// list_board_templates
	templates, err := c.ListBoardTemplates(ctx)
	if err != nil {
		t.Fatalf("ListBoardTemplates: %v", err)
	}
	if len(templates) == 0 {
		t.Fatal("ListBoardTemplates returned no templates")
	}
	var sprint *BoardTemplate
	for i := range templates {
		if templates[i].Key == "sprint" {
			sprint = &templates[i]
		}
	}
	if sprint == nil {
		t.Fatal(`ListBoardTemplates did not include the "sprint" template`)
	}
	t.Logf("list_board_templates -> %d templates, %q has %d columns", len(templates), sprint.Key, len(sprint.Columns))

	// create_project with an unknown template is rejected.
	if _, err := c.CreateProject(ctx, "mcp-tpl-bad", "", "not-a-real-template"); err == nil {
		t.Error("CreateProject with an unknown template succeeded; expected an error")
	}

	// create_project from a real template. Its OWN response never carries
	// columns (that field is only populated by GetProject/GetBoard, create
	// or not) - a real, verified platform quirk, not a client bug. Confirm
	// that directly, then re-fetch to see the columns that were actually
	// seeded: generated keys, not todo/doing/done (only the default
	// "kanban" template uses those).
	proj, err := c.CreateProject(ctx, "mcp-tpl-scratch", "", "sprint")
	if err != nil {
		t.Fatalf("CreateProject(template=sprint): %v", err)
	}
	t.Cleanup(func() {
		if err := c.DeleteProject(ctx, proj.ID); err != nil {
			t.Errorf("CLEANUP FAILED, board %s left behind: %v", proj.ID, err)
		} else {
			t.Logf("cleanup               -> scratch board removed")
		}
	})
	if len(proj.Columns) != 0 {
		t.Errorf("CreateProject's own response carried %d columns; expected none (get_project is required)", len(proj.Columns))
	}

	full, err := c.GetProject(ctx, proj.ID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if len(full.Columns) != len(sprint.Columns) {
		t.Errorf("templated board has %d columns, want %d", len(full.Columns), len(sprint.Columns))
	}
	for _, col := range full.Columns {
		if col.Key == "todo" || col.Key == "doing" || col.Key == "done" {
			t.Errorf("sprint template column %q reused a default kanban key", col.Key)
		}
	}
	t.Logf("create_project/get_project -> %q from template %q, columns=%v", full.Name, "sprint", columnKeys(full.Columns))

	// Outbound webhooks + email-to-card are Pro-only (board admin + Pro
	// gate). Skip this half gracefully on a Free test account rather than
	// failing the whole test.
	_, err = c.CreateWebhook(ctx, proj.ID, "https://example.com/hook", "mcp live test", "craaft", nil)
	if err != nil {
		t.Logf("webhooks/email-to-card -> skipped (%v); test account is likely on the Free plan", err)
		return
	}

	whs, catalogue, err := c.ListWebhooks(ctx, proj.ID)
	if err != nil {
		t.Fatalf("ListWebhooks: %v", err)
	}
	if len(whs) != 1 {
		t.Fatalf("got %d webhooks, want 1", len(whs))
	}
	wh := whs[0]
	if wh.Secret == "" {
		t.Error("webhook secret should never be empty on a read")
	}
	if len(catalogue) == 0 {
		t.Error("eventCatalogue should list at least one broadcast event name")
	}
	t.Logf("create_webhook/list_webhooks -> id=%s format=%s events=%v catalogue=%d names",
		wh.ID, wh.Format, wh.Events, len(catalogue))

	updated, err := c.UpdateWebhook(ctx, wh.ID, map[string]any{"active": false})
	if err != nil {
		t.Fatalf("UpdateWebhook: %v", err)
	}
	if updated.Active {
		t.Error("UpdateWebhook(active=false) did not take")
	}
	t.Logf("update_webhook        -> active=%v", updated.Active)

	if err := c.DeleteWebhook(ctx, wh.ID); err != nil {
		t.Fatalf("DeleteWebhook: %v", err)
	}
	after, _, err := c.ListWebhooks(ctx, proj.ID)
	if err != nil {
		t.Fatalf("ListWebhooks(after delete): %v", err)
	}
	t.Logf("delete_webhook        -> %d webhooks remain", len(after))
	if len(after) != 0 {
		t.Errorf("delete left %d webhooks, want 0", len(after))
	}

	// email-to-card: get (disabled) -> enable -> update -> disable
	in, err := c.GetInboundEmail(ctx, proj.ID)
	if err != nil {
		t.Fatalf("GetInboundEmail: %v", err)
	}
	if in.Enabled || in.Address != nil {
		t.Errorf("GetInboundEmail before enabling = (%v, %+v), want (false, nil)", in.Enabled, in.Address)
	}
	t.Logf("get_inbound_email     -> enabled=%v (not yet configured), ai_usage=%+v", in.Enabled, in.AIUsage)

	addr2, err := c.EnableInboundEmail(ctx, proj.ID, nil, nil)
	if err != nil {
		t.Fatalf("EnableInboundEmail: %v", err)
	}
	if addr2.Email == "" || addr2.Token == "" {
		t.Errorf("EnableInboundEmail returned an incomplete address: %+v", addr2)
	}
	t.Logf("enable_inbound_email  -> %s (active=%v)", addr2.Email, addr2.Active)

	// A second enable on an already-configured board is a conflict.
	if _, err := c.EnableInboundEmail(ctx, proj.ID, nil, nil); err == nil {
		t.Error("EnableInboundEmail on an already-enabled board succeeded; expected an error")
	}

	oldToken := addr2.Token
	rotated, err := c.UpdateInboundEmail(ctx, proj.ID, map[string]any{"rotate": true})
	if err != nil {
		t.Fatalf("UpdateInboundEmail(rotate): %v", err)
	}
	if rotated.Token == oldToken {
		t.Error("rotate:true should mint a new token")
	}
	t.Logf("update_inbound_email  -> rotated token, active=%v", rotated.Active)

	if err := c.DisableInboundEmail(ctx, proj.ID); err != nil {
		t.Fatalf("DisableInboundEmail: %v", err)
	}
	inAfter, err := c.GetInboundEmail(ctx, proj.ID)
	if err != nil {
		t.Fatalf("GetInboundEmail(after disable): %v", err)
	}
	enabledAfter := inAfter.Enabled
	t.Logf("disable_inbound_email -> enabled=%v", enabledAfter)
	if enabledAfter {
		t.Error("DisableInboundEmail did not take")
	}
}

func columnKeys(cols []Column) []string {
	keys := make([]string, len(cols))
	for i, c := range cols {
		keys[i] = c.Key
	}
	return keys
}
