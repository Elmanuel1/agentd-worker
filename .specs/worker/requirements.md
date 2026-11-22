# Requirements: agentd-worker

**Format:** EARS. Each requirement uses one pattern:

| Pattern | Template |
|---|---|
| Ubiquitous | The `<system>` shall `<response>`. |
| Event-driven | When `<trigger>`, the `<system>` shall `<response>`. |
| State-driven | While `<state>`, the `<system>` shall `<response>`. |
| Unwanted behaviour | If `<condition>`, then the `<system>` shall `<response>`. |
| Optional feature | Where `<feature>`, the `<system>` shall `<response>`. |

**Systems:** *the proxy* (iron-proxy with our extension), *the image* (our sandbox image and its wrapper), *the
controller* (`agentd-worker scale`), *the fetch container* (`agentd-worker fetch`), *the reaper* (`agentd-worker
reap`, and the same pass inside the controller), *the sidecar* (`agentd-worker sidecar`), *the Docker adapter*, *the
Fargate adapter*, *the deployment* (our EKS values and manifests), *the code*, *the docs*.

**Terms:**
- **Sandbox:** the pod, task or containers that serve one session's run.
- **Work item, work secret, work token, environment key, runtime key, placeholder:** as in agentd's runtime-parity
  spec. A work item's id is its session's id (D5).
- **Handoff:** giving a launched sandbox its work item through `GET /work-item` (D4).
- **Stale item:** an `active` or `starting` item whose `latest_heartbeat_at` (or `acknowledged_at`, before any
  heartbeat) is older than `stale_after_seconds`, default 360.
- **Owed call:** a tool call in the session's events with no `user.tool_result` after it.
- **Error result:** a `user.tool_result` with `is_error: true` for an owed call, as `ant` posts on SIGTERM.

**Build order:** proxy (PX) → image (IM) → controller (SC) → fetch (FE) → reaper (RP) → sidecar (SD) → Docker (DK) →
Fargate (FG) → EKS (EK) → docs (DOC). The conventions (CV) hold for every Go package.

Decisions: [decisions.md](decisions.md). Evidence: [dependencies.md](dependencies.md). Record:
[ADR 0001](adr/0001-reuse-anthropic-sandboxes.md).

## 1. The proxy

| ID | Requirement |
|---|---|
| PX-1 | When a `CONNECT` or plain-HTTP request carries `Proxy-Authorization: Basic` of `<session id>:<work token>`, the proxy shall check the pair through `Callers`, cache the result per token digest for at most 60 s (agentd EG-2), bind the identity to the tunnel, and strip the header before forwarding. |
| PX-2 | If a request has no `Proxy-Authorization`, or the check fails, then the proxy shall refuse it with 407. |
| PX-3 | When a request sent over TLS carries an `agentd-ph-` placeholder that `Placeholders` verifies under the key ring `AGENTD_PLACEHOLDER_KEYS`, whose session is the tunnel's, to a host and location the credential allows, the proxy shall replace it with the credential's value (agentd EG-3). |
| PX-4 | If a placeholder fails verification, its session is not the tunnel's, its host or location is not allowed, or the request is not over TLS, then the proxy shall send the request with the placeholder unchanged (agentd EG-4, EG-5). |
| PX-5 | If the proxy cannot read a value it needs, then it shall refuse the request with 502 (agentd EG-7). |
| PX-6 | The proxy shall take each credential's allowed hosts and injection location from the credential's sealed secret, never from its own config. |
| PX-7 | When the proxy swaps a placeholder, it shall read the value from its source at that moment, with no cache (agentd EG-6). |
| PX-8 | While the tunnel's environment has `limited` networking, the proxy shall allow only its `allowed_hosts`, read through `Policies` and cached for 60 s. |
| PX-9 | While the tunnel's environment has `unrestricted` networking, the proxy shall allow every public host. |
| PX-10 | The proxy shall refuse every connection whose resolved address is loopback, link-local (169.254.0.0/16, fe80::/10, which hold the metadata endpoints), private (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 100.64.0.0/10, fc00::/7) or unspecified, after DNS resolution. |
| PX-11 | The proxy shall connect upstream with TLS 1.2 or later, verify the certificate against the system roots and check the host name. |
| PX-12 | The proxy shall log a credential's id and a fingerprint at `INFO` for each swap, never its value (agentd EG-8). |
| PX-13 | The proxy shall export `proxy_swaps`, `proxy_refusals` and `proxy_read_failures` over OTLP. |
| PX-14 | The deployment shall run at least two proxy replicas behind one address. |
| PX-15 | The proxy's CA private key shall be readable only by the proxy; sandboxes hold only its certificate. |

