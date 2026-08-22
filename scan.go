package zu

/*
#include <zu.h>
*/
import "C"

import (
	"math"
	"reflect"
	"strconv"
	"time"
)

// scan reads one cell into one destination. The type of the cell is
// read first and the destination is switched on second, so that the
// common destinations are reached without the value being boxed on the
// way: scanning a column of integers into an *int64 allocates nothing.
//
// The column name is carried only so that a failure can say which
// column it was, which is the difference between a message a caller
// can act on and one they have to bisect for.
func scan(sc *scratch, v *C.zu_value, dest any, col string) error {
	t := Type(C.zu_value_type(v))
	if t == TypeNull {
		return scanNull(dest, col)
	}

	switch d := dest.(type) {
	case *any:
		got, err := value(sc, v)
		if err != nil {
			return err
		}
		*d = got
		return nil

	case *bool:
		if t != TypeBool {
			return mismatch(col, t, dest)
		}
		if err := fail(C.zu_value_bool(v, &sc.i32), nil); err != nil {
			return err
		}
		*d = sc.i32 != 0
		return nil

	case *int64:
		n, err := integer(sc, v, t, col, dest)
		if err != nil {
			return err
		}
		*d = n
		return nil
	case *int:
		n, err := integer(sc, v, t, col, dest)
		if err != nil {
			return err
		}
		if strconv.IntSize == 32 && (n > math.MaxInt32 || n < math.MinInt32) {
			return tooWide(col, n, dest)
		}
		*d = int(n)
		return nil
	case *int32:
		n, err := integer(sc, v, t, col, dest)
		if err != nil {
			return err
		}
		if n > math.MaxInt32 || n < math.MinInt32 {
			return tooWide(col, n, dest)
		}
		*d = int32(n)
		return nil
	case *int16:
		n, err := integer(sc, v, t, col, dest)
		if err != nil {
			return err
		}
		if n > math.MaxInt16 || n < math.MinInt16 {
			return tooWide(col, n, dest)
		}
		*d = int16(n)
		return nil
	case *int8:
		n, err := integer(sc, v, t, col, dest)
		if err != nil {
			return err
		}
		if n > math.MaxInt8 || n < math.MinInt8 {
			return tooWide(col, n, dest)
		}
		*d = int8(n)
		return nil
	case *uint64:
		n, err := integer(sc, v, t, col, dest)
		if err != nil {
			return err
		}
		if n < 0 {
			return tooWide(col, n, dest)
		}
		*d = uint64(n)
		return nil
	case *uint32:
		n, err := integer(sc, v, t, col, dest)
		if err != nil {
			return err
		}
		if n < 0 || n > math.MaxUint32 {
			return tooWide(col, n, dest)
		}
		*d = uint32(n)
		return nil
	case *uint:
		n, err := integer(sc, v, t, col, dest)
		if err != nil {
			return err
		}
		if n < 0 || (strconv.IntSize == 32 && n > math.MaxUint32) {
			return tooWide(col, n, dest)
		}
		*d = uint(n)
		return nil

	case *float64:
		f, err := decimal(sc, v, t, col, dest)
		if err != nil {
			return err
		}
		*d = f
		return nil
	case *float32:
		f, err := decimal(sc, v, t, col, dest)
		if err != nil {
			return err
		}
		*d = float32(f)
		return nil

	case *string:
		if t != TypeString {
			return mismatch(col, t, dest)
		}
		s, err := str(sc, v)
		if err != nil {
			return err
		}
		*d = s
		return nil
	case *[]byte:
		// Both, because a byte string is octets and a character string
		// is octets a reader has decided are text, and a caller who
		// asked for octets is asking for what is underneath either. It
		// does not go the other way: a byte string into a *string would
		// be this client deciding X'0041' is the letter A, which is the
		// decision the engine keeps two types apart in order not to
		// make.
		switch t {
		case TypeString:
			s, err := str(sc, v)
			if err != nil {
				return err
			}
			*d = []byte(s)
			return nil
		case TypeBytes:
			b, err := octets(sc, v)
			if err != nil {
				return err
			}
			*d = b
			return nil
		default:
			return mismatch(col, t, dest)
		}

	case *Node:
		if t != TypeNode {
			return mismatch(col, t, dest)
		}
		n, err := node(sc, v)
		if err != nil {
			return err
		}
		*d = n
		return nil
	case *Rel:
		if t != TypeRel {
			return mismatch(col, t, dest)
		}
		rl, err := rel(sc, v)
		if err != nil {
			return err
		}
		*d = rl
		return nil
	case *Path:
		if t != TypePath {
			return mismatch(col, t, dest)
		}
		items, err := parts(sc, v)
		if err != nil {
			return err
		}
		*d = Path(items)
		return nil
	case *[]any:
		if t != TypeList {
			return mismatch(col, t, dest)
		}
		items, err := parts(sc, v)
		if err != nil {
			return err
		}
		*d = items
		return nil
	case *Record:
		if t != TypeRecord {
			return mismatch(col, t, dest)
		}
		fields, err := record(sc, v)
		if err != nil {
			return err
		}
		*d = fields
		return nil
	}

	// The temporal destinations, which are worth reading in one place
	// because they all start with the same three out-parameters.
	if done, err := scanTemporal(sc, v, t, dest, col); done {
		return err
	}
	return scanReflect(sc, v, t, dest, col)
}

