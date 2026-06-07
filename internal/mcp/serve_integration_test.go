//go:build integration

package mcp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	weftmcp "github.com/jophira/weft/internal/mcp"
)

// TestServe_initializeAndListTools exercises the full MCP stdio transport:
// initialize handshake → notifications/initialized → tools/list.
// Tagged integration because it exercises the JSON-RPC protocol loop
// which requires goroutine coordination and a running Serve call.
func TestServe_initializeAndListTools(t *testing.T) {
	reg, pm, _ := setup(t)
	srv := weftmcp.NewServer(reg, pm, weftmcp.Config{
		Version:         "0.0.0-test",
		ActiveProfileFn: func() string { return "" },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// serverReader/serverWriter are the server's stdio ends.
	// clientWriter → serverReader (client sends, server reads)
	// serverWriter → clientReader (server sends, client reads)
	serverReader, clientWriter := io.Pipe()
	clientReader, serverWriter := io.Pipe()

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.Serve(ctx, serverReader, serverWriter)
	}()

	enc := json.NewEncoder(clientWriter)
	scanner := bufio.NewScanner(clientReader)

	send := func(v any) {
		t.Helper()
		if err := enc.Encode(v); err != nil {
			t.Errorf("send: %v", err)
		}
	}

	// readUntilID blocks until a JSON-RPC response with the given integer id arrives.
	readUntilID := func(id int) map[string]any {
		t.Helper()
		for scanner.Scan() {
			var msg map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
				continue
			}
			if v, ok := msg["id"]; ok {
				if int(v.(float64)) == id {
					return msg
				}
			}
		}
		t.Fatalf("never received response with id=%d", id)
		return nil
	}

	// 1. Send initialize.
	send(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"clientInfo":      map[string]any{"name": "test-client", "version": "0.1"},
			"capabilities":    map[string]any{},
		},
	})
	initResp := readUntilID(1)
	if _, ok := initResp["result"]; !ok {
		t.Fatalf("initialize response missing result: %v", initResp)
	}

	// 2. Send notifications/initialized (no id — notification, no response expected).
	send(map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
		"params":  map[string]any{},
	})

	// 3. Send tools/list.
	send(map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
		"params":  map[string]any{},
	})
	toolsResp := readUntilID(2)
	result, ok := toolsResp["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/list response missing result: %v", toolsResp)
	}
	tools, _ := result["tools"].([]any)
	if len(tools) == 0 {
		t.Errorf("tools/list returned no tools")
	}

	// Spot-check that well-known tools are registered.
	var names []string
	for _, tool := range tools {
		if m, ok := tool.(map[string]any); ok {
			if n, ok := m["name"].(string); ok {
				names = append(names, n)
			}
		}
	}
	for _, expected := range []string{"weft_profile_list", "weft_source_list", "weft_doctor"} {
		found := false
		for _, name := range names {
			if strings.EqualFold(name, expected) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("tool %q not in tools/list; registered: %v", expected, names)
		}
	}

	cancel()
	_ = clientWriter.Close()
	_ = serverWriter.Close()
}
