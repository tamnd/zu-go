// Deliberately wrong programs, and what each of them is told.
//
// dx/15 section 4 asks every client for a misuse suite: no crash, no
// leak, and a clear message for every program that is wrong on purpose.
// Clear is the hard word, so it is three things here. The message names
// the thing the caller named, being the file they opened, the parameter
// they bound, the column they scanned. It says what was expected
// instead wherever there is something to say. And it is the engine's
// own sentence rather than a syscall's, because "failed to fill whole
// buffer" is a true statement about a everyone that tells nobody which file
// was not a database.
//
// Clear also means the right status, since a Go caller matches with
// errors.Is before reading anything. A contract broken on the Go side
// is [Misuse] and carries no condition, because no statement ran. A
// statement the engine refused is [Refused] and carries the GQLSTATUS
// and the position. A file that is not a database is neither: it is
// whatever the open failed as, and the test says which rather than
// asserting a guess.
//
// No crash is the suite running at all, which for cgo is worth more
// than it sounds: every case below is a path where a wrong Go program
// reaches a C pointer, and the failure mode this is written against is
// a SIGSEGV in a goroutine nobody can recover.
//
// No leak is checked from outside the call that would cause one, three
// ways: every case is followed by a everyone on the connection it was aimed
// at, the failing opens are repeated five hundred times, which is past
// the descriptor limit a process starts with, and the descriptors
// themselves are counted where the operating system will say.
//
// The lifecycle tests are the second half dx/15 asks for. A misuse
// suite watches what a wrong program is told; a lifecycle suite watches
// what a right program leaves behind, which is the failure nobody sees
// until the loop has run for a week.
//
// The last test is the half of a misuse suite that is usually missing:
// the programs that look wrong and are not, each of which is a decision
// somebody would otherwise reverse by accident.

package zu

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// everyone is a statement the seeded database answers, run after every
// misuse to say that the connection the misuse was aimed at still
// works.
const everyone = "MATCH (p:person) RETURN p.uid AS uid"

