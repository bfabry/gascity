# Plan: Extract Shared Object Model

## Status: Complete

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
   internal/agentutil/    internal/graphroute/
   internal/pathutil/
            |
            v
   internal/{beads,config,formula,molecule,agent,events,...}
```

## Domain Packages

### internal/sling/ -- work routing

**Intent-based API** (new):
```go
s, _ := sling.New(deps)           // validate once
s.RouteBead(ctx, beadID, target, opts)
s.LaunchFormula(ctx, name, target, opts)
s.AttachFormula(ctx, name, beadID, target, opts)
s.ExpandConvoy(ctx, convoyID, target, opts, querier)
```

Each method takes exactly the params it needs via focused option
structs (`RouteOpts`, `FormulaOpts`). No flag bag.

**Typed routing** (new):
```go
type BeadRouter interface {
    Route(ctx, RouteRequest) error
}
```
Domain says "route this bead to this target." Implementation decides
how (shell command, direct store, API call).

**Legacy API** (preserved for backward compat):
`DoSling(SlingOpts, SlingDeps, querier)` and `DoSlingBatch` still
work. New methods delegate to them internally.

### internal/graphroute/ -- graph decoration

380 lines of graph.v2 routing extracted from sling. Owns step
binding resolution, cycle detection, control-dispatcher routing.
Own `Deps` interface (`AgentResolver` only).

### internal/convoy/ -- convoy CRUD

ConvoyCreate, ConvoyProgress, ConvoyAddItems, ConvoyClose with
event emission via `events.Recorder`.

### internal/agentutil/ -- agent resolution + pool expansion

Options-driven `ResolveAgent`, `ExpandAgents`, `ScaleParamsFor`,
`LookupSessionName`, `IsMultiSessionAgent`, `DeepCopyAgent`.

### internal/pathutil/ -- path utilities

`NormalizePathForCompare`, `SamePath`. Shared by 11+ CLI files.

## Design Principles

- **Intent-based API**: callers express intent, not flags
- **Typed routing**: domain describes what, implementation decides how
- **Structured data**: domain returns typed fields, callers format
- **Narrow interfaces**: AgentResolver, BranchResolver, Notifier,
  BeadRouter
- **Deps validated at construction**: New() checks required fields
- **No I/O in domain**: zero fmt.Fprintf, zero io.Writer
- **Graph routing separated**: own package, own tests, own deps
- **Backward compat**: old DoSling/DoSlingBatch preserved during
  caller migration

## Remaining Migration (Steps 4-5)

Callers (CLI, API) still use the old DoSling/SlingOpts API.
Migration to the intent-based API is incremental:

- CLI: `cmdSling` maps flags to `s.RouteBead`/`s.LaunchFormula`/etc.
- API: `handleSling` maps request body to the right method
- After all callers migrate: delete DoSling, DoSlingBatch, SlingOpts,
  SlingRunner, preflight, and the dispatch functions
