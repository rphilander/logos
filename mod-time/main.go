package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

// --- Wire helpers ---

func readMsg(conn net.Conn) (map[string]any, error) {
	var length uint32
	if err := binary.Read(conn, binary.BigEndian, &length); err != nil {
		return nil, err
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return nil, err
	}
	var msg map[string]any
	if err := json.Unmarshal(buf, &msg); err != nil {
		return nil, err
	}
	return msg, nil
}

func writeMsg(conn net.Conn, msg map[string]any) error {
	buf, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	length := uint32(len(buf))
	if err := binary.Write(conn, binary.BigEndian, length); err != nil {
		return err
	}
	_, err = conn.Write(buf)
	return err
}

// --- Handlers ---

func handleNow(_ map[string]any) (any, string) {
	now := time.Now()
	return map[string]any{
		"unix": now.Unix(),
		"iso":  now.UTC().Format(time.RFC3339),
	}, ""
}

func handleFormat(req map[string]any) (any, string) {
	t, ok := getFloat(req, "time")
	if !ok {
		return nil, "missing required field: time"
	}
	layout, ok := getString(req, "layout")
	if !ok {
		return nil, "missing required field: layout"
	}
	ts := time.Unix(int64(t), 0).UTC()
	return ts.Format(layout), ""
}

func handleParse(req map[string]any) (any, string) {
	value, ok := getString(req, "value")
	if !ok {
		return nil, "missing required field: value"
	}
	layout, ok := getString(req, "layout")
	if !ok {
		return nil, "missing required field: layout"
	}
	parsed, err := time.Parse(layout, value)
	if err != nil {
		return nil, fmt.Sprintf("parse error: %v", err)
	}
	return parsed.Unix(), ""
}

func handleAdd(req map[string]any) (any, string) {
	t, ok := getFloat(req, "time")
	if !ok {
		return nil, "missing required field: time"
	}
	durStr, ok := getString(req, "duration")
	if !ok {
		return nil, "missing required field: duration"
	}
	dur, err := time.ParseDuration(durStr)
	if err != nil {
		return nil, fmt.Sprintf("invalid duration: %v", err)
	}
	result := time.Unix(int64(t), 0).Add(dur).UTC()
	return map[string]any{
		"unix": result.Unix(),
		"iso":  result.Format(time.RFC3339),
	}, ""
}

func handleDiff(req map[string]any) (any, string) {
	from, ok := getFloat(req, "from")
	if !ok {
		return nil, "missing required field: from"
	}
	to, ok := getFloat(req, "to")
	if !ok {
		return nil, "missing required field: to"
	}
	fromT := time.Unix(int64(from), 0)
	toT := time.Unix(int64(to), 0)
	diff := toT.Sub(fromT)
	return map[string]any{
		"duration": diff.String(),
		"seconds":  diff.Seconds(),
	}, ""
}

// --- Field helpers ---

func getFloat(m map[string]any, key string) (float64, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	f, ok := v.(float64)
	return f, ok
}

func getString(m map[string]any, key string) (string, bool) {
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// --- Manual ---

const manual = `mod-time — Logos time module

Operations:

  now
    Returns the current time.
    Example request:  {"id":"1", "op":"now"}
    Example response: {"id":"1", "ok":true, "value":{"unix":1709078400, "iso":"2024-02-28T00:00:00Z"}}

  format
    Formats a unix timestamp using a Go time layout.
    Fields: time (unix seconds), layout (Go format string)
    Example request:  {"id":"2", "op":"format", "time":1709078400, "layout":"2006-01-02"}
    Example response: {"id":"2", "ok":true, "value":"2024-02-28"}

  parse
    Parses a time string into a unix timestamp.
    Fields: value (string), layout (Go format string)
    Example request:  {"id":"3", "op":"parse", "value":"2024-02-28", "layout":"2006-01-02"}
    Example response: {"id":"3", "ok":true, "value":1709078400}

  add
    Adds a duration to a unix timestamp.
    Fields: time (unix seconds), duration (Go duration string, e.g. "2h30m", "-24h")
    Example request:  {"id":"4", "op":"add", "time":1709078400, "duration":"2h30m"}
    Example response: {"id":"4", "ok":true, "value":{"unix":1709087400, "iso":"2024-02-28T02:30:00Z"}}

  diff
    Computes the difference between two unix timestamps.
    Fields: from (unix seconds), to (unix seconds)
    Example request:  {"id":"5", "op":"diff", "from":1709078400, "to":1709164800}
    Example response: {"id":"5", "ok":true, "value":{"duration":"24h0m0s", "seconds":86400}}
`

// --- Dispatch ---

func dispatch(conn net.Conn) {
	for {
		req, err := readMsg(conn)
		if err != nil {
			return
		}

		id, _ := req["id"]
		resp := map[string]any{"id": id}

		op, _ := getString(req, "op")
		var value any
		var errStr string

		switch op {
		case "":
			value = manual
		case "now":
			value, errStr = handleNow(req)
		case "format":
			value, errStr = handleFormat(req)
		case "parse":
			value, errStr = handleParse(req)
		case "add":
			value, errStr = handleAdd(req)
		case "diff":
			value, errStr = handleDiff(req)
		default:
			errStr = fmt.Sprintf("unknown op: %s", op)
		}

		if errStr != "" {
			resp["ok"] = false
			resp["error"] = errStr
		} else {
			resp["ok"] = true
			resp["value"] = value
		}

		if err := writeMsg(conn, resp); err != nil {
			return
		}
	}
}

// --- main ---

func main() {
	sock := os.Getenv("LOGOS_MOD_SOCK")
	if sock == "" {
		sock = "/tmp/logos-mod.sock"
	}

	conn, err := net.Dial("unix", sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mod-time: dial %s: %v\n", sock, err)
		os.Exit(1)
	}
	defer conn.Close()

	dispatch(conn)
}
