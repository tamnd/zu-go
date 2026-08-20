package zu

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/tamnd/zu-go/internal/arrowc"
)

// rows makes a statement that answers n rows of one integer column,
// built out of a literal list because this engine has no generator.
func rowsOf(n int) string {
	var b strings.Builder
	b.WriteString("UNWIND [")
	for i := range n {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("0")
	}
	b.WriteString("] AS n RETURN n")
	return b.String()
}

func benchConn(b *testing.B) *Conn {
	b.Helper()
	db, err := Memory()
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	conn, err := db.Connect(context.Background())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { conn.Close() })
	return conn
}

// BenchmarkQuery is the whole round trip for the smallest statement
// there is, which is what a caller pays per call before any work.
func BenchmarkQuery(b *testing.B) {
	conn := benchConn(b)
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		rows, err := conn.Query(ctx, "RETURN 1 AS one")
		if err != nil {
			b.Fatal(err)
		}
		rows.Close()
	}
}

// BenchmarkQueryContext is the same call with a context that can be
// cancelled, which is what the watcher goroutine costs.
func BenchmarkQueryContext(b *testing.B) {
	conn := benchConn(b)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.ReportAllocs()
	for b.Loop() {
		rows, err := conn.Query(ctx, "RETURN 1 AS one")
		if err != nil {
			b.Fatal(err)
		}
		rows.Close()
	}
}

// BenchmarkPrepared is the same work with the statement compiled once,
// which is what the caller saves by holding a [Stmt].
func BenchmarkPrepared(b *testing.B) {
	conn := benchConn(b)
	ctx := context.Background()
	stmt, err := conn.Prepare(ctx, "RETURN 1 AS one")
	if err != nil {
		b.Fatal(err)
	}
	defer stmt.Close()
	b.ReportAllocs()
	for b.Loop() {
		rows, err := stmt.Query(ctx)
		if err != nil {
			b.Fatal(err)
		}
		rows.Close()
	}
}

// BenchmarkBind is one parameter across the boundary per call.
func BenchmarkBind(b *testing.B) {
	conn := benchConn(b)
	ctx := context.Background()
	stmt, err := conn.Prepare(ctx, "RETURN $v AS v")
	if err != nil {
		b.Fatal(err)
	}
	defer stmt.Close()
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		rows, err := stmt.Query(ctx, Named("v", int64(i)))
		if err != nil {
			b.Fatal(err)
		}
		rows.Close()
	}
}

// BenchmarkScanInt64 is the row at a time path, one cell per row,
// which is what Scan into a concrete destination costs.
func BenchmarkScanInt64(b *testing.B) {
	benchRead(b, func(b *testing.B, rows *Rows) {
		var n, sum int64
		for rows.Next() {
			if err := rows.Scan(&n); err != nil {
				b.Fatal(err)
			}
			sum += n
		}
	})
}

