// The corpus itself, run against this client.
//
// The cases live in the engine's repository and are versioned with it,
// so this test says where they are with an environment variable and
// skips without one. That is what zu-python does with ZU_CASES and what
// makes a checkout of this repository alone still `go test ./...`
// green: a client whose test suite cannot run without a second
// repository beside it is one nobody clones to fix a typo.
//
// CI sets the variable, having checked the engine out at the revision
// the staged library was built from. Anything else compares a client
// against a corpus that is not the one it was built against, which
// reports the engine catching up to its own cases as this client
// failing.

package corpus

import (
	"context"
	"os"
	"testing"
)

// cases is where the case files are, or the empty string when nobody
// said.
var cases = os.Getenv("ZU_CASES")

// needsCases skips a test that has no corpus to run.
func needsCases(t *testing.T) {
	t.Helper()
	if cases == "" {
		t.Skip("ZU_CASES does not point at the case files")
	}
}

func TestTheCorpusReads(t *testing.T) {
	needsCases(t)
	suites, err := ReadDir(cases)
	if err != nil {
		t.Fatalf("%v", err)
	}
	total := 0
	for _, suite := range suites {
		total += len(suite.Cases)
	}
	if total == 0 {
		t.Fatalf("%d suites and no cases in any of them", len(suites))
	}
	t.Logf("%d suites, %d cases", len(suites), total)
}

// The run, which is the whole point of the package.
//
// A case the engine has not caught up to is unsupported and is not a
// failure, because the corpus is the contract and the engine catches up
// to it. A case that fails is this client answering a question wrongly,
// and there is no allowance for one.
func TestEveryCaseInTheCorpusPassesOrIsAheadOfTheEngine(t *testing.T) {
	needsCases(t)
	suites, err := ReadDir(cases)
	if err != nil {
		t.Fatalf("%v", err)
	}
	report := Run(context.Background(), suites, t.TempDir())
	for _, ran := range report.Ran {
		if ran.Outcome == Failed {
			t.Errorf("%s", ran)
		}
	}
	t.Logf("%s", report.Summary())
	// The ones ahead of the engine are listed rather than counted, so
	// that a release branch has something to read and so that a case
	// quietly becoming unsupported is visible in the log. The corpus is
	// run once and read twice, because running it is minutes.
	for _, ran := range report.Ran {
		if ran.Outcome == Unsupported {
			t.Logf("  %s", ran)
		}
	}
	// A run where nothing passed is a run that did not happen, which is
	// what a corpus read from the wrong directory or a library that
	// answers nothing looks like from here.
	if report.Count(Passed) == 0 {
		t.Errorf("no case passed, and a run where nothing passes is a run that did not happen")
	}
}