## 2. The image

| ID | Requirement |
|---|---|
| IM-1 | The image shall be built `FROM` Anthropic's `cma-sandbox` image pinned by digest, adding only the proxy's CA certificate and the wrapper. |
| IM-2 | The image shall install the wrapper at `cma-sandbox-worker`'s path and keep Anthropic's script under another name. |
| IM-3 | When the wrapper starts, it shall wait for the work item in `/run/cma`, export each entry of the work secret's `environment_variables` (agentd EG-1), and run Anthropic's script. |
| IM-4 | Where the image is built with a proxy address, the wrapper shall set `HTTPS_PROXY`, `HTTP_PROXY`, `https_proxy` and `http_proxy` to it with Basic credentials of the session id and work token, and `NO_PROXY` and `no_proxy` to agentd's host. |
| IM-5 | Where the session's environment is our `cloud` and the session has file resources, the wrapper shall download each to its mount path with the work token before running the script (agentd FO-1). |

## 3. The controller

| ID | Requirement |
|---|---|
| SC-1 | The controller shall poll with `block_ms` 999 and `reclaim_older_than_ms` at twice `handoff_wait` (default 120 s). |
| SC-2 | When the controller gets an item whose session has no sandbox, it shall make a nonce, call `Launch(session, nonce)` and hold the item's secret in memory only. |
| SC-3 | When a request to `GET /work-item` carries a nonce the controller holds and comes from the address `Locator` reports for that session, the controller shall return `session_id`, `work_id`, `environment_id`, `base_url` and `secret`, and ack the item once the response is written. |
| SC-4 | If a request to `GET /work-item` carries an unknown nonce, or comes from another address, or its sandbox is not running, then the controller shall refuse it with 403 and log it at `INFO`. |
| SC-5 | If a sandbox has not collected its item within `handoff_wait`, then the controller shall drop the item unacked and call `Stop(session)`. |
| SC-6 | When an item arrives for a session whose sandbox runs and the controller holds no nonce for it, the controller shall call `Stop(session)` and leave the item unacked. |
| SC-7 | When an item arrives again for a session whose sandbox holds its nonce and has not collected, the controller shall keep waiting and launch nothing. |
| SC-8 | The controller shall handle each item in its own goroutine, at most `max_sandboxes` (default 100) at once, and stop polling while at that number. |
| SC-9 | When `Sessions.Get` returns 404 for a session that `List` reports, the controller shall call `Release(session)`, checking every `release_interval_seconds` (default 600). |
| SC-10 | The controller shall serve `GET /work-item` over TLS 1.2 or later with a certificate the fetch container verifies against the CA it is given. |
| SC-11 | When the controller receives SIGTERM, it shall stop polling, finish the acks of responses already written, and exit; an item not yet collected is left for agentd to hand out again. |
| SC-12 | The controller shall export `queue_depth`, `launches`, `collections`, `handoff_timeouts` and `releases` over OTLP. |

## 4. The fetch container

| ID | Requirement |
|---|---|
| FE-1 | When the fetch container starts, it shall call `GET /work-item` with its nonce every 2 s until it gets the item, write the five fields to `/run/cma` as files owned by uid 1001 with mode 0600, and exit 0. |
| FE-2 | If `handoff_wait` passes with no item, or the controller refuses it, then the fetch container shall exit non-zero, so the worker container never starts. |

## 5. The reaper

| ID | Requirement |
|---|---|
| RP-1 | When the reaper finds a stale item, it shall stop the item, then post an error result for each owed call. |
| RP-2 | When the reaper finds an item stopped since its last pass whose session has owed calls, it shall post an error result for each. |
| RP-3 | The reaper shall run every `reap_interval_seconds` (default 60), reading the environment's items page by page. |
| RP-4 | When the reaper answers an item, it shall log the item id, its last heartbeat and the calls answered at `INFO`. |
| RP-5 | If a call fails, then the reaper shall log it at `WARN` with the item id and continue, retrying on its next pass. |
| RP-6 | The reaper shall export `reaped` and `error_results` over OTLP. |

