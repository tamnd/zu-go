package zu

/*
#include <zu.h>
*/
import "C"

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
)

// A Conn is one connection to a database, and it is the handle that
// cannot be shared. It holds a file handle, the caches and the plans
// compiled against a catalog, all of which are what makes the second
// query on it faster than the first.
//
// Using one Conn from two goroutines at once is refused rather than
// raced: the engine answers [Concurrent] and does nothing. A program
// that queries from several goroutines gives each one its own
// connection, which is [DB.Connect] or [Conn.Duplicate]. The zulint
// analyzer in this module flags the sharing at build time, which is
// earlier than the error.
type Conn struct {
	// mu is held for reading by every call that uses the handle and
	// for writing by Close. It makes a close wait for the calls
	// already inside the engine rather than freeing under them; it
	// does not serialise those calls, because two of them at once is
	// a mistake the engine has to be the one to report.
	mu sync.RWMutex
	h  *C.zu_conn
	// tx is whether this connection has a transaction this binding
	// opened, so that Close and the transaction handle agree about
	// whose work it is to end it.
	tx atomic.Bool
	// drop closes the handle if this Conn is collected without Close
	// having been called. See the comment in cleanup.go.
	drop runtime.Cleanup
}

// An Arg is one named parameter of a statement. Parameters are named
// rather than positional in this language, so an argument is a name
// and a value and there is no order to get wrong.
//
//	rows, err := conn.Query(ctx,
//		`MATCH (p:Person {id: $id}) RETURN p.name AS name`,
//		zu.Named("id", 42))
type Arg struct {
	// Name is the parameter's name without the leading dollar.
	// [Named] drops one if it is given, so both spellings arrive
	// here the same.
	Name string
	// Value is what to bind. The Go types that map to the language's
	// own are listed at [Conn.Query], and anything else is refused
	// when the statement runs rather than silently converted.
	Value any
}

// Named makes an [Arg]. A leading dollar is dropped, so that the name
// can be written the way it appears in the statement or the way the
// engine holds it and both reach the same parameter.
func Named(name string, value any) Arg {
	if len(name) > 0 && name[0] == '$' {
		name = name[1:]
	}
	return Arg{Name: name, Value: value}
}

// A watcher turns a cancelled context into the engine's interrupt. It
// is one goroutine for the length of one statement, and only for a
// context that can be cancelled at all: a [context.Background] has no
// Done channel and gets no goroutine.
type watcher struct {
	stop chan struct{}
	done chan struct{}
	hit  atomic.Bool
	err  error
}

// watch starts the watcher for one statement, or returns nil when the
// context can never be cancelled.
func (c *Conn) watch(ctx context.Context) *watcher {
	done := ctx.Done()
	if done == nil {
		return nil
	}
	w := &watcher{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		select {
		case <-done:
			w.err = ctx.Err()
			w.hit.Store(true)
			// Safe from this goroutine and only from here: the
			// interrupt is the one call the ABI means to be made
			// while a connection is in use, and the caller holds the
			// read lock for as long as this goroutine can run.
			C.zu_conn_interrupt(c.h)
		case <-w.stop:
		}
	}()
	return w
}

// end stops the watcher and answers with the context error when the
// interrupt was sent. It waits for the goroutine, which is what keeps
// an interrupt meant for this statement from landing on the next one.
func (w *watcher) end() error {
	if w == nil {
		return nil
	}
	close(w.stop)
	<-w.done
	if w.hit.Load() {
		return w.err
	}
	return nil
}

// caused folds the context error into a failure the interrupt caused,
// so that one error answers to errors.Is against both
// [context.Canceled] and [Interrupted].
func caused(err error, cause error) error {
	if err == nil || cause == nil {
		return err
	}
	if e, ok := err.(*Error); ok && e.Status == Interrupted {
		e.cause = cause
		return e
	}
	return err
}

