# Decisions: agentd-worker

`agentd-worker` runs agentd's sandboxes for our `cloud` and documents running them for self-hosted environments.
On Kubernetes it uses Anthropic's `cma-sandboxes` chart unchanged. Elsewhere its own controller claims each work
item and launches a sandbox for it through a launcher port, with adapters for Docker and ECS Fargate. A shared
egress proxy swaps `environment_variable` credentials for our `cloud`. agentd's side of the contract is in agentd's
runtime-parity spec, section 11.

## Platforms

| # | Decision | Source |
|---|---|---|
| D1 | The code is Go 1.27, the current release (1.27.4). | User, 2026-11-22 ("Language is not rust. It's go. Because it's infra"); go.dev release history |
| D2 | On Kubernetes, sandboxes run under Anthropic's `cma-sandboxes` chart and `cma-dispatcher`, unchanged, pointed at agentd with `ANTHROPIC_BASE_URL`: a self-hosted environment with its environment key, our `cloud` with agentd's runtime key and `ANTHROPIC_ENVIRONMENT_ID=runtime`. The dispatcher calls only `work poll`, `ack`, `stats` and `sessions get`, which agentd serves. Each session keeps its 10 GiB `/workspace` volume until the session ends; its pod is deleted when the worker goes idle, and the next message starts a pod on the same volume. | User, 2026-11-22 ("We can reuse theirs for our cloud"); anthropic-cli `sandboxes/` (Apache 2.0) |
| D3 | Our `cloud` on Kubernetes is EKS on EC2 nodes scaled by Karpenter, so it scales to zero nodes when idle. The cluster runs Cilium as its CNI and the Karpenter NodePool gives nodes gVisor's `runsc`. EKS on Fargate is not used: Fargate pods cannot mount the chart's EBS volumes and cannot run Cilium (dependencies C4). | User, 2026-11-22 ("Both") |
| D4 | Off Kubernetes, `agentd-worker scale` claims work as `cma-dispatcher` does. It holds the environment or runtime key, polls with `reclaim_older_than_ms` at twice `handoff_wait` (default 120 s), launches a sandbox per item through the launcher port (D17), and keeps the item's secret in memory only. The sandbox's `fetch` container collects the item from the controller's `GET /work-item` with a one-time nonce given at launch; the controller answers only when the nonce matches and the request comes from that sandbox's own address while it runs, and acks once the response is written. The secret never enters the backend's API. The controller keeps no state of its own: after a restart it reads its sandboxes back from the backend, and it stops a sandbox whose nonce it no longer holds, so the item's next delivery launches afresh. | User, 2026-11-22 (the Fargate flow, "Can it be behind a port"); `cma-dispatcher` README and `handoff.go` |
| D5 | A work item's id is its session's id, on agentd as on live, so every adapter keys its sandboxes by session id. On Fargate each session's `/workspace` is a directory on EFS reached through one access point per session, created with the session id as its idempotency token and tagged with it, and mounted by a task-definition family named after the session; each task is started with `startedBy` set to the session id. | dependencies A9, C5, C6; brief, 2026-11-22 (EFS per session acceptable) |
| D6 | Self-hosted users on Docker use Anthropic's published spawn script (`ant beta:worker poll --on-work spawn.sh`), unchanged; agentd-worker documents it. | Anthropic's per-session sandboxes page |
| D20 | Memory stores stay on agentd's memory API, reached from the sandbox with the work token, which agentd refuses (403) on a write to a store the session attaches `read_only`. agentd-worker keeps no copy. | Brief, 2026-11-22 |

## The controller and its ports

