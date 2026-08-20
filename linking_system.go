//go:build zu_system

package zu

/*
// -tags zu_system links against a libzu that is already installed,
// found through pkg-config, which is what a distribution package and a
// `make install` both leave behind. The header comes from there too, so
// this is the one mode where the header and the library are certainly
// the same build. It is also the mode a bisect wants, since pointing
// PKG_CONFIG_PATH at a freshly built engine takes no rebuild here.
#cgo pkg-config: libzu
*/
import "C"
