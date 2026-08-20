package rowserr

import (
	"context"

	zu "github.com/tamnd/zu-go"
)

// The two loops, neither of them asking.
func neverAsks(ctx context.Context, c *zu.Conn) {
	rows, _ := c.Query(ctx, "MATCH (p:Person) RETURN p.name")
	defer rows.Close()
	for rows.Next() { // want `this loop ends on the last row and on a failure alike, and rows.Err is never read`
		var name string
		rows.Row().Scan(&name)
	}
}

func neverAsksRanging(ctx context.Context, c *zu.Conn) {
	rows, _ := c.Query(ctx, "MATCH (p:Person) RETURN p.name")
	defer rows.Close()
	for row := range rows.All() { // want `this loop ends on the last row and on a failure alike, and rows.Err is never read`
		var name string
		row.Scan(&name)
	}
}

// Asking is what makes it right, and where it is asked does not
// matter as long as it is asked.
func asks(ctx context.Context, c *zu.Conn) error {
	rows, _ := c.Query(ctx, "MATCH (p:Person) RETURN p.name")
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Row().Scan(&name); err != nil {
			return err
		}
	}
	return rows.Err()
}

func asksInADefer(ctx context.Context, c *zu.Conn) (err error) {
	rows, _ := c.Query(ctx, "MATCH (p:Person) RETURN p.name")
	defer func() {
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
	}()
	for row := range rows.All() {
		var name string
		row.Scan(&name)
	}
	return nil
}

// Handed back, so asking is the caller's job and this cannot see it.
func handsItBack(ctx context.Context, c *zu.Conn) (*zu.Rows, error) {
	rows, err := c.Query(ctx, "MATCH (p:Person) RETURN p.name")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
	}
	return rows, nil
}

// Two results, and asking about one is not asking about the other.
func twoResults(ctx context.Context, c *zu.Conn) error {
	a, _ := c.Query(ctx, "RETURN 1")
	b, _ := c.Query(ctx, "RETURN 2")
	defer a.Close()
	defer b.Close()
	for a.Next() {
	}
	for b.Next() { // want `this loop ends on the last row and on a failure alike, and b.Err is never read`
	}
	return a.Err()
}

// A loop over something that is not a result is not this check's
// business.
func notARowsLoop(xs []int) int {
	sum := 0
	for _, x := range xs {
		sum += x
	}
	return sum
}

// A result that arrived rather than one this function made. Whoever
// made it is the one who can ask.
func drain(rows *zu.Rows) {
	for rows.Next() {
	}
}

// The loop is in a literal and the result is that literal's own
// parameter, which is a shape a benchmark writes and a check that
// reads the two bodies as one gets wrong.
func inALiteral(run func(func(*zu.Rows))) {
	run(func(rows *zu.Rows) {
		for rows.Next() {
		}
	})
}

// The same comment, on the line itself.
func alsoOnPurpose(ctx context.Context, c *zu.Conn) {
	rows, _ := c.Query(ctx, "RETURN 1")
	defer rows.Close()
	for rows.Next() { //zulint:ignore this one is about the loop and not about the error
	}
}
