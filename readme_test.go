// The README's programs are run here, as printed, character for
// character.
//
// A quickstart is the most read and least executed code a client has.
// It is copied by hand out of a page and it goes wrong quietly, a
// rename at a time, until somebody's first five minutes are spent on an
// error message about a table that does not exist. Both of the whole
// programs on that page were wrong when this test was written, and
// neither was wrong in a way a reader could have guessed at, which is
// the argument for the file.
//
// A block is a whole program when it opens with `package main`, which
// is the rule the README follows: a block that stands on its own is a
// program and a block that shows one call in the middle of a session is
// a fragment. Each program is built and run in a module of its own, in
// a temporary directory, because the file it writes is the one a reader
// would find beside them afterwards. The module reaches this checkout
// through replace directives and the proxy is turned off, so what runs
// is this working tree and nothing a cache happened to have.
//
// Only under the default linking mode. The other two want a libzu the
// caller staged, and a temporary module of our own making has no way to
// know where they put it.

//go:build !zu_system && !zu_static

package zu

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// blocks returns every fenced block of one language, in the order they
// appear on the page.
func blocks(t *testing.T, language string) []string {
	t.Helper()
	page, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("the README does not read: %v", err)
	}
	var found []string
	var current []string
	open := false
	for line := range strings.Lines(string(page)) {
		line = strings.TrimRight(line, "\n")
		switch {
		case !open && strings.TrimRight(line, " ") == "```"+language:
			open, current = true, nil
		case open && strings.TrimRight(line, " ") == "```":
			found = append(found, strings.Join(current, "\n")+"\n")
			open = false
		case open:
			current = append(current, line)
		}
	}
	if open {
		t.Fatal("a fenced block the README never closes")
	}
	return found
}

// programs returns the blocks that are whole programs.
func programs(t *testing.T) []string {
	t.Helper()
	var whole []string
	for _, block := range blocks(t, "go") {
		if strings.HasPrefix(block, "package main\n") {
			whole = append(whole, block)
		}
	}
	return whole
}

// programWith returns the one whole program that has all of words in
// it. By what it contains rather than by where it sits, because a page
// that gains a snippet should not renumber the tests below it.
func programWith(t *testing.T, words ...string) string {
	t.Helper()
	var found []string
	for _, p := range programs(t) {
		all := true
		for _, word := range words {
			all = all && strings.Contains(p, word)
		}
		if all {
			found = append(found, p)
		}
	}
	if len(found) != 1 {
		t.Fatalf("one program with %v, found %d", words, len(found))
	}
	return found[0]
}

// requires is the module path of every requirement this client names,
// read off its own go.mod rather than written down here, so that a
// platform added to the client is one this test picks up.
func requires(t *testing.T) []string {
	t.Helper()
	mod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatalf("the go.mod does not read: %v", err)
	}
	line := regexp.MustCompile(`(?m)^\s*(github\.com/tamnd/zu-go\S*)\s+v`)
	var found []string
	for _, m := range line.FindAllStringSubmatch(string(mod), -1) {
		found = append(found, m[1])
	}
	if len(found) == 0 {
		t.Fatal("the client requires no library module, which cannot be right")
	}
	return found
}

// staged writes program into a module of its own and returns the
// directory it sits in.
func staged(t *testing.T, program string) string {
	t.Helper()
	root, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()

	var mod strings.Builder
	mod.WriteString("module readme\n\ngo 1.26.6\n\n")
	mod.WriteString("require github.com/tamnd/zu-go v0.0.0\n")
	mod.WriteString("replace github.com/tamnd/zu-go v0.0.0 => " + root + "\n")
	for _, path := range requires(t) {
		under := strings.TrimPrefix(path, "github.com/tamnd/zu-go/")
		mod.WriteString("require " + path + " v0.0.0 // indirect\n")
		mod.WriteString("replace " + path + " v0.0.0 => " + filepath.Join(root, under) + "\n")
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", mod.String())
	write("main.go", program)
	return dir
}

// run builds and runs a program and returns what it printed. It fails
// the test if the program does not, because a quickstart that exits
// non-zero is the whole thing this file is here to catch.
func run(t *testing.T, program string) (string, string) {
	t.Helper()
	dir := staged(t, program)
	cmd := exec.CommandContext(t.Context(), "go", "run", ".")
	cmd.Dir = dir
	// Off, so that a program which runs here is one a reader with no
	// network could have run. Everything it imports is a directory.
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off", "GOWORK=off")
	var out, errs strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errs
	if err := cmd.Run(); err != nil {
		t.Fatalf("the README's program does not run: %v\n%s", err, errs.String())
	}
	return out.String(), dir
}

func TestTheReadmePrintsProgramsAndNotOnlyFragments(t *testing.T) {
	whole, all := len(programs(t)), len(blocks(t, "go"))
	if whole != 2 {
		t.Errorf("the README has %d whole programs and had 2", whole)
	}
	if all <= whole {
		t.Errorf("the README has %d go blocks and none of them is a fragment", all)
	}
}

func TestTheQuickstartRunsAsPrinted(t *testing.T) {
	out, dir := run(t, programWith(t, "zu.Create", "rows.All()"))
	want := "ada 1\ngrace 2\nlynn 3\n"
	if out != want {
		t.Errorf("the quickstart printed %q and the page says it prints %q", out, want)
	}
	// A reader runs it in the directory they are standing in, and the
	// database the page says it writes is there when it finishes.
	if _, err := os.Stat(filepath.Join(dir, "social.zu1")); err != nil {
		t.Errorf("the quickstart wrote no social.zu1 beside the reader: %v", err)
	}
}

func TestTheDatabaseSqlProgramRunsAsPrinted(t *testing.T) {
	out, dir := run(t, programWith(t, `sql.Open("zu"`))
	if out != "ada\n" {
		t.Errorf("the database/sql program printed %q and had ada", out)
	}
	// create=true in the connection string, which is the one thing
	// about that string a reader cannot check by reading it.
	if _, err := os.Stat(filepath.Join(dir, "social.zu1")); err != nil {
		t.Errorf("create=true created nothing: %v", err)
	}
}

func TestTheQuickstartRefusesToRunTwice(t *testing.T) {
	// The paragraph under the quickstart says the second run fails and
	// says why. A page that explains a failure nobody gets is worse
	// than one that says nothing.
	program := programWith(t, "zu.Create", "rows.All()")
	dir := staged(t, program)
	for _, want := range []bool{true, false} {
		cmd := exec.CommandContext(t.Context(), "go", "run", ".")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off", "GOWORK=off")
		var errs strings.Builder
		cmd.Stderr = &errs
		err := cmd.Run()
		if want && err != nil {
			t.Fatalf("the first run failed: %v\n%s", err, errs.String())
		}
		if !want {
			if err == nil {
				t.Fatal("the second run worked, so Create opened a database that was already there")
			}
			if !strings.Contains(errs.String(), "social.zu1") {
				t.Errorf("the second run failed without naming the path: %s", errs.String())
			}
		}
	}
}
