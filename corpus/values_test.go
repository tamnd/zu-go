// The value encoding, tested on both sides of it.
//
// A case says what a statement produces by naming a type and a payload,
// and the whole point of naming the type is that a payload cannot be
// misread. So the tests here are mostly refusals: an INT64 written bare
// is refused because some reader will round it, a STRING written where
// a LIST belongs is refused, a payload out of its type's range is
// refused. A round trip through a reader that accepted all three would
// still look green.
//
// The other half is [Show], which is what a failure report prints. It
// is diffed against the Rust runner's line for line, so a float that
// switches to an exponent one power earlier here than there is a
// difference in the report that is not a difference in the answer.

package corpus

import (
	"math"
	"strings"
	"testing"
	"time"

	zu "github.com/tamnd/zu-go"
)

// value is the value a `type:`/`value:` mapping comes to, written the
// way a case writes it.
func value(t *testing.T, text string) any {
	t.Helper()
	node, err := Parse(text)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got, err := Decode(node)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	return got
}

// declined is the message a `type:`/`value:` mapping was refused with,
// and a fatal when it was read instead.
func declined(t *testing.T, text string) string {
	t.Helper()
	node, err := Parse(text)
	if err != nil {
		return err.Error()
	}
	got, err := Decode(node)
	if err == nil {
		t.Fatalf("Decode read this as %s, and it should have been refused", Show(got))
	}
	return err.Error()
}

func TestEveryTypeSaysWhetherItsPayloadIsQuoted(t *testing.T) {
	// The whole table, written out rather than ranged over, because the
	// point of the test is that the table is this and not whatever the
	// map happens to hold.
	bare := []string{"NULL", "BOOL", "INT8", "INT16", "INT32", "UINT8", "UINT16", "UINT32",
		"STRING", "LIST", "PATH"}
	quoted := []string{"INT64", "UINT64", "FLOAT32", "FLOAT64", "BYTES", "DATE", "LOCALTIME",
		"ZONEDTIME", "LOCALDATETIME", "ZONEDDATETIME", "DURATION", "NODE", "EDGE"}
	for _, ty := range bare {
		if isQuoted, known := Form(ty); !known || isQuoted {
			t.Errorf("%s is quoted=%v known=%v, and it is written bare", ty, isQuoted, known)
		}
	}
	for _, ty := range quoted {
		if isQuoted, known := Form(ty); !known || !isQuoted {
			t.Errorf("%s is quoted=%v known=%v, and it is written in quotes", ty, isQuoted, known)
		}
	}
	if want := len(bare) + len(quoted); len(quotedForm) != want {
		t.Errorf("the encoding has %d types and this test names %d", len(quotedForm), want)
	}
	// DECIMAL has a name and no value behind it, and is told apart from a
	// typo so that the message says which of the two happened.
	if _, known := Form("DECIMAL"); known {
		t.Errorf("DECIMAL is a type, and the engine has no value for one")
	}
}

