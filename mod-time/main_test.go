package main

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// --- Handler unit tests ---

func TestHandleNow(t *testing.T) {
	before := time.Now().Unix()
	val, errStr := handleNow(nil)
	after := time.Now().Unix()

	if errStr != "" {
		t.Fatalf("unexpected error: %s", errStr)
	}

	m, ok := val.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", val)
	}

	unix := int64(m["unix"].(int64))
	if unix < before || unix > after {
		t.Errorf("unix %d not in [%d, %d]", unix, before, after)
	}

	iso, ok := m["iso"].(string)
	if !ok || iso == "" {
		t.Errorf("expected non-empty iso string, got %v", m["iso"])
	}

	// Verify ISO parses back correctly
	parsed, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		t.Errorf("iso string %q doesn't parse as RFC3339: %v", iso, err)
	}
	if parsed.Unix() != unix {
		t.Errorf("iso parsed to %d, want %d", parsed.Unix(), unix)
	}
}

func TestHandleFormat(t *testing.T) {
	tests := []struct {
		name   string
		req    map[string]any
		want   string
		wantOk bool
	}{
		{
			name:   "basic date",
			req:    map[string]any{"time": float64(1709078400), "layout": "2006-01-02"},
			want:   "2024-02-28",
			wantOk: true,
		},
		{
			name:   "full datetime",
			req:    map[string]any{"time": float64(1709078400), "layout": time.RFC3339},
			want:   "2024-02-28T00:00:00Z",
			wantOk: true,
		},
		{
			name:   "missing time",
			req:    map[string]any{"layout": "2006-01-02"},
			wantOk: false,
		},
		{
			name:   "missing layout",
			req:    map[string]any{"time": float64(1709078400)},
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, errStr := handleFormat(tt.req)
			if tt.wantOk {
				if errStr != "" {
					t.Fatalf("unexpected error: %s", errStr)
				}
				got, ok := val.(string)
				if !ok {
					t.Fatalf("expected string, got %T", val)
				}
				if got != tt.want {
					t.Errorf("got %q, want %q", got, tt.want)
				}
			} else {
				if errStr == "" {
					t.Fatal("expected error, got none")
				}
			}
		})
	}
}

func TestHandleParse(t *testing.T) {
	tests := []struct {
		name   string
		req    map[string]any
		want   int64
		wantOk bool
	}{
		{
			name:   "basic date",
			req:    map[string]any{"value": "2024-02-28", "layout": "2006-01-02"},
			want:   1709078400,
			wantOk: true,
		},
		{
			name:   "invalid format",
			req:    map[string]any{"value": "not-a-date", "layout": "2006-01-02"},
			wantOk: false,
		},
		{
			name:   "missing value",
			req:    map[string]any{"layout": "2006-01-02"},
			wantOk: false,
		},
		{
			name:   "missing layout",
			req:    map[string]any{"value": "2024-02-28"},
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, errStr := handleParse(tt.req)
			if tt.wantOk {
				if errStr != "" {
					t.Fatalf("unexpected error: %s", errStr)
				}
				got, ok := val.(int64)
				if !ok {
					t.Fatalf("expected int64, got %T", val)
				}
				if got != tt.want {
					t.Errorf("got %d, want %d", got, tt.want)
				}
			} else {
				if errStr == "" {
					t.Fatal("expected error, got none")
				}
			}
		})
	}
}

func TestHandleAdd(t *testing.T) {
	tests := []struct {
		name     string
		req      map[string]any
		wantUnix int64
		wantOk   bool
	}{
		{
			name:     "add 2h30m",
			req:      map[string]any{"time": float64(1709078400), "duration": "2h30m"},
			wantUnix: 1709087400,
			wantOk:   true,
		},
		{
			name:     "subtract 24h",
			req:      map[string]any{"time": float64(1709078400), "duration": "-24h"},
			wantUnix: 1708992000,
			wantOk:   true,
		},
		{
			name:   "invalid duration",
			req:    map[string]any{"time": float64(1709078400), "duration": "bogus"},
			wantOk: false,
		},
		{
			name:   "missing time",
			req:    map[string]any{"duration": "1h"},
			wantOk: false,
		},
		{
			name:   "missing duration",
			req:    map[string]any{"time": float64(1709078400)},
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, errStr := handleAdd(tt.req)
			if tt.wantOk {
				if errStr != "" {
					t.Fatalf("unexpected error: %s", errStr)
				}
				m, ok := val.(map[string]any)
				if !ok {
					t.Fatalf("expected map, got %T", val)
				}
				gotUnix := m["unix"].(int64)
				if gotUnix != tt.wantUnix {
					t.Errorf("unix: got %d, want %d", gotUnix, tt.wantUnix)
				}
				iso := m["iso"].(string)
				if iso == "" {
					t.Error("expected non-empty iso")
				}
			} else {
				if errStr == "" {
					t.Fatal("expected error, got none")
				}
			}
		})
	}
}

