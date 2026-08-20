// Package zuarrow reads a zu result as Arrow record batches, moving
// the engine's buffers rather than copying them.
//
// It is a module of its own beside the client rather than a package
// inside it, for the reason every Go program with a dependency list
// will recognise: arrow-go brings a tree of its own, and a program
// that queries a graph and reads rows should not carry it. A program
// that wants Arrow asks for this one and gets both.
//
// What crosses is the Arrow C Data Interface, which both sides of this
// call already speak: the engine writes an ArrowArrayStream over the C
// ABI and arrow-go imports one. Nothing between them is a copy. The
// arrays a batch is made of are the buffers the executor filled, at
// the addresses it filled them at, so a result of a hundred million
// rows costs a pointer a column to hand over.
//
//	rows, err := conn.Query(ctx, "MATCH (p:person) RETURN p.id AS id")
//	if err != nil {
//		return err
//	}
//	rdr, err := zuarrow.Reader(rows)
//	if err != nil {
//		return err
//	}
//	defer rdr.Release()
//	for rdr.Next() {
//		batch := rdr.RecordBatch()
//		// batch is valid until the next Next
//	}
//	return rdr.Err()
package zuarrow

import (
	"context"
	"errors"
	"unsafe"

	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/cdata"
	zu "github.com/tamnd/zu-go"
)

// Reader turns a result into the reader arrow-go reads everything
// through, in batches of [zu.DefaultBatch] rows.
//
// It spends the rows. The buffers behind them are the batches now, so
// there is nothing left to read the other way and the [zu.Rows] is
// closed whatever this call answered. Releasing the reader is what
// frees them, and it is the caller's to do; a reader that is dropped
// without being released is freed when the collector gets to it, which
// is later than a program reading a large result wants.
func Reader(rows *zu.Rows) (array.RecordReader, error) {
	return ReaderBatched(rows, 0)
}

// ReaderBatched is [Reader] with the batch size named. Zero asks for
// [zu.DefaultBatch]. The batches are slices of arrays that are already
// in memory, so this is about what the code downstream likes to work
// in and not about what gets allocated.
func ReaderBatched(rows *zu.Rows, rowsPerBatch int) (array.RecordReader, error) {
	if rows == nil {
		return nil, errors.New("zuarrow: no rows to read")
	}
	// Zeroed by Go, which is what the C Data Interface asks of the
	// caller, and left behind after the import: arrow-go moves the
	// stream's contents out of it and owns them from then on, so this
	// struct has no life beyond the two calls below.
	var stream cdata.CArrowArrayStream
	if err := rows.ArrowStream(unsafe.Pointer(&stream), rowsPerBatch); err != nil {
		return nil, err
	}
	rdr, err := cdata.ImportCRecordReader(&stream, nil)
	if err != nil {
		return nil, err
	}
	batches, ok := rdr.(array.RecordReader)
	if !ok {
		return nil, errors.New("zuarrow: arrow-go returned a reader that does not read batches")
	}
	return batches, nil
}

// Query runs a statement and hands back its result as Arrow, which is
// the whole of what most callers of this package do.
func Query(ctx context.Context, conn *zu.Conn, q string, args ...zu.Arg) (array.RecordReader, error) {
	rows, err := conn.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	rdr, err := ReaderBatched(rows, 0)
	if err != nil {
		// Spent by the call above whether it worked or not, so this
		// is belt and braces rather than the close that matters.
		rows.Close()
		return nil, err
	}
	return rdr, nil
}
