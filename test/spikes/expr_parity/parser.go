package main

import (
	"fmt"
	"strconv"
)

// node is the AST type. We use a single tagged-union struct rather than an
// interface hierarchy because the spike's parser is small and the production
// evaluator (Task 0.6) can decide independently whether a sealed-interface
// design buys enough. For ~10 node kinds, a tag is shorter, has zero virtual
// dispatch, and walks cache-friendly.
type node struct {
	Kind     nodeKind
	Str      string  // identifier name, string literal, or operator name
	Num      float64 // numeric literal
	Bool     bool    // boolean literal
	Children []*node // operands / arguments / lookup target+index
}

type nodeKind int

const (
	nString nodeKind = iota
	nNumber
	nBool
	nNull
	nIdent       // root identifier (github, runner, matrix, status, ...)
	nMember      // Children[0].field — field stored in Str
	nIndex       // Children[0][Children[1]]
	nCall        // function call: Str = name, Children = args
	nNot         // !x
	nAnd         // x && y
	nOr          // x || y
	nEq          // x == y
	nNeq         // x != y
)

// parser tracks position in the token stream. Recursive-descent with a fixed
// precedence climb: or → and → eq → unary → postfix → primary.
type parser struct {
	toks []token
	pos  int
}

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

func (p *parser) expect(k tokenKind) (token, error) {
	t := p.peek()
	if t.Kind != k {
		return token{}, fmt.Errorf("parse: expected token kind %d at pos %d, got %q", k, t.Pos, t.Val)
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
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.peek().Kind == tkEq || p.peek().Kind == tkNeq {
		op := p.next()
		right, err := p.parseUnary()
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

// parsePostfix handles the .field / [index] / (args) chain.
func (p *parser) parsePostfix() (*node, error) {
	n, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for {
		switch p.peek().Kind {
		case tkDot:
			p.next()
			id, err := p.expect(tkIdent)
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
			if _, err := p.expect(tkRBracket); err != nil {
				return nil, err
			}
			n = &node{Kind: nIndex, Children: []*node{n, idx}}
		case tkLParen:
			// Function call. The callee must be an identifier — GH does not have
			// first-class function values.
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
			if _, err := p.expect(tkRParen); err != nil {
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
		if _, err := p.expect(tkRParen); err != nil {
			return nil, err
		}
		return n, nil
	default:
		return nil, fmt.Errorf("parse: unexpected token %q at pos %d", t.Val, t.Pos)
	}
}
