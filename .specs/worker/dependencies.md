# Dependencies: agentd-worker

Every external fact the design rests on, with where it was read. All read 2026-11-22.

| # | Fact | Source | Status |
|---|---|---|---|
| A1 | `ant beta:worker run` takes an item already claimed and acked; it heartbeats and force-stops the item on exit unless the lease was lost, and uploads no outputs. | anthropic-cli `pkg/cmd/worker.go`; anthropic-sdk-go v2.13.0 `lib/environments/worker.go` | VERIFIED |
| A2 | On attach, the session runner reads the session's history and runs tool calls not yet answered. | anthropic-sdk-go v2.13.0 `betasessiontoolrunner.go` | VERIFIED |
| A3 | On SIGTERM, `ant` "posts its error result, and marks the work item `stopped`"; "A killed process runs no teardown." | platform.claude.com, managed-agents/self-hosted-sandboxes-operations | VERIFIED |
| A4 | `cma-dispatcher` calls only `work poll`, `work ack`, `work stats` and `sessions get`, acks after the pod collects the item, and recovers its claims after a restart. | anthropic-cli `sandboxes/dispatcher` source and README | VERIFIED |
| A5 | The chart gives each session a 10 GiB `ReadWriteOnce` volume at `/workspace`, kept until the session ends; it defaults to the `gvisor` runtime class; its values schema is closed; egress is `publicInternet` or `fqdnGke`; the registry and sandbox image can be your own. | anthropic-cli `sandboxes/kubernetes/chart/values.yaml`, `values.schema.json`; the per-session sandboxes page | VERIFIED |
| A6 | Anthropic's `cma-sandbox-worker` reads `session_id`, `work_id`, `environment_id`, `base_url` and `secret` from the work-item directory under `/run/cma`. | anthropic-cli `sandboxes/image/worker.sh` | VERIFIED |
| A7 | Live substitutes `environment_variable` credentials "at egress, not inside the sandbox", scoped by `allowed_hosts` and `injection_location`; self-hosted sandboxes get none. | platform.claude.com, managed-agents/vaults | VERIFIED |
| A8 | Live does not re-queue a stopped item; a message to a session whose worker has exited queues it again. | User, 2026-11-22; `cma-dispatcher` README | VERIFIED |
| A9 | On live a work item's id is its session's id; the dispatcher relies on it to tell a redelivery from a new message. | anthropic-cli `sandboxes/dispatcher/internal/dispatcher/handoff.go` | VERIFIED |
| A10 | The dispatcher hands a pod its item through `GET /work-item`, authenticated by a token mounted only into `fetch` or by a one-time nonce accepted only from the sandbox's own running pod, acks after the response is written, and polls with `reclaim_older_than_ms` at twice its handoff wait (120 s default). After a restart it holds no nonces and re-arms the sandbox once its pod is gone. | `cma-dispatcher` README; `internal/config/config.go`; `internal/cmaapi/client.go` | VERIFIED |
| A11 | The dispatcher treats a session as gone only on a 404 whose message is `Session not found: <id>`. | `internal/cmaapi/client.go` | VERIFIED |
| A12 | The chart fixes the worker container's command (`cma-sandbox-worker`) and environment (`CMA_MAX_IDLE`); its NetworkPolicies allow only DNS, public IPv4 and the CMA server. | chart `values.schema.json`, `templates/` | VERIFIED |
| A13 | `ant beta:worker run` does not export the work secret's `environment_variables`. | anthropic-cli `pkg/cmd/worker.go` | VERIFIED |
| B1 | agentd's `work/stats` returns `depth` and `pending`, with no active count; the work object serves `latest_heartbeat_at` and `acknowledged_at`. | agentd `crates/resources/work/src/types.rs` | VERIFIED |
| B2 | agentd queues a stopped item again when its session makes a new worker call. | agentd `loop_steps/held.rs`, `work_items/stop.rs` | VERIFIED |
| B3 | An environment or runtime key may post events to the sessions it reaches; the runtime key may read an environment with an item on the runtime queue. | agentd runtime-parity W-16, W-22; `crates/api/src/edge/reach.rs` | VERIFIED |
| B4 | agentd's scripted model (`conformance/mock_model.py`) is meant for agentd-worker's CI. | agentd `conformance/README.md` | VERIFIED |
| B5 | agentd's poll takes `reclaim_older_than_ms` (5000 when absent, as on live) and `block_ms` 1 to 999. | agentd `crates/resources/work/src/routes.rs`, `lifecycle.rs` | VERIFIED |
| B6 | agentd answers a missing session with 404 `session not found`. | agentd `crates/resources/sessions/src/service.rs:116`, `crates/api/src/edge/scoped.rs:102`, `crates/api/src/session_grants.rs:37` | VERIFIED |
| B7 | agentd's work item id is the session id, one row per session upserted on `(workspace_id, session_id)` and kept; its list has no state filter. | agentd `crates/resources/work/src/service.rs`, `store-postgres/.../work_items/row.rs` | VERIFIED |
| B8 | agentd's placeholder is `agentd-ph-` with a MAC over its fields under a key ring. | agentd `hash.rs` | VERIFIED |
| C1 | iron-proxy intercepts TLS with its own CA, denies metadata and loopback after resolution, reads `aws_sm` and `aws_ssm`, matches fixed `proxy_value` strings per host, and has no caller identity. | github.com/ironsh/iron-proxy README; docs.iron.sh | VERIFIED |
| C2 | No published proxy verifies runtime-minted placeholders against their caller; Anthropic's vaults page suggests "your own proxy that replaces placeholder values" for self-hosted sandboxes. | Survey of 14 tools, 2026-11-22; platform.claude.com, managed-agents/vaults | VERIFIED |
| C3 | "Each Fargate task has its own isolation boundary and does not share the underlying kernel" with another task. | docs.aws.amazon.com, ECS developer guide, AWS_Fargate.html | VERIFIED |
| C4 | On EKS Fargate: "You can't mount Amazon EBS volumes to Fargate Pods"; "You can't use Alternate CNI plugins" (so no Cilium); DaemonSets and privileged containers are not supported. | docs.aws.amazon.com, EKS user guide, fargate.html | VERIFIED |
| C5 | `RunTask` overrides set container command, environment, CPU, memory, roles and ephemeral storage, never EFS volumes; volumes come from the task definition. | ECS API reference, `RunTask`, `TaskOverride` | VERIFIED |
| C6 | `RunTask`'s `startedBy` takes up to 128 letters, digits, `-`, `/` and `_`, and `ListTasks` filters on it; EFS `CreateAccessPoint` takes a `ClientToken` of up to 64 characters for idempotent creation. | ECS API reference, `RunTask`; EFS API reference, `CreateAccessPoint` | VERIFIED |
| C7 | Go 1.27.4 is the current release; 1.27 adds generic methods, `encoding/json/v2`, the goroutine-leak profile; 1.25 and 1.26 added container-aware `GOMAXPROCS`, `sync.WaitGroup.Go`, `testing/synctest` and `errors.AsType`. | go.dev/VERSION, go.dev/doc/go1.27 | VERIFIED (1.27); 1.25 and 1.26 to re-read in PR 4 |
| C8 | iron-proxy reads `Proxy-Authorization` on `CONNECT`, and its `aws_sm` source can read without a cache. | iron-proxy source | TO VERIFY in PR 3; if it caches, PR 3 adds a no-cache option |
