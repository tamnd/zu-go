package zuarrow_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/apache/arrow-go/v18/arrow/array"
	zu "github.com/tamnd/zu-go"
	"github.com/tamnd/zu-go/zuarrow"
)

// benchScan runs one way of reading a projection of a stored column,
// ten thousand rows of it. That shape is the one the sink fills down
// its columns, so it is the one that has buffers to hand over, and the
// export of it moves them rather than reading them.
func benchScan(b *testing.B, read func(*testing.B, *zu.Rows)) {
	const n = 10000
	db, err := zu.Memory()
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	conn, err := db.Connect(ctx)
	if err != nil {
		b.Fatal(err)
	}
	defer conn.Close()
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

// sum adds up an integer column through every batch a reader gives,
// which is what makes the export an export rather than a thing the
// compiler can delete.
func sum(b *testing.B, rdr array.RecordReader) {
	b.Helper()
	var total int64
	for rdr.Next() {
		column, ok := rdr.RecordBatch().Column(0).(*array.Int64)
		if !ok {
			b.Fatalf("a column of integers is a %T", rdr.RecordBatch().Column(0))
		}
		for _, n := range column.Int64Values() {
			total += n
		}
	}
	if err := rdr.Err(); err != nil {
		b.Fatal(err)
	}
	rdr.Release()
}

// BenchmarkReader is ten thousand rows read as Arrow and added up. The
// buffers do not move, so what is here is the export, the import and
// the walk over the array, and only the last of those is proportional
// to the answer.
func BenchmarkReader(b *testing.B) {
	benchScan(b, func(b *testing.B, rows *zu.Rows) {
		rdr, err := zuarrow.Reader(rows)
		if err != nil {
			b.Fatal(err)
		}
		sum(b, rdr)
	})
}

// BenchmarkReaderBatched is the same rows cut into batches of a
// thousand, which is what a consumer that works in batches asks for.
// Every batch is a slice of arrays that are already there, so the
// difference between this and the whole result in one batch is what the
// Arrow structures around a batch cost and nothing else.
func BenchmarkReaderBatched(b *testing.B) {
	benchScan(b, func(b *testing.B, rows *zu.Rows) {
		rdr, err := zuarrow.ReaderBatched(rows, 1000)
		if err != nil {
			b.Fatal(err)
		}
		sum(b, rdr)
	})
}

// BenchmarkColumn is the same rows borrowed off the result and added up
// without Arrow in between, which is the number the two above are
// measured against.
func BenchmarkColumn(b *testing.B) {
	benchScan(b, func(b *testing.B, rows *zu.Rows) {
		col, err := rows.Int64s(0)
		if err != nil {
			b.Fatal(err)
		}
		var total int64
		for _, n := range col {
			total += n
		}
	})
}
