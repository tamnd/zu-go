package corpus

// What a case is, and how a file of them is read.
//
// A case is a statement and what running it must produce. That is
// deliberately the whole of it. Every client in every language can run a
// statement and look at the rows that come back, so a corpus written in
// those terms is one every client can run, and a corpus written in terms
// of a client's own API would be nine corpora.
//
// The expectation is either rows or a condition. A case expecting a
// condition names the GQLSTATUS code, not the message, because the code
// is the contract and the message is prose that will improve.
//
// A statement may take parameters, which is the other direction the same
// values travel: a case with params: writes a value in the encoding,
// hands it to this client's own binding call, and asserts what came
// back. A client that decodes a date correctly and encodes it a day
// early passes every case that has no parameters in it.
//
// A case may say which connection each of its statements runs on, which
// is how a case about a transaction is written: a transaction is only
// observable from outside it, so a case that has to say what a commit
// means needs a second connection to say it to. A case that says nothing
// runs everything on one connection called main, which is every case but
// a handful.

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Schema is the schema version a file declares. It exists so that a
// corpus unpacked from an old release tells a new runner what it is
// instead of failing in the middle.
const Schema = 4

// Main is the connection a statement runs on when the case does not name
// one.
const Main = "main"

var (
	suiteKeys = []string{"schema", "suite", "doc", "load", "cases"}
	caseKeys  = []string{"name", "doc", "setup", "on", "params", "query", "columns", "rows",
		"raises", "arrow"}
	loadKeys = []string{"nodes", "edges", "count", "columns", "pairs"}
)

// A Step is a statement run before the one under test, and the
// connection it runs on.
type Step struct {
	// On is the connection this statement runs on, which is [Main]
	// unless the step names another.
	On string
	// Query is the statement, written the way the case wrote it.
	Query string
}

// A Param is one parameter a case binds: the name a statement writes
// after the $, and the value.
type Param struct {
	// Name is the name without the $, since that is what binding it
	// takes.
	Name string
	// Value is the value, read the way every other value in the corpus
	// is read.
	Value any
}

// A Case is one statement and what it owes.
//
// Columns and Rows are set together or neither is, and Raises is set
// when neither is: a case says what it produces one way or the other.
// Columns being set and empty is a case of its own, since FINISH is a
// statement that answers no columns at all.
type Case struct {
	// Name is the case's name within its suite, which with the suite
	// name is what a report is diffed on.
	Name string
	// Doc is why the case is here, which is the part a reader of the
	// corpus needs and no runner does.
	Doc string
	// Query is the statement under test.
	Query string
	// Line is where the case starts in its file, so that a failure
	// names somewhere to look.
	Line int
	// Setup is the statements run before the one under test, in order.
	Setup []Step
	// On is the connection the statement under test runs on, which is
	// [Main] unless the case says otherwise.
	On string
	// Params is what the statement binds, in the order the case wrote
	// them.
	Params []Param
	// HasColumns is whether the case said `columns:` at all, which is
	// what tells a case expecting no columns from one expecting a
	// condition.
	HasColumns bool
	// Columns is the column names the statement answers, in order.
	Columns []string
	// Rows is the rows it answers, one value per column.
	Rows [][]any
	// Raises is the GQLSTATUS the statement owes instead of an answer,
	// and the empty string for a case that answers.
	Raises string
	// Arrow is what the same result looks like on the way out through
	// Arrow, for a case that says, and nil for one that does not. Most
	// do not: the export gives one answer per column type and a handful
	// of cases pin every one of them, so the rest would be repeating a
	// type the corpus already covers.
	Arrow *Export
}

// A Column is one column of a load: a name, the type every value in it
// has, and the values in row order.
type Column struct {
	// Name is the property name the column is loaded under.
	Name string
	// Type is the type every value in the column has, named once here
	// rather than beside each value.
	Type string
	// Values is the column's values in row order, one per row the load
	// declares.
	Values []any
}

// A Load is one node table, its columns, and the edges between its rows.
//
// Everything else in the corpus is an expression, and an expression says
// what a value means on the way out and nothing about how it got in. A
// load is the other half, and every runner puts it in through its own
// bulk load path, which for this client is [zu.NewLoader].
type Load struct {
	// Nodes is the name of the node table the rows go into.
	Nodes string
	// Edges is the name of the edge table the pairs go into.
	Edges string
	// Count is how many rows the load has, which every column is
	// checked against so that a short column is a refusal here rather
	// than a puzzle later.
	Count int
	// Columns is the node table's columns, in the order they are
	// written and loaded.
	Columns []Column
	// Pairs is the edges, each a from and a to row number within the
	// node table.
	Pairs [][2]int
}

