# Plan: Extract Shared Object Model

## Status: Complete

## What Was Done

### Extraction (Phases 1-5)

Extracted business logic from CLI (`cmd/gc/`) and API
(`internal/api/`) into shared domain packages:

- `internal/sling/` -- work routing (DoSling, DoSlingBatch)
- `internal/convoy/` -- convoy CRUD with event emission
- `internal/agentutil/` -- agent resolution, pool expansion

The API handler calls `sling.DoSling` directly -- no more subprocess
delegation. The CLI is a thin adapter that formats structured results.

### Quality Pass

- **Structured data**: Domain returns typed fields (BeadID, Target,
  AgentSuspended, AutoBurned, etc.). No OutputLine, no msg/warn,
  no user-facing text in domain code.
- **Decomposed DoSling**: 20-line dispatcher -> preflight,
  slingFormula, slingOnFormula, slingDefaultFormula, slingPlainBead,
  finalize.
- **Converged dispatch_runtime.go**: Local graph routing duplicates
  replaced with sling package types.
- **Narrow interfaces**: AgentResolver, BranchResolver, Notifier.
- **Read-only molecule check**: FindBlockingMolecule for dry-run
  (no auto-burn during preview).

### Review Council Fixes

- Deleted `if false` placeholder blocks
- API surfaces MetadataErrors as `warnings` in JSON response
- DoSling validates required deps (Cfg, Store, Runner) at entry
- API wires BranchResolver for formula var population
- preflight returns idiomatic `(SlingResult, error)`
- Deleted dead "remove after tests" comment

## Architecture

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
```

### Key Design Decisions

- Structured data, not text strings, in domain layer
- Per-domain dep structs (SlingDeps, ConvoyDeps), not monolithic
- Narrow interfaces for DI (AgentResolver, BranchResolver, Notifier)
- Required deps validated at entry (Cfg, Store, Runner)
- preflight returns (Result, error) per Go convention
- CLI type aliases for verbosity reduction (legitimate Go pattern)
- Batch results as []SlingChildResult with per-child outcome data
