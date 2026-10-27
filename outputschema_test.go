package main

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

// toolOutputSchemas lists every tool's declared output schema, keyed by
// tool name. Tools without structured output are left out.
func toolOutputSchemas(t *testing.T) map[string]*jsonschema.Schema {
	t.Helper()
	cs := toolSession(t, NewClient("http://localhost:0", "cra_testtoken"))
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	out := map[string]*jsonschema.Schema{}
	for _, tool := range res.Tools {
		if tool.OutputSchema == nil {
			continue
		}
		b, err := json.Marshal(tool.OutputSchema)
		if err != nil {
			t.Fatalf("%s: marshal output schema: %v", tool.Name, err)
		}
		var s jsonschema.Schema
		if err := json.Unmarshal(b, &s); err != nil {
			t.Fatalf("%s: decode output schema: %v", tool.Name, err)
		}
		out[tool.Name] = &s
	}
	return out
}

// A json.RawMessage (or any []byte) field makes the SDK declare
// {"type": ["null", "array"], "items": {"type": "integer"}}, so whatever
// object or string the API sent fails the client's validation. That's how
// get_me broke. No tool should declare that shape anywhere in its output.
func TestNoOutputSchemaDeclaresBytes(t *testing.T) {
	for name, s := range toolOutputSchemas(t) {
		for path, sub := range walkSchema("", s) {
			if isByteArray(sub) {
				t.Errorf("%s: %s is declared as a byte array; use a real type instead of json.RawMessage or []byte", name, path)
			}
		}
	}
}

// get_me's real output, built from the platform's /me response, has to pass
// the output schema the tool declares.
func TestGetMeOutputMatchesSchema(t *testing.T) {
	c, _ := stubAPI(t, map[string]any{
		"GET /api/v1/me": map[string]any{
			"id": "9a7c63cc-c366-433a-b862-c310a6f841c9", "email": "a@example.com",
			"name": "A", "username": "a", "avatarUrl": "", "hasPassword": true,
			"emailVerified": true, "twoFactorEnabled": false,
			"newsletterSubscribed": false, "newsletterAvailable": true,
			"emailNotifications": true, "isSuperAdmin": false, "csrfToken": "tok",
		},
	})
	cs := toolSession(t, c)
	res := callTool(t, cs, "get_me", map[string]any{})

	resolved, err := toolOutputSchemas(t)["get_me"].Resolve(nil)
	if err != nil {
		t.Fatalf("resolve schema: %v", err)
	}
	b, _ := json.Marshal(res.StructuredContent)
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("decode structured content: %v", err)
	}
	if err := resolved.Validate(got); err != nil {
		t.Fatalf("get_me output fails its own schema: %v\n%s", err, b)
	}
	user, _ := got["user"].(map[string]any)
	if user["username"] != "a" {
		t.Errorf("user.username = %v, want a", user["username"])
	}
	if _, ok := user["csrfToken"]; ok {
		t.Error("get_me should not pass csrfToken on")
	}
}

func isByteArray(s *jsonschema.Schema) bool {
	types := s.Types
	if s.Type != "" {
		types = append(types, s.Type)
	}
	return slices.Contains(types, "array") && s.Items != nil && s.Items.Type == "integer"
}

// walkSchema returns s and every schema nested in it, keyed by a readable path.
func walkSchema(path string, s *jsonschema.Schema) map[string]*jsonschema.Schema {
	out := map[string]*jsonschema.Schema{}
	if s == nil {
		return out
	}
	if path == "" {
		path = "(root)"
	}
	out[path] = s
	add := func(p string, sub *jsonschema.Schema) {
		for k, v := range walkSchema(p, sub) {
			out[k] = v
		}
	}
	for k, sub := range s.Properties {
		add(path+"."+k, sub)
	}
	add(path+"[]", s.Items)
	add(path+"{}", s.AdditionalProperties)
	for k, sub := range s.Defs {
		add("$defs."+k, sub)
	}
	for i, sub := range s.AnyOf {
		add(path+"|anyOf"+string(rune('0'+i)), sub)
	}
	for i, sub := range s.OneOf {
		add(path+"|oneOf"+string(rune('0'+i)), sub)
	}
	return out
}
