package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

var (
	conn   net.Conn
	connMu sync.Mutex
)

// send sends a request to the logos core and returns the response.
func send(req map[string]any) (map[string]any, error) {
	req["id"] = nextID()
	connMu.Lock()
	defer connMu.Unlock()
	if err := writeMsg(conn, req); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}
	resp, err := readMsg(conn)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	return resp, nil
}

// formatResult turns a logos response into an MCP tool result.
func formatResult(resp map[string]any) (*mcp.CallToolResult, error) {
	ok, _ := resp["ok"].(bool)
	if !ok {
		errMsg, _ := resp["error"].(string)
		if errMsg == "" {
			errMsg = "unknown error"
		}
		return mcp.NewToolResultError(errMsg), nil
	}
	out, err := json.MarshalIndent(resp["value"], "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal value: %w", err)
	}
	return mcp.NewToolResultText(string(out)), nil
}

func handleEval(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	expr, err := request.RequireString("expr")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp, err := send(map[string]any{"op": "eval", "expr": expr})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return formatResult(resp)
}

func handleDefine(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	expr, err := request.RequireString("expr")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp, err := send(map[string]any{"op": "define", "name": name, "expr": expr})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return formatResult(resp)
}

func handleRefreshAll(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	targets, err := request.RequireStringSlice("targets")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	targetsAny := make([]any, len(targets))
	for i, t := range targets {
		targetsAny[i] = t
	}
	req := map[string]any{"op": "refresh-all", "targets": targetsAny}
	dry := request.GetBool("dry", false)
	if dry {
		req["dry"] = true
	}
	resp, err := send(req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return formatResult(resp)
}

func handleDelete(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp, err := send(map[string]any{"op": "delete", "name": name})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return formatResult(resp)
}

func handlePreludeAdd(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp, err := send(map[string]any{"op": "prelude-add", "name": name})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return formatResult(resp)
}

func handlePreludeRemove(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp, err := send(map[string]any{"op": "prelude-remove", "name": name})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return formatResult(resp)
}

func handlePreludeList(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	resp, err := send(map[string]any{"op": "prelude-list"})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return formatResult(resp)
}

func handleClear(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	resp, err := send(map[string]any{"op": "clear"})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return formatResult(resp)
}

func main() {
	sockPath := os.Getenv("LOGOS_SOCK")
	if sockPath == "" {
		sockPath = "/tmp/logos.sock"
	}

	var err error
	conn, err = net.Dial("unix", sockPath)
	if err != nil {
		log.Fatalf("connect to %s: %v", sockPath, err)
	}
	defer conn.Close()
	log.Printf("connected to logos core: %s", sockPath)

	s := server.NewMCPServer(
		"logos",
		"1.0.0",
		server.WithToolCapabilities(false),
	)

	s.AddTool(
		mcp.NewTool("logos_eval",
			mcp.WithDescription("Evaluate a logos expression. Returns the result value."),
			mcp.WithString("expr",
				mcp.Required(),
				mcp.Description("S-expression to evaluate, e.g. (list 1 2 3)"),
			),
		),
		handleEval,
	)

	s.AddTool(
		mcp.NewTool("logos_define",
			mcp.WithDescription("Define a named symbol in logos. The expression is parsed and stored."),
			mcp.WithString("name",
				mcp.Required(),
				mcp.Description("Symbol name to define"),
			),
			mcp.WithString("expr",
				mcp.Required(),
				mcp.Description("S-expression for the symbol's value"),
			),
		),
		handleDefine,
	)

	s.AddTool(
		mcp.NewTool("logos_delete",
			mcp.WithDescription("Delete a named symbol from logos."),
			mcp.WithString("name",
				mcp.Required(),
				mcp.Description("Symbol name to delete"),
			),
		),
		handleDelete,
	)

	s.AddTool(
		mcp.NewTool("logos_refresh_all",
			mcp.WithDescription("Re-resolve all symbols that depend on the given targets. Creates new nodes, never mutates."),
			mcp.WithArray("targets",
				mcp.Required(),
				mcp.Description("List of symbol names or node IDs to refresh dependents of"),
			),
			mcp.WithBoolean("dry",
				mcp.Description("If true, return list of affected symbols without making changes"),
			),
		),
		handleRefreshAll,
	)

	s.AddTool(
		mcp.NewTool("logos_prelude_add",
			mcp.WithDescription("Promote a defined symbol to the prelude. The symbol and all its dependencies must already be builtins or in the prelude."),
			mcp.WithString("name",
				mcp.Required(),
				mcp.Description("Symbol name to add to the prelude"),
			),
		),
		handlePreludeAdd,
	)

	s.AddTool(
		mcp.NewTool("logos_prelude_remove",
			mcp.WithDescription("Remove a symbol from the prelude."),
			mcp.WithString("name",
				mcp.Required(),
				mcp.Description("Symbol name to remove from the prelude"),
			),
		),
		handlePreludeRemove,
	)

	s.AddTool(
		mcp.NewTool("logos_prelude_list",
			mcp.WithDescription("List all symbols in the prelude."),
		),
		handlePreludeList,
	)

	s.AddTool(
		mcp.NewTool("logos_clear",
			mcp.WithDescription("Clear the session: truncate log, reset graph to prelude-only state, clear traces."),
		),
		handleClear,
	)

	if err := server.ServeStdio(s); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