func TestAPayloadIsReadAsTheTypeBesideIt(t *testing.T) {
	for _, c := range []struct {
		text string
		want any
	}{
		{"type: NULL\n", nil},
		{"type: BOOL\nvalue: true\n", true},
		{"type: BOOL\nvalue: false\n", false},
		{"type: INT8\nvalue: -128\n", int64(-128)},
		{"type: INT16\nvalue: 32767\n", int64(32767)},
		{"type: INT32\nvalue: -2147483648\n", int64(-2147483648)},
		{`type: INT64` + "\n" + `value: "9223372036854775807"` + "\n", int64(math.MaxInt64)},
		{"type: UINT8\nvalue: 255\n", int64(255)},
		{"type: UINT16\nvalue: 65535\n", int64(65535)},
		{"type: UINT32\nvalue: 4294967295\n", int64(4294967295)},
		{`type: UINT64` + "\n" + `value: "0"` + "\n", int64(0)},
		{`type: FLOAT64` + "\n" + `value: "1.5"` + "\n", 1.5},
		{`type: FLOAT64` + "\n" + `value: "-0.0"` + "\n", math.Copysign(0, -1)},
		{`type: FLOAT64` + "\n" + `value: "inf"` + "\n", math.Inf(1)},
		{`type: FLOAT64` + "\n" + `value: "-inf"` + "\n", math.Inf(-1)},
		// A FLOAT32 is held as the double the single rounds to, since
		// that is what comes back out of a column of them.
		{`type: FLOAT32` + "\n" + `value: "0.1"` + "\n", float64(float32(0.1))},
		{"type: STRING\nvalue: plain\n", "plain"},
		{"type: STRING\nvalue: ''\n", ""},
		{`type: BYTES` + "\n" + `value: "00AB00"` + "\n", []byte{0, 0xab, 0}},
		{`type: BYTES` + "\n" + `value: ""` + "\n", []byte{}},
		{`type: NODE` + "\n" + `value: "person#1"` + "\n", NodeAt{Table: "person", Offset: 1}},
		{`type: EDGE` + "\n" + `value: "knows#0->2"` + "\n",
			EdgeAt{Table: "knows", Src: 0, Dst: 2}},
	} {
		got := value(t, c.text)
		if !Same(c.want, got) {
			t.Errorf("%s came to %s, and it should be %s",
				Quote(strings.ReplaceAll(c.text, "\n", " ")), Show(got), Show(c.want))
		}
	}
}

// A STRING is the one type that reads either way, because a string is
// what a plain scalar already is and a case quotes one only when it has
// to. Everything else is written one way and refused the other.
func TestAStringReadsQuotedOrBare(t *testing.T) {
	if got := value(t, "type: STRING\nvalue: 42\n"); got != "42" {
		t.Errorf("a bare STRING came to %s", Show(got))
	}
	if got := value(t, `type: STRING`+"\n"+`value: "42"`+"\n"); got != "42" {
		t.Errorf("a quoted STRING came to %s", Show(got))
	}
}

func TestAnIntegerIsRefusedOutsideTheRangeItsTypeHolds(t *testing.T) {
	for _, c := range []struct{ ty, text string }{
		{"INT8", "128"},
		{"INT8", "-129"},
		{"INT16", "32768"},
		{"INT32", "2147483648"},
		{"UINT8", "256"},
		{"UINT8", "-1"},
		{"UINT16", "65536"},
		{"UINT32", "4294967296"},
	} {
		text := "type: " + c.ty + "\nvalue: " + c.text + "\n"
		want := "line 2: " + Quote(c.text) + " is not a " + c.ty
		if got := declined(t, text); got != want {
			t.Errorf("%s was refused with %s, and it should be %s", text, Quote(got), Quote(want))
		}
	}
	// UINT64 stops at the signed maximum, because the engine's integer is
	// signed and wrapping the top half into a negative would be a case
	// that passes while meaning the opposite of what it says.
	text := `type: UINT64` + "\n" + `value: "9223372036854775808"` + "\n"
	want := `line 2: "9223372036854775808" is not a UINT64`
	if got := declined(t, text); got != want {
		t.Errorf("a UINT64 above the signed maximum was refused with %s", Quote(got))
	}
}

// The rule the whole encoding exists for. A bare INT64 is a number some
// reader in some language rounds, and a bare NODE is a name and two
// numbers no reader has a scalar for, so the two are told apart.
func TestAQuotedTypeWrittenBareIsRefusedAndSaysWhy(t *testing.T) {
	for _, c := range []struct{ text, want string }{
		{
			"type: INT64\nvalue: 1\n",
			"line 2: INT64 is written in quotes, because a bare 1 is a number and some reader of " +
				"this file will round it",
		},
		{
			"type: FLOAT64\nvalue: 1.5\n",
			"line 2: FLOAT64 is written in quotes, because a bare 1.5 is a number and some reader " +
				"of this file will round it",
		},
		{
			"type: NODE\nvalue: person#1\n",
			"line 2: NODE is written in quotes, because person#1 is a name and two numbers and no " +
				"reader has a scalar for that",
		},
		{
			"type: EDGE\nvalue: knows#0->1\n",
			"line 2: EDGE is written in quotes, because knows#0->1 is a name and two numbers and " +
				"no reader has a scalar for that",
		},
	} {
		if got := declined(t, c.text); got != c.want {
			t.Errorf("%s was refused with\n  %s\nand it should be\n  %s",
				Quote(c.text), Quote(got), Quote(c.want))
		}
	}
}