// A Suite is one file of cases.
type Suite struct {
	// Name is the suite's name, which is also its file's name without
	// the extension.
	Name string
	// Doc is what the suite is about.
	Doc string
	// Load is the data every case in the suite runs against, or nil for
	// a suite whose cases need none.
	Load *Load
	// Cases is the cases, in the order the file writes them.
	Cases []Case
}

// ReadDir is every suite in a directory, in the order a sorted listing
// gives, which is the order the reference runner walks them in.
func ReadDir(directory string) ([]Suite, error) {
	paths, err := filepath.Glob(filepath.Join(directory, "*.yaml"))
	if err != nil {
		return nil, refuse("%s: %v", directory, err)
	}
	// Sorted, because Glob's order is the filesystem's and a report that
	// is diffed against another runner's has to walk them the same way.
	slices.Sort(paths)
	var suites []Suite
	for _, path := range paths {
		text, err := os.ReadFile(path)
		if err != nil {
			return nil, refuse("%s: %v", path, err)
		}
		suite, err := Read(string(text))
		if err != nil {
			return nil, refuse("%s: %s", path, err)
		}
		stem := strings.TrimSuffix(filepath.Base(path), ".yaml")
		if suite.Name != stem {
			return nil, refuse("%s: the suite calls itself %s and the file calls it %s",
				path, Quote(suite.Name), Quote(stem))
		}
		suites = append(suites, suite)
	}
	if len(suites) == 0 {
		return nil, refuse("%s: no case files", directory)
	}
	return suites, nil
}

// Read is a suite, or the first thing in the file that is not one.
func Read(text string) (Suite, error) {
	doc, err := Parse(text)
	if err != nil {
		return Suite{}, err
	}
	if unknown := doc.Unknown(suiteKeys...); len(unknown) > 0 {
		return Suite{}, refuse("line %d: a suite has no key %s", doc.Line(), Quote(unknown[0]))
	}
	schemaNode := doc.Get("schema")
	if schemaNode == nil {
		return Suite{}, refuse("the file does not open with `schema:`")
	}
	schema, ok := schemaNode.Str()
	if !ok {
		return Suite{}, refuse("the file does not open with `schema:`")
	}
	version, err := strconv.Atoi(schema)
	if err != nil {
		return Suite{}, refuse("%s is not a schema version", Quote(schema))
	}
	if version != Schema {
		return Suite{}, refuse("this is schema %d and the runner reads schema %d", version, Schema)
	}

	name, err := field(doc, "suite")
	if err != nil {
		return Suite{}, err
	}
	docText, err := field(doc, "doc")
	if err != nil {
		return Suite{}, err
	}
	var load *Load
	if node := doc.Get("load"); node != nil {
		load, err = readLoad(*node)
		if err != nil {
			return Suite{}, err
		}
	}

	casesNode := doc.Get("cases")
	if casesNode == nil {
		return Suite{}, refuse("a suite with no `cases:`")
	}
	items, ok := casesNode.Seq()
	if !ok {
		return Suite{}, refuse("`cases:` is a sequence")
	}
	if len(items) == 0 {
		return Suite{}, refuse("a suite with no cases in it")
	}
	cases := make([]Case, 0, len(items))
	// Names are what a report cites and what a binding's skip list
	// names, so two cases sharing one is a report that says less than it
	// looks like it does.
	seen := map[string]bool{}
	for _, item := range items {
		one, err := readCase(item)
		if err != nil {
			return Suite{}, err
		}
		if seen[one.Name] {
			return Suite{}, refuse("two cases are called %s", Quote(one.Name))
		}
		seen[one.Name] = true
		cases = append(cases, one)
	}
	return Suite{Name: name, Doc: docText, Load: load, Cases: cases}, nil
}

func field(node Node, key string) (string, error) {
	value := node.Get(key)
	if value == nil {
		return "", refuse("line %d: no `%s:`", node.Line(), key)
	}
	text, ok := value.Str()
	if !ok {
		return "", refuse("line %d: `%s:` is one line of text", node.Line(), key)
	}
	return text, nil
}

