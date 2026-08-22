package zu

/*
#include <zu.h>
*/
import "C"

import (
	"context"
	"runtime"
	"sync"
)

// A DB is a path and a configuration that have been checked against a
// real file. It holds no descriptor and no cache, so it is safe to use
// from any number of goroutines and is the handle to share. What
// cannot be shared is a [Conn].
//
// Closing a DB does not close the connections opened from it: each
// holds its own file handle, and Close releases only the path and the
// configuration.
type DB struct {
	// mu is held for reading by everything that touches the handle
	// and for writing by Close, so that a close cannot free a handle
	// another goroutine is inside a call with. It is not what stops
	// two goroutines from sharing a connection, which is the
	// engine's own guard and answers [Concurrent].
	mu sync.RWMutex
	h  *C.zu_database
	// drop closes the handle if this DB is collected without Close
	// having been called. See the comment in cleanup.go.
	drop runtime.Cleanup
}

// A Config is how a database is opened. The zero value is the default
// in every field, which is what [Open] uses when it is given no
// options.
type Config struct {
	// MemoryLimit is how many bytes the caches may hold. Zero is the
	// default. The unit is bytes and not a suffix, because the two
	// readings of MB differ by 4.9% and the program that knows which
	// one the user meant is the one above this.
	MemoryLimit uint64
	// Threads is how many workers a statement may use. Zero lets the
	// executor pick and one is sequential.
	Threads int
	// ReadOnly opens a descriptor this process cannot write through.
	// A read-only open of a path that is not there fails, since
	// nothing creates a database it may not write to.
	ReadOnly bool
}

// An Option sets one field of a [Config]. The options exist so that
// the common call is one line and the uncommon one does not need a
// struct literal with four zeroes in it.
type Option func(*Config)

// WithMemoryLimit caps the caches at the given number of bytes.
func WithMemoryLimit(bytes uint64) Option {
	return func(c *Config) { c.MemoryLimit = bytes }
}

// WithThreads sets how many workers a statement may use. One is
// sequential, which is what a program running its own pool of
// connections usually wants.
func WithThreads(n int) Option {
	return func(c *Config) { c.Threads = n }
}

// WithReadOnly opens the database without the write side.
func WithReadOnly() Option {
	return func(c *Config) { c.ReadOnly = true }
}

// WithConfig sets every field at once, for a program that reads its
// configuration from somewhere rather than writing it out.
func WithConfig(cfg Config) Option {
	return func(c *Config) { *c = cfg }
}

// settled turns the options into the C struct the ABI takes. The
// struct is versioned by its own size, which is what lets a field
// appended later be invisible to a binding compiled against an older
// header rather than fatal to it.
func settled(opts []Option) C.zu_config {
	var cfg Config
	for _, opt := range opts {
		opt(&cfg)
	}
	var c C.zu_config
	C.zu_config_init(&c)
	c.memory_limit = C.size_t(cfg.MemoryLimit)
	c.threads = C.size_t(cfg.Threads)
	if cfg.ReadOnly {
		c.read_only = 1
	}
	return c
}

// Open opens the database at path. The file is opened once here and
// closed again, so a path that is not a zu database fails now rather
// than on the first query.
//
// The path must exist. [Create] is the call that makes one, and the
// two are separate because an open that quietly created what it did
// not find is the call that writes into the wrong place when a
// deployment gets a path wrong.
func Open(path string, opts ...Option) (*DB, error) {
	cfg := settled(opts)
	p, n := lend(path)
	var h *C.zu_database
	var e *C.zu_error
	st := C.zu_database_open(p, n, &cfg, &h, &e)
	runtime.KeepAlive(path)
	if err := fail(st, e); err != nil {
		return nil, err
	}
	return newDB(h), nil
}

// Create makes a database at path and opens it. The path must not
// exist: a create that opened what it found there would be the call
// that quietly writes into somebody else's data.
//
// What it makes is a valid database with nothing in it, which is what
// a program has to start from to run any statement at all.
func Create(path string, opts ...Option) (*DB, error) {
	cfg := settled(opts)
	p, n := lend(path)
	var h *C.zu_database
	var e *C.zu_error
	st := C.zu_database_create(p, n, &cfg, &h, &e)
	runtime.KeepAlive(path)
	if err := fail(st, e); err != nil {
		return nil, err
	}
	return newDB(h), nil
}

// Memory makes a database that never touches the filesystem. The
// blocks a file would hold are held in memory instead, and the log
// beside it too, so everything above this point runs unchanged and
// nothing survives the process.
//
// Every call makes a database of its own. Two connections on one DB
// are two views of one graph, and two calls to Memory share nothing.
func Memory(opts ...Option) (*DB, error) {
	cfg := settled(opts)
	var h *C.zu_database
	var e *C.zu_error
	if err := fail(C.zu_database_memory(&cfg, &h, &e), e); err != nil {
		return nil, err
	}
	return newDB(h), nil
}

// Connect opens a connection on the database. A connection keeps the
// catalog, the statistics, the plan cache and the block caches
// resident, so queries after the first run without touching the
// catalog on disk. That is why it is per connection rather than per
// database, and why a pool calls Connect once per worker instead of
// sharing one.
//
// The context is honoured for the length of the call, which is short:
// this is a handle being made, not a file being read.
func (db *DB) Connect(ctx context.Context) (*Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.h == nil {
		return nil, misuse("the database is closed")
	}
	var h *C.zu_conn
	var e *C.zu_error
	if err := fail(C.zu_connect(db.h, &h, &e), e); err != nil {
		return nil, err
	}
	return newConn(h), nil
}

// Path is what this process calls the database. For one on disk it is
// the path it was opened with. For one in memory it is a name and not
// a path: it is what an error message needs and not something to open.
// [DB.InMemory] is the way to ask which, rather than parsing this.
func (db *DB) Path() string {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.h == nil {
		return ""
	}
	var p *C.char
	var n C.size_t
	if C.zu_database_path(db.h, &p, &n) != C.ZU_OK {
		return ""
	}
	return text(p, n)
}

// InMemory reports whether this database is the kind that never
// touches the filesystem.
func (db *DB) InMemory() bool {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.h != nil && C.zu_database_is_memory(db.h) == C.ZU_OK
}

// Close releases the path and the configuration. Connections opened
// from this database are not closed with it and keep working, since
// each holds its own file handle.
//
// Close is safe to call twice and safe to call while another goroutine
// is in a call on the same DB: it waits for that call to return.
//
// A DB dropped without it is closed by the collector instead, which is
// a backstop and not a plan: until that happens the configuration and
// the path are still held.
func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.h == nil {
		return nil
	}
	// Stopped before the free rather than after it, which is the
	// order that cannot free twice: once the registration is gone
	// there is no second caller left to reach the handle.
	db.drop.Stop()
	C.zu_database_close(db.h)
	db.h = nil
	return nil
}
