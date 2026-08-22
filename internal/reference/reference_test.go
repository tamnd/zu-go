// Every name this repository publishes carries a doc comment.
//
// The reference for a Go library is not written, it is generated: godoc
// reads the source and pkg.go.dev publishes what it read, on the tag,
// without anybody being asked. That is the whole of the apparatus, and
// it is why nobody has to maintain it. It is also why an undocumented
// name is caught by nothing. It renders as a bare identifier and a
// type, on the page a reader is on precisely because they did not know
// what it meant, and no build anywhere goes red over it.
//
// Eighteen fields were bare when this was written, every one of them on
// a type whose own comment explained the field, which is the shape the
// miss takes almost every time. The type reads as documented to the
// person writing it and the field reads as empty to the person reading
// it, and the two people are never the same person.
//
// It lives here, in a package of its own, for two reasons. It is a
// test rather than a workflow step so that it fails the same way on a
// laptop as in CI, which is the rule the ABI check already follows. And
// it is out of the client package so that it links nothing: the gate
// then answers on a checkout with no libzu staged and no Rust
// installed, in a fraction of a second, rather than only in the jobs
// that got as far as linking. The walk it shares with the idiom gate is
// in internal/source.
//
// Exported means exported to somebody. internal is excluded because the
// language already promises nobody outside can import it and
// pkg.go.dev does not publish it, so a comment there is a note to us
// and not a reference. Everything else in the repository is in, and the
// directories are walked rather than listed, so a package added
// tomorrow is held to this without anybody remembering to add it.

package reference

import (
	"go/ast"
	"go/doc"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/zu-go/internal/source"
)

// root is the top of the checkout.
func root(t *testing.T) string {
	t.Helper()
	dir, err := source.Root()
	if err != nil {
		t.Fatalf("this is not a checkout of this repository: %v", err)
	}
	return dir
}

// published is every directory godoc would publish.
func published(t *testing.T) []string {
	t.Helper()
	dirs, err := source.Published(root(t))
	if err != nil {
		t.Fatalf("the repository does not walk: %v", err)
	}
	if len(dirs) == 0 {
		t.Fatal("no Go source found, which means the walk is wrong and not that there is none")
	}
	return dirs
}

// bare is one exported name with nothing said about it, and where to
// find it.
type bare struct {
	where string
	kind  string
	name  string
}

// undocumented reads one directory the way godoc would and returns
// every exported name in it that carries no comment.
//
// Build constraints are deliberately not applied. A file that only
// builds on Windows publishes its names on pkg.go.dev like any other,
// and a reference that is complete on the machine that ran the test and
// short on the page is not complete.
func undocumented(t *testing.T, dir string) []bare {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := source.Parse(fset, dir, true)
	if err != nil {
		t.Fatalf("%s does not parse: %v", dir, err)
	}
	var out []bare
	for name, pkg := range pkgs {
		d := doc.New(pkg, name, doc.PreserveAST)
		if strings.TrimSpace(d.Doc) == "" {
			out = append(out, bare{dir, "package", name})
		}
		at := func(pos token.Pos) string { return fset.Position(pos).String() }
		report := func(kind, name, text string, pos token.Pos) {
			if strings.TrimSpace(text) == "" {
				out = append(out, bare{at(pos), kind, name})
			}
		}
		for _, c := range d.Consts {
			report("const", strings.Join(c.Names, ", "), c.Doc, c.Decl.Pos())
		}
		for _, v := range d.Vars {
			report("var", strings.Join(v.Names, ", "), v.Doc, v.Decl.Pos())
		}
		for _, f := range d.Funcs {
			report("func", f.Name, f.Doc, f.Decl.Pos())
		}
		for _, typ := range d.Types {
			report("type", typ.Name, typ.Doc, typ.Decl.Pos())
			for _, c := range typ.Consts {
				report("const", strings.Join(c.Names, ", "), c.Doc, c.Decl.Pos())
			}
			for _, v := range typ.Vars {
				report("var", strings.Join(v.Names, ", "), v.Doc, v.Decl.Pos())
			}
			for _, f := range typ.Funcs {
				report("func", f.Name, f.Doc, f.Decl.Pos())
			}
			for _, m := range typ.Methods {
				report("method", typ.Name+"."+m.Name, m.Doc, m.Decl.Pos())
			}
			out = append(out, members(typ, at)...)
		}
	}
	return out
}

