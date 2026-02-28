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

// --- types ---

type Request struct {
	ID string `json:"id"`
	Op string `json:"op,omitempty"`

	// listen / stop
	Port int `json:"port,omitempty"`
}

type Response struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Value any    `json:"value,omitempty"`
	Error string `json:"error,omitempty"`
}

type CallbackRequest struct {
	ID      string            `json:"id"`
	Module  string            `json:"module"`
	Port    int               `json:"port"`
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Query   string            `json:"query,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

type HTTPResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body"`
}

type CallbackResponse struct {
	ID    string        `json:"id"`
	OK    bool          `json:"ok"`
	Value *HTTPResponse `json:"value,omitempty"`
	Error string        `json:"error,omitempty"`
}

// --- module ---

type Module struct {
	moduleID string

	modConn net.Conn
	cbConn  net.Conn

	cbMu   sync.Mutex // protects writes to cbConn
	modMu  sync.Mutex // protects writes to modConn

	listeners   map[int]*http.Server
	listenersMu sync.Mutex

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
	case "":
		return m.opManual(req)
	default:
		return &Response{ID: req.ID, OK: false, Error: fmt.Sprintf("unknown op: %q", req.Op)}
	}
}

func (m *Module) opManual(req *Request) *Response {
	manual := fmt.Sprintf(`mod-http-server — HTTP server module for logos

Module UUID: %s

Operations:
  listen   {"op": "listen", "port": 8080}       Start listening on a port.
  stop     {"op": "stop", "port": 8080}          Stop listening on a port.
  list-ports {"op": "list-ports"}                List active ports.

When an HTTP request arrives on a listening port, this module sends a
callback to the core with:
  id, module, port, method, path, query, headers, body

The core should respond with:
  {"ok": true, "value": {"status": 200, "headers": {...}, "body": "..."}}

The "module" field in every callback is this module's UUID shown above.
Use it to identify and route requests from this module instance.`, m.moduleID)

	return &Response{ID: req.ID, OK: true, Value: manual}
}

func (m *Module) opListen(req *Request) *Response {
	if req.Port == 0 {
		return &Response{ID: req.ID, OK: false, Error: "missing port"}
	}

	m.listenersMu.Lock()
	defer m.listenersMu.Unlock()

	if _, exists := m.listeners[req.Port]; exists {
		return &Response{ID: req.ID, OK: false, Error: fmt.Sprintf("already listening on %d", req.Port)}
	}

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", req.Port),
		Handler: m.httpHandler(req.Port),
	}

	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return &Response{ID: req.ID, OK: false, Error: err.Error()}
	}

	m.listeners[req.Port] = srv
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("http server on port %d: %v", req.Port, err)
		}
	}()

	log.Printf("listening on port %d", req.Port)
	return &Response{ID: req.ID, OK: true, Value: fmt.Sprintf("listening on %d", req.Port)}
}

func (m *Module) opStop(req *Request) *Response {
	if req.Port == 0 {
		return &Response{ID: req.ID, OK: false, Error: "missing port"}
	}

	m.listenersMu.Lock()
	srv, exists := m.listeners[req.Port]
	if !exists {
		m.listenersMu.Unlock()
		return &Response{ID: req.ID, OK: false, Error: fmt.Sprintf("not listening on %d", req.Port)}
	}
	delete(m.listeners, req.Port)
	m.listenersMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		return &Response{ID: req.ID, OK: false, Error: err.Error()}
	}

	log.Printf("stopped port %d", req.Port)
	return &Response{ID: req.ID, OK: true, Value: fmt.Sprintf("stopped %d", req.Port)}
}

func (m *Module) opListPorts(req *Request) *Response {
	m.listenersMu.Lock()
	defer m.listenersMu.Unlock()

	ports := make([]int, 0, len(m.listeners))
	for p := range m.listeners {
		ports = append(ports, p)
	}
	sort.Ints(ports)

	return &Response{ID: req.ID, OK: true, Value: ports}
}

func (m *Module) httpHandler(port int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}

		headers := make(map[string]string, len(r.Header))
		for k := range r.Header {
			headers[k] = r.Header.Get(k)
		}

		reqID := newUUID()
		cbReq := &CallbackRequest{
			ID:      reqID,
			Module:  m.moduleID,
			Port:    port,
			Method:  r.Method,
			Path:    r.URL.Path,
			Query:   r.URL.RawQuery,
			Headers: headers,
			Body:    string(body),
		}

		ch := make(chan *CallbackResponse, 1)
		m.pendingMu.Lock()
		m.pending[reqID] = ch
		m.pendingMu.Unlock()

		m.cbMu.Lock()
		err = WriteMsg(m.cbConn, cbReq)
		m.cbMu.Unlock()
		if err != nil {
			m.pendingMu.Lock()
			delete(m.pending, reqID)
			m.pendingMu.Unlock()
			http.Error(w, "failed to reach core", http.StatusBadGateway)
			return
		}

		select {
		case resp := <-ch:
			if !resp.OK || resp.Value == nil {
				errMsg := resp.Error
				if errMsg == "" {
					errMsg = "core error"
				}
				http.Error(w, errMsg, http.StatusBadGateway)
				return
			}
			for k, v := range resp.Value.Headers {
				w.Header().Set(k, v)
			}
			w.WriteHeader(resp.Value.Status)
			w.Write([]byte(resp.Value.Body))

		case <-time.After(30 * time.Second):
			m.pendingMu.Lock()
			delete(m.pending, reqID)
			m.pendingMu.Unlock()
			http.Error(w, "core timeout", http.StatusGatewayTimeout)
		}
	})
}

func (m *Module) readCallbackLoop() {
	for {
		raw, err := ReadMsg(m.cbConn)
		if err != nil {
			log.Printf("callback socket read: %v", err)
			// Fail all pending requests
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
	m.listenersMu.Lock()
	defer m.listenersMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for port, srv := range m.listeners {
		log.Printf("shutting down port %d", port)
		srv.Shutdown(ctx)
		delete(m.listeners, port)
	}
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
		moduleID:  newUUID(),
		modConn:   modConn,
		cbConn:    cbConn,
		listeners: make(map[int]*http.Server),
		pending:   make(map[string]chan *CallbackResponse),
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
