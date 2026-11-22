# Configuration

Every setting is an environment variable. Secrets are environment variables too; how the deployment
fills them (a Kubernetes Secret, an ECS task definition's `secrets`, a vault) is the deployment's concern.

## `agentd-worker scale` (the controller)

| Variable | Default | Meaning |
|---|---|---|
| `ANTHROPIC_BASE_URL` | required | agentd's API, as the controller reaches it |
| `ANTHROPIC_ENVIRONMENT_ID` | required | the environment served, or `runtime` for agentd's runtime queue |
| `ANTHROPIC_ENVIRONMENT_KEY` | required | its environment key, or agentd's runtime key |
| `AGENTD_WORKER_LAUNCHER` | `docker` | `docker` or `fargate` |
| `AGENTD_HANDOFF_ADDR` | `:8443` | where `GET /work-item` listens |
| `AGENTD_HANDOFF_URL` | required | `GET /work-item`'s base URL as sandboxes reach it |
| `AGENTD_HANDOFF_CERT`, `AGENTD_HANDOFF_KEY` | required | its TLS certificate and key, PEM |
| `AGENTD_HANDOFF_CA` | required | the CA, PEM, that sandboxes verify the certificate against |
| `AGENTD_HANDOFF_WAIT_SECONDS` | `120` | how long a sandbox has to collect its item; the queue reclaims an unacked item after twice this. At most 110 on Fargate, whose containers wait at most 120 s for a dependency |
| `AGENTD_MAX_SANDBOXES` | `100` | items handled at once; polling pauses at this number |
| `AGENTD_RELEASE_INTERVAL_SECONDS` | `600` | how often the storage of deleted and archived sessions is released |
| `AGENTD_RETENTION_DAYS` | `0` | also releases a session idle this many days with no running sandbox; `0` keeps it until it is deleted or archived, as Anthropic's dispatcher's `retentionDays` |
| `AGENTD_REAP_INTERVAL_SECONDS` | `60` | the reaper's pass interval |
| `AGENTD_STALE_AFTER_SECONDS` | `360` | an active item with no heartbeat for this long is stale: the 300 s lease plus 60 s |
| `AGENTD_SANDBOX_BASE_URL` | `ANTHROPIC_BASE_URL` | agentd's API as sandboxes reach it |
| `AGENTD_SANDBOX_IMAGE` | required | the sandbox image (`image/sandbox`) |
| `AGENTD_TOOL_IMAGE` | required | agentd-worker's own image, which runs fetch and the sidecar |
| `AGENTD_LOG_LEVEL` | `INFO` | `DEBUG`, `INFO`, `WARN` or `ERROR` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | empty | where metrics go; empty exports none. Every `OTEL_*` variable applies. |

The Docker adapter reads `AGENTD_DOCKER_NETWORK` (required: the network sandboxes join, from which
they reach agentd and the controller), `AGENTD_DOCKER_RUNTIME` (default `runsc`; the controller
refuses to start when the engine lacks it) and the engine's own `DOCKER_HOST` and `DOCKER_*` settings.
Under `runsc` a sandbox cannot reach the engine's embedded DNS, so the adapter runs
`agentd-worker resolver`, a DNS relay, under `runc` on the sandbox network, and mounts a
read-only `resolv.conf` naming it into each sandbox from `AGENTD_DOCKER_STATE_DIR` (default
`/var/lib/agentd-worker`), a directory on the engine's host the controller can write.
Anthropic's worker script refuses to run where the metadata address answers, so the host rejects
it for containers: `iptables -I DOCKER-USER -d 169.254.0.0/16 -j REJECT`.

The controller names itself in each poll with its host name, so `work stats` counts it in
`workers_polling`.

The Fargate adapter reads `AGENTD_ECS_CLUSTER`, `AGENTD_SUBNETS` and `AGENTD_SECURITY_GROUPS`
(comma-separated), `AGENTD_EFS_ID`, `AGENTD_EFS_ROOT` (default `/mnt/efs`, where the controller
mounts the file system's root), `AGENTD_EXECUTION_ROLE_ARN`, `AGENTD_TASK_ROLE_ARN`,
`AGENTD_TASK_CPU` (default `512`), `AGENTD_TASK_MEMORY` (default `1024`), `AGENTD_LOG_GROUP`,
`AWS_REGION` and the AWS SDK's own credential chain. `deploy/fargate` sets them all.

## `agentd-worker reap`

`ANTHROPIC_BASE_URL`, `ANTHROPIC_ENVIRONMENT_ID`, `ANTHROPIC_ENVIRONMENT_KEY`,
`AGENTD_REAP_INTERVAL_SECONDS`, `AGENTD_STALE_AFTER_SECONDS` and the `OTEL_*` variables, as above.

## `agentd-worker resolver`

`AGENTD_RESOLVER_ADDR` (default `:53`) and `AGENTD_RESOLVER_UPSTREAM` (default `127.0.0.11:53`,
the engine's embedded resolver). The Docker adapter starts it; nothing else needs it.

## `agentd-worker fetch` and `agentd-worker sidecar`

The launcher sets these in each sandbox: `AGENTD_HANDOFF_URL`, `AGENTD_HANDOFF_CA`,
`AGENTD_HANDOFF_WAIT` (a Go duration) and `AGENTD_HANDOFF_NONCE` for fetch; `CMA_SHARED_DIR`
(default `/run/cma`) for both.