func TestABareTypeWrittenInQuotesIsRefused(t *testing.T) {
	text := `type: INT8` + "\n" + `value: "1"` + "\n"
	want := "line 2: INT8 is written without quotes, so that a reader cannot take it for a string"
	if got := declined(t, text); got != want {
		t.Errorf("a quoted INT8 was refused with %s", Quote(got))
	}
}

func TestAValueSaysWhatIsWrongWithItInTheOrderThatHelps(t *testing.T) {
	for _, c := range []struct{ what, text, want string }{
		{
			"a type nothing knows",
			"type: INTEGER\nvalue: 1\n",
			"line 1: INTEGER is not a type this encoding knows",
		},
		{
			"a type the encoding holds a name for",
			`type: DECIMAL` + "\n" + `value: "1.0"` + "\n",
			"line 1: DECIMAL is a type the encoding reserves and the engine has no value for",
		},
		{
			// The type is the mistake and the missing payload is a
			// consequence of it, so the type is what the message names.
			"a type nothing knows and no payload either",
			"type: INTEGER\n",
			"line 1: INTEGER is not a type this encoding knows",
		},
		{
			"no type at all",
			"value: 1\n",
			"line 1: a value with no `type`",
		},
		{
			"a type that is not a name",
			"type:\n  - INT8\nvalue: 1\n",
			"line 1: a `type` that is not a name",
		},
		{
			"no payload",
			"type: INT8\n",
			"line 1: a INT8 with no `value`",
		},
		{
			"a payload under NULL",
			"type: NULL\nvalue: 1\n",
			"line 1: NULL carries no `value`",
		},
		{
			"a key the encoding has no room for",
			"type: INT8\nvalue: 1\nname: n\n",
			`line 1: a value has no key "name"`,
		},
		{
			"a sequence where a value belongs",
			"- type: INT8\n",
			"line 1: a value is a mapping of `type` and `value`, and this is a sequence",
		},
		{
			"a scalar where a value belongs",
			"just a scalar\n",
			"line 1: a value is a mapping of `type` and `value`, and this is a scalar",
		},
		{
			"a sequence under a scalar type",
			"type: INT8\nvalue:\n  - 1\n",
			"line 3: a INT8 holds one scalar, and this is a sequence",
		},
		{
			"a scalar under LIST",
			"type: LIST\nvalue: 1\n",
			"line 2: a LIST holds a sequence of values, and this is a scalar",
		},
	} {
		if got := declined(t, c.text); got != c.want {
			t.Errorf("%s was refused with\n  %s\nand it should be\n  %s",
				c.what, Quote(got), Quote(c.want))
		}
	}
}

func TestAListHoldsValuesAndTheEmptyOneHasASpelling(t *testing.T) {
	got := value(t, strings.Join([]string{
		"type: LIST",
		"value:",
		"  - type: INT8",
		"    value: 1",
		"  - type: NULL",
		"  - type: STRING",
		"    value: two",
		"",
	}, "\n"))
	want := []any{int64(1), nil, "two"}
	if !Same(want, got) {
		t.Errorf("read %s, and it should be %s", Show(got), Show(want))
	}
	// A "value:" with nothing under it, which is the empty list and a
	// value a case asserts.
	empty := value(t, "type: LIST\nvalue:\n")
	if !Same([]any{}, empty) {
		t.Errorf("the empty list read as %s", Show(empty))
	}
}

