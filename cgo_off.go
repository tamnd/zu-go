//go:build !cgo

package zu

// This client is cgo over the engine's C ABI, so CGO_ENABLED=0 cannot
// build it at all. Without this file the failure would be a hundred
// lines of "undefined: LocalTime", because with cgo off every file
// that declares anything touching C is excluded and only the handful
// that do not are left. That reads like a broken package rather than a
// build that turned cgo off, so it says which it is instead, by naming
// an identifier that does not exist.
//
// Cross-compiling is the usual way to arrive here, because cgo
// defaults to off the moment GOOS or GOARCH is not the host's. See the
// README under Linking for the cross-compilation recipes, which come
// down to setting CGO_ENABLED=1 and pointing CC at a compiler that
// targets the platform you asked for.
var _ = zu_is_cgo_and_needs_CGO_ENABLED_1__see_README_Linking
