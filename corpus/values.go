package corpus

// The {type, value} encoding a case writes its values in.
//
// Every value in the corpus is a mapping with a "type" naming the GQL
// type and a "value" holding the payload. The type is written down
// rather than inferred because the corpus is read by nine languages and
// inference is where they differ: a bare 1 is an integer in YAML, and
// which integer it becomes is a decision each host language makes on
// its own.
//
// The payload is a YAML scalar where a YAML scalar is exact, and a
// string where it is not. An integer wider than 53 bits is a string,
// because most YAML readers hand a number to a double. A float is a
// string, for that reason and for NaN, inf and -0.0. A temporal value
// is a string, because YAML has no type that keeps an offset.
//
// NODE, EDGE and PATH are the values a graph has and a table does not,
// and they are written as names rather than as the numbers the engine
// holds. A node is "person#1", the table it is a row of and which row of
// it. An edge is "knows#0->1", its table and the two rows it runs
// between. A path is a sequence, like a list, holding a node and then an
// edge and a node for each hop.
//
// Refusing the wrong form is half the point, and refusing it here is
// what makes this a second reader of the corpus rather than a consumer
// of it.
//
// What a decoded value becomes is a Go value, because that is what a
// statement gives back through this client and what a comparison has to
// be against. The declared width is dropped in the process, so a case
// that says INT8 and one that says INT64 both become an int64: that is a
// fact about this engine rather than about the corpus, since its own
// value is one signed 64 bit integer either way.
//
// One thing this client does not need and the Python one does. There,
// a temporal written finer than a microsecond is a value the host
// language cannot hold, and a case carrying one is reported unsupported
// rather than run. Go's day is not a datetime: a date is a count of
// days and everything else is a count of nanoseconds, which is the same
// resolution the engine keeps, so every temporal in the corpus is a
// value this client holds exactly.

import (
	"fmt"
	"math"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"time"

	zu "github.com/tamnd/zu-go"
)

// A NodeAt is a node as a case names it: the node table it is a row of
// and which row.
//
// Not [zu.Node], which carries the table's id rather than its name. The
// id is a number the file decided and every client builds its own file,
// so a case that named one would be asserting something about the
// order the tables went in.
type NodeAt struct {
	// Table is the node table's name.
	Table string
	// Offset is the row's number within that table, counted from zero
	// in the order the load wrote it.
	Offset uint64
}

// An EdgeAt is an edge as a case names it: the rel table it is in and
// the two rows it runs between.
//
// Not [zu.Rel], which carries the table's id and, in the engine, a
// fourth field the corpus does not write. That field is where the
// edge's properties sit, which is its place in the order the table was
// loaded in, and that is a number the loader chose rather than one the
// case did. A pair may run more than once, and a case that has to tell
// two parallel edges apart asserts a property of them instead.
type EdgeAt struct {
	// Table is the rel table's name.
	Table string
	// Src and Dst are the row numbers the edge runs from and to, within
	// the node table the load names.
	Src, Dst uint64
}

// A Walk is a path as a case writes it: nodes and edges alternating, a
// node at each end.
//
// Not [zu.Path] for the reason [EdgeAt] is not [zu.Rel], and for one
// more: what a case compares is the walk, and two walks that cross the
// same edges are the same walk whichever copy of a parallel edge the
// engine happened to hand back.
type Walk struct {
	// Elements is the walk in order, a [NodeAt] at each end and an
	// [EdgeAt] between every two of them.
	Elements []any
}

// quotedForm is whether a type's payload is written as a quoted string.
// False is a type a YAML scalar carries without loss, true is one it
// does not.
var quotedForm = map[string]bool{
	"NULL":    false,
	"BOOL":    false,
	"INT8":    false,
	"INT16":   false,
	"INT32":   false,
	"INT64":   true,
	"UINT8":   false,
	"UINT16":  false,
	"UINT32":  false,
	"UINT64":  true,
	"FLOAT32": true,
	"FLOAT64": true,
	"STRING":  false,
	// A byte string is written in quotes because its hexits are digits
	// as often as not: a bare 0041 is a number with a leading zero in
	// one reader and the string it looks like in another, and neither of
	// them is the two octets the case meant.
	"BYTES":         true,
	"DATE":          true,
	"LOCALTIME":     true,
	"ZONEDTIME":     true,
	"LOCALDATETIME": true,
	"ZONEDDATETIME": true,
	"DURATION":      true,
	"LIST":          false,
	// A node and an edge are written in quotes because what a case
	// spells is a name and two numbers with punctuation between them,
	// which is text in every reader and a number in none.
	"NODE": true,
	"EDGE": true,
	// A path is a sequence, like a list, because that is what it is: the
	// nodes and edges of a walk, in the order they were walked.
	"PATH": false,
}

