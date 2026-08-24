// The reader, tested against the subset it claims to read and against
// the constructs it claims to refuse.
//
// Both halves matter and the second one is the reason this file is
// long. A reader that accepts everything the corpus writes is half a
// reader: the other half is that a case using a block scalar, an
// anchor or a flow sequence is refused with a line number rather than
// read as something the author did not write. Four implementations of
// this subset exist and a construct one of them quietly accepts is a
// case that passes in one repository and fails in three.
//
// The refusal messages are checked in full rather than by substring,
// because they are diffed against the reference runner's and a wording
// that drifted would be a difference in the report that is not a
// difference in the answer.

package corpus

import (
	"strings"
	"testing"
)

// read is a document or a fatal, for the tests that are about what a
// well formed file comes to.
func read(t *testing.T, text string) Node {
	t.Helper()
	node, err := Parse(text)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return node
}

// refused is the message a document was refused with, and a fatal when
// it was not refused at all.
func refused(t *testing.T, text string) string {
	t.Helper()
	node, err := Parse(text)
	if err == nil {
		t.Fatalf("Parse read this as %s, and it should have been refused", node.What())
	}
	if _, ok := err.(*CorpusError); !ok {
		t.Fatalf("Parse gave a %T, and everything here is a *CorpusError", err)
	}
	return err.Error()
}

func TestAMappingKeepsItsKeysInTheOrderTheyWereWritten(t *testing.T) {
	doc := read(t, "suite: string\ndoc: what a string does\nschema: 4\n")
	pairs, ok := doc.Map()
	if !ok {
		t.Fatalf("the document is %s, and it should be a mapping", doc.What())
	}
	want := []string{"suite", "doc", "schema"}
	if len(pairs) != len(want) {
		t.Fatalf("%d keys, and there are %d in the file", len(pairs), len(want))
	}
	for i, key := range want {
		if pairs[i].Key != key {
			t.Errorf("key %d is %s, and the file writes %s", i, Quote(pairs[i].Key), Quote(key))
		}
	}
	if got, _ := doc.Get("doc").Str(); got != "what a string does" {
		t.Errorf("doc is %s", Quote(got))
	}
	if doc.Get("load") != nil {
		t.Errorf("Get answered a key that is not in the mapping")
	}
}

func TestASequenceOfMappingsIsOneNodePerItem(t *testing.T) {
	doc := read(t, strings.Join([]string{
		"cases:",
		"  - name: one",
		"    query: RETURN 1",
		"  - name: two",
		"    query: RETURN 2",
		"",
	}, "\n"))
	items, ok := doc.Get("cases").Seq()
	if !ok {
		t.Fatalf("`cases:` is %s", doc.Get("cases").What())
	}
	if len(items) != 2 {
		t.Fatalf("%d items, and the file writes 2", len(items))
	}
	for i, want := range []string{"one", "two"} {
		got, _ := items[i].Get("name").Str()
		if got != want {
			t.Errorf("item %d is named %s, and the file writes %s", i, Quote(got), Quote(want))
		}
	}
	// The line a refusal would cite is the line the item opened on and
	// not the line the sequence did, which is the whole reason a node
	// carries one.
	if items[1].Line() != 4 {
		t.Errorf("the second item opens on line %d, and the file opens it on line 4", items[1].Line())
	}
}

// A `- ` and the key it opens are one line in the file and two lines by
// the time the parser sees them, and the split is what lets an item
// written on one line and an item written under its dash be the same
// shape. Both spellings are in the corpus.
func TestADashAndItsFirstKeyMayShareALine(t *testing.T) {
	together := read(t, "cases:\n  - name: one\n    query: RETURN 1\n")
	apart := read(t, "cases:\n  -\n    name: one\n    query: RETURN 1\n")
	for _, doc := range []Node{together, apart} {
		items, ok := doc.Get("cases").Seq()
		if !ok || len(items) != 1 {
			t.Fatalf("`cases:` is %s with %d items", doc.Get("cases").What(), len(items))
		}
		name, _ := items[0].Get("name").Str()
		if name != "one" {
			t.Errorf("name is %s", Quote(name))
		}
	}
}

func TestAScalarRemembersWhetherItWasQuoted(t *testing.T) {
	doc := read(t, "bare: 42\nsingle: '42'\ndouble: \"42\"\n")
	for _, c := range []struct {
		key    string
		quoted bool
	}{
		{"bare", false},
		{"single", true},
		{"double", true},
	} {
		text, quoted, ok := doc.Get(c.key).Scalar()
		if !ok {
			t.Fatalf("%s is %s", c.key, doc.Get(c.key).What())
		}
		if text != "42" {
			t.Errorf("%s reads %s", c.key, Quote(text))
		}
		if quoted != c.quoted {
			t.Errorf("%s came back quoted=%v, and the file writes it the other way", c.key, quoted)
		}
	}
}

