package zusql_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	zu "github.com/tamnd/zu-go"
	"github.com/tamnd/zu-go/zusql"
)

func Example() {
	db, err := sql.Open("zu", ":memory:")
	if err != nil {
		panic(err)
	}
	defer db.Close()

	rows, err := db.Query("UNWIND [1, 2, 3] AS n RETURN n * 10 AS ten")
	if err != nil {
		panic(err)
	}
	defer rows.Close()

	for rows.Next() {
		var n int64
		if err := rows.Scan(&n); err != nil {
			panic(err)
		}
		fmt.Println(n)
	}
	if err := rows.Err(); err != nil {
		panic(err)
	}
	// Output:
	// 10
	// 20
	// 30
}

// Every parameter in this language is named, so every argument is a
// [database/sql.Named] and a positional one is refused.
func Example_named() {
	db, err := sql.Open("zu", ":memory:")
	if err != nil {
		panic(err)
	}
	defer db.Close()

	var sum int64
	err = db.QueryRow("RETURN $a + $b AS sum", sql.Named("a", 20), sql.Named("b", 22)).Scan(&sum)
	if err != nil {
		panic(err)
	}
	fmt.Println(sum)

	_, err = db.Query("RETURN $a AS a", 1)
	fmt.Println(err != nil)
	// Output:
	// 42
	// true
}

// A statement run for its effect answers a [database/sql.Result] that
// reports neither number, because the engine has neither to report.
func Example_result() {
	db, err := sql.Open("zu", ":memory:")
	if err != nil {
		panic(err)
	}
	defer db.Close()

	res, err := db.Exec("RETURN 1 AS one")
	if err != nil {
		panic(err)
	}
	if _, err := res.RowsAffected(); err != nil {
		fmt.Println(err)
	}
	// Output:
	// zu: this engine does not report how many rows a statement changed
}

// Every failure arrives as the same *[zu.Error] the client itself
// raises, so one errors.As covers the driver and the engine together.
func Example_errors() {
	db, err := sql.Open("zu", ":memory:")
	if err != nil {
		panic(err)
	}
	defer db.Close()

	_, err = db.Query("RETRUN 1")
	var e *zu.Error
	if errors.As(err, &e) {
		fmt.Println("condition:", e.Code)
		fmt.Println("at:", e.Position)
		fmt.Println("excerpt:", e.Excerpt)
		fmt.Println("retryable:", e.Retryable)
	}
	// Output:
	// condition: 42001
	// at: 1:1
	// excerpt: RETRUN 1
	// retryable: false
}

// Underlying reaches the connection itself, for the calls database/sql
// has no word for.
func ExampleUnderlying() {
	db, err := sql.Open("zu", ":memory:")
	if err != nil {
		panic(err)
	}
	defer db.Close()

	c, err := db.Conn(context.Background())
	if err != nil {
		panic(err)
	}
	defer c.Close()

	err = c.Raw(func(dc any) error {
		conn, ok := zusql.Underlying(dc)
		if !ok {
			return errors.New("not a zu connection")
		}
		read, err := conn.RowsRead()
		if err != nil {
			return err
		}
		fmt.Println("rows read so far:", read)
		return nil
	})
	if err != nil {
		panic(err)
	}
	// Output:
	// rows read so far: 0
}
