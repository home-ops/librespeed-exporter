#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

if [ "${1:-}" = "--self-test" ]; then
	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT HUP INT TERM
	printf '{ invalid json\n' >"$tmp/invalid.json"
	printf 'key: [unterminated\n' >"$tmp/invalid.yaml"
	if python3 -m json.tool "$tmp/invalid.json" >/dev/null 2>&1; then
		printf '%s\n' 'JSON parser accepted malformed input' >&2
		exit 1
	fi
	if ruby -e 'require "yaml"; YAML.parse_file(ARGV.fetch(0))' "$tmp/invalid.yaml" >/dev/null 2>&1; then
		printf '%s\n' 'YAML parser accepted malformed input' >&2
		exit 1
	fi
	printf '%s\n' 'Malformed JSON and YAML rejected'
	exit 0
fi

json_files="$root/examples/servers.json $root/grafana/librespeed-dashboard.json"
yaml_files="$root/deploy/docker-compose.yaml $root/deploy/kubernetes/deployment.yaml $root/deploy/kubernetes/kustomization.yaml $root/deploy/kubernetes/networkpolicy.yaml $root/deploy/kubernetes/servers-configmap.yaml $root/deploy/kubernetes/service.yaml $root/deploy/prometheus/prometheus.yaml $root/examples/web-config.yaml"

for file in $json_files; do
	python3 -m json.tool "$file" >/dev/null
done

ruby -e 'require "yaml"; ARGV.each { |file| YAML.parse_file(file) }' $yaml_files

sh "$root/scripts/check-kubernetes-deploy-test.sh"

python3 - "$root" <<'PY'
import json
import re
import sys
from pathlib import Path

root = Path(sys.argv[1])
metrics = (root / "internal/metrics/metrics.go").read_text()
descriptors = set(re.findall(r'\{"(librespeed_[a-z0-9_]+)"', metrics))
dashboard = json.loads((root / "grafana/librespeed-dashboard.json").read_text())
queries = [
    target.get("expr", "")
    for panel in dashboard.get("panels", [])
    for target in panel.get("targets", [])
]
referenced = {name for query in queries for name in re.findall(r"\b(librespeed_[a-z0-9_]+)\b", query)}
unknown = sorted(referenced - descriptors)
if unknown:
    raise SystemExit("Dashboard references unknown metrics: " + ", ".join(unknown))
if not referenced:
    raise SystemExit("Dashboard contains no exporter metric queries")
print(f"Dashboard metric names match descriptors ({len(referenced)} referenced)")
PY