func TestASingleQuotedRunEscapesOnlyByDoublingTheQuote(t *testing.T) {
	doc := read(t, `query: 'RETURN ''it''''s'' AS s'`+"\n")
	got, quoted, _ := doc.Get("query").Scalar()
	if !quoted {
		t.Errorf("the scalar came back unquoted")
	}
	if want := `RETURN 'it''s' AS s`; got != want {
		t.Errorf("read %s, and the file writes %s", Quote(got), Quote(want))
	}
	// A backslash inside a single quoted run is a backslash, which is
	// what lets a case write a regular expression without doubling
	// every one of them.
	back := read(t, `query: 'a\nb'`+"\n")
	if got, _ := back.Get("query").Str(); got != `a\nb` {
		t.Errorf("read %s, and a single quoted backslash stays a backslash", Quote(got))
	}
}

func TestADoubleQuotedRunTakesTheEscapesTheCorpusUses(t *testing.T) {
	doc := read(t, `text: "a\nb\tc\\d\"e\r\0f\bg"`+"\n")
	want := "a\nb\tc\\d\"e\r\x00f\bg"
	if got, _ := doc.Get("text").Str(); got != want {
		t.Errorf("read %s, and the escapes come to %s", Quote(got), Quote(want))
	}
}

// A comment is dropped, and the three rules that keep the dropping from
// eating content are each worth a line: a # inside a word is part of
// the word, a quote inside a word is part of the word, and a quote that
// opens nothing that closes was not a run.
func TestACommentGoesAndAHashInsideAValueStays(t *testing.T) {
	for _, c := range []struct{ text, key, want string }{
		{"a: 1 # why\n", "a", "1"},
		{"# whole line\nb: 2\n", "b", "2"},
		{"c: person#1\n", "c", "person#1"},
		{"d: 'a # b'\n", "d", "a # b"},
		{"e: it's a plain scalar # and a comment\n", "e", "it's a plain scalar"},
		{"f: cast('  42  ' AS INT64)\n", "f", "cast('  42  ' AS INT64)"},
		{"g: RETURN 'a' AS a # a comment after a run that closed\n", "g", "RETURN 'a' AS a"},
	} {
		doc := read(t, c.text)
		got, _ := doc.Get(c.key).Str()
		if got != c.want {
			t.Errorf("%s read %s, and it should be %s", Quote(c.text), Quote(got), Quote(c.want))
		}
	}
}

// A key with nothing under it is a node rather than an error, because a
// case that expects no rows writes "rows:" and stops. Every accessor
// says no to it, so a "name:" somebody left blank is still caught.
func TestAKeyWithNothingUnderItIsAnEmptyNode(t *testing.T) {
	doc := read(t, "rows:\nname: after\n")
	empty := doc.Get("rows")
	if empty.Kind() != KindEmpty {
		t.Fatalf("`rows:` is %s, and it should be nothing", empty.What())
	}
	if empty.What() != "nothing" {
		t.Errorf("an empty node calls itself %s", Quote(empty.What()))
	}
	if _, ok := empty.Str(); ok {
		t.Errorf("Str answered an empty node")
	}
	if _, ok := empty.Seq(); ok {
		t.Errorf("Seq answered an empty node")
	}
	if _, ok := empty.Map(); ok {
		t.Errorf("Map answered an empty node")
	}
	if items, ok := empty.SeqOrEmpty(); !ok || len(items) != 0 {
		t.Errorf("SeqOrEmpty gave %v, %v, and empty is the answer it is for", items, ok)
	}
	// The key after it is still read, so an empty value ends at its own
	// line rather than swallowing what follows.
	if got, _ := doc.Get("name").Str(); got != "after" {
		t.Errorf("the key after an empty one reads %s", Quote(got))
	}
}

func TestUnknownNamesTheKeysThatAreNotExpected(t *testing.T) {
	doc := read(t, "name: one\nquery: RETURN 1\nqeury: RETURN 2\nrows:\n")
	unknown := doc.Unknown("name", "query", "rows")
	if len(unknown) != 1 || unknown[0] != "qeury" {
		t.Errorf("Unknown gave %v, and the typo is qeury", unknown)
	}
	if got := doc.Unknown("name", "query", "qeury", "rows"); len(got) != 0 {
		t.Errorf("Unknown gave %v where every key is known", got)
	}
	// A scalar has no keys and is not a mapping, so it has no unknown
	// ones either rather than being an error at this level.
	scalarNode := read(t, "just a scalar\n")
	if got := scalarNode.Unknown("name"); got != nil {
		t.Errorf("Unknown gave %v for %s", got, scalarNode.What())
	}
}

func TestWhatSaysWhichShapeANodeIs(t *testing.T) {
	for _, c := range []struct{ text, want string }{
		{"a plain scalar\n", "a scalar"},
		{"- one\n- two\n", "a sequence"},
		{"key: value\n", "a mapping"},
	} {
		if got := read(t, c.text).What(); got != c.want {
			t.Errorf("%s is %s, and it is %s", Quote(c.text), Quote(got), Quote(c.want))
		}
	}
}

