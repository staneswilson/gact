# ADR-005: Cache-Key Schema Versioning Policy

- **Status:** Accepted
- **Date:** 2026-05-28
- **Deciders:** Stanes Wilson

## Context

`gact`'s step cache is content-addressed: a step's result is keyed on a
SHA-256 over (action SHA or run-body, `with:` inputs as canonical JSON, env
subset, declared input file hashes). A cache hit means: "this exact step
has been run before on this exact input, so we will replay its outputs
instead of executing it."

The honesty commitment of `gact` (plan §1) makes false-positive cache hits a
release-blocker class of bug. If we change what goes into the key — to
include a previously-missed input, or to fix a normalisation bug — old
entries computed under the prior rules become silently incorrect.

We need a mechanism that makes such changes safe by construction.

## Decision

Every cache key is computed over a canonical input that begins with a
`schema=N` line. The current schema number lives in a Go constant:

```go
// internal/caching/key.go
const CacheKeySchema = 1
```

Any change to the key computation that could affect the resulting hash for
the same logical step **must** bump `CacheKeySchema`. Old entries are not
migrated — they become unreachable and the LRU eviction logic reclaims
them.

A major-version bump of `gact` itself automatically counts as a key change
(the `gact` version string is also included in the hashed input), so users
who upgrade across majors automatically invalidate prior caches without
needing the schema bump to be explicit.

## Consequences

### Positive

- Bug fixes that change key inputs are safe: ship the fix with a schema
  bump; old entries become unreachable; no false-positive hits possible.
- The schema constant is grep-able. Every change to the file that defines
  it surfaces in code review as a key-affecting change.
- LRU handles cleanup. No migration code. No "if old format then ..." paths.

### Negative

- Schema bumps invalidate every user's cache for that machine. For users
  with many large cache entries (e.g. Node action `node_modules`), the
  first run after a bump pays full execution cost. This is the correct
  cost — the alternative is silent incorrectness.
- The schema constant is one more thing reviewers must remember to bump.
  Mitigated by: (a) we will lint for changes to the key file that do not
  also change the constant (a per-file CI check, added when we hit the
  first real bump), and (b) cache-key changes are rare in steady state.

## Alternatives Considered

### Alternative 1: No schema — just hash everything

Rejected because we cannot retroactively fix a bug in the key inputs
without potentially returning stale results to users who upgrade. Schema
versioning makes the fix safe.

### Alternative 2: Migrate old cache entries on key changes

Rejected as YAGNI. The cost of migration code exceeds the cost of
re-execution. The cache is a performance optimisation, not durable state.

### Alternative 3: Include the `gact` build commit hash in every key

Rejected because dev builds would never hit cache across each other, making
local development painful. We include the `gact` semver version, not the
commit hash — minor and patch releases share keys; majors do not.

## References

- Plan §2.5, §6.4, Task 1.11.
- Design spec §6.4 — step cache description.
