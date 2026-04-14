# Plan: Extract Shared Object Model (`internal/ops`)

## Context

The CLI (`cmd/gc/cmd_*.go`, ~45 commands) and the HTTP API
(`internal/api/handler_*.go`, ~80 endpoints) currently relate to domain
logic in three **inconsistent** patterns:

1. **Subprocess delegation** -- The sling API handler shells out to
   `gc sling` as a subprocess. All ~2040 lines of business logic live
   in `cmd/gc/cmd_sling.go`.
2. **Asymmetric logic** -- Convoy operations, city status, and agent
   lifecycle have related but non-identical implementations in CLI and
   API. The CLI does multi-store resolution, metadata persistence, and
   event emission; the API often omits these (e.g., API convoy
   handlers silently drop lifecycle events).
3. **Shared primitives** -- Session create, mail send, and bead CRUD
   both go through shared interfaces (`session.Manager`,
   `mail.Provider`, `beads.Store`). These are already well-factored.

The goal: **ALL business logic lives in `internal/ops/`.** The CLI
and API become truly thin adapters. Multi-consumer operations move
first (highest DRY value -- eliminates active duplication). Single-
consumer operations follow. New business logic goes to ops by
default.

Note: CLAUDE.md's "no premature abstraction" rule says "don't build
interfaces until two implementations exist." This refers to Go
`interface` types, not code organization. Moving concrete functions
from `cmd/gc/` to `internal/ops/` is restructuring, not abstracting.
We're not creating premature interfaces -- we're moving concrete
implementations to the correct layer.

## Architecture Best Practices Alignment

- **DRY** -- One source of truth for business logic, not two
  divergent implementations.
- **Separation of Concerns / SRP** -- Ops has one job (business
  logic), CLI has one job (terminal UX), API has one job (HTTP).
- **Layered Architecture** -- Strict downward-only dependency: callers
  -> ops -> primitives. No upward imports (enforced by test).
- **Keep Serialization at the Edges** -- Ops functions return typed Go
  structs. Serialization to JSON (API) or text (CLI) happens only in
  the adapter layer.
- **KISS / YAGNI** -- Per-domain dep structs. Extract multi-consumer
  ops first (highest DRY value). Don't over-specify future phases.
  Single-consumer ops stay until duplication or a second consumer
  appears (per CLAUDE.md "no premature abstraction").
- **Don't Swallow Errors** -- Ops functions return explicit errors
  with sentinel values. Intermediate warnings/messages are returned
  in result structs, not swallowed.
- **TDD** -- Tests written first. Existing fakes
  (`beads.NewMemStore()`, `runtime.NewFake()`, `events.NewFake()`)
  make this practical.
- **Low Coupling, High Cohesion** -- Per-domain dep structs contain
  only what that domain needs.
- **Clear Abstractions & Contracts** -- Small, stable operation
  signatures (input struct -> result struct + error).

## Design Principles

### Stateless library, not a new process

`internal/ops` is a Go library linked into both the supervisor (long-
lived daemon) and the CLI (short-lived process). It introduces no new
processes, no new synchronization, and no new storage.

### Clean dependency direction

```
cmd/gc/cmd_*.go               internal/api/handler_*.go
  (arg parsing,                 (HTTP routing,
   text formatting,              JSON serialization,
   exit codes)                   status codes)
        \                              /
         \                            /
          v                          v
            internal/ops/*.go
            (domain logic)
                    |
                    v
          internal/{beads,session,mail,events,...}
          (persistence + runtime primitives)
```

- `internal/ops` never imports `internal/api` or `cmd/gc` (enforced
  by automated test)

### Per-domain dependency structs

Each domain gets its own dep struct containing only what it needs.
Store access uses **lazy resolution functions** rather than eagerly
populated maps (CLI resolves one store per command; eager init would
regress performance).

### Operations return structured data; adapters format

- Ops functions return result structs (including `Warnings []string`
  and `Messages []string` for intermediate user-facing info) and Go
  errors
- Never write to `io.Writer` or `http.ResponseWriter`
- Errors use sentinel values (`ErrNotFound`, `ErrAlreadyClosed`)
- CLI adapter formats warnings/messages to stderr/stdout
- API adapter includes them in JSON responses or drops as appropriate

