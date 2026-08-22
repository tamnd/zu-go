// The published surface is the shape Go writes, and stays that shape.
//
// The scorecard's idiom item asks four questions about a binding: how
// it iterates, how it releases a resource, how it awaits, and what it
// raises. A binding that answers them in the shape of the C ABI with Go
// syntax on top is one nobody wants to write against, and the way that
// happens is never a decision. It happens one method at a time, each
// one reasonable on its own, over a year.
//
// This is the half of the answer that can be read off the source. The
// other half is behaviour and is tested by running the thing:
// cancellation in await_test.go, closing and misuse in misuse_test.go,
// the range loop in rows_test.go. Neither half is worth much alone. A
// method can take a context in the right position and ignore it, and a
// method can honour a context it takes second, and only the two
// together say the convention holds.
//
// Every rule below held when it was written, so none of this is a
// cleanup. It is the thing that notices in review rather than in a
// release, which is the only time noticing is cheap.
//
// It links nothing and imports only the standard library, so it answers
// on a checkout with no libzu staged, in well under a second, on every
// pull request.

package idiom

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/zu-go/internal/source"
)

// a break is one place the surface does not have the shape, and where.
type broken struct {
	where string
	why   string
}

// fn is one exported function or method, flattened into the shape these
// rules ask about: parameters and results as type names, one entry per
// name, since `func(a, b string)` is two parameters and go/ast holds it
// as one field.
type fn struct {
	// name is what a caller writes after the package: Named, or
	// Conn.Query for a method.
	name string
	// bare is the last element of it, which is what the naming rules
	// look at.
	bare string
	// where is the file and line, relative to the checkout.
	where   string
	params  []string
	results []string
	body    *ast.BlockStmt
}

// name of a type as it is written, which is what these rules compare
// against. This is deliberately syntactic: there is no type checker
// here, so `context.Context` is matched by how it is spelled. A file
// that imported context under another name would slip through, which is
// worth knowing and is not worth a type checker, because nobody does
// that and go vet would have opinions if they did.
func typeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return typeName(t.X) + "." + t.Sel.Name
	case *ast.StarExpr:
		return "*" + typeName(t.X)
	case *ast.ArrayType:
		return "[]" + typeName(t.Elt)
	case *ast.Ellipsis:
		return "..." + typeName(t.Elt)
	case *ast.IndexExpr:
		return typeName(t.X) + "[" + typeName(t.Index) + "]"
	case *ast.IndexListExpr:
		var parts []string
		for _, i := range t.Indices {
			parts = append(parts, typeName(i))
		}
		return typeName(t.X) + "[" + strings.Join(parts, ", ") + "]"
	case *ast.ChanType:
		return "chan " + typeName(t.Value)
	case *ast.MapType:
		return "map[" + typeName(t.Key) + "]" + typeName(t.Value)
	case *ast.FuncType:
		return "func"
	case *ast.InterfaceType:
		return "interface"
	case *ast.StructType:
		return "struct"
	}
	return "?"
}

func flatten(fl *ast.FieldList) []string {
	var out []string
	if fl == nil {
		return out
	}
	for _, f := range fl.List {
		n := max(len(f.Names), 1)
		for range n {
			out = append(out, typeName(f.Type))
		}
	}
	return out
}