// A path alternates and ends at both ends with a node, so a sequence
// that does not is refused where it is written rather than at the
// comparison, which is the difference between a message naming a line
// and a report saying the row differs.
func TestAPathAlternatesNodeAndEdgeOrIsRefused(t *testing.T) {
	oneHop := strings.Join([]string{
		"type: PATH",
		"value:",
		"  - type: NODE",
		`    value: "person#0"`,
		"  - type: EDGE",
		`    value: "knows#0->1"`,
		"  - type: NODE",
		`    value: "person#1"`,
		"",
	}, "\n")
	got := value(t, oneHop)
	walked, ok := got.(Walk)
	if !ok || len(walked.Elements) != 3 {
		t.Fatalf("a one hop path read as %s", Show(got))
	}

	for _, c := range []struct{ what, text, want string }{
		{
			"an even number of values",
			"type: PATH\nvalue:\n  - type: NODE\n    value: \"person#0\"\n" +
				"  - type: EDGE\n    value: \"knows#0->1\"\n",
			"line 3: a PATH is a node, then an edge and a node for each hop, so it holds an odd " +
				"number of values and this holds 2",
		},
		{
			"an edge where the walk starts",
			"type: PATH\nvalue:\n  - type: EDGE\n    value: \"knows#0->1\"\n",
			"line 3: a PATH alternates, so value 1 is an EDGE where it should be a NODE",
		},
		{
			"a node in the hop position",
			"type: PATH\nvalue:\n  - type: NODE\n    value: \"person#0\"\n" +
				"  - type: NODE\n    value: \"person#1\"\n  - type: NODE\n    value: \"person#2\"\n",
			"line 3: a PATH alternates, so value 2 is a NODE where it should be an EDGE",
		},
		{
			"something that is neither",
			"type: PATH\nvalue:\n  - type: INT8\n    value: 1\n",
			"line 3: a PATH alternates, so value 1 is neither a NODE nor an EDGE where it should " +
				"be a NODE",
		},
	} {
		if got := declined(t, c.text); got != c.want {
			t.Errorf("%s was refused with\n  %s\nand it should be\n  %s",
				c.what, Quote(got), Quote(c.want))
		}
	}
	// The empty path is refused too, since zero is an even number and a
	// walk with no nodes in it is not a walk.
	if got := declined(t, "type: PATH\nvalue:\n"); !strings.Contains(got, "odd number") {
		t.Errorf("the empty path was refused with %s", Quote(got))
	}
}

func TestANodeAndAnEdgeAreATableNameAndRowNumbers(t *testing.T) {
	// Split from the right, so a table whose name holds a # still reads.
	if got := value(t, `type: NODE`+"\n"+`value: "od#d#7"`+"\n"); got != (NodeAt{Table: "od#d", Offset: 7}) {
		t.Errorf("a table name holding a # read as %s", Show(got))
	}
	for _, text := range []string{
		"person",      // no offset
		"#1",          // no table
		"person#",     // no digits
		"person#-1",   // a sign, which ParseUint would take
		"person#1_0",  // an underscore, which ParseUint would take too
		"person#a",    // not a number
		"person#1->2", // an edge under a node's type
	} {
		want := "line 2: " + Quote(text) + " is not a NODE"
		if got := declined(t, `type: NODE`+"\n"+`value: `+Quote(text)+"\n"); got != want {
			t.Errorf("NODE %s was refused with %s, and it should be %s",
				Quote(text), Quote(got), Quote(want))
		}
	}
	for _, text := range []string{
		"knows#0",     // one row rather than two
		"knows#0->",   // no second row
		"knows#->1",   // no first row
		"knows#0-1",   // the wrong arrow
		"#0->1",       // no table
		"knows#0->-1", // a sign
	} {
		want := "line 2: " + Quote(text) + " is not a EDGE"
		if got := declined(t, `type: EDGE`+"\n"+`value: `+Quote(text)+"\n"); got != want {
			t.Errorf("EDGE %s was refused with %s, and it should be %s",
				Quote(text), Quote(got), Quote(want))
		}
	}
}

// An integer is written back out and compared, so that a spelling Go's
// ParseInt would take and no other reader would is refused.
func TestAnIntegerIsRefusedWhenItIsSpeltUnusually(t *testing.T) {
	for _, text := range []string{"+1", "01", "1_0", "0x10"} {
		want := "line 2: " + Quote(text) + " is not a INT8"
		if got := declined(t, `type: INT8`+"\n"+`value: `+text+"\n"); got != want {
			t.Errorf("INT8 %s was refused with %s, and it should be %s",
				Quote(text), Quote(got), Quote(want))
		}
	}
	// Space around a bare payload never reaches here, because the reader
	// takes it off along with the space after the colon. This is asserted
	// rather than left implied, since it is the reason the list above has
	// no padded spelling in it.
	if got := value(t, "type: INT8\nvalue:   1  \n"); !Same(int64(1), got) {
		t.Errorf("a padded INT8 read as %s", Show(got))
	}
}

