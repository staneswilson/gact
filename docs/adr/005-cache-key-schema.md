# ADR 005: Cache-Key Schema Versioning Policy

**Status:** Accepted
**Date:** 2026-05-28
**Deciders:** Stanes Wilson

## Context

`gact`'s step cache is content-addressed: a step's result is keyed on a
SHA-256 over a canonical input bundle. A cache hit means: "this exact step
has been run before on this exact input, so we will replay its outputs
instead of executing it." Plan §2.5 declares the schema-versioning invariant;
plan §6.7 defines the perf budgets (cached 5-job run ≤ 1 s p95) that the
cache exists to meet; Task 1.11 implements the key computer.

The honesty commitment of `gact` (plan §1) makes false-positive cache hits
a release-blocker class of bug. If we change what goes into the key — to
include a previously-missed input, or to fix a normalisation bug — old
entries computed under the prior rules become silently incorrect. A
disciplined versioning mechanism is required so any such change is safe
by construction rather than safe by careful review.

## Decision

Every cache key is computed over a canonical input bundle that begins with
the schema version. The current schema lives in a Go constant in the
caching context:

```go
// internal/caching/key.go
const CacheKeySchema = 1
```

The hashed inputs are, in fixed order:

1. `schema=N` (the `CacheKeySchema` constant).
2. `gact=<version>` (the running `gact` semver — bumping the major version
   automatically invalidates all prior cache entries).
3. The action ref resolved to a SHA (for `uses:` steps) or the literal
   `run:` body bytes (for shell steps).
4. The `with:` inputs serialised as canonical JSON (sorted keys, no
   whitespace variance).
5. The env subset visible to the step: sorted keys, escaped values, only
   the keys the step actually reads or whose presence affects the action.
6. Declared input-file hashes — paths the step or action manifest names
   as inputs, hashed under their canonical path form.

Any change to the key computation that could affect the resulting hash for
the same logical step **must** bump `CacheKeySchema`. Old entries are not
migrated — they become unreachable, and the LRU cleanup logic reclaims
them on the next `gact cache gc` or natural eviction at `max_size_gb`.

A major-version bump of `gact` itself counts as a key change (the version
string is in input 2), so users who upgrade across majors automatically
invalidate prior caches without the schema constant needing to move.

## Consequences

**Positive:**
- Bug fixes that change key inputs are safe: ship the fix with a schema
  bump; old entries become unreachable; no false-positive hits possible.
- The schema constant is grep-able. Every change to the file that defines
  it surfaces in code review as a key-affecting change.
- LRU handles cleanup. No migration code. No "if old format then …" paths.
- The `gact` major-version coupling means downstream users get a clean
  cache reset on every breaking release without thinking about it.

**Negative / trade-offs accepted:**
- Schema bumps invalidate every user's cache on that machine. For users
  with large entries (e.g. Node action `node_modules`), the first run
  after a bump pays full execution cost. This is the correct cost — the
  alternative is silent incorrectness, which violates the honesty
  commitment.
- One more thing reviewers must remember to bump. Mitigated by a per-file
  CI check on `internal/caching/key.go` (added when we hit the first real
  bump); cache-key changes are rare in steady state anyway.

**Neutral:**
- The `run_id` (UUIDv7, see [ADR-002](002-slog.md)) is included only in
  the cache-key prefix for ops debuggability, never in the hash itself.

## Alternatives considered

### No schema — just hash everything

Rejected because we cannot retroactively fix a bug in the key inputs
without potentially returning stale results to users who upgrade. Schema
versioning makes the fix safe; the absence of a version makes every input
change a potential silent-incorrectness incident.

### Migrate old cache entries on key changes

Rejected as YAGNI. The cost of migration code (translating old entries
into new-format entries while preserving correctness) exceeds the cost of
re-execution. The cache is a performance optimisation, not durable state —
re-derivability is the whole point.

### Include the `gact` build commit hash in every key

Rejected because dev builds would never hit cache across each other,
making local development painful. We include the `gact` semver version
(input 2), not the commit hash — minor and patch releases share keys;
majors do not.