### Event recording via existing interfaces

`events.Provider` already embeds `events.Recorder` (see
`internal/events/events.go:80`). No new method on `api.State` is
needed. Ops dep structs accept `events.Recorder` interface:
- API adapter passes `state.EventProvider()` (type-compatible)
- CLI adapter passes its `FileRecorder`
- Tests pass `events.NewFake()`
- When nil, use `events.Discard` fallback

---

## Phased Migration

All phases are incremental -- existing tests pass at every step.
Multi-consumer ops first (eliminate duplication + anti-patterns),
then single-consumer ops migrate when touched.

### Phase 1: Sling (eliminate subprocess delegation)

**Why first:** Sling has 2040 lines of real business logic, a
concrete anti-pattern (API shells out to CLI subprocess), and its
extraction has the highest impact.

**Extraction boundary:** End-to-end, from "I have inputs (target,
bead/formula, options)" to "work is routed, system is notified,
result is returned." This includes:
- Context building: rig path resolution, store/store-ref selection,
  runner env setup (GC_SLING_TARGET injection)
- Formula instantiation + graph-workflow decoration (these are
  inseparable -- `instantiateSlingFormula` calls `applyGraphRouting`
  which calls `decorateGraphWorkflowRecipe`)
- Core routing: single-bead dispatch, batch/convoy expansion
- Post-routing: metadata writes, auto-convoy creation, controller
  poke, nudge

**Sub-phases (sequential):**
- 1a: Extract formula instantiation + graph-workflow decoration as
  a unit (`instantiateSlingFormula`, `applyGraphRouting`,
  `decorateGraphWorkflowRecipe`, and related helpers at
  `cmd_sling.go:1070+`). These are inseparable in the call chain.
- 1b: Extract sling context building (`resolveRigPaths`, store/
  store-ref selection, runner env setup) into ops. Also move
  `ConvoyFields` from `cmd/gc/convoy_fields.go` (sling creates
  auto-convoys).
- 1c: Extract single-bead sling (`doSling` -> `ops.Sling`) and
  batch sling (`doSlingBatch` -> `ops.SlingBatch`), using the
  helpers from 1a and context from 1b.
- 1d: Update `handler_sling.go` to call `ops.Sling` directly,
  remove subprocess delegation. Verify end-to-end.

**Agent resolution during Phase 1:** Sling needs to resolve
targets. Extract just enough resolution to make sling work (the
dispatch-target mode). Full three-mode unification is Phase 3.

**Dependency struct:** Derived from the existing `slingDeps` struct
at `cmd_sling.go:158`:
```go
type SlingDeps struct {
    Cfg       *config.City
    CityName  string
    CityPath  string
    Store     beads.Store
    StoreRef  string
    SP        runtime.Provider
    Recorder  events.Recorder     // from EventProvider()
    Runner    func(dir, command string, env map[string]string) (string, error)
    Poke      func()              // trigger immediate reconciliation
}
```

**Result struct includes user-facing info:**
```go
type SlingResult struct {
    BeadID     string
    Target     string
    WorkflowID string            // if formula-based
    ConvoyID   string            // if auto-convoy created
    Warnings   []string          // cross-rig, suspension, etc.
    Messages   []string          // "Slung X -> Y", etc.
}
```

**Critical files:**
- `cmd/gc/cmd_sling.go` (~2040 lines) -- all sling business logic
- `internal/api/handler_sling.go` -- subprocess delegation to remove
- `cmd/gc/convoy_fields.go` -- ConvoyFields (moved during 1b)

### Phase 2: Convoy Operations

**Why second:** Convoy operations are used by both CLI and API, and
there's a concrete bug to fix (API drops lifecycle events). Sling
also creates auto-convoys, so convoy ops complement Phase 1.

**Dependency struct:**
```go
type ConvoyDeps struct {
    Cfg       *config.City
    GetStore  func(rig string) (beads.Store, error)
    FindStore func(beadID string) (beads.Store, error)
    Recorder  events.Recorder
}
```

