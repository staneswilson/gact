package expr

import (
	"fmt"
	"strconv"
)

// nodeKind tags AST nodes. We use a single tagged-union struct rather
// than an interface hierarchy for two reasons: (1) the grammar has only
// ~12 productions so the visitor cost of a giant switch is trivial, and
// (2) tagged structs walk cache-friendly and allocate uniformly.
type nodeKind int

const (
	nString nodeKind = iota
	nNumber
	nBool
	nNull
	nIdent
	nMember
	nIndex
	nCall
	nNot
	nAnd
	nOr
	nEq
	nNeq
	nLt
	nLte
	nGt
	nGte
)

// node is the AST type. Each field is reused depending on Kind:
//   - Str: identifier name, string literal, or function/member name
//   - Num: numeric literal value (KindNumber-equivalent)
//   - Bool: boolean literal value
//   - Children: sub-expressions (operands, arguments, lookup base+index)
type node struct {
	Kind     nodeKind
	Str      string
	Num      float64
	Bool     bool
	Children []*node
}

// parser walks a pre-tokenised stream with a fixed precedence climb.
// Lowest to highest: || → && → equality (==,!=) → comparison (<,<=,>,>=)
// → unary ! → postfix (.field, [expr], (args)) → primary.
type parser struct {
	toks []token
	pos  int
}

// parseExpr tokenises src and runs the parser. The returned *node is
// the root of the AST; trailing tokens after the expression are
// reported as an error so users notice typos like "a b".
func parseExpr(src string) (*node, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	n, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.peek().Kind != tkEOF {
		return nil, fmt.Errorf("parse: trailing tokens starting at pos %d (%q)", p.peek().Pos, p.peek().Val)
	}
	return n, nil
}

func (p *parser) peek() token { return p.toks[p.pos] }

func (p *parser) next() token {
	t := p.toks[p.pos]
	p.pos++
	return t
}

// expect consumes the next token if it matches k, otherwise returns a
// positioned error. The caller passes a name so the message mentions
// the human-readable kind (e.g. "expected ')' at pos 14").
func (p *parser) expect(k tokenKind, name string) (token, error) {
	t := p.peek()
	if t.Kind != k {
		return token{}, fmt.Errorf("parse: expected %s at pos %d, got %q", name, t.Pos, t.Val)
	}
	return p.next(), nil
}

func (p *parser) parseOr() (*node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.peek().Kind == tkOr {
		p.next()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &node{Kind: nOr, Children: []*node{left, right}}
	}
	return left, nil
}

func (p *parser) parseAnd() (*node, error) {
	left, err := p.parseEq()
	if err != nil {
		return nil, err
	}
	for p.peek().Kind == tkAnd {
		p.next()
		right, err := p.parseEq()
		if err != nil {
			return nil, err
		}
		left = &node{Kind: nAnd, Children: []*node{left, right}}
	}
	return left, nil
}

func (p *parser) parseEq() (*node, error) {
	left, err := p.parseComparison()
	if err != nil {
		return nil, err
	}
	for p.peek().Kind == tkEq || p.peek().Kind == tkNeq {
		op := p.next()
		right, err := p.parseComparison()
		if err != nil {
			return nil, err
		}
		k := nEq
		if op.Kind == tkNeq {
			k = nNeq
		}
		left = &node{Kind: k, Children: []*node{left, right}}
	}
	return left, nil
}

// parseComparison handles <, <=, >, >=. These bind tighter than equality
// so `a == b < c` parses as `a == (b < c)`. That matches GH (and most
// C-style languages) — equality is a separate, lower-precedence level.
func (p *parser) parseComparison() (*node, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		k := p.peek().Kind
		var nk nodeKind
		switch k {
		case tkLt:
			nk = nLt
		case tkLte:
			nk = nLte
		case tkGt:
			nk = nGt
		case tkGte:
			nk = nGte
		default:
			return left, nil
		}
		p.next()
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = &node{Kind: nk, Children: []*node{left, right}}
	}
}

// parseUnary handles a leading `!`. We recurse so that `!!x` parses
// cleanly (double negation).
func (p *parser) parseUnary() (*node, error) {
	if p.peek().Kind == tkBang {
		p.next()
		inner, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &node{Kind: nNot, Children: []*node{inner}}, nil
	}
	return p.parsePostfix()
}

// parsePostfix handles the .field / [index] / (args) chain. A call is
// only legal directly on an identifier — GH does not have method calls
// or first-class function values.
func (p *parser) parsePostfix() (*node, error) {
	n, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for {
		switch p.peek().Kind {
		case tkDot:
			p.next()
			id, err := p.expect(tkIdent, "identifier")
			if err != nil {
				return nil, err
			}
			n = &node{Kind: nMember, Str: id.Val, Children: []*node{n}}
		case tkLBracket:
			p.next()
			idx, err := p.parseOr()
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(tkRBracket, "']'"); err != nil {
				return nil, err
			}
			n = &node{Kind: nIndex, Children: []*node{n, idx}}
		case tkLParen:
			if n.Kind != nIdent {
				return nil, fmt.Errorf("parse: call applied to non-identifier at pos %d", p.peek().Pos)
			}
			p.next()
			var args []*node
			if p.peek().Kind != tkRParen {
				for {
					a, err := p.parseOr()
					if err != nil {
						return nil, err
					}
					args = append(args, a)
					if p.peek().Kind != tkComma {
						break
					}
					p.next()
				}
			}
			if _, err := p.expect(tkRParen, "')'"); err != nil {
				return nil, err
			}
			n = &node{Kind: nCall, Str: n.Str, Children: args}
		default:
			return n, nil
		}
	}
}

func (p *parser) parsePrimary() (*node, error) {
	t := p.next()
	switch t.Kind {
	case tkString:
		return &node{Kind: nString, Str: t.Val}, nil
	case tkNumber:
		f, err := strconv.ParseFloat(t.Val, 64)
		if err != nil {
			return nil, fmt.Errorf("parse: bad number %q at pos %d", t.Val, t.Pos)
		}
		return &node{Kind: nNumber, Num: f}, nil
	case tkTrue:
		return &node{Kind: nBool, Bool: true}, nil
	case tkFalse:
		return &node{Kind: nBool, Bool: false}, nil
	case tkNull:
		return &node{Kind: nNull}, nil
	case tkIdent:
		return &node{Kind: nIdent, Str: t.Val}, nil
	case tkLParen:
		n, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tkRParen, "')'"); err != nil {
			return nil, err
		}
		return n, nil
	}
	return nil, fmt.Errorf("parse: unexpected token %q at pos %d", t.Val, t.Pos)
}
