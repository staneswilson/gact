package expr

import (
	"fmt"
	"strings"
	"unicode"
)

// tokenKind enumerates the lexeme classes the GH expression grammar
// admits. Kept intentionally small — there are no compound assignments,
// no bitwise operators, no string interpolation, no template directives.
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

// token bundles the kind, source text, and byte offset together so the
// parser can produce positioned error messages without re-walking src.
type token struct {
	Kind tokenKind
	Val  string
	Pos  int
}

// punctKinds maps single-character punctuation to its token kind. Lookups
// are O(1) and the table keeps the per-byte branch out of the main lexer
// loop.
var punctKinds = map[byte]tokenKind{
	'.': tkDot,
	'[': tkLBracket,
	']': tkRBracket,
	'(': tkLParen,
	')': tkRParen,
	',': tkComma,
}

// singleOpKinds maps single-character operators (used when no two-character
// form matches). `==`, `!=`, `<=`, `>=`, `&&`, `||` are handled separately.
var singleOpKinds = map[byte]tokenKind{
	'<': tkLt,
	'>': tkGt,
	'!': tkBang,
}

// lex converts src into a flat slice of tokens terminated by tkEOF.
// We tokenise once up front so the parser is a straight slice walk —
// the simpler shape pays off when the parser needs to look at multiple
// tokens to disambiguate.
func lex(src string) ([]token, error) {
	var out []token
	i := 0
	for i < len(src) {
		if isSpace(src[i]) {
			i++
			continue
		}
		tok, n, err := nextToken(src, i)
		if err != nil {
			return nil, err
		}
		out = append(out, tok)
		i += n
	}
	out = append(out, token{Kind: tkEOF, Pos: len(src)})
	return out, nil
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// nextToken consumes the single token that starts at src[i] and returns it
// together with the number of bytes consumed.
func nextToken(src string, i int) (token, int, error) {
	c := src[i]
	if c == '\'' {
		s, n, err := readString(src, i)
		if err != nil {
			return token{}, 0, err
		}
		return token{Kind: tkString, Val: s, Pos: i}, n, nil
	}
	if k, ok := punctKinds[c]; ok {
		return token{Kind: k, Val: string(c), Pos: i}, 1, nil
	}
	if tok, n, ok := lexCompoundOp(src, i); ok {
		return tok, n, nil
	}
	if k, ok := singleOpKinds[c]; ok {
		return token{Kind: k, Val: string(c), Pos: i}, 1, nil
	}
	if isNumberStart(c) {
		s, n := readNumber(src, i)
		return token{Kind: tkNumber, Val: s, Pos: i}, n, nil
	}
	if isIdentStart(c) {
		s, n := readIdent(src, i)
		return identToken(s, i), n, nil
	}
	return token{}, 0, fmt.Errorf("lex: unexpected character %q at pos %d", c, i)
}

func isNumberStart(c byte) bool {
	return c == '-' || (c >= '0' && c <= '9')
}

// lexCompoundOp returns the two-character operator starting at src[i] when
// one matches. The boolean third return value distinguishes "matched" from
// "no compound operator here" so callers can fall through to single-char
// operator handling.
func lexCompoundOp(src string, i int) (token, int, bool) {
	if i+1 >= len(src) {
		return token{}, 0, false
	}
	pair := src[i : i+2]
	kind, ok := compoundOpKinds[pair]
	if !ok {
		return token{}, 0, false
	}
	return token{Kind: kind, Val: pair, Pos: i}, 2, true
}

var compoundOpKinds = map[string]tokenKind{
	"==": tkEq,
	"!=": tkNeq,
	"<=": tkLte,
	">=": tkGte,
	"&&": tkAnd,
	"||": tkOr,
}

// identToken turns an identifier lexeme into a token, classifying the
// reserved words true / false / null as their dedicated kinds and everything
// else as tkIdent.
func identToken(s string, pos int) token {
	tk := token{Val: s, Pos: pos}
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
	return tk
}

// readString consumes a single-quoted string. GH escapes the quote by
// doubling it: 'it”s' represents the three-character string "it's".
// This is the only escape the GH expression grammar recognises — no
// backslash escapes, no unicode escapes.
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

// readNumber consumes an optional leading minus sign followed by one or
// more decimal digits and an optional fractional part. No exponents —
// they are not part of the GH grammar.
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

// readIdent consumes a run of identifier characters. GH identifiers
// can contain letters, digits, and underscores; the first character
// must be a letter or underscore.
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
