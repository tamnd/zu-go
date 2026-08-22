package zu

/*
#include <zu.h>
*/
import "C"

import (
	"context"
	"sync/atomic"
)

// A Tx makes several statements one. Every statement outside a
// transaction is already a transaction of its own, so this does not
// turn transactions on: what it does is make what several statements
// wrote either all kept or all unmade, and keep any of it invisible to
// another connection until the commit publishes it.
//
// A transaction runs on the connection it was begun on, and the
// statements that are in it are the ones run through that connection
// while it is open. Query and Exec here are the connection's, spelled
// through the transaction so that a reader of the code can see which
// statements are inside it.
type Tx struct {
	conn *Conn
	done atomic.Bool
}

// Begin starts a transaction. Beginning one inside another is refused
// by the engine rather than nested.
//
// A commit that answers nil is durable: the log frame is on the disk
// before the call returns.
func (c *Conn) Begin(ctx context.Context) (*Tx, error) {
	return c.begin(ctx, false)
}

// BeginReadOnly starts a transaction that may not write, which is
// enforced rather than advisory: a write inside one fails at the
// statement that wrote, not at the commit.
func (c *Conn) BeginReadOnly(ctx context.Context) (*Tx, error) {
	return c.begin(ctx, true)
}

func (c *Conn) begin(ctx context.Context, readOnly bool) (*Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.h == nil {
		return nil, misuse("the connection is closed")
	}
	flag := C.int32_t(0)
	if readOnly {
		flag = 1
	}
	var e *C.zu_error
	if err := fail(C.zu_begin(c.h, flag, &e), e); err != nil {
		return nil, err
	}
	c.tx.Store(true)
	return &Tx{conn: c}, nil
}

// InTransaction reports whether this connection has a transaction
// open. It is the one thing about a transaction that no statement
// answers, and every cleanup path that has to know whether the body
// already ended the transaction needs it.
func (c *Conn) InTransaction() (bool, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.h == nil {
		return false, misuse("the connection is closed")
	}
	var open C.int32_t
	if err := fail(C.zu_conn_in_transaction(c.h, &open), nil); err != nil {
		return false, err
	}
	return open != 0, nil
}

// Query runs a statement inside the transaction. It answers [ErrDone]
// once the transaction has been committed or rolled back, because a
// statement spelled through a finished transaction is not inside it
// and running it anyway would write outside the block a reader can see.
func (t *Tx) Query(ctx context.Context, q string, args ...Arg) (*Rows, error) {
	if t.done.Load() {
		return nil, ErrDone
	}
	return t.conn.Query(ctx, q, args...)
}

// Exec runs a statement inside the transaction and throws its result
// away. [ErrDone] once the transaction has finished, for the reason
// [Tx.Query] gives.
func (t *Tx) Exec(ctx context.Context, q string, args ...Arg) error {
	if t.done.Load() {
		return ErrDone
	}
	return t.conn.Exec(ctx, q, args...)
}

// Prepare compiles a statement on the connection this transaction runs
// on. The statement outlives the transaction, since it belongs to the
// connection, but compiling one through a transaction that has already
// finished is the same mistake [Tx.Query] refuses and answers the same
// [ErrDone].
func (t *Tx) Prepare(ctx context.Context, q string) (*Stmt, error) {
	if t.done.Load() {
		return nil, ErrDone
	}
	return t.conn.Prepare(ctx, q)
}

// Conn is the connection this transaction runs on, for a caller that
// has a function taking one.
func (t *Tx) Conn() *Conn {
	return t.conn
}

// Commit keeps what the transaction wrote and publishes it. A commit
// that answers nil is durable.
//
// A context that is already cancelled or past its deadline refuses the
// commit and leaves the transaction open, so that the deferred rollback
// beside it is still the thing that ends it.
func (t *Tx) Commit(ctx context.Context) error {
	return t.end(ctx, true)
}

// Rollback unmakes what the transaction wrote. It answers [ErrDone]
// when the transaction is already finished, which is what the usual
//
//	defer tx.Rollback(ctx)
//
// beside a commit does on the path where the commit worked. That path
// is not a failure and ignoring the error there is correct.
//
// It runs whatever the context says. Every other call here refuses a
// context that is already done, because a caller who has given up does
// not want the result. The result of a rollback is that this connection
// is not left inside a transaction, and that is not something a caller
// can stop wanting: the usual deferred rollback is reached most often
// on the path where something was cancelled, which is the path where it
// has to work.
func (t *Tx) Rollback(ctx context.Context) error {
	return t.end(ctx, false)
}

func (t *Tx) end(ctx context.Context, commit bool) error {
	// A commit is a result a caller can stop wanting, so a context
	// that is already done refuses one. A rollback is not. What a
	// rollback produces is a connection that is no longer inside a
	// transaction, and a caller who has given up needs that more than
	// anybody: refusing would leave the write in place on a connection
	// about to be closed or handed to the next request, which is the
	// one outcome nobody asked for.
	//
	// The refusal is before the swap rather than after it. Marking the
	// transaction finished and then refusing leaves the engine inside
	// a transaction no Tx will ever end, because every later call
	// answers ErrDone, and the deferred rollback beside the commit is
	// exactly the call that would have fixed it.
	if commit {
		if t.done.Load() {
			return ErrDone
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if !t.done.CompareAndSwap(false, true) {
		return ErrDone
	}
	c := t.conn
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.h == nil {
		return misuse("the connection is closed")
	}
	var e *C.zu_error
	var st C.zu_status
	if commit {
		st = C.zu_commit(c.h, &e)
	} else {
		st = C.zu_rollback(c.h, &e)
	}
	c.tx.Store(false)
	return fail(st, e)
}
