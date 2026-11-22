#!/bin/sh
# Tests the wrapper (IM-3, IM-4) with Anthropic's script replaced by one that prints its environment.
#   sh image/sandbox/wrapper_test.sh
set -eu
here="$(cd "$(dirname "$0")" && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/run/work-item"
secret="$(printf '%s' '{"sessions_token":"tok/+=","environment_variables":{"GITHUB_TOKEN":"agentd-ph-1","QUOTED":"a b'"'"'c","ANTHROPIC_BASE_URL":"evil","1BAD":"x"}}' | base64 | tr -d '\n=' | tr '/+' '_-')"
printf '%s' "$secret" > "$tmp/run/work-item/secret"
printf 'sesn_1' > "$tmp/run/work-item/session_id"
printf 'https://agentd.example:8443/' > "$tmp/run/work-item/base_url"
printf '#!/bin/sh\nenv\n' > "$tmp/fake-worker"
chmod +x "$tmp/fake-worker"
printf 'CA\n' > "$tmp/ca.crt"

run() {
	env -i PATH="$PATH" CMA_SHARED_DIR="$tmp/run" AGENTD_ANTHROPIC_WORKER="$tmp/fake-worker" AGENTD_PROXY_CA="$tmp/ca.crt" AGENTD_SYSTEM_CA="$tmp/ca.crt" "$@" sh "$here/wrapper.sh"
}
fail() { echo "FAIL: $1" >&2; exit 1; }

out="$(run)"
echo "$out" | grep -qx 'GITHUB_TOKEN=agentd-ph-1' || fail "the secret's variable is not exported"
echo "$out" | grep -qx "QUOTED=a b'c" || fail "a value with spaces and quotes is not kept whole"
echo "$out" | grep -q '^ANTHROPIC_BASE_URL=' && fail "an ANTHROPIC_ variable from the secret was exported"
echo "$out" | grep -q '^1BAD=' && fail "a name that is not a shell name was exported"
echo "$out" | grep -qi '_proxy=' && fail "proxy set in an image built without one"
echo "$out" | grep -qx 'KUBERNETES_SERVICE_HOST=169.254.169.254' || fail "the isolation probe has no stand-in host"

out="$(run AGENTD_PROXY_URL=http://proxy.internal:3128)"
for v in HTTPS_PROXY HTTP_PROXY https_proxy http_proxy; do
	echo "$out" | grep -qx "$v=http://sesn_1:tok%2F%2B%3D@proxy.internal:3128" || fail "$v is not the proxy with the session's credentials"
done
echo "$out" | grep -qx 'NO_PROXY=agentd.example' || fail "NO_PROXY is not agentd's host"
echo "$out" | grep -qx 'no_proxy=agentd.example' || fail "no_proxy is not agentd's host"
echo "$out" | grep -qx "NODE_EXTRA_CA_CERTS=$tmp/ca.crt" || fail "the proxy CA is not trusted"
echo "wrapper: ok"