// scanTemporal reads a temporal cell into any of the destinations that
// can hold one, and reports whether the destination was one of them.
func scanTemporal(sc *scratch, v *C.zu_value, t Type, dest any, col string) (bool, error) {
	switch dest.(type) {
	case *Date, *LocalTime, *ZonedTime, *LocalDateTime, *ZonedDateTime, *YearMonth,
		*time.Time, *time.Duration:
	default:
		return false, nil
	}
	if t != TypeTemporal {
		return true, mismatch(col, t, dest)
	}
	if err := fail(C.zu_value_temporal(v, &sc.kind, &sc.i64, &sc.off), nil); err != nil {
		return true, err
	}
	got, err := temporal(TemporalKind(sc.kind), int64(sc.i64), int32(sc.off))
	if err != nil {
		return true, err
	}

	switch d := dest.(type) {
	case *Date:
		return true, take1(got, d, col)
	case *LocalTime:
		return true, take1(got, d, col)
	case *ZonedTime:
		return true, take1(got, d, col)
	case *LocalDateTime:
		return true, take1(got, d, col)
	case *ZonedDateTime:
		return true, take1(got, d, col)
	case *YearMonth:
		return true, take1(got, d, col)
	case *time.Time:
		// The three temporals that name an instant convert; a time of
		// day and a duration do not, because a time.Time made out of
		// one would be a date somebody invented.
		switch x := got.(type) {
		case ZonedDateTime:
			*d = x.Time()
		case LocalDateTime:
			*d = x.Time()
		case Date:
			*d = x.Time()
		default:
			return true, wrongTemporal(col, got, dest)
		}
		return true, nil
	case *time.Duration:
		switch x := got.(type) {
		case time.Duration:
			*d = x
		case LocalTime:
			*d = x.Duration()
		default:
			return true, wrongTemporal(col, got, dest)
		}
		return true, nil
	}
	return true, mismatch(col, t, dest)
}

// take1 assigns a temporal to a destination of its own type, which is
// the case where the kind the cell holds has to be the kind the caller
// asked for.
func take1[T any](got any, dest *T, col string) error {
	v, ok := got.(T)
	if !ok {
		return wrongTemporal(col, got, dest)
	}
	*dest = v
	return nil
}

// scanReflect is the destination this package did not name: a pointer
// to a pointer, which is how a null is scanned into a typed
// destination, and a named type over one of the basic kinds, which is
// how `type UserID int64` is scanned without a conversion at every
// call site.
func scanReflect(sc *scratch, v *C.zu_value, t Type, dest any, col string) error {
	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return misuse("column " + quoted(col) + " was given a destination of type " +
			reflect.TypeOf(dest).String() + ", and a destination has to be a non-nil pointer")
	}
	el := rv.Elem()
	if el.Kind() == reflect.Pointer {
		fresh := reflect.New(el.Type().Elem())
		if err := scan(sc, v, fresh.Interface(), col); err != nil {
			return err
		}
		el.Set(fresh)
		return nil
	}

	switch el.Kind() {
	case reflect.Bool:
		if t != TypeBool {
			return mismatch(col, t, dest)
		}
		if err := fail(C.zu_value_bool(v, &sc.i32), nil); err != nil {
			return err
		}
		el.SetBool(sc.i32 != 0)
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := integer(sc, v, t, col, dest)
		if err != nil {
			return err
		}
		if el.OverflowInt(n) {
			return tooWide(col, n, dest)
		}
		el.SetInt(n)
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := integer(sc, v, t, col, dest)
		if err != nil {
			return err
		}
		if n < 0 || el.OverflowUint(uint64(n)) {
			return tooWide(col, n, dest)
		}
		el.SetUint(uint64(n))
		return nil
	case reflect.Float32, reflect.Float64:
		f, err := decimal(sc, v, t, col, dest)
		if err != nil {
			return err
		}
		el.SetFloat(f)
		return nil
	case reflect.String:
		if t != TypeString {
			return mismatch(col, t, dest)
		}
		s, err := str(sc, v)
		if err != nil {
			return err
		}
		el.SetString(s)
		return nil
	}

	// Nothing else is a destination, which includes the composites
	// that have a named type of their own above. An `any` is the way
	// to read a value this client has no narrower home for.
	return mismatch(col, t, dest)
}

