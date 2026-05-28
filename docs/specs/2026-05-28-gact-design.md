# gact — Run GitHub Actions Locally, Without Docker

**Status:** Draft design
**Date:** 2026-05-28
**Author:** noble
**Target v1.0:** ~5 months solo, ~2-3 months for a small team

---

## 1. Summary

`gact` is a cross-platform CLI + VS Code extension that runs GitHub Actions workflows locally, **without requiring Docker for the common path**. Its primary job is to give developers a fast, honest pre-push gate: type `git push`, and within seconds know whether the workflow will pass on GitHub.

The wedge against existing tools (`act`, `actionlint`, GitHub itself) is the combination of:

1. **Zero hard runtime dependencies** for ~85% of real-world workflows (Node detected and used only if a JavaScript action runs).
2. **Honest tiered fallback** for steps that genuinely cannot run natively — degrade through Dockerfile-translation, local services, embedded service alternatives, alternative container runtimes (Podman / containerd), or cloud-dispatch, but **never silently skip**.
3. **Pre-push integration** as a first-class flow: diff-aware job selection, sub-second startup, content-addressed step cache for fast re-runs.

This document specifies the v1 design and a phased roadmap. It is intentionally focused on a single product wedge; non-goals are listed explicitly in §3.

---

## 2. Problem & motivation

GitHub Actions has a slow inner loop. To verify a workflow change, the developer must:

1. Commit the change.
2. Push it (or open a PR).
3. Wait for GitHub-hosted runners to pick up the job.
4. Wait for the workflow to finish.
5. Read the logs.
6. Fix the typo / missing input / bad expression.
7. Repeat.

A round-trip on a typical CI workflow is 2-10 minutes. Most failures are not real test failures — they are workflow-authoring mistakes (a misspelled secret reference, a missing input on a reusable workflow, a bad matrix combination) that should have been caught locally in seconds.

**Existing tools and their gaps:**

| Tool | Gap |
|---|---|
| `act` | Requires Docker Desktop. Won't work on locked-down corporate laptops, machines without admin rights, or in container-in-container CI. Diverges from real GitHub on subtle features (`hashFiles()` semantics, expression evaluation edge cases, matrix expansion order). Slow cold start. No incremental caching. |
| `actionlint` | Static analysis only. Catches syntax and shellcheck issues, but cannot tell you whether the workflow actually runs. No data-flow analysis across steps. |
| VS Code "GitHub Actions" extension | Schema validation and basic completion. No execution. |
| GitHub itself | The slow loop we are trying to eliminate. |

**What developers want:**

> "I want to type `git push`, and have something tell me — without Docker, without waiting 5 minutes — that my workflow will pass."

That is the product.

---

## 3. Goals & non-goals

### 3.1 Goals (v1)

- Execute GitHub Actions workflows on a developer machine with high parity to `ubuntu-latest`, `windows-latest`, and `macos-latest`, using the host OS when it matches the requested runner.
- **Zero hard dependencies** on the common path: ship a single static binary; `git` is the only assumed peer.
- Pre-push integration: `git push` is blocked unless affected workflows pass.
- Sub-second startup; sub-10-second cached re-run on a typical CI workflow.
- Honest tiered fallback for steps that cannot run natively. Always report what was executed at which tier.
- Cross-platform: macOS (Intel + Apple Silicon), Linux (amd64 + arm64), Windows (amd64) — single binary per OS.
- Diff-aware: only run workflows and jobs that GitHub would actually trigger on the user's push.
- LSP-backed VS Code extension sharing the same engine.

### 3.2 Non-goals (v1)

- Replacing GitHub Actions as a CI provider.
- Acting as a self-hosted runner (registering against the GitHub API and pulling real jobs). Considered and rejected — adds a control plane with no user benefit for the pre-push goal.
- Web UI / desktop GUI / Electron app.
- Plugin marketplace / third-party tier extensions.
- Multi-repo orchestration.
- Replacing `actionlint` for users who want pure linting (we wrap or re-implement only what we need; we do not aim to be a drop-in replacement).
- Mocking or recording GitHub API responses for non-runner actions (e.g., we do not stub `gh` API calls inside steps).

---

## 4. User experience

### 4.1 First-time setup

```text
$ cd my-project
$ gact init
✓ Detected 3 workflows in .github/workflows/
✓ Installed git pre-push hook (.git/hooks/pre-push)
✓ Verified Node 20.10.0 available — JavaScript actions supported
✓ Detected Podman 4.7 — container fallback available
ℹ No local PostgreSQL found — `postgres` services will use embedded-pg
ℹ Run `gact doctor` to view all detected capabilities
```

