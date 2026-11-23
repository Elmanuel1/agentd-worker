# agentd's sandbox image: Anthropic's cma-sandbox, unchanged, with the proxy's CA certificate and a
# wrapper installed at cma-sandbox-worker's path, because the chart fixes the worker's command.
# Anthropic's script stays as cma-sandbox-worker-anthropic, and the wrapper runs it.
#
#   docker buildx build -t agentd-sandbox image/sandbox
#   docker buildx build --build-arg AGENTD_PROXY_URL=http://proxy.agentd:3128 \
#     --build-arg AGENTD_CLOUD=1 --build-context proxy-ca=<dir with ca.crt> -t agentd-sandbox:cloud image/sandbox
#
# The digest is the multi-arch index of cma-sandbox 0.1.0, read 2026-11-22. CI fails a FROM that is
# not pinned, and a layer that is not one of the COPY steps below.
ARG BASE=us-docker.pkg.dev/anthropic-public-registry/sandboxes/cma-sandbox:0.1.0@sha256:d686b816635367985352f25a28801d89a1953d0b1beff347b1a6e14e77b11956

FROM ${BASE} AS anthropic

FROM scratch AS no-proxy-ca
COPY empty.crt /ca.crt

FROM no-proxy-ca AS proxy-ca

FROM ${BASE}
ARG AGENTD_PROXY_URL=""
ARG AGENTD_CLOUD=""
ENV AGENTD_PROXY_URL=${AGENTD_PROXY_URL} AGENTD_CLOUD=${AGENTD_CLOUD}
COPY --from=anthropic /usr/local/bin/cma-sandbox-worker /usr/local/bin/cma-sandbox-worker-anthropic
COPY --from=proxy-ca /ca.crt /usr/local/share/agentd/proxy-ca.crt
COPY --chmod=0755 wrapper.sh /usr/local/bin/cma-sandbox-worker
# live's cloud mounts a session's files under /mnt/session; Anthropic's image has no such directory.
COPY --chown=1001:1000 session/ /mnt/session/
