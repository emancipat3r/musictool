package mcp

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func testServer() *Server {
	return NewServer("musictool-test", "0.0.0", []Tool{
		{
			Name:        "echo",
			Description: "echoes back its message",
			InputSchema: obj(map[string]any{"msg": strProp("message")}, "msg"),
			Handler: func(_ context.Context, args json.RawMessage) (any, error) {
				var a struct {
					Msg string `json:"msg"`
				}
				_ = json.Unmarshal(args, &a)
				return map[string]any{"echo": a.Msg}, nil
			},
		},
	})
}

func post(t *testing.T, srv *Server, body string) map[string]any {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 && rr.Code != 202 {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Code == 202 {
		return nil // notification
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, rr.Body.String())
	}
	return out
}

func TestInitialize(t *testing.T) {
	srv := testServer()
	resp := post(t, srv, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	res, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %v", resp)
	}
	if res["protocolVersion"] != protocolVersion {
		t.Fatalf("protocolVersion = %v", res["protocolVersion"])
	}
}

func TestToolsList(t *testing.T) {
	srv := testServer()
	resp := post(t, srv, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	res := resp["result"].(map[string]any)
	tools := res["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(tools))
	}
	first := tools[0].(map[string]any)
	if first["name"] != "echo" {
		t.Fatalf("tool name = %v", first["name"])
	}
	if _, ok := first["inputSchema"]; !ok {
		t.Fatal("tool missing inputSchema")
	}
}

func TestToolsCall(t *testing.T) {
	srv := testServer()
	resp := post(t, srv, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"msg":"hi"}}}`)
	res := resp["result"].(map[string]any)
	if res["isError"] == true {
		t.Fatalf("unexpected error result: %v", res)
	}
	content := res["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, `"echo":"hi"`) {
		t.Fatalf("unexpected content: %s", text)
	}
}

func TestNotificationNoBody(t *testing.T) {
	srv := testServer()
	resp := post(t, srv, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if resp != nil {
		t.Fatalf("notification should get no body, got %v", resp)
	}
}

func TestUnknownMethod(t *testing.T) {
	srv := testServer()
	resp := post(t, srv, `{"jsonrpc":"2.0","id":9,"method":"does/not/exist"}`)
	if _, ok := resp["error"]; !ok {
		t.Fatalf("expected error for unknown method, got %v", resp)
	}
}