### 4.2 Daily flow (green path)

```text
$ git commit -am "fix: handle empty input"
$ git push
# pre-push hook fires automatically
gact: running affected workflows for push to main…

  ci.yml / lint               ✓ 1.2s   (T1 native, cached)
  ci.yml / test (node 18)     ✓ 8.3s   (T1 native)
  ci.yml / test (node 20)     ✓ 7.9s   (T1 native)
  ci.yml / build              ✓ 4.1s   (T1 native)
  release.yml                 ⊘ skipped (only fires on tags)

Parity: 100% — all steps ran at T1 native.
All 4 jobs passed in 21.5s. Pushing.
```

### 4.3 Failure flow

```text
ci.yml / test (node 20)     ✗ 12.4s  (T1 native)
  ↳ step "Run tests" failed at line 47 in __tests__/auth.test.ts
    expected 200, got 401

  Quick actions:
    Replay step:        gact replay ci.yml:test:node-20 --step "Run tests"
    Interactive shell:  gact shell  ci.yml:test:node-20 --at "Run tests"
    Bypass hook:        git push --no-verify

Push aborted by gact pre-push hook.
```

`gact shell` drops the user into an interactive shell with the exact env vars, working directory, and tool versions the failing step had. This is the killer debug feature — you can reproduce the failure manually in seconds, not minutes.

### 4.4 Parity reporting

Every run ends with an explicit parity report:

```text
Parity summary:
   4 steps  T1 Native           ✓ exact match expected
   1 step   T2 Translated       ⚠ Dockerfile heuristic — verify on GH if critical
   1 step   T7 Skipped          ✗ requires windows-latest runner on Linux host

Confidence: 92% — 1 step will only run on GitHub.
```

The user always knows what they're getting. Trust is built on never silently skipping or pretending we ran something we didn't.

### 4.5 CLI surface (v1)

Kept deliberately small:

```text
gact init                            install hook, detect tools, write .gact.yml
gact run [workflow[:job[:matrix]]]   run all, or a specific target
gact run --changed-since=@{u}        what the pre-push hook uses
gact list                            show workflows, jobs, matrix expansions
gact lint                            static pass only, no execution
gact replay <id> --step <name>       re-run one step from prior cached state
gact shell  <id> --at <step>         interactive shell at a step's env
gact secrets (set|list|rm|sync)      manage local secret store
gact daemon (start|stop|status)      optional warm-state daemon
gact doctor                          diagnose missing tools, suggest fixes
gact cache (status|clean|gc)         manage step cache
```

### 4.6 VS Code extension

- LSP-backed inline diagnostics (same engine as `gact lint`).
- Hover: action input/output signatures, marketplace docs.
- Code lens above each job header: `▶ Run job locally`.
- Status bar widget showing last `gact run` result and a click-to-rerun affordance.
- Problem panel integration for failed steps.

---

## 5. Architecture

```text
┌────────────────────────────────────────────────────────────────┐
│  Surfaces (thin)                                                │
│  ┌─────────┐  ┌─────────────────┐  ┌──────────────────────┐    │
│  │  CLI    │  │ Git pre-push    │  │ VS Code Ext (LSP)    │    │
│  │ (gact)  │  │ hook            │  │ ─ inline diagnostics │    │
│  │         │  │                 │  │ ─ "Run job" codelens │    │
│  └────┬────┘  └────────┬────────┘  └──────────┬───────────┘    │
└───────┼────────────────┼──────────────────────┼────────────────┘
        │                │                      │
        └────────────────┴──────────────────────┘
                         │ JSON-RPC over local socket
                ┌────────▼─────────┐
                │   Core engine    │  (single Go binary)
                └────────┬─────────┘
                         │
   ┌─────────────────────┼─────────────────────┐
   │                     │                     │
┌──▼──────────┐  ┌───────▼────────┐  ┌─────────▼───────┐
│ Static pass │  │ Diff selector  │  │ Job DAG         │
│ ─ YAML AST  │  │ ─ paths filter │  │ ─ topo sort     │
│ ─ Expr eval │  │ ─ on: triggers │  │ ─ needs deps    │
│ ─ Matrix    │  │ ─ git diff     │  │ ─ matrix expand │
│ ─ Schema    │  │                │  │ ─ concurrency   │
└─────────────┘  └────────────────┘  └─────────┬───────┘
                                               │
                              ┌────────────────▼────────────────┐
                              │ Step executor (tiered)          │
                              │ T1 Native (Node / host shell)   │
                              │ T2 Dockerfile→native translation│
                              │ T3 Local service binary         │
                              │ T4 Embedded service alt         │
                              │ T5 Podman/containerd if present │
                              │ T6 Cloud-dispatch (gh API)      │
                              │ T7 Skip + warn (last resort)    │
                              └────────────────┬────────────────┘
                                               │
                              ┌────────────────▼────────────────┐
                              │ Step cache (content-addressed)  │
                              │  hash(action SHA + inputs +     │
                              │       env subset + file hashes) │
                              │  → cached stdout/exit/outputs   │
                              └─────────────────────────────────┘
```

