// The case reader, which is the layer between the YAML subset and the
// runner.
//
// A case says what a statement produces one way or the other: columns
// and rows, or a GQLSTATUS. Most of what this file tests is the ways a
// case can say neither, or both, or something that looks like one and
// is not, because a corpus file is hand written and read by people in
// nine repositories who did not write it. A case that was quietly
// dropped for a typo in a key is a suite that runs green with less in
// it than anybody thinks.

package corpus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// suiteAround is a whole file with body as its list of cases, so that a
// test about one case does not have to write a header every time.
func suiteAround(body string) string {
	return "schema: 4\nsuite: test\ndoc: a suite for the tests\ncases:\n" + body
}

// oneCase is the case a body comes to, or a fatal.
func oneCase(t *testing.T, body string) Case {
	t.Helper()
	suite, err := Read(suiteAround(body))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(suite.Cases) != 1 {
		t.Fatalf("%d cases, and the body writes one", len(suite.Cases))
	}
	return suite.Cases[0]
}

// rejected is the message a file was refused with, and a fatal when it
// was read instead.
func rejected(t *testing.T, text string) string {
	t.Helper()
	suite, err := Read(text)
	if err == nil {
		t.Fatalf("Read took this as the suite %s with %d cases in it, and it should have been "+
			"refused", Quote(suite.Name), len(suite.Cases))
	}
	return err.Error()
}

func TestASuiteIsItsHeaderAndItsCases(t *testing.T) {
	suite, err := Read(strings.Join([]string{
		"schema: 4",
		"suite: string",
		"doc: what a string does",
		"cases:",
		"  - name: one",
		"    doc: the first",
		"    query: RETURN 1 AS n",
		"    columns:",
		"      - n",
		"    rows:",
		"      - values:",
		"          - type: INT64",
		`            value: "1"`,
		"",
	}, "\n"))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if suite.Name != "string" || suite.Doc != "what a string does" {
		t.Errorf("the header read as %s / %s", Quote(suite.Name), Quote(suite.Doc))
	}
	if suite.Load != nil {
		t.Errorf("a suite with no `load:` came back with one")
	}
	one := suite.Cases[0]
	if one.Name != "one" || one.Query != "RETURN 1 AS n" || one.Line != 5 {
		t.Errorf("the case read as %s / %s on line %d", Quote(one.Name), Quote(one.Query), one.Line)
	}
	if one.On != Main {
		t.Errorf("a case that names no connection runs on %s, and it should be %s",
			Quote(one.On), Quote(Main))
	}
	if !one.HasColumns || len(one.Columns) != 1 || one.Columns[0] != "n" {
		t.Errorf("the columns read as %v", one.Columns)
	}
	if len(one.Rows) != 1 || len(one.Rows[0]) != 1 || !Same(int64(1), one.Rows[0][0]) {
		t.Errorf("the rows read as %v", one.Rows)
	}
}

// The version is checked before anything else, so that a corpus
// unpacked from an old release says what it is instead of failing
// somewhere in the middle with a message about a key.
func TestTheSchemaVersionIsCheckedBeforeAnythingElse(t *testing.T) {
	for _, c := range []struct{ what, text, want string }{
		{
			"a version this runner does not read",
			"schema: 3\nsuite: test\ndoc: d\ncases:\n  - name: one\n",
			"this is schema 3 and the runner reads schema 4",
		},
		{
			"a version that is not a number",
			"schema: four\nsuite: test\ndoc: d\n",
			`"four" is not a schema version`,
		},
		{
			"no version at all",
			"suite: test\ndoc: d\n",
			"the file does not open with `schema:`",
		},
		{
			"a version that is not one line",
			"schema:\n  - 4\nsuite: test\ndoc: d\n",
			"the file does not open with `schema:`",
		},
	} {
		if got := rejected(t, c.text); got != c.want {
			t.Errorf("%s was refused with %s, and it should be %s",
				c.what, Quote(got), Quote(c.want))
		}
	}
}

