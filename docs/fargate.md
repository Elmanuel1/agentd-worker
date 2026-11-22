# ECS Fargate

Off Kubernetes, `agentd-worker scale` claims each item as Anthropic's dispatcher does and runs a
Fargate task for it. `deploy/fargate` is the Terraform for one environment.

1. **Network.** A VPC with private subnets, interface endpoints for ECR (`ecr.api`, `ecr.dkr`) and
   CloudWatch Logs, and an S3 gateway endpoint. Sandboxes have no route to the internet: their
   security group allows outbound only to the proxy, agentd, the controller's handoff port, EFS and
   those endpoints. S3's prefix list covers every bucket in the region, so the gateway endpoint's
   policy must allow only ECR's layer bucket, `arn:aws:s3:::prod-<region>-starport-layer-bucket/*`;
   a Terraform check fails the plan when it allows more.
2. **Images.** Push agentd-worker's image (`image/controller`) and the sandbox image
   (`image/sandbox`, built with the proxy for our cloud) to ECR, and pass both by digest.
3. **Key.** Store the environment key, or agentd's runtime key with `environment_id = "runtime"`,
   in Secrets Manager and pass its ARN.
4. **Apply.** `terraform apply` with the variables in `variables.tf`. It makes the cluster, the
   EFS file system, the roles, the security groups, the handoff certificate and a private DNS name
   for it, and the controller service. Keep the state encrypted: it holds the handoff key.

Each session gets an EFS access point at `/sessions/<session id>`, created with the session id as
its client token, and a task-definition family `agentd-<session id>` that mounts it. The task runs
fetch, then the worker once fetch succeeds, and a sidecar, on ARM64, with a task role that holds no
permission. When agentd deletes the session, the controller deletes its access point, its
directory and its family.