// seeded is a connection to a database of three people, on disk, plus
// the directory it lives in for the cases that want a second file.
func seeded(t *testing.T) (*Conn, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := Create(filepath.Join(dir, "social.zu1"))
	if err != nil {
		t.Fatalf("a database does not open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	conn, err := db.Connect(t.Context())
	if err != nil {
		t.Fatalf("a database does not connect: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	for _, q := range []string{
		"INSERT (p:person {uid: 1, name: 'ada'})",
		"INSERT (p:person {uid: 2, name: 'grace'})",
		"INSERT (p:person {uid: 3, name: 'lynn'})",
	} {
		if err := conn.Exec(t.Context(), q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return conn, dir
}

// junk writes a file that is not a database where a caller would have
// one.
func junk(t *testing.T, dir, name string, contents []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	return path
}

// closedConn is a connection of its own, closed. Its own, because a
// case that closed the seeded connection would break every case after
// it.
func closedConn(t *testing.T, dir string) *Conn {
	t.Helper()
	db, err := Create(filepath.Join(dir, "closed.zu1"))
	if err != nil {
		t.Fatalf("a database does not open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	conn, err := db.Connect(t.Context())
	if err != nil {
		t.Fatalf("a database does not connect: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}
	return conn
}

// misuseCase is one deliberately wrong program. Returning a nil error
// fails the test: every program in the table is wrong.
type misuseCase struct {
	what string
	run  func(t *testing.T, conn *Conn, dir string) error
	// The status errors.Is has to match. Zero means the case does not
	// pin one, which is the open of a file that is not a database:
	// what that fails as is the operating system's answer and not a
	// promise this client makes.
	is Status
	// Substrings the message has to hold, all of them.
	says []string
}

var misuses = []misuseCase{
	{
		what: "opens a file too small to be a database",
		run: func(t *testing.T, conn *Conn, dir string) error {
			_, err := Open(junk(t, dir, "small.zu1", []byte("not a database at all")))
			return err
		},
		says: []string{"small.zu1"},
	},
	{
		what: "opens a file the right size and the wrong kind",
		run: func(t *testing.T, conn *Conn, dir string) error {
			_, err := Open(junk(t, dir, "big.zu1", []byte(strings.Repeat("x", 40960))))
			return err
		},
		says: []string{"big.zu1"},
	},
	{
		what: "opens a database that is not there",
		// Open never makes one, so this is the mistyped path, and the
		// message has to hold the path or nobody can see that it was
		// mistyped.
		run: func(t *testing.T, conn *Conn, dir string) error {
			_, err := Open(filepath.Join(dir, "nowhere.zu1"))
			return err
		},
		says: []string{"nowhere.zu1"},
	},
	{
		what: "creates a database over one that is already there",
		run: func(t *testing.T, conn *Conn, dir string) error {
			_, err := Create(filepath.Join(dir, "social.zu1"))
			return err
		},
		says: []string{"social.zu1"},
	},
	{
		what: "writes through a connection it opened read-only",
		run: func(t *testing.T, conn *Conn, dir string) error {
			db, err := Open(filepath.Join(dir, "social.zu1"), WithReadOnly())
			if err != nil {
				t.Skipf("a read-only open of a live database is its own answer: %v", err)
			}
			t.Cleanup(func() { db.Close() })
			reader, err := db.Connect(t.Context())
			if err != nil {
				t.Fatalf("connecting read-only: %v", err)
			}
			t.Cleanup(func() { reader.Close() })
			return reader.Exec(t.Context(), "INSERT (p:person {uid: 4, name: 'zoe'})")
		},
		says: []string{"read-only"},
	},
	{
		what: "runs text that will not parse",
		run: func(t *testing.T, conn *Conn, dir string) error {
			return conn.Exec(t.Context(), "MATCH (p:person) RETRUN p.uid")
		},
		is:   Refused,
		says: []string{"42001"},
	},
	{
		what: "leaves out a parameter the statement reads",
		run: func(t *testing.T, conn *Conn, dir string) error {
			return conn.Exec(t.Context(), "MATCH (p:person) WHERE p.uid = $uid RETURN p.uid AS uid")
		},
		is:   Refused,
		says: []string{"42002", "uid"},
	},
	{
		what: "binds a parameter of a type the engine has no value for",
		run: func(t *testing.T, conn *Conn, dir string) error {
			return conn.Exec(t.Context(), "RETURN $x AS x", Named("x", make(chan int)))
		},
		is:   Misuse,
		says: []string{`"x"`, "chan int"},
	},
	{
		what: "runs a statement on a connection it closed",
		run: func(t *testing.T, conn *Conn, dir string) error {
			return closedConn(t, dir).Exec(t.Context(), everyone)
		},
		is:   Misuse,
		says: []string{"the connection is closed"},
	},
	{
		what: "prepares on a connection it closed",
		run: func(t *testing.T, conn *Conn, dir string) error {
			_, err := closedConn(t, dir).Prepare(t.Context(), everyone)
			return err
		},
		is:   Misuse,
		says: []string{"the connection is closed"},
	},
	{
		what: "runs a statement it closed",
		run: func(t *testing.T, conn *Conn, dir string) error {
			stmt, err := conn.Prepare(t.Context(), everyone)
			if err != nil {
				t.Fatalf("preparing: %v", err)
			}
			if err := stmt.Close(); err != nil {
				t.Fatalf("closing the statement: %v", err)
			}
			_, err = stmt.Query(t.Context())
			return err
		},
		is:   Misuse,
		says: []string{"the statement is closed"},
	},
	{
		what: "scans a row before it called Next",
		run: func(t *testing.T, conn *Conn, dir string) error {
			rows, err := conn.Query(t.Context(), everyone)
			if err != nil {
				t.Fatalf("querying: %v", err)
			}
			defer rows.Close()
			var uid int64
			return rows.Scan(&uid)
		},
		is:   Misuse,
		says: []string{"did not call Next"},
	},
	{
		what: "scans more destinations than the result has columns",
		run: func(t *testing.T, conn *Conn, dir string) error {
			rows, err := conn.Query(t.Context(), everyone)
			if err != nil {
				t.Fatalf("querying: %v", err)
			}
			defer rows.Close()
			if !rows.Next() {
				t.Fatal("three people went in and no row came back")
			}
			var uid, spare int64
			return rows.Scan(&uid, &spare)
		},
		is:   Misuse,
		says: []string{"2 destinations", "1 columns"},
	},
	{
		what: "scans into a destination that is not a pointer",
		run: func(t *testing.T, conn *Conn, dir string) error {
			rows, err := conn.Query(t.Context(), everyone)
			if err != nil {
				t.Fatalf("querying: %v", err)
			}
			defer rows.Close()
			if !rows.Next() {
				t.Fatal("three people went in and no row came back")
			}
			var uid int64
			return rows.Scan(uid)
		},
		is:   Misuse,
		says: []string{"uid", "non-nil pointer"},
	},
	{
		what: "reads a column that is not in the result",
		run: func(t *testing.T, conn *Conn, dir string) error {
			rows, err := conn.Query(t.Context(), everyone)
			if err != nil {
				t.Fatalf("querying: %v", err)
			}
			defer rows.Close()
			_, err = rows.Int64s(7)
			return err
		},
		is:   Misuse,
		says: []string{"column 7", "1 columns"},
	},
	{
		what: "reads a result it closed",
		run: func(t *testing.T, conn *Conn, dir string) error {
			rows, err := conn.Query(t.Context(), everyone)
			if err != nil {
				t.Fatalf("querying: %v", err)
			}
			if err := rows.Close(); err != nil {
				t.Fatalf("closing the result: %v", err)
			}
			_, err = rows.Int64s(0)
			return err
		},
		is:   Misuse,
		says: []string{"the result is closed"},
	},
	{
		what: "commits a transaction it already committed",
		run: func(t *testing.T, conn *Conn, dir string) error {
			tx, err := conn.Begin(t.Context())
			if err != nil {
				t.Fatalf("beginning: %v", err)
			}
			if err := tx.Commit(t.Context()); err != nil {
				t.Fatalf("committing: %v", err)
			}
			return tx.Commit(t.Context())
		},
		says: []string{"already finished"},
	},
	{
		what: "runs a statement on a transaction it rolled back",
		run: func(t *testing.T, conn *Conn, dir string) error {
			tx, err := conn.Begin(t.Context())
			if err != nil {
				t.Fatalf("beginning: %v", err)
			}
			if err := tx.Rollback(t.Context()); err != nil {
				t.Fatalf("rolling back: %v", err)
			}
			return tx.Exec(t.Context(), everyone)
		},
		says: []string{"already finished"},
	},
	{
		what: "names a table on a connection it closed",
		run: func(t *testing.T, conn *Conn, dir string) error {
			_, err := closedConn(t, dir).TableName(0)
			return err
		},
		is:   Misuse,
		says: []string{"the connection is closed"},
	},
	{
		what: "connects to a database it closed",
		run: func(t *testing.T, conn *Conn, dir string) error {
			db, err := Create(filepath.Join(dir, "shut.zu1"))
			if err != nil {
				t.Fatalf("opening: %v", err)
			}
			if err := db.Close(); err != nil {
				t.Fatalf("closing: %v", err)
			}
			_, err = db.Connect(t.Context())
			return err
		},
		is:   Misuse,
		says: []string{"the database is closed"},
	},
}

func TestAWrongProgramIsToldWhatIsWrong(t *testing.T) {
	for _, c := range misuses {
		t.Run(c.what, func(t *testing.T) {
			conn, dir := seeded(t)

			err := c.run(t, conn, dir)
			if err == nil {
				t.Fatalf("a program that %s was not refused", c.what)
			}
			if c.is != 0 && !errors.Is(err, c.is) {
				t.Errorf("a program that %s answers %v, which is not %v", c.what, err, c.is)
			}
			for _, want := range c.says {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("a program that %s is told %q, which does not say %q", c.what, err, want)
				}
			}

			// The connection the misuse was aimed at is still a
			// connection, which is the half of "no crash" a panic
			// would not have caught: a wrong call that left a handle
			// claimed answers Concurrent here rather than anywhere a
			// user would see it.
			rows, err := conn.Query(t.Context(), everyone)
			if err != nil {
				t.Fatalf("the connection is unusable after a program that %s: %v", c.what, err)
			}
			rows.Close()
		})
	}
}

func TestEveryRefusalCarriesTheConditionAndThePlace(t *testing.T) {
	conn, _ := seeded(t)
	err := conn.Exec(t.Context(), "MATCH (p:person) RETRUN p.uid")

	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("a refused statement is not a *zu.Error: %v", err)
	}
	if e.Code == "" {
		t.Error("a refused statement carries no GQLSTATUS")
	}
	if e.DocURL == "" {
		t.Error("a refused statement carries no doc URL")
	}
	if e.StandardText == "" {
		t.Error("a refused statement carries no standard text")
	}
	if !e.Position.Valid() {
		t.Errorf("a syntax error carries no place: %v", e.Position)
	}
	if e.Severity != SeverityException {
		t.Errorf("a refused statement is severity %v", e.Severity)
	}
}

// openFiles counts the descriptors this process holds, on the systems
// that will say. A count that stands still across a thousand opens is
// the assertion; the absolute number is nobody's business.
func openFiles(t *testing.T) int {
	t.Helper()
	where := "/proc/self/fd"
	if runtime.GOOS == "darwin" {
		where = "/dev/fd"
	}
	entries, err := os.ReadDir(where)
	if err != nil {
		t.Skipf("this system does not say what it has open: %v", err)
	}
	return len(entries)
}

func TestFiveHundredFailedOpensLeaveNothingOpen(t *testing.T) {
	// Past the descriptor limit a process starts with, so a failing
	// open that kept the file would run out here rather than in
	// somebody's retry loop.
	dir := t.TempDir()
	path := junk(t, dir, "small.zu1", []byte("not a database at all"))
	before := openFiles(t)
	for i := range 500 {
		if db, err := Open(path); err == nil {
			db.Close()
			t.Fatalf("open %d of a file that is not a database worked", i)
		}
	}
	if after := openFiles(t); after > before+8 {
		t.Errorf("five hundred failed opens left %d descriptors behind", after-before)
	}
}

func TestAThousandConnectionsOpenedAndClosedLeaveNothingBehind(t *testing.T) {
	db, err := Memory()
	if err != nil {
		t.Fatalf("a database in memory does not open: %v", err)
	}
	defer db.Close()

	before := openFiles(t)
	for i := range 1000 {
		conn, err := db.Connect(t.Context())
		if err != nil {
			t.Fatalf("connection %d: %v", i, err)
		}
		if err := conn.Close(); err != nil {
			t.Fatalf("closing connection %d: %v", i, err)
		}
	}
	// A few, because the runtime opens things of its own while a test
	// runs and a count that demanded exactness would fail on somebody
	// else's file.
	if after := openFiles(t); after > before+8 {
		t.Errorf("a thousand connections left %d descriptors behind", after-before)
	}
}

func TestAThousandConnectionsDroppedRatherThanClosedLeaveNothingBehind(t *testing.T) {
	db, err := Memory()
	if err != nil {
		t.Fatalf("a database in memory does not open: %v", err)
	}
	defer db.Close()

	before := openFiles(t)
	for i := range 1000 {
		if _, err := db.Connect(t.Context()); err != nil {
			t.Fatalf("connection %d: %v", i, err)
		}
	}
	// Twice, because a finalizer set during a collection runs in the
	// next one.
	runtime.GC()
	runtime.GC()
	if after := openFiles(t); after > before+8 {
		t.Errorf("a thousand dropped connections left %d descriptors behind", after-before)
	}
}

func TestADatabaseClosedWithThingsOpenOnItClosesThemToo(t *testing.T) {
	db, err := Memory()
	if err != nil {
		t.Fatalf("a database in memory does not open: %v", err)
	}
	conn, err := db.Connect(t.Context())
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	stmt, err := conn.Prepare(t.Context(), "RETURN 1 AS one")
	if err != nil {
		t.Fatalf("preparing: %v", err)
	}
	rows, err := stmt.Query(t.Context())
	if err != nil {
		t.Fatalf("querying: %v", err)
	}

	// Out of order on purpose: the database goes first, with a
	// statement and a result still on it, which is the shape a
	// deferred Close in the wrong order gives.
	if err := db.Close(); err != nil {
		t.Fatalf("closing the database with things open on it: %v", err)
	}
	// Every one of these is safe afterwards, and every one of them is
	// what a deferred Close does next.
	rows.Close()
	stmt.Close()
	conn.Close()
}

func TestAStatementThatFailedWroteNothingAndLeftTheConnectionAlone(t *testing.T) {
	conn, _ := seeded(t)

	if err := conn.Exec(t.Context(), "INSERT (p:person {uid: 4, name: 'zoe'}) RETRUN p"); err == nil {
		t.Fatal("a statement that does not parse inserted a person")
	}
	rows, err := conn.Query(t.Context(), "MATCH (p:person) RETURN count(p) AS n")
	if err != nil {
		t.Fatalf("counting after a failure: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("counting answered no rows")
	}
	var n int64
	if err := rows.Scan(&n); err != nil {
		t.Fatalf("scanning the count: %v", err)
	}
	if n != 3 {
		t.Errorf("a statement that did not parse left %d people where there were 3", n)
	}
}

func TestOneConnectionInTwoGoroutinesIsRefusedRatherThanRaced(t *testing.T) {
	// The mistake a Go programmer makes with a handle, and the one the
	// race detector is run over in CI. It is refused rather than
	// serialised, because a connection that quietly queued would make
	// a program that is wrong look like a program that is slow.
	conn, _ := seeded(t)

	var wg sync.WaitGroup
	var mu sync.Mutex
	var refused, answered int
	for range 8 {
		wg.Add(1)
		//zulint:ignore this is the test that proves the refusal
		go func() {
			defer wg.Done()
			for range 50 {
				rows, err := conn.Query(t.Context(), everyone)
				mu.Lock()
				switch {
				case err == nil:
					answered++
				case errors.Is(err, Concurrent):
					refused++
				default:
					t.Errorf("a shared connection answers %v", err)
				}
				mu.Unlock()
				if rows != nil {
					rows.Close()
				}
			}
		}()
	}
	wg.Wait()
	if answered+refused != 400 {
		t.Errorf("%d of 400 statements neither answered nor were refused", 400-answered-refused)
	}
	if answered == 0 {
		t.Error("a shared connection refused every statement, so nothing was tested")
	}
}

func TestTheProgramsThatLookLikeMisuseAndAreNot(t *testing.T) {
	// Each of these is a decision somebody would otherwise reverse by
	// accident, so each is written down as a program that works.
	conn, dir := seeded(t)

	t.Run("closing a result twice", func(t *testing.T) {
		rows, err := conn.Query(t.Context(), everyone)
		if err != nil {
			t.Fatalf("querying: %v", err)
		}
		if err := rows.Close(); err != nil {
			t.Fatalf("the first close: %v", err)
		}
		if err := rows.Close(); err != nil {
			t.Errorf("the second close of a result: %v", err)
		}
	})

	t.Run("rolling back a transaction that committed", func(t *testing.T) {
		// The usual `defer tx.Rollback()` beside a commit, which is
		// the Go idiom and is not a failure.
		tx, err := conn.Begin(t.Context())
		if err != nil {
			t.Fatalf("beginning: %v", err)
		}
		if err := tx.Commit(t.Context()); err != nil {
			t.Fatalf("committing: %v", err)
		}
		if err := tx.Rollback(t.Context()); !errors.Is(err, ErrDone) {
			t.Errorf("rolling back after a commit answers %v, which is not ErrDone", err)
		}
	})

	t.Run("iterating a result with no rows in it", func(t *testing.T) {
		rows, err := conn.Query(t.Context(), "MATCH (p:person) WHERE p.uid = 99 RETURN p.uid AS uid")
		if err != nil {
			t.Fatalf("querying: %v", err)
		}
		defer rows.Close()
		for range rows.All() {
			t.Error("a result of no rows gave one")
		}
		if err := rows.Err(); err != nil {
			t.Errorf("iterating an empty result: %v", err)
		}
	})

	t.Run("reading a result after its connection closed", func(t *testing.T) {
		// The rows are already out of the engine and copied, so this
		// works. It is written down because it looks like a
		// use-after-free and is not.
		second := closedConn(t, dir)
		_ = second
		db, err := Create(filepath.Join(dir, "outlive.zu1"))
		if err != nil {
			t.Fatalf("opening: %v", err)
		}
		defer db.Close()
		c, err := db.Connect(t.Context())
		if err != nil {
			t.Fatalf("connecting: %v", err)
		}
		rows, err := c.Query(t.Context(), "RETURN 1 AS one")
		if err != nil {
			t.Fatalf("querying: %v", err)
		}
		defer rows.Close()
		if err := c.Close(); err != nil {
			t.Fatalf("closing the connection: %v", err)
		}
		if !rows.Next() {
			t.Fatal("a result outliving its connection gave no row")
		}
		var one int64
		if err := rows.Scan(&one); err != nil {
			t.Errorf("scanning a result whose connection closed: %v", err)
		}
	})
}
