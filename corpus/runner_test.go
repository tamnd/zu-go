// The runner, on cases written here rather than on the corpus.
//
// The corpus itself is run by the test beside this one, which needs the
// case files and is skipped without them. This file is the other half:
// a handful of cases written to make the runner do each of the things
// it does, including the ones the corpus has no case for because the
// corpus is a corpus of correct expectations. A case that wants the
// wrong number of rows has to be reported and not merely fail, and the
// only way to have one is to write one.
//
// The detail strings are checked in full, because they are what a
// report says and the report is diffed against the reference runner's.

package corpus

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	zu "github.com/tamnd/zu-go"
)

// ranHere runs a suite written inline and gives back what each case
// came to. The databases go under the test's own directory, which the
// testing package removes.
func ranHere(t *testing.T, text string) []Ran {
	t.Helper()
	suite, err := Read(text)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return Run(context.Background(), []Suite{suite}, t.TempDir()).Ran
}

// caseText is a suite of one case, with body under `cases:`.
func caseText(body string) string {
	return "schema: 4\nsuite: inline\ndoc: cases written for the runner's own tests\ncases:\n" + body
}

// only is the one case a suite of one came to.
func only(t *testing.T, body string) Ran {
	t.Helper()
	all := ranHere(t, caseText(body))
	if len(all) != 1 {
		t.Fatalf("%d cases ran, and the suite writes one", len(all))
	}
	return all[0]
}

func TestACaseThatSaysWhatItProducesPasses(t *testing.T) {
	got := only(t, strings.Join([]string{
		"  - name: a-statement-returns-what-it-names",
		"    doc: d",
		"    query: UNWIND [1, 2] AS n RETURN n, n * 2 AS twice",
		"    columns:",
		"      - n",
		"      - twice",
		"    rows:",
		"      - values:",
		"          - type: INT64",
		`            value: "1"`,
		"          - type: INT64",
		`            value: "2"`,
		"      - values:",
		"          - type: INT64",
		`            value: "2"`,
		"          - type: INT64",
		`            value: "4"`,
		"",
	}, "\n"))
	if got.Outcome != Passed {
		t.Errorf("came to %s: %s", got.Outcome.mark(), got.Detail)
	}
	if got.Detail != "" {
		t.Errorf("a case that passed carries %s", Quote(got.Detail))
	}
	if got.Suite != "inline" || got.Case != "a-statement-returns-what-it-names" {
		t.Errorf("the report names %s/%s", Quote(got.Suite), Quote(got.Case))
	}
}

func TestACaseThatWantsAConditionPassesOnThatCode(t *testing.T) {
	got := only(t, "  - name: an-empty-statement-is-a-condition\n    doc: d\n"+
		"    query: \"\"\n    raises: \"42001\"\n")
	if got.Outcome != Passed {
		t.Errorf("came to %s: %s", got.Outcome.mark(), got.Detail)
	}
}

// The failures, which are what the runner is for and what the corpus
// has no examples of.
func TestEveryWayACaseCanFailIsReportedInFull(t *testing.T) {
	for _, c := range []struct{ what, body, want string }{
		{
			"the wrong columns",
			"  - name: one\n    doc: d\n    query: RETURN 1 AS n\n    columns:\n      - m\n" +
				"    rows:\n      - values:\n          - type: INT64\n" +
				`            value: "1"` + "\n",
			`columns ["n"] where the case wants ["m"]`,
		},
		{
			"the wrong value",
			"  - name: one\n    doc: d\n    query: RETURN 1 AS n\n    columns:\n      - n\n" +
				"    rows:\n      - values:\n          - type: INT64\n" +
				`            value: "2"` + "\n",
			`row 1 column n is INT64 "1" where the case wants INT64 "2"`,
		},
		{
			"the wrong type in the right column",
			"  - name: one\n    doc: d\n    query: RETURN 1 AS n\n    columns:\n      - n\n" +
				"    rows:\n      - values:\n          - type: STRING\n            value: '1'\n",
			`row 1 column n is INT64 "1" where the case wants STRING "1"`,
		},
		{
			"too few rows",
			"  - name: one\n    doc: d\n    query: UNWIND [1] AS n RETURN n\n    columns:\n" +
				"      - n\n    rows:\n      - values:\n          - type: INT64\n" +
				`            value: "1"` + "\n      - values:\n          - type: INT64\n" +
				`            value: "2"` + "\n",
			"1 rows where the case wants 2",
		},
		{
			"too many rows",
			"  - name: one\n    doc: d\n    query: UNWIND [1, 2] AS n RETURN n\n    columns:\n" +
				"      - n\n    rows:\n      - values:\n          - type: INT64\n" +
				`            value: "1"` + "\n",
			"2 rows where the case wants 1",
		},
		{
			"rows where the case wants a condition",
			"  - name: one\n    doc: d\n    query: RETURN 1 AS n\n    raises: \"22003\"\n",
			"returned rows where the case wants 22003",
		},
		{
			"a condition where the case wants another one",
			"  - name: one\n    doc: d\n    query: \"\"\n    raises: \"22003\"\n",
			"raised 42001 where the case wants 22003",
		},
		{
			"a setup that will not run",
			"  - name: one\n    doc: d\n    setup:\n      - INSERT (:nowhere)\n" +
				"    query: RETURN 1 AS n\n    columns:\n      - n\n    rows:\n" +
				"      - values:\n          - type: INT64\n" + `            value: "1"` + "\n",
			"setup 1",
		},
	} {
		// A setup that will not run is a failure or a case ahead of the
		// engine depending on what the engine says about the statement,
		// and either way it is never a pass. Everything else here is a
		// failure and nothing else.
		wantsFailed := !strings.HasPrefix(c.want, "setup")
		got := only(t, c.body)
		if got.Outcome == Passed {
			t.Errorf("%s passed", c.what)
			continue
		}
		if wantsFailed && got.Outcome != Failed {
			t.Errorf("%s came to %s: %s", c.what, got.Outcome.mark(), got.Detail)
			continue
		}
		if !strings.HasPrefix(got.Detail, c.want) {
			t.Errorf("%s was reported as\n  %s\nand it should open with\n  %s",
				c.what, Quote(got.Detail), Quote(c.want))
		}
	}
}

