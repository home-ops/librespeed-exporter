#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM

if ! output=$(kustomize build "$root/deploy/kubernetes" 2>&1); then
	printf '%s\n' "$output" >&2
	exit 1
fi

printf '%s\n' "$output" >"$tmp/rendered.yaml"

grep -Fq 'kind: ConfigMap' "$tmp/rendered.yaml"
grep -Fq 'kind: Deployment' "$tmp/rendered.yaml"
grep -Fq 'kind: Service' "$tmp/rendered.yaml"
grep -Fq 'kind: NetworkPolicy' "$tmp/rendered.yaml"
grep -Fq 'name: librespeed-exporter-servers' "$tmp/rendered.yaml"
grep -Fq 'servers.json: |' "$tmp/rendered.yaml"
grep -Fq 'namespace: default' "$tmp/rendered.yaml"
grep -Fq 'name: LIBRESPEED_SERVERS_FILE' "$tmp/rendered.yaml"
grep -Fq 'name: TEST_INTERVAL' "$tmp/rendered.yaml"
grep -Fq 'name: TEST_TIMEOUT' "$tmp/rendered.yaml"
if grep -Fq 'name: WEB_CONFIG_FILE' "$tmp/rendered.yaml"; then
	printf '%s\n' 'Default deployment must not require WEB_CONFIG_FILE' >&2
	exit 1
fi
if grep -Fq 'name: web-config' "$tmp/rendered.yaml"; then
	printf '%s\n' 'Default deployment must not require web-config Secret' >&2
	exit 1
fi
printf '%s\n' 'Kubernetes bundle contains expected resources and optional web config'