func readCase(node Node) (Case, error) {
	at := node.Line()
	if _, ok := node.Map(); !ok {
		return Case{}, refuse("line %d: a case is a mapping, and this is %s", at, node.What())
	}
	if unknown := node.Unknown(caseKeys...); len(unknown) > 0 {
		return Case{}, refuse("line %d: a case has no key %s", at, Quote(unknown[0]))
	}

	name, err := field(node, "name")
	if err != nil {
		return Case{}, err
	}
	if !dashedWords(name) {
		return Case{}, refuse("line %d: %s is a case name, which is lower case words joined by "+
			"dashes", at, Quote(name))
	}
	doc, err := field(node, "doc")
	if err != nil {
		return Case{}, err
	}
	query, err := field(node, "query")
	if err != nil {
		return Case{}, err
	}

	var setup []Step
	if setupNode := node.Get("setup"); setupNode != nil {
		items, ok := setupNode.Seq()
		if !ok {
			return Case{}, refuse("line %d: `setup:` is a sequence of statements", at)
		}
		for _, item := range items {
			step, err := readStep(item)
			if err != nil {
				return Case{}, err
			}
			setup = append(setup, step)
		}
	}

	on := Main
	if onNode := node.Get("on"); onNode != nil {
		on, err = connectionName(*onNode)
		if err != nil {
			return Case{}, err
		}
	}

	params, err := readParams(node)
	if err != nil {
		return Case{}, err
	}

	var export *Export
	if arrowNode := node.Get("arrow"); arrowNode != nil {
		export, err = ParseExport(*arrowNode)
		if err != nil {
			return Case{}, err
		}
	}

	one := Case{Name: name, Doc: doc, Query: query, Line: at, Setup: setup, On: on,
		Params: params, Arrow: export}

	raisesNode := node.Get("raises")
	columnsNode := node.Get("columns")
	if raisesNode != nil && columnsNode != nil {
		return Case{}, refuse("line %d: a case that raises has no rows, and one that returns rows "+
			"does not raise", at)
	}
	if raisesNode != nil {
		code, ok := raisesNode.Str()
		if !ok {
			return Case{}, refuse("line %d: `raises:` is a GQLSTATUS code", at)
		}
		if !gqlstatusShaped(code) {
			return Case{}, refuse("line %d: %s is not the shape of a GQLSTATUS, which is five "+
				"characters of digits and capitals", raisesNode.Line(), Quote(code))
		}
		one.Raises = code
		return one, nil
	}
	if columnsNode == nil {
		return Case{}, refuse("line %d: a case says what it produces, with `columns:` and `rows:` "+
			"or with `raises:`", at)
	}
	// Empty counts, because FINISH is a query that answers no columns at
	// all, which is not the same as a query whose columns held no rows,
	// and the corpus writes it as a `columns:` with nothing under it.
	names, ok := columnsNode.SeqOrEmpty()
	if !ok {
		return Case{}, refuse("line %d: `columns:` is a sequence of names", at)
	}
	columns := make([]string, 0, len(names))
	for _, item := range names {
		text, ok := item.Str()
		if !ok {
			return Case{}, refuse("line %d: a column name is one word", item.Line())
		}
		columns = append(columns, text)
	}
	rows, err := readRows(node)
	if err != nil {
		return Case{}, err
	}
	for _, row := range rows {
		if len(row) != len(columns) {
			return Case{}, refuse("line %d: a row of %d against %d columns",
				at, len(row), len(columns))
		}
	}
	one.HasColumns = true
	one.Columns = columns
	one.Rows = rows
	return one, nil
}

// readStep is one setup statement, which is a line of its own or a line
// and the connection it runs on.
func readStep(node Node) (Step, error) {
	if text, ok := node.Str(); ok {
		return Step{On: Main, Query: text}, nil
	}
	if _, ok := node.Map(); !ok {
		return Step{}, refuse("line %d: a setup statement is one line, or `on:` and `query:`, and "+
			"this is %s", node.Line(), node.What())
	}
	if unknown := node.Unknown("on", "query"); len(unknown) > 0 {
		return Step{}, refuse("line %d: a setup statement has no key %s",
			node.Line(), Quote(unknown[0]))
	}
	onNode := node.Get("on")
	if onNode == nil {
		return Step{}, refuse("line %d: a setup statement written as a mapping names the "+
			"connection it runs on", node.Line())
	}
	on, err := connectionName(*onNode)
	if err != nil {
		return Step{}, err
	}
	query, err := field(node, "query")
	if err != nil {
		return Step{}, err
	}
	return Step{On: on, Query: query}, nil
}

// connectionName is the name of a connection, spelled the way a case
// name is, because a report cites it and a name a reader has to guess at
// is a report that says less than it looks like it does.
func connectionName(node Node) (string, error) {
	name, ok := node.Str()
	if !ok {
		return "", refuse("line %d: `on:` is the name of a connection", node.Line())
	}
	if !dashedWords(name) {
		return "", refuse("line %d: %s is a connection name, which is lower case words joined by "+
			"dashes", node.Line(), Quote(name))
	}
	return name, nil
}

