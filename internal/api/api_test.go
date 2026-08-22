// The published surface is written down, so that moving it is a diff.
//
// The scorecard's stability item asks for a tool that tells a reviewer
// the public surface moved, before a user finds out. Go has no such
// tool in the toolchain: `go build` is perfectly happy to remove a
// method, and the person who finds out is whoever upgrades. What Go
// does have is a convention, the api/go1.N.txt files the language
// itself is held to, and this is the same thing at the scale of one
// client.
//
// api/surface.txt is every name this repository publishes and the shape
// it publishes it in, one per line, sorted, in the order and the
// spelling the toolchain prints. It is generated rather than written:
//
//	go test ./internal/api -update
//
// and reviewed like any other file. The test below rebuilds it from the
// source and fails when the two disagree, saying which names went, which
// arrived and which changed shape, because those are three different
// pieces of news. A name that arrived is a minor release. A name that
// went or changed is a major one, or a mistake, and the point of the
// gate is that a reviewer is told which they are looking at while it is
// still a diff.
//
// It reads source and links nothing, so it runs beside the reference
// and idiom gates on a checkout with no libzu staged.
//
// Two things it deliberately does not do. It does not type check, so a
// constant written as a cgo name reads as that name rather than as its
// number; what the number is is checked by the tests that use it, and a
// gate that needed a C compiler would answer in minutes instead of in a
// fraction of a second. And it says nothing about behaviour: a
// signature that held still while its meaning changed passes here and
// is caught, if it is caught, by the suite that runs the thing.

package api

import (
	"flag"
	"go/ast"
	"go/printer"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tamnd/zu-go/internal/source"
)

var update = flag.Bool("update", false, "rewrite api/surface.txt from the source")

// where the surface is written down, under the checkout root.
const golden = "api/surface.txt"

// render prints one syntax node the way the toolchain would, on one
// line. A signature written across four lines in the source and the
// same signature written across one are one entry here, so reflowing a
// declaration is not a change to the surface.
func render(fset *token.FileSet, node any) string {
	var buf strings.Builder
	if err := printer.Fprint(&buf, fset, node); err != nil {
		return "?"
	}
	return strings.Join(strings.Fields(buf.String()), " ")
}

// published is the name an embedded field publishes, which is the last
// element of the type it embeds and not something the field was called,
// there being nothing it was called.
func published(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return published(t.X)
	case *ast.SelectorExpr:
		return t.Sel.Name
	case *ast.IndexExpr:
		return published(t.X)
	case *ast.IndexListExpr:
		return published(t.X)
	}
	return ""
}

// surface is every name the given directories publish, sorted.
func surface(t *testing.T, dirs []string) []string {
	t.Helper()
	fset := token.NewFileSet()
	var out []string
	for _, dir := range dirs {
		pkgs, err := source.Parse(fset, dir, false)
		if err != nil {
			t.Fatalf("%s does not parse: %v", dir, err)
		}
		path, err := source.ImportPath(dir)
		if err != nil {
			t.Fatalf("%s has no import path: %v", dir, err)
		}
		for name, pkg := range pkgs {
			// A command publishes nothing: there is no import path
			// anybody can write that reaches into it.
			if name == "main" {
				continue
			}
			for _, file := range slices.Sorted(maps.Keys(pkg.Files)) {
				out = append(out, fileSurface(fset, path, pkg.Files[file])...)
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// fileSurface is every published name in one file.
func fileSurface(fset *token.FileSet, path string, file *ast.File) []string {
	var out []string
	at := func(s string) string { return "pkg " + path + ", " + s }

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if !d.Name.IsExported() {
				continue
			}
			if d.Recv != nil && len(d.Recv.List) > 0 {
				// A method on a type nobody outside can name is not a
				// published method, however exported its own name is.
				if !ast.IsExported(published(d.Recv.List[0].Type)) {
					continue
				}
			}
			// The body is not the surface, and the doc comment is the
			// reference gate's business rather than this one's.
			bare := *d
			bare.Body, bare.Doc = nil, nil
			out = append(out, at(render(fset, &bare)))

		case *ast.GenDecl:
			out = append(out, groupSurface(fset, at, d)...)
		}
	}
	return out
}

// groupSurface reads one const, var or type declaration.
func groupSurface(fset *token.FileSet, at func(string) string, d *ast.GenDecl) []string {
	var out []string
	// What a spec inherits when it declares neither, which is the rule
	// that makes an iota block work: a spec with no type and no value
	// repeats the last one that had them.
	var lastType ast.Expr
	var lastValues []ast.Expr

	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			if !s.Name.IsExported() {
				continue
			}
			for _, line := range typeLine(fset, s) {
				out = append(out, at(line))
			}

		case *ast.ValueSpec:
			typ, values := s.Type, s.Values
			if typ == nil && len(values) == 0 {
				typ, values = lastType, lastValues
			} else {
				lastType, lastValues = typ, values
			}
			for _, n := range s.Names {
				if !n.IsExported() {
					continue
				}
				line := d.Tok.String() + " " + n.Name
				if typ != nil {
					line += " " + render(fset, typ)
				}
				switch {
				case d.Tok == token.CONST && len(values) == 1:
					// A constant is its value, which is part of what it
					// promises and is an ABI tag half the time.
					line += " = " + render(fset, values[0])
				case typ == nil && len(values) == 1:
					line += written(fset, values[0])
				}
				out = append(out, at(line))
			}
		}
	}
	return out
}

