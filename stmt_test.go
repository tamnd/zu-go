package zu

import (
	"errors"
	"testing"
	"time"
)

func TestAParameterOfEveryKindGoesAcrossAndComesBack(t *testing.T) {
	conn := memory(t)
	when := time.Date(2024, 1, 15, 10, 30, 0, 0, time.FixedZone("+02:00", 2*3600))

	for _, c := range []struct {
		name  string
		value any
		check func(t *testing.T, row Row)
	}{
		{"an integer", int64(7), func(t *testing.T, row Row) {
			var n int64
			mustScan(t, row, &n)
			if n != 7 {
				t.Errorf("7 came back as %d", n)
			}
		}},
		{"a plain int", 7, func(t *testing.T, row Row) {
			var n int64
			mustScan(t, row, &n)
			if n != 7 {
				t.Errorf("7 came back as %d", n)
			}
		}},
		{"a float", 2.5, func(t *testing.T, row Row) {
			var f float64
			mustScan(t, row, &f)
			if f != 2.5 {
				t.Errorf("2.5 came back as %v", f)
			}
		}},
		{"a string", "hi", func(t *testing.T, row Row) {
			var s string
			mustScan(t, row, &s)
			if s != "hi" {
				t.Errorf("hi came back as %q", s)
			}
		}},
		{"a bool", true, func(t *testing.T, row Row) {
			var b bool
			mustScan(t, row, &b)
			if !b {
				t.Error("true came back as false")
			}
		}},
		{"a null", nil, func(t *testing.T, row Row) {
			var a any
			mustScan(t, row, &a)
			if a != nil {
				t.Errorf("a null came back as %v", a)
			}
		}},
		{"an instant", when, func(t *testing.T, row Row) {
			var got time.Time
			mustScan(t, row, &got)
			if !got.Equal(when) {
				t.Errorf("%v came back as %v", when, got)
			}
		}},
		{"a duration", 90 * time.Minute, func(t *testing.T, row Row) {
			var got time.Duration
			mustScan(t, row, &got)
			if got != 90*time.Minute {
				t.Errorf("90m came back as %v", got)
			}
		}},
		{"a date", Date{Days: 19737}, func(t *testing.T, row Row) {
			var got Date
			mustScan(t, row, &got)
			if got.String() != "2024-01-15" {
				t.Errorf("the date came back as %v", got)
			}
		}},
		{"a year-month duration", YearMonth{Months: 14}, func(t *testing.T, row Row) {
			var got YearMonth
			mustScan(t, row, &got)
			if got.Months != 14 {
				t.Errorf("fourteen months came back as %v", got)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			rows, err := conn.Query(t.Context(), "RETURN $v AS v", Named("v", c.value))
			if err != nil {
				t.Fatalf("binding %s: %v", c.name, err)
			}
			defer rows.Close()
			if !rows.Next() {
				t.Fatal("no row")
			}
			c.check(t, rows.Row())
		})
	}
}

func TestTheNameOfAParameterMayCarryItsDollar(t *testing.T) {
	conn := memory(t)
	for _, name := range []string{"v", "$v"} {
		rows, err := conn.Query(t.Context(), "RETURN $v AS v", Named(name, 7))
		if err != nil {
			t.Fatalf("binding the parameter as %q: %v", name, err)
		}
		rows.Close()
	}
}

func TestAParameterNobodyBoundIsRefusedRatherThanNull(t *testing.T) {
	conn := memory(t)
	_, err := conn.Query(t.Context(), "RETURN $missing AS v")
	if err == nil {
		t.Fatal("a statement with an unbound parameter ran")
	}
	var e *Error
	if !errors.As(err, &e) || e.Code == "" {
		t.Fatalf("the failure carries no condition: %v", err)
	}
}

func TestAValueThisEngineDoesNotTakeIsRefusedBeforeItRuns(t *testing.T) {
	conn := memory(t)
	_, err := conn.Query(t.Context(), "RETURN $v AS v", Named("v", make(chan int)))
	if !errors.Is(err, Misuse) {
		t.Errorf("binding a channel answers %v", err)
	}
}

func TestAPreparedStatementRunsAgainWithOnlyWhatChangedRebound(t *testing.T) {
	conn := memory(t)
	stmt, err := conn.Prepare(t.Context(), "RETURN $a + $b AS sum")
	if err != nil {
		t.Fatalf("preparing: %v", err)
	}
	defer stmt.Close()

	if err := stmt.Bind(Named("a", 1), Named("b", 2)); err != nil {
		t.Fatalf("binding: %v", err)
	}
	if got := sumOf(t, stmt); got != 3 {
		t.Errorf("1 + 2 is %d", got)
	}

	// Only one of the two is bound again, and the other keeps the
	// value it had, which is the whole reason a statement holds its
	// bindings across an execution.
	if got := sumOf(t, stmt, Named("b", 40)); got != 41 {
		t.Errorf("1 + 40 is %d", got)
	}
}

func TestAStatementOutlivesNothingItsConnectionDoesNot(t *testing.T) {
	db, err := Memory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := conn.Prepare(t.Context(), "RETURN 1 AS one")
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	// Closed rather than a dangling pointer, and the handle is still
	// safe to close afterwards.
	if _, err := stmt.Query(t.Context()); !errors.Is(err, Closed) {
		t.Errorf("running a statement whose connection closed answers %v", err)
	}
	if err := stmt.Close(); err != nil {
		t.Errorf("closing that statement: %v", err)
	}
	if err := stmt.Close(); err != nil {
		t.Errorf("closing it twice: %v", err)
	}
}

func TestExecThrowsTheResultAway(t *testing.T) {
	conn := memory(t)
	if err := conn.Exec(t.Context(), "UNWIND [1, 2, 3] AS n RETURN n"); err != nil {
		t.Errorf("running a statement for its effect: %v", err)
	}
	if err := conn.Exec(t.Context(), "RETURN $v AS v", Named("v", 1)); err != nil {
		t.Errorf("running a statement with a parameter for its effect: %v", err)
	}
}

// sumOf runs a prepared statement and reads the one integer it
// answers.
func sumOf(t *testing.T, stmt *Stmt, args ...Arg) int64 {
	t.Helper()
	rows, err := stmt.Query(t.Context(), args...)
	if err != nil {
		t.Fatalf("running the prepared statement: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("the prepared statement answered no rows")
	}
	var n int64
	mustScan(t, rows.Row(), &n)
	return n
}

// mustScan reads one cell and fails the test if it will not read.
func mustScan(t *testing.T, row Row, dest any) {
	t.Helper()
	if err := row.Scan(dest); err != nil {
		t.Fatalf("scanning into %T: %v", dest, err)
	}
}
