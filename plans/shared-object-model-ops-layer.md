# Plan: Extract Shared Object Model

## Status: Quality Pass In Progress

Phases 1-5 extracted business logic into domain packages. The quality
pass addresses five remaining issues with the domain API.

## Completed Work

### Phases 1-5: Extraction (DONE)

- `internal/sling/` -- work routing (DoSling, DoSlingBatch, formula
  instantiation, graph routing, helpers)
- `internal/convoy/` -- convoy CRUD with event emission
- `internal/agentutil/` -- agent resolution, pool expansion
- API handler calls domain directly (no subprocess)
- Narrow interfaces (AgentResolver, BranchResolver, Notifier)
- Zero I/O in domain packages
- All tests pass, zero regressions

### Quality Pass (IN PROGRESS)

#### Step 1: Eliminate OutputLine

Replace `OutputLine` (user-facing text strings in domain) with
structured data fields on `SlingResult`. The domain returns data;
callers format display strings.

New fields: `AgentSuspended`, `PoolEmpty`, `CrossRigBlocked`,
`WispRootID`, `AutoBurned []string`, `MetadataErrors []string`.
Remove `Output []OutputLine`, `msg()`, `warn()` entirely.

#### Step 2: Decompose DoSling

Split 237-line god function into focused dispatch functions:
`preflight`, `slingFormula`, `slingOnFormula`,
`slingDefaultFormula`, `slingPlainBead`, `finalize`.

#### Step 3: Converge dispatch_runtime.go

Replace local `graphRouteBinding` / `applyGraphRouting` in
`cmd/gc/dispatch_runtime.go` with imported `sling.GraphRouteBinding`
/ `sling.ApplyGraphRouting`.

#### Step 4: Delete StartGraphWorkflow wrapper

Remove deprecated wrapper function.

#### Step 5: Slim dry-run display

Update `dryRunSingle`/`dryRunBatch` to format from `SlingResult`
fields instead of re-querying beads.

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
   (persistence + runtime primitives)
```

### Structural patterns

- Package doc comments on every package
- Narrow interfaces for dependency injection
- Context-only error messages (no CLI command names)
- Structured result data (no user-facing text in domain)
- Return (Result, error), no I/O
- Tests alongside code
- Strict downward-only dependency direction
