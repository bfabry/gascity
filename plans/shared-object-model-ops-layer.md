# Plan: Extract Shared Object Model

## Status: Complete

## Completed Work

### Phases 1-5: Extraction (DONE)

- `internal/sling/` -- work routing
- `internal/convoy/` -- convoy CRUD with event emission
- `internal/agentutil/` -- agent resolution, pool expansion
- API handler calls domain directly (no subprocess)
- Narrow interfaces (AgentResolver, BranchResolver, Notifier)

### Quality Pass (DONE)

#### Step 1: Eliminate OutputLine (DONE)

Replaced `OutputLine` (user-facing text strings) with structured
data fields on `SlingResult`: `AgentSuspended`, `PoolEmpty`,
`AutoBurned`, `MetadataErrors`, `WispRootID`, `FormulaName`,
`ContainerType`, `Children []SlingChildResult`, `IdempotentCt`.

Domain returns pure data. CLI formats display strings in
`printSlingResult`/`printBatchSlingResult`. API reads fields
directly into JSON.

#### Step 2: Decompose DoSling (DONE)

Split into focused functions: `DoSling` (20-line dispatcher) ->
`preflight`, `slingFormula`, `slingOnFormula`,
`slingDefaultFormula`, `slingPlainBead`, `finalize`.

#### Step 3: Converge dispatch_runtime.go (DONE)

Local graph routing duplicates replaced with thin delegations to
sling package types (`GraphRouteBinding`, `ApplyGraphRouting`,
etc.). ~80 lines of duplicated code eliminated.

#### Step 4: Delete StartGraphWorkflow wrapper (DONE)

Removed deprecated wrapper.

#### Step 5: Slim dry-run display (DONE)

Dry-run functions now use domain functions directly
(`sling.CheckBeadState`, `sling.BuildSlingCommand`,
`sling.FindBlockingMolecule`) instead of local copies.

Fixed a bug: dry-run was auto-burning molecules (mutating during
preview). Now uses read-only `FindBlockingMolecule` check.

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

### Domain API Design

- **Structured data, no text**: Domain returns typed fields
  (BeadID, Target, Method, AgentSuspended, AutoBurned, etc.).
  Callers format display strings.
- **Decomposed functions**: DoSling is a 20-line dispatcher.
  Each dispatch path (formula, on-formula, default-formula,
  plain-bead) is a focused function. Shared post-steps in
  `finalize`.
- **Narrow interfaces**: AgentResolver, BranchResolver, Notifier.
  Direct imports for IsMultiSessionAgent, LookupSessionName,
  ScaleParamsFor.
- **Per-child results**: Batch operations return
  `[]SlingChildResult` with per-child outcome data.
- **Read-only checks**: `FindBlockingMolecule` for dry-run
  molecule validation without auto-burn mutations.
- **Zero I/O, zero OutputLine, zero msg()/warn()** in domain.
