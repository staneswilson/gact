package expr

import (
	"os"

	pubexpr "github.com/staneswilson/gact/pkg/expr"
)

// StaticInputs is the frozen snapshot of everything the lint pass and
// dry-run pipelines can know about a workflow run before it actually
// runs. It is the constructor argument for StaticContextFor.
//
// The shape follows GH's expression contexts (github.*, runner.*,
// matrix.*, vars.*, secrets.*). Two principles govern what lives here:
//
//  1. Only values that are STABLE at static analysis time. The lint pass
//     must produce the same diagnostics for the same inputs across
//     machines and times of day. That means no host env, no clock, no
//     network.
//  2. Secrets are NEVER stored — only their names. Values are opaque
//     sentinels at lint time (see Get) so callers cannot accidentally
//     leak them through a Stringer or a log line. Real secret values
//     live in the keyring adapter and are resolved by a RuntimeContext,
//     not this one.
//
// Empty strings mean "not supplied"; the resolver below treats them as
// missing rather than as the literal empty string.
type StaticInputs struct {
	// Event is the workflow trigger name, e.g. "push", "pull_request",
	// "schedule". Exposed as github.event (matching the simplified form
	// in the design spec) AND as github.event_name for parity with the
	// GH-documented identifier. The full event payload is not modelled
	// here — lint cannot know it without re-implementing GitHub's event
	// dispatch, which is out of scope.
	Event string

	// Ref is the symbolic ref, e.g. "refs/heads/main",
	// "refs/pull/42/merge". Exposed as github.ref.
	Ref string

	// SHA is the commit hash that triggered the workflow. May be empty
	// when not known (e.g. lint of a workflow file in isolation).
	// Exposed as github.sha.
	SHA string

	// Repository is the "owner/repo" slug. Exposed as github.repository
	// and decomposed into github.repository_owner / github.event.repository
	// is intentionally NOT populated — the lint pass does not need it.
	Repository string

	// Actor is the username of the user (or bot) that initiated the
	// workflow. Exposed as github.actor.
	Actor string

	// Workspace is the absolute path that GH-Actions sets as the working
	// directory for every step. If empty, StaticContextFor defaults to
	// os.Getwd() so `${{ github.workspace }}` interpolation produces a
	// real path during lint. Falls back to "" if the cwd cannot be read.
	Workspace string

	// RunnerOS is the canonical OS string for runner.os: "Linux",
	// "macOS", or "Windows". Lint runs against the developer's choice
	// of platform; defaults are intentionally NOT provided here so the
	// caller has to be explicit about which platform's workflow they
	// are linting.
	RunnerOS string

	// RunnerArch is the canonical architecture string for runner.arch:
	// "X64", "ARM64", "X86", "ARM". Same explicitness contract as
	// RunnerOS.
	RunnerArch string

	// Matrix is a SINGLE concrete combination after matrix expansion —
	// e.g. {"node": 18, "os": "linux"}. The lint pass evaluates the
	// expression once per combo by constructing one StaticInputs per
	// combo. Values follow fromAny's conversion rules: float64 / int /
	// int64 / string / bool / map / slice.
	Matrix map[string]any

	// Vars holds repository- and organisation-level configuration
	// variables (the `vars.*` scope), typically sourced from
	// .gact.yml. Keys are case-sensitive to match GH's behaviour for
	// vars.
	Vars map[string]string

	// Secrets is the LIST OF SECRET NAMES the workflow may reference.
	// Values are intentionally absent. The names allowlist drives the
	// opaque-sentinel behaviour of Get for the secrets.* scope: known
	// names return "<secrets.NAME>", unknown names return Null.
	Secrets []string
}

// StaticContextFor returns a pubexpr.Context whose Get method resolves
// identifiers against in. The returned context is safe for concurrent
// use by goroutines because it captures in by value and never mutates
// internal state.
//
// Behaviour summary (also documented per-scope in (*staticContext).Get):
//
//	github.*   → values from in; unknown keys → KindNull
//	runner.*   → os/arch from in; temp/tool_cache → opaque sentinels;
//	             other keys → KindNull
//	matrix.*   → values from in.Matrix via fromAny; missing → KindNull
//	vars.*     → values from in.Vars; missing → KindNull
//	env.*      → always KindNull. env is an evaluation-time concept;
//	             pulling from os.Environ would make lint output
//	             machine-dependent. RuntimeContext handles env.
//	secrets.*  → KindString "<secrets.NAME>" if NAME ∈ in.Secrets;
//	             else KindNull. Values are NEVER returned here.
//	steps.*    → KindNull. Step outputs do not exist before execution.
//	             Strict-mode lint can flag references to steps.* via
//	             AST inspection.
//	needs.*    → KindNull. Same rationale as steps.*.
//	any other  → KindNull.
//
// Get never returns a non-nil error: lint must not be derailed by
// unknown scopes, and the soft-lookup contract documented on
// pubexpr.Context.Get reserves errors for adapter-level failures.
func StaticContextFor(in StaticInputs) pubexpr.Context {
	if in.Workspace == "" {
		if cwd, err := os.Getwd(); err == nil {
			in.Workspace = cwd
		}
	}
	return &staticContext{in: in}
}

