#!/bin/sh
#
# The install, run inside the container the Install workflow starts.
#
# It is a program rather than lines in a YAML file because it is run
# twice, once for the install and once to prove the install would have
# failed if the library for this platform were missing, and because a
# thing that decides whether a client is installable should be readable
# without opening a workflow.
#
# What it does is what a reader does. Take the first whole program off
# the README, put it in an empty module somewhere else on the machine,
# add one requirement, build it and run it. Nothing is copied out of the
# checkout except that program and nothing is resolved out of it either:
# the module it builds is its own, and the client arrives through the
# proxy like any other dependency.
#
# It is driven by four variables, all of them set by the workflow:
#
#   SRC     the checkout, read only, for the README and nothing else
#   PROXY   a module proxy holding this commit, since there is no
#           release to install yet. scripts/mkproxy writes it.
#   APP     an empty directory, outside the checkout, to install into
#   ABSENT  the tools this image is claimed not to have

set -eu

: "${SRC:=/src}"
: "${PROXY:=/proxy}"
: "${APP:=/app}"
: "${ABSENT:=rustc cargo}"

export PATH="/usr/local/go/bin:$PATH"

# What the image is claimed to be, checked rather than believed. The day
# a base image starts shipping a Rust toolchain is the day this job
# quietly stops being about anything, because the thing it exists to
# prove is that none of it is needed.
for tool in $ABSENT; do
    if command -v "$tool" >/dev/null 2>&1; then
        echo "this image has $tool on it, so it is not the machine this job is about" >&2
        exit 1
    fi
done

# And no libzu already on it, which is the failure that would be worst
# of all: the vendored archive would look like it worked while the
# linker took something a previous job left behind.
if command -v pkg-config >/dev/null 2>&1 && pkg-config --exists zu 2>/dev/null; then
    echo "this image has a libzu registered with pkg-config" >&2
    exit 1
fi
for path in /usr/include/zu.h /usr/local/include/zu.h \
    /usr/lib/libzu.a /usr/local/lib/libzu.a \
    /usr/lib/libzu.so /usr/local/lib/libzu.so; do
    if [ -e "$path" ]; then
        echo "this image has $path on it, so the vendored library is not what would be linked" >&2
        exit 1
    fi
done

go version

# The quickstart, taken off the page rather than written again here, so
# that the program this job installs is the program a reader copies. A
# whole program is a block that opens with the package clause, which is
# the rule the README follows and the same one its own test applies.
mkdir -p "$APP/run"
awk '
    $0 == "```go"           { inblock = 1; n = 0; first = ""; next }
    $0 == "```" && inblock  {
        if (first == "package main") { for (i = 1; i <= n; i++) print line[i]; exit }
        inblock = 0
        next
    }
    inblock                 { n++; line[n] = $0; if (n == 1) first = $0 }
' "$SRC/README.md" > "$APP/run/main.go"
if [ ! -s "$APP/run/main.go" ]; then
    echo "the README has no whole program on it, which is a page to fix rather than an install to report" >&2
    exit 1
fi

cd "$APP/run"

# Offline, against the proxy and nothing else, so that a dependency this
# client does not declare cannot arrive from anywhere. The checksum
# database is off for the same reason it has to be: there is no release
# for it to have a line about yet.
#
# GOTOOLCHAIN=local is the one that makes the floor in go.mod mean
# something. Left alone, a Go older than the floor downloads a newer one
# and the job says nothing about a user whose machine would have done
# the same over a network they may not have.
export GOFLAGS=-mod=mod
export GOPROXY="file://$PROXY"
export GOSUMDB=off
export GOTOOLCHAIN=local
export GOMODCACHE="$APP/cache"
export GOCACHE="$APP/build"

go mod init quickstart >/dev/null
go mod edit -require=github.com/tamnd/zu-go@v0.0.0
go build -o "$APP/quickstart" .

# In a directory of its own because the program writes a database
# beside itself, and Create refuses a path that is already there.
work="$APP/work"
rm -rf "$work"
mkdir -p "$work"
got=$(cd "$work" && "$APP/quickstart")

want='ada 1
grace 2
lynn 3'
if [ "$got" != "$want" ]; then
    echo "the quickstart printed:" >&2
    echo "$got" >&2
    echo "and the page says it prints:" >&2
    echo "$want" >&2
    exit 1
fi

# What the install cost. Five platforms are five modules so that a build
# downloads the archive it links and not the other four, and this is the
# line that says whether that is still true. One is 6 MB over the wire
# and five are 27, which is the whole argument for the shape, and the
# README quotes both numbers.
found=$(find "$APP/cache" -name libzu.a | sort)
count=$(printf '%s\n' "$found" | grep -c . || true)
if [ "$count" -ne 1 ]; then
    echo "a build downloaded $count static libraries rather than the one it links:" >&2
    printf '%s\n' "$found" >&2
    exit 1
fi

wire=$(du -sh "$APP/cache/cache/download" 2>/dev/null | cut -f1)
echo "installed and ran, having downloaded $wire and linked $found"
