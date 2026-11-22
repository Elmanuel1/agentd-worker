# ADR 0001: Reuse Anthropic's sandboxes, launch the rest through a port, swap credentials in one shared proxy

**Status:** Proposed

## Context

agentd hands a session's tool calls to a worker through a work queue. Anthropic publishes a restart-safe dispatcher
and Helm chart (`cma-sandboxes`) that gives each session its own gVisor pod and a volume kept until the session ends,
hands the pod its item through an attested fetch, and calls only routes agentd serves. Off Kubernetes Anthropic
publishes nothing. Live attaches `environment_variable` credentials at egress, outside the sandbox, and no published
proxy verifies placeholders minted per session for their caller.

## Decision

On Kubernetes, run Anthropic's chart unchanged against agentd, for self-hosted environments and for our `cloud` on
EKS. Elsewhere, run our own controller that follows the dispatcher's contract and launches sandboxes through a
`Launcher` port, with a Docker adapter by default and a Fargate adapter for our `cloud`. Every other outside
dependency is a port too. For our `cloud`, route every sandbox through one shared iron-proxy, extended through ports
to verify agentd's signed placeholders against the calling session. A reaper stops a dead worker's item and answers
its owed tool calls with error results, as `ant` does on SIGTERM.

## Consequences

- Kubernetes sandboxes follow live's own design and get Anthropic's fixes by upgrading the chart.
- A new sandbox backend is one adapter that passes the shared launcher suite.
- agentd changes one message, so a deleted session reads as live's `Session not found: <id>`.
- The proxy is shared by every `cloud` session, so it runs replicated and is load-tested before launch.
- We carry an iron-proxy fork until the extension merges upstream.
