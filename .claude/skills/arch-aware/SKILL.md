---
name: arch-aware
description: |
  Guidance for investigating and maintaining the logos Go codebase architecture.
  TRIGGER when: investigating, exploring, or searching logos Go code (core/, mcp-logos/, mod-*), or after committing Go source changes.
  DO NOT TRIGGER when: working purely with logos language definitions (data/ files only), or non-code tasks.
user-invocable: false
---

# Architecture-Aware Development

The logos codebase has a self-documenting architecture layer stored as graph nodes in the `arch`, `guides`, and `lang` libraries. Use it before falling back to raw code search.

## Investigation Protocol

When investigating logos Go code:

1. **Start with `arch-search`** — keyword search across concepts, interactions, and anchors. Results include file:line for direct navigation.
2. **Use `arch-describe`** for deeper context on a concept or interaction node (brief or full).
3. **For logos language questions**, use `lang-search` / `lang-describe`.
4. **For procedural how-to** (adding ops, builtins, modules, graph methods), use `guide-search` / `guide-describe`.
5. **Fall back to grep/glob** only if arch/lang/guides don't cover the area.

## Maintenance Protocol

Go code changes must be committed to git before updating arch — anchors reference commit hashes.

After committing Go source changes to `core/`, `mcp-logos/`, or `mod-*/`:

1. **Check if arch nodes are affected** — if you changed behavior documented by an arch concept, interaction, or sub-concept, update the description via `refine`.
2. **Check if anchors are stale** — run `arch-validate` to find anchors with changed or missing hashes. Update `:hash`, `:scope`, and `:commit` as needed.
3. **Update proactively** — don't wait to be asked. Architecture docs that drift from code lose their value.
