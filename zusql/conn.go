package zusql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strconv"

	zu "github.com/tamnd/zu-go"
)

// A conn is one [zu.Conn] wearing the database/sql interfaces.
// database/sql guarantees one goroutine at a time on it, which is the
// same rule the connection has and the reason a pool of these is the
// right way to use the engine from a server.
type conn struct {
	c  *zu.Conn
	tx *zu.Tx
}

var (
	_ driver.Conn               = (*conn)(nil)
	_ driver.ConnBeginTx        = (*conn)(nil)
	_ driver.ConnPrepareContext = (*conn)(nil)
	_ driver.ExecerContext      = (*conn)(nil)
	_ driver.QueryerContext     = (*conn)(nil)
	_ driver.NamedValueChecker  = (*conn)(nil)
	_ driver.SessionResetter    = (*conn)(nil)
	_ driver.Validator          = (*conn)(nil)
)

// Connect opens one connection on the shared database.
func (c *connector) Connect(ctx context.Context) (driver.Conn, error) {
	db, err := c.database()
	if err != nil {
		return nil, err
	}
	got, err := db.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &conn{c: got}, nil
}

// Underlying is the [zu.Conn] behind a database/sql connection, for the
// calls database/sql has no word for: [zu.Conn.Interrupt] from another
// goroutine, [zu.Conn.RowsRead] for a progress bar, the columnar reads.
//
// It is meant to be used through [database/sql.Conn.Raw], which is the
// only place database/sql lets a caller hold the connection at all:
//
//	err := sqlConn.Raw(func(dc any) error {
//		c, ok := zusql.Underlying(dc)
//		if !ok {
//			return errors.New("not a zu connection")
//		}
//		return c.Interrupt()
//	})
//
// The connection belongs to the pool. Keeping it past the callback is
// using a connection somebody else has been given.
func Underlying(dc any) (*zu.Conn, bool) {
	c, ok := dc.(*conn)
	if !ok {
		return nil, false
	}
	return c.c, true
}

// Prepare is [conn.PrepareContext] without a context, for the
// interface.
func (c *conn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}

// PrepareContext compiles a statement on this connection.
func (c *conn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	s, err := c.c.Prepare(ctx, query)
	if err != nil {
		return nil, bad(err)
	}
	return &stmt{c: c, s: s}, nil
}

// QueryContext runs a statement in one call, without the prepare and
// close a [driver.Stmt] would cost.
func (c *conn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	a, err := argsOf(args)
	if err != nil {
		return nil, err
	}
	r, err := c.c.Query(ctx, query, a...)
	if err != nil {
		return nil, bad(err)
	}
	return &rows{r: r}, nil
}

// ExecContext runs a statement for its effect and throws the rows away.
func (c *conn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	a, err := argsOf(args)
	if err != nil {
		return nil, err
	}
	if err := c.c.Exec(ctx, query, a...); err != nil {
		return nil, bad(err)
	}
	return result{}, nil
}

// CheckNamedValue takes every value through untouched, which is the
// point of implementing it. Without it database/sql converts arguments
// to its own six types first, and a [time.Time] would arrive here
// having lost its zone while an int32 would arrive as an int64. The
// bind in the client is the one place that knows what the engine takes,
// and a copy of that switch here would be a second place to keep it
// right.
func (c *conn) CheckNamedValue(*driver.NamedValue) error {
	return nil
}

// BeginTx starts a transaction. The isolation level is not a setting
// here: the engine has one, and a caller asking for a different one is
// told rather than quietly given this one.
func (c *conn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	level := sql.IsolationLevel(opts.Isolation)
	if level != sql.LevelDefault && level != sql.LevelSerializable {
		return nil, misuse("this engine runs at " + sql.LevelSerializable.String() +
			" and was asked for " + level.String())
	}
	var t *zu.Tx
	var err error
	if opts.ReadOnly {
		t, err = c.c.BeginReadOnly(ctx)
	} else {
		t, err = c.c.Begin(ctx)
	}
	if err != nil {
		return nil, bad(err)
	}
	c.tx = t
	return &tx{c: c, t: t}, nil
}

