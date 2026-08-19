package zu

import (
	"iter"
	"reflect"
	"strings"
	"time"
)

// Collect reads a whole result into a slice of T, which is the call
// for a query whose answer fits in memory and whose shape the program
// knows.
//
//	type person struct {
//		Name string `zu:"name"`
//		ID   int64  `zu:"id"`
//	}
//	people, err := zu.Collect[person](rows)
//
// T may be a struct, and then its fields are matched to the columns by
// the `zu` tag or, with no tag, by name without regard to case. A
// column no field claims is skipped, which is what lets one struct
// read the results of two statements that differ by a column.
//
// T may also be a single value, and then the result has to have
// exactly one column:
//
//	names, err := zu.Collect[string](rows)
//
// Collect does not close the rows. The caller does, because the caller
// is the one who deferred it.
func Collect[T any](rows *Rows) ([]T, error) {
	out := make([]T, 0, rows.Len()-rows.i-1)
	for v, err := range Iter[T](rows) {
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// Iter is [Collect] one row at a time, for a result that is larger
// than the program wants to hold or a loop that stops early.
//
//	for p, err := range zu.Iter[person](rows) {
//		if err != nil {
//			return err
//		}
//		if p.Name == want {
//			break
//		}
//	}
//
// The sequence stops at the first failure, after yielding it. A
// caller that breaks out early leaves the rest of the result unread,
// which costs nothing: the rows are already in memory and closing the
// result is what frees them.
func Iter[T any](rows *Rows) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		var zero T
		p, err := planFor[T](rows)
		if err != nil {
			yield(zero, err)
			return
		}
		// One destination for the whole loop rather than one per row.
		// Handing a fresh &v to a scan would put every row on the heap,
		// since the destination crosses an interface and escapes;
		// yielding a copy of this one means the caller never sees the
		// buffer and the reuse cannot be observed.
		v := new(T)
		b := p.bind(v)
		for rows.Next() {
			*v = zero
			if err := b.read(rows); err != nil {
				yield(zero, rows.note(err))
				return
			}
			if !yield(*v, nil) {
				return
			}
		}
	}
}

// A plan is how one result's columns reach one Go type, worked out
// once for the whole result rather than at every row.
type plan struct {
	// fields is one entry per column: the index of the field it fills
	// in, or -1 for a column nothing claims.
	fields []int
	// scalar is set when T is a single value rather than a struct, in
	// which case the whole row is one column.
	scalar bool
}

// values are the types that are one value rather than a struct of
// columns, even though Go spells several of them as structs. A [Node]
// has two fields and is still one value, and a result of one node
// column collected into []Node must not try to fill Table from a
// column called table.
var values = map[reflect.Type]bool{
	reflect.TypeFor[time.Time]():     true,
	reflect.TypeFor[Node]():          true,
	reflect.TypeFor[Rel]():           true,
	reflect.TypeFor[Date]():          true,
	reflect.TypeFor[LocalTime]():     true,
	reflect.TypeFor[ZonedTime]():     true,
	reflect.TypeFor[LocalDateTime](): true,
	reflect.TypeFor[ZonedDateTime](): true,
	reflect.TypeFor[YearMonth]():     true,
	reflect.TypeFor[Graph]():         true,
	reflect.TypeFor[BindingTable]():  true,
}

// planFor works out how the columns of this result reach T.
func planFor[T any](rows *Rows) (plan, error) {
	t := reflect.TypeFor[T]()
	if t.Kind() != reflect.Struct || values[t] {
		if len(rows.cols) != 1 {
			return plan{}, misuse("a result of " + quoted(strings.Join(rows.cols, ", ")) +
				" has more than one column, so it needs a struct rather than a " + t.String())
		}
		return plan{scalar: true}, nil
	}

	fields := make([]int, len(rows.cols))
	claimed := 0
	for c, name := range rows.cols {
		fields[c] = fieldFor(t, name)
		if fields[c] >= 0 {
			claimed++
		}
	}
	if claimed == 0 {
		return plan{}, misuse("no field of " + t.String() + " matches a column of " +
			quoted(strings.Join(rows.cols, ", ")) + ", so every row would come back empty")
	}
	return plan{fields: fields}, nil
}

// fieldFor is the field of t that column name fills in, or -1. The tag
// wins, and without one the name matches without regard to case,
// because a column called name and a field called Name are the same
// thing said twice in two languages' conventions.
func fieldFor(t reflect.Type, name string) int {
	loose := -1
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		switch tag := f.Tag.Get("zu"); tag {
		case "":
			if loose < 0 && strings.EqualFold(f.Name, name) {
				loose = i
			}
		case "-":
		case name:
			return i
		}
	}
	return loose
}

// A bound is a plan aimed at one destination value. The addresses of
// the fields are taken once here rather than at every row, because
// each of them boxes a pointer into an interface and a result of ten
// thousand rows would otherwise do that ten thousand times over for
// destinations that never move.
type bound struct {
	plan
	// dest is the whole value, for the single-value case.
	dest any
	// addrs is one entry per column: a pointer to the field it fills
	// in, or nil for a column nothing claims.
	addrs []any
}

// bind aims a plan at the value the loop will read into and keeps
// reading it, which is what makes the reuse worth anything.
func (p plan) bind(dest any) bound {
	if p.scalar {
		return bound{plan: p, dest: dest}
	}
	el := reflect.ValueOf(dest).Elem()
	addrs := make([]any, len(p.fields))
	for c, f := range p.fields {
		if f >= 0 {
			addrs[c] = el.Field(f).Addr().Interface()
		}
	}
	return bound{plan: p, addrs: addrs}
}

// read fills the bound value from the current row.
func (b bound) read(rows *Rows) error {
	row := rows.Row()
	if b.scalar {
		v, err := row.cell(0)
		if err != nil {
			return err
		}
		return scan(&rows.sc, v, b.dest, rows.cols[0])
	}
	for c, d := range b.addrs {
		if d == nil {
			continue
		}
		v, err := row.cell(c)
		if err != nil {
			return err
		}
		if err := scan(&rows.sc, v, d, rows.cols[c]); err != nil {
			return err
		}
	}
	return nil
}
