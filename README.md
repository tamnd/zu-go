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

## What you get

- `context.Context` first on every call that can block, and cancelling it calls into the engine's interrupt rather than leaking a goroutine.
- `errors.Is` and `errors.As` against `*zu.Error`, which carries `Code`, `Position`, `DocURL`, and `Retryable`.
- Range-over-func iteration: `rows.All()` is an `iter.Seq[Row]`, and `zu.Iter[T]` streams into your own struct type.
- Three scanning levels: `database/sql`-style `Scan`, generic struct scanning with tags, and a columnar view for the hot path.
- A `database/sql` driver in the `zusql` subpackage, for shops standardized on it, with its limits stated plainly rather than papered over.
- `zulint`, a shipped analyzer for the mistakes that actually happen: using a columnar view after `Close()`, forgetting `rows.Err()`, sharing a `*zu.Conn` across goroutines.

Static libraries are vendored per platform, so `go get` works with `CGO_ENABLED=1` and a C compiler and nothing else. Cross-compilation is documented per target triple with a `zig cc` recipe that actually works, because cross-compiling a cgo project is where Go users abandon a library. `CGO_ENABLED=0` is supported through a `purego` build tag over `dlopen`.

Floor is `go 1.25`, the older of the two supported lines. CI runs 1.25 and 1.26.

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
