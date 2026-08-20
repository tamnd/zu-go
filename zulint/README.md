# zulint

Three static checks for the Go client, for the three mistakes that actually happen. Every one of them compiles, passes review, and is a use-after-free, a swallowed failure, or a data race at run time.

```
go install github.com/tamnd/zu-go/zulint/cmd/zulint@latest
zulint ./...
```

It reads source. No engine, no C toolchain, no database, and it does not need the package it checks to be linked against anything.

## The checks

**`viewafterclose`** finds a column borrowed from a result and used after the result was closed or handed away.

`Int64s`, `Float64s`, `NodeOffsets` and `Valid` hand back the engine's own memory rather than a copy, which is the whole point of them: a million integers cost nothing to read and nothing to hold. What they cost is a lifetime. The slice is valid until `Rows.Close` and not one statement longer, and Go's type system has nothing to say about that.

```go
ids, _ := rows.Int64s(0)
rows.Close()
for _, id := range ids { // ids borrows from rows, and rows.Close has already freed it
	sum += id
}
```

Whether a use can run after a Close is asked of the control flow graph and not of the line numbers, so a use written above the Close and reachable from it counts. The other shape it reports is a view returned out of the function that closes the result, which is the same bug written so that it crashes in the caller.

Handing the result to Arrow ends the same lifetime and is caught the same way, because that call moves the buffers rather than copying them. `Rows.ArrowStream`, `zuarrow.Reader` and `zuarrow.ReaderBatched` all count, and a borrow read after one of them is the worse version of this bug: the memory is alive and belongs to the consumer, so it works until the consumer releases the batch.

```go
ids, _ := rows.Int64s(0)
rdr, _ := zuarrow.Reader(rows)
for _, id := range ids { // ids borrows from rows, and zuarrow.Reader has already handed it to an Arrow consumer
	sum += id
}
```

**`rowserr`** finds a loop over a result that never asks why it ended.

Both loops end quietly. `for rows.Next()` stops on the last row and stops on a failure, and `for row := range rows.All()` does the same, because a range-over-func has nowhere to put an error. `Rows.Err` is where the answer went. A program that does not read it treats a cancelled query, a conflict and a corrupt page as an empty result, which is the failure mode where nobody finds out for a week.

```go
for rows.Next() { // this loop ends on the last row and on a failure alike, and rows.Err is never read
	rows.Scan(&name)
}
```

Only for a result the function made. One that arrived as a parameter, or as the receiver of a method, or that is handed back to the caller, belongs to whoever made it, and so does the question.

**`connshare`** finds a `*zu.Conn` that two goroutines can reach.

A connection is exactly the state that cannot be shared: a file handle, the caches, and the plans compiled against a catalog. A program that queries from four goroutines opens one database and connects four times. The client takes a lock and answers `ZU_MISUSE_CONCURRENT` rather than corrupting anything, so this is a program that fails under load and passes every test.

```go
conn, _ := db.Connect(ctx)
go read(ctx, conn)
read(ctx, conn) // conn is a connection used here and inside a goroutine
```

Handing a connection to one goroutine and never touching it again is not sharing, and is not reported. Three things are: a connection used inside a go statement and outside it as well, a connection used by two go statements, and a go statement inside a loop using a connection made outside it, which is one connection and as many goroutines as the loop runs.

Three calls do not count as sharing. `Interrupt` and `RowsRead` are meant to be made from another goroutine while a statement is running, and both take the read side of the lock to do it: one is what makes a Ctrl-C and a deadline work, the other is what a progress bar asks. `Close` is lifecycle rather than work, and a deferred `Close` beside a goroutine is almost always paired with a join no analyzer can see.

## Turning one off

```go
//zulint:ignore this is the test that proves the refusal
read(ctx, conn)
```

On the line itself or on the line above it. It exists because code that provokes a mistake on purpose is still code, and a test that proves a connection refuses a second goroutine has to give it one. Everything after the word is for whoever reads the line next and is not parsed.

## Using it from somewhere else

Every check is an `analysis.Analyzer`, so `zulint.Analyzers()` plugs into any driver: `singlechecker`, `multichecker`, a `golangci-lint` module plugin, or a `go vet -vettool` binary of your own.

```go
import "github.com/tamnd/zu-go/zulint"

func main() {
	multichecker.Main(zulint.Analyzers()...)
}
```

## A module of its own

This is `github.com/tamnd/zu-go/zulint`, separate from the client, so that a program importing the client does not carry `golang.org/x/tools` in its module graph. It imports nothing from the client: the tests declare the part of the client's surface the checks read, with the real import path and the real method names, which is why they run with no cgo and no library.
