package expr

import pubexpr "github.com/staneswilson/gact/pkg/expr"

// init wires the internal implementation into the public façade. The
// public package (pkg/expr) declares the API types and a
// RegisterCompiler hook; we hand it a closure that builds an
// evaluator's evaluate method.
//
// Why this indirection: archlint forbids pkg/ from importing internal/
// (ADR-003: pkg/ is the semver-stable surface; internal/ is free to
// change). The data flow therefore has to run the other way — the
// internal package imports pkg/expr for types and registers behaviour
// at init() time. Consumers blank-import this package once (typically
// from main, in the style of database/sql drivers) to make
// pkg/expr.New work.
func init() {
	pubexpr.RegisterCompiler(func(src string) (func(pubexpr.Context) (pubexpr.Value, error), error) {
		e, err := compile(src)
		if err != nil {
			return nil, err
		}
		return e.evaluate, nil
	})
}
