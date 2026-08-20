# libzu for linux/arm64

The static library the Go client links against on this platform, built from [tamnd/zu](https://github.com/tamnd/zu) for `aarch64-unknown-linux-gnu`.

- `libzu.a` is the archive.
- `REVISION` is the commit of the engine it was built from.
- `NATIVE_STATIC_LIBS` is what rustc said that build needs beside it at link time. What `prebuilt.go` actually passes is that list minus whatever cgo already puts on the link, which is why the two are not always the same.

Import it for its cgo directives or not at all. There is nothing in the package.

```
go get github.com/tamnd/zu-go
```

pulls this in on its own. Nothing here is meant to be depended on directly.
