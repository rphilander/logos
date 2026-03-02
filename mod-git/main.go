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

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
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

func getInt(m map[string]any, key string) (int, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	f, ok := v.(float64)
	if !ok {
		return 0, false
	}
	return int(f), true
}

// --- Globals ---

var repo *git.Repository

// --- Handlers ---

func handleCurrentCommit(req map[string]any) (any, string) {
	ref, err := repo.Head()
	if err != nil {
		return nil, fmt.Sprintf("get HEAD: %v", err)
	}
	commit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		return nil, fmt.Sprintf("get commit: %v", err)
	}
	return map[string]any{
		"sha":     commit.Hash.String(),
		"message": strings.TrimSpace(commit.Message),
	}, ""
}

func handleLog(req map[string]any) (any, string) {
	file, _ := getString(req, "file")
	since, hasSince := getString(req, "since")
	limit, hasLimit := getInt(req, "limit")
	if !hasLimit {
		limit = 50
	}

	logOpts := &git.LogOptions{Order: git.LogOrderCommitterTime}
	if file != "" {
		logOpts.FileName = &file
	}

	iter, err := repo.Log(logOpts)
	if err != nil {
		return nil, fmt.Sprintf("git log: %v", err)
	}
	defer iter.Close()

	var sinceHash plumbing.Hash
	if hasSince {
		sinceHash = plumbing.NewHash(since)
	}

	results := make([]any, 0)
	err = iter.ForEach(func(c *object.Commit) error {
		if hasSince && c.Hash == sinceHash {
			return fmt.Errorf("stop")
		}
		if len(results) >= limit {
			return fmt.Errorf("stop")
		}
		results = append(results, map[string]any{
			"sha":     c.Hash.String(),
			"message": strings.TrimSpace(c.Message),
			"author":  c.Author.Name,
			"date":    c.Author.When.UTC().Format("2006-01-02T15:04:05Z"),
		})
		return nil
	})
	// "stop" is our sentinel, not a real error
	if err != nil && err.Error() != "stop" {
		return nil, fmt.Sprintf("iterate log: %v", err)
	}

	return results, ""
}

func handleShow(req map[string]any) (any, string) {
	sha, ok := getString(req, "commit")
	if !ok {
		return nil, "missing required field: commit"
	}

	hash := plumbing.NewHash(sha)
	commit, err := repo.CommitObject(hash)
	if err != nil {
		return nil, fmt.Sprintf("get commit: %v", err)
	}

	// Get changed files by diffing with parent
	var files []any
	stats, err := commit.Stats()
	if err == nil {
		for _, s := range stats {
			files = append(files, map[string]any{
				"name":      s.Name,
				"additions": s.Addition,
				"deletions": s.Deletion,
			})
		}
	}

	return map[string]any{
		"sha":     commit.Hash.String(),
		"message": strings.TrimSpace(commit.Message),
		"author":  commit.Author.Name,
		"date":    commit.Author.When.UTC().Format("2006-01-02T15:04:05Z"),
		"files":   files,
	}, ""
}

func handleDiffFile(req map[string]any) (any, string) {
	file, ok := getString(req, "file")
	if !ok {
		return nil, "missing required field: file"
	}
	fromSha, ok := getString(req, "from")
	if !ok {
		return nil, "missing required field: from"
	}
	toSha, ok := getString(req, "to")
	if !ok {
		return nil, "missing required field: to"
	}

	fromCommit, err := repo.CommitObject(plumbing.NewHash(fromSha))
	if err != nil {
		return nil, fmt.Sprintf("get from commit: %v", err)
	}
	toCommit, err := repo.CommitObject(plumbing.NewHash(toSha))
	if err != nil {
		return nil, fmt.Sprintf("get to commit: %v", err)
	}

	fromTree, err := fromCommit.Tree()
	if err != nil {
		return nil, fmt.Sprintf("get from tree: %v", err)
	}
	toTree, err := toCommit.Tree()
	if err != nil {
		return nil, fmt.Sprintf("get to tree: %v", err)
	}

	// Get file contents at each commit
	fromContent := ""
	if f, err := fromTree.File(file); err == nil {
		fromContent, _ = f.Contents()
	}
	toContent := ""
	if f, err := toTree.File(file); err == nil {
		toContent, _ = f.Contents()
	}

	if fromContent == "" && toContent == "" {
		return nil, fmt.Sprintf("file %s not found in either commit", file)
	}

	return map[string]any{
		"file": file,
		"from": map[string]any{"sha": fromSha, "content": fromContent},
		"to":   map[string]any{"sha": toSha, "content": toContent},
	}, ""
}

// --- Manual ---

const manual = `mod-git — Logos git introspection module

Operates on the git repository at LOGOS_PROJECT_ROOT.

Operations:

  current-commit
    Returns HEAD sha and commit message.
    Fields: (none)
    Example request:  {"id":"1", "op":"current-commit"}
    Example response: {"id":"1", "ok":true, "value":{"sha":"abc123...","message":"add mod-fs"}}

  log
    Returns commits, optionally filtered by file and/or since a given commit.
    Fields: file (string, optional), since (string, optional — stop before this sha), limit (int, optional, default 50)
    Example request:  {"id":"2", "op":"log", "file":"core/eval.go", "limit":10}
    Example response: {"id":"2", "ok":true, "value":[{"sha":"...","message":"...","author":"...","date":"..."},...]}

  show
    Returns commit details including changed files with addition/deletion counts.
    Fields: commit (string, required — sha)
    Example request:  {"id":"3", "op":"show", "commit":"abc123"}
    Example response: {"id":"3", "ok":true, "value":{"sha":"...","message":"...","author":"...","date":"...","files":[{"name":"...","additions":10,"deletions":3}]}}

  diff-file
    Returns file contents at two commits for comparison.
    Fields: file (string, required), from (string, required — sha), to (string, required — sha)
    Example request:  {"id":"4", "op":"diff-file", "file":"core/eval.go", "from":"abc123", "to":"def456"}
    Example response: {"id":"4", "ok":true, "value":{"file":"...","from":{"sha":"...","content":"..."},"to":{"sha":"...","content":"..."}}}
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
			resp["name"] = "mod-git"
			value = manual
		case "current-commit":
			value, errStr = handleCurrentCommit(req)
		case "log":
			value, errStr = handleLog(req)
		case "show":
			value, errStr = handleShow(req)
		case "diff-file":
			value, errStr = handleDiffFile(req)
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
	projectRoot := os.Getenv("LOGOS_PROJECT_ROOT")
	if projectRoot == "" {
		fmt.Fprintln(os.Stderr, "mod-git: LOGOS_PROJECT_ROOT not set")
		os.Exit(1)
	}
	var err error
	projectRoot, err = filepath.Abs(projectRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mod-git: resolve project root: %v\n", err)
		os.Exit(1)
	}

	repo, err = git.PlainOpen(projectRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mod-git: open repo at %s: %v\n", projectRoot, err)
		os.Exit(1)
	}

	sock := os.Getenv("LOGOS_MOD_SOCK")
	if sock == "" {
		sock = "/tmp/logos-mod.sock"
	}

	conn, err := net.Dial("unix", sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mod-git: dial %s: %v\n", sock, err)
		os.Exit(1)
	}
	defer conn.Close()

	dispatch(conn)
}
