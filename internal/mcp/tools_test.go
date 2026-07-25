package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// Live regression: Claude Code accepted initialize but rejected the whole
// tools/list ("tools fetch failed") because one tool marshaled
// "properties": null. Every registered tool must serialize a well-formed
// object schema.
func TestToolSchemasMarshalWellFormed(t *testing.T) {
	tools := Tools(nil) // handlers are not invoked; only schemas are inspected
	if len(tools) == 0 {
		t.Fatal("no tools registered")
	}
	for _, tool := range tools {
		b, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("%s: marshal schema: %v", tool.Name, err)
		}
		s := string(b)
		if strings.Contains(s, `"properties":null`) {
			t.Fatalf("%s: schema has properties:null (breaks strict MCP clients): %s", tool.Name, s)
		}
		var parsed struct {
			Type       string         `json:"type"`
			Properties map[string]any `json:"properties"`
		}
		if err := json.Unmarshal(b, &parsed); err != nil {
			t.Fatalf("%s: schema not an object: %v", tool.Name, err)
		}
		if parsed.Type != "object" || parsed.Properties == nil {
			t.Fatalf("%s: schema must be type=object with a properties map: %s", tool.Name, s)
		}
	}
}