func TestHandleDiff(t *testing.T) {
	tests := []struct {
		name        string
		req         map[string]any
		wantSeconds float64
		wantOk      bool
	}{
		{
			name:        "24 hours",
			req:         map[string]any{"from": float64(1709078400), "to": float64(1709164800)},
			wantSeconds: 86400,
			wantOk:      true,
		},
		{
			name:        "negative diff",
			req:         map[string]any{"from": float64(1709164800), "to": float64(1709078400)},
			wantSeconds: -86400,
			wantOk:      true,
		},
		{
			name:   "missing from",
			req:    map[string]any{"to": float64(1709164800)},
			wantOk: false,
		},
		{
			name:   "missing to",
			req:    map[string]any{"from": float64(1709078400)},
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, errStr := handleDiff(tt.req)
			if tt.wantOk {
				if errStr != "" {
					t.Fatalf("unexpected error: %s", errStr)
				}
				m, ok := val.(map[string]any)
				if !ok {
					t.Fatalf("expected map, got %T", val)
				}
				gotSec := m["seconds"].(float64)
				if math.Abs(gotSec-tt.wantSeconds) > 0.001 {
					t.Errorf("seconds: got %f, want %f", gotSec, tt.wantSeconds)
				}
				dur := m["duration"].(string)
				if dur == "" {
					t.Error("expected non-empty duration")
				}
			} else {
				if errStr == "" {
					t.Fatal("expected error, got none")
				}
			}
		})
	}
}

func TestHandleManual(t *testing.T) {
	// An empty request (no op) should return the manual string.
	// We test this through the dispatch path in the integration test,
	// but verify the constant is non-empty here.
	if manual == "" {
		t.Fatal("manual string is empty")
	}
}

// --- Integration test ---

