// The three ways this client finds libzu, one file each, chosen by
// build tag. Every one of them has to hand cgo two things: where zu.h
// is, and what to link. The rest of the package writes `#include
// <zu.h>` and does not care which of the three answered.

//go:build !zu_system && !zu_static

package zu

/*
// The header that ships with this module, which is the one this
// binding was written against and the one the ABI test holds it to. A
// header found on the include path instead would be whatever a machine
// happened to have installed.
#cgo CFLAGS: -I${SRCDIR}/include
*/
import "C"
