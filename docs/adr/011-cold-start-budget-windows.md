# ADR 011: Cold-Start Budget — Platform-Specific Floor on Windows

**Status:** Accepted
**Date:** 2026-05-28
**Deciders:** Stanes Wilson

## Context

Plan §6.7 declares a 50 ms p95 cold-start budget for `gact` invoked with a
no-work leaf command (`gact version`). The budget was set when the plan was
written without measured input from any specific operating system; in
practice it anchored on the Linux process model.

`test/perf/cold_start_test.go` (Task 0.19) implements the budget as a
regression gate: 11 timed invocations of the pre-built binary, p95 by
nearest rank, fail-on-breach. After implementing the test and adding a
warmup pass to the timing helper (to eliminate the first-invocation
OS-image-cache outlier), measurement on this Windows 11 development host
produces a tight, repeatable distribution:

```
sorted (n=11): 43.6  47.2  51.2  51.6  52.2  53.7  54.5  55.3  58.0  58.1  59.7   ms
p50 = 53.7 ms
p95 = 59.7 ms
```

The p50 itself is already over the 50 ms budget and the minimum sample
(43.6 ms) leaves no headroom for the gate to be meaningful. This is not a
noise problem — the variance is ~6 ms across 11 samples, which is normal
OS-scheduler jitter.

Profiling with `GODEBUG=inittrace=1` shows the Go init chain reaches the
binary's `main()` at ~11 ms. The init breakdown:

- `runtime` and stdlib init through `os`: ~3.5 ms
- `crypto/internal/fips140/*` chain (pulled in by `crypto/sha256`): ~1 ms
- `net` package init (pulled in by `spf13/pflag` for IP-typed flag
  support): **4.4 ms**
- `gopkg.in/yaml.v3`, `regexp`, `text/template`, `spf13/cobra`,
  `gact/cmd`: ~2 ms combined

The remaining ~40–50 ms between process invocation and Go runtime startup
is OS-level cost — Windows CreateProcess, PE loader, kernel image-cache
fill — and runs before any `init()` we control. On Linux this same window
is typically ~5–15 ms; on Windows it is 30–50 ms. This is documented
Windows behaviour and matches the plan's own warning in
`cold_start_test.go`:

> *If this test fails on a platform with high process-spawn overhead
> (Windows in particular pays for fork-equivalent + image load), the
> remedies in priority order are:*
> 1. *Profile gact's package `init()` chain ...*
> 2. *Audit imports for transitive dependencies that pull in heavy
>    packages ...*
> 3. *As a last resort, document the platform-specific budget in ADR
>    form.*

Remedies 1 and 2 were executed. Removing `net` would require forking
`spf13/pflag` to strip IP-typed flag support; this is a poor cost/benefit
trade for ~4 ms and forks a foundational dependency. Removing
`gopkg.in/yaml.v3` from the version command's init path is feasible (lazy
package import) but recovers only ~0.5 ms. The realistic optimisation
ceiling within user code is ~5 ms — bringing p95 to ~55 ms, still over
budget. The remaining cost is structural, not algorithmic.

## Decision

Adopt platform-stratified cold-start budgets. The unified 50 ms budget in
plan §6.7 was Linux-anchored; we make that anchor explicit by splitting:

- **Linux / macOS:** 50 ms p95 cold start. Unchanged from plan §6.7.
- **Windows:** 100 ms p95 cold start. Recognises the ~30–50 ms structural
  process-spawn cost on the platform.

The constant in `test/perf/budgets.go` (`BudgetColdStart`) selects the
platform-appropriate value via `runtime.GOOS`. The constant's doc comment
points at this ADR so any future raise of either budget is visible to
reviewers and not silent.

The 100 ms Windows budget remains a tight gate: it allows the current
profile of 60 ms p95 with ~40 ms headroom, which is enough to catch a
regression (e.g. a new `init()` that does I/O, a new transitive import of
`net/http`, a cobra completion that registers eagerly) without flaking on
ordinary OS jitter. If profiling later shows the realistic ceiling is
lower than 100 ms, this ADR is superseded by a new one that tightens.

The other two P0 budgets (`gact list` ≤ 200 ms, `gact lint` ≤ 500 ms) are
unchanged. They measure work-doing, not OS process-spawn cost, and clear
their budgets on Windows with several-fold headroom (current measurements:
list p95 = 66 ms, lint p95 = 97 ms).

## Consequences

**Positive:**
- The cold-start gate stays meaningful on Windows. A 50 ms gate that
  cannot be cleared even by a perfectly-optimised binary teaches the team
  to ignore the test or weaken its assertions — both worse than
  acknowledging the platform floor.
- The split surfaces a real product fact: Windows pre-push UX has a ~50 ms
  per-invocation floor that no amount of engineering inside `gact` can
  remove. Phase 2 (pre-push integration) will need to decide whether
  multiple `gact` invocations during a single `git push` should be
  amortised behind the daemon driver (plan §4.4, ADR-008 when written).
- The init-chain audit (`GODEBUG=inittrace=1` results above) is now
  captured for future reference. If the chain grows, the diff is
  observable against this baseline.

**Negative / trade-offs accepted:**
- Two budgets to maintain instead of one. The risk is that a
  Linux-anchored regression sneaks under the Windows budget and ships.
  Mitigated: CI runs `go test ./test/perf/...` on the OS × arch matrix
  (plan §7.9), so a Linux regression that would breach 50 ms is caught by
  the Linux job, not the Windows job.
- The headline product claim "sub-second pre-push" is unaffected (the
  end-to-end pre-push budget is dominated by lint and selection, not
  cold-start), but anyone benchmarking `gact version` in isolation on
  Windows will see ~60 ms and should not be surprised.

**Neutral:**
- The split mirrors how other Go CLIs that target Windows have handled
  the same constraint (e.g. `gh`, `kubectl` both pay >50 ms cold-start on
  Windows and do not gate on it). We are explicit about the cost rather
  than implicit.

## Alternatives considered

### Keep the unified 50 ms budget and skip the Windows assertion

Rejected. Skipping the assertion on Windows means a Windows-specific
regression (e.g. a new init that reads from `%APPDATA%`) sails through
unchallenged. The Linux assertion would still pass and the breach would
only surface as user complaints. The point of a perf gate is to catch
regressions where they happen.

### Fork `spf13/pflag` to drop `net` and recover 4.4 ms

Rejected. Forking a foundational CLI dependency for 4.4 ms of init time
is a poor trade — it adds upstream-sync burden indefinitely and would
need to be re-evaluated every pflag release. The remaining ~30–50 ms is
still OS-level and untouched by the fork. The math does not work even at
zero maintenance cost.

### Refactor the version command to a separate static binary

Rejected. Producing a second binary (`gact-version` or similar) just to
clear a perf gate is the wrong shape of fix — the gate exists to validate
the *real* startup path, not a contrived one. Users invoke
`gact version` against the real binary; the test must measure the same.

### Adopt a single 100 ms budget for both platforms

Rejected. The Linux floor is genuinely ~15 ms with current code; setting
the budget at 100 ms loses 85 ms of headroom on Linux and would mask a
real regression there. Platform-stratified budgets cost only the small
complexity of a `runtime.GOOS` switch.
