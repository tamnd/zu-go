package zulint

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/ctrlflow"
	"golang.org/x/tools/go/cfg"
)

// ViewAfterClose finds a column borrowed from a result and used after
// the result was closed.
//
// Int64s, Float64s, NodeOffsets and Valid hand back the engine's own
// memory rather than a copy, which is the whole point of them: a
// million integers cost nothing to read and nothing to hold. What they
// cost is a lifetime. The slice is valid until Rows.Close and not one
// statement longer, and Go's type system has nothing to say about
// that, so this does.
//
// Two shapes are reported. A use that can run after a Close, which is
// asked of the control flow graph rather than of the order of the
// lines, so a use inside a loop that the Close can reach counts even
// though it is written above it. And a view returned out of the
// function that closes the result, which is the same bug written so it
// crashes somewhere else.
var ViewAfterClose = &analysis.Analyzer{
	Name:     "viewafterclose",
	Doc:      "report a columnar view used after the result it borrows from is closed",
	URL:      "https://github.com/tamnd/zu-go/tree/main/zulint",
	Requires: []*analysis.Analyzer{ctrlflow.Analyzer},
	Run:      runViewAfterClose,
}

// views are the four methods that lend rather than copy.
var views = map[string]bool{
	"Int64s":      true,
	"Float64s":    true,
	"NodeOffsets": true,
	"Valid":       true,
}

func runViewAfterClose(pass *analysis.Pass) (any, error) {
	graphs := pass.ResultOf[ctrlflow.Analyzer].(*ctrlflow.CFGs)
	quiet := suppressions(pass)
	for _, f := range pass.Files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncDecl:
				if n.Body != nil {
					checkViews(pass, quiet, graphs.FuncDecl(n), n.Body)
				}
			case *ast.FuncLit:
				checkViews(pass, quiet, graphs.FuncLit(n), n.Body)
			}
			return true
		})
	}
	return nil, nil
}

// view is a variable holding a borrowed column, and the result it was
// borrowed from.
type view struct {
	name string
	from *types.Var
}

func checkViews(pass *analysis.Pass, quiet suppress, g *cfg.CFG, body *ast.BlockStmt) {
	if g == nil {
		return
	}
	info := pass.TypesInfo

	// Everything below is about this function's own statements. A
	// nested literal has a graph of its own and is visited on its own,
	// and reading its body as part of this one would put a borrow and a
	// close in an order neither of them is in.
	borrowed := map[*types.Var]view{}
	closed := map[*types.Var]bool{}
	var closes []*ast.CallExpr

	own(body, func(n ast.Node) {
		switch n := n.(type) {
		case *ast.AssignStmt:
			if len(n.Rhs) != 1 {
				return
			}
			call, ok := n.Rhs[0].(*ast.CallExpr)
			if !ok {
				return
			}
			recv, name, ok := method(info, call, "Rows")
			if !ok || !views[name] {
				return
			}
			id, ok := n.Lhs[0].(*ast.Ident)
			if !ok || id.Name == "_" {
				return
			}
			v, _ := info.Defs[id].(*types.Var)
			if v == nil {
				v, _ = info.Uses[id].(*types.Var)
			}
			if v != nil {
				borrowed[v] = view{name: name, from: recv}
			}
		case *ast.CallExpr:
			if recv, name, ok := method(info, n, "Rows"); ok && name == "Close" {
				closed[recv] = true
				closes = append(closes, n)
			}
		}
	})

	// A deferred or go-started Close still closes, but it does not
	// happen where it is written, so it counts for the escape rule and
	// not for the reachability one.
	deferred := map[*ast.CallExpr]bool{}
	own(body, func(n ast.Node) {
		switch n := n.(type) {
		case *ast.DeferStmt:
			deferred[n.Call] = true
		case *ast.GoStmt:
			deferred[n.Call] = true
		}
	})

	if len(borrowed) == 0 {
		return
	}

	reported := map[ast.Node]bool{}

	for _, c := range closes {
		if deferred[c] {
			continue
		}
		recv, _, _ := method(info, c, "Rows")
		for _, n := range after(g, c) {
			for v, b := range borrowed {
				if b.from != recv {
					continue
				}
				id := use(info, n, v)
				if id == nil || reported[id] {
					continue
				}
				reported[id] = true
				quiet.report(pass, id.Pos(), "%s borrows from %s, and %s.Close has already freed it",
					v.Name(), recv.Name(), recv.Name())
			}
		}
	}

	// The same bug written so that it crashes in the caller: the
	// function closes the result and hands the borrow out anyway.
	own(body, func(n ast.Node) {
		ret, ok := n.(*ast.ReturnStmt)
		if !ok {
			return
		}
		for _, r := range ret.Results {
			id, ok := r.(*ast.Ident)
			if !ok {
				continue
			}
			v, _ := info.Uses[id].(*types.Var)
			b, ok := borrowed[v]
			if !ok || !closed[b.from] {
				continue
			}
			quiet.report(pass, id.Pos(), "%s borrows from %s, which this function closes, so the caller gets freed memory",
				v.Name(), b.from.Name())
		}
	})
}

// own walks a function body without descending into the bodies of the
// literals inside it, so that what it reports belongs to this function
// and not to a goroutine somebody started from it.
func own(body *ast.BlockStmt, visit func(ast.Node)) {
	ast.Inspect(body, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		if n != nil {
			visit(n)
		}
		return true
	})
}

// after is every node of the graph that a call can be followed by:
// the rest of its own block, and every block reachable from it. A
// block reachable from itself, which is what a loop is, brings all of
// its own nodes back with it.
func after(g *cfg.CFG, call *ast.CallExpr) []ast.Node {
	var out []ast.Node
	for _, b := range g.Blocks {
		i := indexOf(b, call)
		if i < 0 {
			continue
		}
		out = append(out, b.Nodes[i+1:]...)
		seen := map[*cfg.Block]bool{}
		var walk func(*cfg.Block)
		walk = func(b *cfg.Block) {
			if b == nil || seen[b] {
				return
			}
			seen[b] = true
			out = append(out, b.Nodes...)
			for _, s := range b.Succs {
				walk(s)
			}
		}
		for _, s := range b.Succs {
			walk(s)
		}
		break
	}
	return out
}

func indexOf(b *cfg.Block, call *ast.CallExpr) int {
	for i, n := range b.Nodes {
		found := false
		ast.Inspect(n, func(n ast.Node) bool {
			if n == ast.Node(call) {
				found = true
			}
			return !found
		})
		if found {
			return i
		}
	}
	return -1
}

// use is where in n the variable v is mentioned, or nil.
func use(info *types.Info, n ast.Node, v *types.Var) *ast.Ident {
	var found *ast.Ident
	ast.Inspect(n, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		if id, ok := n.(*ast.Ident); ok && info.Uses[id] == v {
			found = id
		}
		return found == nil
	})
	return found
}
