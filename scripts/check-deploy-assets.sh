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
servers = json.loads((root / "examples/servers.json").read_text())
if len(servers) != 1:
    raise SystemExit("Example server list must contain exactly one pinned public server")
server = servers[0]
expected_server = {
    "id": 51,
    "name": "Amsterdam, Netherlands (Clouvider)",
    "server": "https://ams.speedtest.clouvider.net/backend",
    "dlURL": "garbage.php",
    "ulURL": "empty.php",
    "pingURL": "empty.php",
    "getIpURL": "getIP.php",
}
if server != expected_server:
    raise SystemExit("Pinned public example server does not match LibreSpeed.org server id 51")

import subprocess

rendered = subprocess.check_output(["kubectl", "kustomize", str(root / "deploy/kubernetes")], text=True)
configmap = rendered.split("\n---\n", 1)[0]
config_data = configmap.split("  servers.json: |\n", 1)[1]
json_lines = []
for line in config_data.splitlines():
    if line.startswith("    "):
        json_lines.append(line[4:])
    else:
        break
deployed_servers = json.loads("\n".join(json_lines))
if deployed_servers != servers:
    raise SystemExit("Kubernetes server ConfigMap must match examples/servers.json")

metrics = (root / "internal/metrics/metrics.go").read_text()
descriptors = set(re.findall(r'\{"(librespeed_[a-z0-9_]+)"', metrics))
dashboard = json.loads((root / "grafana/librespeed-dashboard.json").read_text())
panels = {panel.get("id"): panel for panel in dashboard.get("panels", [])}
panels_by_title = {panel.get("title"): panel for panel in dashboard.get("panels", [])}
expected_kpis = {
    16: ("Servers used", (0, 6), [
        'count(sum by (server_id) (increase(librespeed_test_attempts_total{server_id=~"$server_id"}[$__range])) > 0)',
        'count(librespeed_test_success{server_id=~"$server_id"})',
    ]),
    17: ("Tests in range", (6, 6), [
        'round(sum(increase(librespeed_test_attempts_total{server_id=~"$server_id"}[$__range])))',
        'round(sum(increase(librespeed_test_successes_total{server_id=~"$server_id"}[$__range])))',
        'round(sum(increase(librespeed_test_failures_total{server_id=~"$server_id"}[$__range])))',
    ]),
    18: ("Success rate", (12, 4), []),
    19: ("Data uploaded", (16, 4), [
        'round(sum(increase(librespeed_bytes_sent_total{server_id=~"$server_id"}[$__range])))',
    ]),
    20: ("Data downloaded", (20, 4), [
        'round(sum(increase(librespeed_bytes_received_total{server_id=~"$server_id"}[$__range])))',
    ]),
}
for panel_id, (title, position, expressions) in expected_kpis.items():
    panel = panels.get(panel_id)
    if not panel or panel.get("title") != title or panel.get("type") != "stat":
        raise SystemExit(f"Dashboard must contain Stat panel {panel_id}: {title}")
    targets = panel.get("targets", [])
    if not targets or any(target.get("instant") is not True for target in targets):
        raise SystemExit(f"Dashboard KPI {title} must use instant targets")
    if panel_id != 18 and [target.get("expr") for target in targets] != expressions:
        raise SystemExit(f"Dashboard KPI {title} has unexpected PromQL expressions")
    if any('server_id=~"$server_id"' not in target.get("expr", "") for target in targets):
        raise SystemExit(f"Dashboard KPI {title} must filter by selected server")
    if panel.get("gridPos") != {"x": position[0], "y": 0, "w": position[1], "h": 5}:
        raise SystemExit(f"Dashboard KPI {title} must occupy the top row")
    if panel.get("fieldConfig", {}).get("defaults", {}).get("decimals") != 0 and panel_id != 18:
        raise SystemExit(f"Dashboard KPI {title} must round displayed counts and bytes")
expected_legends = {16: ["Tested", "Configured"], 17: ["Attempts", "Succeeded", "Failed"]}
for panel_id, legends in expected_legends.items():
    actual = [target.get("legendFormat") for target in panels[panel_id].get("targets", [])]
    if actual != legends:
        raise SystemExit(f"Dashboard KPI {panels[panel_id]['title']} has unexpected target names")

success_rate = panels[18]["targets"][0].get("expr", "") if 18 in panels else ""
normalized_rate = re.sub(r"\s+", " ", success_rate).strip()
expected_rate = '(100*sum(increase(librespeed_test_successes_total{server_id=~"$server_id"}[$__range]))/sum(increase(librespeed_test_attempts_total{server_id=~"$server_id"}[$__range])))and(sum(increase(librespeed_test_attempts_total{server_id=~"$server_id"}[$__range]))>0)'
if re.sub(r"\s+", "", normalized_rate) != expected_rate:
    raise SystemExit("Dashboard success rate must omit zero-attempt windows")
if len(panels.get(18, {}).get("targets", [])) != 1:
    raise SystemExit("Dashboard success rate must have one target")
if panels[18].get("fieldConfig", {}).get("defaults", {}).get("unit") != "percent":
    raise SystemExit("Dashboard success rate must use Grafana percent units")
for title in ("Data uploaded", "Data downloaded"):
    panel = panels_by_title.get(title, {})
    description = panel.get("description", "").lower()
    if "successful" not in description or "pre-deployment" not in description or "unavailable" not in description:
        raise SystemExit(f"Dashboard KPI {title} must describe successful-only bytes and unavailable pre-deployment history")
    defaults = panel.get("fieldConfig", {}).get("defaults", {})
    if defaults.get("unit") != "bytes" or defaults.get("decimals") != 0:
        raise SystemExit(f"Dashboard KPI {title} must display rounded bytes")

for panel_id in range(1, 16):
    panel = panels.get(panel_id)
    if not panel or panel.get("gridPos", {}).get("y") != {
        1: 5, 2: 5, 3: 5, 4: 5, 5: 13, 6: 13, 7: 17, 8: 17,
        9: 22, 10: 22, 11: 30, 12: 30, 13: 30, 14: 37, 15: 37,
    }[panel_id]:
        raise SystemExit(f"Dashboard panel {panel_id} must shift down five grid rows")
for panel_id in (1, 2, 5):
    panel = panels[panel_id]
    defaults = panel.get("fieldConfig", {}).get("defaults", {})
    if defaults.get("color", {}).get("mode") != "continuous-BlPu":
        raise SystemExit(f"Dashboard panel {panel_id} must use continuous-BlPu")
for panel_id in (1, 2):
    if not panels[panel_id].get("options", {}).get("effects", {}).get("gradient"):
        raise SystemExit(f"Dashboard gauge {panel_id} must enable gradient rendering")
if panels[5].get("fieldConfig", {}).get("defaults", {}).get("custom", {}).get("gradientMode") != "scheme":
    raise SystemExit("Throughput history must use scheme gradient mode")
for panel_id in (3, 4):
    if panels[panel_id].get("fieldConfig", {}).get("defaults", {}).get("color", {}).get("mode") != "thresholds":
        raise SystemExit(f"Dashboard panel {panel_id} must preserve threshold colors")
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
