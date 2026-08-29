package main

// Round-trips the attachment tools against a real server on a scratch board.
// Skipped unless CRAAFT_E2E is set.
import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestLiveAttachments(t *testing.T) {
	base, tok := os.Getenv("CRAAFT_BASE_URL"), os.Getenv("CRAAFT_API_TOKEN")
	if os.Getenv("CRAAFT_E2E") == "" || base == "" || tok == "" {
		t.Skip("set CRAAFT_E2E + CRAAFT_BASE_URL + CRAAFT_API_TOKEN")
	}
	c, ctx := NewClient(base, tok), context.Background()

	var proj struct {
		ID                   string `json:"id"`
		CanUploadAttachments bool   `json:"canUploadAttachments"`
	}
	data, _, err := c.do(ctx, "POST", "/api/v1/projects", map[string]any{"name": "mcp-attach-scratch"})
	if err != nil {
		t.Fatalf("create scratch project: %v", err)
	}
	if err := json.Unmarshal(data, &proj); err != nil {
		t.Fatalf("decode project: %v", err)
	}
	t.Cleanup(func() {
		if _, _, err := c.do(ctx, "DELETE", "/api/v1/projects/"+proj.ID, nil); err != nil {
			t.Errorf("CLEANUP FAILED, board %s left behind: %v", proj.ID, err)
		} else {
			t.Logf("scratch board %s deleted", proj.ID)
		}
	})
	t.Logf("canUploadAttachments = %v", proj.CanUploadAttachments)

	made, err := c.BulkCreateCards(ctx, proj.ID, []map[string]any{{"title": "attach target", "column": "todo"}})
	if err != nil {
		t.Fatalf("BulkCreateCards: %v", err)
	}
	card := made[0]

	// Text upload.
	const body = "# Notes\n\nline two with a £ sign and an emoji 🎉\n"
	txt, err := c.UploadAttachment(ctx, card.ID, "notes.md", "text/markdown", []byte(body))
	if err != nil {
		t.Fatalf("UploadAttachment(text): %v", err)
	}
	t.Logf("upload text   -> %q %d bytes, %s", txt.Name, txt.Size, txt.ContentType)

	// Binary upload: a 1x1 PNG.
	png := []byte{
		0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
		'I', 'H', 'D', 'R', 0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0,
		0x1f, 0x15, 0xc4, 0x89, 0x00, 0x00, 0x00, 0x0a, 'I', 'D', 'A', 'T',
		0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05, 0x00, 0x01,
		0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 'I', 'E', 'N', 'D',
		0xae, 0x42, 0x60, 0x82,
	}
	bin, err := c.UploadAttachment(ctx, card.ID, "pixel.png", "image/png", png)
	if err != nil {
		t.Fatalf("UploadAttachment(binary): %v", err)
	}
	t.Logf("upload binary -> %q %d bytes, %s", bin.Name, bin.Size, bin.ContentType)

	list, err := c.ListAttachments(ctx, card.ID)
	if err != nil {
		t.Fatalf("ListAttachments: %v", err)
	}
	t.Logf("list          -> %d attachments", len(list))
	if len(list) != 2 {
		t.Errorf("got %d attachments, want 2", len(list))
	}

	// Text round-trips byte-for-byte, including multibyte characters.
	got, ctype, err := c.DownloadAttachment(ctx, txt.ID)
	if err != nil {
		t.Fatalf("DownloadAttachment(text): %v", err)
	}
	if string(got) != body {
		t.Errorf("text round-trip mismatch:\n got %q\nwant %q", string(got), body)
	}
	t.Logf("download text -> %d bytes, %s, byte-identical=%v", len(got), ctype, string(got) == body)
	if !isTextual(ctype) {
		t.Errorf("isTextual(%q) = false, want true", ctype)
	}

	gotBin, binType, err := c.DownloadAttachment(ctx, bin.ID)
	if err != nil {
		t.Fatalf("DownloadAttachment(binary): %v", err)
	}
	if !bytes.Equal(gotBin, png) {
		t.Errorf("binary round-trip mismatch: got %d bytes, want %d", len(gotBin), len(png))
	}
	t.Logf("download bin  -> %d bytes, %s, byte-identical=%v", len(gotBin), binType, bytes.Equal(gotBin, png))
	if isTextual(binType) {
		t.Errorf("isTextual(%q) = true, want false", binType)
	}
}

func TestIsTextual(t *testing.T) {
	for _, tc := range []struct {
		ct   string
		want bool
	}{
		{"text/plain", true},
		{"text/markdown; charset=utf-8", true},
		{"application/json", true},
		{"application/vnd.api+json", true},
		{"image/svg+xml", true},
		{"image/png", false},
		{"application/pdf", false},
		{"application/octet-stream", false},
		{"audio/webm", false},
		{"", false},
	} {
		if got := isTextual(tc.ct); got != tc.want {
			t.Errorf("isTextual(%q) = %v, want %v", tc.ct, got, tc.want)
		}
	}
}
