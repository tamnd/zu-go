// Package zu is the Go client for zu, an embedded property-graph
// database.
//
// A database is a file and a configuration. A connection is the state
// that cannot be shared: a file handle, the caches, and the plans
// compiled against a catalog. A program that queries from four
// goroutines opens one database and connects four times, which is one
// [DB] and four calls to [DB.Connect].
//
//	db, err := zu.Open("social.zu1")
//	if err != nil {
//		return err
//	}
//	defer db.Close()
//
//	conn, err := db.Connect(ctx)
//	if err != nil {
//		return err
//	}
//	defer conn.Close()
//
// Close is what gives the engine's memory back, and the two defers
// above are how a program should say so. A handle dropped without one
// is not a leak either: the collector closes it when it gets to it,
// which is later than a program holding a file handle and a set of
// caches wants and is still an answer rather than memory nobody can
// reach.
//
// Every call that can block takes a [context.Context] first and
// returns an error last. Cancelling that context calls into the
// engine's own interrupt, so a query that is halfway through a hundred
// million rows stops at the next chunk boundary rather than running to
// the end with nobody waiting for it. Nothing here panics on a failure
// the engine reported: a bad statement, a missing file and a lost
// write are all errors.
//
// A failure carries the whole diagnostic record rather than a
// sentence, so [errors.As] onto an [*Error] gives the GQLSTATUS
// condition, the line and column, the page that explains the
// condition, and whether running the same statement again could work.
// [errors.Is] against a [Status] answers the coarser question.
//
// This package is cgo over the engine's C ABI. The static library for
// your platform ships with it, so `go get` and `go build` need no Rust
// toolchain, no pkg-config and nothing installed. See [linking] for the
// two build tags that point it somewhere else.
//
// [linking]: https://github.com/tamnd/zu-go#linking
package zu

/*
#include <stdlib.h>
#include <zu.h>

// ZU_ABI_VERSION is a string macro, and cgo reads macros that expand
// to integers. One function makes it readable from Go, which is what
// the test that holds this binding to a revision of the header needs.
static const char *zu_go_abi_version(void) { return ZU_ABI_VERSION; }
*/
import "C"

import (
	"unsafe"
)

// Version is the version of the engine this binding is linked
// against. The engine, the C ABI and this client move on one version
// number, so a release here always pairs with the same release of the
// engine.
func Version() string {
	return C.GoString(C.zu_version())
}

// ABIVersion is the revision of the C ABI the linked library was
// compiled with. The two numbers are counts rather than the halves of
// a decimal, so 0.12 is the revision after 0.11 and a caller comparing
// them compares each on its own.
func ABIVersion() string {
	return C.GoString(C.zu_go_abi_version())
}

// text turns a borrowed C string and its length into a Go string. The
// engine hands back a pointer and a length because most languages have
// counted strings, and a copy is made here because everything the
// engine lends is good only until the handle it came from is freed.
func text(p *C.char, n C.size_t) string {
	if p == nil {
		return ""
	}
	return C.GoStringN(p, C.int(n))
}

// lend passes a Go string to C as a pointer and a length, without a
// copy. The bytes of a string hold no Go pointers, so passing them is
// allowed, and none of the calls here keeps what it is lent past the
// return. The caller keeps the string alive across the call with
// [runtime.KeepAlive].
//
// An empty string is a nil pointer and a zero length, which the ABI
// reads as the empty string rather than as a mistake.
func lend(s string) (*C.char, C.size_t) {
	if len(s) == 0 {
		return nil, 0
	}
	return (*C.char)(unsafe.Pointer(unsafe.StringData(s))), C.size_t(len(s))
}
