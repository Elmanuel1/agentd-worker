# agentd-worker

Sandboxes for [agentd](https://github.com/Elmanuel1/agentd)'s sessions: each session's tool calls
run in a sandbox of its own, which holds the session's work token and never an environment key.

- On Kubernetes, Anthropic's `cma-sandboxes` chart and dispatcher run unchanged against agentd.
- Elsewhere, `agentd-worker scale` claims each work item as the dispatcher does and launches a
  sandbox for it through a launcher adapter: Docker, the default, or ECS Fargate.
- A reaper answers the tool calls of a worker that died without its own shutdown.

## Quickstart: Docker

```sh
docker build -f image/controller/Dockerfile -t agentd-worker .
docker build -t agentd-sandbox image/sandbox
docker network create agentd-sandboxes
```

Run the controller on the host with the settings in [docs/configuration.md](docs/configuration.md):
agentd's URL and the environment's id and key, `AGENTD_DOCKER_NETWORK=agentd-sandboxes`, both images,
and a handoff certificate whose name sandboxes reach on that network. Sandboxes run under `runsc`
(gVisor) by default; the controller refuses to start when the engine lacks it.

```sh
agentd-worker scale
```

## Quickstart: EKS

Install Anthropic's chart with `deploy/eks/values.yaml`, the egress policies, the Karpenter
NodePool and the reaper. See [docs/eks.md](docs/eks.md).

## Quickstart: ECS Fargate

```sh
cd deploy/fargate && terraform init && terraform apply
```

See [docs/fargate.md](docs/fargate.md).

## Quickstart: self-hosted Docker with Anthropic's spawn script

A self-hosted environment can also run Anthropic's own per-session sandboxes on Docker, with no
controller: `ant beta:worker poll --on-work spawn.sh`, with the spawn script from Anthropic's
self-hosted sandboxes documentation and `ANTHROPIC_BASE_URL` pointed at agentd. Add
`agentd-worker reap` for the same environment to answer a killed sandbox's tool calls.

## More

- [Configuration](docs/configuration.md): every setting.
- [Metrics](docs/metrics.md): each signal and its threshold.
- [Adding a launcher adapter](docs/adapters.md).
- The design: [.specs/worker](.specs/worker).
