// Command corpus runs the shared cross-client corpus against this
// client and prints what happened.
//
//	go run ./cmd/corpus ../../zu/conformance/cases
//
// The report is the reference runner's, line for line, so a
// disagreement between two clients is a diff and not a reading
// exercise. It exits zero when nothing failed and one when something
// did or when the corpus will not read, which is also the reference
// runner's rule.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/tamnd/zu-go/corpus"
)

func main() {
	os.Exit(run())
}

func run() int {
	strict := flag.Bool("strict", false,
		"an unsupported case fails the run, which is what a release branch wants")
	quiet := flag.Bool("quiet", false, "print the summary and nothing else")
	work := flag.String("work", "",
		"a directory to make the case databases under, kept rather than removed")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: corpus [flags] <dir>")
		fmt.Fprintln(os.Stderr, "run the shared corpus cases against this client")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		return 2
	}

	suites, err := corpus.ReadDir(flag.Arg(0))
	if err != nil {
		// One rather than two, because the reference runner exits one
		// for a corpus it cannot read and a report that is compared line
		// for line is worth less if the two disagree about what the run
		// came to.
		fmt.Fprintln(os.Stderr, "zu corpus:", err)
		return 1
	}

	directory := *work
	if directory == "" {
		// Removed when the run ends, and each case removes its own as it
		// finishes, so what is left in here at the end is the databases
		// of the cases that failed. A run with -work keeps them.
		made, err := os.MkdirTemp("", "zu-corpus-")
		if err != nil {
			fmt.Fprintln(os.Stderr, "zu corpus:", err)
			return 1
		}
		defer os.RemoveAll(made)
		directory = made
	} else if err := os.MkdirAll(directory, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "zu corpus:", err)
		return 1
	}

	report := corpus.Run(context.Background(), suites, directory)
	if !*quiet {
		for _, ran := range report.Ran {
			if ran.Outcome != corpus.Passed {
				fmt.Println(ran)
			}
		}
	}
	fmt.Println(report.Summary())
	if report.Count(corpus.Failed) > 0 {
		return 1
	}
	if *strict && report.Count(corpus.Unsupported) > 0 {
		return 1
	}
	return 0
}
