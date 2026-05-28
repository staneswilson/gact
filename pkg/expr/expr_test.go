// Black-box test for the public façade. Lives in package expr_test so we
// only see the exported API surface that ADR-003 promises will remain
// semver-stable.
package expr_test

import (
	"testing"

	// The internal implementation must be imported (for its init()) for
	// pkg/expr to have a registered compiler. Production consumers do the
	// same blank import — typically once, from main, in the style of
	// database/sql drivers. See ADR-003 and pkg/expr/expr.go for the
	// rationale (archlint forbids pkg/ importing internal/).
	_ "github.com/staneswilson/gact/internal/authoring/expr"
	"github.com/staneswilson/gact/pkg/expr"
)

// TestFacade_LiteralString round-trips a string literal through the façade:
// parse, evaluate against the empty context, render. If any of the public
// names disappear or change shape, this test stops compiling — which is the
// whole point of having a black-box test for the public surface.
func TestFacade_LiteralString(t *testing.T) {
	e, err := expr.New("'hello'")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	v, err := e.Evaluate(expr.NoContext())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if got, want := v.AsString(), "hello"; got != want {
		t.Fatalf("AsString = %q, want %q", got, want)
	}
}

// TestFacade_LiteralNumber confirms that the public Value type's AsString
// renders numbers in the GH-actions style (no trailing .0 for integers).
func TestFacade_LiteralNumber(t *testing.T) {
	e, err := expr.New("42")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	v, err := e.Evaluate(expr.NoContext())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if got, want := v.AsString(), "42"; got != want {
		t.Fatalf("AsString = %q, want %q", got, want)
	}
}

// TestFacade_ParseError surfaces a parse failure through the façade so
// consumers get a clear, non-panicking error from malformed input.
func TestFacade_ParseError(t *testing.T) {
	if _, err := expr.New("("); err == nil {
		t.Fatalf("expected parse error for unbalanced paren, got nil")
	}
}