## 6. The sidecar

| ID | Requirement |
|---|---|
| SD-1 | When the worker container exits, the sidecar shall stop the item with the work token, treating 401 and 409 as already stopped. |

## 7. The Docker adapter

| ID | Requirement |
|---|---|
| DK-1 | When `Launch` is called, the Docker adapter shall create the session's volume if it has none, then run the fetch, worker and sidecar containers labelled with the session id on its network, the worker started after fetch exits 0, `/workspace` on the session's volume. |
| DK-2 | The Docker adapter shall run sandbox containers under the runtime `docker_runtime` names, default `runsc`, and refuse to launch when the engine lacks it. |
| DK-4 | The Docker adapter shall give sandbox containers no Docker socket, no host path, no host or shared process namespace and no added capabilities. |
| DK-3 | The Docker adapter shall answer `List` and `Address` from the engine's containers and volumes by their session label, `Stop` by removing the session's containers, and `Release` by also removing its volume. |

## 8. The Fargate adapter

| ID | Requirement |
|---|---|
| FG-1 | When `Launch` is called, the Fargate adapter shall create the session's access point with the session id as its client token and tag, register the session's task-definition family if it has none, and call `RunTask` with `startedBy` set to the session id and the nonce in the fetch container's environment. |
| FG-2 | The Fargate adapter shall answer `List` from the cluster's tasks and the access points tagged with a session id, and `Address` from the task's private IP while its status is `RUNNING`. |
| FG-3 | When `Stop` is called, the Fargate adapter shall stop the session's tasks. |
| FG-4 | When `Release` is called, the Fargate adapter shall stop the session's tasks and delete its access point, its directory, through the file system's root, and its task-definition family. |
| FG-5 | The task definition shall run the fetch container, the worker container after fetch succeeds, and a non-essential sidecar, on ARM64, with a task role that holds no permission. |
| FG-6 | The task's security group shall allow outbound only to the proxy, agentd, the controller's handoff port and EFS. |
| FG-7 | The controller's role shall hold only `ecs:RunTask`, `StopTask`, `ListTasks`, `DescribeTasks`, `RegisterTaskDefinition`, `DeregisterTaskDefinition`, `DeleteTaskDefinitions`, `iam:PassRole` on the task's execution role, and EFS access-point create, describe, tag and delete, with root access to the file system. |
| FG-8 | The task definition shall leave `pidMode` unset, so no container reads another's processes, and give no container a `secrets` entry. |

## 9. EKS

| ID | Requirement |
|---|---|
| EK-1 | The deployment shall install Anthropic's `cma-sandboxes` chart unchanged with `platform: eks`, `ANTHROPIC_BASE_URL` set to agentd, our image and `runtimeClass.name: gvisor`. |
| EK-2 | Where the environment is our runtime queue, the deployment shall give the dispatcher the runtime key as `ANTHROPIC_ENVIRONMENT_KEY` and `ANTHROPIC_ENVIRONMENT_ID=runtime`. |
| EK-3 | The deployment shall apply a NetworkPolicy that lets sandbox pods reach the proxy, and a Cilium policy that denies every other egress from them except to agentd and the cluster's DNS. |
| EK-4 | The deployment shall give Karpenter a NodePool whose nodes run `runsc`, on a cluster whose CNI is Cilium. |
| EK-5 | The deployment shall run the reaper as one Deployment per environment, holding that environment's key. |

## 10. The code

