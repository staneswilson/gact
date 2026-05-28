// Position helpers for the YAML → workflow IR translation.
//
// yaml.v3 hands us a tree of *yaml.Node values with line and column
// information. The helpers in this file convert those to wf.SourceSpan
// values and locate child nodes inside mappings.
//
// The package keeps these concerns isolated from parser.go so the decode
// helpers can read like a top-down translation of the YAML schema, with
// span/lookup mechanics hidden behind small, self-explanatory verbs.

package parser

import (
	"gopkg.in/yaml.v3"

	wf "github.com/staneswilson/gact/pkg/workflow"
)

// span returns a SourceSpan for the given yaml.Node anchored at path.
//
// yaml.v3 reports 1-based Line and Column positions, which matches the
// contract documented on wf.SourceSpan. EndLine and EndCol are derived
// from the node's content: for scalar nodes we approximate the end as the
// start plus the rendered Value length, which is sufficient for
// diagnostics that quote a single token. For mapping/sequence nodes we
// extend EndLine/EndCol to the position of the final descendant — this
// gives diagnostics a useful "block here" range without being precise to
// the byte.
func span(path string, n *yaml.Node) wf.SourceSpan {
	if n == nil {
		return wf.SourceSpan{Path: path}
	}
	s := wf.SourceSpan{
		Path:    path,
		Line:    n.Line,
		Column:  n.Column,
		EndLine: n.Line,
		EndCol:  n.Column,
	}
	switch n.Kind {
	case yaml.ScalarNode:
		// Approximate the end column as start + len(value). YAML values can
		// span multiple lines (block scalars, folded scalars), so we keep
		// EndLine equal to Line and let consumers treat EndCol as a hint.
		s.EndCol = n.Column + len(n.Value)
	case yaml.MappingNode, yaml.SequenceNode:
		if last := lastDescendant(n); last != nil {
			s.EndLine = last.Line
			s.EndCol = last.Column + len(last.Value)
		}
	case yaml.DocumentNode, yaml.AliasNode:
		if last := lastDescendant(n); last != nil {
			s.EndLine = last.Line
			s.EndCol = last.Column + len(last.Value)
		}
	}
	return s
}

// lastDescendant returns the deepest right-most descendant of n. It is
// used to extend a span to cover the full block in the source — the deepest
// scalar in a mapping or sequence is a reasonable approximation of "where
// this block ends" for diagnostic ranges.
func lastDescendant(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	if len(n.Content) == 0 {
		if n.Kind == yaml.ScalarNode {
			return n
		}
		return nil
	}
	return lastDescendant(n.Content[len(n.Content)-1])
}

// findChild locates a child of a mapping node by its string key. It
// returns the key node, the value node, and a found flag. The key node is
// returned in addition to the value so that callers reporting errors can
// point at the *key* — for instance, `unknown trigger "fooo"` should
// underline `fooo:` rather than its value.
//
// findChild returns (nil, nil, false) when n is not a mapping, when key
// is empty, or when no entry matches. Empty key never matches because
// YAML allows the explicit `null:` key which we deliberately ignore in
// our schema.
func findChild(n *yaml.Node, key string) (k, v *yaml.Node, ok bool) {
	if n == nil || n.Kind != yaml.MappingNode || key == "" {
		return nil, nil, false
	}
	// A YAML mapping stores entries as alternating key, value content
	// nodes. We walk pairs and string-compare keys.
	for i := 0; i+1 < len(n.Content); i += 2 {
		kn := n.Content[i]
		if kn.Kind != yaml.ScalarNode {
			continue
		}
		if kn.Value == key {
			return kn, n.Content[i+1], true
		}
	}
	return nil, nil, false
}

