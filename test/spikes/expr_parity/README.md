# Spike A — Expression evaluator parity probe

## Purpose

Pre-flight check before committing to the wider P0 plan: can a hand-rolled
expression evaluator hit >=90% behavioural parity with the real GitHub Actions
evaluator on a representative corpus? See `we-have-a-plan-streamed-cherny.md`
§8 Spike A and §9 Task 0.4.

If parity is below 90%, the architectural premise of the production evaluator
(internal/authoring/expr, Task 0.6) changes — most likely the `Value` model
needs an explicit ADT instead of the `any`-backed shape used here.

## Run

```
go run ./test/spikes/expr_parity
```

Exits 0 on PROCEED (>=90% pass), exits 1 on FAIL — so CI can gate on it.

## Output

Per-case PASS/FAIL lines, then a final two-line summary:

```
spike-a: <N>/<M> passed (<pct>%)
go: PROCEED|FAIL
```

## Go/no-go criterion

`pct >= 90.0` -> PROCEED. Anything lower -> FAIL, redesign required.

## Caveats

- `hashFiles()` is intentionally excluded from the corpus. A real implementation
  needs globbing plus a filesystem fixture (see plan Task 0.8). The evaluator
  rejects `hashFiles(...)` with an explicit error.
- The expressions in `corpus.json` are hand-crafted to mirror common OSS
  workflow patterns (deploy gates, default-value patterns, JSON round-trips).
  The full 200-expression corpus is the production evaluator's job.
- Object/array equality semantics are deliberately under-specified — GH itself
  is fuzzy here, and the corpus avoids the ambiguous cases.
- This whole directory is throwaway. It will be deleted or harvested when
  Task 0.6 lands the production evaluator.
