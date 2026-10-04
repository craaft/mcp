package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The import endpoints take a multipart upload, not JSON: a "file" part plus
// optional form fields. Check what actually goes on the wire.
func TestPreviewImportSendsMultipart(t *testing.T) {
	var gotPath, gotFile, gotName, gotSource, gotMapping string
	var hasSource bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("not a multipart request: %v", err)
			return
		}
		f, hdr, err := r.FormFile("file")
		if err != nil {
			t.Errorf("no file part: %v", err)
			return
		}
		b, _ := io.ReadAll(f)
		gotFile, gotName = string(b), hdr.Filename
		_, hasSource = r.MultipartForm.Value["source"]
		gotSource = r.FormValue("source")
		gotMapping = r.FormValue("mapping")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"source":"csv","name":"issues","columns":[{"title":"To Do","isDone":false,"cards":2}],
			"totals":{"cards":2},"assignees":{"matched":[],"unmatched":["bev"]},"warnings":[],
			"csv":{"headers":["Summary","Status"],"mapping":{"title":"Summary"},"sample":[["a","To Do"]]}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "cra_testtoken")
	p, err := c.PreviewImport(context.Background(), ImportRequest{
		Filename: "issues.csv",
		Data:     []byte("Summary,Status\na,To Do\nb,To Do\n"),
		Mapping:  map[string]string{"title": "Summary", "column": "Status"},
	})
	if err != nil {
		t.Fatalf("PreviewImport: %v", err)
	}
	if gotPath != "/api/v1/imports/preview" {
		t.Errorf("path = %q", gotPath)
	}
	if gotName != "issues.csv" || !strings.HasPrefix(gotFile, "Summary,Status") {
		t.Errorf("file part = %q %q", gotName, gotFile)
	}
	if hasSource || gotSource != "" {
		t.Errorf("an empty source must be left out so the server detects it, got %q", gotSource)
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(gotMapping), &m); err != nil || m["column"] != "Status" {
		t.Errorf("mapping field = %q (%v)", gotMapping, err)
	}
	if p.Totals.Cards != 2 || len(p.Columns) != 1 || p.CSV == nil || p.CSV.Headers[0] != "Summary" ||
		len(p.Assignees.Unmatched) != 1 {
		t.Errorf("decoded preview = %+v", p)
	}
}

// A full workspace answers 402; the plan is the part a caller can act on.
func TestImportBoardBoardCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/imports" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":"Free plan allows 3 projects","limit":"projects","currentPlan":"free","max":3,"current":3}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "cra_testtoken")
	_, err := c.ImportBoard(context.Background(), ImportRequest{Filename: "b.json", Data: []byte("{}"), Source: "trello"})
	if err == nil || !strings.Contains(err.Error(), "workspace plan: free") {
		t.Fatalf("err = %v, want the 402 message with the plan", err)
	}
}
