# zu for Go

The Go client for [zu](https://github.com/tamnd/zu), an embedded property-graph database.

```go
package main

import (
	"context"
	"fmt"
	"log"

	zu "github.com/tamnd/zu-go"
)

func main() {
	ctx := context.Background()

	db, err := zu.Open("social.zu1")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	conn, err := db.Connect(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	rows, err := conn.Query(ctx, `MATCH (p:Person) RETURN p.name AS name, p.id AS id LIMIT 5`)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	for row := range rows.All() {
		var name string
		var id int64
		if err := row.Scan(&name, &id); err != nil {
			log.Fatal(err)
		}
		fmt.Println(name, id)
	}
	if err := rows.Err(); err != nil {
		log.Fatal(err)
	}
}
```

```
go get github.com/tamnd/zu-go
```

Longer than the other clients' first example, and correct by the standards of Go. A binding that hides errors to look short is a binding Go programmers will not trust.

## Building

This client is cgo over the engine's C ABI, so it needs `libzu` where pkg-config can find it. Until the static libraries are vendored, build the engine and point at what it staged.

```
git clone https://github.com/tamnd/zu
cd zu
cargo build --release -p zu-capi -p zu-cli
cargo run -p xtask -- package --stage dist/libzu --built target/release \
    --target "$(rustc -vV | sed -n 's/^host: //p')" --syslibs "$(cargo rustc -q --release \
    -p zu-capi --crate-type staticlib -- --print native-static-libs 2>&1 |
    sed -n 's/^note: native-static-libs: //p' | tail -1)"
```

```
export PKG_CONFIG_PATH=/path/to/zu/dist/libzu/lib/pkgconfig
go test ./...
```

The CLI is built beside the library because the staging step packages both, and the `--syslibs` line is asked of rustc rather than written down because the answer differs per target and changes with the toolchain.

## What you get

- `context.Context` first on every call that can block. Cancelling it calls into the engine's interrupt, and the failure that comes back answers `errors.Is` against both `context.Canceled` and `zu.Interrupted`, because a caller who wrote the deadline and a caller handling the failure ask different questions of the same error. A context that can never be cancelled starts no goroutine.
- `errors.Is` and `errors.As` against `*zu.Error`, which carries `Code`, `Message`, `StandardText`, `DocURL`, `Severity`, `Retryable`, `Position` and the source line the position points into. The statuses are sentinels of their own: `errors.Is(err, zu.Conflict)` is the retry question and needs no unwrapping.
- Range-over-func iteration: `rows.All()` is an `iter.Seq[Row]`, and `zu.Iter[T]` is an `iter.Seq2[T, error]` that streams into your own type.
- Three levels of reading, in the order you reach for them: `Scan` into concrete destinations, `Collect[T]` and `Iter[T]` into a struct matched by `zu` tags or by name, and `Int64s`, `Float64s`, `NodeOffsets` and `Valid` for a whole column borrowed from the result without a copy.
- The seven temporal types spelled out rather than flattened into `time.Time`. A date is a `zu.Date`, a time of day is a `zu.LocalTime`, and a year-month duration is a `zu.YearMonth`, because a `time.Time` made out of a time of day is a date somebody invented. The three that name an instant scan into a `time.Time` when you ask for one.
- Transactions with `Begin`, `BeginReadOnly`, `Commit` and `Rollback`, where a rollback deferred beside a commit answers `zu.ErrDone` rather than a failure.

Reading a column of integers a row at a time allocates nothing at all, and so does collecting a whole result into a slice of structs: the out-parameters every C accessor writes through are fields of the result rather than locals, and the destinations a struct scan writes into are taken once rather than at every row.

Floor is `go 1.25`, the older of the two supported lines. CI runs 1.25 and 1.26 on Linux and macOS, under the race detector.

## Concurrency

A `*zu.DB` is safe to share. A `*zu.Conn` is not, and neither is the `*zu.Rows` it produced. Two goroutines on one connection are refused rather than raced: the second one answers `zu.Concurrent` and nothing was done. A program that queries from several goroutines gives each one its own connection, which is `db.Connect` or `conn.Duplicate`.

`Interrupt` is the exception and the point of it: it is meant to be called from another goroutine while a statement runs, and so is `RowsRead`, which is what a progress bar reads.

A result owns its rows outright, so it stays readable after the connection that produced it has gone back to a pool. What it does not outlive is `Close`, and that includes every slice the columnar readers handed back.

## Not here yet

The pieces of this client that milestone DX4 lists and this first release does not have: the `zusql` `database/sql` subpackage, vendored static libraries per platform with the cross-compilation recipes, the `purego` build over `dlopen` for `CGO_ENABLED=0`, and the `zulint` analyzer for the mistakes that actually happen, which are a columnar view used after `Close`, a loop that never reads `rows.Err()`, and a `*zu.Conn` shared across goroutines.

The engine itself has no way to name a node's table, so a `zu.Node` carries the numeric table id the ABI gives it.

## Specification

Spec/2064g/dx/07-go.md in [tamnd/zu](https://github.com/tamnd/zu). Milestone: DX4 (tamnd/zu#170).

## Status

Pre-1.0 and pre-release. Nothing is published yet. The engine, the C ABI, and this client all move on one version number, so a release here always pairs with the same release of [`tamnd/zu`](https://github.com/tamnd/zu).

## Where things live

| What | Where |
|---|---|
| Engine, Rust SDK, CLI, `zu.h`, conformance corpus | [tamnd/zu](https://github.com/tamnd/zu) |
| Documentation and website | [tamnd/zu-web](https://github.com/tamnd/zu-web) |
| This client | here |

If a bug reproduces through the `zu` CLI, it belongs in [tamnd/zu](https://github.com/tamnd/zu/issues), not here.

## License

Apache-2.0, same as the engine.