func TestIntegration(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "test.sock")

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	// Start the module in a goroutine, connecting to our mock listener.
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := net.Dial("unix", sock)
		if err != nil {
			return
		}
		defer conn.Close()
		dispatch(conn)
	}()

	// Accept the connection from the module.
	serverConn, err := ln.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	defer serverConn.Close()

	// Helper to send a request and read a response.
	sendRecv := func(req map[string]any) map[string]any {
		t.Helper()
		if err := writeMsg(serverConn, req); err != nil {
			t.Fatalf("writeMsg: %v", err)
		}
		resp, err := readMsg(serverConn)
		if err != nil {
			t.Fatalf("readMsg: %v", err)
		}
		return resp
	}

	// Test: empty request returns manual
	t.Run("manual", func(t *testing.T) {
		resp := sendRecv(map[string]any{"id": "m1"})
		if resp["ok"] != true {
			t.Fatalf("expected ok=true, got %v, error: %v", resp["ok"], resp["error"])
		}
		val, ok := resp["value"].(string)
		if !ok || val == "" {
			t.Fatal("expected non-empty manual string")
		}
		if resp["id"] != "m1" {
			t.Errorf("id: got %v, want m1", resp["id"])
		}
	})

	// Test: now
	t.Run("now", func(t *testing.T) {
		before := time.Now().Unix()
		resp := sendRecv(map[string]any{"id": "n1", "op": "now"})
		after := time.Now().Unix()

		if resp["ok"] != true {
			t.Fatalf("expected ok=true, error: %v", resp["error"])
		}
		m, ok := resp["value"].(map[string]any)
		if !ok {
			t.Fatalf("expected map value, got %T", resp["value"])
		}
		unix := int64(m["unix"].(float64))
		if unix < before || unix > after {
			t.Errorf("unix %d not in [%d, %d]", unix, before, after)
		}
	})

	// Test: format
	t.Run("format", func(t *testing.T) {
		resp := sendRecv(map[string]any{
			"id": "f1", "op": "format",
			"time": float64(1709078400), "layout": "2006-01-02",
		})
		if resp["ok"] != true {
			t.Fatalf("expected ok=true, error: %v", resp["error"])
		}
		if resp["value"] != "2024-02-28" {
			t.Errorf("got %v, want 2024-02-28", resp["value"])
		}
	})

	// Test: parse
	t.Run("parse", func(t *testing.T) {
		resp := sendRecv(map[string]any{
			"id": "p1", "op": "parse",
			"value": "2024-02-28", "layout": "2006-01-02",
		})
		if resp["ok"] != true {
			t.Fatalf("expected ok=true, error: %v", resp["error"])
		}
		got := int64(resp["value"].(float64))
		if got != 1709078400 {
			t.Errorf("got %d, want 1709078400", got)
		}
	})

	// Test: add
	t.Run("add", func(t *testing.T) {
		resp := sendRecv(map[string]any{
			"id": "a1", "op": "add",
			"time": float64(1709078400), "duration": "2h30m",
		})
		if resp["ok"] != true {
			t.Fatalf("expected ok=true, error: %v", resp["error"])
		}
		m := resp["value"].(map[string]any)
		gotUnix := int64(m["unix"].(float64))
		if gotUnix != 1709087400 {
			t.Errorf("unix: got %d, want 1709087400", gotUnix)
		}
	})

	// Test: diff
	t.Run("diff", func(t *testing.T) {
		resp := sendRecv(map[string]any{
			"id": "d1", "op": "diff",
			"from": float64(1709078400), "to": float64(1709164800),
		})
		if resp["ok"] != true {
			t.Fatalf("expected ok=true, error: %v", resp["error"])
		}
		m := resp["value"].(map[string]any)
		gotSec := m["seconds"].(float64)
		if gotSec != 86400 {
			t.Errorf("seconds: got %f, want 86400", gotSec)
		}
	})

	// Test: unknown op
	t.Run("unknown op", func(t *testing.T) {
		resp := sendRecv(map[string]any{"id": "u1", "op": "bogus"})
		if resp["ok"] != false {
			t.Fatal("expected ok=false for unknown op")
		}
		if resp["error"] == nil || resp["error"] == "" {
			t.Fatal("expected error message")
		}
	})

	// Close the server side to end the dispatch loop.
	serverConn.Close()
	<-done

	// Clean up socket file.
	os.Remove(sock)
}

// --- Wire format test ---

func TestWireRoundTrip(t *testing.T) {
	// Create a pipe to test wire format without a real socket.
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	msg := map[string]any{"id": "w1", "op": "test", "data": float64(42)}

	go func() {
		if err := writeMsg(client, msg); err != nil {
			t.Errorf("writeMsg: %v", err)
		}
	}()

	got, err := readMsg(server)
	if err != nil {
		t.Fatalf("readMsg: %v", err)
	}

	if got["id"] != "w1" || got["op"] != "test" || got["data"] != float64(42) {
		t.Errorf("round-trip mismatch: got %v", got)
	}
}

func TestWireManualBytesVerify(t *testing.T) {
	// Verify the exact wire format: 4-byte big-endian length + JSON.
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	msg := map[string]any{"id": "x"}

	go func() {
		if err := writeMsg(client, msg); err != nil {
			t.Errorf("writeMsg: %v", err)
		}
	}()

	// Read raw bytes: 4-byte length header.
	var length uint32
	if err := binary.Read(server, binary.BigEndian, &length); err != nil {
		t.Fatalf("read length: %v", err)
	}

	buf := make([]byte, length)
	if _, err := io.ReadFull(server, buf); err != nil {
		t.Fatalf("read body: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(buf, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed["id"] != "x" {
		t.Errorf("got id=%v, want x", parsed["id"])
	}
}
