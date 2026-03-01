# Lang Library — Session Notes

## What Was Built

A `lang` library (`data/lang.logos`) containing a self-validating language reference for logos types.

### Structure
- **`lang`** root node — top-level entry point for the whole reference
- **`lang-types`** — 13 type nodes + 2 concept nodes, with `members` and `concepts` as direct graph references
- **68 example/test nodes** (`lang-ex-*` for examples, `lang-test-*` for tests)
- **Functions**: `lang-validate`, `lang-describe`, `lang-search`, `lang-run-test`, `lang-collect-tests`

### Conventions
- Each reference node has: `"symbol"` (own symbol name), `"name"` (short label), `"description"` (explanation), `"keywords"` (list of lowercase search terms), `"examples"` (direct refs to example nodes), `"tests"` (direct refs to test nodes)
- Example/test nodes have: `"name"` (short label), `"expr"` (string that evaluates to true/false)
- Top-level nodes have `"members"` and `"concepts"` as direct graph references to child nodes
- `(lang-validate node)` runs all examples + tests recursively, returns `{total, passed, failed, failures}`
- `(lang-describe node)` returns brief markdown; `(lang-describe node :full)` returns full recursive markdown
- `(lang-search query)` searches names, descriptions, and keywords. Optional depth (0-3) controls result detail. Optional roots list scopes the search: `(lang-search "lambda" 2 (list 'lang-types))`

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

## Still To Build (lang library)

- **Core forms** section: `if`, `let`, `letrec`, `do`, `fn`, `form`, `quote`, `apply`, `sort-by`
- **Builtins** section: all 31, grouped by category
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

## Enhancements (after lang library is complete)

- **Case-insensitive search**: `contains?` is case-sensitive. `lang-search` relies on lowercase keywords to partially cover this, but searching "arithmetic" won't match description text "Arithmetic builtins...". Fix: add a `lower-case` string builtin in Go, then lowercase both query and fields in `lang-search-matches?`.
- **`eval` or `resolve` builtin**: `lang-eval-symbol` uses `step-eval` to go from symbol name to value — heavyweight. A simple builtin would make the graph-as-data-store pattern much more ergonomic and reduce fuel cost of search/validate.
- **Variadic `and`/`or`**: binary forms require nesting. Rest params + recursive expansion would fix this.
- **Generalize `lang-describe`**: not specific to lang nodes. Any node following the name/description/examples/tests/members convention could use it. Maybe belongs in a shared utility space.
- **Fuel**: `lang-validate lang-types` needs ~500K fuel for 68 tests via step-eval. Will grow as more sections are added.
