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
