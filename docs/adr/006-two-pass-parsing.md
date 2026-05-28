# ADR-006: Two-Pass Parsing for Sub-Second Cold Start

- **Status:** Accepted
- **Date:** 2026-05-28
- **Deciders:** Stanes Wilson

## Context

The product's headline promise is that the pre-push hook reports go/no-go
in sub-second cold start. The Selection bounded context (plan §3.3) must
decide which workflows GitHub would actually trigger for a push — which
requires reading every workflow under `.github/workflows/`.

A naive implementation full-parses every YAML file, expands matrices,
resolves expressions, and only then checks `on:`, `paths:`, and
`paths-ignore:`. On a monorepo with 30+ workflows and many matrices, this
exceeds the 200 ms `gact list` budget and the 500 ms `gact lint` budget
defined in plan §6.7.

But most workflows in a repo are irrelevant to any given push (their
`paths:` filter does not match). Doing full work on irrelevant workflows
violates the principle of paying only for what you use.

## Decision

The Authoring bounded context exposes two materialiser functions:

```go
// MaterialisePartial: parse only the YAML map keys we need for selection.
// Reads `name`, `on:` (triggers with paths/paths-ignore/branches/tags),
// and stops. Does NOT parse `jobs:`, `env:`, matrices, expressions.
func MaterialisePartial(path string, src []byte) (wf.Workflow, error)

// MaterialiseFull: full parse, schema validation, matrix expansion,
// static expression resolution, DAG construction. Called only on
// workflows that survived selection.
func MaterialiseFull(path string, src []byte, ctx ResolutionContext) (wf.Workflow, error)
```

The Selection context calls `MaterialisePartial` on every workflow, filters
by trigger and paths against the current event, then hands the surviving
workflow paths to a step that calls `MaterialiseFull` on each.

The two outputs share the same `wf.Workflow` value type — `MaterialisePartial`
leaves `JobsByID == nil` (or empty) and zero-values for other unparsed
fields.

## Consequences

### Positive

- On a typical pre-push event that touches 1 service in a monorepo, only
  the 1-2 workflows whose `paths:` filter matches incur full parse cost.
  The other 28 workflows pay only the partial-parse cost (~1 ms per file).
- Cold start meets the sub-second budget even on large repos.
- The split surfaces a useful design invariant: anything below the trigger
  level that affects selection is a bug. (We never need `jobs:` content
  to decide whether to trigger a workflow.)

### Negative

- Two parse paths must be kept in sync — if `on:` gains a new sub-key in
  the GitHub schema, both paths must handle it. Mitigated by: the partial
  parser only reads `on:` and `name:`, so the surface is narrow.
- A `wf.Workflow` returned from `MaterialisePartial` is incomplete. Callers
  must know which fields are populated. Documented on the function, and
  the type-level distinction is intentional (no separate `PartialWorkflow`
  type so call sites stay simple).

## Alternatives Considered

### Alternative 1: Always full-parse, optimise the YAML parser

Rejected. Even a hand-rolled parser cannot beat skipping work entirely.
The fastest YAML parse is the one we do not do.

### Alternative 2: Cache the materialised IR on disk between invocations

Rejected as YAGNI for v1. A persistent IR cache adds invalidation
complexity (what if the YAML changed? what if `gact` updated and the IR
shape changed?). Two-pass parsing alone meets the budget; we can revisit
caching if profiling reveals it as the bottleneck.

### Alternative 3: Two separate types — `PartialWorkflow` and `Workflow`

Rejected because it makes the Selection → Scheduling handoff awkward.
Selection produces a `[]SelectedJob` which already carries the full
`Workflow` it came from after the full-parse pass. Using one type with
partially-populated fields keeps the value flow simple.

## References

- Plan §3.3, §3.7, Task 0.13.
- Performance budgets: plan §6.7.