// A case the engine has not caught up to is unsupported and not a
// failure, which is what lets the corpus be the contract and the engine
// catch up to it. The two classes that say so are 42 and 0A.
//
// The statement has to be one the engine really has not reached, and a
// test like this is a canary by construction: the day CREATE lands, this
// stops testing what it says it tests and has to pick another spelling.
// It was SELECT before, which the engine now parses.
func TestACaseAheadOfTheEngineIsUnsupportedAndNotAFailure(t *testing.T) {
	got := only(t, "  - name: one\n    doc: d\n    query: CREATE NODE TABLE person(uid INT64)\n"+
		"    columns:\n      - n\n"+
		"    rows:\n")
	if got.Outcome != Unsupported {
		t.Errorf("came to %s: %s", got.Outcome.mark(), got.Detail)
	}
	if !strings.HasPrefix(got.Detail, "42001") {
		t.Errorf("the detail is %s, and it should open with the code", Quote(got.Detail))
	}
}

// Two connections over one file share the write side, so each sees what
// the other has committed, which is what a case about a session means.
func TestACaseMayNameTheConnectionEachStatementRunsOn(t *testing.T) {
	got := only(t, strings.Join([]string{
		"  - name: a-second-connection-sees-what-the-first-committed",
		"    doc: d",
		"    setup:",
		"      - on: writer",
		"        query: INSERT (:person {name: 'a'})",
		"    on: reader",
		"    query: MATCH (p:person) RETURN count(*) AS c",
		"    columns:",
		"      - c",
		"    rows:",
		"      - values:",
		"          - type: INT64",
		`            value: "1"`,
		"",
	}, "\n"))
	if got.Outcome == Failed {
		t.Errorf("came to %s: %s", got.Outcome.mark(), got.Detail)
	}
}

func TestACaseMayBindParameters(t *testing.T) {
	got := only(t, strings.Join([]string{
		"  - name: a-parameter-is-bound-by-name",
		"    doc: d",
		"    query: RETURN $n AS n",
		"    params:",
		"      - name: n",
		"        type: INT64",
		`        value: "7"`,
		"    columns:",
		"      - n",
		"    rows:",
		"      - values:",
		"          - type: INT64",
		`            value: "7"`,
		"",
	}, "\n"))
	if got.Outcome != Passed {
		t.Errorf("came to %s: %s", got.Outcome.mark(), got.Detail)
	}
}