func TestASuiteSaysWhatIsWrongWithItsHeader(t *testing.T) {
	for _, c := range []struct{ what, text, want string }{
		{
			"a key a suite has no room for",
			"schema: 4\nsuite: test\ndoc: d\nloadd:\ncases:\n",
			`line 1: a suite has no key "loadd"`,
		},
		{
			"no name",
			"schema: 4\ndoc: d\ncases:\n",
			"line 1: no `suite:`",
		},
		{
			"no doc",
			"schema: 4\nsuite: test\ncases:\n",
			"line 1: no `doc:`",
		},
		{
			"no cases",
			"schema: 4\nsuite: test\ndoc: d\n",
			"a suite with no `cases:`",
		},
		{
			"cases that are not a sequence",
			"schema: 4\nsuite: test\ndoc: d\ncases: one\n",
			"`cases:` is a sequence",
		},
		{
			"a `cases:` with nothing under it",
			"schema: 4\nsuite: test\ndoc: d\ncases:\n",
			"`cases:` is a sequence",
		},
	} {
		if got := rejected(t, c.text); got != c.want {
			t.Errorf("%s was refused with %s, and it should be %s",
				c.what, Quote(got), Quote(c.want))
		}
	}
}

// A name is what a report cites and what a binding's skip list names,
// so two cases sharing one is a report that says less than it looks
// like it does.
func TestTwoCasesMayNotShareAName(t *testing.T) {
	body := strings.Join([]string{
		"  - name: one",
		"    doc: d",
		"    query: RETURN 1",
		"    raises: \"42001\"",
		"  - name: one",
		"    doc: d",
		"    query: RETURN 2",
		"    raises: \"42001\"",
		"",
	}, "\n")
	want := `two cases are called "one"`
	if got := rejected(t, suiteAround(body)); got != want {
		t.Errorf("refused with %s, and it should be %s", Quote(got), Quote(want))
	}
}

func TestACaseNameIsLowerCaseWordsJoinedByDashes(t *testing.T) {
	for _, name := range []string{"One", "a name", "a_name", "a.name", `""`} {
		body := "  - name: " + name + "\n    doc: d\n    query: RETURN 1\n"
		got := rejected(t, suiteAround(body))
		if !strings.Contains(got, "is a case name, which is lower case words joined by dashes") {
			t.Errorf("%s was refused with %s", Quote(name), Quote(got))
		}
	}
	// A `name:` with nothing after it is a key with nothing under it
	// rather than an empty name, so it is refused for its shape and not
	// for what it spells. The two messages say different things and this
	// is the one that helps.
	got := rejected(t, suiteAround("  - name:\n    doc: d\n    query: RETURN 1\n"))
	if want := "line 5: `name:` is one line of text"; got != want {
		t.Errorf("a `name:` with nothing after it was refused with %s, and it should be %s",
			Quote(got), Quote(want))
	}
	// Digits and dashes are in, since a case is named after what it does
	// and some of those have numbers in them.
	one := oneCase(t, "  - name: a-3-hop-walk\n    doc: d\n    query: RETURN 1\n"+
		"    raises: \"42001\"\n")
	if one.Name != "a-3-hop-walk" {
		t.Errorf("the name read as %s", Quote(one.Name))
	}
}

