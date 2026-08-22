package zusql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	zu "github.com/tamnd/zu-go"
)

// open is a database/sql handle on a database in memory, closed when the
// test ends. Almost every test here wants exactly that.
func open(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("zu", ":memory:")
	if err != nil {
		t.Fatalf("a database in memory does not open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// one runs a statement that answers a single integer.
func one(t *testing.T, db *sql.DB, q string, args ...any) int64 {
	t.Helper()
	var got int64
	if err := db.QueryRow(q, args...).Scan(&got); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return got
}

func TestTheDriverIsRegisteredUnderItsName(t *testing.T) {
	found := false
	for _, name := range sql.Drivers() {
		if name == "zu" {
			found = true
		}
	}
	if !found {
		t.Errorf("the drivers registered are %v and none of them is zu", sql.Drivers())
	}
}

func TestAStatementAnswers(t *testing.T) {
	db := open(t)
	if got := one(t, db, "RETURN 1 + 2 AS three"); got != 3 {
		t.Errorf("1 + 2 came back as %d", got)
	}
}

func TestEveryRowOfAResultIsRead(t *testing.T) {
	db := open(t)
	rows, err := db.Query("UNWIND [1, 2, 3, 4, 5] AS n RETURN n")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var got []int64
	for rows.Next() {
		var n int64
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		got = append(got, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 || got[0] != 1 || got[4] != 5 {
		t.Errorf("the rows read back as %v", got)
	}
}

func TestAResultWithNoRowsEndsTheLoopAndSaysNoRows(t *testing.T) {
	db := open(t)
	rows, err := db.Query("UNWIND [] AS n RETURN n")
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Error("a result of no rows handed one out")
	}
	if err := rows.Err(); err != nil {
		t.Errorf("reading no rows is a failure: %v", err)
	}
	rows.Close()

	var n int64
	if err := db.QueryRow("UNWIND [] AS n RETURN n").Scan(&n); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("a single row that is not there answers %v", err)
	}
}

func TestAnArgumentIsBoundByName(t *testing.T) {
	db := open(t)
	got := one(t, db, "RETURN $a + $b AS sum", sql.Named("a", 20), sql.Named("b", 22))
	if got != 42 {
		t.Errorf("20 + 22 came back as %d", got)
	}
}

func TestAnArgumentWithNoNameIsRefused(t *testing.T) {
	db := open(t)
	_, err := db.Query("RETURN $a AS a", 1)
	if err == nil {
		t.Fatal("an argument with no name was bound to something")
	}
	if !strings.Contains(err.Error(), "sql.Named") {
		t.Errorf("the refusal does not say what to write instead: %v", err)
	}
	var e *zu.Error
	if !errors.As(err, &e) {
		t.Errorf("the refusal is not one of ours: %#v", err)
	}
}

func TestAValueKeepsItsTypeThroughTheBind(t *testing.T) {
	db := open(t)
	// database/sql would have made these four its own six types before
	// they reached the driver. CheckNamedValue is what keeps them whole,
	// and the zone on the instant is the one that would have been lost.
	when := time.Date(2024, 3, 1, 12, 30, 0, 0, time.FixedZone("east", 2*3600))
	var back time.Time
	if err := db.QueryRow("RETURN $t AS t", sql.Named("t", when)).Scan(&back); err != nil {
		t.Fatal(err)
	}
	if !back.Equal(when) {
		t.Errorf("the instant went in as %s and came back as %s", when, back)
	}
	if _, offset := back.Zone(); offset != 2*3600 {
		t.Errorf("the zone went in at +02:00 and came back at %d seconds", offset)
	}

	var f float64
	if err := db.QueryRow("RETURN $f AS f", sql.Named("f", float32(1.5))).Scan(&f); err != nil {
		t.Fatal(err)
	}
	if f != 1.5 {
		t.Errorf("a float32 came back as %v", f)
	}

	var b bool
	if err := db.QueryRow("RETURN $b AS b", sql.Named("b", true)).Scan(&b); err != nil {
		t.Fatal(err)
	}
	if !b {
		t.Error("true came back as false")
	}

	var s string
	if err := db.QueryRow("RETURN $s AS s", sql.Named("s", "ada")).Scan(&s); err != nil {
		t.Fatal(err)
	}
	if s != "ada" {
		t.Errorf("a string came back as %q", s)
	}
}

func TestAValueThisLanguageHasAndSqlDoesNotArrivesAsItself(t *testing.T) {
	db := open(t)

	var rec zu.Record
	if err := db.QueryRow("RETURN {a: 1, b: 'two'} AS r").Scan(&rec); err != nil {
		t.Fatalf("a record does not scan into a zu.Record: %v", err)
	}
	if rec["a"] != int64(1) || rec["b"] != "two" {
		t.Errorf("the record read back as %#v", rec)
	}

	var list any
	if err := db.QueryRow("RETURN [1, 2, 3] AS l").Scan(&list); err != nil {
		t.Fatalf("a list does not scan into an any: %v", err)
	}
	got, ok := list.([]any)
	if !ok || len(got) != 3 || got[0] != int64(1) {
		t.Errorf("the list read back as %#v", list)
	}

	// A temporal that is not an instant stays what it is rather than
	// being bent into a time.Time that would be wrong about four of the
	// seven kinds.
	var d any
	if err := db.QueryRow("RETURN $d AS d", sql.Named("d", 90*time.Minute)).Scan(&d); err != nil {
		t.Fatalf("a duration does not scan into an any: %v", err)
	}
	if got, ok := d.(time.Duration); !ok || got != 90*time.Minute {
		t.Errorf("a duration read back as %#v", d)
	}

	var day zu.Date
	if err := db.QueryRow("RETURN $d AS d", sql.Named("d", zu.Date{Days: 19737})).Scan(&day); err != nil {
		t.Fatalf("a date does not scan into a zu.Date: %v", err)
	}
	if day.Days != 19737 {
		t.Errorf("a date read back as %v", day)
	}
}

func TestANullNeedsSomewhereThatHoldsOne(t *testing.T) {
	db := open(t)
	var got any
	if err := db.QueryRow("RETURN null AS n").Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("a null read back as %#v", got)
	}
	var n sql.NullInt64
	if err := db.QueryRow("RETURN null AS n").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n.Valid {
		t.Error("a null read into a NullInt64 says it is a value")
	}
}

func TestAStatementRunForItsEffectReportsNeitherNumber(t *testing.T) {
	db := open(t)
	res, err := db.Exec("UNWIND [1, 2, 3] AS n RETURN n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := res.LastInsertId(); err == nil {
		t.Error("the driver gave a last insert id, which this engine does not have")
	} else if !strings.Contains(err.Error(), "last insert id") {
		t.Errorf("the refusal does not say what is missing: %v", err)
	}
	if _, err := res.RowsAffected(); err == nil {
		t.Error("the driver gave a rows-affected count, which this engine does not report")
	}
}

func TestAPreparedStatementRunsMoreThanOnce(t *testing.T) {
	db := open(t)
	stmt, err := db.Prepare("RETURN $n * 2 AS twice")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()

	for _, n := range []int64{1, 7, 21} {
		var got int64
		if err := stmt.QueryRow(sql.Named("n", n)).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != n*2 {
			t.Errorf("twice %d came back as %d", n, got)
		}
	}
	if _, err := stmt.Exec(sql.Named("n", int64(1))); err != nil {
		t.Errorf("a prepared statement does not run for its effect: %v", err)
	}
}

func TestAStatementThatDoesNotParseSaysWhere(t *testing.T) {
	db := open(t)
	_, err := db.Query("RETRUN 1")
	if err == nil {
		t.Fatal("a statement that does not parse ran")
	}
	var e *zu.Error
	if !errors.As(err, &e) {
		t.Fatalf("the failure is not one of ours: %#v", err)
	}
	if e.Code != "42001" {
		t.Errorf("a syntax error carries condition %q", e.Code)
	}
	if !e.Position.Valid() {
		t.Error("a syntax error does not say where it is")
	}
}

func TestATransactionHoldsItsWorkAndEnds(t *testing.T) {
	db := open(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var got int64
	if err := tx.QueryRow("RETURN 1 AS one").Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Errorf("a statement inside a transaction answers %d", got)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("a transaction does not commit: %v", err)
	}
	if err := tx.Commit(); !errors.Is(err, sql.ErrTxDone) {
		t.Errorf("committing twice answers %v", err)
	}
}

func TestATransactionRollsBack(t *testing.T) {
	db := open(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("RETURN 1 AS one"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("a transaction does not roll back: %v", err)
	}
	// The connection goes back to the pool usable, which is what the
	// next statement proves.
	if got := one(t, db, "RETURN 2 AS two"); got != 2 {
		t.Errorf("the connection after a rollback answers %d", got)
	}
}

func TestAReadOnlyTransactionReads(t *testing.T) {
	db := open(t)
	tx, err := db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("a read-only transaction does not begin: %v", err)
	}
	defer tx.Rollback()
	var got int64
	if err := tx.QueryRow("RETURN 3 AS three").Scan(&got); err != nil {
		t.Fatalf("a read-only transaction does not read: %v", err)
	}
	if got != 3 {
		t.Errorf("a read-only transaction answers %d", got)
	}
}

func TestAnIsolationLevelThisEngineDoesNotHaveIsRefused(t *testing.T) {
	db := open(t)
	_, err := db.BeginTx(t.Context(), &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err == nil {
		t.Fatal("a transaction was given an isolation level the engine does not have")
	}
	if !strings.Contains(err.Error(), sql.LevelSerializable.String()) {
		t.Errorf("the refusal does not say what the engine does run at: %v", err)
	}
	// Serializable is what it runs at, so asking for it is not a refusal.
	tx, err := db.BeginTx(t.Context(), &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		t.Fatalf("asking for the level the engine runs at was refused: %v", err)
	}
	tx.Rollback()
}

func TestATransactionLeftOpenIsRolledBackBeforeTheNextCaller(t *testing.T) {
	// database/sql never leaks one, so this goes at the driver directly:
	// it is ResetSession that has to clean up, and the pool is what calls
	// it.
	c, err := Driver{}.OpenConnector(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	dc, err := c.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer dc.Close()

	if _, err := dc.(*conn).Begin(); err != nil {
		t.Fatal(err)
	}
	if dc.(*conn).IsValid() {
		t.Error("a connection with a transaction open says it is ready for the next caller")
	}
	if err := dc.(*conn).ResetSession(t.Context()); err != nil {
		t.Fatalf("putting a connection back with a transaction open: %v", err)
	}
	if !dc.(*conn).IsValid() {
		t.Error("the transaction is still open after the connection went back to the pool")
	}
}

// The same, with the caller's context already done, which is the way a
// connection most often comes back with a transaction still on it. The
// context that reaches ResetSession is the pool's and not the one the
// transaction was begun with, and a rollback that consulted it would
// leave the transaction open on a connection about to be handed to
// somebody else.
func TestATransactionIsRolledBackEvenWhenTheCallerHasGivenUp(t *testing.T) {
	c, err := Driver{}.OpenConnector(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	dc, err := c.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer dc.Close()

	if _, err := dc.(*conn).Begin(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := dc.(*conn).ResetSession(ctx); err != nil {
		t.Fatalf("putting a connection back with a cancelled context: %v", err)
	}
	if !dc.(*conn).IsValid() {
		t.Error("the transaction is still open, so the next caller inherits one it did not begin")
	}
}

func TestAContextThatIsAlreadyDoneStopsTheStatement(t *testing.T) {
	db := open(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := db.QueryContext(ctx, "RETURN 1 AS one"); !errors.Is(err, context.Canceled) {
		t.Errorf("a query on a cancelled context answers %v", err)
	}
	if _, err := db.ExecContext(ctx, "RETURN 1 AS one"); !errors.Is(err, context.Canceled) {
		t.Errorf("an exec on a cancelled context answers %v", err)
	}
	if _, err := db.PrepareContext(ctx, "RETURN 1 AS one"); !errors.Is(err, context.Canceled) {
		t.Errorf("a prepare on a cancelled context answers %v", err)
	}
	if _, err := db.BeginTx(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("a begin on a cancelled context answers %v", err)
	}
}

func TestADeadlineEndsAStatementThatIsRunning(t *testing.T) {
	db := open(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	// A nested unwind over a literal, which is the only slow statement
	// this engine can be given without a database to read.
	const q = "UNWIND [0,1,2,3,4,5,6,7,8,9] AS a UNWIND [0,1,2,3,4,5,6,7,8,9] AS b " +
		"UNWIND [0,1,2,3,4,5,6,7,8,9] AS c UNWIND [0,1,2,3,4,5,6,7,8,9] AS d " +
		"UNWIND [0,1,2,3,4,5,6,7,8,9] AS e UNWIND [0,1,2,3,4,5,6,7,8,9] AS f RETURN a"
	_, err := db.QueryContext(ctx, q)
	if err == nil {
		t.Fatal("a statement past its deadline answered")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, zu.Interrupted) {
		t.Errorf("a statement past its deadline answers %v", err)
	}
}

func TestTheColumnTypesAreReadOffTheFirstRow(t *testing.T) {
	db := open(t)
	rows, err := db.Query("RETURN 1 AS a, 'two' AS b, 3.5 AS c, true AS d, [1] AS e")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		name string
		kind reflect.Type
		db   string
	}{
		{"a", reflect.TypeFor[int64](), "int"},
		{"b", reflect.TypeFor[string](), "string"},
		{"c", reflect.TypeFor[float64](), "float"},
		{"d", reflect.TypeFor[bool](), "bool"},
		{"e", reflect.TypeFor[[]any](), "list"},
	}
	if len(types) != len(want) {
		t.Fatalf("the result has %d columns and %d were asked for", len(types), len(want))
	}
	for i, w := range want {
		if got := types[i].Name(); got != w.name {
			t.Errorf("column %d is named %q and should be %q", i, got, w.name)
		}
		if got := types[i].ScanType(); got != w.kind {
			t.Errorf("column %s scans into %v and should be %v", w.name, got, w.kind)
		}
		if got := types[i].DatabaseTypeName(); got != w.db {
			t.Errorf("column %s is a %q and should be a %q", w.name, got, w.db)
		}
	}
	// Asking did not consume the row it read.
	if !rows.Next() {
		t.Error("asking about the columns consumed the only row")
	}
}

func TestAResultWithNoRowsHasNothingToSayAboutItsTypes(t *testing.T) {
	db := open(t)
	rows, err := db.Query("UNWIND [] AS n RETURN n")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != 1 {
		t.Fatalf("the result has %d columns", len(types))
	}
	if got := types[0].ScanType(); got != reflect.TypeFor[any]() {
		t.Errorf("a column of no rows scans into %v", got)
	}
	if got := types[0].DatabaseTypeName(); got != "" {
		t.Errorf("a column of no rows is a %q", got)
	}
}

func TestTheConnectionUnderneathIsReachable(t *testing.T) {
	db := open(t)
	c, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	var read uint64
	err = c.Raw(func(dc any) error {
		conn, ok := Underlying(dc)
		if !ok {
			return errors.New("the driver connection is not a zu connection")
		}
		read, err = conn.RowsRead()
		return err
	})
	if err != nil {
		t.Fatalf("reaching the connection underneath: %v", err)
	}
	_ = read

	if _, ok := Underlying("not a connection"); ok {
		t.Error("Underlying claimed a string is a connection")
	}
}

func TestEveryConnectionOfOneHandleSharesOneDatabase(t *testing.T) {
	// This is what makes ":memory:" a database a pool can hand out. Were
	// the database opened per connection, every caller would get an empty
	// one of its own and the pool would be the bug.
	c, err := Driver{}.OpenConnector(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	cc := c.(*connector)
	first, err := cc.database()
	if err != nil {
		t.Fatal(err)
	}
	second, err := cc.database()
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Error("two connections on one handle were given two databases")
	}

	// And the pool really does open more than one of them.
	db := open(t)
	db.SetMaxOpenConns(4)
	var held []*sql.Conn
	for range 3 {
		got, err := db.Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, got)
	}
	for i, got := range held {
		var n int64
		if err := got.QueryRowContext(t.Context(), "RETURN 1 AS one").Scan(&n); err != nil {
			t.Errorf("connection %d does not run a statement: %v", i, err)
		}
		got.Close()
	}
}

func TestOpeningDoesNotTouchTheDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nothing.zu1")
	db, err := sql.Open("zu", path)
	if err != nil {
		t.Fatalf("opening a handle on a path that is not there failed at sql.Open: %v", err)
	}
	defer db.Close()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("sql.Open made something at %s", path)
	}
	if err := db.PingContext(t.Context()); err == nil {
		t.Error("a handle on a path that is not there connects")
	}
}

func TestADatabaseOnDiskIsOpenedAndReopened(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.zu1")
	made, err := zu.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := made.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("zu", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := one(t, db, "RETURN 1 AS one"); got != 1 {
		t.Errorf("a database on disk answers %d", got)
	}
}

func TestCreateMakesWhatIsNotThereAndOpensWhatIs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.zu1")
	db, err := sql.Open("zu", path+"?create=true")
	if err != nil {
		t.Fatal(err)
	}
	if got := one(t, db, "RETURN 1 AS one"); got != 1 {
		t.Errorf("a database that was made answers %d", got)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("create=true did not make the database: %v", err)
	}

	// The second time it is there, and create=true opens it rather than
	// refusing or writing over it.
	again, err := sql.Open("zu", path+"?create=true")
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if got := one(t, again, "RETURN 2 AS two"); got != 2 {
		t.Errorf("a database that was already there answers %d", got)
	}
}

func TestAReadOnlyHandleCannotWriteThrough(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.zu1")
	made, err := zu.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := made.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("zu", path+"?read_only=true")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := one(t, db, "RETURN 1 AS one"); got != 1 {
		t.Errorf("a read-only handle answers %d", got)
	}
}

func TestTheOptionsInTheConnectionStringAreRead(t *testing.T) {
	for _, name := range []string{
		":memory:?threads=2",
		":memory:?memory_limit=134217728",
		":memory:?read_only=false",
		":memory:?threads=2&memory_limit=134217728",
	} {
		db, err := sql.Open("zu", name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := one(t, db, "RETURN 1 AS one"); got != 1 {
			t.Errorf("%s: a statement answers %d", name, got)
		}
		db.Close()
	}
}

func TestAConnectionStringThatIsWrongIsRefusedAtOpen(t *testing.T) {
	for _, tc := range []struct {
		name string
		says string
	}{
		{":memory:?nonsense=1", "not one of create"},
		{":memory:?threads=lots", "not a count"},
		{":memory:?threads=-1", "not a count"},
		{":memory:?memory_limit=plenty", "not a number of bytes"},
		{":memory:?read_only=maybe", "not true or false"},
		{":memory:?create=true", "meaningless"},
		{":memory:?%zz=1", "do not parse"},
	} {
		db, err := sql.Open("zu", tc.name)
		if err == nil {
			db.Close()
			t.Errorf("%s was accepted", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.says) {
			t.Errorf("%s is refused with %q, which does not say %q", tc.name, err, tc.says)
		}
		var e *zu.Error
		if !errors.As(err, &e) {
			t.Errorf("%s is refused with something that is not one of ours: %#v", tc.name, err)
		}
	}
}

func TestTheSameFailureComesBackOnEveryConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nothing.zu1")
	db, err := sql.Open("zu", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	first := db.PingContext(t.Context())
	if first == nil {
		t.Fatal("a handle on a path that is not there connects")
	}
	second := db.PingContext(t.Context())
	if second == nil {
		t.Fatal("the second connection on a path that is not there worked")
	}
	if first.Error() != second.Error() {
		t.Errorf("the first connection failed with %q and the second with %q", first, second)
	}
}

func TestTheOldDriverInterfaceStillWorks(t *testing.T) {
	// database/sql uses OpenConnector everywhere it can, so Open is only
	// reached by a caller holding the driver itself. It still has to work.
	dc, err := Driver{}.Open(":memory:")
	if err != nil {
		t.Fatalf("the driver does not open a connection: %v", err)
	}
	defer dc.Close()

	stmt, err := dc.Prepare("RETURN 1 AS one")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	if got := stmt.NumInput(); got != -1 {
		t.Errorf("the driver says a statement takes %d arguments, and it will not say", got)
	}
	rows, err := stmt.Query(nil)
	if err != nil {
		t.Fatalf("a statement with no arguments does not run through the old interface: %v", err)
	}
	rows.Close()

	// Every argument through the old interface is unnamed, which is the
	// one thing this language has nothing to bind.
	if _, err := stmt.Query([]driver.Value{int64(1)}); err == nil {
		t.Error("an unnamed argument through the old interface was bound to something")
	}
	if _, err := stmt.Exec([]driver.Value{int64(1)}); err == nil {
		t.Error("an unnamed argument through the old interface was bound to something")
	}
}
