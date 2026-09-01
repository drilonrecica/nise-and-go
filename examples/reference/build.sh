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

# recipe.json is read with sed rather than jq, because jq is not in
# docs/toolchain.md and this script must run on a clean machine. The file is
# flattened first: the modules array spans several lines when the file is
# pretty-printed, and a line-oriented match silently produced *no* modules —
# which is why the verification below exists rather than trusting this parse.
flat=$(tr -d ' \t\n' < "$here/recipe.json")
name=$(printf '%s' "$flat" | sed -n 's/.*"name":"\([^"]*\)".*/\1/p')
module_path=$(printf '%s' "$flat" | sed -n 's/.*"modulePath":"\([^"]*\)".*/\1/p')
modules=$(printf '%s' "$flat" | sed -n 's/.*"modules":\[\([^]]*\)\].*/\1/p' | tr -d '"' | tr ',' '\n')

if [ -z "$name" ] || [ -z "$module_path" ]; then
	echo "refusing: could not read the project name or module path from recipe.json" >&2
	exit 1
fi

set -- new "$name" --module-path "$module_path" --yes
for module in $modules; do
	[ -n "$module" ] || continue
	set -- "$@" --module "$module"
done

parent=$(dirname "$destination")
mkdir -p "$parent"
( cd "$parent" && "$nise" "$@" )
mv "$parent/$name" "$destination"

# Verify rather than trust. A module flag that failed to parse produces an
# application silently missing whole subsystems — no organizations, no uploads,
# no notifications — and every later check would pass on it, because there is
# nothing wrong with the application that was built. It is simply not the one
# that was asked for. This has happened once already.
for module in $modules; do
	[ -n "$module" ] || continue
	if ! grep -q "\"$module\"" "$destination/nise.json"; then
		echo "refusing: the generated project does not record the \"$module\" module" >&2
		echo "recipe.json asked for it; $destination/nise.json does not have it." >&2
		exit 1
	fi
done

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
echo "  make generate                  # the overlay changes api/openapi.yaml"
echo "  pnpm --dir frontend install"
echo "  nise dev"
echo
echo "The overlay carries api/openapi.yaml and not the code generated from it."
echo "internal/platform/httpapi/openapigen/openapi.gen.go is Nise-owned, so"
echo "copying one into the overlay would mean the reference application works"
echo "by overwriting generated output — the one thing ADR 0003 says an"
echo "application never needs to do. It is regenerated instead, from the"
echo "contract the overlay does carry."
