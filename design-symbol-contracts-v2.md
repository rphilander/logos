# Design: Symbol Contracts v2

## Problem

Refresh-all is the biggest source of friction in logos development. When a symbol is redefined, all its dependents may become stale. The LLM must manually call refresh-all, often multiple times, and verify correctness afterward. Multi-level chains are particularly fragile.

## Core Idea

Nodes gain a **tests** field. Tests are a **gate** at node creation — the operation fails if tests don't pass. When a dependency changes, the runtime automatically re-resolves dependents and runs their tests. Passing tests trigger auto-refresh; failing tests mark the symbol red. Red stops propagation.

## Node Model

A node is: **expression + tests**. Both are immutable. Tests are a list of expressions that must each evaluate to truthy.

```
GraphNode {
    ID      string
    Expr    *Node       // resolved AST
    Refs    []Ref       // dependencies
    Source  string      // original source
    Tests   []*Node     // resolved test ASTs (optional)
}
```

There is no separate concept of "symbol metadata" for tests. Tests live on the node. The contract for a symbol is the tests on its current node. History of contracts = history of nodes.

## Define vs Refine

**`define`** creates a new symbol from scratch. Blank slate — expression and optionally tests.

**`refine`** creates a new node for an existing symbol by transforming the existing node. Unmentioned fields carry forward from the previous node.

- `refine name :expr new-expr` — change code, carry forward tests
- `refine name :add-test test-expr` — add a test, carry forward expression and existing tests
- `refine name :remove-test "label"` — remove one test by label
- `refine name :expr new-expr :add-test test-expr` — change both at once

`refine` is "immutable data structure update" semantics applied to nodes. It's also the seed for future AST-level patching — structural transformations on the expression.

## Test Gate

When a new node is created (via `define` or `refine`), the tests must pass right then. If any test fails, the operation fails — no node is created, no symbol is updated, no log entry written.

This means: **a node always satisfies its own contract at the moment of creation.** You can never have a node that doesn't pass its tests.

## Green and Red

Green and red are about **freshness**, not correctness.

- **Green**: symbol points to the most recent node for all its dependencies, and its tests pass.
- **Red**: symbol points to an old version of at least one dependency. The node was valid when created (tests passed then), but the world has moved on.

Red does not mean "broken." It means "pinned to an older version of the world, intentionally preserved for stability." The LLM must reconcile.

## Cascade (Auto-Refresh)

When A is redefined (and A's tests pass):

1. Find all symbols that depend on A (via Refs/BFS).
2. For each dependent B:
   - If B has no contract → mark B as stale (current behavior). **Stop this branch.** No contract = no auto-refresh.
   - If B has a contract → re-resolve B with new A, run B's tests:
     - **Tests pass** → B is auto-refreshed (new node created, green). Continue to B's dependents.
     - **Tests fail** → B stays on its old node. B is marked **red**. **Stop this branch.**

Red is a circuit breaker. The cascade goes as deep as tests keep passing. First failure stops propagation down that branch.

## No Propagation When Contract Changes

If a symbol's expression **and** its tests are changed in the same operation (e.g., `refine name :expr new-expr :add-test new-test`), auto-refresh does **not** propagate to dependents. The LLM owns the situation — they changed the rules and the code simultaneously, so the system can't distinguish "updated test to match new behavior" from "loosened test to hide a problem."

If only the tests change (expression unchanged), propagation **does** happen. A new node is created (same expression, new tests), and dependents are refreshed. This is technically churn (the expression didn't change), but it preserves immutability and simplicity. Optimize node representation later.

If only the expression changes, propagation happens as described in the cascade section.

## What Tests Can Reference

Tests can only reference:

1. **Core forms** — if, let, do, fn, form, quote, apply, sort-by, loop, recur
2. **Builtins** — eq, list, dict, get, add, len, assert, etc.
3. **Base library symbols** — map, filter, fold, not, and, or, type predicates, etc.
4. **The symbol under test** — implicitly available; you need to call the thing you're testing

Nothing else. Tests cannot reference arbitrary graph symbols.

This is enforced at parse/resolve time. When a test expression is parsed, every symbol must resolve to a builtin, a base library symbol, or the symbol under test. Anything else is a define/refine error.

### Rationale

If tests could reference arbitrary graph symbols, then tests would have dependencies, those dependencies could change, and you'd need contracts for contracts. By restricting tests to builtins + base, tests have no dependency graph of their own. Base is the bedrock — it changes rarely and deliberately. Builtins never change (they're Go code).

### Base Library's Privileged Position

Base completes the language. Builtins are the primitives (Go code), base is the standard library (logos code), and together they form the trusted foundation. Tests are assertions written in "the language" applied to the thing being tested.

Base library symbols' own tests can only use builtins + other base symbols. This is natural since base is the lowest layer.

## Persistence

Tests are part of the node and logged with the define/refine entry. The log format extends to include test expressions. On replay, tests are parsed and resolved along with the main expression.

No separate test log entries. No separate test storage. Tests are just another field on the node.

## API Surface

### Core Operations

- **`define`** — unchanged semantics, gains optional tests field
- **`refine`** — new op: transform existing node (change expr, add/remove test)
- **`symbol-status`** — query a symbol's green/red/untested status
- **`red-symbols`** — list all red symbols (LLM work list)
- **`green-symbols`** — list all green symbols

### MCP Tools

- `logos_define` — gains optional tests parameter
- `logos_refine` — new tool
- `logos_symbol_status` — new tool
- `logos_red_symbols` / `logos_green_symbols` — new tools

## Test Shape

Each test is a labeled expression:

```
(dict :name "adds one" :expr "(eq (inc 5) 6)")
```

The `:name` is for identification (used by `:remove-test` in refine). The `:expr` is an expression string that is parsed, resolved (restricted scope), and evaluated. It must return truthy.

A symbol's contract is the boolean AND of all its tests. All must pass for the gate to open.

## Symbol State

Symbols gain a status field:

- **untested** — no contract. Current behavior. No auto-refresh.
- **green** — has contract, all dependencies current, tests pass.
- **red** — has contract, at least one dependency is not current. Stable but stale.

The runtime maintains this status and provides query operations for the LLM to inspect.

## Implementation Sequence

1. Add `Tests` field to `GraphNode`
2. Add test resolution (restricted resolveAST for tests)
3. Add test evaluation in Define (the gate)
4. Add `refine` operation
5. Add symbol status tracking (green/red/untested)
6. Modify RefreshAll for cascade with circuit breaker
7. Add status query operations
8. Update log format and replay for tests
9. Update MCP tools
10. Update arch library with new concepts
