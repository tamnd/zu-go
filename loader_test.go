package zu

// Building a database out of columns and an edge list.
//
// A load is the only way a Go program makes a graph with edges in it,
// so these check both halves: that what went in comes back out through
// statements, and that a load which cannot mean anything is refused at
// the call that made the mistake rather than written to disk and found
// out about later.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"
)

// built runs a load and returns the path it wrote, failing the test on
// anything the load answered. The graph is three people and two edges,
// which is the smallest one where the direction of an edge and the
// order of the rows are both visible.
func built(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "g.zu1")
	loader, err := NewLoader(path)
	if err != nil {
		t.Fatalf("a loader on an empty directory: %v", err)
	}
	defer loader.Close()

	if err := loader.Table("person", "knows", 3); err != nil {
		t.Fatalf("naming the tables: %v", err)
	}
	if err := loader.Int64s("uid", []int64{10, 20, 30}); err != nil {
		t.Fatalf("a column of integers: %v", err)
	}
	if err := loader.Strings("name", []string{"ada", "grace", "kay"}); err != nil {
		t.Fatalf("a column of strings: %v", err)
	}
	if err := loader.Edges([]uint32{0, 1}, []uint32{1, 2}); err != nil {
		t.Fatalf("an edge list: %v", err)
	}
	if err := loader.Finish(t.Context()); err != nil {
		t.Fatalf("finishing the load: %v", err)
	}
	return path
}

// loaded is [built] with a read-only connection open on what it wrote,
// which is what a test that only reads the graph back wants.
func loaded(t *testing.T) *Conn {
	t.Helper()
	return opened(t, built(t))
}

