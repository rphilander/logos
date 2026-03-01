# Logos Vision

## What Logos Is

Logos is a graph runtime plus a collection of modules that let it interact with the outside world. The graph holds code and data as named, immutable, dependency-tracked expressions. The LLM is the programmer — it reads, writes, and evolves the graph through a conversational interface. There are no source files, no builds, no deployments. Change is instantaneous: redefine a function, refresh its dependents, and the running system picks it up.

This is not a platform for building applications in the traditional sense. An application is a well-defined bundle of functionality that takes significant effort to build, test, and operate, and therefore changes infrequently. Logos dissolves that concept. The graph is a single continuous fabric. Today it has functions for routing HTTP requests and querying a database. Tomorrow it has functions for scheduling reminders or summarizing an RSS feed. There are no boundaries between capabilities — they are all regions of the same graph, sharing the same runtime, the same modules, the same ability to be redefined in seconds.

The cost of change is near zero. Not "we can deploy faster" — the concept of deployment doesn't exist. The feedback loop is as fast as the conversation. This is a phase transition: the boundary between wanting something and having it effectively disappears.

## Building Blocks

The graph is the brain. Modules are the sensory organs — they let the graph touch the outside world. The current set is a start:

- **mod-time** — clocks and time math
- **mod-http-server** — serve HTTP, dispatch requests to the graph
- **mod-sqlite** — persistent structured storage
- **mod-mcp-server** — expose tools to MCP clients (including other LLMs)

What's needed to reach full capability:

- **HTTP client** — call external APIs, webhooks, the Anthropic API
- **File I/O** — read/write files, watch directories
- **Scheduler** — cron-like triggers, delayed execution
- **MCP client** — call tools on external MCP servers

With these, the range of what can be created by writing to the graph is immense.

## Agents as a Primitive

An HTTP client that can call the Anthropic API makes inference available as a graph-level operation. Define tools as data, write a loop with success criteria, and you have an agent. Fast, cheap models (Sonnet, Haiku) become available for AI-required tasks — summarization, classification, extraction, planning — as just another primitive alongside `send` and `db-query`.

Multiple agents can run concurrently — one monitoring, one building, one researching — all reading and writing the same graph.

## Concurrency

The core currently serializes everything through a single goroutine. This was a deliberate simplification and has been fine for interactive use. But agents, schedulers, background jobs, and concurrent HTTP handlers are inherently concurrent.

Go's goroutines and channels are the right substrate. The interesting work is making the graph safe for concurrent access while preserving the simplicity of the programming model. The immutable node design helps — readers never see half-written state. Significant work but well within reach.

## Quality Without Traditional Testing

Testing is hedging against the cost of rewriting and redeploying code. When change is cheap and the system can self-diagnose, the economics flip.

The traditional testing pyramid exists because change is expensive, regressions are hard to detect, and the humans making changes don't have full context. In logos, all three are different: change is instantaneous and reversible (immutable nodes), the LLM can inspect any part of the running system (graph builtins, traces, step debugger), and the LLM has — or can be given — full context about the system's design and intent.

The pattern becomes: quick manual smoke test, then make sure documentation and context are in order so any issue can be rapidly diagnosed and fixed. The documentation *is* the safety net.

## Context Management

The critical capability that ties everything together. The LLM's effectiveness is bounded by what's in its context window. The system needs to surface the right knowledge at the right time — not dump everything upfront (MEMORY.md's scaling problem), not leave the LLM to guess.

Three kinds of knowledge:

- **Architecture** — what the system *is*. Design decisions, component relationships, rationale. Lives in the graph as data, queryable by the same tools used for code.
- **Roadmap** — what the system *will become*. Milestones, priorities, dependencies. Also graph data, evolving as understanding deepens.
- **Operational** — the current state of things. What's running, what broke recently, what changed.

Delivery mechanism: MCP response hooks. Open a library, get its documentation. Hit an error, get relevant architecture context. Close a library, trigger a smoke test. The MCP layer becomes context-aware, not just a passthrough.

## Graph as Data Store

The graph already has the properties needed for storing structured knowledge:

- `define` binds a name to any value — a map, a list, a string, a function.
- Redefining a symbol creates a new immutable node. The old node still exists — history for free.
- Self-referential evolution: `(define foo (put foo "new-field" value))` resolves `foo` to its current node, creates a new node with the updated value.
- No schema. No migrations. Entities are open — add fields whenever needed.
- Graph builtins (`ref-by`, `symbols`, `node-expr`, `search-source`) work on data nodes the same as code nodes.

### Functions Instead of Queries

The LLM builds access functions alongside the data model. They co-evolve. Domain concepts become named functions. The LLM calls functions it wrote and tested, rather than constructing ad-hoc queries. Functions *are* documentation.

### Data Model Evolution

Incremental, non-breaking changes to running systems:

1. Define new function versions under temporary names. Test against real data.
2. Redefine the live symbols and `refresh-all` to update all dependents.
3. Old data doesn't have to change immediately. Functions bridge old and new formats.

### Atomic Multi-Symbol Updates

Extend `refresh-all` to accept multiple defines as an atomic unit. All succeed or none take effect. Immutable nodes make rollback trivial.

### Role of SQLite

SQLite remains for bulk structured records, complex filtering, aggregation. The graph stores the system's self-knowledge. They coexist.
