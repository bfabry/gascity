# Plan: Extract Shared Object Model

## Status: Phases 1-4 Complete

The shared domain layer is implemented across three packages with
natural names. The generic `internal/ops/` package was eliminated in
favor of domain-specific packages.

## What Was Done

### Phase 1: Sling (`internal/sling/`) -- DONE

Extracted ~1400 lines of sling business logic from `cmd/gc/cmd_sling.go`.
The API handler (`internal/api/handler_sling.go`) now calls
`sling.DoSling` directly -- the subprocess delegation anti-pattern
(API shelling out to `gc sling`) is eliminated.

**Package:** `internal/sling/`
- `sling.go` -- Types (SlingDeps, SlingOpts, SlingRunner), ~30 helper
  functions, formula instantiation, workflow launch
- `sling_core.go` -- DoSling and DoSlingBatch core routing
- `sling_graph.go` -- Graph.v2 workflow routing and decoration
- `sling_attachment.go` -- Molecule/workflow attachment checks,
  CheckBeadState
- `path_util.go` -- Path normalization helpers
- `sling_test.go` -- 22 tests

### Phase 2: Convoy (`internal/convoy/`) -- DONE

Extracted convoy create, progress, add-items, and close operations.
Fixed a bug where the API silently dropped lifecycle events
(ConvoyCreated, ConvoyClosed). The ops functions emit events via
the injected Recorder.

**Package:** `internal/convoy/`
- `convoy.go` -- ConvoyCreate, ConvoyProgress, ConvoyAddItems,
  ConvoyClose with event emission
- `convoy_fields.go` -- ConvoyFields metadata type and helpers
- Tests: 12 tests

### Phase 3: Agent Resolution (`internal/agentutil/`) -- DONE

Unified agent resolution with options-driven behavior serving three
modes via ResolveOpts:
- CLI dispatch: UseAmbientRig=true, AllowPoolMembers=true
- API sling dispatch: AllowPoolMembers=true (no ambient rig)
- API session creation: TemplateOnly=true

**Package:** `internal/agentutil/`
- `resolve.go` -- ResolveAgent, DeepCopyAgent, pool instance matching
- `resolve_test.go` -- 8 tests

### Phase 4: Pool Expansion (`internal/agentutil/`) -- DONE

Extracted shared pool expansion for enumerating agents including pool
instances.

**Package:** `internal/agentutil/` (same package as agent resolution)
- `pool.go` -- ExpandAgents, PoolInstanceName
- `pool_test.go` -- 6 tests

## Architecture

### Package layout (natural names, not generic containers)

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

- Domain packages never import `internal/api` or `cmd/gc`
- Each domain has its natural name, not a generic container
- Callers import domain packages, never the reverse

### Key design decisions

- **Per-domain dependency structs** (SlingDeps, ConvoyDeps) instead of
  one monolithic Deps. Each contains only what that domain needs.
- **Lazy store resolution** via function fields rather than eagerly
  populated maps (preserves CLI performance).
- **Events via existing interface**: `events.Provider` embeds
  `events.Recorder`. No new method on `api.State` was needed.
- **Injected callbacks** for cmd/gc-specific functions that domain
  packages can't import directly (resolveAgentIdentity, etc.).
- **I/O stays in callers**: Domain functions still accept `io.Writer`
  for now. Structured results (Warnings/Messages fields) are the
  next improvement.

### Why not `internal/ops/`?

The original plan used a generic `internal/ops/` package. During
implementation we realized these are domain concepts with natural
names (sling, convoy, agent), not "operations." A generic container
obscures what's inside and invites becoming a god package. Each
domain gets its own package.

`internal/agentutil/` (not `internal/agent/`) is used because the
existing `internal/agent/` package is imported by `internal/config/`,
which would create an import cycle.

## What Remains (Phase 5+)

All remaining business logic migrates to domain packages when touched:
- Agent suspend/resume (needs config-editor abstraction)
- Session lifecycle orchestration (beyond `session.Manager`)
- Mail orchestration, city lifecycle, order/formula operations
- Single-consumer operations (convoy autoclose/land, etc.)

New business logic goes to domain packages by default.

CLAUDE.md's "no premature abstraction" refers to Go `interface` types,
not code organization. Moving concrete functions to the correct layer
is restructuring, not abstracting.
