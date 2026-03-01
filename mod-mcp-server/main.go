package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"sync"
	"syscall"
	"time"
)

func newUUID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		log.Fatalf("generate uuid: %v", err)
	}
	buf[6] = (buf[6] & 0x0f) | 0x40 // version 4
	buf[8] = (buf[8] & 0x3f) | 0x80 // variant 2
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}

// --- JSON-RPC types ---

type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      any           `json:"id"`
	Result  any           `json:"result,omitempty"`
	Error   *JSONRPCError `json:"error,omitempty"`
}

type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type JSONRPCNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
}

// --- MCP types ---

type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema"`
}

type ToolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type ToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// --- MCP session ---

type Session struct {
	id string
	// SSE stream for server-initiated notifications (optional)
	mu      sync.Mutex
	sseW    http.ResponseWriter
	sseDone chan struct{}
}

func (s *Session) sendSSE(data any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sseW == nil {
		return
	}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	fmt.Fprintf(s.sseW, "event: message\ndata: %s\n\n", jsonData)
	if f, ok := s.sseW.(http.Flusher); ok {
		f.Flush()
	}
}

// --- MCP Server (per port) ---

type MCPServer struct {
	port   int
	module *Module
	srv    *http.Server

	tools   map[string]*Tool
	toolsMu sync.RWMutex

	sessions   map[string]*Session
	sessionsMu sync.RWMutex
}

func newMCPServer(port int, module *Module) *MCPServer {
	s := &MCPServer{
		port:     port,
		module:   module,
		tools:    make(map[string]*Tool),
		sessions: make(map[string]*Session),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", s.handleMCP)

	s.srv = &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	return s
}

func (s *MCPServer) handleMCP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Mcp-Session-Id")
	w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id")

	switch r.Method {
	case "OPTIONS":
		w.WriteHeader(http.StatusOK)
	case "POST":
		s.handlePost(w, r)
	case "GET":
		s.handleGet(w, r)
	case "DELETE":
		s.handleDelete(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *MCPServer) handlePost(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONRPCError(w, nil, -32700, "failed to read body")
		return
	}

	var req JSONRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSONRPCError(w, nil, -32700, "parse error")
		return
	}

	// initialize does not require a session
	if req.Method == "initialize" {
		s.handleInitialize(w, &req)
		return
	}

	// notifications don't require session validation
	if req.ID == nil {
		s.processNotification(&req)
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// all other requests require a valid session
	sessionID := r.Header.Get("Mcp-Session-Id")
	s.sessionsMu.RLock()
	_, ok := s.sessions[sessionID]
	s.sessionsMu.RUnlock()
	if !ok {
		writeJSONRPCError(w, req.ID, -32600, "invalid or missing session")
		return
	}

	resp := s.processRequest(&req)
	w.Header().Set("Mcp-Session-Id", sessionID)
	writeJSON(w, resp)
}

func (s *MCPServer) handleGet(w http.ResponseWriter, r *http.Request) {
	// SSE stream for server-initiated notifications
	sessionID := r.Header.Get("Mcp-Session-Id")
	s.sessionsMu.RLock()
	session, ok := s.sessions[sessionID]
	s.sessionsMu.RUnlock()
	if !ok {
		http.Error(w, "invalid or missing session", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Mcp-Session-Id", sessionID)
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	done := make(chan struct{})

	session.mu.Lock()
	session.sseW = w
	session.sseDone = done
	session.mu.Unlock()

	log.Printf("port %d: session %s opened SSE stream", s.port, sessionID)

	select {
	case <-done:
	case <-r.Context().Done():
	}

	session.mu.Lock()
	session.sseW = nil
	session.sseDone = nil
	session.mu.Unlock()

	log.Printf("port %d: session %s SSE stream closed", s.port, sessionID)
}

func (s *MCPServer) handleDelete(w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get("Mcp-Session-Id")

	s.sessionsMu.Lock()
	session, ok := s.sessions[sessionID]
	if ok {
		delete(s.sessions, sessionID)
	}
	s.sessionsMu.Unlock()

	if !ok {
		http.Error(w, "invalid or missing session", http.StatusBadRequest)
		return
	}

	// close SSE stream if open
	session.mu.Lock()
	if session.sseDone != nil {
		close(session.sseDone)
	}
	session.mu.Unlock()

	log.Printf("port %d: session %s deleted", s.port, sessionID)
	w.WriteHeader(http.StatusOK)
}

func (s *MCPServer) handleInitialize(w http.ResponseWriter, req *JSONRPCRequest) {
	sessionID := newUUID()

	session := &Session{id: sessionID}
	s.sessionsMu.Lock()
	s.sessions[sessionID] = session
	s.sessionsMu.Unlock()

	log.Printf("port %d: new session %s", s.port, sessionID)

	resp := &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]any{
				"tools": map[string]any{
					"listChanged": true,
				},
			},
			"serverInfo": map[string]any{
				"name":    "logos-mcp",
				"version": "1.0.0",
			},
		},
	}

	w.Header().Set("Mcp-Session-Id", sessionID)
	writeJSON(w, resp)
}