// A case says what it produces one way or the other, and the reader
// refuses both ways at once and neither way at all.
func TestACaseSaysWhatItProducesExactlyOneWay(t *testing.T) {
	both := strings.Join([]string{
		"  - name: one",
		"    doc: d",
		"    query: RETURN 1",
		`    raises: "42001"`,
		"    columns:",
		"      - n",
		"",
	}, "\n")
	want := "line 5: a case that raises has no rows, and one that returns rows does not raise"
	if got := rejected(t, suiteAround(both)); got != want {
		t.Errorf("a case saying both was refused with %s", Quote(got))
	}

	neither := "  - name: one\n    doc: d\n    query: RETURN 1\n"
	want = "line 5: a case says what it produces, with `columns:` and `rows:` or with `raises:`"
	if got := rejected(t, suiteAround(neither)); got != want {
		t.Errorf("a case saying neither was refused with %s", Quote(got))
	}

	// Columns with no rows is the one that looks finished and is not, so
	// the message says how to write the case that was meant.
	noRows := "  - name: one\n    doc: d\n    query: RETURN 1\n    columns:\n      - n\n"
	want = "line 5: `columns:` with no `rows:`. A case expecting nothing back writes `rows:` with " +
		"an empty sequence under it."
	if got := rejected(t, suiteAround(noRows)); got != want {
		t.Errorf("columns with no rows was refused with %s", Quote(got))
	}
}

// FINISH answers no columns at all, which is not the same as a query
// whose columns held no rows, so an empty `columns:` is a case and not
// an omission.
func TestNoColumnsAndNoRowsAreTwoDifferentExpectations(t *testing.T) {
	empty := oneCase(t, "  - name: one\n    doc: d\n    query: FINISH\n    columns:\n    rows:\n")
	if !empty.HasColumns {
		t.Errorf("a `columns:` with nothing under it read as a case with no columns key")
	}
	if len(empty.Columns) != 0 || len(empty.Rows) != 0 {
		t.Errorf("read as %v / %v", empty.Columns, empty.Rows)
	}

	noRows := oneCase(t, "  - name: one\n    doc: d\n    query: RETURN 1 AS n\n"+
		"    columns:\n      - n\n    rows:\n")
	if len(noRows.Columns) != 1 || len(noRows.Rows) != 0 {
		t.Errorf("read as %v / %v", noRows.Columns, noRows.Rows)
	}
}

func TestARowHoldsOneValuePerColumn(t *testing.T) {
	body := strings.Join([]string{
		"  - name: one",
		"    doc: d",
		"    query: RETURN 1 AS a, 2 AS b",
		"    columns:",
		"      - a",
		"      - b",
		"    rows:",
		"      - values:",
		"          - type: INT8",
		"            value: 1",
		"",
	}, "\n")
	want := "line 5: a row of 1 against 2 columns"
	if got := rejected(t, suiteAround(body)); got != want {
		t.Errorf("refused with %s, and it should be %s", Quote(got), Quote(want))
	}
}

func TestARaisesIsTheShapeOfAGqlstatus(t *testing.T) {
	one := oneCase(t, "  - name: one\n    doc: d\n    query: RETURN\n    raises: \"42001\"\n")
	if one.Raises != "42001" {
		t.Errorf("raises read as %s", Quote(one.Raises))
	}
	// The shape and not the list, because a corpus that had to be told
	// about every code the standard defines is one nobody could add a
	// case to.
	if got := oneCase(t, "  - name: one\n    doc: d\n    query: RETURN\n"+
		"    raises: \"22G0Z\"\n").Raises; got != "22G0Z" {
		t.Errorf("a code nothing has raised yet read as %s", Quote(got))
	}
	for _, code := range []string{"4200", "420011", "42a01", "42-01", ""} {
		body := "  - name: one\n    doc: d\n    query: RETURN\n    raises: " + Quote(code) + "\n"
		want := "line 8: " + Quote(code) + " is not the shape of a GQLSTATUS, which is five " +
			"characters of digits and capitals"
		if got := rejected(t, suiteAround(body)); got != want {
			t.Errorf("%s was refused with %s, and it should be %s",
				Quote(code), Quote(got), Quote(want))
		}
	}
}