// written is what a variable with no declared type still says about
// itself, which is its type where the initializer writes one down.
//
// A composite literal does. A call does not, and nothing here guesses:
// `var ErrDone = errors.New(...)` is recorded by name alone, because
// reading `error` off it means running the type checker. That is one
// name in this repository, it is a sentinel nobody constructs, and what
// it is is fixed by every test that compares against it.
func written(fset *token.FileSet, v ast.Expr) string {
	switch t := v.(type) {
	case *ast.BasicLit:
		return " = " + render(fset, t)
	case *ast.CompositeLit:
		if t.Type != nil {
			return " " + render(fset, t.Type)
		}
	case *ast.UnaryExpr:
		if t.Op == token.AND {
			if lit, ok := t.X.(*ast.CompositeLit); ok && lit.Type != nil {
				return " *" + render(fset, lit.Type)
			}
		}
	}
	return ""
}

// typeLine is one type and, for a struct or an interface, its members,
// which are published names of their own and move on their own.
func typeLine(fset *token.FileSet, s *ast.TypeSpec) []string {
	head := "type " + s.Name.Name + typeParams(fset, s.TypeParams)
	if s.Assign.IsValid() {
		head += " ="
	}

	var fields *ast.FieldList
	kind, member := "", ""
	switch t := s.Type.(type) {
	case *ast.StructType:
		fields, kind, member = t.Fields, "struct", "field"
	case *ast.InterfaceType:
		fields, kind, member = t.Methods, "interface", "method"
	default:
		return []string{head + " " + render(fset, s.Type)}
	}

	out := []string{head + " " + kind}
	if fields == nil {
		return out
	}
	for _, f := range fields.List {
		if len(f.Names) == 0 {
			// Embedded, which publishes the name of the thing embedded
			// along with everything that thing publishes.
			if name := published(f.Type); ast.IsExported(name) {
				out = append(out, member+" "+s.Name.Name+"."+name+" "+render(fset, f.Type))
			}
			continue
		}
		for _, n := range f.Names {
			if n.IsExported() {
				out = append(out, member+" "+s.Name.Name+"."+n.Name+" "+render(fset, f.Type))
			}
		}
	}
	return out
}

