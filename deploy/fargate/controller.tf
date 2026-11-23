resource "aws_ecs_cluster" "this" {
  name = var.name
  setting {
    name  = "containerInsights"
    value = "enabled"
  }
}

resource "aws_cloudwatch_log_group" "controller" {
  name              = "/${var.name}/controller"
  retention_in_days = 30
}

resource "aws_cloudwatch_log_group" "sandboxes" {
  name              = "/${var.name}/sandboxes"
  retention_in_days = 30
}

locals {
  controller_env = {
    ANTHROPIC_BASE_URL          = var.agentd_base_url
    ANTHROPIC_ENVIRONMENT_ID    = var.environment_id
    AGENTD_WORKER_LAUNCHER      = "fargate"
    AGENTD_MAX_SANDBOXES        = tostring(var.max_sandboxes)
    AGENTD_HANDOFF_WAIT_SECONDS = "90"
    AGENTD_RETENTION_DAYS       = tostring(var.retention_days)
    AGENTD_ECS_CLUSTER          = aws_ecs_cluster.this.name
    AGENTD_SUBNETS              = join(",", var.private_subnet_ids)
    AGENTD_SECURITY_GROUPS      = aws_security_group.sandbox.id
    AGENTD_EFS_ID               = aws_efs_file_system.workspaces.id
    AGENTD_EFS_ROOT             = "/mnt/efs"
    AGENTD_EXECUTION_ROLE_ARN   = aws_iam_role.sandbox_execution.arn
    AGENTD_TASK_ROLE_ARN        = aws_iam_role.sandbox.arn
    AGENTD_TASK_CPU             = var.sandbox_cpu
    AGENTD_TASK_MEMORY          = var.sandbox_memory
    AGENTD_LOG_GROUP            = aws_cloudwatch_log_group.sandboxes.name
    AWS_REGION                  = local.region
    AGENTD_HANDOFF_URL          = "https://${local.handoff_host}:8443"
    AGENTD_HANDOFF_CA           = tls_self_signed_cert.ca.cert_pem
    AGENTD_HANDOFF_CERT         = tls_locally_signed_cert.handoff.cert_pem
    AGENTD_SANDBOX_IMAGE        = var.sandbox_image
    AGENTD_TOOL_IMAGE           = var.controller_image
    OTEL_EXPORTER_OTLP_ENDPOINT = var.otel_exporter_otlp_endpoint
  }
}

resource "aws_ecs_task_definition" "controller" {
  family                   = "${var.name}-controller"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = "512"
  memory                   = "1024"
  execution_role_arn       = aws_iam_role.execution.arn
  task_role_arn            = aws_iam_role.controller.arn
  runtime_platform {
    cpu_architecture        = "ARM64"
    operating_system_family = "LINUX"
  }
  volume {
    name = "efs-root"
    efs_volume_configuration {
      file_system_id     = aws_efs_file_system.workspaces.id
      transit_encryption = "ENABLED"
      authorization_config {
        iam = "ENABLED"
      }
    }
  }
  container_definitions = jsonencode([{
    name      = "controller"
    image     = var.controller_image
    command   = ["scale"]
    essential = true
    # Root, to delete a released session's directory, owned by the sandbox's user, on the EFS root.
    user         = "0"
    portMappings = [{ containerPort = 8443, protocol = "tcp" }]
    environment  = [for k, v in local.controller_env : { name = k, value = v }]
    secrets = [
      { name = "ANTHROPIC_ENVIRONMENT_KEY", valueFrom = var.environment_key_secret_arn },
      { name = "AGENTD_HANDOFF_KEY", valueFrom = aws_secretsmanager_secret.handoff_key.arn },
    ]
    mountPoints = [{ sourceVolume = "efs-root", containerPath = "/mnt/efs" }]
    logConfiguration = {
      logDriver = "awslogs"
      options = {
        awslogs-group         = aws_cloudwatch_log_group.controller.name
        awslogs-region        = local.region
        awslogs-stream-prefix = "controller"
      }
    }
  }])
}

# One controller per environment: it holds the handoff nonces in memory, so a second would refuse
# the first's sandboxes. A deployment stops the old task before it starts the new one.
resource "aws_ecs_service" "controller" {
  name                               = "${var.name}-controller"
  cluster                            = aws_ecs_cluster.this.id
  task_definition                    = aws_ecs_task_definition.controller.arn
  desired_count                      = 1
  launch_type                        = "FARGATE"
  deployment_minimum_healthy_percent = 0
  deployment_maximum_percent         = 100
  network_configuration {
    subnets          = var.private_subnet_ids
    security_groups  = [aws_security_group.controller.id]
    assign_public_ip = false
  }
  service_registries {
    registry_arn = aws_service_discovery_service.controller.arn
  }
}