// The load is the other half of the corpus: an expression says what a
// value means on the way out and nothing about how it got in.
func TestASuiteWithALoadPutsItInThroughTheLoader(t *testing.T) {
	all := ranHere(t, strings.Join([]string{
		"schema: 4",
		"suite: inline",
		"doc: d",
		"load:",
		"  nodes: person",
		"  edges: knows",
		"  count: 3",
		"  columns:",
		"    - name: name",
		"      type: STRING",
		"      values:",
		"        - a",
		"        - b",
		"        - c",
		"  pairs:",
		"    - from: 0",
		"      to: 1",
		"    - from: 1",
		"      to: 2",
		"cases:",
		"  - name: the-rows-that-went-in-come-back",
		"    doc: d",
		"    query: MATCH (p:person) RETURN count(*) AS c",
		"    columns:",
		"      - c",
		"    rows:",
		"      - values:",
		"          - type: INT64",
		`            value: "3"`,
		"  - name: the-edges-that-went-in-come-back",
		"    doc: d",
		"    query: MATCH (:person)-[:knows]->(:person) RETURN count(*) AS c",
		"    columns:",
		"      - c",
		"    rows:",
		"      - values:",
		"          - type: INT64",
		`            value: "2"`,
		"",
	}, "\n"))
	for _, ran := range all {
		if ran.Outcome == Failed {
			t.Errorf("%s came to %s: %s", ran.Case, ran.Outcome.mark(), ran.Detail)
		}
	}
	// Each case of the suite gets its own copy of the load, so the
	// second one does not see what the first one did.
	if len(all) != 2 {
		t.Fatalf("%d cases ran", len(all))
	}
}

// A failure leaves its database behind, which is the one thing somebody
// reading the report will want to open. Everything else goes as it
// finishes, because fourteen hundred cases are fourteen hundred files.
func TestOnlyAFailedCaseLeavesItsDatabaseBehind(t *testing.T) {
	directory := t.TempDir()
	suite, err := Read(caseText(strings.Join([]string{
		"  - name: one-that-passes",
		"    doc: d",
		"    query: RETURN 1 AS n",
		"    columns:",
		"      - n",
		"    rows:",
		"      - values:",
		"          - type: INT64",
		`            value: "1"`,
		"  - name: one-that-fails",
		"    doc: d",
		"    query: RETURN 1 AS n",
		"    columns:",
		"      - n",
		"    rows:",
		"      - values:",
		"          - type: INT64",
		`            value: "2"`,
		"",
	}, "\n")))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	Run(context.Background(), []Suite{suite}, directory)

	if _, err := os.Stat(casePath(directory, "inline", "one-that-passes")); !os.IsNotExist(err) {
		t.Errorf("a case that passed left its database behind")
	}
	if _, err := os.Stat(casePath(directory, "inline", "one-that-fails")); err != nil {
		t.Errorf("a case that failed left nothing to open: %v", err)
	}
}

func TestAReportSaysWhatEachCaseDidAndWhatTheRunCameTo(t *testing.T) {
	report := Report{Ran: []Ran{
		{Suite: "string", Case: "one", Line: 12, Outcome: Passed},
		{Suite: "string", Case: "two", Line: 20, Outcome: Failed, Detail: "1 rows where the case wants 2"},
		{Suite: "select", Case: "three", Line: 38, Outcome: Unsupported, Detail: "42001: no"},
	}}
	for i, want := range []string{
		"string/one line 12 ok",
		"string/two line 20 FAILED: 1 rows where the case wants 2",
		"select/three line 38 unsupported: 42001: no",
	} {
		if got := report.Ran[i].String(); got != want {
			t.Errorf("line %d reads %s, and it should be %s", i, Quote(got), Quote(want))
		}
	}
	if got, want := report.Summary(), "3 cases, 1 passed, 1 failed, 1 unsupported"; got != want {
		t.Errorf("the summary reads %s, and it should be %s", Quote(got), Quote(want))
	}
	for _, c := range []struct {
		outcome Outcome
		count   int
	}{{Passed, 1}, {Failed, 1}, {Unsupported, 1}} {
		if got := report.Count(c.outcome); got != c.count {
			t.Errorf("%s counted %d, and there is %d", c.outcome.mark(), got, c.count)
		}
	}
	if got := (Report{}).Summary(); got != "0 cases, 0 passed, 0 failed, 0 unsupported" {
		t.Errorf("an empty report summarises as %s", Quote(got))
	}
}

