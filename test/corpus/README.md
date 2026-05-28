# gact lint corpus

This directory is the parity-comparison corpus that backs
`compare_actionlint_test.go`. Each numbered subdirectory holds exactly one
workflow YAML file shaped after a common open-source CI pattern.

**Provenance note.** The plan calls for workflows "copied from real OSS
repos." The Task 0.18 authoring environment has no web access, so the
workflows here are *synthesised* by hand to mirror common shapes — they are
plausible but not traceable to any specific public source. The numeric counts
that the comparison test asserts (gact ≥ actionlint, and at least one case
where gact > actionlint) are valid regardless of provenance because both
tools see the same input.

## Index

| Dir                              | Shape                                              |
| -------------------------------- | -------------------------------------------------- |
| 01_simple_ci                     | Single-job push CI with `actions/checkout@v4`.     |
| 02_matrix_node                   | Node.js matrix build across versions and OSes.     |
| 03_multi_job_needs               | `lint -> build -> test` linear `needs:` chain.     |
| 04_services_postgres             | Service container for integration tests.           |
| 05_action_with_args              | `actions/cache` with `with:` arguments using `${{ runner.os }}` and `hashFiles`. |
| 06_reusable_callee               | Reusable workflow (`workflow_call`) with outputs.  |
| 07_pull_request                  | Pull-request trigger with type and branch filters. |
| 08_scheduled_nightly             | Cron-scheduled nightly job.                        |
| 09_manual_dispatch_inputs        | `workflow_dispatch` with typed inputs.             |
| 10_release_publish               | Tag-based release with `release.published`.        |
| 11_container_job                 | Job-level `container:` (Docker) configuration.     |
| 12_js_action_pin                 | Pinned JavaScript action via SHA.                  |
| 13_composite_local               | Local composite action reference.                  |
| 14_expression_heavy              | Many `${{ }}` expressions across steps.            |
| 15_secrets_step                  | Step env reading a `secrets.*` reference.          |
| 16_conditional_jobs              | Conditional job execution with `if:` predicates.   |
| 17_concurrency_group             | Concurrency control with cancellation.             |
| 18_env_at_all_levels             | `env:` at workflow, job, and step levels.          |
| 19_continue_on_error             | Step with `continue-on-error: true`.               |
| 20_github_bogus_typo             | Deliberate `github.<typo>` reference for gact.     |

`20_github_bogus_typo` carries the deliberate issue that justifies the
"gact catches more than actionlint" assertion in the test. The rest of the
corpus is intended to lint clean on both tools.
