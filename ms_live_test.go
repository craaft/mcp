package main

// Round-trips milestones, public sharing and avatar against a real server.
// Skipped unless CRAAFT_E2E is set. Uses a scratch board it deletes.
import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestLiveMilestonesSharingAvatar(t *testing.T) {
	base, tok := os.Getenv("CRAAFT_BASE_URL"), os.Getenv("CRAAFT_API_TOKEN")
	if os.Getenv("CRAAFT_E2E") == "" || base == "" || tok == "" {
		t.Skip("set CRAAFT_E2E + CRAAFT_BASE_URL + CRAAFT_API_TOKEN")
	}
	c, ctx := NewClient(base, tok), context.Background()

	proj, err := c.CreateProject(ctx, "mcp-ms-scratch", "", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	t.Cleanup(func() {
		if err := c.DeleteProject(ctx, proj.ID); err != nil {
			t.Errorf("CLEANUP FAILED, board %s left behind: %v", proj.ID, err)
		} else {
			t.Logf("cleanup             -> scratch board removed")
		}
	})

	// milestones: add, list, achieve, delete
	m, err := c.AddMilestone(ctx, proj.ID, "Beta cut", "2026-09-30")
	if err != nil {
		t.Fatalf("AddMilestone: %v", err)
	}
	t.Logf("add_milestone       -> %q due %s achievedAt=%v", m.Name, m.DueOn, m.AchievedAt)
	if m.AchievedAt != nil {
		t.Errorf("a new milestone should not be achieved")
	}

	list, err := c.ListMilestones(ctx, proj.ID)
	if err != nil {
		t.Fatalf("ListMilestones: %v", err)
	}
	t.Logf("list_milestones     -> %d", len(list))
	if len(list) != 1 {
		t.Errorf("got %d milestones, want 1", len(list))
	}

	done, err := c.UpdateMilestone(ctx, m.ID, map[string]any{"achieved": true})
	if err != nil {
		t.Fatalf("UpdateMilestone: %v", err)
	}
	if done.AchievedAt == nil {
		t.Error("achieved:true should stamp achievedAt")
	}
	stamp := *done.AchievedAt
	t.Logf("update_milestone    -> achievedAt=%s", stamp)

	// Re-sending true must keep the ORIGINAL stamp, per the spec.
	again, err := c.UpdateMilestone(ctx, m.ID, map[string]any{"achieved": true})
	if err != nil {
		t.Fatalf("UpdateMilestone(again): %v", err)
	}
	if again.AchievedAt == nil || *again.AchievedAt != stamp {
		t.Errorf("re-achieving changed the stamp: %v -> %v", stamp, again.AchievedAt)
	}
	t.Logf("update_milestone    -> re-achieving kept the original stamp")

	if err := c.DeleteMilestone(ctx, m.ID); err != nil {
		t.Fatalf("DeleteMilestone: %v", err)
	}
	left, _ := c.ListMilestones(ctx, proj.ID)
	t.Logf("delete_milestone    -> %d remain", len(left))

	// sharing: enable is idempotent, disable then re-enable rotates
	tok1, err := c.EnableShare(ctx, proj.ID)
	if err != nil {
		t.Fatalf("EnableShare: %v", err)
	}
	tok2, err := c.EnableShare(ctx, proj.ID)
	if err != nil {
		t.Fatalf("EnableShare(again): %v", err)
	}
	if tok1 != tok2 {
		t.Errorf("enable is meant to be idempotent: %q then %q", tok1, tok2)
	}
	t.Logf("enable_sharing      -> token stable across two calls")

	if err := c.DisableShare(ctx, proj.ID); err != nil {
		t.Fatalf("DisableShare: %v", err)
	}
	tok3, err := c.EnableShare(ctx, proj.ID)
	if err != nil {
		t.Fatalf("EnableShare(after revoke): %v", err)
	}
	if tok3 == tok1 {
		t.Error("re-enabling after a revoke must mint a NEW token, not restore the old one")
	}
	t.Logf("disable_sharing     -> re-enable minted a different token (old links stay dead)")
	_ = c.DisableShare(ctx, proj.ID)

	// avatar: the seeded owner has no upload, so this is the not-an-error path
	uid := os.Getenv("CRAAFT_TEST_USER_ID")
	if uid == "" {
		t.Log("get_avatar          -> skipped (set CRAAFT_TEST_USER_ID)")
		return
	}
	data, ctype, err := c.GetAvatar(ctx, uid)
	switch {
	case errors.Is(err, ErrNoAvatar):
		t.Logf("get_avatar          -> no upload (ErrNoAvatar), the placeholder cue")
	case err != nil:
		t.Fatalf("GetAvatar: %v", err)
	default:
		t.Logf("get_avatar          -> %d bytes, %s", len(data), ctype)
	}
}
