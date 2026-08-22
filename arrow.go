package zu

/*
#include <zu.h>
*/
import "C"

import (
	"unsafe"
)

// DefaultBatch is the batch size the engine uses when a caller does
// not name one, which is what [Rows.ArrowStream] takes a zero to mean.
const DefaultBatch = 65536

// ArrowStream hands the whole result to an Arrow consumer through the
// C Data Interface and spends the Rows.
//
// This is the low-level door, and most programs want the zuarrow
// subpackage rather than this: it wraps this call in the reader
// arrow-go already knows how to read, and it is a separate module so
// that a program with no use for Arrow does not carry the dependency.
// What is here is what that module needs and what a program with its
// own Arrow bindings can use instead.
//
// out is the address of an ArrowArrayStream the caller owns and has
// not initialised. It is written only on success, and the consumer
// releases it through its own release callback rather than through
// anything in this package. rowsPerBatch is how many rows a consumer
// sees at a time, and zero asks for [DefaultBatch]; the batches are
// slices of arrays that are already in memory, so this is about what a
// consumer likes to work in and not about what gets allocated.
//
// Nothing on this path is a copy. The arrays that cross are the
// buffers the engine's executor filled, at the addresses it filled
// them at, which is why the Rows is spent: after the buffers have left
// there is nothing here to read a second time. The Rows is closed
// whatever the call answered, including a refusal, and closing it
// again afterwards is the no-op it always was.
//
// A node column names its table out of the catalog the connection
// holds. A connection that has been closed is not an error here, and
// the export then names a table after its id, which is still an answer
// for a program that kept a result and let its connection go.
func (r *Rows) ArrowStream(out unsafe.Pointer, rowsPerBatch int) error {
	if r.h == nil {
		return misuse("the rows are closed")
	}
	if out == nil {
		return misuse("the stream to write into is nil")
	}
	if rowsPerBatch < 0 {
		return misuse("a batch of fewer than no rows")
	}

	var conn *C.zu_conn
	if r.conn != nil {
		// Read under the same lock every other use of the handle
		// takes, so that a Close on another goroutine waits for this
		// call rather than freeing the connection under it.
		r.conn.mu.RLock()
		defer r.conn.mu.RUnlock()
		conn = r.conn.h
	}

	var e *C.zu_error
	st := C.zu_result_arrow(conn, &r.h, C.uint64_t(rowsPerBatch),
		(*C.struct_ArrowArrayStream)(out), &e)
	// The engine wrote NULL back through the handle on every path,
	// which is what keeps a caller from freeing what it gave away.
	// This mirrors it into the Go side of the Rows: there is nothing
	// left to read, so there is nothing left to say there are rows.
	//
	// The cleanup goes with it, and this is the one place where
	// stopping it is not a tidiness. It holds the handle the engine
	// has just taken, so a Rows dropped after an export would
	// otherwise be a second free of a result somebody else now owns,
	// which is the mistake the NULL above exists to prevent on the
	// caller's side.
	r.drop.Stop()
	r.h = nil
	r.n = 0
	r.i = 0
	return fail(st, e)
}