// A setup statement is one line, or a line and the connection it runs
// on, which is what a case testing two sessions against one file needs.
func TestSetupIsALineOrALineAndAConnection(t *testing.T) {
	one := oneCase(t, strings.Join([]string{
		"  - name: one",
		"    doc: d",
		"    setup:",
		"      - INSERT (:person {name: 'a'})",
		"      - on: other",
		"        query: INSERT (:person {name: 'b'})",
		"    on: other",
		"    query: MATCH (p:person) RETURN count(*) AS c",
		`    raises: "42001"`,
		"",
	}, "\n"))
	if len(one.Setup) != 2 {
		t.Fatalf("%d setup statements", len(one.Setup))
	}
	if one.Setup[0].On != Main || one.Setup[0].Query != "INSERT (:person {name: 'a'})" {
		t.Errorf("the first step read as %+v", one.Setup[0])
	}
	if one.Setup[1].On != "other" {
		t.Errorf("the second step runs on %s", Quote(one.Setup[1].On))
	}
	if one.On != "other" {
		t.Errorf("the statement runs on %s", Quote(one.On))
	}

	for _, c := range []struct{ what, body, want string }{
		{
			"setup that is not a sequence",
			"  - name: one\n    doc: d\n    setup: INSERT (:p)\n    query: RETURN 1\n",
			"line 5: `setup:` is a sequence of statements",
		},
		{
			"a step written as a mapping with no connection",
			"  - name: one\n    doc: d\n    setup:\n      - query: INSERT (:p)\n" +
				"    query: RETURN 1\n",
			"line 8: a setup statement written as a mapping names the connection it runs on",
		},
		{
			"a step with a key it has no room for",
			"  - name: one\n    doc: d\n    setup:\n      - on: other\n        params: x\n" +
				"    query: RETURN 1\n",
			`line 8: a setup statement has no key "params"`,
		},
		{
			"a connection name that is not one",
			"  - name: one\n    doc: d\n    on: Other\n    query: RETURN 1\n",
			`line 7: "Other" is a connection name, which is lower case words joined by dashes`,
		},
	} {
		if got := rejected(t, suiteAround(c.body)); got != c.want {
			t.Errorf("%s was refused with\n  %s\nand it should be\n  %s",
				c.what, Quote(got), Quote(c.want))
		}
	}
}

// A parameter is the value encoding with a name beside it, and the name
// is checked against what a statement may write after the $.
func TestAParameterIsAValueWithAName(t *testing.T) {
	one := oneCase(t, strings.Join([]string{
		"  - name: one",
		"    doc: d",
		"    query: RETURN $n AS n",
		"    params:",
		"      - name: n",
		"        type: INT64",
		`        value: "1"`,
		"      - name: nothing",
		"        type: NULL",
		`    raises: "42001"`,
		"",
	}, "\n"))
	if len(one.Params) != 2 {
		t.Fatalf("%d parameters", len(one.Params))
	}
	if one.Params[0].Name != "n" || !Same(int64(1), one.Params[0].Value) {
		t.Errorf("the first parameter read as %+v", one.Params[0])
	}
	if one.Params[1].Name != "nothing" || one.Params[1].Value != nil {
		t.Errorf("the second parameter read as %+v", one.Params[1])
	}

	for _, c := range []struct{ what, body, want string }{
		{
			"params that are not a sequence",
			"  - name: one\n    doc: d\n    query: RETURN $n\n    params: n\n",
			"line 8: `params:` is a sequence",
		},
		{
			"a parameter that is not a mapping",
			"  - name: one\n    doc: d\n    query: RETURN $n\n    params:\n      - n\n",
			"line 9: a parameter is a mapping of `name`, `type` and `value`, and this is a scalar",
		},
		{
			"a parameter with a key it has no room for",
			"  - name: one\n    doc: d\n    query: RETURN $n\n    params:\n      - name: n\n" +
				"        type: NULL\n        on: other\n",
			`line 9: a parameter has no key "on"`,
		},
		{
			"a name no statement can write",
			"  - name: one\n    doc: d\n    query: RETURN $n\n    params:\n      - name: n one\n" +
				"        type: NULL\n",
			"line 9: \"n one\" is a parameter name, which is what a statement writes after the `$`",
		},
		{
			"two parameters with one name",
			"  - name: one\n    doc: d\n    query: RETURN $n\n    params:\n      - name: n\n" +
				"        type: NULL\n      - name: n\n        type: NULL\n",
			`line 11: two parameters are called "n"`,
		},
	} {
		if got := rejected(t, suiteAround(c.body)); got != c.want {
			t.Errorf("%s was refused with\n  %s\nand it should be\n  %s",
				c.what, Quote(got), Quote(c.want))
		}
	}
}

