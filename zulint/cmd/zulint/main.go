// Command zulint runs the static checks for the Go client over the
// packages you name.
//
//	go install github.com/tamnd/zu-go/zulint/cmd/zulint@latest
//	zulint ./...
//
// It takes the flags every analysis driver takes, so a single check is
// -viewafterclose and the machine-readable form is -json. What it
// reports is in the package documentation for
// [github.com/tamnd/zu-go/zulint].
package main

import (
	"golang.org/x/tools/go/analysis/multichecker"

	"github.com/tamnd/zu-go/zulint"
)

func main() {
	multichecker.Main(zulint.Analyzers()...)
}
