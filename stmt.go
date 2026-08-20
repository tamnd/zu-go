package zu

/*
#include <zu.h>
*/
import "C"

import (
	"context"
	"math"
	"reflect"
	"runtime"
	"strconv"
	"time"
)

// A Stmt is a statement compiled once and run many times. The bindings
// live on it and survive an execution, so a loop rebinds only what
// changed, and the plan behind it is compiled once rather than at
// every call.
//
// A statement belongs to the connection it was prepared on. Using one
// after that connection closes answers [Closed] rather than following
// a dangling pointer, and closing it after that is still safe.
type Stmt struct {
	conn *Conn
	h    *C.zu_stmt
}

// Bind sets parameters without running the statement, for a caller
// that binds in one place and executes in another.
func (s *Stmt) Bind(args ...Arg) error {
	if s.h == nil {
		return misuse("the statement is closed")
	}
	for _, a := range args {
		if err := s.bind(a); err != nil {
			return err
		}
	}
	return nil
}

// Query binds the arguments, runs the statement and reads the whole
// result. Arguments given here are set before the run and stay set
// after it, which is what makes the second call in a loop cheaper than
// the first.
func (s *Stmt) Query(ctx context.Context, args ...Arg) (*Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.h == nil {
		return nil, misuse("the statement is closed")
	}
	s.conn.mu.RLock()
	defer s.conn.mu.RUnlock()
	if err := s.Bind(args...); err != nil {
		return nil, err
	}

	w := s.conn.watch(ctx)
	var h *C.zu_result
	var e *C.zu_error
	st := C.zu_execute(s.h, &h, &e)
	if err := caused(fail(st, e), w.end()); err != nil {
		return nil, err
	}
	return newRows(s.conn, h)
}

// Exec binds the arguments, runs the statement and throws its result
// away, which is what a write is.
func (s *Stmt) Exec(ctx context.Context, args ...Arg) error {
	rows, err := s.Query(ctx, args...)
	if err != nil {
		return err
	}
	return rows.Close()
}

// Close releases the statement. It is safe to call twice, and safe
// after the connection it was prepared on has closed.
func (s *Stmt) Close() error {
	if s.h == nil {
		return nil
	}
	C.zu_stmt_close(s.h)
	s.h = nil
	return nil
}

// bind sets one parameter. The value is switched on rather than
// reflected over for everything a program actually binds, so the loop
// that runs one statement a million times pays no reflection.
func (s *Stmt) bind(a Arg) error {
	name, size := lend(a.Name)
	defer runtime.KeepAlive(a.Name)

	switch v := a.Value.(type) {
	case nil:
		return fail(C.zu_bind_null(s.h, name, size), nil)
	case bool:
		n := C.int(0)
		if v {
			n = 1
		}
		return fail(C.zu_bind_bool(s.h, name, size, n), nil)
	case int:
		return fail(C.zu_bind_i64(s.h, name, size, C.int64_t(v)), nil)
	case int8:
		return fail(C.zu_bind_i64(s.h, name, size, C.int64_t(v)), nil)
	case int16:
		return fail(C.zu_bind_i64(s.h, name, size, C.int64_t(v)), nil)
	case int32:
		return fail(C.zu_bind_i64(s.h, name, size, C.int64_t(v)), nil)
	case int64:
		return fail(C.zu_bind_i64(s.h, name, size, C.int64_t(v)), nil)
	case uint8:
		return fail(C.zu_bind_i64(s.h, name, size, C.int64_t(v)), nil)
	case uint16:
		return fail(C.zu_bind_i64(s.h, name, size, C.int64_t(v)), nil)
	case uint32:
		return fail(C.zu_bind_i64(s.h, name, size, C.int64_t(v)), nil)
	case uint:
		if uint64(v) > math.MaxInt64 {
			return unbindable(a.Name, "is larger than the integers this engine holds")
		}
		return fail(C.zu_bind_i64(s.h, name, size, C.int64_t(v)), nil)
	case uint64:
		if v > math.MaxInt64 {
			return unbindable(a.Name, "is larger than the integers this engine holds")
		}
		return fail(C.zu_bind_i64(s.h, name, size, C.int64_t(v)), nil)
	case float32:
		return fail(C.zu_bind_f64(s.h, name, size, C.double(v)), nil)
	case float64:
		return fail(C.zu_bind_f64(s.h, name, size, C.double(v)), nil)
	case string:
		p, n := lend(v)
		st := C.zu_bind_str(s.h, name, size, p, n)
		runtime.KeepAlive(v)
		return fail(st, nil)
	case []byte:
		p, n := lend(string(v))
		st := C.zu_bind_str(s.h, name, size, p, n)
		return fail(st, nil)

	case time.Time:
		// An instant with the zone it was written in, which is the
		// only reading of a time.Time that keeps both halves of it.
		_, offset := v.Zone()
		return s.temporal(name, size, KindZonedDateTime, v.UnixNano(), int32(offset/60))
	case time.Duration:
		return s.temporal(name, size, KindDayTimeDuration, int64(v), 0)
	case Date:
		return s.temporal(name, size, KindDate, int64(v.Days), 0)
	case LocalTime:
		return s.temporal(name, size, KindLocalTime, v.Nanos, 0)
	case ZonedTime:
		return s.temporal(name, size, KindZonedTime, v.Nanos, v.Offset)
	case LocalDateTime:
		return s.temporal(name, size, KindLocalDateTime, v.Nanos, 0)
	case ZonedDateTime:
		return s.temporal(name, size, KindZonedDateTime, v.Nanos, v.Offset)
	case YearMonth:
		return s.temporal(name, size, KindYearMonthDuration, v.Months, 0)
	}

	return s.bindReflect(a)
}

// temporal binds one of the seven temporal kinds, which all cross as a
// kind, a count in the unit that kind implies, and an offset the five
// kinds without a zone ignore.
func (s *Stmt) temporal(name *C.char, size C.size_t, kind TemporalKind, count int64, offset int32) error {
	return fail(C.zu_bind_temporal(s.h, name, size, C.int32_t(kind), C.int64_t(count), C.int32_t(offset)), nil)
}

// bindReflect is the value this package did not name: a nil pointer,
// which binds as a null, a pointer to something it did name, and a
// named type over one of the basic kinds, so that `type UserID int64`
// binds without a conversion at the call site.
func (s *Stmt) bindReflect(a Arg) error {
	rv := reflect.ValueOf(a.Value)
	switch rv.Kind() {
	case reflect.Pointer:
		if rv.IsNil() {
			return s.bind(Arg{Name: a.Name, Value: nil})
		}
		return s.bind(Arg{Name: a.Name, Value: rv.Elem().Interface()})
	case reflect.Bool:
		return s.bind(Arg{Name: a.Name, Value: rv.Bool()})
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return s.bind(Arg{Name: a.Name, Value: rv.Int()})
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return s.bind(Arg{Name: a.Name, Value: rv.Uint()})
	case reflect.Float32, reflect.Float64:
		return s.bind(Arg{Name: a.Name, Value: rv.Float()})
	case reflect.String:
		return s.bind(Arg{Name: a.Name, Value: rv.String()})
	}
	return unbindable(a.Name, "is a "+reflect.TypeOf(a.Value).String()+", which is not a value this engine takes as a parameter")
}

// unbindable is the failure for a parameter this client will not send,
// which is a mistake on the Go side and carries no condition because
// no statement ran.
func unbindable(name string, why string) error {
	return misuse("the parameter " + strconv.Quote(name) + " " + why)
}