// scanNull puts a null somewhere. Only a destination that can hold the
// absence of a value takes one, which is a pointer to a pointer or a
// pointer to any. Everything else is an error naming the column,
// because the alternative is a zero that the caller reads as a value.
func scanNull(dest any, col string) error {
	if d, ok := dest.(*any); ok {
		*d = nil
		return nil
	}
	rv := reflect.ValueOf(dest)
	if rv.Kind() == reflect.Pointer && !rv.IsNil() {
		if el := rv.Elem(); el.Kind() == reflect.Pointer || el.Kind() == reflect.Map ||
			el.Kind() == reflect.Slice || el.Kind() == reflect.Interface {
			el.SetZero()
			return nil
		}
	}
	return &Error{
		Status: Misuse,
		Message: "column " + quoted(col) + " is null, and " + kindOf(dest) +
			" cannot hold one: scan into a pointer to it, or into an any",
	}
}

// integer reads an integer cell, which is the read behind every
// integer destination.
func integer(sc *scratch, v *C.zu_value, t Type, col string, dest any) (int64, error) {
	if t != TypeInt {
		return 0, mismatch(col, t, dest)
	}
	if err := fail(C.zu_value_i64(v, &sc.i64), nil); err != nil {
		return 0, err
	}
	return int64(sc.i64), nil
}

// decimal reads a float cell, and an integer one too. An integer
// widens here and nowhere else: a caller who asked for a float has
// said which of the two they want, and refusing the widening would
// mean writing two scans for a column of averages that happens to be
// whole in the test data.
func decimal(sc *scratch, v *C.zu_value, t Type, col string, dest any) (float64, error) {
	switch t {
	case TypeFloat:
		if err := fail(C.zu_value_f64(v, &sc.f64), nil); err != nil {
			return 0, err
		}
		return float64(sc.f64), nil
	case TypeInt:
		if err := fail(C.zu_value_i64(v, &sc.i64), nil); err != nil {
			return 0, err
		}
		return float64(sc.i64), nil
	default:
		return 0, mismatch(col, t, dest)
	}
}

// mismatch is the failure for a cell that does not hold what the
// destination reads. It names both, because either one could be the
// mistake and the caller is the one who knows which.
func mismatch(col string, t Type, dest any) error {
	return &Error{
		Status:  Misuse,
		Message: "column " + quoted(col) + " holds a " + t.String() + ", and " + kindOf(dest) + " cannot hold that",
	}
}

// wrongTemporal is mismatch for the seven temporal types, where the
// cell and the destination are both temporals and still do not agree.
func wrongTemporal(col string, got any, dest any) error {
	return &Error{
		Status: Misuse,
		Message: "column " + quoted(col) + " holds a " + reflect.TypeOf(got).String() +
			", and " + kindOf(dest) + " cannot hold that",
	}
}

// tooWide is the failure for an integer that does not fit where it was
// asked to go. It is a failure and not a truncation, because a number
// that came back wrong is worse than a scan that would not run.
func tooWide(col string, n int64, dest any) error {
	return &Error{
		Status: Misuse,
		Message: "column " + quoted(col) + " holds " + strconv.FormatInt(n, 10) +
			", which does not fit in " + kindOf(dest),
	}
}

// kindOf names a destination type the way a caller wrote it.
func kindOf(dest any) string {
	if dest == nil {
		return "a nil destination"
	}
	return "a " + reflect.TypeOf(dest).String()
}

// quoted puts a column name in quotes for a message.
func quoted(col string) string {
	return strconv.Quote(col)
}
