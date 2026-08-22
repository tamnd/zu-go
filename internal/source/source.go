// Package source reads this repository's own published Go source.
//
// Two gates need the same three things: where the checkout is, which
// directories in it godoc would publish, and the parsed syntax of each
// one. The reference gate asks whether every published name says what
// it is, and the idiom gate asks whether the published shapes are the
// ones Go writes. Neither compiles anything and neither imports the
// client, so both answer on a checkout with no library staged.
//
// It lives under internal because it is apparatus and not surface. That
// also keeps it out of its own reference gate, which is correct: a
// comment here is a note to us.
package source

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Root is the top of the checkout, found by walking up for the
// workspace file rather than by counting how deep the caller happens to
// sit.
func Root() (string, error) {
	dir, err := filepath.Abs(".")
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir, nil
		}
		up := filepath.Dir(dir)
		if up == dir {
			return "", fs.ErrNotExist
		}
		dir = up
	}
}

// Published is every directory under root holding non-test Go source,
// minus the ones nobody outside can reach.
//
// internal is excluded because the language already promises nobody
// outside can import it and pkg.go.dev does not publish it. testdata is
// excluded because the go tool does not see it at all, and this
// repository's copy holds source that is wrong on purpose. A dot
// directory is not source.
func Published(root string) ([]string, error) {
	seen := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (name == "internal" || name == "testdata" || strings.HasPrefix(name, ".")) {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			seen[filepath.Dir(path)] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	dirs := make([]string, 0, len(seen))
	for dir := range seen {
		dirs = append(dirs, dir)
	}
	// Ordered, so that a failure names the same directory first every
	// run and two runs can be compared to each other.
	slices.Sort(dirs)
	return dirs, nil
}

// Parse reads one directory's non-test source.
//
// Build constraints are deliberately not applied. A file that only
// builds on Windows publishes its names on pkg.go.dev and is read by
// somebody on Windows like any other, and a check that is satisfied on
// the machine that ran it and not on the page is not satisfied.
func Parse(fset *token.FileSet, dir string, comments bool) (map[string]*ast.Package, error) {
	mode := parser.Mode(0)
	if comments {
		mode = parser.ParseComments
	}
	return parser.ParseDir(fset, dir, func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, mode)
}
