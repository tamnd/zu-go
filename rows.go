package zu

/*
#include <zu.h>
*/
import "C"

import (
	"iter"
	"strconv"
	"unsafe"
)

// Rows is the answer to a statement. It owns its rows outright, so it
// stays readable after the connection that produced it has gone back
// to a pool, and it has to be closed: the engine's buffers are held
// until it is.
//
// A Rows is not safe for use from two goroutines at once, which is the
// same rule as the connection it came from and for the same reason.
type Rows struct {
	h    *C.zu_result
	cols []string
	n    int64
	i    int64
	err  error
	// sc is where the accessors write, reused for every cell of this
	// result so that reading one costs no allocation. See [scratch].
	sc scratch
}

// newRows takes ownership of a result handle and reads the column
// names off it once, since they are what every row is read through and
// they do not change between rows.
func newRows(h *C.zu_result) (*Rows, error) {
	r := &Rows{h: h, i: -1, n: int64(C.zu_result_rows(h))}
	cols := int(C.zu_result_cols(h))
	r.cols = make([]string, cols)
	for c := range cols {
		var p *C.char
		var n C.size_t
		if err := fail(C.zu_result_col_name(h, C.uint32_t(c), &p, &n), nil); err != nil {
			C.zu_result_free(h)
			return nil, err
		}
		r.cols[c] = text(p, n)
	}
	return r, nil
}

// Columns are the names the statement gave its columns, in order.
func (r *Rows) Columns() []string {
	return r.cols
}

// Len is how many rows the result holds. It is known before the first
// one is read, because the engine hands back a whole result rather
// than a cursor.
func (r *Rows) Len() int64 {
	return r.n
}

// Next moves to the next row and reports whether there is one, which
// is the shape a Go program expects even though nothing here blocks.
//
//	for rows.Next() {
//		if err := rows.Scan(&name); err != nil {
//			return err
//		}
//	}
func (r *Rows) Next() bool {
	if r.i >= r.n {
		return false
	}
	r.i++
	return r.i < r.n
}

// Row is the row Next moved to, for a caller that would rather pass it
// somewhere than scan it here.
func (r *Rows) Row() Row {
	return Row{rows: r, i: r.i}
}

// All is the rows as a range-over-func sequence, which is the loop
// most programs want:
//
//	for row := range rows.All() {
//		if err := row.Scan(&name, &id); err != nil {
//			return err
//		}
//	}
//	if err := rows.Err(); err != nil {
//		return err
//	}
//
// It reads nothing on its own, so it cannot fail on its own. What can
// fail is the scan inside the loop, and [Rows.Err] is the backstop for
// a loop that did not check every one of them.
func (r *Rows) All() iter.Seq[Row] {
	return func(yield func(Row) bool) {
		for r.Next() {
			if !yield(Row{rows: r, i: r.i}) {
				return
			}
		}
	}
}

// Err is the first failure any read on these rows reported. It is
// there for the loop that scanned without checking, and it stays nil
// for a result that was read cleanly.
func (r *Rows) Err() error {
	return r.err
}

// GQLStatus is the completion condition of the statement that produced
// these rows: "00000" for one that answered with columns and "00001",
// successful completion with the result omitted, for one that had none
// to give back. It is the half of the diagnostic envelope that a
// caller reading rows and errors could not otherwise see.
func (r *Rows) GQLStatus() string {
	if r.h == nil {
		return ""
	}
	var n C.size_t
	return text(C.zu_result_gqlstatus(r.h, &n), n)
}

// Notices are the conditions the statement raised and carried on
// through. An exception replaces a result and arrives as an error from
// the call; a warning rides along with a result, because a statement
// that dropped a null out of an aggregate still has rows to give you
// and the standard still wants you told.
//
// Almost every statement raises none, so a program that asks and finds
// nothing has paid for one call.
func (r *Rows) Notices() []*Error {
	if r.h == nil {
		return nil
	}
	n := uint32(C.zu_result_notices(r.h))
	if n == 0 {
		return nil
	}
	out := make([]*Error, 0, n)
	for i := uint32(0); i < n; i++ {
		var e *C.zu_error
		if C.zu_result_notice(r.h, C.uint32_t(i), &e) != C.ZU_OK || e == nil {
			break
		}
		out = append(out, take(C.ZU_OK, e))
	}
	return out
}

// Scan reads the current row into the destinations, one per column
// asked for. See [Row.Scan] for what a destination may be.
func (r *Rows) Scan(dest ...any) error {
	return r.Row().Scan(dest...)
}

// Close frees the result. It is safe to call twice, and it is what
// every slice the columnar readers handed back stops being valid at.
func (r *Rows) Close() error {
	if r.h == nil {
		return nil
	}
	C.zu_result_free(r.h)
	r.h = nil
	return nil
}

// note records the first failure a read reported, so that [Rows.Err]
// has something to say at the end of a loop that did not check.
func (r *Rows) note(err error) error {
	if err != nil && r.err == nil {
		r.err = err
	}
	return err
}

// A Row is one row of a result. It is a position and not a copy, so it
// is valid until the next [Rows.Next] and until [Rows.Close], and
// keeping one past either reads a row that is no longer there.
type Row struct {
	rows *Rows
	i    int64
}

