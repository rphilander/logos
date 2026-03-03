package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
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

// --- Field helpers ---

func getString(m map[string]any, key string) (string, bool) {
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// --- Globals ---

var projectRoot string

// --- Parsing helpers ---

func parseFile(file string) (*ast.File, *token.FileSet, []byte, error) {
	abs := filepath.Join(projectRoot, file)
	src, err := os.ReadFile(abs)
	if err != nil {
		return nil, nil, nil, err
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, abs, src, parser.ParseComments)
	if err != nil {
		return nil, nil, nil, err
	}
	return f, fset, src, nil
}

func bodyHash(src []byte, fset *token.FileSet, start, end token.Pos) string {
	s := fset.Position(start).Offset
	e := fset.Position(end).Offset
	if s < 0 || e < 0 || s >= len(src) || e > len(src) {
		return ""
	}
	body := strings.TrimSpace(string(src[s:e]))
	h := sha256.Sum256([]byte(body))
	return fmt.Sprintf("sha256:%x", h)
}

func funcSignature(src []byte, fset *token.FileSet, fd *ast.FuncDecl) string {
	// Extract from "func" keyword to the opening brace (or end if no body)
	start := fset.Position(fd.Pos()).Offset
	var end int
	if fd.Body != nil {
		end = fset.Position(fd.Body.Lbrace).Offset
	} else {
		end = fset.Position(fd.End()).Offset
	}
	if start < 0 || end < 0 || start >= len(src) || end > len(src) {
		return ""
	}
	return strings.TrimSpace(string(src[start:end]))
}

// --- Handlers ---

func handleListFiles(req map[string]any) (any, string) {
	dir, ok := getString(req, "dir")
	if !ok {
		dir = "."
	}
	abs := filepath.Join(projectRoot, dir)
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, fmt.Sprintf("read dir: %v", err)
	}
	var result []any
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
			result = append(result, e.Name())
		}
	}
	return result, ""
}

func handleListDecls(req map[string]any) (any, string) {
	file, ok := getString(req, "file")
	if !ok {
		return nil, "missing required field: file"
	}
	f, fset, _, err := parseFile(file)
	if err != nil {
		return nil, fmt.Sprintf("parse file: %v", err)
	}

	var result []any
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			entry := map[string]any{
				"kind": "func",
				"name": d.Name.Name,
				"line": fset.Position(d.Pos()).Line,
			}
			if d.Recv != nil && len(d.Recv.List) > 0 {
				entry["receiver"] = receiverName(d.Recv.List[0].Type)
			}
			result = append(result, entry)
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					result = append(result, map[string]any{
						"kind": "type",
						"name": s.Name.Name,
						"line": fset.Position(s.Pos()).Line,
					})
				case *ast.ValueSpec:
					kind := "var"
					if d.Tok == token.CONST {
						kind = "const"
					}
					for _, name := range s.Names {
						result = append(result, map[string]any{
							"kind": kind,
							"name": name.Name,
							"line": fset.Position(name.Pos()).Line,
						})
					}
				}
			}
		}
	}
	return result, ""
}

func handleGetDecl(req map[string]any) (any, string) {
	file, ok := getString(req, "file")
	if !ok {
		return nil, "missing required field: file"
	}
	name, ok := getString(req, "name")
	if !ok {
		return nil, "missing required field: name"
	}
	receiver, _ := getString(req, "receiver")

	f, fset, src, err := parseFile(file)
	if err != nil {
		return nil, fmt.Sprintf("parse file: %v", err)
	}

	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name.Name != name {
				continue
			}
			recv := ""
			if d.Recv != nil && len(d.Recv.List) > 0 {
				recv = receiverName(d.Recv.List[0].Type)
			}
			if recv != receiver {
				continue
			}
			result := map[string]any{
				"kind":      "func",
				"name":      name,
				"signature": funcSignature(src, fset, d),
				"line":      fset.Position(d.Pos()).Line,
				"end_line":  fset.Position(d.End()).Line,
			}
			if recv != "" {
				result["receiver"] = recv
			}
			if d.Doc != nil {
				result["doc"] = strings.TrimSpace(d.Doc.Text())
			}
			if d.Body != nil {
				result["body_hash"] = bodyHash(src, fset, d.Body.Lbrace, d.Body.Rbrace+1)
			}
			return result, ""

		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name.Name != name {
						continue
					}
					result := map[string]any{
						"kind":      "type",
						"name":      name,
						"line":      fset.Position(s.Pos()).Line,
						"end_line":  fset.Position(s.End()).Line,
						"body_hash": bodyHash(src, fset, s.Pos(), s.End()),
					}
					if d.Doc != nil {
						result["doc"] = strings.TrimSpace(d.Doc.Text())
					}
					return result, ""
				case *ast.ValueSpec:
					for _, n := range s.Names {
						if n.Name != name {
							continue
						}
						kind := "var"
						if d.Tok == token.CONST {
							kind = "const"
						}
						result := map[string]any{
							"kind":      kind,
							"name":      name,
							"line":      fset.Position(s.Pos()).Line,
							"end_line":  fset.Position(s.End()).Line,
							"body_hash": bodyHash(src, fset, s.Pos(), s.End()),
						}
						return result, ""
					}
				}
			}
		}
	}
	return nil, fmt.Sprintf("declaration not found: %s", name)
}

