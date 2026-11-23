# Each session's /workspace is /sessions/<id> on this file system, reached through an access point
# the controller creates per session; the controller mounts the root to delete a released session's
# directory.
resource "aws_efs_file_system" "workspaces" {
  encrypted        = true
  performance_mode = "generalPurpose"
  throughput_mode  = "elastic"
  tags             = { Name = "${var.name}-workspaces" }
}

data "aws_subnet" "private" {
  for_each = toset(var.private_subnet_ids)
  id       = each.value
}

# EFS takes one mount target per Availability Zone, so one subnet of each zone gets it.
locals {
  subnet_by_zone = { for s in data.aws_subnet.private : s.availability_zone => s.id... }
}

resource "aws_efs_mount_target" "workspaces" {
  for_each        = local.subnet_by_zone
  file_system_id  = aws_efs_file_system.workspaces.id
  subnet_id       = each.value[0]
  security_groups = [aws_security_group.efs.id]
}

resource "aws_efs_file_system_policy" "workspaces" {
  file_system_id = aws_efs_file_system.workspaces.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      # Sandboxes mount with their task role, and only through an access point: one claims the
      # client's uid without it, and could reach every session's directory.
      Sid       = "SandboxesThroughAccessPoints"
      Effect    = "Allow"
      Principal = { AWS = aws_iam_role.sandbox.arn }
      Action    = ["elasticfilesystem:ClientMount", "elasticfilesystem:ClientWrite"]
      Resource  = aws_efs_file_system.workspaces.arn
      Condition = {
        Bool       = { "elasticfilesystem:AccessedViaMountTarget" = "true" }
        StringLike = { "elasticfilesystem:AccessPointArn" = "arn:aws:elasticfilesystem:${local.region}:${local.account}:access-point/*" }
      }
      }, {
      Sid       = "TLSOnly"
      Effect    = "Deny"
      Principal = { AWS = "*" }
      Action    = "*"
      Resource  = aws_efs_file_system.workspaces.arn
      Condition = { Bool = { "aws:SecureTransport" = "false" } }
    }]
  })
}
