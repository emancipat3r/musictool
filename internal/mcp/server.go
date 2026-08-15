// Package mcp is a hand-rolled Model Context Protocol server over Streamable
// HTTP (JSON-RPC 2.0). Living outside the Claude container over HTTP is exactly
// why the transport is HTTP and not stdio. The implementation is deliberately
// small and auditable: initialize, tools/list, tools/call, ping.
//
// Guardrail: tool results are compact JSON. Nothing here writes to stdout — all
// diagnostics go to stderr via logx — so `serve` emits zero non-protocol bytes
// on stdout.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/emancipat3r/musictool/internal/logx"
)

// protocolVersion is the MCP revision this server implements.
const protocolVersion = "2025-06-18"

// Tool is one registered MCP tool.
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
	Handler     func(ctx context.Context, args json.RawMessage) (any, error)
}

// Server holds the tool registry and serves the /mcp endpoint.
type Server struct {
	name    string
	version string
	tools   []Tool
	index   map[string]Tool
}

// NewServer builds an MCP server with the given tools.
func NewServer(name, version string, tools []Tool) *Server {
	idx := make(map[string]Tool, len(tools))
	for _, t := range tools {
		idx[t.Name] = t
	}
	return &Server{name: name, version: version, tools: tools, index: idx}
}

// --- JSON-RPC envelopes ---

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Handler returns the http.Handler for the MCP endpoint.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", s.handleMCP)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "ok")
	})
	return mux
}

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handlePost(w, r)
	case http.MethodGet:
		// The client may open a GET stream for server-initiated messages. We
		// have none, so signal that cleanly rather than hanging a connection.
		w.WriteHeader(http.StatusMethodNotAllowed)
	case http.MethodDelete:
		// Session teardown; nothing stateful to clean up.
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handlePost(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeError(w, nil, -32700, "parse error: "+err.Error())
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, nil, -32700, "parse error: "+err.Error())
		return
	}

	// Notifications (no id) get a 202 and no body.
	isNotification := len(req.ID) == 0
	resp := s.dispatch(r.Context(), req)
	if isNotification {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) dispatch(ctx context.Context, req rpcRequest) rpcResponse {
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": s.name, "version": s.version},
		}
	case "notifications/initialized", "initialized":
		// no-op notification
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = map[string]any{"tools": s.toolList()}
	case "tools/call":
		resp.Result, resp.Error = s.callTool(ctx, req.Params)
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
	return resp
}

func (s *Server) toolList() []map[string]any {
	out := make([]map[string]any, 0, len(s.tools))
	for _, t := range s.tools {
		schema := t.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": schema,
		})
	}
	return out
}

// callTool executes a tools/call. Tool-level failures are returned as a result
// with isError=true (MCP convention), not a JSON-RPC protocol error, so the
// model can read and react to them.
func (s *Server) callTool(ctx context.Context, params json.RawMessage) (any, *rpcError) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid params: " + err.Error()}
	}
	tool, ok := s.index[p.Name]
	if !ok {
		return nil, &rpcError{Code: -32602, Message: "unknown tool: " + p.Name}
	}
	logx.Debugf("tools/call %s", p.Name)
	result, err := tool.Handler(ctx, p.Arguments)
	if err != nil {
		return toolResult(fmt.Sprintf("error: %v", err), true), nil
	}
	payload, mErr := json.Marshal(result)
	if mErr != nil {
		return toolResult("error: could not encode result: "+mErr.Error(), true), nil
	}
	return toolResult(string(payload), false), nil
}

// toolResult wraps text in the MCP content envelope.
func toolResult(text string, isError bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		logx.Errorf("write response: %v", err)
	}
}

func writeError(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	writeJSON(w, http.StatusOK, rpcResponse{
		JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg},
	})
}

// ErrMissingArg is returned by handlers when a required argument is absent.
var ErrMissingArg = errors.New("missing required argument")