// opened opens a database that is already there and connects to it.
func opened(t *testing.T, path string) *Conn {
	t.Helper()
	db, err := Open(path, WithReadOnly())
	if err != nil {
		t.Fatalf("opening what the load wrote: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	conn, err := db.Connect(t.Context())
	if err != nil {
		t.Fatalf("connecting to what the load wrote: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// loader is a loader on a path in a directory of this test's own, with
// its tables already named, for the checks that are about one call and
// not about a whole load.
func loader(t *testing.T, rows uint64) *Loader {
	t.Helper()
	l, err := NewLoader(filepath.Join(t.TempDir(), "g.zu1"))
	if err != nil {
		t.Fatalf("a loader on an empty directory: %v", err)
	}
	t.Cleanup(func() { l.Close() })
	if err := l.Table("person", "knows", rows); err != nil {
		t.Fatalf("naming the tables: %v", err)
	}
	return l
}

func TestTheRowsReadBackInTheOrderTheyWentIn(t *testing.T) {
	conn := loaded(t)
	rows := query(t, conn, "MATCH (p:person) RETURN p.uid AS uid, p.name AS name")

	type person struct {
		UID  int64
		Name string
	}
	got, err := Collect[person](rows)
	if err != nil {
		t.Fatal(err)
	}
	want := []person{{10, "ada"}, {20, "grace"}, {30, "kay"}}
	if len(got) != len(want) {
		t.Fatalf("the load put in 3 rows and %d came back", len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d came back %v where the load put in %v", i, got[i], want[i])
		}
	}
}

func TestTheEdgesAreATableAPatternCanWalk(t *testing.T) {
	conn := loaded(t)
	rows := query(t, conn,
		"MATCH (a:person)-[:knows]->(b:person) RETURN a.name AS a, b.name AS b")

	type hop struct {
		A string
		B string
	}
	got, err := Collect[hop](rows)
	if err != nil {
		t.Fatal(err)
	}
	want := []hop{{"ada", "grace"}, {"grace", "kay"}}
	if len(got) != len(want) {
		t.Fatalf("the load put in 2 edges and %d came back", len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("edge %d came back %v where the load put in %v", i, got[i], want[i])
		}
	}
}

func TestALoadWithNoEdgesIsAGraphWithNone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "g.zu1")
	l, err := NewLoader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := l.Table("person", "knows", 2); err != nil {
		t.Fatal(err)
	}
	if err := l.Int64s("uid", []int64{1, 2}); err != nil {
		t.Fatal(err)
	}
	if err := l.Finish(t.Context()); err != nil {
		t.Fatal(err)
	}

	// The rel table is there whether or not anything is in it, which is
	// why naming it is not optional even for a graph with no edges.
	conn := opened(t, path)
	rows := query(t, conn, "MATCH ()-[r:knows]->() RETURN count(r) AS n")
	got, err := Collect[int64](rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != 0 {
		t.Errorf("a load with no edges left %v edges behind", got)
	}
}

func TestTheSameEdgeTwiceIsOneEdge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "g.zu1")
	l, err := NewLoader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := l.Table("person", "knows", 2); err != nil {
		t.Fatal(err)
	}
	if err := l.Int64s("uid", []int64{1, 2}); err != nil {
		t.Fatal(err)
	}
	// Three calls rather than one, because appending is the half of the
	// promise a single call would not exercise.
	for range 3 {
		if err := l.Edges([]uint32{0}, []uint32{1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Finish(t.Context()); err != nil {
		t.Fatal(err)
	}

	conn := opened(t, path)
	rows := query(t, conn, "MATCH ()-[r:knows]->() RETURN count(r) AS n")
	got, err := Collect[int64](rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("one edge given three times came back as %v", got)
	}
}

func TestAColumnOfEveryKindReadsBackAsWhatItWas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "g.zu1")
	l, err := NewLoader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := l.Table("person", "knows", 2); err != nil {
		t.Fatal(err)
	}

	born := []Date{{Days: -56371}, {Days: -23033}}
	woke := []LocalTime{{Nanos: 23_400_000_000_000}, {Nanos: 86_399_000_000_000}}
	seen := []LocalDateTime{{Nanos: 1_704_164_645_000_006_000}, {Nanos: -2_208_988_800_000_000_000}}
	took := []time.Duration{86_402 * time.Second, 0}
	aged := []YearMonth{{Months: 14}, {Months: -1}}

	if err := l.Int64s("tally", []int64{1, -2}); err != nil {
		t.Fatal(err)
	}
	if err := l.Float64s("ratio", []float64{1.5, -0.25}); err != nil {
		t.Fatal(err)
	}
	if err := l.Bools("flag", []bool{true, false}); err != nil {
		t.Fatal(err)
	}
	if err := l.Strings("name", []string{"ada", "grace"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Dates("born", born); err != nil {
		t.Fatal(err)
	}
	if err := l.LocalTimes("woke", woke); err != nil {
		t.Fatal(err)
	}
	if err := l.LocalDateTimes("seen", seen); err != nil {
		t.Fatal(err)
	}
	if err := l.Durations("took", took); err != nil {
		t.Fatal(err)
	}
	if err := l.YearMonths("aged", aged); err != nil {
		t.Fatal(err)
	}
	if err := l.Finish(t.Context()); err != nil {
		t.Fatal(err)
	}

	conn := opened(t, path)
	rows := query(t, conn, "MATCH (p:person) RETURN p.tally AS tally, p.ratio AS ratio, "+
		"p.flag AS flag, p.name AS name, p.born AS born, p.woke AS woke, "+
		"p.seen AS seen, p.took AS took, p.aged AS aged")

	type row struct {
		Tally int64
		Ratio float64
		Flag  bool
		Name  string
		Born  Date
		Woke  LocalTime
		Seen  LocalDateTime
		Took  time.Duration
		Aged  YearMonth
	}
	got, err := Collect[row](rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("two rows went in and %d came back", len(got))
	}
	want := []row{
		{1, 1.5, true, "ada", born[0], woke[0], seen[0], took[0], aged[0]},
		{-2, -0.25, false, "grace", born[1], woke[1], seen[1], took[1], aged[1]},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d came back\n\t%+v\nwhere the load put in\n\t%+v", i, got[i], want[i])
		}
	}
}

func TestAStringColumnHoldsEmptyStringsAndTextThatIsNotAscii(t *testing.T) {
	path := filepath.Join(t.TempDir(), "g.zu1")
	l, err := NewLoader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := l.Table("person", "knows", 4); err != nil {
		t.Fatal(err)
	}
	// The empty strings are the ones worth naming: they share a pointer
	// with whatever follows them, and the length array is the only
	// thing that says where each one ends.
	want := []string{"", "ada", "", "grâce et 日本語"}
	if err := l.Strings("name", want); err != nil {
		t.Fatal(err)
	}
	if err := l.Finish(t.Context()); err != nil {
		t.Fatal(err)
	}

	conn := opened(t, path)
	rows := query(t, conn, "MATCH (p:person) RETURN p.name AS name")
	got, err := Collect[string](rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("%d strings went in and %d came back", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d came back %q where the load put in %q", i, got[i], want[i])
		}
	}
}

func TestAStringThatIsNotTextIsRefusedAtTheColumnThatHeldIt(t *testing.T) {
	l := loader(t, 2)
	// A lone continuation byte, which no encoder produces and no query
	// could ever return.
	err := l.Strings("name", []string{"ada", "\x80"})
	if err == nil {
		t.Fatal("a column holding bytes that are not text was accepted")
	}
	// The load has not been finished, so nothing of it reached the file
	// either way, but the refusal has to come from the call that passed
	// the value rather than from the finish.
	if errors.Is(err, Closed) {
		t.Errorf("the refusal closed the loader: %v", err)
	}
}

func TestAColumnBeforeTheTableIsAMisuse(t *testing.T) {
	l, err := NewLoader(filepath.Join(t.TempDir(), "g.zu1"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	if err := l.Int64s("uid", []int64{1}); !errors.Is(err, Misuse) {
		t.Errorf("a column before the table answers %v", err)
	}
	// And the loader is still usable, because a misuse did nothing.
	if err := l.Table("person", "knows", 1); err != nil {
		t.Errorf("naming the tables after a refused column: %v", err)
	}
	if err := l.Int64s("uid", []int64{1}); err != nil {
		t.Errorf("the same column once the table is named: %v", err)
	}
}

func TestAColumnTheWrongLengthIsRefusedAtTheCallThatPassedIt(t *testing.T) {
	l := loader(t, 3)
	for _, values := range [][]int64{{1, 2}, {1, 2, 3, 4}, {}} {
		err := l.Int64s("uid", values)
		if !errors.Is(err, Misuse) {
			t.Errorf("a column of %d values against a table of 3 rows answers %v",
				len(values), err)
		}
	}
	// The message names the column, which is the whole reason this is
	// checked here and not at the finish.
	var e *Error
	if err := l.Int64s("uid", []int64{1, 2}); errors.As(err, &e) {
		if e.Message == "" {
			t.Error("a column the wrong length was refused without saying anything")
		}
	}
}

func TestAnEdgeListOfTwoDifferentLengthsIsRefusedHere(t *testing.T) {
	l := loader(t, 2)
	// This one never reaches the engine: the ABI takes one count for
	// both arrays, so a binding that passed the shorter length would be
	// silently dropping edges and one that passed the longer would be
	// reading off the end of a slice.
	err := l.Edges([]uint32{0, 1}, []uint32{1})
	if !errors.Is(err, Misuse) {
		t.Errorf("two edge columns of different lengths answer %v", err)
	}
}

func TestAnEdgeThatNamesNoRowIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "g.zu1")
	l, err := NewLoader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := l.Table("person", "knows", 2); err != nil {
		t.Fatal(err)
	}
	if err := l.Int64s("uid", []int64{1, 2}); err != nil {
		t.Fatal(err)
	}
	// Row 5 of a table with two rows in it. Whether this is refused at
	// the call or at the finish is the engine's to decide; what matters
	// is that it is refused and that no database is left behind.
	edged := l.Edges([]uint32{0}, []uint32{5})
	finished := l.Finish(t.Context())
	if edged == nil && finished == nil {
		t.Fatal("an edge pointing at a row that is not there was written")
	}
}

func TestEverythingAfterAFinishIsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "g.zu1")
	l, err := NewLoader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := l.Table("person", "knows", 1); err != nil {
		t.Fatal(err)
	}
	if err := l.Int64s("uid", []int64{1}); err != nil {
		t.Fatal(err)
	}
	if err := l.Finish(t.Context()); err != nil {
		t.Fatal(err)
	}

	for what, again := range map[string]func() error{
		"a table":  func() error { return l.Table("person", "knows", 1) },
		"a column": func() error { return l.Int64s("uid", []int64{1}) },
		"edges":    func() error { return l.Edges([]uint32{0}, []uint32{0}) },
		"a finish": func() error { return l.Finish(t.Context()) },
		"strings":  func() error { return l.Strings("name", []string{"ada"}) },
	} {
		err := again()
		if !errors.Is(err, Closed) && !errors.Is(err, Misuse) {
			t.Errorf("%s after the finish answers %v", what, err)
		}
	}
	// And the only call left is the one that gives the memory back.
	if err := l.Close(); err != nil {
		t.Errorf("closing a finished loader: %v", err)
	}
}

func TestEverythingAfterACloseIsAMisuseAndCloseIsSafeTwice(t *testing.T) {
	l := loader(t, 1)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Errorf("closing a closed loader: %v", err)
	}
	if err := l.Int64s("uid", []int64{1}); !errors.Is(err, Misuse) {
		t.Errorf("a column on a closed loader answers %v", err)
	}
	if err := l.Table("person", "knows", 1); !errors.Is(err, Misuse) {
		t.Errorf("a table on a closed loader answers %v", err)
	}
	if err := l.Finish(t.Context()); !errors.Is(err, Misuse) {
		t.Errorf("a finish on a closed loader answers %v", err)
	}
	if err := l.Edges([]uint32{0}, []uint32{0}); !errors.Is(err, Misuse) {
		t.Errorf("edges on a closed loader answer %v", err)
	}
	if err := l.Strings("name", []string{"a"}); !errors.Is(err, Misuse) {
		t.Errorf("strings on a closed loader answer %v", err)
	}
}

func TestALoaderNeverWritesOverADatabaseThatIsThere(t *testing.T) {
	path := built(t)
	if _, err := NewLoader(path); err == nil {
		t.Fatal("a load into a path that already holds a database was allowed to start")
	}

	// And what was there is untouched.
	conn := opened(t, path)
	rows := query(t, conn, "MATCH (p:person) RETURN count(p) AS n")
	got, err := Collect[int64](rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != 3 {
		t.Errorf("the refused load left %v rows behind where there were 3", got)
	}
}

func TestALoaderClosedBeforeItsFinishWroteNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "g.zu1")
	l, err := NewLoader(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Table("person", "knows", 1); err != nil {
		t.Fatal(err)
	}
	if err := l.Int64s("uid", []int64{1}); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	// The file the loader made at the start is still there and is the
	// caller's to remove, which is the ABI's behaviour and not this
	// package's to improve on.
	//
	// What it leaves is worth being exact about, because the header
	// calls it an empty file and it is not one. It is a whole database,
	// a quarter of a megabyte of it, and the node table the load named
	// is already in it with no rows. So a program whose load was
	// interrupted finds a path that no second load may start on, since
	// a loader refuses one that exists, and that opens perfectly well
	// as a graph with nothing in it. A partial load reads as a
	// successful load of nothing, and this test is here so that a
	// change to either half is a change somebody reads.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the file the loader created went somewhere: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("the loader left a zero length file, which this test was written to say it does not")
	}

	conn := opened(t, path)
	rows := query(t, conn, "MATCH (p:person) RETURN count(p) AS n")
	got, err := Collect[int64](rows)
	if err != nil {
		t.Fatal(err)
	}
	// None of the rows, which is the half that has to hold: the values
	// the load had already handed over were never written.
	if len(got) != 1 || got[0] != 0 {
		t.Errorf("a load that was never finished left %v rows behind", got)
	}
}

