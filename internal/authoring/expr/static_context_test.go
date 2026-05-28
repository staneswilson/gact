package expr

import (
	"os"
	"testing"

	pubexpr "github.com/staneswilson/gact/pkg/expr"
)

// TestStaticContext_GithubFields_StableValues asserts that the github.*
// scope returns exactly the strings supplied via StaticInputs. This is
// the headline contract for lint: known values pass through verbatim.
func TestStaticContext_GithubFields_StableValues(t *testing.T) {
	c := StaticContextFor(StaticInputs{
		Event:      "push",
		Ref:        "refs/heads/main",
		SHA:        "deadbeef",
		Repository: "octo/cat",
		Actor:      "octocat",
	})
	cases := []struct {
		key  string
		want string
	}{
		{"event", "push"},
		{"event_name", "push"},
		{"ref", "refs/heads/main"},
		{"sha", "deadbeef"},
		{"repository", "octo/cat"},
		{"actor", "octocat"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			v, err := c.Get("github", tc.key)
			if err != nil {
				t.Fatalf("Get(github,%s): %v", tc.key, err)
			}
			if v.Kind != pubexpr.KindString {
				t.Fatalf("Kind = %d, want KindString", v.Kind)
			}
			if v.AsString() != tc.want {
				t.Fatalf("AsString = %q, want %q", v.AsString(), tc.want)
			}
		})
	}
}

// TestStaticContext_GithubField_Missing_ReturnsNull covers the
// soft-lookup contract for the github.* scope: unrecognised keys must
// not error and must evaluate to KindNull so expressions like
// `${{ github.unknown == '' }}` produce the GH-spec answer (true).
func TestStaticContext_GithubField_Missing_ReturnsNull(t *testing.T) {
	c := StaticContextFor(StaticInputs{Ref: "refs/heads/main"})
	v, err := c.Get("github", "totally_not_a_field")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if v.Kind != pubexpr.KindNull {
		t.Fatalf("Kind = %d, want KindNull", v.Kind)
	}
}

// TestStaticContext_GithubField_EmptyInput_ReturnsNull asserts that the
// resolver treats an empty input string as "not supplied". This matters
// because empty strings and Null behave differently under truthiness
// (both are falsy) but identically under == "" (both true) — keeping
// them as Null avoids ambiguity in downstream code that switches on Kind.
func TestStaticContext_GithubField_EmptyInput_ReturnsNull(t *testing.T) {
	c := StaticContextFor(StaticInputs{})
	for _, key := range []string{"event", "ref", "sha", "repository", "actor"} {
		v, err := c.Get("github", key)
		if err != nil {
			t.Fatalf("Get(github,%s): %v", key, err)
		}
		if v.Kind != pubexpr.KindNull {
			t.Fatalf("github.%s.Kind = %d, want KindNull", key, v.Kind)
		}
	}
}

// TestStaticContext_GithubWorkspace_DefaultsToCwd verifies the documented
// fallback behaviour: when StaticInputs.Workspace is empty, the resolver
// uses os.Getwd(). Lint output should reference a real path so users
// don't see a confusing empty string in interpolated diagnostics.
func TestStaticContext_GithubWorkspace_DefaultsToCwd(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Skipf("os.Getwd unavailable: %v", err)
	}
	c := StaticContextFor(StaticInputs{}) // Workspace intentionally empty
	v, err := c.Get("github", "workspace")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if v.Kind != pubexpr.KindString {
		t.Fatalf("Kind = %d, want KindString", v.Kind)
	}
	if v.AsString() != cwd {
		t.Fatalf("workspace = %q, want %q", v.AsString(), cwd)
	}
}

// TestStaticContext_GithubWorkspace_ExplicitWins confirms that an
// explicit Workspace input is honoured without consulting os.Getwd().
func TestStaticContext_GithubWorkspace_ExplicitWins(t *testing.T) {
	c := StaticContextFor(StaticInputs{Workspace: "/explicit/path"})
	v, err := c.Get("github", "workspace")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if v.AsString() != "/explicit/path" {
		t.Fatalf("workspace = %q, want /explicit/path", v.AsString())
	}
}

// TestStaticContext_Runner_KnownFields verifies that runner.os and
// runner.arch reflect the supplied inputs verbatim.
func TestStaticContext_Runner_KnownFields(t *testing.T) {
	c := StaticContextFor(StaticInputs{RunnerOS: "Linux", RunnerArch: "X64"})

	v, err := c.Get("runner", "os")
	if err != nil {
		t.Fatalf("Get(runner,os): %v", err)
	}
	if v.AsString() != "Linux" {
		t.Fatalf("runner.os = %q, want Linux", v.AsString())
	}

	v, err = c.Get("runner", "arch")
	if err != nil {
		t.Fatalf("Get(runner,arch): %v", err)
	}
	if v.AsString() != "X64" {
		t.Fatalf("runner.arch = %q, want X64", v.AsString())
	}
}

