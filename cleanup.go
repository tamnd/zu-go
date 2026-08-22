package zu

/*
#include <zu.h>
*/
import "C"

import "runtime"

// The engine's memory is not the collector's. A handle in this package
// names a database, a connection, a statement or a result that lives
// inside the library, and the only thing that gives one back is a
// call. Every type that holds one has Close, and Close in a defer is
// what a program should write, because it gives the memory back at the
// line the program is done with it rather than whenever the collector
// next runs.
//
// A program that forgets is not left leaking. Each handle is
// registered with [runtime.AddCleanup] where it is made, so one that
// becomes unreachable is closed by the runtime, and Close stops the
// registration, so exactly one of the two frees it. Without this a
// dropped connection was memory nothing in the process could reach and
// nothing in the process would free, which is the one kind of leak a
// garbage collected language is not supposed to have. It is also what
// the other clients in this family already do, a Python connection
// going when its last reference does and a JavaScript one at the next
// collection, and a Go program was the only one where dropping a
// handle lost the memory outright.
//
// Nothing here depends on the order the cleanups run in, and that is a
// property of the ABI rather than luck. Closing a database leaves the
// connections opened from it working, a statement can be closed after
// the connection it was prepared on, and a result owns its rows
// outright. So a database, a connection, a statement and a result that
// all become unreachable in the same collection may be given back in
// any order.
//
// What it is not is a substitute for Close. A cleanup runs at a moment
// nobody chose, and until it does the file handle, the caches and the
// plans are still held. That is why the misuse suite counts open
// descriptors after a drop rather than only after a close: the promise
// is that they come back, not that they come back at once.
func onDrop[T any, H any](obj *T, free func(H), h H) runtime.Cleanup {
	return runtime.AddCleanup(obj, free, h)
}

// The four calls that give a handle back, written as functions rather
// than inline so that what a cleanup runs and what Close runs are
// visibly the same call. The argument is a C pointer, which holds no
// Go pointer and so can be the value a cleanup keeps.

func closeDatabase(h *C.zu_database) { C.zu_database_close(h) }

func closeConn(h *C.zu_conn) { C.zu_conn_close(h) }

func closeStmt(h *C.zu_stmt) { C.zu_stmt_close(h) }

func freeResult(h *C.zu_result) { C.zu_result_free(h) }

// newDB takes ownership of a database handle.
func newDB(h *C.zu_database) *DB {
	db := &DB{h: h}
	db.drop = onDrop(db, closeDatabase, h)
	return db
}

// newConn takes ownership of a connection handle.
func newConn(h *C.zu_conn) *Conn {
	c := &Conn{h: h}
	c.drop = onDrop(c, closeConn, h)
	return c
}

// newStmt takes ownership of a statement handle prepared on c.
func newStmt(c *Conn, h *C.zu_stmt) *Stmt {
	s := &Stmt{conn: c, h: h}
	s.drop = onDrop(s, closeStmt, h)
	return s
}
