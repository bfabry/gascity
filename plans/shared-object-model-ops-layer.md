# Plan: Extract Shared Object Model

## Status: Phases 1-4 Complete, Conformance Pass In Progress

The shared domain layer is implemented across three packages with
natural names. The generic `internal/ops/` package was eliminated in
favor of domain-specific packages.

## What Was Done

### Phase 1: Sling (`internal/sling/`) -- DONE
### Phase 2: Convoy (`internal/convoy/`) -- DONE
### Phase 3: Agent Resolution (`internal/agentutil/`) -- DONE
### Phase 4: Pool Expansion (`internal/agentutil/`) -- DONE
### I/O Removal -- DONE

Domain functions return structured results (SlingResult, etc.) with
Messages and Warnings fields. Zero io.Writer, zero fmt.Fprintf in
domain packages. CLI adapter prints results; API adapter reads
struct fields directly into JSON.

### Conformance Pass -- IN PROGRESS

After comparing new packages against existing internal packages,
four structural mismatches were identified:

**1. Package doc comments** -- `sling` and `agentutil` are missing
package doc comments. `convoy` has a stale one ("Package ops").
Existing packages all have proper doc comments.
Fix: Add correct package doc comments to all three packages.

**2. Dependency injection style** -- Existing packages use narrow
interfaces (e.g., `convergence.Store`, `convergence.EventEmitter`).
Our `SlingDeps` uses raw function callbacks (`ResolveAgent func(...)`,
`IsMultiSession func(...)`, etc.). This is a structural mismatch.
Fix: Replace func fields with narrow interfaces where the callback
has a clear single-method contract. Keep func fields only where the
callback is truly ad-hoc (e.g., `PokeController`).

**3. Error message prefixes** -- Existing packages use context-only
prefixes (`"reading root bead metadata: %w"`). Our sling errors
include the CLI command name (`"gc sling: instantiating formula..."`).
Domain packages shouldn't know they're called from `gc sling`.
Fix: Remove "gc sling:" prefix from all domain-package errors.
Use context-only messages like `"instantiating formula %q: %w"`.

**4. Output ordering** -- Original CLI interleaved warnings and
messages per-operation. The new code batches all warnings first via
`printSlingResult`. For batch operations with per-child warnings,
this changes what the user sees.
Fix: Replace separate Warnings/Messages slices with a single
ordered Output slice that preserves interleaving, with each entry
tagged as message or warning.

## Architecture

### Package layout

```
cmd/gc/cmd_*.go               internal/api/handler_*.go
  (arg parsing,                 (HTTP routing,
   text formatting,              JSON serialization,
   exit codes)                   status codes)
        \                              /
         \                            /
          v                          v
   internal/sling/        internal/convoy/
   internal/agentutil/
            |
            v
   internal/{beads,config,formula,molecule,agent,events,...}
   (persistence + runtime primitives)
```

### Structural patterns (must match existing packages)

- **Package doc comments** on every package
- **Narrow interfaces** for dependency injection, not raw func fields
- **Context-only error messages** -- no CLI command names in errors
- **Ordered output** preserving interleaving of warnings and messages
- **Return structured results** -- (ResultType, error), no I/O
- **Tests alongside code** in *_test.go files
- **Error wrapping** with %w and context prefix

## What Remains (Phase 5+)

All remaining business logic migrates to domain packages when touched.
New business logic goes to domain packages by default.
