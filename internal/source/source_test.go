// The walk three gates stand on.
//
// Nothing here is published, so none of it is held to the reference
// gate, and all of it is held to this: the reference, idiom and api
// gates each read whatever this hands them, and a walk that quietly
// skipped a directory or named a package wrongly would make all three
// pass on a surface they never looked at.

package source

import (
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestTheRootIsTheDirectoryTheWorkspaceIsIn(t *testing.T) {
	root, err := Root()
	if err != nil {
		t.Fatalf("this is not a checkout of this repository: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.work")); err != nil {
		t.Errorf("the root came back as %s, which holds no go.work: %v", root, err)
	}
	// Found by walking up rather than by counting how deep the caller
	// sits, so it is the same answer from three directories down.
	if _, err := os.Stat(filepath.Join(root, "include", "zu.h")); err != nil {
		t.Errorf("the root came back as %s, which is not this repository: %v", root, err)
	}
}

func TestTheWalkFindsWhatIsPublishedAndLeavesOutWhatIsNot(t *testing.T) {
	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	dirs, err := Published(root)
	if err != nil {
		t.Fatalf("the repository does not walk: %v", err)
	}
	rel := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		r, err := filepath.Rel(root, dir)
		if err != nil {
			t.Fatal(err)
		}
		rel = append(rel, filepath.ToSlash(r))
	}

	for _, want := range []string{".", "zusql", "zuarrow", "zulint"} {
		if !slices.Contains(rel, want) {
			t.Errorf("%s publishes Go source and the walk missed it, it found %v", want, rel)
		}
	}
	for _, r := range rel {
		switch {
		case r == "internal" || filepath.ToSlash(r) == "internal/source":
			t.Errorf("%s is internal and nobody outside can import it, so it is not published", r)
		case r == "testdata" || len(r) > 9 && r[:9] == "testdata/":
			t.Errorf("%s is testdata, which the go tool does not see at all", r)
		}
	}

	// Ordered, so a failure names the same directory first every run.
	if !slices.IsSorted(dirs) {
		t.Errorf("the walk came back unordered: %v", dirs)
	}
}

func TestAPackageIsNamedByTheModuleItIsIn(t *testing.T) {
	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ dir, want string }{
		{".", "github.com/tamnd/zu-go"},
		{"zusql", "github.com/tamnd/zu-go/zusql"},
		// Its own module, and a path worked out from the tree rather
		// than read off a go.mod would name it the same either way,
		// which is the case that says nothing about whether the read
		// happened.
		{"zuarrow", "github.com/tamnd/zu-go/zuarrow"},
		// Its own module and its own workspace, so this is the one that
		// would come out wrong if the nearest go.mod were not the one
		// consulted.
		{"zulint/cmd/zulint", "github.com/tamnd/zu-go/zulint/cmd/zulint"},
	} {
		got, err := ImportPath(filepath.Join(root, filepath.FromSlash(c.dir)))
		if err != nil {
			t.Errorf("%s has no import path: %v", c.dir, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s imports as %q and should be %q", c.dir, got, c.want)
		}
	}

	if _, err := ImportPath(t.TempDir()); err == nil {
		t.Error("a directory in no module answered with an import path anyway")
	}
}

// The one line of go.mod this package reads by hand, on the cases that
// decide whether reading it by hand was a mistake.
func TestTheModulePathIsTheOneDeclaredAndNotSomethingThatLooksLikeIt(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"the ordinary one", "module example.test/thing\n\ngo 1.26.6\n", "example.test/thing"},
		{"indented", "  module example.test/thing\n", "example.test/thing"},
		{"a tab after the keyword", "module\texample.test/thing\n", "example.test/thing"},
		{"quoted, which go.mod allows", "module \"example.test/thing\"\n", "example.test/thing"},
		{"no final newline", "module example.test/thing", "example.test/thing"},
		{"a comment first", "// module example.test/wrong\nmodule example.test/thing\n", "example.test/thing"},
		// The trap the space in the check is there for.
		{"a keyword that starts the same way", "modules example.test/wrong\n", ""},
		{"the keyword alone", "module\n", ""},
		{"nothing at all", "go 1.26.6\n", ""},
	} {
		if got := module(c.in); got != c.want {
			t.Errorf("%s: read as %q and should be %q", c.name, got, c.want)
		}
	}
}

func TestATestFileIsNotPartOfWhatIsRead(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"real.go":      "package sample\n\nfunc Real() {}\n",
		"real_test.go": "package sample\n\nfunc Fake() {}\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pkgs, err := Parse(token.NewFileSet(), dir, false)
	if err != nil {
		t.Fatalf("the sample does not parse: %v", err)
	}
	pkg, ok := pkgs["sample"]
	if !ok {
		t.Fatalf("the sample package is not there, %d were found", len(pkgs))
	}
	for name := range pkg.Files {
		if filepath.Base(name) == "real_test.go" {
			t.Error("a test file was read, and a name declared in one is published to nobody")
		}
	}
	if len(pkg.Files) != 1 {
		t.Errorf("%d files were read and one was written", len(pkg.Files))
	}
}
