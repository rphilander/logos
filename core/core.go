package logos

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"
)

// ModuleInfo tracks a connected module.
type ModuleInfo struct {
	ID     string
	Manual Value
	conn   net.Conn
	mu     sync.Mutex // serializes reads/writes to this module's connection
}

// Core is the central actor that owns the graph and handles requests.
type Core struct {
	graph       *Graph
	modules     []*ModuleInfo
	modMu       sync.Mutex // protects modules slice
	requests    chan coreRequest
	listener    net.Listener // CLI socket
	modListener net.Listener // module socket
	cbListener  net.Listener // callback socket
	traces      []Trace
	maxTraces   int
	defaultFuel int // 0 = unlimited
}

type coreRequest struct {
	msg      map[string]any
	response chan map[string]any
	callback bool
}

// NewCore creates a new core with the given data directory and socket paths.
func NewCore(dir, sockPath, modSockPath, cbSockPath string) (*Core, error) {
	// Clean up stale sockets
	os.Remove(sockPath)
	os.Remove(modSockPath)
	os.Remove(cbSockPath)

	c := &Core{
		requests:  make(chan coreRequest, 64),
		maxTraces: 1000,
	}

	// Build builtins: data primitives + module interaction
	builtins := DataBuiltins()
	builtins["modules"] = c.builtinModules
	builtins["send"] = c.builtinSend
	builtins["traces"] = c.builtinTraces

	graph, err := NewGraph(dir, builtins)
	if err != nil {
		return nil, fmt.Errorf("init graph: %w", err)
	}
	c.graph = graph

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		graph.Close()
		return nil, fmt.Errorf("listen cli: %w", err)
	}
	c.listener = listener

	modListener, err := net.Listen("unix", modSockPath)
	if err != nil {
		listener.Close()
		graph.Close()
		return nil, fmt.Errorf("listen module: %w", err)
	}
	c.modListener = modListener

	cbListener, err := net.Listen("unix", cbSockPath)
	if err != nil {
		modListener.Close()
		listener.Close()
		graph.Close()
		return nil, fmt.Errorf("listen callback: %w", err)
	}
	c.cbListener = cbListener

	return c, nil
}

// Run starts the core actor goroutine and accepts connections. Blocks until shutdown.
func (c *Core) Run() {
	go c.actorLoop()
	go c.acceptClients()
	go c.acceptModules()
	c.acceptCallbacks()
}

func (c *Core) acceptClients() {
	for {
		conn, err := c.listener.Accept()
		if err != nil {
			return
		}
		go c.handleClientConnection(conn)
	}
}

func (c *Core) acceptModules() {
	for {
		conn, err := c.modListener.Accept()
		if err != nil {
			return
		}
		go c.handleModuleConnection(conn)
	}
}

func (c *Core) acceptCallbacks() {
	for {
		conn, err := c.cbListener.Accept()
		if err != nil {
			return
		}
		go c.handleCallbackConnection(conn)
	}
}

// Shutdown cleanly stops the core.
func (c *Core) Shutdown() {
	c.listener.Close()
	c.modListener.Close()
	c.cbListener.Close()
	close(c.requests)
	c.graph.Close()

	c.modMu.Lock()
	for _, m := range c.modules {
		m.conn.Close()
	}
	c.modMu.Unlock()
}

// actorLoop is the single goroutine that owns graph state.
func (c *Core) actorLoop() {
	for req := range c.requests {
		var resp map[string]any
		if req.callback {
			resp = c.handleCallbackRequest(req.msg)
		} else {
			resp = c.handleRequest(req.msg)
		}
		req.response <- resp
	}
}

// sendToActor sends a request to the core actor and waits for the response.
func (c *Core) sendToActor(msg map[string]any) map[string]any {
	resp := make(chan map[string]any, 1)
	c.requests <- coreRequest{msg: msg, response: resp}
	return <-resp
}

func (c *Core) sendCallbackToActor(msg map[string]any) map[string]any {
	resp := make(chan map[string]any, 1)
	c.requests <- coreRequest{msg: msg, response: resp, callback: true}
	return <-resp
}

