# Nise & Go — local check entry point.
#
# This file is the single source of truth for the checks CI runs. Every
# target here is safe to run on Linux and macOS with a POSIX /bin/sh: no
# bash-only builtins, no bashisms, no tool that docs/toolchain.md does not
# already pin. `make check` runs exactly what `.github/workflows/ci.yml`
# runs; see docs/checks.md for what each check protects and why.
#
# A check is never weakened to make a change pass — fix the change instead.
SHELL := /bin/sh
.SHELLFLAGS := -ec
.DEFAULT_GOAL := help

# --- Go package scope ---------------------------------------------------
#
# frontend/node_modules (checked-in state described in
# docs/generated-application-layout.md, and eventually present under
# templates/) is part of this module's directory tree but is not Go source
# Nise owns. `./...` and `gofmt -s -l .` both walk into it; an npm package
# that happens to ship a stray .go file would then show up as a package or
# formatting violation of this repository. Every Go target below therefore
# names Nise's own package roots explicitly instead of using `./...` or `.`.
#
# GO_PACKAGES currently covers everything with Go source today: cmd/,
# internal/, and runtime/. It already covers internal/cli, internal/
# generator, cmd/nise, and every runtime/* package landing later in this
# slice, because `...` matches new packages created under an existing root
# with no Makefile change required. It does NOT yet cover modules/,
# templates/, examples/, or test/: modules/ has no Go source yet (add
# `./modules/...` here the day it does — an empty `...` root mixed with
# non-empty roots is silently skipped by `go build`/`go vet`, so adding it
# early is harmless, but there is nothing to gain by doing so before it
# holds a package), and templates/ and examples/ are exactly the
# kind of tree that risks holding a vendored frontend in this slice, so
# they are named explicitly only once a future task confirms what belongs
# in each.
#
# test/ was excluded for that same reason until M1-009 and M1-010 confirmed
# what it holds: Go conformance suites only (test/golden, test/nonetwork),
# with fixture data under testdata/ where the Go toolchain already ignores
# it. Those suites were committed but gated by nothing, which is the worst
# of both worlds — so test/ is named here now.
GO_PACKAGES := ./cmd/... ./internal/... ./runtime/... ./test/...
GO_FMT_DIRS := cmd internal runtime test

GOLANGCI_LINT_VERSION := 2.11.3

# A literal '#' inside a recipe line starts a Makefile comment even inside
# shell quotes, unless escaped. HASH lets docs-check build shell patterns
# and parameter expansions that contain '#' without that ever appearing as
# a raw character in this file. See docs/checks.md for the full write-up.
HASH := $(shell printf '\043')

.PHONY: fmt fmt-check vet lint test test-race generate generate-diff docs-check check help

fmt: ## Format Go source in place (gofmt -s -w) over cmd, internal, runtime.
	gofmt -s -w $(GO_FMT_DIRS)

fmt-check: ## Fail if any file under cmd, internal, runtime is not gofmt -s clean.
	@unformatted="$$(gofmt -s -l $(GO_FMT_DIRS))"; \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt -s found unformatted files:" >&2; \
		echo "$$unformatted" >&2; \
		echo "run 'make fmt' to fix" >&2; \
		exit 1; \
	fi

vet: ## Run go vet over ./cmd/... ./internal/... ./runtime/....
	go vet $(GO_PACKAGES)

lint: ## Run golangci-lint (pinned version; see docs/toolchain.md).
	@version_output="$$(golangci-lint version 2>&1)"; \
	case "$$version_output" in \
		*"version $(GOLANGCI_LINT_VERSION) "*) : ;; \
		*) \
			echo "golangci-lint version mismatch: this repository is pinned to $(GOLANGCI_LINT_VERSION) (docs/toolchain.md)" >&2; \
			echo "installed: $$version_output" >&2; \
			exit 1 ;; \
	esac
	golangci-lint run $(GO_PACKAGES)

test: ## Run go test over ./cmd/... ./internal/... ./runtime/....
	go test $(GO_PACKAGES)

test-race: ## Run go test -race over ./cmd/... ./internal/... ./runtime/....
	go test -race $(GO_PACKAGES)

generate: ## Run every //go:generate directive under ./cmd/... ./internal/... ./runtime/....
	go generate $(GO_PACKAGES)

generate-diff: generate ## Regenerate, then fail if the working tree changed (determinism gate).
	@status="$$(git status --porcelain)"; \
	if [ -n "$$status" ]; then \
		echo "generated output does not match what is committed:" >&2; \
		echo "$$status" >&2; \
		echo "run 'make generate' and commit the result" >&2; \
		exit 1; \
	fi

docs-check: ## Fail if any relative Markdown link in docs/** or a root *.md file is broken.
	@status=0; \
	files="$$(git ls-files -- 'docs' ':(glob)*.md')"; \
	for f in $$files; do \
		dir="$$(dirname "$$f")"; \
		targets="$$(sed -E 's/`[^`]*`//g' "$$f" | grep -oE '\]\([^)]+\)' | sed -E 's/^\]\(//; s/\)$$//')" || true; \
		for t in $$targets; do \
			case "$$t" in \
				http://*|https://*|mailto:*|//*) continue ;; \
				"$(HASH)"*) continue ;; \
			esac; \
			path=$${t%%$(HASH)*}; \
			if [ -z "$$path" ]; then continue; fi; \
			resolved="$$dir/$$path"; \
			if [ ! -e "$$resolved" ]; then \
				echo "broken relative link in $$f: $$t" >&2; \
				status=1; \
			fi; \
		done; \
	done; \
	exit $$status

check: fmt-check vet lint test test-race generate-diff docs-check ## Run everything CI runs.

help: ## Show this help.
	@grep -E '^[a-zA-Z_-]+:.*## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-16s %s\n", $$1, $$2}'