func handleResolveAnchor(req map[string]any) (any, string) {
	file, ok := getString(req, "file")
	if !ok {
		return nil, "missing required field: file"
	}
	scope, ok := getString(req, "scope")
	if !ok {
		return nil, "missing required field: scope"
	}
	expectedHash, ok := getString(req, "expected_hash")
	if !ok {
		return nil, "missing required field: expected_hash"
	}

	name, receiver := parseScope(scope)

	lookupReq := map[string]any{"file": file, "name": name}
	if receiver != "" {
		lookupReq["receiver"] = receiver
	}

	result, errStr := handleGetDecl(lookupReq)
	if errStr != "" {
		return map[string]any{"status": "not_found"}, ""
	}

	m := result.(map[string]any)
	currentHash, _ := m["body_hash"].(string)
	line, _ := m["line"].(int)
	endLine, _ := m["end_line"].(int)
	if currentHash == expectedHash {
		return map[string]any{"status": "valid", "line": line, "end_line": endLine}, ""
	}
	return map[string]any{"status": "changed", "current_hash": currentHash, "line": line, "end_line": endLine}, ""
}

func handleValidateAnchors(req map[string]any) (any, string) {
	anchorsRaw, ok := req["anchors"]
	if !ok {
		return nil, "missing required field: anchors"
	}
	anchors, ok := anchorsRaw.([]any)
	if !ok {
		return nil, "anchors must be a list"
	}

	var results []any
	for _, a := range anchors {
		anchor, ok := a.(map[string]any)
		if !ok {
			results = append(results, map[string]any{"status": "not_found", "error": "invalid anchor"})
			continue
		}
		result, errStr := handleResolveAnchor(anchor)
		if errStr != "" {
			results = append(results, map[string]any{"status": "not_found", "error": errStr})
		} else {
			results = append(results, result)
		}
	}
	return results, ""
}

// --- Helpers ---

func receiverName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			return id.Name
		}
	}
	return ""
}

func parseScope(scope string) (name, receiver string) {
	if i := strings.LastIndex(scope, "."); i >= 0 {
		return scope[i+1:], scope[:i]
	}
	return scope, ""
}

// --- Manual ---

const manual = `mod-go — Logos Go AST introspection module

Introspects Go source files relative to LOGOS_PROJECT_ROOT.

Operations:

  list-files
    Lists .go files in a directory.
    Fields: dir (string, optional, defaults to ".")
    Example request:  {"id":"1", "op":"list-files", "dir":"core"}
    Example response: {"id":"1", "ok":true, "value":["core.go","eval.go","parser.go",...]}

  list-decls
    Lists top-level declarations in a Go file. One entry per spec.
    Fields: file (string, required — relative path)
    Example request:  {"id":"2", "op":"list-decls", "file":"core/eval.go"}
    Example response: {"id":"2", "ok":true, "value":[{"kind":"func","name":"evalLoop","receiver":"Evaluator","line":42},...]}}

  get-decl
    Returns details of a specific declaration including body hash.
    Fields: file (string, required), name (string, required), receiver (string, optional — for methods)
    Example request:  {"id":"3", "op":"get-decl", "file":"core/eval.go", "name":"evalLoop", "receiver":"Evaluator"}
    Example response: {"id":"3", "ok":true, "value":{"kind":"func","name":"evalLoop","receiver":"Evaluator","signature":"func (e *Evaluator) evalLoop() (Value, error)","doc":"...","line":42,"end_line":180,"body_hash":"sha256:abc123..."}}

  resolve-anchor
    Checks if a declaration still matches an expected hash.
    Fields: file (string, required), scope (string, required — "Name" or "Receiver.Name"), expected_hash (string, required)
    Example request:  {"id":"4", "op":"resolve-anchor", "file":"core/eval.go", "scope":"Evaluator.evalLoop", "expected_hash":"sha256:abc123..."}
    Example response: {"id":"4", "ok":true, "value":{"status":"valid"}} or {"status":"changed","current_hash":"sha256:def456..."} or {"status":"not_found"}

  validate-anchors
    Batch version of resolve-anchor.
    Fields: anchors (list, required — each with file, scope, expected_hash)
    Example request:  {"id":"5", "op":"validate-anchors", "anchors":[{"file":"core/eval.go","scope":"Evaluator.evalLoop","expected_hash":"sha256:abc123..."}]}
    Example response: {"id":"5", "ok":true, "value":[{"status":"valid"}]}
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
			resp["name"] = "mod-go"
			value = manual
		case "list-files":
			value, errStr = handleListFiles(req)
		case "list-decls":
			value, errStr = handleListDecls(req)
		case "get-decl":
			value, errStr = handleGetDecl(req)
		case "resolve-anchor":
			value, errStr = handleResolveAnchor(req)
		case "validate-anchors":
			value, errStr = handleValidateAnchors(req)
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
		fmt.Fprintln(os.Stderr, "mod-go: LOGOS_PROJECT_ROOT not set")
		os.Exit(1)
	}
	var err error
	projectRoot, err = filepath.Abs(projectRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mod-go: resolve project root: %v\n", err)
		os.Exit(1)
	}

	sock := os.Getenv("LOGOS_MOD_SOCK")
	if sock == "" {
		sock = "/tmp/logos-mod.sock"
	}

	conn, err := net.Dial("unix", sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mod-go: dial %s: %v\n", sock, err)
		os.Exit(1)
	}
	defer conn.Close()

	dispatch(conn)
}
