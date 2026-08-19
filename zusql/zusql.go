// Package zusql is a database/sql driver over [zu], for a program that
// is already built around database/sql and would rather not have a
// second shape of database handle in it.
//
//	import (
//		"database/sql"
//		_ "github.com/tamnd/zu-go/zusql"
//	)
//
//	db, err := sql.Open("zu", "social.zu1")
//
// The client itself is the better interface, and this is worth being
// plain about rather than discovering. database/sql was designed around
// SQL and around a server, and three things it assumes are not true
// here.
//
// A statement that writes reports nothing. There is no rows-affected
// count and no last-insert-id in the engine, so [sql.Result] answers
// both with an error that says so rather than with a zero somebody
// reads as a count.
//
// Parameters are named and never positional. zuQL has no `?` and no
// `$1`, so every argument has to be a [database/sql.Named], and a
// positional one is refused with a message rather than bound to the
// wrong thing.
//
// A value that is not a number, a string or a time comes back as
// itself: a [zu.Node], a [zu.Path], a [zu.Record], one of the seven
// temporals. database/sql passes those through to Scan unchanged, so
// scanning into an any or into a pointer of that type works, and
// scanning into a string does not.
//
// What is not lost by coming through here: the pool, the context
// handling, the transactions, and every error, which arrives as the
// same *[zu.Error] the client raises and answers to the same
// [errors.Is] and [errors.As]. [Underlying] reaches the connection
// itself for the calls database/sql has no word for.
//
// # The connection string
//
// The path, or ":memory:" for a database that never touches the
// filesystem, followed by options:
//
//		social.zu1
//		social.zu1?create=true&threads=4
//		:memory:
//		/var/lib/app/graph.zu1?read_only=true&memory_limit=1073741824
//
//	  - create=true opens the database if it is there and makes it if it
//	    is not, which is the behaviour a service starting for the first
//	    time wants and the one [zu.Open] deliberately does not have.
//	  - read_only=true opens a descriptor this process cannot write
//	    through.
//	  - threads=N is how many workers a statement may use.
//	  - memory_limit=N is how many bytes the caches may hold.
//
// Every connection from one [database/sql.DB] shares one [zu.DB], which
// is what makes ":memory:" a database the pool can hand out rather than
// a new empty one per connection.
package zusql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"

	zu "github.com/tamnd/zu-go"
)

func init() {
	sql.Register("zu", Driver{})
}

// Driver is the database/sql driver, registered as "zu".
type Driver struct{}

var (
	_ driver.Driver        = Driver{}
	_ driver.DriverContext = Driver{}
)

// Open opens one connection. database/sql calls [Driver.OpenConnector]
// instead whenever it can, which is every version that has had it, and
// this is here for the interface.
func (d Driver) Open(name string) (driver.Conn, error) {
	c, err := d.OpenConnector(name)
	if err != nil {
		return nil, err
	}
	return c.Connect(context.Background())
}

// OpenConnector parses the connection string once, so that a pool
// opening its tenth connection does not parse it a tenth time and
// cannot fail on it at a different moment than the first.
func (Driver) OpenConnector(name string) (driver.Connector, error) {
	cfg, err := parse(name)
	if err != nil {
		return nil, err
	}
	return &connector{cfg: cfg}, nil
}

// A config is a connection string that has been read.
type config struct {
	path   string
	memory bool
	create bool
	opts   []zu.Option
}

// parse reads a connection string. The query is split off at the last
// question mark rather than by a URL parser, because a Windows path is
// not a URL and neither is a relative one.
func parse(name string) (config, error) {
	cfg := config{path: name}
	if i := strings.LastIndex(name, "?"); i >= 0 {
		cfg.path = name[:i]
		q, err := url.ParseQuery(name[i+1:])
		if err != nil {
			return config{}, misuse("the options in the connection string do not parse: " + err.Error())
		}
		if err := cfg.read(q); err != nil {
			return config{}, err
		}
	}
	cfg.memory = cfg.path == ":memory:" || cfg.path == ""
	if cfg.memory && cfg.create {
		return config{}, misuse("create=true is meaningless for a database in memory, which is made every time")
	}
	return cfg, nil
}

// read turns the query part into the options the client takes.
func (cfg *config) read(q url.Values) error {
	for key, vals := range q {
		v := vals[len(vals)-1]
		switch key {
		case "create":
			b, err := boolean(key, v)
			if err != nil {
				return err
			}
			cfg.create = b
		case "read_only":
			b, err := boolean(key, v)
			if err != nil {
				return err
			}
			if b {
				cfg.opts = append(cfg.opts, zu.WithReadOnly())
			}
		case "threads":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return misuse("threads in the connection string is " + strconv.Quote(v) + ", which is not a count")
			}
			cfg.opts = append(cfg.opts, zu.WithThreads(n))
		case "memory_limit":
			n, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				return misuse("memory_limit in the connection string is " + strconv.Quote(v) +
					", which is not a number of bytes")
			}
			cfg.opts = append(cfg.opts, zu.WithMemoryLimit(n))
		default:
			return misuse("the connection string sets " + strconv.Quote(key) +
				", which is not one of create, read_only, threads or memory_limit")
		}
	}
	return nil
}

// boolean reads an option that is on or off, in the spellings a
// connection string is written with.
func boolean(key, v string) (bool, error) {
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off", "":
		return false, nil
	default:
		return false, misuse(key + " in the connection string is " + strconv.Quote(v) + ", which is not true or false")
	}
}

// open makes the database this connection string names.
func (cfg config) open() (*zu.DB, error) {
	switch {
	case cfg.memory:
		return zu.Memory(cfg.opts...)
	case cfg.create:
		// Two calls rather than one because the client keeps making and
		// opening apart on purpose: an open that quietly created what it
		// did not find is what writes into the wrong place when a
		// deployment gets a path wrong. A service that means to make its
		// own database on first run says so here instead.
		if _, err := os.Stat(cfg.path); errors.Is(err, os.ErrNotExist) {
			return zu.Create(cfg.path, cfg.opts...)
		}
		return zu.Open(cfg.path, cfg.opts...)
	default:
		return zu.Open(cfg.path, cfg.opts...)
	}
}

// misuse is a failure in what the caller wrote, raised before anything
// was opened. It is a *[zu.Error] like everything else this package
// answers with, so one errors.As covers the whole driver.
func misuse(what string) error {
	return &zu.Error{Status: zu.Misuse, Message: what}
}

// A connector holds the one [zu.DB] every connection from this
// database/sql handle is made on. It is opened on the first connection
// rather than in Open, because database/sql promises that sql.Open does
// not touch the database and a program that relies on that would
// otherwise find its startup failing here.
type connector struct {
	cfg config

	mu   sync.Mutex
	db   *zu.DB
	err  error
	done bool
}

var (
	_ driver.Connector           = (*connector)(nil)
	_ interface{ Close() error } = (*connector)(nil)
)

// database opens the database once for every connection that will be
// made on it. The failure is kept, so a bad path fails the same way on
// the tenth connection as on the first rather than being retried by
// every caller.
func (c *connector) database() (*zu.DB, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done {
		return c.db, c.err
	}
	c.done = true
	c.db, c.err = c.cfg.open()
	return c.db, c.err
}

// Driver is the driver this connector came from.
func (c *connector) Driver() driver.Driver {
	return Driver{}
}

// Close releases the database. database/sql calls it when the
// [database/sql.DB] is closed, and the connections it made are closed
// before it.
func (c *connector) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.db == nil {
		return nil
	}
	err := c.db.Close()
	c.db = nil
	return err
}