// members is the part godoc keeps no list of. A struct field and an
// interface method are published names with no entry in doc.Type, so
// they are read off the declaration itself.
func members(typ *doc.Type, at func(token.Pos) string) []bare {
	var out []bare
	for _, spec := range typ.Decl.Specs {
		ts, ok := spec.(*ast.TypeSpec)
		if !ok {
			continue
		}
		var fields *ast.FieldList
		kind := "field"
		switch t := ts.Type.(type) {
		case *ast.StructType:
			fields = t.Fields
		case *ast.InterfaceType:
			fields, kind = t.Methods, "interface method"
		default:
			continue
		}
		if fields == nil {
			continue
		}
		for _, f := range fields.List {
			// An embedded field has no name of its own and carries
			// the doc of the thing it embeds, which is where a reader
			// is sent and is the right place for it to be.
			if len(f.Names) == 0 {
				continue
			}
			text := ""
			if f.Doc != nil {
				text = f.Doc.Text()
			} else if f.Comment != nil {
				// A comment on the same line is what godoc shows for
				// a short field, so it counts as having said it.
				text = f.Comment.Text()
			}
			if strings.TrimSpace(text) != "" {
				continue
			}
			for _, n := range f.Names {
				if n.IsExported() {
					out = append(out, bare{at(n.Pos()), kind, ts.Name.Name + "." + n.Name})
				}
			}
		}
	}
	return out
}

func TestEveryPublishedNameSaysWhatItIs(t *testing.T) {
	dirs := published(t)
	top := root(t)
	var all []bare
	for _, dir := range dirs {
		all = append(all, undocumented(t, dir)...)
	}
	for _, b := range all {
		where := b.where
		if rel, err := filepath.Rel(top, where); err == nil {
			where = rel
		}
		t.Errorf("%s: %s %s has no doc comment, so the reference publishes it bare", where, b.kind, b.name)
	}
	t.Logf("%d directories read, %d names with nothing said about them", len(dirs), len(all))
}

// The test above is worth exactly as much as its ability to see a miss,
// and a walk that quietly matched nothing would pass just as green.
// This puts a bare name in front of it and checks it is found, and puts
// documented and unexported ones there too and checks they are not.
func TestTheGateSeesABareNameAndOnlyABareName(t *testing.T) {
	dir := t.TempDir()
	sample := `// Package sample is here to be read.
package sample

// A Thing is a thing.
type Thing struct {
	Documented int // what it is
	Bare       int
	unexported int
}

// A Reader reads.
type Reader interface {
	// Read reads.
	Read() int
	Write() int
}

// fine says nothing and is allowed to, because it is not exported.
func fine() {}

func Loud() {}

// Known is documented.
const Known = 1

const Unknown = 2
`
	if err := os.WriteFile(filepath.Join(dir, "sample.go"), []byte(sample), 0o644); err != nil {
		t.Fatalf("the sample does not write: %v", err)
	}
	found := map[string]bool{}
	for _, b := range undocumented(t, dir) {
		found[b.kind+" "+b.name] = true
	}
	for _, want := range []string{
		"field Thing.Bare",
		"interface method Reader.Write",
		"func Loud",
		"const Unknown",
	} {
		if !found[want] {
			t.Errorf("%s carries no comment and the gate did not say so, it found %v", want, found)
		}
	}
	for _, wrong := range []string{
		"field Thing.Documented",
		"field Thing.unexported",
		"interface method Reader.Read",
		"type Thing",
		"const Known",
		"package sample",
	} {
		if found[wrong] {
			t.Errorf("%s is documented or unexported and the gate called it bare", wrong)
		}
	}
}
