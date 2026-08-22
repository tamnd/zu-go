//go:build cgo

// Package leakgate is a leak on purpose, so that the job which counts
// leaks is a job somebody has watched fail.
//
// A gate that has never fired is a gate nobody knows the shape of. The
// sanitizer job runs the whole suite with the allocator watched and
// takes a clean report as the answer, and a clean report is also what
// arrives when the sanitizer was never linked in, when the flag was
// spelled wrong, or when a runner quietly dropped the step. The step
// beside it runs this, which leaks a megabyte and nothing else, and
// treats a passing run as the failure.
//
// It is C memory and not Go memory, because Go memory is not what this
// is about: the client holds allocations made inside the engine and
// handed over a pointer at a time, and this is the smallest thing
// shaped like one. That is also why the whole package is behind the
// cgo tag: with cgo off there is no boundary to leak across, and the
// package drops out of the pattern the way the client itself does.
//
// Nothing here runs unless ZU_LEAK_GATE is set, so the package is
// harmless in the suite it lives in.
package leakgate

/*
#include <stdlib.h>
*/
import "C"

// Bytes is how much the leak is. Large enough that no report could
// round it away and no allocator could have been holding it anyway.
const Bytes = 1 << 20

// Leak allocates and drops it. The pointer is not returned, not
// stored, and not freed, which between them are the whole definition
// of a leak.
//
//go:noinline
func Leak() {
	p := C.malloc(C.size_t(Bytes))
	// Written into, so that no compiler on either side of the
	// boundary can decide the allocation was never used and remove
	// it. One byte is enough to make it a fact.
	*(*byte)(p) = 1
}
