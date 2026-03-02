# Project: Architecture Modules & Arch Library

## Goal

Build Go AST introspection, filesystem, and git modules, then create an architecture documentation library (`arch`) that cross-references the language library (`lang`) through a shared documentation framework (`docs`).

## Phase 1: Three New Modules

Build mod-go, mod-fs, and mod-git. All follow the standard module protocol (unix domain socket, 4-byte length prefix + JSON). Use `mod-time/main.go` as boilerplate template. Add each to `Procfile` and `Makefile`.

### mod-go — Go AST introspection

Operations:

- **`list-files`** — `{"op": "list-files", "dir": "core"}` → list `.go` files in a directory (relative to project root). Returns `["core.go", "eval.go", ...]`.
- **`list-decls`** — `{"op": "list-decls", "file": "core/eval.go"}` → list top-level declarations, one entry per spec (not per block). Returns list of `{"kind": "func|type|const|var", "name": "evalLoop", "receiver": "Evaluator", "line": 42}`.
- **`get-decl`** — `{"op": "get-decl", "file": "core/eval.go", "name": "evalLoop", "receiver": "Evaluator"}` → returns `{"kind": "func", "name": "evalLoop", "receiver": "Evaluator", "signature": "func (e *Evaluator) evalLoop() (Value, error)", "doc": "...", "line": 42, "end_line": 180, "body_hash": "sha256:..."}`. Line numbers are informational, not part of anchor identity.
- **`resolve-anchor`** — `{"op": "resolve-anchor", "file": "core/eval.go", "scope": "Evaluator.evalLoop", "expected_hash": "sha256:..."}` → `{"status": "valid"}` or `{"status": "changed", "current_hash": "sha256:..."}` or `{"status": "not_found"}`. Scope format: `Name` for top-level, `Receiver.Name` for methods.
- **`validate-anchors`** — `{"op": "validate-anchors", "anchors": [...]}` → batch version of resolve-anchor.

Implementation notes:
- Use `go/parser` and `go/ast` from standard library
- Project root via `LOGOS_PROJECT_ROOT` env var (default: working directory)
- Body hash: SHA-256 of function body text, trimmed leading/trailing whitespace only. All code kept gofmt'd.
- `list-decls`: walk `ast.File.Decls` — `*ast.FuncDecl` for functions/methods, `*ast.GenDecl` for types/consts/vars. One entry per spec inside grouped declarations.

### mod-fs — Filesystem operations

Operations:
- **`list-dir`** — `{"op": "list-dir", "path": "core"}` → list directory entries with type (file/dir).
- **`read-file`** — `{"op": "read-file", "path": "core/eval.go"}` → file contents as string.
- **`stat`** — `{"op": "stat", "path": "core/eval.go"}` → file metadata (size, modified time, is_dir).

Paths relative to project root (`LOGOS_PROJECT_ROOT`).

### mod-git — Git introspection

Uses [go-git](https://github.com/go-git/go-git) (pure Go, no C deps).

Operations:
- **`current-commit`** — `{"op": "current-commit"}` → HEAD sha + message.
- **`log`** — `{"op": "log", "file": "core/eval.go", "since": "abc123"}` → commits that touched a file, optionally since a given commit.
- **`show`** — `{"op": "show", "commit": "abc123"}` → commit message + list of changed files.
- **`diff-file`** — `{"op": "diff-file", "file": "core/eval.go", "from": "abc123", "to": "def456"}` → diff of a file between two commits.

## Phase 2: Shared Documentation Framework (`docs` library)

A new logos library (`data/docs.logos`) providing shared functions for both `lang` and `arch` nodes.

Library order: `base` → `docs` → `lang` → `arch`

### What moves into `docs`

The validation and description patterns are analogous across lang and arch:
- **Validation**: lang checks test expressions against runtime behavior; arch checks code hashes against the source. Both walk a tree of nodes, collect validatable items, run checks, report results.
- **Description**: both generate wiki-style markdown at varying detail levels. Arch descriptions can include live source snippets via mod-go anchors. Cross-links between the two wikis via `(link ...)`.
- **Search**: keyword search across node trees. Already works in lang; generalize to work across both libraries.

### Design

Identify the shared patterns after building the arch library (Phase 3), then extract common functions into `docs`. This avoids premature abstraction — build arch first, see what's actually shared, then factor it out.

## Phase 3: Architecture Library (`data/arch.logos`)

### Structure

```
arch
├── arch-core-api        — Core API ops
├── arch-evaluator       — Frame-based iterative evaluator
├── arch-parser          — S-expression parser
├── arch-graph           — Graph system (Define, resolveAST, refresh-all)
├── arch-types           — Value type system
├── arch-modules         — Module protocol, sockets, module management
├── arch-libraries       — Library persistence
├── arch-step-debugger   — Step evaluator architecture
├── arch-traces          — Trace system
└── arch-mcp             — MCP server tools
```

### Node pattern

Each arch node follows the lang convention but adds an `anchors` field:

```
(dict :symbol "arch-evaluator"
      :name "Evaluator"
      :description "..."
      :keywords (list ...)
      :anchors (list anchor-eval-loop anchor-frame-types ...)
      :see-also (list (link 'arch-graph) (link 'lang-form-loop)))
```

### Anchor nodes

```
(dict :symbol "anchor-eval-loop"
      :name "evalLoop"
      :file "core/eval.go"
      :scope "Evaluator.evalLoop"
      :hash "sha256:..."
      :commit "abc123..."
      :description "Main evaluation loop — pops frames from stack and dispatches by frame type")
```

- Anchor identity = `file` + `scope` (AST path). Stable across edits.
- `hash` detects body changes. Trim leading/trailing whitespace only.
- `commit` records when the hash was captured. mod-git provides context for what changed since.
- Line numbers are informational (returned by mod-go queries) but not stored in anchors.
- Only committed code can be anchored.

### Anchor scope

Start lean — a few key constructs per arch node (3-5 anchors). Expand as we use the system. Iteration is the point.

### Validation

`arch-validate` walks the tree, collects anchor nodes, batch-sends to mod-go via `validate-anchors`. Reports valid/changed/not_found. When changed, the LLM can use mod-git to see what commits caused the change and decide whether to refresh the anchor or update the description.

### Cross-references

Arch links to lang via `(link ...)`. `arch-evaluator` → `lang-form-loop`, `lang-concept-iteration`, etc. Search functions work across both libraries. This creates a unified knowledge base covering language semantics and implementation.

## Decisions Log

1. Body hash: trim leading/trailing whitespace only. All Go code kept gofmt'd via Makefile.
2. Scope format: `Name` for top-level, `Receiver.Name` for methods. Uniform across all declaration kinds.
3. `list-decls` granularity: one entry per spec, not per block.
4. Arch anchors: start lean, expand as we use it.
5. Anchors carry `commit`. mod-git provides change context. Only committed code anchored.
6. Library order: `base` → `docs` → `lang` → `arch`.
7. Build `docs` library after arch exists (avoid premature abstraction).
