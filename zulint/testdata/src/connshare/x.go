package connshare

import (
	"context"
	"sync"

	zu "github.com/tamnd/zu-go"
)

func read(ctx context.Context, c *zu.Conn) {
	rows, _ := c.Query(ctx, "RETURN 1")
	defer rows.Close()
	for rows.Next() {
	}
	_ = rows.Err()
}

// One connection, this goroutine and another one.
func hereAndThere(ctx context.Context, db *zu.DB) {
	conn, _ := db.Connect(ctx)
	defer conn.Close()

	go read(ctx, conn)

	read(ctx, conn) // want `conn is a connection used here and inside a goroutine, and a connection is the state that cannot be shared: connect again`
}

// One connection, two goroutines.
func twoGoroutines(ctx context.Context, db *zu.DB) {
	conn, _ := db.Connect(ctx)

	go func() {
		read(ctx, conn)
	}()
	go func() {
		read(ctx, conn) // want `conn is a connection two goroutines both reach, and a connection is the state that cannot be shared: connect again`
	}()
}

// One connection and as many goroutines as the loop runs.
func aLoopOfThem(ctx context.Context, db *zu.DB) {
	conn, _ := db.Connect(ctx)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { // want `conn is a connection a loop hands to every goroutine it starts, and a connection is the state that cannot be shared: connect again`
			defer wg.Done()
			read(ctx, conn)
		}()
	}
	wg.Wait()
}

// Handing a connection to one goroutine and never touching it again
// is not sharing.
func handedOver(ctx context.Context, db *zu.DB) {
	conn, _ := db.Connect(ctx)
	go func() {
		defer conn.Close()
		read(ctx, conn)
	}()
}

// One database, four connections, which is what the client is for.
func fourConnections(ctx context.Context, db *zu.DB) {
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, _ := db.Connect(ctx)
			defer conn.Close()
			read(ctx, conn)
		}()
	}
	wg.Wait()
}

// No goroutine at all, so nothing to share.
func plain(ctx context.Context, db *zu.DB) {
	conn, _ := db.Connect(ctx)
	defer conn.Close()
	read(ctx, conn)
	read(ctx, conn)
}

// Interrupt is the one call meant to be made from another goroutine
// while the connection is in use, which is what makes a deadline and a
// Ctrl-C work at all.
func interrupted(ctx context.Context, db *zu.DB) {
	conn, _ := db.Connect(ctx)
	defer conn.Close()

	go func() {
		read(ctx, conn)
	}()

	_ = conn.Interrupt()
}

// Code that provokes the mistake on purpose is still code, and the
// comment is how it says so.
func onPurpose(ctx context.Context, db *zu.DB) {
	conn, _ := db.Connect(ctx)
	defer conn.Close()

	go read(ctx, conn)

	//zulint:ignore this is the test that proves the refusal
	read(ctx, conn)
}
