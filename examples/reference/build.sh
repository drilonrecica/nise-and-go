#!/bin/sh
# Build the reference application: generate a project, then apply the overlay.
#
# This is the whole story of examples/reference. `nise new` writes a project;
# examples/reference/app/ holds the files a developer then wrote; this script
# is the "then". It runs the same command the documentation gives a reader,
# which is what makes the reference application evidence that generation works
# rather than evidence that it worked once.
#
# Usage:
#   examples/reference/build.sh <destination>
#
# The destination must not exist. Overwriting a directory somebody named is a
# different and much worse command than creating one.
set -eu

here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo=$(CDPATH= cd -- "$here/../.." && pwd)

if [ "$#" -ne 1 ]; then
	echo "usage: $0 <destination>" >&2
	exit 2
fi
destination=$1

if [ -e "$destination" ]; then
	echo "refusing: $destination already exists" >&2
	echo "Name a path that does not exist; this script creates it." >&2
	exit 1
fi

# The nise doing the generating is this checkout's, not whatever is on PATH.
# A reference application built by a different nise than the one being changed
# proves nothing about the change.
nise="$repo/dist/reference-nise"
mkdir -p "$(dirname "$nise")"
( cd "$repo" && go build -o "$nise" ./cmd/nise )

name=$(sed -n 's/.*"name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$here/recipe.json")
module_path=$(sed -n 's/.*"modulePath"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$here/recipe.json")
modules=$(sed -n 's/.*"modules"[[:space:]]*:[[:space:]]*\[\(.*\)\].*/\1/p' "$here/recipe.json" |
	tr -d '" ' | tr ',' '\n')

set -- new "$name" --module-path "$module_path" --yes
for module in $modules; do
	[ -n "$module" ] || continue
	set -- "$@" --module "$module"
done

parent=$(dirname "$destination")
mkdir -p "$parent"
( cd "$parent" && "$nise" "$@" )
mv "$parent/$name" "$destination"

# The overlay. Every file here is application-owned; a Nise-owned path in it
# would mean the reference application works by overwriting generated output,
# which is the one thing an application never needs to do (ADR 0003).
( cd "$here/app" && find . -type f -print ) | while IFS= read -r file; do
	relative=${file#./}
	mkdir -p "$destination/$(dirname "$relative")"
	cp "$here/app/$relative" "$destination/$relative"
done

echo "Built $name in $destination"
echo
echo "Next:"
echo "  cd $destination"
echo "  go mod edit -replace github.com/drilonrecica/nise-and-go=$repo"
echo "  go mod tidy"
echo "  pnpm --dir frontend install"
echo "  nise dev"
