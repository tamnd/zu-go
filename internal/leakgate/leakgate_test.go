//go:build cgo

package leakgate

import (
	"os"
	"testing"
)

// TestTheGateFiresOnALeak leaks, and is meant to. It is skipped unless
// ZU_LEAK_GATE is set, so the suite this lives in stays clean and the
// sanitizer job is the only thing that ever asks for it.
//
// What the job asserts is that this run fails. It fails at exit rather
// than here, because a leak is not a thing that has happened until
// nobody is going to free it, and the sanitizer decides that when the
// process ends.
func TestTheGateFiresOnALeak(t *testing.T) {
	if os.Getenv("ZU_LEAK_GATE") == "" {
		t.Skip("set ZU_LEAK_GATE to leak a megabyte on purpose")
	}
	Leak()
}