// A float is exact here: `1` is an integer somebody meant to write as
// `1.0`, and `1e400` is `inf` under another name. Go's ParseFloat takes
// three more spellings the other runners do not.
func TestAFloatIsRefusedWhenItIsSpeltUnusually(t *testing.T) {
	for _, text := range []string{"1", "-1", "1e400", "-1e400", "Inf", "infinity", "nan",
		"0x1p-2", "1_0.0", "1.0f", ""} {
		want := "line 2: " + Quote(text) + " is not a FLOAT64"
		if got := declined(t, `type: FLOAT64`+"\n"+`value: `+Quote(text)+"\n"); got != want {
			t.Errorf("FLOAT64 %s was refused with %s, and it should be %s",
				Quote(text), Quote(got), Quote(want))
		}
	}
	for _, text := range []string{"1.0", "-1.5", "1e10", "1E10", "1.5e-3", "NaN", "inf", "-inf"} {
		got := value(t, `type: FLOAT64`+"\n"+`value: `+Quote(text)+"\n")
		if _, ok := got.(float64); !ok {
			t.Errorf("FLOAT64 %s read as %s", Quote(text), Show(got))
		}
	}
}

// Space anywhere in a byte string is dropped, which is what the
// standard's production allows and what lets a long literal be written
// in groups. Half a byte is refused.
func TestAByteStringIsHexitsInEitherCaseAndSpaceIsDropped(t *testing.T) {
	for _, c := range []struct {
		text string
		want []byte
	}{
		{"00AB00", []byte{0, 0xab, 0}},
		{"00ab00", []byte{0, 0xab, 0}},
		{"00 AB 00", []byte{0, 0xab, 0}},
		{"", []byte{}},
		{"FF", []byte{0xff}},
	} {
		got := value(t, `type: BYTES`+"\n"+`value: `+Quote(c.text)+"\n")
		if !Same(c.want, got) {
			t.Errorf("BYTES %s read as %s", Quote(c.text), Show(got))
		}
	}
	for _, text := range []string{"0", "ABC", "GG", "0x41", "00-AB"} {
		want := "line 2: " + Quote(text) + " is not a BYTES"
		if got := declined(t, `type: BYTES`+"\n"+`value: `+Quote(text)+"\n"); got != want {
			t.Errorf("BYTES %s was refused with %s, and it should be %s",
				Quote(text), Quote(got), Quote(want))
		}
	}
	// The empty byte string is a value of its own, and comparing it
	// against a nil slice has to say yes, since both are what a column of
	// no bytes gives back.
	if !Same(value(t, `type: BYTES`+"\n"+`value: ""`+"\n"), []byte(nil)) {
		t.Errorf("the empty byte string is not the same as a nil slice")
	}
}

