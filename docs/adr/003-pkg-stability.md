# ADR-003: `pkg/` is Semver-Stable from v1.0; Only `workflow` and `expr` Exported

- **Status:** Accepted
- **Date:** 2026-05-28
- **Deciders:** Stanes Wilson

## Context

Go projects that put everything under `pkg/` end up with a sprawling public
API surface that they cannot evolve without breaking downstream consumers.
The standard escape hatch is `internal/`, which the Go toolchain refuses to
let outside callers import.

`gact` has third-party reasons for a stable public surface: (1) IDE plugins
and editor integrations may want to consume the workflow IR, (2) tools like
`actionlint` competitors or replacements may want to build on top of
`gact`'s expression evaluator, (3) the Definition of Done (plan §15) commits
us to a v1.0 signature lock.

But every additional exported package becomes a future-compat liability.

## Decision

`pkg/` is **semver-stable from the v1.0 tag** and contains exactly two
packages:

1. `pkg/workflow` — the IR value objects: `Workflow`, `Job`, `Step`,
   `SourceSpan`, `Expression`, `UsesRef`, `Matrix`, `Triggers` and the
   `JobID` typedef. No methods that perform I/O. No interfaces. No
   third-party types. No pointers to internal types. Value objects only.
2. `pkg/expr` — the expression evaluator's public façade: `Evaluator`,
   `Value`, `Context`, plus the constructors `New(src string) (*Evaluator,
   error)` and `NoContext() Context`. Internal lexer/parser AST types live
   under `internal/authoring/expr` and never escape.

Adding a third package to `pkg/` requires a new ADR. Adding a third-party
type to a `pkg/` signature is forbidden.

A custom architectural linter (`tools/archlint`) enforces in CI that no file
under `pkg/` imports `internal/`.

## Consequences

### Positive

- Downstream consumers can rely on stable types — the IR shape will not
  break across minor releases.
- The surface area to think about during refactors is small. Almost all
  changes happen inside `internal/`, which has no compatibility commitments.
- `archlint` catches accidental boundary violations in CI rather than at
  release time.

### Negative

- Some convenience methods that would be useful on `Workflow`/`Job`/`Step`
  (e.g. a `Workflow.WalkSteps(func(s Step))` helper) cannot live in `pkg/`
  if they would force us to expose a callback shape we may want to revise.
  Such helpers live in `internal/authoring` instead.
- The `Expression` type in `pkg/workflow` is opaque to consumers — they get
  `Raw string` and `Span SourceSpan` only. They cannot inspect the parsed
  AST without going through `pkg/expr`. This is intentional but does
  require consumers to use both packages together for any non-trivial work.

## Alternatives Considered

### Alternative 1: No `pkg/` — everything under `internal/`

Rejected because downstream consumers cannot import `internal/`. We have a
real use case for downstream consumption (IDE plugins, sibling tools).

### Alternative 2: Many packages under `pkg/`

Rejected because every additional exported package is a future-compat
liability. `workflow` and `expr` are the two surfaces we can credibly
commit to keeping stable.

### Alternative 3: `pkg/` with no stability guarantee (treat as `internal/` you happen to be able to import)

Rejected as dishonest. Go convention treats `pkg/` as a public surface;
documenting otherwise is a footgun for downstream users.

## References

- Plan §2.5, §5.
- Go module versioning: https://go.dev/ref/mod#major-version-suffixes
- `internal/` mechanism: https://go.dev/cmd/go/#hdr-Internal_Directories
