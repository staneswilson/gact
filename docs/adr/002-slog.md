# ADR-002: Use `log/slog` (stdlib) for Structured Logging

- **Status:** Accepted
- **Date:** 2026-05-28
- **Deciders:** Stanes Wilson

## Context

`gact` needs structured logging for:

1. Run-ID correlation across drivers, application services, and adapters.
2. CI-friendly JSON output (when `GACT_LOG=json`) and human-friendly text
   output by default.
3. Predictable behaviour under cancellation, with no log emission on the hot
   path of YAML parsing or process spawn.

The dominant third-party choices in Go are `uber-go/zap`, `rs/zerolog`, and
`sirupsen/logrus`. The stdlib option is `log/slog`, stable since Go 1.21.

Logging is not on `gact`'s hot path — the hot path is parsing, expression
evaluation, and process spawn. Allocation rate of the logger is therefore
not a release-blocker concern.

## Decision

We use **`log/slog` from the standard library** as the only logging package
in `gact`. Handlers chosen at startup based on `GACT_LOG`: `JSONHandler` to
stderr when `GACT_LOG=json`, `TextHandler` to stderr otherwise. Every log
record carries a `run_id` attribute (UUIDv7, generated at request entry,
propagated through `context.Context`).

We expose no logger as a package-level singleton in domain packages. The
logger is injected via constructor on each application service. Adapters
that perform I/O receive the logger if they need it.

## Consequences

### Positive

- Zero third-party deps for logging — `pkg/` cannot accidentally expose a
  third-party logger type (since `pkg/` is semver-stable per ADR-003).
- `slog` is stable across Go versions back to 1.21 — no churn risk.
- API is familiar enough that contributors writing their first `gact` patch
  do not need to learn a new logging framework.
- Custom handlers (e.g. one that wraps emission through the masking pipeline)
  are trivial to write against the `slog.Handler` interface.

### Negative

- `slog`'s allocations are higher than `zap`'s sugared API by ~2-3x. Not a
  concern on the cold path; if it becomes a concern, we can write a thin
  `slog.Handler` that batches.
- `slog` lacks built-in sampling. If we ever need it, we add a wrapping
  handler — not a structural change.

## Alternatives Considered

### Alternative 1: `uber-go/zap`

Zap is the fastest mainstream Go logger. Rejected because (a) logging is not
on `gact`'s hot path, (b) zap's API differs sharply from stdlib, increasing
the learning curve for contributors, (c) we would have to be careful not to
let `zap.Logger` types leak into `pkg/` types.

### Alternative 2: `rs/zerolog`

Similar trade-offs to zap with a different API. Rejected for the same
reasons. Zerolog's chainable builder pattern conflicts with the structured
`slog.Attr` model that handlers expect.

### Alternative 3: A custom logger written in-house

Rejected as a YAGNI violation. `slog` covers our needs and we gain nothing
from inventing one.

## References

- Plan §6.1.
- `log/slog` documentation: https://pkg.go.dev/log/slog