// Not ==, for one reason: a float. NaN is not equal to itself and a
// case asserting NaN has to pass, and 0.0 equals -0.0 and a case
// asserting -0.0 has to fail on 0.0.
func TestSameIsEqualityExceptWhereEqualityIsWrong(t *testing.T) {
	for _, c := range []struct {
		what      string
		want, got any
		same      bool
	}{
		{"NaN against itself", math.NaN(), math.NaN(), true},
		{"a negative zero against a positive one", math.Copysign(0, -1), 0.0, false},
		{"a positive zero against a negative one", 0.0, math.Copysign(0, -1), false},
		{"two ones", 1.0, 1.0, true},
		{"an integer against a float", int64(1), 1.0, false},
		{"a boolean against an integer", true, int64(1), false},
		{"nothing against nothing", nil, nil, true},
		{"nothing against a value", nil, int64(0), false},
		{"two lists", []any{int64(1), nil}, []any{int64(1), nil}, true},
		{"lists of different lengths", []any{int64(1)}, []any{int64(1), nil}, false},
		{"a list against a scalar", []any{int64(1)}, int64(1), false},
		{"a scalar against a list", int64(1), []any{int64(1)}, false},
		{"nested lists", []any{[]any{1.0}}, []any{[]any{1.0}}, true},
		{"two walks", Walk{[]any{NodeAt{"p", 0}}}, Walk{[]any{NodeAt{"p", 0}}}, true},
		{"a walk against a list", Walk{[]any{NodeAt{"p", 0}}}, []any{NodeAt{"p", 0}}, false},
		{"two records", map[string]any{"a": int64(1)}, map[string]any{"a": int64(1)}, true},
		{"records of different sizes", map[string]any{"a": int64(1)},
			map[string]any{"a": int64(1), "b": nil}, false},
		{"records with different names", map[string]any{"a": int64(1)},
			map[string]any{"b": int64(1)}, false},
		{"a record against a scalar", map[string]any{"a": int64(1)}, int64(1), false},
		{"a scalar against a record", int64(1), map[string]any{"a": int64(1)}, false},
		{"two byte strings", []byte{1, 2}, []byte{1, 2}, true},
		{"byte strings that differ", []byte{1, 2}, []byte{1, 3}, false},
		{"a byte string against a string", []byte{65}, "A", false},
		{"a string against a byte string", "A", []byte{65}, false},
		{"two dates", zu.Date{Days: 1}, zu.Date{Days: 1}, true},
		{"dates that differ", zu.Date{Days: 1}, zu.Date{Days: 2}, false},
		{"a year month against a day time", zu.YearMonth{Months: 0}, time.Duration(0), false},
	} {
		if got := Same(c.want, c.got); got != c.same {
			t.Errorf("%s came to %v, and it should be %v", c.what, got, c.same)
		}
	}
}

// Show is what a failure report prints, in the encoding's own spelling
// so that a line can be pasted back into a case.
func TestShowWritesAValueTheWayACaseWouldSpellIt(t *testing.T) {
	for _, c := range []struct {
		value any
		want  string
	}{
		{nil, "NULL"},
		{true, "BOOL true"},
		{false, "BOOL false"},
		{int64(-7), `INT64 "-7"`},
		{1.5, `FLOAT64 "1.5"`},
		{"a string", `STRING "a string"`},
		{`with "quotes"`, `STRING "with \"quotes\""`},
		{[]byte{0, 0xab}, `BYTES "00AB"`},
		{[]byte{}, `BYTES ""`},
		{zu.Date{Days: 0}, `DATE "1970-01-01"`},
		{zu.LocalTime{Nanos: 0}, `LOCALTIME "00:00:00"`},
		{zu.LocalTime{Nanos: 123456789}, `LOCALTIME "00:00:00.123456789"`},
		{zu.ZonedTime{Nanos: 0, Offset: 0}, `ZONEDTIME "00:00:00Z"`},
		{zu.LocalDateTime{Nanos: 0}, `LOCALDATETIME "1970-01-01T00:00:00"`},
		// The instant is UTC and the offset is what the case wrote, so
		// the clock printed beside it is the instant moved into that
		// zone, which is the wall clock the case reads back.
		{zu.ZonedDateTime{Nanos: 0, Offset: 60}, `ZONEDDATETIME "1970-01-01T01:00:00+01:00"`},
		{zu.YearMonth{Months: 14}, `DURATION "P1Y2M"`},
		{time.Duration(0), `DURATION "PT0S"`},
		{[]any{int64(1), nil}, `LIST [INT64 "1", NULL]`},
		{[]any{}, "LIST []"},
		{Walk{[]any{NodeAt{"person", 0}, EdgeAt{"knows", 0, 1}, NodeAt{"person", 1}}},
			`PATH [NODE "person#0", EDGE "knows#0->1", NODE "person#1"]`},
		{NodeAt{"person", 3}, `NODE "person#3"`},
		{EdgeAt{"knows", 3, 4}, `EDGE "knows#3->4"`},
		{map[string]any{"b": int64(2), "a": nil}, `RECORD {a: NULL, b: INT64 "2"}`},
		{map[string]any{}, "RECORD {}"},
		// A value the runner did not put through Cell prints as itself,
		// under a name that is not a type, so a report carrying one
		// cannot be mistaken for a case that could be pasted back in.
		{zu.Node{Table: 7, Offset: 1}, "(zu.Node) node(7:1)"},
	} {
		if got := Show(c.value); got != c.want {
			t.Errorf("Show(%#v) is %s, and it should be %s", c.value, Quote(got), Quote(c.want))
		}
	}
	// A record's names are sorted, because Go's map order is deliberately
	// not stable and a failure that reorders its own fields between runs
	// is a failure nobody can diff.
	record := map[string]any{"z": nil, "a": nil, "m": nil}
	for i := 0; i < 8; i++ {
		if got := Show(record); got != "RECORD {a: NULL, m: NULL, z: NULL}" {
			t.Fatalf("a record printed as %s on run %d", Quote(got), i)
		}
	}
}

