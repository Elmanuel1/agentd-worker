# EKS

On Kubernetes, sandboxes run under Anthropic's `cma-sandboxes` chart and its dispatcher, unchanged,
pointed at agentd. agentd-worker adds the sandbox image, the egress rules, the nodes and the reaper.

1. **Cluster.** EKS on EC2 with Cilium as the CNI and Karpenter. Apply `deploy/eks/nodeclass.yaml`,
   Graviton nodes with gVisor's `runsc`, and one of the two node pools, each labelled and tainted as
   the chart's `gvisor` RuntimeClass expects. EKS on Fargate cannot run Cilium or mount the chart's
   EBS volumes.
   - `nodepool-on-demand.yaml`: on-demand nodes, removed only once empty. Karpenter never stops a
     running sandbox, and the pool still scales to zero.
   - `nodepool-spot.yaml`: spot nodes first, consolidated when under-used. It costs less, but a spot
     reclaim (two minutes' notice), a consolidation or a drift kills the sandboxes on that node, and
     the reaper answers their open tool calls with an error. Its budget limits consolidation and
     drift to one node in ten at a time; a spot reclaim ignores it.

   Both pools are named `agentd-sandboxes`, so applying the other one switches the cluster over.
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

**Known gap: DNS.** Sandboxes reach the cluster's DNS, which resolves any name, and a lookup can
carry data in the name it asks for. Cilium's deny rules match addresses, not names, so they leave
DNS open. Closing it takes a Cilium DNS policy that answers only for agentd's and the proxy's names.