| ID | Requirement |
|---|---|
| CV-1 | The code shall build with Go 1.27, pinned by `go.mod`'s `go` and `toolchain` lines, with its tools pinned by `tool` lines. |
| CV-2 | The code shall declare each port as an interface in the package that uses it, holding only the calls that package makes; adapters shall return concrete types. |
| CV-3 | The code shall give each step of a flow its own type with unexported fields, each step's method only on the state it follows: the handoff `Polled` → `Launched` → `Collected` → `Acked`, the reaper `Stale` → `Stopped` → `Answered`. Each flow runs in one function, so an earlier state's value goes out of scope. |
| CV-4 | The code shall type ids and secrets: `SessionID`, `EnvironmentID`, `Nonce`, and `Secret` and `WorkToken`, whose `String`, `GoString` and slog `LogValue` return `[redacted]`. |
| CV-5 | The code shall hold no package-level variables but errors and constants; `main` shall build the adapters and pass them in through constructors. |
| CV-6 | The code shall pass a `context.Context` first to every call that waits, derived in `main` from `signal.NotifyContext` on SIGTERM and SIGINT. |
| CV-7 | The code shall give every goroutine an owner that waits for it, through `errgroup` or `sync.WaitGroup.Go`. |
| CV-8 | The code shall return errors, wrapped with `%w` and tested with `errors.Is` or `errors.AsType`, and panic nowhere but in `main`'s start-up. |
| CV-9 | The code shall log through `log/slog` as JSON, at the level that says who must act. |
| CV-10 | The code shall use names that do not repeat their package, no `any` in exported signatures, no `else` after a `return`, and files of at most 200 lines. |
| CV-11 | The code shall comment only a reason the code cannot show. |
| CV-12 | The code shall make nonces with `crypto/rand.Text`. |
| CV-13 | The code shall keep binaries under `cmd/` and every other package under `internal/`. |
| CV-14 | The tests shall be table-driven, run code that waits on time under `testing/synctest`, and run every `Launcher` adapter through the shared suite `launchertest.Run`. |
| CV-15 | CI shall run `gofmt -l`, `go vet`, `staticcheck`, `govulncheck` and `go test -race`, and fail on any finding. |
| CV-16 | The code shall give each package one job: the poll loop, the handoff, the reaper, the fetch, the sidecar, and one package per adapter. |

## 11. The docs

| ID | Requirement |
|---|---|
| DOC-1 | `README.md` shall open with quickstarts for the Docker adapter, EKS, Fargate, and self-hosted Docker with Anthropic's spawn script. |
| DOC-2 | The docs shall list each metric, its meaning and a threshold. |
| DOC-3 | The docs shall show how to add a launcher adapter: implement `Launcher` and `Locator`, then pass `launchertest.Run`. |

## Industry practice

| Pattern | Source | Here |
|---|---|---|
| A dispatcher that gives each session its own sandbox, hands it the item through an attested fetch and acks after collection | Anthropic's `cma-sandboxes` and `cma-dispatcher` | Followed on Kubernetes, unchanged (D2), and by our controller elsewhere (D4). |
| The environment key kept out of the sandbox | Anthropic's self-hosted quickstarts | Followed (D11). |
| Credentials attached at egress, outside the sandbox | Live's vaults ("at egress, not inside the sandbox"); iron-proxy | Followed with one shared proxy (D7). |
| Default-deny egress with metadata blocked | iron-proxy; the chart's NetworkPolicies | Followed (PX-8 to PX-10, D9). |
| Ports and adapters, with a contract suite per port | Hexagonal architecture; Go's "accept interfaces" | Followed (D17, CV-2, CV-14). |
| Typestate | Rust typestate pattern; agentd's typed ids | Followed with one Go type per state (CV-3). |

Departures from live: none on Kubernetes. Off Kubernetes, where Anthropic publishes nothing, the controller follows
the dispatcher's contract: one sandbox per session, the key kept out of it, the item handed over by attested fetch,
a volume kept until the session ends.

## Out of scope

- A warm pool beyond the chart's setting (D15).
- EKS on Fargate (D3).
- Launcher adapters beyond Docker and Fargate; DOC-3 shows how to add one.

## Plan of PRs

| PR | Repo | What | Requirements |
|---|---|---|---|
| 1 | agentd | A missing session answers 404 `Session not found: <id>`, as live, so `cma-dispatcher` releases its volume | agentd AG-1 (spec follows in PR 5) |
| 2 | agentd-worker | This spec | none (spec) |
| 3 | iron-proxy fork | The `Placeholders`, `Callers` and `Policies` ports with agentd's adapters, tunnel authentication and the deny ranges, opened upstream as a pull request | PX-1 to PX-15 |
| 4 | agentd-worker | Go module, both images, Docker and Fargate adapters, EKS deployment, CI, docs | IM, SC, FE, RP, SD, DK, FG, EK, CV, DOC |
| 5 | agentd | Section 11 of the runtime-parity spec follows the build: CF-1 to CF-12 and FL-1 to FL-3 point to this spec; D24, D25, D33, D38 and D39 revisited; CF-8 answered by the reaper and sidecar; mounted-file staging (CF-3, FO-1, FO-2) by the wrapper; AG-1 recorded | none (spec) |

