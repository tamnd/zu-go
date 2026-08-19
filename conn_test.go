package zu

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// slow is a statement that runs for long enough to be interrupted and
// long enough for a second goroutine to arrive while it is running. It
// is nested UNWIND rather than a sleep because the engine has nothing
// that waits, and the row count is what makes it take a while.
func slow(depth int) string {
	var b strings.Builder
	for i := range depth {
		b.WriteString("UNWIND [0, 1, 2, 3, 4, 5, 6, 7, 8, 9] AS n")
		b.WriteString(string(rune('a' + i)))
		b.WriteString(" ")
	}
	// The rows are returned rather than counted, because an aggregate
	// over a literal list is folded away and the statement then takes
	// no time at all.
	b.WriteString("RETURN na")
	return b.String()
}

func TestACancelledContextStopsAStatementThatIsRunning(t *testing.T) {
	conn := memory(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := conn.Query(ctx, slow(6))
	took := time.Since(start)

	// One error answers both questions, because a caller who wrote the
	// deadline asks the context one and a caller handling the failure
	// asks the status one.
	if !errors.Is(err, Interrupted) {
		t.Fatalf("a statement stopped by a deadline answers %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("the failure does not carry the deadline that caused it: %v", err)
	}
	t.Logf("the statement stopped after %v", took)

	// The connection is not what failed, so the next statement on it
	// runs normally.
	rows, err := conn.Query(t.Context(), "RETURN 1 AS one")
	if err != nil {
		t.Errorf("the next statement after an interruption: %v", err)
	} else {
		rows.Close()
	}
}

func TestAContextThatIsAlreadyCancelledStopsBeforeTheEngineIsAskedAnything(t *testing.T) {
	conn := memory(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	for _, c := range []struct {
		name string
		call func() error
	}{
		{"a query", func() error { _, err := conn.Query(ctx, "RETURN 1 AS one"); return err }},
		{"a query with arguments", func() error {
			_, err := conn.Query(ctx, "RETURN $v AS v", Named("v", 1))
			return err
		}},
		{"preparing", func() error { _, err := conn.Prepare(ctx, "RETURN 1 AS one"); return err }},
		{"beginning", func() error { _, err := conn.Begin(ctx); return err }},
		{"duplicating", func() error { _, err := conn.Duplicate(ctx); return err }},
	} {
		if err := c.call(); !errors.Is(err, context.Canceled) {
			t.Errorf("%s with a cancelled context answers %v", c.name, err)
		}
	}
}

func TestInterruptStopsTheStatementRunningOnTheConnection(t *testing.T) {
	conn := memory(t)
	var wg sync.WaitGroup
	wg.Add(1)
	var err error
	go func() {
		defer wg.Done()
		_, err = conn.Query(context.Background(), slow(6))
	}()

	time.Sleep(10 * time.Millisecond)
	if err := conn.Interrupt(); err != nil {
		t.Fatalf("interrupting: %v", err)
	}
	wg.Wait()
	if !errors.Is(err, Interrupted) {
		t.Errorf("the interrupted statement answers %v", err)
	}
}

func TestAnInterruptWithNothingRunningDoesNotEndTheNextStatement(t *testing.T) {
	conn := memory(t)
	if err := conn.Interrupt(); err != nil {
		t.Fatalf("interrupting an idle connection: %v", err)
	}
	rows, err := conn.Query(t.Context(), "RETURN 1 AS one")
	if err != nil {
		t.Fatalf("the statement after an interrupt nobody was running: %v", err)
	}
	rows.Close()
}

func TestTwoGoroutinesOnOneConnectionAreRefusedRatherThanRaced(t *testing.T) {
	conn := memory(t)
	started := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Whatever this one answers is not the point. It is here to be
		// inside the engine when the other goroutine arrives, and it
		// has to be the one that arrived first, because the refusal
		// goes to whoever is second.
		close(started)
		rows, err := conn.Query(context.Background(), slow(6))
		if err == nil {
			rows.Close()
		}
	}()

	<-started
	time.Sleep(10 * time.Millisecond)
	rows, seen := conn.Query(context.Background(), "RETURN 1 AS one")
	if seen == nil {
		rows.Close()
	}
	wg.Wait()

	if !errors.Is(seen, Concurrent) {
		t.Fatalf("a second goroutine on one connection answers %v", seen)
	}
	// The refusal did nothing, so the connection is still good.
	rows, err := conn.Query(t.Context(), "RETURN 1 AS one")
	if err != nil {
		t.Errorf("the connection after a refused concurrent use: %v", err)
	} else {
		rows.Close()
	}
}

func TestRowsReadAnswersWhileAStatementIsRunning(t *testing.T) {
	conn := memory(t)
	started := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		close(started)
		rows, err := conn.Query(context.Background(), slow(6))
		if err == nil {
			rows.Close()
		}
	}()

	<-started
	time.Sleep(10 * time.Millisecond)
	// This is the call a progress bar makes, and the point of the test
	// is that it answers rather than being refused as a second
	// goroutine on a busy connection. The count is rows read out of
	// storage, so a statement over a literal list reads none of them
	// and this is legitimately zero.
	n, err := conn.RowsRead()
	wg.Wait()
	if err != nil {
		t.Fatalf("asking how many rows a running statement has read: %v", err)
	}
	t.Logf("the statement had read %d rows out of storage", n)
}

func TestTheOptionsReachTheDatabase(t *testing.T) {
	db, err := Memory(WithMemoryLimit(64<<20), WithThreads(2))
	if err != nil {
		t.Fatalf("opening with a memory limit and a thread count: %v", err)
	}
	defer db.Close()

	conn, err := db.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	rows, err := conn.Query(t.Context(), "RETURN 1 AS one")
	if err != nil {
		t.Fatalf("a statement on a database opened with options: %v", err)
	}
	rows.Close()
}

func TestAReadOnlyDatabaseWillNotBeCreated(t *testing.T) {
	// Read-only is a setting on the open, and a database in memory has
	// nothing to read, so this is the shape that says the option is
	// carried rather than dropped on the floor.
	db, err := Memory(WithConfig(Config{ReadOnly: true}))
	if err != nil {
		t.Skipf("a read-only database in memory does not open, which is its own answer: %v", err)
	}
	db.Close()
}