// Query runs a statement and reads the whole result. The rows are the
// engine's own columns and are borrowed from the result until
// [Rows.Close], which is why the result outlives the connection it
// came from and why Close is not optional.
//
// With no arguments the statement is run in one call. With arguments
// it is prepared, bound and executed, which is the same path
// [Conn.Prepare] takes and is worth taking directly when the statement
// runs more than once.
func (c *Conn) Query(ctx context.Context, q string, args ...Arg) (*Rows, error) {
	if len(args) > 0 {
		return c.queryArgs(ctx, q, args)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.h == nil {
		return nil, misuse("the connection is closed")
	}

	w := c.watch(ctx)
	p, n := lend(q)
	var h *C.zu_result
	var e *C.zu_error
	st := C.zu_query(c.h, p, n, &h, &e)
	runtime.KeepAlive(q)
	if err := caused(fail(st, e), w.end()); err != nil {
		return nil, err
	}
	return newRows(c, h)
}

// queryArgs is Query with parameters, which is prepare, bind, execute
// and close the statement.
func (c *Conn) queryArgs(ctx context.Context, q string, args []Arg) (*Rows, error) {
	stmt, err := c.Prepare(ctx, q)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	return stmt.Query(ctx, args...)
}

// Exec runs a statement and throws its result away, which is what a
// write is. A statement that answers rows is not refused here, since
// the engine has no separate word for a statement that writes, but the
// rows are freed rather than returned.
func (c *Conn) Exec(ctx context.Context, q string, args ...Arg) error {
	rows, err := c.Query(ctx, q, args...)
	if err != nil {
		return err
	}
	return rows.Close()
}

// Prepare compiles a statement once so that it can be run many times.
// The bindings live on the statement and survive an execution, so a
// loop rebinds only what changed.
//
// The statement belongs to this connection. Using it after the
// connection closes answers [Closed] rather than following a dangling
// pointer, and closing it after that is still safe.
func (c *Conn) Prepare(ctx context.Context, q string) (*Stmt, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.h == nil {
		return nil, misuse("the connection is closed")
	}

	w := c.watch(ctx)
	p, n := lend(q)
	var h *C.zu_stmt
	var e *C.zu_error
	st := C.zu_prepare(c.h, p, n, &h, &e)
	runtime.KeepAlive(q)
	if err := caused(fail(st, e), w.end()); err != nil {
		return nil, err
	}
	return newStmt(c, h), nil
}

// Interrupt stops whatever statement is running on this connection at
// the next chunk of rows the executor checks, and that statement
// answers [Interrupted]. This is the one call here meant to be made
// from another goroutine while the connection is in use.
//
// Nothing failed. The connection keeps its plans and its warm caches
// and runs the next statement normally, which is the difference
// between this and closing it. An interrupt raised while nothing is
// running is dropped when the next statement starts, so a Ctrl-C at a
// prompt cannot end whatever the user types next.
func (c *Conn) Interrupt() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.h == nil {
		return misuse("the connection is closed")
	}
	return fail(C.zu_conn_interrupt(c.h), nil)
}

// RowsRead is how many rows the running statement has read out of
// storage, counted from zero at each statement and left at its final
// value once one ends. Rows read rather than rows answered, because
// the statement a user is waiting on is exactly the one reading a
// hundred million rows to answer one.
func (c *Conn) RowsRead() (uint64, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.h == nil {
		return 0, misuse("the connection is closed")
	}
	var n C.uint64_t
	if err := fail(C.zu_conn_rows_read(c.h, &n), nil); err != nil {
		return 0, err
	}
	return uint64(n), nil
}

// TableName is what a table id is called: the Table of a [Node] or of a
// [Rel], which the engine hands over as a number because that is what a
// row holds. Node and rel tables share one id space, so this answers
// for both kinds.
//
// The empty string and no error when no table has that id. A table
// cannot be called nothing, so the empty string is unambiguous, and an
// id nothing answers for is a caller who made a number up rather than a
// failure the connection had. A closed connection is the error, since
// that one is a program to fix.
//
// The name is copied out before this returns. The engine lends it until
// the next call of this on the same connection, which is not a lifetime
// a Go string can have.
func (c *Conn) TableName(table uint32) (string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.h == nil {
		return "", misuse("the connection is closed")
	}
	var n C.size_t
	return text(C.zu_conn_table_name(c.h, C.uint32_t(table), &n), n), nil
}

// Duplicate opens a second connection on the same database, without a
// path. This is what a pool calls once it has handed the [DB] back,
// and it is the only way to a second connection on a database in
// memory, which has no path to reopen.
//
// The switches and the read-only setting come across. The plan cache,
// the block caches, the interrupt and the transaction do not, because
// those are what makes it a connection of its own.
func (c *Conn) Duplicate(ctx context.Context) (*Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.h == nil {
		return nil, misuse("the connection is closed")
	}
	var h *C.zu_conn
	var e *C.zu_error
	if err := fail(C.zu_conn_duplicate(c.h, &h, &e), e); err != nil {
		return nil, err
	}
	return newConn(h), nil
}

// Close ends the connection. A transaction still running is rolled
// back, which is what a program that failed halfway and gave up wants
// and the only answer that does not depend on a deferred call having
// run.
//
// Close is safe to call twice and waits for calls already inside the
// engine. Results this connection produced stay readable afterwards,
// because a result owns its rows outright.
//
// A connection dropped without it is closed by the collector instead,
// and its transaction rolled back the same way. That is the backstop
// rather than the plan: a connection is a file handle, the catalog,
// the statistics and the caches, and a program that waits for a
// collection to give them back is holding all of it in the meantime.
func (c *Conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.h == nil {
		return nil
	}
	// Stopped before the free, which is the order that cannot free
	// twice. See [DB.Close].
	c.drop.Stop()
	C.zu_conn_close(c.h)
	c.h = nil
	c.tx.Store(false)
	return nil
}