// typeParams is the bracketed part of a generic type, spelled out
// here because go/printer prints declarations and expressions and a
// field list is neither.
func typeParams(fset *token.FileSet, fl *ast.FieldList) string {
	if fl == nil || len(fl.List) == 0 {
		return ""
	}
	var parts []string
	for _, f := range fl.List {
		var names []string
		for _, n := range f.Names {
			names = append(names, n.Name)
		}
		parts = append(parts, strings.TrimSpace(strings.Join(names, ", ")+" "+render(fset, f.Type)))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// key is the identity of a line: everything that says which name it is,
// and nothing that says what shape it has. Two lines with the same key
// are the same published name, so a difference between them is a change
// to something that was already there rather than a name coming or
// going, and those are different news for whoever is reading the diff.
func key(line string) string {
	pkg, rest, ok := strings.Cut(line, ", ")
	if !ok {
		return line
	}
	kind, rest, ok := strings.Cut(rest, " ")
	if !ok {
		return line
	}
	if kind == "func" && strings.HasPrefix(rest, "(") {
		// A method. Which type it is on is part of which name it is;
		// what the receiver variable is called is not, so renaming it
		// reads as a change to this method and not as one method
		// leaving and another arriving.
		end := strings.Index(rest, ")")
		if end < 0 {
			return line
		}
		recv := rest[1:end]
		if i := strings.LastIndex(recv, " "); i >= 0 {
			recv = recv[i+1:]
		}
		return pkg + ", func (" + recv + ") " + head(strings.TrimSpace(rest[end+1:]))
	}
	return pkg + ", " + kind + " " + head(rest)
}

// head is the identifier at the front, which ends at the type
// parameters, at the parameters, or at the space before whatever the
// name was declared to be.
func head(s string) string {
	if i := strings.IndexAny(s, "([ "); i >= 0 {
		return s[:i]
	}
	return s
}

func root(t *testing.T) string {
	t.Helper()
	dir, err := source.Root()
	if err != nil {
		t.Fatalf("this is not a checkout of this repository: %v", err)
	}
	return dir
}

func TestThePublishedSurfaceIsTheOneWrittenDown(t *testing.T) {
	top := root(t)
	dirs, err := source.Published(top)
	if err != nil {
		t.Fatalf("the repository does not walk: %v", err)
	}
	now := surface(t, dirs)
	if len(now) == 0 {
		t.Fatal("nothing published anywhere, which means the walk is wrong and not that there is nothing")
	}
	file := filepath.Join(top, golden)

	if *update {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatalf("making %s: %v", filepath.Dir(golden), err)
		}
		if err := os.WriteFile(file, []byte(strings.Join(now, "\n")+"\n"), 0o644); err != nil {
			t.Fatalf("writing %s: %v", golden, err)
		}
		t.Logf("%s rewritten, %d published names", golden, len(now))
		return
	}

	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("%s is not there: %v\nrun: go test ./internal/api -update", golden, err)
	}
	was := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")

	gone, arrived, changed := compare(was, now)
	for _, line := range gone {
		t.Errorf("gone, and nothing a caller wrote against it still compiles: %s", line)
	}
	for _, c := range changed {
		// Whether this one is a break is the thing to look at rather
		// than something this can answer: a parameter added is, a
		// parameter renamed is not, and both read the same here.
		t.Errorf("the same name, a different shape:\n  was %s\n  now %s", c[0], c[1])
	}
	for _, line := range arrived {
		t.Errorf("new, which breaks nobody and is not written down yet: %s", line)
	}
	if len(gone)+len(arrived)+len(changed) > 0 {
		t.Logf("run: go test ./internal/api -update")
		return
	}
	t.Logf("%d published names, all of them written down", len(now))
}

// compare says what moved between two surfaces: names that went, names
// that arrived, and names that stayed and changed shape.
func compare(was, now []string) (gone, arrived []string, changed [][2]string) {
	before := map[string]string{}
	for _, line := range was {
		if line != "" {
			before[key(line)] = line
		}
	}
	after := map[string]string{}
	for _, line := range now {
		after[key(line)] = line
	}
	for k, old := range before {
		switch fresh, still := after[k]; {
		case !still:
			gone = append(gone, old)
		case fresh != old:
			changed = append(changed, [2]string{old, fresh})
		}
	}
	for k, fresh := range after {
		if _, had := before[k]; !had {
			arrived = append(arrived, fresh)
		}
	}
	slices.Sort(gone)
	slices.Sort(arrived)
	slices.SortFunc(changed, func(a, b [2]string) int { return strings.Compare(a[0], b[0]) })
	return gone, arrived, changed
}

// A gate that read a surface and quietly missed half of it would pass
// exactly as green as one that read all of it, so this puts one of
// every kind in front of it and checks the line that comes back is the
// line the file should hold.
func TestEveryKindOfPublishedNameIsReadAndSpelledOut(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.test/sample\n\ngo 1.26.6\n")
	write(t, dir, "sample.go", `package sample

import "context"

type Thing struct {
	Named   int
	Two, Up string
	hidden  int
	Reader
}

type Reader interface {
	Read(ctx context.Context) ([]byte, error)
	hidden()
}

type Count int32

type Pair[K comparable, V any] struct{ Key K }

type Alias = Thing

const Known Count = 1

const (
	First Count = iota
	Second
)

var Sentinel error

func Loud[T any](ctx context.Context, in T) (T, error) { var z T; return z, nil }

func (t Thing) Say() string { return "" }

func (t *Thing) Set(n int) { t.Named = n }

func (h hiddenType) Say() string { return "" }

type hiddenType struct{ Named int }

func quiet() {}
`)

	want := []string{
		"pkg example.test/sample, const First Count = iota",
		"pkg example.test/sample, const Known Count = 1",
		"pkg example.test/sample, const Second Count = iota",
		"pkg example.test/sample, field Pair.Key K",
		"pkg example.test/sample, field Thing.Named int",
		"pkg example.test/sample, field Thing.Reader Reader",
		"pkg example.test/sample, field Thing.Two string",
		"pkg example.test/sample, field Thing.Up string",
		"pkg example.test/sample, func (t *Thing) Set(n int)",
		"pkg example.test/sample, func (t Thing) Say() string",
		"pkg example.test/sample, func Loud[T any](ctx context.Context, in T) (T, error)",
		"pkg example.test/sample, method Reader.Read func(ctx context.Context) ([]byte, error)",
		"pkg example.test/sample, type Alias = Thing",
		"pkg example.test/sample, type Count int32",
		"pkg example.test/sample, type Pair[K comparable, V any] struct",
		"pkg example.test/sample, type Reader interface",
		"pkg example.test/sample, type Thing struct",
		"pkg example.test/sample, var Sentinel error",
	}

	got := surface(t, []string{dir})
	if len(got) != len(want) {
		t.Errorf("the sample publishes %d names and %d were read", len(want), len(got))
	}
	for i := range max(len(got), len(want)) {
		switch {
		case i >= len(got):
			t.Errorf("missing: %s", want[i])
		case i >= len(want):
			t.Errorf("read a name the sample does not publish: %s", got[i])
		case got[i] != want[i]:
			t.Errorf("line %d\n  want %s\n  got  %s", i, want[i], got[i])
		}
	}
}

