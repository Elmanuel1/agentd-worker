# EKS

On Kubernetes, sandboxes run under Anthropic's `cma-sandboxes` chart and its dispatcher, unchanged,
pointed at agentd. agentd-worker adds the sandbox image, the egress rules, the nodes and the reaper.

1. **Cluster.** EKS on EC2 with Cilium as the CNI and Karpenter. Apply `deploy/eks/nodepool.yaml`:
   Graviton nodes with gVisor's `runsc`, labelled and tainted as the chart's `gvisor` RuntimeClass
   expects. EKS on Fargate cannot run Cilium or mount the chart's EBS volumes.
2. **Images.** Build `image/sandbox` with the proxy's address and CA for our cloud
   (`--build-arg AGENTD_PROXY_URL=... --build-arg AGENTD_CLOUD=1 --build-context proxy-ca=<dir>`),
   or without them for a self-hosted environment. Push it to your registry as `agentd-sandbox`, and
   mirror Anthropic's `cma-dispatcher` beside it by digest: the chart pulls both from one registry.
3. **Secret.** In namespace `cma`, create `cma-environment` with `ANTHROPIC_BASE_URL` (agentd),
   `ANTHROPIC_ENVIRONMENT_ID` and `ANTHROPIC_ENVIRONMENT_KEY`. For our cloud the id is `runtime`
   and the key is agentd's runtime key; for a self-hosted environment, its id and environment key.
   Then the dispatcher's TLS Secret and CA ConfigMap, as the chart's README describes.
4. **Chart.** Install it with `deploy/eks/values.yaml`, setting `image.registry`, both digests and
   `egress.cmaServer.cidr` to agentd's address.
5. **Egress.** Apply `deploy/eks/networkpolicy-proxy.yaml`, so sandboxes reach the proxy, and
   `deploy/eks/cilium-egress.yaml` with agentd's CIDR, so they reach nothing else in public.
6. **Reaper.** Apply `deploy/eks/reaper.yaml` with agentd-worker's image. It reads the same Secret,
   and answers the tool calls of a pod that died without its own shutdown.

The dispatcher, the reaper and, for the runtime key, the proxy are the only holders of a key. No
sandbox holds one.
