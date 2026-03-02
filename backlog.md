# Logos Backlog

Everything we could work on, regardless of size or maturity. The underlying goal of the current wave of changes (symbol contracts, arch upgrades) is to be able to model this list in the graph itself as a "garden" — items with lifecycle, effort, and tags — piloting the contracts system.

## Core Runtime

1. **Symbol contracts (auto-refresh via tests)** — Nodes gain a tests field. Tests gate node creation. Auto-refresh cascade with red/green status and circuit breaker. Full design: `design-symbol-contracts-v2.md`.

2. **Refine operation** — Create a new node from an existing one by specifying deltas (change expr, add/remove test). Immutable data structure update semantics. Part of symbol contracts implementation.

3. **Toposort in compact** — `compactLibrary` writes definitions in arbitrary order (Go map iteration), causing startup failures when symbols depend on each other. Fix: topological sort using node Refs.

4. **Refresh-all blank-line separator bug** — `appendLogToOwner` can write consecutive entries without the blank line the replay parser expects. Causes `unexpected input after expression` on restart.

5. **Stale dependent reporting on define** — Include `ref-by` results for the superseded node in the define response. Cheap to compute, turns a hidden footgun into an explicit prompt.

6. **`eval` builtin** — Evaluate an arbitrary expression string from within logos. `link`/`follow` covers named symbols but not general expressions.

7. **Atomic multi-symbol updates** — Extend refresh-all to accept multiple defines as an atomic unit. All succeed or none take effect. Immutable nodes make rollback trivial.

8. **Concurrency** — Make the graph safe for concurrent access. Go goroutines/channels as substrate. Required for agents, schedulers, concurrent HTTP handlers. Immutable node design helps — readers never see half-written state.

9. **`lower-case` string builtin** — Enables case-insensitive search in lang-search and elsewhere.

10. **AST-level patch/diff** — Programmatic structural transformations instead of full redefinitions. `node-expr` returns the AST; a `define-from-ast` or `patch` op would close the loop. Natural extension of `refine`.

11. **Per-module callback routing** — Register handlers per module instead of a single global `on-request`.

12. **`split`/`split-once` arg order inconsistency** — One is `(delimiter, string)`, the other `(string, delimiter)`. Should be consistent.

## Modules

13. **HTTP client module** — Call external APIs, webhooks, Anthropic API. Key enabler for agents-as-primitives.

14. **Scheduler module** — Cron-like triggers, delayed execution.

15. **MCP client module** — Call tools on external MCP servers from within logos.

16. **MCP server module** — Expose graph tools to other MCP clients (other LLMs, other systems).

## Libraries

17. **Shared docs library** (`data/docs.logos`) — Extract common tree-walking, validation, description patterns from lang/arch into shared framework. Deferred pending more usage experience.

18. **Logos-level guides** — How-to guides for adding a base library function, adding a lang node, adding an arch concept, creating a library. Different from current guides (which are Go-level).

19. **Libs library** — Document what's in base, debug, web, db libraries as graph nodes.

## Applications

20. **Nabu rebuild (Phase 4)** — LLM-driven data visualization system, rebuilt on logos v3.

21. **Nisaba (roadmap app)** — Roadmap management app. Has db library and schema. Unclear how far along UI/logic is.

22. **Garden** — Graph-native backlog/idea tracker. Items as nodes with name, description, maturity (seed → sprout → sapling → growing → harvest), effort (small/medium/large), tags. Functions: plant, grow, tend, survey, inspect, prune. First pilot of symbol contracts.

## MCP / Context Management

23. **MCP response hooks for context injection** — Open a library, get its docs. Hit an error, get relevant arch context. Close a library, trigger smoke test. Makes the MCP layer context-aware.

24. **Optimize MCP response format** — Define could return just `{ok, node_id}` instead of echoing everything. Reduces context window consumption.

25. **Purpose-built MCP tools** — `last-trace`, `trace-for-symbol`, diagnostic snapshot, node inspection. Targeted tools that reduce eval round-trips.

## Developer Experience / Workflow

26. **Batch define skill** — Define multiple symbols from a specification in one operation.

27. **Library migration skill** — Move symbols from session to library via subagent.

28. **Validation sweep skill** — Subagent runs tests on all sections, reports failures.

29. **Subagent leverage** — Use subagents for mechanical batch ops, migrations, documentation authoring. Better docs enable better agents (virtuous cycle).

30. **Generalize `lang-describe`** — Any node following the name/description/examples/tests/members convention could use it. Not specific to lang nodes.

## Infrastructure / Operations

31. **Multi-session/project switching** — Separate graph contexts for different work streams.

32. **Search index module** — Indexed full-text search across the graph.

## Arch Library Upgrades (Current)

33. **Sub-concept layer** — Add middle-layer concepts to arch: symbol table, GraphNode fields, log serialization, response format, graph→eval bridge, symbol ownership, eval entry points. Closes the gap between high-level narrative and implementation detail.