// Scan reads the row into the destinations, one per column, in the
// order the statement named them. Fewer destinations than columns is
// allowed and reads the first ones; more is an error, since there is
// nothing to put in the rest.
//
// A destination may be a pointer to bool, to any of the sized integers,
// to float32 or float64, to string or []byte, to any of the graph and
// temporal types, to [time.Time] or [time.Duration], or to any, which
// takes whatever the cell holds. A null needs somewhere that can hold
// one, which is a pointer to a pointer or a pointer to any; a null
// scanned into an int is an error naming the column rather than a
// zero.
func (row Row) Scan(dest ...any) error {
	r := row.rows
	if row.i < 0 || row.i >= r.n {
		return r.note(misuse("Scan was called with no current row, which is a loop that did not call Next"))
	}
	if len(dest) > len(r.cols) {
		return r.note(misuse("Scan was given " + strconv.Itoa(len(dest)) +
			" destinations for a result of " + strconv.Itoa(len(r.cols)) + " columns"))
	}
	for c, d := range dest {
		v, err := row.cell(c)
		if err != nil {
			return r.note(err)
		}
		if err := scan(&r.sc, v, d, r.cols[c]); err != nil {
			return r.note(err)
		}
	}
	return nil
}

// Value is one cell of the row as the Go value that means the same
// thing. See [value] for the mapping.
func (row Row) Value(col int) (any, error) {
	v, err := row.cell(col)
	if err != nil {
		return nil, row.rows.note(err)
	}
	got, err := value(&row.rows.sc, v)
	return got, row.rows.note(err)
}

// Values is the whole row, which is what a program that does not know
// the shape of its result ahead of time reads.
func (row Row) Values() ([]any, error) {
	out := make([]any, len(row.rows.cols))
	for c := range out {
		v, err := row.Value(c)
		if err != nil {
			return nil, err
		}
		out[c] = v
	}
	return out, nil
}

// Type is what the cell holds, for a program that would rather ask
// than scan and find out.
func (row Row) Type(col int) (Type, error) {
	r := row.rows
	if r.h == nil {
		return 0, r.note(misuse("the result is closed"))
	}
	st := C.zu_result_cell_type(r.h, C.uint64_t(row.i), C.uint32_t(col), &r.sc.i32)
	if err := fail(st, nil); err != nil {
		return 0, r.note(err)
	}
	return Type(r.sc.i32), nil
}

// cell borrows one cell from the result. What comes back points into
// the result's own rows and is good until [Rows.Close].
func (row Row) cell(col int) (*C.zu_value, error) {
	r := row.rows
	if r.h == nil {
		return nil, misuse("the result is closed")
	}
	if col < 0 || col >= len(r.cols) {
		return nil, misuse("column " + strconv.Itoa(col) + " of a result with " +
			strconv.Itoa(len(r.cols)) + " columns")
	}
	if err := fail(C.zu_result_cell(r.h, C.uint64_t(row.i), C.uint32_t(col), &r.sc.val), nil); err != nil {
		return nil, err
	}
	return r.sc.val, nil
}

// The columnar readers below are the hot path. They hand back the
// engine's own buffer for a whole column, contiguous and without a
// copy, which is a slice of Go memory only in the sense that Go can
// index it: it belongs to the result and is valid exactly until
// [Rows.Close]. A program that needs one to outlive the result copies
// it, which is the copy it was making anyway on the way into a slice
// of its own. The zulint analyzer in this module flags the use after
// Close, since the compiler cannot.
//
// A null reads as zero in all three of them, which [Rows.Valid] is
// what tells apart.

// Int64s is a whole column of integers, without a copy. Bools read
// here too, as zero and one, because a column is one array and
// something has to go in every slot. A result with no rows answers
// nil, which is a slice of no rows rather than a failure.
func (r *Rows) Int64s(col int) ([]int64, error) {
	if r.h == nil {
		return nil, misuse("the result is closed")
	}
	var p *C.int64_t
	st := C.zu_result_col_i64(r.h, C.uint32_t(col), &p)
	if st == C.ZU_DONE {
		return nil, nil
	}
	if err := fail(st, nil); err != nil {
		return nil, err
	}
	return unsafe.Slice((*int64)(unsafe.Pointer(p)), r.n), nil
}

// Float64s is a whole column of floats, without a copy. Integers read
// here too, converted, which is the one place this client widens a
// value without being asked: a column is one array of one type.
func (r *Rows) Float64s(col int) ([]float64, error) {
	if r.h == nil {
		return nil, misuse("the result is closed")
	}
	var p *C.double
	st := C.zu_result_col_f64(r.h, C.uint32_t(col), &p)
	if st == C.ZU_DONE {
		return nil, nil
	}
	if err := fail(st, nil); err != nil {
		return nil, err
	}
	return unsafe.Slice((*float64)(unsafe.Pointer(p)), r.n), nil
}

// NodeOffsets is a whole column of node row offsets, without a copy.
// A node is not an integer here: reading one as its offset is what
// this is for, and doing it quietly through [Rows.Int64s] is how a
// client ends up handing an internal row number to a user who asked
// for an identity.
func (r *Rows) NodeOffsets(col int) ([]uint64, error) {
	if r.h == nil {
		return nil, misuse("the result is closed")
	}
	var p *C.uint64_t
	st := C.zu_result_col_node_offset(r.h, C.uint32_t(col), &p)
	if st == C.ZU_DONE {
		return nil, nil
	}
	if err := fail(st, nil); err != nil {
		return nil, err
	}
	return unsafe.Slice((*uint64)(unsafe.Pointer(p)), r.n), nil
}

// Valid is which rows of a column hold a value, one byte each and
// nonzero for a row that does. It is how the zeroes the columnar
// readers put in a null's slot are told from the zeroes that are
// values.
func (r *Rows) Valid(col int) ([]byte, error) {
	if r.h == nil {
		return nil, misuse("the result is closed")
	}
	var p *C.uint8_t
	st := C.zu_result_col_valid(r.h, C.uint32_t(col), &p)
	if st == C.ZU_DONE {
		return nil, nil
	}
	if err := fail(st, nil); err != nil {
		return nil, err
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(p)), r.n), nil
}