// staticContext is the concrete Context returned by StaticContextFor.
// It is unexported because the public façade is the constructor.
type staticContext struct {
	in StaticInputs
}

// Get resolves (scope, key) per the table documented on StaticContextFor.
// The switch is intentionally explicit rather than table-driven so that
// each scope's semantics are visible at a glance and so adding a new
// scope is a localised change.
func (s *staticContext) Get(scope, key string) (value, error) {
	switch scope {
	case "github":
		return s.getGithub(key), nil
	case "runner":
		return s.getRunner(key), nil
	case "matrix":
		return s.getMatrix(key), nil
	case "vars":
		return s.getVars(key), nil
	case "env":
		// env.* is evaluation-time, not static. Returning Null here
		// keeps lint deterministic; RuntimeContext supplies real env.
		return nullValue(), nil
	case "secrets":
		return s.getSecret(key), nil
	case "steps", "needs":
		// Step outputs and job dependencies do not exist before
		// execution. Returning Null lets `if: ${{ steps.x.outputs.y }}`
		// type-check; a strict-mode lint can warn on these via the AST.
		return nullValue(), nil
	}
	return nullValue(), nil
}

// getGithub returns fields of the github.* scope. Recognised keys are
// pulled from StaticInputs; everything else is Null. The full GH context
// has many more fields (event_path, run_id, …) — the ones modelled here
// are those whose values are knowable at static-analysis time.
func (s *staticContext) getGithub(key string) value {
	switch key {
	case "event", "event_name":
		// "event" matches the simplified form documented in the design
		// spec; "event_name" mirrors the GH-documented identifier so
		// existing workflows lint correctly. The full event payload
		// (the GH "event" object proper) is not modelled here.
		if s.in.Event == "" {
			return nullValue()
		}
		return stringValue(s.in.Event)
	case "ref":
		if s.in.Ref == "" {
			return nullValue()
		}
		return stringValue(s.in.Ref)
	case "sha":
		if s.in.SHA == "" {
			return nullValue()
		}
		return stringValue(s.in.SHA)
	case "repository":
		if s.in.Repository == "" {
			return nullValue()
		}
		return stringValue(s.in.Repository)
	case "actor":
		if s.in.Actor == "" {
			return nullValue()
		}
		return stringValue(s.in.Actor)
	case "workspace":
		if s.in.Workspace == "" {
			return nullValue()
		}
		return stringValue(s.in.Workspace)
	}
	return nullValue()
}

// getRunner returns fields of the runner.* scope. os/arch reflect the
// caller's inputs. temp and tool_cache are returned as opaque sentinels
// because they exist at runtime as real directories — comparing them to
// the empty string (a common idiom) must therefore evaluate to false.
func (s *staticContext) getRunner(key string) value {
	switch key {
	case "os":
		if s.in.RunnerOS == "" {
			return nullValue()
		}
		return stringValue(s.in.RunnerOS)
	case "arch":
		if s.in.RunnerArch == "" {
			return nullValue()
		}
		return stringValue(s.in.RunnerArch)
	case "temp":
		return stringValue("<runner.temp>")
	case "tool_cache":
		return stringValue("<runner.tool_cache>")
	}
	return nullValue()
}

// getMatrix returns the value for matrix.<key> from the supplied combo,
// running it through fromAny so JSON-shaped values (float64 numbers,
// map[string]any objects, []any arrays) become the right Value kind.
// Missing keys → Null, matching GH's soft-lookup contract.
func (s *staticContext) getMatrix(key string) value {
	if s.in.Matrix == nil {
		return nullValue()
	}
	v, ok := s.in.Matrix[key]
	if !ok {
		return nullValue()
	}
	return fromAny(v)
}

// getVars returns the value for vars.<key>. vars.* values are always
// strings per GH's API (they're stored as repo/org configuration), so
// no fromAny conversion is needed here. Missing keys → Null.
func (s *staticContext) getVars(key string) value {
	if s.in.Vars == nil {
		return nullValue()
	}
	v, ok := s.in.Vars[key]
	if !ok {
		return nullValue()
	}
	return stringValue(v)
}

// getSecret returns the opaque sentinel "<secrets.NAME>" if NAME is in
// the allowlist; otherwise Null. Returning a real string (instead of,
// say, a synthetic Kind) keeps the sentinel comparable: the common
// `${{ secrets.FOO != '' }}` guard then type-checks AND evaluates to
// true at lint time, which is the safer assumption for a strict gate.
//
// Real secret values NEVER appear here. They live in the keyring
// adapter and are resolved at execution time by a different Context
// implementation.
func (s *staticContext) getSecret(key string) value {
	for _, name := range s.in.Secrets {
		if name == key {
			return stringValue("<secrets." + key + ">")
		}
	}
	return nullValue()
}