func (c *Core) handleRequest(msg map[string]any) map[string]any {
	id, _ := msg["id"].(string)

	op, _ := msg["op"].(string)
	if op == "" {
		// Empty request or no op — return manual
		return c.coreManual(id)
	}

	var resp map[string]any
	switch op {
	case "eval":
		resp = c.handleEval(id, msg)
	case "define":
		resp = c.handleDefine(id, msg)
	case "delete":
		resp = c.handleDelete(id, msg)
	case "refresh-all":
		resp = c.handleRefreshAll(id, msg)
	case "library-create":
		resp = c.handleLibraryCreate(id, msg)
	case "library-delete":
		resp = c.handleLibraryDelete(id, msg)
	case "library-open":
		resp = c.handleLibraryOpen(id, msg)
	case "library-close":
		resp = c.handleLibraryClose(id, msg)
	case "library-compact":
		resp = c.handleLibraryCompact(id, msg)
	case "library-order":
		resp = c.handleLibraryOrder(id, msg)
	case "library-order-set":
		resp = c.handleLibraryOrderSet(id, msg)
	case "clear":
		resp = c.handleClear(id, msg)
	case "set-fuel":
		resp = c.handleSetFuel(id, msg)
	case "get-fuel":
		resp = c.handleGetFuel(id, msg)
	default:
		resp = errorResponse(id, fmt.Sprintf("unknown op: %s", op))
	}
	// Include active_library in every response
	activeLib := c.graph.ActiveLibrary()
	if activeLib == "" {
		resp["active_library"] = "session"
	} else {
		resp["active_library"] = activeLib
	}
	return resp
}