func TestALoaderDroppedWithoutACloseIsGivenBackAnyway(t *testing.T) {
	dir := t.TempDir()
	// Made in a function of its own so that the only reference to it is
	// gone by the time the collector is asked to look, which a variable
	// still in scope in the test body would not be.
	func() {
		l, err := NewLoader(filepath.Join(dir, "g.zu1"))
		if err != nil {
			t.Fatal(err)
		}
		if err := l.Table("person", "knows", 1); err != nil {
			t.Fatal(err)
		}
	}()

	// Twice, because the cleanup is registered against an object that
	// the first collection makes unreachable and the second one frees.
	runtime.GC()
	runtime.GC()
	// Nothing to assert beyond not crashing and not leaking: the leak
	// job in CI is what reads this, and a double free would take the
	// process down here.
}

func TestTwoGoroutinesOnOneLoaderAreRefusedRatherThanRaced(t *testing.T) {
	l := loader(t, 400_000)
	// Big enough that the call is still inside the engine when the
	// other goroutine arrives.
	values := make([]int64, 400_000)
	for i := range values {
		values[i] = int64(i)
	}

	started := make(chan struct{})
	var wg sync.WaitGroup
	var first error
	wg.Add(1)
	go func() {
		defer wg.Done()
		close(started)
		first = l.Int64s("uid", values)
	}()

	<-started
	second := l.Float64s("ratio", make([]float64, 400_000))
	wg.Wait()

	// Whichever one arrived second is the one refused, and this is a
	// race by construction, so either could be it. What must not happen
	// is both of them getting through.
	switch {
	case errors.Is(first, Concurrent) || errors.Is(second, Concurrent):
		// The refusal did nothing, so the loader is still good.
	case first == nil && second == nil:
		// Both got through, which is allowed only if they did not in
		// fact overlap. Nothing to check here beyond the race detector,
		// which is what this test is run under.
	default:
		t.Errorf("two goroutines on one loader answered %v and %v", first, second)
	}
}

