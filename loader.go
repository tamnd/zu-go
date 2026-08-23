package zu

/*
#include <stdlib.h>
#include <string.h>
#include <zu.h>
*/
import "C"

import (
	"context"
	"runtime"
	"sync"
	"time"
	"unsafe"
)

// A Loader builds a database out of whole columns and an edge list.
//
// It is the way values get into a database that does not exist yet. A
// statement is not: CREATE and INSERT need a table and no statement
// makes one, so a program holding data and an empty file has nowhere
// else to go. A database that already exists is what [Conn.Exec] and a
// statement are for.
//
// A loader is columnar for the same reason a [Rows] is. One call per
// column and not one per cell, and the arrays it takes are the arrays
// a [Rows] hands back: [Loader.Int64s] against [Rows.Int64s],
// [Loader.Float64s] against [Rows.Float64s]. That symmetry is the
// point of the shape rather than a coincidence of naming, and a
// program that read a column out of one database and wants it in
// another passes what it was given.
//
//	loader, err := zu.NewLoader("social.zu1")
//	if err != nil {
//		return err
//	}
//	defer loader.Close()
//
//	if err := loader.Table("Person", "Knows", 3); err != nil {
//		return err
//	}
//	if err := loader.Strings("name", []string{"ada", "grace", "lynn"}); err != nil {
//		return err
//	}
//	if err := loader.Edges([]uint32{0, 1}, []uint32{1, 2}); err != nil {
//		return err
//	}
//	if err := loader.Finish(ctx); err != nil {
//		return err
//	}
//
// The order is fixed: [NewLoader], then [Loader.Table], then columns
// and edges in any order and as many calls as you like, then
// [Loader.Finish]. A column before the table is a misuse, and so is a
// column whose length disagrees with the row count the table was
// given, reported at the call that passed it rather than at the finish
// so that the error names the column while you still know which one
// you were building.
//
// Nothing reaches the file until Finish, so a load either happened or
// did not. Every array is copied by the engine as it is passed, so a
// caller may reuse or drop its own slices as soon as a call returns.
//
// A Loader is used from one goroutine, like a [Conn], and for the same
// reason: a second goroutine inside a call answers [Concurrent] rather
// than corrupting the columns.
type Loader struct {
	// mu is held for reading by every call that uses the handle and
	// for writing by Close, so that a close cannot free a handle
	// another goroutine is inside a call with. It does not serialise
	// those calls: two at once is a mistake the engine reports.
	mu sync.RWMutex
	h  *C.zu_loader
	// drop frees the handle if this Loader is collected without Close
	// having been called. See the comment in cleanup.go.
	drop runtime.Cleanup
}

// NewLoader starts a load into a new database at path.
//
// The path must not exist. A bulk load builds a database rather than
// adding to one, so a path that already holds one is a caller who
// meant a different path, and opening it instead would be the worst
// available reading of the call.
//
// A loader closed before [Loader.Finish] wrote none of its rows, and
// what it leaves at the path is worth knowing exactly. It is not an
// empty file. It is a database, and the node table [Loader.Table]
// named is already in it with no rows in that table. So an
// interrupted load leaves a path that a second load may not start on,
// since this call refuses a path that exists, and that opens as a
// perfectly good graph with nothing in it.
//
// A program that retries a load therefore removes the path first
// rather than testing whether it is there, because a partial load and
// a finished load of no rows look the same from outside. Loading into
// a temporary path and renaming it once [Loader.Finish] returns is the
// shape that does not have the problem at all.
func NewLoader(path string) (*Loader, error) {
	p, n := lend(path)
	var h *C.zu_loader
	var e *C.zu_error
	st := C.zu_loader_create(p, n, &h, &e)
	runtime.KeepAlive(path)
	if err := fail(st, e); err != nil {
		return nil, err
	}
	l := &Loader{h: h}
	l.drop = onDrop(l, freeLoader, h)
	return l, nil
}

