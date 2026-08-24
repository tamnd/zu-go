package corpus

// Running the corpus through this client, and saying what happened in
// the form the other eight runners are compared against.
//
// Each case gets a database of its own. Cases in a suite are written as
// if nothing came before them, and the cheapest way to keep that true is
// to make it true: a case that leaked a table into the next one would be
// a failure that moves when the file is reordered, which is the worst
// kind to be handed.
//
// An outcome is one of three things and not two. Passed and failed are
// obvious. Unsupported is the third, and it exists because the corpus is
// versioned with the engine and shipped to nine clients that will not
// all implement the same subset at the same time: a client that cannot
// yet parse a statement should say so, and a report should be able to
// tell that apart from an answer that came back wrong.
//
// What this prints is what the Rust runner prints, line for line, so
// that a disagreement between two clients is a diff and not a reading
// exercise.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	zu "github.com/tamnd/zu-go"
)

// An Outcome is what one case came to.
type Outcome int

// The three outcomes.
const (
	Passed Outcome = iota
	Failed
	Unsupported
)

// mark is how a report spells an outcome, which is the reference
// runner's spelling and not a word of it different.
func (o Outcome) mark() string {
	switch o {
	case Passed:
		return "ok"
	case Failed:
		return "FAILED"
	default:
		return "unsupported"
	}
}

// A Ran is what one case did, with the account of why when it did not
// pass.
type Ran struct {
	// Suite is the name of the suite the case is in.
	Suite string
	// Case is the case's name within that suite.
	Case string
	// Line is where the case starts in its file.
	Line int
	// Outcome is what it came to.
	Outcome Outcome
	// Detail is what went wrong, in enough detail to fix the case or
	// the engine without running it again.
	Detail string
}

// String is the line a report prints for one case.
func (r Ran) String() string {
	head := r.Suite + "/" + r.Case + " line " + strconv.Itoa(r.Line) + " " + r.Outcome.mark()
	if r.Detail == "" {
		return head
	}
	return head + ": " + r.Detail
}

// A Report is what a whole run did.
type Report struct {
	// Ran is one entry per case, in the order the cases were run.
	Ran []Ran
}

// Count is how many cases came to one outcome.
func (r Report) Count(outcome Outcome) int {
	n := 0
	for _, ran := range r.Ran {
		if ran.Outcome == outcome {
			n++
		}
	}
	return n
}

// Summary is one line saying what the run came to, which is what a CI
// log keeps and what two runs are compared by.
func (r Report) Summary() string {
	return strconv.Itoa(len(r.Ran)) + " cases, " + strconv.Itoa(r.Count(Passed)) +
		" passed, " + strconv.Itoa(r.Count(Failed)) + " failed, " +
		strconv.Itoa(r.Count(Unsupported)) + " unsupported"
}

// Run runs every case of every suite, in the order they were written.
//
// directory is one the runner may make databases under. Each case gets
// its own file in it, named after the case, so that a failure leaves
// something to open.
func Run(ctx context.Context, suites []Suite, directory string) Report {
	var report Report
	for _, suite := range suites {
		for _, one := range suite.Cases {
			ran := runCase(ctx, suite, one, directory)
			// A failure leaves its database behind, which is the one
			// thing somebody reading the report will want to open.
			// Everything else goes as it finishes, because a corpus of
			// fourteen hundred cases is fourteen hundred files and
			// holding them all until the run ends is gigabytes of a disk
			// that has other work to do. The Rust and C runners do the
			// same.
			if ran.Outcome != Failed {
				path := casePath(directory, suite.Name, one.Name)
				_ = os.Remove(path)
				// The WAL sidecar goes with it. A database is <db> and
				// its log is <db>.wal, and a log left beside a name the
				// next run creates again is a log that run would adopt.
				_ = os.Remove(path + ".wal")
			}
			report.Ran = append(report.Ran, ran)
		}
	}
	return report
}

func casePath(directory, suite, name string) string {
	return filepath.Join(directory, suite+"-"+name+".zu")
}

