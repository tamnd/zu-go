#!/usr/bin/env bash
#
# Release this repository, which is one client and five library
# modules that live inside it.
#
#     scripts/release.sh v0.11.0
#
# Go resolves a nested module by a tag whose prefix is its directory,
# so lib/linux-amd64 is released as lib/linux-amd64/v0.11.0 and the
# client is released as v0.11.0. The order matters and cannot be
# batched: the client's go.mod has to name versions of the libraries
# that already exist, so every library is tagged first, then the
# requirements are rewritten, then that rewrite is reviewed and
# merged, and only then is the client tagged.
#
# That middle step is why this script is safe to run twice. Run it, it
# tags the libraries and rewrites go.mod and stops. Open the pull
# request, get it merged, pull, run it again with the same version and
# it goes the rest of the way.

set -euo pipefail

version="${1:-}"
if [ -z "$version" ]; then
    echo "usage: scripts/release.sh vX.Y.Z" >&2
    exit 2
fi
case "$version" in
    v[0-9]*.[0-9]*.[0-9]*) ;;
    *) echo "a version is vX.Y.Z, not $version" >&2; exit 2 ;;
esac

cd "$(dirname "$0")/.."

if [ -n "$(git status --porcelain)" ]; then
    echo "the tree is dirty, and a tag on a dirty tree is a tag on nothing anybody has" >&2
    git status --short >&2
    exit 1
fi

branch="$(git rev-parse --abbrev-ref HEAD)"
if [ "$branch" != "main" ]; then
    echo "on $branch, and releases come off main" >&2
    exit 1
fi

git fetch --tags origin
if [ "$(git rev-parse HEAD)" != "$(git rev-parse origin/main)" ]; then
    echo "main here is not main on the remote" >&2
    exit 1
fi

libs=""
for d in lib/*/; do
    libs="$libs $(basename "$d")"
done

# Every library that has no archive is a library that would be
# published empty, and an empty module resolves and then fails at the
# link, which is the worst of the two failures.
for lib in $libs; do
    if [ ! -f "lib/$lib/libzu.a" ]; then
        echo "lib/$lib has no libzu.a: run the Libraries workflow first" >&2
        exit 1
    fi
done

tag() {
    if git rev-parse -q --verify "refs/tags/$1" >/dev/null; then
        echo "$1 is already a tag"
        return
    fi
    echo "tagging $1"
    git tag -a "$1" -m "$1"
    git push origin "$1"
}

for lib in $libs; do
    tag "lib/$lib/$version"
done

# The proxy has to have seen them before the client's go.mod can name
# them, and it sees them the first time somebody asks.
for lib in $libs; do
    echo "warming github.com/tamnd/zu-go/lib/$lib@$version"
    GOFLAGS=-mod=mod GOWORK=off go list -m "github.com/tamnd/zu-go/lib/$lib@$version" >/dev/null
done

for lib in $libs; do
    go mod edit -require "github.com/tamnd/zu-go/lib/$lib@$version"
done

if [ -n "$(git status --porcelain go.mod go.sum)" ]; then
    echo
    echo "go.mod now names $version. Commit it, open a pull request, get it"
    echo "merged, pull, and run this again with the same version. The tags"
    echo "above are already pushed, so the second run will skip them."
    git --no-pager diff -- go.mod
    exit 0
fi

echo "go.mod already names $version"
tag "$version"

echo
echo "released $version"
