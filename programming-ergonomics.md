# Programming Ergonomics in Logos

Observations on how the LLM-as-programmer workflow performs in practice, and ideas for improvement.

## Context Window Consumption

Logos programming consumes context faster than traditional file-based coding. Several factors compound:

### Verbose MCP responses
Every `logos_eval` and `logos_define` returns full JSON. Diagnostic calls like `(traces)` can return enormous payloads. In contrast, file-based editing uses small diffs.

**Mitigations:**
- Use targeted queries: `(head (traces))` instead of `(traces)` for the last entry
- Optimize MCP response format — define could return just `{ok, node_id}` instead of echoing everything
- Consider adding purpose-built tools (e.g., `last-trace`, `trace-for-symbol`) as skills or MCP endpoints

### Full expressions on every redefine
Large definitions (like `lang-ui-page`) send the entire expression through the context on every change. In file-based coding, the Edit tool sends a small diff.

**Ideas:**
- Leverage homoiconicity — the code IS a data structure. Could we do programmatic transformations instead of full redefinitions? Something analogous to diffs but operating on the AST: "in this function, replace the third branch of the cond with X"
- This would be a novel capability — not just patching text, but structurally transforming code-as-data
- `node-expr` already returns the AST as logos data. A `define-from-ast` or structural `patch` operation could complete the loop

### Exploratory eval cycles
The test-check-fix loop (try something, wrong arg order, try again) generates many round trips. Each attempt is a full tool call with request and response.

**Mitigations:**
- Better documentation reduces guesswork (e.g., the `split` vs `split-once` arg order issue)
- The completed lang library will serve as an always-available reference, reducing trial-and-error

## Subagent Leverage

### Current state
All logos work happens in the main conversation. Batch operations (adding keywords to 15 nodes, migrating symbols to a library) consume context that could be delegated.

### Opportunity
Subagents could handle:
- Mechanical batch operations ("add keywords to these nodes")
- Library migrations ("move these symbols from session to this library")
- Validation sweeps ("run tests on all sections, report failures")
- Documentation authoring ("add examples and tests for these builtins following existing conventions")

### The documentation bootstrapping loop
The completed language reference will make agents more effective — give them the exact context they need (relevant lang-search results, conventions doc) and a targeted goal. Better docs → better agents → faster development → better docs. This is a virtuous cycle unique to logos.

## Parallel Defines and Refresh Ordering

### The problem
MCP tool calls in the same message may execute in parallel. When two defines reference each other — e.g., redefining a child node and its parent in the same batch — the ordering is not guaranteed. If the parent is processed first, it resolves the child to the **old** node.

### What happened: the `if` incident

While building the core forms section, `lang-ex-if-no-else` had a broken test expression (`(if false :yes)` — `if` requires 3 args). Two fixes were sent as parallel MCP defines:

1. Redefine `lang-ex-if-no-else` (corrected expression)
2. Redefine `lang-form-if` (references `lang-ex-if-no-else` in its examples list)

Since they were parallel, there was no guaranteed ordering. If the core processed `lang-form-if` before `lang-ex-if-no-else`, then `lang-form-if`'s expression resolved `lang-ex-if-no-else` to the **old** node (with the broken expr).

Then `refresh-all` was called with targets `["lang-ex-if-no-else", "lang-form-if"]`. This refreshed their dependents — `lang-forms`, `lang`, `on-request`. But it **skipped** `lang-form-if` itself because it was listed as a target (meaning "I'm already current, refresh things that depend on me"). So `lang-form-if` kept its stale reference to the old `lang-ex-if-no-else`.

The result: `(lang-validate lang-forms)` passed (evaluating `lang-forms` directly resolved to the refreshed node which walked through the current `lang-form-if` symbol). But `(lang-validate lang)` failed — it walked through `lang`'s stored reference to `lang-forms`, which contained the stale `lang-form-if` node with the old `lang-ex-if-no-else` reference.

A second `refresh-all` with just `["lang-ex-if-no-else"]` as the target treated `lang-form-if` as a **dependent**, re-resolved it, and the correct reference propagated through the full chain: `lang-ex-if-no-else` → `lang-form-if` → `lang-forms` → `lang`. All 106 tests passed.

### Why refresh doesn't always fix it
`refresh-all` treats its targets as "already correct" and only re-resolves their dependents. If a node is stale but listed as a target, it won't be fixed. It needs to appear as a **dependent** of some other target.

### Rules
1. **Define dependencies before dependents.** If B references A, define A first (separate message), then B.
2. **When both change, refresh from the leaf.** After parallel defines, call `refresh-all` with just the leaf target. The dependent will be re-resolved as part of the chain.
3. **Don't list stale nodes as refresh targets.** A target means "I'm current." If it might be stale, make it a dependent instead.

### Broader implication
This is a general principle for graph programming: the define-refresh cycle has an implicit ordering contract. Making this explicit — either through sequential defines or smarter refresh semantics — would eliminate a class of subtle bugs.

## Small Functions vs. Tool Call Overhead

In traditional Lisp style, you break things into many small composable functions. In logos, each `define` is a tool call with overhead (context consumed, round trip). This creates a tension:

- **Small functions** = better design, easier to understand and modify individually
- **Fewer defines** = less context consumed, faster overall

The AST-level diff/patch idea could resolve this tension — define small functions, modify them with surgical operations instead of full redefinitions.

## Future: Skills for Common Patterns

Recurring workflows could become skills:
- **Batch define** — define multiple symbols from a specification
- **Library migration** — move symbols from session to library
- **Diagnostic snapshot** — last trace, current fuel, active library, recent errors
- **Node inspection** — targeted view of a symbol's value, dependents, and staleness