func runCase(ctx context.Context, suite Suite, one Case, directory string) Ran {
	ran := func(outcome Outcome, detail string) Ran {
		return Ran{Suite: suite.Name, Case: one.Name, Line: one.Line,
			Outcome: outcome, Detail: detail}
	}
	path := casePath(directory, suite.Name, one.Name)

	// The load goes in before the connection opens, because it is bulk
	// load and bulk load is the path that builds the file rather than
	// one that goes through a statement. Every case of the suite gets
	// its own copy of it for the same reason every case gets its own
	// database.
	//
	// A loader makes the file, so the two halves of this are the two
	// ways a database comes into being in this client and a case has
	// exactly one of them.
	if suite.Load != nil {
		if err := applyLoad(ctx, suite.Load, path); err != nil {
			return ran(Failed, "the suite's load: "+errorText(err))
		}
	} else {
		made, err := zu.Create(path)
		if err != nil {
			return ran(Failed, "creating "+path+": "+errorText(err))
		}
		made.Close()
	}

	db, err := zu.Open(path)
	if err != nil {
		return ran(Failed, "opening "+path+": "+errorText(err))
	}
	defer db.Close()

	main, err := db.Connect(ctx)
	if err != nil {
		return ran(Failed, "opening "+path+": "+errorText(err))
	}
	open := []named{{Main, main}}
	// In reverse, so that the connection the case was opened with is
	// the last one to go, which is the order the ones after it were made
	// from it in.
	defer func() {
		for i := len(open) - 1; i >= 0; i-- {
			open[i].conn.Close()
		}
	}()

	for i, step := range one.Setup {
		on, err := connection(ctx, &open, step.On)
		if err != nil {
			return ran(Failed, "connecting as "+Quote(step.On)+": "+errorText(err))
		}
		if err := on.Exec(ctx, step.Query); err != nil {
			// A setup that fails is not a result about the statement
			// under test, so it is never a pass and never a quiet skip.
			if unsupported(err) {
				return ran(Unsupported, "setup "+strconv.Itoa(i+1)+": "+errorText(err))
			}
			return ran(Failed, "setup "+strconv.Itoa(i+1)+" failed: "+errorText(err))
		}
	}

	on, err := connection(ctx, &open, one.On)
	if err != nil {
		return ran(Failed, "connecting as "+Quote(one.On)+": "+errorText(err))
	}

	args := make([]zu.Arg, 0, len(one.Params))
	for _, param := range one.Params {
		args = append(args, zu.Named(param.Name, param.Value))
	}
	rows, err := on.Query(ctx, one.Query, args...)
	if err != nil {
		if one.Raises != "" {
			code := statusCode(err)
			if code == "" {
				return ran(Failed, "failed with no GQLSTATUS where the case wants "+
					one.Raises+": "+errorText(err))
			}
			if code == one.Raises {
				return ran(Passed, "")
			}
			return ran(Failed, "raised "+code+" where the case wants "+one.Raises+": "+
				errorText(err))
		}
		if unsupported(err) {
			return ran(Unsupported, errorText(err))
		}
		return ran(Failed, errorText(err))
	}
	defer rows.Close()

	if one.Raises != "" {
		return ran(Failed, "returned rows where the case wants "+one.Raises)
	}

	// The catalog after the statement rather than before it, because a
	// statement may have made the table the rows it returns are rows of.
	tables := func(table uint32) string {
		if name, err := on.TableName(table); err == nil && name != "" {
			return name
		}
		// A table with no name is spelled after its id, which is what a
		// node column of an export is named when there is no catalog to
		// ask. It should not happen with the connection right here, and
		// it is a spelling rather than a stop because a report saying
		// the case wants person#1 and this is #7 is more use to whoever
		// has to fix it than one that gave up.
		return "#" + strconv.FormatUint(uint64(table), 10)
	}

	got, err := readAll(rows, tables)
	if err != nil {
		return ran(Failed, errorText(err))
	}
	if detail := compare(one.Columns, one.Rows, rows.Columns(), got); detail != "" {
		return ran(Failed, detail)
	}
	// The export is checked on the result the rows were read from rather
	// than on a second run of the statement, because it is the same
	// result a client exports: one statement, two ways of reading what
	// it gave back.
	if detail := exported(one.Arrow, rows, len(got)); detail != "" {
		return ran(Failed, detail)
	}
	return ran(Passed, "")
}

// A named is one open connection and the name the case calls it by.
type named struct {
	name string
	conn *zu.Conn
}