### 5.1 Language choice — Go

After weighing Node/TypeScript, Rust, and Go:

| Criterion | Go | Rust | Node/TS |
|---|---|---|---|
| Cold-start latency (matters for the pre-push hook) | ~10 ms | ~5 ms | ~200 ms |
| Single static binary across OSes | trivial | trivial | painful (pkg/nexe) |
| Concurrency model for job DAG | goroutines (ideal) | tokio (good) | event loop (awkward) |
| Domain precedent / library ecosystem | `act`, `gh`, Docker, kubectl all Go | growing | limited |
| Dev velocity for a tool of this size | high | medium | high |

**Pick: Go.** The pre-push hook is the hot path — Go's ~10 ms cold start vs Node's ~200 ms matters when the user pushes 20+ times a day. Rust shaves another 5 ms for roughly 2× the implementation effort, which we do not need. JavaScript actions still require a Node runtime to execute, but Node is treated as a *peer dependency we shell out to* (and if it is missing, we tell the user once: `gact requires Node ≥ 18 for JavaScript actions`).

### 5.2 Module layout

```text
cmd/
  gact/                main CLI entry
  gact-lsp/            LSP binary used by VS Code
internal/
  parser/              YAML → IR
  expr/                Expression evaluator
  resolver/            Action / reusable workflow fetcher + cache
  selector/            Diff-aware job selection
  scheduler/           Job DAG + concurrency
  exec/
    tier1_native/      Node + host shell execution
    tier2_translate/   Dockerfile heuristic translation
    tier3_service/     Local service binary detection
    tier4_embedded/    Embedded service alternatives
    tier5_runtime/     Podman / containerd / Docker bridge
    tier6_dispatch/    Cloud-dispatch via gh API
  cache/               Content-addressed step cache
  secrets/             OS keychain integration
  hook/                git hook installer
  daemon/              Optional warm daemon
  lsp/                 LSP server core
  report/              Parity + run reporting
pkg/                   Public re-usable bits (parser, expr) for embedding
test/
  corpus/              Real-world workflows used in parity testing
  parity/              Nightly diff against real GitHub runs
```

---

## 6. Component design

### 6.1 Parser & resolver