func (s *MCPServer) processNotification(req *JSONRPCRequest) {
	switch req.Method {
	case "notifications/initialized":
		log.Printf("port %d: client initialized", s.port)
	case "notifications/cancelled":
		log.Printf("port %d: client cancelled request", s.port)
	}
}

func (s *MCPServer) processRequest(req *JSONRPCRequest) *JSONRPCResponse {
	switch req.Method {
	case "ping":
		return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(req)
	default:
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &JSONRPCError{Code: -32601, Message: fmt.Sprintf("method not found: %s", req.Method)},
		}
	}
}

func (s *MCPServer) handleToolsList(req *JSONRPCRequest) *JSONRPCResponse {
	s.toolsMu.RLock()
	defer s.toolsMu.RUnlock()

	tools := make([]*Tool, 0, len(s.tools))
	for _, t := range s.tools {
		tools = append(tools, t)
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })

	return &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  map[string]any{"tools": tools},
	}
}

func (s *MCPServer) handleToolsCall(req *JSONRPCRequest) *JSONRPCResponse {
	var params ToolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &JSONRPCError{Code: -32602, Message: "invalid params"},
		}
	}

	s.toolsMu.RLock()
	_, exists := s.tools[params.Name]
	s.toolsMu.RUnlock()

	if !exists {
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: &ToolResult{
				Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("unknown tool: %s", params.Name)}},
				IsError: true,
			},
		}
	}

	// send callback to core
	cbReqID := newUUID()
	cbReq := &CallbackRequest{
		ID:        cbReqID,
		Module:    s.module.moduleID,
		Port:      s.port,
		Tool:      params.Name,
		Arguments: params.Arguments,
	}

	ch := make(chan *CallbackResponse, 1)
	s.module.pendingMu.Lock()
	s.module.pending[cbReqID] = ch
	s.module.pendingMu.Unlock()

	s.module.cbMu.Lock()
	err := WriteMsg(s.module.cbConn, cbReq)
	s.module.cbMu.Unlock()
	if err != nil {
		s.module.pendingMu.Lock()
		delete(s.module.pending, cbReqID)
		s.module.pendingMu.Unlock()
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: &ToolResult{
				Content: []ToolContent{{Type: "text", Text: "failed to reach core"}},
				IsError: true,
			},
		}
	}

	select {
	case resp := <-ch:
		if !resp.OK {
			errMsg := resp.Error
			if errMsg == "" {
				errMsg = "core error"
			}
			return &JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: &ToolResult{
					Content: []ToolContent{{Type: "text", Text: errMsg}},
					IsError: true,
				},
			}
		}
		var text string
		switch v := resp.Value.(type) {
		case string:
			text = v
		default:
			jsonBytes, _ := json.Marshal(v)
			text = string(jsonBytes)
		}
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  &ToolResult{Content: []ToolContent{{Type: "text", Text: text}}},
		}

	case <-time.After(30 * time.Second):
		s.module.pendingMu.Lock()
		delete(s.module.pending, cbReqID)
		s.module.pendingMu.Unlock()
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: &ToolResult{
				Content: []ToolContent{{Type: "text", Text: "core timeout"}},
				IsError: true,
			},
		}
	}
}

