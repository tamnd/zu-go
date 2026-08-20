package zu

import (
	"errors"
	"strings"
	"testing"

	"github.com/tamnd/zu-go/internal/arrowc"
)

// stream is room for an ArrowArrayStream, released and freed when the
// test ends whether anything was ever exported into it.
func stream(t *testing.T) *arrowc.Stream {
	t.Helper()
	s := arrowc.NewStream()
	t.Cleanup(s.Free)
	return s
}

// exported runs a statement, hands the result over as a stream and
// reads the whole of it, answering what the first column is called, how
// many batches crossed and how many rows were in them.
func exported(t *testing.T, conn *Conn, q string, rowsPerBatch int) (name string, batches int, rows int64) {
	t.Helper()
	result, err := conn.Query(t.Context(), q)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer result.Close()

	s := stream(t)
	if err := result.ArrowStream(s.Pointer(), rowsPerBatch); err != nil {
		t.Fatalf("%s does not export: %v", q, err)
	}

	cols, first, rc := s.Schema()
	if rc != 0 {
		t.Fatalf("the stream will not say what its schema is: %d", rc)
	}
	if cols != 1 {
		t.Errorf("a statement with one column exported %d", cols)
	}

	n, total, rc := s.Drain()
	if rc != 0 {
		t.Fatalf("the stream will not read: %d", rc)
	}
	return first, n, total
}

func TestAResultCrossesAsArrowBatchesAConsumerCanRead(t *testing.T) {
	conn := memory(t)
	name, batches, rows := exported(t, conn, "UNWIND [1, 2, 3, 4, 5] AS n RETURN n", 2)

	if name != "n" {
		t.Errorf("the column crossed as %q and the statement called it n", name)
	}
	// Five rows in batches of two is three batches, the last of one,
	// and the count is what says the batch size was read rather than
	// ignored.
	if batches != 3 {
		t.Errorf("five rows in batches of two crossed as %d batches", batches)
	}
	if rows != 5 {
		t.Errorf("five rows crossed as %d", rows)
	}
}

func TestAnUnnamedBatchSizeIsTheEnginesOwn(t *testing.T) {
	conn := memory(t)
	// Zero asks for the default, which is larger than anything a test
	// makes, so the whole result arrives as one batch.
	_, batches, rows := exported(t, conn, "UNWIND [1, 2, 3, 4, 5] AS n RETURN n", 0)
	if batches != 1 || rows != 5 {
		t.Errorf("five rows in the default batch crossed as %d batches of %d rows", batches, rows)
	}
}

func TestExportingSpendsTheRows(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, "UNWIND [1, 2, 3] AS n RETURN n")

	s := stream(t)
	if err := rows.ArrowStream(s.Pointer(), 0); err != nil {
		t.Fatalf("a result does not export: %v", err)
	}

	// The buffers are the stream's now, so there is nothing here to
	// read a second time and nothing here to free.
	if rows.Len() != 0 {
		t.Errorf("a result that was handed over still says it holds %d rows", rows.Len())
	}
	if rows.Next() {
		t.Error("a result that was handed over still reads rows")
	}
	if err := rows.Close(); err != nil {
		t.Errorf("closing a result that was handed over: %v", err)
	}
	if err := rows.ArrowStream(s.Pointer(), 0); !errors.Is(err, Misuse) {
		t.Errorf("handing the same result over twice answers %v", err)
	}
}

func TestExportingAfterTheConnectionClosedStillWorks(t *testing.T) {
	db, err := Memory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	rows, err := conn.Query(t.Context(), "UNWIND [1, 2, 3] AS n RETURN n")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	// A result owns its rows outright, and handing them to Arrow is a
	// read like any other. What the connection was there for is naming
	// a node column's table, and a result of integers has none.
	s := stream(t)
	if err := rows.ArrowStream(s.Pointer(), 0); err != nil {
		t.Fatalf("exporting a result whose connection has closed: %v", err)
	}
	batches, total, rc := s.Drain()
	if rc != 0 || total != 3 {
		t.Errorf("the rows crossed as %d rows in %d batches, rc %d", total, batches, rc)
	}
}

func TestExportingRefusesWhatItCannotDo(t *testing.T) {
	conn := memory(t)
	s := stream(t)

	rows := query(t, conn, "RETURN 1 AS one")
	if err := rows.ArrowStream(nil, 0); !errors.Is(err, Misuse) {
		t.Errorf("exporting into nothing answers %v", err)
	}
	if err := rows.ArrowStream(s.Pointer(), -1); !errors.Is(err, Misuse) {
		t.Errorf("a batch of fewer than no rows answers %v", err)
	}
	// Refused before the call, so the result is still there to read.
	if rows.Len() != 1 {
		t.Errorf("a refused export spent the result anyway, leaving %d rows", rows.Len())
	}

	closed := query(t, conn, "RETURN 1 AS one")
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := closed.ArrowStream(s.Pointer(), 0); !errors.Is(err, Misuse) {
		t.Errorf("exporting a result that is closed answers %v", err)
	}
}

func TestAColumnArrowHasNoTypeForIsRefusedAndSpendsTheRows(t *testing.T) {
	conn := memory(t)
	rows, err := conn.Query(t.Context(), "RETURN $v AS v", Named("v", ZonedTime{Nanos: 3600, Offset: 60}))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	s := stream(t)
	err = rows.ArrowStream(s.Pointer(), 0)
	if !errors.Is(err, Misuse) {
		t.Fatalf("a time in a zone crossed as arrow, or failed as %v", err)
	}
	// The refusal happens where the column is known, so it can say
	// which one, and the result is spent either way: the engine took it
	// before it looked at what was in it.
	if !strings.Contains(err.Error(), "v") {
		t.Errorf("the refusal does not name the column: %v", err)
	}
	if rows.Len() != 0 {
		t.Errorf("a refused export left %d rows behind", rows.Len())
	}
}
