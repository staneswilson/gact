# ADR-004: Parity Classifier Has Three Categories — Noise, Warn, Block

- **Status:** Accepted
- **Date:** 2026-05-28
- **Deciders:** Stanes Wilson

## Context

The Phase-3 parity harness (plan §6.3, §7.10, Task 0.20 / Spike B) runs
each corpus workflow on real GitHub and locally via `gact run`, then
diffs the observable output line-by-line. Naked text diff is useless:
GitHub prefixes every log line with an ISO-8601 timestamp, RUNNER_NAME
and RUNNER_TEMP vary per run, service containers bind to random host
ports, and ANSI colour codes only appear on the GitHub side. Without a
classifier every harness run flags hundreds of pseudo-divergences and
either nobody reads the report (kills the product's honesty claim) or
the team papers over real failures because the signal-to-noise is too
low.

The classifier is the first thing the harness leans on. If it fires
too eagerly on noise, real divergences are buried; if it fires too
conservatively, real divergences are not reported. Spike B is a go /
no-go gate at the end of Phase 0: a working classifier on a 5-workflow
sample is a precondition to scaling the harness to 50 in Phase 3
(Task 3.9). The go criterion is ≥ 95% agreement with human judgement
on the 5-workflow sample.

## Decision

The parity classifier exposes exactly three categories, evaluated in a
fixed priority order: **Block > Noise > Warn**, with **Warn as the
default fallback** when no rule claims a diff.

1. **Noise (ignored).** Diffs that are demonstrably non-semantic.
   Stripped from parity reports and never escalate. The taxonomy at
   v1.0 is:

   - ISO-8601 timestamp drift in log prefixes.
   - `RUNNER_NAME` (GitHub hosted-pool agent id vs local host name).
   - `RUNNER_TEMP`, `RUNNER_TOOL_CACHE`, `GITHUB_WORKSPACE` scratch paths.
   - Service-container random host ports (`127.0.0.1:NNNNN` forwarding to
     the same container port).
   - ANSI CSI escape sequences (GitHub emits colour, local non-TTY does not).
   - Line-ending / trailing whitespace drift (CRLF vs LF).
   - `process.env` iteration order on a multi-line env dump.

2. **Warn (PR comment, not blocker).** Stdout text drift when exit codes
   match. The diff is real but the workflow's observable outcome is
   unchanged. Surfaces as a PR comment so a human can decide whether
   the drift matters. This is also the **default fallback**: any diff
   no rule claims is reported as `default-warn`, on the principle that
   we would rather surface an unfamiliar divergence to a human than
   silently drop it as noise or auto-file an issue.

3. **Block (opens an issue).** Diffs that materially change observable
   outcome. The taxonomy at v1.0 is:

   - Exit-code mismatch for the same step.
   - Job-level verdict flip (success ↔ failure).
   - Step skipped on one side but not the other (different `if:` eval).
   - Step-output `name=value` mismatch — same key, divergent value.

Rules are implemented in `test/parity/classifier/classifier.go` as a
package-level `Rules []Rule` slice, exported and mutable so future
work (Task 3.9) can append workflow-specific rules at `init()` time.
Each rule is a pure function of its `Diff` input. The classifier walks
`Rules` in order and returns the first match; this makes priority a
property of the slice ordering rather than rule bodies.

The agreement target is **≥ 95%** with human judgement on a 5-workflow
sample. The Spike B prototype hits this on hand-crafted synthetic
fixtures that exercise each sub-category from §7.10. Real workflow
capture lands in Task 3.9 once `gact run` exists.

## Consequences

### Positive

- A small, fixed taxonomy is easy to teach to a human reviewer: every
  PR comment from the harness reads as "this diff was classified as
  X because of rule Y". The reason token is part of `Result`, not a
  per-rule string the body fabricates.
- Block rules run first, so a Diff with both a noise condition (e.g.
  different timestamps) and a block condition (e.g. different exit
  codes) classifies as Block — the conservative direction.
- The default fallback is Warn, not Noise. An unfamiliar divergence
  surfaces to a human; the team is not surprised by silent
  noise-classification of a new failure mode.
- Rules are pure, so reordering, composition, fuzzing and unit
  testing are straightforward. The `Rules` slice can be extended at
  init time without recompiling consumers.
- The classifier sits under `test/` rather than `internal/` so it is
  not part of the shipping binary's compatibility surface. The same
  package becomes the harness's classification core in P3 without
  needing to migrate.

### Negative

- The hand-crafted fixtures used for Spike B are not real workflow
  captures. The 95% target is, for now, a target on representative
  synthetic data. The real agreement number will only land when the
  parity harness captures real workflows (plan Tasks 0.16-0.18 +
  Task 3.9). If reality disagrees with the synthetic fixtures, the
  rules need to be revised before harness scale-out.
- A rigid three-category model loses information that a
  finer-grained taxonomy could express. E.g. "step skipped because
  `if:` referenced a secret that exists remotely but not locally" is
  a more specific signal than the generic "step-skipped-one-side".
  We trade specificity for tractability at the agreement-rate level.
- The `output-value-mismatch` rule is necessarily fuzzier than the
  others: a step output line is syntactically indistinguishable from
  a `RUNNER_NAME=` assignment or an env-dump token. The rule yields
  on a fixed allow-list of noise-key prefixes. New noise keys must
  be added there explicitly — a maintenance burden but a small one.

## Alternatives Considered

### Alternative 1: A larger fixed taxonomy (e.g. five or seven categories)

Add explicit "metric" and "infra" between Noise and Warn so e.g. a
banner version bump is distinct from a stdout drift. Rejected: more
categories means more thresholds to argue about and a higher chance
that one of them is wrong. The three-category model maps cleanly to
the three escalation actions (ignore, PR comment, file issue); adding
a fourth category requires a fourth action with policy attached.

### Alternative 2: A statistical / ML classifier trained on hand-labelled diffs

Treat the classifier as a Bayesian or simple-NN classifier whose
input is line features and output is a probability over the three
categories. Rejected: the labelled corpus is tiny (5 workflows for
Spike B, 50 for Task 3.9) and the failure mode of a probabilistic
classifier on a tiny corpus is to be confidently wrong on rare cases
— exactly the cases we cannot afford to misclassify. Hand-rolled
regex / structural rules are auditable, debuggable, and trivially
exception-able.

### Alternative 3: Block as the default fallback (conservative-block)

Treat any unrecognised divergence as a Block and auto-open an issue.
Rejected: the harness would spam the issue tracker every time a new
log format appeared in a marketplace action. We would either disable
auto-filing (defeating the purpose) or normalise opening junk issues.
Default-warn surfaces the new divergence to a human as a PR comment;
the human can then add a rule, escalate to block, or dismiss.

### Alternative 4: Noise as the default fallback (conservative-noise)

The opposite trade-off: silently drop anything we don't recognise.
Rejected for the same reason §7.10 calls "the product's honesty
claim" — silently dropping divergences makes the harness useless as
a signal source. The whole point of Spike B is that we cannot ship a
parity harness whose default behaviour is "ignore".

## References

- Plan §6.3 (parity-as-test-suite), §7.10 (classifier categories), §8
  (Spike B go criterion), Task 0.20 (the spike), Task 3.9 (scale to 50).
- ADR-003 (`pkg/` stability) for the reasoning behind keeping the
  classifier under `test/` rather than promoting it to `pkg/`.
- `test/parity/classifier/` for the implementation and fixtures.
