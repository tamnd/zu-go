package zu

import (
	"errors"
	"testing"
)

func TestATransactionIsOpenBetweenBeginAndCommit(t *testing.T) {
	conn := memory(t)
	if open := inTx(t, conn); open {
		t.Error("a fresh connection is already inside a transaction")
	}

	tx, err := conn.Begin(t.Context())
	if err != nil {
		t.Fatalf("beginning: %v", err)
	}
	if open := inTx(t, conn); !open {
		t.Error("the connection is not inside the transaction that was just begun")
	}
	if _, err := tx.Query(t.Context(), "RETURN 1 AS one"); err != nil {
		t.Errorf("a statement inside a transaction: %v", err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatalf("committing: %v", err)
	}
	if open := inTx(t, conn); open {
		t.Error("the connection is still inside a transaction that was committed")
	}
}

func TestARollbackEndsTheTransactionToo(t *testing.T) {
	conn := memory(t)
	tx, err := conn.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatalf("rolling back: %v", err)
	}
	if open := inTx(t, conn); open {
		t.Error("the connection is still inside a transaction that was rolled back")
	}
}

func TestTheUsualDeferredRollbackBesideACommitIsNotAFailure(t *testing.T) {
	conn := memory(t)
	tx, err := conn.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}

	// This is the deferred call on the path where the body worked. It
	// has to be distinguishable from a rollback that failed, which is
	// what ErrDone is for.
	if err := tx.Rollback(t.Context()); !errors.Is(err, ErrDone) {
		t.Errorf("a rollback after a commit answers %v", err)
	}
	if err := tx.Commit(t.Context()); !errors.Is(err, ErrDone) {
		t.Errorf("a second commit answers %v", err)
	}
}

func TestATransactionInsideOneIsRefusedRatherThanNested(t *testing.T) {
	conn := memory(t)
	tx, err := conn.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())

	if _, err := conn.Begin(t.Context()); err == nil {
		t.Error("a transaction begun inside another one worked, so one of the two is not what the caller thinks it is")
	}
}

func TestAReadOnlyTransactionReads(t *testing.T) {
	conn := memory(t)
	tx, err := conn.BeginReadOnly(t.Context())
	if err != nil {
		t.Fatalf("beginning a read-only transaction: %v", err)
	}
	defer tx.Rollback(t.Context())

	rows, err := tx.Query(t.Context(), "UNWIND [1, 2] AS n RETURN n")
	if err != nil {
		t.Fatalf("reading inside a read-only transaction: %v", err)
	}
	defer rows.Close()
	if rows.Len() != 2 {
		t.Errorf("the read answered %d rows", rows.Len())
	}
}

func TestATransactionRunsOnTheConnectionItWasBegunOn(t *testing.T) {
	conn := memory(t)
	tx, err := conn.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())

	if tx.Conn() != conn {
		t.Error("the transaction names a connection other than the one it was begun on")
	}
	stmt, err := tx.Prepare(t.Context(), "RETURN 1 AS one")
	if err != nil {
		t.Fatalf("preparing inside a transaction: %v", err)
	}
	// The statement belongs to the connection, so it outlives the
	// transaction and is still good after the rollback.
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows, err := stmt.Query(t.Context())
	if err != nil {
		t.Errorf("running a statement prepared inside a transaction that has ended: %v", err)
	} else {
		rows.Close()
	}
	stmt.Close()
}

func TestClosingAConnectionEndsTheTransactionItLeftOpen(t *testing.T) {
	db, err := Memory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Begin(t.Context()); err != nil {
		t.Fatal(err)
	}

	// Nobody committed and nobody rolled back, which is what a program
	// that failed halfway and returned looks like.
	if err := conn.Close(); err != nil {
		t.Errorf("closing a connection with a transaction open: %v", err)
	}
	other, err := db.Connect(t.Context())
	if err != nil {
		t.Fatalf("connecting again after that: %v", err)
	}
	defer other.Close()
	if open := inTx(t, other); open {
		t.Error("a new connection is inside the transaction the closed one left open")
	}
}

// inTx asks whether a connection has a transaction open.
func inTx(t *testing.T, conn *Conn) bool {
	t.Helper()
	open, err := conn.InTransaction()
	if err != nil {
		t.Fatalf("asking whether a transaction is open: %v", err)
	}
	return open
}
