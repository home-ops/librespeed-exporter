#!/bin/sh
set -eu
export GIT_CONFIG_GLOBAL=/dev/null

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM

git init --bare "$tmp/remote.git" >/dev/null
git init "$tmp/source" >/dev/null
git -C "$tmp/source" config user.name test
git -C "$tmp/source" config user.email test@example.invalid
printf 'source\n' >"$tmp/source/file"
git -C "$tmp/source" add file
git -C "$tmp/source" commit -m initial >/dev/null
git -C "$tmp/source" -c tag.gpgSign=false tag v1.2.3
git -C "$tmp/source" remote add origin "$tmp/remote.git"
git -C "$tmp/source" push origin HEAD refs/tags/v1.2.3 >/dev/null

git clone --no-checkout --no-tags "$tmp/remote.git" "$tmp/checkout" >/dev/null
git clone --no-checkout --no-tags "$tmp/remote.git" "$tmp/broken-checkout" >/dev/null
git -C "$tmp/broken-checkout" fetch --depth=1 origin \
  "refs/tags/v1.2.3:" "refs/tags/v1.2.3" >/dev/null 2>&1 || true
if git -C "$tmp/broken-checkout" rev-parse --verify v1.2.3 >/dev/null 2>&1; then
	printf '%s\n' 'broken split refspec unexpectedly created local tag' >&2
	exit 1
fi

git -C "$tmp/checkout" fetch --depth=1 origin \
  "refs/tags/v1.2.3:refs/tags/v1.2.3" >/dev/null
test "$(git -C "$tmp/checkout" rev-list -n 1 v1.2.3)" = \
  "$(git -C "$tmp/source" rev-parse HEAD)"

printf '%s\n' 'CLI tag refspec verification passed'
