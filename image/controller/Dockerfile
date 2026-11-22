# The agentd-worker image: one static binary for the controller (scale), the reaper (reap) and a
# sandbox's fetch and sidecar containers. Build from the repository root:
#
#   docker buildx build -f image/controller/Dockerfile -t agentd-worker .
#
# Digests read 2026-11-22: golang:1.27.4 and distroless/static-debian12, both multi-arch.
ARG GO_IMAGE=golang:1.27.4@sha256:205c7636d74e0ffbe3d007162df7d74f859f5a8c8322dfb9f907977686c126cf
ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian12@sha256:d75cdd72874d4790092fcb1b058493ecf6bb5bf2b2b897045b00ff01d91843f2

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd cmd
COPY internal internal
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags=-s -o /out/agentd-worker ./cmd/agentd-worker

FROM ${RUNTIME_IMAGE}
COPY --from=build /out/agentd-worker /usr/local/bin/agentd-worker
# The controller and reaper run as nonroot; fetch and the sidecar are started as root by their
# launcher to give the worker its files.
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/agentd-worker"]