// connection is the connection a case named, made if this is the first
// mention of it.
//
// A new one is a duplicate of the case's own rather than a second open
// of the file, which is what a pool does: the two share the write side,
// so each sees what the other has committed. Opening the path twice
// would be two databases that happen to be the same file, which is a
// different thing and not what a case about a transaction means.
func connection(ctx context.Context, open *[]named, name string) (*zu.Conn, error) {
	for _, had := range *open {
		if had.name == name {
			return had.conn, nil
		}
	}
	made, err := (*open)[0].conn.Duplicate(ctx)
	if err != nil {
		return nil, err
	}
	*open = append(*open, named{name, made})
	return made, nil
}

// readAll is every row of a result, in the corpus's own shape.
//
// The whole result is read before anything is compared, because the
// engine hands back an array rather than a cursor and a comparison that
// stopped at the first difference would leave the rest unread anyway.
func readAll(rows *zu.Rows, tables func(uint32) string) ([][]any, error) {
	out := make([][]any, 0, rows.Len())
	for i := int64(0); i < rows.Len(); i++ {
		values, err := rows.RowAt(i).Values()
		if err != nil {
			return nil, err
		}
		// Into the corpus's own shape here rather than at the
		// comparison, because the engine's edge carries a field the
		// corpus does not write and its node carries an id where a case
		// writes a name.
		for j, value := range values {
			values[j] = Cell(value, tables)
		}
		out = append(out, values)
	}
	return out, nil
}

// applyLoad puts the suite's load in through this client's own bulk load
// path, which is the strongest form of the corpus question: the value
// crosses the boundary twice and by two different mechanisms.
func applyLoad(ctx context.Context, load *Load, path string) error {
	loader, err := zu.NewLoader(path)
	if err != nil {
		return err
	}
	defer loader.Close()
	if err := loader.Table(load.Nodes, load.Edges, uint64(load.Count)); err != nil {
		return err
	}
	for _, column := range load.Columns {
		if err := loadColumn(loader, column); err != nil {
			return err
		}
	}
	if len(load.Pairs) > 0 {
		from := make([]uint32, len(load.Pairs))
		to := make([]uint32, len(load.Pairs))
		for i, pair := range load.Pairs {
			from[i], to[i] = uint32(pair[0]), uint32(pair[1])
		}
		if err := loader.Edges(from, to); err != nil {
			return err
		}
	}
	return loader.Finish(ctx)
}

// loadColumn hands one column to the method that takes its type.
//
// The loader has a method per column type rather than one that takes an
// any, which is what makes a load a real test of the encoding: a column
// of dates goes in as dates. A type with no method here is a column the
// corpus has never written, and it says so by name rather than by
// putting the values somewhere they do not belong.
func loadColumn(loader *zu.Loader, column Column) error {
	switch column.Type {
	case "STRING":
		return loader.Strings(column.Name, cast[string](column.Values))
	case "BOOL":
		return loader.Bools(column.Name, cast[bool](column.Values))
	case "FLOAT32", "FLOAT64":
		return loader.Float64s(column.Name, cast[float64](column.Values))
	case "DATE":
		return loader.Dates(column.Name, cast[zu.Date](column.Values))
	case "LOCALTIME":
		return loader.LocalTimes(column.Name, cast[zu.LocalTime](column.Values))
	case "LOCALDATETIME":
		return loader.LocalDateTimes(column.Name, cast[zu.LocalDateTime](column.Values))
	}
	if _, integer := bounds[column.Type]; integer {
		return loader.Int64s(column.Name, cast[int64](column.Values))
	}
	if column.Type == "DURATION" {
		// The two duration kinds are two columns as far as the loader is
		// concerned, and a column is one or the other, so which one it
		// is comes off the first value.
		if _, months := column.Values[0].(zu.YearMonth); months {
			return loader.YearMonths(column.Name, cast[zu.YearMonth](column.Values))
		}
		return loader.Durations(column.Name, cast[time.Duration](column.Values))
	}
	return refuse("a load column of %s, which this client has no loader method for", column.Type)
}