// Table names the node table the rows go into, the rel table the edges
// go into, and how many rows the node table has.
//
// The count is given rather than counted from the first column, so a
// column with a value missing is an error and not a shorter table. One
// table per loader.
func (l *Loader) Table(nodes, edges string, rows uint64) error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.h == nil {
		return misuse("the loader is closed")
	}
	np, nn := lend(nodes)
	ep, en := lend(edges)
	var e *C.zu_error
	st := C.zu_loader_table(l.h, np, nn, ep, en, C.uint64_t(rows), &e)
	runtime.KeepAlive(nodes)
	runtime.KeepAlive(edges)
	return fail(st, e)
}

// Edges adds the edges between the rows, as the row each one starts at
// and the row it ends at.
//
// Two slices rather than a slice of pairs, because a program that has
// them in columns passes what it has and one that has them in pairs
// writes the loop either way. The two must be the same length.
//
// It appends, so call it as often as you like. The engine sorts and
// deduplicates at the finish, which means a caller does not have to
// and a pair given twice is one edge.
func (l *Loader) Edges(from, to []uint32) error {
	if len(from) != len(to) {
		return misuse("an edge list of two columns of different lengths")
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.h == nil {
		return misuse("the loader is closed")
	}
	var e *C.zu_error
	st := C.zu_loader_edges(l.h, (*C.uint32_t)(u32s(from)), (*C.uint32_t)(u32s(to)),
		C.uint64_t(len(from)), &e)
	runtime.KeepAlive(from)
	runtime.KeepAlive(to)
	return fail(st, e)
}

// Int64s adds a column of integers.
//
// The slice crosses without a copy on this side: the engine reads it
// and takes its own copy before the call returns, so the caller may
// reuse the slice immediately.
func (l *Loader) Int64s(name string, values []int64) error {
	return l.column(name, func(h *C.zu_loader, p *C.char, n C.size_t, e **C.zu_error) C.zu_status {
		st := C.zu_loader_col_i64(h, p, n, (*C.int64_t)(i64s(values)), C.uint64_t(len(values)), e)
		runtime.KeepAlive(values)
		return st
	})
}

// Float64s adds a column of doubles, on the same terms as
// [Loader.Int64s].
func (l *Loader) Float64s(name string, values []float64) error {
	return l.column(name, func(h *C.zu_loader, p *C.char, n C.size_t, e **C.zu_error) C.zu_status {
		st := C.zu_loader_col_f64(h, p, n, (*C.double)(f64s(values)), C.uint64_t(len(values)), e)
		runtime.KeepAlive(values)
		return st
	})
}

// Bools adds a column of truth values.
//
// This one copies, because the ABI carries a boolean as an int32 and a
// Go bool is a byte. It is the one column type where the array is
// rebuilt rather than lent.
func (l *Loader) Bools(name string, values []bool) error {
	wide := make([]C.int32_t, len(values))
	for i, v := range values {
		if v {
			wide[i] = 1
		}
	}
	return l.column(name, func(h *C.zu_loader, p *C.char, n C.size_t, e **C.zu_error) C.zu_status {
		st := C.zu_loader_col_bool(h, p, n, (*C.int32_t)(i32s(wide)), C.uint64_t(len(wide)), e)
		runtime.KeepAlive(wide)
		return st
	})
}

// Strings adds a column of text.
//
// Every string is checked for UTF-8 by the engine rather than read
// back later as something no query could return, so a column holding
// bytes that are not text is refused here.
//
// The bytes are copied into memory this call owns and frees, because
// an array of pointers into Go strings is not something a Go program
// may hand to C: the runtime is free to move what they point at. That
// is one copy of the text on the way through, and it is the reason
// this is the one column call that allocates.
func (l *Loader) Strings(name string, values []string) error {
	if len(values) == 0 {
		return l.column(name, func(h *C.zu_loader, p *C.char, n C.size_t, e **C.zu_error) C.zu_status {
			return C.zu_loader_col_str(h, p, n, nil, nil, 0, e)
		})
	}

	total := 0
	for _, v := range values {
		total += len(v)
	}
	// One allocation for all the bytes and two for the two arrays that
	// describe them, rather than one malloc per string. A column of a
	// million short strings would otherwise be a million calls to the
	// allocator, which is exactly the per-value cost this whole
	// columnar shape exists to avoid.
	//
	// A total of zero still asks for a byte, because malloc(0) is
	// allowed to answer NULL and a NULL here would read as the failure
	// below rather than as a column of empty strings.
	bytes := C.malloc(C.size_t(max(total, 1)))
	ptrs := C.malloc(C.size_t(len(values)) * C.size_t(unsafe.Sizeof((*C.char)(nil))))
	lens := C.malloc(C.size_t(len(values)) * C.size_t(unsafe.Sizeof(C.size_t(0))))
	if bytes == nil || ptrs == nil || lens == nil {
		C.free(bytes)
		C.free(ptrs)
		C.free(lens)
		return misuse("out of memory building a column of strings")
	}
	defer C.free(bytes)
	defer C.free(ptrs)
	defer C.free(lens)

	buf := unsafe.Slice((*byte)(bytes), max(total, 1))
	at := 0
	for i, v := range values {
		copy(buf[at:], v)
		// A pointer into the middle of the one buffer, and for an
		// empty string a pointer to where it would have started rather
		// than nil, so that the two arrays say the same thing about
		// every row.
		*(**C.char)(unsafe.Add(ptrs, uintptr(i)*unsafe.Sizeof((*C.char)(nil)))) =
			(*C.char)(unsafe.Add(bytes, at))
		*(*C.size_t)(unsafe.Add(lens, uintptr(i)*unsafe.Sizeof(C.size_t(0)))) = C.size_t(len(v))
		at += len(v)
	}

	return l.column(name, func(h *C.zu_loader, p *C.char, n C.size_t, e **C.zu_error) C.zu_status {
		return C.zu_loader_col_str(h, p, n, (**C.char)(ptrs), (*C.size_t)(lens),
			C.uint64_t(len(values)), e)
	})
}

// The five temporal columns, which are the five temporal types a
// stored column can hold.
//
// There is no method for a zoned time or a zoned datetime, and that is
// the ABI's refusal written into the types rather than left to run
// time: a stored column has nowhere to keep the offset that makes
// those two what they are, so [ZonedTime] and [ZonedDateTime] are
// values a query can produce and not values a column can hold. A
// program with one of them has to say what it means to do with the
// offset, and dropping it silently is the one thing that must not
// happen.
//
// Each takes the type [Rows] hands back for the same column, so a
// column read out of one database goes into another unchanged.

// Dates adds a column of days.
func (l *Loader) Dates(name string, values []Date) error {
	return l.temporal(name, C.ZU_TEMPORAL_DATE, counts(values, func(v Date) int64 {
		return int64(v.Days)
	}))
}

// LocalTimes adds a column of times of day, as nanoseconds since
// midnight.
func (l *Loader) LocalTimes(name string, values []LocalTime) error {
	return l.temporal(name, C.ZU_TEMPORAL_LOCAL_TIME, counts(values, func(v LocalTime) int64 {
		return v.Nanos
	}))
}

// LocalDateTimes adds a column of instants with no zone, as
// nanoseconds from the epoch.
func (l *Loader) LocalDateTimes(name string, values []LocalDateTime) error {
	return l.temporal(name, C.ZU_TEMPORAL_LOCAL_DATETIME, counts(values, func(v LocalDateTime) int64 {
		return v.Nanos
	}))
}

// YearMonths adds a column of year-month durations, as a count of
// months.
//
// A month is not a number of days and adding one to a date is not the
// same operation as adding thirty of them, which is why this and
// [Loader.Durations] are two columns and not one.
func (l *Loader) YearMonths(name string, values []YearMonth) error {
	return l.temporal(name, C.ZU_TEMPORAL_DURATION_YEAR_MONTH, counts(values, func(v YearMonth) int64 {
		return v.Months
	}))
}

// Durations adds a column of day-time durations, as nanoseconds.
func (l *Loader) Durations(name string, values []time.Duration) error {
	return l.temporal(name, C.ZU_TEMPORAL_DURATION_DAY_TIME, counts(values, func(v time.Duration) int64 {
		return int64(v)
	}))
}

// Finish writes it all. The database is on disk when this returns and
// [Open] on the same path reads it.
//
// The context is checked before the write starts and not during it.
// The ABI has no interrupt for a loader, which is a gap worth knowing
// about rather than papering over: a load of a hundred million rows
// cancelled halfway would still be running when this returned, and
// answering the caller before the file was written would be worse than
// making them wait. What the check buys is a context already cancelled
// costing nothing.
//
// After a Finish, and after a Finish that failed, every call on this
// loader answers [Closed] and only [Loader.Close] is left.
func (l *Loader) Finish(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.h == nil {
		return misuse("the loader is closed")
	}
	var e *C.zu_error
	return fail(C.zu_loader_finish(l.h, &e), e)
}

// Close gives the loader's memory back. A loader closed before
// [Loader.Finish] wrote none of its rows, and the file [NewLoader]
// created is still there for the caller to remove. See [NewLoader] for
// what that file actually is, which is not what its name suggests.
//
// Close is safe to call twice and waits for a call already inside the
// engine. A loader dropped without it is freed by the collector
// instead, which is the backstop and not the plan: until that happens
// every column handed to it is still held.
func (l *Loader) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.h == nil {
		return nil
	}
	// Stopped before the free, which is the order that cannot free
	// twice. See [DB.Close].
	l.drop.Stop()
	C.zu_loader_free(l.h)
	l.h = nil
	return nil
}