// A float is printed the way Rust's {:?} writes one: the shortest text
// that reads back as the same double, always with a point or an
// exponent, switching to an exponent where Rust switches and writing
// the exponent bare rather than with a sign and a padding zero.
func TestAFloatPrintsTheWayTheReferenceRunnerPrintsIt(t *testing.T) {
	for _, c := range []struct {
		value float64
		want  string
	}{
		{0, "0.0"},
		{math.Copysign(0, -1), "-0.0"},
		{1, "1.0"},
		{-1, "-1.0"},
		{1.5, "1.5"},
		{0.1, "0.1"},
		{1.0 / 3.0, "0.3333333333333333"},
		{100, "100.0"},
		{1e15, "1000000000000000.0"},
		// At ten to the sixteenth the digits go behind an exponent, which
		// is where Rust switches and not where Go's %v does.
		{1e16, "1e16"},
		{1e17, "1e17"},
		{1.5e17, "1.5e17"},
		{0.001, "0.001"},
		{0.0001, "0.0001"},
		// And below a ten thousandth, likewise.
		{0.00001, "1e-5"},
		{1.5e-5, "1.5e-5"},
		{math.MaxFloat64, "1.7976931348623157e308"},
		{math.SmallestNonzeroFloat64, "5e-324"},
		{math.NaN(), "NaN"},
		{math.Inf(1), "inf"},
		{math.Inf(-1), "-inf"},
	} {
		if got := showFloat(c.value); got != c.want {
			t.Errorf("showFloat(%v) is %s, and it should be %s", c.value, Quote(got), Quote(c.want))
		}
	}
}

// Everything a table holds is spelled the same on both sides and comes
// through untouched. A graph value is not: the engine's node and edge
// carry the id of their table where a case writes its name.
func TestCellTurnsTheEnginesGraphValuesIntoTheCorpusSpelling(t *testing.T) {
	named := func(table uint32) string {
		if table == 1 {
			return "person"
		}
		return "knows"
	}
	for _, c := range []struct {
		from any
		want any
	}{
		{zu.Node{Table: 1, Offset: 4}, NodeAt{Table: "person", Offset: 4}},
		{zu.Rel{Table: 2, Src: 0, Dst: 1}, EdgeAt{Table: "knows", Src: 0, Dst: 1}},
		{zu.Path{zu.Node{Table: 1, Offset: 0}}, Walk{[]any{NodeAt{Table: "person", Offset: 0}}}},
		{[]any{zu.Node{Table: 1, Offset: 2}}, []any{NodeAt{Table: "person", Offset: 2}}},
		{zu.Record{"n": zu.Node{Table: 1, Offset: 5}},
			map[string]any{"n": NodeAt{Table: "person", Offset: 5}}},
		// Everything else comes through as it is.
		{int64(1), int64(1)},
		{nil, nil},
		{"text", "text"},
	} {
		if got := Cell(c.from, named); !Same(c.want, got) {
			t.Errorf("Cell(%#v) is %s, and it should be %s", c.from, Show(got), Show(c.want))
		}
	}
	// A list nested inside a list is walked all the way down, since a
	// node can be anywhere a value can.
	deep := Cell([]any{[]any{zu.Node{Table: 1, Offset: 9}}}, named)
	if !Same([]any{[]any{NodeAt{Table: "person", Offset: 9}}}, deep) {
		t.Errorf("a nested list came to %s", Show(deep))
	}
}
