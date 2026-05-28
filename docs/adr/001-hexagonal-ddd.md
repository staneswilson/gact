# ADR 001: Hexagonal Architecture with Domain-Driven Bounded Contexts

**Status:** Accepted
**Date:** 2026-05-28
**Deciders:** Stanes Wilson

## Context

`gact` runs GitHub Actions workflows locally across four drivers (CLI, LSP,
daemon, git pre-push hook), seven distinct concerns (authoring, resolution of
remote actions, event-based selection, scheduling, tiered execution, caching,
reporting), and a long tail of I/O adapters (yaml, github, git, filesystem,
process, node, keychain, container runtimes, embedded services, cloud
dispatch). The design spec (`docs/specs/2026-05-28-gact-design.md`) and plan
§3-4 describe a hexagon implicitly — this ADR makes it explicit.

A naive layered architecture fails this shape. The drivers must behave
identically (same hexagon, different shell), so driver code cannot accumulate
domain logic. Tier executors are peers, not a hierarchy, so the scheduler
must not know what a tier is. The public IR must be semver-stable from v1.0,
so domain types must not leak adapter types (see [ADR-003](003-pkg-stability.md)).

## Decision

We organise the codebase as a **hexagonal (Ports and Adapters) architecture**
with **seven Domain-Driven bounded contexts** in `internal/`: Authoring,
Resolution, Selection, Scheduling, Execution, Caching, Reporting. Cross-cutting
infrastructure (masking, secrets, logging, cancellation, config, hook
installation) lives alongside but is not a bounded context.

Ports are defined inside the hexagon, owned by the bounded context that
**consumes** them — not by the adapter that implements them. The `Tier` port
lives in `internal/execution/`, not in each tier adapter package. Adapters
live in `internal/adapters/<port>/` and are wired together only in the
`main()` composition root under `cmd/`.

The workflow IR is **three aggregates** (`Workflow`, `Job`, `Step`) referencing
each other by ID rather than one tree — matrix expansion becomes O(matrix-size)
small-struct allocation instead of clone-on-write of a large tree. The `Tier`
port is split into three interfaces (`Match`, `Execute`, `Capabilities`) to
keep interface segregation tight; the scheduler depends only on `StepRunner`.

Anti-corruption layers translate at every boundary: `yaml.Node` becomes
`SourceSpan` at the parser; GitHub API JSON becomes `ResolvedAction` at the
resolution adapter; Authoring hands Resolution `UsesRef` value objects, not
raw strings; Selection hands Scheduling `[]SelectedJob`; Scheduling hands
Execution `RunnableStep`. Library types die at the adapter boundary.

## Consequences

**Positive:**
- Drivers are interchangeable: CLI, LSP, daemon, and git-hook wire the same
  application services through different shells. The daemon's JSON-RPC
  interface is generated from the application service interface — drift
  between in-process and daemon paths is structurally impossible.
- New tiers (e.g. a future T8 firecracker tier) are purely additive: write
  one adapter implementing the three `Tier` interfaces, register it.
- The custom `archlint` (plan §2.5) enforces invariants in CI: `pkg/`
  cannot import `internal/`; domain packages cannot import `os/exec` or
  `net/http`.

**Negative / trade-offs accepted:**
- More indirection than a flat layout. New contributors must learn the
  context map before landing non-trivial changes.
- A port + adapter + contract test per concern is verbose for trivial cases.

**Neutral:**
- The seven-context list is fixed for v1.0. Adding an eighth requires an ADR.

## Alternatives considered

### Flat layered architecture (controllers / services / repositories)

Rejected because tiered execution does not fit the layered model — tiers are
peers, not a stack. Forcing them into a layered model would make the scheduler
aware of tier identity (a `switch t.Kind`) instead of dispatching through a
port, killing the open/closed property that lets new tiers be added without
touching the scheduler.

### Clean Architecture (entities / use cases / interface adapters / frameworks)

Rejected as roughly isomorphic to hexagonal but with more prescriptive layer
naming. We use the hexagonal vocabulary because it maps more cleanly onto "the
daemon is just another driver" — a central design promise (plan §4.4).

### Service-oriented split (one process per context)

Rejected as a YAGNI violation. `gact` is a single binary that must start in
under 50 ms (plan §6.7). Splitting into processes would dominate cold start
and force protocol design between contexts that today share Go types.
