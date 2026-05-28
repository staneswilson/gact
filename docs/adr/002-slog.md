# ADR 002: Use `log/slog` (stdlib) for Structured Logging

**Status:** Accepted
**Date:** 2026-05-28
**Deciders:** Stanes Wilson

## Context

`gact` needs structured logging for run-ID correlation across drivers,
application services, and adapters; for CI-friendly JSON output when
`GACT_LOG=json` and human-friendly text otherwise; and for predictable
behaviour under cancellation. The dominant third-party choices are
`uber-go/zap`, `rs/zerolog`, and `sirupsen/logrus`. The stdlib option is
`log/slog`, stable since Go 1.21.

Plan §6.1 declares the choice; plan §2.2 (KISS) is the governing principle —
"stdlib over third-party where the perf delta is irrelevant." The hot path
in `gact` is **YAML parsing and process spawn**, not log emission. A run
that emits a few hundred log lines is dominated by `os/exec` and parser
allocation by orders of magnitude. Logger allocation rate is therefore not
a release-blocker concern.

## Decision

We use **`log/slog` from the standard library** as the only logging package
in `gact`. Handlers are chosen at startup based on the `GACT_LOG` environment
variable: `slog.NewJSONHandler(os.Stderr, …)` when `GACT_LOG=json` (CI mode);
`slog.NewTextHandler` otherwise. Every log record carries a `run_id` attribute
generated at request entry — a **UUIDv7** propagated through `context.Context`
so every emission in a single invocation correlates without manual plumbing.

The logger is injected via constructor on each application service. Adapters
that perform I/O receive the logger if they need it. No package-level
singletons in domain code. Levels follow the standard convention: `Debug`
(verbose path), `Info` (lifecycle events), `Warn` (parity warnings, tier
fallbacks), `Error` (operation failures). Secrets never reach the logger —
the masking pipeline (plan §6.5) wraps every writer that could see
secret-containing output.

## Consequences

**Positive:**
- Zero third-party deps for logging. `pkg/` cannot accidentally expose a
  third-party logger type, which matters because [ADR-003](003-pkg-stability.md)
  locks `pkg/` to two packages with no third-party signatures.
- `slog` is stable across Go versions back to 1.21 — no API churn risk over
  the v1.0 support window.
- Custom handlers (e.g. one that wraps emission through the masking pipeline)
  are trivial to write against the `slog.Handler` interface.
- Familiar enough that a contributor writing their first `gact` patch does
  not need to learn a new logging framework.

**Negative / trade-offs accepted:**
- `slog`'s allocations are higher than `zap`'s sugared API by ~2-3x. Acceptable
  because logging is not on the hot path; if it ever becomes one, a batching
  `slog.Handler` is additive, not structural.
- `slog` lacks built-in sampling. If we ever need it, we add a wrapping
  handler — not a framework migration.

**Neutral:**
- `run_id` (UUIDv7) is included in cache-key prefixes for ops debuggability
  (not in the hash itself — see [ADR-005](005-cache-key-schema.md)).

## Alternatives considered

### `uber-go/zap`

Zap is the fastest mainstream Go logger. Rejected because (a) logging is not
on `gact`'s hot path; (b) zap's API differs sharply from stdlib, increasing
the learning curve for contributors; (c) we would have to be careful not to
let `zap.Logger` types leak into `pkg/` signatures, adding ongoing review
burden for no offsetting benefit.

### `rs/zerolog`

Similar trade-offs to zap with a different API. Rejected for the same reasons.
Zerolog's chainable builder pattern conflicts with the structured `slog.Attr`
model that handlers expect, so any future migration would be more painful.

### A custom logger written in-house

Rejected as a YAGNI violation. `slog` covers our needs and we gain nothing
from inventing one. Per plan §2.2: do not pre-build for hypothetical needs.
