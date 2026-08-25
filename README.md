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

	db, err := zu.Create("social.zu1")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	conn, err := db.Connect(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	for _, q := range []string{
		`INSERT (p:person {id: 1, name: 'ada'})`,
		`INSERT (p:person {id: 2, name: 'grace'})`,
		`INSERT (p:person {id: 3, name: 'lynn'})`,
	} {
		if err := conn.Exec(ctx, q); err != nil {
			log.Fatal(err)
		}
	}

	rows, err := conn.Query(ctx, `MATCH (p:person) RETURN p.name AS name, p.id AS id`)
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

Longer than the other clients' first example, and correct by the standards of Go. A binding that hides errors to look short is a binding Go programmers will not trust. It writes `social.zu1` beside you and prints three names, and it is run exactly as printed by this repository's own tests, which is the only way a first example stays true.

Run it twice and the second run fails, because `Create` refuses a path that is already there and `Open` is the call for a database that exists. They are two calls rather than one flag so that a deployment given the wrong path is told about it instead of quietly starting from empty.

## Linking

This client is cgo over the engine's C ABI, so something has to supply `libzu` and `zu.h`. There are three ways it can happen and you pick one with a build tag. The default needs nothing installed.

**The library that ships with the module.** No tag, no Rust toolchain, no pkg-config, nothing on your machine. The static archive for your platform is a module of its own that the client requires, and `go build` downloads that one and links it in.

```
go get github.com/tamnd/zu-go
go build ./...
```

The archives live in `lib/<goos>-<goarch>`, one module each, and each one carries a `REVISION` naming the commit of the engine it was built from and a `NATIVE_STATIC_LIBS` naming what rustc said that build needs at link time. They are built by the [Libraries workflow](.github/workflows/lib.yml) on a runner whose own platform is the target, never by hand. Five platforms ship:

| GOOS | GOARCH | Rust target |
| --- | --- | --- |
| darwin | arm64 | `aarch64-apple-darwin` |
| darwin | amd64 | `x86_64-apple-darwin` |
| linux | amd64 | `x86_64-unknown-linux-gnu` |
| linux | arm64 | `aarch64-unknown-linux-gnu` |
| windows | amd64 | `x86_64-pc-windows-gnu` |

The windows archive is built for the gnu ABI rather than msvc, because cgo drives a gcc-family linker there and cannot read an archive an MSVC toolchain produced. The linux archives are built against glibc, so a musl distribution such as Alpine wants `-tags zu_system` and a `libzu` of its own.

One module each is what makes a build download one archive. `go build`, with the requirement already in your `go.mod`, fetches the platform it links and nothing else: 6 MB over the wire and 29 MB in the module cache. `go get` and `go mod tidy` read the whole module graph rather than the build's, so they fetch all five, which is 27 MB over the wire and 115 MB in the cache. That is the price of adding the dependency and it is paid once, but it is worth knowing before an image build pays it on every layer.

**A libzu you installed,** through pkg-config, with `-tags zu_system`. This is the mode a bisect wants, because it links whatever the engine's working tree just produced rather than the archive this module froze.

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
export LD_LIBRARY_PATH=/path/to/zu/dist/libzu/lib     # linux only
go test -tags zu_system ./...
```

Two variables and not one, on Linux, because pkg-config answers the compiler and nothing answers the loader. A library staged into a directory of its own is found at link time by the `-L` that pkg-config hands over and found at run time by nothing, so a binary links and then dies on the first exec with a message about a shared object rather than about a database. On macOS the dylib carries an absolute install name and needs no second variable.

The CLI is built beside the library because the staging step packages both, and the `--syslibs` line is asked of rustc rather than written down because the answer differs per target and changes with the toolchain.

**An archive of your own,** named in `CGO_LDFLAGS`, with `-tags zu_static`. Nothing is looked up and nothing is assumed. The header that ships with this module is still the one that gets included, which is what keeps the binding honest about the ABI it was written against.

Building the archive that goes there is one command, and `--crate-type staticlib` rather than a plain build is the whole trick: the crate also declares a cdylib and a rlib, and asking cargo for all three leaves an archive three times the size with the same symbols in it. The same command prints what has to be linked beside it, which is a different list on every platform and is not worth guessing at.

```
cargo rustc --release -p zu-capi --crate-type staticlib -- --print native-static-libs
```

```
export CGO_LDFLAGS="/path/to/libzu.a $syslibs"
go test -tags zu_static ./...
```

On macOS the three it names are already on every cgo link, so passing them again only makes the linker warn about duplicates. That is what the `NATIVE_STATIC_LIBS` file beside each shipped archive is for: it is the full list rustc gave, kept next to the shorter one `prebuilt.go` actually passes, so the difference is visible rather than lost.

A platform with no shipped archive and no tag is told so by name at compile time rather than by an undefined symbol at link time. So is `CGO_ENABLED=0`, which cannot build this client at all.

### Cross-compiling

cgo turns itself off the moment `GOOS` or `GOARCH` stops being the host's, so every recipe here starts by turning it back on and pointing `CC` at a compiler that targets what you asked for. The shipped archive for the target is picked up on its own.

From a Mac to the other Mac, using the toolchain you already have:

```
CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 CC="clang -arch x86_64" go build ./...
CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 CC="clang -arch arm64" go build ./...
```

To Linux and to Windows, using [zig](https://ziglang.org) as the cross compiler, which is one download and covers every target here. Go hands the C compiler a few flags zig declines, so it goes through a wrapper rather than being named directly:

```
cat > /usr/local/bin/zcc <<'EOF'
#!/bin/sh
exec zig cc -target "$ZIG_TARGET" "$@"
EOF
chmod +x /usr/local/bin/zcc
```

```
ZIG_TARGET=x86_64-linux-gnu.2.28  CGO_ENABLED=1 GOOS=linux   GOARCH=amd64 CC=zcc go build ./...
ZIG_TARGET=aarch64-linux-gnu.2.28 CGO_ENABLED=1 GOOS=linux   GOARCH=arm64 CC=zcc go build ./...
ZIG_TARGET=x86_64-windows-gnu     CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=zcc go build ./...
```

The glibc version in the target triple is the floor the binary will run against. 2.28 is the right number because the shipped Linux archives are built in a `manylinux_2_28` container, which is the whole reason the Libraries workflow uses one. Naming a newer version produces a binary that will not start on an older distribution, and naming an older one does not make the archive inside it any older.

Cross-compiling **to** darwin from anything that is not darwin is the one direction none of this covers, because it needs Apple's SDK and Apple's linker. Build those two on a Mac.

## What you get

- `context.Context` first on every call that can block. Cancelling it calls into the engine's interrupt, and the failure that comes back answers `errors.Is` against both `context.Canceled` and `zu.Interrupted`, because a caller who wrote the deadline and a caller handling the failure ask different questions of the same error. A context that can never be cancelled starts no goroutine.
- `errors.Is` and `errors.As` against `*zu.Error`, which carries `Code`, `Message`, `StandardText`, `DocURL`, `Severity`, `Retryable`, `Position` and the source line the position points into. It also carries the rest of the diagnostic record ISO 39075 subclause 23.2 asks for: `SubjectKind` and `Subject` are what the condition is about when it is about something the statement named, and `Graph` and `Schema` are where the statement was running. A program that highlights the offending label in an editor reads the name off the record rather than out of an English sentence. The statuses are sentinels of their own: `errors.Is(err, zu.Conflict)` is the retry question and needs no unwrapping.
- Range-over-func iteration: `rows.All()` is an `iter.Seq[Row]`, and `zu.Iter[T]` is an `iter.Seq2[T, error]` that streams into your own type.
- Three levels of reading, in the order you reach for them: `Scan` into concrete destinations, `Collect[T]` and `Iter[T]` into a struct matched by `zu` tags or by name, and `Int64s`, `Float64s`, `NodeOffsets` and `Valid` for a whole column borrowed from the result without a copy.
- The seven temporal types spelled out rather than flattened into `time.Time`. A date is a `zu.Date`, a time of day is a `zu.LocalTime`, and a year-month duration is a `zu.YearMonth`, because a `time.Time` made out of a time of day is a date somebody invented. The three that name an instant scan into a `time.Time` when you ask for one.
- A byte string as a `[]byte` and a character string as a `string`, kept apart the way the engine keeps them apart. `X'0041'` is two octets and not the letter A, so it scans into a `[]byte` and not into a `string`, and a `string` scans into a `[]byte` because that is what is underneath it.
- An exact decimal as a `zu.Decimal`, which is a `*big.Int` of unscaled units and the number of digits after the point. `CAST('1.20' AS DECIMAL(5, 2))` comes back with both places, because a tenth is not a binary fraction and a `float64` would have lost them before you saw them. It widens into a `float64` if you scan it into one, and `Decimal.Float64` is the same conversion with an answer about whether it survived.
- Transactions with `Begin`, `BeginReadOnly`, `Commit` and `Rollback`, where a rollback deferred beside a commit answers `zu.ErrDone` rather than a failure.
- A `zu.Loader` that builds a database out of whole columns and an edge list, which is the section after next and the only way a Go program makes a graph with edges in it.
- A whole result as Arrow record batches for the price of a pointer a column, through `zuarrow`, which is the section below.

Reading a column of integers a row at a time allocates nothing at all, and so does collecting a whole result into a slice of structs: the out-parameters every C accessor writes through are fields of the result rather than locals, and the destinations a struct scan writes into are taken once rather than at every row.

Floor is `go 1.26.6`. CI runs the floor and whatever is current on Linux and macOS, under the race detector.

## Bulk load

The quickstart writes rows with `INSERT`, which needs a table. Nothing in v0 makes one, and neither does anything make a rel table, so a program that wants a graph with edges in it starts from a `zu.Loader` rather than from a statement.

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

	loader, err := zu.NewLoader("graph.zu1")
	if err != nil {
		log.Fatal(err)
	}
	defer loader.Close()

	// The tables first, and how many rows the node table has. The count
	// is given rather than counted from the first column, so a column
	// with a value missing is an error and not a shorter table.
	if err := loader.Table("person", "knows", 3); err != nil {
		log.Fatal(err)
	}
	if err := loader.Int64s("id", []int64{1, 2, 3}); err != nil {
		log.Fatal(err)
	}
	if err := loader.Strings("name", []string{"ada", "grace", "lynn"}); err != nil {
		log.Fatal(err)
	}
	// The row each edge starts at and the row it ends at, in two
	// columns, because a program that has them that way passes what it
	// has. They are sorted and deduplicated for you.
	if err := loader.Edges([]uint32{0, 1}, []uint32{1, 2}); err != nil {
		log.Fatal(err)
	}
	// Nothing has reached the file until here.
	if err := loader.Finish(ctx); err != nil {
		log.Fatal(err)
	}

	db, err := zu.Open("graph.zu1", zu.WithReadOnly())
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	conn, err := db.Connect(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	rows, err := conn.Query(ctx, `MATCH (a:person)-[:knows]->(b:person)
		RETURN a.name AS a, b.name AS b`)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	for row := range rows.All() {
		var a, b string
		if err := row.Scan(&a, &b); err != nil {
			log.Fatal(err)
		}
		fmt.Println(a, "knows", b)
	}
	if err := rows.Err(); err != nil {
		log.Fatal(err)
	}
}
```

```
ada knows grace
grace knows lynn
```

The order is fixed: `NewLoader`, then `Table`, then columns and edges in any order and as many calls as you like, then `Finish`. A column before the table is a `zu.Misuse`, and so is a column whose length disagrees with the row count, reported at the call that passed it rather than at the finish, so the message names the column while you still know which one you were building.

A loader is columnar for the same reason a result is, and the names say so: `Loader.Int64s` against `Rows.Int64s`, `Loader.Float64s` against `Rows.Float64s`. A column read out of one database goes into another unchanged, and that includes the temporals, which are five typed methods taking the same `zu.Date`, `zu.LocalTime`, `zu.LocalDateTime`, `zu.YearMonth` and `time.Duration` a result hands back.

There is no method for a zoned time or a zoned datetime, and that is deliberate. A stored column has nowhere to keep the offset that makes those two what they are, so they are values a query can produce and not values a column can hold. The C ABI refuses them at run time; here they are absent from the type system instead, because a program with one of them has to say what it means to do with the offset and dropping it quietly is the one thing that must not happen.

Three things worth knowing before you build a pipeline on it.

`Finish` takes a context and checks it before the write rather than during it, because the ABI has no interrupt for a loader. A load of a hundred million rows cannot be cancelled halfway, and answering the caller before the file was written would be worse than making them wait. What the context buys is one that was already cancelled costing nothing.

A loader that never reaches its `Finish` leaves a file at the path, and that file is not empty. It is a whole database with the node table already in it and no rows in that table. So an interrupted load leaves a path that no second load may start on, since `NewLoader` refuses one that exists, and that opens perfectly well as a graph with nothing in it. Load into a temporary path and rename it once `Finish` returns, which is the shape that does not have the problem at all.

Every array but two crosses without a copy: `[]int64`, `[]float64`, `[]uint32` and `[]time.Duration` are passed to the engine as the backing array you handed over, and the engine takes its own copy before the call returns, so you may reuse the slice immediately. `[]bool` is rebuilt because the ABI carries a boolean as an `int32`. `[]string` is copied into memory this package owns, one allocation for all the bytes rather than one per string, because an array of pointers into Go strings is not something a Go program may hand to C at all.

One core of a shared AMD EPYC, a hundred thousand rows a column. The machine is worth naming because it is several times slower than the M4 the Arrow numbers below were taken on, and what matters here is the ratio rather than the absolute.

| What | Per column | Per row |
|---|---|---|
| `Loader.Int64s` | lost in the noise of the load around it | |
| `Loader.Strings`, the copy this package does | 26 ms | 260 ns |
| `Loader.Strings`, all in | 235 ms | 2.4 µs |

A column of integers costs nothing measurable above making the database and naming the table, which is what passing a pointer and a length is supposed to look like. A column of strings costs about two microseconds a row inside the engine, and it costs the same whether the strings are fifteen bytes or empty, so it is per string and not per byte. That is the engine's to improve and not this binding's, and it is written down here rather than left for somebody to find at a hundred million rows.

## Arrow

A result leaves the engine as Arrow without being copied on the way. `zuarrow` is a module of its own so that the client keeps its zero dependencies, and it is thirty lines over `arrow-go`, because the work happens on the other side of the C Data Interface.

```go
import "github.com/tamnd/zu-go/zuarrow"

rdr, err := zuarrow.Query(ctx, conn, "MATCH (p:person) RETURN p.id AS id")
if err != nil {
	return err
}
defer rdr.Release()

for rdr.Next() {
	ids := rdr.RecordBatch().Column(0).(*array.Int64)
	for i := 0; i < ids.Len(); i++ {
		sum += ids.Value(i)
	}
}
return rdr.Err()
```

`zuarrow.Reader` takes a `*zu.Rows` you already have, `ReaderBatched` takes the rows per batch, and `Query` is the two of them in one call. All three hand back an `array.RecordReader`, which is what every Arrow consumer in Go already takes.

Ten thousand integers, on an M4:

| | |
|---|---|
| `zuarrow.Reader`, read through arrow-go | 18 µs, 44 allocs |
| `Rows.Int64s`, the borrowed column | 6.8 µs, 1 alloc |
| the export itself, `Rows.ArrowStream` | 6 µs, 2 allocs |

The export is well under a microsecond per thousand rows because nothing is per row: the executor's own column buffers are moved into the Arrow arrays and the stream is handed the result. The borrowed column costs about the same, and for the same reason, since the C accessor under it hands back the buffer the executor filled rather than converting one of its own. Almost all of what is left in both numbers is the loop that adds ten thousand integers up.

Two consequences of moving rather than copying, and both are the price of the number above. Exporting spends the result: the `*zu.Rows` is empty afterwards, every slice a columnar reader handed out before it now belongs to the Arrow consumer, and [`zulint`](zulint) reports a read of one. And a result the engine had to build across rows, which is anything with an `ORDER BY`, has no buffers to move and falls back to a cell at a time, which is closer to a hundred times slower and still correct.

`Rows.ArrowStream` is the layer under all of this, for a program that already has an `ArrowArrayStream` to fill and its own idea of what to do with it. It takes the address of one as an `unsafe.Pointer` and fills it in place, and the consumer's release callback is what frees the result.

## database/sql

`zusql` registers the driver under the name `zu`, for a program that is already built around `database/sql` and would rather not carry a second shape of database handle.

```go
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	_ "github.com/tamnd/zu-go/zusql"
)

func main() {
	ctx := context.Background()

	db, err := sql.Open("zu", "social.zu1?create=true")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if _, err := db.ExecContext(ctx, `INSERT (p:person {id: 1, name: 'ada'})`); err != nil {
		log.Fatal(err)
	}

	rows, err := db.QueryContext(ctx, `MATCH (p:person) RETURN p.name AS name`)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			log.Fatal(err)
		}
		fmt.Println(name)
	}
	if err := rows.Err(); err != nil {
		log.Fatal(err)
	}
}
```

The connection string is the path, or `:memory:`, followed by `create`, `read_only`, `threads` and `memory_limit`. Every connection from one `sql.DB` shares one `zu.DB`, which is what makes `:memory:` a database the pool can hand out rather than a new empty one per connection. `sql.Open` opens nothing, so a bad path fails at the first `Ping` and fails the same way on every later connection.

Three things `database/sql` assumes are not true here, and the package says so rather than letting you find out.

- A statement that writes reports nothing. There is no rows-affected count and no last-insert-id in the engine, so `sql.Result` answers both with an error saying which one is missing rather than a zero somebody reads as a count.
- Parameters are named and never positional. Every argument is a `sql.Named`, and a positional one is refused with a message that says what to write instead.
- A value that is not a number, a string, a bool or an instant comes back as itself: a `zu.Node`, a `zu.Path`, a `zu.Record`, a list as a `[]any`, one of the temporals that does not name an instant. `database/sql` passes those through to `Scan` unchanged, so a destination of that type or of `any` takes them and a `*string` does not.

What is not lost by coming through here: the pool, the context handling, the transactions, and every error, which arrives as the same `*zu.Error` the client raises and answers to the same `errors.Is` and `errors.As`. `CheckNamedValue` takes every argument through untouched, so a `time.Time` keeps its zone and an `int32` stays an `int32` instead of being flattened into `database/sql`'s six types before the bind sees it. `zusql.Underlying`, through `sql.Conn.Raw`, reaches the connection itself for the calls `database/sql` has no word for: `Interrupt` from another goroutine, `RowsRead` for a progress bar, the columnar reads.

The cost of the extra interface, on an M4:

| | client | `database/sql` |
|---|---|---|
| one row | 4.8 µs, 7 allocs | 5.4 µs, 15 allocs |
| a thousand rows, scanned | 714 µs, 1006 allocs | 696 µs, 1757 allocs |

The per-row difference is the `driver.Value` boxing, which the interface requires and no driver can avoid.

## Concurrency

A `*zu.DB` is safe to share. A `*zu.Conn` is not, and neither is the `*zu.Rows` it produced. Two goroutines on one connection are refused rather than raced: the second one answers `zu.Concurrent` and nothing was done. A program that queries from several goroutines gives each one its own connection, which is `db.Connect` or `conn.Duplicate`.

`Interrupt` is the exception and the point of it: it is meant to be called from another goroutine while a statement runs, and so is `RowsRead`, which is what a progress bar reads.

A result owns its rows outright, so it stays readable after the connection that produced it has gone back to a pool. What it does not outlive is `Close`, and that includes every slice the columnar readers handed back. Exporting to Arrow ends the same lifetime for the same reason, since it moves those buffers to the consumer instead of freeing them.

## Static checks

Three of the rules above are the kind a compiler cannot enforce and a review does not catch, so there is a vet tool for them in [`zulint`](zulint).

```
go install github.com/tamnd/zu-go/zulint/cmd/zulint@latest
zulint ./...
```

It reports a columnar view used after the result it borrows from was closed or handed to Arrow, a loop over a result that never reads `rows.Err()`, and a `*zu.Conn` that two goroutines can reach. All three compile, pass review, and are a use-after-free, a swallowed failure and a refused query at run time. It reads source: no engine, no C toolchain, no database. This client runs it on itself in CI, and the one test that provokes the third on purpose says so with a `//zulint:ignore` comment.

## Conformance

Every zu client answers the same corpus. The cases live in [tamnd/zu](https://github.com/tamnd/zu) under `conformance/cases`, twenty five files of them, and each one is a statement written in GQL and the rows or the GQLSTATUS it owes. Nothing in a case names a client's own API, which is what makes one corpus serve five languages: a report from this runner is diffed line for line against the Rust, Python, Node and Java runners, and a line that differs is one client disagreeing with the other four about what the engine said.

The runner is in [`corpus`](corpus), a module of its own so that nobody importing this client to query a graph carries a YAML reader and a value encoding along with it. It depends on this client and on nothing else.

```
git clone https://github.com/tamnd/zu ../zu
ZU_CASES=../zu/conformance/cases go test ./corpus/...
```

Without `ZU_CASES` the tests that need the corpus skip and the rest still run, so a clone of this repository alone is still green. There is a command beside the package for a run outside `go test`:

```
go run ./corpus/cmd/corpus ../zu/conformance/cases
```

Check the cases out at the revision `lib/<goos>-<goarch>/REVISION` names rather than at the engine's HEAD. The corpus is the contract and the engine catches up to it, so a corpus ahead of the library reports the engine catching up to its own cases as this client failing. CI runs it both ways for that reason: against the shipped archive at the revision it was built from, where every case is expected to pass, and against the engine at HEAD, where a case the engine has not answered yet is reported as unsupported and is not a failure.

## Not here yet

The pieces of this client that milestone DX4 lists and this release does not have: the `purego` build over `dlopen` for `CGO_ENABLED=0`.

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

Inside this repository:

| What | Where |
|---|---|
| The client | the root package, `github.com/tamnd/zu-go` |
| The `database/sql` driver | `zusql` |
| The Arrow reader, a module of its own | `zuarrow` |
| The static checks, a module of their own | `zulint` |
| The cross client corpus runner, a module of its own | `corpus` |
| Every published name and the shape it is published in | `api/surface.txt` |
| The gates that read this repository rather than run it | `internal` |
| `zu.h`, the copy this binding was written against | `include` |
| One static library per platform, one module each | `lib/<goos>-<goarch>` |
| Which of the three linking modes is in force | `linking.go`, `linking_system.go`, `linking_static.go` |
| Tagging the libraries, then the client, then `zuarrow` | `scripts/release.sh` |

`api/surface.txt` is generated, in the shape of the `api/go1.N.txt` files the language itself is held to. Regenerate it with `go test ./internal/api -update` and review it like any other file: a name that arrived is a minor release, a name that went or changed shape is a major one or a mistake, and the gate says which of the three a diff is while it is still a diff. It reads source and links nothing, as do the gate that holds every published name to carrying a doc comment and the gate that holds every published signature to the shapes Go writes, so all three answer on a clone with no library staged and no Rust installed.

Eight modules in one `go.work`, which is the client, the five libraries, `zuarrow` and `corpus`, and that is what makes a fresh clone build with nothing installed. `go mod tidy` is the one command it does not cover. The workspace names the six unpublished modules in `replace` directives as well as in `use`, and the comment at the top of `go.work` says why: a `use` directive is enough until something in the workspace imports a module from outside it, and then the build list has to be computed, and computing it means reading the `go.mod` of every requirement by version. `zulint` is a ninth module and a workspace of its own on purpose, so that the `golang.org/x/tools` it needs never reaches anybody who only imports the client.

## License

Apache-2.0, same as the engine.