PR 1 stands alone and can merge first. PR 4 needs PR 1 for EKS release and PR 3 for the `cloud` e2e.

### PR 1: files

`crates/resources/sessions/src/service.rs`, `crates/resources/sessions/src/types.rs`,
`crates/resources/sessions/src/store.rs`, `crates/api/src/edge/scoped.rs`, `crates/api/src/session_grants.rs`, and
the tests that assert the text (`crates/api/tests/edge.rs`). Each message becomes `Session not found: <id>`.

### PR 4: files

| Path | What |
|---|---|
| `cmd/agentd-worker/main.go` | subcommands `scale`, `fetch`, `reap`, `sidecar`; builds the adapters |
| `internal/ids/` | `SessionID`, `EnvironmentID`, `Nonce`, `Secret`, `WorkToken` (CV-4) |
| `internal/dispatch/` | the poll loop, the handoff states, `GET /work-item` (SC) |
| `internal/reap/` | the reaper's states and pass (RP) |
| `internal/fetch/`, `internal/sidecar/` | FE, SD |
| `internal/agentd/` | the `Queue`, `Items`, `Events` and `Sessions` adapter, on `anthropic-sdk-go` v2 |
| `internal/launcher/docker/`, `internal/launcher/fargate/` | DK, FG |
| `internal/launcher/launchertest/` | the shared adapter suite (CV-14) |
| `internal/telemetry/` | OTLP metric set-up |
| `image/sandbox/`, `image/controller/` | the sandbox image and wrapper (IM); the `agentd-worker` image |
| `deploy/eks/` | chart values, NetworkPolicy, Cilium policy, NodePool, reaper Deployment |
| `deploy/fargate/` | Terraform: cluster, controller service, task definition template, IAM, security groups, EFS |
| `.github/workflows/ci.yml`, `e2e/` | CV-15, and two e2e jobs on CI runners |
| `README.md`, `docs/` | DOC-1 to DOC-3 |

Every file stays under 200 lines.

### Tests

- **PR 1, agentd:** a missing, foreign or deleted session answers 404 with `Session not found: <id>` on `GET
  /v1/sessions/{id}` and on the scoped edge (AG-1).
- **PR 3, proxy, Go unit and integration with LocalStack:** a placeholder from the tunnel's session is swapped for an
  allowed host over TLS (PX-3); one for another session, with a bad signature, to a disallowed host or over plain
  HTTP goes unchanged (PX-4); no or a bad `Proxy-Authorization` gets 407, and the header never reaches upstream
  (PX-1, PX-2); an unreadable secret gets 502 (PX-5); a rotated value is used on the next request (PX-7); a host
  outside a `limited` list, and 169.254.169.254, 10.0.0.1 and ::1 are refused, an unrestricted environment reaches a
  public host (PX-8 to PX-10); an upstream with a bad certificate is refused (PX-11); the log holds the id and
  fingerprint and no value (PX-12); the rules come from the sealed secret (PX-6).
- **PR 4, unit:** the handoff states compile only in order (CV-3); a stray nonce, a wrong address and a stopped
  sandbox get 403 (SC-4); an uncollected item is dropped and its sandbox stopped after `handoff_wait`, under
  `synctest` (SC-5); an item for a running sandbox with no nonce stops it (SC-6); a redelivery while waiting launches
  nothing (SC-7); `max_sandboxes` pauses polling (SC-8); a 404 session is released (SC-9); fetch writes 0600 files
  owned by 1001 and exits non-zero on a timeout (FE-1, FE-2); the reaper stops before answering, answers recently
  stopped items, and carries on past a failed call (RP-1 to RP-5); the sidecar treats 401 and 409 as stopped (SD-1);
  `Secret` prints `[redacted]` through `fmt` and `slog` (CV-4); the wrapper exports the secret's variables and sets the
  proxy only when built with one (IM-3, IM-4).
