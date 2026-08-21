package zu

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// abi is the revision of the C ABI this binding was written against.
// The test below is what fails when the header moves, which is the
// point at which somebody has to read what changed rather than find
// out from a caller.
const abi = "0.13"

// memory opens a database that never touches the filesystem and one
// connection on it, closed when the test ends. Almost every test wants
// exactly this and nothing else.
func memory(t *testing.T) *Conn {
	t.Helper()
	db, err := Memory()
	if err != nil {
		t.Fatalf("a database in memory does not open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	conn, err := db.Connect(t.Context())
	if err != nil {
		t.Fatalf("a database in memory does not connect: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// query runs a statement and fails the test if it does not answer.
func query(t *testing.T, conn *Conn, q string, args ...Arg) *Rows {
	t.Helper()
	rows, err := conn.Query(t.Context(), q, args...)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	t.Cleanup(func() { rows.Close() })
	return rows
}

func TestTheLibraryNamesItsVersionAndItsAbi(t *testing.T) {
	if Version() == "" {
		t.Error("the linked library does not say what version it is")
	}
	if got := ABIVersion(); got != abi {
		t.Errorf("the header is ABI %s and this binding was written against %s", got, abi)
	}
}

func TestADatabaseInMemoryKnowsItIsOne(t *testing.T) {
	db, err := Memory()
	if err != nil {
		t.Fatalf("a database in memory does not open: %v", err)
	}
	defer db.Close()

	if !db.InMemory() {
		t.Error("a database made in memory says it is on disk")
	}
	if db.Path() == "" {
		t.Error("a database in memory has no name to put in a message")
	}
}

func TestTwoDatabasesInMemoryShareNothing(t *testing.T) {
	one, err := Memory()
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()
	two, err := Memory()
	if err != nil {
		t.Fatal(err)
	}
	defer two.Close()

	if one.Path() == two.Path() {
		t.Errorf("two databases in memory answer to the same name %q", one.Path())
	}
}

func TestAFileIsMadeAndOpenedAgain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.zu1")
	made, err := Create(path)
	if err != nil {
		t.Fatalf("a database is not created at %s: %v", path, err)
	}
	if made.InMemory() {
		t.Error("a database on disk says it is in memory")
	}
	if got := made.Path(); got != path {
		t.Errorf("the database says its path is %q and it was made at %q", got, path)
	}
	if err := made.Close(); err != nil {
		t.Fatal(err)
	}

	again, err := Open(path)
	if err != nil {
		t.Fatalf("a database that was just made does not open: %v", err)
	}
	defer again.Close()

	conn, err := again.Connect(t.Context())
	if err != nil {
		t.Fatalf("a database that was just made does not connect: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Query(t.Context(), "RETURN 1 AS one"); err != nil {
		t.Errorf("a statement does not run on a database that was just made: %v", err)
	}
}

func TestCreatingOverAFileThatIsThereIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.zu1")
	if err := os.WriteFile(path, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := Create(path)
	if err == nil {
		db.Close()
		t.Fatal("creating a database over a file that is already there worked, which would have written over it")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "not a database" {
		t.Errorf("the refused create wrote over the file anyway: %q %v", got, err)
	}
}

func TestOpeningAPathThatIsNotThereSaysSo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nothing.zu1")
	db, err := Open(path)
	if err == nil {
		db.Close()
		t.Fatal("opening a path that is not there worked")
	}
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("the failure is not one of ours: %#v", err)
	}
	if e.Message == "" {
		t.Error("the failure has nothing to say")
	}
}

func TestClosingTwiceIsNotAFailure(t *testing.T) {
	db, err := Memory()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := conn.Close(); err != nil {
			t.Errorf("closing a connection twice: %v", err)
		}
		if err := db.Close(); err != nil {
			t.Errorf("closing a database twice: %v", err)
		}
	}
}

func TestAConnectionThatIsClosedRefusesRatherThanCrashes(t *testing.T) {
	db, err := Memory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := conn.Query(t.Context(), "RETURN 1 AS one"); !errors.Is(err, Misuse) {
		t.Errorf("a query on a closed connection answers %v", err)
	}
	if _, err := conn.Prepare(t.Context(), "RETURN 1 AS one"); !errors.Is(err, Misuse) {
		t.Errorf("preparing on a closed connection answers %v", err)
	}
	if _, err := conn.Begin(t.Context()); !errors.Is(err, Misuse) {
		t.Errorf("beginning on a closed connection answers %v", err)
	}
	if _, err := conn.TableName(0); !errors.Is(err, Misuse) {
		t.Errorf("naming a table on a closed connection answers %v", err)
	}
}

func TestAResultOutlivesTheConnectionThatMadeIt(t *testing.T) {
	db, err := Memory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	rows, err := conn.Query(t.Context(), "UNWIND [1, 2, 3] AS n RETURN n")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	// A result owns its rows outright, which is what lets a pool hand
	// the connection back before the caller has finished reading.
	got, err := Collect[int64](rows)
	if err != nil {
		t.Fatalf("reading a result after its connection closed: %v", err)
	}
	if len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Errorf("the rows read back as %v", got)
	}
}

func TestADuplicateIsAConnectionOfItsOwn(t *testing.T) {
	conn := memory(t)
	other, err := conn.Duplicate(t.Context())
	if err != nil {
		t.Fatalf("a connection does not duplicate: %v", err)
	}
	defer other.Close()

	if _, err := other.Query(t.Context(), "RETURN 1 AS one"); err != nil {
		t.Errorf("the duplicate does not run a statement: %v", err)
	}
	// The transaction does not come across, which is what makes it a
	// connection of its own rather than a second handle on this one.
	if _, err := conn.Begin(t.Context()); err != nil {
		t.Fatal(err)
	}
	open, err := other.InTransaction()
	if err != nil {
		t.Fatal(err)
	}
	if open {
		t.Error("the duplicate is inside a transaction the original began")
	}
}

func TestRowsReadCountsWhatTheStatementRead(t *testing.T) {
	conn := memory(t)
	if _, err := conn.RowsRead(); err != nil {
		t.Errorf("a connection that has run nothing cannot say how many rows it read: %v", err)
	}
}
