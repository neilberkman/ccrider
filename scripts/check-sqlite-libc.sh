#!/bin/sh
# POSIX sh on purpose: bash 5.3 (Homebrew's default on macOS, and present on
# GitHub macOS runners) deadlocks on heredocs larger than ~512 bytes.
#
# modernc.org/sqlite is transpiled against one exact modernc.org/libc release,
# and downstream modules must use that same release
# (https://gitlab.com/cznic/sqlite/-/issues/177). `go get -u` raises libc past
# it, because minimum version selection treats sqlite's requirement as a floor.
# This fails when go.mod's libc differs from the one sqlite's go.mod requires.
set -eu

sqlite=$(go list -m -f '{{.Version}}' modernc.org/sqlite)
have=$(go list -m -f '{{.Version}}' modernc.org/libc)
want=$(go mod graph | awk -v from="modernc.org/sqlite@$sqlite" '
	$1 == from && index($2, "modernc.org/libc@") == 1 { sub(/^modernc\.org\/libc@/, "", $2); print $2 }
')

if [ -z "$want" ]; then
	echo "modernc.org/sqlite $sqlite does not require modernc.org/libc; update this check" >&2
	exit 1
fi

if [ "$have" != "$want" ]; then
	echo "modernc.org/libc is $have, but modernc.org/sqlite $sqlite requires exactly $want." >&2
	echo "Run: go get modernc.org/libc@$want && go mod tidy" >&2
	exit 1
fi

echo "modernc.org/libc $have matches modernc.org/sqlite $sqlite"
