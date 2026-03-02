# Lang Library — Session Notes

## What Was Built

A `lang` library (`data/lang.logos`) containing a self-validating language reference for logos.

### Structure
- **`lang`** root node — top-level entry point for the whole reference
- **`lang-types`** — 13 type nodes + 2 concept nodes, with `members` and `concepts` as direct graph references
- **`lang-forms`** — 9 core form nodes, with `members` as direct graph references
- **`lang-builtins`** — 31 builtins across 10 category nodes, with `members` as direct graph references
- **Example/test nodes** (`lang-ex-*` for examples, `lang-test-*` for tests)
- **Functions**: `lang-validate`, `lang-describe`, `lang-search`, `lang-run-test`, `lang-collect-tests`

### Conventions
- Each reference node has: `"symbol"` (own symbol name), `"name"` (short label), `"description"` (explanation), `"keywords"` (list of lowercase search terms), `"examples"` (direct refs to example nodes), `"tests"` (direct refs to test nodes)
- Optional `"see-also"` field: list of direct graph references to semantically related nodes (e.g., `lang-form-fn` → `lang-type-fn`). Makes cross-references explicit and visible to graph introspection.
- Example/test nodes have: `"name"` (short label), `"expr"` (string that evaluates to true/false)
- Top-level nodes have `"members"` and `"concepts"` as direct graph references to child nodes
- `(lang-validate node)` runs all examples + tests recursively, returns `{total, passed, failed, failures}`
- `(lang-describe node)` returns brief markdown; `(lang-describe node :full)` returns full recursive markdown
- `(lang-search query)` searches names, descriptions, and keywords. Optional depth (0-3) controls result detail. Optional roots list scopes the search: `(lang-search "lambda" 0 (list lang-forms))`

## Issues Found

### 1. Keywords and strings are equivalent as map keys
`dict` uses Go `map[string]Value` internally. Keywords are coerced to their string form when used as map keys. So `:a` and `"a"` are the same key — `(put (dict :a 1) "a" 2)` overwrites the value. This is probably fine but worth documenting clearly. It means keywords are syntactic sugar for map access, not a distinct key namespace.

### 2. `and`/`or` only take 2 arguments
They are binary forms. `(and a b c)` fails with "expected 2 args, got 3". Must nest: `(and a (and b c))`. This is a real ergonomic issue — variadic `and`/`or` would be much more natural. Could be fixed by changing the form definitions to use rest params and recursive expansion.

### 3. Integer division truncates (doesn't promote to float)
`(div 7 2)` returns `3` (int), not `3.5` (float). Need a float operand for float division: `(div 7.0 2)`. This is standard Go behavior but surprising if you expect automatic promotion. The type description was updated to document this.

### 4. No `eval` builtin — symbol name to value requires step-eval
To go from a symbol name string to its evaluated value within logos, we had to use:
```
(fn (name) (get (run-to-end (step-eval name)) :result))
```
This works but is heavyweight — spinning up the step evaluator just to dereference a symbol. A simple `eval` or `resolve` builtin that takes a string and returns the value would make the graph-as-data-store pattern much more ergonomic. This came up because reference nodes store member/example lists as symbol name strings, and functions need to look up those values.

### 5. `let` takes exactly one body expression
`(let (x 1) expr1 expr2)` fails. Need `(let (x 1) (do expr1 expr2))`. This is by design but tripped up test writing.

### 6. `if` requires all three arguments
`(if false :yes)` fails with "expected 3 args (cond then else), got 2". Unlike many Lisps, the else branch is not optional. Use `nil` explicitly: `(if false :yes nil)`. Discovered via self-validation during the forms section build.

## Still To Build (lang library)

- ~~**Core forms** section~~ ✓ — 9 forms, 38 tests, all passing
- **Builtins** section — 31 builtins defined across 10 categories, **awaiting final validation** (blocked by stack overflow — see `resolver-stack-overflow.md`)
- **Syntax** section: S-expressions, quote reader macro, keyword syntax, rest params
- **Concepts** section: closures, recursion, TCO, scoping, graph resolution

## Still To Build (other libraries)

- **arch** library — Go codebase, core API, MCP tools, module protocol, sockets
- **libs** library — what's in base, debug, web, db

## Design Decision: Direct References, Not Strings

An early version stored member/example/test lists as symbol name strings (e.g., `"lang-type-int"`) and resolved them at runtime via `lang-eval-symbol` (which used step-eval internally). This was refactored to use direct graph references instead:

```
;; Anti-pattern — opaque to the graph:
(dict "members" (list "lang-type-int" "lang-type-float"))

;; Correct — graph tracks dependencies:
(dict "members" (list lang-type-int lang-type-float))
```

**Why this matters:**
- `ref-by`, `dependents`, `downstream` see the relationships
- `refresh-all` propagates changes through the tree
- No `step-eval` hack needed — less fuel, less code
- The graph is honest about what depends on what

**Tradeoff:** After redefining a child node, you must `refresh-all` to update parents. With strings, parents always resolved to current. The refresh discipline is a feature — it makes change propagation explicit.

**Future:** Refreshes could become more ergonomic. For instance, some symbols could auto-refresh with test functions as guard rails — redefine a node, its dependents refresh, and associated tests run automatically as validation.