// A load is the other half of the corpus: everything else is an
// expression, which says what a value means on the way out and nothing
// about how it got in.
func TestALoadIsATableItsColumnsAndTheEdgesBetweenItsRows(t *testing.T) {
	suite, err := Read(strings.Join([]string{
		"schema: 4",
		"suite: test",
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
		"    - name: age",
		"      type: INT64",
		"      values:",
		`        - "1"`,
		`        - "2"`,
		`        - "3"`,
		"  pairs:",
		"    - from: 0",
		"      to: 1",
		"    - from: 1",
		"      to: 2",
		"cases:",
		"  - name: one",
		"    doc: d",
		"    query: MATCH (p:person) RETURN count(*) AS c",
		`    raises: "42001"`,
		"",
	}, "\n"))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	load := suite.Load
	if load == nil {
		t.Fatalf("the suite came back with no load")
	}
	if load.Nodes != "person" || load.Edges != "knows" || load.Count != 3 {
		t.Errorf("the load read as %s / %s / %d", Quote(load.Nodes), Quote(load.Edges), load.Count)
	}
	if len(load.Columns) != 2 {
		t.Fatalf("%d columns", len(load.Columns))
	}
	if load.Columns[1].Type != "INT64" || !Same(int64(2), load.Columns[1].Values[1]) {
		t.Errorf("the second column read as %+v", load.Columns[1])
	}
	if len(load.Pairs) != 2 || load.Pairs[1] != [2]int{1, 2} {
		t.Errorf("the pairs read as %v", load.Pairs)
	}
}

func TestALoadSaysWhatIsWrongWithIt(t *testing.T) {
	// head is a load with the given body, and a case after it so that
	// the file gets as far as the load before it runs out of suite.
	head := func(body string) string {
		return "schema: 4\nsuite: test\ndoc: d\nload:\n" + body +
			"cases:\n  - name: one\n    doc: d\n    query: RETURN 1\n    raises: \"42001\"\n"
	}
	column := "  columns:\n    - name: name\n      type: STRING\n      values:\n        - a\n"
	for _, c := range []struct{ what, body, want string }{
		{
			"a key a load has no room for",
			"  nodes: person\n  edges: knows\n  count: 1\n  rows:\n" + column,
			`line 5: a load has no key "rows"`,
		},
		{
			"a table name that is not one",
			"  nodes: a person\n  edges: knows\n  count: 1\n" + column,
			`line 5: "a person" is not a table name`,
		},
		{
			"no count",
			"  nodes: person\n  edges: knows\n" + column,
			"line 5: a load says how many rows it has, with `count:`",
		},
		{
			"a count that is not a number",
			"  nodes: person\n  edges: knows\n  count: three\n" + column,
			"line 5: `count:` is a number of rows",
		},
		{
			"a count of nothing",
			"  nodes: person\n  edges: knows\n  count: 0\n" + column,
			"line 5: a load of no rows is a load nothing can be read back from",
		},
		{
			"no columns",
			"  nodes: person\n  edges: knows\n  count: 1\n",
			"line 5: a load has `columns:`",
		},
		{
			"a column short of the rows the load declares",
			"  nodes: person\n  edges: knows\n  count: 2\n" + column,
			`line 9: column "name" holds 1 values against the 2 rows the load declares`,
		},
		{
			"two columns with one name",
			"  nodes: person\n  edges: knows\n  count: 1\n" + column +
				"    - name: name\n      type: STRING\n      values:\n        - b\n",
			`line 5: two columns are called "name"`,
		},
		{
			"a column type nothing knows",
			"  nodes: person\n  edges: knows\n  count: 1\n" +
				"  columns:\n    - name: name\n      type: TEXT\n      values:\n        - a\n",
			"line 9: TEXT is not a type this encoding knows",
		},
		{
			"an edge whose row is not in the table",
			"  nodes: person\n  edges: knows\n  count: 2\n" +
				"  columns:\n    - name: name\n      type: STRING\n      values:\n        - a\n" +
				"        - b\n  pairs:\n    - from: 0\n      to: 2\n",
			"line 15: `to: 2` against a table of 2 rows, which are numbered 0 to 1",
		},
		{
			"an edge with a key it has no room for",
			"  nodes: person\n  edges: knows\n  count: 1\n" + column +
				"  pairs:\n    - from: 0\n      to: 0\n      kind: friend\n",
			`line 14: an edge has no key "kind"`,
		},
		{
			"an edge missing an end",
			"  nodes: person\n  edges: knows\n  count: 1\n" + column +
				"  pairs:\n    - from: 0\n",
			"line 14: an edge has a `to:` row number",
		},
	} {
		if got := rejected(t, head(c.body)); got != c.want {
			t.Errorf("%s was refused with\n  %s\nand it should be\n  %s",
				c.what, Quote(got), Quote(c.want))
		}
	}
}

