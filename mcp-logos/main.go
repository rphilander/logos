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
// Includes active_library from core response in the output.
func formatResult(resp map[string]any) (*mcp.CallToolResult, error) {
	ok, _ := resp["ok"].(bool)
	if !ok {
		errMsg, _ := resp["error"].(string)
		if errMsg == "" {
			errMsg = "unknown error"
		}
		return mcp.NewToolResultError(errMsg), nil
	}
	// Build output map with value and active_library
	output := map[string]any{
		"value": resp["value"],
	}
	if al, exists := resp["active_library"]; exists {
		output["active_library"] = al
	}
	out, err := json.MarshalIndent(output, "", "  ")
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
	req := map[string]any{"op": "eval", "expr": expr}
	if fuel := request.GetInt("fuel", 0); fuel > 0 {
		req["fuel"] = fuel
	}
	resp, err := send(req)
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

func handleLibraryCreate(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp, err := send(map[string]any{"op": "library-create", "name": name})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return formatResult(resp)
}

func handleLibraryDelete(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp, err := send(map[string]any{"op": "library-delete", "name": name})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return formatResult(resp)
}

func handleLibraryOpen(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp, err := send(map[string]any{"op": "library-open", "name": name})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return formatResult(resp)
}

func handleLibraryClose(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	resp, err := send(map[string]any{"op": "library-close"})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return formatResult(resp)
}

func handleLibraryCompact(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp, err := send(map[string]any{"op": "library-compact", "name": name})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return formatResult(resp)
}

func handleLibraryOrder(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	resp, err := send(map[string]any{"op": "library-order"})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return formatResult(resp)
}

func handleLibraryOrderSet(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	names, err := request.RequireStringSlice("names")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	namesAny := make([]any, len(names))
	for i, n := range names {
		namesAny[i] = n
	}
	resp, err := send(map[string]any{"op": "library-order-set", "names": namesAny})
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

func handleSetFuel(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	fuel, err := request.RequireInt("fuel")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp, err := send(map[string]any{"op": "set-fuel", "fuel": fuel})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return formatResult(resp)
}

func handleGetFuel(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	resp, err := send(map[string]any{"op": "get-fuel"})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return formatResult(resp)
}

func handleManual(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	resp, err := send(map[string]any{})
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
			mcp.WithNumber("fuel",
				mcp.Description("Optional eval step limit (overrides global default)"),
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
		mcp.NewTool("logos_library_create",
			mcp.WithDescription("Create a new library. Creates an empty library file and adds it to the manifest."),
			mcp.WithString("name",
				mcp.Required(),
				mcp.Description("Library name to create"),
			),
		),
		handleLibraryCreate,
	)

	s.AddTool(
		mcp.NewTool("logos_library_delete",
			mcp.WithDescription("Delete a library. The library must be empty (no symbols) and not currently open."),
			mcp.WithString("name",
				mcp.Required(),
				mcp.Description("Library name to delete"),
			),
		),
		handleLibraryDelete,
	)

	s.AddTool(
		mcp.NewTool("logos_library_open",
			mcp.WithDescription("Open a library for writing. Defines and deletes will be written to this library until it is closed."),
			mcp.WithString("name",
				mcp.Required(),
				mcp.Description("Library name to open"),
			),
		),
		handleLibraryOpen,
	)

	s.AddTool(
		mcp.NewTool("logos_library_close",
			mcp.WithDescription("Close the currently open library and return to the session log."),
		),
		handleLibraryClose,
	)

	s.AddTool(
		mcp.NewTool("logos_library_compact",
			mcp.WithDescription("Rewrite a library file to contain only current live definitions. The library must not be currently open."),
			mcp.WithString("name",
				mcp.Required(),
				mcp.Description("Library name to compact"),
			),
		),
		handleLibraryCompact,
	)

	s.AddTool(
		mcp.NewTool("logos_library_order",
			mcp.WithDescription("Return the ordered list of libraries from the manifest."),
		),
		handleLibraryOrder,
	)

	s.AddTool(
		mcp.NewTool("logos_library_order_set",
			mcp.WithDescription("Set the library load order. All named libraries must have corresponding files."),
			mcp.WithArray("names",
				mcp.Required(),
				mcp.Description("Ordered list of library names"),
			),
		),
		handleLibraryOrderSet,
	)

	s.AddTool(
		mcp.NewTool("logos_clear",
			mcp.WithDescription("Clear the session: truncate log, reset graph, reload libraries, clear traces."),
		),
		handleClear,
	)

	s.AddTool(
		mcp.NewTool("logos_set_fuel",
			mcp.WithDescription("Set the global eval fuel limit (0 = unlimited)."),
			mcp.WithNumber("fuel",
				mcp.Required(),
				mcp.Description("Number of eval steps (0 = unlimited)"),
			),
		),
		handleSetFuel,
	)

	s.AddTool(
		mcp.NewTool("logos_get_fuel",
			mcp.WithDescription("Get the current global fuel limit."),
		),
		handleGetFuel,
	)

	s.AddTool(
		mcp.NewTool("logos_manual",
			mcp.WithDescription("Return the logos core manual: available ops, builtins, and forms."),
		),
		handleManual,
	)

	if err := server.ServeStdio(s); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
