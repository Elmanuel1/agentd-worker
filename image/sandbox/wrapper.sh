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
	| select(.key | test("\\A[A-Za-z_][A-Za-z0-9_]*\\z"))
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

# As live's cloud mounts them: a file's mount_path is under /mnt/session/uploads ("/data.csv" lands
# at /mnt/session/uploads/data.csv, no path at /mnt/session/uploads/<file id>), read-only. A failed
# read fails the sandbox rather than run the session without its files.
UPLOADS="${AGENTD_UPLOADS_DIR:-/mnt/session/uploads}"
stage_files() {
	session="$(cat "$WORK_ITEM_DIR/session_id")"
	token="$(payload | jq -r '.sessions_token')"
	auth="Authorization: Bearer $token"
	listing="$(mktemp)"
	curl -sf -H "$auth" -H 'anthropic-beta: managed-agents-2026-04-01' -o "$listing" "$base_url/v1/sessions/$session"
	jq -r '.resources[]? | select(.type == "file") | "\(.file_id)\t\(.mount_path // "")"' "$listing" > "$listing.files"
	while IFS="$(printf '\t')" read -r file_id mount_path; do
		rel="${mount_path#/mnt/session/uploads}"
		rel="${rel#/}"
		case "/$rel/" in */../*) echo "not staging $file_id: $mount_path leaves the uploads directory" >&2; exit 1 ;; esac
		target="$UPLOADS/${rel:-$file_id}"
		mkdir -p "$(dirname "$target")"
		curl -sf -H "$auth" -H 'anthropic-beta: files-api-2025-04-14' -o "$target" "$base_url/v1/files/$file_id/content"
		chmod 0444 "$target"
	done < "$listing.files"
	rm -f "$listing" "$listing.files"
}
if [ -n "${AGENTD_CLOUD:-}" ]; then
	stage_files
fi

# Off Kubernetes there is no API server for Anthropic's isolation check to probe, so it probes the
# metadata address again in its place.
export KUBERNETES_SERVICE_HOST="${KUBERNETES_SERVICE_HOST:-169.254.169.254}"
export KUBERNETES_SERVICE_PORT="${KUBERNETES_SERVICE_PORT:-80}"

exec "$ANTHROPIC_WORKER" "$@"
