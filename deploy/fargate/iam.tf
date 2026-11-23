data "aws_iam_policy_document" "ecs_tasks" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ecs-tasks.amazonaws.com"]
    }
  }
}

# Pulls the controller's image, writes its logs and reads its two secrets.
resource "aws_iam_role" "execution" {
  name               = "${var.name}-execution"
  assume_role_policy = data.aws_iam_policy_document.ecs_tasks.json
}

resource "aws_iam_role_policy" "execution_secret" {
  name = "controller-secrets"
  role = aws_iam_role.execution.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = "secretsmanager:GetSecretValue"
      Resource = [var.environment_key_secret_arn, aws_secretsmanager_secret.handoff_key.arn]
    }]
  })
}

# Pulls a sandbox's images and writes its logs, and reads no secret.
resource "aws_iam_role" "sandbox_execution" {
  name               = "${var.name}-sandbox-execution"
  assume_role_policy = data.aws_iam_policy_document.ecs_tasks.json
}

locals {
  ecr_image = "^(?P<account>[0-9]{12})\\.dkr\\.ecr\\.(?P<region>[a-z0-9-]+)\\.amazonaws\\.com/(?P<repository>[^@:]+)"
  repositories = {
    for name, image in { controller = var.controller_image, sandbox = var.sandbox_image } :
    name => format("arn:aws:ecr:%s:%s:repository/%s", regex(local.ecr_image, image).region, regex(local.ecr_image, image).account, regex(local.ecr_image, image).repository)
  }
  # The controller's tasks run its image; a sandbox's run both, fetch and the sidecar from the controller's.
  pulls = {
    execution         = { role = aws_iam_role.execution.id, repositories = [local.repositories.controller], log_group = aws_cloudwatch_log_group.controller.arn }
    sandbox_execution = { role = aws_iam_role.sandbox_execution.id, repositories = values(local.repositories), log_group = aws_cloudwatch_log_group.sandboxes.arn }
  }
}

# The execution roles pull only their images' repositories and write only their own log group.
data "aws_iam_policy_document" "pull" {
  for_each = local.pulls
  statement {
    sid       = "Token"
    actions   = ["ecr:GetAuthorizationToken"]
    resources = ["*"]
  }
  statement {
    sid       = "Images"
    actions   = ["ecr:BatchCheckLayerAvailability", "ecr:GetDownloadUrlForLayer", "ecr:BatchGetImage"]
    resources = distinct(each.value.repositories)
  }
  statement {
    sid       = "Logs"
    actions   = ["logs:CreateLogStream", "logs:PutLogEvents"]
    resources = ["${each.value.log_group}:*"]
  }
}

resource "aws_iam_role_policy" "pull" {
  for_each = local.pulls
  name     = "pull-and-log"
  role     = each.value.role
  policy   = data.aws_iam_policy_document.pull[each.key].json
}

# The sandbox task's role holds no permission: a tool call that reaches the task's credentials
# endpoint gets nothing.
resource "aws_iam_role" "sandbox" {
  name               = "${var.name}-sandbox"
  assume_role_policy = data.aws_iam_policy_document.ecs_tasks.json
}

resource "aws_iam_role" "controller" {
  name               = "${var.name}-controller"
  assume_role_policy = data.aws_iam_policy_document.ecs_tasks.json
}

data "aws_caller_identity" "current" {}

locals {
  account = data.aws_caller_identity.current.account_id
  region  = data.aws_region.current.region
}

data "aws_iam_policy_document" "controller" {
  statement {
    sid       = "Tasks"
    actions   = ["ecs:RunTask", "ecs:StopTask", "ecs:DescribeTasks"]
    resources = ["arn:aws:ecs:${local.region}:${local.account}:task-definition/agentd-*", "arn:aws:ecs:${local.region}:${local.account}:task/${aws_ecs_cluster.this.name}/*"]
    condition {
      test     = "ArnEquals"
      variable = "ecs:cluster"
      values   = [aws_ecs_cluster.this.arn]
    }
  }
  statement {
    sid       = "TaskDefinitions"
    actions   = ["ecs:ListTasks", "ecs:RegisterTaskDefinition", "ecs:DeregisterTaskDefinition", "ecs:DescribeTaskDefinition", "ecs:ListTaskDefinitions"]
    resources = ["*"]
  }
  # Unlike the rest, deleting takes a task definition ARN, so it reaches only the sandboxes' families.
  statement {
    sid       = "DeleteRevisions"
    actions   = ["ecs:DeleteTaskDefinitions"]
    resources = ["arn:aws:ecs:${local.region}:${local.account}:task-definition/agentd-*"]
  }
  # Tags on RegisterTaskDefinition are authorised as ecs:TagResource.
  statement {
    sid       = "TagRevisions"
    actions   = ["ecs:TagResource"]
    resources = ["arn:aws:ecs:${local.region}:${local.account}:task-definition/agentd-*"]
    condition {
      test     = "StringEquals"
      variable = "ecs:CreateAction"
      values   = ["RegisterTaskDefinition"]
    }
  }
  statement {
    sid       = "PassRoles"
    actions   = ["iam:PassRole"]
    resources = [aws_iam_role.sandbox_execution.arn, aws_iam_role.sandbox.arn]
  }
  statement {
    sid       = "AccessPoints"
    actions   = ["elasticfilesystem:CreateAccessPoint", "elasticfilesystem:DescribeAccessPoints", "elasticfilesystem:DeleteAccessPoint", "elasticfilesystem:TagResource"]
    resources = [aws_efs_file_system.workspaces.arn, "arn:aws:elasticfilesystem:${local.region}:${local.account}:access-point/*"]
  }
  statement {
    sid       = "RootMount"
    actions   = ["elasticfilesystem:ClientMount", "elasticfilesystem:ClientWrite", "elasticfilesystem:ClientRootAccess"]
    resources = [aws_efs_file_system.workspaces.arn]
  }
}

resource "aws_iam_role_policy" "controller" {
  name   = "controller"
  role   = aws_iam_role.controller.id
  policy = data.aws_iam_policy_document.controller.json
}
