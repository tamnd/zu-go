package zu

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// The five platforms are named in five places: a directory under lib,
// a module path in its go.mod, a build tag in its prebuilt.go, the
// file here that imports it, and the negated list in
// lib_unsupported.go. Adding a sixth platform means editing all five,
// and forgetting one of them is a build that quietly does nothing
// rather than a build that fails. These tests are what makes it fail.

// platforms is every lib/<goos>-<goarch> directory in the repository,
// which is the list the other tests hold everything else to.
func platforms(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir("lib")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		t.Fatal("no platform directories under lib")
	}
	sort.Strings(names)
	return names
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEveryShippedLibraryIsAModuleNamedAfterItsPlatform(t *testing.T) {
	for _, name := range platforms(t) {
		goos, goarch, ok := strings.Cut(name, "-")
		if !ok {
			t.Errorf("lib/%s is not <goos>-<goarch>", name)
			continue
		}

		want := "module github.com/tamnd/zu-go/lib/" + name
		if got := read(t, filepath.Join("lib", name, "go.mod")); !strings.Contains(got, want) {
			t.Errorf("lib/%s/go.mod does not say %q", name, want)
		}

		prebuilt := read(t, filepath.Join("lib", name, "prebuilt.go"))
		tag := fmt.Sprintf("//go:build %s && %s", goos, goarch)
		if !strings.HasPrefix(prebuilt, tag+"\n") {
			t.Errorf("lib/%s/prebuilt.go does not start with %q", name, tag)
		}
		// The archive is what the whole directory is for, and a
		// directive naming one that is not beside it links nothing.
		if !strings.Contains(prebuilt, "#cgo LDFLAGS: ${SRCDIR}/libzu.a") {
			t.Errorf("lib/%s/prebuilt.go does not link the archive beside it", name)
		}
	}
}

func TestEveryShippedLibraryIsImportedOnItsPlatformAndNoOther(t *testing.T) {
	for _, name := range platforms(t) {
		goos, goarch, _ := strings.Cut(name, "-")

		path := fmt.Sprintf("lib_%s_%s.go", goos, goarch)
		src := read(t, path)

		tag := fmt.Sprintf("//go:build cgo && !zu_system && !zu_static && %s && %s", goos, goarch)
		if !strings.HasPrefix(src, tag+"\n") {
			t.Errorf("%s does not start with %q", path, tag)
		}

		imp := fmt.Sprintf("import _ %q", "github.com/tamnd/zu-go/lib/"+name)
		if !strings.Contains(src, imp) {
			t.Errorf("%s does not contain %s", path, imp)
		}
	}
}

func TestThePlatformWithNoLibraryIsTheOneNoDirectoryNames(t *testing.T) {
	tag, _, _ := strings.Cut(read(t, "lib_unsupported.go"), "\n")

	for _, name := range platforms(t) {
		goos, goarch, _ := strings.Cut(name, "-")
		clause := fmt.Sprintf("!(%s && %s)", goos, goarch)
		if !strings.Contains(tag, clause) {
			t.Errorf("lib_unsupported.go would fire on %s: its tag has no %s", name, clause)
		}
	}

	// The other direction, so that a platform whose directory was
	// removed stops being excluded here too.
	for _, clause := range strings.Split(tag, "&&") {
		clause = strings.TrimSpace(clause)
		if !strings.HasPrefix(clause, "!(") {
			continue
		}
		inner := strings.TrimSuffix(strings.TrimPrefix(clause, "!("), ")")
		goos, goarch, ok := strings.Cut(inner, " && ")
		if !ok {
			continue
		}
		name := goos + "-" + goarch
		if _, err := os.Stat(filepath.Join("lib", name)); err != nil {
			t.Errorf("lib_unsupported.go excludes %s, and there is no lib/%s", inner, name)
		}
	}
}

// The library this test binary is linked against, which on a machine
// running the default mode is the one that shipped. It says which
// revision of the engine that was, so that a failure anywhere else in
// the suite can be read against a commit rather than against nothing.
func TestTheShippedLibraryNamesTheRevisionItCameFrom(t *testing.T) {
	name := runtime.GOOS + "-" + runtime.GOARCH
	dir := filepath.Join("lib", name)
	if _, err := os.Stat(filepath.Join(dir, "libzu.a")); err != nil {
		t.Skipf("no archive in %s: this platform has not been built yet", dir)
	}

	rev := strings.TrimSpace(read(t, filepath.Join(dir, "REVISION")))
	if len(rev) != 40 {
		t.Errorf("%s/REVISION is %q, which is not a commit", dir, rev)
	}
	if syslibs := strings.TrimSpace(read(t, filepath.Join(dir, "NATIVE_STATIC_LIBS"))); syslibs == "" {
		t.Errorf("%s/NATIVE_STATIC_LIBS is empty", dir)
	}
	t.Logf("libzu for %s, from zu %s, engine %s, ABI %s", name, rev, Version(), ABIVersion())
}