// reserved are the types the encoding reserves a name for and the
// engine has no runtime value for yet, kept apart from an outright typo
// so that the error says which of the two it is.
var reserved = []string{"DECIMAL"}

// bounds is the range each integer width holds, so that a case writing
// a value its own type cannot carry is refused rather than stored wider
// than it says. UINT64 stops at the signed maximum because the engine's
// integer is signed and 64 bits wide, and wrapping the top half into a
// negative would be a case that passes while meaning the opposite of
// what it says.
var bounds = map[string][2]int64{
	"INT8":   {math.MinInt8, math.MaxInt8},
	"INT16":  {math.MinInt16, math.MaxInt16},
	"INT32":  {math.MinInt32, math.MaxInt32},
	"INT64":  {math.MinInt64, math.MaxInt64},
	"UINT8":  {0, math.MaxUint8},
	"UINT16": {0, math.MaxUint16},
	"UINT32": {0, math.MaxUint32},
	"UINT64": {0, math.MaxInt64},
}

// Form is whether a type is written quoted, and whether it is a type at
// all.
func Form(ty string) (quoted bool, ok bool) {
	quoted, ok = quotedForm[ty]
	return quoted, ok
}

func unknownType(ty string) string {
	for _, held := range reserved {
		if ty == held {
			return ty + " is a type the encoding reserves and the engine has no value for"
		}
	}
	return ty + " is not a type this encoding knows"
}

// Decode is the value a {type, value} mapping describes.
func Decode(node Node) (any, error) {
	if _, ok := node.Map(); !ok {
		return nil, refuse("line %d: a value is a mapping of `type` and `value`, and this is %s",
			node.Line(), node.What())
	}
	if unknown := node.Unknown("type", "value"); len(unknown) > 0 {
		return nil, refuse("line %d: a value has no key %s", node.Line(), Quote(unknown[0]))
	}
	return Typed(node)
}

// Typed is the type and value of a mapping that carries more than those
// two, which is a parameter: it is a value with a name, and the name
// belongs to the case rather than to the encoding.
func Typed(node Node) (any, error) {
	at := node.Line()
	tyNode := node.Get("type")
	if tyNode == nil {
		return nil, refuse("line %d: a value with no `type`", at)
	}
	ty, ok := tyNode.Str()
	if !ok {
		return nil, refuse("line %d: a `type` that is not a name", at)
	}

	// Checked here as well as in Payload, because a value whose type is
	// not a type and which also has no `value` under it should be told
	// about the type first: that is the mistake, and the missing payload
	// is a consequence of it.
	if _, known := Form(ty); !known {
		return nil, refuse("line %d: %s", at, unknownType(ty))
	}

	if ty == "NULL" {
		if node.Get("value") != nil {
			return nil, refuse("line %d: NULL carries no `value`", at)
		}
		return nil, nil
	}
	value := node.Get("value")
	if value == nil {
		return nil, refuse("line %d: a %s with no `value`", at, ty)
	}
	return Payload(ty, *value)
}