// cast is a column of decoded values as the slice one loader method
// takes. A value of the wrong type is the zero one, which cannot happen:
// every value in a column went through the same type's parser.
func cast[T any](values []any) []T {
	out := make([]T, len(values))
	for i, value := range values {
		out[i], _ = value.(T)
	}
	return out
}

// exported is what the export gave that the case did not want, or the
// empty string when the case says nothing about it and when the two
// agree.
//
// A result Arrow has no type for is a refusal from the export rather
// than a condition from the statement, so a case saying `refused` is the
// case where the stream failing to open is the right answer.
func exported(want *Export, rows *zu.Rows, count int) string {
	if want == nil {
		return ""
	}
	got, given, err := Exported(rows)
	if err != nil {
		if want.Refused {
			return ""
		}
		return "arrow refused the result: " + errorText(err)
	}
	if want.Refused {
		return "arrow exported the result where the case wants a refusal"
	}
	if detail := SchemaSays(got, want.Fields); detail != "" {
		return detail
	}
	if given != int64(count) {
		return "arrow gives " + strconv.FormatInt(given, 10) + " rows where the case wants " +
			strconv.Itoa(count)
	}
	return ""
}

// unsupported is whether a condition means the engine does not implement
// the statement rather than that the statement is wrong.
//
// The two GQL classes that say so are 42, syntax error or access rule
// violation, and 0A, feature not supported. A case landing on either is
// a case ahead of the engine, which the corpus allows on purpose: the
// cases are the contract and the engine catches up to them.
func unsupported(err error) bool {
	code := statusCode(err)
	return strings.HasPrefix(code, "42") || strings.HasPrefix(code, "0A")
}

// statusCode is the GQLSTATUS a failure carries, or the empty string for
// one that carries none.
func statusCode(err error) string {
	var e *zu.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// errorText is what the engine said, which is what the Rust runner
// prints for the same failure.
//
// The message rather than Error, which is what a Go program logs: this
// client puts "zu: " in front of every message, and the reference runner
// does not, so a report built out of Error would differ from the one it
// is diffed against on every line that has a failure in it. The message
// a condition carries already opens with its own code, which is why
// nothing is added here either.
func errorText(err error) string {
	var e *zu.Error
	if errors.As(err, &e) && e.Message != "" {
		return e.Message
	}
	var corpus *CorpusError
	if errors.As(err, &corpus) {
		return corpus.Message
	}
	var refused *ArrowError
	if errors.As(err, &refused) {
		return refused.Message
	}
	return err.Error()
}

// compare is what differs between what a case wants and what came back,
// or the empty string if nothing does.
//
// It reports the first difference rather than all of them, because the
// first is nearly always the cause of the rest, and a report that prints
// a hundred rows is one nobody reads to the end. The order the checks
// run in is the reference runner's, so that two runners looking at the
// same wrong answer say the same thing about it.
func compare(wantColumns []string, wantRows [][]any, gotColumns []string, gotRows [][]any) string {
	if !sameNames(wantColumns, gotColumns) {
		return "columns " + names(gotColumns) + " where the case wants " + names(wantColumns)
	}
	for i := 0; i < len(wantRows) && i < len(gotRows); i++ {
		want, got := wantRows[i], gotRows[i]
		for j := 0; j < len(want) && j < len(got); j++ {
			if Same(want[j], got[j]) {
				continue
			}
			name := "?"
			if j < len(wantColumns) {
				name = wantColumns[j]
			}
			return "row " + strconv.Itoa(i+1) + " column " + name + " is " + Show(got[j]) +
				" where the case wants " + Show(want[j])
		}
	}
	if len(wantRows) != len(gotRows) {
		return strconv.Itoa(len(gotRows)) + " rows where the case wants " +
			strconv.Itoa(len(wantRows))
	}
	return ""
}

// sameNames compares two lists of column names. A case with no columns
// writes an empty sequence and a result with none hands back an empty
// slice, and whether either is nil is not something a case can say.
func sameNames(want, got []string) bool {
	if len(want) != len(got) {
		return false
	}
	for i := range want {
		if want[i] != got[i] {
			return false
		}
	}
	return true
}

// names is a list of column names the way Rust's {:?} writes one.
func names(columns []string) string {
	quoted := make([]string, len(columns))
	for i, name := range columns {
		quoted[i] = `"` + name + `"`
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
