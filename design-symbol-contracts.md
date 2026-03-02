# Design: Symbol Contracts (Auto-Refresh via Tests)

## Problem

Refresh-all is the biggest source of friction in logos development. When a symbol is redefined, all its dependents may become stale. Currently the LLM must manually call refresh-all, often multiple times, and verify correctness afterward. Multi-level chains are particularly fragile — refresh can leave intermediate nodes stale, requiring manual bottom-up redefines.

## Core Idea

Attach a **test function** (a "contract") to each symbol. When a dependency changes, the runtime automatically runs the test. If it passes, the symbol is auto-refreshed. If it fails, the symbol is marked red and the LLM is notified.

A symbol becomes: **pointer to current node + contract (test function) + status (green/red)**.

## Key Design Decisions

### Tests attach to symbols, not nodes
Nodes are immutable snapshots. Symbols are the living entities that evolve over time. The test encodes what the symbol *means* — the concept it represents — not what a particular node version looks like. When the node changes, the test persists because it belongs to the symbol.

### Two dependency modes

**Direct dependency (node-ref):** Symbol B references symbol A via node-ref. When A is redefined:
1. Runtime checks if B has a test
2. If yes: evaluate B's expression with A resolved to the new node, run B's test
3. If test passes: auto-refresh B (create new node version), mark green
4. If test fails: mark B red, notify LLM
5. If no test: mark B as "stale" (current behavior — LLM must manually refresh)

**Indirect dependency (link):** Symbol B links to symbol A. Links are already "always-fresh" — follow resolves to the current node. When A changes:
1. If B has a test, run it
2. If test fails: mark B red (informational — no refresh needed, but the contract is broken)
3. This catches cases like A being deleted or changing shape in a way B doesn't expect

### Symbol lifecycle

Symbols progress through states:

1. **Initial** — defined without a test. Works like today. No auto-refresh, no contract validation.
2. **Contracted** — test attached. Symbol enters the auto-refresh system. The test encodes the LLM's understanding of what this symbol should do.
3. **Steady state** — test and node co-evolve. When the node is redefined, the test is updated atomically in the same operation.

The transition from initial to contracted happens when the LLM decides it understands the symbol well enough to write a test. There's no rush — symbols can live without tests indefinitely.

### Tests are not graph nodes

A test is attached to a symbol as metadata, not as a separate named node. This means:
- Tests don't have node IDs or symbol names
- Nothing else can depend on a test
- Tests don't need to be refreshed themselves (they're re-evaluated, not re-resolved)
- Tests CAN reference other symbols (they need to call things to validate behavior)

When a dependency of a test changes and the main node hasn't changed, the test is simply re-run. If it still passes, green. If it fails, red. The test is always run against the current graph state.

### Red/green table

The runtime maintains a status table:
- `(red-symbols)` — list symbols whose contracts are broken
- `(green-symbols)` — list symbols with passing contracts
- `(symbol-status name)` — status + last test result + when it went red

This gives the LLM a programmatic work list. Session startup: "5 symbols went red since last session." The LLM can prioritize what to fix.

## Relationship to Existing Systems

**Lang library tests:** Currently stored as `:tests` data fields on nodes, run manually by `lang-validate`. These could migrate to symbol-attached contracts. `lang-validate` would become "check the red/green table for lang symbols."

**Arch library anchors:** `arch-validate` checks code hashes via mod-go. These could also become symbol contracts — each anchor's test validates its hash against the source.

**Guide library anchors:** Same pattern as arch.

## Open Questions

- **Test execution cost.** Auto-refresh could trigger cascading test runs. Need a fuel limit or fast/full test distinction. Module-calling tests (like arch-validate) are expensive.
- **Atomic define + test.** What's the API? `(define-with-test name expr test-expr)`? Or separate ops: `(define name expr)` then `(attach-test name test-expr)`?
- **Cascade depth.** If auto-refreshing B triggers auto-refresh of C (which depends on B), how deep does it go? Need a bound or a breadth-first approach.
- **Persistence.** Tests need to survive restart. Store in the log alongside defines? A separate metadata file?
- **What constitutes a test?** A function that returns true/false? A function that returns a result map with details? An expression that must not error?

## Implementation Sketch

### Graph changes
- `symbolTests map[string]string` — maps symbol name to test source expression
- `symbolStatus map[string]Status` — green/red/untested
- On `Define`: after creating the new node, check dependents. For each dependent with a test, run the test with the new dependency. Auto-refresh if pass, mark red if fail.

### New core ops
- `attach-test name test-expr` — associate a test with a symbol
- `symbol-status name` — query status
- `red-symbols` / `green-symbols` — list by status

### Log format
```
(define foo (fn (x) (add x 1)))
(test foo (fn () (assert (eq (foo 3) 4))))
```

## Why This Matters

This shifts the LLM's role from **manual graph maintenance** (calling refresh-all, checking staleness, redefining bottom-up) to **responding to automated signals** (fixing red symbols, writing better contracts). The runtime does the mechanical work; the LLM does the creative work.

It also makes symbols into richer entities — not just pointers, but concepts with contracts. A symbol's test is a machine-readable specification of what it means. This is a step toward the system understanding its own semantics.
