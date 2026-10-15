#!/bin/sh
set -eu

repo=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
smoke="$repo/scripts/image-smoke-test.sh"
tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT HUP INT TERM
mkdir "$tmpdir/bin"

cat >"$tmpdir/fake-docker" <<'DOCKER'
#!/bin/sh
case "$1" in
run) printf '%s\n' fake-container ;;
port) printf '%s\n' 127.0.0.1:49152 ;;
logs|rm) exit 0 ;;
*) exit 2 ;;
esac
DOCKER
cat >"$tmpdir/bin/curl" <<'CURL'
#!/bin/sh
printf '%s\n' "$*" >>"$CURL_LOG"
if [ "${1-}" != --noproxy ] || [ "${2-}" != '*' ]; then
	printf '%s\n' 'curl missing loopback proxy bypass' >&2
	exit 90
fi
max_time=
previous=
for arg do
	if [ "$previous" = --max-time ]; then max_time=$arg; fi
	previous=$arg
done
if [ -z "$max_time" ] || ! awk -v value="$max_time" 'BEGIN { exit !(value > 0 && value <= 20) }'; then
	printf '%s\n' 'curl missing --max-time bounded by 20 seconds' >&2
	exit 91
fi
case "${FAKE_CURL_MODE:?}" in
timeout|slow|remaining)
	if ! awk -v value="$max_time" 'BEGIN { exit !(value <= 1) }'; then
		printf '%s\n' 'curl --max-time exceeds remaining one-second test deadline' >&2
		exit 92
	fi
	;;
submillisecond)
	if ! awk -v value="$max_time" 'BEGIN { exit !(value >= 0.001) }'; then
		printf '%s\n' 'positive sub-millisecond deadline formatted as zero' >&2
		exit 94
	fi
	;;
esac
url=
for arg do url=$arg; done
case "${FAKE_CURL_MODE:?}:$url" in
success:*healthz|success:*readyz) exit 0 ;;
success:*metrics)
	printf '%s\n' 'librespeed_test_success{server_id="1847"} 0'
	exit 0
	;;
timeout:*healthz|timeout:*readyz) sleep "$max_time"; exit 28 ;;
slow:*healthz|slow:*readyz) sleep "$max_time"; exit 28 ;;
remaining:*healthz) sleep 0.4; exit 0 ;;
remaining:*readyz)
	sleep "$max_time"
	exit 28
	;;
submillisecond:*healthz) exit 28 ;;
*) exit 22 ;;
esac
CURL
cat >"$tmpdir/bin/perl" <<'PERL'
#!/bin/sh
if [ "${FAKE_PERL_MODE:-}" != submillisecond ]; then exec /usr/bin/perl "$@"; fi
while [ "$#" -gt 0 ]; do
	case "$1" in
	-e) code=$2; shift 2; break ;;
	*) shift ;;
	esac
done
case "$code" in
*'clock_gettime(CLOCK_MONOTONIC) + $ARGV[0]'*) FAKE_CLOCK_EXPR='100' ;;
*'clock_gettime(CLOCK_MONOTONIC)'*)
	count=0
	[ ! -f "$FAKE_CLOCK_COUNT" ] || count=$(cat "$FAKE_CLOCK_COUNT")
	count=$((count + 1))
	printf '%s\n' "$count" >"$FAKE_CLOCK_COUNT"
	if [ "$count" -eq 1 ]; then FAKE_CLOCK_EXPR='(101 - $ENV{FAKE_REMAINING})'
	else FAKE_CLOCK_EXPR='101'
	fi
	;;
*)
	exec /usr/bin/perl -e "$code" "$@"
	;;
esac
export FAKE_CLOCK_EXPR
code=$(printf '%s' "$code" | /usr/bin/perl -pe 's/clock_gettime\(CLOCK_MONOTONIC\)/$ENV{FAKE_CLOCK_EXPR}/g')
exec /usr/bin/perl -e "$code" "$@"
PERL
chmod +x "$tmpdir/fake-docker" "$tmpdir/bin/curl"
chmod +x "$tmpdir/bin/perl"

if ! DOCKER="$tmpdir/fake-docker" \
	CURL_LOG="$tmpdir/curl-success.log" \
	FAKE_CURL_MODE=success \
	PATH="$tmpdir/bin:$PATH" \
	"$smoke" test-image; then
	printf '%s\n' 'smoke test should pass against fake healthy container' >&2
	exit 1
fi

if ! rg --quiet -- '--noproxy \*' "$tmpdir/curl-success.log"; then
	printf '%s\n' 'every successful-mode curl call must bypass proxies' >&2
	exit 1
fi

started=$(perl -MTime::HiRes=clock_gettime,CLOCK_MONOTONIC -e 'print clock_gettime(CLOCK_MONOTONIC)')
if DOCKER="$tmpdir/fake-docker" \
	CURL_LOG="$tmpdir/curl-timeout.log" \
	FAKE_CURL_MODE=timeout \
	SMOKE_TEST_TIMEOUT_SECONDS=1 \
	PATH="$tmpdir/bin:$PATH" \
	"$smoke" test-image >"$tmpdir/timeout.out" 2>&1; then
	printf '%s\n' 'smoke test should fail when readiness probes time out' >&2
	exit 1
