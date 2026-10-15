#!/bin/sh
set -eu

image=${1:?usage: image-smoke-test.sh IMAGE}
docker_bin=${DOCKER:-docker}
container=
tmpdir=$(mktemp -d)

cleanup() {
	status=$?
	if [ "$status" -ne 0 ] && [ -n "$container" ]; then
		printf '%s\n' 'Container logs:' >&2
		"$docker_bin" logs "$container" >&2 || true
	fi
	if [ -n "$container" ]; then
		"$docker_bin" rm -f "$container" >/dev/null 2>&1 || true
	fi
	rm -rf "$tmpdir"
	exit "$status"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

smoke_test_timeout=${SMOKE_TEST_TIMEOUT_SECONDS:-20}
if ! perl -e '
	exit 1 unless $ARGV[0] =~ /\A(?:\d+(?:\.\d*)?|\.\d+)\z/;
	exit 1 unless $ARGV[0] > 0 && $ARGV[0] <= 20;
' "$smoke_test_timeout"; then
	printf '%s\n' 'SMOKE_TEST_TIMEOUT_SECONDS must be greater than 0 and at most 20' >&2
	exit 2
fi

cat >"$tmpdir/servers.json" <<'JSON'
[
  {
    "id": 1847,
    "name": "smoke-local-only",
    "server": "http://127.0.0.1:9/",
    "dlURL": "garbage",
    "ulURL": "garbage",
    "pingURL": "garbage",
    "getIpURL": "garbage"
  }
]
JSON

container=$("$docker_bin" run -d \
	--read-only \
	--tmpfs /tmp:rw,noexec,nosuid,size=16m \
	--cap-drop ALL \
	--security-opt no-new-privileges:true \
	--pids-limit 64 \
	--memory 128m \
	--cpus 1 \
	--user 65532:65532 \
	--publish 127.0.0.1::9469 \
	--mount "type=bind,src=$tmpdir/servers.json,dst=/etc/librespeed/servers.json,readonly" \
	"$image")
port=$("$docker_bin" port "$container" 9469/tcp | awk -F: '{print $NF}')
base_url="http://127.0.0.1:$port"

deadline=$(perl -MTime::HiRes=clock_gettime,CLOCK_MONOTONIC \
	-e 'printf "%.6f", clock_gettime(CLOCK_MONOTONIC) + $ARGV[0]' "$smoke_test_timeout")
remaining_timeout() {
	perl -MTime::HiRes=clock_gettime,CLOCK_MONOTONIC -e '
		my $remaining = $ARGV[0] - clock_gettime(CLOCK_MONOTONIC);
		exit 1 if $remaining <= 0;
		my $timeout = $remaining < 1 ? $remaining : 1;
		my $milliseconds = int($timeout * 1000);
		$milliseconds++ if $milliseconds < $timeout * 1000;
		$milliseconds = 1 if $milliseconds < 1;
		printf "%.3f", $milliseconds / 1000;
	' "$deadline"
}

ready=false
while :; do
	if timeout=$(remaining_timeout) \
		&& curl --noproxy '*' --silent --show-error --fail --max-time "$timeout" "$base_url/healthz" >/dev/null 2>&1 \
		&& timeout=$(remaining_timeout) \
		&& curl --noproxy '*' --silent --show-error --fail --max-time "$timeout" "$base_url/readyz" >/dev/null 2>&1; then
		ready=true
		break
	fi
	if ! timeout=$(remaining_timeout); then
		break
	fi
	sleep "$timeout"
done
if [ "$ready" != true ]; then
	printf 'healthz and readyz did not both succeed within %s seconds\n' "$smoke_test_timeout" >&2
	exit 1
fi

if ! curl --noproxy '*' --silent --show-error --fail --max-time 2 "$base_url/metrics" >"$tmpdir/metrics"; then
	printf '%s\n' 'metrics scrape failed or exceeded 2 seconds' >&2
	exit 1
fi

if ! awk '
/^librespeed_/ {
  if ($0 !~ /^[^{]+\{server_id="1847"\} /) exit 1
  found = 1
}
END { if (!found) exit 1 }
' "$tmpdir/metrics"; then
	printf '%s\n' 'metrics missing expected bounded server_id labels' >&2
	exit 1
fi