// readParams is the parameters a case binds, which is the value encoding
// with a name beside it.
//
// A name is what the statement spells after the $, so it is checked
// against what a statement may spell: a case whose name is "n one" is
// one no client can bind.
func readParams(node Node) ([]Param, error) {
	paramsNode := node.Get("params")
	if paramsNode == nil {
		return nil, nil
	}
	items, ok := paramsNode.Seq()
	if !ok {
		return nil, refuse("line %d: `params:` is a sequence", paramsNode.Line())
	}
	out := make([]Param, 0, len(items))
	for _, item := range items {
		at := item.Line()
		if _, ok := item.Map(); !ok {
			return nil, refuse("line %d: a parameter is a mapping of `name`, `type` and `value`, "+
				"and this is %s", at, item.What())
		}
		if unknown := item.Unknown("name", "type", "value"); len(unknown) > 0 {
			return nil, refuse("line %d: a parameter has no key %s", at, Quote(unknown[0]))
		}
		name, err := field(item, "name")
		if err != nil {
			return nil, err
		}
		if !wordOrUnderscore(name) {
			return nil, refuse("line %d: %s is a parameter name, which is what a statement writes "+
				"after the `$`", at, Quote(name))
		}
		for _, held := range out {
			if held.Name == name {
				return nil, refuse("line %d: two parameters are called %s", at, Quote(name))
			}
		}
		value, err := Typed(item)
		if err != nil {
			return nil, err
		}
		out = append(out, Param{Name: name, Value: value})
	}
	return out, nil
}

func readRows(node Node) ([][]any, error) {
	rowsNode := node.Get("rows")
	if rowsNode == nil {
		// A statement that returns no rows is a case worth having, and
		// writing it as an absent `rows:` would make it the same shape
		// as one somebody forgot to finish.
		return nil, refuse("line %d: `columns:` with no `rows:`. A case expecting nothing back "+
			"writes `rows:` with an empty sequence under it.", node.Line())
	}
	items, ok := rowsNode.SeqOrEmpty()
	if !ok {
		return nil, refuse("line %d: `rows:` is a sequence of rows", rowsNode.Line())
	}
	out := make([][]any, 0, len(items))
	for _, item := range items {
		if unknown := item.Unknown("values"); len(unknown) > 0 {
			return nil, refuse("line %d: a row has no key %s", item.Line(), Quote(unknown[0]))
		}
		cellsNode := item.Get("values")
		if cellsNode == nil {
			return nil, refuse("line %d: a row is a `values:` and the values under it", item.Line())
		}
		cells, ok := cellsNode.SeqOrEmpty()
		if !ok {
			return nil, refuse("line %d: `values:` is a sequence of values", cellsNode.Line())
		}
		row := make([]any, 0, len(cells))
		for _, cell := range cells {
			value, err := Decode(cell)
			if err != nil {
				return nil, err
			}
			row = append(row, value)
		}
		out = append(out, row)
	}
	return out, nil
}

func readLoad(node Node) (*Load, error) {
	at := node.Line()
	if _, ok := node.Map(); !ok {
		return nil, refuse("line %d: a load is a mapping, and this is %s", at, node.What())
	}
	if unknown := node.Unknown(loadKeys...); len(unknown) > 0 {
		return nil, refuse("line %d: a load has no key %s", at, Quote(unknown[0]))
	}
	nodes, err := tableName(node, "nodes")
	if err != nil {
		return nil, err
	}
	edges, err := tableName(node, "edges")
	if err != nil {
		return nil, err
	}
	countNode := node.Get("count")
	if countNode == nil {
		return nil, refuse("line %d: a load says how many rows it has, with `count:`", at)
	}
	countText, ok := countNode.Str()
	if !ok {
		return nil, refuse("line %d: a load says how many rows it has, with `count:`", at)
	}
	count, err := strconv.Atoi(countText)
	if err != nil {
		return nil, refuse("line %d: `count:` is a number of rows", at)
	}
	if count == 0 {
		return nil, refuse("line %d: a load of no rows is a load nothing can be read back from", at)
	}

	columnsNode := node.Get("columns")
	if columnsNode == nil {
		return nil, refuse("line %d: a load has `columns:`", at)
	}
	items, ok := columnsNode.Seq()
	if !ok {
		return nil, refuse("line %d: `columns:` is a sequence", at)
	}
	columns := make([]Column, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		column, err := readColumn(item, count)
		if err != nil {
			return nil, err
		}
		if seen[column.Name] {
			return nil, refuse("line %d: two columns are called %s", at, Quote(column.Name))
		}
		seen[column.Name] = true
		columns = append(columns, column)
	}
	if len(columns) == 0 {
		return nil, refuse("line %d: a load with no columns holds no values", at)
	}

	var pairs [][2]int
	if pairsNode := node.Get("pairs"); pairsNode != nil {
		items, ok := pairsNode.SeqOrEmpty()
		if !ok {
			return nil, refuse("line %d: `pairs:` is a sequence of edges", at)
		}
		for _, item := range items {
			pair, err := readEdge(item, count)
			if err != nil {
				return nil, err
			}
			pairs = append(pairs, pair)
		}
	}
	return &Load{Nodes: nodes, Edges: edges, Count: count, Columns: columns, Pairs: pairs}, nil
}

