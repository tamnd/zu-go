// Every call that takes a context honours one that is already
// cancelled.
//
// How a binding awaits is one of the four questions the scorecard's
// idiom item asks, and in Go the answer is context: a call that can
// block takes one first, and a caller who has already given up gets
// told so rather than waited for. The convention is worth nothing
// unevenly applied. A caller writes the same deferred cancel around
// every call in a package and finds out one at a time which of them
// meant it.
//
// The list of calls is read off the surface rather than written down
// here. A hand-written list is right on the day it is written: five of
// these were checked by hand in conn_test.go and nine were not, and
// nothing anywhere said which nine. Reflection finds every method whose
// first parameter is a context, so a method added tomorrow is in this
// test the moment it exists and a method that stops taking one is a
// compile of the same list with a hole in it.
//
// Rollback is the exception and is checked in the other direction. A
// context is a caller saying they no longer want the result, and the
// result of a rollback is that the transaction is not left open, which
// is not a thing a caller can stop wanting. A Rollback that refused
// because the deadline passed would leave the write it was undoing in
// place, on the connection the caller is about to close, which is the
// one outcome nobody asked for.

package zu

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// awaiting is one exported method that takes a context first.
type awaiting struct {
	name   string
	method reflect.Method
	// receiver makes a fresh one, because these are calls that end
	// things: a transaction that has been committed cannot be
	// committed again and would fail for a reason that is not the one
	// under test.
	receiver func(t *testing.T) reflect.Value
}

// blocking finds every exported method on the four types that own
// something native whose first parameter is a context.
func blocking(t *testing.T) []awaiting {
	t.Helper()
	ctxType := reflect.TypeOf((*context.Context)(nil)).Elem()
	owners := []struct {
		typ  reflect.Type
		make func(t *testing.T) reflect.Value
	}{
		{reflect.TypeOf(&DB{}), func(t *testing.T) reflect.Value {
			db, err := Memory()
			if err != nil {
				t.Fatalf("a database in memory does not open: %v", err)
			}
			t.Cleanup(func() { db.Close() })
			return reflect.ValueOf(db)
		}},
		{reflect.TypeOf(&Conn{}), func(t *testing.T) reflect.Value {
			return reflect.ValueOf(memory(t))
		}},
		{reflect.TypeOf(&Stmt{}), func(t *testing.T) reflect.Value {
			stmt, err := memory(t).Prepare(t.Context(), "RETURN 1 AS one")
			if err != nil {
				t.Fatalf("a statement does not prepare: %v", err)
			}
			t.Cleanup(func() { stmt.Close() })
			return reflect.ValueOf(stmt)
		}},
		{reflect.TypeOf(&Tx{}), func(t *testing.T) reflect.Value {
			tx, err := memory(t).Begin(t.Context())
			if err != nil {
				t.Fatalf("a transaction does not begin: %v", err)
			}
			t.Cleanup(func() { tx.Rollback(context.WithoutCancel(t.Context())) })
			return reflect.ValueOf(tx)
		}},
	}

	var out []awaiting
	for _, owner := range owners {
		for i := range owner.typ.NumMethod() {
			m := owner.typ.Method(i)
			// The receiver is parameter zero of a method read off a
			// type, so the context is parameter one.
			if m.Type.NumIn() < 2 || m.Type.In(1) != ctxType {
				continue
			}
			name := strings.TrimPrefix(owner.typ.String(), "*zu.") + "." + m.Name
			out = append(out, awaiting{name, m, owner.make})
		}
	}
	if len(out) == 0 {
		t.Fatal("no method on any of these takes a context, which means the reflection is wrong and not that none do")
	}
	return out
}

// arguments fills in what a method wants after the context. There are
// only two kinds on this surface: the text of a statement, and the
// parameters to bind to it, of which none is a valid number.
func arguments(t *testing.T, m reflect.Method, ctx reflect.Value) []reflect.Value {
	t.Helper()
	in := []reflect.Value{ctx}
	for i := 2; i < m.Type.NumIn(); i++ {
		p := m.Type.In(i)
		if m.Type.IsVariadic() && i == m.Type.NumIn()-1 {
			continue
		}
		switch p.Kind() {
		case reflect.String:
			// A statement that is valid, so that a refusal is the
			// cancellation and not the text.
			in = append(in, reflect.ValueOf("RETURN 1 AS one"))
		default:
			t.Fatalf("%s takes a %s after the context and this test does not know what to put there", m.Name, p)
		}
	}
	return in
}