- **PR 4, adapter suite:** Docker runs `launchertest.Run` in CI (DK-1 to DK-3), and the suite inspects each container
  for a socket, host path, shared PID namespace or added capability (DK-4); Fargate runs it against a fake ECS and EFS
  client (FG-1 to FG-4), and its task definition is checked against the ECS schema, with no `pidMode` and no
  `secrets` (FG-5, FG-8).
- **PR 4, build:** a CI step fails when the sandbox `Dockerfile`'s `FROM` is not pinned by digest or the image adds a
  layer beyond the CA and the wrapper (IM-1, IM-2); the reaper reads every page of a 250-item list (RP-3).
- **PR 4, e2e with the Docker adapter (CI, agentd's CI image, its scripted model):** a session's tool call runs in a
  container launched by the controller; killing the controller mid-handoff and restarting it ends with exactly one
  sandbox (SC-6); SIGKILL of the worker mid-call is answered by the reaper with an error result and the next call
  runs (RP-1); `env` in the sandbox shows the secret's variables and no `sk-ant-oat01-` value (IM-3, D11).
- **PR 4, e2e on kind with Anthropic's chart:** a `cloud` session's `curl` reaches an allowed echo host with the real
  value and a disallowed one with the placeholder (EK-1, PX-3, PX-4); a direct connection from the sandbox to a
  public IP is refused (EK-3); the dispatcher runs with `ANTHROPIC_ENVIRONMENT_ID=runtime` on the runtime queue
  (EK-2); the reaper Deployment answers a killed pod's owed call (EK-5).
- Fargate runs against a real AWS account only with the user's OK. EK-4, FG-6 and FG-7 are checked by reading the
  Terraform and manifests in review.

## SRE

- **SLO:** 95% of queued items picked up within 15 s on EKS with a node free, 60 s on Fargate; both measured in the
  first e2e and AWS runs and set from them.
- **Sizing:** agentd's peak of 10^4 sessions. agentd keeps one work item per session forever and its list has no state
  filter, so a reaper pass reads up to 10^4 items, 100 pages of 100, once a minute per environment. One shared proxy
  is sized by its own load test before launch.
- **Signals:** `queue_depth` above 0 for 60 s with no `collections`; `handoff_timeouts` above 0; `reaped` above 0;
  `proxy_read_failures` above 0.
- **Accepted risks:** node or task loss takes up to `stale_after_seconds` plus a reaper interval (about 7 minutes) to
  answer; the proxy is a shared dependency of every `cloud` session, so it runs at least two replicas (PX-14); one
  controller per environment, so its restart delays new sandboxes by its start-up time, and loses no work (D4).

### Connections

| From | To | Protocol | Credential | Trust |
|---|---|---|---|---|
| Sandbox | Proxy | HTTP `CONNECT`, then TLS to the proxy's interception | Basic `<session id>:<work token>` on `CONNECT` (PX-1) | Proxy CA certificate in the image (IM-1) |
| Proxy | Upstream host | TLS 1.2+ | the swapped credential | system roots, host name checked (PX-11) |
| Proxy | Secrets Manager | TLS through a VPC endpoint | the proxy's role (D8) | AWS SDK defaults |
| Proxy, controller, reaper, sandbox | agentd | TLS 1.2+ | runtime key, environment key, or work token | system roots or the deployment's CA |
| Fetch container | Controller `GET /work-item` | TLS 1.2+ | one-time nonce, from the task's own address (SC-3) | the controller CA given to the task (SC-10) |
| Controller | ECS, EFS | TLS | the controller's role (FG-6) | AWS SDK defaults |

## Audit of this plan

Run 2026-11-22 against agentd `main` at `5c5f486`, anthropic-cli `sandboxes/`, anthropic-sdk-go v2.13.0, the ECS and
EFS API references and the Go 1.27 release notes.

**Inventory.** The plan calls 11 agentd routes: work `poll`, `ack`, `heartbeat`, `stop`, `stats`, `list`; sessions
`get`; events `list` and `send`; environments `get`; files download. All exist on `main`. Round 2's audit read each route;
this pass re-read the work service, its store and the session errors, found the same set, and added three facts:
the work id is the session id (B7), poll takes `reclaim_older_than_ms` (B5), and the 404 text differs from live
(B6).

**Rounds.**
- Round 1, on an earlier design: 21 findings, closed by the redesign around Anthropic's chart.
- Round 2, on the chart design: 15 findings, all fixed here: the secret's variables exported (IM-3); the controller
  claims on Fargate (D4); all five fields written (FE-1); the 404 text (PR 1) and `ANTHROPIC_ENVIRONMENT_ID` (EK-2);
  the proxy allow, Cilium and `runsc` nodes (D3, EK-3, EK-4); the wrapper at the fixed command path with `NO_PROXY`
  (IM-2, IM-4); Basic on `CONNECT` (PX-1); per-swap reads and TLS-only swaps (PX-3, PX-7); stop before answering
  (RP-1, RP-2, SD-1); the proxy among key holders (D11); connection traces, IAM and deny ranges (Connections, FG-7,
  PX-10); PR 5's agentd decisions and file staging (IM-5); EARS splits, tests and ledger rows; the memory note; no
  history wording.
- Round 3, the code conventions (CV) against the plan: 13 findings, all fixed here:
  1. `internal/sync` shadows the standard library: renamed `internal/sidecar` (CV-10).
  2. One wide `internal/api` client: split into `Queue`, `Items`, `Events`, `Sessions` at their users, one
     `internal/agentd` adapter (CV-2).
  3. `Launcher` could not recover a lost nonce or find kept volumes: `List` and `Stop` added (SC-6, SC-9).
  4. Nothing held the handoff or reaper order: one type per state (CV-3), so ack before collection and answering
     before stopping do not compile.
  5. Secrets and tokens as plain strings: `Secret` and `WorkToken` redact themselves (CV-4).
  6. No shared adapter contract: `launchertest.Run` (CV-14, DOC-3).
  7. Interval code had no deterministic test: `testing/synctest` (CV-14).
  8. A `claim` subcommand left from the self-claiming design: subcommands are `scale`, `fetch`, `reap`, `sidecar`.
  9. The proxy extension was one block: `Placeholders`, `Callers`, `Policies` ports (D7, D17).
  10. The controller binary and the sandbox image were one: two images (IM-1).
  11. The handoff's transport was unstated: TLS with a CA given to the task (SC-10).
  12. The Docker adapter had no isolation default: `runsc` (DK-2, D14).
  13. FG-3 joined two triggers: split into FG-3 and FG-4.

- Round 4, capture: every message of the user's in this design and every line of the brief, checked against the
  spec. 8 gaps, all fixed here: the brief's S3 Mountpoint rejection, its memory rule (D20), and that `ant` uploads no
  outputs (A1); the four conditions for keeping the key out of sandboxes, which lacked the Docker and Fargate halves
  (DK-4, FG-8); the refusal to launch without `runsc` (DK-2); one package per job, the S of SOLID (CV-16); and four
  rejected options the conversation settled (a webhook, per-credential placeholders, a proxy per session or a sync
  job, mitmproxy as the fallback). The brief's self-claiming containers, KEDA, the Cloudflare launcher and the
  session-files tar appear only under Rejected, each with its reason.

**Sources.**

| Source | Result |
|---|---|
| Global rules (CLAUDE.md, planning rules) | PASS: one PR per change, no merge or force-push, no em dashes, no history wording. |
| Docker rule | PASS: Docker runs in CI only; on this Mac once lifted (D18). |
| No local Rust builds | PASS: PR 1 is tested in CI. |
| Standing answers | PASS: settings from environment variables; metrics over OTLP with no alarms (D16); log levels (CV-9); live matched without asking (A9 to A11, B5 to B7). |
| Memory | PASS: fewer PRs (one code PR per repo); an open-source default behind each port (D18; Values has none, see below); match Anthropic exactly (D2, D4). |
| Skills: security | PASS: every connection traced with TLS minimum, trust and credential (Connections); deny ranges (PX-10); least-privilege IAM (FG-7). |
| Skills: SRE | PASS: SLO, sizing, signals and risks stated. |
| File size | PASS: every file under 200 lines; agentd's files in PR 1 change one string each. |

**Open.**
- C8, whether iron-proxy's `aws_sm` reads without a cache, is checked first in PR 3.
- The `Values` port has no open-source default: a value lives where agentd's secret store keeps it, and our `cloud`'s
  store is Secrets Manager (agentd secret-store plan).
- The Fargate e2e needs a real AWS account and the user's OK.
