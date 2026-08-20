// Package zuarrow is the part of the Arrow reader these checks read,
// declared here for the same reason the client beside it is: what
// matters to an analyzer is the import path and the names, and both
// are the real ones. The real module carries arrow-go, and a test of a
// linter has no business building it.
package zuarrow

import zu "github.com/tamnd/zu-go"

func Reader(rows *zu.Rows) (any, error) { return nil, nil }

func ReaderBatched(rows *zu.Rows, rowsPerBatch int) (any, error) { return nil, nil }
