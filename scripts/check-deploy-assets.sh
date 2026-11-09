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
selector = '{server_id=~"$server_id"}'
inventory = f'librespeed_test_success{selector}'
configured = f'count(count by (server_id) ({inventory}))'
counter_names = (
    "librespeed_test_attempts_total", "librespeed_test_successes_total",
    "librespeed_test_failures_total", "librespeed_bytes_sent_total",
    "librespeed_bytes_received_total",
)
covered = [
    f'(min by (server_id) (count_over_time({name}{selector}[$__range])) >= 2)'
    for name in counter_names
]
coverage = f'count({" and on(server_id) ".join(covered)} and on(server_id) count by (server_id) ({inventory})) == {configured}'
gate = f' and on() ({coverage})'

def increase(name):
    return f'increase({name}{selector}[$__range]) and on(server_id) {inventory}'

expected_kpis = {
    16: ("Servers tested (window)", (0, 6), [
        f'(count(sum by (server_id) ({increase(counter_names[0])}) > 0) or vector(0)){gate}',
        configured,
    ]),
    17: ("Test outcomes (window)", (6, 6), [
        f'round(sum({increase(counter_names[0])})){gate}',
        f'round(sum({increase(counter_names[1])})){gate}',
        f'round(sum({increase(counter_names[2])})){gate}',
    ]),
    18: ("Success rate (window)", (12, 4), []),
    19: ("Uploaded (window)", (16, 4), [
        f'round(sum({increase(counter_names[3])})){gate}',
    ]),
    20: ("Downloaded (window)", (20, 4), [
        f'round(sum({increase(counter_names[4])})){gate}',
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

snapshot = panels.get(21)
if not snapshot or snapshot.get("title") != "Current server snapshot" or snapshot.get("type") != "table":
    raise SystemExit("Dashboard must contain Table panel 21: Current server snapshot")
if snapshot.get("gridPos") != {"x": 0, "y": 5, "w": 24, "h": 8}:
    raise SystemExit("Current server snapshot must fill the row below window KPIs")
if {panel_id for panel_id in (1, 2, 3, 4, 7, 8) if panel_id in panels}:
    raise SystemExit("Duplicate current gauge, status, and age panels must be removed")
snapshot_targets = snapshot.get("targets", [])
if [target.get("refId") for target in snapshot_targets] != ["A", "B", "C", "D", "E", "F"]:
    raise SystemExit("Current server snapshot must query six server fields")
for target in snapshot_targets:
    expr = target.get("expr", "")
    if target.get("instant") is not True or target.get("format") != "table":
        raise SystemExit("Current server snapshot queries must be instant table queries")
    if 'server_id=~"$server_id"' not in expr or "by (server_id)" not in expr:
        raise SystemExit("Current server snapshot queries must filter and aggregate by server_id")
    if " > 0" not in expr:
        raise SystemExit("Current server snapshot must hide uninitialized timestamps")
    if any(label in expr for label in ("pod", "instance")):
        raise SystemExit("Current server snapshot queries must not expose scrape-target labels")
transformations = snapshot.get("transformations", [])
join = next((item for item in transformations if item.get("id") == "joinByField"), None)
if not join or join.get("options", {}).get("byField") != "server_id" or join.get("options", {}).get("mode") != "outerTabular":
    raise SystemExit("Current server snapshot must outer-join query results by server_id")
organize = next((item for item in transformations if item.get("id") == "organize"), None)
if not organize or organize.get("options", {}).get("renameByName", {}).get("server_id") != "Server":
    raise SystemExit("Current server snapshot must label server_id as Server")
if organize.get("options", {}).get("renameByName", {}).get("Value #F") != "Last success age":
    raise SystemExit("Current server snapshot must include last success age")
if organize.get("options", {}).get("renameByName", {}).get("Value #E") != "Attempt age":
    raise SystemExit("Current server snapshot must include attempt age")
if any(panel_id in panels for panel_id in (14, 15)):
    raise SystemExit("Freshness history charts must be removed")
if panels[16].get("fieldConfig", {}).get("defaults", {}).get("noValue") != "Waiting for counter samples":
    raise SystemExit("Servers tested KPI must show incomplete counter coverage")
if panels[18].get("fieldConfig", {}).get("defaults", {}).get("noValue") != "—":
    raise SystemExit("Success rate KPI must show a neutral dash when no value exists")

def assert_thresholds(panel_id, expected_steps):
    defaults = panels[panel_id].get("fieldConfig", {}).get("defaults", {})
    thresholds = defaults.get("thresholds", {})
    actual = [(step.get("color"), step.get("value")) for step in thresholds.get("steps", [])]
    if defaults.get("color", {}).get("mode") != "thresholds" or thresholds.get("mode") != "absolute" or actual != expected_steps:
        raise SystemExit(f"Dashboard panel {panel_id} has unexpected semantic thresholds")

assert_thresholds(18, [("red", None), ("yellow", 80), ("green", 95)])
orchid = "#DA70D6"
for panel_id in (16, 17):
    defaults = panels[panel_id].get("fieldConfig", {}).get("defaults", {})
    if defaults.get("color") != {"mode": "fixed", "fixedColor": orchid}:
        raise SystemExit(f"Dashboard count KPI {panels[panel_id]['title']} must use neutral Orchid color by default")
failure_overrides = panels[17].get("fieldConfig", {}).get("overrides", [])
failure_override = next((override for override in failure_overrides if override.get("matcher") == {"id": "byName", "options": "Failed"}), None)
if not failure_override:
    raise SystemExit("Dashboard failed count must have a semantic color override")
failure_properties = {prop.get("id"): prop.get("value") for prop in failure_override.get("properties", [])}
failure_steps = [(step.get("color"), step.get("value")) for step in failure_properties.get("thresholds", {}).get("steps", [])]
if failure_properties.get("color") != {"mode": "thresholds"} or failure_properties.get("thresholds", {}).get("mode") != "absolute" or failure_steps != [("green", None), ("red", 1)]:
    raise SystemExit("Dashboard failed count must be green at zero and red at one or more")
if panels[11].get("fieldConfig", {}).get("defaults", {}).get("color") != {"mode": "fixed", "fixedColor": "red"}:
    raise SystemExit("Failed-test history must use red")

expected_mappings = {
    6: {"0": ("red", "DOWN"), "1": ("green", "UP")},
}
for panel_id, expected in expected_mappings.items():
    defaults = panels[panel_id].get("fieldConfig", {}).get("defaults", {})
    mappings = defaults.get("mappings", [])
    actual = {}
    for mapping in mappings:
        actual.update({key: (value.get("color"), value.get("text")) for key, value in mapping.get("options", {}).items()})
    if actual != expected:
        raise SystemExit(f"Dashboard panel {panel_id} must preserve semantic status mappings")
    assert_thresholds(panel_id, [("red", None), ("green", 1)])
assert_thresholds(10, [("red", None), ("green", 1)])
snapshot_status = next((override for override in snapshot.get("fieldConfig", {}).get("overrides", []) if override.get("matcher") == {"id": "byName", "options": "Latest attempt"}), None)
if not snapshot_status:
    raise SystemExit("Current server snapshot must color latest attempt status semantically")
snapshot_status_properties = {prop.get("id"): prop.get("value") for prop in snapshot_status.get("properties", [])}
snapshot_status_mapping = snapshot_status_properties.get("mappings", [{}])[0].get("options", {})
if {key: (value.get("color"), value.get("text")) for key, value in snapshot_status_mapping.items()} != {"0": ("red", "FAILED"), "1": ("green", "SUCCESS")}:
    raise SystemExit("Current server snapshot must preserve failure and success status mappings")

for panel_id in (17, 19, 20):
    description = panels[panel_id].get("description", "").lower()
    if "estimate" not in description or "range edge" not in description:
        raise SystemExit(f"Dashboard KPI {panels[panel_id]['title']} must explain rounded range-edge estimates")
    if panels[panel_id].get("fieldConfig", {}).get("defaults", {}).get("noValue") != "Waiting for counter samples":
        raise SystemExit(f"Dashboard KPI {panels[panel_id]['title']} must show waiting for incomplete samples")

for panel_id in range(16, 21):
    description = panels[panel_id].get("description", "").lower()
    if "two samples" not in description or "first scrape" not in description:
        raise SystemExit(f"Dashboard KPI {panels[panel_id]['title']} must explain sample coverage and startup loss")

success_rate = panels[18]["targets"][0].get("expr", "") if 18 in panels else ""
normalized_rate = re.sub(r"\s+", " ", success_rate).strip()
expected_rate = f'(100 * sum({increase(counter_names[1])}) / sum({increase(counter_names[0])})) and (sum({increase(counter_names[0])}) > 0){gate}'
if re.sub(r"\s+", "", normalized_rate) != re.sub(r"\s+", "", expected_rate):
    raise SystemExit("Dashboard success rate must omit zero-attempt windows")
if len(panels.get(18, {}).get("targets", [])) != 1:
    raise SystemExit("Dashboard success rate must have one target")
if panels[18].get("fieldConfig", {}).get("defaults", {}).get("unit") != "percent":
    raise SystemExit("Dashboard success rate must use Grafana percent units")
for title in ("Uploaded (window)", "Downloaded (window)"):
    panel = panels_by_title.get(title, {})
    description = panel.get("description", "").lower()
    if "successful" not in description or "pre-deployment" not in description or "unavailable" not in description or "estimate" not in description:
        raise SystemExit(f"Dashboard KPI {title} must describe successful-only bytes and unavailable pre-deployment history")
    defaults = panel.get("fieldConfig", {}).get("defaults", {})
    if defaults.get("unit") != "bytes" or defaults.get("decimals") != 0:
        raise SystemExit(f"Dashboard KPI {title} must display rounded bytes")
    if defaults.get("color") != {"mode": "continuous-BlPu"}:
        raise SystemExit(f"Dashboard KPI {title} must use neutral continuous-BlPu coloring")

if panels[21].get("gridPos", {}).get("y") != 5:
    raise SystemExit("Current server snapshot must follow the KPI row")
expected_history_positions = {
    5: (0, 13, 16, 9), 6: (16, 13, 8, 4),
    9: (0, 22, 12, 8), 10: (12, 22, 12, 8),
    11: (0, 30, 8, 7), 12: (8, 30, 8, 7), 13: (16, 30, 8, 7),
}
for panel_id, (x, y, w, h) in expected_history_positions.items():
    if panels.get(panel_id, {}).get("gridPos") != {"x": x, "y": y, "w": w, "h": h}:
        raise SystemExit(f"Dashboard history panel {panel_id} has unexpected position")
history_panels = (5, 9, 10, 11, 12, 13)
for panel_id in history_panels:
    for target in panels[panel_id].get("targets", []):
        expr = target.get("expr", "")
        if 'server_id=~"$server_id"' not in expr or "by (server_id)" not in expr:
            raise SystemExit(f"Dashboard history panel {panel_id} must filter and aggregate by server_id")
        if any(label in expr for label in ("pod", "instance")):
            raise SystemExit(f"Dashboard history panel {panel_id} must not expose scrape-target labels")
if panels[13].get("title") != "Latest-test transfer":
    raise SystemExit("Transfer history must be labeled as latest-test data")
transfer_description = panels[13].get("description", "").lower()
if "latest successful test" not in transfer_description or "sampled" not in transfer_description or "window total" not in transfer_description:
    raise SystemExit("Transfer history must distinguish sampled latest-test values from window totals")
for panel_id in (5, 9, 10, 12, 13):
    fill = panels[panel_id].get("fieldConfig", {}).get("defaults", {}).get("custom", {}).get("fillOpacity", 100)
    if fill > 6:
        raise SystemExit(f"Dashboard history panel {panel_id} must use reduced chart fill")
if panels[11].get("fieldConfig", {}).get("defaults", {}).get("custom", {}).get("fillOpacity", 100) > 30:
    raise SystemExit("Failed-test history must use reduced bar fill")
for panel_id in (5, 9, 12, 13):
    panel = panels[panel_id]
    defaults = panel.get("fieldConfig", {}).get("defaults", {})
    if defaults.get("color", {}).get("mode") != "continuous-BlPu":
        raise SystemExit(f"Dashboard panel {panel_id} must use continuous-BlPu")
if panels[5].get("fieldConfig", {}).get("defaults", {}).get("custom", {}).get("gradientMode") != "scheme":
    raise SystemExit("Throughput history must use scheme gradient mode")
readme = re.sub(r"\s+", " ", (root / "README.md").read_text().lower())
for phrase in ("estimates", "range-edge extrapolation", "rounding", "successful tests only", "counter rollout", "two samples", "first scrape"):
    if phrase not in readme:
        raise SystemExit(f"README dashboard description must explain {phrase}")
for phrase in ("selected-window estimates", "latest-test", "sampled gauge history", "not a window total", "last success age", "attempt age"):
    if phrase not in readme:
        raise SystemExit(f"README must distinguish window estimates from latest-test history: {phrase}")
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