| # | Decision | Source |
|---|---|---|
| D17 | Every part that reaches outside its own package does so through a port: an interface declared in the package that uses it, holding only the calls that package needs, with one adapter per backend. A new backend, control plane, credential format or value store is a new adapter; the code that uses the port does not change. The ports are listed below. | User, 2026-11-22 ("Can it be behind a port so we can extend and support launching other sandboxes"; "make all part extensible") |
| D18 | Docker is the launcher's default adapter: each item runs in containers on the local Docker engine, so the controller is tested locally and in CI against real containers. On this Mac it runs only once the one-Docker-session rule is lifted. | User, 2026-11-22 ("Yes. For local testing of the controller."); feedback: an open-source default behind each port |
| D19 | The code follows the conventions in requirements section 10 (CV): one Go type per state of each flow, as agentd's Rust does with typed ids, small interfaces at the user, constructor injection, typed ids and self-redacting secrets. | User, 2026-11-22 ("Follow the rust typed state feature"; "Write these down and review all your implementation with it") |

### Ports

| Port | Used by | Calls | Adapters |
|---|---|---|---|
| `Launcher` | controller | `Launch(session, nonce)`, `List()` (each session the backend holds, and whether its sandbox runs), `Stop(session)`, `Release(session)`; each safe to repeat | Docker (default), Fargate |
| `Locator` | handoff server | `Address(session)`: the running sandbox's address | Docker, Fargate |
| `Queue` | controller | `Poll`, `Ack`, `Stats` | agentd over HTTP |
| `Items` | reaper, sidecar | `List`, `Stop` | agentd over HTTP |
| `Events` | reaper | `List(session)`, `Send(session, results)` | agentd over HTTP |
| `Sessions` | controller, proxy | `Get(session)` | agentd over HTTP |
| `Placeholders` | proxy | `Verify(placeholder)`: the credential it names and its session | agentd's signed format |
| `Callers` | proxy | `Check(session, work token)` | agentd `GET /v1/sessions/{id}` |
| `Policies` | proxy | `Egress(environment)`: `limited` with its hosts, or `unrestricted` | agentd `GET /v1/environments/{id}` with the runtime key |
| Values | proxy | read a credential's value | iron-proxy's own sources: `aws_sm` for our `cloud`, `aws_ssm` |
| Metrics | every binary | the OpenTelemetry metric API | any OTLP backend, chosen by `OTEL_*` variables |

The agentd adapter speaks the Managed Agents API, which agentd matches, so it also runs against live.

## Credentials and egress

| # | Decision | Source |
|---|---|---|
| D7 | Our `cloud` swaps `environment_variable` credentials in one shared egress proxy outside every sandbox: iron-proxy, extended with the `Placeholders`, `Callers` and `Policies` ports. A sandbox authenticates with `Proxy-Authorization: Basic` of `<session id>:<work token>`, which clients send on `CONNECT` and on plain-HTTP requests; the proxy checks it once per tunnel, binds the identity to the tunnel and strips the header before forwarding. iron-proxy keeps its TLS interception, address deny list, value sources, per-host rules and audit log. The extension goes upstream as a pull request; we run our fork until it merges. Self-hosted sessions get no swap, as on live. | User, 2026-11-22 ("Yes", extend iron-proxy); no existing proxy verifies runtime-minted placeholders for their caller (dependencies C2) |
| D8 | The proxy is the only holder of Secrets Manager access: `GetSecretValue` on secrets tagged `credential_type=environment_variable` under `AGENTD_SECRETS_PREFIX`, and `kms:Decrypt` through Secrets Manager (agentd EG-9), reached through a Secrets Manager VPC endpoint. It reads a value on each swap (agentd EG-6) and swaps only into requests sent over TLS. | agentd D25, EG-6, EG-9 |
| D9 | `cloud` sandboxes reach only the proxy, agentd and their controller. On EKS a NetworkPolicy lets sandbox pods reach the proxy, which the chart's own policies do not allow, and a Cilium policy denies every other egress, because a NetworkPolicy cannot narrow the chart's `publicInternet` mode; on GKE the chart's `fqdnGke` mode lists only those hosts; on Fargate the task's security group allows outbound only to the proxy, agentd, the controller's handoff port and EFS. | Chart `values.yaml` (`egress.mode`); Kubernetes NetworkPolicy semantics |
| D10 | Our sandbox image is built `FROM` Anthropic's `cma-sandbox` image, as the chart documents for building your own. It adds the proxy's CA certificate and installs a wrapper at `cma-sandbox-worker`'s path, because the chart fixes the worker's command and environment. The wrapper exports the work secret's `environment_variables`, which `ant` does not set (agentd EG-1), sets the proxy variables with the session's credentials and `NO_PROXY` for agentd, stages a `cloud` session's mounted files with the work token as live's `cloud` mounts them (agentd FO-1), and runs Anthropic's script. The proxy address is built into the image. | anthropic-cli `sandboxes/image/worker.sh`, `Dockerfile`, `values.schema.json` |
| D11 | The environment key, or the runtime key, is held only by the dispatcher, the controller, the reaper and, for the runtime key, the proxy (D8). No sandbox holds either, and a sandbox task's role holds no permission. | User, 2026-11-22 ("Got it", on the four conditions); agentd D59 |