// column is the shape every column call has: take the lock, check the
// handle, lend the name, and call. What differs between them is the
// one line that names the ABI call and the array it passes, which is
// what the closure holds, and each one keeps its own values alive
// across the call because the pointer it hands to C is derived through
// unsafe.Pointer and the compiler cannot otherwise see that the slice
// is still in use.
func (l *Loader) column(
	name string,
	call func(*C.zu_loader, *C.char, C.size_t, **C.zu_error) C.zu_status,
) error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.h == nil {
		return misuse("the loader is closed")
	}
	p, n := lend(name)
	var e *C.zu_error
	st := call(l.h, p, n, &e)
	runtime.KeepAlive(name)
	return fail(st, e)
}

// temporal is the one call the five temporal columns share, with the
// kind saying which unit the counts are in.
func (l *Loader) temporal(name string, kind C.int32_t, values []C.int64_t) error {
	return l.column(name, func(h *C.zu_loader, p *C.char, n C.size_t, e **C.zu_error) C.zu_status {
		st := C.zu_loader_col_temporal(h, p, n, kind, (*C.int64_t)(i64c(values)),
			C.uint64_t(len(values)), e)
		runtime.KeepAlive(values)
		return st
	})
}

// counts turns a slice of one of the temporal types into the counts
// the ABI takes. Every one of them is a single field, so this is a
// copy of a slice of integers and not a conversion.
func counts[T any](values []T, of func(T) int64) []C.int64_t {
	out := make([]C.int64_t, len(values))
	for i, v := range values {
		out[i] = C.int64_t(of(v))
	}
	return out
}

// The address of the first element of a slice, or nil for an empty
// one, which is what the ABI reads as a column of no values rather
// than as a mistake.
//
// Five of them rather than one generic, because the result is cast to
// a C pointer type at the call and a generic that returned
// unsafe.Pointer would put the cast at every call site instead. None
// of these slices holds a Go pointer, which is what makes handing the
// address to C allowed at all.

func i64s(v []int64) unsafe.Pointer {
	if len(v) == 0 {
		return nil
	}
	return unsafe.Pointer(&v[0])
}

func f64s(v []float64) unsafe.Pointer {
	if len(v) == 0 {
		return nil
	}
	return unsafe.Pointer(&v[0])
}

func i32s(v []C.int32_t) unsafe.Pointer {
	if len(v) == 0 {
		return nil
	}
	return unsafe.Pointer(&v[0])
}

func i64c(v []C.int64_t) unsafe.Pointer {
	if len(v) == 0 {
		return nil
	}
	return unsafe.Pointer(&v[0])
}

func u32s(v []uint32) unsafe.Pointer {
	if len(v) == 0 {
		return nil
	}
	return unsafe.Pointer(&v[0])
}
