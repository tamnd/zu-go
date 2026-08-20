package viewafterclose

import (
	"context"

	zu "github.com/tamnd/zu-go"
)

func query(ctx context.Context, c *zu.Conn) *zu.Rows {
	rows, _ := c.Query(ctx, "RETURN 1")
	return rows
}

// The plain one, and the reason the check exists.
func closedThenRead(ctx context.Context, c *zu.Conn) int64 {
	rows := query(ctx, c)
	ids, _ := rows.Int64s(0)
	rows.Close()

	var sum int64
	for _, id := range ids { // want `ids borrows from rows, and rows.Close has already freed it`
		sum += id
	}
	return sum
}

// Written above the Close and running after it, which is why this is a
// question for the control flow graph and not for the line numbers.
func readInALoopTheCloseReaches(ctx context.Context, c *zu.Conn) int64 {
	rows := query(ctx, c)
	ids, _ := rows.Int64s(0)

	var sum int64
	for i := 0; i < 10; i++ {
		sum += ids[i] // want `ids borrows from rows, and rows.Close has already freed it`
		if sum > 100 {
			rows.Close()
		}
	}
	return sum
}

// All four of them lend.
func everyView(ctx context.Context, c *zu.Conn) {
	rows := query(ctx, c)
	f, _ := rows.Float64s(0)
	offs, _ := rows.NodeOffsets(1)
	ok, _ := rows.Valid(2)
	rows.Close()
	_ = f[0]    // want `f borrows from rows, and rows.Close has already freed it`
	_ = offs[0] // want `offs borrows from rows, and rows.Close has already freed it`
	_ = ok[0]   // want `ok borrows from rows, and rows.Close has already freed it`
}

// The same bug written so that it crashes in the caller.
func handedOut(ctx context.Context, c *zu.Conn) []int64 {
	rows := query(ctx, c)
	defer rows.Close()
	ids, _ := rows.Int64s(0)
	return ids // want `ids borrows from rows, which this function closes, so the caller gets freed memory`
}

// Read before the Close, which is the whole contract and is fine.
func readThenClosed(ctx context.Context, c *zu.Conn) int64 {
	rows := query(ctx, c)
	ids, _ := rows.Int64s(0)
	var sum int64
	for _, id := range ids {
		sum += id
	}
	rows.Close()
	return sum
}

// A deferred Close runs after the last statement, so nothing here is
// reachable from it.
func deferredIsFine(ctx context.Context, c *zu.Conn) int64 {
	rows := query(ctx, c)
	defer rows.Close()
	ids, _ := rows.Int64s(0)
	var sum int64
	for _, id := range ids {
		sum += id
	}
	return sum
}

// A copy is the caller's own, and copying is what a program that needs
// one to outlive the result does.
func copiedIsFine(ctx context.Context, c *zu.Conn) []int64 {
	rows := query(ctx, c)
	defer rows.Close()
	ids, _ := rows.Int64s(0)
	mine := make([]int64, len(ids))
	copy(mine, ids)
	return mine
}

// Two results, and closing one says nothing about the other.
func twoResults(ctx context.Context, c *zu.Conn) int64 {
	a := query(ctx, c)
	b := query(ctx, c)
	ids, _ := b.Int64s(0)
	a.Close()
	var sum int64
	for _, id := range ids {
		sum += id
	}
	b.Close()
	return sum
}
