data "aws_iam_policy_document" "ecs_tasks" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ecs-tasks.amazonaws.com"]
    }
  }
}

# Pulls images and writes logs for both the controller and the sandboxes.
resource "aws_iam_role" "execution" {
  name               = "${var.name}-execution"
  assume_role_policy = data.aws_iam_policy_document.ecs_tasks.json
}

resource "aws_iam_role_policy_attachment" "execution" {
  role       = aws_iam_role.execution.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
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
    actions   = ["ecs:ListTasks", "ecs:RegisterTaskDefinition", "ecs:DeregisterTaskDefinition", "ecs:DeleteTaskDefinitions", "ecs:DescribeTaskDefinition", "ecs:ListTaskDefinitions"]
    resources = ["*"]
  }
  statement {
    sid       = "PassRoles"
    actions   = ["iam:PassRole"]
    resources = [aws_iam_role.execution.arn, aws_iam_role.sandbox.arn]
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