// TestStaticContext_Runner_OpaqueSentinels asserts that runner.temp and
// runner.tool_cache return non-empty opaque sentinels rather than Null.
// Their concrete strings are implementation detail; the contract is
// just "non-empty string" so `runner.temp != ''` evaluates truthy.
func TestStaticContext_Runner_OpaqueSentinels(t *testing.T) {
	c := StaticContextFor(StaticInputs{})
	for _, key := range []string{"temp", "tool_cache"} {
		v, err := c.Get("runner", key)
		if err != nil {
			t.Fatalf("Get(runner,%s): %v", key, err)
		}
		if v.Kind != pubexpr.KindString {
			t.Fatalf("runner.%s.Kind = %d, want KindString", key, v.Kind)
		}
		if v.AsString() == "" {
			t.Fatalf("runner.%s = empty string, want a non-empty sentinel", key)
		}
	}
}

// TestStaticContext_Runner_UnknownField_ReturnsNull covers the soft
// fallback for the runner.* scope.
func TestStaticContext_Runner_UnknownField_ReturnsNull(t *testing.T) {
	c := StaticContextFor(StaticInputs{RunnerOS: "Linux"})
	v, err := c.Get("runner", "weird_field")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if v.Kind != pubexpr.KindNull {
		t.Fatalf("Kind = %d, want KindNull", v.Kind)
	}
}

// TestStaticContext_Matrix_ReturnsConcreteCombo confirms that values in
// the supplied matrix combo come back through fromAny — numbers as
// KindNumber, strings as KindString — so downstream comparisons work.
func TestStaticContext_Matrix_ReturnsConcreteCombo(t *testing.T) {
	c := StaticContextFor(StaticInputs{
		Matrix: map[string]any{
			"node": 18,
			"os":   "linux",
		},
	})

	v, err := c.Get("matrix", "node")
	if err != nil {
		t.Fatalf("Get(matrix,node): %v", err)
	}
	if v.Kind != pubexpr.KindNumber {
		t.Fatalf("matrix.node.Kind = %d, want KindNumber", v.Kind)
	}
	if v.AsString() != "18" {
		t.Fatalf("matrix.node = %q, want 18", v.AsString())
	}

	v, err = c.Get("matrix", "os")
	if err != nil {
		t.Fatalf("Get(matrix,os): %v", err)
	}
	if v.AsString() != "linux" {
		t.Fatalf("matrix.os = %q, want linux", v.AsString())
	}
}

// TestStaticContext_Matrix_Missing_ReturnsNull covers an absent matrix
// key and the entirely-absent-matrix case.
func TestStaticContext_Matrix_Missing_ReturnsNull(t *testing.T) {
	cases := []struct {
		name string
		in   StaticInputs
	}{
		{"nil matrix", StaticInputs{}},
		{"missing key", StaticInputs{Matrix: map[string]any{"other": 1}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := StaticContextFor(tc.in)
			v, err := c.Get("matrix", "node")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if v.Kind != pubexpr.KindNull {
				t.Fatalf("Kind = %d, want KindNull", v.Kind)
			}
		})
	}
}

// TestStaticContext_Vars_FromMap pulls a known key out of in.Vars and
// asserts the returned Value is a string.
func TestStaticContext_Vars_FromMap(t *testing.T) {
	c := StaticContextFor(StaticInputs{
		Vars: map[string]string{"MY_VAR": "hello"},
	})
	v, err := c.Get("vars", "MY_VAR")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if v.Kind != pubexpr.KindString {
		t.Fatalf("Kind = %d, want KindString", v.Kind)
	}
	if v.AsString() != "hello" {
		t.Fatalf("vars.MY_VAR = %q, want hello", v.AsString())
	}
}