// The constructs this reader will not read. Every one of them is real
// YAML that a general reader would take, and every one of them would
// mean a case says one thing to a reviewer and another to the runner.
func TestTheConstructsThisReaderDoesNotRead(t *testing.T) {
	for _, c := range []struct{ what, text, want string }{
		{
			"a tab",
			"cases:\n\t- name: one\n",
			"line 2: a tab at column 1, and indentation here is spaces",
		},
		{
			"a document marker",
			"---\nschema: 4\n",
			`line 1: "---" opens or closes a document, and a file here holds one`,
		},
		{
			"a document terminator",
			"schema: 4\n...\n",
			`line 2: "..." opens or closes a document, and a file here holds one`,
		},
		{
			"an odd indent",
			"cases:\n   - name: one\n",
			"line 2: indented 3, and indentation here goes two spaces at a time",
		},
		{
			"a dash with two spaces after it",
			"cases:\n  -  name: one\n",
			"line 2: a `- ` takes exactly one space, so that what follows it lines up with the " +
				"lines under it",
		},
		{
			"a sequence opening into a sequence",
			"cases:\n  - - one\n",
			"line 2: a sequence opening straight into another one, which nothing here needs",
		},
		{
			"a dash with nothing after it",
			"cases:\n  -\n",
			"line 2: a `-` with nothing after it",
		},
		{
			"a flow sequence",
			"columns: [a, b]\n",
			"line 1: a plain scalar opening with '[', which is a construct this reader does not read",
		},
		{
			"a flow mapping",
			"value: {type: INT64}\n",
			"line 1: a plain scalar opening with '{', which is a construct this reader does not read",
		},
		{
			"an anchor",
			"row: &base one\n",
			"line 1: a plain scalar opening with '&', which is a construct this reader does not read",
		},
		{
			"an alias",
			"row: *base\n",
			"line 1: a plain scalar opening with '*', which is a construct this reader does not read",
		},
		{
			"a tag",
			"count: !!int 4\n",
			"line 1: a plain scalar opening with '!', which is a construct this reader does not read",
		},
		{
			"a literal block scalar",
			"doc: |\n  one\n",
			"line 1: a plain scalar opening with '|', which is a construct this reader does not read",
		},
		{
			"a folded block scalar",
			"doc: >\n  one\n",
			"line 1: a plain scalar opening with '>', which is a construct this reader does not read",
		},
		{
			"a directive",
			"query: %YAML 1.2\n",
			"line 1: a plain scalar opening with '%', which is a construct this reader does not read",
		},
		{
			"a run that does not close",
			`query: "RETURN 1` + "\n",
			`line 1: a " that opens and does not close on its line`,
		},
		{
			"two runs on one line",
			`query: "a" and "b"` + "\n",
			`line 1: " and \"b\"" after the scalar ends`,
		},
		{
			// The backslash escapes the quote that would have closed the
			// run, so this is reported as a run left open rather than as a
			// scalar ending in a backslash. Both messages are in the
			// reader and this is the one that is reachable, since a
			// backslash before the closing quote always takes the quote
			// with it.
			"a double quoted run whose last character escapes its quote",
			`query: "a\"` + "\n",
			`line 1: a " that opens and does not close on its line`,
		},
		{
			"an escape this reader has no rule for",
			`query: "a\x41b"` + "\n",
			`line 1: \x is not an escape`,
		},
		{
			"a key set twice",
			"name: one\nname: two\n",
			"line 2: name is set twice in one mapping",
		},
		{
			"an indent under a key that is not two",
			"load:\n    nodes: person\n",
			"line 2: indented 4, where what is under `load:` on line 1 is indented 2",
		},
		{
			"an indent under a dash that is not two",
			"cases:\n  -\n      name: one\n",
			"line 3: indented 6, where an item of the sequence on line 2 is indented 4",
		},
		{
			"a first line that is indented",
			"  schema: 4\n",
			"line 1: the first line is indented",
		},
		{
			"a file with nothing in it",
			"# only a comment\n\n",
			"the file has nothing in it",
		},
		{
			"a line belonging to nothing above it",
			"just a scalar\nand another\n",
			"line 2: this belongs to nothing above it",
		},
	} {
		if got := refused(t, c.text); got != c.want {
			t.Errorf("%s was refused with\n  %s\nand the message should be\n  %s",
				c.what, Quote(got), Quote(c.want))
		}
	}
}

// Quote is Rust's {:?} and not Go's %q, because a refusal written in
// four languages and diffed across them cannot have one of them
// escaping a rune the others print.
func TestQuoteWritesAStringTheWayTheOtherRunnersDo(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"plain", `"plain"`},
		{`a "quoted" word`, `"a \"quoted\" word"`},
		{`a\backslash`, `"a\\backslash"`},
		{"a\nb", `"a\nb"`},
		{"a\rb", `"a\rb"`},
		{"a\tb", `"a\tb"`},
		// The one Go would escape and Rust would not.
		{"héllo → 世界", `"héllo → 世界"`},
		{"", `""`},
	} {
		if got := Quote(c.in); got != c.want {
			t.Errorf("Quote(%#v) is %s, and it should be %s", c.in, got, c.want)
		}
	}
}
