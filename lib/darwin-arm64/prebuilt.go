//go:build darwin && arm64

// Package lib is the static libzu for darwin/arm64 and nothing else.
// It is a module of its own so that the client's own history stays
// readable: a library is a new file every release, and five of them in
// the package you are reading would bury it.
//
// Nobody imports this for a symbol. The client imports it for the cgo
// line below, which is how a linker flag written here reaches a link
// started somewhere else.
//
// The archive is built for aarch64-apple-darwin from tamnd/zu at the
// revision named in REVISION, by the lib workflow rather than by hand,
// because a library nobody can reproduce is a library nobody can
// trust. NATIVE_STATIC_LIBS beside it is what rustc said that build
// needs at link time, recorded so that a change in it shows up as a
// diff rather than as somebody's failing link.
package lib

/*
// What rustc names for this target is -lSystem -lc -lm, and cgo
// already puts all three on a darwin link, so naming them again here
// would only make the linker warn about duplicate libraries.
#cgo LDFLAGS: ${SRCDIR}/libzu.a
*/
import "C"