// Begin is [conn.BeginTx] with no options, for the interface.
func (c *conn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

// IsValid is what the pool asks before handing this connection to the
// next caller.
func (c *conn) IsValid() bool {
	open, err := c.c.InTransaction()
	return err == nil && !open
}

// ResetSession is the pool putting a connection back. A transaction
// still open is one the caller lost, and rolling it back here is what
// keeps the next caller from inheriting it.
func (c *conn) ResetSession(ctx context.Context) error {
	if c.tx == nil {
		return nil
	}
	err := c.tx.Rollback(ctx)
	c.tx = nil
	if err != nil && !errors.Is(err, zu.ErrDone) {
		return driver.ErrBadConn
	}
	return nil
}

// Close ends the connection.
func (c *conn) Close() error {
	c.tx = nil
	return c.c.Close()
}

// A tx is a transaction on one pooled connection.
type tx struct {
	c *conn
	t *zu.Tx
}

// Commit keeps what the transaction wrote. It is durable when it
// answers nil.
func (t *tx) Commit() error {
	t.c.tx = nil
	return t.t.Commit(context.Background())
}

// Rollback unmakes what the transaction wrote.
func (t *tx) Rollback() error {
	t.c.tx = nil
	if err := t.t.Rollback(context.Background()); err != nil && !errors.Is(err, zu.ErrDone) {
		return err
	}
	return nil
}

// A stmt is a compiled statement. The bindings live on it and survive
// an execution, but database/sql hands the whole argument list at every
// call, so what that saves here is the compile and not the binding.
type stmt struct {
	c *conn
	s *zu.Stmt
}

var (
	_ driver.Stmt              = (*stmt)(nil)
	_ driver.StmtExecContext   = (*stmt)(nil)
	_ driver.StmtQueryContext  = (*stmt)(nil)
	_ driver.NamedValueChecker = (*stmt)(nil)
)

// NumInput is -1, which is database/sql's word for a driver that will
// not say. It is not a refusal to count: the parameters here are named
// and a statement may mention one of them three times, so a count would
// be a number database/sql would check the argument list against and
// reject correct calls over.
func (s *stmt) NumInput() int {
	return -1
}

// CheckNamedValue takes every value through untouched. See
// [conn.CheckNamedValue].
func (s *stmt) CheckNamedValue(*driver.NamedValue) error {
	return nil
}

// QueryContext runs the statement and reads the whole result.
func (s *stmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	a, err := argsOf(args)
	if err != nil {
		return nil, err
	}
	r, err := s.s.Query(ctx, a...)
	if err != nil {
		return nil, bad(err)
	}
	return &rows{r: r}, nil
}

// ExecContext runs the statement for its effect.
func (s *stmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	a, err := argsOf(args)
	if err != nil {
		return nil, err
	}
	if err := s.s.Exec(ctx, a...); err != nil {
		return nil, bad(err)
	}
	return result{}, nil
}

// Query is [stmt.QueryContext] without a context, for the interface.
func (s *stmt) Query(args []driver.Value) (driver.Rows, error) {
	return s.QueryContext(context.Background(), positional(args))
}

// Exec is [stmt.ExecContext] without a context, for the interface.
func (s *stmt) Exec(args []driver.Value) (driver.Result, error) {
	return s.ExecContext(context.Background(), positional(args))
}

// Close releases the compiled statement.
func (s *stmt) Close() error {
	return s.s.Close()
}

// positional turns the old interface's argument list into the new one's
// so that the refusal below is written once. Every one of them is
// unnamed, which is exactly what argsOf will not take.
func positional(args []driver.Value) []driver.NamedValue {
	out := make([]driver.NamedValue, len(args))
	for i, v := range args {
		out[i] = driver.NamedValue{Ordinal: i + 1, Value: v}
	}
	return out
}

// argsOf turns database/sql's arguments into the client's. Every one of
// them has to be named, because zuQL has no positional parameter and
// there is nothing sensible to bind an unnamed value to.
func argsOf(args []driver.NamedValue) ([]zu.Arg, error) {
	if len(args) == 0 {
		return nil, nil
	}
	out := make([]zu.Arg, len(args))
	for i, a := range args {
		if a.Name == "" {
			return nil, misuse("argument " + strconv.Itoa(a.Ordinal) +
				" has no name, and this language has only named parameters: pass sql.Named(\"id\", v) and write $id in the statement")
		}
		out[i] = zu.Named(a.Name, a.Value)
	}
	return out, nil
}

// bad marks the failures that mean this connection is no longer usable,
// which is the one thing database/sql needs told: a connection that
// answers [driver.ErrBadConn] is dropped from the pool and the call is
// retried on another one. Everything else goes through unchanged, so
// the caller gets the same *[zu.Error] the client would have raised.
func bad(err error) error {
	if errors.Is(err, zu.Closed) {
		return driver.ErrBadConn
	}
	return err
}
