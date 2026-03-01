package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"sort"
	"sync"
	"syscall"

	_ "github.com/mattn/go-sqlite3"
)

// --- types ---

type Request struct {
	ID     string      `json:"id"`
	Op     string      `json:"op,omitempty"`
	DB     string      `json:"db,omitempty"`
	SQL    string      `json:"sql,omitempty"`
	Params []any       `json:"params,omitempty"`
	Stmts  []Statement `json:"stmts,omitempty"`
}

type Statement struct {
	SQL    string `json:"sql"`
	Params []any  `json:"params,omitempty"`
}

type Response struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Value any    `json:"value,omitempty"`
	Error string `json:"error,omitempty"`
}

// --- module ---

type Module struct {
	modConn net.Conn
	modMu   sync.Mutex

	dbs   map[string]*sql.DB
	dbsMu sync.Mutex
}

const moduleName = "mod-sqlite"

func (m *Module) handleOp(req *Request) any {
	switch req.Op {
	case "open":
		return m.opOpen(req)
	case "close":
		return m.opClose(req)
	case "query":
		return m.opQuery(req)
	case "exec":
		return m.opExec(req)
	case "exec-multi":
		return m.opExecMulti(req)
	case "list":
		return m.opList(req)
	case "drop":
		return m.opDrop(req)
	case "":
		return m.opManual(req)
	default:
		return &Response{ID: req.ID, OK: false, Error: fmt.Sprintf("unknown op: %q", req.Op)}
	}
}

func (m *Module) opManual(req *Request) any {
	manual := `mod-sqlite — SQLite database module for logos

Operations:
  open       {"op": "open", "db": "mydb.db"}
             Open (or create) a SQLite database file.

  close      {"op": "close", "db": "mydb.db"}
             Close an open database.

  query      {"op": "query", "db": "mydb.db", "sql": "SELECT ...", "params": [...]}
             Execute a read query. Returns a list of maps (column→value).
             params is optional (positional ? placeholders).

  exec       {"op": "exec", "db": "mydb.db", "sql": "INSERT ...", "params": [...]}
             Execute a write statement. Returns {"rows_affected": N, "last_insert_id": N}.
             params is optional.

  exec-multi {"op": "exec-multi", "db": "mydb.db", "stmts": [{"sql": "...", "params": [...]}, ...]}
             Execute multiple write statements in a single transaction.
             Returns {"results": [{"rows_affected": N, "last_insert_id": N}, ...]}.

  list       {"op": "list"}
             List all open database names.

  drop       {"op": "drop", "db": "mydb.db"}
             Close the database and delete its file.`

	return map[string]any{"id": req.ID, "ok": true, "name": moduleName, "value": manual}
}

func (m *Module) getDB(req *Request) (*sql.DB, *Response) {
	if req.DB == "" {
		return nil, &Response{ID: req.ID, OK: false, Error: "missing db"}
	}
	m.dbsMu.Lock()
	db, ok := m.dbs[req.DB]
	m.dbsMu.Unlock()
	if !ok {
		return nil, &Response{ID: req.ID, OK: false, Error: fmt.Sprintf("database %q not open", req.DB)}
	}
	return db, nil
}

func (m *Module) opOpen(req *Request) *Response {
	if req.DB == "" {
		return &Response{ID: req.ID, OK: false, Error: "missing db"}
	}

	m.dbsMu.Lock()
	defer m.dbsMu.Unlock()

	if _, exists := m.dbs[req.DB]; exists {
		return &Response{ID: req.ID, OK: false, Error: fmt.Sprintf("database %q already open", req.DB)}
	}

	db, err := sql.Open("sqlite3", req.DB)
	if err != nil {
		return &Response{ID: req.ID, OK: false, Error: err.Error()}
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return &Response{ID: req.ID, OK: false, Error: err.Error()}
	}

	m.dbs[req.DB] = db
	log.Printf("opened database: %s", req.DB)
	return &Response{ID: req.ID, OK: true, Value: fmt.Sprintf("opened %s", req.DB)}
}

func (m *Module) opClose(req *Request) *Response {
	if req.DB == "" {
		return &Response{ID: req.ID, OK: false, Error: "missing db"}
	}

	m.dbsMu.Lock()
	db, exists := m.dbs[req.DB]
	if !exists {
		m.dbsMu.Unlock()
		return &Response{ID: req.ID, OK: false, Error: fmt.Sprintf("database %q not open", req.DB)}
	}
	delete(m.dbs, req.DB)
	m.dbsMu.Unlock()

	if err := db.Close(); err != nil {
		return &Response{ID: req.ID, OK: false, Error: err.Error()}
	}

	log.Printf("closed database: %s", req.DB)
	return &Response{ID: req.ID, OK: true, Value: fmt.Sprintf("closed %s", req.DB)}
}

