# ECS Fargate

Off Kubernetes, `agentd-worker scale` claims each item as Anthropic's dispatcher does and runs a
Fargate task for it. `deploy/fargate` is the Terraform for one environment.

1. **Network.** A VPC with private subnets and no route to the internet, an S3 gateway endpoint,
   and interface endpoints with private DNS for ECR (`ecr.api`, `ecr.dkr`) and CloudWatch Logs,
   which tasks pull and log through, and for Secrets Manager, ECS and EFS (`elasticfilesystem`),
   which the controller calls. The endpoints' security group must allow TCP 443 from the
   `controller_security_group_id` and `sandbox_security_group_id` outputs. A sandbox's security
   group allows outbound only to the proxy, agentd, the controller's handoff port, EFS and those
   endpoints. S3's prefix list covers every bucket in the region, so the gateway endpoint's policy
   must allow only ECR's layer bucket, `arn:aws:s3:::prod-<region>-starport-layer-bucket/*`; a
   Terraform check fails the plan when it allows more.
2. **Images.** Push agentd-worker's image (`image/controller`) and the sandbox image
   (`image/sandbox`, built with the proxy for our cloud) to ECR, and pass both by digest. Each
   execution role pulls only those repositories.
3. **Key.** Store the environment key, or agentd's runtime key with `environment_id = "runtime"`,
   in Secrets Manager and pass its ARN.
4. **Apply.** `terraform apply` with the variables in `variables.tf`. It makes the cluster, the
   EFS file system, the roles, the security groups, the handoff certificate and a private DNS name
   for it, and the controller service. Keep the state encrypted: it holds the handoff key.

**Known gap: DNS.** A task reaches the VPC's DNS server, which security groups cannot filter, and
a lookup can carry data in the name it asks for. Closing it takes Route 53 Resolver DNS Firewall on
the VPC, allowing only agentd's, the proxy's and the controller's names and the endpoints' service
names: `ecr.api`, `ecr.dkr`, `s3`, `logs`, `secretsmanager`, `ecs` and `elasticfilesystem` under
`<region>.amazonaws.com`, and ECR's layer bucket.

## Quotas

Request these before load, from Service Quotas:

| Quota | Default | Why |
|---|---|---|
| Fargate On-Demand vCPU resource count | 6 | each sandbox takes 0.5 vCPU: 100 sandboxes need 50 |
| ECS task-definition modify actions (L-1E6DEB89) | 1 per second, burst 20 | each new session registers its family once, and Release deregisters it |
| EFS access points per file system | 10,000 | one per session until the session is deleted or archived, or idle past `retention_days` |

The S3 gateway endpoint's route tables must cover the private subnets, or image pulls fail.

## Sessions

Each session gets an EFS access point at `/sessions/<session id>`, created with the session id as
its client token, and a task-definition family `agentd-sandbox-<session id>` that mounts it. The task runs
fetch, then the worker once fetch succeeds, and a sidecar, on ARM64, with a task role that holds no
permission. When the session is deleted or archived, or idle past `retention_days`, the
controller deletes its access point, its directory and its family. The workspace mounts with the
task role through its access point: the file system's policy allows the sandbox's task role only
through an access point. The controller mounts the file system's root through its own role's
grant, to remove a released session's directory.