func TestALoadIntoACancelledContextDoesNotStart(t *testing.T) {
	l := loader(t, 1)
	if err := l.Int64s("uid", []int64{1}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := l.Finish(ctx); err == nil {
		t.Fatal("a finish with a cancelled context wrote the database anyway")
	}
	// And the loader is not closed by it, because nothing was done.
	if err := l.Finish(t.Context()); err != nil {
		t.Errorf("finishing after a cancelled context: %v", err)
	}
}

func BenchmarkLoad(b *testing.B) {
	const rows = 100_000
	uid := make([]int64, rows)
	name := make([]string, rows)
	from := make([]uint32, rows)
	to := make([]uint32, rows)
	for i := range rows {
		uid[i] = int64(i)
		name[i] = "person-" + string(rune('a'+i%26))
		from[i] = uint32(i)
		to[i] = uint32((i*7 + 1) % rows)
	}

	dir := b.TempDir()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		func() {
			// A path of its own each time, because a loader refuses one
			// that is already there and the file from the last
			// iteration is.
			l, err := NewLoader(filepath.Join(dir, "b"+strconv.Itoa(i)+".zu1"))
			if err != nil {
				b.Fatal(err)
			}
			defer l.Close()
			if err := l.Table("person", "knows", rows); err != nil {
				b.Fatal(err)
			}
			if err := l.Int64s("uid", uid); err != nil {
				b.Fatal(err)
			}
			if err := l.Strings("name", name); err != nil {
				b.Fatal(err)
			}
			if err := l.Edges(from, to); err != nil {
				b.Fatal(err)
			}
			if err := l.Finish(b.Context()); err != nil {
				b.Fatal(err)
			}
		}()
	}
	b.ReportMetric(float64(rows), "rows/op")
}

