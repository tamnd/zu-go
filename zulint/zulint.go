// Package zulint holds the static checks for the Go client, as
// [analysis.Analyzer] values that plug into go vet, golangci-lint, or
// the command in cmd/zulint.
//
//	go install github.com/tamnd/zu-go/zulint/cmd/zulint@latest
//	zulint ./...
//
// Three checks, and they are three because these are the mistakes that
// actually happen rather than the ones that could. Every one of them
// compiles, passes review, and is a use-after-free, a swallowed
// failure, or a data race at run time.
//
//   - [ViewAfterClose], for a borrowed column read after the result it
//     was borrowed from is closed or handed to Arrow. The columnar
//     readers hand back memory the engine owns, closing the result frees
//     it, and exporting it gives it to somebody else.
//   - [RowsErr], for a loop over a result that never asks whether the
//     loop ended because the rows ran out or because something failed.
//   - [ConnShare], for a *zu.Conn reachable from two goroutines. A
//     connection is the state that cannot be shared, which is why
//     [zu.DB.Connect] exists.
//
// None of them needs the engine, the C toolchain or a database. They
// read source.
package zulint

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// Package is the import path the analyzers recognise types by. A type
// is this client's type because of where it was declared and not
// because of what it is called, so a local type named Rows is not one.
const Package = "github.com/tamnd/zu-go"

// Analyzers is every check here, which is what a driver wants.
func Analyzers() []*analysis.Analyzer {
	return []*analysis.Analyzer{ViewAfterClose, RowsErr, ConnShare}
}

// Ignore is the comment that turns a check off, on the line it is
// written or on the line after it.
//
//	rows, err := conn.Query(ctx, q) //zulint:ignore this test is the one that proves the refusal
//
// It exists because code that provokes a mistake on purpose is still
// code, and a test that proves a connection refuses a second goroutine
// has to give it one. Everything after the word is for whoever reads
// the line next and is not parsed.
const Ignore = "zulint:ignore"

// suppress is the lines an Ignore covers, by file.
type suppress map[string]map[int]bool

func suppressions(pass *analysis.Pass) suppress {
	s := suppress{}
	for _, f := range pass.Files {
		for _, g := range f.Comments {
			for _, c := range g.List {
				if !strings.Contains(c.Text, Ignore) {
					continue
				}
				at := pass.Fset.Position(c.Pos())
				if s[at.Filename] == nil {
					s[at.Filename] = map[int]bool{}
				}
				s[at.Filename][at.Line] = true
				s[at.Filename][at.Line+1] = true
			}
		}
	}
	return s
}

// report is Reportf with the ignore comment honoured.
func (s suppress) report(pass *analysis.Pass, pos token.Pos, format string, args ...any) {
	at := pass.Fset.Position(pos)
	if s[at.Filename][at.Line] {
		return
	}
	pass.Reportf(pos, format, args...)
}

// isType reports whether t is *zu.<name>, following one pointer and no
// more, and reading the declaring package rather than the spelling.
func isType(t types.Type, name string) bool {
	p, ok := types.Unalias(t).(*types.Pointer)
	if !ok {
		return false
	}
	n, ok := types.Unalias(p.Elem()).(*types.Named)
	if !ok {
		return false
	}
	o := n.Obj()
	return o != nil && o.Name() == name && o.Pkg() != nil && o.Pkg().Path() == Package
}

// method takes a call apart into the value it was called on and the
// name of the method, when it is a method call on a *zu.<typeName> and
// the receiver is a plain variable. A receiver that is an expression
// rather than a variable, like conns[i].Query(...), is not something
// these checks can follow, and guessing at it is how a linter earns a
// reputation.
func method(info *types.Info, call *ast.CallExpr, typeName string) (recv *types.Var, name string, ok bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil, "", false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return nil, "", false
	}
	v, ok := info.Uses[id].(*types.Var)
	if !ok {
		if v, ok = info.Defs[id].(*types.Var); !ok {
			return nil, "", false
		}
	}
	if !isType(v.Type(), typeName) {
		return nil, "", false
	}
	return v, sel.Sel.Name, true
}

// bodies is every function body in a file: the declarations and the
// literals, each with the node a diagnostic about the whole function
// should point at. A literal is walked on its own as well as inside
// the function holding it, because a goroutine's body is a scope of
// its own and reading it as part of its parent is how a check about
// goroutines gets both of them wrong.
func bodies(f *ast.File) []*ast.BlockStmt {
	var out []*ast.BlockStmt
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncDecl:
			if n.Body != nil {
				out = append(out, n.Body)
			}
		case *ast.FuncLit:
			out = append(out, n.Body)
		}
		return true
	})
	return out
}
