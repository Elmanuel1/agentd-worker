#!/bin/sh
# Fails unless the sandbox image is Anthropic's cma-sandbox pinned by digest plus only the COPY
# steps of the Dockerfile (IM-1, IM-2).
#   image/sandbox/check.sh agentd-sandbox:ci
set -eu
image="$1"
dockerfile="$(dirname "$0")/Dockerfile"
fail() { echo "FAIL: $1" >&2; exit 1; }

base="$(sed -n 's/^ARG BASE=//p' "$dockerfile")"
echo "$base" | grep -Eq '^us-docker\.pkg\.dev/anthropic-public-registry/sandboxes/cma-sandbox:[^@]+@sha256:[0-9a-f]{64}$' ||
	fail "the base is not Anthropic's cma-sandbox pinned by digest: $base"
# shellcheck disable=SC2016 # the Dockerfile's own ${BASE}, matched as written
grep '^FROM' "$dockerfile" | grep -v -e '^FROM \${BASE}' -e '^FROM scratch AS no-proxy-ca$' -e '^FROM no-proxy-ca AS proxy-ca$' &&
	fail "a FROM other than the pinned base"

docker pull -q "$base" >/dev/null
layers() { docker image inspect -f '{{len .RootFS.Layers}}' "$1"; }
steps() { docker history --no-trunc --format '{{.CreatedBy}}' "$1"; }
added=$(($(layers "$image") - $(layers "$base")))
[ "$added" -eq 4 ] || fail "the image adds $added layers, want 4"
new=$(($(steps "$image" | wc -l) - $(steps "$base" | wc -l)))
steps "$image" | head -n "$new" | grep -Ev '^(COPY|ENV|ARG) ' && fail "a step that is not COPY, ENV or ARG"
[ "$(steps "$image" | head -n "$new" | grep -c '^COPY ')" -eq 4 ] || fail "want 4 COPY steps"
docker run --rm --entrypoint sh "$image" -c '[ -s /usr/local/bin/cma-sandbox-worker-anthropic ] && grep -q "^# agentd.s wrapper" /usr/local/bin/cma-sandbox-worker' ||
	fail "the wrapper is not at cma-sandbox-worker's path, or Anthropic's script is missing"
echo "sandbox image: ok"
