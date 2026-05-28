package expr

import (
	"strings"
	"testing"
)

// TestParse_Literals covers each of the four primary literal node kinds.
func TestParse_Literals(t *testing.T) {
	cases := []struct {
		src  string
		kind nodeKind
	}{
		{"'x'", nString},
		{"42", nNumber},
		{"true", nBool},
		{"false", nBool},
		{"null", nNull},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			n, err := parseExpr(tc.src)
			if err != nil {
				t.Fatalf("parseExpr: %v", err)
			}
			if n.Kind != tc.kind {
				t.Fatalf("kind = %d, want %d", n.Kind, tc.kind)
			}
		})
	}
}

// TestParse_OperatorPrecedence checks that && binds tighter than || (so
// `a || b && c` groups as `a || (b && c)`) and that equality binds tighter
// than &&. We assert structure rather than evaluate so the test stays
// purely about parsing.
func TestParse_OperatorPrecedence(t *testing.T) {
	n, err := parseExpr("a || b && c")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if n.Kind != nOr {
		t.Fatalf("top-level kind = %d, want nOr", n.Kind)
	}
	if n.Children[1].Kind != nAnd {
		t.Fatalf("right child kind = %d, want nAnd", n.Children[1].Kind)
	}

	n, err = parseExpr("a == b && c")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if n.Kind != nAnd {
		t.Fatalf("top-level kind = %d, want nAnd", n.Kind)
	}
	if n.Children[0].Kind != nEq {
		t.Fatalf("left child kind = %d, want nEq", n.Children[0].Kind)
	}

	// Comparison binds tighter than equality: `a == b < c` → `a == (b < c)`.
	n, err = parseExpr("a == b < c")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if n.Kind != nEq {
		t.Fatalf("top-level kind = %d, want nEq", n.Kind)
	}
	if n.Children[1].Kind != nLt {
		t.Fatalf("right child kind = %d, want nLt", n.Children[1].Kind)
	}
}

// TestParse_LeftAssociativity verifies that `a || b || c` parses as
// `(a || b) || c` — sequence of binary operators chains left-to-right.
func TestParse_LeftAssociativity(t *testing.T) {
	n, err := parseExpr("a || b || c")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if n.Kind != nOr || n.Children[0].Kind != nOr {
		t.Fatalf("not left-associative: top=%d, left=%d", n.Kind, n.Children[0].Kind)
	}
}

// TestParse_MemberAndIndex confirms that postfix chains (.foo[expr]) layer
// correctly on a root ident.
func TestParse_MemberAndIndex(t *testing.T) {
	n, err := parseExpr("github.event.commits[0].id")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Outermost node should be nMember with Str=="id".
	if n.Kind != nMember || n.Str != "id" {
		t.Fatalf("top = (%d, %q), want (nMember, id)", n.Kind, n.Str)
	}
	// Below it: nIndex wrapping nMember("commits") wrapping nMember("event") wrapping nIdent("github").
	if n.Children[0].Kind != nIndex {
		t.Fatalf("expected nIndex inside, got %d", n.Children[0].Kind)
	}
}

// TestParse_CallArgs accepts identifiers as the callee and a comma-list of
// expressions as args. Anything else (call applied to a member chain) is a
// parse error — GH does not have method calls.
func TestParse_CallArgs(t *testing.T) {
	n, err := parseExpr("contains(a, 'b', 3)")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if n.Kind != nCall || n.Str != "contains" {
		t.Fatalf("got (%d, %q), want (nCall, contains)", n.Kind, n.Str)
	}
	if len(n.Children) != 3 {
		t.Fatalf("argc = %d, want 3", len(n.Children))
	}
}

// TestParse_Errors locks in friendly parse-error messages for the common
// mistakes a user could make.
func TestParse_Errors(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"missing close paren", "(a", "expected"},
		{"trailing operator", "a ||", "unexpected"},
		{"trailing tokens", "a b", "trailing"},
		{"call on member", "a.b()", "non-identifier"},
		{"missing close bracket", "a[1", "expected"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseExpr(tc.src)
			if err == nil {
				t.Fatalf("expected parse error for %q, got nil", tc.src)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}
