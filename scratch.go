package zu

/*
#include <zu.h>
*/
import "C"

// A scratch is where the C calls write their out-parameters.
//
// It exists for one reason. Every accessor in the ABI writes through a
// pointer, and a local variable whose address is handed to a cgo call
// escapes: the compiler cannot see what C does with the pointer, so it
// puts the variable on the heap. Reading one integer cell would then
// allocate twice, once for the cell handle and once for the integer,
// and a loop over ten thousand rows would allocate twenty thousand
// times for values none of which live past the loop body.
//
// A field of a struct that is already on the heap costs nothing to
// take the address of. One scratch hangs off each [Rows] and is reused
// for every cell of it, which is what makes scanning a column of
// integers allocate nothing at all.
//
// The reuse is why a [Rows] is single-goroutine, which it already was:
// two goroutines reading one result would be writing over each other's
// out-parameters here as well as racing the engine.
//
// A caller that needs a value to outlive the read copies it, which
// every path here already does: a string is copied into a Go string, a
// node into a [Node], a list into a slice.
type scratch struct {
	// val is the cell handle, and the item handle inside a list or a
	// record. What it points at belongs to the result.
	val *C.zu_value
	// The scalars, one per width the accessors write.
	i64  C.int64_t
	f64  C.double
	i32  C.int32_t
	kind C.int32_t
	off  C.int32_t
	u32  C.uint32_t
	src  C.uint64_t
	dst  C.uint64_t
	// txt and size are the borrowed bytes of a string or a field name,
	// which point into the result and are copied out before anything
	// else is read.
	txt  *C.char
	size C.size_t
}
