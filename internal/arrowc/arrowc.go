// Package arrowc is the C side of the tests for the Arrow export.
//
// A stream is four function pointers, and calling one is what a
// consumer does with it. cgo will not call a function pointer from Go
// and does not allow cgo in a test file at all, so the calls live here,
// in a package the tests import. There is nothing in it a program
// outside this module would want: a program that wants Arrow wants the
// zuarrow module, which hands the stream to arrow-go and gets a reader
// back.
//
// What this package does not do is link the engine. It includes the
// header for the three structs of the C Data Interface and calls
// nothing but the pointers in them, so it builds on its own and its
// only tie to the library is that both agree on a layout. That
// agreement is the thing the tests are checking, and a C compiler
// laying the structs out for itself is what checks it rather than the
// Go side agreeing with the Go side.
package arrowc

/*
#cgo CFLAGS: -I${SRCDIR}/../../include

#include <stdlib.h>
#include <string.h>
#include <zu.h>

static int arrowc_schema(struct ArrowArrayStream *s, long *cols, char **first) {
  struct ArrowSchema schema;
  int rc = s->get_schema(s, &schema);
  if (rc != 0) {
    return rc;
  }
  *cols = (long)schema.n_children;
  *first = (schema.n_children > 0) ? strdup(schema.children[0]->name) : NULL;
  schema.release(&schema);
  return 0;
}

static int arrowc_drain(struct ArrowArrayStream *s, long *batches, long long *rows) {
  *batches = 0;
  *rows = 0;
  for (;;) {
    struct ArrowArray batch;
    batch.release = NULL;
    int rc = s->get_next(s, &batch);
    if (rc != 0) {
      return rc;
    }
    if (batch.release == NULL) {
      return 0;
    }
    (*batches)++;
    *rows += (long long)batch.length;
    batch.release(&batch);
  }
}

static void arrowc_release(struct ArrowArrayStream *s) {
  if (s->release != NULL) {
    s->release(s);
  }
}
*/
import "C"

import "unsafe"

// A Stream is room for an ArrowArrayStream, zeroed, which is what the
// C Data Interface asks a caller to hand over. It is in C memory rather
// than Go memory because what goes into it is written by the engine and
// read by C, and Go's collector has no business moving it.
type Stream struct {
	p *C.struct_ArrowArrayStream
}

// NewStream makes room for a stream. Free returns it.
func NewStream() *Stream {
	p := C.calloc(1, C.size_t(unsafe.Sizeof(C.struct_ArrowArrayStream{})))
	return &Stream{p: (*C.struct_ArrowArrayStream)(p)}
}

// Pointer is the address to hand to the export.
func (s *Stream) Pointer() unsafe.Pointer {
	return unsafe.Pointer(s.p)
}

// Schema is how many columns the stream carries and what the first one
// is called, which is the half of the schema these tests are about.
func (s *Stream) Schema() (cols int, first string, rc int) {
	var n C.long
	var name *C.char
	got := C.arrowc_schema(s.p, &n, &name)
	if name != nil {
		first = C.GoString(name)
		C.free(unsafe.Pointer(name))
	}
	return int(n), first, int(got)
}

// Drain reads the stream to its end, releasing every batch, and answers
// how many batches there were and how many rows they held.
func (s *Stream) Drain() (batches int, rows int64, rc int) {
	var n C.long
	var total C.longlong
	got := C.arrowc_drain(s.p, &n, &total)
	return int(n), int64(total), int(got)
}

// Free releases the stream if it was ever written and gives back the
// room it was in. It is safe on a stream nothing was exported into,
// since that one is still the zeroes it started as.
func (s *Stream) Free() {
	if s.p == nil {
		return
	}
	C.arrowc_release(s.p)
	C.free(unsafe.Pointer(s.p))
	s.p = nil
}