// Payload is the value a payload spells under a type that has already
// been read.
//
// A row of a case names its type beside every value. A column of a load
// names it once at the top and every value under it is a bare payload,
// which is the same encoding with the type factored out, so it is the
// same function reading it.
func Payload(ty string, value Node) (any, error) {
	quoted, known := Form(ty)
	if !known {
		return nil, refuse("line %d: %s", value.Line(), unknownType(ty))
	}

	if ty == "LIST" || ty == "PATH" {
		// The empty list is a value worth a case and needs a spelling,
		// which is a "value:" with nothing under it.
		items, ok := value.SeqOrEmpty()
		if !ok {
			return nil, refuse("line %d: a %s holds a sequence of values, and this is %s",
				value.Line(), ty, value.What())
		}
		decoded := make([]any, 0, len(items))
		for _, item := range items {
			one, err := Decode(item)
			if err != nil {
				return nil, err
			}
			decoded = append(decoded, one)
		}
		if ty == "LIST" {
			return decoded, nil
		}
		return walk(decoded, value.Line())
	}

	text, wasQuoted, ok := value.Scalar()
	if !ok {
		return nil, refuse("line %d: a %s holds one scalar, and this is %s",
			value.Line(), ty, value.What())
	}
	at := value.Line()
	// The one rule the whole encoding exists for, checked before the
	// text is looked at, because a value that parses is exactly the case
	// where a silent misread would survive review.
	if quoted && !wasQuoted {
		// A node and an edge are quoted for a different reason from the
		// numbers, so they are told a different reason. Both reasons are
		// the same rule: a payload is quoted where a bare one would read
		// as something else in some reader of this file.
		if ty == "NODE" || ty == "EDGE" {
			return nil, refuse("line %d: %s is written in quotes, because %s is a name and two "+
				"numbers and no reader has a scalar for that", at, ty, text)
		}
		return nil, refuse("line %d: %s is written in quotes, because a bare %s is a number and "+
			"some reader of this file will round it", at, ty, text)
	}
	if !quoted && wasQuoted && ty != "STRING" {
		return nil, refuse("line %d: %s is written without quotes, so that a reader cannot take it "+
			"for a string", at, ty)
	}

	var out any
	var ok2 bool
	switch ty {
	case "NODE":
		out, ok2 = nodeAt(text)
	case "EDGE":
		out, ok2 = edgeAt(text)
	default:
		out, ok2 = scalar(ty, text)
	}
	if !ok2 {
		return nil, refuse("line %d: %s is not a %s", at, Quote(text), ty)
	}
	return out, nil
}

// walk is the nodes and edges of a walk, or what is wrong with the
// sequence somebody wrote.
//
// A path alternates and ends at both ends with a node, so a sequence
// that does not is a case that could never pass. Refusing it here rather
// than at the comparison is the difference between a message naming the
// line and a report saying the row differs.
func walk(items []any, at int) (any, error) {
	if len(items)%2 == 0 {
		return nil, refuse("line %d: a PATH is a node, then an edge and a node for each hop, so it "+
			"holds an odd number of values and this holds %d", at, len(items))
	}
	for i, item := range items {
		wantNode := i%2 == 0
		var ok bool
		var was string
		switch item.(type) {
		case NodeAt:
			ok, was = wantNode, "a NODE"
		case EdgeAt:
			ok, was = !wantNode, "an EDGE"
		default:
			ok, was = false, "neither a NODE nor an EDGE"
		}
		if !ok {
			wanted := "an EDGE"
			if wantNode {
				wanted = "a NODE"
			}
			return nil, refuse("line %d: a PATH alternates, so value %d is %s where it should be %s",
				at, i+1, was, wanted)
		}
	}
	return Walk{Elements: items}, nil
}

// nodeAt is a node, written as its table and the offset of its row:
// person#1.
//
// The table's name rather than its id, because the id is a number the
// file decided and every client builds its own file. Split from the
// right, so that a table whose name holds a "#" is still readable.
func nodeAt(text string) (any, bool) {
	hash := strings.LastIndexByte(text, '#')
	if hash <= 0 {
		return nil, false
	}
	if !digits(text[hash+1:]) {
		return nil, false
	}
	offset, err := strconv.ParseUint(text[hash+1:], 10, 64)
	if err != nil {
		return nil, false
	}
	return NodeAt{Table: text[:hash], Offset: offset}, true
}

// edgeAt is an edge, written as its table and the rows it runs between:
// knows#0->1.
func edgeAt(text string) (any, bool) {
	hash := strings.LastIndexByte(text, '#')
	if hash <= 0 {
		return nil, false
	}
	src, dst, found := strings.Cut(text[hash+1:], "->")
	if !found || !digits(src) || !digits(dst) {
		return nil, false
	}
	from, err := strconv.ParseUint(src, 10, 64)
	if err != nil {
		return nil, false
	}
	to, err := strconv.ParseUint(dst, 10, 64)
	if err != nil {
		return nil, false
	}
	return EdgeAt{Table: text[:hash], Src: from, Dst: to}, true
}

