# Project: Architecture Modules & Arch Library

## Goal

Build Go AST introspection, filesystem, and git modules, then create an architecture documentation library (`arch`) that cross-references the language library (`lang`) through a shared documentation framework (`docs`).

## Phase 1: Three New Modules ✓ COMPLETE

Built mod-go, mod-fs, and mod-git. All follow the standard module protocol (unix domain socket, 4-byte length prefix + JSON). Added to `Procfile`, `Makefile`, and `.gitignore`. All smoke-tested via logos `send`.

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

## Phase 2: Shared Documentation Framework (`docs` library) — DEFERRED

A new logos library (`data/docs.logos`) providing shared functions for both `lang` and `arch` nodes. Deferred until we have more usage experience with the arch library.

Library order: `base` → `docs` → `lang` → `arch`

### What moves into `docs`

The validation and description patterns are analogous across lang and arch:
- **Validation**: lang checks test expressions against runtime behavior; arch checks code hashes against the source. Both walk a tree of nodes, collect validatable items, run checks, report results.
- **Description**: both generate wiki-style markdown at varying detail levels. Arch descriptions can include live source snippets via mod-go anchors. Cross-links between the two wikis via `(link ...)`.
- **Search**: keyword search across node trees. Already works in lang; generalize to work across both libraries.

## Phase 3: Architecture Library (`data/arch.logos`) ✓ COMPLETE

### What was built

10 concept nodes, 43 anchors, 5 interaction nodes, 1 root node. All anchors validated against live source.

**Concept nodes:**
- `arch-evaluator` — 8 anchors, 3 interaction nodes, 11 see-also links
- `arch-graph` — 7 anchors, 3 interaction nodes (shares eval↔graph), 5 see-also links
- `arch-parser` — 5 anchors, 1 interaction node (shares graph↔parser)
- `arch-types` — 7 anchors, 1 interaction node (shares eval↔types)
- `arch-core-api` — 6 anchors
- `arch-modules` — 4 anchors
- `arch-libraries` — 8 anchors (includes shared anchors from graph), 1 interaction node (shares graph↔libraries)
- `arch-step-debugger` — 6 anchors (includes shared anchors from eval), 1 interaction node (shares eval↔step-debugger)
- `arch-traces` — 5 anchors
- `arch-mcp` — 2 anchors

**Interaction nodes** (with their own anchors at boundary points):
- `arch-eval-graph-interaction` — ResolveNode, ResolveAST, currentNodeID
- `arch-eval-step-debugger-interaction` — evalState serialization/deserialization
- `arch-eval-types-interaction` — nodeToValue/valueToNode, DataBuiltins
- `arch-graph-libraries-interaction` — replay, appendLog, compact
- `arch-graph-parser-interaction` — Parse→resolveAST pipeline

**Cross-references to lang library:**
- `arch-evaluator` → lang-form-loop, lang-form-fn, lang-form-form, lang-form-if, lang-form-let, lang-concept-closures, lang-concept-iteration, lang-concept-define-time
- `arch-graph` → lang-concept-define-time, lang-concept-graph
- `arch-parser` → lang-syntax
- `arch-types` → lang-types
- `arch-step-debugger` → lang-builtin-step-eval

### Node patterns established

**Three tiers:**
- **Anchors**: code facts (file, scope, hash, commit, description). No keywords.
- **Interaction nodes**: relationship descriptions with own anchors + keywords. Link both concepts.
- **Concept nodes**: high-level narrative with anchors, interactions, keywords, see-also.

**Strong interactions** → full interaction nodes. **Weak interactions** → simple `(link ...)` in see-also.

Interaction nodes are shared: both concept nodes reference the same interaction in their `:interactions` list.

### Anchor conventions
- Identity = `file` + `scope` (AST path). Stable across edits.
- `hash`: SHA-256 of body text, trimmed leading/trailing whitespace only.
- `commit`: git SHA when hash was captured.
- Line numbers informational only, not stored in anchors.
- Only committed code anchored.

## Phase 4: Arch Functions ✓ COMPLETE

### arch-validate ✓
`arch-validate` and helper `arch-collect-anchors` in the arch library. Walks the tree (concepts + interactions, deduplicating shared interactions), collects all anchor nodes, deduplicates by symbol, batch-sends to mod-go via `validate-anchors`. Returns `{:total :valid :changed :not-found :details}`. 63 unique anchors validated. When anchors show as changed, the LLM can use mod-git to see what commits caused the change and decide whether to refresh or update.

### arch-describe ✓
Wiki-style markdown generation for arch nodes. Loop-based tree walk (no recursion). Two modes:
- `:brief` — header + keywords + compact lists of anchor/interaction names; for root, lists members as summaries
- `:full` — header + keywords + expanded anchors (name, file, scope, description) + expanded interactions + see-also links; for root, recurses into all concept members

### arch-search ✓
Keyword search across concepts, interactions, and anchors. Reuses `lang-search-matches?` for generic matching (name, description, keywords). Tree walk covers root → concepts → interactions → anchors. Deduplicates results by symbol (shared interactions appear once). Depth parameter controls result detail: 0 = name/symbol/path, 1 = + description, 2 = + file/scope (anchors) or keywords (concepts/interactions).

### docs library extraction (Phase 2) — DEFERRED
After building arch-validate/describe/search, extract shared patterns into `data/docs.logos`. The tree-walking, validation dispatch, and description generation are analogous between lang and arch.

## Decisions Log

1. Body hash: trim leading/trailing whitespace only. All Go code kept gofmt'd via Makefile.
2. Scope format: `Name` for top-level, `Receiver.Name` for methods. Uniform across all declaration kinds.
3. `list-decls` granularity: one entry per spec, not per block.
4. Arch anchors: start lean, expand as we use it.
5. Anchors carry `commit`. mod-git provides change context. Only committed code anchored.
6. Library order: `base` → `docs` → `lang` → `arch`.
7. Build `docs` library after arch exists (avoid premature abstraction).
8. Keywords on concept nodes and interaction nodes, not on anchors.
9. Strong interactions → full interaction nodes. Weak → see-also links.
10. Interaction nodes shared by both concept nodes they connect.