func (m *Module) opList(req *Request) *Response {
	m.dbsMu.Lock()
	defer m.dbsMu.Unlock()

	names := make([]string, 0, len(m.dbs))
	for name := range m.dbs {
		names = append(names, name)
	}
	sort.Strings(names)

	return &Response{ID: req.ID, OK: true, Value: names}
}

func (m *Module) opDrop(req *Request) *Response {
	if req.DB == "" {
		return &Response{ID: req.ID, OK: false, Error: "missing db"}
	}

	m.dbsMu.Lock()
	db, open := m.dbs[req.DB]
	if open {
		delete(m.dbs, req.DB)
	}
	m.dbsMu.Unlock()

	if open {
		db.Close()
	}

	if err := os.Remove(req.DB); err != nil {
		return &Response{ID: req.ID, OK: false, Error: err.Error()}
	}

	log.Printf("dropped database: %s", req.DB)
	return &Response{ID: req.ID, OK: true, Value: fmt.Sprintf("dropped %s", req.DB)}
}

func (m *Module) opQuery(req *Request) *Response {
	db, errResp := m.getDB(req)
	if errResp != nil {
		return errResp
	}
	if req.SQL == "" {
		return &Response{ID: req.ID, OK: false, Error: "missing sql"}
	}

	rows, err := db.Query(req.SQL, req.Params...)
	if err != nil {
		return &Response{ID: req.ID, OK: false, Error: err.Error()}
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return &Response{ID: req.ID, OK: false, Error: err.Error()}
	}

	results := make([]map[string]any, 0)
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return &Response{ID: req.ID, OK: false, Error: err.Error()}
		}
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			if b, ok := vals[i].([]byte); ok {
				row[col] = string(b)
			} else {
				row[col] = vals[i]
			}
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return &Response{ID: req.ID, OK: false, Error: err.Error()}
	}

	return &Response{ID: req.ID, OK: true, Value: results}
}

func (m *Module) opExec(req *Request) *Response {
	db, errResp := m.getDB(req)
	if errResp != nil {
		return errResp
	}
	if req.SQL == "" {
		return &Response{ID: req.ID, OK: false, Error: "missing sql"}
	}

	result, err := db.Exec(req.SQL, req.Params...)
	if err != nil {
		return &Response{ID: req.ID, OK: false, Error: err.Error()}
	}

	ra, _ := result.RowsAffected()
	li, _ := result.LastInsertId()

	return &Response{ID: req.ID, OK: true, Value: map[string]int64{
		"rows_affected":  ra,
		"last_insert_id": li,
	}}
}

func (m *Module) opExecMulti(req *Request) *Response {
	db, errResp := m.getDB(req)
	if errResp != nil {
		return errResp
	}
	if len(req.Stmts) == 0 {
		return &Response{ID: req.ID, OK: false, Error: "missing stmts"}
	}

	tx, err := db.Begin()
	if err != nil {
		return &Response{ID: req.ID, OK: false, Error: err.Error()}
	}

	var results []map[string]int64
	for i, stmt := range req.Stmts {
		if stmt.SQL == "" {
			tx.Rollback()
			return &Response{ID: req.ID, OK: false, Error: fmt.Sprintf("stmt %d: missing sql", i)}
		}
		result, err := tx.Exec(stmt.SQL, stmt.Params...)
		if err != nil {
			tx.Rollback()
			return &Response{ID: req.ID, OK: false, Error: fmt.Sprintf("stmt %d: %s", i, err.Error())}
		}
		ra, _ := result.RowsAffected()
		li, _ := result.LastInsertId()
		results = append(results, map[string]int64{
			"rows_affected":  ra,
			"last_insert_id": li,
		})
	}

	if err := tx.Commit(); err != nil {
		return &Response{ID: req.ID, OK: false, Error: err.Error()}
	}

	return &Response{ID: req.ID, OK: true, Value: map[string]any{"results": results}}
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
	m.dbsMu.Lock()
	defer m.dbsMu.Unlock()

	for name, db := range m.dbs {
		log.Printf("closing database: %s", name)
		db.Close()
		delete(m.dbs, name)
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

	modConn, err := net.Dial("unix", modSock)
	if err != nil {
		log.Fatalf("connect to module socket: %v", err)
	}
	defer modConn.Close()
	log.Printf("connected to module socket: %s", modSock)

	m := &Module{
		modConn: modConn,
		dbs:     make(map[string]*sql.DB),
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("shutting down...")
		m.shutdown()
		os.Exit(0)
	}()

	m.readModLoop()
	m.shutdown()
}