## Observations from Building the Web UI

### 1. Refresh discipline is real friction
Redefined `lang-ui-page`, but the browser showed the old version because `on-request` held a stale node-ref. Had to manually `refresh-all`. The auto-refresh with test guards idea (from the design decision section) would have caught this. This is the highest-priority ergonomic improvement.

### 2. String→node lookup problem resurfaced at the API boundary
The HTTP API receives query parameters as strings (e.g., `"lang-type-int"`). We need the node value. Built `lang-node-index` to walk the tree and create a name→value map — another workaround for the missing `eval`/`resolve` builtin. It also rebuilds the index on every request. An `eval` builtin would eliminate this entirely.

### 3. `split` vs `split-once` argument order is inconsistent
`split-once` takes `(delimiter, string)`, `split` takes `(string, delimiter)`. Tripped up the query string parser. Should be documented in the lang library builtins section and possibly fixed for consistency.

### 4. Library migration is manual
Moving symbols from session to library required: close library → delete each from session → reopen library → redefine each. A `move-to-library` core operation would streamline this common workflow.

### 5. `on-request` is a single global callback handler
All module callbacks (mod-http-server, mod-mcp-server) dispatch to the same `on-request` function. The callback message includes `"module"`, so we can dispatch on that, but the single-handler design may need rethinking as more modules use callbacks. Consider per-module handlers or a callback routing layer.

### 6. `define` should report stale dependents
When you redefine a symbol, the response is just `{name, node_id}`. It should also report which symbols still reference the now-superseded node — a staleness reminder. The graph already tracks `ref-by`, so this is cheap to compute. Example: redefine `lang-ui-page` → response includes `"stale_dependents": ["on-request"]`. This turns a hidden footgun into an explicit prompt.

### 7. The vision works
Redefine a function → refresh → live website updates. No build, no deploy. The loop from "I want search" to "search is live in the browser" happened in one conversation. Zero source files written — only the library log.

## Observations from Building Core Forms

### 1. Parallel defines with dependencies can cause stale references
When redefining `lang-ex-if-no-else` and `lang-form-if` in parallel (same message, two MCP calls), the ordering is not guaranteed. If `lang-form-if` was processed first, its expression resolved `lang-ex-if-no-else` to the **old** node because the new one hadn't been committed yet. The subsequent `refresh-all` with both as targets didn't fix it — `lang-form-if` was treated as a target ("already current") rather than a dependent needing re-resolution.

**Rule:** When redefining nodes that reference each other, define the dependency first, then the dependent. Or: define both in any order, then refresh the leaf target only — the dependent will be re-resolved as part of the refresh chain.

### 2. `refresh-all` targets vs dependents
A target in `refresh-all` means "I am already correct — re-resolve things that depend on me." If a symbol is listed as a target but is actually stale, it won't be fixed. To fix it, it must appear as a **dependent** of some target. This distinction matters when multiple related symbols change at once.

### 3. `see-also` cross-references
Established a convention for explicit semantic links between related nodes. Three form→type links: `lang-form-fn` → `lang-type-fn`, `lang-form-form` → `lang-type-form`, `lang-form-quote` → `lang-type-symbol`. These are real graph references visible to `ref-by`, `dependents`, `downstream`.

## Blocker: Resolver Stack Overflow

Evaluating `lang-builtins` (or `lang` which includes it) causes a **Go stack overflow** — 1GB goroutine stack exhausted, 1.4M frames elided. The root cause is infinite recursion in the resolver's eval-on-access pattern. Full analysis in `resolver-stack-overflow.md`. This blocks `(lang-validate lang-builtins)` and must be fixed before the builtins section is complete.

**Workaround options** (if fixing the root cause is deferred):
- Remove `see-also` node-refs from builtin nodes (or replace with inert strings)
- Validate each category independently: `(lang-validate lang-builtins-data)` etc.

## Enhancements (after lang library is complete)

- **Case-insensitive search**: `contains?` is case-sensitive. `lang-search` relies on lowercase keywords to partially cover this, but searching "arithmetic" won't match description text "Arithmetic builtins...". Fix: add a `lower-case` string builtin in Go, then lowercase both query and fields in `lang-search-matches?`.
- **`eval` or `resolve` builtin**: `lang-eval-symbol` uses `step-eval` to go from symbol name to value — heavyweight. A simple builtin would make the graph-as-data-store pattern much more ergonomic and reduce fuel cost of search/validate. Also needed at API boundaries (HTTP query params → node values).
- **Variadic `and`/`or`**: binary forms require nesting. Rest params + recursive expansion would fix this.
- **Generalize `lang-describe`**: not specific to lang nodes. Any node following the name/description/examples/tests/members convention could use it. Maybe belongs in a shared utility space.
- **Fuel**: `lang-validate lang-types` needs ~500K fuel for 68 tests via step-eval. Will grow as more sections are added.
- **Stale dependent reporting on define**: include `ref-by` results for the superseded node in the define response, so the caller knows what needs refreshing.
- **`move-to-library` operation**: streamline the session → library migration workflow.
- **Per-module callback routing**: allow registering handlers per module instead of a single `on-request`.
