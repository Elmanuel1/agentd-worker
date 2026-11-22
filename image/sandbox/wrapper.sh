#!/bin/sh
# agentd's wrapper, at cma-sandbox-worker's path. It waits for the work item the fetch container
# leaves in /run/cma, exports the work secret's environment_variables (ant does not), points
# tools at the egress proxy when the image was built with one, stages a cloud session's files,
# then execs Anthropic's script, which runs ant. POSIX sh so it runs under busybox.
set -eu

SHARED_DIR="${CMA_SHARED_DIR:-/run/cma}"
WORK_ITEM_DIR="$SHARED_DIR/work-item"
ANTHROPIC_WORKER="${AGENTD_ANTHROPIC_WORKER:-/usr/local/bin/cma-sandbox-worker-anthropic}"
PROXY_CA="${AGENTD_PROXY_CA:-/usr/local/share/agentd/proxy-ca.crt}"
SYSTEM_CA="${AGENTD_SYSTEM_CA:-/etc/ssl/certs/ca-certificates.crt}"

until [ -r "$WORK_ITEM_DIR/secret" ]; do sleep 1; done

# The secret is URL-safe base64 JSON, with or without padding.
payload() {
	s="$(tr '_-' '/+' < "$WORK_ITEM_DIR/secret")"
	case $((${#s} % 4)) in 2) s="$s==" ;; 3) s="$s=" ;; esac
	printf '%s' "$s" | base64 -d
}

# Each entry becomes an export; a name that is not a shell name, or that would replace the
# worker's own ANTHROPIC_* settings, is skipped.
exports="$(payload | jq -r '(.environment_variables // {}) | to_entries[]
	| select(.key | test("^[A-Za-z_][A-Za-z0-9_]*$"))
	| select(.key | startswith("ANTHROPIC_") | not)
	| "export \(.key)=\(.value | @sh)"')"
eval "$exports"

base_url="$(cat "$WORK_ITEM_DIR/base_url")"
agentd_host="$(printf '%s' "$base_url" | sed -E 's#^[a-z]+://##; s#[/:].*$##')"

if [ -n "${AGENTD_PROXY_URL:-}" ]; then
	session="$(cat "$WORK_ITEM_DIR/session_id")"
	token="$(payload | jq -r '.sessions_token | @uri')"
	scheme="${AGENTD_PROXY_URL%%://*}"
	proxy="$scheme://$session:$token@${AGENTD_PROXY_URL#*://}"
	export HTTPS_PROXY="$proxy" HTTP_PROXY="$proxy" https_proxy="$proxy" http_proxy="$proxy"
	export NO_PROXY="$agentd_host" no_proxy="$agentd_host"
	if [ -s "$PROXY_CA" ]; then
		bundle="$(mktemp)"
		cat "$SYSTEM_CA" "$PROXY_CA" > "$bundle"
		export SSL_CERT_FILE="$bundle" CURL_CA_BUNDLE="$bundle" REQUESTS_CA_BUNDLE="$bundle" NODE_EXTRA_CA_CERTS="$PROXY_CA"
	fi
fi

# Files mount under /mnt/session or /workspace; another path is left out, as no tool may write it.
stage_files() {
	session="$(cat "$WORK_ITEM_DIR/session_id")"
	token="$(payload | jq -r '.sessions_token')"
	auth="Authorization: Bearer $token"
	curl -sf -H "$auth" -H 'anthropic-beta: managed-agents-2026-04-01' "$base_url/v1/sessions/$session" |
		jq -r '.resources[]? | select(.type == "file") | "\(.file_id)\t\(.mount_path)"' |
		while IFS="$(printf '\t')" read -r file_id mount_path; do
			case "$mount_path" in
			/mnt/session/* | /workspace/*) ;;
			*) echo "not staging $file_id at $mount_path: outside /mnt/session and /workspace" >&2; continue ;;
			esac
			mkdir -p "$(dirname "$mount_path")"
			curl -sf -H "$auth" -H 'anthropic-beta: files-api-2025-04-14' -o "$mount_path" "$base_url/v1/files/$file_id/content"
		done
}
if [ -n "${AGENTD_CLOUD:-}" ]; then
	stage_files
fi

# Off Kubernetes there is no API server for Anthropic's isolation check to probe, so it probes the
# metadata address again in its place.
export KUBERNETES_SERVICE_HOST="${KUBERNETES_SERVICE_HOST:-169.254.169.254}"
export KUBERNETES_SERVICE_PORT="${KUBERNETES_SERVICE_PORT:-80}"

exec "$ANTHROPIC_WORKER" "$@"
