//go:build cgo && !zu_system && !zu_static && darwin && arm64

package zu

// The library for this platform, imported for the linker flags in it
// and for nothing else.
import _ "github.com/tamnd/zu-go/lib/darwin-arm64"