fi
ended=$(perl -MTime::HiRes=clock_gettime,CLOCK_MONOTONIC -e 'print clock_gettime(CLOCK_MONOTONIC)')
perl -e 'exit(($ARGV[1] - $ARGV[0] < 2) ? 0 : 1)' "$started" "$ended" || {
	printf 'readiness deadline exceeded test budget: %s seconds\n' "$((1))" >&2
	exit 1
}

if ! rg --quiet -- '--noproxy \*' "$tmpdir/curl-timeout.log"; then
	printf '%s\n' 'every timeout-mode curl call must bypass proxies' >&2
	exit 1
fi

started=$(perl -MTime::HiRes=clock_gettime,CLOCK_MONOTONIC -e 'print clock_gettime(CLOCK_MONOTONIC)')
if DOCKER="$tmpdir/fake-docker" \
	CURL_LOG="$tmpdir/curl-deadline.log" \
	FAKE_CURL_MODE=slow \
	SMOKE_TEST_TIMEOUT_SECONDS=1 \
	PATH="$tmpdir/bin:$PATH" \
	"$smoke" test-image >"$tmpdir/deadline.out" 2>&1; then
	printf '%s\n' 'smoke test should stop at its deadline when request returns only on timeout' >&2
	exit 1
fi
ended=$(perl -MTime::HiRes=clock_gettime,CLOCK_MONOTONIC -e 'print clock_gettime(CLOCK_MONOTONIC)')
perl -e 'exit(($ARGV[1] - $ARGV[0] < 2) ? 0 : 1)' "$started" "$ended" || {
	printf '%s\n' 'probe exceeded configured deadline despite curl max-time' >&2
	exit 1
}

if SMOKE_TEST_TIMEOUT_SECONDS=21 DOCKER="$tmpdir/fake-docker" \
	CURL_LOG="$tmpdir/curl-over-cap.log" \
	FAKE_CURL_MODE=success \
	PATH="$tmpdir/bin:$PATH" \
	"$smoke" test-image >"$tmpdir/over-cap.out" 2>&1; then
	printf '%s\n' 'smoke test should reject timeout budgets above 20 seconds' >&2
	exit 1
fi

if DOCKER="$tmpdir/fake-docker" \
	CURL_LOG="$tmpdir/curl-remaining.log" \
	FAKE_CURL_MODE=remaining \
	SMOKE_TEST_TIMEOUT_SECONDS=1 \
	PATH="$tmpdir/bin:$PATH" \
	"$smoke" test-image >"$tmpdir/remaining.out" 2>&1; then
	printf '%s\n' 'smoke test should fail when readiness remains unavailable' >&2
	exit 1
fi
first_timeout=$(awk 'NR == 1 { for (i = 1; i < NF; i++) if ($i == "--max-time") print $(i + 1) }' "$tmpdir/curl-remaining.log")
second_timeout=$(awk 'NR == 2 { for (i = 1; i < NF; i++) if ($i == "--max-time") print $(i + 1) }' "$tmpdir/curl-remaining.log")
if ! awk -v first="$first_timeout" -v second="$second_timeout" 'BEGIN { exit !(second > 0 && second < first) }'; then
	printf '%s\n' 'subsequent probe timeout was not reduced to remaining budget' >&2
	exit 1
fi

if DOCKER="$tmpdir/fake-docker" \
	CURL_LOG="$tmpdir/curl-submillisecond.log" \
	FAKE_CURL_MODE=submillisecond \
	FAKE_PERL_MODE=submillisecond \
	FAKE_CLOCK_COUNT="$tmpdir/submillisecond-count" \
	FAKE_REMAINING=0.0005 \
	SMOKE_TEST_TIMEOUT_SECONDS=1 \
	PATH="$tmpdir/bin:$PATH" \
	"$smoke" test-image >"$tmpdir/submillisecond.out" 2>&1; then
	printf '%s\n' 'smoke test should fail when health probe does not become ready' >&2
	exit 1
fi
awk '{ for (i = 1; i < NF; i++) if ($i == "--max-time" && $(i + 1) + 0 >= 0.001) found = 1 } END { exit !found }' "$tmpdir/curl-submillisecond.log" || {
	cat "$tmpdir/submillisecond.out" >&2
	printf '%s\n' 'positive 0.5ms remainder must emit curl timeout of at least 0.001' >&2
	exit 1
}

if DOCKER="$tmpdir/fake-docker" \
	CURL_LOG="$tmpdir/curl-submicrosecond.log" \
	FAKE_CURL_MODE=submillisecond \
	FAKE_PERL_MODE=submillisecond \
	FAKE_CLOCK_COUNT="$tmpdir/submicrosecond-count" \
	FAKE_REMAINING=0.0000005 \
	SMOKE_TEST_TIMEOUT_SECONDS=1 \
	PATH="$tmpdir/bin:$PATH" \
	"$smoke" test-image >"$tmpdir/submicrosecond.out" 2>&1; then
	printf '%s\n' 'smoke test should fail when health probe does not become ready' >&2
	exit 1
fi
awk '{ for (i = 1; i < NF; i++) if ($i == "--max-time" && $(i + 1) + 0 >= 0.001) found = 1 } END { exit !found }' "$tmpdir/curl-submicrosecond.log" || {
	printf '%s\n' 'positive 0.5us remainder must emit curl timeout of at least 0.001' >&2
	exit 1
}