func (s *MCPServer) notifyToolsChanged() {
	s.sessionsMu.RLock()
	defer s.sessionsMu.RUnlock()

	notification := &JSONRPCNotification{
		JSONRPC: "2.0",
		Method:  "notifications/tools/list_changed",
	}

	for _, session := range s.sessions {
		session.sendSSE(notification)
	}
}

func (s *MCPServer) closeSessions() {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	for _, session := range s.sessions {
		session.mu.Lock()
		if session.sseDone != nil {
			close(session.sseDone)
		}
		session.mu.Unlock()
	}
	s.sessions = make(map[string]*Session)
}

// --- logos protocol types ---

type Request struct {
	ID          string         `json:"id"`
	Op          string         `json:"op,omitempty"`
	Port        int            `json:"port,omitempty"`
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	Schema      map[string]any `json:"schema,omitempty"`
}

type Response struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Value any    `json:"value,omitempty"`
	Error string `json:"error,omitempty"`
}

type CallbackRequest struct {
	ID        string         `json:"id"`
	Module    string         `json:"module"`
	Port      int            `json:"port"`
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type CallbackResponse struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Value any    `json:"value,omitempty"`
	Error string `json:"error,omitempty"`
}

// --- Module ---

type Module struct {
	moduleID string

	modConn net.Conn
	cbConn  net.Conn

	cbMu  sync.Mutex
	modMu sync.Mutex

	servers   map[int]*MCPServer
	serversMu sync.Mutex

	pending   map[string]chan *CallbackResponse
	pendingMu sync.Mutex
}

func (m *Module) handleOp(req *Request) *Response {
	switch req.Op {
	case "listen":
		return m.opListen(req)
	case "stop":
		return m.opStop(req)
	case "list-ports":
		return m.opListPorts(req)
	case "add-tool":
		return m.opAddTool(req)
	case "remove-tool":
		return m.opRemoveTool(req)
	case "list-tools":
		return m.opListTools(req)
	case "":
		return m.opManual(req)
	default:
		return &Response{ID: req.ID, OK: false, Error: fmt.Sprintf("unknown op: %q", req.Op)}
	}
}

func (m *Module) opManual(req *Request) *Response {
	manual := fmt.Sprintf(`mod-mcp-server — MCP server module for logos

Module UUID: %s

Every callback message sent to the core includes "module": "<uuid>" set to
the UUID above, so the core can identify requests from this module instance.

Operations:
  listen       {"op": "listen", "port": 8080}
    Start an MCP server on a port (Streamable HTTP transport at /mcp).

  stop         {"op": "stop", "port": 8080}
    Stop the MCP server on a port.

  list-ports   {"op": "list-ports"}
    List active ports.

  add-tool     {"op": "add-tool", "port": 8080, "name": "...", "description": "...", "schema": {...}}
    Add a tool to the MCP server on a port. Schema is a JSON Schema object
    for the tool's input. Omit schema for tools that take no arguments.

  remove-tool  {"op": "remove-tool", "port": 8080, "name": "..."}
    Remove a tool from the MCP server on a port.

  list-tools   {"op": "list-tools", "port": 8080}
    List tools registered on a port.

When an MCP client calls a tool, this module sends a callback to the core:
  {"id": "...", "module": "%s", "port": 8080, "tool": "name", "arguments": {...}}

The core should respond with:
  {"ok": true, "value": "result text or data"}`, m.moduleID, m.moduleID)

	return &Response{ID: req.ID, OK: true, Value: manual}
}