func (c *Core) handleCallbackRequest(msg map[string]any) map[string]any {
	id, _ := msg["id"].(string)

	handler, ok := c.graph.ResolveSymbol("on-request")
	if !ok {
		return errorResponse(id, "on-request is not defined")
	}
	if handler.Kind != ValFn {
		return errorResponse(id, "on-request is not a function")
	}

	reqVal := GoToValue(msg)

	trace := &Trace{
		Entry:     handler.Fn.NodeID,
		Args:      []Value{reqVal},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	c.graph.eval.activeTrace = trace
	c.setEvalFuel(nil)
	result, err := c.graph.eval.CallFnWithValues(handler.Fn, []Value{reqVal})
	c.graph.eval.activeTrace = nil
	c.graph.eval.FuelSet = false

	if err != nil {
		trace.Error = err.Error()
		c.appendTrace(trace)
		return errorResponse(id, err.Error())
	}

	trace.Result = result
	c.appendTrace(trace)

	goVal, err := ValueToGo(result)
	if err != nil {
		return errorResponse(id, fmt.Sprintf("serialize result: %s", err))
	}
	return map[string]any{"id": id, "ok": true, "value": goVal}
}

func (c *Core) coreManual(id string) map[string]any {
	return map[string]any{
		"id": id,
		"ok": true,
		"value": map[string]any{
			"name":    "logos-core",
			"version": "3.0.0",
			"ops": map[string]any{
				"eval":              "Evaluate a logos expression. Params: expr (string)",
				"define":            "Define a named symbol. Params: name (string), expr (string)",
				"delete":            "Delete a named symbol. Params: name (string)",
				"refresh-all":       "Re-resolve dependents of target symbols. Params: targets ([]string), dry (bool, optional)",
				"library-create":    "Create a new library. Params: name (string)",
				"library-delete":    "Delete an empty library. Params: name (string)",
				"library-open":      "Open a library for writing. Params: name (string)",
				"library-close":     "Close the open library, return to session.",
				"library-compact":   "Rewrite a library to only live definitions. Params: name (string)",
				"library-order":     "Return the ordered list of libraries.",
				"library-order-set": "Set the library load order. Params: names ([]string)",
				"clear":             "Clear session: truncate log, reset graph, reload libraries.",
				"set-fuel":          "Set global eval fuel limit. Params: fuel (int, 0=unlimited)",
				"get-fuel":          "Get current fuel limit.",
			},
			"builtins": []any{
				"list", "dict", "get", "head", "rest", "len", "keys",
				"eq", "to-string", "concat", "modules", "send",
				"add", "sub", "mul", "div", "mod",
				"lt", "gt",
				"cons", "nth", "append",
				"put", "has?",
				"split-once",
				"type",
				"symbols", "node-expr", "ref-by", "follow",
				"assert", "traces",
				"link", "link-target",
			},
			"forms": []any{
				"if", "let", "do", "fn", "form", "quote",
				"apply", "sort-by", "loop", "recur",
			},
		},
	}
}

func (c *Core) handleEval(id string, msg map[string]any) map[string]any {
	expr, ok := msg["expr"].(string)
	if !ok {
		return errorResponse(id, "eval: missing 'expr' string")
	}

	trace := &Trace{
		Entry:     expr,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	c.graph.eval.activeTrace = trace
	c.setEvalFuel(msg)
	val, err := c.graph.Eval(expr)
	c.graph.eval.activeTrace = nil
	c.graph.eval.FuelSet = false

	if err != nil {
		trace.Error = err.Error()
		var ae *AssertError
		if errors.As(err, &ae) {
			trace.Error = ae.Error()
		}
		c.appendTrace(trace)
		return errorResponse(id, err.Error())
	}

	trace.Result = val
	c.appendTrace(trace)

	goVal, err := ValueToGo(val)
	if err != nil {
		return errorResponse(id, fmt.Sprintf("serialize result: %s", err))
	}
	return map[string]any{"id": id, "ok": true, "value": goVal}
}

func (c *Core) handleDefine(id string, msg map[string]any) map[string]any {
	name, ok := msg["name"].(string)
	if !ok {
		return errorResponse(id, "define: missing 'name' string")
	}
	expr, ok := msg["expr"].(string)
	if !ok {
		return errorResponse(id, "define: missing 'expr' string")
	}
	node, err := c.graph.Define(name, expr)
	if err != nil {
		return errorResponse(id, err.Error())
	}
	return map[string]any{
		"id":    id,
		"ok":    true,
		"value": map[string]any{"node_id": node.ID, "name": name},
	}
}

func (c *Core) handleDelete(id string, msg map[string]any) map[string]any {
	name, ok := msg["name"].(string)
	if !ok {
		return errorResponse(id, "delete: missing 'name' string")
	}
	if err := c.graph.Delete(name); err != nil {
		return errorResponse(id, err.Error())
	}
	return map[string]any{"id": id, "ok": true, "value": name}
}

func (c *Core) handleRefreshAll(id string, msg map[string]any) map[string]any {
	targetsRaw, ok := msg["targets"].([]any)
	if !ok {
		return errorResponse(id, "refresh-all: missing 'targets' array")
	}
	targets := make([]string, len(targetsRaw))
	for i, t := range targetsRaw {
		s, ok := t.(string)
		if !ok {
			return errorResponse(id, fmt.Sprintf("refresh-all: target %d must be string", i))
		}
		targets[i] = s
	}
	dry, _ := msg["dry"].(bool)

	result, err := c.graph.RefreshAll(targets, dry)
	if err != nil {
		return errorResponse(id, err.Error())
	}
	refreshed := make([]any, len(result.Refreshed))
	for i, name := range result.Refreshed {
		refreshed[i] = name
	}
	return map[string]any{
		"id":    id,
		"ok":    true,
		"value": map[string]any{"refreshed": refreshed},
	}
}

func (c *Core) handleLibraryCreate(id string, msg map[string]any) map[string]any {
	name, ok := msg["name"].(string)
	if !ok {
		return errorResponse(id, "library-create: missing 'name' string")
	}
	if err := c.graph.LibraryCreate(name); err != nil {
		return errorResponse(id, err.Error())
	}
	return map[string]any{"id": id, "ok": true, "value": name}
}

func (c *Core) handleLibraryDelete(id string, msg map[string]any) map[string]any {
	name, ok := msg["name"].(string)
	if !ok {
		return errorResponse(id, "library-delete: missing 'name' string")
	}
	if err := c.graph.LibraryDelete(name); err != nil {
		return errorResponse(id, err.Error())
	}
	return map[string]any{"id": id, "ok": true, "value": name}
}

func (c *Core) handleLibraryOpen(id string, msg map[string]any) map[string]any {
	name, ok := msg["name"].(string)
	if !ok {
		return errorResponse(id, "library-open: missing 'name' string")
	}
	if err := c.graph.LibraryOpen(name); err != nil {
		return errorResponse(id, err.Error())
	}
	return map[string]any{"id": id, "ok": true, "value": name}
}

func (c *Core) handleLibraryClose(id string, msg map[string]any) map[string]any {
	if err := c.graph.LibraryClose(); err != nil {
		return errorResponse(id, err.Error())
	}
	return map[string]any{"id": id, "ok": true, "value": "session"}
}

func (c *Core) handleLibraryCompact(id string, msg map[string]any) map[string]any {
	name, ok := msg["name"].(string)
	if !ok {
		return errorResponse(id, "library-compact: missing 'name' string")
	}
	if err := c.graph.LibraryCompact(name); err != nil {
		return errorResponse(id, err.Error())
	}
	return map[string]any{"id": id, "ok": true, "value": name}
}

func (c *Core) handleLibraryOrder(id string, msg map[string]any) map[string]any {
	order := c.graph.LibraryOrder()
	result := make([]any, len(order))
	for i, n := range order {
		result[i] = n
	}
	return map[string]any{"id": id, "ok": true, "value": result}
}

func (c *Core) handleLibraryOrderSet(id string, msg map[string]any) map[string]any {
	namesRaw, ok := msg["names"].([]any)
	if !ok {
		return errorResponse(id, "library-order-set: missing 'names' array")
	}
	names := make([]string, len(namesRaw))
	for i, n := range namesRaw {
		s, ok := n.(string)
		if !ok {
			return errorResponse(id, fmt.Sprintf("library-order-set: name %d must be string", i))
		}
		names[i] = s
	}
	if err := c.graph.LibraryOrderSet(names); err != nil {
		return errorResponse(id, err.Error())
	}
	result := make([]any, len(names))
	for i, n := range names {
		result[i] = n
	}
	return map[string]any{"id": id, "ok": true, "value": result}
}

func (c *Core) handleClear(id string, msg map[string]any) map[string]any {
	if err := c.graph.Clear(); err != nil {
		return errorResponse(id, err.Error())
	}
	c.traces = nil
	return map[string]any{"id": id, "ok": true, "value": "cleared"}
}

// setEvalFuel configures fuel on the evaluator before an eval.
// Resolution: per-eval (msg["fuel"]) > global default.
func (c *Core) setEvalFuel(msg map[string]any) {
	fuel := c.defaultFuel
	if msg != nil {
		if f, ok := msg["fuel"].(float64); ok && int(f) > 0 {
			fuel = int(f)
		}
	}
	if fuel > 0 {
		c.graph.eval.Fuel = fuel
		c.graph.eval.FuelSet = true
	} else {
		c.graph.eval.FuelSet = false
	}
}

func (c *Core) handleSetFuel(id string, msg map[string]any) map[string]any {
	f, ok := msg["fuel"].(float64)
	if !ok {
		return errorResponse(id, "set-fuel: missing 'fuel' number")
	}
	c.defaultFuel = int(f)
	return map[string]any{"id": id, "ok": true, "value": c.defaultFuel}
}

func (c *Core) handleGetFuel(id string, msg map[string]any) map[string]any {
	return map[string]any{"id": id, "ok": true, "value": c.defaultFuel}
}

func errorResponse(id, errMsg string) map[string]any {
	return map[string]any{"id": id, "ok": false, "error": errMsg}
}

// --- Connection handling ---

func (c *Core) handleClientConnection(conn net.Conn) {
	defer conn.Close()

	for {
		msg, err := ReadMsg(conn)
		if err != nil {
			if err != io.EOF {
				log.Printf("read client message: %v", err)
			}
			return
		}

		resp := c.sendToActor(msg)
		if err := WriteMsg(conn, resp); err != nil {
			log.Printf("write client response: %v", err)
			return
		}
	}
}

func (c *Core) handleCallbackConnection(conn net.Conn) {
	defer conn.Close()

	for {
		msg, err := ReadMsg(conn)
		if err != nil {
			if err != io.EOF {
				log.Printf("read callback message: %v", err)
			}
			return
		}

		resp := c.sendCallbackToActor(msg)
		if err := WriteMsg(conn, resp); err != nil {
			log.Printf("write callback response: %v", err)
			return
		}
	}
}

func (c *Core) handleModuleConnection(conn net.Conn) {
	// Send empty request — module responds with name + manual
	discoverID := NextID()
	if err := WriteMsg(conn, map[string]any{"id": discoverID}); err != nil {
		log.Printf("module: write discovery: %v", err)
		conn.Close()
		return
	}

	manualMsg, err := ReadMsg(conn)
	if err != nil {
		log.Printf("module: read discovery: %v", err)
		conn.Close()
		return
	}

	// Extract module name
	name, ok := manualMsg["name"].(string)
	if !ok || name == "" {
		log.Fatalf("module connected without a name — every module must include a \"name\" field in its discovery response")
	}

	// Check for duplicate name
	c.modMu.Lock()
	for _, m := range c.modules {
		if m.ID == name {
			c.modMu.Unlock()
			log.Fatalf("duplicate module name: %q — a module with this name is already connected", name)
		}
	}

	mod := &ModuleInfo{
		ID:   name,
		conn: conn,
	}
	if val, ok := manualMsg["value"]; ok {
		mod.Manual = GoToValue(val)
	} else {
		mod.Manual = GoToValue(manualMsg)
	}
	c.modules = append(c.modules, mod)
	c.modMu.Unlock()

	log.Printf("module %s connected", name)
}

func (c *Core) removeModule(id string) {
	c.modMu.Lock()
	defer c.modMu.Unlock()
	for i, m := range c.modules {
		if m.ID == id {
			m.conn.Close()
			c.modules = append(c.modules[:i], c.modules[i+1:]...)
			return
		}
	}
}

func (c *Core) findModule(id string) *ModuleInfo {
	c.modMu.Lock()
	defer c.modMu.Unlock()
	for _, m := range c.modules {
		if m.ID == id {
			return m
		}
	}
	return nil
}

// --- Module builtins ---

// builtinModules: (modules) → list of module info maps
func (c *Core) builtinModules(args []Value) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("modules: expected 0 args, got %d", len(args))
	}
	c.modMu.Lock()
	mods := make([]Value, len(c.modules))
	for i, m := range c.modules {
		mods[i] = MapVal(map[string]Value{
			"id":     StringVal(m.ID),
			"manual": m.Manual,
		})
	}
	c.modMu.Unlock()
	return ListVal(mods), nil
}

