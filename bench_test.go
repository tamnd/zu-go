package zu

import (
	"context"
	"strings"
	"testing"
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

// BenchmarkColumn is the whole column borrowed from the result rather
// than read a row at a time. Nothing is copied and nothing is
// converted, so this is the number the row at a time paths are
// measured against.
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