- YAML parser producing a fully typed AST (Go structs with positional info preserved for diagnostics).
- Expression evaluator implementing the full [GitHub expression syntax](https://docs.github.com/en/actions/learn-github-actions/expressions): `${{ }}`, `contains()`, `fromJSON()`, `toJSON()`, `hashFiles()`, `format()`, `startsWith()`, `endsWith()`, `success()`, `failure()`, `always()`, `cancelled()`, context access (`github.*`, `env.*`, `vars.*`, `secrets.*`, `steps.*`, `matrix.*`, `needs.*`, `runner.*`).
- Matrix expansion: includes / excludes, `max-parallel`, `fail-fast`.
- Reusable workflow resolver: fetches `uses: org/repo/.github/workflows/foo.yml@ref`, links input types, validates `with:` against the declared inputs.
- Action signature resolver: fetches `action.yml` from the `uses:` ref by resolving the ref to a SHA, validates `with:` keys against declared inputs, type-checks required vs optional inputs.
- Output: a fully resolved **workflow IR** — every expression evaluated where possible, every job's runner / env / steps / matrix value materialized.

### 6.2 Diff-aware selector

On `git push`, the selector reads:

- `git diff --name-only <since>..HEAD` (where `<since>` defaults to `@{u}` for the pre-push hook).
- The list of changed files.
- Each workflow's `on:` triggers, including `paths:` / `paths-ignore:` filters and branch filters.

It computes: *which workflows would GitHub trigger on this push, and within them, which jobs are reachable?* A workflow with `paths: ['src/**']` is skipped entirely if the diff touches only `docs/`. A job whose `if:` evaluates to false statically is pruned. This is the primary "fast" lever — most pushes touch only a small slice of the workflow surface.

### 6.3 Job DAG scheduler

- Topological sort by `needs:`.
- Parallel execution of independent jobs, default concurrency = `runtime.NumCPU()`, override via `--concurrency` or `.gact.yml`.
- Matrix jobs run in parallel up to `max-parallel`.
- `if:` conditions evaluated at job and step level using the latest evaluator state.
- Failure propagation honors `continue-on-error` and `fail-fast` per GitHub semantics.
- Cancellation: SIGINT propagates to all running steps; in-flight steps get 5s graceful shutdown then SIGKILL.

### 6.4 Step executor — tiered fallback

For each step, the executor picks the **lightest tier that can faithfully execute it**. Selection logic is deterministic and reported.

| Tier | Step kind | Behavior |
|---|---|---|
| **T1 Native** | JS action (`runs.using: node20`) | Clone action source at SHA into `~/.gact/cache/actions/<sha>/`, run `npm ci --omit=dev` if needed, then `node action/dist/index.js` with `INPUT_*`, `GITHUB_*`, and `RUNNER_*` env populated. Outputs captured via the `$GITHUB_OUTPUT` file. |
| **T1 Native** | Composite action | Recursively process sub-steps as if inlined, respecting `with:` inputs. |
| **T1 Native** | `run:` shell step | Host shell — `bash` on macOS/Linux/WSL, `pwsh` on Windows, `sh` fallback. `shell:` override respected. Working dir defaults to `$GITHUB_WORKSPACE`. |
| **T2 Translated** | Docker action with a *simple* Dockerfile | Parse the Dockerfile. If single-stage, base image is a known Linux distro, and steps are limited to `RUN apt-get install <pkg>` / `RUN pip install <pkg>` / `COPY` plus an `ENTRYPOINT` to a binary, install the binary natively into `~/.gact/cache/tools/<name>/` via the host package manager (or download the pinned release artifact) and run it. Heuristic; falls through to T5 on any unhandled pattern. |
| **T3 Local service** | `services:` block with image matching a host-installed binary | If `postgres ≥ N` is on `$PATH` for `services.db.image: postgres:N`, spin up a temp instance on a random port, inject host/port env into the job's steps. Supported in v1: `postgres`, `mysql`, `mariadb`, `redis`, `mongodb`. |
| **T4 Embedded** | Service with a known embedded equivalent | `redis` → `miniredis` (in-process). `postgres` → embedded-postgres-go. `mysql` → embedded MariaDB binary. Spun up in-process or as a short-lived subprocess. |
| **T5 Container runtime** | Anything still unhandled | If a container runtime is detected, use it transparently. Detection order: Podman → containerd (via `nerdctl`) → Docker. The user did not "need" Docker; they simply benefit if it (or an alternative) is present. |
| **T6 Cloud-dispatch** | Still unhandled, user opted in | Create a temporary branch `gact/scratch-<hash>`, push a synthesized workflow that runs only the target job with the user's inputs, watch via `gh run watch`, stream logs back, delete the branch on completion. Slower (round-trip to GitHub) but provides full coverage. Off by default — user opts in via `.gact.yml` or `--allow-cloud-dispatch`. |
| **T7 Skip + warn** | Last resort | Mark the step `SKIPPED — <reason>`. Run exits non-zero with a summary. Never silent. |

**Cross-OS matrix.** When `runs-on` does not match the host OS:

| Requested | Host | Behavior |
|---|---|---|
| ubuntu-latest | Linux | T1 native, full parity expected. |
| ubuntu-latest | macOS | T1 native using host shell. Parity warning if step uses Linux-specific tooling (`apt-get`, `/proc`, glibc-specific). |
| ubuntu-latest | Windows | T1 native via WSL2 if available; else falls through to T5/T6. |
| windows-latest | Windows | T1 native via `pwsh`. |
| windows-latest | non-Windows | T6 cloud-dispatch if user has opted in; else T7 skip + warn. Can be configured to T5 via Windows container. |
| macos-latest | macOS | T1 native. |
| macos-latest | non-macOS | T6 cloud-dispatch if user has opted in (Apple licensing prevents reliable virtualization elsewhere); else T7 skip + warn. |
| self-hosted | any | T1 if the runner labels match host capabilities; else T7. |

### 6.5 Step cache (content-addressed)

- Cache key = SHA-256 of:
  - Action ref resolved to SHA (or, for composite/`run:` steps, the step body bytes).
  - Resolved `with:` inputs in canonical JSON form.
  - Relevant env subset (declared via `env:` at job/step level, plus an opt-in list).
  - Hashes of declared file inputs, following `hashFiles()` semantics.
  - `gact` version.
- On hit: reuse stored stdout, stderr, exit code, output values, and any files written to `$GITHUB_OUTPUT` / `$GITHUB_STEP_SUMMARY`. No execution.
- On miss: execute, store atomically.
- Located at `~/.gact/cache/steps/<hash>/`.
- LRU eviction at a configurable size cap (default 5 GB, configurable via `.gact.yml`).
- `gact cache (status|clean|gc)` exposes this surface.

This is the lever for fast re-runs: change one file, and only steps whose inputs changed re-execute.

### 6.6 Daemon mode (optional)

- `gact daemon start` boots a long-lived process holding warm parser state, action registry cache, and an open connection to the step cache.
- The CLI / hook talks to it via a local Unix socket (or named pipe on Windows).
- Saves ~100 ms per invocation by avoiding cold-start parsing of unchanged workflows.
- Auto-shuts down after 1 hour of idle.
- Pure optimization — every command must work without the daemon present.

### 6.7 Secrets

- Stored in the OS keychain by default: macOS Keychain, Windows Credential Manager, libsecret (Linux).
- `gact secrets set FOO` prompts for value, stores under a per-repo namespace.
- `gact secrets sync` diffs against `gh secret list` and warns on names referenced in workflows but missing locally (and vice versa).
- Secrets are never written to disk in plaintext, never logged. Masking of secret values in step output mirrors GitHub's behavior.

### 6.8 Parity validation harness

- A curated corpus of ~50 real-world OSS workflows lives in `test/corpus/`.
- Nightly CI runs each one under `gact` and (separately) on real GitHub Actions, then diffs:
  - Job pass/fail outcomes.
  - Step exit codes.
  - Step output values where deterministic.
- Divergences are filed as issues automatically. This is how we keep parity honest as GitHub evolves.

---

## 7. Configuration — `.gact.yml`

Optional, repo-local. Sensible defaults mean most projects need none.

```yaml
# .gact.yml
runners:
  ubuntu-latest:
    map_to: host             # 'host' | 'podman:<image>' | 'cloud-dispatch'
  windows-latest:
    map_to: cloud-dispatch
  macos-latest:
    map_to: cloud-dispatch

secrets:
  source: keychain           # 'keychain' | 'file:<path>' | 'env'
  sync_with: gh              # if set, `gact secrets sync` checks against `gh secret list`

cloud_dispatch:
  enabled: false             # opt in to T6
  branch_prefix: gact/scratch-
  cleanup: always            # 'always' | 'on-success' | 'never'

cache:
  max_size_gb: 5
  ignore_env: [HOME, USER, PATH]  # env vars to exclude from cache keys

selector:
  default_since: "@{u}"      # base ref for diff-aware selection

execution:
  max_concurrency: auto      # 'auto' uses runtime.NumCPU()
  step_timeout: 10m

hooks:
  pre_push: enabled
```

---

## 8. Distribution

- **Single static binary per OS / arch**:
  - `gact_darwin_arm64`, `gact_darwin_amd64`
  - `gact_linux_amd64`, `gact_linux_arm64`
  - `gact_windows_amd64.exe`
- **Installers**:
  - `brew install gact` (custom tap initially, homebrew-core once stable)
  - `scoop install gact`
  - `apt`/`rpm` packages via a generated repo
  - `curl -fsSL https://gact.dev/install.sh | sh`
- **VS Code extension**: published to the Marketplace and OpenVSX.
- Auto-update via `gact update` (off by default; opt in).

---

## 9. Testing strategy

- **Unit tests** per package (`go test ./...`). Target ≥ 80% coverage on `parser`, `expr`, `selector`, `scheduler`.
- **Golden tests** for the parser: a directory of `.yml` inputs and `.json` expected ASTs.
- **Integration tests** for each tier under `test/exec/`: a fixture workflow → assert outcome and tier reported.
- **Parity tests** in nightly CI against the corpus, diffing local outcomes against real GitHub runs.
- **End-to-end tests** for the pre-push hook: a scratch repo, simulated push, assert hook behavior.
- **Snapshot tests** for the parity report and CLI output formatting.

---

## 10. Phased roadmap

| Phase | Duration (solo) | Scope | Exit criteria |
|---|---|---|---|
| **P0 — Foundation** | 4 wks | YAML parser, expression evaluator, matrix expansion, job DAG, `gact list`, `gact lint`. No execution. | Static analyzer demonstrably exceeds `actionlint` on data-flow checks across a 20-workflow corpus. |
| **P1 — Native execution** | 4 wks | T1 only. JS actions, composite, shell. `gact run`. Step cache. Secrets store. | 80% of corpus workflows run end-to-end on Linux/macOS with passing outcome. |
| **P2 — Pre-push integration** | 2 wks | `gact init`, git hook, diff-aware selector. | Hook blocks push on failure across three fixture repos covering typical layouts. |
| **P3 — Fallback tiers** | 4 wks | T2 Dockerfile translation (limited heuristic), T3/T4 services, T5 container runtimes, T6 cloud-dispatch. | "Support every action" promise demonstrably lands — every corpus workflow either passes or surfaces a clear T7 warning. |
| **P4 — Debug & polish** | 3 wks | `gact shell`, `gact replay`, parity report, `gact doctor`. | Failure flow from §4.3 works end-to-end; user can reproduce a failure interactively in under 30s. |
| **P5 — VS Code & distribution** | 3 wks | LSP wrap, code lens, status bar, packaging, install scripts, docs site. | Public 1.0 release published to Homebrew, Scoop, VS Code Marketplace. |

**Total: ~20 weeks solo (~5 months), ~10-12 weeks for a small team (2-3 engineers).**

---

## 11. Risks & mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Parity drift — `gact` passes but GitHub fails | High | High | Nightly parity harness against a real corpus (§6.8). File divergences automatically. Be conservative — when in doubt, report a parity warning, not a green. |
| Marketplace actions misbehave under native execution | Medium | Medium | Maintain an allow-list of validated actions. Community-contributed compatibility shims. Escape hatch to T5/T6 is always available. |
| Dockerfile → native translation gets ugly | Medium | Low | Restrict to a small, well-defined heuristic surface (single-stage, known base images, `apt-get`/`pip`/`apk` install + `ENTRYPOINT` binary). Anything outside falls through to T5. |
| Windows host parity for `ubuntu-latest` workflows is poor | Medium | High on Windows | Detect WSL2 presence and prefer it. Otherwise warn loudly and offer cloud-dispatch. Document the limitation prominently. |
| Maintenance burden of tracking GitHub-side changes to expression syntax / action.yml schema | Low | Medium | Scheduled job that diffs against the `actions/runner` repo schema and opens issues on drift. |
| Step cache produces incorrect cache hits (false positive) | Low | High | Conservative cache key: when in doubt, miss. Provide `--no-cache` and `gact cache clean`. Cache keys include `gact` version so a bug fix invalidates prior hits. |
| Cloud-dispatch leaks secrets or junk branches | Low | Medium | Use `gh` with the user's existing auth (never store tokens). Always clean up branches per `cleanup:` setting. Refuse to dispatch if the synthesized workflow would reference a secret not present on GitHub. |

---

## 12. Open questions

1. **`gact` vs another name.** "gact" overlaps with `act` enough to be confusing. Consider alternatives: `liftoff`, `runlocal`, `localact`, `ghx`, or a non-CI-jargon name. To resolve before public release; not blocking design.
2. **Telemetry.** Off by default. Should there be an opt-in crash-report channel from day one, or defer to v1.1?
3. **Public schema for tier reporting.** Should the parity report be machine-readable (JSON output mode) from v1? Useful for CI-of-CI scenarios.
4. **Reusable workflow caching across repos.** v1 fetches by ref each time (with a short TTL). Cross-repo caching is straightforward but adds invalidation complexity — defer to v1.1.

These do not block implementation. They are flagged for resolution during P5 or v1.1.

---

## 13. Definition of done for v1.0

A developer who has never used `gact` before:

1. Runs `brew install gact` (or equivalent on their OS).
2. `cd`s into a real OSS project that uses GitHub Actions.
3. Runs `gact init`.
4. Makes a code change, runs `git push`.
5. Within 10 seconds (or under 30 seconds on cold first run), `gact` reports green or red with a parity summary.
6. Pushes succeed when local checks pass.
7. When a step fails, `gact shell` reproduces the failure interactively.
8. Docker is never installed, never required.

That is the product.