// ReadDir walks a directory in sorted order, because Glob's order is
// the filesystem's and a report diffed against another runner's has to
// walk them the same way.
func TestReadDirWalksTheFilesInOrderAndChecksTheirNames(t *testing.T) {
	dir := t.TempDir()
	write := func(name, suite string) {
		t.Helper()
		text := "schema: 4\nsuite: " + suite + "\ndoc: d\ncases:\n  - name: one\n    doc: d\n" +
			"    query: RETURN 1\n    raises: \"42001\"\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	write("zebra.yaml", "zebra")
	write("alpha.yaml", "alpha")
	// Not a case file, and not read.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("writing README.md: %v", err)
	}

	suites, err := ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(suites) != 2 || suites[0].Name != "alpha" || suites[1].Name != "zebra" {
		t.Errorf("read %d suites, and they should be alpha then zebra:", len(suites))
		for i, suite := range suites {
			t.Errorf("  %d: %s", i, Quote(suite.Name))
		}
	}

	// A suite whose name and file disagree, which is a file somebody
	// copied and half renamed, and which would otherwise run under a
	// name no report could be diffed on.
	write("beta.yaml", "gamma")
	_, err = ReadDir(dir)
	if err == nil {
		t.Fatalf("a suite named apart from its file was read")
	}
	if want := "the suite calls itself \"gamma\" and the file calls it \"beta\""; !strings.Contains(
		err.Error(), want) {
		t.Errorf("refused with %s", Quote(err.Error()))
	}

	// And a directory with nothing in it, which is almost always a path
	// that was wrong rather than a corpus that is empty.
	if _, err := ReadDir(t.TempDir()); err == nil {
		t.Errorf("an empty directory read as a corpus")
	} else if !strings.HasSuffix(err.Error(), "no case files") {
		t.Errorf("an empty directory was refused with %s", Quote(err.Error()))
	}
}

func TestANameIsCheckedAgainstWhatWritesIt(t *testing.T) {
	for _, c := range []struct {
		text   string
		dashed bool
		word   bool
	}{
		{"one", true, true},
		{"a-name", true, false},
		{"a-3-hop", true, false},
		{"a_name", false, true},
		{"Name", false, true},
		{"n1", true, true},
		{"a name", false, false},
		{"", false, false},
		{"héllo", false, false},
	} {
		if got := dashedWords(c.text); got != c.dashed {
			t.Errorf("dashedWords(%s) is %v, and it should be %v", Quote(c.text), got, c.dashed)
		}
		if got := wordOrUnderscore(c.text); got != c.word {
			t.Errorf("wordOrUnderscore(%s) is %v, and it should be %v", Quote(c.text), got, c.word)
		}
	}
}
