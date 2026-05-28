package main

import (
	"fmt"
	"strings"
	"unicode"
)

// tokenKind enumerates lexeme classes. Kept intentionally small — the spike
// only needs literals, identifiers, the comparison / boolean operators, and
// punctuation for property lookups, function calls, and grouping.
type tokenKind int

const (
	tkEOF tokenKind = iota
	tkIdent
	tkString
	tkNumber
	tkTrue
	tkFalse
	tkNull
	tkDot
	tkLBracket
	tkRBracket
	tkLParen
	tkRParen
	tkComma
	tkEq
	tkNeq
	tkLt
	tkGt
	tkLte
	tkGte
	tkAnd
	tkOr
	tkBang
)

type token struct {
	Kind tokenKind
	Val  string
	Pos  int
}

func (t token) String() string {
	return fmt.Sprintf("<%d %q@%d>", t.Kind, t.Val, t.Pos)
}

// twoCharOp describes one of the multi-byte operators: the required second
// byte, the resulting token kind, and the literal value to store on the
// token. Pulled out so the lex dispatch table can express "= followed by =
// is ==" without an explicit switch arm per case.
type twoCharOp struct {
	second byte
	kind   tokenKind
	val    string
}

// twoCharOps lists the two-byte operators by their first byte. Entries
// here MUST be tried before singleCharOps, because '<' / '>' / '!' are
// also valid as single-byte tokens — the longest-match rule means '<='
// has to win over '<' when both could fire.
var twoCharOps = map[byte]twoCharOp{
	'=': {'=', tkEq, "=="},
	'!': {'=', tkNeq, "!="},
	'<': {'=', tkLte, "<="},
	'>': {'=', tkGte, ">="},
	'&': {'&', tkAnd, "&&"},
	'|': {'|', tkOr, "||"},
}

// singleCharOps maps a byte to the token kind for single-byte punctuation
// and unary operators. '=', '&', and '|' are intentionally absent: they
// are only valid as part of a two-character operator, so a bare '=' must
// fall through to the "unexpected character" error rather than tokenise.
var singleCharOps = map[byte]tokenKind{
	'.': tkDot,
	'[': tkLBracket,
	']': tkRBracket,
	'(': tkLParen,
	')': tkRParen,
	',': tkComma,
	'<': tkLt,
	'>': tkGt,
	'!': tkBang,
}

// lex converts src into a flat token slice. We tokenise once up front so the
// parser is a straight slice walk — a simple shape that scales to the
// production package without restructuring.
//
// The implementation is a tiny state machine: lex itself owns the loop and
// EOF, step decides which sub-consumer handles the byte at lx.i. Splitting
// it this way keeps each function below the gocyclo threshold and makes
// the longest-match precedence (two-char ops before single-char ops)
// visible in one place.
func lex(src string) ([]token, error) {
	lx := &lexer{src: src}
	for lx.i < len(lx.src) {
		if err := lx.step(); err != nil {
			return nil, err
		}
	}
	lx.out = append(lx.out, token{Kind: tkEOF, Pos: len(src)})
	return lx.out, nil
}

// lexer threads the mutable state of a single lex pass: source bytes,
// current index, and the token slice being built up. Methods consume
// bytes from src[i:] and advance i; they never seek backwards.
type lexer struct {
	src string
	i   int
	out []token
}

// step handles one logical lexeme starting at lx.i. The order of checks
// encodes lexer precedence: whitespace, string literal, two-byte op,
// single-byte op, number, identifier, error. Each arm either consumes
// bytes and returns nil, or returns an error that bubbles to lex.
func (lx *lexer) step() error {
	c := lx.src[lx.i]
	if isWhitespace(c) {
		lx.i++
		return nil
	}
	if c == '\'' {
		return lx.consumeString()
	}
	if lx.tryTwoCharOp() {
		return nil
	}
	if lx.trySingleCharOp() {
		return nil
	}
	if c == '-' || (c >= '0' && c <= '9') {
		lx.consumeNumber()
		return nil
	}
	if isIdentStart(c) {
		lx.consumeIdent()
		return nil
	}
	return fmt.Errorf("lex: unexpected character %q at pos %d", c, lx.i)
}

