// A module of its own, and not a package of the client beside it, so
// that arrow-go and everything under it is a dependency of the
// programs that want Arrow and of nobody else. The client module has
// no dependencies at all outside this repository, and that is worth
// keeping.
module github.com/tamnd/zu-go/zuarrow

go 1.26.6

require (
	github.com/apache/arrow-go/v18 v18.7.0
	github.com/tamnd/zu-go v0.0.0
)

require (
	github.com/goccy/go-json v0.10.6 // indirect
	github.com/google/flatbuffers v25.12.19+incompatible // indirect
	github.com/klauspost/compress v1.19.0 // indirect
	github.com/klauspost/cpuid/v2 v2.4.0 // indirect
	github.com/pierrec/lz4/v4 v4.1.27 // indirect
	github.com/tamnd/zu-go/lib/darwin-amd64 v0.0.0 // indirect
	github.com/tamnd/zu-go/lib/darwin-arm64 v0.0.0 // indirect
	github.com/tamnd/zu-go/lib/linux-amd64 v0.0.0 // indirect
	github.com/tamnd/zu-go/lib/linux-arm64 v0.0.0 // indirect
	github.com/tamnd/zu-go/lib/windows-amd64 v0.0.0 // indirect
	github.com/zeebo/xxh3 v1.1.0 // indirect
	golang.org/x/exp v0.0.0-20260112195511-716be5621a96 // indirect
	golang.org/x/sys v0.47.0 // indirect
)