// answer pulls the error out of what a call returned, and closes
// anything it handed back, since a call that ignored the cancellation
// hands back something real and this test is the only thing holding it.
func answer(out []reflect.Value) error {
	var err error
	for _, v := range out {
		if v.Type() == reflect.TypeOf((*error)(nil)).Elem() {
			if e, ok := v.Interface().(error); ok {
				err = e
			}
			continue
		}
		if v.IsValid() && !v.IsZero() {
			if c, ok := v.Interface().(interface{ Close() error }); ok {
				c.Close()
			}
		}
	}
	return err
}

func TestEveryCallThatTakesAContextHonoursOneAlreadyCancelled(t *testing.T) {
	calls := blocking(t)
	t.Logf("%d calls on this surface take a context", len(calls))

	for _, c := range calls {
		t.Run(c.name, func(t *testing.T) {
			// Rollback has to work after the caller gave up, or the
			// transaction it was undoing stays open. It has a test of
			// its own below.
			if strings.HasSuffix(c.name, ".Rollback") {
				t.Skip("a rollback is not a result a caller can stop wanting, see the test below")
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			recv := c.receiver(t)
			err := answer(c.method.Func.Call(append([]reflect.Value{recv}, arguments(t, c.method, reflect.ValueOf(ctx))...)))
			if !errors.Is(err, context.Canceled) {
				t.Errorf("with a context already cancelled it answers %v, and a caller who gave up should be told so", err)
			}
		})
	}
}

func TestEveryCallThatTakesAContextHonoursOneAlreadyPastItsDeadline(t *testing.T) {
	for _, c := range blocking(t) {
		t.Run(c.name, func(t *testing.T) {
			if strings.HasSuffix(c.name, ".Rollback") {
				t.Skip("a rollback is not a result a caller can stop wanting, see the test below")
			}
			// A deadline in the past rather than a cancel, because the
			// two arrive at the call as different errors and a binding
			// that checks only for one reports the other as a
			// success.
			ctx, cancel := context.WithTimeout(t.Context(), -time.Second)
			defer cancel()
			recv := c.receiver(t)
			err := answer(c.method.Func.Call(append([]reflect.Value{recv}, arguments(t, c.method, reflect.ValueOf(ctx))...)))
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("with a deadline already passed it answers %v", err)
			}
		})
	}
}

// The other direction. A caller who gave up still needs the transaction
// closed, so a rollback runs whatever the context says, and the
// connection is usable afterwards rather than left inside a transaction
// nobody can end.
func TestARollbackRunsEvenWhenTheCallerHasGivenUp(t *testing.T) {
	conn := memory(t)
	tx, err := conn.Begin(t.Context())
	if err != nil {
		t.Fatalf("a transaction does not begin: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := tx.Rollback(ctx); err != nil {
		t.Errorf("a rollback with a cancelled context answers %v, and then the transaction is still open", err)
	}
	switch inside, err := conn.InTransaction(); {
	case err != nil:
		t.Errorf("the connection will not say whether it is in a transaction: %v", err)
	case inside:
		t.Error("the rollback did not end the transaction, so the connection is stuck inside one")
	}
	if err := conn.Exec(t.Context(), "RETURN 1 AS one"); err != nil {
		t.Errorf("the connection does not work afterwards: %v", err)
	}
}

// The other half of the same rule. A commit refused because the caller
// gave up has to leave the transaction open, or the deferred rollback
// beside it has nothing left to end and the connection is stranded
// inside a transaction that no call will ever close.
func TestACommitRefusedByAGivenUpCallerLeavesSomethingToRollBack(t *testing.T) {
	conn := memory(t)
	tx, err := conn.Begin(t.Context())
	if err != nil {
		t.Fatalf("a transaction does not begin: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := tx.Commit(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("a commit with a cancelled context answers %v", err)
	}
	// This is the deferred rollback, run by hand so the test can look
	// at what it answered.
	if err := tx.Rollback(t.Context()); err != nil {
		t.Errorf("the rollback after the refused commit answers %v, so the transaction is stranded", err)
	}
	switch inside, err := conn.InTransaction(); {
	case err != nil:
		t.Errorf("the connection will not say whether it is in a transaction: %v", err)
	case inside:
		t.Error("the connection is still inside the transaction the commit refused to end")
	}
}