// isWhitespace reports whether c is one of the four whitespace bytes the
// lexer recognises. GH expressions don't permit other whitespace forms.
func isWhitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// consumeString reads a quoted string starting at lx.i (which must point
// at the opening quote) and appends the resulting token.
func (lx *lexer) consumeString() error {
	s, n, err := readString(lx.src, lx.i)
	if err != nil {
		return err
	}
	lx.out = append(lx.out, token{Kind: tkString, Val: s, Pos: lx.i})
	lx.i += n
	return nil
}

// tryTwoCharOp emits a two-byte operator token if one matches at lx.i and
// returns true; otherwise leaves state untouched and returns false.
func (lx *lexer) tryTwoCharOp() bool {
	if lx.i+1 >= len(lx.src) {
		return false
	}
	op, ok := twoCharOps[lx.src[lx.i]]
	if !ok {
		return false
	}
	if lx.src[lx.i+1] != op.second {
		return false
	}
	lx.out = append(lx.out, token{Kind: op.kind, Val: op.val, Pos: lx.i})
	lx.i += 2
	return true
}

// trySingleCharOp emits a single-byte punctuation/unary token if one
// matches at lx.i and returns true; otherwise leaves state untouched.
func (lx *lexer) trySingleCharOp() bool {
	kind, ok := singleCharOps[lx.src[lx.i]]
	if !ok {
		return false
	}
	lx.out = append(lx.out, token{Kind: kind, Val: string(lx.src[lx.i]), Pos: lx.i})
	lx.i++
	return true
}

// consumeNumber reads a numeric literal (optionally negative, optionally
// decimal) starting at lx.i and appends the resulting token.
func (lx *lexer) consumeNumber() {
	s, n := readNumber(lx.src, lx.i)
	lx.out = append(lx.out, token{Kind: tkNumber, Val: s, Pos: lx.i})
	lx.i += n
}

// consumeIdent reads an identifier starting at lx.i and appends a token
// with the right kind: literal true/false/null are recognised by their
// case-insensitive spelling, everything else becomes tkIdent.
func (lx *lexer) consumeIdent() {
	s, n := readIdent(lx.src, lx.i)
	tk := token{Val: s, Pos: lx.i}
	switch strings.ToLower(s) {
	case "true":
		tk.Kind = tkTrue
	case "false":
		tk.Kind = tkFalse
	case "null":
		tk.Kind = tkNull
	default:
		tk.Kind = tkIdent
	}
	lx.out = append(lx.out, tk)
	lx.i += n
}

// readString consumes a single-quoted string. GH escapes the quote by doubling
// it: 'it”s' represents "it's". This is the only escape it recognises in
// expressions.
func readString(src string, start int) (string, int, error) {
	var b strings.Builder
	i := start + 1
	for i < len(src) {
		if src[i] == '\'' {
			if i+1 < len(src) && src[i+1] == '\'' {
				b.WriteByte('\'')
				i += 2
				continue
			}
			return b.String(), i - start + 1, nil
		}
		b.WriteByte(src[i])
		i++
	}
	return "", 0, fmt.Errorf("lex: unterminated string starting at pos %d", start)
}

func readNumber(src string, start int) (string, int) {
	i := start
	if src[i] == '-' {
		i++
	}
	for i < len(src) && (src[i] >= '0' && src[i] <= '9') {
		i++
	}
	if i < len(src) && src[i] == '.' {
		i++
		for i < len(src) && (src[i] >= '0' && src[i] <= '9') {
			i++
		}
	}
	return src[start:i], i - start
}

func readIdent(src string, start int) (string, int) {
	i := start
	for i < len(src) && isIdentPart(src[i]) {
		i++
	}
	return src[start:i], i - start
}

func isIdentStart(c byte) bool {
	return c == '_' || unicode.IsLetter(rune(c))
}

func isIdentPart(c byte) bool {
	return c == '_' || unicode.IsLetter(rune(c)) || (c >= '0' && c <= '9')
}
