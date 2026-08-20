//go:build cgo && !zu_system && !zu_static && !(darwin && arm64) && !(darwin && amd64) && !(linux && amd64) && !(linux && arm64) && !(windows && amd64)

package zu

// No static library ships for this platform, and a link that ended in
// "undefined symbol: zu_open" would not say that. This does, by naming
// an identifier that does not exist, so that the message the compiler
// prints is the message a reader needs.
//
// Three ways forward, in the order they are likely to be right:
//
//   - build the engine for your platform and link against it with
//     -tags zu_static and CGO_LDFLAGS naming the archive
//   - install libzu and build with -tags zu_system, which finds it
//     through pkg-config
//   - open an issue asking for the platform, if it is one a release
//     could reasonably carry
//
// Every one of them is in the README under Linking.
var _ = zu_ships_no_static_library_for_this_GOOS_GOARCH__see_README_Linking
