# ADR-001: Hexagonal Architecture with Domain-Driven Bounded Contexts

- **Status:** Accepted
- **Date:** 2026-05-28
- **Deciders:** Stanes Wilson

## Context

`gact` runs GitHub Actions workflows locally across four drivers (CLI, LSP,
daemon, git pre-push hook), seven distinct concerns (authoring, resolution of
remote actions, event-based selection, scheduling, tiered execution, caching,
reporting), and a long tail of I/O adapters (yaml, github, git, filesystem,
process, node, keychain, container runtimes, embedded services, cloud
dispatch).

Naive layered architectures fail this shape because: (1) the drivers must
behave identically — same hexagon, different shell — so we cannot let driver
code accumulate domain logic; (2) the tier executors are peers, not a
hierarchy, so the scheduler must not know what a tier is; (3) the public IR
must be semver-stable from v1.0, so domain types must not leak adapter types.

The design spec (`docs/specs/2026-05-28-gact-design.md`) describes a hexagon
implicitly — this ADR makes it explicit and binds the rest of the codebase to
it.

## Decision

We organise the codebase as a **hexagonal (Ports & Adapters) architecture**
with **seven Domain-Driven bounded contexts** in `internal/`. Ports are
defined inside the hexagon, owned by the bounded context that consumes them.
Adapters live in `internal/adapters/<port>/` and are wired together only in
the `main()` composition root under `cmd/`.

The seven bounded contexts are: Authoring, Resolution, Selection, Scheduling,
Execution, Caching, Reporting. Cross-cutting infrastructure (masking,
secrets, logging, cancellation, config, hook installation) lives alongside
but is not a bounded context in the DDD sense.

The workflow IR is three aggregates (`Workflow`, `Job`, `Step`) referencing
each other by ID rather than a single tree — this makes matrix expansion an
O(matrix-size) allocation of small structs instead of clone-on-write of a
large tree.

The `Tier` port is split into three interfaces — `Matcher`, `Executor`,
`Describer` — to keep interface segregation tight; the scheduler depends only
on `StepRunner`, which is satisfied by the tier selector.

## Consequences

### Positive

- Drivers are interchangeable: CLI, LSP, daemon, and git-hook all wire the
  same application services through different shells. The daemon's JSON-RPC
  interface can be generated from the application service interface — drift
  between in-process and daemon paths is structurally impossible.
- New tiers (e.g. a future T8 firecracker tier) are purely additive: register
  a new adapter implementing the three Tier interfaces.
- Anti-corruption layers (ACLs) at every context boundary mean GitHub API
  types die at the resolution adapter; `yaml.Node` dies at the parser
  boundary; library types never reach the domain.
- The custom `archlint` enforces the most important invariants in CI:
  `pkg/` cannot import `internal/`; domain packages cannot import `os/exec`
  or `net/http`.

### Negative

- More indirection than a flat layout. New contributors must learn the
  context map before they can land non-trivial changes.
- More files: a port + adapter + contract test per concern is verbose for
  trivial concerns.
- Risk of over-decomposition. Mitigated by keeping the context list small
  (seven) and only adding ports where there is a real I/O or substitutability
  need.

## Alternatives Considered

### Alternative 1: Flat layered architecture (controllers / services / repositories)

Rejected because tiered execution does not fit the layered model — tiers are
peers, not a stack. Forcing them into a layered model would make the
scheduler aware of tier identity (a `switch t.Kind`) instead of dispatching
through a port.

### Alternative 2: Clean Architecture (entities / use cases / interface adapters / frameworks)

Rejected as roughly isomorphic to hexagonal but with more prescriptive layer
naming. We use the hexagonal vocabulary because it maps more cleanly onto
"the daemon is just another driver" — a central design promise.

### Alternative 3: Service-oriented split (one process per context)

Rejected as a YAGNI violation. `gact` is a single binary that should start
in under 50 ms. Splitting into processes would dominate cold start.

## References

- Plan: `docs/superpowers/plans/...` (`we-have-a-plan-streamed-cherny.md`), §3 and §4.
- Design spec: `docs/specs/2026-05-28-gact-design.md` §5.
- Alistair Cockburn, "Hexagonal Architecture", 2005.
- Eric Evans, *Domain-Driven Design*, 2003 — bounded contexts, anti-corruption layers.
