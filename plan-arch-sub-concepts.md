# Plan: Add 31 Sub-Concept Nodes to Arch Library

## Context

During planning for symbol contracts, we compared what the arch library tells us vs. what a direct Go codebase investigation reveals. The arch library covers high-level subsystems well but lacks a middle layer — key data structures, internal APIs, serialization formats, and mechanisms needed to plan implementation work. For example: arch says "symbols map (name → node ID)" but doesn't document that it's a plain `map[string]string` with no struct, which matters when you need to add status metadata.

Adding 31 sub-concept nodes closes this gap, giving the LLM enough architectural context to plan implementation directly from the graph.

## What We're Adding

- **11 new anchor nodes** — code references for functions/structs not yet anchored
- **31 sub-concept nodes** — grouped by parent concept, each with anchors, keywords, description, see-also
- **8 parent concept updates** — add `:sub-concepts` field (parser and step-debugger have no sub-concepts)
- **3 function updates** — arch-collect-anchors, arch-describe, arch-search handle new `:sub-concepts` field

## Sub-Concepts by Parent

### Graph (5)
| Symbol | Name | Anchors (existing + new) |
|--------|------|--------------------------|
| `arch-graph-symbol-table` | Symbol Table | anchor-graph |
| `arch-graph-node-id-format` | Node ID Format | anchor-make-node-id |
| `arch-graph-node-fields` | Graph Node Fields | anchor-graph-node |
| `arch-graph-reference-tracking` | Reference Tracking | anchor-ref, anchor-resolve-ast |
| `arch-graph-builtin-closure-pattern` | Graph Builtin Closure Pattern | **anchor-arch-graph-builtins** (new) |

### Libraries (6)
| Symbol | Name | Anchors |
|--------|------|---------|
| `arch-libraries-log-serialization` | Log Serialization | anchor-append-log, **anchor-split-log-entries** (new), **anchor-extract-define-expr** (new) |
| `arch-libraries-manifest` | Library Manifest | anchor-read-manifest, **anchor-write-manifest** (new) |
| `arch-libraries-replay-mechanics` | Replay Mechanics | anchor-replay-file, **anchor-replay-entry-for-lib** (new) |
| `arch-libraries-compact-internals` | Compact Internals | anchor-library-compact, **anchor-compact-session** (new) |
| `arch-libraries-symbol-ownership-guards` | Symbol Ownership Guards | anchor-define |
| `arch-libraries-active-library-tracking` | Active Library Tracking | anchor-library-open, anchor-library-close |

### Evaluator (6)
| Symbol | Name | Anchors |
|--------|------|---------|
| `arch-eval-entry-points` | Evaluator Entry Points | anchor-eval-loop, anchor-call-fn-with-values, **anchor-eval-string** (new) |
| `arch-eval-locals-stack` | Locals Stack and Scopes | **anchor-lookup-local** (new) |
| `arch-eval-frame-stack` | Frame Stack | anchor-frame, anchor-eval-step |
| `arch-eval-fuel-mechanism` | Fuel Mechanism | anchor-eval-step |
| `arch-eval-node-resolution-callbacks` | Node Resolution Callbacks | anchor-evaluator |
| `arch-eval-builtin-registration` | Builtin Registration | anchor-arch-graph-builtins (new) |

### Core API (6)
| Symbol | Name | Anchors |
|--------|------|---------|
| `arch-core-api-value-serialization` | Value Serialization Boundary | anchor-value-to-go, anchor-go-to-value |
| `arch-core-api-response-format` | Response Format | anchor-handle-request |
| `arch-core-api-request-param-extraction` | Request Parameter Extraction | anchor-handle-request, anchor-handle-eval |
| `arch-core-api-actor-model` | Core Actor Model | anchor-actor-loop, anchor-core |
| `arch-core-api-message-framing` | Message Framing | anchor-write-msg, anchor-read-msg |
| `arch-core-api-manual-format` | Core Manual Format | **anchor-core-manual** (new) |

### Types (2)
| Symbol | Name | Anchors |
|--------|------|---------|
| `arch-types-value-constructors` | Value Constructors | **anchor-value-constructors** (new) |
| `arch-types-fn-value-internals` | FnValue Internals | anchor-fn-value |

### Modules (2)
| Symbol | Name | Anchors |
|--------|------|---------|
| `arch-modules-registry` | Module Registry | anchor-module-info, anchor-handle-module-connection |
| `arch-modules-communication` | Module Communication | anchor-builtin-send, anchor-write-msg, anchor-read-msg |

