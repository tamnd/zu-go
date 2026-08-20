package zuarrow_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	zu "github.com/tamnd/zu-go"
	"github.com/tamnd/zu-go/zuarrow"
)

// memory opens a database that never touches the filesystem and one
// connection on it, closed when the test ends.
func memory(t *testing.T) *zu.Conn {
	t.Helper()
	db, err := zu.Memory()
	if err != nil {
		t.Fatalf("a database in memory does not open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	conn, err := db.Connect(t.Context())
	if err != nil {
		t.Fatalf("a database in memory does not connect: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// people puts five nodes in a table called person, which is what the
// tests about node columns need and what nothing else here does.
func people(t *testing.T, conn *zu.Conn) {
	t.Helper()
	for id := range 5 {
		if err := conn.Exec(t.Context(), "INSERT (p:person {id: "+strconv.Itoa(id)+"})"); err != nil {
			t.Fatalf("a person does not go in: %v", err)
		}
	}
}

// query runs a statement and fails the test if it does not answer.
func query(t *testing.T, conn *zu.Conn, q string, args ...zu.Arg) *zu.Rows {
	t.Helper()
	rows, err := conn.Query(t.Context(), q, args...)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	t.Cleanup(func() { rows.Close() })
	return rows
}

// read drains a reader into the batches it gave, releasing it at the
// end of the test rather than here, since a batch is only good while
// the reader that made it is.
func read(t *testing.T, rdr array.RecordReader) []arrow.RecordBatch {
	t.Helper()
	t.Cleanup(rdr.Release)
	var out []arrow.RecordBatch
	for rdr.Next() {
		batch := rdr.RecordBatch()
		batch.Retain()
		t.Cleanup(batch.Release)
		out = append(out, batch)
	}
	if err := rdr.Err(); err != nil {
		t.Fatalf("the reader stopped on %v", err)
	}
	return out
}

func TestAResultReadsAsRecordBatches(t *testing.T) {
	conn := memory(t)
	rdr, err := zuarrow.Reader(query(t, conn, "UNWIND [1, 2, 3, 4, 5] AS n RETURN n"))
	if err != nil {
		t.Fatalf("a result does not read as arrow: %v", err)
	}
	batches := read(t, rdr)

	if got := rdr.Schema().Field(0).Name; got != "n" {
		t.Errorf("the column arrived as %q and the statement called it n", got)
	}
	if got := rdr.Schema().Field(0).Type.ID(); got != arrow.INT64 {
		t.Errorf("a column of integers arrived as %v", got)
	}
	if len(batches) != 1 {
		t.Fatalf("five rows in the default batch arrived as %d batches", len(batches))
	}

	column, ok := batches[0].Column(0).(*array.Int64)
	if !ok {
		t.Fatalf("a column of integers is a %T", batches[0].Column(0))
	}
	want := []int64{1, 2, 3, 4, 5}
	got := column.Int64Values()
	if len(got) != len(want) {
		t.Fatalf("five rows arrived as %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d arrived as %d and was unwound as %d", i, got[i], want[i])
		}
	}
}

func TestBatchesAreTheSizeThatWasAsked(t *testing.T) {
	conn := memory(t)
	rdr, err := zuarrow.ReaderBatched(query(t, conn, "UNWIND [1, 2, 3, 4, 5] AS n RETURN n"), 2)
	if err != nil {
		t.Fatalf("a result does not read as arrow: %v", err)
	}
	batches := read(t, rdr)

	want := []int64{2, 2, 1}
	if len(batches) != len(want) {
		t.Fatalf("five rows in batches of two arrived as %d batches", len(batches))
	}
	for i, n := range want {
		if got := batches[i].NumRows(); got != n {
			t.Errorf("batch %d holds %d rows and should hold %d", i, got, n)
		}
	}
}

func TestANodeColumnCarriesTheNameOfItsTable(t *testing.T) {
	conn := memory(t)
	people(t, conn)

	rdr, err := zuarrow.Query(t.Context(), conn, "MATCH (p:person) RETURN p AS p")
	if err != nil {
		t.Fatalf("a result with a node in it does not read as arrow: %v", err)
	}
	batches := read(t, rdr)
	if len(batches) != 1 {
		t.Fatalf("five nodes arrived as %d batches", len(batches))
	}

	// A node is a struct of the table it belongs to and where in that
	// table it is, and the name is the catalog's answer rather than the
	// id the result carries.
	nodes, ok := batches[0].Column(0).(*array.Struct)
	if !ok {
		t.Fatalf("a column of nodes is a %T", batches[0].Column(0))
	}
	names, ok := nodes.Field(0).(*array.String)
	if !ok {
		t.Fatalf("the table of a node is a %T", nodes.Field(0))
	}
	if got := names.Value(0); got != "person" {
		t.Errorf("the nodes say their table is %q and it is called person", got)
	}
}

func TestANodeColumnWithoutItsConnectionNamesTheTableAfterItsId(t *testing.T) {
	db, err := zu.Memory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Exec(t.Context(), "INSERT (p:person {id: 1})"); err != nil {
		t.Fatal(err)
	}
	rows, err := conn.Query(t.Context(), "MATCH (p:person) RETURN p AS p")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	// The catalog goes with the connection, and the result still has to
	// answer, which it does by saying which table by id.
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	rdr, err := zuarrow.Reader(rows)
	if err != nil {
		t.Fatalf("a result whose connection has closed does not read as arrow: %v", err)
	}
	batches := read(t, rdr)
	if len(batches) != 1 {
		t.Fatalf("one node arrived as %d batches", len(batches))
	}
	nodes := batches[0].Column(0).(*array.Struct)
	names := nodes.Field(0).(*array.String)
	if got := names.Value(0); !strings.HasPrefix(got, "#") {
		t.Errorf("with no catalog to ask, the table is called %q", got)
	}
}

func TestReadingSpendsTheRows(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, "UNWIND [1, 2, 3] AS n RETURN n")

	rdr, err := zuarrow.Reader(rows)
	if err != nil {
		t.Fatalf("a result does not read as arrow: %v", err)
	}
	defer rdr.Release()

	// The buffers are the reader's now. Closing what is left is a
	// no-op and reading it gives nothing, which is what keeps a
	// program from freeing what it handed over.
	if rows.Len() != 0 {
		t.Errorf("a result that was handed over still says it holds %d rows", rows.Len())
	}
	if rows.Next() {
		t.Error("a result that was handed over still reads rows")
	}
	if err := rows.Close(); err != nil {
		t.Errorf("closing a result that was handed over: %v", err)
	}
	if _, err := zuarrow.Reader(rows); !errors.Is(err, zu.Misuse) {
		t.Errorf("reading the same result twice answers %v", err)
	}
}

func TestAColumnArrowHasNoTypeForIsRefused(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, "RETURN $v AS v", zu.Named("v", zu.ZonedTime{Nanos: 3600, Offset: 60}))

	rdr, err := zuarrow.Reader(rows)
	if err == nil {
		rdr.Release()
		t.Fatal("a time in a zone read as arrow, and arrow has no type for one")
	}
	if !errors.Is(err, zu.Misuse) {
		t.Errorf("the refusal is %v", err)
	}
	if !strings.Contains(err.Error(), "v") {
		t.Errorf("the refusal does not name the column: %v", err)
	}
}

func TestNoRowsIsRefusedRatherThanRead(t *testing.T) {
	if _, err := zuarrow.Reader(nil); err == nil {
		t.Error("reading nothing as arrow worked")
	}
}

func TestQueryClosesTheRowsWhenItRefuses(t *testing.T) {
	conn := memory(t)
	if _, err := zuarrow.Query(t.Context(), conn, "NOT A QUERY"); err == nil {
		t.Error("a statement that is not one read as arrow")
	}
}