// digits is whether text is one or more ASCII digits and nothing else,
// which is what a row number in a node or an edge is. Go's ParseUint
// takes a leading sign and an underscore between digits, and neither is
// a row number anybody meant to write.
func digits(text string) bool {
	if text == "" {
		return false
	}
	for i := 0; i < len(text); i++ {
		if text[i] < '0' || text[i] > '9' {
			return false
		}
	}
	return true
}

// Cell is the corpus's own shape for a value that came back from a
// statement.
//
// Everything a table holds is spelled the same on both sides and comes
// through untouched. A graph value is not: the engine's node and edge
// carry the id of their table where a case writes its name, so they are
// put into the shapes above before anything is compared, which is what
// the Rust runner's from_engine does for the same reason.
//
// A table with no name is spelled "#7" after its id, which is what a
// node column of an Arrow export is named when there is no catalog to
// ask. It should not happen here, since the connection is right there,
// and it is a spelling rather than a panic because a report saying "the
// case wants person#1 and this is #7" is more use to whoever has to fix
// it than one that died.
func Cell(value any, named func(uint32) string) any {
	switch v := value.(type) {
	case zu.Node:
		return NodeAt{Table: named(v.Table), Offset: v.Offset}
	case zu.Rel:
		return EdgeAt{Table: named(v.Table), Src: v.Src, Dst: v.Dst}
	case zu.Path:
		return Walk{Elements: cells(v, named)}
	case []any:
		return cells(v, named)
	case zu.Record:
		out := make(map[string]any, len(v))
		for name, item := range v {
			out[name] = Cell(item, named)
		}
		return out
	default:
		return value
	}
}

func cells(items []any, named func(uint32) string) []any {
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = Cell(item, named)
	}
	return out
}

// scalar is the value a type's text spells, and whether it spells one
// at all. The second result cannot be folded into the first, because
// nil is the value NULL spells.
func scalar(ty, text string) (any, bool) {
	switch ty {
	case "BOOL":
		switch text {
		case "true":
			return true, true
		case "false":
			return false, true
		}
		return nil, false
	case "STRING":
		return text, true
	case "BYTES":
		return fromHexits(text)
	case "FLOAT32", "FLOAT64":
		f, ok := parseFloat(text)
		if !ok {
			return nil, false
		}
		if ty == "FLOAT32" {
			return float64(float32(f)), true
		}
		return f, true
	case "DATE":
		return parseDate(text)
	case "LOCALTIME":
		return parseLocalTime(text)
	case "ZONEDTIME":
		return parseZonedTime(text)
	case "LOCALDATETIME":
		return parseLocalDateTime(text)
	case "ZONEDDATETIME":
		return parseZonedDateTime(text)
	case "DURATION":
		return parseDuration(text)
	}
	if low, ok := bounds[ty]; ok {
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return nil, false
		}
		// Written back out and compared, so that a leading plus, a
		// leading zero and an underscore between digits are all refused
		// rather than read as the number they resemble.
		if strconv.FormatInt(n, 10) != text {
			return nil, false
		}
		if n < low[0] || n > low[1] {
			return nil, false
		}
		return n, true
	}
	return nil, false
}

// parseFloat is a float, including the three spellings YAML has no
// opinion about. They are spelled the way Rust prints them, because
// that is what the reference runner writes into a failure report and
// what a case is pasted from.
func parseFloat(text string) (float64, bool) {
	switch text {
	case "NaN":
		return math.NaN(), true
	case "inf":
		return math.Inf(1), true
	case "-inf":
		return math.Inf(-1), true
	}
	// A float is exact here, so `1` is not a FLOAT64 and neither is
	// `1e400`. The first is an integer somebody meant to write as `1.0`
	// and the second is `inf` under another name.
	if !strings.ContainsAny(text, ".eE") {
		return 0, false
	}
	// Go's ParseFloat takes "Inf", "0x1p-2" and an underscore between
	// digits, none of which the corpus writes and all of which would be
	// a case that reads differently in the other three runners.
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c >= '0' && c <= '9' {
			continue
		}
		if strings.IndexByte(".eE+-", c) < 0 {
			return 0, false
		}
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return 0, false
	}
	return f, true
}

