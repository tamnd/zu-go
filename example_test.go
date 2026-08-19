package zu_test

import (
	"context"
	"errors"
	"fmt"
	"log"

	zu "github.com/tamnd/zu-go"
)

// open is the two lines every example starts with, on a database that
// never touches the filesystem.
func open() (*zu.DB, *zu.Conn) {
	db, err := zu.Memory()
	if err != nil {
		log.Fatal(err)
	}
	conn, err := db.Connect(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	return db, conn
}

func Example() {
	ctx := context.Background()
	db, conn := open()
	defer db.Close()
	defer conn.Close()

	rows, err := conn.Query(ctx, `UNWIND [1, 2, 3] AS n RETURN n, n * n AS square`)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	for row := range rows.All() {
		var n, square int64
		if err := row.Scan(&n, &square); err != nil {
			log.Fatal(err)
		}
		fmt.Println(n, square)
	}
	if err := rows.Err(); err != nil {
		log.Fatal(err)
	}
	// Output:
	// 1 1
	// 2 4
	// 3 9
}

func ExampleCollect() {
	type row struct {
		N    int64  `zu:"n"`
		Word string `zu:"word"`
	}

	ctx := context.Background()
	db, conn := open()
	defer db.Close()
	defer conn.Close()

	rows, err := conn.Query(ctx, `UNWIND [1, 2] AS n RETURN n, "row" AS word`)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	got, err := zu.Collect[row](rows)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(got)
	// Output: [{1 row} {2 row}]
}

func ExampleIter() {
	ctx := context.Background()
	db, conn := open()
	defer db.Close()
	defer conn.Close()

	rows, err := conn.Query(ctx, `UNWIND [1, 2, 3, 4, 5] AS n RETURN n`)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	// A single value rather than a struct, and a loop that stops when
	// it has what it came for.
	for n, err := range zu.Iter[int64](rows) {
		if err != nil {
			log.Fatal(err)
		}
		if n > 3 {
			break
		}
		fmt.Println(n)
	}
	// Output:
	// 1
	// 2
	// 3
}

func ExampleNamed() {
	ctx := context.Background()
	db, conn := open()
	defer db.Close()
	defer conn.Close()

	// Parameters are named rather than positional, so there is no order
	// to get wrong. The dollar may be written or left off.
	rows, err := conn.Query(ctx, `RETURN $a + $b AS sum`,
		zu.Named("a", 40), zu.Named("b", 2))
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	var sum int64
	if rows.Next() {
		if err := rows.Scan(&sum); err != nil {
			log.Fatal(err)
		}
	}
	fmt.Println(sum)
	// Output: 42
}

func ExampleRows_Int64s() {
	ctx := context.Background()
	db, conn := open()
	defer db.Close()
	defer conn.Close()

	rows, err := conn.Query(ctx, `UNWIND [10, 20, 30] AS n RETURN n`)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	// The whole column, contiguous and without a copy. It belongs to
	// the result and is valid exactly until Close.
	col, err := rows.Int64s(0)
	if err != nil {
		log.Fatal(err)
	}
	var sum int64
	for _, n := range col {
		sum += n
	}
	fmt.Println(len(col), sum)
	// Output: 3 60
}

func ExampleError() {
	ctx := context.Background()
	db, conn := open()
	defer db.Close()
	defer conn.Close()

	_, err := conn.Query(ctx, `RETRUN 1`)

	var e *zu.Error
	if errors.As(err, &e) {
		fmt.Println("condition:", e.Code)
		fmt.Println("retryable:", e.Retryable)
		if e.Position.Valid() {
			// The excerpt is the line the position is on, so a caller
			// can underline the token rather than print five digits.
			fmt.Println(e.Excerpt)
			fmt.Printf("%*s\n", e.Position.Column, "^")
		}
		fmt.Println("docs:", e.DocURL)
	}
	fmt.Println("refused:", errors.Is(err, zu.Refused))
	// Output:
	// condition: 42001
	// retryable: false
	// RETRUN 1
	// ^
	// docs: https://zu.dev/docs/errors/42001
	// refused: true
}

func ExampleTx() {
	ctx := context.Background()
	db, conn := open()
	defer db.Close()
	defer conn.Close()

	tx, err := conn.Begin(ctx)
	if err != nil {
		log.Fatal(err)
	}
	// On the path where the commit worked this answers ErrDone, which
	// is not a failure and is why it can be ignored here.
	defer tx.Rollback(ctx)

	if err := tx.Exec(ctx, `RETURN 1 AS one`); err != nil {
		log.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		log.Fatal(err)
	}

	open, err := conn.InTransaction()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("still open:", open)
	// Output: still open: false
}
