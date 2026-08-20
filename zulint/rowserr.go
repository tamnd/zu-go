package zulint

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

// RowsErr finds a loop over a result that never asks why it ended.
//
// Both loops this client offers end quietly. `for rows.Next()` stops
// on the last row and stops on a failure, and `for row := range
// rows.All()` does the same, because a range-over-func has nowhere to
// put an error. Rows.Err is where the answer went. A program that does
// not read it treats a cancelled query, a conflict and a corrupt page
// as an empty result, which is the failure mode where nobody finds out
// for a week.
//
// It is reported only for a result this function made. A result that
// arrived as a parameter, or as the receiver of a method, belongs to
// whoever made it, and so does the question. So does one handed back
// to the caller.
var RowsErr = &analysis.Analyzer{
	Name: "rowserr",
	Doc:  "report a loop over a result whose Err is never read",
	URL:  "https://github.com/tamnd/zu-go/tree/main/zulint",
	Run:  runRowsErr,
}

func runRowsErr(pass *analysis.Pass) (any, error) {
	info := pass.TypesInfo
	quiet := suppressions(pass)
	for _, f := range pass.Files {
		for _, body := range bodies(f) {
			// Err and the loops are looked for across the whole
			// function including its literals, because a deferred
			// closure that reads Err is a program that asked.
			asked := map[*types.Var]bool{}
			escaped := map[*types.Var]bool{}
			mine := func(v *types.Var) bool {
				return body.Pos() <= v.Pos() && v.Pos() < body.End()
			}
			ast.Inspect(body, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.CallExpr:
					if recv, name, ok := method(info, n, "Rows"); ok && name == "Err" {
						asked[recv] = true
					}
				case *ast.ReturnStmt:
					for _, r := range n.Results {
						if id, ok := r.(*ast.Ident); ok {
							if v, ok := info.Uses[id].(*types.Var); ok && isType(v.Type(), "Rows") {
								escaped[v] = true
							}
						}
					}
				}
				return true
			})

			// The loops of this function, not of the literals inside it,
			// which are functions of their own and are visited as such.
			// A parameter of a literal is declared inside the body that
			// holds the literal, so reading the two together is how a
			// result that arrived somewhere gets blamed on the function
			// it arrived in.
			own(body, func(n ast.Node) {
				var call *ast.CallExpr
				var loop string
				switch n := n.(type) {
				case *ast.ForStmt:
					c, ok := n.Cond.(*ast.CallExpr)
					if !ok {
						return
					}
					call, loop = c, "Next"
				case *ast.RangeStmt:
					c, ok := n.X.(*ast.CallExpr)
					if !ok {
						return
					}
					call, loop = c, "All"
				default:
					return
				}
				recv, name, ok := method(info, call, "Rows")
				if !ok || name != loop {
					return
				}
				if asked[recv] || escaped[recv] || !mine(recv) {
					return
				}
				quiet.report(pass, n.Pos(), "this loop ends on the last row and on a failure alike, and %s.Err is never read",
					recv.Name())
			})
		}
	}
	return nil, nil
}
