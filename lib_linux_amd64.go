//go:build cgo && !zu_system && !zu_static && linux && amd64

package zu

// The library for this platform, imported for the linker flags in it
// and for nothing else.
import _ "github.com/tamnd/zu-go/lib/linux-amd64"