## Failure and recovery

| # | Decision | Source |
|---|---|---|
| D12 | A stop does not re-queue work, as on live. | User, 2026-11-22 ("Stop is not requeing. Live also does it") |
| D13 | A worker that dies without its own shutdown is answered as `ant` answers on SIGTERM. The reaper stops the item first, then posts an error `user.tool_result` for each tool call the session still owes, so the agent sees the error and its next tool call queues the stopped item again. It does this for a stale item (no heartbeat for the 300 s lease plus 60 s) and for an item stopped since its last pass with calls still owed, every 60 s. In a Fargate or Docker sandbox the sidecar stops the item with the work token when the worker container exits. | User, 2026-11-22 ("Yes", design from the SIGTERM behaviour); Anthropic's self-hosted operations page; agentd `held.rs` |

## Isolation and cost

| # | Decision | Source |
|---|---|---|
| D14 | Strong isolation is the default: the chart's `gvisor` runtime class on EKS; on Fargate each task's own isolation boundary, with no kernel shared between tasks (dependencies C3); on Docker the engine's `runsc` runtime, which CI replaces with `runc`. | User, 2026-11-22 ("Sandbox isolation should be the default") |
| D15 | Sandboxes scale to zero, on ARM where the image allows, with no warm pool beyond the chart's setting until start-up latency is measured to hurt. | Brief |
| D16 | The app exports its metrics over OTLP and defines no alarms; the docs name each signal and its threshold. | Standing answer, 2026-11-11 |

## Rejected

| Option | Why |
|---|---|
| Our own Kubernetes controller and KEDA ScaledJob | Anthropic's chart does it, restart-safe, and matches live (D2). |
| A tar of the workspace kept as a session file | Live keeps a volume per session (D2, D5). |
| The Cloudflare launcher (agentd D38) | EKS and Fargate serve our `cloud` (D2 to D5); Cloudflare can come back as a launcher adapter. |
| An iron-proxy sidecar per pod, or a job that registers each session's placeholders | The sidecar puts credentials inside the pod's sandbox; the job races the first call and cannot stop a copied placeholder. |
| A stop that re-queues | Live does not (D12). |
| Containers that claim their own item, with no dispatcher (the brief's first design) | Live uses a restart-safe dispatcher (D2); a task cannot mount the session's access point once it learns the session (dependencies C5); the claim step would put the environment key in every sandbox task. |
| The work secret in a `RunTask` override | Anyone with `ecs:DescribeTasks` could read it; live hands it over through the dispatcher instead (D4). |
| `/workspace` on S3 through the Mountpoint CSI driver | No rename, in-place edit, append or delete, and agents do all of those (brief). |
| A mutating webhook that wires each sandbox pod to the proxy | Our image sets the proxy and CA (D10), so the chart stays unchanged with no webhook. |
| One placeholder per credential on stock iron-proxy | A copied placeholder would be swapped in any session, which agentd D28 forbids. |
| One iron-proxy per session, or agentd writing each session's placeholder map to Secrets Manager for a sync job | Up to 10^4 extra proxies with Secrets Manager access, per-pod routing the chart lacks, and a race at the first call; the map in Secrets Manager still leaves a shared proxy unable to stop a copied placeholder. |
| A mitmproxy addon | Python and unproven at 10^4 sessions; it is the fallback if the iron-proxy extension is refused upstream and the fork proves too costly. |
