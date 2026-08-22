package zusql

import (
	"database/sql/driver"
	"io"
	"reflect"

	zu "github.com/tamnd/zu-go"
)

// rows is a whole result being handed out one row at a time, which is
// the only shape database/sql has. The engine gives back the whole
// result at once, so nothing here is a cursor and Next cannot fail on
// the network.
type rows struct {
	r *zu.Rows
}

var (
	_ driver.Rows                           = (*rows)(nil)
	_ driver.RowsColumnTypeScanType         = (*rows)(nil)
	_ driver.RowsColumnTypeDatabaseTypeName = (*rows)(nil)
)

// Columns are the names the statement gave its columns.
func (r *rows) Columns() []string {
	return r.r.Columns()
}

// Next fills one row and answers [io.EOF] at the end, which is
// database/sql's way of ending the loop.
//
// A value that is a number, a string, a bool or an instant arrives as
// the type database/sql knows. Everything else arrives as itself: a
// [zu.Node], a [zu.Rel], a [zu.Path], a [zu.Record], a list as a
// []any, or one of the temporals that is not an instant. database/sql
// passes those through to Scan unchanged, so a destination of that type
// or of any takes them and a *string does not.
func (r *rows) Next(dest []driver.Value) error {
	if !r.r.Next() {
		return io.EOF
	}
	row := r.r.Row()
	for c := range dest {
		v, err := row.Value(c)
		if err != nil {
			return err
		}
		dest[c] = value(v)
	}
	return nil
}

// value turns what the client read into what database/sql takes,
// which for most of them is nothing at all.
func value(v any) driver.Value {
	switch x := v.(type) {
	case zu.ZonedDateTime:
		return x.Time()
	case zu.LocalDateTime:
		return x.Time()
	default:
		return v
	}
}

// Close frees the result.
func (r *rows) Close() error {
	return r.r.Close()
}

// ColumnTypeScanType is what a caller should scan a column into. It is
// read off the first row, because a column here is not declared: this
// language types values and not columns, and a column of a union is
// legal. A result with no rows says any, which is the honest answer to
// a question about values that are not there.
func (r *rows) ColumnTypeScanType(index int) reflect.Type {
	switch r.typeOf(index) {
	case zu.TypeBool:
		return reflect.TypeFor[bool]()
	case zu.TypeInt:
		return reflect.TypeFor[int64]()
	case zu.TypeFloat:
		return reflect.TypeFor[float64]()
	case zu.TypeString:
		return reflect.TypeFor[string]()
	case zu.TypeBytes:
		return reflect.TypeFor[[]byte]()
	case zu.TypeNode:
		return reflect.TypeFor[zu.Node]()
	case zu.TypeRel:
		return reflect.TypeFor[zu.Rel]()
	case zu.TypeList:
		return reflect.TypeFor[[]any]()
	case zu.TypePath:
		return reflect.TypeFor[zu.Path]()
	case zu.TypeRecord:
		return reflect.TypeFor[zu.Record]()
	case zu.TypeTemporal:
		// One of seven, and which one is not knowable from the tag
		// alone. time.Time would be right for three of them and a lie
		// for the other four.
		return reflect.TypeFor[any]()
	default:
		return reflect.TypeFor[any]()
	}
}

// ColumnTypeDatabaseTypeName is the engine's own word for what the
// column holds, read off the first row for the reason above.
func (r *rows) ColumnTypeDatabaseTypeName(index int) string {
	t := r.typeOf(index)
	if t == zu.TypeNull && r.r.Len() == 0 {
		return ""
	}
	return t.String()
}

// typeOf is what the first row of a column holds, which is what both
// column-type answers are worked out from.
func (r *rows) typeOf(index int) zu.Type {
	if r.r.Len() == 0 {
		return zu.TypeNull
	}
	// Reading a cell does not move the cursor, so asking this in the
	// middle of a loop is safe and asking it before the loop starts
	// does not consume the first row.
	t, err := r.r.RowAt(0).Type(index)
	if err != nil {
		return zu.TypeNull
	}
	return t
}

// result is what a statement that wrote answers with. The engine
// reports neither of the two numbers database/sql asks for, and
// answering zero would be read as a count rather than as the absence of
// one, so both say what is true.
type result struct{}

var _ driver.Result = result{}

// LastInsertId is not something this engine has. A node's identity is
// its table and its row offset, and a program that wants a key of its
// own writes one as a property.
func (result) LastInsertId() (int64, error) {
	return 0, misuse("this engine has no last insert id: a node is identified by its table and its row offset, " +
		"and an application key is a property you wrote")
}

// RowsAffected is not reported over the C ABI, so there is no number to
// give. [zu.Conn.RowsRead] is the count that does exist, and it is rows
// read out of storage rather than rows written.
func (result) RowsAffected() (int64, error) {
	return 0, misuse("this engine does not report how many rows a statement changed")
}
