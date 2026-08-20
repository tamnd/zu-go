package zulint

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

// ConnShare finds a *zu.Conn that two goroutines can reach.
//
// A connection is exactly the state that cannot be shared: a file
// handle, the caches, and the plans compiled against a catalog. A
// program that queries from four goroutines opens one database and
// connects four times. The client takes a lock and answers
// ZU_MISUSE_CONCURRENT rather than corrupting anything, so this is a
// program that fails under load and passes every test.
//
// Handing a connection to one goroutine and never touching it again is
// not sharing, and is not reported. Three things are:
//
//   - a connection used inside a go statement and outside it as well
//   - a connection used by two go statements
//   - a go statement inside a loop using a connection made outside it,
//     which is one connection and as many goroutines as the loop runs
//
// Three calls do not count. Conn.Interrupt and Conn.RowsRead are meant
// to be made from another goroutine while a statement is running, and
// both take the read side of the lock to do it: one is what makes a
// Ctrl-C and a deadline work, the other is what a progress bar asks.
// Conn.Close is lifecycle rather than work, and a deferred Close beside
// a goroutine is almost always paired with a join this cannot see.
var ConnShare = &analysis.Analyzer{
	Name: "connshare",
	Doc:  "report a *zu.Conn that two goroutines can reach",
	URL:  "https://github.com/tamnd/zu-go/tree/main/zulint",
	Run:  runConnShare,
}

func runConnShare(pass *analysis.Pass) (any, error) {
	info := pass.TypesInfo
	quiet := suppressions(pass)
	for _, f := range pass.Files {
		for _, body := range bodies(f) {
			checkConns(pass, info, quiet, body)
		}
	}
	return nil, nil
}

// notWork is the calls that do not make a mention of a connection a
// use of it: two that another goroutine is allowed to make while a
// statement runs, and one that ends the connection rather than using
// it.
var notWork = map[string]bool{
	"Interrupt": true,
	"RowsRead":  true,
	"Close":     true,
}

type span struct{ from, to token.Pos }

func (s span) holds(p token.Pos) bool { return s.from <= p && p < s.to }

func checkConns(pass *analysis.Pass, info *types.Info, quiet suppress, body *ast.BlockStmt) {
	// The go statements and the loops of this function, not of the
	// literals inside it, which are functions of their own and are
	// visited as such.
	var gos []*ast.GoStmt
	var loops []span
	own(body, func(n ast.Node) {
		switch n := n.(type) {
		case *ast.GoStmt:
			gos = append(gos, n)
		case *ast.ForStmt:
			loops = append(loops, span{n.Pos(), n.End()})
		case *ast.RangeStmt:
			loops = append(loops, span{n.Pos(), n.End()})
		}
	})
	if len(gos) == 0 {
		return
	}

	// A call another goroutine is allowed to make is not a mention.
	safe := map[*ast.Ident]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !notWork[sel.Sel.Name] {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok {
			if v, ok := info.Uses[id].(*types.Var); ok && isType(v.Type(), "Conn") {
				safe[id] = true
			}
		}
		return true
	})

	// Every mention of every connection anywhere under this function,
	// literals included, because a captured variable is the whole
	// point.
	mentions := map[*types.Var][]*ast.Ident{}
	ast.Inspect(body, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok || safe[id] {
			return true
		}
		v, ok := info.Uses[id].(*types.Var)
		if !ok || !isType(v.Type(), "Conn") {
			return true
		}
		mentions[v] = append(mentions[v], id)
		return true
	})

	reported := map[*types.Var]bool{}
	report := func(v *types.Var, at token.Pos, why string) {
		if reported[v] {
			return
		}
		reported[v] = true
		quiet.report(pass, at, "%s is a connection %s, and a connection is the state that cannot be shared: connect again", v.Name(), why)
	}

	for _, g := range gos {
		started := span{g.Pos(), g.End()}
		for v, ids := range mentions {
			if reported[v] {
				continue
			}
			// A connection made inside the goroutine belongs to it.
			if started.holds(v.Pos()) {
				continue
			}

			// The first mention outside the goroutine rather than any
			// of them, so that the line a diagnostic lands on is the
			// same one on every run and is the one a reader would look
			// at first.
			var inside, outside *ast.Ident
			for _, id := range ids {
				switch {
				case started.holds(id.Pos()):
					if inside == nil {
						inside = id
					}
				case outside == nil:
					outside = id
				}
			}
			if inside == nil {
				continue
			}

			switch {
			case outside != nil && startedElsewhere(gos, g, outside.Pos()):
				report(v, outside.Pos(), "two goroutines both reach")
			case outside != nil:
				report(v, outside.Pos(), "used here and inside a goroutine")
			default:
				for _, l := range loops {
					if l.holds(g.Pos()) && !l.holds(v.Pos()) {
						report(v, g.Pos(), "a loop hands to every goroutine it starts")
						break
					}
				}
			}
		}
	}
}

// startedElsewhere reports whether p is inside a go statement other
// than g, which is the difference between a connection shared with the
// function that made it and one shared between two goroutines.
func startedElsewhere(gos []*ast.GoStmt, g *ast.GoStmt, p token.Pos) bool {
	for _, o := range gos {
		if o != g && (span{o.Pos(), o.End()}).holds(p) {
			return true
		}
	}
	return false
}
