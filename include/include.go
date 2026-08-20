// Package include is here so that this directory is a package and not
// just files beside one. Go copies directories that hold a package and
// skips the rest, so without this `go mod vendor` and the module zip
// would leave zu.h behind and every build would fail on the include.
//
// It has nothing in it and is imported by nobody. The header beside it
// is the whole point.
package include
