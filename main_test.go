package main

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerTools builds a JSON schema for every tool's argument struct via
// reflection (github.com/google/jsonschema-go, through the mcp SDK). A type
// that reflection can't handle - notably the *[]string used by
// update_webhook's Events field, an unusual shape versus the rest of this
// file's plain slices and pointer-to-scalar fields - would panic here rather
// than fail a normal compile. This calls registerTools against a client
// pointed at nothing (no network call is made; that would require an actual
// tool invocation) purely to exercise schema generation for every tool this
// file registers.
func TestRegisterToolsDoesNotPanic(t *testing.T) {
	c := NewClient("http://localhost:0", "cra_testtoken")
	s := mcp.NewServer(&mcp.Implementation{Name: "craaft-mcp-test", Version: "test"}, nil)
	registerTools(s, c)
}