// BenchmarkLoadColumns is the per-column cost on its own, with no file
// written, which is what says whether the crossing is the cost or the
// write is.
func BenchmarkLoadColumns(b *testing.B) {
	const rows = 100_000
	uid := make([]int64, rows)
	for i := range rows {
		uid[i] = int64(i)
	}

	dir := b.TempDir()
	l, err := NewLoader(filepath.Join(dir, "g.zu1"))
	if err != nil {
		b.Fatal(err)
	}
	defer l.Close()
	if err := l.Table("person", "knows", rows); err != nil {
		b.Fatal(err)
	}

	for i := 0; b.Loop(); i++ {
		// A new name each time, because a column given twice is a
		// different question from a column given once.
		if err := l.Int64s("uid"+strconv.Itoa(i), uid); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(rows), "rows/op")
}

// BenchmarkLoadStringColumn is the same thing for the one column type
// that has to copy, which is what says what that copy costs.
func BenchmarkLoadStringColumn(b *testing.B) {
	const rows = 100_000
	name := make([]string, rows)
	for i := range rows {
		name[i] = "person-" + strconv.Itoa(i)
	}

	dir := b.TempDir()
	l, err := NewLoader(filepath.Join(dir, "g.zu1"))
	if err != nil {
		b.Fatal(err)
	}
	defer l.Close()
	if err := l.Table("person", "knows", rows); err != nil {
		b.Fatal(err)
	}

	for i := 0; b.Loop(); i++ {
		if err := l.Strings("name"+strconv.Itoa(i), name); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(rows), "rows/op")
}