func (m *Module) opListen(req *Request) *Response {
	if req.Port == 0 {
		return &Response{ID: req.ID, OK: false, Error: "missing port"}
	}

	m.serversMu.Lock()
	defer m.serversMu.Unlock()

	if _, exists := m.servers[req.Port]; exists {
		return &Response{ID: req.ID, OK: false, Error: fmt.Sprintf("already listening on %d", req.Port)}
	}

	s := newMCPServer(req.Port, m)

	ln, err := net.Listen("tcp", s.srv.Addr)
	if err != nil {
		return &Response{ID: req.ID, OK: false, Error: err.Error()}
	}

	m.servers[req.Port] = s
	go func() {
		if err := s.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("mcp server on port %d: %v", req.Port, err)
		}
	}()

	log.Printf("listening on port %d", req.Port)
	return &Response{ID: req.ID, OK: true, Value: fmt.Sprintf("listening on %d", req.Port)}
}

func (m *Module) opStop(req *Request) *Response {
	if req.Port == 0 {
		return &Response{ID: req.ID, OK: false, Error: "missing port"}
	}

	m.serversMu.Lock()
	s, exists := m.servers[req.Port]
	if !exists {
		m.serversMu.Unlock()
		return &Response{ID: req.ID, OK: false, Error: fmt.Sprintf("not listening on %d", req.Port)}
	}
	delete(m.servers, req.Port)
	m.serversMu.Unlock()

	s.closeSessions()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.srv.Shutdown(ctx); err != nil {
		return &Response{ID: req.ID, OK: false, Error: err.Error()}
	}

	log.Printf("stopped port %d", req.Port)
	return &Response{ID: req.ID, OK: true, Value: fmt.Sprintf("stopped %d", req.Port)}
}

func (m *Module) opListPorts(req *Request) *Response {
	m.serversMu.Lock()
	defer m.serversMu.Unlock()

	ports := make([]int, 0, len(m.servers))
	for p := range m.servers {
		ports = append(ports, p)
	}
	sort.Ints(ports)

	return &Response{ID: req.ID, OK: true, Value: ports}
}

func (m *Module) opAddTool(req *Request) *Response {
	if req.Port == 0 {
		return &Response{ID: req.ID, OK: false, Error: "missing port"}
	}
	if req.Name == "" {
		return &Response{ID: req.ID, OK: false, Error: "missing name"}
	}

	m.serversMu.Lock()
	s, exists := m.servers[req.Port]
	m.serversMu.Unlock()

	if !exists {
		return &Response{ID: req.ID, OK: false, Error: fmt.Sprintf("not listening on %d", req.Port)}
	}

	schema := req.Schema
	if schema == nil {
		schema = map[string]any{"type": "object", "properties": map[string]any{}}
	}

	tool := &Tool{
		Name:        req.Name,
		Description: req.Description,
		InputSchema: schema,
	}

	s.toolsMu.Lock()
	s.tools[req.Name] = tool
	s.toolsMu.Unlock()

	s.notifyToolsChanged()

	log.Printf("port %d: added tool %q", req.Port, req.Name)
	return &Response{ID: req.ID, OK: true, Value: fmt.Sprintf("added tool %q on port %d", req.Name, req.Port)}
}

func (m *Module) opRemoveTool(req *Request) *Response {
	if req.Port == 0 {
		return &Response{ID: req.ID, OK: false, Error: "missing port"}
	}
	if req.Name == "" {
		return &Response{ID: req.ID, OK: false, Error: "missing name"}
	}

	m.serversMu.Lock()
	s, exists := m.servers[req.Port]
	m.serversMu.Unlock()

	if !exists {
		return &Response{ID: req.ID, OK: false, Error: fmt.Sprintf("not listening on %d", req.Port)}
	}

	s.toolsMu.Lock()
	_, exists = s.tools[req.Name]
	if !exists {
		s.toolsMu.Unlock()
		return &Response{ID: req.ID, OK: false, Error: fmt.Sprintf("no tool %q on port %d", req.Name, req.Port)}
	}
	delete(s.tools, req.Name)
	s.toolsMu.Unlock()

	s.notifyToolsChanged()

	log.Printf("port %d: removed tool %q", req.Port, req.Name)
	return &Response{ID: req.ID, OK: true, Value: fmt.Sprintf("removed tool %q from port %d", req.Name, req.Port)}
}

