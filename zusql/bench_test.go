package zusql

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"testing"

	zu "github.com/tamnd/zu-go"
)

// rowsOf is a statement answering n rows of one integer, which is the
// cheapest thing the engine can be asked for that still has rows to read.
func rowsOf(n int) string {
	var b strings.Builder
	b.WriteString("UNWIND [")
	for i := range n {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(strconv.Itoa(i))
	}
	b.WriteString("] AS n RETURN n")
	return b.String()
}

// benchDB is a database/sql handle held open for a whole benchmark.
func benchDB(b *testing.B) *sql.DB {
	b.Helper()
	db, err := sql.Open("zu", ":memory:")
	if err != nil {
		b.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	return db
}

// benchConn is the same database reached through the client itself, so
// that the two numbers below are the cost of the pool and the interface
// and nothing else.
func benchConn(b *testing.B) *zu.Conn {
	b.Helper()
	db, err := zu.Memory()
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	conn, err := db.Connect(context.Background())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { conn.Close() })
	return conn
}

func BenchmarkQueryRow(b *testing.B) {
	db := benchDB(b)
	for b.Loop() {
		var n int64
		if err := db.QueryRow("RETURN 1 AS one").Scan(&n); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkQueryRowDirect(b *testing.B) {
	conn := benchConn(b)
	ctx := context.Background()
	for b.Loop() {
		rows, err := conn.Query(ctx, "RETURN 1 AS one")
		if err != nil {
			b.Fatal(err)
		}
		var n int64
		rows.Next()
		if err := rows.Scan(&n); err != nil {
			b.Fatal(err)
		}
		rows.Close()
	}
}

func BenchmarkPrepared(b *testing.B) {
	db := benchDB(b)
	stmt, err := db.Prepare("RETURN $n + 1 AS next")
	if err != nil {
		b.Fatal(err)
	}
	defer stmt.Close()
	for b.Loop() {
		var n int64
		if err := stmt.QueryRow(sql.Named("n", int64(41))).Scan(&n); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkScan1000(b *testing.B) {
	db := benchDB(b)
	q := rowsOf(1000)
	for b.Loop() {
		rows, err := db.Query(q)
		if err != nil {
			b.Fatal(err)
		}
		var sum int64
		for rows.Next() {
			var n int64
			if err := rows.Scan(&n); err != nil {
				b.Fatal(err)
			}
			sum += n
		}
		if err := rows.Err(); err != nil {
			b.Fatal(err)
		}
		if err := rows.Err(); err != nil {
			b.Fatal(err)
		}
		rows.Close()
	}
}

func BenchmarkScan1000Direct(b *testing.B) {
	conn := benchConn(b)
	ctx := context.Background()
	q := rowsOf(1000)
	for b.Loop() {
		rows, err := conn.Query(ctx, q)
		if err != nil {
			b.Fatal(err)
		}
		var sum int64
		for rows.Next() {
			var n int64
			if err := rows.Scan(&n); err != nil {
				b.Fatal(err)
			}
			sum += n
		}
		if err := rows.Err(); err != nil {
			b.Fatal(err)
		}
		rows.Close()
	}
}