// Same is whether two values are the same value.
//
// Not ==, for one reason: a float. NaN is not equal to itself and a
// case asserting NaN has to pass, and 0.0 equals -0.0 and a case
// asserting -0.0 has to fail on 0.0, because the sign of zero is
// exactly the sort of thing that survives one binding and not another.
//
// Everything else is Go's own equality on the dynamic type, which
// already says no to a boolean where an integer was wanted and to a
// [zu.YearMonth] where a [time.Duration] was. The three shapes that
// hold other values are compared by walking them, since a slice and a
// map are not values == will take.
func Same(want, got any) bool {
	switch w := want.(type) {
	case float64:
		g, ok := got.(float64)
		if !ok {
			return false
		}
		if math.IsNaN(w) && math.IsNaN(g) {
			return true
		}
		return math.Signbit(w) == math.Signbit(g) && w == g
	case []any:
		g, ok := got.([]any)
		return ok && sameAll(w, g)
	case Walk:
		g, ok := got.(Walk)
		return ok && sameAll(w.Elements, g.Elements)
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok || len(w) != len(g) {
			return false
		}
		for name, value := range w {
			other, held := g[name]
			if !held || !Same(value, other) {
				return false
			}
		}
		return true
	case []byte:
		g, ok := got.([]byte)
		return ok && string(w) == string(g)
	case zu.Decimal:
		// The scale as well as the number. Two decimals of one number
		// at two scales are one value to the engine and print
		// differently, and what a case asserts is what a reader would
		// see. Compared through the unscaled integers rather than with
		// ==, since the struct holds a pointer and two of those are
		// never the same one.
		g, ok := got.(zu.Decimal)
		if !ok || w.Scale != g.Scale {
			return false
		}
		return unscaled(w).Cmp(unscaled(g)) == 0
	}
	if _, ok := got.([]any); ok {
		return false
	}
	if _, ok := got.([]byte); ok {
		return false
	}
	if _, ok := got.(map[string]any); ok {
		return false
	}
	return want == got
}

// unscaled is a decimal's digits as an integer, with the nil a zero
// value carries read as zero rather than dereferenced.
func unscaled(d zu.Decimal) *big.Int {
	if d.Unscaled == nil {
		return new(big.Int)
	}
	return d.Unscaled
}

func sameAll(want, got []any) bool {
	if len(want) != len(got) {
		return false
	}
	for i := range want {
		if !Same(want[i], got[i]) {
			return false
		}
	}
	return true
}

// Show is how a value reads in a failure report, in the encoding's own
// spelling so that it can be pasted into a case, and line for line what
// the Rust runner prints so that two reports can be diffed.
func Show(value any) string {
	switch v := value.(type) {
	case nil:
		return "NULL"
	case bool:
		if v {
			return "BOOL true"
		}
		return "BOOL false"
	case int64:
		return `INT64 "` + strconv.FormatInt(v, 10) + `"`
	case float64:
		return `FLOAT64 "` + showFloat(v) + `"`
	case string:
		return "STRING " + Quote(v)
	case []byte:
		return `BYTES "` + hexits(v) + `"`
	case zu.Decimal:
		// A decimal is a value a statement can hand back today even
		// though DECIMAL is still a reserved name a case may not write,
		// since CAST reaches one and no case declares one. That makes
		// this the got side of a report and never the want side, and a
		// report that could not print what it got would be the least
		// useful moment to find out.
		return `DECIMAL "` + v.String() + `"`
	case zu.Date:
		return `DATE "` + showDate(v) + `"`
	case zu.LocalTime:
		return `LOCALTIME "` + showClock(v.Nanos) + `"`
	case zu.ZonedTime:
		return `ZONEDTIME "` + showClock(v.Nanos) + showOffset(v.Offset) + `"`
	case zu.LocalDateTime:
		return `LOCALDATETIME "` + showStamp(v.Nanos) + `"`
	case zu.ZonedDateTime:
		return `ZONEDDATETIME "` + showStamp(v.Nanos+int64(v.Offset)*int64(time.Minute)) +
			showOffset(v.Offset) + `"`
	case zu.YearMonth:
		return `DURATION "` + showMonths(v.Months) + `"`
	case time.Duration:
		return `DURATION "` + showNanos(int64(v)) + `"`
	case []any:
		return "LIST [" + showAll(v) + "]"
	case Walk:
		return "PATH [" + showAll(v.Elements) + "]"
	case NodeAt:
		return `NODE "` + v.Table + "#" + strconv.FormatUint(v.Offset, 10) + `"`
	case EdgeAt:
		return `EDGE "` + v.Table + "#" + strconv.FormatUint(v.Src, 10) + "->" +
			strconv.FormatUint(v.Dst, 10) + `"`
	case map[string]any:
		fields := make([]string, 0, len(v))
		for _, name := range sortedKeys(v) {
			fields = append(fields, name+": "+Show(v[name]))
		}
		return "RECORD {" + strings.Join(fields, ", ") + "}"
	}
	// A [zu.Node], a [zu.Rel] or a [zu.Path] reaching here is a value
	// the runner did not put through [Cell], and a [zu.Graph] or a
	// [zu.BindingTable] is one no case can write. Either way it prints
	// as itself, under a name that is not a type, so that a report
	// carrying one cannot be mistaken for a case that could be pasted
	// back into the corpus.
	return fmt.Sprintf("(%T) %v", value, value)
}

