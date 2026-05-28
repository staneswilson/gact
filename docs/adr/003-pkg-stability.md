# ADR 003: `pkg/` is Semver-Stable; Only `workflow` and `expr` Exported

**Status:** Accepted
**Date:** 2026-05-28
**Deciders:** Stanes Wilson

## Context

Go projects that put everything under `pkg/` end up with a sprawling public
API surface they cannot evolve without breaking downstream consumers. The
standard escape hatch is `internal/`, which the Go toolchain refuses to let
outside callers import.

`gact` has third-party reasons for a stable public surface: IDE plugins and
editor integrations may want to consume the workflow IR; sibling tools
(actionlint successors, custom linters) may want to build on the expression
evaluator. The Definition of Done in plan §15 commits us to a v1.0 signature
lock. But every additional exported package becomes a future-compat liability,
so the surface must stay small.

Plan §2.5 declares the architectural invariant; plan §3 places `pkg/workflow`
under Authoring's public surface; plan §5 fixes the module layout. The plan
also describes a `database/sql`-driver-style registration pattern (plan §3.1):
`internal/authoring/expr` registers a compiler with `pkg/expr` at `init()`,
so consumers depend only on the public façade while the implementation can
evolve under `internal/`.

## Decision

`pkg/` is **semver-stable from the v1.0 tag** and contains exactly two
packages:

1. **`pkg/workflow`** — the IR value objects: `Workflow`, `Job`, `Step`,
   `SourceSpan`, `Expression`, `UsesRef`, `Matrix`, `Triggers`, and the
   `JobID` typedef. No methods that perform I/O. No interfaces. No
   third-party types. No pointers to internal types. Value objects only.
2. **`pkg/expr`** — the expression evaluator's public façade: `Evaluator`,
   `Value`, `Context`, plus the constructors `New(src string) (*Evaluator,
   error)` and `NoContext() Context`. The internal lexer/parser AST lives
   under `internal/authoring/expr` and never escapes.

The implementation registers with the façade at init() — `internal/authoring/expr`
calls `expr.RegisterCompiler(...)` in its package init, in the style of
`database/sql` drivers. Consumers import only `pkg/expr`; the linker pulls in
the registered implementation transitively. This keeps the implementation
fully under `internal/` while the call site stays public.

Adding a third package to `pkg/` requires a new ADR. Adding a third-party
type to a `pkg/` signature is forbidden. The custom architectural linter
`tools/archlint` (plan Task 0.2) enforces in CI that no file under `pkg/`
imports `internal/`.

## Consequences

**Positive:**
- Downstream consumers can rely on stable types — the IR shape will not
  break across minor releases.
- The surface area to think about during refactors is small. Almost all
  changes happen inside `internal/`, which carries no compatibility
  commitments.
- `archlint` catches accidental boundary violations in CI, not at release.
- The driver-style registration pattern lets us swap evaluator
  implementations entirely (e.g. a faster evaluator post-v1) without
  changing the public API.

**Negative / trade-offs accepted:**
- Convenience helpers that would be useful on `Workflow`/`Job`/`Step`
  (e.g. `Workflow.WalkSteps(func(Step))`) cannot live in `pkg/` if their
  callback shape might need revision. Such helpers live in
  `internal/authoring` instead.
- The `Expression` type in `pkg/workflow` is opaque to consumers — they
  get `Raw string` and `Span SourceSpan` only. Inspecting the parsed AST
  requires `pkg/expr`. Two-package coupling is intentional but visible.

**Neutral:**
- The two-package limit will be re-evaluated only via a new ADR, not via
  ad-hoc additions.

## Alternatives considered

### No `pkg/` — everything under `internal/`

Rejected because downstream consumers cannot import `internal/`. We have a
real use case for downstream consumption (IDE plugins, sibling tools), so
"no public API" is a non-starter.

### Many packages under `pkg/`

Rejected because every additional exported package is a future-compat
liability. `workflow` and `expr` are the two surfaces we can credibly commit
to keeping stable through v1.x. A `pkg/scheduler`, `pkg/caching`,
`pkg/reporting` etc. would all be plausible-looking but would lock us out
of architectural evolution under `internal/`.

### `pkg/` with no stability guarantee

Rejected as dishonest. Go convention treats `pkg/` as public surface;
documenting otherwise is a footgun for downstream users, and the moment a
consumer pins to a `pkg/` import we are locked anyway.