func (m *Module) opListTools(req *Request) *Response {
	if req.Port == 0 {
		return &Response{ID: req.ID, OK: false, Error: "missing port"}
	}

	m.serversMu.Lock()
	s, exists := m.servers[req.Port]
	m.serversMu.Unlock()

	if !exists {
		return &Response{ID: req.ID, OK: false, Error: fmt.Sprintf("not listening on %d", req.Port)}
	}

	s.toolsMu.RLock()
	defer s.toolsMu.RUnlock()

	tools := make([]map[string]any, 0, len(s.tools))
	for _, t := range s.tools {
		tools = append(tools, map[string]any{
			"name":        t.Name,
			"description": t.Description,
		})
	}

	return &Response{ID: req.ID, OK: true, Value: tools}
}

// --- callback loop ---

func (m *Module) readCallbackLoop() {
	for {
		raw, err := ReadMsg(m.cbConn)
		if err != nil {
			log.Printf("callback socket read: %v", err)
			m.pendingMu.Lock()
			for id, ch := range m.pending {
				ch <- &CallbackResponse{ID: id, OK: false, Error: "callback socket closed"}
				delete(m.pending, id)
			}
			m.pendingMu.Unlock()
			return
		}

		var resp CallbackResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			log.Printf("callback unmarshal: %v", err)
			continue
		}

		m.pendingMu.Lock()
		ch, ok := m.pending[resp.ID]
		if ok {
			delete(m.pending, resp.ID)
		}
		m.pendingMu.Unlock()

		if ok {
			ch <- &resp
		} else {
			log.Printf("no pending request for callback id %s", resp.ID)
		}
	}
}

func (m *Module) readModLoop() {
	for {
		raw, err := ReadMsg(m.modConn)
		if err != nil {
			log.Printf("module socket closed: %v", err)
			return
		}

		var req Request
		if err := json.Unmarshal(raw, &req); err != nil {
			log.Printf("unmarshal request: %v", err)
			continue
		}

		resp := m.handleOp(&req)

		m.modMu.Lock()
		err = WriteMsg(m.modConn, resp)
		m.modMu.Unlock()
		if err != nil {
			log.Printf("write response: %v", err)
			return
		}
	}
}

func (m *Module) shutdown() {
	m.serversMu.Lock()
	defer m.serversMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for port, s := range m.servers {
		log.Printf("shutting down port %d", port)
		s.closeSessions()
		s.srv.Shutdown(ctx)
		delete(m.servers, port)
	}
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeJSONRPCError(w http.ResponseWriter, id any, code int, message string) {
	writeJSON(w, &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &JSONRPCError{Code: code, Message: message},
	})
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	modSock := envOr("LOGOS_MOD_SOCK", "/tmp/logos-mod.sock")
	cbSock := envOr("LOGOS_CB_SOCK", "/tmp/logos-cb.sock")

	modConn, err := net.Dial("unix", modSock)
	if err != nil {
		log.Fatalf("connect to module socket: %v", err)
	}
	defer modConn.Close()
	log.Printf("connected to module socket: %s", modSock)

	cbConn, err := net.Dial("unix", cbSock)
	if err != nil {
		log.Fatalf("connect to callback socket: %v", err)
	}
	defer cbConn.Close()
	log.Printf("connected to callback socket: %s", cbSock)

	m := &Module{
		moduleID: newUUID(),
		modConn:  modConn,
		cbConn:   cbConn,
		servers:  make(map[int]*MCPServer),
		pending:  make(map[string]chan *CallbackResponse),
	}
	log.Printf("module id: %s", m.moduleID)

	go m.readCallbackLoop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("shutting down...")
		m.shutdown()
		os.Exit(0)
	}()

	m.readModLoop() // blocks until module socket closes
	m.shutdown()
}