// sortedKeys is the names of a record, in order, so that a report of
// one reads the same twice. Go's map order is deliberately not stable
// and a failure that reorders its own fields between runs is a failure
// nobody can diff.
func sortedKeys(fields map[string]any) []string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func showAll(items []any) string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = Show(item)
	}
	return strings.Join(out, ", ")
}

// showFloat is a float the way Rust's {:?} writes one, which is the
// shortest text that reads back as the same double and always carries a
// point or an exponent.
//
// Written out rather than taken from strconv, which switches to an
// exponent at a different place and writes the exponent with a sign and
// a padding zero. Both of those are a report that differs from the
// reference one without the answer differing, which is the thing this
// whole file exists to avoid.
func showFloat(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	}
	sign, g := "", f
	if math.Signbit(f) {
		sign, g = "-", -f
	}
	// The shortest round trip digits, and where the point goes in them.
	// Taken off the exponent form because that one always writes the
	// digits and the exponent apart, whatever the size of the number.
	e := strconv.FormatFloat(g, 'e', -1, 64)
	at := strings.IndexByte(e, 'e')
	run := strings.Replace(e[:at], ".", "", 1)
	exp, _ := strconv.Atoi(e[at+1:])
	point := exp + 1
	switch {
	case point <= -4 || point > 16:
		out := run[:1]
		if len(run) > 1 {
			out += "." + run[1:]
		}
		return sign + out + "e" + strconv.Itoa(exp)
	case point <= 0:
		return sign + "0." + strings.Repeat("0", -point) + run
	case point >= len(run):
		return sign + run + strings.Repeat("0", point-len(run)) + ".0"
	default:
		return sign + run[:point] + "." + run[point:]
	}
}

// hexits is a byte string the way the engine writes one: two hexits to
// a byte, upper case, no quotes and no X.
//
// Upper case because the standard writes the literal that way, and a
// reader comparing two of these is comparing text, so one case is one
// answer.
func hexits(raw []byte) string {
	const to = "0123456789ABCDEF"
	out := make([]byte, 0, len(raw)*2)
	for _, b := range raw {
		out = append(out, to[b>>4], to[b&0xf])
	}
	return string(out)
}

// fromHexits is the bytes a run of hexits names, and nothing for
// anything that is not a run of hexits or that names half a byte.
//
// Space is allowed anywhere and dropped, which is what the standard's
// production allows and what lets a long literal be written in groups.
// Either case reads, because a value that went in as 00ab and came back
// as 00AB is the same value.
func fromHexits(text string) (any, bool) {
	nibbles := make([]byte, 0, len(text))
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f':
			continue
		case c >= '0' && c <= '9':
			nibbles = append(nibbles, c-'0')
		case c >= 'a' && c <= 'f':
			nibbles = append(nibbles, c-'a'+10)
		case c >= 'A' && c <= 'F':
			nibbles = append(nibbles, c-'A'+10)
		default:
			return nil, false
		}
	}
	if len(nibbles)%2 != 0 {
		return nil, false
	}
	// Not nil, because the empty byte string is a value of its own and a
	// case asserts it: a nil slice and an empty one are the same []byte
	// to every comparison here, but the value that comes back from the
	// engine for X'' is an empty one and the two should read alike.
	out := make([]byte, 0, len(nibbles)/2)
	for i := 0; i < len(nibbles); i += 2 {
		out = append(out, nibbles[i]<<4|nibbles[i+1])
	}
	return out, true
}
