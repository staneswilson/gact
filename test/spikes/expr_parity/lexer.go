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

// lex converts src into a flat token slice. We tokenise once up front so the
// parser is a straight slice walk — a simple shape that scales to the
// production package without restructuring.
func lex(src string) ([]token, error) {
	var out []token
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '\'':
			s, n, err := readString(src, i)
			if err != nil {
				return nil, err
			}
			out = append(out, token{Kind: tkString, Val: s, Pos: i})
			i += n
		case c == '.':
			out = append(out, token{Kind: tkDot, Val: ".", Pos: i})
			i++
		case c == '[':
			out = append(out, token{Kind: tkLBracket, Val: "[", Pos: i})
			i++
		case c == ']':
			out = append(out, token{Kind: tkRBracket, Val: "]", Pos: i})
			i++
		case c == '(':
			out = append(out, token{Kind: tkLParen, Val: "(", Pos: i})
			i++
		case c == ')':
			out = append(out, token{Kind: tkRParen, Val: ")", Pos: i})
			i++
		case c == ',':
			out = append(out, token{Kind: tkComma, Val: ",", Pos: i})
			i++
		case c == '=' && i+1 < len(src) && src[i+1] == '=':
			out = append(out, token{Kind: tkEq, Val: "==", Pos: i})
			i += 2
		case c == '!' && i+1 < len(src) && src[i+1] == '=':
			out = append(out, token{Kind: tkNeq, Val: "!=", Pos: i})
			i += 2
		case c == '<' && i+1 < len(src) && src[i+1] == '=':
			out = append(out, token{Kind: tkLte, Val: "<=", Pos: i})
			i += 2
		case c == '>' && i+1 < len(src) && src[i+1] == '=':
			out = append(out, token{Kind: tkGte, Val: ">=", Pos: i})
			i += 2
		case c == '<':
			out = append(out, token{Kind: tkLt, Val: "<", Pos: i})
			i++
		case c == '>':
			out = append(out, token{Kind: tkGt, Val: ">", Pos: i})
			i++
		case c == '&' && i+1 < len(src) && src[i+1] == '&':
			out = append(out, token{Kind: tkAnd, Val: "&&", Pos: i})
			i += 2
		case c == '|' && i+1 < len(src) && src[i+1] == '|':
			out = append(out, token{Kind: tkOr, Val: "||", Pos: i})
			i += 2
		case c == '!':
			out = append(out, token{Kind: tkBang, Val: "!", Pos: i})
			i++
		case c == '-' || (c >= '0' && c <= '9'):
			s, n := readNumber(src, i)
			out = append(out, token{Kind: tkNumber, Val: s, Pos: i})
			i += n
		case isIdentStart(c):
			s, n := readIdent(src, i)
			tk := token{Val: s, Pos: i}
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
			out = append(out, tk)
			i += n
		default:
			return nil, fmt.Errorf("lex: unexpected character %q at pos %d", c, i)
		}
	}
	out = append(out, token{Kind: tkEOF, Pos: len(src)})
	return out, nil
}

// readString consumes a single-quoted string. GH escapes the quote by doubling
// it: 'it''s' represents "it's". This is the only escape it recognises in
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
