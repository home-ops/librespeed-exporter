#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"
export GOOS=linux GOARCH=amd64 CGO_ENABLED=0

tool=$(command -v go-licenses || true)
if [ -z "$tool" ]; then
	printf '%s\n' 'go-licenses v2.0.1 is required; install with go install github.com/google/go-licenses/v2@v2.0.1' >&2
	exit 1
fi
version=$(go version -m "$tool" | awk '$1 == "mod" && $2 == "github.com/google/go-licenses/v2" {print $3; exit}')
if [ "$version" != "v2.0.1" ]; then
	printf 'go-licenses version %s found; require v2.0.1\n' "${version:-unknown}" >&2
	exit 1
fi

if [ "${1:-}" = "--check" ]; then
	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT HUP INT TERM
	go-licenses save ./cmd/librespeed-exporter \
		--ignore github.com/home-ops/librespeed-exporter \
		--save_path="$tmp/GO-MODULES"
	diff -ru LICENSES/GO-MODULES "$tmp/GO-MODULES"
	printf '%s\n' 'Go module license notices are current'
	exit 0
fi

if [ "${1:-}" != "" ]; then
	printf 'usage: %s [--check]\n' "$0" >&2
	exit 2
fi

go-licenses save ./cmd/librespeed-exporter \
	--ignore github.com/home-ops/librespeed-exporter \
	--save_path=LICENSES/GO-MODULES --force