// BenchmarkScanAny is the same rows into an interface, which is the
// boxing the concrete path avoids.
func BenchmarkScanAny(b *testing.B) {
	benchRead(b, func(b *testing.B, rows *Rows) {
		var v any
		for rows.Next() {
			if err := rows.Scan(&v); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkCollect is the same rows through the reflection path, which
// is what Collect into a struct costs over Scan.
func BenchmarkCollect(b *testing.B) {
	benchRead(b, func(b *testing.B, rows *Rows) {
		if _, err := Collect[int64](rows); err != nil {
			b.Fatal(err)
		}
	})
}

// BenchmarkColumn is the whole column read in one call rather than a
// row at a time, which is the number the row at a time paths above are
// measured against. The result is the one UNWIND of a literal list
// gives, which the engine builds across its rows, so the column is
// converted out of them once on the first call. BenchmarkColumnScanned
// below is the same call on a result the engine filled down its
// columns, where there is nothing left to convert.
func BenchmarkColumn(b *testing.B) {
	benchRead(b, func(b *testing.B, rows *Rows) {
		col, err := rows.Int64s(0)
		if err != nil {
			b.Fatal(err)
		}
		var sum int64
		for _, n := range col {
			sum += n
		}
	})
}

// exportAndDrain hands a result to an Arrow consumer and reads the
// whole of it back, which is what the two benchmarks below measure over
// the two shapes a result comes in.
func exportAndDrain(b *testing.B, rows *Rows) {
	s := arrowc.NewStream()
	if err := rows.ArrowStream(s.Pointer(), 0); err != nil {
		b.Fatal(err)
	}
	if _, _, rc := s.Drain(); rc != 0 {
		b.Fatalf("the stream will not read: %d", rc)
	}
	s.Free()
}

// BenchmarkArrowStream is a stored column handed to an Arrow consumer.
// Nothing here is proportional to the answer: the buffers the sink
// filled are moved into the arrays that leave, so this is the schema,
// the stream, one batch and the pointers in it, and it should cost
// about the same for ten thousand rows as for ten million. What tells
// you it still does is this number against BenchmarkColumnScanned,
// which reads the same buffers the same way with no Arrow in between.
func BenchmarkArrowStream(b *testing.B) {
	benchScan(b, exportAndDrain)
}

// BenchmarkColumnScanned is that same result borrowed straight off the
// result rather than exported, which is what the number above is
// measured against. Both are pointers to the buffers the executor
// filled, so what separates them is arrow-go and not the engine, and
// what is left in either is mostly the loop that adds the column up.
func BenchmarkColumnScanned(b *testing.B) {
	benchScan(b, func(b *testing.B, rows *Rows) {
		col, err := rows.Int64s(0)
		if err != nil {
			b.Fatal(err)
		}
		var sum int64
		for _, n := range col {
			sum += n
		}
	})
}

// BenchmarkArrowStreamRowBuilt is the other shape, and the reason the
// two are separate. A result built across its rows rather than down its
// columns has no buffers to hand over, so the export reads it into
// buffers of its own and pays a cost per row. That is the fallback
// working rather than the fast path failing, and the two numbers beside
// each other are what say which one a statement got.
func BenchmarkArrowStreamRowBuilt(b *testing.B) {
	benchRead(b, exportAndDrain)
}

// benchRead runs one way of reading a result of ten thousand rows,
// with the query itself outside the measurement so that what is left
// is the reading.
func benchRead(b *testing.B, read func(*testing.B, *Rows)) {
	const n = 10000
	conn := benchConn(b)
	ctx := context.Background()
	stmt, err := conn.Prepare(ctx, rowsOf(n))
	if err != nil {
		b.Fatal(err)
	}
	defer stmt.Close()

	b.ReportAllocs()
	b.SetBytes(n * 8)
	for b.Loop() {
		b.StopTimer()
		rows, err := stmt.Query(ctx)
		if err != nil {
			b.Fatal(err)
		}
		b.StartTimer()

		read(b, rows)

		b.StopTimer()
		rows.Close()
		b.StartTimer()
	}
}

// benchScan runs one way of reading a projection of a stored column,
// which is the shape the sink fills down its columns and so the shape
// that has buffers to hand over. The rows above are unwound out of a
// literal list and are built across their rows instead, which is a
// different path through the export and worth its own number.
func benchScan(b *testing.B, read func(*testing.B, *Rows)) {
	const n = 10000
	conn := benchConn(b)
	ctx := context.Background()
	for id := range n {
		if err := conn.Exec(ctx, "INSERT (p:person {id: "+strconv.Itoa(id)+"})"); err != nil {
			b.Fatal(err)
		}
	}
	stmt, err := conn.Prepare(ctx, "MATCH (p:person) RETURN p.id AS id")
	if err != nil {
		b.Fatal(err)
	}
	defer stmt.Close()

	b.ReportAllocs()
	b.SetBytes(n * 8)
	for b.Loop() {
		b.StopTimer()
		rows, err := stmt.Query(ctx)
		if err != nil {
			b.Fatal(err)
		}
		if rows.Len() != n {
			b.Fatalf("the graph answered %d rows and holds %d nodes", rows.Len(), n)
		}
		b.StartTimer()

		read(b, rows)

		b.StopTimer()
		rows.Close()
		b.StartTimer()
	}
}
