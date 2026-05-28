package expr

import (
	"strings"
	"testing"
)

// TestLex_SingleToken covers every token kind in isolation so a regression
// in any one of them points straight at the offending lexeme.
func TestLex_SingleToken(t *testing.T) {
	cases := []struct {
		src  string
		kind tokenKind
		val  string
	}{
		{"abc", tkIdent, "abc"},
		{"_foo123", tkIdent, "_foo123"},
		{"'hello'", tkString, "hello"},
		{"'it''s'", tkString, "it's"},
		{"42", tkNumber, "42"},
		{"3.14", tkNumber, "3.14"},
		{"-7", tkNumber, "-7"},
		{"true", tkTrue, "true"},
		{"false", tkFalse, "false"},
		{"null", tkNull, "null"},
		{".", tkDot, "."},
		{"[", tkLBracket, "["},
		{"]", tkRBracket, "]"},
		{"(", tkLParen, "("},
		{")", tkRParen, ")"},
		{",", tkComma, ","},
		{"==", tkEq, "=="},
		{"!=", tkNeq, "!="},
		{"<", tkLt, "<"},
		{"<=", tkLte, "<="},
		{">", tkGt, ">"},
		{">=", tkGte, ">="},
		{"&&", tkAnd, "&&"},
		{"||", tkOr, "||"},
		{"!", tkBang, "!"},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			got, err := lex(tc.src)
			if err != nil {
				t.Fatalf("lex(%q): %v", tc.src, err)
			}
			// Always EOF appended, so we expect exactly 2 tokens.
			if len(got) != 2 {
				t.Fatalf("lex(%q): got %d tokens, want 2 (token+EOF)", tc.src, len(got))
			}
			if got[0].Kind != tc.kind {
				t.Fatalf("kind = %d, want %d", got[0].Kind, tc.kind)
			}
			if got[0].Val != tc.val {
				t.Fatalf("val = %q, want %q", got[0].Val, tc.val)
			}
			if got[1].Kind != tkEOF {
				t.Fatalf("expected EOF as final token, got %d", got[1].Kind)
			}
		})
	}
}

// TestLex_Whitespace confirms that all forms of whitespace are skipped and
// do not produce tokens of their own.
func TestLex_Whitespace(t *testing.T) {
	got, err := lex("  a\t\n b\r")
	if err != nil {
		t.Fatalf("lex: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d tokens, want 3 (a, b, EOF)", len(got))
	}
	if got[0].Val != "a" || got[1].Val != "b" {
		t.Fatalf("got values %q,%q; want a,b", got[0].Val, got[1].Val)
	}
}

// TestLex_PositionsArePreserved makes sure the Pos field tracks the offset
// of each token's first byte — the parser turns this into "trailing
// tokens at pos N" error messages.
func TestLex_PositionsArePreserved(t *testing.T) {
	got, err := lex("a == 'b'")
	if err != nil {
		t.Fatalf("lex: %v", err)
	}
	// Token positions: a@0, ==@2, 'b'@5, EOF@8
	want := []int{0, 2, 5, 8}
	if len(got) != len(want) {
		t.Fatalf("got %d tokens, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Pos != w {
			t.Fatalf("tok[%d].Pos = %d, want %d", i, got[i].Pos, w)
		}
	}
}

// TestLex_Errors covers the malformed inputs the lexer must surface as
// positioned errors rather than panicking or silently dropping bytes.
func TestLex_Errors(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string // substring that must appear in the error message
	}{
		{"unterminated string", "'abc", "unterminated string"},
		{"unterminated empty string", "'", "unterminated string"},
		{"lone ampersand", "a & b", "unexpected character"},
		{"lone pipe", "a | b", "unexpected character"},
		{"stray at-sign", "@", "unexpected character"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := lex(tc.src)
			if err == nil {
				t.Fatalf("expected error for %q, got nil", tc.src)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}