func tableName(node Node, key string) (string, error) {
	text, err := field(node, key)
	if err != nil {
		return "", err
	}
	if !wordOrUnderscore(text) {
		return "", refuse("line %d: %s is not a table name", node.Line(), Quote(text))
	}
	return text, nil
}

func readColumn(node Node, count int) (Column, error) {
	at := node.Line()
	if unknown := node.Unknown("name", "type", "values"); len(unknown) > 0 {
		return Column{}, refuse("line %d: a column has no key %s", at, Quote(unknown[0]))
	}
	name, err := tableName(node, "name")
	if err != nil {
		return Column{}, err
	}
	ty, err := field(node, "type")
	if err != nil {
		return Column{}, err
	}
	if _, known := Form(ty); !known {
		return Column{}, refuse("line %d: %s is not a type this encoding knows", at, ty)
	}
	valuesNode := node.Get("values")
	if valuesNode == nil {
		return Column{}, refuse("line %d: a column holds `values:` in row order", at)
	}
	items, ok := valuesNode.Seq()
	if !ok {
		return Column{}, refuse("line %d: a column holds `values:` in row order", at)
	}
	if len(items) != count {
		return Column{}, refuse("line %d: column %s holds %d values against the %d rows the load "+
			"declares", at, Quote(name), len(items), count)
	}
	values := make([]any, 0, len(items))
	for _, item := range items {
		value, err := Payload(ty, item)
		if err != nil {
			return Column{}, err
		}
		values = append(values, value)
	}
	return Column{Name: name, Type: ty, Values: values}, nil
}

func readEdge(node Node, count int) ([2]int, error) {
	at := node.Line()
	if unknown := node.Unknown("from", "to"); len(unknown) > 0 {
		return [2]int{}, refuse("line %d: an edge has no key %s", at, Quote(unknown[0]))
	}
	var ends [2]int
	for i, key := range [2]string{"from", "to"} {
		value := node.Get(key)
		if value == nil {
			return [2]int{}, refuse("line %d: an edge has a `%s:` row number", at, key)
		}
		text, ok := value.Str()
		if !ok {
			return [2]int{}, refuse("line %d: an edge has a `%s:` row number", at, key)
		}
		end, err := strconv.Atoi(text)
		if err != nil {
			return [2]int{}, refuse("line %d: `%s:` is a row number", at, key)
		}
		if end < 0 || end >= count {
			return [2]int{}, refuse("line %d: `%s: %d` against a table of %d rows, which are "+
				"numbered 0 to %d", at, key, end, count, count-1)
		}
		ends[i] = end
	}
	return ends, nil
}

// dashedWords is whether text is lower case ASCII words joined by
// dashes, which is how a case and a connection are named.
func dashedWords(text string) bool {
	if text == "" {
		return false
	}
	for i := 0; i < len(text); i++ {
		c := text[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			continue
		}
		return false
	}
	return true
}

// wordOrUnderscore is whether text is ASCII letters, digits and
// underscores, which is what a statement may write after a $ and what a
// table may be called.
func wordOrUnderscore(text string) bool {
	if text == "" {
		return false
	}
	for i := 0; i < len(text); i++ {
		c := text[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			continue
		}
		return false
	}
	return true
}

// gqlstatusShaped is whether a code is the shape of a GQLSTATUS, which
// is five characters of digits and capitals. The shape and not the list:
// a corpus that had to be told about every code the standard defines
// would be one nobody could add a case to.
func gqlstatusShaped(code string) bool {
	if len(code) != 5 {
		return false
	}
	for i := 0; i < len(code); i++ {
		c := code[i]
		if (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') {
			continue
		}
		return false
	}
	return true
}
