package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
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

// --- Path resolution ---

var projectRoot string

func resolvePath(rel string) (string, string) {
	if projectRoot == "" {
		return "", "LOGOS_PROJECT_ROOT not set"
	}
	if filepath.IsAbs(rel) {
		return "", "path must be relative"
	}
	abs := filepath.Join(projectRoot, rel)
	abs = filepath.Clean(abs)
	if !strings.HasPrefix(abs, projectRoot) {
		return "", "path escapes project root"
	}
	return abs, ""
}

// --- Handlers ---

func handleListDir(req map[string]any) (any, string) {
	path, ok := getString(req, "path")
	if !ok {
		path = "."
	}
	abs, errStr := resolvePath(path)
	if errStr != "" {
		return nil, errStr
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, fmt.Sprintf("read dir: %v", err)
	}
	result := make([]any, 0, len(entries))
	for _, e := range entries {
		typ := "file"
		if e.IsDir() {
			typ = "dir"
		}
		result = append(result, map[string]any{
			"name": e.Name(),
			"type": typ,
		})
	}
	return result, ""
}

func handleReadFile(req map[string]any) (any, string) {
	path, ok := getString(req, "path")
	if !ok {
		return nil, "missing required field: path"
	}
	abs, errStr := resolvePath(path)
	if errStr != "" {
		return nil, errStr
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Sprintf("read file: %v", err)
	}
	return string(data), ""
}

func handleStat(req map[string]any) (any, string) {
	path, ok := getString(req, "path")
	if !ok {
		return nil, "missing required field: path"
	}
	abs, errStr := resolvePath(path)
	if errStr != "" {
		return nil, errStr
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Sprintf("stat: %v", err)
	}
	return map[string]any{
		"name":     info.Name(),
		"size":     info.Size(),
		"is_dir":   info.IsDir(),
		"modified": info.ModTime().UTC().Format(time.RFC3339),
	}, ""
}

// --- Field helpers ---

func getString(m map[string]any, key string) (string, bool) {
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// --- Manual ---

const manual = `mod-fs — Logos filesystem module

All paths are relative to LOGOS_PROJECT_ROOT. Absolute paths are rejected.

Operations:

  list-dir
    Lists directory entries with their type.
    Fields: path (string, optional, defaults to ".")
    Example request:  {"id":"1", "op":"list-dir", "path":"data"}
    Example response: {"id":"1", "ok":true, "value":[{"name":"base.logos","type":"file"},{"name":"lib","type":"dir"}]}

  read-file
    Returns file contents as a string.
    Fields: path (string, required)
    Example request:  {"id":"2", "op":"read-file", "path":"data/base.logos"}
    Example response: {"id":"2", "ok":true, "value":"(define not (fn (x) (if x false true)))..."}

  stat
    Returns file metadata.
    Fields: path (string, required)
    Example request:  {"id":"3", "op":"stat", "path":"data/base.logos"}
    Example response: {"id":"3", "ok":true, "value":{"name":"base.logos","size":1234,"is_dir":false,"modified":"2024-02-28T00:00:00Z"}}
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
			resp["name"] = "mod-fs"
			value = manual
		case "list-dir":
			value, errStr = handleListDir(req)
		case "read-file":
			value, errStr = handleReadFile(req)
		case "stat":
			value, errStr = handleStat(req)
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
	projectRoot = os.Getenv("LOGOS_PROJECT_ROOT")
	if projectRoot == "" {
		fmt.Fprintln(os.Stderr, "mod-fs: LOGOS_PROJECT_ROOT not set")
		os.Exit(1)
	}
	var err error
	projectRoot, err = filepath.Abs(projectRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mod-fs: resolve project root: %v\n", err)
		os.Exit(1)
	}

	sock := os.Getenv("LOGOS_MOD_SOCK")
	if sock == "" {
		sock = "/tmp/logos-mod.sock"
	}

	conn, err := net.Dial("unix", sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mod-fs: dial %s: %v\n", sock, err)
		os.Exit(1)
	}
	defer conn.Close()

	dispatch(conn)
}