// compare reports the first difference rather than all of them, because
// the first is nearly always the cause of the rest, and the order the
// checks run in is the reference runner's.
func TestCompareReportsTheFirstDifferenceAndNothingAfterIt(t *testing.T) {
	for _, c := range []struct {
		what        string
		wantColumns []string
		wantRows    [][]any
		gotColumns  []string
		gotRows     [][]any
		detail      string
	}{
		{
			"nothing wrong",
			[]string{"n"}, [][]any{{int64(1)}}, []string{"n"}, [][]any{{int64(1)}}, "",
		},
		{
			"the columns before the rows, since a wrong column makes every row wrong",
			[]string{"n"}, [][]any{{int64(1)}}, []string{"m"}, [][]any{{int64(2)}},
			`columns ["m"] where the case wants ["n"]`,
		},
		{
			"the first row that differs and not the second",
			[]string{"n"}, [][]any{{int64(1)}, {int64(2)}},
			[]string{"n"}, [][]any{{int64(9)}, {int64(8)}},
			`row 1 column n is INT64 "9" where the case wants INT64 "1"`,
		},
		{
			"a value before a count, since a row that differs says more than a total",
			[]string{"n"}, [][]any{{int64(1)}, {int64(2)}},
			[]string{"n"}, [][]any{{int64(9)}},
			`row 1 column n is INT64 "9" where the case wants INT64 "1"`,
		},
		{
			"the count when every row that is there matches",
			[]string{"n"}, [][]any{{int64(1)}, {int64(2)}},
			[]string{"n"}, [][]any{{int64(1)}},
			"1 rows where the case wants 2",
		},
		{
			"no columns against no columns, which FINISH answers",
			[]string{}, [][]any{}, []string{}, [][]any{}, "",
		},
		{
			"no columns against some, which is still a difference",
			[]string{}, [][]any{}, []string{"n"}, [][]any{},
			`columns ["n"] where the case wants []`,
		},
	} {
		if got := compare(c.wantColumns, c.wantRows, c.gotColumns, c.gotRows); got != c.detail {
			t.Errorf("%s came to\n  %s\nand it should be\n  %s",
				c.what, Quote(got), Quote(c.detail))
		}
	}
	// A case with no columns writes an empty sequence and a result with
	// none hands back an empty slice, and whether either is nil is not
	// something a case can say.
	if !sameNames(nil, []string{}) {
		t.Errorf("a nil list of names and an empty one are not the same list")
	}
}

// The message rather than Error, because this client puts "zu: " in
// front of every one and the reference runner does not, so a report
// built out of Error would differ on every line that has a failure in
// it.
func TestTheReportPrintsWhatTheEngineSaidAndNotWhatGoWouldLog(t *testing.T) {
	engine := &zu.Error{Code: "42001", Message: "42001: line 1, column 1: expected MATCH"}
	if got := errorText(engine); got != "42001: line 1, column 1: expected MATCH" {
		t.Errorf("an engine failure reads %s", Quote(got))
	}
	if strings.HasPrefix(errorText(engine), "zu: ") {
		t.Errorf("the report opens a line with what a Go program logs")
	}
	if got := statusCode(engine); got != "42001" {
		t.Errorf("the code reads %s", Quote(got))
	}
	if !unsupported(engine) {
		t.Errorf("42001 is a case ahead of the engine and not a failure")
	}

	// The two classes that say a case is ahead of the engine, and
	// nothing else.
	for _, c := range []struct {
		code  string
		ahead bool
	}{
		{"42001", true},
		{"42002", true},
		{"0A000", true},
		{"22003", false},
		{"22G03", false},
		{"00000", false},
		{"", false},
	} {
		if got := unsupported(&zu.Error{Code: c.code}); got != c.ahead {
			t.Errorf("%s is unsupported=%v, and it should be %v", Quote(c.code), got, c.ahead)
		}
	}

	// The reader's own failures and the export's, which go into the
	// same report and have no GQLSTATUS at all.
	if got := errorText(&CorpusError{Message: "line 4: no `doc:`"}); got != "line 4: no `doc:`" {
		t.Errorf("a corpus failure reads %s", Quote(got))
	}
	if got := errorText(&ArrowError{Message: "no export"}); got != "no export" {
		t.Errorf("an export failure reads %s", Quote(got))
	}
	if got := errorText(errors.New("something else")); got != "something else" {
		t.Errorf("a plain failure reads %s", Quote(got))
	}
	if got := statusCode(errors.New("something else")); got != "" {
		t.Errorf("a failure carrying no status reads %s", Quote(got))
	}
	// A failure the engine wrapped, since errors.As is what finds it.
	wrapped := fmt.Errorf("running the statement: %w", engine)
	if got := statusCode(wrapped); got != "42001" {
		t.Errorf("a wrapped failure carries the code %s", Quote(got))
	}
}

func TestNamesWritesAColumnListTheWayTheReferenceRunnerDoes(t *testing.T) {
	for _, c := range []struct {
		columns []string
		want    string
	}{
		{nil, "[]"},
		{[]string{}, "[]"},
		{[]string{"n"}, `["n"]`},
		{[]string{"n", "twice"}, `["n", "twice"]`},
	} {
		if got := names(c.columns); got != c.want {
			t.Errorf("names(%v) is %s, and it should be %s", c.columns, Quote(got), Quote(c.want))
		}
	}
}