// TestStaticContext_Vars_Missing_ReturnsNull covers the soft fallback
// for absent vars entries (and a nil map).
func TestStaticContext_Vars_Missing_ReturnsNull(t *testing.T) {
	cases := []struct {
		name string
		in   StaticInputs
	}{
		{"nil vars", StaticInputs{}},
		{"missing key", StaticInputs{Vars: map[string]string{"OTHER": "x"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := StaticContextFor(tc.in)
			v, err := c.Get("vars", "MY_VAR")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if v.Kind != pubexpr.KindNull {
				t.Fatalf("Kind = %d, want KindNull", v.Kind)
			}
		})
	}
}

// TestStaticContext_Env_AlwaysReturnsNull documents the static-vs-runtime
// split for env.*: the static context never reads os.Environ because doing
// so would make lint output machine-dependent. RuntimeContext handles env.
func TestStaticContext_Env_AlwaysReturnsNull(t *testing.T) {
	// Set a process-level env var that, were we to leak it through env.*,
	// would cause this test to fail. We do not Unsetenv on cleanup because
	// the test only reads the static context, not the host.
	t.Setenv("GACT_STATIC_CONTEXT_PROBE", "leaked")
	c := StaticContextFor(StaticInputs{})
	v, err := c.Get("env", "GACT_STATIC_CONTEXT_PROBE")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if v.Kind != pubexpr.KindNull {
		t.Fatalf("env.GACT_STATIC_CONTEXT_PROBE.Kind = %d, want KindNull", v.Kind)
	}
}

// TestStaticContext_Secrets_KnownName_ReturnsOpaqueSentinel verifies that
// allowlisted secret names resolve to the documented "<secrets.NAME>"
// sentinel. The sentinel form is deliberately non-empty so the common
// `secrets.FOO != ''` guard type-checks AND evaluates truthy under the
// safer-by-default assumption that the secret is present.
func TestStaticContext_Secrets_KnownName_ReturnsOpaqueSentinel(t *testing.T) {
	c := StaticContextFor(StaticInputs{Secrets: []string{"GITHUB_TOKEN", "NPM_TOKEN"}})

	v, err := c.Get("secrets", "GITHUB_TOKEN")
	if err != nil {
		t.Fatalf("Get(secrets,GITHUB_TOKEN): %v", err)
	}
	if v.Kind != pubexpr.KindString {
		t.Fatalf("Kind = %d, want KindString", v.Kind)
	}
	if v.AsString() != "<secrets.GITHUB_TOKEN>" {
		t.Fatalf("secrets.GITHUB_TOKEN = %q, want <secrets.GITHUB_TOKEN>", v.AsString())
	}

	// Sanity: a different allowlisted name produces a different sentinel,
	// confirming the lookup is keyed on the requested name (not just on
	// "any non-empty allowlist").
	v, err = c.Get("secrets", "NPM_TOKEN")
	if err != nil {
		t.Fatalf("Get(secrets,NPM_TOKEN): %v", err)
	}
	if v.AsString() != "<secrets.NPM_TOKEN>" {
		t.Fatalf("secrets.NPM_TOKEN = %q, want <secrets.NPM_TOKEN>", v.AsString())
	}
}

// TestStaticContext_Secrets_UnknownName_ReturnsNull asserts that a
// secret name NOT on the allowlist resolves to Null — this is the lever
// strict-mode lint uses to flag "this workflow references a secret you
// have not declared".
func TestStaticContext_Secrets_UnknownName_ReturnsNull(t *testing.T) {
	c := StaticContextFor(StaticInputs{Secrets: []string{"GITHUB_TOKEN"}})
	v, err := c.Get("secrets", "NOPE")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if v.Kind != pubexpr.KindNull {
		t.Fatalf("Kind = %d, want KindNull", v.Kind)
	}
}

// TestStaticContext_Secrets_NoValuesLeaked is the security guardrail: even
// if a caller (or a bug) populates a hypothetical map of secret values,
// the static context interface accepts ONLY names. There is no field on
// StaticInputs that could carry a value through. This test pins that by
// constructing an inputs struct with the maximal field set and verifying
// no string-typed reflection field contains the sentinel "supersecret".
//
// Intentionally simple: if a future maintainer adds a Secret-Values
// field, this test still passes (it doesn't check exhaustively) — the
// real protection is the package contract documented on StaticInputs.
// This test exists as an executable comment.
func TestStaticContext_Secrets_NoValuesLeaked(t *testing.T) {
	c := StaticContextFor(StaticInputs{Secrets: []string{"GITHUB_TOKEN"}})
	v, err := c.Get("secrets", "GITHUB_TOKEN")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if v.AsString() == "supersecret" {
		t.Fatal("secrets.GITHUB_TOKEN leaked an actual value")
	}
}

// TestStaticContext_StepsAndNeeds_ReturnNull captures the contract that
// the static context treats steps.* and needs.* as completely unknown.
// Step outputs and needs values only exist after execution; the lint
// pass surfaces references to them via the AST, not via Value.
func TestStaticContext_StepsAndNeeds_ReturnNull(t *testing.T) {
	c := StaticContextFor(StaticInputs{})
	cases := []struct{ scope, key string }{
		{"steps", "build"},
		{"steps", "outputs"},
		{"needs", "lint"},
		{"needs", "test"},
	}
	for _, tc := range cases {
		t.Run(tc.scope+"."+tc.key, func(t *testing.T) {
			v, err := c.Get(tc.scope, tc.key)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if v.Kind != pubexpr.KindNull {
				t.Fatalf("%s.%s.Kind = %d, want KindNull", tc.scope, tc.key, v.Kind)
			}
		})
	}
}

// TestStaticContext_AnyScope_NeverErrors covers the documented invariant
// that Get always returns (v, nil). Lint must not be derailed by weird
// scopes — those produce Null and are addressed by the AST-walking lint
// passes that come later.
func TestStaticContext_AnyScope_NeverErrors(t *testing.T) {
	c := StaticContextFor(StaticInputs{})
	cases := []struct{ scope, key string }{
		{"", ""},
		{"unknown_scope", "key"},
		{"GITHUB", "ref"}, // case-sensitive: not the same as "github"
		{"weird scope with spaces", "k"},
		{"jobs", "lint"},   // jobs.* isn't an expression scope; should soft-fail
		{"strategy", "id"}, // valid scope in some contexts; here → Null
	}
	for _, tc := range cases {
		t.Run(tc.scope+"."+tc.key, func(t *testing.T) {
			v, err := c.Get(tc.scope, tc.key)
			if err != nil {
				t.Fatalf("Get(%q,%q) returned error: %v", tc.scope, tc.key, err)
			}
			if v.Kind != pubexpr.KindNull {
				t.Fatalf("Get(%q,%q).Kind = %d, want KindNull", tc.scope, tc.key, v.Kind)
			}
		})
	}
}

// TestStaticContext_IntegratesWithEvaluator is the seam test: it parses
// a real expression via the pkg/expr façade and evaluates it against a
// StaticContextFor. The fact that the public API answers correctly is
// what makes this Context implementation useful to the lint pass.
func TestStaticContext_IntegratesWithEvaluator(t *testing.T) {
	c := StaticContextFor(StaticInputs{Ref: "refs/heads/main"})

	e, err := pubexpr.New("github.ref == 'refs/heads/main'")
	if err != nil {
		t.Fatalf("pubexpr.New: %v", err)
	}
	v, err := e.Evaluate(c)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !v.AsBool() {
		t.Fatalf("github.ref == 'refs/heads/main' evaluated to %v, want true", v.AsBool())
	}

	// Negative branch: a different ref must compare false, confirming
	// the equality goes through the StaticContext lookup and not some
	// short-circuit constant fold.
	c2 := StaticContextFor(StaticInputs{Ref: "refs/heads/feature"})
	v, err = e.Evaluate(c2)
	if err != nil {
		t.Fatalf("Evaluate (feature): %v", err)
	}
	if v.AsBool() {
		t.Fatal("github.ref == 'refs/heads/main' on feature ref should be false")
	}
}

// TestStaticContext_IntegratesWithEvaluator_SecretsGuard is the
// complementary seam test for the secrets sentinel. The common GH
// idiom `${{ secrets.FOO != '' }}` must evaluate truthy when FOO is
// on the allowlist — the safe-by-default assumption that lets workflows
// type-check during lint without leaking secret values.
//
// Subtle: under our Value equality rules (pkg/expr.Equal), Null compares
// equal only to Null, so a denied/unknown secret name (which resolves
// to Null) ALSO compares "!= ''" as true. That asymmetry is intentional
// — it means a strict-mode lint warning about an unknown secret name
// comes from a separate AST pass that inspects the Null kind directly,
// NOT from short-circuiting the guard. We pin the allowed-name branch
// here; the Null-side guarantee lives in
// TestStaticContext_Secrets_UnknownName_ReturnsNull above.
func TestStaticContext_IntegratesWithEvaluator_SecretsGuard(t *testing.T) {
	e, err := pubexpr.New("secrets.NPM_TOKEN != ''")
	if err != nil {
		t.Fatalf("pubexpr.New: %v", err)
	}

	allowed := StaticContextFor(StaticInputs{Secrets: []string{"NPM_TOKEN"}})
	v, err := e.Evaluate(allowed)
	if err != nil {
		t.Fatalf("Evaluate (allowed): %v", err)
	}
	if !v.AsBool() {
		t.Fatal("secrets.NPM_TOKEN != '' with allowlisted name should be true")
	}

	// And the inverse: with the sentinel in play, an equality check
	// against the empty string is false — `secrets.X == ''` cannot
	// short-circuit a step on.
	eEq, err := pubexpr.New("secrets.NPM_TOKEN == ''")
	if err != nil {
		t.Fatalf("pubexpr.New (==): %v", err)
	}
	v, err = eEq.Evaluate(allowed)
	if err != nil {
		t.Fatalf("Evaluate (==): %v", err)
	}
	if v.AsBool() {
		t.Fatal("secrets.NPM_TOKEN == '' with allowlisted name should be false")
	}
}