// exported reads every exported function and method the repository
// publishes. A method on an unexported type is not published, however
// exported its own name, so it is left out.
func exported(t *testing.T) []fn {
	t.Helper()
	root, err := source.Root()
	if err != nil {
		t.Fatalf("this is not a checkout of this repository: %v", err)
	}
	dirs, err := source.Published(root)
	if err != nil {
		t.Fatalf("the repository does not walk: %v", err)
	}
	fset := token.NewFileSet()
	var out []fn
	for _, dir := range dirs {
		pkgs, err := source.Parse(fset, dir, false)
		if err != nil {
			t.Fatalf("%s does not parse: %v", dir, err)
		}
		for _, pkg := range pkgs {
			for _, file := range pkg.Files {
				for _, decl := range file.Decls {
					d, ok := decl.(*ast.FuncDecl)
					if !ok || !d.Name.IsExported() {
						continue
					}
					name := d.Name.Name
					if d.Recv != nil && len(d.Recv.List) > 0 {
						recv := strings.TrimPrefix(typeName(d.Recv.List[0].Type), "*")
						if !ast.IsExported(recv) {
							continue
						}
						name = recv + "." + name
					}
					p := fset.Position(d.Pos())
					if rel, err := filepath.Rel(root, p.Filename); err == nil {
						p.Filename = rel
					}
					out = append(out, fn{
						name:    name,
						bare:    d.Name.Name,
						where:   p.String(),
						params:  flatten(d.Type.Params),
						results: flatten(d.Type.Results),
						body:    d.Body,
					})
				}
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("no exported function found anywhere, which means the walk is wrong and not that there are none")
	}
	return out
}

// report fails the test once per break, with the place first, because
// the place is what a reader does something with.
func report(t *testing.T, rule string, breaks []broken) {
	t.Helper()
	for _, b := range breaks {
		t.Errorf("%s: %s", b.where, b.why)
	}
	if len(breaks) == 0 {
		t.Logf("%s: nothing", rule)
	}
}

// How it awaits. A context goes first or it goes nowhere: a caller
// cannot pass one uniformly if its position moves, and a package where
// it moves is one where every call site is read twice.
func contextFirst(fns []fn) []broken {
	var breaks []broken
	for _, f := range fns {
		for i, p := range f.params {
			if p == "context.Context" && i != 0 {
				breaks = append(breaks, broken{f.at(), fmt.Sprintf(
					"takes a context as parameter %d of %v, and a context goes first", i+1, f.params)})
			}
		}
	}
	return breaks
}

func TestAContextIsAlwaysTheFirstParameter(t *testing.T) {
	report(t, "context first", contextFirst(exported(t)))
}

// What it raises. An error goes last, for the same reason: `v, err :=`
// is how Go is read, and a package where the error is sometimes first
// is one where every assignment has to be checked against the
// signature.
func errorLast(fns []fn) []broken {
	var breaks []broken
	for _, f := range fns {
		for i, r := range f.results {
			if r == "error" && i != len(f.results)-1 {
				breaks = append(breaks, broken{f.at(), fmt.Sprintf(
					"returns an error as result %d of %v, and an error goes last", i+1, f.results)})
			}
		}
	}
	return breaks
}

func TestAnErrorIsAlwaysTheLastResult(t *testing.T) {
	report(t, "error last", errorLast(exported(t)))
}

// What it raises, the other half. This binding holds a handle to a C
// library, and a panic out of cgo is a stack a caller cannot recover
// anything useful from and cannot have expected. Everything that can go
// wrong comes back as an error, including the misuse: closing twice,
// using a closed connection, two goroutines on one connection.
//
// A test may panic and does. This reads published source only.
func noPanic(fns []fn) []broken {
	var breaks []broken
	for _, f := range fns {
		if f.body == nil {
			continue
		}
		ast.Inspect(f.body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := call.Fun.(*ast.Ident)
			if !ok || id.Name != "panic" {
				return true
			}
			breaks = append(breaks, broken{f.at(),
				"panics, and a caller of this package gets errors rather than a stack out of cgo"})
			return true
		})
	}
	return breaks
}

func TestNothingPublishedPanics(t *testing.T) {
	report(t, "no panic", noPanic(exported(t)))
}

// How it releases a resource. Close returns an error and nothing else,
// which is what io.Closer says and what every deferred close in every
// Go program is written against.
func closeReturnsError(fns []fn) []broken {
	var breaks []broken
	for _, f := range fns {
		if f.bare != "Close" {
			continue
		}
		if len(f.results) != 1 || f.results[0] != "error" {
			breaks = append(breaks, broken{f.at(), fmt.Sprintf(
				"returns %v, and a Close returns one error so that io.Closer holds and a deferred close reads the same everywhere", f.results)})
		}
	}
	return breaks
}

func TestCloseReturnsAnErrorAndNothingElse(t *testing.T) {
	report(t, "close returns error", closeReturnsError(exported(t)))
}

// How it iterates. Since Go 1.23 the answer is iter.Seq, and the
// channel that used to be the answer leaks a goroutine on every caller
// who stops early. Nothing here hands one out.
func noChannel(fns []fn) []broken {
	var breaks []broken
	for _, f := range fns {
		for _, r := range f.results {
			if strings.HasPrefix(r, "chan ") {
				breaks = append(breaks, broken{f.at(),
					"hands back a channel, and iteration here is iter.Seq, which does not leak a goroutine when the caller stops early"})
			}
		}
	}
	return breaks
}

func TestIterationIsASequenceAndNotAChannel(t *testing.T) {
	report(t, "no channel", noChannel(exported(t)))
}

// The names. Go reads the package with the name, so zu.ZuError is said
// twice and zu.GetVersion is said the way Java says it. Neither is
// wrong enough to break anything, which is exactly why they accumulate.
func names(fns []fn) []broken {
	var breaks []broken
	for _, f := range fns {
		bare := f.bare
		if strings.HasPrefix(bare, "Zu") {
			breaks = append(breaks, broken{f.at(),
				"repeats the package name, and a caller writes zu. in front of it already"})
		}
		if len(bare) > 3 && strings.HasPrefix(bare, "Get") && ast.IsExported(bare[3:]) {
			breaks = append(breaks, broken{f.at(),
				"is spelled as a getter, and Go names one for the thing rather than for the getting"})
		}
	}
	return breaks
}

func TestNoNameStuttersOrAsksForAGetter(t *testing.T) {
	report(t, "names", names(exported(t)))
}

// The gate has to be able to see a break, or every test above is a walk
// that matched nothing and passed just as green. Each rule is put in
// front of a signature that breaks it and one that does not, and both
// answers are checked, because a rule that fires on everything is as
// useless as one that fires on nothing.
func TestEachRuleSeesItsOwnBreakAndNothingElse(t *testing.T) {
	for _, c := range []struct {
		what   string
		rule   func([]fn) []broken
		breaks string
		holds  string
	}{
		{
			"context first",
			contextFirst,
			"func Late(q string, ctx context.Context) error",
			"func Early(ctx context.Context, q string) error",
		},
		{
			"error last",
			errorLast,
			"func Backwards() (error, int)",
			"func Forwards() (int, error)",
		},
		{
			"no panic",
			noPanic,
			"func Rude() { panic(\"no\") }",
			"func Polite() error { return nil }",
		},
		{
			"close returns error",
			closeReturnsError,
			"func Close() (int, error)",
			"func Close() error",
		},
		{
			"no channel",
			noChannel,
			"func Rows() chan int",
			"func Rows() iter.Seq[int]",
		},
		{
			"names",
			names,
			"func ZuVersion() string",
			"func Version() string",
		},
		{
			"names, the getter half",
			names,
			"func GetVersion() string",
			"func Version() string",
		},
	} {
		t.Run(c.what, func(t *testing.T) {
			if got := c.rule([]fn{parseOne(t, c.breaks)}); len(got) == 0 {
				t.Errorf("%s breaks this rule and the rule said nothing", c.breaks)
			}
			if got := c.rule([]fn{parseOne(t, c.holds)}); len(got) != 0 {
				t.Errorf("%s holds to this rule and the rule said %v", c.holds, got)
			}
		})
	}
}

// The rules read the flattened signature, so the flattening is what has
// to be right underneath all of them. These are the cases that would
// quietly make every rule above pass: a field holding two names, a
// signature with no results, and a variadic, which is one parameter and
// not one per argument a caller passes.
func TestASignatureFlattensToWhatItSays(t *testing.T) {
	for _, c := range []struct {
		decl    string
		params  int
		results int
	}{
		{"func Two(a, b string) error", 2, 1},
		{"func None()", 0, 0},
		{"func Var(q string, args ...Arg) error", 2, 1},
		{"func Pair() (int, error)", 0, 2},
	} {
		f := parseOne(t, c.decl)
		if len(f.params) != c.params || len(f.results) != c.results {
			t.Errorf("%s flattened to %v and %v, wanted %d parameters and %d results",
				c.decl, f.params, f.results, c.params, c.results)
		}
	}
}

// parseOne reads one function declaration out of a snippet, so the test
// above can put a shape in front of the flattening without a file.
func parseOne(t *testing.T, decl string) fn {
	t.Helper()
	fset := token.NewFileSet()
	// A body only if the snippet did not bring one, so that the panic
	// rule can be given a function that panics and the others can be
	// given one that does not.
	src := "package sample\n" +
		"import (\n\t\"context\"\n\t\"iter\"\n)\n" +
		"var (\n\t_ = context.Background\n\t_ iter.Seq[int]\n)\n" +
		"type Arg struct{}\n" + decl
	if !strings.HasSuffix(strings.TrimSpace(src), "}") || !strings.Contains(decl, "{") {
		src += " { return }"
	}
	file, err := parser.ParseFile(fset, "sample.go", src, 0)
	if err != nil {
		t.Fatalf("the sample does not parse: %v", err)
	}
	for _, d := range file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		return fn{
			name:    fd.Name.Name,
			bare:    fd.Name.Name,
			where:   "sample.go",
			params:  flatten(fd.Type.Params),
			results: flatten(fd.Type.Results),
			body:    fd.Body,
		}
	}
	t.Fatalf("no function in %q", decl)
	return fn{}
}

// at is the place, with the name after it, which is the order a reader
// wants: the file and line is what they click.
func (f fn) at() string { return f.where + ": " + f.name }