// builtinSend: (send "mod:0" request-value) → response value
func (c *Core) builtinSend(args []Value) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("send: expected 2 args (module-id, request), got %d", len(args))
	}
	if args[0].Kind != ValString {
		return Value{}, fmt.Errorf("send: module-id must be String, got %s", args[0].KindName())
	}

	mod := c.findModule(args[0].Str)
	if mod == nil {
		return Value{}, fmt.Errorf("send: unknown module: %s", args[0].Str)
	}

	reqBody, err := ValueToGo(args[1])
	if err != nil {
		return Value{}, fmt.Errorf("send: serialize request: %w", err)
	}

	reqMsg := map[string]any{"id": NextID()}
	// If the request is a map, merge it into the message (preserving the id)
	if m, ok := reqBody.(map[string]any); ok {
		for k, v := range m {
			if k != "id" {
				reqMsg[k] = v
			}
		}
	} else {
		reqMsg["value"] = reqBody
	}

	// Serialize send to this module's connection
	mod.mu.Lock()
	defer mod.mu.Unlock()

	if err := WriteMsg(mod.conn, reqMsg); err != nil {
		return Value{}, fmt.Errorf("send: write to %s: %w", mod.ID, err)
	}

	respMsg, err := ReadMsg(mod.conn)
	if err != nil {
		return Value{}, fmt.Errorf("send: read from %s: %w", mod.ID, err)
	}

	// Check for error
	if ok, exists := respMsg["ok"]; exists {
		if okBool, isBool := ok.(bool); isBool && !okBool {
			errStr, _ := respMsg["error"].(string)
			return Value{}, fmt.Errorf("send: %s returned error: %s", mod.ID, errStr)
		}
	}

	// Extract response value
	var respVal Value
	if val, exists := respMsg["value"]; exists {
		respVal = GoToValue(val)
	} else {
		respVal = GoToValue(respMsg)
	}

	// Record send in active trace
	if c.graph.eval.activeTrace != nil {
		c.graph.eval.activeTrace.Sends = append(c.graph.eval.activeTrace.Sends, SendRecord{
			Module:   args[0].Str,
			Request:  args[1],
			Response: respVal,
		})
	}

	return respVal, nil
}

// builtinTraces: (traces) or (traces N) — returns last N traces as list of maps.
func (c *Core) builtinTraces(args []Value) (Value, error) {
	n := len(c.traces)
	if len(args) == 1 {
		if args[0].Kind != ValInt {
			return Value{}, fmt.Errorf("traces: expected Int arg, got %s", args[0].KindName())
		}
		limit := int(args[0].Int)
		if limit < n {
			n = limit
		}
	} else if len(args) > 1 {
		return Value{}, fmt.Errorf("traces: expected 0 or 1 args, got %d", len(args))
	}

	start := len(c.traces) - n
	result := make([]Value, n)
	for i := 0; i < n; i++ {
		result[i] = c.traces[start+i].ToValue()
	}
	return ListVal(result), nil
}

// appendTrace adds a trace and enforces the maxTraces cap.
func (c *Core) appendTrace(t *Trace) {
	c.traces = append(c.traces, *t)
	if len(c.traces) > c.maxTraces {
		// Drop oldest traces
		excess := len(c.traces) - c.maxTraces
		c.traces = c.traces[excess:]
	}
}