// And the part that turns two surfaces into news. Every one of these
// would read as a name leaving and another arriving if the identity of
// a line were the whole line, which is the mistake that makes such a
// gate noise a reviewer learns to skip.
func TestWhatMovedIsToldApartFromWhatArrived(t *testing.T) {
	was := []string{
		"pkg zu, func (c *Conn) Query(ctx context.Context, q string) (*Rows, error)",
		"pkg zu, func (r *Rows) Close() error",
		"pkg zu, func Open(path string) (*DB, error)",
		"pkg zu, type Arg struct",
	}
	now := []string{
		// A parameter added, which is a break.
		"pkg zu, func (c *Conn) Query(ctx context.Context, q string, args ...Arg) (*Rows, error)",
		// Only the receiver variable renamed, which is not.
		"pkg zu, func (rows *Rows) Close() error",
		// Open is gone.
		// Create has arrived.
		"pkg zu, func Create(path string) (*DB, error)",
		"pkg zu, type Arg struct",
	}

	gone, arrived, changed := compare(was, now)
	if len(gone) != 1 || !strings.Contains(gone[0], "func Open") {
		t.Errorf("what went is %v", gone)
	}
	if len(arrived) != 1 || !strings.Contains(arrived[0], "func Create") {
		t.Errorf("what arrived is %v", arrived)
	}
	// Both of these are changes and neither is a name coming or going,
	// which is the whole point of keying a line by its name. Which of
	// the two is a break is for a reviewer and not for this.
	if len(changed) != 2 {
		t.Fatalf("what changed is %v", changed)
	}
	if !strings.Contains(changed[0][1], "args ...Arg") {
		t.Errorf("the added parameter is not the first change: %v", changed[0])
	}
	if !strings.Contains(changed[1][1], "(rows *Rows)") {
		t.Errorf("the renamed receiver is not the second change: %v", changed[1])
	}
	// And the type that did not move is in none of the three.
	for _, line := range append(append([]string{}, gone...), arrived...) {
		if strings.Contains(line, "type Arg") {
			t.Errorf("a line that did not change was reported: %s", line)
		}
	}
}

// The identity of a line, on the cases that decide whether the three
// answers above are told apart at all.
func TestALineIsIdentifiedByItsNameAndNotByItsShape(t *testing.T) {
	for _, c := range []struct{ line, want string }{
		{"pkg zu, func Open(path string) (*DB, error)", "pkg zu, func Open"},
		{"pkg zu, func Collect[T any](rows *Rows) ([]T, error)", "pkg zu, func Collect"},
		{"pkg zu, func (r *Rows) Close() error", "pkg zu, func (*Rows) Close"},
		{"pkg zu, func (t Thing) Say() string", "pkg zu, func (Thing) Say"},
		{"pkg zu, type Pair[K comparable, V any] struct", "pkg zu, type Pair"},
		{"pkg zu, type Count int32", "pkg zu, type Count"},
		{"pkg zu, const TypeBytes Type = C.ZU_TYPE_BYTES", "pkg zu, const TypeBytes"},
		{"pkg zu, var ErrDone error", "pkg zu, var ErrDone"},
		{"pkg zu, field Thing.Named int", "pkg zu, field Thing.Named"},
		{"pkg zu, method Reader.Read func(ctx context.Context) error", "pkg zu, method Reader.Read"},
	} {
		if got := key(c.line); got != c.want {
			t.Errorf("%s\n  is identified as %s\n  and should be    %s", c.line, got, c.want)
		}
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}
