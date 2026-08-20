//go:build zu_static

package zu

/*
// -tags zu_static links against a static archive you name yourself in
// CGO_LDFLAGS, which is what a build that cross-compiles to a target
// this module does not vendor needs, and what an engine built with
// features of your own needs.
//
// The header is this module's, because the flags say nothing about
// where a header is. If your archive is a different revision of the C
// ABI, the test in zu_test.go is what tells you.
#cgo CFLAGS: -I${SRCDIR}/include
*/
import "C"
