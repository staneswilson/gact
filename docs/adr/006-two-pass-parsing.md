# ADR 006: Two-Pass Parsing for Sub-Second Cold Start

**Status:** Accepted
**Date:** 2026-05-28
**Deciders:** Stanes Wilson

## Context

The product's headline promise is that the pre-push hook reports go/no-go
in sub-second cold start. Plan §6.7 fixes the perf budgets that operationalise
this promise: `gact list` ≤ 200 ms p95 on a 30-workflow repo, `gact lint`
≤ 500 ms p95 on the same corpus, `gact` cold start ≤ 50 ms p95 with no work.

The Selection bounded context (plan §3.3) must decide which workflows GitHub
would actually trigger for a given push — which requires reading every
workflow file under `.github/workflows/`. A naive implementation full-parses
every YAML file, expands matrices, resolves expressions, and only then checks
`on:`, `paths:`, and `paths-ignore:`. On a monorepo with 30+ workflows and
many matrices, this blows through the budgets above before any execution
work has started.

But most workflows in a repo are irrelevant to any given push: their `paths:`
filter does not match the changed files. Doing full work on irrelevant
workflows violates the principle of paying only for what you use. Task 0.13
is the implementation task for the materialiser; this ADR records why it
splits into two passes.

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

The Selection context calls `MaterialisePartial` on every workflow under
`.github/workflows/`, filters by trigger and paths against the current
event, then hands the surviving workflow paths to a step that calls
`MaterialiseFull` on each. Both functions return the same `wf.Workflow`
value type (see [ADR-001](001-hexagonal-ddd.md) on the three-aggregate IR);
`MaterialisePartial` leaves `JobsByID` empty and zero-values for other
unparsed fields. The single type makes the Selection → Scheduling handoff
trivial — by the time Scheduling sees a workflow, it has been fully
materialised.

## Consequences

**Positive:**
- On a typical pre-push event that touches one service in a monorepo,
  only the one or two workflows whose `paths:` filter matches incur full
  parse cost. The other 28 pay only the partial-parse cost (~1 ms per
  file).
- Cold start meets the sub-second budget even on large repos. The 200 ms
  `gact list` budget is reachable; the 500 ms `gact lint` budget has
  headroom for the static-analysis passes layered on top.
- The split surfaces a useful design invariant: anything below the
  trigger level that affects selection is a bug. We never need `jobs:`
  content to decide whether to trigger a workflow.

**Negative / trade-offs accepted:**
- Two parse paths must be kept in sync. If `on:` gains a new sub-key in
  the GitHub schema, both paths must handle it. Mitigated: the partial
  parser only reads `on:` and `name:`, so the surface is narrow and the
  divergence-risk is bounded.
- A `wf.Workflow` returned from `MaterialisePartial` is incomplete.
  Callers must know which fields are populated. Documented on the
  function; no separate `PartialWorkflow` type so call sites stay simple.

**Neutral:**
- Golden tests (plan §7.2) cover both passes against the same fixtures,
  so a schema drift between them shows up as a golden mismatch in CI.

## Alternatives considered

### Always full-parse, optimise the YAML parser

Rejected. Even a hand-rolled parser cannot beat skipping work entirely.
The fastest YAML parse is the one we do not do. Profiling on a 30-workflow
repo shows the full-parse cost is dominated by matrix expansion and
expression lexing, neither of which a faster YAML reader addresses.

### Cache the materialised IR on disk between invocations

Rejected as YAGNI for v1. A persistent IR cache adds invalidation
complexity (what if the YAML changed? what if `gact` updated and the IR
shape changed? — the same schema-versioning problem solved in
[ADR-005](005-cache-key-schema.md) but with a less-bounded blast radius).
Two-pass parsing alone meets the budget; we can revisit caching if
profiling later reveals it as the bottleneck.

### Two separate types — `PartialWorkflow` and `Workflow`

Rejected because it makes the Selection → Scheduling handoff awkward.
Selection produces `[]SelectedJob` carrying the full `Workflow` after the
full-parse pass. One type with partially-populated fields keeps the value
flow simple and the type signatures stable across the boundary.