**Store resolution contract:** Adapters must resolve the convoy's
home store first and error on ambiguous IDs (same convoy ID in
multiple stores). The API's current "first match wins" behavior is
a bug to fix, not preserve. Ops receives a concrete resolved store
for the convoy; `FindStore` handles cross-store item lookup.

**Multi-consumer operations (extract first):**
- `ConvoyCreate(deps, store, input) -> (ConvoyCreateResult, error)`
- `ConvoyProgress(deps, store, id) -> (ConvoyProgress, error)`
- `ConvoyAddItems(deps, store, id, items) -> error` -- uses
  `FindStore` for cross-store items
- `ConvoyClose(deps, store, id) -> error`

**Single-consumer operations (extract when touched):**
- `ConvoyAutoCloseAll` -- needs `AllStores()` in deps
- `ConvoyLand`, `ConvoyRemoveItems`

**Critical files:**
- `cmd/gc/cmd_convoy.go` -- CLI convoy commands
- `internal/api/handler_convoys.go` -- API convoy handlers

### Phase 3: Agent Resolution

Three resolution modes in the codebase, unified via options:

```go
type ResolveOpts struct {
    // CLI dispatch: RigContextDir set, AllowPoolMembers true.
    // When set, resolver tries rig-scoped agent FIRST (contextual
    // preference), then falls back to literal/bare-name match.
    RigContextDir string

    // API session creation: TemplateOnly true.
    // Configured templates only. No ambient rig qualification.
    // No pool-member synthesis. Rejects bare names unless
    // city-unique.
    TemplateOnly bool

    // Dispatch modes (CLI + API sling): AllowPoolMembers true.
    // Allows "-N" suffixed pool instance synthesis.
    AllowPoolMembers bool
}

func ResolveAgent(cfg, input string, opts ResolveOpts)
    -> (config.Agent, error)
```

**Note:** Partial extraction happens during Phase 1 (sling needs
dispatch-target resolution). Full unification here.

**Critical files:**
- `cmd/gc/cmd_agent.go` -- CLI resolution (line 51+)
- `internal/api/agent_resolution.go` -- API template resolution
- `internal/api/handler_sling.go` -- API sling dispatch resolution

### Phase 4: Pool Expansion and Status Helpers

Extract shared pool expansion. Agent state derivation stays in
callers (intentionally different taxonomies). Current pool discovery
has a known TODO about session-name prefix matching -- treat the
extracted helper as a compatibility shim, not the long-term contract.

**Operations:**
- `ExpandAgents(cfg, sp, quarantineCheck) -> []AgentInfo` -- returns
  lower-level facts (running, draining, activeBead, lastActivity)

**Critical files:**
- `cmd/gc/pool.go` -- CLI pool expansion
- `internal/api/handler_agents.go` -- `expandAgent()` to move

### Phase 5+: Remaining Domain Logic

All remaining business logic migrates to ops, prioritized by
complexity and impact:
- Agent suspend/resume (needs config-editor abstraction)
- Session lifecycle orchestration (beyond `session.Manager`)
- Mail orchestration, city lifecycle, order/formula operations
- Single-consumer operations (convoy autoclose/land, etc.)

The extraction is largely mechanical -- move functions, adjust
signatures to return structs instead of writing to I/O, update
callers. Business logic doesn't change.

---

## Verification

After each phase:
1. `go test ./...` -- all tests pass
2. `go vet ./...` -- clean
3. Every exported function has a doc comment
4. Tests cover happy path AND edge cases
5. `go test ./internal/ops/...` -- ops tests pass in isolation
6. Automated dependency-direction check:
   ```go
   // internal/ops/deps_test.go
   func TestNoCrossLayerImports(t *testing.T) {
       // verify internal/ops imports do not include
       // internal/api or cmd/gc
   }
   ```
7. Manual smoke test of affected CLI commands and API endpoints

Phase 1: verify `handler_sling.go` calls `ops.Sling()` directly
and no longer shells out to `gc sling`.

## Scope for First Implementation

**Implement Phase 1 (Sling) only.** Start with sub-phase 1a
(formula + graph-workflow helpers), proceed through 1b-1d. This
eliminates the subprocess anti-pattern, establishes `internal/ops`
with substantial content, and proves the pattern with the meatiest
target in the codebase.
