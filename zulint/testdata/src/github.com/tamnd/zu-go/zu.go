// Package zu is the part of the client's surface these checks read,
// declared here so that the tests need neither cgo nor a library. What
// matters to an analyzer is the import path and the method names, and
// both are the real ones.
package zu

import (
	"context"
	"iter"
	"unsafe"
)

type DB struct{}

func Open(path string) (*DB, error) { return nil, nil }

func (db *DB) Connect(ctx context.Context) (*Conn, error) { return nil, nil }

func (db *DB) Close() error { return nil }

type Conn struct{}

func (c *Conn) Query(ctx context.Context, q string, args ...any) (*Rows, error) { return nil, nil }

func (c *Conn) Close() error { return nil }

func (c *Conn) Interrupt() error { return nil }

func (c *Conn) RowsRead() (uint64, error) { return 0, nil }

type Row struct{}

func (Row) Scan(dest ...any) error { return nil }

type Rows struct{}

func (r *Rows) Next() bool { return false }

func (r *Rows) Row() Row { return Row{} }

func (r *Rows) All() iter.Seq[Row] { return nil }

func (r *Rows) Err() error { return nil }

func (r *Rows) Close() error { return nil }

func (r *Rows) Len() int64 { return 0 }

func (r *Rows) Int64s(col int) ([]int64, error) { return nil, nil }

func (r *Rows) Float64s(col int) ([]float64, error) { return nil, nil }

func (r *Rows) NodeOffsets(col int) ([]uint64, error) { return nil, nil }

func (r *Rows) Valid(col int) ([]byte, error) { return nil, nil }

func (r *Rows) ArrowStream(out unsafe.Pointer, rowsPerBatch int) error { return nil }
