#!/bin/sh
# Runs the e2e suite with the Docker adapter against an agentd already listening on 0.0.0.0:8080
# (CI starts it). Sandboxes join the network agentd-e2e and reach agentd and the controller at its
# gateway, 172.30.0.1, so the controller sees each fetch container's own address.
#   AGENTD_BIN=... AGENTD_TEST_KEY=... AGENTD_SANDBOX_IMAGE=... AGENTD_TOOL_IMAGE=... e2e/docker.sh
set -eu
here="$(cd "$(dirname "$0")" && pwd)"
out="$here/out"
mkdir -p "$out"
gateway=172.30.0.1

docker network inspect agentd-e2e >/dev/null 2>&1 ||
	docker network create --subnet 172.30.0.0/24 --gateway "$gateway" agentd-e2e >/dev/null
# Anthropic's script refuses to run where the metadata address answers, as it should on a host
# that blocks it; the runner's own metadata service is blocked here the way a deployment blocks it.
# Each rule added is removed on exit, however the run ends.
added=0
cleanup() {
	if [ "$added" -ge 1 ]; then sudo iptables -D DOCKER-USER -d 169.254.0.0/16 -j REJECT; fi
	if [ "$added" -ge 2 ]; then sudo iptables -D DOCKER-USER -i br-+ -d 168.63.129.16 -j REJECT; fi
}
trap cleanup EXIT
sudo iptables -I DOCKER-USER -d 169.254.0.0/16 -j REJECT
added=1
sudo iptables -I DOCKER-USER -i br-+ -d 168.63.129.16 -j REJECT
added=2

# Each sandbox container's output, kept past its removal for the failure log.
docker events --filter type=container --filter event=start --filter label=agentd.session --format '{{.ID}}' |
	while read -r id; do docker logs -f "$id" >"$out/container-$id.log" 2>&1 & done &

openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 1 -subj "/CN=agentd-e2e" \
	-addext "subjectAltName=IP:$gateway" -keyout "$out/handoff.key" -out "$out/handoff.crt" 2>/dev/null
(cd "$here/.." && go build -o "$out/agentd-worker" ./cmd/agentd-worker)

export AGENTD_WORKER_BIN="$out/agentd-worker"
export AGENTD_E2E_LOG="$out/controller.log"
export AGENTD_WORKER_LAUNCHER=docker AGENTD_DOCKER_NETWORK=agentd-e2e AGENTD_DOCKER_RUNTIME="${AGENTD_DOCKER_RUNTIME:-runsc}"
export AGENTD_DOCKER_STATE_DIR="$out"
export AGENTD_SANDBOX_BASE_URL="http://$gateway:8080"
export AGENTD_HANDOFF_URL="https://$gateway:8443" AGENTD_HANDOFF_ADDR="$gateway:8443"
AGENTD_HANDOFF_CERT="$(cat "$out/handoff.crt")" AGENTD_HANDOFF_KEY="$(cat "$out/handoff.key")" AGENTD_HANDOFF_CA="$(cat "$out/handoff.crt")"
export AGENTD_HANDOFF_CERT AGENTD_HANDOFF_KEY AGENTD_HANDOFF_CA
# Short waits, so a restart and a reap fit in a CI run: reclaim after 30 s, reap every 10 s.
export AGENTD_HANDOFF_WAIT_SECONDS=15 AGENTD_REAP_INTERVAL_SECONDS=10

pip install -q -r "$here/requirements.txt"
cd "$here" && python3 -m pytest -v -p no:cacheprovider .