### Traces (2)
| Symbol | Name | Anchors |
|--------|------|---------|
| `arch-traces-lifecycle` | Trace Lifecycle | anchor-trace, anchor-append-trace |
| `arch-traces-value-format` | Trace Value Format | anchor-trace-to-value |

### MCP (2)
| Symbol | Name | Anchors |
|--------|------|---------|
| `arch-mcp-tool-registration` | MCP Tool Registration | **anchor-arch-mcp-main** (new) |
| `arch-mcp-handler-template` | MCP Handler Template | anchor-mcp-send, anchor-mcp-format-result |

## New Anchors (11)

| Symbol | File | Scope | Notes |
|--------|------|-------|-------|
| `anchor-split-log-entries` | core/graph.go | splitLogEntries | Splits log content on blank-line boundaries |
| `anchor-extract-define-expr` | core/graph.go | extractDefineExpr | Extracts expr from `(define name expr)` entry |
| `anchor-write-manifest` | core/graph.go | writeManifest | Writes library-order.txt |
| `anchor-replay-entry-for-lib` | core/graph.go | Graph.replayEntryForLib | Replays a single log entry for a library |
| `anchor-compact-session` | core/graph.go | Graph.compactSession | Rewrites session log with only live symbols |
| `anchor-eval-string` | core/eval.go | Evaluator.EvalString | Parse + eval from string |
| `anchor-lookup-local` | core/eval.go | Evaluator.lookupLocal | Searches locals stack for a binding |
| `anchor-core-manual` | core/core.go | Core.coreManual | Returns ops/builtins/forms discovery map |
| `anchor-value-constructors` | core/value.go | IntVal | Representative; covers all 14 constructor helpers |
| `anchor-arch-graph-builtins` | core/graph.go | Graph.graphBuiltins | Arch-specific copy (original in guides) |
| `anchor-arch-mcp-main` | mcp-logos/main.go | main | Arch-specific copy (original in guides) |

Note: Two anchors are copies of existing guides anchors with different names, since arch loads before guides and can't reference guides symbols.

## Function Updates

### arch-collect-anchors
Add inner loop: for each concept, walk `:sub-concepts` and collect their `:anchors`. Handle nil (concepts without sub-concepts).

### arch-describe
- **Brief mode**: add "Sub-concepts: name1, name2, ..." line for concepts that have them
- **Full mode**: push sub-concepts onto the traversal stack at `level + 1`, so they render with their own anchors/keywords/description

### arch-search
Add sub-concepts to the children list when walking a concept node. One-line change in the children assembly.

### arch-validate
No changes — relies on arch-collect-anchors which we're updating.

## Execution Phases

### Phase 1: Hash Discovery
Get hashes for 11 new anchor targets via `send "mod-go"` with `get-decl` calls. Verify all scopes resolve.

### Phase 2: Open Arch Library, Define 11 New Anchors
No dependencies between anchors — define in any order.

### Phase 3: Define 31 Sub-Concept Nodes
Depend on anchors (Phase 2) but not on each other. Define in batches by parent for clean logging.

### Phase 4: Redefine 8 Parent Concept Nodes
Add `:sub-concepts` field. Get existing source via `logos_source`, add the new field. Concepts don't depend on each other (cross-refs use `link`).

### Phase 5: Redefine Arch Root + 3 Functions
Root picks up new concept node versions. Functions gain `:sub-concepts` handling. Order: root first, then functions (functions reference root shape).

### Phase 6: Close Library, Validate, Compact
Run `arch-validate arch` — expect ~75 anchors, all valid. Then compact.

### Phase 7: Smoke Tests
- `(arch-describe arch)` — verify sub-concepts listed
- `(arch-describe arch-graph :full)` — verify sub-concepts expanded
- `(arch-search "symbol table")` — should find `arch-graph-symbol-table`
- `(arch-search "compact")` — should find `arch-libraries-compact-internals`

## Risks

| Risk | Mitigation |
|------|------------|
| Anchor scope name wrong → get-decl returns not_found | Phase 1 catches this before any library changes |
| Cross-library anchor conflict (guides owns anchor-graph-builtins) | Use distinct names: anchor-arch-graph-builtins, anchor-arch-mcp-main |
| Parent concept redefinition makes arch root stale | Intentional — root is redefined in Phase 5 |
| Fuel exhaustion on arch-describe full with 31 new nodes | Set fuel to 1M before smoke tests |
| Go code changes between phases invalidate hashes | Run all phases in single session, Phase 6 validation catches drift |

## Operation Count
~74 tool calls total: 11 hash lookups + 11 anchor defines + 31 sub-concept defines + 8 parent redefines + 1 root + 3 functions + 2 validate/compact + 5 smoke tests + library open/close.
